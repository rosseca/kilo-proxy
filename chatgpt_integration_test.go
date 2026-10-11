package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func subscriptionTestApp(t *testing.T) *app {
	t.Helper()
	a, err := newApp(t.TempDir(), &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	a.chatgpt.creds = chatGPTCredentials{Access: "synthetic-chatgpt-access", Refresh: "synthetic-refresh", Account: "synthetic-account", Expires: time.Now().Add(time.Hour).Unix()}
	a.chatgpt.state = chatGPTState{Status: "idle"}
	a.adminHost = "127.0.0.1:9999"
	t.Cleanup(a.stop)
	return a
}

func subscriptionTextSSE(w http.ResponseWriter, omitContentType ...bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	if len(omitContentType) > 0 && omitContentType[0] {
		w.Header()["Content-Type"] = nil
	}
	item := map[string]any{"id": "msg_test", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "Subscription works", "annotations": []any{}}}}
	response := map[string]any{"id": "resp_test", "object": "response", "status": "completed", "model": "gpt-test", "output": []any{item}, "usage": map[string]any{"input_tokens": 20, "output_tokens": 3, "input_tokens_details": map[string]any{"cached_tokens": 5}}}
	for _, event := range []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_test", "model": "gpt-test", "output": []any{}}},
		{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_test", "type": "message", "role": "assistant", "content": []any{}}},
		{"type": "response.content_part.added", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": ""}},
		{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "delta": "Subscription works"},
		{"type": "response.output_item.done", "output_index": 0, "item": item},
		{"type": "response.completed", "response": response},
	} {
		b, _ := json.Marshal(event)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], b)
	}
}

func TestChatGPTGatewayAllProtocols(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", path, stream), func(t *testing.T) {
				a := subscriptionTestApp(t)
				a.captureEnabled = true
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer synthetic-chatgpt-access" || r.Header.Get("ChatGPT-Account-Id") != "synthetic-account" {
						t.Error("subscription credentials missing")
					}
					for _, name := range []string{"X-KiloCode-OrganizationId", "Cookie", "X-Api-Key"} {
						if r.Header.Get(name) != "" {
							t.Errorf("unexpected header %s", name)
						}
					}
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != "gpt-test" || body["stream"] != true || body["store"] != false {
						t.Errorf("incorrect subscription payload")
					}
					subscriptionTextSSE(w)
				}))
				defer upstream.Close()
				a.chatGPTResponsesURL = upstream.URL
				body := map[string]any{"model": "chatgpt/gpt-test", "stream": stream}
				if path == "/v1/responses" {
					body["input"] = "Hello"
				} else {
					body["messages"] = []any{map[string]any{"role": "user", "content": "Hello"}}
				}
				b, _ := json.Marshal(body)
				r := httptest.NewRequest("POST", "http://127.0.0.1:8877"+path, strings.NewReader(string(b)))
				r.Header.Set("Authorization", "Bearer "+a.config.LocalKey)
				r.Header.Set("Cookie", "secret-cookie")
				r.Header.Set("X-KiloCode-OrganizationId", "wrong-team")
				w := httptest.NewRecorder()
				a.inferenceHandler("kilo-should-not-leave", "kilo-team", a.config.LocalKey, r.Host).ServeHTTP(w, r)
				if w.Code != 200 || !strings.Contains(w.Body.String(), "Subscription works") {
					t.Fatalf("%d: %s", w.Code, w.Body.String())
				}
				if a.usageTotal.SubscriptionRequests != 1 || a.usageTotal.Priced != 0 || a.usageTotal.WithTokens != 1 || a.usageTotal.Cached != 5 || a.usageTotal.Incomplete != 0 {
					t.Fatalf("bad subscription accounting: %+v", a.usageTotal)
				}
				for _, trace := range a.traces {
					data, _ := json.Marshal(trace)
					for _, secret := range []string{"synthetic-chatgpt-access", "synthetic-account", a.config.LocalKey, "secret-cookie"} {
						if strings.Contains(string(data), secret) {
							t.Fatal("credential leaked in activity")
						}
					}
				}
			})
		}
	}
}

func TestChatGPTCoexistsWithKiloAndSharedLibrary(t *testing.T) {
	a := subscriptionTestApp(t)
	a.apiKey = "kilo-original"
	a.config.OrgID = "original-team"
	library := testModelLibrary()
	library.Models = append(library.Models, modelLibraryItem{ID: "chatgpt/gpt-test"})
	if _, err := a.modelLibrary.save(library, 0, false); err != nil {
		t.Fatal(err)
	}
	if !a.kiloReadyLocked() || !a.chatGPTReadyLocked() || !a.connectionReadyLocked() {
		t.Fatal("both connections must be ready")
	}
	data, err := os.ReadFile(filepath.Join(a.dir, "models.json"))
	if err != nil || !strings.Contains(string(data), "chatgpt/gpt-test") {
		t.Fatal("mixed library not persisted")
	}
	if err := a.chatgpt.logout(); err != nil {
		t.Fatal(err)
	}
	if !a.connectionReadyLocked() || !a.kiloReadyLocked() || a.chatGPTReadyLocked() {
		t.Fatal("disconnecting ChatGPT affected Kilo")
	}
	if a.apiKey != "kilo-original" || a.config.OrgID != "original-team" || len(a.modelLibrary.snapshot().Library.Models) != len(library.Models) {
		t.Fatal("existing connection or selections altered")
	}
}

func TestChatGPTNormalizerRequiresCompleteHistoryAndSubscriptionModel(t *testing.T) {
	for _, body := range []string{`{"model":"anthropic/claude-test","input":"Hello"}`, `{"model":"chatgpt/gpt-test","previous_response_id":"resp_old"}`} {
		if _, err := normalizeChatGPTBody([]byte(body)); err == nil {
			t.Fatal("unsupported request accepted")
		}
	}
	raw, err := normalizeChatGPTBody([]byte(`{"model":"chatgpt/gpt-test","input":"Hello","max_output_tokens":20,"temperature":1,"store":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	json.Unmarshal(raw, &body)
	if body["model"] != "gpt-test" || body["store"] != false || body["stream"] != true || body["max_output_tokens"] != nil || body["instructions"] != "" {
		t.Fatal("normalization failed")
	}
	input, ok := body["input"].([]any)
	if !ok || len(input) != 1 || input[0].(map[string]any)["content"] != "Hello" {
		t.Fatal("Responses string input must become a subscription message")
	}
}

func TestChatGPTAndKiloRouteConcurrentlyByModel(t *testing.T) {
	a := subscriptionTestApp(t)
	a.apiKey, a.config.OrgID = "synthetic-kilo-key", "synthetic-team"
	var kiloCalls, subscriptionCalls atomic.Int32
	kilo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-kilo-key" || r.Header.Get("X-KiloCode-OrganizationId") != "synthetic-team" || r.Header.Get("ChatGPT-Account-Id") != "" {
			t.Error("Kilo credentials mixed")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "openai/gpt-test" {
			t.Error("Kilo model changed")
		}
		kiloCalls.Add(1)
		subscriptionTextSSE(w)
	}))
	defer kilo.Close()
	sub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-chatgpt-access" || r.Header.Get("X-KiloCode-OrganizationId") != "" || r.Header.Get("ChatGPT-Account-Id") != "synthetic-account" {
			t.Error("subscription credentials mixed")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "gpt-test" {
			t.Error("subscription prefix not removed")
		}
		subscriptionCalls.Add(1)
		subscriptionTextSSE(w)
	}))
	defer sub.Close()
	a.upstream, _ = url.Parse(kilo.URL)
	a.chatGPTResponsesURL = sub.URL
	handler := a.inferenceHandler(a.apiKey, a.config.OrgID, a.config.LocalKey, "127.0.0.1:8877")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			model := "openai/gpt-test"
			if i%2 == 0 {
				model = "chatgpt/gpt-test"
			}
			r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/responses", strings.NewReader(fmt.Sprintf(`{"model":%q,"stream":true,"input":"Hello"}`, model)))
			r.Header.Set("Authorization", "Bearer "+a.config.LocalKey)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Errorf("%s: %d %s", model, w.Code, w.Body.String())
			}
		})
	}
	wg.Wait()
	if kiloCalls.Load() != 10 || subscriptionCalls.Load() != 10 || a.usageTotal.SubscriptionRequests != 10 || a.usageTotal.Requests != 20 {
		t.Fatal("requests did not remain isolated per connection")
	}
}

func TestChatGPTCatalogMergesConnectionsAndSurvivesOneOutage(t *testing.T) {
	a := subscriptionTestApp(t)
	a.apiKey, a.config.OrgID = "synthetic-kilo-key", "synthetic-team"
	var kiloStatus atomic.Int32
	kiloStatus.Store(200)
	kilo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-kilo-key" || r.Header.Get("ChatGPT-Account-Id") != "" {
			t.Error("incorrect Kilo catalog credentials")
		}
		if kiloStatus.Load() != 200 {
			w.WriteHeader(int(kiloStatus.Load()))
			return
		}
		jsonResponse(w, 200, map[string]any{"data": []any{map[string]any{"id": "openai/gpt-test", "name": "GPT test", "context_length": 128000}}})
	}))
	defer kilo.Close()
	sub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-chatgpt-access" || r.Header.Get("X-KiloCode-OrganizationId") != "" {
			t.Error("incorrect subscription catalog credentials")
		}
		jsonResponse(w, 200, map[string]any{"models": []any{map[string]any{"slug": "gpt-test", "display_name": "GPT test", "context_window": 128000}}})
	}))
	defer sub.Close()
	a.upstream, _ = url.Parse(kilo.URL)
	a.chatgpt.backendURL = sub.URL
	models, _, err := a.fetchModels(context.Background(), true)
	if err != nil || len(models) != 2 {
		t.Fatalf("mixed catalog: %v %v", models, err)
	}
	byID := map[string]modelInfo{}
	for _, m := range models {
		byID[m.ID] = m
	}
	if byID["chatgpt/gpt-test"].Connection != "chatgpt" || byID["openai/gpt-test"].Connection != "kilo" || byID["chatgpt/gpt-test"].InputPrice != nil {
		t.Fatal("ambiguous model identity or fabricated subscription pricing")
	}
	kiloStatus.Store(503)
	models, _, err = a.fetchModels(context.Background(), true)
	if err != nil || len(models) != 1 || models[0].ID != "chatgpt/gpt-test" || len(a.catalogWarnings) != 1 {
		t.Fatal("Kilo outage prevented subscription catalog access")
	}
}

func TestChatGPTQuotaFailureNeverFallsBackToKilo(t *testing.T) {
	a := subscriptionTestApp(t)
	var kiloCalls atomic.Int32
	kilo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { kiloCalls.Add(1) }))
	defer kilo.Close()
	sub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
		fmt.Fprint(w, "synthetic-private-error")
	}))
	defer sub.Close()
	a.upstream, _ = url.Parse(kilo.URL)
	a.chatGPTResponsesURL = sub.URL
	r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/responses", strings.NewReader(`{"model":"chatgpt/gpt-test","input":"Hello"}`))
	r.Header.Set("Authorization", "Bearer "+a.config.LocalKey)
	w := httptest.NewRecorder()
	a.inferenceHandler("synthetic-kilo-key", "synthetic-team", a.config.LocalKey, r.Host).ServeHTTP(w, r)
	if w.Code != 429 || w.Header().Get("Retry-After") != "60" || kiloCalls.Load() != 0 || strings.Contains(w.Body.String(), "synthetic-private-error") {
		t.Fatalf("unsafe quota handling: %d %s", w.Code, w.Body.String())
	}
	if a.usageTotal.SubscriptionRequests != 1 || a.usageTotal.Priced != 0 || a.failures != 1 {
		t.Fatal("quota failure accounting incorrect")
	}
}

func TestChatGPTTrayQuotaPreservesKiloBilling(t *testing.T) {
	a := subscriptionTestApp(t)
	a.config.Language, a.config.OrgID = "en", "synthetic-team"
	used := 25.0
	a.chatgpt.state.Quota = chatGPTQuota{Available: true, FetchedAt: time.Now().UTC().Format(time.RFC3339Nano), Primary: &chatGPTQuotaWindow{UsedPercent: &used}}
	a.config.TrayDisplay = trayDisplaySpend
	before := a.trayState()
	w := traySettingsRequest(a, "PUT", `{"display":"chatgpt-quota"}`, a.adminToken)
	after := a.trayState()
	if w.Code != 200 || after.amount != "75%" || after.spend != before.spend || after.balance != before.balance || after.team != before.team {
		t.Fatalf("quota changed Kilo billing: %d %+v", w.Code, after)
	}
	a.chatgpt.state.Quota.FetchedAt = time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano)
	if a.trayState().amount != "—" {
		t.Fatal("stale quota displayed as current")
	}
}

func TestChatGPTGatewaySSEWithoutContentType(t *testing.T) {
	for _, path := range []string{"responses", "chat/completions", "messages"} {
		t.Run(path, func(t *testing.T) {
			a := subscriptionTestApp(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { subscriptionTextSSE(w, true) }))
			defer upstream.Close()
			a.chatGPTResponsesURL = upstream.URL
			body := `{"model":"chatgpt/gpt-test","input":[{"role":"user","content":"Hello"}],"messages":[{"role":"user","content":"Hello"}]}`
			r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/"+path, strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+a.config.LocalKey)
			w := httptest.NewRecorder()
			a.inferenceHandler("", "", a.config.LocalKey, r.Host).ServeHTTP(w, r)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "Subscription works") || a.usageTotal.WithTokens != 1 || a.usageTotal.Incomplete != 0 || a.usageTotal.Cached != 5 {
				t.Fatalf("headerless SSE failed: %d %s %+v", w.Code, w.Body.String(), a.usageTotal)
			}
		})
	}
}

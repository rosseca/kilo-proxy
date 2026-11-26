package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLiteLLMRoutesSameAliasWithoutFallback(t *testing.T) {
	var kiloRequests, liteRequests atomic.Int32
	const alias = "team/sonnet"
	const catalog = `{"data":[{"id":"team/sonnet","name":"Team Sonnet"}]}`
	kilo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-kilo" || r.Header.Get("X-KiloCode-OrganizationId") != "org-synthetic" {
			t.Error("incorrect Kilo authentication")
		}
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, catalog)
			return
		}
		kiloRequests.Add(1)
		_, _ = io.WriteString(w, `{"model":"team/sonnet","usage":{"prompt_tokens":5}}`)
	}))
	defer kilo.Close()
	lite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-lite" ||
			r.Header.Get("X-KiloCode-OrganizationId") != "" || r.Header.Get("X-Api-Key") != "" ||
			r.Header.Get("Cookie") != "" || r.Header.Get("X-Forwarded-For") != "" {
			t.Error("incorrect isolated LiteLLM authentication")
		}
		if r.Method == http.MethodGet {
			if r.URL.Path != "/gateway/v1/models" && r.URL.Path != "/other/v1/models" {
				t.Error("incorrect mounted LiteLLM catalog path", r.URL.Path)
			}
			_, _ = io.WriteString(w, catalog)
			return
		}
		liteRequests.Add(1)
		var body map[string]any
		if r.URL.Path != "/gateway/v1/messages" || r.URL.RawQuery != "beta=true" || r.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Error("incorrect LiteLLM Messages route")
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != alias {
			t.Error("the exact upstream alias was not restored")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "cookie=private")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"team/real\",\"usage\":{\"input_tokens\":8,\"cost\":17.5}}}\n\n")
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"Limited by LiteLLM"}}`)
	}))
	defer lite.Close()
	a := testApp(t)
	a.captureEnabled = true
	a.apiKey = "synthetic-kilo"
	a.config.OrgID = "org-synthetic"
	setUpstream(a, kilo.URL)

	configure := func(path string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{"url": lite.URL + path, "apiKey": "synthetic-lite"})
		r := adminRequest(a, "providers/litellm", string(payload))
		if r.Code != 200 {
			t.Fatal("configure LiteLLM", r.Code, r.Body.String())
		}
	}
	configure("/gateway")
	inference := a.inferenceHandler(a.apiKey, a.config.OrgID, a.config.LocalKey, "127.0.0.1:8877")
	request := func(method, path, data string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://127.0.0.1:8877"+path, strings.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+a.config.LocalKey)
		req.Header.Set("X-KiloCode-OrganizationId", "never-forward")
		req.Header.Set("Anthropic-Version", "2023-06-01")
		req.Header.Set("Cookie", "secret=private")
		req.Header.Set("X-Api-Key", "not-the-virtual-key")
		w := httptest.NewRecorder()
		inference.ServeHTTP(w, req)
		return w
	}
	r := request("GET", "/v1/models", "")
	var models struct{ Data []struct{ ID string } }
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &models) != nil || len(models.Data) != 2 {
		t.Fatal("combined catalog", r.Code, r.Body.String())
	}
	liteModel := models.Data[0].ID
	if !strings.HasPrefix(liteModel, liteLLMModelPrefix) || models.Data[1].ID != alias {
		t.Fatal("matching names must have independent routes")
	}
	messages := func(stream bool) string {
		data, _ := json.Marshal(map[string]any{
			"model": liteModel, "messages": []map[string]string{{"role": "user", "content": "synthetic"}},
			"stream": stream,
		})
		return string(data)
	}
	r = request("POST", "/v1/messages?beta=true", messages(true))
	if r.Code != 200 || !strings.Contains(r.Body.String(), "event: message_stop") || r.Header().Get("Set-Cookie") != "" ||
		r.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("LiteLLM SSE forwarding and header isolation", r.Code, r.Body.String())
	}
	if len(a.events) == 0 || a.events[0].Usage.Model != liteModel || a.events[0].Usage.Billing != "litellm" ||
		a.events[0].Usage.CostUSD != nil || a.events[0].Usage.Input == nil || *a.events[0].Usage.Input != 8 {
		t.Fatal("LiteLLM usage origin or accounting is incorrect")
	}
	r = request("POST", "/v1/messages?beta=true", messages(false))
	if r.Code != 429 || kiloRequests.Load() != 0 || liteRequests.Load() != 2 {
		t.Fatal("rate limit must stay with the chosen gateway", r.Code)
	}
	if r = request("POST", "/v1/chat/completions", `{"model":"team/sonnet"}`); r.Code != 200 || kiloRequests.Load() != 1 {
		t.Fatal("Kilo alias must still go to Kilo", r.Code)
	}
	if r = adminRequest(a, "providers/kilo/active", `{"enabled":false}`); r.Code != 200 {
		t.Fatal("pause Kilo", r.Code, r.Body.String())
	}
	if r = request("POST", "/v1/chat/completions", `{"model":"team/sonnet"}`); r.Code != 503 || kiloRequests.Load() != 1 {
		t.Fatal("the already-running listener must respect a pause", r.Code)
	}
	if r = request("GET", "/v1/models", ""); r.Code != 200 || bytes.Contains(r.Body.Bytes(), []byte(`"id":"team/sonnet"`)) ||
		!bytes.Contains(r.Body.Bytes(), []byte(liteModel)) {
		t.Fatal("LiteLLM-only catalog", r.Code, r.Body.String())
	}
	configure("/other")
	if r = request("POST", "/v1/messages?beta=true", messages(false)); r.Code != 409 || liteRequests.Load() != 2 {
		t.Fatal("a saved model must reject a change of destination", r.Code)
	}
}

func TestLiteLLMResponsesKeepsToolEventsAndContextIDs(t *testing.T) {
	const terminalEvent = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_current\"}}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"coding"}]}`)
			return
		}
		var req struct {
			Model    string
			Previous string `json:"previous_response_id"`
			Tools    []struct{ Type, Name string }
		}
		if r.URL.Path != "/v1/responses" || json.NewDecoder(r.Body).Decode(&req) != nil || req.Model != "coding" ||
			req.Previous != "resp_previous" || len(req.Tools) != 1 || req.Tools[0].Name != "read_file" {
			t.Error("Responses context or tool schema changed")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_owned\"}}\n\n")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, terminalEvent)
	}))
	defer upstream.Close()
	a := testApp(t)
	config, _ := json.Marshal(map[string]string{"url": upstream.URL})
	if r := adminRequest(a, "providers/litellm", string(config)); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	connection, ready := a.liteLLMConnectionLocked()
	if !ready || !a.connectionReadyLocked() || !a.uiConnectionReady() || a.kiloReadyLocked() || a.catalogScopeLocked() != a.uiCatalogScope() {
		t.Fatal("LiteLLM-only readiness or catalog scope mismatch")
	}
	model := liteLLMNamespace(connection.Base) + "coding"
	body := `{"model":"` + model + `","stream":true,"input":"synthetic","previous_response_id":"resp_previous","tools":[{"type":"function","name":"read_file","parameters":{"type":"object"}}]}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8877/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+a.config.LocalKey)
	w := httptest.NewRecorder()
	a.inferenceHandler("", "", a.config.LocalKey, "127.0.0.1:8877").ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"call_id":"call_owned"`) || !strings.HasSuffix(w.Body.String(), terminalEvent) {
		t.Fatal("Responses streams and tool identifiers must pass through", w.Code, w.Body.String())
	}
}

func TestLiteLLMReportedCostEntersActivityAndSessionTotals(t *testing.T) {
	const messageDelta = `data: {"type":"message_delta","usage":{"output_tokens":2,"cost":0.0125}}` + "\n\n"
	for _, tc := range []struct {
		name, path, body, amount string
		header                   []string
		stream                   bool
		status                   int
	}{
		{
			name: "JSON cost header", path: "/v1/chat/completions", header: []string{"1.25e-7"},
			body: `{"usage":{"prompt_tokens":2,"cost":100}}`, amount: "0.000000125",
		},
		{
			name: "embeddings with explicit zero", path: "/v1/embeddings", header: []string{"0"},
			body: `{"data":[{"embedding":[0.125],"index":0}],"usage":{"prompt_tokens":2,"total_tokens":2}}`, amount: "0.000000000",
		},
		{
			name: "chat usage chunk", path: "/v1/chat/completions", stream: true, header: []string{"0"},
			body: `data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":2,"cost":2.5e-6}}` + "\n\ndata: [DONE]\n\n", amount: "0.000002500",
		},
		{
			name: "Responses final usage", path: "/v1/responses", stream: true,
			body: `data: {"type":"response.completed","response":{"usage":{"input_tokens":2,"cost":0.025}}}` + "\n\n", amount: "0.025000000",
		},
		{
			name: "Messages final usage", path: "/v1/messages", stream: true,
			body: messageDelta + `data: {"type":"message_stop"}` + "\n\n", amount: "0.012500000",
		},
		{
			name: "stream placeholder zero", path: "/v1/responses", stream: true, header: []string{"0"},
			body: `data: {"type":"response.completed","response":{"usage":{"input_tokens":2}}}` + "\n\n",
		},
		{
			name: "incomplete Messages stream", path: "/v1/messages", stream: true, body: messageDelta,
		},
		{
			name: "malformed cost header", path: "/v1/chat/completions", header: []string{"NaN"},
			body: `{"usage":{"prompt_tokens":2,"cost":100}}`,
		},
		{
			name: "ambiguous cost header", path: "/v1/chat/completions", header: []string{"0.02", "0.03"}, body: `{}`,
		},
		{
			name: "failed response", path: "/v1/chat/completions", status: http.StatusTooManyRequests,
			header: []string{"0.10"}, body: `{"error":{"message":"rate limited"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			a.captureEnabled = true
			base, err := normalizeLiteLLMURL("https://litellm.example.invalid")
			if err != nil {
				t.Fatal(err)
			}
			a.config.LiteLLM = liteLLMSettings{URL: base.String(), Enabled: true}
			status := tc.status
			if status == 0 {
				status = http.StatusOK
			}
			a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
				header := make(http.Header)
				header.Set("Content-Type", "application/json")
				if tc.stream {
					header.Set("Content-Type", "text/event-stream")
				}
				for _, value := range tc.header {
					header.Add("X-Litellm-Response-Cost", value)
				}
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})
			model := liteLLMNamespace(base) + "test"
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8877"+tc.path, strings.NewReader(`{"model":"`+model+`","input":"synthetic"}`))
			req.Header.Set("Authorization", "Bearer "+a.config.LocalKey)
			req.Header.Set("X-Litellm-Response-Cost", "99")
			w := httptest.NewRecorder()
			a.inferenceHandler("", "", a.config.LocalKey, "127.0.0.1:8877").ServeHTTP(w, req)
			if w.Code != status || w.Body.String() != tc.body {
				t.Fatalf("observing cost changed LiteLLM response: status %d, body %q", w.Code, w.Body.String())
			}
			if len(a.events) != 1 || a.events[0].Usage == nil || a.events[0].Usage.Billing != "litellm" {
				t.Fatal("LiteLLM usage missing from Activity")
			}
			usage := a.events[0].Usage
			priced, total := int64(0), "0.000000000"
			if tc.amount == "" {
				if usage.CostUSD != nil || usage.CostSource != "" {
					t.Fatalf("unreported cost was treated as known: %+v", usage)
				}
			} else {
				priced, total = 1, tc.amount
				if usage.CostUSD == nil || *usage.CostUSD != tc.amount || usage.CostSource != "litellm_reported" {
					t.Fatalf("reported cost missing from Activity: %+v", usage)
				}
			}
			session := a.usageSessions[usage.Session]
			if a.usageTotal.Priced != priced || a.usageTotal.CostUSD != total || session == nil || session.Priced != priced || session.CostUSD != total {
				t.Fatalf("reported cost mismatch in process or conversation: %+v, %+v", a.usageTotal, session)
			}
		})
	}
}

func TestLiteLLMT3KiloRefreshKeepsSharedCatalog(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/gateway/models" {
			t.Errorf("unexpected Kilo endpoint %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"anthropic/new-kilo-model"}]}`)
	}))
	defer upstream.Close()
	a := testApp(t)
	setUpstream(a, upstream.URL)
	base, err := normalizeLiteLLMURL(upstream.URL + "/private")
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.apiKey, a.config.OrgID = "synthetic-kilo", "synthetic-org"
	a.config.LiteLLM = liteLLMSettings{URL: base.String(), Enabled: true}
	scope := a.catalogScopeLocked()
	liteModel := liteLLMNamespace(base) + "synthetic-litellm-model"
	stale := time.Now().Add(-2 * modelCatalogTTL)
	cache := nativeCachedCatalog{
		SchemaVersion: 1, OrgID: scope, KiloAccount: usageAccountID(a.apiKey, a.config.OrgID),
		FetchedAt: stale, KiloFetchedAt: stale, Models: []modelInfo{
			{ID: "anthropic/old-kilo-model", Connection: "kilo"},
			{ID: liteModel, Connection: "litellm"},
		},
	}
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicCatalogFile(filepath.Join(a.dir, "model-catalog.json"), data); err != nil {
		t.Fatal(err)
	}
	if err := a.loadT3CodeProviderCatalog(context.Background(), normalizeT3CodePreferences(a.config.T3Code), a.modelLibrary.snapshot()); err != nil {
		t.Fatal(err)
	}
	cache = readNativeCachedCatalog(a.dir, scope)
	if len(cache.Models) != 2 || cache.Models[0].ID != "anthropic/new-kilo-model" ||
		cache.Models[1].ID != liteModel || cache.Models[1].Connection != "litellm" {
		t.Fatalf("Kilo refresh replaced the shared catalog: %+v", cache.Models)
	}
	if !modelCatalogNeedsRefresh(cache.FetchedAt, time.Now()) || modelCatalogNeedsRefresh(cache.KiloFetchedAt, time.Now()) {
		t.Fatal("Kilo-only refresh marked the combined catalog as current")
	}

	prefs := a.config.T3Code
	prefs.KiloClaude, prefs.ClaudeModels = true, "official"
	input := t3CodeValidationInput{Dir: a.dir, CatalogScope: scope, KiloAccount: "rotated-kilo-account", Preferences: prefs}
	models := input.catalog()
	if len(models) != 1 || models[0].ID != liteModel {
		t.Fatalf("Kilo account rotation invalidated the LiteLLM catalog: %+v", models)
	}
}

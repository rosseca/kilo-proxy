package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func t3CodeInferenceFixture(t *testing.T) *app {
	t.Helper()
	a := t3CodeTestApp(t, "macos")
	a.t3CodeProbeClaude = func(string, clientLaunchRuntime) claudeCapabilities { return claudeCaps("2.1.288") }
	prefs := t3CodePolicyFixturePreferences()
	prefs.CodexModels, prefs.ClaudeModels = "shared", "shared"
	a.config.T3Code = prefs
	library := emptyModelLibrary()
	for _, model := range t3CodeOfficialCatalogFixture()[:3] {
		library.Models = append(library.Models, modelLibraryItem{ID: model.ID})
	}
	library.Models = append(library.Models, modelLibraryItem{ID: "vendor/custom"})
	library.DefaultModel = library.Models[0].ID
	if _, err := a.modelLibrary.save(library, a.modelLibrary.snapshot().Revision, false); err != nil {
		t.Fatal(err)
	}
	cache, _ := json.Marshal(nativeCachedCatalog{SchemaVersion: 1, OrgID: a.catalogScopeLocked(), KiloAccount: usageAccountID(a.apiKey, a.config.OrgID), KiloFetchedAt: time.Now().UTC(), Models: t3CodeOfficialCatalogFixture(), FetchedAt: time.Now().UTC()})
	if err := os.WriteFile(filepath.Join(a.dir, "model-catalog.json"), cache, 0600); err != nil {
		t.Fatal(err)
	}
	prepareT3CodeFixture(t, a)
	return a
}

func TestT3CodeInferenceRoutesThreeNativeModelsToExactGatewayRequests(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			a := t3CodeInferenceFixture(t)
			a.captureEnabled = true
			seen := []string{}
			for i, model := range []struct{ Native, Gateway, Effort string }{
				{"claude-opus-5-5", "anthropic/claude-opus-5.5", "high"},
				{"claude-sonnet-5-5", "anthropic/claude-sonnet-5.5", "low"},
				{"claude-fable-5-1", "anthropic/claude-fable-5.1", "medium"},
				{"claude-opus-5-5", "anthropic/claude-opus-5.5", "low"},
			} {
				request := `{"model":"` + model.Native + `","max_tokens":4096,"stream":` + fmt.Sprint(stream) + `,"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"Synthetic reasoning."},{"type":"tool_use","id":"call_existing","name":"echo","input":{"exact":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_existing","content":"OK"}]}],"system":[{"type":"text","text":"Synthetic prompt","cache_control":{"type":"ephemeral"}}],"tools":[{"name":"echo","input_schema":{"type":"object","properties":{"exact":{"const":9007199254740993}}}}],"thinking":{"type":"disabled"},"output_config":{"effort":"` + model.Effort + `"}}`
				body, contentType := strings.Replace(messagesGLMJSON, "zai/glm-5.3-flash", model.Gateway, 1), "application/json"
				if stream {
					body, contentType = strings.Replace(messagesGLMStream, "zai/glm-5.3-flash", model.Gateway, 1), "text/event-stream"
				}
				a.transport = usageMemoryTransport(func(upstream *http.Request) (*http.Response, error) {
					raw, err := io.ReadAll(upstream.Body)
					if err != nil {
						t.Fatal(err)
					}
					var before, after map[string]json.RawMessage
					_ = json.Unmarshal([]byte(request), &before)
					if err := json.Unmarshal(raw, &after); err != nil {
						t.Fatal(err)
					}
					var gateway string
					_ = json.Unmarshal(after["model"], &gateway)
					if gateway != model.Gateway {
						t.Fatalf("%s selected but gateway received %s", model.Native, gateway)
					}
					seen = append(seen, gateway)
					for key := range before {
						if key != "model" && !bytes.Equal(before[key], after[key]) {
							t.Fatalf("%s changed for %s: %s", key, model.Native, raw)
						}
					}
					if !bytes.Contains(raw, []byte("9007199254740993")) || upstream.Header.Get("Anthropic-Beta") != "interleaved-thinking-2025-05-14" {
						t.Fatal("exact tool numbers or native request headers changed")
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1, Request: upstream}, nil
				})
				r := httptest.NewRequest("POST", "http://127.0.0.1:9988/t3-code/v1/messages?beta=true", strings.NewReader(request))
				r.Header.Set("Authorization", "Bearer "+a.config.LocalKey)
				r.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
				w := httptest.NewRecorder()
				a.inferenceHandler("upstream-secret", "synthetic-org", a.config.LocalKey, "127.0.0.1:9988").ServeHTTP(w, r)
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"model":"`+model.Native+`"`) {
					t.Fatalf("native response identity: %d %s", w.Code, w.Body.String())
				}
				if len(a.events) != i+1 || a.events[0].Usage == nil || a.events[0].Usage.Model != model.Gateway {
					t.Fatalf("accounting lost exact gateway model: %+v", a.events)
				}
				trace := a.traces[a.events[0].ID]
				if trace == nil || !strings.Contains(trace.Request.Body, model.Native) || !strings.Contains(trace.UpstreamRequest.Body, model.Gateway) || !strings.Contains(trace.Response.Body, model.Native) {
					t.Fatal("trace lost native or exact gateway identity")
				}
			}
			if len(seen) != 4 || seen[0] == seen[1] || seen[1] == seen[2] || seen[0] != seen[3] {
				t.Fatalf("different native choices collapsed to one destination: %v", seen)
			}
		})
	}
}

func TestT3CodeInferenceRejectsUnknownStaleAmbiguousAndDuplicateRequests(t *testing.T) {
	for _, kind := range []string{"unknown", "generic family", "duplicate model", "duplicate tool", "stale key", "modified route", "disabled Claude"} {
		t.Run(kind, func(t *testing.T) {
			a := t3CodeInferenceFixture(t)
			body := `{"model":"claude-opus-5-5","messages":[]}`
			switch kind {
			case "unknown":
				body = `{"model":"claude-opus-4-6","messages":[]}`
			case "generic family":
				body = `{"model":"opus","messages":[]}`
			case "duplicate model":
				body = `{"model":"claude-opus-5-5","model":"claude-sonnet-5-5","messages":[]}`
			case "duplicate tool":
				body = `{"model":"claude-opus-5-5","tools":[{"name":"echo","name":"other"}],"messages":[]}`
			case "stale key":
				a.config.LocalKey = "new-key"
			case "modified route":
				saved, err := a.readT3CodePrepared()
				if err != nil {
					t.Fatal(err)
				}
				saved.ClaudeRoutes["claude-opus-5-5"] = "anthropic/claude-sonnet-5.5"
				encoded, _ := json.Marshal(saved)
				if err := os.WriteFile(t3CodePaths(a.dir).Selection, encoded, 0600); err != nil {
					t.Fatal(err)
				}
			case "disabled Claude":
				a.config.T3Code.KiloClaude = false
			}
			called := false
			a.transport = usageMemoryTransport(func(*http.Request) (*http.Response, error) {
				called = true
				return nil, fmt.Errorf("unexpected upstream request")
			})
			r := httptest.NewRequest("POST", "http://127.0.0.1:9988/t3-code/v1/messages", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+a.config.LocalKey)
			w := httptest.NewRecorder()
			a.inferenceHandler("upstream-secret", "synthetic-org", a.config.LocalKey, "127.0.0.1:9988").ServeHTTP(w, r)
			if w.Code != 400 || called {
				t.Fatalf("invalid managed request forwarded: %d %s called=%v", w.Code, w.Body.String(), called)
			}
		})
	}
}

func TestT3CodeInferenceLeavesOtherIntegrationsAndExactCustomIDsAlone(t *testing.T) {
	a := t3CodeInferenceFixture(t)
	for _, path := range []string{"/v1/messages", "/synara/v1/messages"} {
		raw := `{ "model": "claude-opus-5-5", "messages": [ ], "exact":9007199254740993 }`
		r := httptest.NewRequest("POST", "http://localhost"+path, strings.NewReader(raw))
		out, err := a.prepareT3CodeRequest(r)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(out.Body)
		if string(got) != raw || out.Context().Value(claudeDesktopAliasContextKey{}) != nil {
			t.Fatalf("%s was rewritten by T3", path)
		}
	}
	raw := `{ "model": "vendor/custom", "messages": [ ], "exact":9007199254740993 }`
	r := httptest.NewRequest("POST", "http://localhost/v1/messages", strings.NewReader(raw)).WithContext(context.WithValue(context.Background(), t3CodeRequestContextKey{}, true))
	out, err := a.prepareT3CodeRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(out.Body)
	if string(got) != raw || out.Context().Value(claudeDesktopAliasContextKey{}) != nil {
		t.Fatal("exact non-Claude shared model or its capabilities changed")
	}
}

func TestT3CodeInferenceRouteIsExactAndUploadLimitIsPreserved(t *testing.T) {
	for _, rawURL := range []string{"/t3-code/v1/messages", "/t3-code/v1/messages?beta=true"} {
		if !t3CodeInferencePath(httptest.NewRequest("POST", "http://localhost"+rawURL, nil)) {
			t.Fatalf("supported route rejected: %s", rawURL)
		}
	}
	for _, rawURL := range []string{"/t3-code/v1/messages?x=1", "/t3-code/v1/messages?", "/t3-code/v1/messages/", "/t3-code/v1/%6dessages", "/v1/messages", "/synara/v1/messages", "/t3-code/v1/responses"} {
		if t3CodeInferencePath(httptest.NewRequest("POST", "http://localhost"+rawURL, nil)) {
			t.Fatalf("unsupported route accepted: %s", rawURL)
		}
	}
	if t3CodeInferencePath(httptest.NewRequest("GET", "http://localhost/t3-code/v1/messages", nil)) {
		t.Fatal("GET inference accepted")
	}
	a := t3CodeInferenceFixture(t)
	called := false
	a.transport = usageMemoryTransport(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, fmt.Errorf("unexpected upstream")
	})
	r := httptest.NewRequest("POST", "http://127.0.0.1:9988/t3-code/v1/messages", strings.NewReader(strings.Repeat("x", (32<<20)+1)))
	r.ContentLength = -1
	r.Header.Set("Authorization", "Bearer "+a.config.LocalKey)
	w := httptest.NewRecorder()
	a.inferenceHandler("upstream-secret", "synthetic-org", a.config.LocalKey, "127.0.0.1:9988").ServeHTTP(w, r)
	if w.Code != 413 || called {
		t.Fatal("managed chunked body escaped the existing 32 MiB limit", w.Code, called)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func openMausBotReasoningLibrary() modelLibrary {
	return modelLibrary{SchemaVersion: 1, DefaultModel: "vendor/high", Models: []modelLibraryItem{
		{ID: "vendor/low", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, ReasoningEffort: "low"},
		{ID: "vendor/high", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, ReasoningEffort: "high"},
		{ID: "vendor/max", ReasoningCustom: true, ReasoningLevels: []string{"max"}, ReasoningEffort: "max"},
		{ID: "vendor/none", ReasoningCustom: true, ReasoningLevels: []string{"none"}, ReasoningEffort: "none"},
		{ID: "vendor/auto"},
		{ID: "vendor/disabled", ReasoningCustom: true},
		{ID: "chatgpt/gpt-test", ReasoningCustom: true, ReasoningLevels: []string{"high"}, ReasoningEffort: "high"},
	}}
}

func openMausBotReasoningTestApp(t *testing.T) *app {
	t.Helper()
	a := openMausBotTestApp(t, "macos")
	body, _ := json.Marshal(map[string]any{"library": openMausBotReasoningLibrary()})
	if w := adminRequest(a, "clients/openmausbot", string(body)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.captureEnabled = true
	return a
}

func openMausBotInferenceTestRequest(a *app, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8877"+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+a.config.LocalKey)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.inferenceHandler(a.apiKey, a.config.OrgID, a.config.LocalKey, r.Host).ServeHTTP(w, r)
	return w
}

func TestOpenMausBotReasoningResolvesCapabilitiesAndExplicitNone(t *testing.T) {
	library := openMausBotReasoningLibrary()
	library.Models = append(library.Models,
		modelLibraryItem{ID: "vendor/catalog", ReasoningEffort: "high"},
		modelLibraryItem{ID: "vendor/not-supported", ReasoningEffort: "ultra"},
		modelLibraryItem{ID: "openai/gpt-6-astra"},
		modelLibraryItem{ID: "z-ai/glm-5.3"})
	no := false
	catalog := []modelInfo{{ID: "vendor/catalog", ReasoningEfforts: []string{"low", "high"}}, {ID: "vendor/not-supported", ReasoningEfforts: []string{"low", "high"}, Reasoning: &no}, {ID: "z-ai/glm-5.3", Reasoning: &no}}
	got := openMausBotReasoning(library, catalog)
	want := map[string]string{"vendor/low": "low", "vendor/high": "high", "vendor/max": "max", "vendor/none": "none", "chatgpt/gpt-test": "high", "vendor/catalog": "high", "openai/gpt-6-astra": "low"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved defaults=%v, want %v", got, want)
	}
	if !validOpenMausBotReasoning(library, got) || validOpenMausBotReasoning(library, nil) || validOpenMausBotReasoning(library, map[string]string{"vendor/disabled": "high"}) || validOpenMausBotReasoning(library, map[string]string{"missing": "high"}) || validOpenMausBotReasoning(library, map[string]string{"vendor/low": "ultra"}) {
		t.Fatal("invalid policy validation")
	}
}

func TestOpenMausBotReasoningKiloPreservesToolsAndTraces(t *testing.T) {
	a := openMausBotReasoningTestApp(t)
	var last []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/gateway/chat/completions" || r.Header.Get("Authorization") != "Bearer "+a.apiKey || r.Header.Get("X-KiloCode-OrganizationId") != a.config.OrgID {
			t.Error("wrong scoped upstream route/auth")
		}
		last, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"cost":0.001}}`)
	}))
	defer upstream.Close()
	setUpstream(a, upstream.URL)
	for model, effort := range map[string]string{"vendor/low": "low", "vendor/high": "high", "vendor/max": "max", "vendor/none": "none", "vendor/auto": "", "vendor/disabled": ""} {
		body := `{"model":"` + model + `","messages":[{"role":"user","content":"Keep exact 日本語"}],"tools":[{"type":"function","function":{"name":"test","parameters":{"type":"object","properties":{"id":{"const":9007199254740993},"value":{"oneOf":[{"type":"string"},{"type":"integer"}]}}}}}],"custom":9007199254740993}`
		w := openMausBotInferenceTestRequest(a, "POST", openMausBotBasePath+"/chat/completions", body)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", model, w.Code, w.Body.String())
		}
		var before, after map[string]json.RawMessage
		_ = json.Unmarshal([]byte(body), &before)
		_ = json.Unmarshal(last, &after)
		for _, key := range []string{"model", "messages", "tools", "custom"} {
			if !bytes.Equal(before[key], after[key]) {
				t.Errorf("%s: modified %s", model, key)
			}
		}
		if effort == "" {
			if string(last) != body {
				t.Error("automatic unknown/disabled request was changed")
			}
		} else {
			var reasoning map[string]any
			_ = json.Unmarshal(after["reasoning"], &reasoning)
			if !reflect.DeepEqual(reasoning, map[string]any{"enabled": effort != "none", "effort": effort}) || after["reasoning_effort"] != nil {
				t.Errorf("wrong Kilo wire reasoning: %s", after["reasoning"])
			}
		}
		trace := a.traces[a.events[0].ID]
		if trace == nil || strings.Contains(trace.Request.Body, `"reasoning"`) {
			t.Fatal("original trace already contains injected reasoning")
		}
		if effort != "" && !strings.Contains(trace.UpstreamRequest.Body, `"reasoning"`) {
			t.Fatal("upstream trace omitted effective reasoning")
		}
	}
	if a.requests != 6 || a.usageTotal.WithTokens != 6 {
		t.Fatal("scoped route bypassed normal activity/accounting")
	}
}

func TestOpenMausBotReasoningPreservesExplicitFieldsAndOtherClientBodies(t *testing.T) {
	a := openMausBotReasoningTestApp(t)
	var received string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[]}`)
	}))
	defer upstream.Close()
	setUpstream(a, upstream.URL)
	for _, option := range []string{`"reasoning":{"effort":"low","enabled":true}`, `"reasoning":null`, `"reasoning_effort":"low"`, `"thinking":{"type":"disabled"}`, `"output_config":{"effort":"low"}`} {
		body := `{ "model": "vendor/high", "messages": [], ` + option + ` }`
		w := openMausBotInferenceTestRequest(a, "POST", openMausBotBasePath+"/chat/completions", body)
		if w.Code != 200 || received != body {
			t.Fatal("explicit option changed", option, w.Code, received)
		}
	}
	for _, route := range []string{"/v1/chat/completions", zedProxyPrefix(a.config.LocalKey) + "/v1/chat/completions", "/xcode/v1/chat/completions"} {
		body := `{ "model":"vendor/high", "messages":[], "custom":9007199254740993 }`
		w := openMausBotInferenceTestRequest(a, "POST", route, body)
		if w.Code != 200 || received != body {
			t.Fatal("another client's body changed", route, w.Code)
		}
	}
}

func TestOpenMausBotReasoningChatGPTAdapterReceivesConfiguredDefault(t *testing.T) {
	a := openMausBotReasoningTestApp(t)
	a.chatgpt.creds = chatGPTCredentials{Access: "synthetic-subscription-access", Refresh: "synthetic-refresh", Account: "synthetic-account", Expires: time.Now().Add(time.Hour).Unix()}
	a.chatgpt.state = chatGPTState{Status: "idle"}
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-subscription-access" || r.Header.Get("X-KiloCode-OrganizationId") != "" {
			t.Error("wrong subscription authentication")
		}
		if json.NewDecoder(r.Body).Decode(&received) != nil {
			t.Error("invalid Responses body")
		}
		subscriptionTextSSE(w)
	}))
	defer upstream.Close()
	a.chatGPTResponsesURL = upstream.URL
	for _, explicit := range []bool{false, true} {
		body := `{"model":"chatgpt/gpt-test","messages":[{"role":"user","content":"Hello"}]`
		effort := "high"
		if explicit {
			body += `,"reasoning_effort":"low"`
			effort = "low"
		}
		body += `}`
		w := openMausBotInferenceTestRequest(a, "POST", openMausBotBasePath+"/chat/completions", body)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Subscription works") {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		if received["model"] != "gpt-test" || !reflect.DeepEqual(received["reasoning"], map[string]any{"effort": effort}) {
			t.Fatalf("Responses effort mismatch: %#v", received["reasoning"])
		}
		trace := a.traces[a.events[0].ID]
		if !strings.Contains(trace.UpstreamRequest.Body, `"effort":"`+effort+`"`) {
			t.Fatal("ChatGPT trace missing effective effort")
		}
	}
	if a.usageTotal.SubscriptionRequests != 2 || a.usageTotal.Cached != 10 {
		t.Fatal("subscription accounting bypassed")
	}
}

func TestOpenMausBotReasoningRoutesRejectAmbiguityAndAuthenticate(t *testing.T) {
	a := openMausBotReasoningTestApp(t)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, `{}`) }))
	defer upstream.Close()
	setUpstream(a, upstream.URL)
	valid := openMausBotBasePath + "/chat/completions"
	for _, tc := range []struct {
		method, path, auth, host, origin string
		status                           int
	}{
		{"POST", valid, "", "127.0.0.1:8877", "", 401}, {"POST", valid, "wrong", "127.0.0.1:8877", "", 401}, {"POST", valid, a.config.LocalKey, "evil.example", "", 403}, {"POST", valid, a.config.LocalKey, "127.0.0.1:8877", "http://evil.example", 403},
		{"POST", openMausBotBasePath + "/responses", a.config.LocalKey, "127.0.0.1:8877", "", 404}, {"POST", openMausBotBasePath + "/messages", a.config.LocalKey, "127.0.0.1:8877", "", 404}, {"GET", valid, a.config.LocalKey, "127.0.0.1:8877", "", 404},
		{"POST", valid + "?", a.config.LocalKey, "127.0.0.1:8877", "", 404}, {"POST", valid + "?api_key=secret", a.config.LocalKey, "127.0.0.1:8877", "", 404}, {"POST", "/openmausbot/v1/%63hat/completions", a.config.LocalKey, "127.0.0.1:8877", "", 404}, {"POST", "/openmausbot/v1//chat/completions", a.config.LocalKey, "127.0.0.1:8877", "", 404},
	} {
		r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, strings.NewReader(`{"model":"vendor/high","messages":[]}`))
		r.Header.Set("Authorization", "Bearer "+tc.auth)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		a.inferenceHandler(a.apiKey, a.config.OrgID, a.config.LocalKey, "127.0.0.1:8877").ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%s %s: %d want %d", tc.method, tc.path, w.Code, tc.status)
		}
	}
	for _, body := range []string{`[]`, `{"model":"vendor/high","model":"vendor/low","messages":[]}`, `{"model":"vendor/high","messages":[],"tools":[{"parameters":{"a":1,"a":2}}]}`, `{"model":"not-selected","messages":[],"reasoning_effort":"high"}`, `{"messages":[]}`} {
		w := openMausBotInferenceTestRequest(a, "POST", valid, body)
		if w.Code != 400 {
			t.Errorf("ambiguous/unselected input: %d", w.Code)
		}
	}
	w := openMausBotInferenceTestRequest(a, "GET", openMausBotBasePath+"/models", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var catalog struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &catalog) != nil || len(catalog.Data) != len(openMausBotReasoningLibrary().Models) || catalog.Data[0].ID != "vendor/high" {
		t.Fatal("dedicated catalog not scoped to prepared models")
	}
	if calls != 0 {
		t.Fatal("invalid requests or scoped model discovery contacted upstream")
	}
}

func TestOpenMausBotReasoningUsesPreparedSnapshotAndRequiresMigration(t *testing.T) {
	a := openMausBotReasoningTestApp(t)
	saved, err := a.readOpenMausBotPrepared()
	if err != nil {
		t.Fatal(err)
	}
	if saved.ReasoningEfforts["vendor/high"] != "high" {
		t.Fatal("prepared reasoning not saved")
	}
	state := a.modelLibrary.snapshot()
	changed := openMausBotReasoningLibrary()
	changed.Models[1].ReasoningEffort = "low"
	if _, err := a.modelLibrary.save(changed, state.Revision, false); err != nil {
		t.Fatal(err)
	}
	got, err := a.openMausBotInferenceSelection()
	if err != nil || got.ReasoningEfforts["vendor/high"] != "high" {
		t.Fatal("global edit hot-changed active prepared policy")
	}
	w := adminRequest(a, "clients/openmausbot", "")
	var metadata struct {
		Library modelLibrary      `json:"library"`
		Efforts map[string]string `json:"reasoningEfforts"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &metadata)
	if metadata.Library.Models[1].ReasoningEffort != "high" || metadata.Efforts["vendor/high"] != "high" {
		t.Fatal("metadata lost prepared snapshot")
	}
	paths := openMausBotProfilePaths(a.dir)
	data, _ := os.ReadFile(paths.Selection)
	var legacy map[string]json.RawMessage
	_ = json.Unmarshal(data, &legacy)
	delete(legacy, "reasoningEfforts")
	data, _ = json.Marshal(legacy)
	if err := os.WriteFile(paths.Selection, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.openMausBotInferenceSelection(); err == nil {
		t.Fatal("legacy selection accepted without prepare")
	}
	old, _ := os.ReadFile(paths.Config)
	old = bytes.ReplaceAll(old, []byte(openMausBotBasePath), []byte("/v1"))
	if err := os.WriteFile(paths.Config, old, 0600); err != nil {
		t.Fatal(err)
	}
	a.openMausBotCheckRunning = func(openMausBotPaths) (bool, error) { return true, nil }
	if w := adminRequest(a, "clients/openmausbot", `{}`); w.Code != 409 {
		t.Fatal("live legacy profile migrated")
	}
	a.openMausBotCheckRunning = func(openMausBotPaths) (bool, error) { return false, nil }
	prepareOpenMausBotFixture(t, a)
	got, err = a.openMausBotInferenceSelection()
	if err != nil || got.ReasoningEfforts["vendor/high"] != "low" {
		t.Fatal("migration failed to save new defaults", err)
	}
	got.ReasoningEfforts["vendor/high"] = "high"
	data, _ = json.Marshal(got)
	if err := os.WriteFile(paths.Selection, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.openMausBotInferenceSelection(); err == nil {
		t.Fatal("fingerprint did not cover reasoning policy")
	}
}

func TestOpenMausBotReasoningAllowsRuntimeModelSelection(t *testing.T) {
	for _, field := range []string{"defaultModelSelection", "newBotDefaults.profile.modelSelection", "both"} {
		t.Run(field, func(t *testing.T) {
			a := openMausBotReasoningTestApp(t)
			paths := openMausBotProfilePaths(a.dir)
			selectionBefore, err := os.ReadFile(paths.Selection)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(paths.Config)
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			if err := json.Unmarshal(data, &config); err != nil {
				t.Fatal(err)
			}
			selection := map[string]any{"instanceId": openMausBotInstance, "model": "vendor/low"}
			if field == "defaultModelSelection" || field == "both" {
				config["defaultModelSelection"] = selection
			}
			if field == "newBotDefaults.profile.modelSelection" || field == "both" {
				config["newBotDefaults"] = map[string]any{"profile": map[string]any{"modelSelection": selection, "name": "Keep this bot preference"}}
			}
			changedConfig, _ := json.Marshal(config)
			if err := os.WriteFile(paths.Config, changedConfig, 0600); err != nil {
				t.Fatal(err)
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model     string `json:"model"`
					Reasoning struct {
						Enabled bool   `json:"enabled"`
						Effort  string `json:"effort"`
					} `json:"reasoning"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				want := map[string]string{"vendor/low": "low", "vendor/high": "high"}[body.Model]
				if want == "" || !body.Reasoning.Enabled || body.Reasoning.Effort != want {
					t.Errorf("runtime selection changed prepared per-model reasoning: %#v", body)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"choices":[]}`)
			}))
			defer upstream.Close()
			setUpstream(a, upstream.URL)
			if w := openMausBotInferenceTestRequest(a, "GET", openMausBotBasePath+"/models", ""); w.Code != 200 {
				t.Fatal("runtime picker blocked catalog", w.Code, w.Body.String())
			}
			for _, model := range []string{"vendor/low", "vendor/high"} {
				if w := openMausBotInferenceTestRequest(a, "POST", openMausBotBasePath+"/chat/completions", `{"model":"`+model+`","messages":[]}`); w.Code != 200 {
					t.Fatal("runtime picker blocked inference", w.Code, w.Body.String())
				}
			}
			var metadata struct {
				Prepared bool `json:"prepared"`
			}
			w := adminRequest(a, "clients/openmausbot", "")
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &metadata) != nil || metadata.Prepared {
				t.Fatal("profile metadata relaxed its initial selection check")
			}
			a.mu.Lock()
			launchErr := a.applyOpenMausBotLaunch(&clientLaunchPlan{}, "macos")
			a.mu.Unlock()
			if launchErr == nil {
				t.Fatal("launch accepted a different initial selection")
			}
			a.openMausBotCheckRunning = func(openMausBotPaths) (bool, error) { return true, nil }
			body, _ := json.Marshal(map[string]any{"library": openMausBotReasoningLibrary()})
			if w := adminRequest(a, "clients/openmausbot", string(body)); w.Code != 409 {
				t.Fatal("preparation overwrote a running instance's selection", w.Code)
			}
			selectionAfter, err := os.ReadFile(paths.Selection)
			if err != nil || !bytes.Equal(selectionBefore, selectionAfter) {
				t.Fatal("runtime selection modified the prepared snapshot", err)
			}
			configAfter, err := os.ReadFile(paths.Config)
			if err != nil || !bytes.Equal(changedConfig, configAfter) {
				t.Fatal("inference or rejected preparation changed the active configuration", err)
			}
		})
	}
}

func TestOpenMausBotReasoningRuntimeSelectionDoesNotRelaxManagedSettings(t *testing.T) {
	for _, field := range []string{"key", "route", "models", "driver", "compaction", "policy"} {
		t.Run(field, func(t *testing.T) {
			a := openMausBotReasoningTestApp(t)
			paths := openMausBotProfilePaths(a.dir)
			data, err := os.ReadFile(paths.Config)
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			if err := json.Unmarshal(data, &config); err != nil {
				t.Fatal(err)
			}
			config["defaultModelSelection"] = map[string]any{"instanceId": openMausBotInstance, "model": "vendor/low"}
			instance := config["instances"].(map[string]any)[openMausBotInstance].(map[string]any)
			connection := instance["config"].(map[string]any)
			switch field {
			case "key":
				instance["environment"].(map[string]any)["KILO_LOCAL_API_KEY"] = "wrong-key"
			case "route":
				connection["url"] = strings.Replace(connection["url"].(string), openMausBotBasePath, "/v1", 1)
			case "models":
				connection["managedModels"] = []string{"vendor/low"}
			case "driver":
				instance["driver"] = "other-driver"
			case "compaction":
				config["context"].(map[string]any)["compactAt"] = 1
			case "policy":
				saved, err := a.readOpenMausBotPrepared()
				if err != nil {
					t.Fatal(err)
				}
				saved.ReasoningEfforts["vendor/high"] = "low"
				changed, _ := json.Marshal(saved)
				if err := os.WriteFile(paths.Selection, changed, 0600); err != nil {
					t.Fatal(err)
				}
			}
			changed, _ := json.Marshal(config)
			if err := os.WriteFile(paths.Config, changed, 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			defer upstream.Close()
			setUpstream(a, upstream.URL)
			if w := openMausBotInferenceTestRequest(a, "GET", openMausBotBasePath+"/models", ""); w.Code != 409 {
				t.Fatal("catalog accepted changed managed settings", w.Code)
			}
			if w := openMausBotInferenceTestRequest(a, "POST", openMausBotBasePath+"/chat/completions", `{"model":"vendor/high","messages":[]}`); w.Code != 400 {
				t.Fatal("inference accepted changed managed settings", w.Code)
			}
			if calls != 0 {
				t.Fatal("changed managed settings reached upstream")
			}
		})
	}
}

func TestOpenMausBotReasoningEnforcesUploadLimit(t *testing.T) {
	a := openMausBotReasoningTestApp(t)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer upstream.Close()
	setUpstream(a, upstream.URL)
	for _, chunked := range []bool{false, true} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8877"+openMausBotBasePath+"/chat/completions", strings.NewReader(strings.Repeat(" ", 32<<20)+"{}"))
		if chunked {
			r.ContentLength = -1
		}
		r.Header.Set("Authorization", "Bearer "+a.config.LocalKey)
		w := httptest.NewRecorder()
		a.inferenceHandler(a.apiKey, a.config.OrgID, a.config.LocalKey, r.Host).ServeHTTP(w, r)
		if w.Code != 413 {
			t.Fatal("upload limit not enforced", chunked, w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("oversize upload reached upstream")
	}
}

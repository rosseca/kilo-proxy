package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func t3LiteLLMTestLibrary(model, name string) modelLibrary {
	library := emptyModelLibrary()
	library.DefaultModel = model
	library.Models = append(library.Models, modelLibraryItem{ID: model, DisplayName: name, ContextWindow: 120000, MaxOutputTokens: 4096})
	return library
}

func TestT3LiteLLMProfilesKeepSeparateHomesAndLocalCredentials(t *testing.T) {
	options := t3ProfileTestOptions(t)
	options.Platform = "macos"
	options.Preferences = &t3CodePreferences{Version: 1, LiteLLMCodex: true, LiteLLMClaude: true}
	options.Library = emptyModelLibrary()
	namespace := "litellm/0123456789abcdef0123456789abcdef/"
	options.Policy = &t3CodeModelPolicy{
		LiteLLMCodexLibrary:  t3LiteLLMTestLibrary(namespace+"gpt-work", "GPT Work"),
		LiteLLMClaudeLibrary: t3LiteLLMTestLibrary(namespace+"claude-work", "Claude Work"),
	}
	planned, err := prepareT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		scope, id, label, dir, keyName, model, configName, catalogName string
	}{
		{"codex", t3CodeLiteLLMCodexID, "LiteLLM · Codex", t3CodeLiteLLMCodexHome, t3CodeLiteLLMCodexKey, namespace + "gpt-work", "config.toml", "models.json"},
		{"claude", t3CodeLiteLLMClaudeID, "LiteLLM · Claude", t3CodeLiteLLMClaudeHome, t3CodeLiteLLMClaudeKey, namespace + "claude-work", "settings.json", "kilo-models.json"},
	} {
		home := filepath.Join(options.RootDir, "profiles", tc.dir)
		base := "http://127.0.0.1:18877/t3-code/litellm/" + tc.scope
		instance := planned.Providers[tc.id].(map[string]any)
		config := instance["config"].(map[string]any)
		if instance["displayName"] != tc.label || instance["enabled"] != true || config["homePath"] != home {
			t.Fatal("wrong instance registration", tc.scope)
		}
		token := t3LiteLLMProxyToken(options.LocalKey, tc.scope)
		sensitive := 0
		for _, entry := range instance["environment"].([]map[string]any) {
			if entry["sensitive"] == true {
				sensitive++
				if entry["name"] != tc.keyName || entry["value"] != token || entry["value"] == options.LocalKey {
					t.Fatal("wrong local credential for", tc.scope)
				}
			}
			if entry["name"] == "HOME" && entry["value"] != home {
				t.Fatal("private environment used the wrong home", tc.scope)
			}
		}
		if sensitive != 1 {
			t.Fatal("expected one scoped credential", tc.scope)
		}
		configData, err := os.ReadFile(filepath.Join(home, tc.configName))
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := os.ReadFile(filepath.Join(home, tc.catalogName))
		if err != nil {
			t.Fatal(err)
		}
		for _, data := range [][]byte{configData, catalog} {
			if bytes.Contains(data, []byte(token)) || bytes.Contains(data, []byte(options.LocalKey)) || bytes.Contains(data, []byte(options.NormalHome)) {
				t.Fatal("managed profile contains credentials or points into the normal home", tc.scope)
			}
		}
		if !bytes.Contains(catalog, []byte(tc.model)) {
			t.Fatal("catalog lost the exact gateway alias", tc.scope)
		}
		if tc.scope == "codex" {
			var settings map[string]any
			if err := toml.Unmarshal(configData, &settings); err != nil {
				t.Fatal(err)
			}
			local := settings["model_providers"].(map[string]any)["kilo-local"].(map[string]any)
			if local["base_url"] != base+"/v1" || local["requires_openai_auth"] != false || settings["model_catalog_json"] != filepath.Join(home, tc.catalogName) {
				t.Fatal("Codex did not use the isolated catalog and local route")
			}
		} else {
			var settings map[string]any
			if err := json.Unmarshal(configData, &settings); err != nil {
				t.Fatal(err)
			}
			env := settings["env"].(map[string]any)
			if env["ANTHROPIC_BASE_URL"] != base || env["ANTHROPIC_MODEL"] != tc.model || env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != tc.model {
				t.Fatal("Claude did not use the isolated route and exact alias")
			}
		}
	}
	options.Preferences.LiteLLMCodex = false
	options.CodexBinary = ""
	planned, err = planT3CodeProfiles(options)
	if err != nil || planned.Providers[t3CodeLiteLLMCodexID] != nil || planned.Providers[t3CodeLiteLLMClaudeID] == nil {
		t.Fatalf("Claude-only profile requires or registers Codex: %v", err)
	}
}

func TestT3LiteLLMInferenceUsesPreparedScopeAndPreservesStreaming(t *testing.T) {
	a := t3CodeTestApp(t, "macos")
	a.captureEnabled = true
	a.apiKey = ""
	a.config.T3Code = normalizeT3CodePreferences(t3CodePreferences{
		Version: 1, CodexModels: "official", ClaudeModels: "official", LiteLLMCodex: true, LiteLLMClaude: true,
	})
	a.t3CodeProbeClaude = func(string, clientLaunchRuntime) claudeCapabilities { return claudeCaps("2.1.288") }
	base, err := normalizeLiteLLMURL("https://litellm.example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	a.config.LiteLLM = liteLLMSettings{URL: base.String(), Enabled: true}
	const gatewayKey = "synthetic-gateway-credential"
	a.liteLLMKey = gatewayKey
	codexID, claudeID := liteLLMNamespace(base)+"gpt-work", liteLLMNamespace(base)+"claude-work"
	library := emptyModelLibrary()
	if _, err := a.modelLibrary.save(library, a.modelLibrary.snapshot().Revision, false); err != nil {
		t.Fatal(err)
	}
	rt := a.launchRuntime()
	binary, err := rt.resolve("t3-code", "")
	if err != nil {
		t.Fatal(err)
	}
	lite := t3CodeLiteLLMPlan{
		Catalog: t3LiteLLMCatalog{AccountScope: liteLLMAccountScope(liteLLMConnection{Base: base, Key: gatewayKey})},
		Codex:   t3LiteLLMTestLibrary(codexID, "GPT Work"),
		Claude:  t3LiteLLMTestLibrary(claudeID, "Claude Work"),
	}
	if err := a.prepareT3Code(library, rt, binary, lite); err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:9988"
	key := a.config.LocalKey
	handler := a.inferenceHandler("", "", key, host)
	codexToken, claudeToken := t3LiteLLMProxyToken(key, "codex"), t3LiteLLMProxyToken(key, "claude")
	request := func(method, path, token, header, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
		if header == "Authorization" {
			token = "Bearer " + token
		}
		r.Header.Set(header, token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	networkRequests := 0
	a.transport = usageMemoryTransport(func(upstream *http.Request) (*http.Response, error) {
		networkRequests++
		if upstream.URL.Host != base.Host || upstream.Header.Get("Authorization") != "Bearer "+gatewayKey || upstream.Header.Get("X-Api-Key") != "" {
			t.Fatal("local credentials were forwarded instead of the gateway credential")
		}
		body, err := io.ReadAll(upstream.Body)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(fields["tools"], []byte("9007199254740993")) {
			t.Fatal("tool schema did not survive request routing")
		}
		var stream string
		switch upstream.URL.RequestURI() {
		case "/v1/responses":
			if string(fields["model"]) != "\"gpt-work\"" {
				t.Fatal("Codex route lost the public alias")
			}
			stream = `data: {"type":"response.completed","response":{"usage":{"input_tokens":2}}}`
		case "/v1/messages?beta=true":
			if string(fields["model"]) != "\"claude-work\"" || upstream.Header.Get("Anthropic-Beta") != "interleaved-thinking-2025-05-14" {
				t.Fatal("Claude route lost the public alias or beta support")
			}
			stream = `data: {"type":"message_stop"}`
		default:
			t.Fatal("unexpected gateway route", upstream.URL.RequestURI())
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream + "\n\n")), Request: upstream}, nil
	})
	for _, tc := range []struct {
		scope, selected, other, token, path, header, stream string
	}{
		{"codex", codexID, claudeID, codexToken, "/v1/responses", "Authorization", "response.completed"},
		{"claude", claudeID, codexID, claudeToken, "/v1/messages?beta=true", "X-Api-Key", "message_stop"},
	} {
		path := t3LiteLLMProxyPrefix + tc.scope
		models := request(http.MethodGet, path+"/v1/models", tc.token, tc.header, "")
		if models.Code != http.StatusOK || !strings.Contains(models.Body.String(), tc.selected) || strings.Contains(models.Body.String(), tc.other) {
			t.Fatalf("scope %s model list: %d %s", tc.scope, models.Code, models.Body.String())
		}
		body := `{"model":"` + tc.selected + `","stream":true,"tools":[{"name":"echo","schema":{"const":9007199254740993}}]}`
		success := request(http.MethodPost, path+tc.path, tc.token, tc.header, body)
		if success.Code != http.StatusOK || success.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(success.Body.String(), tc.stream) {
			t.Fatalf("scope %s inference: %d %s", tc.scope, success.Code, success.Body.String())
		}
		trace := a.traces[a.events[0].ID]
		if trace == nil || a.events[0].Usage == nil || a.events[0].Usage.Billing != "litellm" || a.events[0].Usage.Model != tc.selected {
			t.Fatal("LiteLLM usage lost the selected model")
		}
		traceJSON, err := json.Marshal(trace)
		if err != nil || bytes.Contains(traceJSON, []byte(tc.token)) || bytes.Contains(traceJSON, []byte(gatewayKey)) {
			t.Fatal("trace exposed a credential")
		}
		for _, rejected := range []*httptest.ResponseRecorder{
			request(http.MethodPost, path+tc.path, tc.token, tc.header, strings.Replace(body, tc.selected, tc.other, 1)),
			request(http.MethodPost, path+tc.path, tc.token, tc.header, strings.Replace(body, tc.selected, "anthropic/claude-work", 1)),
			request(http.MethodPost, tc.path, tc.token, tc.header, body),
			request(http.MethodPost, path+tc.path, key, "Authorization", body),
		} {
			if rejected.Code < 400 {
				t.Fatalf("out-of-scope request reached inference: %d %s", rejected.Code, rejected.Body.String())
			}
		}
	}
	wrong := request(http.MethodGet, t3LiteLLMProxyPrefix+"claude/v1/models", codexToken, "Authorization", "")
	if wrong.Code < 400 {
		t.Fatal("Codex credential can list Claude models")
	}
	a.liteLLMKey = "new-synthetic-gateway-key"
	stale := request(http.MethodPost, t3LiteLLMProxyPrefix+"codex/v1/responses", codexToken, "Authorization", `{"model":"`+codexID+`"}`)
	if stale.Code < 400 || networkRequests != 2 {
		t.Fatalf("unexpected gateway calls after rejected requests: status %d, calls %d", stale.Code, networkRequests)
	}
}

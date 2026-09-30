package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestCodexDictationLifecycle(t *testing.T) {
	a := testApp(t)
	a.adminHost = "127.0.0.1:1234"
	a.codexProfileDir = t.TempDir()
	// A login is owned by Codex, never copied, read or removed by preparation.
	auth := []byte("opaque Codex-owned credentials")
	if err := os.WriteFile(filepath.Join(a.codexProfileDir, "auth.json"), auth, 0600); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		name, option string
		want         bool
	}{
		{"default", "", false},
		{"enable", `,"chatgptDictation":true`, true},
		{"refresh without option", "", true},
		{"disable", `,"chatgptDictation":false`, false},
		{"refresh disabled", "", false},
	} {
		t.Run(step.name, func(t *testing.T) {
			w := catalogRequest(a, "POST", `{"catalog":`+testCatalog+step.option+`}`, true)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			data, err := os.ReadFile(filepath.Join(a.codexProfileDir, "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			if err := toml.Unmarshal(data, &config); err != nil {
				t.Fatal(err)
			}
			provider, _ := tomlAt(config, []string{"model_providers", "kilo-local"})
			p := provider.(map[string]any)
			if p["requires_openai_auth"] != step.want || p["env_key"] != "KILO_LOCAL_API_KEY" || p["base_url"] != "http://127.0.0.1:8877/v1" {
				t.Fatal(p)
			}
			for _, name := range []string{"experimental_bearer_token", "auth"} {
				if _, exists := p[name]; exists {
					t.Fatal("alternate provider auth", name)
				}
			}
			// The launch gate and preparation use the same merge. Reopening must
			// not reject or silently reset a dictation-enabled profile.
			merged, err := mergeCodexConfig(data, []byte(testCatalog), 8877)
			if err != nil || !bytes.Equal(data, merged) {
				t.Fatal("profile no longer ready", err)
			}
			w = catalogRequest(a, "GET", "", true)
			var response struct {
				Dictation bool `json:"chatgptDictation"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Dictation != step.want {
				t.Fatal(w.Body.String())
			}
			state := httptest.NewRecorder()
			a.state(state)
			var snapshot struct {
				Dictation bool `json:"codexChatGPTDictation"`
			}
			if json.Unmarshal(state.Body.Bytes(), &snapshot) != nil || snapshot.Dictation != step.want {
				t.Fatal(state.Body.String())
			}
			got, err := os.ReadFile(filepath.Join(a.codexProfileDir, "auth.json"))
			if err != nil || !bytes.Equal(got, auth) {
				t.Fatal("Codex credentials changed", err)
			}
		})
	}
}

func TestCodexDictationRejectsCLIAndInvalidTypes(t *testing.T) {
	a := testApp(t)
	a.adminHost = "127.0.0.1:1234"
	a.codexProfileDir, a.codexCLIProfileDir = t.TempDir(), t.TempDir()
	for _, fixture := range []struct{ path, value string }{
		{"codex-cli", "true"}, {"codex-cli", "false"}, {"codex", `"true"`}, {"codex", "1"},
	} {
		r := httptest.NewRequest("POST", "http://"+a.adminHost+"/api/"+fixture.path+"/catalog", strings.NewReader(`{"catalog":`+testCatalog+`,"chatgptDictation":`+fixture.value+`}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+a.adminToken)
		w := httptest.NewRecorder()
		a.adminHandler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(fixture, w.Code, w.Body.String())
		}
	}
	for _, dir := range []string{a.codexProfileDir, a.codexCLIProfileDir} {
		if _, err := os.Stat(filepath.Join(dir, "config.toml")); !os.IsNotExist(err) {
			t.Fatal("invalid request wrote profile", err)
		}
	}
}

func TestCodexDictationPreservesCommentsAndOtherSettings(t *testing.T) {
	original := []byte("# Personal settings\n[features]\nin_app_dictation = false # organization policy\n[model_providers.kilo-local]\nrequires_openai_auth = false # opt-in\nenv_key = 'KILO_LOCAL_API_KEY'\n[model_providers.other]\nrequires_openai_auth = false\n")
	enabled, err := mergeCodexChatGPTDictation(original, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(enabled), "in_app_dictation = false # organization policy") || !strings.Contains(string(enabled), "# opt-in") {
		t.Fatal(string(enabled))
	}
	back, err := mergeCodexChatGPTDictation(enabled, false)
	if err != nil || !bytes.Equal(back, original) {
		t.Fatal("unrelated settings changed", err, string(back))
	}
	for _, bad := range []string{"[", "model_providers = 1", "[model_providers]\nkilo-local = 1"} {
		if _, err := mergeCodexChatGPTDictation([]byte(bad), true); err == nil {
			t.Fatal("invalid TOML accepted", bad)
		}
	}
}

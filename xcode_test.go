package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func xcodeRequest(a *app, method, variant, body string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+a.adminHost+"/api/xcode/"+variant, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if auth {
		r.Header.Set("Authorization", "Bearer "+a.adminToken)
	}
	w := httptest.NewRecorder()
	a.adminHandler().ServeHTTP(w, r)
	return w
}

const chatFixture = `{"models":[{"id":"vendor/a","displayName":"A"},{"id":"vendor/b","displayName":"B"}],"initial":"vendor/b","aliases":{},"mode":"installed"}`

func TestXcodeChatCatalogAndRoute(t *testing.T) {
	a := testApp(t)
	a.adminHost = "127.0.0.1:1234"
	if w := xcodeRequest(a, "POST", "chat", chatFixture, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := xcodeRequest(a, "POST", "chat", chatFixture, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := xcodeRequest(a, "GET", "chat", "", true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	handler := a.inferenceHandler("upstream-key", "org", "local-secret", "127.0.0.1:8877")
	r := httptest.NewRequest("GET", "http://127.0.0.1:8877/xcode/v1/models", nil)
	r.Header.Set("Authorization", "Bearer local-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 200 || len(body.Data) != 2 || body.Data[0].ID != "vendor/b" {
		t.Fatal(w.Code, w.Body.String())
	}
	r.Header.Del("Authorization")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	// Dedicated chat path forwards to the usual authenticated upstream route.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer upstream-key" || r.Header.Get("X-KiloCode-OrganizationId") != "org" {
			t.Error(r.URL.Path, "incorrect gateway authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()
	a.upstream.Scheme = "http"
	a.upstream.Host = strings.TrimPrefix(server.URL, "http://")
	a.upstream.Path = ""
	handler = a.inferenceHandler("upstream-key", "org", "local-secret", "127.0.0.1:8877")
	r = httptest.NewRequest("POST", "http://127.0.0.1:8877/xcode/v1/chat/completions", strings.NewReader(`{"model":"vendor/a","messages":[]}`))
	r.Header.Set("Authorization", "Bearer local-secret")
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// No aliases or custom URL prefix leak into the standard model listing.
	if validRoute("GET", "/xcode/v1/models") {
		t.Fatal("dedicated route accidentally became global")
	}
}
func TestXcodeAgentProfilesUseOwnDirectoriesAndLocalAuth(t *testing.T) {
	a := testApp(t)
	a.adminHost = "127.0.0.1:1234"
	a.xcodeTestRoot = t.TempDir()
	body := `{"catalog":` + testCatalog + `}`
	if w := xcodeRequest(a, "POST", "codex", body, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p := filepath.Join(a.xcodeTestRoot, "codex", "config.toml")
	config, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(config, []byte("env_key")) || !bytes.Contains(config, []byte("experimental_bearer_token")) || !bytes.Contains(config, []byte(a.config.LocalKey)) {
		t.Fatal("Xcode depends on terminal auth or lacks local auth")
	}
	custom := append([]byte("# kept\napproval_policy = 'on-request'\n"), config...)
	os.WriteFile(p, custom, 0600)
	for _, invalid := range []string{strings.ReplaceAll(body, "high", "low"), strings.ReplaceAll(body, "high", "max")} {
		if w := xcodeRequest(a, "POST", "codex", invalid, true); w.Code != 400 {
			t.Fatal(w.Code, "unsupported catalog accepted")
		}
		current, _ := os.ReadFile(p)
		if !bytes.Equal(current, custom) {
			t.Fatal("invalid catalog changed config")
		}
	}
	selection := `{"models":[{"id":"anthropic/claude-sonnet-4.6","effort":"high"},{"id":"anthropic/claude-opus-4.6"}],"initial":"anthropic/claude-sonnet-4.6","aliases":{"opus":"anthropic/claude-opus-4.6"},"mode":"installed"}`
	if w := xcodeRequest(a, "POST", "claude", selection, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	settings, _ := os.ReadFile(filepath.Join(a.xcodeTestRoot, "ClaudeAgentConfig", "settings.json"))
	var c map[string]any
	json.Unmarshal(settings, &c)
	if c["modelPicker"] != nil || c["modelSettings"] != nil || c["effortLevel"] != "high" {
		t.Fatal("invented modern features for bundled 2.1.59")
	}
	env := c["env"].(map[string]any)
	if env["ANTHROPIC_AUTH_TOKEN"] != a.config.LocalKey {
		t.Fatal("missing local auth")
	}
	current, _ := os.ReadFile(p)
	if !bytes.Equal(current, custom) {
		t.Fatal("Claude changed Codex")
	}
}
func TestXcodeRejectsUnsafeAndUnsupportedProfiles(t *testing.T) {
	a := testApp(t)
	a.adminHost = "127.0.0.1:1234"
	a.xcodeTestRoot = t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(a.xcodeTestRoot, "codex")); err != nil {
		t.Skip(err)
	}
	if w := xcodeRequest(a, "POST", "codex", `{"catalog":`+testCatalog+`}`, true); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := xcodeRequest(a, "GET", "codex", "", true); w.Code != 404 {
		t.Fatal(w.Code)
	}
	selection := `{"models":[{"id":"anthropic/claude-fable-5.1","effort":"xhigh"}],"initial":"anthropic/claude-fable-5.1","aliases":{},"mode":"modern"}`
	if w := xcodeRequest(a, "POST", "claude", selection, true); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}

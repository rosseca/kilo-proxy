package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestZedPrepareCredentialsBeforeSettingsAndKeyRotation(t *testing.T) {
	a := testApp(t)
	a.editorTestRoot = t.TempDir()
	a.apiKey = "upstream-secret-must-not-be-stored"
	_, path, choices, _ := a.editorPaths("zed")
	body, _ := json.Marshal(exampleEditorSelection())
	writes := 0
	var previous []byte
	a.zedCredentialStore = func(ctx context.Context, apiURL, key, executable string) error {
		writes++
		if ctx.Err() != nil || key != a.config.LocalKey || key == a.apiKey {
			t.Fatal("wrong credential or context")
		}
		if apiURL != zedBaseURL("http://127.0.0.1:8877/v1", key) {
			t.Fatalf("wrong credential URL: %s", apiURL)
		}
		current, _ := os.ReadFile(path)
		if !bytes.Equal(current, previous) {
			t.Fatal("settings changed before the credential was available")
		}
		return nil
	}
	var previousURL string
	for _, key := range []string{"synthetic-first-key", "synthetic-first-key", "synthetic-rotated-key"} {
		a.config.LocalKey = key
		w := adminRequest(a, "editors/zed/profile", string(body))
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		current, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var settings map[string]any
		if json.Unmarshal(current, &settings) != nil {
			t.Fatal("invalid settings")
		}
		provider := settings["language_models"].(map[string]any)["openai_compatible"].(map[string]any)["kilo-local"].(map[string]any)
		apiURL := provider["api_url"].(string)
		if apiURL != zedBaseURL("http://127.0.0.1:8877/v1", key) || strings.Contains(string(current), key) || strings.Contains(string(current), a.apiKey) {
			t.Fatal("settings URL or credential isolation incorrect")
		}
		if len(provider["available_models"].([]any)) != 2 || settings["agent"].(map[string]any)["default_model"].(map[string]any)["model"] != "vendor/two" {
			t.Fatal("selected models/default not preserved")
		}
		if writes == 2 && (!bytes.Equal(previous, current) || apiURL != previousURL) {
			t.Fatal("repeating preparation changed settings")
		}
		if writes == 3 && apiURL == previousURL {
			t.Fatal("key rotation did not invalidate Zed's credential cache")
		}
		previous, previousURL = current, apiURL
	}
	if writes != 3 {
		t.Fatal("preparation must repair missing credentials even when settings are unchanged")
	}
	beforeChoices, _ := os.ReadFile(choices)
	a.zedCredentialStore = func(context.Context, string, string, string) error { return errors.New(a.config.LocalKey) }
	s := exampleEditorSelection()
	s.Initial = "vendor/one"
	body, _ = json.Marshal(s)
	w := adminRequest(a, "editors/zed/profile", string(body))
	if w.Code != 409 || strings.Contains(w.Body.String(), a.config.LocalKey) {
		t.Fatal("credential failure must be actionable and redacted")
	}
	after, _ := os.ReadFile(path)
	afterChoices, _ := os.ReadFile(choices)
	if !bytes.Equal(after, previous) || !bytes.Equal(afterChoices, beforeChoices) {
		t.Fatal("failed credential write changed the user's profile")
	}
}

func TestZedManagedRoutesAuthenticateAndForward(t *testing.T) {
	const key, host = "synthetic-local-key", "127.0.0.1:8877"
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer upstream-secret" || r.Header.Get("X-KiloCode-OrganizationId") != "company-org" {
			t.Error("wrong upstream authentication")
		}
		if strings.Contains(r.URL.String(), "/zed/") || !strings.HasPrefix(r.URL.Path, "/api/gateway/") {
			t.Error("managed URL leaked upstream")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			io.WriteString(w, `{"data":[{"id":"vendor/one"},{"id":"vendor/two"}]}`)
		} else {
			payload, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(payload), `"model":"vendor/two"`) {
				t.Error("selected model changed")
			}
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"cost":0.002}}`)
		}
	}))
	defer upstream.Close()
	a := testApp(t)
	setUpstream(a, upstream.URL)
	h := a.inferenceHandler("upstream-secret", "company-org", key, host)
	base := zedProxyPrefix(key) + "/v1"
	for _, route := range []struct{ method, path string }{{"GET", "/models"}, {"POST", "/chat/completions"}, {"POST", "/responses"}} {
		r := httptest.NewRequest(route.method, "http://"+host+base+route.path, strings.NewReader(`{"model":"vendor/two","messages":[{"role":"user","content":"hello"}]}`))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", route.path, w.Code, w.Body.String())
		}
	}
	if calls != 3 || a.requests != 3 || len(a.events) != 3 {
		t.Fatal("managed requests bypassed gateway or session activity")
	}
	for _, tc := range []struct {
		path, auth, origin, host string
		status                   int
	}{
		{base + "/models", "", "", host, 401},
		{base + "/models", "wrong", "", host, 401},
		{base + "/models", key, "https://example.com", host, 403},
		{base + "/models", key, "", "evil.example", 403},
		{zedProxyPrefix("old-key") + "/v1/models", key, "", host, 404},
		{base + "//models", key, "", host, 404},
		{base + "/%6dodels", key, "", host, 404},
		{base + "/../models", key, "", host, 404},
		{base + "/models?api_key=unexpected", key, "", host, 404},
		{zedProxyPrefix(key) + "/api/config", key, "", host, 404},
	} {
		r := httptest.NewRequest("GET", "http://"+host+tc.path, nil)
		r.Host = tc.host
		r.Header.Set("Authorization", "Bearer "+tc.auth)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%s: got %d want %d", tc.path, w.Code, tc.status)
		}
	}
	if calls != 3 {
		t.Fatal("invalid managed route reached upstream")
	}
}

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

func desktopOptionsTestRequest(a *app, method, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/claude-desktop/options", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.claudeDesktopOptions(w, r)
	return w
}

func TestClaudeDesktopOptionsStrictInput(t *testing.T) {
	a, _ := desktopProfileTestApp(t)
	for _, body := range []string{"", `{}`, `null`, `[]`, `{"experimentalModels":null}`, `{"experimentalModels":1}`, `{"experimentalModels":"true"}`, `{"experimentalModels":true,"extra":false}`, `{"experimentalModels":true,"experimentalModels":false}`, `{"experimentalModels":true} {}`} {
		w := desktopOptionsTestRequest(a, http.MethodPost, body)
		if w.Code != 400 || a.config.ClaudeDesktopExperimentalModels {
			t.Errorf("invalid options accepted: %s => %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := desktopOptionsTestRequest(a, http.MethodDelete, ""); w.Code != 405 {
		t.Fatalf("wrong unsupported method status: %d", w.Code)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		w := desktopOptionsTestRequest(a, method, `{"experimentalModels":false}`)
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"experimentalModels":false}` {
			t.Fatalf("wrong default options: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestClaudeDesktopOptionsPersistAndMigrate(t *testing.T) {
	a, p := desktopProfileTestApp(t)
	a.config.Language = "en"
	if err := writeSettings(a.dir, a.config); err != nil {
		t.Fatal(err)
	}
	// A configuration from before the option existed defaults to disabled.
	path := filepath.Join(a.dir, "settings.json")
	old, _ := os.ReadFile(path)
	var settingsJSON map[string]json.RawMessage
	if err := json.Unmarshal(old, &settingsJSON); err != nil {
		t.Fatal(err)
	}
	delete(settingsJSON, "claudeDesktopExperimentalModels")
	old, _ = json.Marshal(settingsJSON)
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := readSettings(a.dir)
	if err != nil || loaded.ClaudeDesktopExperimentalModels {
		t.Fatalf("old settings migration failed: %v", err)
	}
	if _, err := a.saveClaudeDesktopProfile(desktopProfileTestSelection(), p); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p.ConfigPath)
	w := desktopOptionsTestRequest(a, http.MethodPost, `{"experimentalModels":true}`)
	if w.Code != 200 || !a.config.ClaudeDesktopExperimentalModels || strings.TrimSpace(w.Body.String()) != `{"experimentalModels":true}` {
		t.Fatalf("enable failed: %d %s", w.Code, w.Body.String())
	}
	loaded, err = readSettings(a.dir)
	if err != nil || !loaded.ClaudeDesktopExperimentalModels || loaded.LocalKey != a.config.LocalKey || loaded.Language != "en" || loaded.Port != a.config.Port {
		t.Fatalf("options lost existing settings: %+v %v", loaded, err)
	}
	if after, _ := os.ReadFile(p.ConfigPath); !bytes.Equal(before, after) {
		t.Fatal("enabling option rewrote native Desktop profile")
	}
	if err := a.verifyClaudeDesktopProfile(); err != nil {
		t.Fatalf("existing native profile broken by option: %v", err)
	}
	first, _ := os.ReadFile(path)
	if w := desktopOptionsTestRequest(a, http.MethodPost, `{"experimentalModels":true}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if next, _ := os.ReadFile(path); !bytes.Equal(first, next) {
		t.Fatal("same option was not idempotent")
	}
}

func TestClaudeDesktopOptionsDisablePreservesMixedProfile(t *testing.T) {
	a, p := desktopProfileTestApp(t)
	if w := desktopOptionsTestRequest(a, http.MethodPost, `{"experimentalModels":true}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, err := a.saveClaudeDesktopProfile(desktopProfileTestMixedSelection(), p); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, path := range []string{p.SelectionPath, p.ConfigPath, p.MetaPath, p.ModePath} {
		before[path], _ = os.ReadFile(path)
	}
	if w := desktopOptionsTestRequest(a, http.MethodPost, `{"experimentalModels":false}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for path, data := range before {
		if after, _ := os.ReadFile(path); !bytes.Equal(data, after) {
			t.Fatalf("disabling option modified saved profile: %s", path)
		}
	}
	if _, err := a.readClaudeDesktopSelection(p); err == nil || !strings.Contains(err.Error(), "Experimental") {
		t.Fatalf("disabled mixed selection did not explain the block: %v", err)
	}
	if w := desktopOptionsTestRequest(a, http.MethodPost, `{"experimentalModels":true}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := a.verifyClaudeDesktopProfile(); err != nil {
		t.Fatalf("re-enabling existing mixed profile failed: %v", err)
	}
}

func TestClaudeDesktopOptionsWriteFailureDoesNotEnable(t *testing.T) {
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			a, _ := desktopProfileTestApp(t)
			path := filepath.Join(a.dir, "settings.json")
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if kind == "directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				target := filepath.Join(t.TempDir(), "untouched.json")
				if err := os.WriteFile(target, []byte(`{"untouched":true}`), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skip(err)
				}
			}
			w := desktopOptionsTestRequest(a, http.MethodPost, `{"experimentalModels":true}`)
			if w.Code != 409 || a.config.ClaudeDesktopExperimentalModels {
				t.Fatalf("failed write changed live option: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

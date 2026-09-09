package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func launchTestApp(t *testing.T) *app {
	t.Helper()
	a := testApp(t)
	home := t.TempDir()
	a.config.Port = 0
	a.apiKey = "synthetic-kilo"
	a.config.OrgID = "synthetic-org"
	a.editorTestRoot = home
	a.codexProfileDir = filepath.Join(home, "desktop")
	a.codexCLIProfileDir = filepath.Join(home, "cli")
	a.claudeProfileDir = filepath.Join(home, "claude")
	a.xcodeTestRoot = filepath.Join(home, "xcode")
	a.launcher = &clientLaunchRuntime{home: home, platform: "macos", resolve: func(string, string) (string, error) { return "/synthetic/client", nil }, terminal: func() (bool, string) { return true, "" }, start: func(clientLaunchPlan) error { return nil }}
	return a
}
func TestClientLaunchAuthenticationAndStrictInput(t *testing.T) {
	a := launchTestApp(t)
	calls := 0
	a.launcher.start = func(clientLaunchPlan) error { calls++; return nil }
	for _, method := range []string{"GET", "POST"} {
		for _, mutate := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Authorization") }, func(r *http.Request) { r.Host = "evil.example" }, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }} {
			r := httptest.NewRequest(method, "http://"+a.adminHost+"/api/clients/launch", strings.NewReader(`{"client":"codex"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+a.adminToken)
			mutate(r)
			w := httptest.NewRecorder()
			a.adminHandler().ServeHTTP(w, r)
			if w.Code != 401 && w.Code != 403 {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
	for _, body := range []string{`{"client":"generic"}`, `{"client":"codex","command":"rm"}`, `{"client":"claude","appPath":"/tmp/exe"}`, `{"client":"codex","directory":"relative"}`, `{"client":"codex"} {}`, `{"client":"codex","directory":"bad\u0000path"}`} {
		w := adminRequest(a, "clients/launch", body)
		if w.Code < 400 {
			t.Fatal(body, w.Code)
		}
	}
	if calls != 0 || a.proxyListener != nil {
		t.Fatal("invalid requests dispatched or started proxy")
	}
	w := adminRequest(a, "clients/launch", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), a.config.LocalKey) {
		t.Fatal(w.Code, "invalid discovery")
	}
	a.launcher.platform = "linux"
	w = adminRequest(a, "clients/launch", `{"client":"xcode-chat"}`)
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
}
func TestClientLaunchRejectsMissingStaleAndUnsafeProfiles(t *testing.T) {
	for _, client := range []string{"codex", "codex-cli", "claude", "opencode", "zed", "xcode-codex", "xcode-claude", "xcode-chat"} {
		t.Run(client, func(t *testing.T) {
			a := launchTestApp(t)
			request := clientLaunchRequest{Client: client}
			if _, e := a.planClientLaunch(request, a.launchRuntime()); e == nil {
				t.Fatal("missing profile accepted")
			}
			launchPrepareFixture(t, a, client)
			plan, e := a.planClientLaunch(request, a.launchRuntime())
			if e != nil {
				t.Fatal(e)
			}
			if plan.Directory != a.launcher.home {
				t.Fatal("wrong project directory")
			}
			if strings.HasPrefix(client, "codex") && (plan.Env["CODEX_HOME"] == "" || plan.Env["KILO_LOCAL_API_KEY"] != a.config.LocalKey) {
				t.Fatal("missing isolated Codex credentials")
			}
			if client == "claude" && (len(plan.Unset) != len(nativeClaudeResetEnv) || plan.Env["CLAUDE_CONFIG_DIR"] != a.claudeProfileDir) {
				t.Fatal("Claude inherited authentication")
			}
			if client == "opencode" && (plan.Env["OPENCODE_CONFIG"] == "" || len(plan.Unset) != 1 || !strings.HasPrefix(plan.Args[1], "kilo-local/")) {
				t.Fatal("OpenCode lost managed profile")
			}
			if client == "xcode-chat" {
				return
			} // Its persisted selection contains no connection settings.
			a.config.Port++
			if _, e = a.planClientLaunch(request, a.launchRuntime()); e == nil {
				t.Fatal("stale port accepted")
			}
			a.config.Port--
			if client == "claude" || client == "opencode" || client == "zed" || client == "xcode-codex" || client == "xcode-claude" {
				a.config.LocalKey = "rotated-synthetic"
				if _, e = a.planClientLaunch(request, a.launchRuntime()); e == nil {
					t.Fatal("stale credential accepted")
				}
			}
		})
	}
	a := launchTestApp(t)
	launchPrepareFixture(t, a, "codex")
	path := filepath.Join(a.codexProfileDir, "models.json")
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := a.planClientLaunch(clientLaunchRequest{Client: "codex"}, a.launchRuntime()); e == nil {
		t.Fatal("nonregular profile accepted")
	}
}
func launchPrepareFixture(t *testing.T, a *app, client string) {
	t.Helper()
	endpoint := ""
	var body []byte
	switch client {
	case "codex", "codex-cli":
		endpoint = client + "/catalog"
		body = []byte(`{"catalog":` + testCatalog + `}`)
	case "xcode-codex":
		endpoint = "xcode/codex"
		body = []byte(`{"catalog":` + testCatalog + `}`)
	case "opencode", "zed":
		endpoint = "editors/" + client + "/profile"
		body, _ = json.Marshal(exampleEditorSelection())
	default:
		s := claudeSelection{Models: []claudeModel{{ID: "vendor/test"}}, Initial: "vendor/test", Mode: "modern"}
		endpoint = "claude/profile"
		if client == "xcode-chat" {
			endpoint = "xcode/chat"
		}
		if client == "xcode-claude" {
			endpoint = "xcode/claude"
		}
		body, _ = json.Marshal(s)
	}
	w := adminRequest(a, endpoint, string(body))
	if w.Code != 200 {
		t.Fatal(client, w.Code, w.Body.String())
	}
}
func TestClientLaunchDispatchAndFailureIsolation(t *testing.T) {
	a := launchTestApp(t)
	launchPrepareFixture(t, a, "codex-cli")
	customDir := filepath.Join(a.launcher.home, "project with 'quotes' & spaces")
	if e := os.Mkdir(customDir, 0700); e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(clientLaunchRequest{Client: "codex-cli", Directory: customDir})
	calls := 0
	a.launcher.start = func(p clientLaunchPlan) error {
		calls++
		if p.Directory != customDir || p.Client != "codex-cli" {
			t.Error("wrong dispatch")
		}
		if a.proxyListener == nil {
			t.Error("proxy not started")
		}
		return nil
	}
	w := adminRequest(a, "clients/launch", string(body))
	if w.Code != 200 || calls != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.launchMu.Lock()
	w = adminRequest(a, "clients/launch", string(body))
	a.launchMu.Unlock()
	if w.Code != 409 || calls != 1 {
		t.Fatal("concurrent launch accepted")
	}
	a.launcher.start = func(clientLaunchPlan) error { return errors.New("sensitive " + a.config.LocalKey) }
	w = adminRequest(a, "clients/launch", string(body))
	if w.Code != 500 || strings.Contains(w.Body.String(), a.config.LocalKey) {
		t.Fatal("dispatch error exposed secret")
	}
	a.stop()
	a.launcher.resolve = func(string, string) (string, error) { return "", errors.New("client missing") }
	w = adminRequest(a, "clients/launch", string(body))
	if w.Code != 409 || a.proxyListener != nil {
		t.Fatal("missing application started proxy")
	}
}
func TestClientLaunchCursorDoesNotStartTunnel(t *testing.T) {
	a := launchTestApp(t)
	w := adminRequest(a, "clients/launch", `{"client":"cursor"}`)
	if w.Code != 409 || a.cursor != nil || a.proxyListener != nil {
		t.Fatal("launch unexpectedly started Cursor ingress")
	}
	a.cursor = &cursorSession{Status: "running", URL: "https://synthetic.invalid/v1"}
	if _, e := a.planClientLaunch(clientLaunchRequest{Client: "cursor"}, a.launchRuntime()); e != nil {
		t.Fatal(e)
	}
	a.cursor = nil
}

func TestClientLaunchRejectsSharedCodexWindowProfile(t *testing.T) {
	a := launchTestApp(t)
	launchPrepareFixture(t, a, "codex")
	normal := filepath.Join(a.launcher.home, "normal-codex")
	if e := os.Mkdir(normal, 0700); e != nil {
		t.Fatal(e)
	}
	marker := filepath.Join(normal, "user-data")
	if e := os.WriteFile(marker, []byte("untouched"), 0600); e != nil {
		t.Fatal(e)
	}
	isolated := filepath.Join(a.launcher.home, "Library", "Application Support", "Codex Kilo")
	if e := os.MkdirAll(filepath.Dir(isolated), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(normal, isolated); e != nil {
		t.Skip("Host cannot create directory symlinks:", e)
	}
	if _, e := a.planClientLaunch(clientLaunchRequest{Client: "codex"}, a.launchRuntime()); e == nil {
		t.Fatal("Codex GUI reused another window profile through a symlink")
	}
	data, e := os.ReadFile(marker)
	if e != nil || string(data) != "untouched" {
		t.Fatal("ordinary Codex data changed")
	}
}

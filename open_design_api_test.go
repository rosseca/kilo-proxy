package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOpenDesignAPIPreparesPrivatePreferencesAndPreservesOtherAgents(t *testing.T) {
	for _, engine := range []string{"codex-cli", "claude", "opencode"} {
		t.Run(engine, func(t *testing.T) {
			a := openDesignTestApp(t, "macos")
			a.launcher.terminal = func() (bool, string) { return false, "no terminal" }
			paths := openDesignProfilePaths(a.dir)
			if err := safeEditorDir(a.dir, paths.Data); err != nil {
				t.Fatal(err)
			}
			original := []byte(`{"onboardingCompleted":false,"telemetryEnabled":false,"customInstructions":"Keep project instructions","agentModels":{"other":{"model":"other/model","reasoning":"low"}},"agentCliEnv":{"other":{"OTHER_BIN":"/private/other"}},"agentCliEnvIntent":{"other":{"apiKeyOverride":false}}}`)
			if err := os.WriteFile(paths.Config, original, 0600); err != nil {
				t.Fatal(err)
			}
			beforeHome := openDesignFixtureFiles(t, a.launcher.home)
			beforeLibrary := a.modelLibrary.snapshot()
			openDesignPrepareFixture(t, a, engine)
			if a.proxyListener != nil {
				t.Fatal("preparation started inference proxy before launch")
			}
			prefs, err := readOpenDesignPreferences(paths.Config)
			if err != nil {
				t.Fatal(err)
			}
			id := openDesignRuntimeID(engine)
			models := prefs["agentModels"].(map[string]any)
			if prefs["agentId"] != id || models[id].(map[string]any)["model"] != "default" || models["other"].(map[string]any)["reasoning"] != "low" || prefs["customInstructions"] != "Keep project instructions" || prefs["onboardingCompleted"] != false || prefs["telemetryEnabled"] != false {
				t.Fatal("preparation replaced unrelated preferences or first-run/privacy choices")
			}
			if _, exists := prefs["mode"]; exists {
				t.Fatal("daemon preparation wrote renderer mode")
			}
			env := prefs["agentCliEnv"].(map[string]any)
			if env["other"].(map[string]any)["OTHER_BIN"] != "/private/other" {
				t.Fatal("other engine environment changed")
			}
			profileEnv := env[id].(map[string]any)
			dirKey := map[string]string{"codex-cli": "CODEX_HOME", "claude": "CLAUDE_CONFIG_DIR", "opencode": "OPENCODE_BIN"}[engine]
			wantPath := filepath.Join(paths.Profiles, engine)
			if engine == "opencode" {
				wantPath = openDesignOpenCodeShimPath(wantPath)
			}
			if profileEnv[dirKey] != wantPath {
				t.Fatal("engine used a non-private profile directory")
			}
			if engine == "claude" && profileEnv["ANTHROPIC_AUTH_TOKEN"] != a.config.LocalKey {
				t.Fatal("Claude private preference lost its local auth")
			}
			backup, _ := os.ReadFile(paths.Config + ".bak")
			if !bytes.Equal(original, backup) {
				t.Fatal("Open Design preference backup was not exact")
			}
			response := adminRequest(a, "open-design/profile", "")
			var info struct {
				Prepared bool                                `json:"prepared"`
				Engine   string                              `json:"engine"`
				Engines  map[string]clientLaunchAvailability `json:"engines"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &info) != nil || !info.Prepared || info.Engine != engine || !info.Engines[engine].Available {
				t.Fatal("prepared engine requires an unnecessary terminal or was not reported ready")
			}
			assertOpenDesignNoResponseKeys(t, a, response.Body.String())
			if !reflect.DeepEqual(beforeHome, openDesignFixtureFiles(t, a.launcher.home)) || !reflect.DeepEqual(beforeLibrary, a.modelLibrary.snapshot()) {
				t.Fatal("preparation modified ordinary application files or the shared library")
			}
		})
	}
}

func TestOpenDesignAPIExplicitBrowserLibraryDoesNotReplaceSharedModels(t *testing.T) {
	a := openDesignTestApp(t, "macos")
	before := a.modelLibrary.snapshot()
	library := modelLibrary{SchemaVersion: 1, DefaultModel: "browser/private", Models: []modelLibraryItem{{ID: "browser/private", DisplayName: "Browser selection"}}}
	body, _ := json.Marshal(openDesignPrepareRequest{Engine: "codex-cli", Library: &library})
	response := adminRequest(a, "open-design/profile", string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("browser preparation failed: %d %s", response.Code, response.Body.String())
	}
	if !reflect.DeepEqual(before, a.modelLibrary.snapshot()) {
		t.Fatal("browser library replaced native shared preferences")
	}
	if saved := mustOpenDesignPrepared(t, a); !reflect.DeepEqual(saved.Library, library) {
		t.Fatal("explicit browser selection not used for its prepared profile")
	}
	assertOpenDesignNoResponseKeys(t, a, response.Body.String())
	data, _ := os.ReadFile(filepath.Join(openDesignProfilePaths(a.dir).Root, "selection.json"))
	assertOpenDesignNoResponseKeys(t, a, string(data))
}

func TestOpenDesignAPIRejectsInvalidSelectionAndUnreadyConnection(t *testing.T) {
	for _, body := range []string{
		`{"engine":"codex"}`, `{"engine":"unsupported"}`, `{"engine":"codex-cli","directory":"/tmp/outside"}`, `{"engine":"codex-cli","command":"arbitrary"}`,
		`{"engine":"codex-cli","library":{"schemaVersion":1,"models":[],"defaultModel":""}}`,
		`{"engine":"codex-cli","library":{"schemaVersion":1,"models":[{"id":"valid/model"}],"defaultModel":"missing"}}`,
		`{"engine":"codex-cli"} {}`,
	} {
		a := openDesignTestApp(t, "macos")
		before := openDesignFixtureFiles(t, a.dir)
		if response := adminRequest(a, "open-design/profile", body); response.Code < 400 {
			t.Fatal("invalid request accepted", body)
		}
		if !reflect.DeepEqual(before, openDesignFixtureFiles(t, a.dir)) || a.proxyListener != nil {
			t.Fatal("invalid request wrote settings or started proxy")
		}
	}
	for _, condition := range []struct {
		name string
		set  func(*app)
	}{
		{"missing key", func(a *app) { a.apiKey = "" }},
		{"missing organization", func(a *app) { a.config.OrgID = "" }},
		{"pending login", func(a *app) { a.login = &loginSession{Status: "pending"} }},
		{"unsaved connection", func(a *app) { a.connectionNeedsSave = true }},
		{"recovering library", func(a *app) { a.modelLibrary.state.RecoveryRequired = true }},
		{"missing engine", func(a *app) {
			a.launcher.resolve = func(string, string) (string, error) { return "", errors.New("CLI is missing") }
		}},
	} {
		t.Run(condition.name, func(t *testing.T) {
			a := openDesignTestApp(t, "macos")
			condition.set(a)
			before := openDesignFixtureFiles(t, a.dir)
			response := adminRequest(a, "open-design/profile", `{"engine":"codex-cli"}`)
			if response.Code != http.StatusConflict || !reflect.DeepEqual(before, openDesignFixtureFiles(t, a.dir)) || a.proxyListener != nil {
				t.Fatal("unready state prepared settings")
			}
			assertOpenDesignNoResponseKeys(t, a, response.Body.String())
		})
	}
}

func TestOpenDesignAPIAuthenticationAndMethods(t *testing.T) {
	a := openDesignTestApp(t, "macos")
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, mutate := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Authorization") }, func(r *http.Request) { r.Host = "evil.example" }, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }} {
			r := httptest.NewRequest(method, "http://"+a.adminHost+openDesignProfileEndpoint, strings.NewReader(`{"engine":"codex-cli"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+a.adminToken)
			mutate(r)
			response := httptest.NewRecorder()
			a.adminHandler().ServeHTTP(response, r)
			if response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
				t.Fatal("unsafe request accepted")
			}
		}
	}
	r := httptest.NewRequest(http.MethodDelete, "http://"+a.adminHost+openDesignProfileEndpoint, nil)
	r.Header.Set("Authorization", "Bearer "+a.adminToken)
	response := httptest.NewRecorder()
	a.adminHandler().ServeHTTP(response, r)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatal("unexpected API method accepted")
	}
}

func TestOpenDesignAPIRunningAndStartupReservationProtectFiles(t *testing.T) {
	for _, state := range []string{"running", "uncertain", "startup reservation"} {
		t.Run(state, func(t *testing.T) {
			a := openDesignTestApp(t, "macos")
			openDesignPrepareFixture(t, a, "codex-cli")
			paths := openDesignProfilePaths(a.dir)
			switch state {
			case "running":
				a.openDesignCheckRunning = func(string) (bool, error) { return true, nil }
			case "uncertain":
				a.openDesignCheckRunning = func(string) (bool, error) { return false, errors.New("synthetic unavailable process inventory") }
			case "startup reservation":
				a.openDesignLaunchUntil = time.Now().Add(30 * time.Second)
			}
			before := openDesignFixtureFiles(t, paths.Root)
			if response := adminRequest(a, "open-design/profile", `{"engine":"claude"}`); response.Code != http.StatusConflict {
				t.Fatal("live or uncertain process allowed engine rewrite")
			}
			if !reflect.DeepEqual(before, openDesignFixtureFiles(t, paths.Root)) {
				t.Fatal("blocked preparation wrote private files")
			}
			response := adminRequest(a, "open-design/profile", `{"engine":"codex-cli"}`)
			want := http.StatusOK
			if state == "uncertain" {
				want = http.StatusConflict
			}
			if response.Code != want || !reflect.DeepEqual(before, openDesignFixtureFiles(t, paths.Root)) {
				t.Fatal("unchanged preparation mishandled running status")
			}
			a.openDesignCheckRunning = func(string) (bool, error) { return false, nil }
			a.openDesignLaunchUntil = time.Now().Add(-time.Second)
			openDesignPrepareFixture(t, a, "claude")
		})
	}
}

func TestOpenDesignAPIRejectsUnsafeAndMalformedPreferenceFiles(t *testing.T) {
	for _, content := range []string{`{broken`, `{"agentCliEnv":[]}`, `{"agentId":"one","agentId":"two"}`} {
		a := openDesignTestApp(t, "macos")
		paths := openDesignProfilePaths(a.dir)
		if err := safeEditorDir(a.dir, paths.Data); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths.Config, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		response := adminRequest(a, "open-design/profile", `{"engine":"codex-cli"}`)
		got, _ := os.ReadFile(paths.Config)
		if response.Code != http.StatusConflict || string(got) != content {
			t.Fatal("malformed preferences were overwritten")
		}
		if _, err := os.Stat(filepath.Join(paths.Profiles, "codex-cli", "config.toml")); !os.IsNotExist(err) {
			t.Fatal("malformed preferences allowed partial engine writes")
		}
	}
	a := openDesignTestApp(t, "macos")
	paths := openDesignProfilePaths(a.dir)
	if err := os.MkdirAll(paths.Root, 0700); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	marker := filepath.Join(external, "private.txt")
	if err := os.WriteFile(marker, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, paths.Profiles); err != nil {
		t.Skip(err)
	}
	if response := adminRequest(a, "open-design/profile", `{"engine":"codex-cli"}`); response.Code != http.StatusConflict {
		t.Fatal("private profile root symlink followed")
	}
	entries, _ := os.ReadDir(external)
	data, _ := os.ReadFile(marker)
	if len(entries) != 1 || string(data) != "unchanged" {
		t.Fatal("unsafe path modified outside files")
	}
}

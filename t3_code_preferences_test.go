package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestT3CodePreferencesDefaultsAndValidation(t *testing.T) {
	p := normalizeT3CodePreferences(t3CodePreferences{})
	if !p.Codex || !p.Claude || !p.KiloCodex || !p.KiloClaude || p.CodexModels != "official" || p.ClaudeModels != "official" || len(p.Providers) != 0 || p.OpenCode2 {
		t.Fatal("unexpected initial provider/model selection", p)
	}
	if p.LiteLLMCodex || p.LiteLLMClaude || p.LiteLLMCodexModels != "official" || p.LiteLLMClaudeModels != "official" {
		t.Fatal("LiteLLM providers should be opt-in and default to family catalogs", p)
	}
	for _, mutate := range []func(*t3CodePreferences){
		func(p *t3CodePreferences) { p.Version = 2 }, func(p *t3CodePreferences) { p.CodexModels = "everything" },
		func(p *t3CodePreferences) { p.ClaudeModels = "" }, func(p *t3CodePreferences) { p.Providers = []string{"unknown"} },
		func(p *t3CodePreferences) { p.Providers = []string{"pi", "pi"} }, func(p *t3CodePreferences) { p.OpenCode2 = true },
	} {
		bad := p
		mutate(&bad)
		if validateT3CodePreferences(bad) == nil {
			t.Fatal("invalid preference accepted", bad)
		}
	}
	p.Providers = []string{"pi", "opencode"}
	p.OpenCode2 = true
	if err := validateT3CodePreferences(p); err != nil {
		t.Fatal(err)
	}
	n := normalizeT3CodePreferences(p)
	n.Providers[0] = "cursor"
	if p.Providers[0] != "pi" {
		t.Fatal("normalization mutated the input")
	}
}

func TestT3CodeLiteLLMPreferencesPreserveLegacySavesAndAllowDisconnect(t *testing.T) {
	a := t3CodeTestApp(t, "linux")
	prefs := a.config.T3Code
	post := func(input any, wantStatus int) {
		t.Helper()
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if w := adminRequest(a, "clients/t3-code/preferences", string(data)); w.Code != wantStatus {
			t.Fatalf("save %s returned %d: %s", data, w.Code, w.Body.String())
		}
	}
	prefs.LiteLLMCodex, prefs.LiteLLMCodexModels = true, "selected"
	post(prefs, 409)
	if a.config.T3Code.LiteLLMCodex {
		t.Fatal("enabled LiteLLM without a connection")
	}
	a.config.LiteLLM = liteLLMSettings{Enabled: true, URL: "https://litellm.example.test/v1"}
	post(prefs, 200)

	legacyJSON, err := json.Marshal(prefs)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(legacyJSON, &legacy); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"liteLLMCodex", "liteLLMClaude", "liteLLMCodexModels", "liteLLMClaudeModels"} {
		delete(legacy, name)
	}
	legacy["codex"] = json.RawMessage("false")
	post(legacy, 200)
	saved, err := readSettings(a.dir)
	if err != nil || !saved.T3Code.LiteLLMCodex || saved.T3Code.LiteLLMClaude || saved.T3Code.LiteLLMCodexModels != "selected" || saved.T3Code.Codex {
		t.Fatal("legacy save overwrote LiteLLM choices or ignored the direct Codex change", saved.T3Code, err)
	}

	a.config.LiteLLM.Enabled = false
	prefs = a.config.T3Code
	prefs.LiteLLMCodex = false
	post(prefs, 200)
	if a.config.T3Code.LiteLLMCodex || a.config.T3Code.LiteLLMCodexModels != "selected" {
		t.Fatal("disable should keep the selected scope after disconnect")
	}
}

func TestT3CodeLiteLLMPreferencesRejectInvalidExplicitFields(t *testing.T) {
	base := `{"version":1,"codexModels":"official","claudeModels":"official",`
	for _, invalid := range []string{
		`"liteLLMCodex":null`,
		`"liteLLMClaude":"true"`,
		`"liteLLMCodexModels":null`,
		`"liteLLMClaudeModels":"shared"`,
		`"liteLLMClaudeModels":""`,
		`"liteLLMCodex":true,"liteLLMCodex":false`,
	} {
		if _, _, err := decodeT3CodePreferencesDocument([]byte(base + invalid + "}")); err == nil {
			t.Errorf("accepted invalid preference %s", invalid)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"t3Code":`+base+`"liteLLMCodexModels":null}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSettings(dir); err == nil {
		t.Fatal("reading settings accepted a null LiteLLM policy")
	}
}

func TestT3CodePreferencesSaveDoesNotMutateWorkspaceOrSharedModels(t *testing.T) {
	a := t3CodeTestApp(t, "linux")
	prepareT3CodeFixture(t, a)
	paths := t3CodePaths(a.dir)
	before := clientInstallationTree(t, paths.Root)
	library := a.modelLibrary.snapshot()
	p := normalizeT3CodePreferences(a.config.T3Code)
	p.Codex = false
	p.KiloClaude = false
	p.Providers = []string{"pi", "opencode"}
	data, _ := json.Marshal(p)
	w := adminRequest(a, "clients/t3-code/preferences", string(data))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if !reflect.DeepEqual(before, clientInstallationTree(t, paths.Root)) || !reflect.DeepEqual(library, a.modelLibrary.snapshot()) {
		t.Fatal("saving options changed workspace/chats/shared selection")
	}
	saved, err := readSettings(a.dir)
	if err != nil || !reflect.DeepEqual(saved.T3Code, normalizeT3CodePreferences(p)) {
		t.Fatal("options were not persisted", saved.T3Code, err)
	}
	old, err := a.readT3CodePrepared()
	if err != nil {
		t.Fatal(err)
	}
	binary, _ := a.launcher.resolve("t3-code", "")
	if a.t3CodeReady(old, binary, *a.launcher) {
		t.Fatal("changed provider settings reused obsolete preparation")
	}
	// A caller cannot apply a stale settings snapshot while generating profiles.
	stale := normalizeT3CodePreferences(t3CodePreferences{})
	data, _ = json.Marshal(map[string]any{"preferences": stale})
	w = adminRequest(a, "clients/t3-code", string(data))
	if w.Code != 409 || !reflect.DeepEqual(before, clientInstallationTree(t, paths.Root)) {
		t.Fatal("stale prepare changed workspace", w.Code, w.Body.String())
	}
}

func TestT3CodePreferencesFailedSaveAndPreparationLockPreserveState(t *testing.T) {
	a := t3CodeTestApp(t, "linux")
	previous := a.config
	p := normalizeT3CodePreferences(previous.T3Code)
	p.Claude = false
	data, _ := json.Marshal(p)
	a.launchMu.Lock()
	w := adminRequest(a, "clients/t3-code/preferences", string(data))
	a.launchMu.Unlock()
	if w.Code != 409 || !reflect.DeepEqual(previous, a.config) {
		t.Fatal("save raced preparation", w.Code)
	}
	// Rename into a directory fails on every host, including privileged runners.
	path := filepath.Join(a.dir, "settings.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	w = adminRequest(a, "clients/t3-code/preferences", string(data))
	if w.Code != 500 || !reflect.DeepEqual(previous, a.config) {
		t.Fatal("failed write changed in-memory preferences", w.Code, w.Body.String())
	}
}

func TestT3CodePreferencesDiscoveryDoesNotBlockStateOrSettings(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		t.Run(method, func(t *testing.T) {
			a := t3CodeTestApp(t, "linux")
			a.imageDependencyLookup = func(string) string { return "" }
			prefs := normalizeT3CodePreferences(a.config.T3Code)
			body := ""
			if method == "POST" {
				prefs.Codex = false
				data, _ := json.Marshal(prefs)
				body = string(data)
			}
			started, gate := make(chan struct{}), make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(gate) }) }
			defer release()
			resolve := a.launcher.resolve
			a.launcher.resolve = func(id, custom string) (string, error) {
				if id == "t3-code" {
					close(started)
					<-gate
				}
				return resolve(id, custom)
			}
			response := make(chan *httptest.ResponseRecorder, 1)
			go func() { response <- adminRequest(a, "clients/t3-code/preferences", body) }()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("T3 discovery did not start")
			}
			// Both operations must finish while discovery is still retained.
			for _, request := range []struct{ path, body string }{{"state", ""}, {"language", `{"language":"es"}`}} {
				result := make(chan *httptest.ResponseRecorder, 1)
				go func(path, body string) { result <- adminRequest(a, path, body) }(request.path, request.body)
				select {
				case w := <-result:
					if w.Code != 200 {
						t.Fatal(request.path, w.Code, w.Body.String())
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("%s blocked behind T3 executable discovery", request.path)
				}
			}
			release()
			select {
			case w := <-response:
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				var result struct {
					Preferences t3CodePreferences `json:"preferences"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || !reflect.DeepEqual(result.Preferences, prefs) {
					t.Fatal("T3 preferences changed during discovery", result, err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("T3 preferences did not finish after releasing discovery")
			}
			saved, err := readSettings(a.dir)
			if err != nil || saved.Language != "es" || !reflect.DeepEqual(saved.T3Code, prefs) {
				t.Fatal("T3 discovery overwrote concurrent settings", saved.Language, saved.T3Code, err)
			}
		})
	}
}

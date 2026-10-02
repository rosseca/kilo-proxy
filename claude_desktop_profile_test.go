package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func desktopProfileTestApp(t *testing.T) (*app, claudeDesktopProfilePaths) {
	t.Helper()
	a := testApp(t)
	a.claudeDesktopCheckRunning = func(string) (bool, error) { return false, nil }
	a.editorTestRoot = t.TempDir()
	a.launcher = &clientLaunchRuntime{platform: "macos", home: a.editorTestRoot}
	p, err := a.claudeDesktopPaths()
	if err != nil {
		t.Fatal(err)
	}
	return a, p
}

func desktopProfileTestSelection() editorSelection {
	return editorSelection{Models: []editorModel{{ID: "anthropic/claude-haiku-4.5", Name: "Haiku"}, {ID: "anthropic/claude-sonnet-4.6", Name: "Sonnet"}}, Initial: "anthropic/claude-sonnet-4.6"}
}

func desktopProfileTestRequest(a *app, method string, s any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(s)
	r := httptest.NewRequest(method, "/api/claude-desktop/profile", bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.claudeDesktopProfile(w, r)
	return w
}

func TestClaudeDesktopSelection(t *testing.T) {
	for _, id := range []string{"claude-sonnet-4-6", "anthropic/claude-opus-4.6", "anthropic/claude-sonnet-4.6:1m"} {
		if !claudeDesktopModelSupported(id) {
			t.Errorf("valid model rejected: %s", id)
		}
	}
	for _, id := range []string{"openai/gpt-5.4", "z-ai/glm-5", "anthropic/glm-5", "openai/claude-sonnet-4.6", "claude-gpt-5", "anthropic/claude-glm-5", "claude-", "../claude-sonnet", "anthropic/claude-sonnet\n", "claude-sonnet/../../file"} {
		if claudeDesktopModelSupported(id) {
			t.Errorf("unsupported model accepted: %s", id)
		}
	}
	valid := desktopProfileTestSelection()
	if err := validateClaudeDesktopSelection(valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*editorSelection){
		func(s *editorSelection) { s.Models = nil },
		func(s *editorSelection) { s.Initial = "anthropic/claude-other" },
		func(s *editorSelection) { s.Models[0].Context = 200000 },
		func(s *editorSelection) { s.Models[0].Output = 4096 },
		func(s *editorSelection) { s.Models[0].ID = s.Models[1].ID },
		func(s *editorSelection) { s.Models[0].Name = "unsafe\nname" },
	} {
		s := desktopProfileTestSelection()
		mutate(&s)
		if validateClaudeDesktopSelection(s) == nil {
			t.Errorf("invalid selection accepted: %+v", s)
		}
	}
}

func TestClaudeDesktopProfileContract(t *testing.T) {
	a, p := desktopProfileTestApp(t)
	if w := desktopProfileTestRequest(a, http.MethodGet, nil); w.Code != 404 {
		t.Fatalf("missing profile: %d %s", w.Code, w.Body.String())
	}
	w := desktopProfileTestRequest(a, http.MethodPost, desktopProfileTestSelection())
	if w.Code != 200 {
		t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), a.config.LocalKey) {
		t.Fatal("API exposed gateway credential")
	}
	config, _, exists, err := readClaudeDesktopObject(p.ConfigPath)
	if err != nil || !exists {
		t.Fatal(err)
	}
	var models []map[string]json.RawMessage
	if json.Unmarshal(config["inferenceModels"], &models) != nil || len(models) != 2 || string(models[0]["name"]) != `"anthropic/claude-sonnet-4.6"` || string(models[0]["labelOverride"]) != `"Sonnet"` || len(models[0]) != 2 {
		t.Fatalf("wrong models: %s", config["inferenceModels"])
	}
	if !bytes.HasPrefix(config["inferenceGatewayBaseUrl"], []byte(`"http://127.0.0.1:`)) || bytes.Contains(config["inferenceGatewayBaseUrl"], []byte("/v1")) || string(config["inferenceProvider"]) != `"gateway"` || string(config["modelDiscoveryEnabled"]) != "false" {
		t.Fatal("wrong gateway contract")
	}
	for _, path := range []string{p.ConfigPath, p.MetaPath, p.ModePath, p.SelectionPath} {
		info, err := os.Stat(path)
		if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatalf("unsafe permissions: %s %v", path, err)
		}
	}
	if err := a.verifyClaudeDesktopProfile(); err != nil {
		t.Fatal(err)
	}
	w = desktopProfileTestRequest(a, http.MethodPost, desktopProfileTestSelection())
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"changed":false`) {
		t.Fatalf("not idempotent: %d %s", w.Code, w.Body.String())
	}
	if w = desktopProfileTestRequest(a, http.MethodGet, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"profileDir"`) {
		t.Fatalf("get profile: %d %s", w.Code, w.Body.String())
	}
	a.config.Port++
	if a.verifyClaudeDesktopProfile() == nil {
		t.Fatal("stale gateway port accepted")
	}
}

func TestClaudeDesktopProfilePreservesLibraryAndBackups(t *testing.T) {
	a, p := desktopProfileTestApp(t)
	if err := safeEditorDir(p.Root, filepath.Dir(p.ConfigPath)); err != nil {
		t.Fatal(err)
	}
	foreignID := "c2c71374-c726-4b74-9da2-2c556c2d3bc4"
	oldMeta := []byte(`{"entries":[{"id":"` + foreignID + `","name":"Other gateway","color":"blue"}],"appliedId":"` + foreignID + `","future":{"n":9007199254740993},"hybridPointer":{"url":"https://example.invalid/config"}}`)
	oldMode := []byte(`{"deploymentMode":"1p","mcpServers":{"keep":{"command":"local-only"}},"preferences":{"keep":true}}`)
	oldConfig := []byte(`{"future":{"n":9007199254740993},"inferenceCredentialHelper":"/old/helper","inferenceCustomHeaders":{"Authorization":"old"}}`)
	for path, data := range map[string][]byte{p.MetaPath: oldMeta, p.ModePath: oldMode, p.ConfigPath: oldConfig} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	foreignPath := filepath.Join(filepath.Dir(p.MetaPath), foreignID+".json")
	if err := os.WriteFile(foreignPath, []byte(`{"untouched":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveClaudeDesktopProfile(desktopProfileTestSelection(), p); err != nil {
		t.Fatal(err)
	}
	for path, old := range map[string][]byte{p.MetaPath: oldMeta, p.ModePath: oldMode, p.ConfigPath: oldConfig} {
		backup, err := os.ReadFile(path + ".bak")
		if err != nil || !bytes.Equal(old, backup) {
			t.Fatalf("original backup lost for %s", path)
		}
	}
	meta, _, _, _ := readClaudeDesktopObject(p.MetaPath)
	if !bytes.Contains(meta["entries"], []byte(foreignID)) || !bytes.Contains(meta["entries"], []byte(`"color": "blue"`)) || !bytes.Contains(meta["future"], []byte("9007199254740993")) || meta["hybridPointer"] != nil {
		t.Fatalf("metadata changed unrelated data: %s", meta)
	}
	mode, _, _, _ := readClaudeDesktopObject(p.ModePath)
	if !launchEqualJSON(mode["mcpServers"], []byte(`{"keep":{"command":"local-only"}}`)) || mode["preferences"] == nil {
		t.Fatal("unrelated desktop settings lost")
	}
	config, _, _, _ := readClaudeDesktopObject(p.ConfigPath)
	if config["future"] == nil || config["inferenceCredentialHelper"] != nil || config["inferenceCustomHeaders"] != nil {
		t.Fatal("wrong connection merge")
	}
	if data, _ := os.ReadFile(foreignPath); string(data) != `{"untouched":true}` {
		t.Fatal("foreign deployment modified")
	}
}

func TestClaudeDesktopProfileRejectsUnsafeState(t *testing.T) {
	for _, kind := range []string{"config-symlink", "directory-symlink", "malformed", "duplicate-key", "non-uuid", "duplicate-uuid", "backup-directory", "mode-malformed", "selection-malformed"} {
		t.Run(kind, func(t *testing.T) {
			a, p := desktopProfileTestApp(t)
			if err := safeEditorDir(p.Root, filepath.Dir(p.ConfigPath)); err != nil {
				t.Fatal(err)
			}
			unchanged := []byte(`{"untouched":true}`)
			if err := os.WriteFile(p.ModePath, unchanged, 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "config-symlink":
				if err := os.Symlink(p.ModePath, p.ConfigPath); err != nil {
					t.Skip(err)
				}
			case "directory-symlink":
				if err := os.Remove(filepath.Dir(p.ConfigPath)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Dir(p.ConfigPath)); err != nil {
					t.Skip(err)
				}
			case "malformed":
				_ = os.WriteFile(p.ConfigPath, []byte(`{"invalid"`), 0600)
			case "duplicate-key":
				_ = os.WriteFile(p.ConfigPath, []byte(`{"a":1,"a":2}`), 0600)
			case "non-uuid":
				_ = os.WriteFile(p.MetaPath, []byte(`{"entries":[{"id":"../../escape","name":"bad"}]}`), 0600)
			case "duplicate-uuid":
				_ = os.WriteFile(p.MetaPath, []byte(`{"entries":[{"id":"`+claudeDesktopProfileID+`","name":"a"},{"id":"`+claudeDesktopProfileID+`","name":"b"}]}`), 0600)
			case "backup-directory":
				_ = os.WriteFile(p.ConfigPath, []byte(`{}`), 0600)
				_ = os.Mkdir(p.ConfigPath+".bak", 0700)
			case "mode-malformed":
				unchanged = []byte(`{"bad"`)
				_ = os.WriteFile(p.ModePath, unchanged, 0600)
			case "selection-malformed":
				_ = os.WriteFile(p.SelectionPath, []byte(`{"models":[]}`), 0600)
			}
			if _, err := a.saveClaudeDesktopProfile(desktopProfileTestSelection(), p); err == nil {
				t.Fatal("unsafe profile accepted")
			}
			if data, _ := os.ReadFile(p.ModePath); !bytes.Equal(data, unchanged) {
				t.Fatal("activation changed on failed save")
			}
		})
	}
}

func TestClaudeDesktopProfileManagedPreferences(t *testing.T) {
	for _, key := range []string{"inferenceProvider", "inferenceGatewayBaseUrl", "disableAutoUpdates"} {
		t.Run(key, func(t *testing.T) {
			a, p := desktopProfileTestApp(t)
			dir := filepath.Join(a.editorTestRoot, "Library", "Managed Preferences")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			data := []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>` + key + `</key><string>value</string></dict></plist>`)
			if err := os.WriteFile(filepath.Join(dir, "com.anthropic.claudefordesktop.plist"), data, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := a.saveClaudeDesktopProfile(desktopProfileTestSelection(), p)
			if (key != "disableAutoUpdates") != (err != nil) {
				t.Fatalf("wrong policy decision for %s: %v", key, err)
			}
			if key != "disableAutoUpdates" {
				if _, err := os.Stat(p.ConfigPath); !os.IsNotExist(err) {
					t.Fatal("managed config was written")
				}
			}
		})
	}
}

func TestClaudeDesktopProfileFakeHomePaths(t *testing.T) {
	for _, platform := range []string{"macos", "darwin", "windows", "linux"} {
		t.Run(platform, func(t *testing.T) {
			a := testApp(t)
			a.claudeDesktopCheckRunning = func(string) (bool, error) { return false, nil }
			home := t.TempDir()
			a.launcher = &clientLaunchRuntime{platform: platform, home: home}
			p, err := a.claudeDesktopPaths()
			base := filepath.Join(a.dir, "claude-desktop")
			wantUI := filepath.Join(base, "ui-3p")
			wantLocal, wantApp := "", ""
			if platform == "windows" {
				wantLocal = filepath.Join(base, "local-app-data")
				wantApp = filepath.Join(base, "app-data")
				wantUI = filepath.Join(wantLocal, "Claude-3p")
			}
			if err != nil || p.Root != a.dir || p.UserDataDir != wantUI || p.ProfileDir != wantUI || p.ClaudeConfigDir != filepath.Join(base, "code") || p.LocalAppData != wantLocal || p.AppData != wantApp || filepath.Dir(p.SelectionPath) != a.dir {
				t.Fatalf("private paths ignored: %+v %v", p, err)
			}
			if !claudeDesktopWithin(a.dir, p.ProfileDir) || claudeDesktopWithin(home, p.ProfileDir) || p.ConfigPath != filepath.Join(wantUI, "configLibrary", claudeDesktopProfileID+".json") || p.ModePath != filepath.Join(wantUI, "claude_desktop_config.json") {
				t.Fatalf("profile escaped private root: %+v", p)
			}
			if _, err := a.saveClaudeDesktopProfile(desktopProfileTestSelection(), p); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{p.UserDataDir, p.ClaudeConfigDir, p.LocalAppData, p.AppData} {
				if dir == "" {
					continue
				}
				info, err := os.Stat(dir)
				if err != nil || !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
					t.Fatalf("private directory not prepared: %s %v", dir, err)
				}
			}
			if err := a.verifyClaudeDesktopProfile(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClaudeDesktopPrivateProfileDoesNotTouchOrdinaryProfiles(t *testing.T) {
	for _, platform := range []string{"macos", "windows", "linux"} {
		t.Run(platform, func(t *testing.T) {
			a := testApp(t)
			a.claudeDesktopCheckRunning = func(string) (bool, error) { return false, nil }
			home := t.TempDir()
			a.editorTestRoot = home
			a.launcher = &clientLaunchRuntime{platform: platform, home: home}
			local, roaming, xdg := filepath.Join(home, "AppData", "Local"), filepath.Join(home, "AppData", "Roaming"), filepath.Join(home, ".config")
			t.Setenv("LOCALAPPDATA", local)
			t.Setenv("APPDATA", roaming)
			t.Setenv("XDG_CONFIG_HOME", xdg)
			var originals = map[string][]byte{}
			for _, base := range []string{filepath.Join(home, "Library", "Application Support"), local, roaming, xdg} {
				for _, profile := range []string{"Claude", "Claude-3p"} {
					for _, file := range []string{"claude_desktop_config.json", "Preferences", filepath.Join("configLibrary", "_meta.json"), filepath.Join("configLibrary", claudeDesktopProfileID+".json")} {
						path := filepath.Join(base, profile, file)
						// Deliberately invalid JSON must not prevent preparation of the
						// new profile or get interpreted as a file to migrate/repair.
						originals[path] = []byte("ordinary authenticated profile: " + path + "\n")
					}
				}
			}
			originals[filepath.Join(home, ".claude", ".credentials.json")] = []byte("ordinary Claude Code authentication\n")
			for path, data := range originals {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			s := desktopProfileTestSelection()
			oldSelection, _ := json.Marshal(s)
			p, err := a.claudeDesktopPaths()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p.SelectionPath, oldSelection, 0600); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{a.config.LocalKey, "rotated-synthetic-key"} {
				a.config.LocalKey = key
				if _, err := a.saveClaudeDesktopProfile(s, p); err != nil {
					t.Fatal(err)
				}
				if err := a.verifyClaudeDesktopProfile(); err != nil {
					t.Fatal(err)
				}
			}
			for path, original := range originals {
				data, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(data, original) {
					t.Fatalf("ordinary profile changed: %s %v", path, err)
				}
				if _, err := os.Lstat(path + ".bak"); !os.IsNotExist(err) {
					t.Fatalf("ordinary backup created: %s %v", path, err)
				}
			}
			if data, err := os.ReadFile(p.SelectionPath); err != nil || !bytes.Equal(data, oldSelection) {
				t.Fatalf("existing model selection replaced: %s %v", data, err)
			}
			for _, path := range []string{filepath.Join(p.UserDataDir, "Preferences"), filepath.Join(p.ClaudeConfigDir, ".credentials.json")} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("ordinary authenticated state copied: %s %v", path, err)
				}
			}
		})
	}
}

func TestClaudeDesktopPrivateProfileRejectsUnsafeAuxiliaryDirectories(t *testing.T) {
	for _, field := range []string{"user-data", "code", "local-app-data", "app-data"} {
		t.Run(field, func(t *testing.T) {
			a, _ := desktopProfileTestApp(t)
			a.launcher.platform = "windows"
			p, err := a.claudeDesktopPaths()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.saveClaudeDesktopProfile(desktopProfileTestSelection(), p); err != nil {
				t.Fatal(err)
			}
			dir := map[string]string{"user-data": p.UserDataDir, "code": p.ClaudeConfigDir, "local-app-data": p.LocalAppData, "app-data": p.AppData}[field]
			// Move our synthetic directory away before replacing its path with
			// a link. Never remove a real vendor directory in this fixture.
			moved := dir + "-fixture"
			if err := os.Rename(dir, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, dir); err != nil {
				t.Skip(err)
			}
			if err := a.verifyClaudeDesktopProfile(); err == nil {
				t.Fatal("accepted linked private directory")
			}
			before, err := os.ReadFile(filepath.Join(moved, "claude_desktop_config.json"))
			if field != "user-data" {
				before, err = os.ReadFile(p.ModePath)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.saveClaudeDesktopProfile(desktopProfileTestSelection(), p); err == nil {
				t.Fatal("accepted linked private directory during preparation")
			}
			after, err := os.ReadFile(p.ModePath)
			if err != nil || !bytes.Equal(after, before) {
				t.Fatal("profile changed on failed preparation", err)
			}
		})
	}
}

func TestClaudeDesktopProfileRepairPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	a, p := desktopProfileTestApp(t)
	if _, err := a.saveClaudeDesktopProfile(desktopProfileTestSelection(), p); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.ConfigPath, 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := a.saveClaudeDesktopProfile(desktopProfileTestSelection(), p)
	if err != nil || !changed {
		t.Fatalf("permissions not repaired: %v", err)
	}
	for _, path := range []string{p.ConfigPath, p.ConfigPath + ".bak"} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("unsafe file: %s", path)
		}
	}
}

func desktopProfileTestMixedSelection() editorSelection {
	return editorSelection{Models: []editorModel{
		{ID: "anthropic/claude-haiku-4.5", Name: "Haiku"},
		{ID: "openai/gpt-4.1-nano", Name: "GPT-4.1 Nano"},
		{ID: "z-ai/glm-5.3-flash"},
	}, Initial: "openai/gpt-4.1-nano"}
}

func TestClaudeDesktopExperimentalAliases(t *testing.T) {
	id := "openai/gpt-4.1-nano"
	want := "claude-kilo-v1-56000253345871890788775171972345815131729034098819977729670265502282893184032"
	if got := claudeDesktopAlias(id); got != want {
		t.Fatalf("wrong stable full-SHA alias: %s", got)
	}
	// This real-ID fixture's hexadecimal digest contains Desktop's blocked
	// "abab" fragment. Decimal aliases must remain clear of that denylist.
	fixture := "vendor/model-1782"
	hash := sha256.Sum256([]byte(fixture))
	if !strings.Contains(hex.EncodeToString(hash[:]), "abab") {
		t.Fatal("denylist regression fixture changed")
	}
	for _, real := range []string{id, fixture, "z-ai/glm-5.3-flash", "openai/gpt-5.4"} {
		alias := claudeDesktopAlias(real)
		digest := strings.TrimPrefix(alias, claudeDesktopAliasPrefix)
		if len(digest) == 0 || len(digest) > 78 || strings.IndexFunc(digest, func(r rune) bool { return r < '0' || r > '9' }) >= 0 || len(alias) > 200 || !catalogID.MatchString(alias) || claudeDesktopOtherModel.MatchString(alias) {
			t.Fatalf("Desktop rejects generated alias: %s", alias)
		}
	}
	if claudeDesktopAlias(id) == claudeDesktopAlias("other/gpt-4.1-nano") {
		t.Fatal("provider identity lost in alias")
	}
	s := desktopProfileTestMixedSelection()
	if validateClaudeDesktopSelection(s) == nil || validateClaudeDesktopSelectionMode(s, false) == nil || validateClaudeDesktopSelectionMode(s, true) != nil {
		t.Fatal("experimental opt-in was not enforced")
	}
	for _, alias := range []string{want, "anthropic/" + want} {
		if claudeDesktopModelSupported(alias) {
			t.Fatal("synthetic alias recognized as native Claude")
		}
		bad := editorSelection{Models: []editorModel{{ID: alias}}, Initial: alias}
		if validateClaudeDesktopSelectionMode(bad, true) == nil {
			t.Fatal("synthetic alias accepted as a stored real ID")
		}
	}
	for _, mutate := range []func(*editorSelection){
		func(s *editorSelection) { s.Models[1].ID = "bad model id" },
		func(s *editorSelection) { s.Models[1].Name = "bad\nname" },
		func(s *editorSelection) { s.Models[1].Context = 200000 },
		func(s *editorSelection) { s.Models[1].Output = 4096 },
	} {
		bad := desktopProfileTestMixedSelection()
		mutate(&bad)
		if validateClaudeDesktopSelectionMode(bad, true) == nil {
			t.Fatal("experimental mode bypassed basic selection validation")
		}
	}
	config := claudeDesktopOwnedConfig(s, 8877, "test-only-key", true)
	models := config["inferenceModels"].([]map[string]string)
	if len(models) != 3 || models[0]["name"] != want || models[0]["labelOverride"] != "GPT-4.1 Nano" || models[1]["name"] != s.Models[0].ID || models[2]["name"] != claudeDesktopAlias(s.Models[2].ID) || models[2]["labelOverride"] != s.Models[2].ID {
		t.Fatalf("wrong model identities, labels, or order: %+v", models)
	}
	for _, model := range models {
		if len(model) != 2 {
			t.Fatalf("invented model capability: %+v", model)
		}
	}
	legacy := claudeDesktopOwnedConfig(desktopProfileTestSelection(), 8877, "test-only-key")
	nativeExperimental := claudeDesktopOwnedConfig(desktopProfileTestSelection(), 8877, "test-only-key", true)
	a, _ := json.Marshal(legacy)
	b, _ := json.Marshal(nativeExperimental)
	if !bytes.Equal(a, b) {
		t.Fatal("experimental mode changed native Claude configuration")
	}
}

func TestClaudeDesktopExperimentalProfileKeepsRealSelection(t *testing.T) {
	a, p := desktopProfileTestApp(t)
	s := desktopProfileTestMixedSelection()
	if _, err := a.saveClaudeDesktopProfile(s, p); err == nil {
		t.Fatal("mixed profile prepared without opting in")
	}
	a.config.ClaudeDesktopExperimentalModels = true
	if _, err := a.saveClaudeDesktopProfile(s, p); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p.SelectionPath)
	if err != nil || bytes.Contains(data, []byte(claudeDesktopAliasPrefix)) || !bytes.Contains(data, []byte(s.Initial)) {
		t.Fatalf("real selection was replaced by aliases: %s %v", data, err)
	}
	loaded, err := a.readClaudeDesktopSelection(p)
	if err != nil || loaded.Initial != s.Initial || len(loaded.Models) != len(s.Models) {
		t.Fatalf("cannot read real selection: %+v %v", loaded, err)
	}
	if err := a.verifyClaudeDesktopProfile(); err != nil {
		t.Fatal(err)
	}
	w := desktopProfileTestRequest(a, http.MethodGet, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), claudeDesktopAliasPrefix) || !strings.Contains(w.Body.String(), s.Initial) {
		t.Fatalf("profile API did not preserve real IDs: %d %s", w.Code, w.Body.String())
	}
	a.config.ClaudeDesktopExperimentalModels = false
	if err := a.verifyClaudeDesktopProfile(); err == nil || !strings.Contains(err.Error(), "Experimental") {
		t.Fatalf("disabled mode accepted saved mixed profile: %v", err)
	}
	if _, err := a.saveClaudeDesktopProfile(desktopProfileTestSelection(), p); err != nil {
		t.Fatalf("cannot recover native profile after disabling: %v", err)
	}
	if err := a.verifyClaudeDesktopProfile(); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(p.SelectionPath + ".bak")
	if err != nil || !bytes.Equal(backup, data) {
		t.Fatal("mixed selection was not backed up during native recovery")
	}
}

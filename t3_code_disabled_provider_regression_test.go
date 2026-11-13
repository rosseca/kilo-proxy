package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// This synthetic library deliberately contains two exact gateway identities
// with incompatible defaults for one native Claude effort key. The defaults
// are valid independently; no provider installation or personal settings are
// consulted by these profile-planning tests.
func t3DisabledProviderRegressionOptions(t *testing.T) t3CodeProfileOptions {
	t.Helper()
	options := t3ProfileTestOptions(t)
	options.ClaudeModelDefaults = true
	options.ClaudeCaps = claudeCaps("2.1.288")
	options.Library = modelLibrary{SchemaVersion: 1, DefaultModel: "openai/gpt-6.1-sol"}
	ids := []string{
		"openai/gpt-6.1-sol",
		"anthropic/claude-opus-5.5",
		"anthropic/claude-sonnet-5.5",
		"anthropic/claude-fable-5.1",
		"anthropic/claude-opus-4.6",
		"anthropic/claude-opus-4-6-20260813",
	}
	for i := 0; len(ids) < 20; i++ {
		ids = append(ids, fmt.Sprintf("synthetic/model-%02d", i))
	}
	options.Catalog = nil
	for _, id := range ids {
		effort := ""
		if id == "anthropic/claude-opus-4.6" {
			effort = "high"
		} else if id == "anthropic/claude-opus-4-6-20260813" {
			effort = "low"
		}
		options.Library.Models = append(options.Library.Models, modelLibraryItem{ID: id, ReasoningEffort: effort, ContextWindow: 200000, MaxOutputTokens: 4096})
		options.Catalog = append(options.Catalog, modelInfo{ID: id, ContextWindow: 200000, MaxOutputTokens: 4096, ReasoningEfforts: []string{"low", "high"}})
	}
	return options
}

func TestT3CodeDisabledKiloClaudeDoesNotBlockCodexLibrary(t *testing.T) {
	options := t3DisabledProviderRegressionOptions(t)
	before := cloneModelLibrary(options.Library)
	prefs := normalizeT3CodePreferences(t3CodePreferences{})
	prefs.KiloClaude = false
	policy, err := deriveT3CodeModelPolicy(prefs, options.Library, options.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.ClaudeLibrary.Models) != 0 || len(policy.CodexLibrary.Models) != 1 {
		t.Fatal("disabled Claude or active Codex policy is incorrect", policy)
	}
	options.Policy, options.Preferences = &policy, &prefs
	profiles, err := planT3CodeProfiles(options)
	if err != nil {
		t.Fatal("disabled Claude Kilo must not validate unrelated Claude defaults or block active Codex Kilo:", err)
	}
	if !reflect.DeepEqual(before, options.Library) {
		t.Fatal("preparing one provider rewrote the shared library")
	}
	claudeDir := filepath.Join(options.RootDir, "profiles", "claude-kilo")
	codexDir := filepath.Join(options.RootDir, "profiles", "codex-kilo")
	foundCodex := false
	for _, file := range profiles.Files {
		if strings.HasPrefix(file.path, claudeDir+string(filepath.Separator)) {
			t.Fatal("disabled Claude Kilo still plans a private Claude profile", file.path)
		}
		if file.path == filepath.Join(codexDir, "config.toml") {
			foundCodex = true
		}
	}
	if !foundCodex {
		t.Fatal("enabled Codex Kilo profile was not planned")
	}
}

func TestT3CodeLegacyPreferencesUseOfficialSelectionsWithoutChangingSharedLibrary(t *testing.T) {
	options := t3DisabledProviderRegressionOptions(t)
	before := cloneModelLibrary(options.Library)
	prefs := normalizeT3CodePreferences(t3CodePreferences{})
	if prefs.ClaudeModels != "official" || prefs.CodexModels != "official" || !prefs.KiloClaude || !prefs.KiloCodex {
		t.Fatal("legacy preferences did not select the official provider defaults", prefs)
	}
	policy, err := deriveT3CodeModelPolicy(prefs, options.Library, options.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	var claudeIDs []string
	for _, model := range policy.ClaudeLibrary.Models {
		claudeIDs = append(claudeIDs, model.ID)
	}
	want := []string{"anthropic/claude-opus-5.5", "anthropic/claude-sonnet-5.5", "anthropic/claude-fable-5.1", "anthropic/claude-opus-4.6"}
	if !reflect.DeepEqual(claudeIDs, want) || policy.CodexLibrary.DefaultModel != options.Library.DefaultModel || len(policy.CodexLibrary.Models) != 1 {
		t.Fatal("legacy effective selections or Codex default changed", policy)
	}
	options.Policy, options.Preferences = &policy, &prefs
	if _, err := planT3CodeProfiles(options); err != nil {
		t.Fatal("official Claude selection inherited unrelated shared-default collision:", err)
	}
	if len(options.Library.Models) != 20 || !reflect.DeepEqual(before, options.Library) {
		t.Fatal("official preparation mutated the shared twenty-model library")
	}
}

func TestT3CodeDisabledProviderKeepsPreviousProfilesAndNeedsNoBinary(t *testing.T) {
	for _, disabled := range []string{"codex", "claude"} {
		t.Run(disabled, func(t *testing.T) {
			options := t3DisabledProviderRegressionOptions(t)
			before := cloneModelLibrary(options.Library)
			prefs := normalizeT3CodePreferences(t3CodePreferences{})
			if disabled == "codex" {
				prefs.Codex, prefs.KiloCodex = false, false
				options.CodexBinary = ""
			} else {
				prefs.Claude, prefs.KiloClaude = false, false
				options.ClaudeBinary = ""
			}
			policy, err := deriveT3CodeModelPolicy(prefs, options.Library, options.Catalog)
			if err != nil {
				t.Fatal(err)
			}
			options.Policy, options.Preferences = &policy, &prefs
			disabledDir := filepath.Join(options.RootDir, "profiles", disabled+"-kilo")
			if err := os.MkdirAll(disabledDir, 0700); err != nil {
				t.Fatal(err)
			}
			// Invalid managed settings must not even be parsed for a disabled
			// provider, and unrelated files from its old home remain untouched.
			sentinels := map[string][]byte{
				"config.toml":      []byte("not valid TOML [[["),
				"settings.json":    []byte("not valid JSON {"),
				"models.json":      []byte("old model identities"),
				"kilo-models.json": []byte("old Claude identities and defaults"),
				"auth.json":        []byte("synthetic previous account sentinel"),
				"chat-history":     []byte("synthetic previous conversation sentinel"),
			}
			for name, data := range sentinels {
				if err := os.WriteFile(filepath.Join(disabledDir, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			profiles, err := prepareT3CodeProfiles(options)
			if err != nil {
				t.Fatal("disabled provider settings/binary blocked the enabled provider:", err)
			}
			for _, file := range profiles.Files {
				if strings.HasPrefix(file.path, disabledDir+string(filepath.Separator)) {
					t.Fatal("planned a disabled provider file", file.path)
				}
			}
			for name, expected := range sentinels {
				path := filepath.Join(disabledDir, name)
				actual, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(actual, expected) {
					t.Fatal("changed a disabled profile/account/conversation file", path, err)
				}
				if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
					t.Fatal("backed up a disabled provider file", path, err)
				}
			}
			if !reflect.DeepEqual(options.Library, before) {
				t.Fatal("changed the shared model library")
			}
			id := t3CodeCodexProxyID
			if disabled == "claude" {
				id = t3CodeClaudeProxyID
			}
			if profiles.Providers[id].(map[string]any)["enabled"] != false {
				t.Fatal("old disabled provider identity was enabled")
			}
		})
	}
}

func TestT3CodeDisabledProviderPreparationReadinessAndReenable(t *testing.T) {
	for _, disabled := range []string{"codex", "claude"} {
		for _, invalidSecret := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/invalid-secret-%t", disabled, invalidSecret), func(t *testing.T) {
				a := t3CodeTestApp(t, "macos")
				a.t3CodeProbeClaude = func(string, clientLaunchRuntime) claudeCapabilities { return claudeCaps("2.1.288") }
				a.config.T3Code.Codex, a.config.T3Code.Claude = false, false
				prepareT3CodeFixture(t, a)
				paths := t3CodePaths(a.dir)
				disabledID, disabledKey, disabledClient := t3CodeCodexProxyID, "KILO_LOCAL_API_KEY", "codex-cli"
				if disabled == "claude" {
					disabledID, disabledKey, disabledClient = t3CodeClaudeProxyID, "ANTHROPIC_AUTH_TOKEN", "claude"
				}
				disabledDir := filepath.Join(paths.Root, "profiles", disabled+"-kilo")
				chat := filepath.Join(disabledDir, "synthetic-chat-history")
				if err := os.WriteFile(chat, []byte("keep previous conversations"), 0600); err != nil {
					t.Fatal(err)
				}
				before := map[string][]byte{}
				entries, err := os.ReadDir(disabledDir)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					path := filepath.Join(disabledDir, entry.Name())
					before[path], err = os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
				}
				secret := filepath.Join(paths.Secrets, t3CodeSecretName(disabledID, disabledKey))
				oldSecret, err := os.ReadFile(secret)
				if err != nil {
					t.Fatal(err)
				}
				if invalidSecret {
					if err := os.Remove(secret); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(secret, 0700); err != nil {
						t.Fatal(err)
					}
				} else {
					before[secret] = oldSecret
				}
				if disabled == "codex" {
					a.config.T3Code.KiloCodex = false
				} else {
					a.config.T3Code.KiloClaude = false
				}
				resolve := a.launcher.resolve
				a.launcher.resolve = func(client, custom string) (string, error) {
					if client == disabledClient {
						t.Fatal("disabled CLI was discovered", client)
					}
					return resolve(client, custom)
				}
				prepareT3CodeFixture(t, a)
				saved := mustReadT3CodePrepared(t, a)
				binary, _ := resolve("t3-code", "")
				if len(saved.Files) != 3 || !a.t3CodeReady(saved, binary, a.launchRuntime()) {
					t.Fatal("one-account preparation is not ready with its two profiles and one secret", saved.Files)
				}
				provider := saved.Providers[disabledID].(map[string]any)
				if provider["enabled"] != false {
					t.Fatal("disabled account identity was not retained")
				}
				for _, raw := range provider["environment"].([]any) {
					value := raw.(map[string]any)
					if value["sensitive"] == true || value["name"] == "ANTHROPIC_BASE_URL" {
						t.Fatal("disabled account still references a secret or proxy endpoint", value)
					}
				}
				for path, expected := range before {
					actual, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(actual, expected) {
						t.Fatal("disabled account/profile/chat changed", path, err)
					}
				}
				if invalidSecret {
					if info, err := os.Stat(secret); err != nil || !info.IsDir() {
						t.Fatal("disabled invalid secret was inspected or replaced", err)
					}
				}
				selectionData, err := os.ReadFile(paths.Selection)
				if err != nil {
					t.Fatal(err)
				}
				var activePath string
				for relative := range saved.Files {
					activePath = relative
					break
				}
				delete(saved.Files, activePath)
				badSelection, _ := json.Marshal(saved)
				if err := os.WriteFile(paths.Selection, badSelection, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := a.readT3CodePrepared(); err == nil {
					t.Fatal("manifest missing an active provider file was accepted", activePath)
				}
				if err := os.WriteFile(paths.Selection, selectionData, 0600); err != nil {
					t.Fatal(err)
				}
				saved = mustReadT3CodePrepared(t, a)
				activeFile := filepath.Join(paths.Root, activePath)
				original, err := os.ReadFile(activeFile)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(activeFile, append(append([]byte{}, original...), '\n'), 0600); err != nil {
					t.Fatal(err)
				}
				if a.t3CodeReady(saved, binary, a.launchRuntime()) {
					t.Fatal("tampered active provider file was accepted")
				}
				if err := os.WriteFile(activeFile, original, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(activeFile, activeFile+".synthetic-hold"); err != nil {
					t.Fatal(err)
				}
				if a.t3CodeReady(saved, binary, a.launchRuntime()) {
					t.Fatal("missing active provider file was accepted")
				}
				if err := os.Rename(activeFile+".synthetic-hold", activeFile); err != nil {
					t.Fatal(err)
				}
				if disabled == "codex" {
					a.config.T3Code.KiloCodex = true
				} else {
					a.config.T3Code.KiloClaude = true
				}
				a.launcher.resolve = resolve
				if a.t3CodeReady(saved, binary, a.launchRuntime()) {
					t.Fatal("re-enabled account did not require preparation")
				}
				if invalidSecret {
					response := adminRequest(a, "clients/t3-code", `{}`)
					if response.Code != 409 {
						t.Fatal("re-enabled malformed secret destination was accepted", response.Code, response.Body.String())
					}
					if err := os.Remove(secret); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(secret, oldSecret, 0600); err != nil {
						t.Fatal(err)
					}
				}
				prepareT3CodeFixture(t, a)
				if !a.t3CodeReady(mustReadT3CodePrepared(t, a), binary, a.launchRuntime()) {
					t.Fatal("re-enabled account did not become ready after preparation")
				}
				if actual, err := os.ReadFile(chat); err != nil || string(actual) != "keep previous conversations" {
					t.Fatal("re-enabling account changed previous conversations", err)
				}
			})
		}
	}
}

func TestT3CodeDisabledDirectAccountCannotOverlapActiveKiloHome(t *testing.T) {
	for _, active := range []string{"codex", "claude"} {
		for _, normalHome := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
			for _, alias := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/alias-%t", active, normalHome, alias), func(t *testing.T) {
					options := t3DisabledProviderRegressionOptions(t)
					prefs := normalizeT3CodePreferences(t3CodePreferences{})
					prefs.Codex, prefs.Claude = false, false
					prefs.KiloCodex, prefs.KiloClaude = active == "codex", active == "claude"
					policy, err := deriveT3CodeModelPolicy(prefs, options.Library, options.Catalog)
					if err != nil {
						t.Fatal(err)
					}
					options.Policy, options.Preferences = &policy, &prefs
					private := filepath.Join(options.RootDir, "profiles", active+"-kilo")
					path := private
					if alias {
						path = filepath.Join(t.TempDir(), "external-home-alias")
						if err := os.Symlink(private, path); err != nil {
							t.Skip("symlinks unavailable:", err)
						}
					}
					options.NormalEnvironment[normalHome] = path
					if _, err := planT3CodeProfiles(options); err == nil || !strings.Contains(err.Error(), "separate from normal provider homes") {
						t.Fatal("disabled direct account allowed Kilo to overwrite its external home", err)
					}
					if _, err := os.Stat(options.RootDir); !os.IsNotExist(err) {
						t.Fatal("overlap rejection created a private profile", err)
					}
				})
			}
		}
	}
}

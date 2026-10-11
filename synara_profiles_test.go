package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func synaraProfileTestOptions(t *testing.T) synaraProfileOptions {
	t.Helper()
	base := t3ProfileTestOptions(t)
	root := filepath.Join(filepath.Dir(base.RootDir), "synara")
	return synaraProfileOptions{RootDir: root, DataDir: filepath.Join(root, "data"), UserDataDir: filepath.Join(root, "electron"), NormalHome: base.NormalHome, CodexBinary: base.CodexBinary, ClaudeBinary: base.ClaudeBinary, NormalEnvironment: base.NormalEnvironment, Library: base.Library, Catalog: base.Catalog, ClaudeCaps: base.ClaudeCaps, Port: base.Port, LocalKey: base.LocalKey}
}

func saveSynaraProfilePlan(t *testing.T, options synaraProfileOptions) synaraProfiles {
	t.Helper()
	plan, err := planSynaraProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saveEditorFiles(plan.Files); err != nil {
		t.Fatal(err)
	}
	return plan
}

// Mirrors the installed beta's intentionally small discovery parser contract:
// bare model_provider before the first table, and bare env_key in an explicit
// model_providers table. Valid TOML alone does not satisfy those consumers.
func synaraNativeCodexDiscovery(data []byte) (provider, envKey string) {
	assignment := func(line, key string) string {
		match := regexp.MustCompile(`^` + key + `\s*=\s*(?:"([^"]+)"|'([^']+)')`).FindStringSubmatch(line)
		if len(match) == 3 {
			return match[1] + match[2]
		}
		return ""
	}
	sectionName := regexp.MustCompile(`^\[\s*model_providers\.(?:"([^"]+)"|'([^']+)'|([A-Za-z0-9_-]+))\s*\]$`)
	root, section := true, ""
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			root, section = false, ""
			if match := sectionName.FindStringSubmatch(line); len(match) == 4 {
				section = match[1] + match[2] + match[3]
			}
			continue
		}
		if root && provider == "" {
			provider = assignment(line, "model_provider")
		}
		if section == provider && provider != "" && envKey == "" {
			envKey = assignment(line, "env_key")
		}
	}
	return provider, envKey
}

func TestSynaraCodexConfigurationMatchesInstalledDiscovery(t *testing.T) {
	t.Run("new private profile", func(t *testing.T) {
		options := synaraProfileTestOptions(t)
		plan := saveSynaraProfilePlan(t, options)
		data, err := os.ReadFile(filepath.Join(plan.CodexHome, "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		provider, envKey := synaraNativeCodexDiscovery(data)
		if provider != "kilo-local" || envKey != "KILO_LOCAL_API_KEY" {
			t.Fatalf("installed health/credential discovery cannot see private provider: %q %q\n%s", provider, envKey, data)
		}
		if _, err := os.Stat(filepath.Join(plan.CodexHome, "auth.json")); !os.IsNotExist(err) {
			t.Fatalf("custom-provider setup created an auth file: %v", err)
		}
	})
	t.Run("quoted dotted and multiline settings", func(t *testing.T) {
		data := []byte(`# private comments are not credentials
"model_provider" = 'kilo-local'
"cli_auth_credentials_store" = 'keyring'
instructions = '''line one
[model_providers.openai]
env_key = 'DO_NOT_USE'
line four'''
"model_providers"."kilo-local"."env_key" = 'KILO_LOCAL_API_KEY'
"model_providers"."kilo-local"."wire_api" = 'responses'
profile = 'work'
["profiles"."work"]
model_provider = 'kilo-local'
unrelated = ['one', 'two']
[[custom]]
name = 'first'
[custom.nested]
number = 9007199254740993
[[custom]]
name = 'second'
`)
		var expected map[string]any
		if err := toml.Unmarshal(data, &expected); err != nil {
			t.Fatal(err)
		}
		if provider, _ := synaraNativeCodexDiscovery(data); provider != "" {
			t.Fatal("regression fixture must be rejected by the installed parser")
		}
		got, err := synaraCodexFileAuthentication(data)
		if err != nil {
			t.Fatal(err)
		}
		var actual map[string]any
		if err := toml.Unmarshal(got, &actual); err != nil {
			t.Fatal(err)
		}
		expected["cli_auth_credentials_store"] = "file"
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("private canonical formatting changed TOML meaning: got %#v want %#v", actual, expected)
		}
		provider, envKey := synaraNativeCodexDiscovery(got)
		if provider != "kilo-local" || envKey != "KILO_LOCAL_API_KEY" {
			t.Fatalf("installed health/credential discovery cannot see canonical provider: %q %q\n%s", provider, envKey, got)
		}
	})
}

func TestSynaraProfilesUseFourIsolatedInstancesAndNativeSecretStore(t *testing.T) {
	options := synaraProfileTestOptions(t)
	for _, dir := range []string{filepath.Join(options.NormalHome, ".codex"), filepath.Join(options.NormalHome, ".claude")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	normal := map[string][]byte{filepath.Join(options.NormalHome, ".codex", "config.toml"): []byte("cli_auth_credentials_store='keyring'\nmodel='normal'\n"), filepath.Join(options.NormalHome, ".codex", "auth.json"): []byte("normal-codex-login"), filepath.Join(options.NormalHome, ".claude", "settings.json"): []byte("{\"normal\":true}\n"), filepath.Join(options.NormalHome, ".claude.json"): []byte("normal-claude-login")}
	for path, data := range normal {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	plan := saveSynaraProfilePlan(t, options)
	for path, want := range normal {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("normal provider data changed: %s %v", path, err)
		}
	}
	if len(plan.Providers) != 4 {
		t.Fatalf("want four instances: %#v", plan.Providers)
	}
	for _, id := range []string{synaraCodexNormalID, synaraCodexProxyID, synaraClaudeNormalID, synaraClaudeProxyID} {
		instance := plan.Providers[id].(map[string]any)
		config := instance["config"].(map[string]any)
		models := config["customModels"].([]any)
		proxy := id == synaraCodexProxyID || id == synaraClaudeProxyID
		if proxy {
			if instance["accentColor"] != "#327653" || !strings.HasPrefix(instance["displayName"].(string), "Kilo Proxy") {
				t.Fatalf("proxy badge contract: %#v", instance)
			}
			if len(models) != len(options.Library.Models) || models[0] != options.Library.DefaultModel {
				t.Fatalf("custom strings/default first: %#v", models)
			}
		} else if len(models) != 0 {
			t.Fatalf("normal custom models changed: %#v", models)
		}
		for _, model := range models {
			if _, ok := model.(string); !ok {
				t.Fatalf("Synara accepts strings, not descriptors: %#v", model)
			}
		}
		for _, raw := range instance["environment"].([]any) {
			variable := raw.(map[string]any)
			if variable["name"] == "ANTHROPIC_API_KEY" {
				t.Fatal("normal inherited credential copied")
			}
			if variable["name"] == "HOME" {
				home := variable["value"]
				if proxy && home != config["homePath"] || !proxy && home != options.NormalHome {
					t.Fatalf("wrong provider HOME: %#v", variable)
				}
			}
		}
	}
	claudeNormal := plan.Providers[synaraClaudeNormalID].(map[string]any)["config"].(map[string]any)
	if claudeNormal["homePath"] != options.NormalHome || claudeNormal["configDir"] != filepath.Join(options.NormalHome, ".claude") {
		t.Fatalf("normal Claude HOME/configDir: %#v", claudeNormal)
	}
	if claudeNormal["secureStorageDir"] != "" {
		t.Fatal("normal keychain namespace changed by explicit configDir")
	}
	var normalKeychainOverride bool
	for _, raw := range plan.Providers[synaraClaudeNormalID].(map[string]any)["environment"].([]any) {
		variable := raw.(map[string]any)
		normalKeychainOverride = normalKeychainOverride || variable["name"] == "CLAUDE_SECURESTORAGE_CONFIG_DIR" && variable["value"] == ""
	}
	if !normalKeychainOverride {
		t.Fatal("normal unsuffixed keychain namespace not preserved")
	}
	var codex map[string]any
	codexBytes, _ := os.ReadFile(filepath.Join(plan.CodexHome, "config.toml"))
	if err := toml.Unmarshal(codexBytes, &codex); err != nil {
		t.Fatal(err)
	}
	if codex["cli_auth_credentials_store"] != "file" || codex["model_provider"] != "kilo-local" {
		t.Fatalf("Synara private Codex auth/routing: %#v", codex)
	}
	var claude map[string]any
	claudeBytes, _ := os.ReadFile(filepath.Join(plan.ClaudeHome, "settings.json"))
	if err := json.Unmarshal(claudeBytes, &claude); err != nil {
		t.Fatal(err)
	}
	if claude["env"].(map[string]any)["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:18877/synara" {
		t.Fatalf("dedicated route: %#v", claude)
	}
	if _, exists := claude["effortLevel"]; exists {
		t.Fatal("global reasoning would leak between models")
	}
	perModel := claude["modelSettings"].(map[string]any)
	if perModel["claude-fable-5-1"].(map[string]any)["effortLevel"] != "high" {
		t.Fatalf("saved per-model reasoning: %#v", perModel)
	}
	settingsData, _ := os.ReadFile(filepath.Join(options.DataDir, "userdata", "settings.json"))
	var envelope map[string]any
	if err := json.Unmarshal(settingsData, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["migrationVersion"] != float64(3) || envelope["revision"] != float64(0) {
		t.Fatalf("native envelope: %#v", envelope)
	}
	settings := envelope["settings"].(map[string]any)
	for _, driver := range synaraDrivers() {
		if settings["providers"].(map[string]any)[driver].(map[string]any)["enabled"] != (driver == "codex" || driver == "claudeAgent") {
			t.Fatalf("custom accounts blocked or unrelated driver enabled: %s", driver)
		}
	}
	instances := settings["providerInstances"].(map[string]any)
	for _, driver := range []string{"codex", "claudeAgent"} {
		instance := instances[driver].(map[string]any)
		if instance["driver"] != driver || instance["enabled"] != false || instance["config"].(map[string]any)["enabled"] != false {
			t.Fatalf("implicit default account not disabled: %#v", instance)
		}
	}
	for id, name := range map[string]string{synaraCodexProxyID: "KILO_LOCAL_API_KEY", synaraClaudeProxyID: "ANTHROPIC_AUTH_TOKEN"} {
		path := filepath.Join(options.DataDir, "userdata", "secrets", t3CodeSecretName(id, name))
		data, err := os.ReadFile(path)
		if err != nil || string(data) != options.LocalKey {
			t.Fatalf("native secret store %s: %v", path, err)
		}
		if runtime.GOOS != "windows" {
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0600 {
				t.Fatalf("secret mode: %v", info.Mode())
			}
		}
	}
	for _, file := range plan.Files {
		if strings.Contains(file.path, string(filepath.Separator)+"secrets"+string(filepath.Separator)) {
			continue
		}
		if bytes.Contains(file.new, []byte(options.LocalKey)) || bytes.Contains(file.new, []byte("normal-codex-login")) {
			t.Fatalf("credentials outside native secret store: %s", file.path)
		}
	}
	if !synaraProfilesReady(options, plan.Providers) {
		t.Fatal("planned native registration not ready")
	}
}

func TestSynaraProfilesPreserveUnknownSettingsAndTolerateNativeEnvelopeUpdates(t *testing.T) {
	options := synaraProfileTestOptions(t)
	settingsPath := filepath.Join(options.DataDir, "userdata", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0700); err != nil {
		t.Fatal(err)
	}
	old := []byte(`{"revision":11,"migrationVersion":3,"settings":{"sidechatExpiry":"24h","onboardingCompletedAt":null,"providers":{"codex":{"binaryPath":"custom-normal-codex","enabled":true}},"providerInstances":{"future":{"driver":"futureDriver","config":{"id":9007199254740993123}}},"custom":{"large":9007199254740993123}}}`)
	if err := os.WriteFile(settingsPath, old, 0600); err != nil {
		t.Fatal(err)
	}
	plan := saveSynaraProfilePlan(t, options)
	data, _ := os.ReadFile(settingsPath)
	envelope, settings, err := synaraSettingsDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(envelope["revision"]) != "11" || !bytes.Contains(settings["custom"], []byte("9007199254740993123")) {
		t.Fatal("unowned number/revision changed")
	}
	var providers map[string]map[string]any
	json.Unmarshal(settings["providers"], &providers)
	if providers["codex"]["binaryPath"] != "custom-normal-codex" {
		t.Fatal("unowned legacy setting changed")
	}
	instances, _ := decodeClaudeDesktopObject(settings["providerInstances"])
	if !bytes.Contains(instances["future"], []byte("9007199254740993123")) {
		t.Fatal("unowned instance changed")
	}
	claudeDesktopSet(envelope, "revision", 42)
	claudeDesktopSet(settings, "enableAssistantStreaming", false)
	claudeDesktopSet(settings, "onboardingCompletedAt", "2026-10-05T10:00:00Z")
	claudeDesktopSet(envelope, "settings", settings)
	updated, _ := json.Marshal(envelope)
	if err := os.WriteFile(settingsPath, updated, 0600); err != nil {
		t.Fatal(err)
	}
	if !synaraProfilesReady(options, plan.Providers) {
		t.Fatal("normal Synara runtime defaults invalidated owned registration")
	}
	claudeDesktopSet(settings, "providerInstances", map[string]any{})
	claudeDesktopSet(envelope, "settings", settings)
	updated, _ = json.Marshal(envelope)
	os.WriteFile(settingsPath, updated, 0600)
	if synaraProfilesReady(options, plan.Providers) {
		t.Fatal("missing managed providers stayed ready")
	}
}

func TestSynaraProfilesEnableDriversWithoutImplicitAccounts(t *testing.T) {
	options := synaraProfileTestOptions(t)
	path := filepath.Join(options.DataDir, "userdata", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	// A previous setup may contain disabled driver flags, legacy accounts,
	// or an explicit entry colliding with a reserved default-instance ID.
	old := []byte(`{"revision":7,"migrationVersion":3,"settings":{"providers":{"codex":{"enabled":false,"accounts":[{"id":"extra","label":"Extra legacy","homePath":"old-home"}],"binaryPath":"keep-binary"},"claudeAgent":{"enabled":false}},"providerInstances":{"codex":{"driver":"cursor","enabled":true,"displayName":"Keep label","config":{"enabled":true,"custom":9007199254740993}},"claudeAgent":{"driver":"claudeAgent","enabled":true,"config":{"enabled":true}},"future":{"driver":"futureDriver","enabled":false,"config":{"unknown":true}}}}}`)
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	plan := saveSynaraProfilePlan(t, options)
	data, _ := os.ReadFile(path)
	_, settings, err := synaraSettingsDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	var providers map[string]map[string]any
	json.Unmarshal(settings["providers"], &providers)
	if providers["codex"]["enabled"] != true || providers["claudeAgent"]["enabled"] != true || len(providers["codex"]["accounts"].([]any)) != 0 || providers["codex"]["binaryPath"] != "keep-binary" {
		t.Fatal("picker drivers blocked or legacy accounts were still materialized")
	}
	instances, _ := decodeClaudeDesktopObject(settings["providerInstances"])
	future, _ := decodeClaudeDesktopObject(instances["future"])
	futureConfig, _ := decodeClaudeDesktopObject(future["config"])
	if !bytes.Contains(instances["codex"], []byte(`9007199254740993`)) || !bytes.Contains(instances["codex"], []byte(`"Keep label"`)) || string(futureConfig["unknown"]) != "true" {
		t.Fatal("unowned default-instance or future-instance metadata changed")
	}
	active := map[string]bool{}
	for id, raw := range instances {
		var instance map[string]any
		json.Unmarshal(raw, &instance)
		config, _ := instance["config"].(map[string]any)
		if instance["enabled"] != false && config["enabled"] != false {
			active[id] = true
		}
	}
	if len(active) != 4 || !active[synaraCodexNormalID] || !active[synaraCodexProxyID] || !active[synaraClaudeNormalID] || !active[synaraClaudeProxyID] || !synaraProfilesReady(options, plan.Providers) {
		t.Fatalf("four owned accounts not eligible: %v", active)
	}
}

func TestSynaraReadinessRejectsDriverAndImplicitAccountDrift(t *testing.T) {
	for _, mutation := range []string{"blocked-driver", "legacy-enabled", "legacy-config-enabled", "legacy-missing", "legacy-accounts"} {
		t.Run(mutation, func(t *testing.T) {
			options := synaraProfileTestOptions(t)
			plan := saveSynaraProfilePlan(t, options)
			path := filepath.Join(options.DataDir, "userdata", "settings.json")
			data, _ := os.ReadFile(path)
			var envelope map[string]any
			json.Unmarshal(data, &envelope)
			settings := envelope["settings"].(map[string]any)
			instances := settings["providerInstances"].(map[string]any)
			providers := settings["providers"].(map[string]any)
			switch mutation {
			case "blocked-driver":
				providers["claudeAgent"].(map[string]any)["enabled"] = false
			case "legacy-enabled":
				instances["codex"].(map[string]any)["enabled"] = true
			case "legacy-config-enabled":
				instances["claudeAgent"].(map[string]any)["config"].(map[string]any)["enabled"] = true
			case "legacy-missing":
				delete(instances, "codex")
			case "legacy-accounts":
				providers["codex"].(map[string]any)["accounts"] = []any{map[string]any{"id": "extra"}}
			}
			data, _ = json.Marshal(envelope)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if synaraProfilesReady(options, plan.Providers) {
				t.Fatal("picker/account eligibility drift stayed ready")
			}
		})
	}
}

func TestSynaraProfilesRejectMalformedSettingsWithoutWritingProfiles(t *testing.T) {
	for _, document := range []string{`null`, `[]`, `{"settings":null}`, `{"settings":{"providers":[]}}`, `{"settings":{"providerInstances":[]}}`, `{"settings":{"providerInstances":{"bad id":{"driver":"codex"}}}}`, `{"settings":{"providerInstances":{"future":{"driver":"codex","environment":[{"name":"TOKEN","value":123}]}}}}`, `{"settings":{"providers":{"codex":{"customModels":[{}]}}}}`, `{"settings":{"enableAssistantStreaming":"yes"}}`, `{"settings":{"skills":{"disabled":false}}}`, `{"settings":{"providers":{},"providers":{}}}`, `{"settings":{"providerInstances":{"future":{"driver":"codex","config":{"x":1,"x":2}}}}}`, `{"settings":{"enableAssistantStreaming":null}}`, `{"settings":{"providers":{"codex":{"customModels":[null]}}}}`, `{"settings":{"providers":{"codex":{"binaryPath":null}}}}`, `{"settings":{"providerInstances":{"future":{"driver":"codex","environment":[{"name":"TOKEN","value":null}]}}}}`} {
		t.Run(document, func(t *testing.T) {
			options := synaraProfileTestOptions(t)
			path := filepath.Join(options.DataDir, "userdata", "settings.json")
			os.MkdirAll(filepath.Dir(path), 0700)
			os.WriteFile(path, []byte(document), 0600)
			if _, err := planSynaraProfiles(options); err == nil {
				t.Fatal("malformed Synara settings accepted")
			}
			got, _ := os.ReadFile(path)
			if string(got) != document {
				t.Fatal("malformed input changed")
			}
			if _, err := os.Stat(filepath.Join(options.RootDir, "profiles", "codex-kilo", "config.toml")); !os.IsNotExist(err) {
				t.Fatalf("partial profile written: %v", err)
			}
		})
	}
}

func TestSynaraComposerSeedIsInitialAndCanBeConsumed(t *testing.T) {
	options := synaraProfileTestOptions(t)
	plan := saveSynaraProfilePlan(t, options)
	seedPath := filepath.Join(options.UserDataDir, synaraComposerSeedName)
	seedData, _ := os.ReadFile(seedPath)
	var seed struct {
		Version int               `json:"version"`
		Entries map[string]string `json:"entries"`
	}
	if err := json.Unmarshal(seedData, &seed); err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		Version int `json:"version"`
		State   struct {
			Sticky map[string]map[string]any `json:"stickyModelSelectionByProvider"`
			Active string                    `json:"stickyActiveProvider"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(seed.Entries["synara:composer-drafts:v1"]), &persisted); err != nil {
		t.Fatal(err)
	}
	if seed.Version != 1 || persisted.Version != 6 || persisted.State.Active != synaraCodexProxyID || persisted.State.Sticky[synaraClaudeProxyID]["model"] != options.Library.DefaultModel {
		t.Fatalf("initial Synara UI defaults: %#v", persisted)
	}
	if err := os.Remove(seedPath); err != nil {
		t.Fatal(err)
	}
	if !synaraProfilesReady(options, plan.Providers) {
		t.Fatal("native consumed seed should not invalidate registration")
	}
	options.Library.DefaultModel = options.Library.Models[0].ID
	second, err := planSynaraProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range second.Files {
		if file.path == seedPath {
			t.Fatal("reprepare overwrites existing user's sticky composer selection")
		}
	}
	if _, err := os.Stat(seedPath); !os.IsNotExist(err) {
		t.Fatal("seed restored during planning")
	}
}

func TestSynaraProfilesGuardClaudeReasoningAndFamilyCollisions(t *testing.T) {
	options := synaraProfileTestOptions(t)
	options.ClaudeCaps = claudeCaps("2.1.250")
	if _, err := planSynaraProfiles(options); err == nil || !strings.Contains(err.Error(), "2.1.251") {
		t.Fatalf("old Claude CLI accepted saved reasoning: %v", err)
	}
	options.ClaudeCaps = claudeCaps("2.1.251")
	options.Library.Models = append(options.Library.Models, modelLibraryItem{ID: "anthropic/claude-fable-5-1", ReasoningEffort: "low", ContextWindow: 200000})
	if _, err := planSynaraProfiles(options); err == nil || !strings.Contains(err.Error(), "family/version") {
		t.Fatalf("conflicting native per-model defaults accepted: %v", err)
	}
}

func TestSynaraReadyRejectsTokenDriftAndRoutingChanges(t *testing.T) {
	options := synaraProfileTestOptions(t)
	plan := saveSynaraProfilePlan(t, options)
	secret := filepath.Join(options.DataDir, "userdata", "secrets", t3CodeSecretName(synaraClaudeProxyID, "ANTHROPIC_AUTH_TOKEN"))
	os.WriteFile(secret, []byte("wrong-key"), 0600)
	if synaraProfilesReady(options, plan.Providers) {
		t.Fatal("different runtime token accepted")
	}
	os.WriteFile(secret, []byte(options.LocalKey), 0600)
	path := filepath.Join(options.DataDir, "userdata", "settings.json")
	data, _ := os.ReadFile(path)
	envelope, settings, _ := synaraSettingsDocument(data)
	var actual map[string]any
	json.Unmarshal(settings["providerInstances"], &actual)
	actual[synaraClaudeProxyID].(map[string]any)["config"].(map[string]any)["homePath"] = options.NormalHome
	claudeDesktopSet(settings, "providerInstances", actual)
	claudeDesktopSet(envelope, "settings", settings)
	data, _ = json.Marshal(envelope)
	os.WriteFile(path, data, 0600)
	if synaraProfilesReady(options, plan.Providers) {
		t.Fatal("normal profile substituted for private Claude home")
	}
}

func TestSynaraProfilePlanUsesOnlyPrivatePaths(t *testing.T) {
	options := synaraProfileTestOptions(t)
	options.DataDir = options.NormalHome
	if _, err := planSynaraProfiles(options); err == nil {
		t.Fatal("external data directory accepted")
	}
	options = synaraProfileTestOptions(t)
	options.NormalEnvironment["CODEX_HOME"] = filepath.Join(options.RootDir, "profiles", "codex-kilo")
	if _, err := planSynaraProfiles(options); err == nil {
		t.Fatal("normal/private profile overlap accepted")
	}
}

func TestSynaraNormalSecureStorageIsReferencedAndCannotOverlap(t *testing.T) {
	options := synaraProfileTestOptions(t)
	secure := filepath.Join(filepath.Dir(options.RootDir), "normal-custom-secure-storage")
	options.NormalEnvironment["CLAUDE_SECURESTORAGE_CONFIG_DIR"] = secure
	if err := os.MkdirAll(secure, 0700); err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(secure, "credentials.json")
	if err := os.WriteFile(credential, []byte("normal-unread-login"), 0600); err != nil {
		t.Fatal(err)
	}
	plan := saveSynaraProfilePlan(t, options)
	if plan.Providers[synaraClaudeNormalID].(map[string]any)["config"].(map[string]any)["secureStorageDir"] != secure {
		t.Fatal("normal secure-storage override ignored")
	}
	got, _ := os.ReadFile(credential)
	if string(got) != "normal-unread-login" {
		t.Fatal("normal secure storage changed")
	}
	for _, file := range plan.Files {
		if bytes.Contains(file.new, []byte("normal-unread-login")) {
			t.Fatal("normal secure storage copied")
		}
	}
	for _, value := range []string{"relative", filepath.Join(options.RootDir, "profiles", "claude-kilo"), options.RootDir} {
		options.NormalEnvironment["CLAUDE_SECURESTORAGE_CONFIG_DIR"] = value
		if _, err := planSynaraProfiles(options); err == nil {
			t.Fatalf("unsafe normal secure-storage %s accepted", value)
		}
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(filepath.Dir(options.RootDir), "normal-secure-alias")
		if err := os.Symlink(filepath.Join(options.RootDir, "profiles", "claude-kilo"), link); err != nil {
			t.Fatal(err)
		}
		options.NormalEnvironment["CLAUDE_SECURESTORAGE_CONFIG_DIR"] = link
		if _, err := planSynaraProfiles(options); err == nil {
			t.Fatal("aliased normal secure storage overlaps private profile")
		}
	}
}

func TestSynaraNormalSecureStoragePreservesOriginalNamespace(t *testing.T) {
	options := synaraProfileTestOptions(t)
	literal := filepath.Join(options.NormalHome, "custom-claude") + string(filepath.Separator)
	for _, test := range []struct {
		config string
		secure *string
		want   string
	}{{want: ""}, {config: filepath.Join(options.NormalHome, "custom-claude"), want: filepath.Join(options.NormalHome, "custom-claude")}, {config: filepath.Join(options.NormalHome, "custom-claude"), secure: new(string), want: ""}, {config: literal, want: literal}, {secure: &literal, want: literal}} {
		delete(options.NormalEnvironment, "CLAUDE_CONFIG_DIR")
		delete(options.NormalEnvironment, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
		if test.config != "" {
			options.NormalEnvironment["CLAUDE_CONFIG_DIR"] = test.config
		}
		if test.secure != nil {
			options.NormalEnvironment["CLAUDE_SECURESTORAGE_CONFIG_DIR"] = *test.secure
		}
		got, err := synaraNormalSecureStorage(t3CodeProfileOptions{RootDir: options.RootDir, NormalHome: options.NormalHome, NormalEnvironment: options.NormalEnvironment})
		if err != nil || got != test.want {
			t.Fatalf("normal config=%q secure=%v: got%q %v", test.config, test.secure, got, err)
		}
		plan, err := planSynaraProfiles(options)
		if err != nil {
			t.Fatal(err)
		}
		instance := plan.Providers[synaraClaudeNormalID].(map[string]any)
		if instance["config"].(map[string]any)["secureStorageDir"] != test.want {
			t.Fatal("normal Claude keychain namespace changed during planning")
		}
	}
}

func TestSynaraReadinessUsesPreparedOwnedSubset(t *testing.T) {
	options := synaraProfileTestOptions(t)
	plan := saveSynaraProfilePlan(t, options)
	path := filepath.Join(options.DataDir, "userdata", "settings.json")
	data, _ := os.ReadFile(path)
	envelope, settings, _ := synaraSettingsDocument(data)
	var actual map[string]any
	json.Unmarshal(settings["providerInstances"], &actual)
	actual[synaraCodexProxyID].(map[string]any)["config"].(map[string]any)["futureOption"] = "retained"
	claudeDesktopSet(settings, "providerInstances", actual)
	claudeDesktopSet(envelope, "settings", settings)
	data, _ = json.Marshal(envelope)
	os.WriteFile(path, data, 0600)
	if !synaraProfilesReady(options, plan.Providers) {
		t.Fatal("unowned extra settings rejected")
	}
	if !reflect.DeepEqual(plan.Providers[synaraCodexProxyID].(map[string]any)["config"].(map[string]any)["customModels"].([]any)[0], options.Library.DefaultModel) {
		t.Fatal("prepared identity drift")
	}
}

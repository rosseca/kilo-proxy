package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func t3ProfileTestOptions(t *testing.T) t3CodeProfileOptions {
	t.Helper()
	root := t.TempDir()
	return t3CodeProfileOptions{
		RootDir: filepath.Join(root, "t3-code"), NormalHome: filepath.Join(root, "normal-home"),
		CodexBinary: filepath.Join(root, "codex"), ClaudeBinary: filepath.Join(root, "claude"),
		NormalEnvironment: map[string]string{"USERPROFILE": filepath.Join(root, "normal-home"), "APPDATA": filepath.Join(root, "normal-home", "AppData", "Roaming"), "XDG_CONFIG_HOME": filepath.Join(root, "normal-home", ".config"), "ANTHROPIC_API_KEY": "DO-NOT-COPY"},
		Library: modelLibrary{SchemaVersion: 1, DefaultModel: "openai/gpt-6.1-sol", Models: []modelLibraryItem{
			{ID: "anthropic/claude-fable-5.1", DisplayName: "Claude Fable", ReasoningEffort: "high", ContextWindow: 200000, MaxOutputTokens: 4096},
			{ID: "openai/gpt-6.1-sol", DisplayName: "Sol", ReasoningEffort: "max", ContextWindow: 150000, MaxOutputTokens: 8192},
			{ID: "deepseek/deepseek-v4-pro-0813", ReasoningEffort: "max", ContextWindow: 250000, MaxOutputTokens: 8192},
		}},
		Catalog: []modelInfo{
			{ID: "anthropic/claude-fable-5.1", Name: "Fable", ContextWindow: 200000, ReasoningEfforts: []string{"low", "medium", "high", "xhigh"}},
			{ID: "openai/gpt-6.1-sol", Name: "Sol", ContextWindow: 150000, ReasoningEfforts: []string{"low", "high", "max"}},
			{ID: "deepseek/deepseek-v4-pro-0813", ContextWindow: 250000, ReasoningEfforts: []string{"high", "max"}},
		},
		ClaudeCaps: claudeCaps("2.1.251"), Port: 18877, LocalKey: "t3-test-local-key",
	}
}

func TestT3CodeProfilesAreSeparateAndKeepTokensInEnvironment(t *testing.T) {
	options := t3ProfileTestOptions(t)
	if err := os.MkdirAll(filepath.Join(options.NormalHome, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(options.NormalHome, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	normalFiles := map[string]string{
		filepath.Join(options.NormalHome, ".codex", "config.toml"):    "# normal codex\nmodel_provider = 'openai'\n",
		filepath.Join(options.NormalHome, ".codex", "auth.json"):      "{\"test\":\"normal-codex-login\"}\n",
		filepath.Join(options.NormalHome, ".claude", "settings.json"): "{\"test\":\"normal-claude\"}\n",
		filepath.Join(options.NormalHome, ".claude.json"):             "{\"test\":\"normal-claude-login\"}\n",
	}
	for path, data := range normalFiles {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := prepareT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	for path, expected := range normalFiles {
		actual, err := os.ReadFile(path)
		if err != nil || string(actual) != expected {
			t.Fatalf("normal file changed: %s (%v)", path, err)
		}
	}
	for _, file := range result.Files {
		data, err := os.ReadFile(file.path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(options.LocalKey)) || bytes.Contains(data, []byte("normal-codex-login")) || bytes.Contains(data, []byte("normal-claude-login")) {
			t.Fatalf("profile copied credentials: %s", file.path)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(file.path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("profile mode: %v %v", info, err)
			}
		}
	}
	for _, dir := range []string{result.CodexHome, result.ClaudeHome} {
		if runtime.GOOS != "windows" {
			info, err := os.Stat(dir)
			if err != nil || info.Mode().Perm() != 0700 {
				t.Fatalf("profile directory mode: %v %v", info, err)
			}
		}
	}
	var codex map[string]any
	data, _ := os.ReadFile(filepath.Join(result.CodexHome, "config.toml"))
	if err := toml.Unmarshal(data, &codex); err != nil {
		t.Fatal(err)
	}
	if codex["model_catalog_json"] != filepath.Join(result.CodexHome, "models.json") || codex["model_provider"] != "kilo-local" {
		t.Fatalf("wrong codex routing/catalog: %#v", codex)
	}
	local := codex["model_providers"].(map[string]any)["kilo-local"].(map[string]any)
	if local["base_url"] != "http://127.0.0.1:18877/v1" || local["env_key"] != "KILO_LOCAL_API_KEY" || local["requires_openai_auth"] != false {
		t.Fatalf("wrong proxy provider: %#v", local)
	}
	var claude map[string]any
	data, _ = os.ReadFile(filepath.Join(result.ClaudeHome, "settings.json"))
	if err := json.Unmarshal(data, &claude); err != nil {
		t.Fatal(err)
	}
	env := claude["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:18877" || env["ANTHROPIC_MODEL"] != options.Library.DefaultModel {
		t.Fatalf("wrong claude route/model: %#v", env)
	}
	for _, name := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if _, ok := env[name]; ok {
			t.Fatalf("persisted auth environment %s", name)
		}
	}
	if _, ok := claude["effortLevel"]; ok {
		t.Fatal("global effort would leak into non-Claude models")
	}
	if claude["autoCompactWindow"] != float64(150000) {
		t.Fatalf("wrong common compaction window: %#v", claude)
	}
	assertT3ProfileProviders(t, options, result)
}

func assertT3ProfileProviders(t *testing.T, options t3CodeProfileOptions, result t3CodeProfiles) {
	t.Helper()
	if len(result.Providers) != 4 {
		t.Fatalf("wrong provider count: %d", len(result.Providers))
	}
	for _, id := range []string{t3CodeCodexNormalID, t3CodeCodexProxyID, t3CodeClaudeNormalID, t3CodeClaudeProxyID} {
		instance := result.Providers[id].(map[string]any)
		config := instance["config"].(map[string]any)
		if strings.Contains(id, "codex") {
			if instance["driver"] != "codex" || config["setupMode"] != "existing" || config["shadowHomePath"] != "" {
				t.Fatalf("bad Codex envelope: %#v", instance)
			}
		} else if instance["driver"] != "claudeAgent" {
			t.Fatalf("bad Claude driver: %#v", instance)
		}
		isProxy := strings.HasSuffix(id, "proxy")
		if isProxy {
			label := "Kilo Proxy · Claude"
			if id == t3CodeCodexProxyID {
				label = "Kilo Proxy · Codex"
			}
			if instance["displayName"] != label || instance["accentColor"] != "#327653" {
				t.Fatalf("proxy badge identity missing for %s: %#v", id, instance)
			}
		} else if _, exists := instance["accentColor"]; exists {
			t.Fatalf("normal agent acquired a Kilo badge: %s", id)
		}
		secrets := 0
		baseURL := ""
		for _, variable := range instance["environment"].([]map[string]any) {
			if variable["name"] == "ANTHROPIC_API_KEY" {
				t.Fatal("copied unapproved normal auth variable")
			}
			if variable["name"] == "HOME" && variable["value"] != options.NormalHome {
				t.Fatal("provider inherited private UI home")
			}
			if variable["name"] == "ANTHROPIC_BASE_URL" {
				baseURL, _ = variable["value"].(string)
			}
			if variable["sensitive"] == true {
				secrets++
				if !isProxy || variable["value"] != options.LocalKey {
					t.Fatalf("wrong secret scope: %s %#v", id, variable)
				}
			}
		}
		if isProxy && secrets != 1 || !isProxy && secrets != 0 {
			t.Fatalf("wrong secret count for %s: %d", id, secrets)
		}
		if id == t3CodeClaudeProxyID && baseURL != "http://127.0.0.1:18877" || id != t3CodeClaudeProxyID && baseURL != "" {
			t.Fatalf("wrong direct Claude routing scope for %s: %q", id, baseURL)
		}
		if !isProxy && !strings.HasPrefix(config["homePath"].(string), options.NormalHome+string(filepath.Separator)) {
			t.Fatalf("normal home redirected: %#v", config)
		}
	}
}

func TestT3CodeProfilesModelIDsAndReasoningCapabilities(t *testing.T) {
	options := t3ProfileTestOptions(t)
	result, err := prepareT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(result.CodexHome, "models.json"))
	var catalog struct {
		Models []struct {
			Slug    string `json:"slug"`
			Default string `json:"default_reasoning_level"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Models[0].Slug != options.Library.DefaultModel || catalog.Models[0].Default != "max" {
		t.Fatalf("wrong default: %#v", catalog.Models)
	}
	for _, id := range []string{t3CodeCodexProxyID, t3CodeClaudeProxyID} {
		models := result.Providers[id].(map[string]any)["config"].(map[string]any)["customModels"].([]map[string]any)
		if len(models) != len(options.Library.Models) {
			t.Fatal("models dropped")
		}
		for i, model := range models {
			if model["slug"] != options.Library.Models[i].ID {
				t.Fatalf("model ID rewritten: %#v", model)
			}
			descriptors := model["capabilities"].(map[string]any)["optionDescriptors"].([]map[string]any)
			if id == t3CodeClaudeProxyID && i > 0 {
				if len(descriptors) != 0 {
					t.Fatalf("invented Claude effort for %s: %#v", model["slug"], descriptors)
				}
				continue
			}
			if len(descriptors) != 1 {
				t.Fatalf("missing reasoning: %#v", model)
			}
			expectedID := "reasoningEffort"
			if id == t3CodeClaudeProxyID {
				expectedID = "effort"
			}
			if descriptors[0]["id"] != expectedID {
				t.Fatalf("wrong reasoning adapter key: %#v", descriptors)
			}
			if id == t3CodeCodexProxyID && i == 1 && descriptors[0]["currentValue"] != "max" {
				t.Fatal("Codex max lost")
			}
			if id == t3CodeClaudeProxyID && descriptors[0]["currentValue"] != "high" {
				t.Fatal("Claude high lost")
			}
		}
	}
	options.ClaudeCaps = claudeCapabilities{}
	result, err = planT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	models := result.Providers[t3CodeClaudeProxyID].(map[string]any)["config"].(map[string]any)["customModels"].([]map[string]any)
	optionsList := models[0]["capabilities"].(map[string]any)["optionDescriptors"].([]map[string]any)[0]["options"].([]map[string]any)
	for _, item := range optionsList {
		if item["id"] == "xhigh" {
			t.Fatal("exposed xhigh to unverified older CLI")
		}
	}
}

func TestT3CodeNightlyProfilesApplyPrivateClaudeDefaultsWithoutGlobalEffort(t *testing.T) {
	options := t3ProfileTestOptions(t)
	options.Version = t3CodeNightlyVersion
	options.Library.Models = []modelLibraryItem{
		{ID: "anthropic/claude-opus-4-6", ReasoningEffort: "high", ContextWindow: 200000, MaxOutputTokens: 4096},
		{ID: "anthropic/claude-sonnet-4-6", ReasoningEffort: "low", ContextWindow: 200000, MaxOutputTokens: 4096},
		{ID: "anthropic/claude-fable-5.1", ContextWindow: 200000, MaxOutputTokens: 4096},
		{ID: "anthropic/claude-sonnet-5", ReasoningEffort: "none", ContextWindow: 200000, MaxOutputTokens: 4096},
		{ID: "openai/gpt-6.1-sol", ReasoningEffort: "max", ContextWindow: 200000, MaxOutputTokens: 4096},
	}
	options.Catalog = nil
	for _, item := range options.Library.Models {
		options.Catalog = append(options.Catalog, modelInfo{ID: item.ID, ContextWindow: 200000, ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max"}})
	}
	claudePath := filepath.Join(options.RootDir, "profiles", "claude-kilo", "settings.json")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0700); err != nil {
		t.Fatal(err)
	}
	old := []byte(`{"effortLevel":"high","modelOverrides":{"old-alias":"old-model"},"modelSettings":{"claude-opus-4-6":{"effortLevel":"low","custom":"keep"},"claude-fable-5-1":{"effortLevel":"high"},"claude-sonnet-5":{"effortLevel":"high"}}}`)
	if err := os.WriteFile(claudePath, old, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := prepareT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(claudePath)
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"effortLevel", "modelOverrides", "modelPicker"} {
		if _, present := settings[key]; present {
			t.Fatal("nightly created global effort or family aliases", key)
		}
	}
	modelSettings := settings["modelSettings"].(map[string]any)
	if len(modelSettings) != 2 || modelSettings["claude-opus-4-6"].(map[string]any)["effortLevel"] != "high" || modelSettings["claude-sonnet-4-6"].(map[string]any)["effortLevel"] != "low" {
		t.Fatal("per-model defaults leaked into automatic/none/unrelated models", modelSettings)
	}
	if modelSettings["claude-opus-4-6"].(map[string]any)["custom"] != "keep" {
		t.Fatal("unmanaged per-model settings were lost")
	}
	var snapshot claudeSelection
	data, _ = os.ReadFile(filepath.Join(result.ClaudeHome, "kilo-models.json"))
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	for i, expected := range []string{"high", "low", "", "", ""} {
		if snapshot.Models[i].ID != options.Library.Models[i].ID || snapshot.Models[i].Effort != expected {
			t.Fatal("snapshot rewrote model identity or a saved default", snapshot.Models[i])
		}
	}
	claudeModels := result.Providers[t3CodeClaudeProxyID].(map[string]any)["config"].(map[string]any)["customModels"].([]map[string]any)
	for _, model := range claudeModels {
		if len(model["capabilities"].(map[string]any)["optionDescriptors"].([]map[string]any)) != 0 {
			t.Fatal("nightly exposed an effort selector ignored by its driver", model)
		}
	}
	codexModels := result.Providers[t3CodeCodexProxyID].(map[string]any)["config"].(map[string]any)["customModels"].([]map[string]any)
	if len(codexModels[0]["capabilities"].(map[string]any)["optionDescriptors"].([]map[string]any)) == 0 {
		t.Fatal("nightly removed the working Codex effort selector")
	}
	options.Version = t3CodeSupportedVersion
	result, err = prepareT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(claudePath)
	settings = nil
	if json.Unmarshal(data, &settings) != nil || settings["modelSettings"] != nil || settings["effortLevel"] != nil {
		t.Fatal("stable retained nightly CLI defaults", string(data))
	}
	claudeModels = result.Providers[t3CodeClaudeProxyID].(map[string]any)["config"].(map[string]any)["customModels"].([]map[string]any)
	if len(claudeModels[0]["capabilities"].(map[string]any)["optionDescriptors"].([]map[string]any)) != 1 {
		t.Fatal("stable lost its working Claude effort selector")
	}
}

func TestT3CodeNightlyProfilesRequireVerifiedCLIForExplicitClaudeDefaults(t *testing.T) {
	options := t3ProfileTestOptions(t)
	previous, err := prepareT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, file := range previous.Files {
		before[file.path], err = os.ReadFile(file.path)
		if err != nil {
			t.Fatal(err)
		}
	}
	options.Version = t3CodeNightlyVersion
	for _, caps := range []claudeCapabilities{claudeCaps("2.1.250"), {}} {
		options.ClaudeCaps = caps
		if _, err := prepareT3CodeProfiles(options); err == nil || !strings.Contains(err.Error(), "Claude Code 2.1.251") {
			t.Fatal("older or unverified CLI silently lost saved effort", caps, err)
		}
		for path, expected := range before {
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, expected) {
				t.Fatal("failed CLI compatibility check modified a prepared profile", path, err)
			}
		}
	}
	for _, automatic := range []string{"", "none"} {
		options.Library.Models[0].ReasoningEffort = automatic
		if _, err := prepareT3CodeProfiles(options); err != nil {
			t.Fatal("automatic/unsupported-none model required native effort support", automatic, err)
		}
	}
}

func TestT3CodeNightlyProfilesRejectConflictingCanonicalClaudeDefaults(t *testing.T) {
	for _, second := range []string{"low", ""} {
		t.Run(second, func(t *testing.T) {
			options := t3ProfileTestOptions(t)
			options.Version = t3CodeNightlyVersion
			options.Library.DefaultModel = "anthropic/claude-opus-4.6"
			options.Library.Models = []modelLibraryItem{
				{ID: "anthropic/claude-opus-4.6", ReasoningEffort: "high", ContextWindow: 200000, MaxOutputTokens: 4096},
				{ID: "anthropic/claude-opus-4-6-20260813", ReasoningEffort: second, ContextWindow: 200000, MaxOutputTokens: 4096},
			}
			options.Catalog = []modelInfo{
				{ID: options.Library.Models[0].ID, ReasoningEfforts: []string{"low", "high"}},
				{ID: options.Library.Models[1].ID, ReasoningEfforts: []string{"low", "high"}},
			}
			if _, err := prepareT3CodeProfiles(options); err == nil || !strings.Contains(err.Error(), "one gateway ID per Claude family") {
				t.Fatal("canonical key conflict silently changed a model's effort", second, err)
			}
			for _, relative := range []string{"profiles/codex-kilo/config.toml", "profiles/codex-kilo/models.json", "profiles/claude-kilo/settings.json", "profiles/claude-kilo/kilo-models.json"} {
				if _, err := os.Stat(filepath.Join(options.RootDir, filepath.FromSlash(relative))); !os.IsNotExist(err) {
					t.Fatal("conflicting defaults wrote provider files", relative, err)
				}
			}
			options.Library.Models[1].ReasoningEffort = "high"
			if _, err := prepareT3CodeProfiles(options); err != nil {
				t.Fatal("identical canonical defaults were unnecessarily rejected", err)
			}
			options.Version = t3CodeSupportedVersion
			options.Library.Models[1].ReasoningEffort = second
			if _, err := prepareT3CodeProfiles(options); err != nil {
				t.Fatal("stable descriptor compatibility changed", err)
			}
		})
	}
}

func TestT3CodeNightlyProfilesRequireQualifiedClaudeIDsForSavedDefaults(t *testing.T) {
	options := t3ProfileTestOptions(t)
	options.Version = t3CodeNightlyVersion
	options.Library.DefaultModel = "claude-opus-4-6"
	options.Library.Models = []modelLibraryItem{{ID: options.Library.DefaultModel, ReasoningEffort: "low", ContextWindow: 200000, MaxOutputTokens: 4096}}
	options.Catalog = []modelInfo{{ID: options.Library.DefaultModel, ReasoningEfforts: []string{"low", "high"}}}
	if _, err := prepareT3CodeProfiles(options); err == nil || !strings.Contains(err.Error(), "provider-qualified Claude model IDs") {
		t.Fatal("nightly silently accepted a builtin effort that overrides Models", err)
	}
	claudePath := filepath.Join(options.RootDir, "profiles", "claude-kilo", "settings.json")
	if _, err := os.Stat(claudePath); !os.IsNotExist(err) {
		t.Fatal("failed builtin effort validation wrote the Claude profile", err)
	}
	options.Library.DefaultModel = "anthropic/claude-opus-4-6"
	options.Library.Models[0].ID = options.Library.DefaultModel
	options.Catalog[0].ID = options.Library.DefaultModel
	if _, err := prepareT3CodeProfiles(options); err != nil {
		t.Fatal("qualified gateway ID was rejected", err)
	}
	data, _ := os.ReadFile(claudePath)
	var settings struct {
		Model         string `json:"model"`
		ModelSettings map[string]struct {
			Effort string `json:"effortLevel"`
		} `json:"modelSettings"`
	}
	if json.Unmarshal(data, &settings) != nil || settings.Model != options.Library.DefaultModel || settings.ModelSettings["claude-opus-4-6"].Effort != "low" {
		t.Fatal("qualified model changed identity or lost its prepared effort", string(data))
	}
	options.Library.DefaultModel = "claude-opus-4-6"
	options.Library.Models[0].ID = options.Library.DefaultModel
	options.Catalog[0].ID = options.Library.DefaultModel
	options.Library.Models[0].ReasoningEffort = ""
	if _, err := prepareT3CodeProfiles(options); err != nil {
		t.Fatal("builtin automatic default was unnecessarily rejected", err)
	}
	options.Version = t3CodeSupportedVersion
	options.Library.Models[0].ReasoningEffort = "low"
	if _, err := prepareT3CodeProfiles(options); err != nil {
		t.Fatal("stable builtin descriptor compatibility changed", err)
	}
}

func TestT3CodeProfilesRestoreDefaultAndCustomProviderHomes(t *testing.T) {
	options := t3ProfileTestOptions(t)
	options.NormalEnvironment = nil
	result, err := planT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"HOME": options.NormalHome}
	if runtime.GOOS == "windows" {
		expected["USERPROFILE"] = options.NormalHome
		expected["APPDATA"] = filepath.Join(options.NormalHome, "AppData", "Roaming")
		expected["LOCALAPPDATA"] = filepath.Join(options.NormalHome, "AppData", "Local")
	} else {
		expected["XDG_CONFIG_HOME"] = filepath.Join(options.NormalHome, ".config")
		expected["XDG_DATA_HOME"] = filepath.Join(options.NormalHome, ".local", "share")
		expected["XDG_CACHE_HOME"] = filepath.Join(options.NormalHome, ".cache")
		expected["XDG_STATE_HOME"] = filepath.Join(options.NormalHome, ".local", "state")
	}
	check := func(providers map[string]any, expected map[string]string) {
		t.Helper()
		for id, raw := range providers {
			env := map[string]string{}
			for _, variable := range raw.(map[string]any)["environment"].([]map[string]any) {
				env[variable["name"].(string)] = variable["value"].(string)
			}
			for name, value := range expected {
				if env[name] != value {
					t.Fatalf("%s inherited a T3 private UI directory for %s: %q", id, name, env[name])
				}
			}
			if _, provided := expected["XDG_RUNTIME_DIR"]; !provided {
				if _, ok := env["XDG_RUNTIME_DIR"]; ok {
					t.Fatalf("%s invented a session runtime directory", id)
				}
			}
		}
	}
	check(result.Providers, expected)
	options.NormalEnvironment = map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(options.NormalHome, "custom-config"),
		"APPDATA":         filepath.Join(options.NormalHome, "custom-roaming"),
		"XDG_RUNTIME_DIR": filepath.Join(t.TempDir(), "runtime"),
		"HOME":            filepath.Join(options.RootDir, "ui-home"),
	}
	result, err = planT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range options.NormalEnvironment {
		if name != "HOME" {
			expected[name] = value
		}
	}
	check(result.Providers, expected)
}

func TestT3CodeProfilesUseNormalCustomHomesWithoutCreatingThem(t *testing.T) {
	options := t3ProfileTestOptions(t)
	codexHome := filepath.Join(t.TempDir(), "custom-codex")
	claudeHome := filepath.Join(t.TempDir(), "custom-claude")
	options.NormalEnvironment["CODEX_HOME"] = codexHome
	options.NormalEnvironment["CLAUDE_CONFIG_DIR"] = claudeHome
	result, err := planT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	for id, path := range map[string]string{t3CodeCodexNormalID: codexHome, t3CodeClaudeNormalID: claudeHome} {
		provider := result.Providers[id].(map[string]any)
		if provider["config"].(map[string]any)["homePath"] != path {
			t.Fatalf("normal custom provider home was redirected: %s", id)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("normal custom home was created: %s (%v)", path, err)
		}
	}
	for _, raw := range result.Providers {
		for _, variable := range raw.(map[string]any)["environment"].([]map[string]any) {
			if variable["name"] == "CODEX_HOME" || variable["name"] == "CLAUDE_CONFIG_DIR" {
				t.Fatal("normal custom home leaked into the Kilo provider environment")
			}
		}
	}
}

func TestT3CodeProfilesRejectInvalidOrOverlappingNormalCustomHomes(t *testing.T) {
	for _, name := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		for _, kind := range []string{"relative", "newline", "private", "private-child", "private-parent"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				options := t3ProfileTestOptions(t)
				path := "relative-home"
				private := filepath.Join(options.RootDir, "profiles", "codex-kilo")
				switch kind {
				case "newline":
					path = filepath.Join(options.NormalHome, "invalid\nhome")
				case "private":
					path = private
				case "private-child":
					path = filepath.Join(private, "normal")
				case "private-parent":
					path = options.RootDir
				}
				options.NormalEnvironment[name] = path
				if _, err := planT3CodeProfiles(options); err == nil {
					t.Fatal("unsafe normal custom home was accepted")
				}
				if _, err := os.Stat(options.RootDir); !os.IsNotExist(err) {
					t.Fatalf("invalid homes changed the private T3 directory: %v", err)
				}
			})
		}
	}
}

func TestT3CodeProfilesRejectNormalHomeAliasToPrivateProfile(t *testing.T) {
	options := t3ProfileTestOptions(t)
	private := filepath.Join(options.RootDir, "profiles", "codex-kilo")
	if err := os.MkdirAll(private, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "normal-home-alias")
	if err := os.Symlink(private, alias); err != nil {
		t.Skip(err)
	}
	options.NormalEnvironment["CODEX_HOME"] = alias
	if _, err := planT3CodeProfiles(options); err == nil {
		t.Fatal("normal alias accepted the private Kilo profile")
	}
	entries, err := os.ReadDir(private)
	if err != nil || len(entries) != 0 {
		t.Fatalf("overlapping setup wrote provider files: %v %v", entries, err)
	}
}

func TestT3CodeProfilesPlanFailsWithoutChangingPreparedProfiles(t *testing.T) {
	options := t3ProfileTestOptions(t)
	result, err := prepareT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, file := range result.Files {
		before[file.path], err = os.ReadFile(file.path)
		if err != nil {
			t.Fatal(err)
		}
	}
	claudePath := filepath.Join(result.ClaudeHome, "settings.json")
	broken := []byte("{\"env\":false}\n")
	if err := os.WriteFile(claudePath, broken, 0600); err != nil {
		t.Fatal(err)
	}
	before[claudePath] = broken
	options.Library.Models[1].ReasoningEffort = "high"
	if _, err := prepareT3CodeProfiles(options); err == nil {
		t.Fatal("accepted malformed Claude profile")
	}
	for path, original := range before {
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, original) {
			t.Fatalf("partial preparation changed %s: %v", path, err)
		}
		if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
			t.Fatalf("wrote backup before validating whole setup: %s", path)
		}
	}
}

func TestT3CodeProfilesRollbackAllEarlierFilesOnSaveFailure(t *testing.T) {
	options := t3ProfileTestOptions(t)
	first, err := prepareT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, file := range first.Files {
		before[file.path], err = os.ReadFile(file.path)
		if err != nil {
			t.Fatal(err)
		}
	}
	options.Library.DefaultModel = options.Library.Models[0].ID
	result, err := planT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	last := result.Files[len(result.Files)-1].path
	if err := os.Rename(last, last+".test-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(last, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := saveEditorFiles(result.Files); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	if err := os.Remove(last); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(last+".test-original", last); err != nil {
		t.Fatal(err)
	}
	for path, original := range before {
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, original) {
			t.Fatalf("rollback did not restore %s: %v", path, err)
		}
	}
}

func TestT3CodeProfilesNoOpAndPreserveUnmanagedSettings(t *testing.T) {
	options := t3ProfileTestOptions(t)
	result, err := prepareT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(result.CodexHome, "config.toml")
	data, _ := os.ReadFile(path)
	data = append([]byte("# keep me\ncustom_number = 9007199254740993\n"), data...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	pathClaude := filepath.Join(result.ClaudeHome, "settings.json")
	dataClaude, _ := os.ReadFile(pathClaude)
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(dataClaude, &settings); err != nil {
		t.Fatal(err)
	}
	settings["custom_number"] = json.RawMessage("9007199254740993")
	settings["permissions"] = json.RawMessage(`{"allow":["Read(/my-project/**)"]}`)
	dataClaude, _ = json.MarshalIndent(settings, "", "  ")
	dataClaude = append(dataClaude, '\n')
	if err := os.WriteFile(pathClaude, dataClaude, 0600); err != nil {
		t.Fatal(err)
	}
	result, err = prepareT3CodeProfiles(options)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, data) {
		t.Fatal("lost Codex comments/unmanaged data")
	}
	actual, _ = os.ReadFile(pathClaude)
	if !bytes.Equal(actual, dataClaude) {
		t.Fatal("lost Claude unmanaged data or rewrote no-op")
	}
	for _, file := range result.Files {
		if file.changed {
			t.Fatalf("semantic no-op rewrote %s", file.path)
		}
	}
	if !reflect.DeepEqual(result.Providers[t3CodeClaudeProxyID].(map[string]any)["config"].(map[string]any)["homePath"], result.ClaudeHome) {
		t.Fatal("wrong Claude private home")
	}
}

func TestT3CodeProfilesRejectSymlinkedManagedDirectories(t *testing.T) {
	options := t3ProfileTestOptions(t)
	if err := os.Mkdir(options.RootDir, 0700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(options.RootDir, "profiles")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks require Windows developer mode")
		}
		t.Fatal(err)
	}
	if _, err := prepareT3CodeProfiles(options); err == nil {
		t.Fatal("followed managed profile ancestor link")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatal("wrote through profile link")
	}
}

func TestT3CodeProfilesRejectRootInsideNormalProviders(t *testing.T) {
	for _, provider := range []string{".codex", ".claude"} {
		t.Run(provider, func(t *testing.T) {
			options := t3ProfileTestOptions(t)
			options.RootDir = filepath.Join(options.NormalHome, provider, "t3-code")
			if _, err := planT3CodeProfiles(options); err == nil {
				t.Fatal("accepted writes under a normal provider home")
			}
			if _, err := os.Stat(options.NormalHome); !os.IsNotExist(err) {
				t.Fatal("created or touched normal provider home")
			}
		})
	}
}

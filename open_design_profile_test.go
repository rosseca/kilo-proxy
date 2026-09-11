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
)

func TestOpenDesignCodexProfileUsesSharedModelsAndPrivateSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	personal := map[string][]byte{
		".codex/config.toml":          []byte("model = 'personal'\n"),
		".codex-kilo-cli/config.toml": []byte("model = 'terminal'\n"),
		".claude/settings.json":       []byte(`{"model":"personal"}`),
		".claude-kilo/settings.json":  []byte(`{"model":"terminal"}`),
	}
	for name, data := range personal {
		path := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(home, "kilo", "open-design", "profiles", "codex-cli")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("# Preserve private custom settings\nnotify = ['private-notifier']\n[mcp_servers.docs]\ncommand = 'private-docs-server'\n")
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), original, 0600); err != nil {
		t.Fatal(err)
	}
	library := testModelLibrary()
	metadata := []modelInfo{{ID: "vendor/one", Name: "Published name", InputModalities: []string{"text", "image"}}}
	images := imageGenerationSettings{Enabled: true, Model: "vendor/image"}
	if err := prepareOpenDesignEngineProfile(dir, "codex-cli", library, metadata, claudeCapabilities{}, 8877, "synthetic-local-one", images); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	config := imagesTOML(t, data)
	provider := config["model_providers"].(map[string]any)["kilo-local"].(map[string]any)
	servers := config["mcp_servers"].(map[string]any)
	if config["model"] != library.DefaultModel || config["model_reasoning_effort"] != "high" || provider["env_key"] != "KILO_LOCAL_API_KEY" || provider["wire_api"] != "responses" || provider["experimental_bearer_token"] != nil {
		t.Fatal("private Codex profile lost the shared default or inherited auth contract")
	}
	if !managedCodexImages(servers[codexImagesServer]) || servers["docs"].(map[string]any)["command"] != "private-docs-server" || !bytes.Contains(data, []byte("# Preserve private custom settings")) {
		t.Fatal("image preparation changed unrelated private settings")
	}
	models, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := validateCatalog(models)
	if err != nil || first != library.DefaultModel {
		t.Fatal("shared default was not first in the generated catalog")
	}
	for _, want := range []string{"Daily coding", "Fast ✨", `"image"`, `"high"`, `"low"`} {
		if !bytes.Contains(models, []byte(want)) {
			t.Fatalf("generated model catalog lost %s", want)
		}
	}
	if err := prepareOpenDesignEngineProfile(dir, "codex-cli", library, metadata, claudeCapabilities{}, 8899, "synthetic-local-two", images); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "config.toml"))
	if !bytes.Contains(data, []byte("http://127.0.0.1:8899/v1")) || !bytes.Contains(data, []byte("http://127.0.0.1:8899/mcp/images")) || bytes.Contains(data, []byte("synthetic-local")) {
		t.Fatal("port rotation failed or a local key was embedded in Codex config")
	}
	claudeDir := filepath.Join(home, "kilo", "open-design", "profiles", "claude")
	if err := prepareOpenDesignEngineProfile(claudeDir, "claude", library, nil, claudeCapabilities{}, 8877, "synthetic-local-two", imageGenerationSettings{}); err != nil {
		t.Fatal(err)
	}
	for name, expected := range personal {
		got, err := os.ReadFile(filepath.Join(home, name))
		if err != nil || !bytes.Equal(got, expected) {
			t.Fatalf("ordinary profile changed: %s", name)
		}
	}
}

func TestOpenDesignClaudeProfilePreservesAliasesAndRotatesAuth(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude")
	library := modelLibrary{SchemaVersion: 1, DefaultModel: "anthropic/claude-fable-5.1", Models: []modelLibraryItem{
		{ID: "anthropic/claude-fable-5.1", DisplayName: "Shared default", ReasoningEffort: "high"},
		{ID: "anthropic/claude-opus-4.6", DisplayName: "Careful", ReasoningEffort: "medium"},
		{ID: "vendor/other", DisplayName: "Other", ReasoningEffort: "high"},
	}}
	previous := claudeFixture()
	if _, err := saveClaudeProfile(dir, previous, claudeCaps("2.1.263"), 8877, "old-local-key"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	data, _ := os.ReadFile(path)
	var custom map[string]json.RawMessage
	if err := json.Unmarshal(data, &custom); err != nil {
		t.Fatal(err)
	}
	custom["permissions"] = json.RawMessage(`{"deny":["Read(.env)"]}`)
	custom["custom"] = json.RawMessage(`{"large":9007199254740993}`)
	data, _ = json.Marshal(custom)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareOpenDesignEngineProfile(dir, "claude", library, nil, claudeCaps("2.1.263"), 8899, "rotated-local-key", imageGenerationSettings{}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	var settings struct {
		Model          string            `json:"model"`
		Env            map[string]string `json:"env"`
		ModelOverrides map[string]string `json:"modelOverrides"`
	}
	if err := json.Unmarshal(got, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Model != "claude-fable-5-1" || settings.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8899" || settings.Env["ANTHROPIC_AUTH_TOKEN"] != "rotated-local-key" || settings.Env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "claude-opus-4-6" || settings.ModelOverrides["claude-fable-5-1"] != library.DefaultModel {
		t.Fatal("Claude profile lost routing, native model mapping, or retained aliases")
	}
	if !bytes.Contains(got, []byte("Read(.env)")) || !bytes.Contains(got, []byte("9007199254740993")) || bytes.Contains(got, []byte("old-local-key")) {
		t.Fatal("rotation changed custom settings or kept stale auth")
	}
	choices, _ := os.ReadFile(filepath.Join(dir, "kilo-models.json"))
	var selection claudeSelection
	if err := json.Unmarshal(choices, &selection); err != nil {
		t.Fatal(err)
	}
	if selection.Aliases["haiku"] != "anthropic/claude-opus-4.6" || selection.Models[0].Effort != "high" || selection.Models[2].Effort != "" {
		t.Fatal("shared choices or compatible reasoning were not preserved")
	}
	backup, _ := os.ReadFile(path + ".bak")
	if !bytes.Equal(backup, data) {
		t.Fatal("private settings were not backed up")
	}
}

func TestOpenDesignProfileRejectsInvalidInputBeforeWrites(t *testing.T) {
	for _, engine := range []string{"", "codex", "unsupported", "../claude"} {
		dir := filepath.Join(t.TempDir(), "private")
		if err := prepareOpenDesignEngineProfile(dir, engine, testModelLibrary(), nil, claudeCapabilities{}, 8877, "local", imageGenerationSettings{}); err == nil {
			t.Fatal("unsupported engine accepted", engine)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("invalid engine created a profile")
		}
	}
	for _, library := range []modelLibrary{emptyModelLibrary(), {SchemaVersion: 1, DefaultModel: "missing", Models: []modelLibraryItem{{ID: "vendor/one"}}}} {
		dir := filepath.Join(t.TempDir(), "private")
		if err := prepareOpenDesignEngineProfile(dir, "codex-cli", library, nil, claudeCapabilities{}, 8877, "local", imageGenerationSettings{}); err == nil {
			t.Fatal("invalid shared selection accepted")
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("invalid selection created a profile")
		}
	}
	for _, engine := range []string{"codex-cli", "claude", "opencode"} {
		dir := t.TempDir()
		name := map[string]string{"codex-cli": "config.toml", "claude": "settings.json", "opencode": "opencode.json"}[engine]
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("{invalid"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := prepareOpenDesignEngineProfile(dir, engine, testModelLibrary(), nil, claudeCapabilities{}, 8877, "local", imageGenerationSettings{}); err == nil {
			t.Fatal("malformed existing profile accepted")
		}
		got, _ := os.ReadFile(path)
		entries, _ := os.ReadDir(dir)
		if string(got) != "{invalid" || len(entries) != 1 {
			t.Fatal("malformed profile was changed or partially saved")
		}
	}
}

func TestOpenDesignEnginePreferencesUseAllowedNames(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile with spaces")
	binary := filepath.Join(t.TempDir(), "local cli")
	got, err := openDesignEnginePreferences("codex-cli", dir, binary, 8877, "local-one")
	if err != nil || !reflect.DeepEqual(got, map[string]string{"CODEX_HOME": dir, "CODEX_BIN": binary}) {
		t.Fatal("Codex preferences must use its isolated profile and inherited process key")
	}
	got, err = openDesignEnginePreferences("claude", dir, binary, 8899, "local-two")
	if err != nil || got["CLAUDE_CONFIG_DIR"] != dir || got["CLAUDE_BIN"] != binary || got["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8899" || got["ANTHROPIC_AUTH_TOKEN"] != "local-two" || len(got) != 4 {
		t.Fatal("Claude preferences lost its private profile or current local auth")
	}
	got, err = openDesignEnginePreferences("opencode", dir, binary, 8899, "local-two")
	shim := "kilo-proxy-opencode"
	if runtime.GOOS == "windows" {
		shim += ".exe"
	}
	if err != nil || !reflect.DeepEqual(got, map[string]string{"OPENCODE_BIN": filepath.Join(dir, shim)}) {
		t.Fatal("OpenCode preferences must point to the private native shim")
	}
	for _, tc := range []struct {
		engine, dir, binary string
		port                int
		key                 string
	}{
		{"codex", dir, binary, 8877, "local"},
		{"claude", "relative", binary, 8877, "local"},
		{"claude", dir, "claude", 8877, "local"},
		{"claude", dir, binary, 80, "local"},
		{"claude", dir, binary, 8877, ""},
		{"claude", dir, binary, 8877, "private\ninvalid"},
	} {
		_, err := openDesignEnginePreferences(tc.engine, tc.dir, tc.binary, tc.port, tc.key)
		if err == nil || strings.Contains(err.Error(), "private\ninvalid") {
			t.Fatal("invalid preference accepted or key leaked in error")
		}
	}
}

func TestOpenDesignOpenCodeProfileKeepsCustomSettingsAndSharedIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	old := []byte("{\n // Keep private custom settings\n \"permission\":{\"bash\":\"ask\"},\n \"custom\":9007199254740993,\n \"provider\":{\"other\":{\"name\":\"Preserved\"}}\n}\n")
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	library := testModelLibrary()
	if err := prepareOpenDesignEngineProfile(dir, "opencode", library, []modelInfo{{ID: "vendor/two", ContextWindow: 64000}}, claudeCapabilities{}, 8877, "old-local", imageGenerationSettings{}); err != nil {
		t.Fatal(err)
	}
	if err := prepareOpenDesignEngineProfile(dir, "opencode", library, []modelInfo{{ID: "vendor/two", ContextWindow: 64000}}, claudeCapabilities{}, 8899, "new-local", imageGenerationSettings{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Keep private custom settings", "9007199254740993", "Preserved", "kilo-local/vendor/one", "Daily coding", "http://127.0.0.1:8899/v1", "new-local"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("OpenCode settings lost %s", want)
		}
	}
	if bytes.Contains(data, []byte("old-local")) {
		t.Fatal("OpenCode retained stale local auth")
	}
	choices, _ := os.ReadFile(filepath.Join(dir, "kilo-models.json"))
	var selection editorSelection
	if json.Unmarshal(choices, &selection) != nil || validateEditorSelection(selection) != nil || selection.Initial != library.DefaultModel || selection.Models[0].Context != 64000 || selection.Models[1].Output != 32000 {
		t.Fatal("OpenCode selection lost shared identity or valid model limits")
	}
}

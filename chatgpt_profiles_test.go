package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// Same slug, different connection: neither profile generation nor aliasing may
// collapse the Kilo and subscription IDs into one model.
func chatGPTProfileLibrary() modelLibrary {
	return modelLibrary{SchemaVersion: 1, DefaultModel: "chatgpt/gpt-test", Models: []modelLibraryItem{
		{ID: "openai/gpt-test", DisplayName: "GPT on Kilo", ContextWindow: 128000, MaxOutputTokens: 8192},
		{ID: "chatgpt/gpt-test", DisplayName: "GPT on ChatGPT", ContextWindow: 128000, MaxOutputTokens: 8192},
		{ID: "anthropic/claude-sonnet-4.6", DisplayName: "Claude on Kilo", ContextWindow: 128000, MaxOutputTokens: 8192},
	}}
}

func chatGPTProfileApp(t *testing.T) (*app, modelLibrary) {
	t.Helper()
	a := launchTestApp(t)
	a.config.Port, a.config.LocalKey = 8877, "synthetic-profile-local-key"
	a.apiKey = "synthetic-profile-kilo-secret"
	a.chatgpt.creds = chatGPTCredentials{Access: "synthetic-profile-chatgpt-access", Refresh: "synthetic-profile-chatgpt-refresh", Account: "synthetic-profile-account", Expires: time.Now().Add(time.Hour).Unix()}
	a.chatgpt.state = chatGPTState{Status: "idle"}
	a.launcher.start = func(clientLaunchPlan) error { t.Fatal("profile preparation launched a process"); return nil }
	library := chatGPTProfileLibrary()
	models := []modelInfo{}
	for _, m := range library.Models {
		models = append(models, modelInfo{ID: m.ID, Name: m.DisplayName, ContextWindow: m.ContextWindow, MaxOutputTokens: m.MaxOutputTokens, InputModalities: []string{"text", "image"}})
	}
	cache, err := json.Marshal(nativeCachedCatalog{SchemaVersion: 1, OrgID: a.catalogScopeLocked(), Models: models})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.dir, "model-catalog.json"), cache, 0600); err != nil {
		t.Fatal(err)
	}
	return a, library
}

func chatGPTProfileRead(t *testing.T, a *app, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{a.apiKey, a.chatgpt.creds.Access, a.chatgpt.creds.Refresh, a.chatgpt.creds.Account} {
		if secret != "" && bytes.Contains(data, []byte(secret)) {
			t.Fatalf("%s contains upstream credentials or account identity", filepath.Base(path))
		}
	}
	return data
}

func chatGPTProfileMap(t *testing.T, a *app, path string) map[string]any {
	t.Helper()
	data := chatGPTProfileRead(t, a, path)
	var got map[string]any
	var err error
	switch filepath.Ext(path) {
	case ".toml":
		err = toml.Unmarshal(data, &got)
	case ".yml":
		err = yaml.Unmarshal(data, &got)
	default:
		err = json.Unmarshal(data, &got)
	}
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func chatGPTProfileJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func chatGPTProfileIDs(library modelLibrary, initialFirst bool) []string {
	ids := []string{}
	if initialFirst {
		ids = append(ids, library.DefaultModel)
	}
	for _, model := range library.Models {
		if !initialFirst || model.ID != library.DefaultModel {
			ids = append(ids, model.ID)
		}
	}
	return ids
}

func chatGPTProfileCheckCodex(t *testing.T, a *app, dir string, library modelLibrary, inlineToken bool) {
	t.Helper()
	catalog := chatGPTProfileMap(t, a, filepath.Join(dir, "models.json"))
	ids := []string{}
	for _, entry := range catalog["models"].([]any) {
		ids = append(ids, stringValue(object(entry)["slug"]))
	}
	if !reflect.DeepEqual(ids, chatGPTProfileIDs(library, true)) {
		t.Fatalf("Codex lost model identity/order: %q", ids)
	}
	config := chatGPTProfileMap(t, a, filepath.Join(dir, "config.toml"))
	provider := object(object(config["model_providers"])[stringValue(config["model_provider"])])
	if config["model"] != library.DefaultModel || provider["base_url"] != "http://127.0.0.1:8877/v1" {
		t.Fatal("wrong Codex default or endpoint")
	}
	if inlineToken {
		if provider["experimental_bearer_token"] != a.config.LocalKey || provider["env_key"] != nil {
			t.Fatal("wrong Xcode Codex credential")
		}
	} else if provider["env_key"] != "KILO_LOCAL_API_KEY" || provider["experimental_bearer_token"] != nil {
		t.Fatal("Codex must use the local-key environment variable")
	}
}

func chatGPTProfileCheckClaude(t *testing.T, a *app, dir string, library modelLibrary, picker bool) {
	t.Helper()
	var selected claudeSelection
	if err := json.Unmarshal(chatGPTProfileRead(t, a, filepath.Join(dir, "kilo-models.json")), &selected); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, m := range selected.Models {
		ids = append(ids, m.ID)
	}
	if selected.Initial != library.DefaultModel || !reflect.DeepEqual(ids, chatGPTProfileIDs(library, false)) {
		t.Fatal("Claude selection changed namespace, order or default")
	}
	settings := chatGPTProfileMap(t, a, filepath.Join(dir, "settings.json"))
	env := object(settings["env"])
	if settings["model"] != library.DefaultModel || env["ANTHROPIC_MODEL"] != library.DefaultModel || env["ANTHROPIC_AUTH_TOKEN"] != a.config.LocalKey || env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8877" {
		t.Fatal("wrong Claude default or local authentication")
	}
	if picker {
		options := object(settings["modelPicker"])["options"].([]any)
		if len(options) != len(library.Models) || object(options[0])["model"] != library.Models[0].ID || object(options[1])["model"] != library.Models[1].ID || object(options[1])["label"] != library.Models[1].DisplayName {
			t.Fatal("Claude picker collapsed connections or lost labels")
		}
	}
}

func chatGPTProfileCheckOpenCode(t *testing.T, a *app, path string, library modelLibrary) {
	t.Helper()
	config := chatGPTProfileMap(t, a, path)
	provider := object(object(config["provider"])["kilo-local"])
	models, options := object(provider["models"]), object(provider["options"])
	if len(models) != len(library.Models) || config["model"] != "kilo-local/"+library.DefaultModel || config["small_model"] != config["model"] || options["apiKey"] != a.config.LocalKey || options["baseURL"] != "http://127.0.0.1:8877/v1" {
		t.Fatal("OpenCode lost model/default/local auth")
	}
	for _, m := range library.Models {
		if object(models[m.ID])["name"] != m.DisplayName {
			t.Fatalf("OpenCode lost exact ID/name %q", m.ID)
		}
	}
}

func TestChatGPTMixedTerminalProfiles(t *testing.T) {
	for _, client := range []string{"codex-cli", "claude", "opencode", "omp"} {
		t.Run(client, func(t *testing.T) {
			a, library := chatGPTProfileApp(t)
			if err := a.prepareTerminalProfile(client, a.launcher.home, library, claudeCaps("2.1.263")); err != nil {
				t.Fatal(err)
			}
			plan := clientLaunchPlan{Client: client, Name: client, Env: map[string]string{}}
			if err := a.launchProfile(&plan, a.launcher.home); err != nil {
				t.Fatal(err)
			}
			switch client {
			case "codex-cli":
				chatGPTProfileCheckCodex(t, a, a.codexCLIProfileDir, library, false)
				if plan.Env["KILO_LOCAL_API_KEY"] != a.config.LocalKey || plan.Env["CODEX_HOME"] != a.codexCLIProfileDir {
					t.Fatal("wrong Codex launch environment")
				}
			case "claude":
				chatGPTProfileCheckClaude(t, a, a.claudeProfileDir, library, true)
			case "opencode":
				_, path, _, err := a.editorPaths(client)
				if err != nil {
					t.Fatal(err)
				}
				chatGPTProfileCheckOpenCode(t, a, path, library)
				if plan.Env["OPENCODE_CONFIG"] != path || !reflect.DeepEqual(plan.Args, []string{"--model", "kilo-local/" + library.DefaultModel}) {
					t.Fatal("wrong OpenCode launch mapping")
				}
			case "omp":
				models := chatGPTProfileMap(t, a, filepath.Join(a.ompProfileDir, "models.yml"))
				provider := object(object(models["providers"])["kilo-local"])
				ids := []string{}
				for _, entry := range provider["models"].([]any) {
					ids = append(ids, stringValue(object(entry)["id"]))
				}
				config := chatGPTProfileMap(t, a, filepath.Join(a.ompProfileDir, "config.yml"))
				if !reflect.DeepEqual(ids, chatGPTProfileIDs(library, false)) || provider["apiKey"] != a.config.LocalKey || provider["baseUrl"] != "http://127.0.0.1:8877/v1" || object(config["modelRoles"])["default"] != "kilo-local/"+library.DefaultModel {
					t.Fatal("OMP lost IDs, default or local key")
				}
				if !reflect.DeepEqual(plan.Args, []string{"--model", "kilo-local/" + library.DefaultModel}) {
					t.Fatal("wrong OMP launch mapping")
				}
			}
			serialized := chatGPTProfileJSON(t, plan)
			for _, secret := range []string{a.apiKey, a.chatgpt.creds.Access, a.chatgpt.creds.Refresh} {
				if strings.Contains(serialized, secret) {
					t.Fatal("launch plan leaked upstream authentication")
				}
			}
			if a.proxyServer != nil {
				t.Fatal("profile preparation started the proxy")
			}
		})
	}
}

func TestChatGPTMixedDesktopAndEditorProfiles(t *testing.T) {
	for _, client := range []string{"codex", "zed", "claude-desktop", "xcode-codex", "xcode-claude", "xcode-chat"} {
		t.Run(client, func(t *testing.T) {
			a, library := chatGPTProfileApp(t)
			choices := terminalLibraryChoices(library, readNativeCatalogCache(a.dir, a.catalogScopeLocked()))
			selection := claudeSelection{Initial: library.DefaultModel, Aliases: map[string]string{"sonnet": library.Models[0].ID, "opus": library.Models[1].ID, "haiku": library.Models[2].ID}, Mode: "installed"}
			editor := editorSelection{Initial: library.DefaultModel}
			for _, m := range library.Models {
				selection.Models = append(selection.Models, claudeModel{ID: m.ID, DisplayName: m.DisplayName})
				editor.Models = append(editor.Models, editorModel{ID: m.ID, Name: m.DisplayName, Context: m.ContextWindow, Output: m.MaxOutputTokens})
			}
			switch client {
			case "codex", "xcode-codex":
				catalog, err := buildCodexCatalog(choices, library.DefaultModel, client == "xcode-codex")
				if err != nil {
					t.Fatal(err)
				}
				path, dir := "codex/catalog", a.codexProfileDir
				if client == "xcode-codex" {
					path, dir = "xcode/codex", filepath.Join(a.xcodeTestRoot, "codex")
				}
				if w := adminRequest(a, path, chatGPTProfileJSON(t, map[string]any{"catalog": json.RawMessage(catalog)})); w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				chatGPTProfileCheckCodex(t, a, dir, library, client == "xcode-codex")
			case "xcode-claude", "xcode-chat":
				variant := strings.TrimPrefix(client, "xcode-")
				if w := adminRequest(a, "xcode/"+variant, chatGPTProfileJSON(t, selection)); w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				if variant == "claude" {
					chatGPTProfileCheckClaude(t, a, filepath.Join(a.xcodeTestRoot, "ClaudeAgentConfig"), library, false)
					settings := chatGPTProfileMap(t, a, filepath.Join(a.xcodeTestRoot, "ClaudeAgentConfig", "settings.json"))
					env := object(settings["env"])
					if env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != library.Models[0].ID || env["ANTHROPIC_DEFAULT_OPUS_MODEL"] != library.Models[1].ID {
						t.Fatal("Xcode aliases collapsed connections")
					}
				} else {
					w := httptest.NewRecorder()
					a.xcodeChatModels(w)
					var result struct{ Data []struct{ ID string } }
					if json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Data) != 3 || result.Data[0].ID != library.DefaultModel {
						t.Fatal("Xcode Chat lost mixed catalog or default")
					}
					chatGPTProfileRead(t, a, filepath.Join(a.dir, "xcode-chat.json"))
				}
			case "zed":
				stored := 0
				a.zedCredentialStore = func(_ context.Context, endpoint, key, executable string) error {
					stored++
					if key != a.config.LocalKey || endpoint != zedBaseURL("http://127.0.0.1:8877/v1", a.config.LocalKey) {
						t.Error("Zed stored wrong credential")
					}
					return nil
				}
				if w := adminRequest(a, "editors/zed/profile", chatGPTProfileJSON(t, editor)); w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				_, path, _, _ := a.editorPaths("zed")
				settings := chatGPTProfileMap(t, a, path)
				provider := object(object(object(settings["language_models"])["openai_compatible"])["kilo-local"])
				ids := []string{}
				for _, entry := range provider["available_models"].([]any) {
					ids = append(ids, stringValue(object(entry)["name"]))
				}
				if stored != 1 || !reflect.DeepEqual(ids, chatGPTProfileIDs(library, false)) || object(object(settings["agent"])["default_model"])["model"] != library.DefaultModel {
					t.Fatal("Zed lost mixed selection/default")
				}
				if bytes.Contains(chatGPTProfileRead(t, a, path), []byte(a.config.LocalKey)) {
					t.Fatal("Zed wrote key into JSON")
				}
			case "claude-desktop":
				for i := range editor.Models {
					editor.Models[i].Context, editor.Models[i].Output = 0, 0
				}
				// Connecting ChatGPT enables the required alias bridge without
				// changing the saved opt-in for other experimental providers.
				if a.config.ClaudeDesktopExperimentalModels {
					t.Fatal("fixture unexpectedly opted in")
				}
				if w := desktopProfileTestRequest(a, http.MethodPost, editor); w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				paths, err := a.claudeDesktopPaths()
				if err != nil {
					t.Fatal(err)
				}
				settings := chatGPTProfileMap(t, a, paths.ConfigPath)
				models := settings["inferenceModels"].([]any)
				if len(models) != 3 || settings["inferenceGatewayApiKey"] != a.config.LocalKey || object(models[0])["name"] != claudeDesktopAlias(library.DefaultModel) || object(models[0])["labelOverride"] != library.Models[1].DisplayName {
					t.Fatal("Desktop lost default alias/name/local key")
				}
				for _, id := range []string{library.Models[0].ID, library.Models[1].ID} {
					resolved, err := a.resolveClaudeDesktopAlias(claudeDesktopAlias(id))
					if err != nil || resolved != id {
						t.Fatalf("alias did not map to exact selected connection: %s %v", id, err)
					}
				}
				if claudeDesktopAlias(library.Models[0].ID) == claudeDesktopAlias(library.Models[1].ID) {
					t.Fatal("same slug collapsed across providers")
				}
				var saved editorSelection
				if json.Unmarshal(chatGPTProfileRead(t, a, paths.SelectionPath), &saved) != nil || !reflect.DeepEqual(saved, editor) {
					t.Fatal("Desktop persisted aliases instead of real IDs")
				}
			}
			plan := clientLaunchPlan{Client: client, Name: client, Env: map[string]string{}}
			if err := a.launchProfile(&plan, a.launcher.home); err != nil {
				t.Fatal(err)
			}
			if a.proxyServer != nil {
				t.Fatal("profile preparation started the proxy")
			}
		})
	}
}

func TestChatGPTMixedOpenDesignProfiles(t *testing.T) {
	for _, engine := range []string{"codex-cli", "claude", "opencode"} {
		t.Run(engine, func(t *testing.T) {
			a, library := chatGPTProfileApp(t)
			dir := filepath.Join(a.launcher.home, "open-design", engine)
			if err := prepareOpenDesignEngineProfile(dir, engine, library, readNativeCatalogCache(a.dir, a.catalogScopeLocked()), claudeCaps("2.1.263"), a.config.Port, a.config.LocalKey, imageGenerationSettings{}); err != nil {
				t.Fatal(err)
			}
			switch engine {
			case "codex-cli":
				chatGPTProfileCheckCodex(t, a, dir, library, false)
			case "claude":
				chatGPTProfileCheckClaude(t, a, dir, library, true)
			case "opencode":
				chatGPTProfileCheckOpenCode(t, a, filepath.Join(dir, "opencode.json"), library)
			}
		})
	}
}

func TestChatGPTOnlyOpenCodeProfileOmitsSavedKiloImageMCP(t *testing.T) {
	a, library := chatGPTProfileApp(t)
	a.config.ImageGeneration = imageGenerationSettings{Enabled: true, Model: "vendor/image"}
	a.apiKey, a.config.OrgID = "", ""
	if err := a.prepareTerminalProfile("opencode", a.launcher.home, library, claudeCapabilities{}); err != nil {
		t.Fatal(err)
	}
	_, path, _, _ := a.editorPaths("opencode")
	if bytes.Contains(chatGPTProfileRead(t, a, path), []byte("kilo-images")) {
		t.Fatal("ChatGPT-only OpenCode advertised Kilo image generation")
	}
}

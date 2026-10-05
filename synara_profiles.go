package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	synaraCodexNormalID      = "kilo_codex_normal"
	synaraCodexProxyID       = "kilo_codex_proxy"
	synaraClaudeNormalID     = "kilo_claude_normal"
	synaraClaudeProxyID      = "kilo_claude_proxy"
	synaraComposerSeedName   = "synara-storage-origin-v1.json"
	synaraComposerMarkerName = "composer-seeded.json"
)

var synaraIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var synaraEnvironmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

type synaraProfileOptions struct {
	RootDir, DataDir, UserDataDir, NormalHome, CodexBinary, ClaudeBinary string
	NormalEnvironment                                                    map[string]string
	Library                                                              modelLibrary
	Catalog                                                              []modelInfo
	ClaudeCaps                                                           claudeCapabilities
	Port                                                                 int
	LocalKey                                                             string
	Images                                                               imageGenerationSettings
}

type synaraProfiles struct {
	CodexHome, ClaudeHome string
	Providers             map[string]any
	Files                 []profileFile
}

func validateSynaraLibrary(library modelLibrary) error {
	if err := validateModelLibrary(library); err != nil {
		return err
	}
	if len(library.Models) > t3CodeCustomModelLimit {
		return errors.New("Synara supports up to 32 shared models. Reduce your selection in Models and prepare again.")
	}
	return nil
}

func synaraProfilePaths(options synaraProfileOptions) (string, string, error) {
	data, ui := options.DataDir, options.UserDataDir
	if data == "" {
		data = filepath.Join(options.RootDir, "data")
	}
	if ui == "" {
		ui = filepath.Join(options.RootDir, "electron")
	}
	for _, path := range []string{options.RootDir, data, ui} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") || !claudeDesktopWithin(options.RootDir, path) {
			return "", "", errors.New("Synara requires absolute, separate private data and desktop directories.")
		}
	}
	if filepath.Clean(data) == filepath.Clean(ui) {
		return "", "", errors.New("Synara data and desktop directories must be separate.")
	}
	return data, ui, nil
}

// Plan every file before the caller commits the whole registration transaction.
// Normal CLI homes are referenced by path; their configuration, login and chats
// are not read or copied here. Synara itself manages their continuation overlays.
func planSynaraProfiles(options synaraProfileOptions) (synaraProfiles, error) {
	result := synaraProfiles{}
	if err := validateSynaraLibrary(options.Library); err != nil {
		return result, err
	}
	dataDir, userDataDir, err := synaraProfilePaths(options)
	if err != nil {
		return result, err
	}
	baseOptions := t3CodeProfileOptions{RootDir: options.RootDir, NormalHome: options.NormalHome, CodexBinary: options.CodexBinary, ClaudeBinary: options.ClaudeBinary, NormalEnvironment: options.NormalEnvironment, Library: options.Library, Catalog: options.Catalog, ClaudeCaps: options.ClaudeCaps, Version: t3CodeNightlyVersion, Port: options.Port, LocalKey: options.LocalKey, Images: options.Images}
	if _, err := synaraNormalSecureStorage(baseOptions); err != nil {
		return result, err
	}
	base, err := planT3CodeProfiles(baseOptions)
	if err != nil {
		return result, errors.New(strings.ReplaceAll(strings.ReplaceAll(err.Error(), "T3 Code nightly", "Synara"), "T3 Code", "Synara"))
	}
	result.CodexHome, result.ClaudeHome = base.CodexHome, base.ClaudeHome
	for _, file := range base.Files {
		contents := file.new
		switch file.path {
		case filepath.Join(result.CodexHome, "config.toml"):
			contents, err = synaraCodexFileAuthentication(contents)
		case filepath.Join(result.ClaudeHome, "settings.json"):
			contents, err = synaraClaudeBaseURL(contents, options.Port)
		}
		if err != nil {
			return result, err
		}
		planned, err := prepareProfileFile(file.path, contents)
		if err != nil {
			return result, err
		}
		result.Files = append(result.Files, planned)
	}
	result.Providers, err = synaraProviderInstances(options, result, baseOptions)
	if err != nil {
		return result, err
	}
	settingsFiles, err := planSynaraSettings(options, dataDir, result.Providers)
	if err != nil {
		return result, err
	}
	result.Files = append(result.Files, settingsFiles...)
	seedFiles, err := planSynaraComposerSeed(options, userDataDir)
	if err != nil {
		return result, err
	}
	result.Files = append(result.Files, seedFiles...)
	return result, nil
}

func synaraCodexFileAuthentication(data []byte) ([]byte, error) {
	var config map[string]any
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	// Synara requires observable auth for its private app-server overlay. The
	// Kilo provider still authenticates through its env_key, not auth.json.
	config["cli_auth_credentials_store"] = "file"
	// Its installed health and credential-identity parsers recognize only bare
	// root keys and explicit model-provider tables. Canonicalize this private
	// file so quoted/dotted settings cannot hide our custom provider from them;
	// all unrelated TOML values retain their meaning.
	return toml.Marshal(config)
}

func synaraClaudeBaseURL(data []byte, port int) ([]byte, error) {
	object, err := decodeClaudeDesktopObject(data)
	if err != nil {
		return nil, err
	}
	var environment map[string]json.RawMessage
	if json.Unmarshal(object["env"], &environment) != nil || environment == nil {
		return nil, errors.New("Invalid private Synara Claude environment.")
	}
	claudeDesktopSet(environment, "ANTHROPIC_BASE_URL", "http://127.0.0.1:"+strconv.Itoa(port)+"/synara")
	object["env"], _ = json.Marshal(environment)
	encoded, err := json.MarshalIndent(object, "", "  ")
	return append(encoded, '\n'), err
}

func synaraProviderInstances(options synaraProfileOptions, profiles synaraProfiles, baseOptions t3CodeProfileOptions) (map[string]any, error) {
	normalCodex, normalClaude, err := t3CodeNormalProviderHomes(baseOptions)
	if err != nil {
		return nil, err
	}
	normalSecureStorage, err := synaraNormalSecureStorage(baseOptions)
	if err != nil {
		return nil, err
	}
	// Reuse the vetted normal-home/XDG allowlist; the Synara-specific config
	// below replaces T3 descriptors with the string list this beta accepts.
	base := normalizeT3CodeProviders(t3CodeProviderInstances(baseOptions, t3CodeProfiles{CodexHome: profiles.CodexHome, ClaudeHome: profiles.ClaudeHome}, terminalLibraryChoices(options.Library, options.Catalog), claudeSelection{}))
	models := make([]string, 0, len(options.Library.Models))
	models = append(models, options.Library.DefaultModel)
	for _, model := range options.Library.Models {
		if model.ID != options.Library.DefaultModel {
			models = append(models, model.ID)
		}
	}
	for _, id := range []string{synaraCodexNormalID, synaraCodexProxyID, synaraClaudeNormalID, synaraClaudeProxyID} {
		instance := base[id].(map[string]any)
		config := instance["config"].(map[string]any)
		delete(config, "setupMode")
		delete(config, "launchArgs")
		delete(config, "autoCompactWindow")
		config["customModels"] = []string{}
		if id == synaraCodexProxyID || id == synaraClaudeProxyID {
			config["customModels"] = models
		}
		if id == synaraCodexNormalID {
			config["homePath"] = normalCodex
		}
		if id == synaraClaudeNormalID {
			config["homePath"], config["configDir"], config["secureStorageDir"] = options.NormalHome, normalClaude, normalSecureStorage
			// Setting configDir is necessary after the desktop HOME is isolated.
			// A defined empty secure-storage override preserves Claude's normal
			// unsuffixed keychain service when the user's shell had no override.
			instance["environment"] = append(instance["environment"].([]any), map[string]any{"name": "CLAUDE_SECURESTORAGE_CONFIG_DIR", "value": normalSecureStorage, "sensitive": false})
		}
		if id == synaraCodexProxyID || id == synaraClaudeProxyID {
			privateHome := profiles.CodexHome
			if id == synaraClaudeProxyID {
				privateHome = profiles.ClaudeHome
				config["configDir"], config["secureStorageDir"] = privateHome, privateHome
			}
			config["homePath"] = privateHome
			environment := instance["environment"].([]any)
			for _, raw := range environment {
				variable := raw.(map[string]any)
				name := variable["name"].(string)
				switch name {
				case "HOME", "USERPROFILE":
					variable["value"] = privateHome
				case "APPDATA":
					variable["value"] = filepath.Join(privateHome, "AppData", "Roaming")
				case "LOCALAPPDATA":
					variable["value"] = filepath.Join(privateHome, "AppData", "Local")
				case "XDG_CONFIG_HOME":
					variable["value"] = filepath.Join(privateHome, ".config")
				case "XDG_DATA_HOME":
					variable["value"] = filepath.Join(privateHome, ".local", "share")
				case "XDG_CACHE_HOME":
					variable["value"] = filepath.Join(privateHome, ".cache")
				case "XDG_STATE_HOME":
					variable["value"] = filepath.Join(privateHome, ".local", "state")
				case "XDG_RUNTIME_DIR":
					variable["value"] = filepath.Join(privateHome, ".runtime")
				case "ANTHROPIC_BASE_URL":
					variable["value"] = "http://127.0.0.1:" + strconv.Itoa(options.Port) + "/synara"
				}
			}
		}
	}
	return normalizeT3CodeProviders(base), nil
}

func synaraNormalSecureStorage(options t3CodeProfileOptions) (string, error) {
	_, _, err := t3CodeNormalProviderHomes(options)
	if err != nil {
		return "", err
	}
	// Claude hashes the literal NFC-normalized override for its keychain
	// service. Cleaning a trailing separator or dot segment changes the
	// existing login namespace even when the directory remains identical.
	secure := options.NormalEnvironment["CLAUDE_CONFIG_DIR"]
	if value, exists := options.NormalEnvironment["CLAUDE_SECURESTORAGE_CONFIG_DIR"]; exists {
		secure = value
	}
	if secure != "" && !filepath.IsAbs(secure) || strings.ContainsAny(secure, "\x00\r\n") {
		return "", errors.New("Synara normal Claude secure storage must be an absolute path.")
	}
	effective := secure
	if effective == "" {
		effective = filepath.Join(options.NormalHome, ".claude")
	}
	canonical, err := t3CodeCanonicalPath(effective)
	if err != nil {
		return "", errors.New("Cannot safely resolve normal Claude secure storage.")
	}
	private, err := t3CodeCanonicalPath(options.RootDir)
	if err != nil || withinT3CodePath(canonical, private) || withinT3CodePath(private, canonical) {
		return "", errors.New("Synara Kilo profiles must be separate from normal Claude secure storage.")
	}
	return secure, nil
}

func synaraSettingsDocument(data []byte) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}")
	}
	envelope, err := decodeClaudeDesktopObject(data)
	if err != nil {
		return nil, nil, errors.New("Private Synara settings must be JSON objects with unique keys; no setup changes saved.")
	}
	settings := envelope
	if raw, exists := envelope["settings"]; exists {
		settings, err = decodeClaudeDesktopObject(raw)
		if err != nil {
			return nil, nil, errors.New("Private Synara settings envelope must contain an object.")
		}
	} else {
		envelope = map[string]json.RawMessage{}
	}
	if err := validateSynaraSettings(settings); err != nil {
		return nil, nil, err
	}
	return envelope, settings, nil
}

// Reject shapes that would make Synara quarantine settings and use its defaults.
// Unknown fields remain untouched for forward-compatible private UI settings.
func validateSynaraSettings(settings map[string]json.RawMessage) error {
	invalid := func() error { return errors.New("Invalid private Synara settings; no setup changes saved.") }
	for _, name := range []string{"enableAssistantStreaming", "enableProviderUpdateChecks", "githubInboxIncludeUpstreams"} {
		if raw, exists := settings[name]; exists {
			if !synaraBoolean(raw) {
				return invalid()
			}
		}
	}
	for _, name := range []string{"addProjectBaseDirectory"} {
		if raw, exists := settings[name]; exists {
			var value string
			if !synaraJSONString(raw) || json.Unmarshal(raw, &value) != nil || len(value) > 4096 {
				return invalid()
			}
		}
	}
	for name, choices := range map[string][]string{"defaultThreadEnvMode": {"local", "worktree"}, "sidechatExpiry": {"1h", "24h", "never"}} {
		if raw, exists := settings[name]; exists {
			var value string
			if json.Unmarshal(raw, &value) != nil || !helperContains(choices, value) {
				return invalid()
			}
		}
	}
	if raw, exists := settings["onboardingCompletedAt"]; exists && string(raw) != "null" {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return invalid()
		}
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			return invalid()
		}
	}
	if raw, exists := settings["providers"]; exists {
		providers, err := decodeClaudeDesktopObject(raw)
		if err != nil {
			return invalid()
		}
		for _, raw := range providers {
			provider, err := decodeClaudeDesktopObject(raw)
			if err != nil {
				return invalid()
			}
			for _, name := range []string{"enabled", "enableArtifacts", "experimentalWebSockets", "serverPasswordConfigured"} {
				if raw, exists := provider[name]; exists {
					if !synaraBoolean(raw) {
						return invalid()
					}
				}
			}
			for _, name := range []string{"binaryPath", "homePath", "launchArgs", "apiEndpoint", "serverUrl", "agentDir", "selectedAccountId"} {
				if raw, exists := provider[name]; exists {
					var value string
					if !synaraJSONString(raw) || json.Unmarshal(raw, &value) != nil || len(value) > 4096 {
						return invalid()
					}
				}
			}
			if raw, exists := provider["customModels"]; exists {
				if !synaraStringArray(raw, 256) {
					return invalid()
				}
			}
			if raw, exists := provider["accounts"]; exists {
				var accounts []json.RawMessage
				if string(raw) == "null" || json.Unmarshal(raw, &accounts) != nil {
					return invalid()
				}
				for _, raw := range accounts {
					account, err := decodeClaudeDesktopObject(raw)
					if err != nil {
						return invalid()
					}
					for _, name := range []string{"id", "label", "homePath", "shadowHomePath"} {
						if field, exists := account[name]; exists {
							var value string
							limit := 4096
							if name == "id" {
								limit = 64
							}
							if !synaraJSONString(field) || json.Unmarshal(field, &value) != nil || len(value) > limit {
								return invalid()
							}
						}
					}
					if _, exists := account["id"]; !exists {
						return invalid()
					}
				}
			}
		}
	}
	if raw, exists := settings["providerInstances"]; exists {
		instances, err := decodeClaudeDesktopObject(raw)
		if err != nil {
			return invalid()
		}
		for id, raw := range instances {
			if !synaraIdentifier.MatchString(id) {
				return invalid()
			}
			instance, err := decodeClaudeDesktopObject(raw)
			if err != nil {
				return invalid()
			}
			var driver string
			if json.Unmarshal(instance["driver"], &driver) != nil || !synaraIdentifier.MatchString(driver) {
				return invalid()
			}
			for _, name := range []string{"displayName", "accentColor"} {
				if raw, exists := instance[name]; exists {
					var value string
					if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
						return invalid()
					}
				}
			}
			if raw, exists := instance["enabled"]; exists {
				if !synaraBoolean(raw) {
					return invalid()
				}
			}
			if raw, exists := instance["environment"]; exists {
				var environment []json.RawMessage
				if string(raw) == "null" || json.Unmarshal(raw, &environment) != nil {
					return invalid()
				}
				seen := map[string]bool{}
				for _, raw := range environment {
					variable, err := decodeClaudeDesktopObject(raw)
					if err != nil {
						return invalid()
					}
					var name string
					if json.Unmarshal(variable["name"], &name) != nil || !synaraEnvironmentName.MatchString(name) || seen[name] {
						return invalid()
					}
					seen[name] = true
					if raw, exists := variable["value"]; exists {
						var value string
						if !synaraJSONString(raw) || json.Unmarshal(raw, &value) != nil {
							return invalid()
						}
					}
					for _, name := range []string{"sensitive", "valueRedacted"} {
						if raw, exists := variable[name]; exists {
							if !synaraBoolean(raw) {
								return invalid()
							}
						}
					}
				}
			}
		}
	}
	if raw, exists := settings["skills"]; exists {
		skills, err := decodeClaudeDesktopObject(raw)
		if err != nil {
			return invalid()
		}
		if list, exists := skills["disabled"]; exists && !synaraStringArray(list, 256) {
			return invalid()
		}
	}
	if raw, exists := settings["textGenerationModelSelection"]; exists {
		selection, err := decodeClaudeDesktopObject(raw)
		if err != nil {
			return invalid()
		}
		var provider, model string
		if json.Unmarshal(selection["provider"], &provider) != nil || !helperContains(synaraDrivers(), provider) || json.Unmarshal(selection["model"], &model) != nil || len(model) > 256 {
			return invalid()
		}
		if raw, exists := selection["instanceId"]; exists {
			var id string
			if json.Unmarshal(raw, &id) != nil || !synaraIdentifier.MatchString(id) {
				return invalid()
			}
		}
	}
	return nil
}

func synaraStringArray(raw json.RawMessage, limit int) bool {
	if string(raw) == "null" {
		return false
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return false
	}
	for _, raw := range values {
		var value string
		if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil || len(value) > limit {
			return false
		}
	}
	return true
}

func synaraBoolean(raw json.RawMessage) bool {
	value := string(bytes.TrimSpace(raw))
	return value == "true" || value == "false"
}

func synaraJSONString(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '"'
}

func synaraDrivers() []string {
	return []string{"codex", "claudeAgent", "cursor", "devin", "antigravity", "grok", "droid", "opencode", "pi", "omp"}
}

func planSynaraSettings(options synaraProfileOptions, dataDir string, providers map[string]any) ([]profileFile, error) {
	settingsPath := filepath.Join(dataDir, "userdata", "settings.json")
	secretsDir := filepath.Join(dataDir, "userdata", "secrets")
	if err := safeEditorDir(options.RootDir, secretsDir); err != nil {
		return nil, err
	}
	data, err := readCatalogFile(settingsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("Cannot safely read the private Synara settings.")
	}
	envelope, settings, err := synaraSettingsDocument(data)
	if err != nil {
		return nil, err
	}
	all := map[string]json.RawMessage{}
	if raw, exists := settings["providerInstances"]; exists {
		all, _ = decodeClaudeDesktopObject(raw)
	}
	files := []profileFile{}
	for id, raw := range providers {
		instance, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("Invalid private Synara provider configuration.")
		}
		environment, ok := instance["environment"].([]any)
		if !ok {
			return nil, errors.New("Invalid private Synara environment configuration.")
		}
		for _, raw := range environment {
			variable := raw.(map[string]any)
			sensitive, _ := variable["sensitive"].(bool)
			if !sensitive {
				continue
			}
			name, _ := variable["name"].(string)
			value, _ := variable["value"].(string)
			if (id != synaraCodexProxyID || name != "KILO_LOCAL_API_KEY") && (id != synaraClaudeProxyID || name != "ANTHROPIC_AUTH_TOKEN") {
				return nil, errors.New("Unexpected private Synara secret.")
			}
			if value == "" || strings.ContainsAny(value, "\x00\r\n") {
				return nil, errors.New("Invalid local Synara proxy credential.")
			}
			file, err := prepareProfileFile(filepath.Join(secretsDir, t3CodeSecretName(id, name)), []byte(value))
			if err != nil {
				return nil, err
			}
			files = append(files, file)
			variable["value"], variable["valueRedacted"] = "", true
		}
		all[id], _ = json.Marshal(instance)
	}
	// The picker gates every account on its driver's global enabled flag.
	// Disable the two implicit default instances separately so enabling those
	// drivers exposes our four accounts without adding two legacy accounts.
	for _, driver := range []string{"codex", "claudeAgent"} {
		instance := map[string]json.RawMessage{}
		if raw, exists := all[driver]; exists {
			instance, _ = decodeClaudeDesktopObject(raw)
		}
		config := map[string]json.RawMessage{}
		if raw, exists := instance["config"]; exists {
			config, err = decodeClaudeDesktopObject(raw)
			if err != nil {
				return nil, errors.New("Invalid private Synara settings; no setup changes saved.")
			}
		}
		claudeDesktopSet(instance, "driver", driver)
		claudeDesktopSet(instance, "enabled", false)
		claudeDesktopSet(config, "enabled", false)
		claudeDesktopSet(instance, "config", config)
		all[driver], _ = json.Marshal(instance)
	}
	claudeDesktopSet(settings, "providerInstances", all)
	legacy := map[string]json.RawMessage{}
	if raw, exists := settings["providers"]; exists {
		legacy, _ = decodeClaudeDesktopObject(raw)
	}
	for _, driver := range synaraDrivers() {
		provider := map[string]json.RawMessage{}
		if raw, exists := legacy[driver]; exists {
			provider, _ = decodeClaudeDesktopObject(raw)
		}
		claudeDesktopSet(provider, "enabled", driver == "codex" || driver == "claudeAgent")
		if driver == "codex" {
			// Native legacy Codex accounts would otherwise become additional
			// active accounts when its driver is enabled for our custom instances.
			claudeDesktopSet(provider, "accounts", []any{})
		}
		legacy[driver], _ = json.Marshal(provider)
	}
	claudeDesktopSet(settings, "providers", legacy)
	// Text generation has its own server setting; keep title/git requests on a
	// prepared Kilo agent rather than an unavailable implicit normal instance.
	claudeDesktopSet(settings, "textGenerationModelSelection", map[string]any{"provider": "codex", "instanceId": synaraCodexProxyID, "model": options.Library.DefaultModel})
	claudeDesktopSet(envelope, "settings", settings)
	claudeDesktopSet(envelope, "migrationVersion", 3)
	if _, exists := envelope["revision"]; !exists {
		claudeDesktopSet(envelope, "revision", 0)
	}
	encoded, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil || len(encoded)+1 > catalogLimit {
		return nil, errors.New("Private Synara settings exceed the size limit.")
	}
	file, err := prepareProfileFile(settingsPath, append(encoded, '\n'))
	if err != nil {
		return nil, err
	}
	return append(files, file), nil
}

func planSynaraComposerSeed(options synaraProfileOptions, userDataDir string) ([]profileFile, error) {
	if err := safeEditorDir(options.RootDir, userDataDir); err != nil {
		return nil, err
	}
	markerPath := filepath.Join(options.RootDir, synaraComposerMarkerName)
	marker := []byte("{\"version\":1}\n")
	old, err := readCatalogFile(markerPath)
	if err == nil {
		if !bytes.Equal(old, marker) {
			return nil, errors.New("Invalid private Synara composer marker.")
		}
		file, err := prepareProfileFile(markerPath, marker)
		return []profileFile{file}, err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("Cannot safely read the private Synara composer marker.")
	}
	sticky := map[string]any{
		synaraCodexProxyID:  map[string]any{"provider": "codex", "instanceId": synaraCodexProxyID, "model": options.Library.DefaultModel},
		synaraClaudeProxyID: map[string]any{"provider": "claudeAgent", "instanceId": synaraClaudeProxyID, "model": options.Library.DefaultModel},
	}
	for _, choice := range terminalLibraryChoices(options.Library, options.Catalog) {
		if choice.Model.ID == options.Library.DefaultModel {
			_, effort := nativeReasoningFor(choice)
			if effort != "" {
				sticky[synaraCodexProxyID].(map[string]any)["options"] = map[string]any{"reasoningEffort": effort}
			}
		}
	}
	payload, _ := json.Marshal(map[string]any{"state": map[string]any{"draftsByThreadId": map[string]any{}, "draftThreadsByThreadId": map[string]any{}, "projectDraftThreadIdByProjectId": map[string]any{}, "stickyModelSelectionByProvider": sticky, "stickyActiveProvider": synaraCodexProxyID}, "version": 6})
	seed, _ := json.MarshalIndent(map[string]any{"version": 1, "exportedAt": time.Now().UTC().Format(time.RFC3339Nano), "entries": map[string]string{"synara:composer-drafts:v1": string(payload)}}, "", "  ")
	seedFile, err := prepareProfileFile(filepath.Join(userDataDir, synaraComposerSeedName), append(seed, '\n'))
	if err != nil {
		return nil, err
	}
	markerFile, err := prepareProfileFile(markerPath, marker)
	if err != nil {
		return nil, err
	}
	return []profileFile{seedFile, markerFile}, nil
}

func synaraProfilesReady(options synaraProfileOptions, expectedProviders map[string]any) bool {
	dataDir, _, err := synaraProfilePaths(options)
	if err != nil {
		return false
	}
	data, err := readCatalogFile(filepath.Join(dataDir, "userdata", "settings.json"))
	if err != nil {
		return false
	}
	_, settings, err := synaraSettingsDocument(data)
	if err != nil {
		return false
	}
	var actual map[string]any
	if json.Unmarshal(settings["providerInstances"], &actual) != nil {
		return false
	}
	for id, expected := range expectedProviders {
		if !t3CodeSubset(expected, actual[id]) {
			return false
		}
	}
	for _, driver := range []string{"codex", "claudeAgent"} {
		if !t3CodeSubset(map[string]any{"driver": driver, "enabled": false, "config": map[string]any{"enabled": false}}, actual[driver]) {
			return false
		}
	}
	var legacy map[string]map[string]any
	if json.Unmarshal(settings["providers"], &legacy) != nil {
		return false
	}
	for _, driver := range synaraDrivers() {
		if legacy[driver]["enabled"] != (driver == "codex" || driver == "claudeAgent") {
			return false
		}
	}
	if accounts, ok := legacy["codex"]["accounts"].([]any); !ok || len(accounts) != 0 {
		return false
	}
	var selection map[string]any
	if json.Unmarshal(settings["textGenerationModelSelection"], &selection) != nil || !reflect.DeepEqual(selection, map[string]any{"provider": "codex", "instanceId": synaraCodexProxyID, "model": options.Library.DefaultModel}) {
		return false
	}
	for id, name := range map[string]string{synaraCodexProxyID: "KILO_LOCAL_API_KEY", synaraClaudeProxyID: "ANTHROPIC_AUTH_TOKEN"} {
		data, err := readCatalogFile(filepath.Join(dataDir, "userdata", "secrets", t3CodeSecretName(id, name)))
		if err != nil || string(data) != options.LocalKey {
			return false
		}
	}
	return true
}

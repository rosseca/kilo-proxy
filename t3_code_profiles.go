package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	t3CodeCustomModelLimit = 32
	t3CodeCodexNormalID    = "kilo_codex_normal"
	t3CodeCodexProxyID     = "kilo_codex_proxy"
	t3CodeClaudeNormalID   = "kilo_claude_normal"
	t3CodeClaudeProxyID    = "kilo_claude_proxy"
	t3CodeProxyAccentColor = "#327653"
)

func validateT3CodeLibrary(library modelLibrary) error {
	if err := validateModelLibrary(library); err != nil {
		return err
	}
	if len(library.Models) > t3CodeCustomModelLimit {
		return errors.New("T3 Code supports up to 32 shared models. Reduce your selection in Models and prepare again.")
	}
	return nil
}

type t3CodeProfileOptions struct {
	RootDir, NormalHome, CodexBinary, ClaudeBinary string
	Version                                        string
	NormalEnvironment                              map[string]string
	Library                                        modelLibrary
	Catalog                                        []modelInfo
	ClaudeCaps                                     claudeCapabilities
	Port                                           int
	LocalKey                                       string
	Images                                         imageGenerationSettings
}

type t3CodeProfiles struct {
	CodexHome, ClaudeHome string
	Providers             map[string]any
	Files                 []profileFile
}

// Build every managed file before committing anything. The caller can append
// T3 settings and secret-store files to Files and commit the complete setup in
// one transaction; normal provider configuration and credentials are never read.
func planT3CodeProfiles(options t3CodeProfileOptions) (t3CodeProfiles, error) {
	result := t3CodeProfiles{}
	for _, value := range []string{options.RootDir, options.NormalHome, options.CodexBinary, options.ClaudeBinary} {
		if !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
			return result, errors.New("T3 Code requires absolute private-profile, normal-home and CLI paths.")
		}
	}
	if options.Port < 1024 || options.Port > 65535 || options.LocalKey == "" || strings.ContainsAny(options.LocalKey, "\x00\r\n") {
		return result, errors.New("T3 Code requires a valid local proxy port and key.")
	}
	if err := validateT3CodeLibrary(options.Library); err != nil {
		return result, err
	}
	if len(options.Library.Models) == 0 {
		return result, errors.New("Choose shared models before preparing T3 Code.")
	}
	normalCodex, normalClaude, err := t3CodeNormalProviderHomes(options)
	if err != nil {
		return result, err
	}
	result.CodexHome = filepath.Join(options.RootDir, "profiles", "codex-kilo")
	result.ClaudeHome = filepath.Join(options.RootDir, "profiles", "claude-kilo")
	for _, normal := range []string{normalCodex, normalClaude} {
		normal, err = t3CodeCanonicalPath(normal)
		if err != nil {
			return t3CodeProfiles{}, errors.New("Cannot safely resolve the normal T3 Code provider home.")
		}
		for _, private := range []string{result.CodexHome, result.ClaudeHome} {
			private, err = t3CodeCanonicalPath(private)
			if err != nil {
				return t3CodeProfiles{}, errors.New("Cannot safely resolve the private T3 Code provider home.")
			}
			if withinT3CodePath(normal, private) || withinT3CodePath(private, normal) {
				return t3CodeProfiles{}, errors.New("T3 Code Kilo profiles must be separate from normal provider homes.")
			}
		}
	}
	for _, dir := range []string{result.CodexHome, result.ClaudeHome} {
		if err := safeEditorDir(options.RootDir, dir); err != nil {
			return t3CodeProfiles{}, err
		}
	}
	choices := terminalLibraryChoices(options.Library, options.Catalog)
	catalog, err := buildCodexCatalog(choices, options.Library.DefaultModel, false)
	if err != nil {
		return t3CodeProfiles{}, err
	}
	read := func(path string) ([]byte, error) {
		data, err := readCatalogFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("Cannot safely read the private T3 Code provider settings.")
		}
		return data, nil
	}
	configPath := filepath.Join(result.CodexHome, "config.toml")
	oldCodex, err := read(configPath)
	if err != nil {
		return t3CodeProfiles{}, err
	}
	codexConfig, err := mergeCodexConfig(oldCodex, catalog, options.Port)
	if err != nil {
		return t3CodeProfiles{}, err
	}
	codexConfig, err = mergeCodexImages(codexConfig, options.Images, options.Port)
	if err != nil {
		return t3CodeProfiles{}, err
	}
	// T3 status probes use its server cwd, and tasks use their project cwd.
	// An absolute catalog path keeps every app-server on this prepared catalog.
	codexConfig, err = mergeT3CodeCatalogPath(codexConfig, filepath.Join(result.CodexHome, "models.json"))
	if err != nil {
		return t3CodeProfiles{}, err
	}
	selection := claudeSelection{Initial: options.Library.DefaultModel, Mode: "installed", Aliases: map[string]string{}}
	claudeSettingsCaps := claudeCapabilities{}
	if options.Version == t3CodeNightlyVersion && options.ClaudeCaps.PerModelEffort {
		// Nightly V2 discards Claude effort options for custom gateway IDs.
		// Keep the defaults in the private CLI profile without family aliases,
		// a replacement picker or a global effort shared by unrelated models.
		claudeSettingsCaps.PerModelEffort = true
	}
	for i, model := range options.Library.Models {
		context, err := contextPolicyForChoice(choices[i])
		if err != nil {
			return t3CodeProfiles{}, err
		}
		effort := ""
		if options.Version == t3CodeNightlyVersion && model.ReasoningEffort != "" && validClaudeEffort(model.ID, model.ReasoningEffort) {
			if !strings.Contains(model.ID, "/") {
				return t3CodeProfiles{}, errors.New("T3 Code nightly requires provider-qualified Claude model IDs (for example anthropic/claude-opus-4-6) for saved reasoning defaults. Use the exact gateway ID in Models.")
			}
			_, preferred := nativeReasoningFor(choices[i])
			if preferred != "" && validClaudeEffort(model.ID, preferred) {
				if !options.ClaudeCaps.PerModelEffort {
					return t3CodeProfiles{}, errors.New("T3 Code nightly requires Claude Code 2.1.251 or newer to apply saved per-model reasoning. Update Claude Code and prepare again.")
				}
				effort = preferred
			}
		}
		selection.Models = append(selection.Models, claudeModel{ID: model.ID, DisplayName: model.DisplayName, Effort: effort, Context: context.ContextWindow, Output: context.MaxOutputTokens})
	}
	if claudeSettingsCaps.PerModelEffort {
		byFamily := map[string]string{}
		for _, model := range selection.Models {
			family := claudeEffortKey(model.ID)
			if family == "" {
				continue
			}
			if effort, exists := byFamily[family]; exists && effort != model.Effort {
				return t3CodeProfiles{}, errors.New("Choose one gateway ID per Claude family/version for native reasoning")
			}
			byFamily[family] = model.Effort
		}
	}
	claudePath := filepath.Join(result.ClaudeHome, "settings.json")
	oldClaude, err := read(claudePath)
	if err != nil {
		return t3CodeProfiles{}, err
	}
	// T3 supplies exact model IDs itself. Do not generate native family aliases
	// or modelOverrides: those would change the identity selected in T3.
	claudeConfig, err := mergeClaudeSettings(oldClaude, selection, claudeSettingsCaps, options.Port, options.LocalKey)
	if err != nil {
		return t3CodeProfiles{}, err
	}
	claudeConfig, err = removeT3CodeClaudeToken(claudeConfig)
	if err != nil {
		return t3CodeProfiles{}, err
	}
	selectionJSON, err := json.MarshalIndent(selection, "", "  ")
	if err != nil {
		return t3CodeProfiles{}, err
	}
	for _, item := range []struct {
		path string
		data []byte
	}{
		{filepath.Join(result.CodexHome, "models.json"), append(bytes.TrimSpace(catalog), '\n')},
		{configPath, codexConfig},
		{filepath.Join(result.ClaudeHome, "kilo-models.json"), append(selectionJSON, '\n')},
		{claudePath, claudeConfig},
	} {
		file, err := prepareProfileFile(item.path, item.data)
		if err != nil {
			return t3CodeProfiles{}, err
		}
		result.Files = append(result.Files, file)
	}
	result.Providers = t3CodeProviderInstances(options, result, choices, selection)
	return result, nil
}

func prepareT3CodeProfiles(options t3CodeProfileOptions) (t3CodeProfiles, error) {
	result, err := planT3CodeProfiles(options)
	if err != nil {
		return result, err
	}
	_, err = saveEditorFiles(result.Files)
	return result, err
}

func withinT3CodePath(root, path string) bool {
	root, path = strings.ToLower(filepath.Clean(root)), strings.ToLower(filepath.Clean(path))
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// Resolve directory aliases without creating missing homes or reading any
// provider configuration. Future directories use their closest existing parent.
func t3CodeCanonicalPath(path string) (string, error) {
	tail := []string{}
	for depth := 0; depth < 128; depth++ {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			target, linkErr := os.Readlink(path)
			if linkErr != nil {
				return "", linkErr
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}
			path = filepath.Clean(target)
			continue
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		tail = append(tail, filepath.Base(path))
		path = parent
	}
	return "", errors.New("Too many directory aliases in T3 Code provider homes.")
}

func t3CodeNormalProviderHomes(options t3CodeProfileOptions) (string, string, error) {
	homes := []string{filepath.Join(options.NormalHome, ".codex"), filepath.Join(options.NormalHome, ".claude")}
	for i, name := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		if value := options.NormalEnvironment[name]; value != "" {
			if !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
				return "", "", errors.New("T3 Code normal provider homes must be absolute paths.")
			}
			homes[i] = filepath.Clean(value)
		}
	}
	return homes[0], homes[1], nil
}

func mergeT3CodeCatalogPath(data []byte, catalogPath string) ([]byte, error) {
	before := map[string]any{}
	if err := toml.Unmarshal(data, &before); err != nil {
		return nil, err
	}
	var after map[string]any
	if err := toml.Unmarshal(data, &after); err != nil {
		return nil, err
	}
	after["model_catalog_json"] = catalogPath
	if profile, ok := after["profile"].(string); ok && profile != "" {
		profiles, err := configTable(after, "profiles")
		if err != nil {
			return nil, err
		}
		active, err := configTable(profiles, profile)
		if err != nil {
			return nil, err
		}
		active["model_catalog_json"] = catalogPath
	}
	return editCodexTOML(data, before, after)
}

func removeT3CodeClaudeToken(data []byte) ([]byte, error) {
	settings, err := decodeClaudeSettings(data)
	if err != nil {
		return nil, err
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(settings["env"], &env); err != nil {
		return nil, err
	}
	delete(env, "ANTHROPIC_AUTH_TOKEN")
	settings["env"], err = json.Marshal(env)
	if err != nil {
		return nil, err
	}
	data, err = json.MarshalIndent(settings, "", "  ")
	return append(data, '\n'), err
}

// The validated T3 releases accept this provider-instance envelope and environment records.
// Sensitive values belong to its server secret store; the registration layer
// replaces their payload here with valueRedacted markers before writing JSON.
func t3CodeProviderInstances(options t3CodeProfileOptions, profiles t3CodeProfiles, choices []nativeModelChoice, selection claudeSelection) map[string]any {
	normalCodex, normalClaude, _ := t3CodeNormalProviderHomes(options) // Validated before any profile is planned.
	baseEnv := []map[string]any{}
	allowed := []string{"USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"}
	normalEnv := map[string]string{}
	if runtime.GOOS == "windows" {
		normalEnv["USERPROFILE"] = options.NormalHome
		normalEnv["APPDATA"] = filepath.Join(options.NormalHome, "AppData", "Roaming")
		normalEnv["LOCALAPPDATA"] = filepath.Join(options.NormalHome, "AppData", "Local")
	} else {
		// The T3 window inherits private XDG directories even when none were set
		// in the user's shell. Explicit normal defaults prevent its provider CLIs
		// from inheriting those private UI paths after HOME is restored.
		normalEnv["XDG_CONFIG_HOME"] = filepath.Join(options.NormalHome, ".config")
		normalEnv["XDG_DATA_HOME"] = filepath.Join(options.NormalHome, ".local", "share")
		normalEnv["XDG_CACHE_HOME"] = filepath.Join(options.NormalHome, ".cache")
		normalEnv["XDG_STATE_HOME"] = filepath.Join(options.NormalHome, ".local", "state")
	}
	for _, name := range allowed {
		if value := options.NormalEnvironment[name]; value != "" && !strings.ContainsRune(value, '\x00') {
			normalEnv[name] = value
		}
	}
	baseEnv = append(baseEnv, map[string]any{"name": "HOME", "value": options.NormalHome, "sensitive": false})
	for _, name := range allowed {
		if value, ok := normalEnv[name]; ok {
			baseEnv = append(baseEnv, map[string]any{"name": name, "value": value, "sensitive": false})
		}
	}
	provider := func(driver, displayName string, config map[string]any, secretName string) map[string]any {
		env := append([]map[string]any{}, baseEnv...)
		instance := map[string]any{"driver": driver, "displayName": displayName, "enabled": true, "config": config}
		if secretName != "" {
			env = append(env, map[string]any{"name": secretName, "value": options.LocalKey, "sensitive": true})
			// T3 badges use the first two words of displayName. Prefixing Kilo
			// Proxy yields KP; an accent forces the badge even with one agent.
			instance["accentColor"] = t3CodeProxyAccentColor
		}
		instance["environment"] = env
		return instance
	}
	codex := map[string]any{"setupMode": "existing", "binaryPath": options.CodexBinary, "homePath": profiles.CodexHome, "shadowHomePath": "", "launchArgs": "", "customModels": t3CodeCustomModels(choices, false, options.ClaudeCaps)}
	claudeModels := t3CodeCustomModels(choices, true, options.ClaudeCaps)
	if options.Version == t3CodeNightlyVersion {
		for _, model := range claudeModels {
			model["capabilities"].(map[string]any)["optionDescriptors"] = []map[string]any{}
		}
	}
	claude := map[string]any{"binaryPath": options.ClaudeBinary, "homePath": profiles.ClaudeHome, "launchArgs": "", "customModels": claudeModels}
	if window, _ := claudeContextBudget(selection); window >= 100000 {
		claude["autoCompactWindow"] = strconv.Itoa(window)
	}
	instances := map[string]any{
		t3CodeCodexNormalID:  provider("codex", "Codex · Normal", map[string]any{"setupMode": "existing", "binaryPath": options.CodexBinary, "homePath": normalCodex, "shadowHomePath": "", "launchArgs": "", "customModels": []any{}}, ""),
		t3CodeCodexProxyID:   provider("codex", "Kilo Proxy · Codex", codex, "KILO_LOCAL_API_KEY"),
		t3CodeClaudeNormalID: provider("claudeAgent", "Claude · Normal", map[string]any{"binaryPath": options.ClaudeBinary, "homePath": normalClaude, "launchArgs": "", "customModels": []any{}}, ""),
		t3CodeClaudeProxyID:  provider("claudeAgent", "Kilo Proxy · Claude", claude, "ANTHROPIC_AUTH_TOKEN"),
	}
	claudeInstance := instances[t3CodeClaudeProxyID].(map[string]any)
	claudeInstance["environment"] = append(claudeInstance["environment"].([]map[string]any), map[string]any{"name": "ANTHROPIC_BASE_URL", "value": "http://127.0.0.1:" + strconv.Itoa(options.Port), "sensitive": false})
	return instances
}

func t3CodeCustomModels(choices []nativeModelChoice, claude bool, caps claudeCapabilities) []map[string]any {
	models := make([]map[string]any, 0, len(choices))
	for _, choice := range choices {
		levels, initial := nativeReasoningFor(choice)
		id := "reasoningEffort"
		if claude {
			id = "effort"
			filtered := []string{}
			for _, level := range levels {
				if validClaudeEffort(choice.Model.ID, level) && (level != "xhigh" || caps.PerModelEffort) {
					filtered = append(filtered, level)
				}
			}
			levels = filtered
			if !helperContains(levels, initial) {
				initial = ""
			}
		}
		descriptors := []map[string]any{}
		if len(levels) > 0 {
			options := []map[string]any{}
			for _, level := range levels {
				option := map[string]any{"id": level, "label": strings.ToUpper(level[:1]) + level[1:]}
				if level == initial {
					option["isDefault"] = true
				}
				options = append(options, option)
			}
			descriptor := map[string]any{"id": id, "label": "Reasoning", "type": "select", "options": options}
			if initial != "" {
				descriptor["currentValue"] = initial
			}
			descriptors = append(descriptors, descriptor)
		}
		models = append(models, map[string]any{"slug": choice.Model.ID, "name": nativeCodexDisplayName(choice), "capabilities": map[string]any{"optionDescriptors": descriptors}})
	}
	return models
}

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	t3CodeCustomModelLimit  = 32
	t3CodeCodexNormalID     = "kilo_codex_normal"
	t3CodeCodexProxyID      = "kilo_codex_proxy"
	t3CodeClaudeNormalID    = "kilo_claude_normal"
	t3CodeClaudeProxyID     = "kilo_claude_proxy"
	t3CodeProxyAccentColor  = "#327653"
	t3CodeLiteLLMCodexID    = "kilo_codex_litellm"
	t3CodeLiteLLMClaudeID   = "kilo_claude_litellm"
	t3CodeLiteLLMCodexHome  = "codex-litellm"
	t3CodeLiteLLMClaudeHome = "claude-litellm"
	t3CodeLiteLLMCodexKey   = "KILO_LOCAL_API_KEY"
	t3CodeLiteLLMClaudeKey  = "ANTHROPIC_AUTH_TOKEN"
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
	Platform                                       string
	OpenCodeKiloSupported                          bool
	NormalOpenCodePaths                            []string
	MuseKiloSupported                              bool
	NormalMusePaths                                []string
	ClaudeModelDefaults                            bool
	NormalEnvironment                              map[string]string
	Library                                        modelLibrary
	Catalog                                        []modelInfo
	ClaudeCaps                                     claudeCapabilities
	Port                                           int
	LocalKey                                       string
	Images                                         imageGenerationSettings
	// Explicit opt-in; Synara keeps the existing shared-profile behavior.
	Policy              *t3CodeModelPolicy
	Preferences         *t3CodePreferences
	AdditionalProviders map[string]any
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
	usesKilo := options.Preferences == nil || t3CodeUsesKilo(*options.Preferences)
	kiloCodex, kiloClaude := true, true
	liteLLMCodex, liteLLMClaude := false, false
	normalCodexEnabled, normalClaudeEnabled := true, true
	if options.Preferences != nil {
		prefs := normalizeT3CodePreferences(*options.Preferences)
		kiloCodex, kiloClaude = prefs.KiloCodex, prefs.KiloClaude
		liteLLMCodex, liteLLMClaude = prefs.LiteLLMCodex, prefs.LiteLLMClaude
		normalCodexEnabled, normalClaudeEnabled = prefs.Codex, prefs.Claude
	}
	paths := []string{options.RootDir, options.NormalHome}
	if kiloCodex || liteLLMCodex || normalCodexEnabled {
		paths = append(paths, options.CodexBinary)
	}
	if kiloClaude || liteLLMClaude || normalClaudeEnabled {
		paths = append(paths, options.ClaudeBinary)
	}
	for _, value := range paths {
		if !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
			return result, errors.New("T3 Code requires absolute private-profile, normal-home and CLI paths.")
		}
	}
	usesProxy := usesKilo || liteLLMCodex || liteLLMClaude
	if usesProxy && (options.Port < 1024 || options.Port > 65535 || options.LocalKey == "" || strings.ContainsAny(options.LocalKey, "\x00\r\n")) {
		return result, errors.New("T3 Code requires a valid local proxy port and key.")
	}
	if options.Policy == nil {
		if err := validateT3CodeLibrary(options.Library); err != nil {
			return result, err
		}
	} else if err := validateModelLibrary(options.Library); err != nil {
		return result, err
	}
	if usesKilo && (options.Policy == nil || options.Preferences == nil || t3CodeNeedsSavedLibrary(*options.Preferences)) && len(options.Library.Models) == 0 {
		return result, errors.New("Choose shared models before preparing T3 Code.")
	}
	normalCodex, normalClaude, err := t3CodeNormalProviderHomes(options)
	if err != nil {
		return result, err
	}
	result.CodexHome = filepath.Join(options.RootDir, "profiles", "codex-kilo")
	result.ClaudeHome = filepath.Join(options.RootDir, "profiles", "claude-kilo")
	privateHomes := []string{}
	if kiloCodex {
		privateHomes = append(privateHomes, result.CodexHome)
	}
	if kiloClaude {
		privateHomes = append(privateHomes, result.ClaudeHome)
	}
	if liteLLMCodex {
		privateHomes = append(privateHomes, filepath.Join(options.RootDir, "profiles", t3CodeLiteLLMCodexHome))
	}
	if liteLLMClaude {
		privateHomes = append(privateHomes, filepath.Join(options.RootDir, "profiles", t3CodeLiteLLMClaudeHome))
	}
	normalHomes := []string{normalCodex, normalClaude}
	// The native Claude environment retains its literal path. Keep the
	// historical normalized home check as well as the actual alias traversal.
	if t3CodeNormalProviderEnabled(options, 1) {
		if literal := options.NormalEnvironment["CLAUDE_CONFIG_DIR"]; literal != "" {
			normalHomes = append(normalHomes, literal)
		}
	}
	secure, err := t3CodeNormalClaudeSecureStorageHome(options)
	if err != nil {
		return result, err
	}
	if secure != "" {
		normalHomes = append(normalHomes, secure)
	}
	for index, normal := range normalHomes {
		if index < 2 && !t3CodeNormalProviderEnabled(options, index) {
			continue
		}
		normal, err = t3CodeCanonicalPath(normal)
		if err != nil {
			return t3CodeProfiles{}, errors.New("Cannot safely resolve the normal T3 Code provider home.")
		}
		for _, private := range privateHomes {
			private, err = t3CodeCanonicalPath(private)
			if err != nil {
				return t3CodeProfiles{}, errors.New("Cannot safely resolve the private T3 Code provider home.")
			}
			if withinT3CodePath(normal, private) || withinT3CodePath(private, normal) {
				return t3CodeProfiles{}, errors.New("T3 Code proxy profiles must be separate from normal provider homes.")
			}
		}
	}
	if !usesProxy {
		// Retain stable account IDs so T3 conversations and saved native model
		// selections survive. Disabled Kilo slots need no profiles or secrets.
		result.Providers = t3CodeProviderInstances(options, result, nil, claudeSelection{})
		t3CodeNativeClaudeAuthEnvironment(options, result.Providers)
		for _, id := range []string{t3CodeCodexProxyID, t3CodeClaudeProxyID} {
			instance := result.Providers[id].(map[string]any)
			env := []map[string]any{}
			for _, value := range instance["environment"].([]map[string]any) {
				if value["sensitive"] != true && value["name"] != "ANTHROPIC_BASE_URL" {
					env = append(env, value)
				}
			}
			instance["environment"] = env
		}
		if err := addT3CodeOpenCodeProfile(options, &result); err != nil {
			return t3CodeProfiles{}, err
		}
		if err := addT3CodeMuseProfile(options, &result); err != nil {
			return t3CodeProfiles{}, err
		}
		return result, nil
	}
	for _, dir := range privateHomes {
		if err := safeEditorDir(options.RootDir, dir); err != nil {
			return t3CodeProfiles{}, err
		}
	}
	codexLibrary, claudeLibrary := options.Library, options.Library
	if options.Policy != nil {
		if len(options.Policy.CodexLibrary.Models) > 0 {
			codexLibrary = options.Policy.CodexLibrary
		}
		if len(options.Policy.ClaudeLibrary.Models) > 0 {
			claudeLibrary = options.Policy.ClaudeLibrary
		}
	}
	read := func(path string) ([]byte, error) {
		data, err := readCatalogFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("Cannot safely read the private T3 Code provider settings.")
		}
		return data, nil
	}
	appendFile := func(path string, data []byte) error {
		file, err := prepareProfileFile(path, data)
		if err != nil {
			return err
		}
		result.Files = append(result.Files, file)
		return nil
	}
	var choices []nativeModelChoice
	selection := claudeSelection{}
	if kiloCodex {
		choices = terminalLibraryChoices(codexLibrary, options.Catalog)
		catalog, err := buildCodexCatalog(choices, codexLibrary.DefaultModel, false)
		if err != nil {
			return t3CodeProfiles{}, err
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
		codexConfig, err = mergeT3CodeCatalogPath(codexConfig, filepath.Join(result.CodexHome, "models.json"), "")
		if err != nil {
			return t3CodeProfiles{}, err
		}
		if err := appendFile(filepath.Join(result.CodexHome, "models.json"), append(bytes.TrimSpace(catalog), '\n')); err != nil {
			return t3CodeProfiles{}, err
		}
		if err := appendFile(configPath, codexConfig); err != nil {
			return t3CodeProfiles{}, err
		}
	}
	if kiloClaude {
		claudeChoices := terminalLibraryChoices(claudeLibrary, options.Catalog)
		selection = claudeSelection{Initial: claudeLibrary.DefaultModel, Mode: "installed", Aliases: map[string]string{}}
		claudeSettingsCaps := claudeCapabilities{}
		if options.ClaudeModelDefaults && options.ClaudeCaps.PerModelEffort {
			// T3 V2 discards Claude effort options for custom gateway IDs.
			// Keep the defaults in the private CLI profile without family aliases,
			// a replacement picker or a global effort shared by unrelated models.
			claudeSettingsCaps.PerModelEffort = true
			claudeSettingsCaps.Version = options.ClaudeCaps.Version
		}
		for i, model := range claudeLibrary.Models {
			context, err := contextPolicyForChoice(claudeChoices[i])
			if err != nil {
				return t3CodeProfiles{}, err
			}
			effort := ""
			if options.ClaudeModelDefaults && model.ReasoningEffort != "" && validClaudeEffort(model.ID, model.ReasoningEffort) {
				if !strings.Contains(model.ID, "/") {
					return t3CodeProfiles{}, errors.New("T3 Code requires provider-qualified Claude model IDs (for example anthropic/claude-opus-4-6) for saved reasoning defaults. Use the exact gateway ID in Models.")
				}
				_, preferred := nativeReasoningFor(claudeChoices[i])
				if preferred != "" && validClaudeEffort(model.ID, preferred) {
					if !claudeEffortCompatible(model.ID, options.ClaudeCaps) {
						return t3CodeProfiles{}, managedClaudeEffortVersionError(model.ID, "T3 Code")
					}
					effort = preferred
				}
			}
			selection.Models = append(selection.Models, claudeModel{ID: model.ID, DisplayName: model.DisplayName, Effort: effort, Context: context.ContextWindow, Output: context.MaxOutputTokens})
		}
		settingsSelection := selection
		if options.Policy != nil {
			// T3 passes native slugs to Claude Code. Write effort defaults under
			// those slugs while keeping exact gateway IDs in the saved selection.
			settingsSelection.Initial = t3CodeClaudePickerModelID(selection.Initial, options.Policy.ClaudeRoutes)
			settingsSelection.Models = slices.Clone(selection.Models)
			for i := range settingsSelection.Models {
				model := &settingsSelection.Models[i]
				model.ID = t3CodeClaudePickerModelID(model.ID, options.Policy.ClaudeRoutes)
			}
		}
		if claudeSettingsCaps.PerModelEffort {
			byFamily := map[string]string{}
			for _, model := range settingsSelection.Models {
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
		claudeConfig, err := mergeClaudeSettings(oldClaude, settingsSelection, claudeSettingsCaps, options.Port, options.LocalKey)
		if err != nil {
			return t3CodeProfiles{}, err
		}
		if options.Policy != nil {
			claudeConfig, err = t3CodeNativeClaudeSettings(claudeConfig, *options.Policy, selection.Initial, options.Port)
			if err != nil {
				return t3CodeProfiles{}, err
			}
		}
		claudeConfig, err = t3CodeClaudeProfileSettings(claudeConfig, "")
		if err != nil {
			return t3CodeProfiles{}, err
		}
		selectionJSON, err := json.MarshalIndent(selection, "", "  ")
		if err != nil {
			return t3CodeProfiles{}, err
		}
		if err := appendFile(filepath.Join(result.ClaudeHome, "kilo-models.json"), append(selectionJSON, '\n')); err != nil {
			return t3CodeProfiles{}, err
		}
		if err := appendFile(claudePath, claudeConfig); err != nil {
			return t3CodeProfiles{}, err
		}
	}
	result.Providers = t3CodeProviderInstances(options, result, choices, selection)
	t3CodeNativeClaudeAuthEnvironment(options, result.Providers)
	if liteLLMCodex {
		if err := planT3LiteLLMProfile(options, &result, "codex", read, appendFile); err != nil {
			return t3CodeProfiles{}, err
		}
	}
	if liteLLMClaude {
		if err := planT3LiteLLMProfile(options, &result, "claude", read, appendFile); err != nil {
			return t3CodeProfiles{}, err
		}
	}
	if err := addT3CodeOpenCodeProfile(options, &result); err != nil {
		return t3CodeProfiles{}, err
	}
	if err := addT3CodeMuseProfile(options, &result); err != nil {
		return t3CodeProfiles{}, err
	}
	return result, nil
}

func managedClaudeEffortVersionError(id, client string) error {
	minimum := "2.1.251"
	if claude55ID.MatchString(id) {
		minimum = claude55EffortMinVersion
	}
	return errors.New(client + " requires Claude Code " + minimum + " or newer to apply saved per-model reasoning. Update Claude Code and prepare again.")
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
		parent, base := t3CodeLiteralPathParent(path)
		if base == "" {
			return "", err
		}
		if info, statErr := os.Lstat(parent + base); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			target, linkErr := os.Readlink(parent + base)
			if linkErr != nil {
				return "", linkErr
			}
			if !filepath.IsAbs(target) {
				target = parent + target
			}
			path = target
			continue
		}
		if parent == "" || parent == path {
			return "", err
		}
		tail = append(tail, base)
		path = parent
	}
	return "", errors.New("Too many directory aliases in T3 Code provider homes.")
}

// Split only the final lexical component. filepath.Dir/Clean would collapse
// a preceding symlink/.. before the filesystem has resolved that alias.
func t3CodeLiteralPathParent(path string) (string, string) {
	volume := len(filepath.VolumeName(path))
	end := len(path)
	for end > volume && os.IsPathSeparator(path[end-1]) {
		end--
	}
	if end == volume {
		return path, ""
	}
	for i := end - 1; i >= volume; i-- {
		if os.IsPathSeparator(path[i]) {
			return path[:i+1], path[i+1 : end]
		}
	}
	return "", path[:end]
}

func t3CodeNormalProviderHomes(options t3CodeProfileOptions) (string, string, error) {
	homes := []string{filepath.Join(options.NormalHome, ".codex"), filepath.Join(options.NormalHome, ".claude")}
	for i, name := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		if !t3CodeNormalProviderEnabled(options, i) {
			continue
		}
		if value := options.NormalEnvironment[name]; value != "" {
			if !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
				return "", "", errors.New("T3 Code normal provider homes must be absolute paths.")
			}
			homes[i] = filepath.Clean(value)
		}
	}
	return homes[0], homes[1], nil
}

// Return only the filesystem identity used for isolation checks. Provider
// descriptors retain the original override, including explicit empty values.
func t3CodeNormalClaudeSecureStorageHome(options t3CodeProfileOptions) (string, error) {
	secure, exists := options.NormalEnvironment["CLAUDE_SECURESTORAGE_CONFIG_DIR"]
	if !exists || !t3CodeNormalProviderEnabled(options, 1) {
		return "", nil
	}
	if secure != "" && !filepath.IsAbs(secure) || strings.ContainsAny(secure, "\x00\r\n") {
		return "", errors.New("T3 Code normal Claude secure storage must be an absolute path.")
	}
	if secure == "" {
		secure = filepath.Join(options.NormalHome, ".claude")
	}
	return secure, nil
}

func t3CodeNormalProviderEnabled(options t3CodeProfileOptions, index int) bool {
	// A disabled T3 direct account can still be used by an external CLI.
	// Private proxy writes must stay separate from both normal provider homes.
	if options.Preferences == nil || options.Preferences.KiloCodex || options.Preferences.KiloClaude || options.Preferences.LiteLLMCodex || options.Preferences.LiteLLMClaude {
		return true
	}
	prefs := normalizeT3CodePreferences(*options.Preferences)
	if index == 0 {
		return prefs.Codex
	}
	return prefs.Claude
}

func mergeT3CodeCatalogPath(data []byte, catalogPath, liteLLMBaseURL string) ([]byte, error) {
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
	if liteLLMBaseURL != "" {
		providers, err := configTable(after, "model_providers")
		if err != nil {
			return nil, err
		}
		provider, err := configTable(providers, "kilo-local")
		if err != nil {
			return nil, err
		}
		provider["name"] = "LiteLLM"
		provider["base_url"] = liteLLMBaseURL
		provider["requires_openai_auth"] = false
		provider["env_key_instructions"] = "Open LiteLLM · Codex from the private T3 Code workspace."
	}
	return editCodexTOML(data, before, after)
}

func t3CodeClaudeProfileSettings(data []byte, liteLLMBaseURL string) ([]byte, error) {
	settings, err := decodeClaudeSettings(data)
	if err != nil {
		return nil, err
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(settings["env"], &env); err != nil {
		return nil, err
	}
	delete(env, "ANTHROPIC_AUTH_TOKEN")
	if liteLLMBaseURL != "" {
		env["ANTHROPIC_BASE_URL"], _ = json.Marshal(liteLLMBaseURL)
	}
	settings["env"], err = json.Marshal(env)
	if err != nil {
		return nil, err
	}
	data, err = json.MarshalIndent(settings, "", "  ")
	return append(data, '\n'), err
}

func validateT3LiteLLMLibrary(library modelLibrary) error {
	if len(library.Models) == 0 {
		return errors.New("Choose LiteLLM models before preparing T3 Code.")
	}
	if err := validateT3CodeLibrary(library); err != nil {
		return err
	}
	var namespace string
	for _, model := range library.Models {
		rest, namespaced := strings.CutPrefix(model.ID, liteLLMModelPrefix)
		prefix, alias, found := strings.Cut(rest, "/")
		if !namespaced || !found || len(prefix) != 32 || strings.Trim(prefix, "0123456789abcdef") != "" || alias == "" || namespace != "" && namespace != prefix {
			return errors.New("T3 Code LiteLLM models must belong to one selected gateway.")
		}
		namespace = prefix
	}
	return nil
}

func planT3LiteLLMProfile(options t3CodeProfileOptions, result *t3CodeProfiles, scope string, read func(string) ([]byte, error), appendFile func(string, []byte) error) error {
	if options.Policy == nil {
		return errors.New("Choose LiteLLM models before preparing T3 Code.")
	}
	isClaude := scope == "claude"
	library := options.Policy.LiteLLMCodexLibrary
	id, driver, label := t3CodeLiteLLMCodexID, "codex", "LiteLLM · Codex"
	homeName, keyName, binary := t3CodeLiteLLMCodexHome, t3CodeLiteLLMCodexKey, options.CodexBinary
	if isClaude {
		library = options.Policy.LiteLLMClaudeLibrary
		id, driver, label = t3CodeLiteLLMClaudeID, "claudeAgent", "LiteLLM · Claude"
		homeName, keyName, binary = t3CodeLiteLLMClaudeHome, t3CodeLiteLLMClaudeKey, options.ClaudeBinary
	}
	if err := validateT3LiteLLMLibrary(library); err != nil {
		return err
	}
	home := filepath.Join(options.RootDir, "profiles", homeName)
	base := "http://127.0.0.1:" + strconv.Itoa(options.Port) + t3LiteLLMProxyPrefix + scope
	choices := terminalLibraryChoices(library, options.Catalog)
	config := map[string]any{"binaryPath": binary, "homePath": home, "launchArgs": "", "customModels": t3CodeCustomModels(choices, isClaude, options.ClaudeCaps)}
	environment := t3CodeNormalEnvironment(home, nil, options.Platform)
	if isClaude {
		// With no native aliases, the CLI's built-in names also resolve to
		// the prepared initial LiteLLM model.
		selection := claudeSelection{Initial: library.DefaultModel, Mode: "installed", Aliases: map[string]string{}}
		for _, choice := range choices {
			limits, err := contextPolicyForChoice(choice)
			if err != nil {
				return err
			}
			selection.Models = append(selection.Models, claudeModel{ID: choice.Model.ID, DisplayName: nativeCodexDisplayName(choice), Context: limits.ContextWindow, Output: limits.MaxOutputTokens})
		}
		if window, _ := claudeContextBudget(selection); window >= 100000 {
			config["autoCompactWindow"] = strconv.Itoa(window)
		}
		configPath := filepath.Join(home, "settings.json")
		previous, err := read(configPath)
		if err != nil {
			return err
		}
		settings, err := mergeClaudeSettings(previous, selection, claudeCapabilities{}, options.Port, "")
		if err != nil {
			return err
		}
		settings, err = t3CodeClaudeProfileSettings(settings, base)
		if err != nil {
			return err
		}
		models, err := json.MarshalIndent(selection, "", "  ")
		if err != nil {
			return err
		}
		if err := appendFile(filepath.Join(home, "kilo-models.json"), append(models, '\n')); err != nil {
			return err
		}
		if err := appendFile(configPath, settings); err != nil {
			return err
		}
		environment["ANTHROPIC_BASE_URL"] = base
		environment["CLAUDE_SECURESTORAGE_CONFIG_DIR"] = home
	} else {
		config["setupMode"], config["shadowHomePath"] = "existing", ""
		catalog, err := buildCodexCatalog(choices, library.DefaultModel, false)
		if err != nil {
			return err
		}
		configPath, catalogPath := filepath.Join(home, "config.toml"), filepath.Join(home, "models.json")
		previous, err := read(configPath)
		if err != nil {
			return err
		}
		settings, err := mergeCodexConfig(previous, catalog, options.Port)
		if err != nil {
			return err
		}
		settings, err = mergeT3CodeCatalogPath(settings, catalogPath, base+"/v1")
		if err != nil {
			return err
		}
		if err := appendFile(catalogPath, append(bytes.TrimSpace(catalog), '\n')); err != nil {
			return err
		}
		if err := appendFile(configPath, settings); err != nil {
			return err
		}
	}
	env := []map[string]any{}
	for _, name := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "ANTHROPIC_BASE_URL", "CLAUDE_SECURESTORAGE_CONFIG_DIR"} {
		if value, ok := environment[name]; ok {
			env = append(env, map[string]any{"name": name, "value": value, "sensitive": false})
		}
	}
	env = append(env, map[string]any{"name": keyName, "value": t3LiteLLMProxyToken(options.LocalKey, scope), "sensitive": true})
	result.Providers[id] = map[string]any{"driver": driver, "displayName": label, "enabled": true, "config": config, "environment": env, "accentColor": t3CodeProxyAccentColor}
	return nil
}

// The validated T3 releases accept this provider-instance envelope and environment records.
// Sensitive values belong to its server secret store; the registration layer
// replaces their payload here with valueRedacted markers before writing JSON.
func t3CodeProviderInstances(options t3CodeProfileOptions, profiles t3CodeProfiles, choices []nativeModelChoice, selection claudeSelection) map[string]any {
	normalCodex, _, _ := t3CodeNormalProviderHomes(options) // Validated before any profile is planned.
	// T3 resolves a nonempty homePath before exporting CLAUDE_CONFIG_DIR.
	// Keep the native descriptor unset; intentional overrides belong to its
	// per-instance environment so their authentication namespace stays literal.
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
	claudeChoices := choices
	if options.Policy != nil {
		claudeChoices = terminalLibraryChoices(options.Policy.ClaudeLibrary, options.Catalog)
	}
	claudeModels := t3CodeCustomModels(claudeChoices, true, options.ClaudeCaps)
	if options.Policy != nil {
		custom := make([]map[string]any, 0, len(claudeModels))
		for _, model := range claudeModels {
			id, _ := model["slug"].(string)
			if native := t3CodeNativeClaudeModelID(id); native != "" && options.Policy.ClaudeRoutes[native] == id {
				continue
			}
			custom = append(custom, model)
		}
		claudeModels = custom
	}
	if options.ClaudeModelDefaults {
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
		t3CodeClaudeNormalID: provider("claudeAgent", "Claude · Normal", map[string]any{"binaryPath": options.ClaudeBinary, "homePath": "", "launchArgs": "", "customModels": []any{}}, ""),
		t3CodeClaudeProxyID:  provider("claudeAgent", "Kilo Proxy · Claude", claude, "ANTHROPIC_AUTH_TOKEN"),
	}
	claudeInstance := instances[t3CodeClaudeProxyID].(map[string]any)
	claudeInstance["environment"] = append(claudeInstance["environment"].([]map[string]any), map[string]any{"name": "ANTHROPIC_BASE_URL", "value": "http://127.0.0.1:" + strconv.Itoa(options.Port), "sensitive": false})
	if options.Preferences != nil {
		prefs := normalizeT3CodePreferences(*options.Preferences)
		for id, enabled := range map[string]bool{t3CodeCodexNormalID: prefs.Codex, t3CodeClaudeNormalID: prefs.Claude, t3CodeCodexProxyID: prefs.KiloCodex, t3CodeClaudeProxyID: prefs.KiloClaude} {
			instance := instances[id].(map[string]any)
			instance["enabled"] = enabled
			if !enabled && (id == t3CodeCodexProxyID || id == t3CodeClaudeProxyID) {
				// Keep the account identity for existing conversations, without
				// planning or inspecting the disabled account's previous secret.
				env := []map[string]any{}
				for _, value := range instance["environment"].([]map[string]any) {
					if value["sensitive"] != true && value["name"] != "ANTHROPIC_BASE_URL" {
						env = append(env, value)
					}
				}
				instance["environment"] = env
			}
		}
		instances[t3CodeCodexNormalID].(map[string]any)["displayName"] = "Codex"
		instances[t3CodeClaudeNormalID].(map[string]any)["displayName"] = "Claude"
		claudeEnv := claudeInstance["environment"].([]map[string]any)
		for _, item := range claudeEnv {
			if item["name"] == "ANTHROPIC_BASE_URL" {
				item["value"] = "http://127.0.0.1:" + strconv.Itoa(options.Port) + "/t3-code"
			}
		}
		for id, provider := range options.AdditionalProviders {
			instances[id] = provider
		}
	}
	return instances
}

// Synara shares provider construction but owns its separate secure-storage
// descriptor. Apply this environment only to profiles planned for T3 itself.
func t3CodeNativeClaudeAuthEnvironment(options t3CodeProfileOptions, instances map[string]any) {
	if options.Preferences != nil && !normalizeT3CodePreferences(*options.Preferences).Claude {
		return
	}
	instance := instances[t3CodeClaudeNormalID].(map[string]any)
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR"} {
		if value, exists := options.NormalEnvironment[name]; exists {
			instance["environment"] = append(instance["environment"].([]map[string]any), map[string]any{"name": name, "value": value, "sensitive": false})
		}
	}
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
				if validClaudeEffort(choice.Model.ID, level) && (level != "xhigh" || caps.PerModelEffort) && (!claude55ID.MatchString(choice.Model.ID) || claudeEffortCompatible(choice.Model.ID, caps)) {
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

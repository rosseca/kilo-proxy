package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

func deriveSynaraModelPolicy(prefs synaraPreferences, library modelLibrary, catalog []modelInfo) (t3CodeModelPolicy, error) {
	policy, err := deriveT3CodeModelPolicyWithClaude(synaraT3Preferences(prefs), library, catalog, nil, func() (modelLibrary, error) {
		return synaraOfficialClaudeLibrary(library, catalog)
	})
	if err != nil {
		return policy, errors.New(strings.ReplaceAll(err.Error(), "T3 Code", "Synara"))
	}
	return policy, nil
}

func synaraOfficialClaudeLibrary(library modelLibrary, catalog []modelInfo) (modelLibrary, error) {
	result := emptyModelLibrary()
	for _, official := range t3CodeOfficialClaudeModels {
		var match *modelInfo
		for i := range catalog {
			parts := t3CodeClaudeGatewayID.FindStringSubmatch(catalog[i].ID)
			if parts == nil || strings.ReplaceAll(parts[1], ".", "-") != official.Slug {
				continue
			}
			if match != nil && match.ID != catalog[i].ID {
				return result, fmt.Errorf("T3 Code Claude has multiple gateway IDs for %s. Choose an exact shared selection instead.", official.Name)
			}
			match = &catalog[i]
		}
		if match == nil {
			return result, fmt.Errorf("%s is unavailable in the connected catalog. Refresh the catalog, choose shared models, or disable Claude · Kilo.", official.Name)
		}
		result.Models = append(result.Models, t3CodeCatalogLibraryItem(*match, library))
	}
	result.DefaultModel = t3CodeEffectiveDefault(result, library.DefaultModel)
	return result, nil
}

func synaraOptionsPolicy(options synaraProfileOptions) t3CodeModelPolicy {
	if options.Policy != nil {
		return *options.Policy
	}
	routes, _ := t3CodeBuildClaudeRoutes(options.Library)
	return t3CodeModelPolicy{CodexLibrary: options.Library, ClaudeLibrary: options.Library, ClaudeRoutes: routes}
}

func synaraModelSummaries(library modelLibrary) []synaraModelSummary {
	result := []synaraModelSummary{}
	for _, m := range library.Models {
		result = append(result, synaraModelSummary{ID: m.ID, Name: m.DisplayName, ReasoningEffort: m.ReasoningEffort})
	}
	return result
}

// Preserve valid direct-account selections; only our disabled/changed Kilo
// selection is replaced. Nil means no active model was verified; the settings
// writer separately chooses a safe disabled selection or rejects preparation.
func synaraDefaultSelection(options synaraProfileOptions, providers map[string]any, raw []byte) any {
	if options.Preferences == nil {
		return map[string]any{"provider": "codex", "instanceId": synaraCodexProxyID, "model": options.Library.DefaultModel}
	}
	var current any
	_ = json.Unmarshal(raw, &current)
	selected := t3CodeDefaultSelection(current, providers, synaraOptionsPolicy(options))
	if object, ok := selected.(map[string]any); ok {
		copy := map[string]any{}
		for key, value := range object {
			copy[key] = value
		}
		id, _ := copy["instanceId"].(string)
		if instance, ok := providers[id].(map[string]any); ok {
			copy["provider"] = instance["driver"]
		}
		return copy
	}
	// Synara's native text-generation resolver needs an exact enabled account.
	// Optional native accounts need the same exact imported default as core
	// accounts. A null selection cannot identify an enabled account upstream.
	for _, id := range append([]string{synaraCodexNormalID, synaraClaudeNormalID}, synaraDrivers()...) {
		if instance, ok := providers[id].(map[string]any); ok && instance["enabled"] == true {
			model := options.NativeDefaults[id]
			if model == "" {
				continue
			}
			return map[string]any{"provider": instance["driver"], "instanceId": id, "model": model}
		}
	}
	return nil
}

// Synara's settings schema requires a selection even when every account is
// off. The server may replace a disabled selection with any enabled dedicated
// account, including retained accounts outside Kilo's four managed slots.
func synaraSettingsSelectionChecked(options synaraProfileOptions, providers map[string]any, settings map[string]json.RawMessage) (any, error) {
	if options.Preferences == nil {
		return synaraDefaultSelection(options, providers, settings["textGenerationModelSelection"]), nil
	}
	fail := errors.New("Choose a verified model for an enabled Synara text generation account before preparing again. Its saved settings have not been changed.")
	var legacy map[string]map[string]any
	var all map[string]map[string]any
	if json.Unmarshal(settings["providers"], &legacy) != nil || json.Unmarshal(settings["providerInstances"], &all) != nil {
		return nil, fail
	}
	// Match deriveProviderInstanceConfigMap's built-in legacy/config merge.
	// Preparation clears legacy Codex accounts before reaching this point.
	effective := map[string]any{}
	for _, driver := range synaraDrivers() {
		config := map[string]any{}
		for key, value := range legacy[driver] {
			config[key] = value
		}
		effective[driver] = map[string]any{"driver": driver, "enabled": config["enabled"] != false, "config": config}
	}
	for id, instance := range all {
		if strings.HasPrefix(id, "synara_unresolved_automation_") {
			continue
		}
		driver, _ := instance["driver"].(string)
		config := map[string]any{}
		enabled := instance["enabled"] != false
		if helperContains(synaraDrivers(), id) {
			if driver != id {
				continue // Upstream ignores retargeted canonical built-in ids.
			}
			for key, value := range legacy[driver] {
				config[key] = value
			}
			if _, exists := instance["enabled"]; !exists {
				enabled = config["enabled"] != false
			}
		}
		if raw, ok := instance["config"].(map[string]any); ok {
			for key, value := range raw {
				config[key] = value
			}
		}
		effective[id] = map[string]any{"driver": driver, "enabled": enabled && config["enabled"] != false, "config": config}
	}
	dedicated := func(driver string) bool {
		return helperContains([]string{"codex", "claudeAgent", "cursor", "opencode", "droid"}, driver)
	}
	unsafeFallback := false
	for _, raw := range effective {
		instance := raw.(map[string]any)
		driver, _ := instance["driver"].(string)
		if instance["enabled"] == true && (dedicated(driver) || !helperContains(synaraDrivers(), driver)) {
			unsafeFallback = true
		}
	}
	selected := synaraDefaultSelection(options, effective, settings["textGenerationModelSelection"])
	if object, ok := selected.(map[string]any); ok {
		driver, _ := object["provider"].(string)
		if !helperContains(synaraDrivers(), driver) {
			return nil, fail
		}
		if dedicated(driver) || !unsafeFallback {
			return object, nil
		}
	}
	if unsafeFallback {
		// A chat-only choice is not a text-generation route. Bind an enabled
		// owned account only when its installed default was actually verified.
		for _, id := range append([]string{synaraCodexNormalID, synaraClaudeNormalID}, synaraDrivers()...) {
			instance, ok := effective[id].(map[string]any)
			driver, _ := instance["driver"].(string)
			if ok && instance["enabled"] == true && dedicated(driver) && options.NativeDefaults[id] != "" {
				return map[string]any{"provider": driver, "instanceId": id, "model": options.NativeDefaults[id]}, nil
			}
		}
		return nil, fail
	}
	instance, ok := effective[synaraCodexProxyID].(map[string]any)
	if !ok || instance["driver"] != "codex" || instance["enabled"] != false {
		return nil, fail
	}
	// "default" is only a schema-valid dormant value, never a listed model.
	// With no enabled dedicated fallback, upstream retains this disabled
	// account and resolveInvocation rejects it before selecting a runtime.
	return map[string]any{"provider": "codex", "instanceId": synaraCodexProxyID, "model": "default"}, nil
}

func (a *app) synaraAvailabilityFor(binary string, rt clientLaunchRuntime, prefs synaraPreferences) error {
	if _, err := synaraPackageMetadata(binary, rt.platform); err != nil {
		return err
	}
	if err := a.synaraSystemRequirement(rt.platform); err != nil {
		return err
	}
	prefs = normalizeSynaraPreferences(prefs)
	if prefs.Codex || prefs.KiloCodex {
		if _, err := resolveOpenDesignCLI("codex-cli", rt); err != nil {
			return errors.New("Install a native Codex CLI executable before preparing Synara.")
		}
	}
	if prefs.Claude || prefs.KiloClaude {
		if _, err := rt.resolve("claude", ""); err != nil {
			return errors.New("Install Claude Code CLI before preparing Synara. Claude Desktop is a separate application.")
		}
	}
	return a.synaraAdditionalAvailability(binary, rt, prefs, false)
}

func (a *app) synaraOptions(rt clientLaunchRuntime, library modelLibrary) synaraProfileOptions {
	options, _ := a.synaraOptionsChecked(rt, library)
	return options
}

func (a *app) synaraOptionsChecked(rt clientLaunchRuntime, library modelLibrary) (synaraProfileOptions, error) {
	paths := synaraPaths(a.dir)
	prefs := normalizeSynaraPreferences(a.config.Synara)
	codex, claude := filepath.Join(paths.Root, "disabled-codex"), filepath.Join(paths.Root, "disabled-claude")
	if prefs.Codex || prefs.KiloCodex {
		codex, _ = resolveOpenDesignCLI("codex-cli", rt)
	}
	if prefs.Claude || prefs.KiloClaude {
		claude, _ = rt.resolve("claude", "")
	}
	options := synaraProfileOptions{RootDir: paths.Root, DataDir: paths.Data, UserDataDir: paths.Electron, NormalHome: rt.home, Platform: rt.platform, CodexBinary: codex, NormalCodexBinary: synaraCodexNormalShimPath(paths.Root, rt.platform), ClaudeBinary: claude, NormalEnvironment: synaraNormalEnvironment(rt.home, os.Environ(), rt.platform), Library: library, Catalog: readNativeCatalogCache(a.dir, a.catalogScopeLocked()), Port: a.config.Port, LocalKey: a.config.LocalKey, Images: a.clientImageSettingsLocked()}
	if a.config.Synara.Version != 0 {
		options.Preferences = &prefs
		policy, err := deriveSynaraModelPolicy(prefs, library, options.Catalog)
		if err != nil {
			return options, err
		}
		options.Policy = &policy
	}
	if !synaraUsesKilo(prefs) {
		options.Library = emptyModelLibrary()
	}
	if !prefs.KiloCodex && !prefs.KiloClaude {
		if !prefs.Codex {
			delete(options.NormalEnvironment, "CODEX_HOME")
		}
		if !prefs.Claude {
			delete(options.NormalEnvironment, "CLAUDE_CONFIG_DIR")
			delete(options.NormalEnvironment, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
		}
	}
	if options.Preferences == nil {
		// Legacy profiles have no optional providers to inspect. Keep their
		// selected Synara executable inside the existing launch transaction.
		return options, nil
	}
	binary, err := rt.resolve("synara", "")
	if err != nil {
		return options, err
	}
	if err = a.populateSynaraAdditionalOptions(&options, rt, binary); err != nil {
		return options, err
	}
	return options, nil
}

func (a *app) synaraFingerprint(binary string, library modelLibrary, rt clientLaunchRuntime) string {
	if a.config.Synara.Version == 0 {
		return a.synaraLegacyFingerprint(binary, library, rt)
	}
	options, optionsErr := a.synaraOptionsChecked(rt, library)
	if optionsErr != nil {
		return ""
	}
	prefs := *options.Preferences
	policy, err := deriveSynaraModelPolicy(prefs, options.Library, options.Catalog)
	if err != nil {
		return ""
	}
	t3prefs := synaraCoreT3Preferences(prefs)
	normalCodex, normalClaude, err := t3CodeNormalProviderHomes(t3CodeProfileOptions{NormalHome: rt.home, NormalEnvironment: options.NormalEnvironment, Preferences: &t3prefs})
	if err != nil {
		return ""
	}
	identities := []string{}
	for i, path := range []string{normalCodex, normalClaude} {
		if !prefs.KiloCodex && !prefs.KiloClaude && (i == 0 && !prefs.Codex || i == 1 && !prefs.Claude) {
			continue
		}
		canonical, err := t3CodeCanonicalPath(path)
		if err != nil {
			return ""
		}
		identities = append(identities, canonical)
	}
	if prefs.KiloCodex || prefs.KiloClaude || prefs.Claude {
		secure, err := synaraNormalSecureStorage(t3CodeProfileOptions{RootDir: options.RootDir, NormalHome: rt.home, NormalEnvironment: options.NormalEnvironment, Preferences: &t3prefs})
		if err != nil {
			return ""
		}
		if secure == "" {
			secure = filepath.Join(rt.home, ".claude")
		}
		canonical, err := t3CodeCanonicalPath(secure)
		if err != nil {
			return ""
		}
		identities = append(identities, canonical)
	}
	adapterHash := ""
	if prefs.Codex {
		adapterHash, err = a.synaraAdapterSourceHash()
		if err != nil {
			return ""
		}
	}
	additional, err := synaraAdditionalFingerprint(options, rt)
	if err != nil {
		return ""
	}
	values := []any{additional, binary, options.CodexBinary, options.ClaudeBinary, prefs, rt.home, options.NormalEnvironment, identities, adapterHash, synaraProfileRevision}
	if synaraUsesKilo(prefs) {
		values = append(values, library, policy, terminalLibraryChoices(policy.CodexLibrary, options.Catalog), terminalLibraryChoices(policy.ClaudeLibrary, options.Catalog), a.config.Port, a.config.LocalKey, a.config.OrgID, a.catalogScopeLocked(), a.clientImageSettingsLocked())
	}
	data, _ := json.Marshal(values)
	return openDesignHash(data)
}

func synaraDriverEnabled(providers map[string]any, driver string) bool {
	for _, raw := range providers {
		instance, ok := raw.(map[string]any)
		if ok && instance["driver"] == driver && instance["enabled"] == true {
			return true
		}
	}
	return false
}

func synaraBaseOptions(options synaraProfileOptions) t3CodeProfileOptions {
	base := t3CodeProfileOptions{RootDir: options.RootDir, NormalHome: options.NormalHome, CodexBinary: options.CodexBinary, ClaudeBinary: options.ClaudeBinary, NormalEnvironment: options.NormalEnvironment, Library: options.Library, Catalog: options.Catalog, ClaudeCaps: options.ClaudeCaps, ClaudeModelDefaults: true, Port: options.Port, LocalKey: options.LocalKey, Images: options.Images, Policy: options.Policy, Platform: options.Platform}
	if options.Preferences != nil {
		prefs := synaraCoreT3Preferences(*options.Preferences)
		base.Preferences = &prefs
	}
	return base
}

func validateSynaraCoreProviders(saved synaraPrepared) error {
	if saved.Preferences == nil {
		return nil
	}
	prefs := *saved.Preferences
	for _, entry := range []struct {
		id, driver string
		enabled    bool
	}{{synaraCodexNormalID, "codex", prefs.Codex}, {synaraClaudeNormalID, "claudeAgent", prefs.Claude}, {synaraCodexProxyID, "codex", prefs.KiloCodex}, {synaraClaudeProxyID, "claudeAgent", prefs.KiloClaude}} {
		instance, ok := saved.Providers[entry.id].(map[string]any)
		if !ok || instance["driver"] != entry.driver || instance["enabled"] != entry.enabled {
			return errors.New("Invalid private Synara agent selection.")
		}
	}
	return nil
}

// Rebuild only public registration data. No profile planning, filesystem writes,
// credential reads or CLI probes are permitted during readiness checks.
func synaraCoreProvidersReady(options synaraProfileOptions, actual map[string]any) bool {
	profiles := synaraProfiles{CodexHome: filepath.Join(options.RootDir, "profiles", "codex-kilo"), ClaudeHome: filepath.Join(options.RootDir, "profiles", "claude-kilo")}
	expected, err := synaraProviderInstances(options, profiles, synaraBaseOptions(options))
	if err != nil {
		return false
	}
	for id, raw := range expected {
		instance := raw.(map[string]any)
		for _, raw := range instance["environment"].([]any) {
			variable := raw.(map[string]any)
			if variable["sensitive"] == true {
				variable["value"] = ""
				variable["valueRedacted"] = true
			}
		}
		if !reflect.DeepEqual(instance, actual[id]) {
			return false
		}
	}
	return true
}

func validateSynaraPreparedFiles(saved synaraPrepared) error {
	if saved.Preferences == nil {
		return nil
	}
	prefs := *saved.Preferences
	expected := map[string]bool{synaraComposerMarkerName: true}
	for _, entry := range []struct {
		enabled bool
		paths   []string
	}{{prefs.KiloCodex, []string{filepath.Join("profiles", "codex-kilo", "config.toml"), filepath.Join("profiles", "codex-kilo", "models.json"), filepath.Join("data", "userdata", "secrets", t3CodeSecretName(synaraCodexProxyID, "KILO_LOCAL_API_KEY"))}}, {prefs.KiloClaude, []string{filepath.Join("profiles", "claude-kilo", "settings.json"), filepath.Join("profiles", "claude-kilo", "kilo-models.json"), filepath.Join("data", "userdata", "secrets", t3CodeSecretName(synaraClaudeProxyID, "ANTHROPIC_AUTH_TOKEN"))}}} {
		if entry.enabled {
			for _, path := range entry.paths {
				expected[path] = true
			}
		}
	}
	if prefs.Codex {
		provider, ok := saved.Providers[synaraCodexNormalID].(map[string]any)
		if !ok {
			return errors.New("Invalid private Synara active-profile manifest.")
		}
		config, ok := provider["config"].(map[string]any)
		if !ok {
			return errors.New("Invalid private Synara active-profile manifest.")
		}
		binary, _ := config["binaryPath"].(string)
		name := filepath.Base(binary)
		if name != synaraCodexNormalShimName && name != synaraCodexNormalShimName+".exe" {
			return errors.New("Invalid private Synara active-profile manifest.")
		}
		expected[filepath.Join("adapters", name)] = true
		expected[filepath.Join("adapters", synaraCodexNormalDescriptorName)] = true
	}
	if synaraOpenCodeKiloEnabled(prefs) {
		_, config := t3CodeOpenCodePaths("", prefs.OpenCode2)
		expected[config] = true
		expected[filepath.Join("data", "userdata", "secrets", t3CodeSecretName(synaraOpenCodeProxyID, synaraOpenCodeKey))] = true
	}
	if len(expected) != len(saved.Files) {
		return errors.New("Invalid private Synara active-profile manifest.")
	}
	for path := range expected {
		if saved.Files[path] == "" {
			return errors.New("Invalid private Synara active-profile manifest.")
		}
	}
	return nil
}

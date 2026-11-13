package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"
)

const t3CodeProfileEndpoint = "/api/clients/t3-code"
const t3CodeProfileRevision = 9

type t3CodeManagedPaths struct{ Root, Data, UIHome, Settings, ClientSettings, Secrets, Selection string }

func t3CodePaths(appDir string) t3CodeManagedPaths {
	root := filepath.Join(appDir, "t3-code")
	data := filepath.Join(root, "data")
	return t3CodeManagedPaths{Root: root, Data: data, UIHome: filepath.Join(root, "ui-home"), Settings: filepath.Join(data, "userdata", "settings.json"), ClientSettings: filepath.Join(data, "userdata", "client-settings.json"), Secrets: filepath.Join(data, "userdata", "secrets"), Selection: filepath.Join(root, "selection.json")}
}

type t3CodePrepared struct {
	Runtime             t3CodePrivateRuntime `json:"runtime"`
	ClaudeBuiltins      []string             `json:"claudeBuiltins"`
	Preferences         t3CodePreferences    `json:"preferences"`
	CodexLibrary        modelLibrary         `json:"codexLibrary"`
	ClaudeLibrary       modelLibrary         `json:"claudeLibrary"`
	OpenCodeLibrary     modelLibrary         `json:"openCodeLibrary"`
	MuseLibrary         modelLibrary         `json:"museLibrary"`
	MuseBinary          museBinaryIdentity   `json:"museBinary"`
	MuseRelease         string               `json:"museRelease"`
	ClaudeRoutes        map[string]string    `json:"claudeRoutes"`
	Library             modelLibrary         `json:"library"`
	Version             string               `json:"version"`
	ProtocolV2          bool                 `json:"protocolV2"`
	ClaudeModelDefaults bool                 `json:"claudeModelDefaults"`
	Fingerprint         string               `json:"fingerprint"`
	Providers           map[string]any       `json:"providers"`
	Files               map[string]string    `json:"files"`
}

type t3CodeModelSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

func t3CodeUsesKilo(prefs t3CodePreferences) bool {
	prefs = normalizeT3CodePreferences(prefs)
	return prefs.KiloCodex || prefs.KiloClaude || t3CodeOpenCodeKiloEnabled(prefs) || t3CodeMuseKiloEnabled(prefs)
}

func t3CodeNeedsSavedLibrary(prefs t3CodePreferences) bool {
	prefs = normalizeT3CodePreferences(prefs)
	return prefs.KiloCodex || t3CodeOpenCodeKiloEnabled(prefs) ||
		prefs.KiloClaude && prefs.ClaudeModels == "shared" ||
		t3CodeMuseKiloEnabled(prefs) && prefs.MuseModels == "shared"
}

func t3CodeUsesProviderCatalog(prefs t3CodePreferences) bool {
	prefs = normalizeT3CodePreferences(prefs)
	return prefs.KiloClaude && prefs.ClaudeModels == "official" ||
		t3CodeMuseKiloEnabled(prefs) && prefs.MuseModels == "official"
}

func t3CodeOptionalSavedLibrary(state modelLibraryState) modelLibrary {
	if state.RecoveryRequired || state.Warning != "" || validateModelLibrary(state.Library) != nil {
		return emptyModelLibrary()
	}
	return cloneModelLibrary(state.Library)
}

// A request validates an immutable selection without holding the mutex used by
// the native UI and /api/state. Files and executable identities are still read
// on every request; this is not a readiness cache.
type t3CodeValidationInput struct {
	Dir, LocalKey, OrgID, CatalogScope string
	KiloAccount                        string
	Port                               int
	Preferences                        t3CodePreferences
	Images                             imageGenerationSettings
	CatalogRevision                    uint64
	KiloReady                          bool
	CLISelections                      [5]string
	Environment                        []string
}

// Caller holds app.mu. Provider slices are cloned by normalization.
func (a *app) t3CodeValidationInputLocked() t3CodeValidationInput {
	return t3CodeValidationInput{
		Dir: a.dir, Port: a.config.Port, LocalKey: a.config.LocalKey,
		OrgID: a.config.OrgID, CatalogScope: a.catalogScopeLocked(),
		Preferences: normalizeT3CodePreferences(a.config.T3Code),
		Images:      a.clientImageSettingsLocked(), CatalogRevision: a.catalogRevision, KiloReady: a.kiloReadyLocked(),
		KiloAccount: usageAccountID(a.apiKey, a.config.OrgID),
		CLISelections: [5]string{a.cliExecutable("codex-cli"), a.cliExecutable("claude"),
			a.cliExecutable("muse"), a.openCodeExecutable("opencode"), a.openCodeExecutable("opencode2")},
		Environment: os.Environ(),
	}
}

func (input t3CodeValidationInput) catalog() []modelInfo {
	cache := readNativeCachedCatalog(input.Dir, input.CatalogScope)
	if !t3CodeUsesProviderCatalog(input.Preferences) || cache.KiloAccount == input.KiloAccount {
		return cache.Models
	}
	var models []modelInfo
	for _, model := range cache.Models {
		if model.Connection == "chatgpt" {
			models = append(models, model)
		}
	}
	return models
}

func (a *app) t3CodeFingerprint(binary, version string, library modelLibrary, rt clientLaunchRuntime) string {
	input := a.t3CodeValidationInputLocked()
	installation, err := inspectT3CodeInstallation(binary, rt.platform)
	if err != nil {
		return ""
	}
	return t3CodeFingerprintFrom(input, installation, binary, version, library, input.catalog(), rt, nil)
}

func t3CodeFingerprintFrom(input t3CodeValidationInput, installation t3CodeInstallation, binary, version string, library modelLibrary, catalog []modelInfo, rt clientLaunchRuntime, prepared *t3CodePrepared) string {
	prefs := input.Preferences
	codex, claude := "", ""
	if prefs.Codex || prefs.KiloCodex {
		codex, _ = resolveOpenDesignCLI("codex-cli", rt)
	}
	if prefs.Claude || prefs.KiloClaude {
		claude, _ = rt.resolve("claude", "")
	}
	normalEnvironment := t3CodeNormalEnvironment(rt.home, input.Environment, rt.platform)
	if !prefs.KiloCodex && !prefs.KiloClaude {
		if !prefs.Codex {
			delete(normalEnvironment, "CODEX_HOME")
		}
		if !prefs.Claude {
			delete(normalEnvironment, "CLAUDE_CONFIG_DIR")
			delete(normalEnvironment, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
		}
	}
	normalCodex, normalClaude, err := t3CodeNormalProviderHomes(t3CodeProfileOptions{NormalHome: rt.home, NormalEnvironment: normalEnvironment, Preferences: &prefs})
	if err != nil {
		return ""
	}
	canonicalCodex, canonicalClaude := "", ""
	if prefs.KiloCodex || prefs.KiloClaude || prefs.Codex {
		canonicalCodex, err = t3CodeCanonicalPath(normalCodex)
		if err != nil {
			return ""
		}
	}
	if prefs.KiloCodex || prefs.KiloClaude || prefs.Claude {
		canonicalClaude, err = t3CodeCanonicalPath(normalClaude)
		if err != nil {
			return ""
		}
	}
	canonicalClaudeLiteral := ""
	if t3CodeNormalProviderEnabled(t3CodeProfileOptions{Preferences: &prefs}, 1) {
		if literal := normalEnvironment["CLAUDE_CONFIG_DIR"]; literal != "" {
			canonicalClaudeLiteral, err = t3CodeCanonicalPath(literal)
			if err != nil {
				return ""
			}
		}
	}
	secureHome, err := t3CodeNormalClaudeSecureStorageHome(t3CodeProfileOptions{NormalHome: rt.home, NormalEnvironment: normalEnvironment, Preferences: &prefs})
	if err != nil {
		return ""
	}
	canonicalSecure := ""
	if secureHome != "" {
		canonicalSecure, err = t3CodeCanonicalPath(secureHome)
		if err != nil {
			return ""
		}
	}
	// A retained symlink spelling can resolve to a different provider home
	// after preparation. Record its resolved directory identity as well.
	if installation.Version != version {
		return ""
	}
	additional, err := planT3CodeOptionalProviders(prefs, installation, rt, t3CodePaths(input.Dir).Root, false)
	if err != nil {
		return ""
	}
	policy, err := deriveT3CodeCatalogPolicy(prefs, library, catalog, installation, prepared)
	if err != nil {
		return ""
	}
	openCodeProvider, _, _, err := planT3CodeOpenCode(t3CodeProfileOptions{Preferences: &prefs, RootDir: t3CodePaths(input.Dir).Root, Platform: rt.platform, NormalOpenCodePaths: t3CodeOpenCodeNormalPaths(t3CodePaths(input.Dir).Root, rt), OpenCodeKiloSupported: installation.OpenCodeKiloSupported, Library: library, Catalog: catalog, Port: input.Port, LocalKey: input.LocalKey, AdditionalProviders: additional})
	if err != nil {
		return ""
	}
	if openCodeProvider != nil {
		additional[t3CodeOpenCodeProxyID] = openCodeProvider
	}
	museProvider, _, err := planT3CodeMuse(t3CodeProfileOptions{Policy: &policy, Preferences: &prefs, RootDir: t3CodePaths(input.Dir).Root, Platform: rt.platform, NormalMusePaths: t3CodeMuseNormalPaths(rt), MuseKiloSupported: installation.MuseKiloSupported, Library: library, Catalog: catalog, Port: input.Port, LocalKey: input.LocalKey, AdditionalProviders: additional})
	if err != nil {
		return ""
	}
	if museProvider != nil {
		additional[t3CodeMuseProxyID] = museProvider
	}
	if !t3CodeUsesKilo(prefs) {
		// Native providers own their catalog and login. An unrelated gateway,
		// shared-model recovery or token rotation cannot stale their workspace.
		data, _ := json.Marshal([]any{binary, version, installation.SourceDigest, codex, claude, prefs, additional, rt.home, normalEnvironment, canonicalCodex, canonicalClaude, canonicalClaudeLiteral, canonicalSecure, t3CodeProfileRevision})
		return openDesignHash(data)
	}
	var openCodeIdentities []string
	if t3CodeOpenCodeKiloEnabled(prefs) {
		openCodeIdentities, err = t3CodeOpenCodeIsolation(t3CodePaths(input.Dir).Root, prefs.OpenCode2, t3CodeOpenCodeNormalPaths(t3CodePaths(input.Dir).Root, rt))
		if err != nil {
			return ""
		}
	}
	var museIdentity museBinaryIdentity
	var museIdentities []string
	if t3CodeMuseKiloEnabled(prefs) {
		provider := additional["muse"].(map[string]any)
		config := provider["config"].(map[string]any)
		musePath, _ := config["binaryPath"].(string)
		museIdentity, err = museExecutableIdentity(musePath)
		if err != nil {
			return ""
		}
		home, _, _ := t3CodeMusePaths(t3CodePaths(input.Dir).Root)
		museIdentities, err = museKiloIsolation(home, t3CodeMuseNormalPaths(rt))
		if err != nil {
			return ""
		}
	}
	fields := []any{binary, version, installation.SourceDigest, codex, claude, library, prefs, policy, museIdentity, museIdentities, terminalLibraryChoices(policy.CodexLibrary, catalog), terminalLibraryChoices(policy.ClaudeLibrary, catalog), additional, openCodeIdentities, input.Port, input.LocalKey, input.OrgID, input.CatalogScope, input.Images, rt.home, normalEnvironment, canonicalCodex, canonicalClaude, canonicalClaudeLiteral, canonicalSecure, t3CodeProfileRevision}
	if t3CodeUsesProviderCatalog(prefs) {
		fields = append(fields, input.KiloAccount)
	}
	data, _ := json.Marshal(fields)
	return openDesignHash(data)
}

func (a *app) readT3CodePrepared() (t3CodePrepared, error) {
	return readT3CodePreparedAt(a.dir)
}

func readT3CodePreparedAt(dir string) (t3CodePrepared, error) {
	var saved t3CodePrepared
	if !safeLaunchDir(t3CodePaths(dir).Root, dir) {
		return saved, errors.New("Prepare the private T3 Code profiles first.")
	}
	data, err := readCatalogFile(t3CodePaths(dir).Selection)
	if err != nil || json.Unmarshal(data, &saved) != nil || !t3CodeVersionValid(saved.Version) || saved.Fingerprint == "" || validateModelLibrary(saved.Library) != nil || (len(saved.Providers) < 4 || len(saved.Providers) > 12) || len(saved.Files) > 32 {
		return t3CodePrepared{}, errors.New("Prepare the private T3 Code profiles first.")
	}
	if validateT3CodePreferences(saved.Preferences) != nil || validateT3CodeLibrary(saved.CodexLibrary) != nil || validateT3CodeClaudeRoutes(saved.ClaudeLibrary, saved.ClaudeRoutes) != nil || validateModelLibrary(saved.OpenCodeLibrary) != nil || validateModelLibrary(saved.MuseLibrary) != nil {
		return t3CodePrepared{}, errors.New("Prepare the private T3 Code profiles again for these provider settings.")
	}
	if t3CodeUsesKilo(saved.Preferences) {
		if t3CodeNeedsSavedLibrary(saved.Preferences) && len(saved.Library.Models) == 0 {
			return t3CodePrepared{}, errors.New("Prepare the private T3 Code profiles first.")
		}
		// Each enabled Kilo account owns exactly two profile files and its
		// local secret. Disabled accounts retain their old files on disk, but
		// must not appear in this preparation's integrity manifest.
		for _, provider := range []struct {
			enabled bool
			profile string
			files   []string
			id, key string
		}{
			{saved.Preferences.KiloCodex, "codex-kilo", []string{"config.toml", "models.json"}, t3CodeCodexProxyID, "KILO_LOCAL_API_KEY"},
			{saved.Preferences.KiloClaude, "claude-kilo", []string{"settings.json", "kilo-models.json"}, t3CodeClaudeProxyID, "ANTHROPIC_AUTH_TOKEN"},
		} {
			paths := []string{filepath.Join("data", "userdata", "secrets", t3CodeSecretName(provider.id, provider.key))}
			for _, name := range provider.files {
				paths = append(paths, filepath.Join("profiles", provider.profile, name))
			}
			for _, path := range paths {
				digest, exists := saved.Files[path]
				if provider.enabled && (!exists || len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "") || !provider.enabled && exists {
					return t3CodePrepared{}, errors.New("Prepare the private T3 Code profiles again for these provider settings.")
				}
			}
		}
	} else if len(saved.Library.Models) != 0 || len(saved.CodexLibrary.Models) != 0 || len(saved.ClaudeLibrary.Models) != 0 || len(saved.OpenCodeLibrary.Models) != 0 || len(saved.MuseLibrary.Models) != 0 || len(saved.ClaudeRoutes) != 0 || len(saved.Files) != 0 {
		return t3CodePrepared{}, errors.New("Prepare the native T3 Code accounts again.")
	}
	if err := validateT3CodeOpenCodeManifest(saved, t3CodePaths(dir)); err != nil {
		return t3CodePrepared{}, err
	}
	if err := validateT3CodeMuseManifest(saved, t3CodePaths(dir)); err != nil {
		return t3CodePrepared{}, err
	}
	if !validT3CodeRuntimeRoot(saved.Runtime, t3CodePaths(dir).Root) {
		return t3CodePrepared{}, errors.New("Prepare the private T3 Code interface again.")
	}
	for _, id := range []string{t3CodeCodexNormalID, t3CodeCodexProxyID, t3CodeClaudeNormalID, t3CodeClaudeProxyID} {
		if saved.Providers[id] == nil {
			return t3CodePrepared{}, errors.New("Invalid private T3 Code agent selection.")
		}
	}
	if !t3CodeUsesKilo(saved.Preferences) {
		for _, expected := range []struct {
			id, driver string
			enabled    bool
		}{{t3CodeCodexNormalID, "codex", saved.Preferences.Codex}, {t3CodeCodexProxyID, "codex", false}, {t3CodeClaudeNormalID, "claudeAgent", saved.Preferences.Claude}, {t3CodeClaudeProxyID, "claudeAgent", false}} {
			provider, ok := saved.Providers[expected.id].(map[string]any)
			if !ok || provider["driver"] != expected.driver || provider["enabled"] != expected.enabled {
				return t3CodePrepared{}, errors.New("Invalid native T3 Code account selection.")
			}
		}
		for _, id := range saved.Preferences.Providers {
			provider, ok := saved.Providers[id].(map[string]any)
			if !ok || provider["driver"] != id || provider["enabled"] != true {
				return t3CodePrepared{}, errors.New("Invalid native T3 Code provider selection.")
			}
		}
	}
	return saved, nil
}

func (a *app) t3CodeReady(saved t3CodePrepared, binary string, rt clientLaunchRuntime) bool {
	return a.t3CodeProfileReady(saved, binary, rt, true)
}

func (a *app) t3CodeProfileReady(saved t3CodePrepared, binary string, rt clientLaunchRuntime, preparedOnly bool) bool {
	input := a.t3CodeValidationInputLocked()
	installation, err := inspectT3CodeInstallation(binary, rt.platform)
	if err != nil {
		return false
	}
	return t3CodeReadyFrom(input, saved, binary, installation, input.catalog(), rt, preparedOnly)
}

func t3CodeReadyFrom(input t3CodeValidationInput, saved t3CodePrepared, binary string, installation t3CodeInstallation, catalog []modelInfo, rt clientLaunchRuntime, preparedOnly bool) bool {
	version := installation.Version
	var prepared *t3CodePrepared
	if preparedOnly {
		prepared = &saved
	}
	if t3CodeUsesProviderCatalog(input.Preferences) && !input.KiloReady {
		return false
	}
	if !reflect.DeepEqual(saved.Preferences, input.Preferences) || saved.Version != version || saved.ProtocolV2 != installation.ProtocolV2 || saved.ClaudeModelDefaults != installation.ClaudeModelDefaults || saved.Fingerprint != t3CodeFingerprintFrom(input, installation, binary, version, saved.Library, catalog, rt, prepared) {
		return false
	}
	policy, policyErr := deriveT3CodeCatalogPolicy(saved.Preferences, saved.Library, catalog, installation, prepared)
	if policyErr != nil || !reflect.DeepEqual(saved.Runtime.ModelAllowlist, t3CodeRuntimeModelAllowlist(policy)) || !reflect.DeepEqual(saved.Runtime.ModelDefaults, t3CodeRuntimeModelDefaults(policy)) || !reflect.DeepEqual(saved.Runtime.OpenCodeVariants, t3CodeRuntimeOpenCodeVariants(policy)) || !reflect.DeepEqual(saved.CodexLibrary, policy.CodexLibrary) || !reflect.DeepEqual(saved.ClaudeLibrary, policy.ClaudeLibrary) || !reflect.DeepEqual(saved.OpenCodeLibrary, policy.OpenCodeLibrary) || !reflect.DeepEqual(saved.MuseLibrary, policy.MuseLibrary) || !reflect.DeepEqual(saved.Runtime.MuseReasoningLevels, policy.MuseReasoningLevels) || !reflect.DeepEqual(saved.Runtime.MuseReasoningDefaults, policy.MuseReasoningDefaults) || !reflect.DeepEqual(saved.ClaudeRoutes, policy.ClaudeRoutes) {
		return false
	}
	paths := t3CodePaths(input.Dir)
	if !safeLaunchDir(paths.Root, input.Dir) {
		return false
	}
	additional, err := planT3CodeOptionalProviders(saved.Preferences, installation, rt, paths.Root, false)
	if err != nil {
		return false
	}
	openCodeProvider, configPath, config, err := planT3CodeOpenCode(t3CodeProfileOptions{Preferences: &saved.Preferences, RootDir: paths.Root, Platform: rt.platform, NormalOpenCodePaths: t3CodeOpenCodeNormalPaths(t3CodePaths(input.Dir).Root, rt), OpenCodeKiloSupported: installation.OpenCodeKiloSupported, Library: saved.Library, Catalog: catalog, Port: input.Port, LocalKey: input.LocalKey, AdditionalProviders: additional})
	if err != nil || openCodeProvider != nil && !reflect.DeepEqual(redactT3CodeProvider(openCodeProvider), saved.Providers[t3CodeOpenCodeProxyID]) {
		return false
	}
	if configPath != "" {
		relative, err := filepath.Rel(paths.Root, configPath)
		if err != nil || saved.Files[relative] != openDesignHash(config) {
			return false
		}
	}
	museProvider, museFiles, err := planT3CodeMuse(t3CodeProfileOptions{Policy: &policy, Preferences: &saved.Preferences, RootDir: paths.Root, Platform: rt.platform, NormalMusePaths: t3CodeMuseNormalPaths(rt), MuseKiloSupported: installation.MuseKiloSupported, Library: saved.Library, Catalog: catalog, Port: input.Port, LocalKey: input.LocalKey, AdditionalProviders: additional})
	if err != nil || museProvider != nil && !reflect.DeepEqual(redactT3CodeProvider(museProvider), saved.Providers[t3CodeMuseProxyID]) {
		return false
	}
	if t3CodeMuseKiloEnabled(saved.Preferences) {
		home, _, _ := t3CodeMusePaths(paths.Root)
		if !museKiloStateSafe(home, paths.Root) {
			return false
		}
	}
	for _, file := range museFiles {
		relative, err := filepath.Rel(paths.Root, file.path)
		info, statErr := os.Lstat(file.path)
		if err != nil || saved.Files[relative] != openDesignHash(file.new) || statErr != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return false
		}
	}
	if t3CodeMuseKiloEnabled(saved.Preferences) {
		provider := additional["muse"].(map[string]any)
		config := provider["config"].(map[string]any)
		musePath, _ := config["binaryPath"].(string)
		identity, err := museExecutableIdentity(musePath)
		if err != nil || identity != saved.MuseBinary {
			return false
		}
	}
	if !t3CodeUsesKilo(saved.Preferences) {
		codex, claude := filepath.Join(paths.Root, "disabled-codex"), filepath.Join(paths.Root, "disabled-claude")
		if saved.Preferences.Codex {
			codex, err = resolveOpenDesignCLI("codex-cli", rt)
			if err != nil {
				return false
			}
		}
		if saved.Preferences.Claude {
			claude, err = rt.resolve("claude", "")
			if err != nil {
				return false
			}
		}
		planned, err := planT3CodeProfiles(t3CodeProfileOptions{Policy: &policy, Preferences: &saved.Preferences, AdditionalProviders: additional, RootDir: paths.Root, Platform: rt.platform, NormalOpenCodePaths: t3CodeOpenCodeNormalPaths(t3CodePaths(input.Dir).Root, rt), OpenCodeKiloSupported: installation.OpenCodeKiloSupported, MuseKiloSupported: installation.MuseKiloSupported, NormalMusePaths: t3CodeMuseNormalPaths(rt), NormalHome: rt.home, CodexBinary: codex, ClaudeBinary: claude, NormalEnvironment: t3CodeNormalEnvironment(rt.home, input.Environment, rt.platform), Library: saved.Library})
		if err != nil || !reflect.DeepEqual(normalizeT3CodeProviders(planned.Providers), saved.Providers) {
			return false
		}
	}
	for relative, expected := range saved.Files {
		if filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return false
		}
		path := filepath.Join(paths.Root, relative)
		if !safeLaunchDir(filepath.Dir(path), input.Dir) {
			return false
		}
		digest, err := openDesignFileHash(path, catalogLimit)
		if err != nil || digest != expected {
			return false
		}
	}
	settings, err := readCatalogFile(paths.Settings)
	var document map[string]any
	if err != nil || json.Unmarshal(settings, &document) != nil {
		return false
	}
	providers, ok := document["providerInstances"].(map[string]any)
	if !ok {
		return false
	}
	// T3 fills defaults and persists normal runtime settings. Only the configured
	// managed entries must retain our owned fields, not the entire settings file.
	for id, expected := range saved.Providers {
		if !t3CodeSubset(expected, providers[id]) {
			return false
		}
	}
	clientSettings, err := readCatalogFile(paths.ClientSettings)
	if err != nil {
		return false
	}
	if !saved.Preferences.KiloClaude {
		_, err = readT3CodeClientSettings(clientSettings)
		return err == nil
	}
	return t3CodeClientSettingsReady(clientSettings, t3CodeClaudePickerLibrary(t3CodeFallbackLibrary(saved.ClaudeLibrary, saved.Library), saved.ClaudeRoutes), saved.ClaudeBuiltins)
}

func t3CodeSubset(expected, actual any) bool {
	if object, ok := expected.(map[string]any); ok {
		other, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range object {
			if !t3CodeSubset(value, other[key]) {
				return false
			}
		}
		return true
	}
	if array, ok := expected.([]any); ok {
		other, ok := actual.([]any)
		if !ok || len(array) != len(other) {
			return false
		}
		for i, value := range array {
			if !t3CodeSubset(value, other[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(expected, actual)
}

func t3CodeSecretName(instance, name string) string {
	return "provider-env-" + base64.RawURLEncoding.EncodeToString([]byte(instance)) + "-" + base64.RawURLEncoding.EncodeToString([]byte(name)) + ".bin"
}

// This is T3 0.0.45's documented implementation: raw owner-only .bin files,
// and redacted provider env entries. Only the local proxy token is supplied.
// No credential is copied from a normal provider home.
func planT3CodeSettings(paths t3CodeManagedPaths, providers map[string]any, library ...modelLibrary) ([]profileFile, error) {
	return planT3CodeSettingsWithPolicy(paths, providers, nil, library...)
}

func planT3CodeSettingsWithPolicy(paths t3CodeManagedPaths, providers map[string]any, policy *t3CodeModelPolicy, library ...modelLibrary) ([]profileFile, error) {
	settings := map[string]any{}
	data, err := readCatalogFile(paths.Settings)
	if err == nil {
		if json.Unmarshal(data, &settings) != nil || settings == nil {
			return nil, errors.New("Private T3 Code settings are invalid JSON; no setup changes saved.")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("Cannot safely read the private T3 Code settings.")
	}
	all := map[string]any{}
	if raw, exists := settings["providerInstances"]; exists {
		var ok bool
		all, ok = raw.(map[string]any)
		if !ok {
			return nil, errors.New("Private T3 Code provider instances must be an object.")
		}
	}
	files := []profileFile{}
	if providers[t3CodeMuseProxyID] == nil {
		delete(all, t3CodeMuseProxyID)
	}
	if providers[t3CodeOpenCodeProxyID] == nil {
		// A downgraded driver can reject a second instance even when disabled.
		// Remove only our registration; retained profiles, secrets and chats are
		// left intact, and a supported build restores this same managed ID.
		delete(all, t3CodeOpenCodeProxyID)
	}
	for id, raw := range providers {
		instance, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("Invalid private T3 Code provider configuration.")
		}
		env, ok := instance["environment"].([]any)
		if !ok {
			return nil, errors.New("Invalid private T3 Code environment configuration.")
		}
		for _, item := range env {
			variable, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("Invalid private T3 Code environment variable.")
			}
			sensitive, _ := variable["sensitive"].(bool)
			if !sensitive {
				continue
			}
			name, _ := variable["name"].(string)
			value, _ := variable["value"].(string)
			if (id != t3CodeCodexProxyID || name != "KILO_LOCAL_API_KEY") && (id != t3CodeClaudeProxyID || name != "ANTHROPIC_AUTH_TOKEN") && (id != t3CodeOpenCodeProxyID || name != t3CodeOpenCodeKey) && (id != t3CodeMuseProxyID || name != t3CodeMuseKey) {
				return nil, errors.New("Unexpected private T3 Code secret.")
			}
			if value == "" || strings.ContainsAny(value, "\x00\r\n") {
				return nil, errors.New("Invalid local T3 Code proxy credential.")
			}
			secret, err := prepareProfileFile(filepath.Join(paths.Secrets, t3CodeSecretName(id, name)), []byte(value))
			if err != nil {
				return nil, err
			}
			files = append(files, secret)
			variable["value"] = ""
			variable["valueRedacted"] = true
		}
		all[id] = instance
	}
	settings["providerInstances"] = all
	if _, exists := settings["providers"]; !exists {
		// 0.0.45 synthesizes its legacy Codex/Claude instances even when an
		// explicit instance map exists. A new Kilo workspace should offer
		// our four agents first; later user changes to legacy agents survive.
		settings["providers"] = map[string]any{"codex": map[string]any{"enabled": false}, "claudeAgent": map[string]any{"enabled": false}}
	}
	if policy != nil {
		settings["defaultModelSelection"] = t3CodeDefaultSelection(settings["defaultModelSelection"], all, *policy)
	} else if len(library) > 0 {
		selected, _ := settings["defaultModelSelection"].(map[string]any)
		instance, _ := selected["instanceId"].(string)
		model, _ := selected["model"].(string)
		valid := false
		for _, choice := range library[0].Models {
			valid = valid || choice.ID == model
		}
		if selected == nil || (instance == t3CodeCodexProxyID || instance == t3CodeClaudeProxyID) && !valid {
			settings["defaultModelSelection"] = map[string]any{"instanceId": t3CodeCodexProxyID, "model": library[0].DefaultModel}
		}
	}
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(encoded) > catalogLimit {
		return nil, errors.New("Private T3 Code settings exceed the size limit.")
	}
	file, err := prepareProfileFile(paths.Settings, append(encoded, '\n'))
	if err != nil {
		return nil, err
	}
	return append(files, file), nil
}

func (a *app) prepareT3Code(library modelLibrary, rt clientLaunchRuntime, binary string) error {
	if err := a.t3CodeAvailability(binary, rt, normalizeT3CodePreferences(a.config.T3Code)); err != nil {
		return err
	}
	installation, err := inspectT3CodeInstallation(binary, rt.platform)
	if err != nil {
		return err
	}
	version := installation.Version
	prefs := normalizeT3CodePreferences(a.config.T3Code)
	if !t3CodeUsesKilo(prefs) {
		library = emptyModelLibrary()
	}
	paths := t3CodePaths(a.dir)
	codex, claude := filepath.Join(paths.Root, "disabled-codex"), filepath.Join(paths.Root, "disabled-claude")
	if prefs.Codex || prefs.KiloCodex {
		codex, err = resolveOpenDesignCLI("codex-cli", rt)
		if err != nil {
			return err
		}
	}
	if prefs.Claude || prefs.KiloClaude {
		claude, err = rt.resolve("claude", "")
		if err != nil {
			return err
		}
	}
	for _, dir := range []string{paths.Root, paths.Secrets, paths.UIHome} {
		if err := safeEditorDir(a.dir, dir); err != nil {
			return err
		}
	}
	catalog := a.t3CodeValidationInputLocked().catalog()
	policy, err := deriveT3CodeModelPolicy(prefs, library, catalog, installation)
	if err != nil {
		return err
	}
	additional, err := planT3CodeOptionalProviders(prefs, installation, rt, t3CodePaths(a.dir).Root)
	if err != nil {
		return err
	}
	var museIdentity museBinaryIdentity
	museRelease := ""
	if t3CodeMuseKiloEnabled(prefs) {
		provider := additional["muse"].(map[string]any)
		config := provider["config"].(map[string]any)
		musePath, _ := config["binaryPath"].(string)
		museIdentity, err = museExecutableIdentity(musePath)
		if err != nil {
			return err
		}
		museRelease, err = a.checkMuseKiloVersion(musePath)
		if err != nil {
			return err
		}
		currentIdentity, err := museExecutableIdentity(musePath)
		if err != nil || currentIdentity != museIdentity {
			return errors.New("Muse Code changed while checking its version. Prepare again.")
		}
	}
	claudeCaps := claudeCapabilities{}
	if prefs.KiloClaude {
		claudeCaps = a.t3CodeClaudeCapabilities(claude, rt)
	}
	if err := validateT3CodeNativeClaudeVersion(policy.ClaudeLibrary, claudeCaps); err != nil {
		return err
	}
	privateRuntime, err := prepareT3CodeRuntimeWithMuse(paths.Root, binary, rt.platform, t3CodeRuntimeModelAllowlist(policy), t3CodeRuntimeModelDefaults(policy), t3CodeRuntimeOpenCodeVariants(policy), t3CodeMuseRuntimePolicy{Levels: policy.MuseReasoningLevels, Defaults: policy.MuseReasoningDefaults})
	if err != nil {
		return err
	}
	planned, err := planT3CodeProfiles(t3CodeProfileOptions{Policy: &policy, Preferences: &prefs, AdditionalProviders: additional, RootDir: paths.Root, Platform: rt.platform, NormalOpenCodePaths: t3CodeOpenCodeNormalPaths(t3CodePaths(a.dir).Root, rt), OpenCodeKiloSupported: installation.OpenCodeKiloSupported, MuseKiloSupported: installation.MuseKiloSupported, NormalMusePaths: t3CodeMuseNormalPaths(rt), NormalHome: rt.home, CodexBinary: codex, ClaudeBinary: claude, NormalEnvironment: t3CodeNormalEnvironment(rt.home, os.Environ(), rt.platform), Library: library, Catalog: catalog, ClaudeCaps: claudeCaps, Version: version, ClaudeModelDefaults: installation.ClaudeModelDefaults, Port: a.config.Port, LocalKey: a.config.LocalKey, Images: a.clientImageSettingsLocked()})
	if err != nil {
		return err
	}
	planned.Providers = normalizeT3CodeProviders(planned.Providers)
	settings, err := planT3CodeSettingsWithPolicy(paths, planned.Providers, &policy, t3CodeFallbackLibrary(policy.CodexLibrary, library))
	if err != nil {
		return err
	}
	files := append(planned.Files, settings...)
	clientSettings, err := readCatalogFile(paths.ClientSettings)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("Cannot safely read the private T3 Code client settings.")
	}
	claudeBuiltins := append(t3CodeCachedClaudeSlugs(paths), installation.ClaudeBuiltins...)
	if prefs.KiloClaude {
		clientSettings, err = planT3CodeClientSettings(clientSettings, t3CodeClaudePickerLibrary(t3CodeFallbackLibrary(policy.ClaudeLibrary, library), policy.ClaudeRoutes), claudeBuiltins)
		if err != nil {
			return err
		}
	} else {
		if _, err := readT3CodeClientSettings(clientSettings); err != nil {
			return err
		}
		if clientSettings == nil {
			clientSettings = []byte("{}\n")
		}
	}
	clientFile, err := prepareProfileFile(paths.ClientSettings, clientSettings)
	if err != nil {
		return err
	}
	files = append(files, clientFile)
	saved := t3CodePrepared{Runtime: privateRuntime, ClaudeBuiltins: claudeBuiltins, Preferences: prefs, CodexLibrary: policy.CodexLibrary, ClaudeLibrary: policy.ClaudeLibrary, OpenCodeLibrary: policy.OpenCodeLibrary, MuseLibrary: policy.MuseLibrary, MuseBinary: museIdentity, MuseRelease: museRelease, ClaudeRoutes: policy.ClaudeRoutes, Library: library, Version: version, ProtocolV2: installation.ProtocolV2, ClaudeModelDefaults: installation.ClaudeModelDefaults, Fingerprint: a.t3CodeFingerprint(binary, version, library, rt), Providers: planned.Providers, Files: map[string]string{}}
	for _, file := range files {
		if file.path == paths.Settings || file.path == paths.ClientSettings {
			continue
		}
		relative, err := filepath.Rel(paths.Root, file.path)
		if err != nil {
			return err
		}
		saved.Files[relative] = openDesignHash(file.new)
	}
	current, err := inspectT3CodeInstallation(binary, rt.platform)
	if err != nil || current.SourceDigest != installation.SourceDigest || saved.Fingerprint == "" {
		return errors.New("T3 Code changed while preparing. Refresh detection and prepare again.")
	}
	if t3CodeMuseKiloEnabled(prefs) {
		identity, err := museExecutableIdentity(museIdentity.Path)
		if err != nil || identity != museIdentity {
			return errors.New("Muse Code changed while preparing. Refresh detection and prepare again.")
		}
	}
	data, _ := json.MarshalIndent(saved, "", "  ")
	selection, err := prepareProfileFile(paths.Selection, append(data, '\n'))
	if err != nil {
		return err
	}
	_, err = saveEditorFiles(append(files, selection))
	return err
}

// Keep a cold preparation independent of the Models screen. The request holds
// launchMu and releases app.mu for network I/O, then rechecks its inputs.
func (a *app) loadT3CodeProviderCatalog(ctx context.Context, prefs t3CodePreferences, library modelLibraryState) error {
	scope, revision := a.catalogScopeLocked(), a.catalogRevision
	account := usageAccountID(a.apiKey, a.config.OrgID)
	cache := readNativeCachedCatalog(a.dir, scope)
	if cache.KiloAccount == account && !modelCatalogNeedsRefresh(cache.KiloFetchedAt, time.Now()) {
		return nil
	}
	a.mu.Unlock()
	models, fetchedRevision, err := a.fetchKiloModels(ctx, true)
	currentLibrary := a.modelLibrary.snapshot()
	a.mu.Lock()
	if err != nil {
		return err
	}
	if revision != fetchedRevision || a.catalogRevision != revision || a.catalogScopeLocked() != scope ||
		!a.kiloReadyLocked() || !reflect.DeepEqual(normalizeT3CodePreferences(a.config.T3Code), prefs) ||
		!reflect.DeepEqual(currentLibrary, library) {
		return &catalogError{409, "A connection changed while loading models. Refresh the catalog."}
	}
	for i := range models {
		models[i].Connection = "kilo"
		models[i].HasUserByokAvailable = nil
	}
	kiloFetchedAt := time.Now().UTC()
	fetchedAt := time.Time{}
	if !a.chatGPTReadyLocked() {
		fetchedAt = kiloFetchedAt
	}
	cache = readNativeCachedCatalog(a.dir, scope)
	for _, model := range cache.Models {
		if model.Connection == "chatgpt" {
			models = append(models, model)
		}
	}
	data, err := json.Marshal(nativeCachedCatalog{SchemaVersion: 1, OrgID: scope, KiloAccount: account, Models: models, FetchedAt: fetchedAt, KiloFetchedAt: kiloFetchedAt})
	if err != nil || len(models) > 10000 || len(data) > 4<<20 {
		return &catalogError{502, "The model catalog exceeds the size limit."}
	}
	if err := atomicCatalogFile(filepath.Join(a.dir, "model-catalog.json"), data); err != nil {
		return &catalogError{500, "Cannot save the private model catalog."}
	}
	return nil
}

func (a *app) t3CodeProfileAPI(w http.ResponseWriter, r *http.Request) {
	rt := a.launchRuntime()
	binary, resolveErr := rt.resolve("t3-code", "")
	if r.Method == http.MethodGet {
		paths := t3CodePaths(a.dir)
		state := a.modelLibrary.snapshot()
		library := state.Library
		version, _ := t3CodeVersion(binary, rt.platform)
		installation, _ := inspectT3CodeInstallation(binary, rt.platform)
		message := "Prepare T3 Code to add Codex and Claude agents with direct and Kilo Proxy connections in a separate T3 window."
		a.mu.Lock()
		defer a.mu.Unlock()
		if resolveErr == nil {
			resolveErr = a.t3CodeAvailability(binary, rt, normalizeT3CodePreferences(a.config.T3Code))
		}
		if resolveErr != nil {
			message = resolveErr.Error()
		}
		saved, err := a.readT3CodePrepared()
		ready := resolveErr == nil && err == nil && a.t3CodeReady(saved, binary, rt)
		models := []t3CodeModelSummary{}
		if ready {
			message = "T3 Code prepared with your selected accounts and models."
			library = saved.Library
			for _, model := range library.Models {
				models = append(models, t3CodeModelSummary{ID: model.ID, Name: model.DisplayName, ReasoningEffort: model.ReasoningEffort})
			}
		}
		prefs := normalizeT3CodePreferences(a.config.T3Code)
		if !ready && t3CodeUsesKilo(prefs) && !t3CodeNeedsSavedLibrary(prefs) {
			library = t3CodeOptionalSavedLibrary(state)
		}
		catalog := a.t3CodeValidationInputLocked().catalog()
		var prepared *t3CodePrepared
		if ready {
			prepared = &saved
		}
		policy, policyErr := deriveT3CodeCatalogPolicy(prefs, library, catalog, installation, prepared)
		if resolveErr == nil {
			if t3CodeUsesProviderCatalog(prefs) && !a.kiloReadyLocked() {
				message = "Connect Kilo to use the Claude or Meta catalog in T3 Code."
			} else if policyErr != nil {
				message = policyErr.Error()
			}
		}
		jsonResponse(w, 200, map[string]any{"preferences": prefs, "supportedProviders": installation.SupportedProviders, "openCode2Supported": installation.OpenCode2Supported, "openCodeKiloSupported": installation.OpenCodeKiloSupported, "museKiloSupported": installation.MuseKiloSupported, "effectiveMuseModels": t3CodeModelSummaries(policy.MuseLibrary, catalog), "effectiveOpenCodeModels": t3CodeModelSummaries(policy.OpenCodeLibrary, catalog), "effectiveCodexModels": t3CodeModelSummaries(policy.CodexLibrary, catalog), "effectiveClaudeModels": t3CodeModelSummaries(policy.ClaudeLibrary, catalog), "prepared": ready, "library": library, "profileDir": paths.Root, "version": version, "protocolV2": installation.ProtocolV2, "claudeModelDefaults": installation.ClaudeModelDefaults, "models": models, "message": message})
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, 405, "Method not allowed.")
		return
	}
	var input struct {
		Library     *modelLibrary   `json:"library,omitempty"`
		Preferences json.RawMessage `json:"preferences,omitempty"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	if resolveErr != nil {
		jsonError(w, 409, resolveErr.Error())
		return
	}
	if !a.launchMu.TryLock() {
		jsonError(w, 409, "Another agent is being prepared. Try again.")
		return
	}
	defer a.launchMu.Unlock()
	state := a.modelLibrary.snapshot()
	library := state.Library
	a.mu.Lock()
	defer a.mu.Unlock()
	prefs := normalizeT3CodePreferences(a.config.T3Code)
	if !t3CodeUsesKilo(prefs) {
		library = emptyModelLibrary()
	} else if !t3CodeNeedsSavedLibrary(prefs) {
		library = t3CodeOptionalSavedLibrary(state)
	} else if input.Library != nil {
		library = *input.Library
	} else if state.RecoveryRequired || state.Warning != "" {
		jsonError(w, 409, "Recover and save your shared models first.")
		return
	}
	if err := validateModelLibrary(library); err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	if t3CodeNeedsSavedLibrary(prefs) && len(library.Models) == 0 {
		jsonError(w, 400, "Choose a valid shared model library before preparing T3 Code.")
		return
	}
	if len(input.Preferences) > 0 {
		inputPreferences, fields, err := decodeT3CodePreferencesDocument(input.Preferences)
		if err != nil {
			jsonError(w, 400, err.Error())
			return
		}
		if !fields.OpenCodeConnection {
			inputPreferences.OpenCodeConnection = prefs.OpenCodeConnection
		}
		if !fields.MuseConnection {
			inputPreferences.MuseConnection = prefs.MuseConnection
		}
		if !fields.MuseModels {
			inputPreferences.MuseModels = prefs.MuseModels
		}
		if !reflect.DeepEqual(normalizeT3CodePreferences(inputPreferences), prefs) {
			jsonError(w, 409, "T3 Code settings changed. Reload them before preparing.")
			return
		}
	}
	if len(library.Models) > t3CodeCustomModelLimit && (prefs.KiloCodex && prefs.CodexModels == "shared" || prefs.KiloClaude && prefs.ClaudeModels == "shared") {
		jsonError(w, 400, "T3 Code supports up to 32 shared models per account. Reduce your selection in Models or choose official models.")
		return
	}
	if t3CodeUsesProviderCatalog(prefs) && !a.kiloReadyLocked() {
		jsonError(w, 409, "Connect Kilo to use the Claude or Meta catalog in T3 Code.")
		return
	}
	if t3CodeUsesKilo(prefs) && !a.connectionReadyLocked() {
		jsonError(w, 409, "Connect the selected provider first.")
		return
	}
	paths := t3CodePaths(a.dir)
	check := a.t3CodeCheckRunning
	if check == nil {
		check = t3CodeRunning
	}
	running, err := check(paths.Root)
	if err != nil {
		jsonError(w, 409, "Cannot verify whether T3 Code Kilo is running. Close its window and try again.")
		return
	}
	inUse := running || time.Now().Before(a.t3CodeLaunchUntil)
	if t3CodeUsesProviderCatalog(prefs) && !inUse {
		if err := a.loadT3CodeProviderCatalog(r.Context(), prefs, state); err != nil {
			catalogFailure(w, err)
			return
		}
	}
	saved, readErr := a.readT3CodePrepared()
	unchanged := readErr == nil && reflect.DeepEqual(saved.Library, library) && a.t3CodeProfileReady(saved, binary, rt, inUse)
	if inUse && !unchanged {
		jsonError(w, 409, "Quit the T3 Code Kilo window before changing its models or connection. Your regular T3 Code window can stay open.")
		return
	}
	if !unchanged {
		if err = a.prepareT3Code(library, rt, binary); err != nil {
			jsonError(w, 409, err.Error())
			return
		}
	}
	models := []t3CodeModelSummary{}
	for _, model := range library.Models {
		models = append(models, t3CodeModelSummary{ID: model.ID, Name: model.DisplayName, ReasoningEffort: model.ReasoningEffort})
	}
	saved, _ = a.readT3CodePrepared()
	version := saved.Version
	installation, _ := inspectT3CodeInstallation(binary, rt.platform)
	catalog := a.t3CodeValidationInputLocked().catalog()
	jsonResponse(w, 200, map[string]any{"preferences": saved.Preferences, "supportedProviders": installation.SupportedProviders, "openCode2Supported": installation.OpenCode2Supported, "openCodeKiloSupported": installation.OpenCodeKiloSupported, "museKiloSupported": installation.MuseKiloSupported, "effectiveMuseModels": t3CodeModelSummaries(saved.MuseLibrary, catalog), "effectiveOpenCodeModels": t3CodeModelSummaries(saved.OpenCodeLibrary, catalog), "effectiveCodexModels": t3CodeModelSummaries(saved.CodexLibrary, catalog), "effectiveClaudeModels": t3CodeModelSummaries(saved.ClaudeLibrary, catalog), "prepared": true, "library": library, "profileDir": paths.Root, "version": version, "protocolV2": saved.ProtocolV2, "claudeModelDefaults": saved.ClaudeModelDefaults, "models": models, "message": "T3 Code prepared with your selected accounts and models."})
}

// Round-trip generated provider maps so persisted JSON and runtime JSON share
// identical number/array types during the managed-subset readiness comparison.
func normalizeT3CodeProviders(providers map[string]any) map[string]any {
	data, _ := json.Marshal(providers)
	var result map[string]any
	_ = json.NewDecoder(bytes.NewReader(data)).Decode(&result)
	return result
}

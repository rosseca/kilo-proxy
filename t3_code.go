package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const t3CodeProfileEndpoint = "/api/clients/t3-code"
const t3CodeProfileRevision = 3

type t3CodeManagedPaths struct{ Root, Data, UIHome, Settings, ClientSettings, Secrets, Selection string }

func t3CodePaths(appDir string) t3CodeManagedPaths {
	root := filepath.Join(appDir, "t3-code")
	data := filepath.Join(root, "data")
	return t3CodeManagedPaths{Root: root, Data: data, UIHome: filepath.Join(root, "ui-home"), Settings: filepath.Join(data, "userdata", "settings.json"), ClientSettings: filepath.Join(data, "userdata", "client-settings.json"), Secrets: filepath.Join(data, "userdata", "secrets"), Selection: filepath.Join(root, "selection.json")}
}

type t3CodePrepared struct {
	Library     modelLibrary      `json:"library"`
	Version     string            `json:"version"`
	Fingerprint string            `json:"fingerprint"`
	Providers   map[string]any    `json:"providers"`
	Files       map[string]string `json:"files"`
}

type t3CodeModelSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

func (a *app) t3CodeFingerprint(binary, version string, library modelLibrary, rt clientLaunchRuntime) string {
	codex, _ := resolveOpenDesignCLI("codex-cli", rt)
	claude, _ := rt.resolve("claude", "")
	normalEnvironment := t3CodeNormalEnvironment(rt.home, os.Environ(), rt.platform)
	normalCodex, normalClaude, err := t3CodeNormalProviderHomes(t3CodeProfileOptions{NormalHome: rt.home, NormalEnvironment: normalEnvironment})
	if err != nil {
		return ""
	}
	canonicalCodex, err := t3CodeCanonicalPath(normalCodex)
	if err != nil {
		return ""
	}
	canonicalClaude, err := t3CodeCanonicalPath(normalClaude)
	if err != nil {
		return ""
	}
	// A retained symlink spelling can resolve to a different provider home
	// after preparation. Record its resolved directory identity as well.
	data, _ := json.Marshal([]any{binary, version, codex, claude, library, a.config.Port, a.config.LocalKey, a.config.OrgID, a.catalogScopeLocked(), a.clientImageSettingsLocked(), rt.home, normalEnvironment, canonicalCodex, canonicalClaude, t3CodeProfileRevision})
	return openDesignHash(data)
}

func (a *app) readT3CodePrepared() (t3CodePrepared, error) {
	var saved t3CodePrepared
	if !safeLaunchDir(t3CodePaths(a.dir).Root, a.dir) {
		return saved, errors.New("Prepare the private T3 Code profiles first.")
	}
	data, err := readCatalogFile(t3CodePaths(a.dir).Selection)
	if err != nil || json.Unmarshal(data, &saved) != nil || !t3CodeVersionSupported(saved.Version) || saved.Fingerprint == "" || len(saved.Library.Models) == 0 || validateT3CodeLibrary(saved.Library) != nil || len(saved.Providers) != 4 || len(saved.Files) < 4 || len(saved.Files) > 32 {
		return t3CodePrepared{}, errors.New("Prepare the private T3 Code profiles first.")
	}
	for _, id := range []string{t3CodeCodexNormalID, t3CodeCodexProxyID, t3CodeClaudeNormalID, t3CodeClaudeProxyID} {
		if saved.Providers[id] == nil {
			return t3CodePrepared{}, errors.New("Invalid private T3 Code agent selection.")
		}
	}
	return saved, nil
}

func (a *app) t3CodeReady(saved t3CodePrepared, binary string, rt clientLaunchRuntime) bool {
	version, err := t3CodeVersion(binary, rt.platform)
	if err != nil || !t3CodeVersionSupported(version) || saved.Version != version || saved.Fingerprint != a.t3CodeFingerprint(binary, version, saved.Library, rt) {
		return false
	}
	paths := t3CodePaths(a.dir)
	if !safeLaunchDir(paths.Root, a.dir) {
		return false
	}
	for relative, expected := range saved.Files {
		if filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return false
		}
		path := filepath.Join(paths.Root, relative)
		if !safeLaunchDir(filepath.Dir(path), a.dir) {
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
	// T3 fills defaults and persists normal runtime settings. Only the four
	// managed entries must retain our owned fields, not the entire settings file.
	for id, expected := range saved.Providers {
		if !t3CodeSubset(expected, providers[id]) {
			return false
		}
	}
	clientSettings, err := readCatalogFile(paths.ClientSettings)
	return err == nil && t3CodeClientSettingsReady(clientSettings, saved.Library, t3CodeCachedClaudeSlugs(paths))
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
			if (id != t3CodeCodexProxyID || name != "KILO_LOCAL_API_KEY") && (id != t3CodeClaudeProxyID || name != "ANTHROPIC_AUTH_TOKEN") {
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
	if len(library) > 0 {
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
	if err := a.t3CodeAvailability(binary, rt); err != nil {
		return err
	}
	version, _ := t3CodeVersion(binary, rt.platform)
	codex, err := resolveOpenDesignCLI("codex-cli", rt)
	if err != nil {
		return err
	}
	claude, err := rt.resolve("claude", "")
	if err != nil {
		return err
	}
	paths := t3CodePaths(a.dir)
	for _, dir := range []string{paths.Root, paths.Secrets, paths.UIHome} {
		if err := safeEditorDir(a.dir, dir); err != nil {
			return err
		}
	}
	planned, err := planT3CodeProfiles(t3CodeProfileOptions{RootDir: paths.Root, NormalHome: rt.home, CodexBinary: codex, ClaudeBinary: claude, NormalEnvironment: t3CodeNormalEnvironment(rt.home, os.Environ(), rt.platform), Library: library, Catalog: readNativeCatalogCache(a.dir, a.catalogScopeLocked()), ClaudeCaps: a.t3CodeClaudeCapabilities(claude, rt), Version: version, Port: a.config.Port, LocalKey: a.config.LocalKey, Images: a.clientImageSettingsLocked()})
	if err != nil {
		return err
	}
	planned.Providers = normalizeT3CodeProviders(planned.Providers)
	settings, err := planT3CodeSettings(paths, planned.Providers, library)
	if err != nil {
		return err
	}
	files := append(planned.Files, settings...)
	clientSettings, err := readCatalogFile(paths.ClientSettings)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("Cannot safely read the private T3 Code client settings.")
	}
	clientSettings, err = planT3CodeClientSettings(clientSettings, library, t3CodeCachedClaudeSlugs(paths))
	if err != nil {
		return err
	}
	clientFile, err := prepareProfileFile(paths.ClientSettings, clientSettings)
	if err != nil {
		return err
	}
	files = append(files, clientFile)
	saved := t3CodePrepared{Library: library, Version: version, Fingerprint: a.t3CodeFingerprint(binary, version, library, rt), Providers: planned.Providers, Files: map[string]string{}}
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
	data, _ := json.MarshalIndent(saved, "", "  ")
	selection, err := prepareProfileFile(paths.Selection, append(data, '\n'))
	if err != nil {
		return err
	}
	_, err = saveEditorFiles(append(files, selection))
	return err
}

func (a *app) t3CodeProfileAPI(w http.ResponseWriter, r *http.Request) {
	rt := a.launchRuntime()
	binary, resolveErr := rt.resolve("t3-code", "")
	if r.Method == http.MethodGet {
		paths := t3CodePaths(a.dir)
		state := a.modelLibrary.snapshot()
		library := state.Library
		version, _ := t3CodeVersion(binary, rt.platform)
		message := "Prepare T3 Code to add Codex and Claude agents with normal and Kilo Proxy connections in a separate T3 window."
		if resolveErr == nil {
			resolveErr = a.t3CodeAvailability(binary, rt)
		}
		if resolveErr != nil {
			message = resolveErr.Error()
		}
		a.mu.Lock()
		saved, err := a.readT3CodePrepared()
		ready := resolveErr == nil && err == nil && a.t3CodeReady(saved, binary, rt)
		a.mu.Unlock()
		models := []t3CodeModelSummary{}
		if ready {
			message = "T3 Code prepared with Codex and Claude agents using normal and Kilo Proxy connections."
			library = saved.Library
			for _, model := range library.Models {
				models = append(models, t3CodeModelSummary{ID: model.ID, Name: model.DisplayName, ReasoningEffort: model.ReasoningEffort})
			}
		}
		jsonResponse(w, 200, map[string]any{"prepared": ready, "library": library, "profileDir": paths.Root, "version": version, "models": models, "message": message})
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, 405, "Method not allowed.")
		return
	}
	var input struct {
		Library *modelLibrary `json:"library,omitempty"`
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
	if input.Library != nil {
		library = *input.Library
	} else if state.RecoveryRequired || state.Warning != "" {
		jsonError(w, 409, "Recover and save your shared models first.")
		return
	}
	if err := validateT3CodeLibrary(library); err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	if len(library.Models) == 0 {
		jsonError(w, 400, "Choose a valid shared model library before preparing T3 Code.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.connectionReadyLocked() {
		jsonError(w, 409, "Connect the selected provider first.")
		return
	}
	paths := t3CodePaths(a.dir)
	saved, readErr := a.readT3CodePrepared()
	unchanged := readErr == nil && reflect.DeepEqual(saved.Library, library) && a.t3CodeReady(saved, binary, rt)
	check := a.t3CodeCheckRunning
	if check == nil {
		check = t3CodeRunning
	}
	running, err := check(paths.Root)
	if err != nil {
		jsonError(w, 409, "Cannot verify whether T3 Code Kilo is running. Close its window and try again.")
		return
	}
	if (running || time.Now().Before(a.t3CodeLaunchUntil)) && !unchanged {
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
	version, _ := t3CodeVersion(binary, rt.platform)
	jsonResponse(w, 200, map[string]any{"prepared": true, "library": library, "profileDir": paths.Root, "version": version, "models": models, "message": "T3 Code prepared with Codex and Claude agents using normal and Kilo Proxy connections."})
}

// Round-trip generated provider maps so persisted JSON and runtime JSON share
// identical number/array types during the managed-subset readiness comparison.
func normalizeT3CodeProviders(providers map[string]any) map[string]any {
	data, _ := json.Marshal(providers)
	var result map[string]any
	_ = json.NewDecoder(bytes.NewReader(data)).Decode(&result)
	return result
}

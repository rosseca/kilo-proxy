package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const synaraProfileEndpoint = "/api/clients/synara"
const synaraProfileRevision = 4

type synaraManagedPaths struct{ Root, Data, UIHome, Electron, Settings, Secrets, Selection string }

func synaraPaths(appDir string) synaraManagedPaths {
	root := filepath.Join(appDir, "synara")
	data := filepath.Join(root, "data")
	return synaraManagedPaths{Root: root, Data: data, UIHome: filepath.Join(root, "ui-home"), Settings: filepath.Join(data, "userdata", "settings.json"), Electron: filepath.Join(root, "electron"), Secrets: filepath.Join(data, "userdata", "secrets"), Selection: filepath.Join(root, "selection.json")}
}

type synaraPrepared struct {
	Library     modelLibrary          `json:"library"`
	Version     string                `json:"version"`
	Fingerprint string                `json:"fingerprint"`
	Providers   map[string]any        `json:"providers"`
	Files       map[string]string     `json:"files"`
	Runtime     *synaraPrivateRuntime `json:"runtime,omitempty"`
}

func (a *app) synaraUsesPrivateRuntime(rt clientLaunchRuntime) bool {
	return !a.synaraRuntimeDisabled && (rt.platform == "macos" || rt.platform == "darwin")
}

type synaraModelSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

func (a *app) synaraFingerprint(binary string, library modelLibrary, rt clientLaunchRuntime) string {
	codex, _ := resolveOpenDesignCLI("codex-cli", rt)
	claude, _ := rt.resolve("claude", "")
	normalEnvironment := synaraNormalEnvironment(rt.home, os.Environ(), rt.platform)
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
	secureStorage, err := synaraNormalSecureStorage(t3CodeProfileOptions{RootDir: synaraPaths(a.dir).Root, NormalHome: rt.home, NormalEnvironment: normalEnvironment})
	if err != nil {
		return ""
	}
	if secureStorage == "" {
		secureStorage = filepath.Join(rt.home, ".claude")
	}
	canonicalSecure, err := t3CodeCanonicalPath(secureStorage)
	if err != nil {
		return ""
	}
	adapterSourceHash, err := a.synaraAdapterSourceHash()
	if err != nil {
		return ""
	}
	// A retained symlink spelling can resolve to a different provider home
	// after preparation. Record its resolved directory identity as well.
	data, _ := json.Marshal([]any{binary, codex, claude, library, a.config.Port, a.config.LocalKey, a.config.OrgID, a.catalogScopeLocked(), a.clientImageSettingsLocked(), rt.home, normalEnvironment, canonicalCodex, canonicalClaude, canonicalSecure, synaraProfileRevision, adapterSourceHash})
	return openDesignHash(data)
}

func (a *app) readSynaraPrepared() (synaraPrepared, error) {
	var saved synaraPrepared
	if !safeLaunchDir(synaraPaths(a.dir).Root, a.dir) {
		return saved, errors.New("Prepare the private Synara profiles first.")
	}
	data, err := readCatalogFile(synaraPaths(a.dir).Selection)
	if err != nil || json.Unmarshal(data, &saved) != nil || saved.Fingerprint == "" || len(saved.Library.Models) == 0 || validateSynaraLibrary(saved.Library) != nil || len(saved.Providers) != 4 || len(saved.Files) < 4 || len(saved.Files) > 32 {
		return synaraPrepared{}, errors.New("Prepare the private Synara profiles first.")
	}
	for _, id := range []string{synaraCodexNormalID, synaraCodexProxyID, synaraClaudeNormalID, synaraClaudeProxyID} {
		if saved.Providers[id] == nil {
			return synaraPrepared{}, errors.New("Invalid private Synara agent selection.")
		}
	}
	return saved, nil
}

func (a *app) synaraReady(saved synaraPrepared, binary string, rt clientLaunchRuntime) bool {
	_, err := synaraPackageMetadata(binary, rt.platform)
	if err != nil || saved.Fingerprint != a.synaraFingerprint(binary, saved.Library, rt) {
		return false
	}
	paths := synaraPaths(a.dir)
	if !safeLaunchDir(paths.Root, a.dir) {
		return false
	}
	if a.synaraUsesPrivateRuntime(rt) {
		if saved.Runtime == nil || !synaraPrivateRuntimeReady(a.synaraProfileOptions(rt, saved.Library), binary, rt.platform, *saved.Runtime) {
			return false
		}
	} else if saved.Runtime != nil {
		return false
	}
	shim := synaraCodexNormalShimPath(paths.Root, rt.platform)
	shimRelative, _ := filepath.Rel(paths.Root, shim)
	descriptorRelative := filepath.Join("adapters", synaraCodexNormalDescriptorName)
	sourceHash, sourceErr := a.synaraAdapterSourceHash()
	if sourceErr != nil || sourceHash == "" || saved.Files[shimRelative] != sourceHash || saved.Files[descriptorRelative] == "" {
		return false
	}
	normal, ok := saved.Providers[synaraCodexNormalID].(map[string]any)
	if !ok {
		return false
	}
	normalConfig, ok := normal["config"].(map[string]any)
	if !ok || normalConfig["binaryPath"] != shim {
		return false
	}
	target, err := synaraCodexNormalShimTarget(shim, rt.platform)
	codex, resolveErr := resolveOpenDesignCLI("codex-cli", rt)
	canonicalCodex, canonicalErr := filepath.EvalSymlinks(codex)
	if err != nil || resolveErr != nil || canonicalErr != nil || target != canonicalCodex {
		return false
	}
	if info, err := os.Lstat(shim); err != nil || rt.platform != "windows" && info.Mode().Perm() != 0700 {
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
		digest, err := openDesignFileHash(path, synaraCodexNormalFileLimit(relative, rt.platform))
		if err != nil || digest != expected {
			return false
		}
	}
	return synaraProfilesReady(a.synaraProfileOptions(rt, saved.Library), saved.Providers)
}

func (a *app) prepareSynara(library modelLibrary, rt clientLaunchRuntime, binary string) error {
	if err := a.synaraAvailability(binary, rt); err != nil {
		return err
	}
	version, _ := synaraVersion(binary, rt.platform)

	paths := synaraPaths(a.dir)
	for _, dir := range []string{paths.Root, paths.Secrets, paths.UIHome} {
		if err := safeEditorDir(a.dir, dir); err != nil {
			return err
		}
	}
	options := a.synaraProfileOptions(rt, library)
	options.ClaudeCaps = a.synaraClaudeCapabilities(options.ClaudeBinary, rt)
	planned, err := planSynaraProfiles(options)
	if err != nil {
		return err
	}
	planned.Providers = normalizeT3CodeProviders(planned.Providers)
	files := planned.Files
	adapterSource := a.synaraAdapterSource
	if adapterSource == nil {
		adapterSource = os.Executable
	}
	source, err := adapterSource()
	if err != nil {
		return errors.New("Cannot locate Kilo Proxy's native Synara Codex adapter.")
	}
	adapterFiles, err := planSynaraCodexNormalShim(paths.Root, options.CodexBinary, source, rt.platform)
	if err != nil {
		return err
	}
	files = append(files, adapterFiles...)

	adapterHash, err := a.synaraAdapterSourceHash()
	if err != nil || adapterHash != openDesignHash(adapterFiles[0].new) {
		return errors.New("Kilo Proxy's native Synara adapter changed during preparation. Restart Kilo Proxy and prepare again.")
	}
	fingerprint := a.synaraFingerprint(binary, library, rt)
	if fingerprint == "" {
		return errors.New("Cannot safely identify the private Synara configuration; no profile changes saved.")
	}
	saved := synaraPrepared{Library: library, Version: version, Fingerprint: fingerprint, Providers: planned.Providers, Files: map[string]string{}}
	if a.synaraUsesPrivateRuntime(rt) {
		privateRuntime, err := prepareSynaraPrivateRuntime(options, binary, rt.platform)
		if err != nil {
			return err
		}
		saved.Runtime = &privateRuntime
	}
	for _, file := range files {
		if file.path == paths.Settings || file.path == filepath.Join(paths.Electron, "synara-storage-origin-v1.json") {
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

func (a *app) synaraProfileAPI(w http.ResponseWriter, r *http.Request) {
	rt := a.launchRuntime()
	binary, resolveErr := rt.resolve("synara", "")
	if r.Method == http.MethodGet {
		paths := synaraPaths(a.dir)
		state := a.modelLibrary.snapshot()
		library := state.Library
		version, _ := synaraVersion(binary, rt.platform)
		message := "Prepare Synara to add Codex and Claude agents with normal and Kilo Proxy connections in a separate Synara window."
		if resolveErr == nil {
			resolveErr = a.synaraAvailability(binary, rt)
		}
		if resolveErr != nil {
			message = resolveErr.Error()
		}
		a.mu.Lock()
		saved, err := a.readSynaraPrepared()
		ready := resolveErr == nil && err == nil && a.synaraReady(saved, binary, rt)
		a.mu.Unlock()
		models := []synaraModelSummary{}
		if ready {
			message = "Synara prepared with Codex and Claude agents using normal and Kilo Proxy connections."
			library = saved.Library
			for _, model := range library.Models {
				models = append(models, synaraModelSummary{ID: model.ID, Name: model.DisplayName, ReasoningEffort: model.ReasoningEffort})
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
	if err := validateSynaraLibrary(library); err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	if len(library.Models) == 0 {
		jsonError(w, 400, "Choose a valid shared model library before preparing Synara.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.connectionReadyLocked() {
		jsonError(w, 409, "Connect the selected provider first.")
		return
	}
	paths := synaraPaths(a.dir)
	saved, readErr := a.readSynaraPrepared()
	unchanged := readErr == nil && reflect.DeepEqual(saved.Library, library) && a.synaraReady(saved, binary, rt)
	check := a.synaraCheckRunning
	if check == nil {
		check = synaraRunning
	}
	running, err := check(paths.Root)
	if err != nil {
		jsonError(w, 409, "Cannot verify whether Synara Kilo is running. Close its window and try again.")
		return
	}
	if (running || time.Now().Before(a.synaraLaunchUntil)) && !unchanged {
		jsonError(w, 409, "Quit the Synara Kilo window before changing its models or connection. Your regular Synara window can stay open.")
		return
	}
	if !unchanged {
		if err = a.prepareSynara(library, rt, binary); err != nil {
			jsonError(w, 409, err.Error())
			return
		}
	}
	models := []synaraModelSummary{}
	for _, model := range library.Models {
		models = append(models, synaraModelSummary{ID: model.ID, Name: model.DisplayName, ReasoningEffort: model.ReasoningEffort})
	}
	version, _ := synaraVersion(binary, rt.platform)
	jsonResponse(w, 200, map[string]any{"prepared": true, "library": library, "profileDir": paths.Root, "version": version, "models": models, "message": "Synara prepared with Codex and Claude agents using normal and Kilo Proxy connections."})
}

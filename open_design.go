package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tailscale/hujson"
)

const openDesignProfileEndpoint = "/api/open-design/profile"

var openDesignEngines = []string{"codex-cli", "claude", "opencode"}

type openDesignPrepareRequest struct {
	Engine  string        `json:"engine"`
	Library *modelLibrary `json:"library,omitempty"`
}

type openDesignPrepared struct {
	Engine      string            `json:"engine"`
	Library     modelLibrary      `json:"library"`
	Fingerprint string            `json:"fingerprint"`
	Files       map[string]string `json:"files"`
}

func validOpenDesignEngine(engine string) bool {
	for _, candidate := range openDesignEngines {
		if candidate == engine {
			return true
		}
	}
	return false
}

func openDesignRuntimeID(engine string) string {
	if engine == "codex-cli" {
		return "codex"
	}
	return engine
}

func openDesignHash(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// Hash the shipped executable once. A new Kilo process rebuilds the private
// OpenCode adapter when the installed app changes, without reading it on every poll.
var openDesignSourceHash = sync.OnceValues(func() (string, error) {
	path, err := os.Executable()
	if err == nil {
		path, err = filepath.EvalSymlinks(path)
	}
	if err != nil {
		return "", err
	}
	return openDesignFileHash(path, openDesignOpenCodeBinaryLimit)
})

func openDesignFileHash(path string, limit int64) (string, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > limit {
		return "", errors.New("Open Design profile files must be bounded regular files.")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return "", errors.New("Open Design profile changed during inspection.")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, limit+1))
	if err != nil || n > limit || n != before.Size() {
		return "", errors.New("Cannot read the complete Open Design profile.")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func openDesignManagedFiles(engine string) []string {
	switch engine {
	case "codex-cli":
		return []string{"config.toml", "models.json"}
	case "claude":
		return []string{"settings.json", "kilo-models.json"}
	case "opencode":
		return []string{"opencode.json", "kilo-models.json", filepath.Base(openDesignOpenCodeShimPath("")), openDesignOpenCodeDescriptorName}
	}
	return nil
}

func openDesignManagedFileHash(dir, name string) (string, error) {
	limit := int64(catalogLimit)
	if name == filepath.Base(openDesignOpenCodeShimPath("")) {
		limit = openDesignOpenCodeBinaryLimit
	}
	return openDesignFileHash(filepath.Join(dir, name), limit)
}

func (a *app) openDesignReadPrepared() (openDesignPrepared, error) {
	var saved openDesignPrepared
	data, err := readCatalogFile(filepath.Join(openDesignProfilePaths(a.dir).Root, "selection.json"))
	if err == nil {
		err = json.Unmarshal(data, &saved)
	}
	if err == nil && (!validOpenDesignEngine(saved.Engine) || len(saved.Library.Models) == 0 || validateModelLibrary(saved.Library) != nil) {
		err = errors.New("Invalid Open Design profile. Prepare it again.")
	}
	return saved, err
}

// The private manifest contains hashes, never either API credential.
func (a *app) openDesignFingerprint(engine, binary string, library modelLibrary) string {
	source := ""
	if engine == "opencode" {
		var err error
		source, err = openDesignSourceHash()
		if err != nil {
			return ""
		}
	}
	data, _ := json.Marshal([]any{engine, binary, library, a.config.Port, a.config.LocalKey, a.config.OrgID, a.config.ImageGeneration, source})
	return openDesignHash(data)
}

func readOpenDesignPreferences(path string) (map[string]any, error) {
	data, err := readCatalogFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, errors.New("Cannot safely read Open Design settings.")
	}
	value, err := hujson.Parse(data)
	if err != nil || value.Value.Kind() != '{' || !uniqueEditorJSON(value) {
		return nil, errors.New("Open Design settings must contain a valid JSON object with unique keys.")
	}
	var result map[string]any
	if json.Unmarshal(data, &result) != nil {
		return nil, errors.New("Open Design settings must be valid JSON.")
	}
	return result, nil
}

func mergeOpenDesignPreferences(old map[string]any, engine string, env map[string]string) (map[string]any, error) {
	encoded, _ := json.Marshal(old)
	var prefs map[string]any
	_ = json.Unmarshal(encoded, &prefs)
	if prefs == nil {
		prefs = map[string]any{}
	}
	id := openDesignRuntimeID(engine)
	for _, entry := range []struct {
		key   string
		value any
	}{
		{"agentCliEnv", env}, {"agentCliEnvIntent", map[string]bool{"apiKeyOverride": true}}, {"agentModels", map[string]string{"model": "default"}},
	} {
		record, ok := prefs[entry.key].(map[string]any)
		if prefs[entry.key] != nil && !ok {
			return nil, errors.New("An Open Design agent settings section is not an object.")
		}
		if record == nil {
			record = map[string]any{}
		}
		record[id] = entry.value
		prefs[entry.key] = record
	}
	prefs["agentId"] = id
	// Leave onboarding, privacy choices, existing projects and renderer mode to Open Design.
	return prefs, nil
}

func (a *app) openDesignEngineInfo(rt clientLaunchRuntime) map[string]clientLaunchAvailability {
	result := map[string]clientLaunchAvailability{}
	for _, engine := range openDesignEngines {
		name, _ := launchClientIdentity(engine)
		path, err := rt.resolve(engine, "")
		if err == nil && engine == "opencode" && rt.platform == "windows" {
			err = validateOpenDesignShimBinary(path, "windows", false)
		}
		info := clientLaunchAvailability{Name: name, Kind: "cli", Path: path, Available: err == nil}
		if err != nil {
			info.Reason = a.clientLaunchMessage(err.Error())
		}
		result[engine] = info // Open Design hosts the process; no interactive terminal is required.
	}
	return result
}

func (a *app) openDesignFilesMatch(saved openDesignPrepared) bool {
	paths := openDesignProfilePaths(a.dir)
	dir := filepath.Join(paths.Profiles, saved.Engine)
	if !safeLaunchDir(dir, a.dir) || !safeLaunchDir(paths.Data, a.dir) {
		return false
	}
	expected := openDesignManagedFiles(saved.Engine)
	if len(saved.Files) != len(expected) {
		return false
	}
	for _, name := range expected {
		hash, err := openDesignManagedFileHash(dir, name)
		if err != nil || saved.Files[name] != hash {
			return false
		}
	}
	return true
}

func (a *app) openDesignReady(saved openDesignPrepared, binary string) bool {
	if saved.Fingerprint == "" || saved.Fingerprint != a.openDesignFingerprint(saved.Engine, binary, saved.Library) || !a.openDesignFilesMatch(saved) {
		return false
	}
	paths := openDesignProfilePaths(a.dir)
	prefs, err := readOpenDesignPreferences(paths.Config)
	if err != nil {
		return false
	}
	env, err := openDesignEnginePreferences(saved.Engine, filepath.Join(paths.Profiles, saved.Engine), binary, a.config.Port, a.config.LocalKey)
	if err != nil {
		return false
	}
	expected, err := mergeOpenDesignPreferences(prefs, saved.Engine, env)
	if err != nil {
		return false
	}
	before, _ := json.Marshal(prefs)
	after, _ := json.Marshal(expected)
	return launchEqualJSON(before, after)
}

func (a *app) openDesignProfileAPI(w http.ResponseWriter, r *http.Request) {
	rt := a.launchRuntime()
	if r.Method == http.MethodGet {
		paths := openDesignProfilePaths(a.dir)
		library := a.modelLibrary.snapshot().Library
		saved, err := a.openDesignReadPrepared()
		engine := "codex-cli"
		if err == nil {
			engine = saved.Engine
			library = saved.Library
		}
		engines := a.openDesignEngineInfo(rt)
		a.mu.Lock()
		ready := err == nil && engines[engine].Available && a.openDesignReady(saved, engines[engine].Path)
		a.mu.Unlock()
		jsonResponse(w, 200, map[string]any{"engine": engine, "library": library, "engines": engines, "prepared": ready, "profileDir": paths.Root, "configPath": paths.Config})
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, 405, "Method not allowed.")
		return
	}
	var input openDesignPrepareRequest
	if !decodeBody(w, r, &input) {
		return
	}
	if !validOpenDesignEngine(input.Engine) {
		jsonError(w, 400, "Choose a supported Open Design CLI engine.")
		return
	}
	if reason := launchClientPlatformReason("open-design", rt.platform); reason != "" {
		a.clientLaunchError(w, 409, reason)
		return
	}
	if !a.launchMu.TryLock() {
		jsonError(w, 409, "Another agent is being prepared. Try again.")
		return
	}
	defer a.launchMu.Unlock()
	binary, err := rt.resolve(input.Engine, "")
	if err == nil && input.Engine == "opencode" && rt.platform == "windows" {
		err = validateOpenDesignShimBinary(binary, "windows", false)
	}
	if err != nil {
		a.clientLaunchError(w, 409, err.Error())
		return
	}
	state := a.modelLibrary.snapshot()
	library := state.Library
	if input.Library != nil {
		library = *input.Library
	} else if state.RecoveryRequired || state.Warning != "" {
		jsonError(w, 409, "Recover and save your shared models first.")
		return
	}
	if err = validateModelLibrary(library); err != nil || len(library.Models) == 0 {
		jsonError(w, 400, "Choose a valid model library with at least one model.")
		return
	}
	caps := claudeCapabilities{}
	if input.Engine == "claude" && a.editorTestRoot == "" {
		caps = installedClaude()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.apiKey == "" || a.config.OrgID == "" || a.authPending() || a.connectionNeedsSave {
		jsonError(w, 409, "Save your Kilo account and team connection first.")
		return
	}
	paths := openDesignProfilePaths(a.dir)
	saved, readErr := a.openDesignReadPrepared()
	fingerprint := a.openDesignFingerprint(input.Engine, binary, library)
	if fingerprint == "" {
		jsonError(w, 409, "Cannot read Kilo Proxy's native OpenCode adapter.")
		return
	}
	unchanged := readErr == nil && saved.Fingerprint == fingerprint && a.openDesignReady(saved, binary)
	check := a.openDesignCheckRunning
	if check == nil {
		check = openDesignRunning
	}
	running, err := check(paths.Namespace)
	if err != nil {
		jsonError(w, 409, "Could not verify whether Open Design is running. Close its Kilo window and try again.")
		return
	}
	if (running || time.Now().Before(a.openDesignLaunchUntil)) && !unchanged {
		jsonError(w, 409, "Quit the Open Design Kilo instance before changing its engine, models or connection, then launch again.")
		return
	}
	if !unchanged {
		if err = a.prepareOpenDesignProfile(input.Engine, binary, library, caps, fingerprint); err != nil {
			jsonError(w, 409, err.Error())
			return
		}
	}
	jsonResponse(w, 200, map[string]any{"engine": input.Engine, "library": library, "prepared": true, "profileDir": paths.Root, "configPath": paths.Config})
}

// Caller owns launchMu and mu. Each writer validates its destinations; a failed
// preparation leaves the final manifest absent or stale, never launchable.
func (a *app) prepareOpenDesignProfile(engine, binary string, library modelLibrary, caps claudeCapabilities, fingerprint string) error {
	paths := openDesignProfilePaths(a.dir)
	dir := filepath.Join(paths.Profiles, engine)
	for _, path := range []string{paths.Data, dir} {
		if err := safeEditorDir(a.dir, path); err != nil {
			return err
		}
	}
	prefs, err := readOpenDesignPreferences(paths.Config)
	if err != nil {
		return err
	}
	env, err := openDesignEnginePreferences(engine, dir, binary, a.config.Port, a.config.LocalKey)
	if err != nil {
		return err
	}
	prefs, err = mergeOpenDesignPreferences(prefs, engine, env)
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(prefs, "", "  ")
	config, err := prepareProfileFile(paths.Config, append(data, '\n'))
	if err != nil {
		return err
	}
	selectionPath := filepath.Join(paths.Root, "selection.json")
	if _, err = prepareProfileFile(selectionPath, []byte("{}\n")); err != nil {
		return err
	}
	if err = prepareOpenDesignEngineProfile(dir, engine, library, readNativeCatalogCache(a.dir, a.config.OrgID), caps, a.config.Port, a.config.LocalKey, a.config.ImageGeneration); err != nil {
		return err
	}
	if engine == "opencode" {
		if _, err = prepareOpenDesignOpenCodeShim(dir, binary); err != nil {
			return err
		}
	}
	saved := openDesignPrepared{Engine: engine, Library: library, Fingerprint: fingerprint, Files: map[string]string{}}
	for _, name := range openDesignManagedFiles(engine) {
		hash, err := openDesignManagedFileHash(dir, name)
		if err != nil {
			return err
		}
		saved.Files[name] = hash
	}
	data, _ = json.MarshalIndent(saved, "", "  ")
	selection, err := prepareProfileFile(selectionPath, append(data, '\n'))
	if err != nil {
		return err
	}
	_, err = saveEditorFiles([]profileFile{config, selection})
	return err
}

func (a *app) applyOpenDesignProfile(plan *clientLaunchPlan, engine string, rt clientLaunchRuntime) error {
	saved, err := a.openDesignReadPrepared()
	if err != nil || engine != "" && saved.Engine != engine {
		return errors.New("Prepare the selected Open Design CLI engine before launching.")
	}
	binary, err := rt.resolve(saved.Engine, "")
	if err != nil {
		return err
	}
	if !a.openDesignReady(saved, binary) {
		return errors.New("Open Design settings changed. Prepare the CLI engine again before launching.")
	}
	plan.Env["KILO_LOCAL_API_KEY"] = a.config.LocalKey
	plan.Unset = append(plan.Unset, nativeClaudeResetEnv...)
	plan.Unset = append(plan.Unset, "OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_BASE_URL", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "OPENCODE_CONFIG", "OPENCODE_CONFIG_CONTENT")
	return configureOpenDesignLaunch(plan, a.dir, rt.platform)
}

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tailscale/hujson"
)

const openMausBotEndpoint = "/api/clients/openmausbot"
const openMausBotInstance = "kilo-local"
const openMausBotMessage = "Models and the initial selection are ready. OpenMausBot displays exact model IDs; custom names and reasoning levels are not supported by this driver. The smallest shared context budget sets one automatic compaction threshold; OpenMausBot may compact earlier."

type openMausBotPaths struct{ Root, Data, UI, Config, Selection string }

func openMausBotProfilePaths(dir string) openMausBotPaths {
	root := filepath.Join(dir, "openmausbot")
	data := filepath.Join(root, "data")
	return openMausBotPaths{root, data, filepath.Join(root, "ui"), filepath.Join(data, "config.json"), filepath.Join(root, "selection.json")}
}

type openMausBotPrepared struct {
	Library     modelLibrary `json:"library"`
	Fingerprint string       `json:"fingerprint"`
	CompactAt   int          `json:"compactAt"`
}

func openMausBotFingerprint(library modelLibrary, port int, key string, compactAt int) string {
	data, _ := json.Marshal([]any{library, port, key, compactAt})
	return openDesignHash(data)
}

func openMausBotCompaction(library modelLibrary, catalog []modelInfo) (int, error) {
	metadata := map[string]modelInfo{}
	for _, model := range catalog {
		metadata[model.ID] = model
	}
	limit := 10000000
	for _, model := range library.Models {
		policy, err := contextPolicyForChoice(contextChoiceFromLibrary(model, metadata[model.ID]))
		if err != nil {
			return 0, err
		}
		limit = min(limit, policy.AutoCompactTokenLimit)
	}
	return max(1, limit), nil
}

// Own only our named instance, the default selection, and global compaction.
// The private profile can contain other providers, projects and preferences.
func mergeOpenMausBotConfig(old []byte, library modelLibrary, port int, key string, compactAt int) ([]byte, error) {
	if validateModelLibrary(library) != nil || len(library.Models) == 0 {
		return nil, errors.New("Choose a valid model library before preparing OpenMausBot.")
	}
	if port < 1024 || port > 65535 || key == "" || strings.ContainsAny(key, "\x00\r\n") || compactAt < 1 || compactAt > 10000000 {
		return nil, errors.New("Invalid local OpenMausBot connection or compaction budget.")
	}
	if len(bytes.TrimSpace(old)) == 0 {
		old = []byte("{}")
	}
	tree, err := hujson.Parse(old)
	if err != nil || tree.Value.Kind() != '{' || !uniqueEditorJSON(tree) || !json.Valid(old) {
		return nil, errors.New("OpenMausBot settings must contain one valid JSON object with unique keys; nothing saved.")
	}
	for _, path := range []string{"/instances", "/context", "/newBotDefaults", "/newBotDefaults/profile"} {
		if value := tree.Find(path); value != nil && value.Value.Kind() != '{' {
			return nil, errors.New("OpenMausBot settings contain an invalid settings section; nothing saved.")
		}
	}
	instancePath := "/instances/" + openMausBotInstance
	if value := tree.Find(instancePath); value != nil && !managedOpenMausBotInstance(*value) {
		return nil, errors.New("OpenMausBot instance kilo-local is already used by another configuration. Rename that instance before preparing Kilo; nothing saved.")
	}
	for _, path := range []string{instancePath + "/environment", instancePath + "/config"} {
		if value := tree.Find(path); value != nil && value.Value.Kind() != '{' {
			return nil, errors.New("OpenMausBot's managed instance contains an invalid settings section; nothing saved.")
		}
	}
	// This driver uses its first catalog entry as its fallback default even
	// when config.model is set. Keep the shared default first; retain the
	// relative order of all remaining choices without editing the library.
	models := []string{library.DefaultModel}
	for _, model := range library.Models {
		if model.ID != library.DefaultModel {
			models = append(models, model.ID)
		}
	}
	operations := []map[string]any{}
	add := func(path string, value any) {
		operations = append(operations, map[string]any{"op": "add", "path": path, "value": value})
	}
	if tree.Find("/instances") == nil {
		add("/instances", map[string]any{})
	}
	if tree.Find(instancePath) == nil {
		add(instancePath, map[string]any{})
	}
	for _, field := range []struct {
		name  string
		value any
	}{{"driver", "openai-compat"}, {"displayName", "Kilo Proxy"}, {"enabled", true}, {"access", "api"}} {
		add(instancePath+"/"+field.name, field.value)
	}
	for _, section := range []string{"environment", "config"} {
		if tree.Find(instancePath+"/"+section) == nil {
			add(instancePath+"/"+section, map[string]any{})
		}
	}
	add(instancePath+"/environment/KILO_LOCAL_API_KEY", key)
	// Preserve appearance and unrelated preferences, while removing alternate
	// key/catalog overrides that would supersede this managed connection.
	for _, name := range []string{"key", "catalog"} {
		if tree.Find(instancePath+"/config/"+name) != nil {
			operations = append(operations, map[string]any{"op": "remove", "path": instancePath + "/config/" + name})
		}
	}
	for _, field := range []struct {
		name  string
		value any
	}{{"url", "http://127.0.0.1:" + strconv.Itoa(port) + "/v1"}, {"apiKeyEnv", "KILO_LOCAL_API_KEY"}, {"model", library.DefaultModel}, {"managedModels", models}, {"provider", ""}, {"tools", true}} {
		add(instancePath+"/config/"+field.name, field.value)
	}
	selection := map[string]string{"instanceId": openMausBotInstance, "model": library.DefaultModel}
	add("/defaultModelSelection", selection)
	// OpenMausBot's newer bot creation preference may take precedence over the
	// top-level default. Keep both in agreement, preserving other bot defaults.
	if tree.Find("/newBotDefaults/profile") != nil {
		add("/newBotDefaults/profile/modelSelection", selection)
	}
	if tree.Find("/context") == nil {
		add("/context", map[string]any{})
	}
	add("/context/autoCompact", true)
	add("/context/compactAt", compactAt)
	patch, _ := json.Marshal(operations)
	if tree.Patch(patch) != nil {
		return nil, errors.New("Cannot update the private OpenMausBot configuration; nothing saved.")
	}
	result := tree.Pack()
	if len(result) > catalogLimit {
		return nil, errors.New("OpenMausBot settings exceed the size limit.")
	}
	return result, nil
}

func managedOpenMausBotInstance(value hujson.Value) bool {
	var instance struct {
		Driver string `json:"driver"`
		Config struct {
			URL    string `json:"url"`
			KeyEnv string `json:"apiKeyEnv"`
		} `json:"config"`
	}
	if json.Unmarshal(value.Pack(), &instance) != nil || instance.Driver != "openai-compat" || instance.Config.KeyEnv != "KILO_LOCAL_API_KEY" {
		return false
	}
	u, err := url.Parse(instance.Config.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "/v1" || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	return err == nil && port >= 1024 && port <= 65535
}

func (a *app) readOpenMausBotPrepared() (openMausBotPrepared, error) {
	var saved openMausBotPrepared
	paths := openMausBotProfilePaths(a.dir)
	if !safeLaunchDir(paths.Root, a.dir) {
		return saved, errors.New("The private OpenMausBot profile is missing or unsafe.")
	}
	data, err := readCatalogFile(paths.Selection)
	if err == nil {
		err = json.Unmarshal(data, &saved)
	}
	if err == nil && (validateModelLibrary(saved.Library) != nil || len(saved.Library.Models) == 0 || saved.CompactAt < 1 || saved.CompactAt > 10000000) {
		err = errors.New("Invalid private OpenMausBot selection.")
	}
	return saved, err
}

// Caller owns mu. Never expose the private config or local credential in GET.
func (a *app) openMausBotReady(saved openMausBotPrepared) bool {
	paths := openMausBotProfilePaths(a.dir)
	if !safeLaunchDir(paths.Data, a.dir) || !safeLaunchDir(paths.UI, a.dir) || saved.Fingerprint != openMausBotFingerprint(saved.Library, a.config.Port, a.config.LocalKey, saved.CompactAt) {
		return false
	}
	old, err := readCatalogFile(paths.Config)
	if err != nil {
		return false
	}
	want, err := mergeOpenMausBotConfig(old, saved.Library, a.config.Port, a.config.LocalKey, saved.CompactAt)
	return err == nil && launchEqualJSON(old, want)
}

func (a *app) openMausBotProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.mu.Lock()
		saved, err := a.readOpenMausBotPrepared()
		ready := err == nil && a.openMausBotReady(saved)
		if err != nil {
			saved = openMausBotPrepared{}
		}
		a.mu.Unlock()
		jsonResponse(w, 200, map[string]any{"prepared": ready, "configPath": openMausBotProfilePaths(a.dir).Config, "modelCount": len(saved.Library.Models), "initialModel": saved.Library.DefaultModel, "message": openMausBotMessage})
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, 405, "Method not allowed.")
		return
	}
	var input struct {
		Library *modelLibrary `json:"library,omitempty"`
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		jsonError(w, http.StatusUnsupportedMediaType, "JSON required.")
		return
	}
	// A complete 50-model library can exceed the generic 16 KiB action body;
	// use the same bounded JSON contract as the shared-library endpoint.
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, modelLibraryLimit))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		jsonError(w, http.StatusBadRequest, "Invalid OpenMausBot library JSON.")
		return
	}
	if !a.launchMu.TryLock() {
		jsonError(w, 409, "Another agent is being prepared. Try again.")
		return
	}
	defer a.launchMu.Unlock()
	rt := a.launchRuntime()
	binary, err := rt.resolve("openmausbot", "")
	if err == nil {
		err = openMausBotCompatibility(binary, rt.platform)
	}
	if err != nil {
		jsonError(w, 409, "Install OpenMausBot 0.1.92 or newer with managed model support, then refresh installed apps.")
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
	if validateModelLibrary(library) != nil || len(library.Models) == 0 {
		jsonError(w, 400, "Choose a valid model library with at least one model.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.connectionReadyLocked() {
		jsonError(w, 409, "Save an available provider connection before preparing OpenMausBot.")
		return
	}
	compactAt, err := openMausBotCompaction(library, readNativeCatalogCache(a.dir, a.catalogScopeLocked()))
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	saved, readErr := a.readOpenMausBotPrepared()
	fingerprint := openMausBotFingerprint(library, a.config.Port, a.config.LocalKey, compactAt)
	unchanged := readErr == nil && saved.Fingerprint == fingerprint && a.openMausBotReady(saved)
	check := a.openMausBotCheckRunning
	if check == nil {
		check = openMausBotRunning
	}
	running, err := check(openMausBotProfilePaths(a.dir))
	if err != nil {
		jsonError(w, 409, "Cannot verify whether the OpenMausBot Kilo window is closed. Quit that instance and try again.")
		return
	}
	if !unchanged && (running || time.Now().Before(a.openMausBotLaunchUntil)) {
		jsonError(w, 409, "Quit the OpenMausBot Kilo instance before changing models or connection, then open it again.")
		return
	}
	if !unchanged {
		paths := openMausBotProfilePaths(a.dir)
		// Precreate the data directory before Electron starts: upstream migrates
		// ~/.opengrokbot when OMB_DATA_DIR does not yet exist.
		for _, dir := range []string{paths.Root, paths.Data, paths.UI} {
			if err := safeEditorDir(a.dir, dir); err != nil {
				jsonError(w, 409, "Cannot create a safe private OpenMausBot profile.")
				return
			}
			if err := os.Chmod(dir, 0700); err != nil {
				jsonError(w, 409, "Cannot protect the private OpenMausBot profile.")
				return
			}
		}
		old, err := readCatalogFile(paths.Config)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			jsonError(w, 409, "Cannot safely read the private OpenMausBot configuration.")
			return
		}
		config, err := mergeOpenMausBotConfig(old, library, a.config.Port, a.config.LocalKey, compactAt)
		if err != nil {
			jsonError(w, 409, err.Error())
			return
		}
		configFile, err := prepareProfileFile(paths.Config, config)
		if err != nil {
			jsonError(w, 409, "Cannot safely prepare the private OpenMausBot configuration.")
			return
		}
		data, _ := json.MarshalIndent(openMausBotPrepared{library, fingerprint, compactAt}, "", "  ")
		selection, err := prepareProfileFile(paths.Selection, append(data, '\n'))
		if err != nil {
			jsonError(w, 409, "Cannot safely prepare the private OpenMausBot selection.")
			return
		}
		if _, err = saveEditorFiles([]profileFile{configFile, selection}); err != nil {
			jsonError(w, 409, "Cannot save the private OpenMausBot profile.")
			return
		}
	}
	jsonResponse(w, 200, map[string]any{"prepared": true, "configPath": openMausBotProfilePaths(a.dir).Config, "modelCount": len(library.Models), "initialModel": library.DefaultModel, "message": openMausBotMessage})
}

func (a *app) applyOpenMausBotLaunch(plan *clientLaunchPlan, platform string) error {
	saved, err := a.readOpenMausBotPrepared()
	if err != nil || !a.openMausBotReady(saved) {
		return profileLaunchError("OpenMausBot")
	}
	if err := openMausBotCompatibility(plan.Executable, platform); err != nil {
		return err
	}
	if platform == "macos" || platform == "darwin" {
		plan.Executable = filepath.Join(plan.Executable, "Contents", "MacOS", "OpenMausBot")
	}
	paths := openMausBotProfilePaths(a.dir)
	plan.Args = []string{"--user-data-dir=" + paths.UI}
	plan.Env["OMB_DATA_DIR"] = paths.Data
	plan.Env["KILO_LOCAL_API_KEY"] = a.config.LocalKey
	plan.Unset = append(plan.Unset, "ELECTRON_RUN_AS_NODE", "OMB_USER_DATA", "OMB_PORT", "OMB_DESKTOP_PARENT", "OMB_STATIC_DIR", "OMB_RESOURCES_PATH", "OMB_BROWSER_CONNECTION", "OPENMAUSBOT_INTERNAL_DATA_DIR_LEASE")
	return nil
}

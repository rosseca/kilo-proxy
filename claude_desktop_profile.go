package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tailscale/hujson"
)

// Claude Desktop 2.9939.2 stores local third-party deployments in configLibrary.
// This UUID belongs only to Kilo; other library entries and settings are retained.
const claudeDesktopProfileID = "58dca950-b244-4d6c-88d9-1c42625f9769"
const claudeDesktopAliasPrefix = "claude-kilo-v1-"

// Match Desktop's config-library ID guard, including existing non-RFC IDs.
// Only hexadecimal characters and hyphens are accepted, so these cannot escape
// the library directory. Kilo itself always creates a normal UUID v4.
var claudeDesktopUUID = regexp.MustCompile(`^[a-f0-9-]{36}$`)
var claudeDesktopModelID = regexp.MustCompile(`^(?:anthropic/)?claude-[A-Za-z0-9][A-Za-z0-9._:-]*$`)
var claudeDesktopOtherModel = regexp.MustCompile(`(?i)ark-code|astron|command-r|deepseek|doubao|gemini|gemma|glm|gpt|grok|hermes|hy3|kimi|lfm|\bling\b|llama|longcat|mimo|minimax|mistral|mixtral|moonshot|nemotron|openai|phi-|qianfan|qwen|tc-code|\bunic\b|yi-|stepfun|step-3|seed-|bytedance|hunyuan|granite|amazon\.nova|nova-|devstral|ministral|ernie|codex|arcee|trinity|abab|phi\d|\bk2\.|\bm2\.|jamba|arctic|solar|mercury|zamba|kat-coder|\bds-|dpsk`)

// This predicate identifies native Claude routes. Experimental aliases are
// generated only when preparing Desktop's profile, never stored as real IDs.
func claudeDesktopModelSupported(id string) bool {
	return catalogID.MatchString(id) && claudeDesktopModelID.MatchString(id) && !claudeDesktopOtherModel.MatchString(id) && !claudeDesktopReservedAlias(id)
}

func validateClaudeDesktopSelection(s editorSelection) error {
	return validateClaudeDesktopSelectionMode(s, false)
}

func claudeDesktopAlias(id string) string {
	hash := sha256.Sum256([]byte(id))
	// Decimal retains all 256 bits without letter sequences such as "abab",
	// which Desktop's provider-name denylist rejects even inside a hex digest.
	return claudeDesktopAliasPrefix + new(big.Int).SetBytes(hash[:]).String()
}

func claudeDesktopReservedAlias(id string) bool {
	return strings.HasPrefix(strings.TrimPrefix(id, "anthropic/"), claudeDesktopAliasPrefix)
}

func validateClaudeDesktopSelectionMode(s editorSelection, experimental bool) error {
	if len(s.Models) < 1 || len(s.Models) > 50 {
		return errors.New("Choose 1–50 models.")
	}
	seen := map[string]bool{}
	for _, m := range s.Models {
		if !catalogID.MatchString(m.ID) || claudeDesktopReservedAlias(m.ID) || seen[m.ID] || len([]rune(m.Name)) > 80 || strings.IndexFunc(m.Name, func(r rune) bool { return r < 32 || r == 127 }) >= 0 || m.Context != 0 || m.Output != 0 {
			return errors.New("Choose real model IDs with valid names; Desktop context/output overrides are not supported.")
		}
		if !experimental && !claudeDesktopModelSupported(m.ID) {
			return errors.New("Experimental Claude Desktop models are disabled. Enable them or prepare a profile containing only Claude models.")
		}
		seen[m.ID] = true
	}
	if !seen[s.Initial] {
		return errors.New("Choose an initial model from the selection.")
	}
	return nil
}

type claudeDesktopProfilePaths struct {
	Root, ProfileDir, ConfigPath, MetaPath, ModePath, SelectionPath, Platform string
}

func (a *app) claudeDesktopPaths() (claudeDesktopProfilePaths, error) {
	rt := a.launchRuntime()
	home := rt.home
	if a.editorTestRoot != "" {
		home = a.editorTestRoot
	}
	if !filepath.IsAbs(home) || !filepath.IsAbs(a.dir) {
		return claudeDesktopProfilePaths{}, errors.New("Cannot locate Claude Desktop settings.")
	}
	root := home
	var dir string
	realHome, _ := os.UserHomeDir()
	useSystemEnv := a.editorTestRoot == "" && filepath.Clean(home) == filepath.Clean(realHome)
	switch rt.platform {
	case "macos", "darwin":
		dir = filepath.Join(home, "Library", "Application Support", "Claude-3p")
	case "windows":
		base := filepath.Join(home, "AppData", "Local")
		if env := os.Getenv("LOCALAPPDATA"); useSystemEnv && runtime.GOOS == "windows" && filepath.IsAbs(env) {
			base = env
		}
		dir = filepath.Join(base, "Claude-3p")
		if !claudeDesktopWithin(home, base) {
			root = base
		}
	case "linux":
		base := filepath.Join(home, ".config")
		if env := os.Getenv("XDG_CONFIG_HOME"); useSystemEnv && runtime.GOOS == "linux" && filepath.IsAbs(env) {
			base = env
		}
		dir = filepath.Join(base, "Claude-3p")
		if !claudeDesktopWithin(home, base) {
			root = base
		}
	default:
		return claudeDesktopProfilePaths{}, errors.New("Unsupported Claude Desktop platform.")
	}
	return claudeDesktopProfilePaths{
		Root: root, ProfileDir: dir, Platform: rt.platform,
		ConfigPath:    filepath.Join(dir, "configLibrary", claudeDesktopProfileID+".json"),
		MetaPath:      filepath.Join(dir, "configLibrary", "_meta.json"),
		ModePath:      filepath.Join(dir, "claude_desktop_config.json"),
		SelectionPath: filepath.Join(a.dir, "claude-desktop-models.json"),
	}, nil
}

func claudeDesktopWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func decodeClaudeDesktopObject(data []byte) (map[string]json.RawMessage, error) {
	tree, err := hujson.Parse(data)
	if err != nil || !json.Valid(data) || tree.Value.Kind() != '{' || !uniqueEditorJSON(tree) {
		return nil, errors.New("Claude Desktop settings must be JSON objects with unique keys; nothing saved.")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil || obj == nil {
		return nil, errors.New("Invalid Claude Desktop settings; nothing saved.")
	}
	return obj, nil
}

func readClaudeDesktopObject(path string) (map[string]json.RawMessage, []byte, bool, error) {
	data, err := readCatalogFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, errors.New("Cannot safely read Claude Desktop settings; nothing saved.")
	}
	obj, err := decodeClaudeDesktopObject(data)
	return obj, data, true, err
}

func claudeDesktopSet(obj map[string]json.RawMessage, key string, value any) {
	obj[key], _ = json.Marshal(value)
}

func claudeDesktopEncoded(obj map[string]json.RawMessage, old []byte) ([]byte, error) {
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil || len(data)+1 > catalogLimit {
		return nil, errors.New("Claude Desktop settings exceed the size limit.")
	}
	if len(old) > 0 && launchEqualJSON(old, data) {
		return old, nil
	}
	return append(data, '\n'), nil
}

func prepareClaudeDesktopFile(path string, data []byte) (profileFile, error) {
	f, err := prepareProfileFile(path, data)
	if err != nil {
		return f, err
	}
	// Replacing an otherwise unchanged file also repairs loose permissions.
	// The atomic writer and its backups use private 0600 temporary files.
	if runtime.GOOS != "windows" && f.exists && !f.changed {
		if info, err := os.Lstat(path); err != nil {
			return f, errors.New("Cannot inspect Claude Desktop profile permissions.")
		} else if info.Mode().Perm()&0077 != 0 {
			f.changed = true
			if backup, err := os.Lstat(path + ".bak"); err == nil && !backup.Mode().IsRegular() || err != nil && !errors.Is(err, os.ErrNotExist) {
				return f, errors.New("Unsafe Claude Desktop profile backup; nothing saved.")
			}
		}
	}
	return f, nil
}

func claudeDesktopOwnedConfig(s editorSelection, port int, key string, experimental ...bool) map[string]any {
	models := make([]map[string]string, 0, len(s.Models))
	useAliases := len(experimental) > 0 && experimental[0]
	add := func(m editorModel) {
		name := m.Name
		if name == "" {
			name = m.ID
		}
		id := m.ID
		if useAliases && !claudeDesktopModelSupported(id) {
			id = claudeDesktopAlias(id)
		}
		models = append(models, map[string]string{"name": id, "labelOverride": name})
	}
	for _, m := range s.Models {
		if m.ID == s.Initial {
			add(m)
		}
	}
	for _, m := range s.Models {
		if m.ID != s.Initial {
			add(m)
		}
	}
	return map[string]any{
		"inferenceProvider": "gateway", "inferenceCredentialKind": "static",
		"inferenceGatewayBaseUrl": "http://127.0.0.1:" + strconv.Itoa(port),
		"inferenceGatewayApiKey":  key, "inferenceGatewayAuthScheme": "bearer",
		"modelDiscoveryEnabled": false, "inferenceModels": models,
		"chatTabEnabled": true, "coworkTabEnabled": true, "isClaudeCodeForDesktopEnabled": true,
		"deploymentDisplayName": "Kilo Proxy", "deploymentOrganizationUuid": claudeDesktopProfileID,
	}
}

func claudeDesktopMetadata(obj map[string]json.RawMessage, exists bool, apply bool) error {
	var entries []map[string]json.RawMessage
	if exists {
		if raw, ok := obj["entries"]; !ok || json.Unmarshal(raw, &entries) != nil || entries == nil {
			return errors.New("Invalid Claude Desktop config library metadata; nothing saved.")
		}
	}
	seen, own := map[string]bool{}, -1
	for i, entry := range entries {
		var id, name string
		if entry == nil || json.Unmarshal(entry["id"], &id) != nil || !claudeDesktopUUID.MatchString(id) || seen[strings.ToLower(id)] || json.Unmarshal(entry["name"], &name) != nil || name == "" {
			return errors.New("Claude Desktop config library entries must have unique UUIDs and names; nothing saved.")
		}
		seen[strings.ToLower(id)] = true
		if id == claudeDesktopProfileID {
			own = i
		}
	}
	if raw, ok := obj["appliedId"]; ok {
		var id string
		if json.Unmarshal(raw, &id) != nil || id != "" && (!claudeDesktopUUID.MatchString(id) || !seen[strings.ToLower(id)]) {
			return errors.New("Invalid active Claude Desktop config UUID; nothing saved.")
		}
	}
	if !apply {
		return nil
	}
	if own < 0 {
		entries = append(entries, map[string]json.RawMessage{})
		own = len(entries) - 1
	}
	claudeDesktopSet(entries[own], "id", claudeDesktopProfileID)
	claudeDesktopSet(entries[own], "name", "Kilo Proxy")
	claudeDesktopSet(obj, "entries", entries)
	claudeDesktopSet(obj, "appliedId", claudeDesktopProfileID)
	// Desktop's own Apply action clears this pointer: otherwise it takes
	// precedence over appliedId and would silently ignore the selected config.
	delete(obj, "hybridPointer")
	return nil
}

// These policy keys control the app itself and do not suppress local configs.
var claudeDesktopAppPolicy = map[string]bool{
	"disableAutoUpdates": true, "autoUpdaterEnforcementHours": true,
	"updateViaUpdatesHost": true, "relaunchEnforcementHours": true,
	"configRecheckIntervalMinutes": true, "egressProxyUrl": true, "egressProxyPacUrl": true,
	"dangerousMaxVersion": true, "disableWslSessions": true,
	"claudeCodeProcessWrapperEnabled": true, "relocateUncUserData": true,
}

func claudeDesktopManagedKeys(keys []string) bool {
	for _, k := range keys {
		if !claudeDesktopAppPolicy[k] {
			return true
		}
	}
	return false
}

// Read only the names of top-level managed preferences, never return secrets.
func claudeDesktopPlistKeys(data []byte) ([]string, error) {
	if bytes.HasPrefix(data, []byte("bplist")) {
		if runtime.GOOS != "darwin" {
			return nil, errors.New("Cannot inspect managed Claude Desktop preferences.")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "xml1", "-o", "-", "-")
		cmd.Stdin = bytes.NewReader(data)
		converted, err := cmd.Output()
		if err != nil || len(converted) > catalogLimit {
			return nil, errors.New("Cannot inspect managed Claude Desktop preferences.")
		}
		data = converted
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth, sawDict, expectingKey := 0, false, true
	var keys []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if !sawDict || depth != 0 || !expectingKey {
				return nil, errors.New("Invalid managed Claude Desktop preferences.")
			}
			return keys, nil
		}
		if err != nil {
			return nil, errors.New("Invalid managed Claude Desktop preferences.")
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 && t.Name.Local != "plist" || depth == 2 && (t.Name.Local != "dict" || sawDict) {
				return nil, errors.New("Invalid managed Claude Desktop preferences.")
			}
			if depth == 2 {
				sawDict = true
			}
			if depth == 3 {
				if expectingKey {
					var key string
					if t.Name.Local != "key" || decoder.DecodeElement(&key, &t) != nil || key == "" {
						return nil, errors.New("Invalid managed Claude Desktop preferences.")
					}
					keys = append(keys, key)
					expectingKey = false
					depth--
				} else {
					if t.Name.Local == "key" || decoder.Skip() != nil {
						return nil, errors.New("Invalid managed Claude Desktop preferences.")
					}
					expectingKey = true
					depth--
				}
			}
		case xml.EndElement:
			depth--
		}
	}
}

func (a *app) claudeDesktopManagedConfig(paths claudeDesktopProfilePaths) error {
	rt := a.launchRuntime()
	home := rt.home
	if a.editorTestRoot != "" {
		home = a.editorTestRoot
	}
	realHome, _ := os.UserHomeDir()
	isolated := a.editorTestRoot != "" || filepath.Clean(home) != filepath.Clean(realHome)
	var files []string
	switch paths.Platform {
	case "macos", "darwin":
		base := "/Library/Managed Preferences"
		username := filepath.Base(home)
		if isolated {
			base = filepath.Join(home, "Library", "Managed Preferences")
		} else {
			account, err := user.Current()
			if err != nil || account.Username == "" || filepath.Base(account.Username) != account.Username {
				return errors.New("Cannot inspect user-managed Claude Desktop settings.")
			}
			username = account.Username
		}
		files = []string{filepath.Join(base, username, "com.anthropic.claudefordesktop.plist"), filepath.Join(base, "com.anthropic.claudefordesktop.plist")}
	case "linux":
		base := "/etc/claude-desktop"
		if isolated {
			base = filepath.Join(home, "etc", "claude-desktop")
		}
		files = []string{filepath.Join(base, "managed-settings.json")}
	case "windows":
		if isolated || runtime.GOOS != "windows" {
			return nil
		}
		for _, hive := range []string{"LocalMachine", "CurrentUser"} {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			// Query names only. .NET distinguishes an absent key from an access
			// failure without depending on the language of reg.exe error text.
			script := "$ErrorActionPreference='Stop'; try { $k=[Microsoft.Win32.Registry]::" + hive + ".OpenSubKey('SOFTWARE\\Policies\\Claude'); if ($null -eq $k) { exit 3 }; foreach ($n in $k.GetValueNames()) { [Console]::WriteLine($n) }; $k.Close() } catch { exit 1 }"
			data, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
			cancel()
			if err != nil {
				var exit *exec.ExitError
				if errors.As(err, &exit) && exit.ExitCode() == 3 {
					continue
				}
				return errors.New("Cannot inspect managed Claude Desktop settings.")
			}
			keys := strings.Split(strings.TrimSuffix(string(data), "\r\n"), "\n")
			if len(data) == 0 {
				keys = nil
			}
			for i := range keys {
				keys[i] = strings.TrimSuffix(keys[i], "\r")
			}
			if claudeDesktopManagedKeys(keys) {
				return errors.New("Claude Desktop inference is managed by your organization; local profiles would be ignored.")
			}
			return nil // An existing HKLM key takes precedence over HKCU.
		}
		return nil
	}
	for _, path := range files {
		data, err := readCatalogFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return errors.New("Cannot safely inspect managed Claude Desktop settings.")
		}
		var keys []string
		if strings.HasSuffix(path, ".plist") {
			keys, err = claudeDesktopPlistKeys(data)
		} else {
			var obj map[string]json.RawMessage
			obj, err = decodeClaudeDesktopObject(data)
			for key := range obj {
				keys = append(keys, key)
			}
		}
		if err != nil {
			return errors.New("Cannot inspect managed Claude Desktop settings; local profile not changed.")
		}
		if claudeDesktopManagedKeys(keys) {
			return errors.New("Claude Desktop inference is managed by your organization; local profiles would be ignored.")
		}
	}
	return nil
}

func (a *app) readClaudeDesktopSelection(paths claudeDesktopProfilePaths) (editorSelection, error) {
	return a.readClaudeDesktopSelectionMode(paths, a.config.ClaudeDesktopExperimentalModels)
}

// Reading with experimental=true during preparation validates the saved real
// IDs even after the option is disabled, so a native-only profile can replace it.
func (a *app) readClaudeDesktopSelectionMode(paths claudeDesktopProfilePaths, experimental bool) (editorSelection, error) {
	var s editorSelection
	if !safeLaunchDir(a.dir, a.dir) {
		return s, errors.New("Unsafe Kilo profile directory.")
	}
	data, err := readCatalogFile(paths.SelectionPath)
	if err != nil {
		return s, errors.New("No saved Claude Desktop model selection.")
	}
	if _, err = decodeClaudeDesktopObject(data); err != nil {
		return s, errors.New("Invalid saved Claude Desktop model selection.")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&s) != nil {
		return s, errors.New("Invalid saved Claude Desktop model selection.")
	}
	if err := validateClaudeDesktopSelectionMode(s, experimental); err != nil {
		return s, err
	}
	return s, nil
}

// Caller holds a.mu. This checks activation and current gateway credentials
// without writing settings or starting/stopping Claude Desktop.
func (a *app) verifyClaudeDesktopProfile() error {
	paths, err := a.claudeDesktopPaths()
	if err != nil {
		return err
	}
	if err = a.claudeDesktopManagedConfig(paths); err != nil {
		return err
	}
	s, err := a.readClaudeDesktopSelection(paths)
	if err != nil {
		return err
	}
	if !safeLaunchDir(filepath.Dir(paths.ConfigPath), paths.Root) {
		return errors.New("Prepare the Claude Desktop profile first; its settings directory is missing or unsafe.")
	}
	config, _, exists, err := readClaudeDesktopObject(paths.ConfigPath)
	if err != nil || !exists {
		return errors.New("Prepare the Claude Desktop profile first; its configuration is missing or invalid.")
	}
	for key, value := range claudeDesktopOwnedConfig(s, a.config.Port, a.config.LocalKey, a.config.ClaudeDesktopExperimentalModels) {
		want, _ := json.Marshal(value)
		if !launchEqualJSON(config[key], want) {
			return errors.New("Claude Desktop gateway settings changed; prepare the profile again.")
		}
	}
	if len(config["inferenceCredentialHelper"]) > 0 || len(config["inferenceCustomHeaders"]) > 0 || len(config["bootstrapUrl"]) > 0 {
		return errors.New("Claude Desktop gateway authentication changed; prepare the profile again.")
	}
	meta, _, exists, err := readClaudeDesktopObject(paths.MetaPath)
	if err != nil || !exists || claudeDesktopMetadata(meta, exists, false) != nil || !launchEqualJSON(meta["appliedId"], []byte(`"`+claudeDesktopProfileID+`"`)) || len(meta["hybridPointer"]) > 0 {
		return errors.New("The Kilo Claude Desktop configuration is not active; prepare the profile again.")
	}
	mode, _, exists, err := readClaudeDesktopObject(paths.ModePath)
	if err != nil || !exists || !launchEqualJSON(mode["deploymentMode"], []byte(`"3p"`)) {
		return errors.New("Claude Desktop third-party mode is not active; prepare the profile again.")
	}
	return nil
}

// Caller holds a.mu. All contents and backup destinations are validated before
// writing; saveEditorFiles backs up and rolls back this multi-file change.
func (a *app) saveClaudeDesktopProfile(s editorSelection, paths claudeDesktopProfilePaths) (bool, error) {
	if err := validateClaudeDesktopSelectionMode(s, a.config.ClaudeDesktopExperimentalModels); err != nil {
		return false, err
	}
	if err := a.claudeDesktopManagedConfig(paths); err != nil {
		return false, err
	}
	if err := safeEditorDir(paths.Root, filepath.Dir(paths.ConfigPath)); err != nil {
		return false, err
	}
	if err := safeEditorDir(a.dir, a.dir); err != nil {
		return false, err
	}
	config, oldConfig, _, err := readClaudeDesktopObject(paths.ConfigPath)
	if err != nil {
		return false, err
	}
	meta, oldMeta, metaExists, err := readClaudeDesktopObject(paths.MetaPath)
	if err != nil {
		return false, err
	}
	mode, oldMode, _, err := readClaudeDesktopObject(paths.ModePath)
	if err != nil {
		return false, err
	}
	if err = claudeDesktopMetadata(meta, metaExists, true); err != nil {
		return false, err
	}
	// These are connection settings owned by this profile. A credential helper
	// always wins over the static API key, so clear stale helper/header overrides.
	delete(config, "inferenceCredentialHelper")
	delete(config, "inferenceCustomHeaders")
	delete(config, "bootstrapUrl")
	delete(config, "bootstrapEnabled")
	for key, value := range claudeDesktopOwnedConfig(s, a.config.Port, a.config.LocalKey, a.config.ClaudeDesktopExperimentalModels) {
		claudeDesktopSet(config, key, value)
	}
	claudeDesktopSet(mode, "deploymentMode", "3p")
	var files []profileFile
	for _, item := range []struct {
		path string
		obj  map[string]json.RawMessage
		old  []byte
	}{{paths.ConfigPath, config, oldConfig}, {paths.MetaPath, meta, oldMeta}, {paths.ModePath, mode, oldMode}} {
		data, err := claudeDesktopEncoded(item.obj, item.old)
		if err != nil {
			return false, err
		}
		file, err := prepareClaudeDesktopFile(item.path, data)
		if err != nil {
			return false, err
		}
		files = append(files, file)
	}
	selection, _ := json.MarshalIndent(s, "", "  ")
	selection = append(selection, '\n')
	if old, err := readCatalogFile(paths.SelectionPath); err == nil {
		if _, err = a.readClaudeDesktopSelectionMode(paths, true); err != nil {
			return false, errors.New("Invalid saved Claude Desktop selection; nothing saved.")
		}
		if launchEqualJSON(old, selection) {
			selection = old
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, errors.New("Cannot safely read saved Claude Desktop selection; nothing saved.")
	}
	file, err := prepareClaudeDesktopFile(paths.SelectionPath, selection)
	if err != nil {
		return false, err
	}
	files = append(files, file)
	return saveEditorFiles(files)
}

func (a *app) claudeDesktopProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	paths, err := a.claudeDesktopPaths()
	if err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var s editorSelection
	changed := false
	if r.Method == http.MethodGet {
		s, err = a.readClaudeDesktopSelection(paths)
		if err != nil {
			jsonError(w, 404, err.Error())
			return
		}
		if err = a.verifyClaudeDesktopProfile(); err != nil {
			jsonError(w, 409, err.Error())
			return
		}
	} else {
		data, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, catalogLimit))
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		_, objectErr := decodeClaudeDesktopObject(data)
		if readErr != nil || objectErr != nil || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || decoder.Decode(&s) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			jsonError(w, 400, "Invalid Claude Desktop setup JSON.")
			return
		}
		if err = validateClaudeDesktopSelectionMode(s, a.config.ClaudeDesktopExperimentalModels); err != nil {
			jsonError(w, 400, err.Error())
			return
		}
		changed, err = a.saveClaudeDesktopProfile(s, paths)
		if err != nil {
			jsonError(w, 409, err.Error())
			return
		}
	}
	jsonResponse(w, 200, map[string]any{"ok": true, "changed": changed, "selection": s, "configPath": paths.ConfigPath, "profileDir": paths.ProfileDir, "experimentalModels": a.config.ClaudeDesktopExperimentalModels})
}

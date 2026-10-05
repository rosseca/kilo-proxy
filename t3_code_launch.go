package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const t3CodeSupportedVersion = "0.0.45"
const t3CodeNightlyVersion = "0.0.46-nightly.20261003.2610"
const t3CodeMarker = "--kilo-proxy-t3-root="

func t3CodeVersionSupported(version string) bool {
	return version == t3CodeSupportedVersion || version == t3CodeNightlyVersion
}

func t3CodeInstallationCandidates(platform, home, localAppData string) []string {
	candidates := []string{}
	switch platform {
	case "darwin", "macos":
		for _, name := range []string{"T3 Code (Nightly).app", "T3 Code.app", "T3 Code (Alpha).app", "T3 Code (Beta).app"} {
			for _, root := range []string{"/Applications", filepath.Join(home, "Applications")} {
				candidates = append(candidates, filepath.Join(root, name))
			}
		}
	case "windows":
		for _, name := range []string{"T3 Code (Nightly).exe", "T3 Code.exe", "T3 Code (Alpha).exe", "t3code.exe"} {
			for _, root := range []string{localAppData, os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
				if !filepath.IsAbs(root) {
					continue
				}
				for _, dir := range []string{"Programs/T3 Code (Nightly)", "T3 Code (Nightly)", "Programs/t3code", "Programs/T3 Code", "Programs/T3 Code (Alpha)", "t3code", "T3 Code", "T3 Code (Alpha)"} {
					candidates = append(candidates, filepath.Join(root, filepath.FromSlash(dir), name))
				}
			}
		}
	default:
		for _, name := range []string{"t3code", "t3-code", "T3 Code (Nightly)"} {
			candidates = append(candidates, filepath.Join("/opt/T3 Code (Nightly)", name))
		}
		for _, name := range []string{"t3code", "t3-code"} {
			if path := launchLookPath(name); path != "" {
				candidates = append(candidates, path)
			}
		}
		for _, root := range []string{"/opt/t3code", "/opt/T3 Code", "/opt/T3 Code (Alpha)", filepath.Join(home, ".local", "share", "t3code")} {
			for _, name := range []string{"t3code", "t3-code", "T3 Code (Nightly)", "T3 Code", "T3 Code (Alpha)"} {
				candidates = append(candidates, filepath.Join(root, name))
			}
		}
	}
	return candidates
}

func resolveT3Code(platform, home, localAppData string) (string, error) {
	return resolveT3CodeCandidates(t3CodeInstallationCandidates(platform, home, localAppData))
}

func resolveT3CodeCandidates(candidates []string) (string, error) {
	for _, path := range candidates {
		if strings.HasSuffix(path, ".app") {
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				return path, nil
			}
		} else if launchExecutable(path) {
			return path, nil
		}
	}
	return "", errors.New("Install T3 Code desktop 0.0.45 or nightly 0.0.46-nightly.20261003.2610, Codex CLI and Claude Code, then refresh installed apps. Extract an AppImage before using it on Linux.")
}

type t3CodeASAREntry struct {
	Files    map[string]t3CodeASAREntry `json:"files"`
	Size     int64                      `json:"size"`
	Offset   string                     `json:"offset"`
	Unpacked bool                       `json:"unpacked"`
	Link     string                     `json:"link"`
}

// Inspect metadata without executing Electron or a CLI. Only the root package
// entry is read, never package dependencies or user configuration.
func t3CodeVersion(executable, platform string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	root := filepath.Dir(executable)
	if (platform == "darwin" || platform == "macos") && strings.HasSuffix(executable, ".app") {
		root = filepath.Join(executable, "Contents")
	}
	var packageData []byte
	var err error
	for _, resources := range []string{"Resources", "resources"} {
		packageData, err = t3CodeASARPackage(filepath.Join(root, resources, "app.asar"))
		if err == nil {
			break
		}
	}
	if err != nil {
		return "", errors.New("Cannot verify this T3 Code desktop installation. Install supported desktop 0.0.45 or nightly 0.0.46-nightly.20261003.2610 and refresh detection.")
	}
	var metadata struct{ Name, Version string }
	if json.Unmarshal(packageData, &metadata) != nil || metadata.Name != "t3code" || len(metadata.Version) > 80 {
		return "", errors.New("Cannot verify the T3 Code desktop package metadata.")
	}
	return metadata.Version, nil
}

func t3CodeASARPackage(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("Invalid T3 Code package.")
	}
	var prefix [16]byte
	if _, err = io.ReadFull(file, prefix[:]); err != nil {
		return nil, err
	}
	headerSize, jsonSize := int64(binary.LittleEndian.Uint32(prefix[4:8])), int64(binary.LittleEndian.Uint32(prefix[12:16]))
	if binary.LittleEndian.Uint32(prefix[:4]) != 4 || headerSize < 8 || headerSize > 8<<20 || jsonSize < 2 || jsonSize > headerSize-8 || headerSize+8 > info.Size() {
		return nil, errors.New("Invalid T3 Code package header.")
	}
	header := make([]byte, jsonSize)
	if _, err = io.ReadFull(file, header); err != nil {
		return nil, err
	}
	var tree t3CodeASAREntry
	if json.Unmarshal(header, &tree) != nil {
		return nil, errors.New("Invalid T3 Code package index.")
	}
	entry, ok := tree.Files["package.json"]
	offset, numberErr := strconv.ParseInt(entry.Offset, 10, 64)
	base := headerSize + 8
	if !ok || numberErr != nil || offset < 0 || entry.Size < 1 || entry.Size > 64<<10 || entry.Unpacked || entry.Link != "" || offset > info.Size()-base || entry.Size > info.Size()-base-offset {
		return nil, errors.New("Invalid T3 Code package metadata entry.")
	}
	data := make([]byte, entry.Size)
	_, err = file.ReadAt(data, base+offset)
	return data, err
}

func t3CodeCompatibility(executable, platform string) error {
	version, err := t3CodeVersion(executable, platform)
	if err != nil {
		return err
	}
	if !t3CodeVersionSupported(version) {
		return errors.New("This integration supports T3 Code desktop 0.0.45 and nightly 0.0.46-nightly.20261003.2610. Other versions must be validated before preparing their private configuration.")
	}
	return nil
}

func t3CodeBundleExecutable(bundle string) string {
	data, err := readOpenDesignShimFile(filepath.Join(bundle, "Contents", "Info.plist"), 1<<20)
	if err != nil {
		return ""
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	key := ""
	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "key" {
			if decoder.DecodeElement(&key, &start) != nil {
				return ""
			}
		} else if start.Name.Local == "string" {
			var value string
			if decoder.DecodeElement(&value, &start) != nil {
				return ""
			}
			if key == "CFBundleExecutable" {
				if value == "" || filepath.Base(value) != value || strings.ContainsAny(value, "\x00\r\n") {
					return ""
				}
				path := filepath.Join(bundle, "Contents", "MacOS", value)
				if launchExecutable(path) {
					return path
				}
				return ""
			}
			key = ""
		}
	}
}

func (a *app) t3CodeAvailability(executable string, rt clientLaunchRuntime) error {
	if err := t3CodeCompatibility(executable, rt.platform); err != nil {
		return err
	}
	if _, err := resolveOpenDesignCLI("codex-cli", rt); err != nil {
		return errors.New("Install a native Codex CLI executable before preparing T3 Code.")
	}
	if _, err := rt.resolve("claude", ""); err != nil {
		return errors.New("Install Claude Code CLI before preparing T3 Code. Claude Desktop is a separate application.")
	}
	return nil
}

func t3CodeNormalEnvironment(home string, parent []string, platform string) map[string]string {
	result := map[string]string{"HOME": home}
	for _, entry := range parent {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(key) {
		case "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "CODEX_HOME", "CLAUDE_CONFIG_DIR":
			if value != "" {
				result[strings.ToUpper(key)] = value
			}
		}
	}
	if platform == "windows" {
		result["USERPROFILE"] = home
		if result["APPDATA"] == "" {
			result["APPDATA"] = filepath.Join(home, "AppData", "Roaming")
		}
		if result["LOCALAPPDATA"] == "" {
			result["LOCALAPPDATA"] = filepath.Join(home, "AppData", "Local")
		}
	} else {
		for name, suffix := range map[string]string{"XDG_CONFIG_HOME": ".config", "XDG_DATA_HOME": ".local/share", "XDG_CACHE_HOME": ".cache", "XDG_STATE_HOME": ".local/state"} {
			if result[name] == "" {
				result[name] = filepath.Join(home, filepath.FromSlash(suffix))
			}
		}
	}
	return result
}

// T3's process-wide overrides can select a different server, launch extra
// Codex arguments, or expose its private server over the network. Only the
// explicit private workspace values below belong to this launcher.
func t3CodeUnsetEnvironment(parent []string) []string {
	names := append([]string{}, nativeClaudeResetEnv...)
	names = append(names, "CODEX_HOME", "CODEX_API_KEY", "OPENAI_API_KEY", "OPENAI_BASE_URL", "KILO_LOCAL_API_KEY", "CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDECODE", "NODE_OPTIONS", "ELECTRON_RUN_AS_NODE", "VITE_DEV_SERVER_URL", "T3CODE_CODEX_LAUNCH_ARGS", "T3CODE_PORT", "T3CODE_DEV_REMOTE_T3_SERVER_ENTRY_PATH", "T3CODE_DEV_AUTH_TOKEN", "T3CODE_DEV_ALLOWED_ORIGINS", "T3CODE_DESKTOP_WS_URL", "T3CODE_DESKTOP_LAN_ACCESS", "T3CODE_DESKTOP_LAN_HOST", "T3CODE_DESKTOP_HTTPS_ENDPOINTS", "T3CODE_BOOTSTRAP_FD", "T3CODE_HOST", "T3CODE_MODE", "T3CODE_TAILSCALE_SERVE", "T3CODE_TAILSCALE_SERVE_PORT", "T3CODE_DESKTOP_MOCK_UPDATES", "T3CODE_DESKTOP_MOCK_UPDATE_SERVER_PORT")
	for _, entry := range parent {
		name, _, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(strings.ToUpper(name), "T3CODE_") {
			names = append(names, name)
		}
	}
	return names
}

func (a *app) t3CodeClaudeCapabilities(executable string, rt clientLaunchRuntime) claudeCapabilities {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "--version")
	hideOpenDesignProbeWindow(command)
	paths := t3CodePaths(a.dir)
	command.Env = clientChildEnvironment(os.Environ(), map[string]string{"HOME": paths.UIHome, "USERPROFILE": paths.UIHome, "CLAUDE_CONFIG_DIR": filepath.Join(paths.Root, "profiles", "claude-kilo")}, t3CodeUnsetEnvironment(os.Environ()), rt.platform)
	command.Dir = paths.UIHome
	output := &openDesignLimitedOutput{limit: 256}
	command.Stdout = output
	if err := command.Run(); err != nil || output.exceeded {
		return claudeCapabilities{}
	}
	return claudeCaps(strings.TrimSpace(output.String()))
}

func (a *app) applyT3CodeLaunch(plan *clientLaunchPlan, rt clientLaunchRuntime) error {
	saved, err := a.readT3CodePrepared()
	if err != nil || !a.t3CodeReady(saved, plan.Executable, rt) {
		return errors.New("Prepare T3 Code again: its private profiles or proxy connection changed.")
	}
	if saved.Version == t3CodeNightlyVersion {
		var caps *claudeCapabilities
		for _, model := range saved.Library.Models {
			if model.ReasoningEffort == "" || !validClaudeEffort(model.ID, model.ReasoningEffort) {
				continue
			}
			if caps == nil {
				claude, err := rt.resolve("claude", "")
				current := claudeCapabilities{}
				if err == nil {
					current = a.t3CodeClaudeCapabilities(claude, rt)
				}
				caps = &current
			}
			if !claudeEffortCompatible(model.ID, *caps) {
				return managedClaudeEffortVersionError(model.ID, "T3 Code nightly")
			}
		}
	}
	paths := t3CodePaths(a.dir)
	check := a.t3CodeCheckRunning
	if check == nil {
		check = t3CodeRunning
	}
	running, err := check(paths.Root)
	if err != nil {
		return errors.New("Cannot check whether the T3 Code Kilo window is running. Close that window and try again.")
	}
	if running || time.Now().Before(a.t3CodeLaunchUntil) {
		return errors.New("Quit the T3 Code Kilo window before opening it again. Your regular T3 Code window can stay open.")
	}
	for _, dir := range []string{paths.UIHome, paths.Data} {
		if err := safeEditorDir(a.dir, dir); err != nil {
			return err
		}
	}
	plan.Env["T3CODE_HOME"] = paths.Data
	plan.Env["HOME"] = paths.UIHome
	plan.Env["T3CODE_DISABLE_AUTO_UPDATE"] = "true"
	plan.Unset = append(plan.Unset, t3CodeUnsetEnvironment(os.Environ())...)
	plan.Args = []string{t3CodeMarker + paths.Root}
	if rt.platform == "windows" {
		plan.Env["USERPROFILE"] = paths.UIHome
		plan.Env["APPDATA"] = filepath.Join(paths.UIHome, "AppData", "Roaming")
		plan.Env["LOCALAPPDATA"] = filepath.Join(paths.UIHome, "AppData", "Local")
	} else {
		plan.Env["XDG_CONFIG_HOME"] = filepath.Join(paths.UIHome, ".config")
		plan.Env["XDG_DATA_HOME"] = filepath.Join(paths.UIHome, ".local", "share")
		plan.Env["XDG_CACHE_HOME"] = filepath.Join(paths.UIHome, ".cache")
		plan.Env["XDG_STATE_HOME"] = filepath.Join(paths.UIHome, ".local", "state")
		if rt.platform == "macos" || rt.platform == "darwin" {
			binary := t3CodeBundleExecutable(plan.Executable)
			if binary == "" {
				return errors.New("The T3 Code desktop application bundle is invalid.")
			}
			plan.Executable = binary
		}
	}
	plan.Directory = rt.home
	return nil
}

func t3CodeRunning(root string) (bool, error) {
	if !filepath.IsAbs(root) || strings.ContainsAny(root, "\x00\r\n") {
		return false, errors.New("Invalid private T3 Code workspace.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	running, err := t3CodeProcessRunning(ctx, root)
	if err != nil || running {
		return running, err
	}
	// Electron passes the primary server's base directory over fd 3, not in
	// argv. Its private runtime record also protects against a server left
	// alive after a desktop crash, without inspecting process environments.
	return t3CodeRuntimeRunning(root)
}

func t3CodeRuntimeRunning(root string) (bool, error) {
	parent := filepath.Join(root, "data", "userdata")
	if _, err := os.Lstat(parent); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if !safeLaunchDir(parent, root) {
		return false, errors.New("Unsafe private T3 Code runtime directory.")
	}
	data, err := readOpenDesignShimFile(filepath.Join(parent, "server-runtime.json"), 16<<10)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	var state struct {
		Version   int    `json:"version"`
		PID       int    `json:"pid"`
		StartedAt string `json:"startedAt"`
	}
	if err != nil || json.Unmarshal(data, &state) != nil || state.Version != 1 || state.PID < 1 || state.PID > 1<<31-1 {
		return false, errors.New("Cannot verify the private T3 Code server runtime.")
	}
	if _, err := time.Parse(time.RFC3339Nano, state.StartedAt); err != nil {
		return false, errors.New("Cannot verify the private T3 Code server runtime.")
	}
	return openMausBotProcessAlive(state.PID)
}

func t3CodeCommandMatches(command, root string) bool {
	// Reuse the argument boundary parser, replacing only the fixed option name.
	return claudeDesktopProfileArgument(strings.ReplaceAll(command, t3CodeMarker, "--user-data-dir="), root, "unix")
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type synaraPackageInfo struct {
	Name, Version, Main, SynaraDesktopFlavor, SynaraCommitHash string
}

// Read bounded package metadata; never start the application during discovery.
// Package identity selects the Beta channel. Version and commit are descriptive
// metadata, not a compatibility allowlist.
func synaraPackageMetadata(executable, platform string) (synaraPackageInfo, error) {
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	root := filepath.Dir(executable)
	if (platform == "macos" || platform == "darwin") && strings.HasSuffix(executable, ".app") {
		root = filepath.Join(executable, "Contents")
	}
	for _, resources := range []string{"Resources", "resources"} {
		data, err := t3CodeASARPackage(filepath.Join(root, resources, "app.asar"))
		if err != nil {
			continue
		}
		var meta synaraPackageInfo
		if json.Unmarshal(data, &meta) != nil || meta.Name != "synara-desktop-beta" || meta.SynaraDesktopFlavor != "beta" {
			return synaraPackageInfo{}, errors.New("Cannot verify this Synara Beta package. Install an official Synara Beta release.")
		}
		return meta, nil
	}
	return synaraPackageInfo{}, errors.New("Cannot verify this Synara Beta installation. Install an official Synara Beta release and refresh detection. Extract an AppImage before using it on Linux.")
}

func synaraVersion(executable, platform string) (string, error) {
	meta, err := synaraPackageMetadata(executable, platform)
	if err != nil {
		return "", err
	}
	// An absent or unsuitable display label does not make a Beta incompatible.
	version := strings.TrimSpace(meta.Version)
	if len(version) > 80 || strings.ContainsAny(version, "\x00\r\n") {
		version = ""
	}
	return version, nil
}

func (a *app) synaraAvailability(binary string, rt clientLaunchRuntime) error {
	_, err := synaraPackageMetadata(binary, rt.platform)
	if err != nil {
		return err
	}
	if _, err := resolveOpenDesignCLI("codex-cli", rt); err != nil {
		return errors.New("Install a native Codex CLI executable before preparing Synara.")
	}
	if _, err := rt.resolve("claude", ""); err != nil {
		return errors.New("Install Claude Code CLI before preparing Synara. Claude Desktop is a separate application.")
	}
	return nil
}

func synaraUnsetEnvironment(parent []string) []string {
	names := t3CodeUnsetEnvironment(parent)
	for _, entry := range parent {
		name, _, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(strings.ToUpper(name), "SYNARA_") {
			names = append(names, name)
		}
	}
	// A normal shell startup file must not inject provider overrides into the
	// private process. Explicit HOME and XDG directories survive vendor hydration.
	return append(names, "ZDOTDIR", "BASH_ENV", "ENV")
}

func synaraNormalEnvironment(home string, parent []string, platform string) map[string]string {
	result := t3CodeNormalEnvironment(home, parent, platform)
	for _, entry := range parent {
		name, value, ok := strings.Cut(entry, "=")
		if ok && (strings.EqualFold(name, "CLAUDE_SECURESTORAGE_CONFIG_DIR") || strings.EqualFold(name, "CLAUDE_CONFIG_DIR")) {
			result[strings.ToUpper(name)] = value
		}
	}
	return result
}

func (a *app) synaraProfileOptions(rt clientLaunchRuntime, library modelLibrary) synaraProfileOptions {
	paths := synaraPaths(a.dir)
	codex, _ := resolveOpenDesignCLI("codex-cli", rt)
	claude, _ := rt.resolve("claude", "")
	return synaraProfileOptions{RootDir: paths.Root, DataDir: paths.Data, UserDataDir: paths.Electron, NormalHome: rt.home, CodexBinary: codex, NormalCodexBinary: synaraCodexNormalShimPath(paths.Root, rt.platform), ClaudeBinary: claude, NormalEnvironment: synaraNormalEnvironment(rt.home, os.Environ(), rt.platform), Library: library, Catalog: readNativeCatalogCache(a.dir, a.catalogScopeLocked()), Port: a.config.Port, LocalKey: a.config.LocalKey, Images: a.clientImageSettingsLocked()}
}

func (a *app) synaraClaudeCapabilities(binary string, rt clientLaunchRuntime) claudeCapabilities {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	paths := synaraPaths(a.dir)
	command := exec.CommandContext(ctx, binary, "--version")
	hideOpenDesignProbeWindow(command)
	command.Env = clientChildEnvironment(os.Environ(), map[string]string{"HOME": paths.UIHome, "USERPROFILE": paths.UIHome, "CLAUDE_CONFIG_DIR": filepath.Join(paths.Root, "profiles", "claude-kilo")}, synaraUnsetEnvironment(os.Environ()), rt.platform)
	command.Dir = paths.UIHome
	output := &openDesignLimitedOutput{limit: 256}
	command.Stdout = output
	if command.Run() != nil || output.exceeded {
		return claudeCapabilities{}
	}
	return claudeCaps(strings.TrimSpace(output.String()))
}

func synaraServerRuntimeRunning(root string) (bool, error) {
	parent := filepath.Join(root, "data", "userdata")
	if _, err := os.Lstat(parent); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if !safeLaunchDir(parent, root) {
		return false, errors.New("Unsafe private Synara runtime directory.")
	}
	data, err := readOpenDesignShimFile(filepath.Join(parent, "server-runtime.json"), 16<<10)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	var state struct {
		Version, PID int
		StartedAt    string
	}
	if err != nil || json.Unmarshal(data, &state) != nil || state.Version != 1 || state.PID < 1 || state.PID > 1<<31-1 {
		return false, errors.New("Cannot verify the private Synara server runtime.")
	}
	if _, err = time.Parse(time.RFC3339Nano, state.StartedAt); err != nil {
		return false, errors.New("Cannot verify the private Synara server runtime.")
	}
	return openMausBotProcessAlive(state.PID)
}

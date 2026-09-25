package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func launchExecutable(path string) bool {
	st, e := os.Stat(path)
	return e == nil && st.Mode().IsRegular() && (runtime.GOOS == "windows" || st.Mode()&0111 != 0)
}
func launchLookPath(name string) string {
	if path, e := exec.LookPath(name); e == nil && filepath.IsAbs(path) {
		return path
	}
	home, _ := os.UserHomeDir()
	dirs := []string{filepath.Join(home, ".local", "bin"), filepath.Join(home, ".opencode", "bin"), filepath.Join(home, ".bun", "bin"), filepath.Join(home, ".npm-global", "bin"), filepath.Join(home, ".volta", "bin"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"}
	names := []string{name}
	if runtime.GOOS == "windows" {
		dirs = []string{filepath.Join(home, ".local", "bin"), filepath.Join(home, "scoop", "shims"), filepath.Join(os.Getenv("APPDATA"), "npm")}
		names = []string{name + ".exe", name + ".cmd", name + ".bat"}
	}
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) {
			continue
		}
		for _, file := range names {
			path := filepath.Join(dir, file)
			if launchExecutable(path) {
				return path
			}
		}
	}
	return ""
}
func resolveLaunchClient(id, custom string) (string, error) {
	name, kind := launchClientIdentity(id)
	if name == "" {
		return "", errors.New("Unknown launch client.")
	}
	missing := errors.New("Install " + name + " on this computer, then refresh installed apps.")
	if custom != "" {
		if id != "codex" || len(custom) > 8192 || !filepath.IsAbs(custom) || strings.ContainsAny(custom, "\x00\r\n") {
			return "", errors.New("Choose an absolute path to the Codex application.")
		}
		if runtime.GOOS == "darwin" && strings.HasSuffix(custom, ".app") {
			if st, e := os.Stat(custom); e == nil && st.IsDir() {
				return filepath.Clean(custom), nil
			}
		}
		if launchExecutable(custom) {
			return filepath.Clean(custom), nil
		}
		return "", errors.New("The selected Codex application does not exist or cannot be executed.")
	}
	if reason := launchClientPlatformReason(id, runtime.GOOS); reason != "" {
		return "", errors.New(reason)
	}
	if id == "open-design" {
		home, _ := os.UserHomeDir()
		return resolveOpenDesignLaunchClient(runtime.GOOS, home, os.Getenv("LOCALAPPDATA"))
	}
	if id == "claude-desktop" {
		home, _ := os.UserHomeDir()
		return resolveClaudeDesktop(runtime.GOOS, home, os.Getenv("LOCALAPPDATA"))
	}
	if kind == "terminal" {
		if id == "omp" {
			if path, err := exec.LookPath("omp"); err == nil && filepath.IsAbs(path) {
				return path, nil
			}
			home, _ := os.UserHomeDir()
			for _, path := range ompInstallationPaths(home, runtime.GOOS, os.Getenv("LOCALAPPDATA"), os.Getenv("PI_INSTALL_DIR")) {
				if launchExecutable(path) {
					return path, nil
				}
			}
		}
		command := id
		if id == "codex-cli" {
			command = "codex"
		}
		if path := launchLookPath(command); path != "" {
			return path, nil
		}
		// The desktop bundle includes its matching CLI on macOS.
		if id == "codex-cli" && runtime.GOOS == "darwin" {
			for _, bundle := range []string{"Codex.app", "ChatGPT.app"} {
				path := filepath.Join("/Applications", bundle, "Contents", "Resources", "codex")
				if launchExecutable(path) {
					return path, nil
				}
			}
		}
		return "", missing
	}
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(id, "xcode-") {
		if x := detectXcode(); x.Available {
			return x.Path, nil
		}
		return "", missing
	}
	if runtime.GOOS == "darwin" {
		apps := map[string][]string{"codex": {"Codex.app", "ChatGPT.app"}, "zed": {"Zed.app", "Zed Preview.app"}}[id]
		for _, base := range []string{"/Applications", filepath.Join(home, "Applications")} {
			for _, app := range apps {
				path := filepath.Join(base, app)
				if st, e := os.Stat(path); e == nil && st.IsDir() {
					return path, nil
				}
			}
		}
	} else {
		command := id
		if id == "codex" {
			command = "codex-desktop"
		}
		if path := launchLookPath(command); path != "" {
			return path, nil
		}
		if runtime.GOOS == "windows" {
			for _, relative := range map[string][]string{"zed": {"Programs/Zed/zed.exe"}, "codex": {"Programs/Codex/Codex.exe", "Programs/ChatGPT/ChatGPT.exe"}}[id] {
				path := filepath.Join(os.Getenv("LOCALAPPDATA"), filepath.FromSlash(relative))
				if filepath.IsAbs(path) && launchExecutable(path) {
					return path, nil
				}
			}
		}
	}
	return "", missing
}

func ompInstallationPaths(home, platform, localAppData, installDir string) []string {
	name := "omp"
	if platform == "windows" {
		name += ".exe"
	}
	var paths []string
	if filepath.IsAbs(installDir) {
		paths = append(paths, filepath.Join(installDir, name))
	}
	if platform == "windows" && filepath.IsAbs(localAppData) {
		paths = append(paths, filepath.Join(localAppData, "omp", name))
	}
	return append(paths, filepath.Join(home, ".local", "bin", name), filepath.Join(home, ".bun", "bin", name))
}

func resolveOpenDesignLaunchClient(platform, home, localAppData string) (string, error) {
	if reason := launchClientPlatformReason("open-design", platform); reason != "" {
		return "", errors.New(reason)
	}
	if platform == "darwin" || platform == "macos" {
		for _, base := range []string{"/Applications", filepath.Join(home, "Applications")} {
			bundle := filepath.Join(base, "Open Design.app")
			if launchExecutable(filepath.Join(bundle, "Contents", "MacOS", "Open Design")) {
				return bundle, nil
			}
		}
	} else if platform == "windows" {
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		path := filepath.Join(localAppData, "Programs", "Open Design", "Open Design.exe")
		if filepath.IsAbs(path) && launchExecutable(path) {
			return path, nil
		}
	}
	// Never fall back to `od`: that name also belongs to the system octal-dump utility.
	return "", errors.New("Install Open Design on this computer, then refresh installed apps.")
}

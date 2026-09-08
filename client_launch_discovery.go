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
	if kind == "terminal" {
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
		apps := map[string][]string{"codex": {"Codex.app", "ChatGPT.app"}, "zed": {"Zed.app", "Zed Preview.app"}, "cursor": {"Cursor.app"}}[id]
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
			for _, relative := range map[string][]string{"cursor": {"Programs/cursor/Cursor.exe"}, "zed": {"Programs/Zed/zed.exe"}, "codex": {"Programs/Codex/Codex.exe", "Programs/ChatGPT/ChatGPT.exe"}}[id] {
				path := filepath.Join(os.Getenv("LOCALAPPDATA"), filepath.FromSlash(relative))
				if filepath.IsAbs(path) && launchExecutable(path) {
					return path, nil
				}
			}
		}
	}
	return "", missing
}

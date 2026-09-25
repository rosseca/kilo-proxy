package main

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func resolveClaudeDesktop(platform, home, localAppData string) (string, error) {
	var candidates []string
	switch platform {
	case "darwin", "macos":
		for _, base := range []string{"/Applications", filepath.Join(home, "Applications")} {
			bundle := filepath.Join(base, "Claude.app")
			if launchExecutable(filepath.Join(bundle, "Contents", "MacOS", "Claude")) {
				return bundle, nil
			}
		}
	case "windows":
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		for _, relative := range []string{"AnthropicClaude/claude.exe", "Programs/Claude/Claude.exe", "Claude/Claude.exe"} {
			candidates = append(candidates, filepath.Join(localAppData, filepath.FromSlash(relative)))
		}
		// The supported Cowork installer is MSIX. Query its installed package
		// rather than confusing the unrelated `claude` terminal executable.
		if platform == runtime.GOOS {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; Get-AppxPackage -Name '*Claude*' | ForEach-Object { Join-Path $_.InstallLocation 'app\\Claude.exe'; Join-Path $_.InstallLocation 'Claude.exe' }").Output()
			if err == nil && len(out) < 64<<10 {
				for _, path := range strings.Split(string(out), "\n") {
					candidates = append(candidates, strings.TrimSpace(path))
				}
			}
		}
	default:
		if path := launchLookPath("claude-desktop"); path != "" {
			candidates = append(candidates, path)
		}
		candidates = append(candidates, "/opt/Claude/claude-desktop", "/opt/claude-desktop/claude-desktop", filepath.Join(home, ".local", "bin", "claude-desktop"))
	}
	for _, path := range candidates {
		if filepath.IsAbs(path) && launchExecutable(path) {
			return path, nil
		}
	}
	return "", errors.New("Install Claude Desktop, then refresh installed apps. Claude Code CLI is a separate application.")
}

// Do not kill or relaunch a user's sessions in order to apply a different
// provider. Desktop reads 3P configuration at startup and has no supported
// separate-instance contract. Process inventory contains executable names only.
func claudeDesktopRunning(application string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out []byte
	var err error
	if runtime.GOOS == "windows" {
		out, err = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; Get-CimInstance Win32_Process -Filter \"Name='Claude.exe'\" | Select-Object -ExpandProperty ExecutablePath").Output()
	} else {
		out, err = exec.CommandContext(ctx, "ps", "-A", "-o", "comm=").Output()
	}
	if err != nil || len(out) > 4<<20 {
		return false, errors.New("Cannot inspect running desktop applications.")
	}
	for _, line := range strings.Split(string(out), "\n") {
		if claudeDesktopProcessMatches(strings.TrimSpace(line), application, runtime.GOOS) {
			return true, nil
		}
	}
	return false, nil
}

func claudeDesktopProcessMatches(path, application, platform string) bool {
	want := filepath.Clean(application)
	if strings.HasSuffix(want, ".app") {
		want = filepath.Join(want, "Contents", "MacOS", "Claude")
	}
	if path == want || platform == "windows" && strings.EqualFold(path, want) {
		return true
	}
	// Multiple installed copies still share Claude's third-party profile.
	// Include a user-installed or mounted copy without matching the Claude CLI
	// or Electron helper processes.
	return (platform == "darwin" || platform == "macos") && filepath.IsAbs(path) && strings.HasSuffix(path, "/Claude.app/Contents/MacOS/Claude")
}

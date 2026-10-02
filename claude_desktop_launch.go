package main

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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

func (a *app) claudeDesktopProfileRunning(paths claudeDesktopProfilePaths) (bool, error) {
	check := a.claudeDesktopCheckRunning
	if check == nil {
		check = claudeDesktopRunning
	}
	return check(paths.UserDataDir)
}

func (a *app) claudeDesktopCanPrepare(paths claudeDesktopProfilePaths) error {
	running, err := a.claudeDesktopProfileRunning(paths)
	if err != nil {
		return errors.New("Cannot check whether the Kilo Claude Desktop window is running. Close that window and try again.")
	}
	if running {
		return errors.New("Close the Kilo Claude Desktop window before changing its configuration. Your regular Claude session can stay open.")
	}
	return nil
}

// Inspect only Claude processes and discard command lines after matching the
// exact private profile argument. Never persist, log, or return process details.
// Normal Claude processes do not prevent launching or preparing the Kilo one.
func claudeDesktopRunning(userDataDir string) (bool, error) {
	if !filepath.IsAbs(userDataDir) || strings.ContainsAny(userDataDir, "\x00\r\n") {
		return false, errors.New("Cannot inspect the Claude Desktop profile.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out []byte
	var err error
	if runtime.GOOS == "windows" {
		out, err = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); $p=@(Get-CimInstance Win32_Process -Filter \"Name='Claude.exe'\" | Select-Object ExecutablePath,CommandLine); ConvertTo-Json -InputObject $p -Compress").Output()
	} else {
		out, err = exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,comm=").Output()
	}
	if err != nil || len(out) > 4<<20 {
		return false, errors.New("Cannot inspect running desktop applications.")
	}
	if runtime.GOOS == "windows" {
		var processes []struct{ ExecutablePath, CommandLine string }
		if json.Unmarshal(out, &processes) != nil {
			return false, errors.New("Cannot inspect running desktop applications.")
		}
		for _, process := range processes {
			if process.ExecutablePath == "" || process.CommandLine == "" {
				return false, errors.New("Cannot inspect running desktop applications.")
			}
			if strings.EqualFold(filepath.Base(process.ExecutablePath), "Claude.exe") && claudeDesktopProfileArgument(process.CommandLine, userDataDir, "windows") {
				return true, nil
			}
		}
		return false, nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		pidText, path, ok := strings.Cut(strings.TrimSpace(line), " ")
		pid, parseErr := strconv.Atoi(pidText)
		path = strings.TrimSpace(path)
		if !ok || parseErr != nil || pid <= 0 || !claudeDesktopMainExecutable(path, runtime.GOOS) {
			continue
		}
		args, argsErr := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
		if argsErr != nil {
			// A process may exit between the two reads. Any other inspection
			// failure must fail closed instead of allowing a live profile edit.
			var exit *exec.ExitError
			if errors.As(argsErr, &exit) && exit.ExitCode() == 1 && len(args) == 0 && ctx.Err() == nil {
				continue
			}
			return false, errors.New("Cannot inspect running desktop applications.")
		}
		if len(args) > 64<<10 {
			return false, errors.New("Cannot inspect running desktop applications.")
		}
		if claudeDesktopProfileArgument(strings.TrimSpace(string(args)), userDataDir, runtime.GOOS) {
			return true, nil
		}
	}
	return false, nil
}

func claudeDesktopMainExecutable(path, platform string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	if platform == "darwin" || platform == "macos" {
		return strings.HasSuffix(path, "/Claude.app/Contents/MacOS/Claude")
	}
	return filepath.Base(path) == "claude-desktop"
}

func claudeDesktopProfileArgument(command, dir, platform string) bool {
	if platform == "windows" {
		command, dir = strings.ToLower(command), strings.ToLower(dir)
	}
	for _, argument := range []string{"--user-data-dir=" + dir, `"--user-data-dir=` + dir + `"`, `--user-data-dir="` + dir + `"`} {
		for start := 0; start < len(command); {
			index := strings.Index(command[start:], argument)
			if index < 0 {
				break
			}
			index += start
			end := index + len(argument)
			boundary := func(b byte) bool { return b == ' ' || b == '\t' }
			if (index == 0 || boundary(command[index-1])) && (end == len(command) || boundary(command[end])) {
				return true
			}
			start = index + 1
		}
	}
	return false
}

// Launch with a clean provider environment. In particular, packaged Desktop's
// developer-only directory override must never select a different data root,
// and embedded Code must not inherit a regular login, API key, or E2E settings.
func claudeDesktopInheritedEnvironment(parent []string) []string {
	unset := []string{"CLAUDE_USER_DATA_DIR", "CLAUDE_CDP_AUTH", "ELECTRON_RUN_AS_NODE", "NODE_OPTIONS"}
	seen := map[string]bool{}
	for _, name := range unset {
		seen[name] = true
	}
	for _, entry := range parent {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if (strings.HasPrefix(upper, "CLAUDE_") || strings.HasPrefix(upper, "ANTHROPIC_") || upper == "CLAUDECODE") && !seen[name] {
			unset = append(unset, name)
			seen[name] = true
		}
	}
	return unset
}

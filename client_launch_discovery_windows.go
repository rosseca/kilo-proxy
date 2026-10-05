//go:build windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// Store installations live in a versioned WindowsApps package, rather than
// LocalAppData/Programs. Ask Windows for packages registered to the current
// user; scanning WindowsApps could select a stale or another user's package.
func queryWindowsAppPackagePaths(name string) []string {
	var query string
	switch name {
	case "OpenAI.Codex":
		query = "Get-AppxPackage -Name 'OpenAI.Codex'"
	case "*Claude*":
		query = "Get-AppxPackage -Name '*Claude*'"
	default:
		return nil
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	script := "$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); $paths=@(" + query + " | Sort-Object Version -Descending | ForEach-Object { $_.InstallLocation }); ConvertTo-Json -InputObject $paths -Compress"
	command := exec.CommandContext(ctx, filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var output terminalPowerShellOutput
	command.Stdout = &output
	if command.Run() != nil || output.overflow {
		return nil
	}
	return parseWindowsAppPackagePaths(output.Bytes())
}

func parseWindowsAppPackagePaths(data []byte) []string {
	if len(data) > 16<<10 {
		return nil
	}
	var paths []string
	if json.Unmarshal(data, &paths) != nil {
		return nil
	}
	var out []string
	for _, path := range paths {
		if filepath.IsAbs(path) && len(path) <= 8192 && !strings.ContainsAny(path, "\x00\r\n") {
			out = append(out, filepath.Clean(path))
		}
	}
	return out
}

func windowsCodexDesktopPaths(localAppData, programFiles, programFilesX86 string, packages []string) []string {
	var paths []string
	if filepath.IsAbs(localAppData) {
		for _, relative := range []string{"Programs/Codex/Codex.exe", "Programs/ChatGPT/ChatGPT.exe", "Codex/Codex.exe"} {
			paths = append(paths, filepath.Join(localAppData, filepath.FromSlash(relative)))
		}
	}
	for _, base := range []string{programFiles, programFilesX86} {
		if filepath.IsAbs(base) {
			paths = append(paths, filepath.Join(base, "Codex", "Codex.exe"), filepath.Join(base, "ChatGPT", "ChatGPT.exe"))
		}
	}
	for _, base := range packages {
		if filepath.IsAbs(base) {
			// Current Store manifests name the desktop entry point ChatGPT.exe.
			// Older Codex packages use Codex.exe. Their resources/codex.exe is
			// the terminal client and must never be mistaken for the desktop.
			paths = append(paths, filepath.Join(base, "app", "ChatGPT.exe"), filepath.Join(base, "app", "Codex.exe"), filepath.Join(base, "Codex.exe"))
		}
	}
	return paths
}

func windowsClientExecutable(paths []string) string {
	for _, path := range paths {
		if filepath.IsAbs(path) && launchExecutable(path) {
			return path
		}
	}
	return ""
}

func windowsClientLocalAppData(home, localAppData string) string {
	if localAppData == "" {
		return filepath.Join(home, "AppData", "Local")
	}
	return localAppData
}

func resolveWindowsCodexDesktop(home, localAppData string) string {
	localAppData = windowsClientLocalAppData(home, localAppData)
	local := windowsCodexDesktopPaths(localAppData, os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), nil)
	if path := windowsClientExecutable(local); path != "" {
		return path
	}
	return windowsClientExecutable(windowsCodexDesktopPaths("", "", "", queryWindowsAppPackagePaths("OpenAI.Codex")))
}

func windowsCodexCLIPaths(localAppData string, packages []string) []string {
	var paths []string
	if filepath.IsAbs(localAppData) {
		base := filepath.Join(localAppData, "OpenAI", "Codex")
		paths = append(paths, filepath.Join(base, "codex.exe"))
		versions, _ := filepath.Glob(filepath.Join(base, "bin", "*", "codex.exe"))
		// Desktop updates install CLI builds in hash-named directories. Pick
		// the newest installed binary if the launcher's inherited PATH is old.
		sort.SliceStable(versions, func(i, j int) bool {
			left, leftErr := os.Stat(versions[i])
			right, rightErr := os.Stat(versions[j])
			if leftErr != nil || rightErr != nil {
				return leftErr == nil && rightErr != nil
			}
			return left.ModTime().After(right.ModTime())
		})
		paths = append(paths, versions...)
	}
	for _, base := range packages {
		if filepath.IsAbs(base) {
			paths = append(paths, filepath.Join(base, "app", "resources", "codex.exe"))
		}
	}
	return paths
}

func resolveWindowsCodexCLI(home, localAppData string) string {
	localAppData = windowsClientLocalAppData(home, localAppData)
	if path := windowsClientExecutable(windowsCodexCLIPaths(localAppData, nil)); path != "" {
		return path
	}
	return windowsClientExecutable(windowsCodexCLIPaths("", queryWindowsAppPackagePaths("OpenAI.Codex")))
}

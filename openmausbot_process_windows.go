//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func openMausBotProcessAlive(pid int) (bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false, err
	}
	return code == 259, nil
}

func openMausBotWindowRunning(ctx context.Context, ui string) (bool, error) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return false, err
	}
	// Only an exact managed profile match leaves PowerShell. Never return
	// unrelated command lines or interpolate a filesystem path into a script.
	const script = `$ErrorActionPreference='Stop'
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
$pattern='(?:^|[\s"''])--user-data-dir=["'']?'+[regex]::Escape($env:KILO_OMB_USER_DATA)+'(?=$|[\s"''])'
$found=Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -and $_.CommandLine -match $pattern } | Select-Object -First 1
if ($found) { 'running' } else { 'stopped' }`
	env := clientChildEnvironment(os.Environ(), map[string]string{"KILO_OMB_USER_DATA": ui}, nil, "windows")
	out, err := openDesignCommandOutput(ctx, filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"), []string{"-NoProfile", "-NonInteractive", "-Command", script}, env)
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(string(out)) {
	case "running":
		return true, nil
	case "stopped":
		return false, nil
	}
	return false, errOpenMausBotRunning
}

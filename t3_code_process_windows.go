package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func t3CodeProcessStartTime(pid int) (time.Time, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return time.Time{}, err
	}
	if code != 259 {
		return time.Time{}, nil
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, created.Nanoseconds()), nil
}

func t3CodeProcessRunning(ctx context.Context, root string) (bool, error) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return false, err
	}
	// Return only an exact marker match. No unrelated process command line or
	// account data leaves this query and the path is passed as an env value.
	const script = `$ErrorActionPreference='Stop'
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
$pattern='(?:^|[\s"''])--kilo-proxy-t3-root=["'']?'+[regex]::Escape($env:KILO_T3_ROOT)+'(?=$|[\s"''])'
$found=Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -and $_.CommandLine -match $pattern } | Select-Object -First 1
if ($found) { 'running' } else { 'stopped' }`
	env := clientChildEnvironment(os.Environ(), map[string]string{"KILO_T3_ROOT": root}, nil, "windows")
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
	return false, errors.New("Cannot inspect the private T3 Code instance.")
}

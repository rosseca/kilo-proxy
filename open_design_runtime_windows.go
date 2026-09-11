//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func hideOpenDesignProbeWindow(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func openDesignPowerShell(ctx context.Context, script, namespace string) ([]byte, error) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, errOpenDesignRunningCheck
	}
	path := filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe")
	environment := clientChildEnvironment(os.Environ(), map[string]string{"KILO_OPEN_DESIGN_NAMESPACE": namespace}, nil, "windows")
	return openDesignCommandOutput(ctx, path, []string{"-NoProfile", "-NonInteractive", "-Command", script}, environment)
}

func openDesignProcessSnapshot(ctx context.Context) ([]string, error) {
	// CIM supplies complete command lines. Return only ownership stamp tokens,
	// never unrelated process arguments or credentials, from PowerShell.
	const script = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$pattern = '--od-stamp-(?:channel|namespace|source|mode|app)=[A-Za-z0-9][A-Za-z0-9._-]{0,127}|--od-sidecar-lifecycle=launcher'
Get-CimInstance Win32_Process | ForEach-Object {
  if ($_.CommandLine) {
    $tokens = [regex]::Matches($_.CommandLine, $pattern)
    if ($tokens.Count -gt 0) { ($tokens | ForEach-Object { $_.Value }) -join ' ' }
  }
}`
	data, err := openDesignPowerShell(ctx, script, "")
	if err != nil {
		return nil, err
	}
	return strings.Split(string(data), "\n"), nil
}

func openDesignProbeRuntime(ctx context.Context, namespace string) (bool, error) {
	// A bounded named-pipe describe probe catches startup/handoff between process
	// snapshots. The child returns only a boolean, never the response's contents.
	const script = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$namespace = $env:KILO_OPEN_DESIGN_NAMESPACE
if ($namespace -notmatch '^kilo-proxy-[a-f0-9]{12}$') { exit 2 }
foreach ($source in @('packaged', 'tools-pack')) {
  foreach ($mode in @('runtime', 'headless')) {
    foreach ($app in @('desktop', 'daemon', 'web')) {
      $stamp = @('channel=stable', ('namespace=' + $namespace), ('source=' + $source), ('mode=' + $mode), ('app=' + $app)) -join [char]10
      $hash = [Security.Cryptography.SHA256]::Create()
      try { $digest = -join ($hash.ComputeHash([Text.Encoding]::UTF8.GetBytes([Environment]::UserName + [char]10 + $stamp)) | ForEach-Object { $_.ToString('x2') }) } finally { $hash.Dispose() }
      $pipe = [IO.Pipes.NamedPipeClientStream]::new('.', ('open-design-sidecar-' + $digest.Substring(0,32)), [IO.Pipes.PipeDirection]::InOut, [IO.Pipes.PipeOptions]::Asynchronous)
      try {
        try { $pipe.Connect(75) } catch [TimeoutException] { continue }
        $request = [Text.Encoding]::UTF8.GetBytes('{"type":"sidecar:describe"}' + [char]10)
        $pipe.Write($request, 0, $request.Length)
        $reader = [IO.StreamReader]::new($pipe, [Text.Encoding]::UTF8)
        $read = $reader.ReadLineAsync()
        if (-not $read.Wait(300)) { exit 2 }
        $reply = $read.Result | ConvertFrom-Json
        $actual = $reply.result.stamp
        if (-not $reply.ok -or $reply.result.resources.pid -le 0 -or $actual.channel -ne 'stable' -or $actual.namespace -ne $namespace -or $actual.source -ne $source -or $actual.mode -ne $mode -or $actual.app -ne $app) { exit 2 }
        Write-Output 'running'
        exit 0
      } finally { $pipe.Dispose() }
    }
  }
}
Write-Output 'stopped'`
	data, err := openDesignPowerShell(ctx, script, namespace)
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(string(data)) {
	case "running":
		return true, nil
	case "stopped":
		return false, nil
	default:
		return false, errOpenDesignRunningCheck
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var errTerminalPowerShellProfile = errors.New("Cannot locate the PowerShell profile safely. Open PowerShell and check $PROFILE.CurrentUserAllHosts.")

// Keep generated blocks ASCII so appending to an existing Windows ANSI profile
// never changes its encoding. Unicode paths remain exact UTF-8 string values.
func terminalPowerShellLiteral(value string) string {
	for _, ch := range value {
		if ch > 127 {
			return "[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" + base64.StdEncoding.EncodeToString([]byte(value)) + "'))"
		}
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// ProcessStartInfo.Arguments uses Windows native quoting, not PowerShell syntax.
func terminalWindowsQuote(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	slashes := 0
	for _, ch := range value {
		if ch == '\\' {
			slashes++
			continue
		}
		if ch == '"' {
			out.WriteString(strings.Repeat("\\", slashes*2+1))
		} else {
			out.WriteString(strings.Repeat("\\", slashes))
		}
		out.WriteRune(ch)
		slashes = 0
	}
	out.WriteString(strings.Repeat("\\", slashes*2))
	out.WriteByte('"')
	return out.String()
}

func terminalPowerShellManualCommands(binary, configDir string) (terminalManualResult, error) {
	for _, path := range []string{binary, configDir} {
		if !filepath.IsAbs(path) || len(path) > 8192 || !utf8.ValidString(path) || strings.ContainsAny(path, "\x00\r\n") {
			return terminalManualResult{}, errTerminalManualPaths
		}
	}
	result := terminalManualResult{Supported: true, Shell: "powershell", Commands: make(map[string]string, 4)}
	var blocks []string
	for _, command := range []struct{ name, client string }{{"kilo-codex", "codex-cli"}, {"kilo-claude", "claude"}, {"kilo-omp", "omp"}, {"kilo-opencode", "opencode"}} {
		fixed := "--terminal-agent " + command.client + " --config-dir " + terminalWindowsQuote(configDir) + " --encoded-args "
		invoke := "& (" + terminalPowerShellLiteral(binary) + ") --terminal-agent " + command.client + " --config-dir (" + terminalPowerShellLiteral(configDir) + ") --encoded-args $kiloEncodedArgs | Microsoft.PowerShell.Utility\\Write-Output\n"
		// Base64 avoids PowerShell 5.1's loss of empty/quoted native arguments.
		// Normal invocation inherits console handles; piping Out-Default here
		// would make interactive CLIs see a redirected stdout instead of a TTY.
		// Text piped into a native command uses $OutputEncoding, independently
		// of Console.OutputEncoding. Shadow it only in this function so PS5.1's
		// ASCII default or a BOM-emitting caller preference cannot alter prompts.
		// Use a constructor expression: PS5.1 ignores New-Object's PSObject
		// wrapper in a child-scope preference (PowerShell/PowerShell#5763).
		// .NET Framework Process.Start also creates and AutoFlushes its first
		// stdin writer with Console.InputEncoding, before PowerShell replaces it.
		// Suppress that writer's BOM, then restore the encoding and Console.In
		// reader (the encoding setter resets it, potentially losing buffered input).
		block := "function " + command.name + " {\n" +
			"  $kiloEncodedArgs = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes((Microsoft.PowerShell.Utility\\ConvertTo-Json -InputObject ([string[]]$args) -Compress)))\n" +
			"  if ($MyInvocation.ExpectingInput) {\n" +
			"    $OutputEncoding = [System.Text.UTF8Encoding]::new($false)\n" +
			"    $kiloInputEncoding = [Console]::InputEncoding\n" +
			"    $kiloInputReader = [Console]::In\n" +
			"    $kiloInputHasPreamble = $kiloInputEncoding.GetPreamble().Length -gt 0\n" +
			"    try {\n" +
			"      if ($kiloInputHasPreamble) { [Console]::InputEncoding = $OutputEncoding }\n" +
			"      $input | " + invoke +
			"      $global:LASTEXITCODE = $LASTEXITCODE\n" +
			"    } finally {\n" +
			"      if ($kiloInputHasPreamble) {\n" +
			"        [Console]::InputEncoding = $kiloInputEncoding\n" +
			"        [Console]::SetIn($kiloInputReader)\n" +
			"      }\n    }\n    return\n  }\n" +
			"  if ($MyInvocation.PipelinePosition -lt $MyInvocation.PipelineLength) {\n" +
			"    " + invoke +
			"    $global:LASTEXITCODE = $LASTEXITCODE\n    return\n  }\n" +
			"  $kiloProcess = $null\n  try {\n" +
			"    $kiloStart = New-Object System.Diagnostics.ProcessStartInfo\n" +
			"    $kiloStart.FileName = " + terminalPowerShellLiteral(binary) + "\n" +
			"    $kiloStart.Arguments = " + terminalPowerShellLiteral(fixed) + " + $kiloEncodedArgs\n" +
			"    $kiloStart.UseShellExecute = $false\n" +
			"    $kiloStart.WorkingDirectory = (Get-Location).ProviderPath\n" +
			"    $kiloProcess = [System.Diagnostics.Process]::Start($kiloStart)\n" +
			"    $kiloProcess.WaitForExit()\n" +
			"    $global:LASTEXITCODE = $kiloProcess.ExitCode\n" +
			"  } catch {\n    $global:LASTEXITCODE = 1\n    Write-Error 'Could not run Kilo Proxy. Check the application path and try again.'\n" +
			"  } finally {\n    if ($null -ne $kiloProcess) { $kiloProcess.Dispose() }\n  }\n}\n"
		result.Commands[command.name] = block
		blocks = append(blocks, block)
	}
	result.All = strings.Join(blocks, "\n")
	return result, nil
}

const terminalPowerShellProfileQuery = "[Console]::Out.Write([Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([string]$PROFILE.CurrentUserAllHosts)))"

type terminalPowerShellOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *terminalPowerShellOutput) Write(data []byte) (int, error) {
	n := len(data)
	remaining := (16 << 10) - b.Len()
	if len(data) > remaining {
		b.overflow = true
		data = data[:remaining]
	}
	_, _ = b.Buffer.Write(data)
	return n, nil
}

func queryTerminalPowerShellProfile(binary string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", terminalPowerShellProfileQuery)
	hideOpenDesignProbeWindow(cmd)
	var output terminalPowerShellOutput
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	if cmd.Run() != nil || output.overflow {
		return "", errTerminalPowerShellProfile
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(output.String()))
	path := string(data)
	if err != nil || !utf8.Valid(data) || !validTerminalPowerShellProfile(path) {
		return "", errTerminalPowerShellProfile
	}
	return filepath.Clean(path), nil
}

func detectTerminalPowerShellProfiles() ([]string, error) {
	candidates := []string{}
	for _, name := range []string{"powershell.exe", "pwsh.exe"} {
		if path, err := exec.LookPath(name); err == nil && filepath.IsAbs(path) {
			candidates = append(candidates, path)
		}
	}
	for _, path := range []string{
		filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "PowerShell", "7", "pwsh.exe"),
	} {
		if filepath.IsAbs(path) && launchExecutable(path) {
			candidates = append(candidates, path)
		}
	}
	seenBinaries, seenProfiles := map[string]bool{}, map[string]bool{}
	var profiles []string
	for _, binary := range candidates {
		identity := strings.ToLower(filepath.Clean(binary))
		if seenBinaries[identity] {
			continue
		}
		seenBinaries[identity] = true
		profile, err := queryTerminalPowerShellProfile(binary)
		if err != nil {
			return nil, err
		}
		identity = strings.ToLower(profile)
		if !seenProfiles[identity] {
			profiles = append(profiles, profile)
			seenProfiles[identity] = true
		}
	}
	if len(profiles) == 0 {
		return nil, errors.New("Install Windows PowerShell or PowerShell 7 to install terminal commands automatically.")
	}
	return profiles, nil
}

func validTerminalPowerShellProfile(path string) bool {
	if !filepath.IsAbs(path) || len(path) > 8192 || strings.ContainsAny(path, "\x00\r\n") || !strings.EqualFold(filepath.Base(path), "profile.ps1") {
		return false
	}
	parent := filepath.Base(filepath.Dir(path))
	return strings.EqualFold(parent, "PowerShell") || strings.EqualFold(parent, "WindowsPowerShell")
}

var terminalPowerShellCommandConflict = regexp.MustCompile(`(?im)^\s*(?:function\s+(?:(?:global|script|local):)?|(?:Set-Alias|New-Alias|sal|nal)\s+(?:-Name\s+)?)["']?(?:kilo-codex|kilo-claude|kilo-omp|kilo-opencode)(?:["']?(?:\s|\(|\{)|$)`)

func terminalPowerShellCommandsPlan(configDir, binary string, profiles []string) (terminalCommandsInstallResult, []terminalInstallFile, error) {
	result := terminalCommandsInstallResult{Shell: "powershell", InstallerStyle: "powershell-profile", Commands: map[string]string{}}
	manual, err := terminalPowerShellManualCommands(binary, configDir)
	if err != nil {
		return result, nil, err
	}
	if len(profiles) == 0 || len(profiles) > 8 {
		return result, nil, errTerminalPowerShellProfile
	}
	for name := range manual.Commands {
		result.Commands[name] = name
	}
	result.Installed, result.PathConfigured = true, true
	seen := map[string]bool{}
	var files []terminalInstallFile
	for _, path := range profiles {
		if !validTerminalPowerShellProfile(path) {
			return result, nil, errTerminalPowerShellProfile
		}
		path = filepath.Clean(path)
		if seen[strings.ToLower(path)] {
			continue
		}
		seen[strings.ToLower(path)] = true
		// Documents can be redirected outside the home or into OneDrive. Trust
		// its location only after querying the shell's actual CurrentUserAllHosts;
		// the PowerShell subdirectory and profile must still be ordinary files.
		root := filepath.Dir(filepath.Dir(path))
		f, err := readTerminalInstallFile(root, path)
		if err != nil {
			return result, nil, err
		}
		f.data, err = terminalPowerShellProfileContent(f.old, manual.All)
		if err != nil {
			return result, nil, err
		}
		if len(f.data) > terminalStartupLimit {
			return result, nil, errors.New("The PowerShell profile would exceed 1 MiB; nothing was changed.")
		}
		if !f.exists || !bytes.Equal(f.old, f.data) {
			result.Installed, result.PathConfigured = false, false
		}
		result.StartupFiles = append(result.StartupFiles, path)
		files = append(files, f)
	}
	result.Message = "Install the four Kilo commands in your PowerShell profiles. Execution policy will not be changed."
	if result.Installed {
		result.Message = "The four Kilo commands are installed in your PowerShell profiles. Open a new PowerShell window; keep Kilo Proxy open. Execution policy was not changed."
	}
	return result, files, nil
}

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
)

func terminalPowerShellFixture(t *testing.T) (*app, []string) {
	t.Helper()
	a := launchTestApp(t)
	a.launcher.platform = "windows"
	a.terminalCommandsBinary = filepath.Join(a.launcher.home, "Kilo's app", "kilo-proxy.exe")
	// Documents may be redirected outside the ordinary home directory.
	documents := t.TempDir()
	profiles := []string{filepath.Join(documents, "WindowsPowerShell", "profile.ps1"), filepath.Join(documents, "PowerShell", "profile.ps1")}
	a.terminalCommandsProfiles = profiles
	return a, profiles
}

func TestTerminalPowerShellAPIsAndProfileInstallation(t *testing.T) {
	a, profiles := terminalPowerShellFixture(t)
	a.terminalCommandsShell = "unsupported-login-shell"
	for _, path := range profiles {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# User settings stay intact\r\n$UserPreference = 'keep'\r\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manualResponse := adminRequest(a, "terminal/manual", "")
	var manual terminalManualResult
	if manualResponse.Code != http.StatusOK || json.Unmarshal(manualResponse.Body.Bytes(), &manual) != nil || !manual.Supported || manual.Shell != "powershell" || len(manual.Commands) != 4 {
		t.Fatal("invalid Windows manual response", manualResponse.Body.String())
	}
	if !strings.Contains(manual.All, "--encoded-args") || !strings.Contains(manual.All, "UseShellExecute = $false") || !strings.Contains(manual.All, "WaitForExit()") || strings.Contains(manual.All, "Set-ExecutionPolicy") {
		t.Fatal("manual commands changed policy or lost native console/argument handling")
	}
	for _, secret := range []string{a.apiKey, a.config.LocalKey, a.adminToken} {
		if secret != "" && strings.Contains(manual.All, secret) {
			t.Fatal("PowerShell snippet leaked credentials")
		}
	}
	read := func(body string) terminalCommandsInstallResult {
		t.Helper()
		response := adminRequest(a, "terminal/commands", body)
		var info terminalCommandsInstallResult
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &info) != nil {
			t.Fatal("Windows installer failed", response.Code, response.Body.String())
		}
		return info
	}
	before := read("")
	if before.Installed || before.PathConfigured || before.Shell != "powershell" || before.InstallerStyle != "powershell-profile" || before.Directory != "" || !reflect.DeepEqual(before.StartupFiles, profiles) {
		t.Fatal("unexpected PowerShell installation status", before)
	}
	installed := read("{}")
	if !installed.Installed || !installed.PathConfigured || len(installed.Backups) != 2 || len(installed.Commands) != 4 {
		t.Fatal("PowerShell installation incomplete", installed)
	}
	for name, command := range installed.Commands {
		if name != command || manual.Commands[name] == "" {
			t.Fatal("PowerShell command should be a function name, not a wrapper path")
		}
	}
	for i, path := range profiles {
		data := terminalInstallerRead(t, path)
		old := []byte("# User settings stay intact\r\n$UserPreference = 'keep'\r\n")
		if !bytes.HasPrefix(data, old) || !bytes.Equal(terminalInstallerRead(t, installed.Backups[i]), old) || bytes.Count(data, []byte(terminalPathBegin)) != 1 {
			t.Fatal("existing PowerShell settings or exact backup lost")
		}
	}
	if repeated := read("{}"); !repeated.Installed || len(repeated.Backups) != 0 {
		t.Fatal("PowerShell installation was not idempotent")
	}
	a.terminalCommandsBinary = filepath.Join(a.launcher.home, "Moved Kilo Proxy.exe")
	if read("").Installed {
		t.Fatal("old application location was accepted as current")
	}
	if updated := read("{}"); len(updated.Backups) != 2 || !updated.Installed {
		t.Fatal("app move did not update managed PowerShell functions")
	}
	if _, err := os.Stat(filepath.Join(a.launcher.home, ".local")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Windows installation created POSIX wrappers")
	}
}

func terminalTestUTF16(text string, order binary.ByteOrder) []byte {
	units := utf16.Encode([]rune(text))
	data := make([]byte, len(units)*2+2)
	order.PutUint16(data, 0xfeff)
	for i, value := range units {
		order.PutUint16(data[2+i*2:], value)
	}
	return data
}

func TestTerminalPowerShellPreservesProfileEncodings(t *testing.T) {
	for name, old := range map[string][]byte{
		"new":      nil,
		"utf8":     []byte("# café\r\n$keep='😀'\r\n"),
		"utf8-bom": append([]byte{0xef, 0xbb, 0xbf}, []byte("# café\n$keep='😀'\n")...),
		"ansi":     []byte("# caf\xe9\r\n$keep=1\r\n"),
		"utf16-le": terminalTestUTF16("# café\r\n$keep='😀'\r\n", binary.LittleEndian),
		"utf16-be": terminalTestUTF16("# café\n$keep='😀'\n", binary.BigEndian),
	} {
		t.Run(name, func(t *testing.T) {
			result, err := terminalPowerShellProfileContent(old, "function kilo-codex { 'synthetic' }\n")
			if err != nil || !bytes.HasPrefix(result, old) {
				t.Fatal("profile bytes or encoding changed", err)
			}
			if name == "new" && !bytes.HasPrefix(result, []byte{0xef, 0xbb, 0xbf}) {
				t.Fatal("new profile lacks Windows PowerShell-compatible UTF-8 BOM")
			}
			again, err := terminalPowerShellProfileContent(result, "function kilo-codex { 'synthetic' }\n")
			if err != nil || !bytes.Equal(again, result) {
				t.Fatal("encoding conversion or managed block is not idempotent", err)
			}
		})
	}
	for _, old := range [][]byte{{0xff, 0xfe, 0}, {0xfe, 0xff, 0xdc, 0}, {0xff, 0xfe, 0, 0xd8}, {0xef, 0xbb, 0xbf, 0xff}, {0xff, 0xfe, 0, 0}, {'a', 0}} {
		if _, err := terminalPowerShellProfileContent(old, "function kilo-codex {}\n"); err == nil {
			t.Fatal("invalid profile encoding accepted")
		}
	}
}

func TestTerminalPowerShellPreflightProtectsAllProfiles(t *testing.T) {
	for _, conflict := range []string{"unowned function", "unowned alias", "signed", "broken markers", "duplicate markers", "profile link", "directory link", "invalid path"} {
		t.Run(conflict, func(t *testing.T) {
			a, profiles := terminalPowerShellFixture(t)
			if err := os.MkdirAll(filepath.Dir(profiles[0]), 0700); err != nil {
				t.Fatal(err)
			}
			original := []byte("# First profile remains unchanged\n")
			if err := os.WriteFile(profiles[0], original, 0600); err != nil {
				t.Fatal(err)
			}
			if conflict == "directory link" {
				if err := os.Symlink(t.TempDir(), filepath.Dir(profiles[1])); err != nil {
					t.Skip(err)
				}
			} else if conflict == "invalid path" {
				a.terminalCommandsProfiles[1] = filepath.Join(filepath.Dir(profiles[1]), "other.ps1")
			} else {
				if err := os.MkdirAll(filepath.Dir(profiles[1]), 0700); err != nil {
					t.Fatal(err)
				}
				contents := map[string]string{"unowned function": "function global:kilo-codex { 'mine' }\n", "unowned alias": "Set-Alias -Name kilo-claude -Value other\n", "signed": "# SIG # Begin signature block\n", "broken markers": terminalPathBegin + "\n", "duplicate markers": terminalPathBegin + "\n" + terminalPathBegin + "\n" + terminalPathEnd + "\n"}
				if conflict == "profile link" {
					if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), profiles[1]); err != nil {
						t.Skip(err)
					}
				} else if err := os.WriteFile(profiles[1], []byte(contents[conflict]), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if response := adminRequest(a, "terminal/commands", "{}"); response.Code != http.StatusConflict {
				t.Fatal("unsafe PowerShell installation succeeded", response.Code)
			}
			if !bytes.Equal(terminalInstallerRead(t, profiles[0]), original) {
				t.Fatal("preflight wrote a partial profile installation")
			}
			if response := adminRequest(a, "terminal/manual", ""); response.Code != http.StatusOK {
				t.Fatal("installation conflict disabled manual setup")
			}
		})
	}
}

func TestTerminalPowerShellNativeQuotesAndASCIISnippets(t *testing.T) {
	for input, want := range map[string]string{"": `""`, `C:\with spaces\`: `"C:\with spaces\\"`, `one"two`: `"one\"two"`, `one\"two`: `"one\\\"two"`} {
		if got := terminalWindowsQuote(input); got != want {
			t.Fatalf("native quoting %q: got %q want %q", input, got, want)
		}
	}
	home := t.TempDir()
	manual, err := terminalPowerShellManualCommands(filepath.Join(home, "Kilo’s app 😀.exe"), filepath.Join(home, "config's $(literal)"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range manual.All {
		if ch > 127 {
			t.Fatal("non-ASCII snippet can corrupt an existing ANSI profile")
		}
	}
	if !strings.Contains(manual.All, "FromBase64String('") || !strings.Contains(manual.All, "config''s $(literal)") {
		t.Fatal("Unicode or PowerShell quote handling missing")
	}
}

func TestTerminalPowerShellRollsBackWhenLaterProfileCannotBeWritten(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires POSIX directory write permissions")
	}
	a, profiles := terminalPowerShellFixture(t)
	original := []byte("# Keep the exact original profile\n")
	for _, path := range profiles {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
	}
	blocked := filepath.Dir(profiles[1])
	if err := os.Chmod(blocked, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(blocked, 0700)
	result, err := installTerminalCommands(a.launcher.home, a.dir, a.terminalCommandsBinary, "", "windows", profiles)
	if err == nil || result.Installed || len(result.Backups) != 1 {
		t.Fatal("later write failure did not return a backed-up partial failure", err)
	}
	for _, path := range profiles {
		if !bytes.Equal(original, terminalInstallerRead(t, path)) {
			t.Fatal("rollback did not restore the exact original profile", path)
		}
	}
}

func TestTerminalPowerShellProfileQueryDoesNotLoadProfiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake shell; real PowerShell functions are tested separately")
	}
	home := t.TempDir()
	profile := filepath.Join(home, "Redirected Documents", "WindowsPowerShell", "profile.ps1")
	encoded := base64.StdEncoding.EncodeToString([]byte(profile))
	binary := filepath.Join(home, "fake-powershell")
	script := "#!/bin/sh\n[ \"$1\" = -NoLogo ] && [ \"$2\" = -NoProfile ] && [ \"$3\" = -NonInteractive ] && [ \"$4\" = -Command ] && [ \"$5\" = " + helperShellQuote(terminalPowerShellProfileQuery) + " ] || exit 90\nprintf %s " + helperShellQuote(encoded) + "\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := queryTerminalPowerShellProfile(binary); err != nil || got != profile {
		t.Fatal("profile query changed its no-profile contract", got, err)
	}
	if _, err := os.Stat(filepath.Dir(profile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("profile detection created personal directories")
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'private-secret-error'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := queryTerminalPowerShellProfile(binary); err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatal("profile query exposed process output")
	}
}

func TestTerminalPowerShellFunctionsWithInstalledShell(t *testing.T) {
	var shells []string
	if path := os.Getenv("KILO_TEST_POWERSHELL_BINARY"); path != "" {
		shells = append(shells, path)
	}
	for _, name := range []string{"powershell.exe", "pwsh.exe", "pwsh"} {
		if path, err := exec.LookPath(name); err == nil {
			found := false
			for _, existing := range shells {
				found = found || existing == path
			}
			if !found {
				shells = append(shells, path)
			}
		}
	}
	if len(shells) == 0 {
		t.Skip("no PowerShell shell is installed; use KILO_TEST_POWERSHELL_BINARY for a portable test shell")
	}
	home := t.TempDir()
	source := filepath.Join(home, "probe.go")
	binary := filepath.Join(home, "Kilo's $(literal) 😀 probe.exe")
	program := `package main
import("encoding/base64";"encoding/json";"io";"os")
func main(){
 var payload string
 for i,a:=range os.Args {if a=="--encoded-args"&&i+1<len(os.Args){payload=os.Args[i+1]}}
 data,err:=base64.StdEncoding.DecodeString(payload);if err!=nil{os.Exit(91)}
 var args []string;if json.Unmarshal(data,&args)!=nil{os.Exit(92)}
 input,_:=io.ReadAll(os.Stdin);cwd,_:=os.Getwd()
 json.NewEncoder(os.Stdout).Encode(map[string]any{"raw":os.Args[1:],"args":args,"cwd":cwd,"input":string(input)})
 os.Exit(23)
}`
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("go", "build", "-o", binary, source).CombinedOutput(); err != nil {
		t.Fatal("cannot build synthetic native probe", err, string(output))
	}
	configDir := filepath.Join(home, "settings' $(literal) 😀")
	manual, err := terminalPowerShellManualCommands(binary, configDir)
	if err != nil {
		t.Fatal(err)
	}
	functions := filepath.Join(home, "functions.ps1")
	if err := os.WriteFile(functions, append([]byte{0xef, 0xbb, 0xbf}, []byte(manual.All)...), 0600); err != nil {
		t.Fatal(err)
	}
	for _, shell := range shells {
		for name, client := range map[string]string{"kilo-codex": "codex-cli", "kilo-claude": "claude", "kilo-omp": "omp", "kilo-opencode": "opencode"} {
			args := []string{"run", "", `one"two`, `tail\`, "with spaces", "$(literal)", "é😀", "--config-dir", "literal"}
			if client == "claude" {
				args = []string{}
			} else if client == "omp" {
				args = []string{""}
			}
			var literalArgs []string
			for _, arg := range args {
				literalArgs = append(literalArgs, "("+terminalPowerShellLiteral(arg)+")")
			}
			for _, mode := range []string{"console", "input-pipeline", "input-pipeline-ascii", "input-pipeline-console-ascii", "input-pipeline-literal-bom", "output-pipeline"} {
				t.Run(filepath.Base(shell)+"/"+name+"/"+mode, func(t *testing.T) {
					// A literal typed array also preserves zero arguments and one empty
					// argument on Windows PowerShell 5.1, unlike ConvertFrom-Json output.
					script := "[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)\n. (" + terminalPowerShellLiteral(functions) + ")\n[string[]]$manualArgs = @(" + strings.Join(literalArgs, ", ") + ")\n"
					// Reproduce a caller's BOM-emitting UTF-8 encoding even in PS7,
					// and PS5.1's ASCII preference without depending on host defaults.
					script += "$OutputEncoding = [Text.Encoding]::UTF8\n[Console]::InputEncoding = [Text.Encoding]::UTF8\n"
					if mode == "input-pipeline-ascii" {
						script += "$OutputEncoding = [Text.Encoding]::ASCII\n"
					}
					if mode == "input-pipeline-console-ascii" {
						script += "[Console]::InputEncoding = [Text.Encoding]::ASCII\n"
					}
					script += "$manualOutputEncoding = $OutputEncoding\n$manualInputEncoding = [Console]::InputEncoding\n$manualInputReader = [Console]::In\n"
					pipelineInput := "pipeline input café 日本語 😀"
					if mode == "input-pipeline-literal-bom" {
						pipelineInput = "\ufeff" + pipelineInput
					}
					if mode != "console" {
						script += "$manualCaptured = "
					}
					if strings.HasPrefix(mode, "input-pipeline") {
						script += "(" + terminalPowerShellLiteral(pipelineInput) + ") | "
					}
					script += name + " @manualArgs"
					if mode == "output-pipeline" {
						script += " | Microsoft.PowerShell.Utility\\Write-Output"
					}
					script += "\nif (-not [Object]::ReferenceEquals($OutputEncoding, $manualOutputEncoding)) { throw 'caller output encoding changed' }\n"
					script += "if (-not [Console]::InputEncoding.Equals($manualInputEncoding) -or -not [Object]::ReferenceEquals([Console]::In, $manualInputReader)) { throw 'caller console input changed' }\n"
					if mode != "console" {
						script += "if ($null -eq $manualCaptured) { throw 'pipeline output was consumed' }\n[Console]::Out.WriteLine($manualCaptured)\n"
					}
					script += "[Console]::Out.Write('SHELL_ALIVE:' + $global:LASTEXITCODE)\nexit 0\n"
					path := filepath.Join(home, "invoke.ps1")
					if err := os.WriteFile(path, append([]byte{0xef, 0xbb, 0xbf}, []byte(script)...), 0600); err != nil {
						t.Fatal(err)
					}
					command := exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", path)
					command.Dir = home
					command.Stdin = strings.NewReader("stdin preserved\n")
					output, err := command.CombinedOutput()
					if err != nil || !bytes.HasSuffix(output, []byte("SHELL_ALIVE:23")) {
						t.Fatal("PowerShell did not wait or preserve child status", err, string(output))
					}
					var got struct {
						Raw, Args  []string
						Cwd, Input string
					}
					if json.Unmarshal(bytes.TrimSuffix(output, []byte("SHELL_ALIVE:23")), &got) != nil || !reflect.DeepEqual(got.Args, args) {
						t.Fatal("native arguments lost empty strings or quotes", string(output))
					}
					wantPrefix := []string{"--terminal-agent", client, "--config-dir", configDir, "--encoded-args"}
					if len(got.Raw) != 6 || !reflect.DeepEqual(got.Raw[:5], wantPrefix) {
						t.Fatal("fixed flags/config path changed", got.Raw)
					}
					wantInput := "stdin preserved\n"
					if strings.HasPrefix(mode, "input-pipeline") {
						wantInput = pipelineInput + "\n"
					}
					canonical, _ := filepath.EvalSymlinks(home)
					gotCwd, _ := filepath.EvalSymlinks(got.Cwd)
					if gotCwd != canonical || strings.ReplaceAll(got.Input, "\r\n", "\n") != wantInput {
						t.Fatalf("cwd/stdin changed: cwd=%q stdin=%q want stdin=%q", got.Cwd, got.Input, wantInput)
					}
				})
			}
		}
		t.Run(filepath.Base(shell)+"/input-pipeline-start-failure-restores-console", func(t *testing.T) {
			missing, err := terminalPowerShellManualCommands(filepath.Join(home, "missing.exe"), configDir)
			if err != nil {
				t.Fatal(err)
			}
			script := "[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)\n" +
				"[Console]::InputEncoding = [Text.Encoding]::UTF8\n$OutputEncoding = [Text.Encoding]::ASCII\n" +
				"[Console]::SetIn([IO.StringReader]::new('buffered caller input'))\n" +
				"$manualInputEncoding = [Console]::InputEncoding\n$manualInputReader = [Console]::In\n$manualOutputEncoding = $OutputEncoding\n" +
				"$ErrorActionPreference = 'Stop'\n" + missing.Commands["kilo-codex"] +
				"$manualFailed = $false\ntry { 'input' | kilo-codex | Out-Null } catch { $manualFailed = $true }\n" +
				"if (-not $manualFailed) { throw 'missing executable succeeded' }\n" +
				"if (-not [Console]::InputEncoding.Equals($manualInputEncoding) -or -not [Object]::ReferenceEquals([Console]::In, $manualInputReader)) { throw 'caller console input changed after failure' }\n" +
				"if ([Console]::In.ReadToEnd() -ne 'buffered caller input') { throw 'caller console buffer lost after failure' }\n" +
				"if (-not [Object]::ReferenceEquals($OutputEncoding, $manualOutputEncoding)) { throw 'caller output encoding changed after failure' }\n" +
				"[Console]::Out.Write('RESTORED')\n"
			path := filepath.Join(home, "failure.ps1")
			if err := os.WriteFile(path, append([]byte{0xef, 0xbb, 0xbf}, []byte(script)...), 0600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", path)
			if output, err := command.CombinedOutput(); err != nil || string(output) != "RESTORED" {
				t.Fatalf("failed native invocation did not restore console preferences: %v; %s", err, output)
			}
		})
	}
}

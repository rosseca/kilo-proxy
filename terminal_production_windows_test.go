//go:build windows

package main

import (
	"bytes"
	"context"
	"debug/pe"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Packaging supplies the real -H=windowsgui executable. A console-subsystem
// test helper alone cannot catch PowerShell returning before a GUI app exits.
func TestTerminalAgentWindowsProductionPowerShell(t *testing.T) {
	built := os.Getenv("KILO_TEST_TERMINAL_BINARY")
	if built == "" {
		t.Skip("set KILO_TEST_TERMINAL_BINARY to the production desktop executable")
	}
	if !filepath.IsAbs(built) {
		t.Fatal("KILO_TEST_TERMINAL_BINARY must be absolute")
	}
	bin := filepath.Join(t.TempDir(), "tools with ' spaces café")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(built)
	if err != nil {
		t.Fatal(err)
	}
	program, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatal("production executable is not a Windows PE file:", err)
	}
	header, ok := program.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || header.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		t.Fatal("production terminal test requires the actual GUI-subsystem Windows executable")
	}
	_ = program.Close()
	binary := filepath.Join(bin, "Kilo Proxy.exe")
	if err := os.WriteFile(binary, data, 0700); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(t.TempDir(), "client.go")
	const source = `package main
import ("encoding/json"; "fmt"; "io"; "os")
func main() {
 if len(os.Args) == 2 && os.Args[1] == "--version" { fmt.Println("2.1.251 (synthetic client)"); return }
 input, _ := io.ReadAll(os.Stdin)
 cwd, _ := os.Getwd()
 json.NewEncoder(os.Stdout).Encode(map[string]any{
  "args": os.Args[1:], "input": string(input), "cwd": cwd,
  "codexHome": os.Getenv("CODEX_HOME"), "localKey": os.Getenv("KILO_LOCAL_API_KEY"),
  "claudeHome": os.Getenv("CLAUDE_CONFIG_DIR"), "anthropicKey": os.Getenv("ANTHROPIC_API_KEY"),
  "ompHome": os.Getenv("PI_CODING_AGENT_DIR"), "ompProfile": os.Getenv("OMP_PROFILE"),
  "stateful": os.Getenv("PI_OPENAI_STATEFUL"), "openCodeConfig": os.Getenv("OPENCODE_CONFIG"),
  "openCodeContent": os.Getenv("OPENCODE_CONFIG_CONTENT"),
 })
 os.Stderr.WriteString("synthetic client stderr\n")
 os.Exit(23)
}`
	if err := os.WriteFile(fixture, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	helper := filepath.Join(bin, "codex.exe")
	if output, err := exec.CommandContext(ctx, "go", "build", "-o", helper, fixture).CombinedOutput(); err != nil {
		t.Fatalf("build synthetic client: %v; %s", err, output)
	}
	data, err = os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude.exe", "omp.exe", "opencode.exe"} {
		if err := os.WriteFile(filepath.Join(bin, name), data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	a := terminalTestApp(t)
	a.launcher.platform = "windows"
	a.terminalCommandsBinary = binary
	a.terminalCommandsProfiles = []string{filepath.Join(t.TempDir(), "PowerShell", "profile.ps1")}
	server := httptest.NewServer(a.adminHandler())
	defer server.Close()
	a.adminHost = strings.TrimPrefix(server.URL, "http://")
	cleanup, err := a.publishTerminalRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	w := adminRequest(a, "terminal/manual", "")
	var manual terminalManualResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &manual) != nil || !manual.Supported || manual.Shell != "powershell" {
		t.Fatalf("manual setup: %d %s", w.Code, w.Body.String())
	}
	project := filepath.Join(t.TempDir(), "project ' café")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	arguments := []string{"", "with spaces", "quote\"literal", "trailing\\", "a'b", "$(no-command)", "%PATH% !&|^<>()", "line\nbreak", "日本語"}
	var literals []string
	for _, arg := range arguments {
		literals = append(literals, quote(arg))
	}
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		path, err := exec.LookPath(shell)
		if err != nil {
			// Both editions are required by the Windows packaging gate.
			t.Fatal("PowerShell edition unavailable:", shell, err)
		}
		for _, mode := range []string{"manual", "installed"} {
			setup := manual.All
			if mode == "installed" {
				w := adminRequest(a, "terminal/commands", `{}`)
				if w.Code != 200 {
					t.Fatal(w.Body.String())
				}
				setup = ". " + quote(a.terminalCommandsProfiles[0]) + "\n"
			}
			for _, client := range []string{"codex-cli", "claude", "omp", "opencode"} {
				t.Run(shell+"/"+mode+"/"+client, func(t *testing.T) {
					name := map[string]string{"codex-cli": "kilo-codex", "claude": "kilo-claude", "omp": "kilo-omp", "opencode": "kilo-opencode"}[client]
					code := "$ErrorActionPreference = 'Stop'\n[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)\n" + setup + "\n" + name + " " + strings.Join(literals, " ") + "\n$kiloResult = $LASTEXITCODE\n[Console]::Out.WriteLine('SHELL_ALIVE')\nexit $kiloResult\n"
					script := filepath.Join(t.TempDir(), "test.ps1")
					if err := os.WriteFile(script, append([]byte{0xef, 0xbb, 0xbf}, []byte(code)...), 0600); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					command := exec.CommandContext(ctx, path, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", script)
					command.Dir = project
					command.Env = clientChildEnvironment(os.Environ(), map[string]string{"PATH": bin + string(os.PathListSeparator) + os.Getenv("PATH"), "CODEX_HOME": "wrong", "KILO_LOCAL_API_KEY": "wrong", "CLAUDE_CONFIG_DIR": "wrong", "ANTHROPIC_API_KEY": "wrong", "PI_CODING_AGENT_DIR": "wrong", "OMP_PROFILE": "wrong", "PI_OPENAI_STATEFUL": "1", "OPENCODE_CONFIG": "wrong", "OPENCODE_CONFIG_CONTENT": `{"model":"wrong/model"}`}, nil, "windows")
					command.Stdin = strings.NewReader("stdin preserved\n")
					var output, stderr bytes.Buffer
					command.Stdout, command.Stderr = &output, &stderr
					var exit *exec.ExitError
					if err := command.Run(); !errors.As(err, &exit) || exit.ExitCode() != 23 {
						t.Fatalf("exit=%v; stdout=%s stderr=%s", err, output.String(), stderr.String())
					}
					lines := strings.Split(strings.TrimSpace(output.String()), "\n")
					if len(lines) != 2 || strings.TrimSpace(lines[1]) != "SHELL_ALIVE" {
						t.Fatal("PowerShell did not wait for the CLI and resume:", output.String())
					}
					var got struct {
						Args                                                           []string
						Input, Cwd, CodexHome, LocalKey, ClaudeHome, AnthropicKey      string
						OMPHome, OMPProfile, Stateful, OpenCodeConfig, OpenCodeContent string
					}
					if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
						t.Fatal(err, output.String())
					}
					wantArgs := arguments
					if client == "claude" {
						wantArgs = append([]string{"--settings", filepath.Join(a.claudeProfileDir, "settings.json")}, arguments...)
					}
					if client == "omp" {
						wantArgs = append([]string{"--model", "kilo-local/vendor/two"}, arguments...)
					}
					if !reflect.DeepEqual(got.Args, wantArgs) {
						t.Errorf("CLI arguments = %q; want %q", got.Args, wantArgs)
					}
					if got.Input != "stdin preserved\n" {
						t.Errorf("CLI stdin = %q; want %q", got.Input, "stdin preserved\n")
					}
					assertTerminalProductionDirectory(t, got.Cwd, project)
					if stderr.String() != "synthetic client stderr\n" {
						t.Errorf("CLI stderr = %q; want %q", stderr.String(), "synthetic client stderr\n")
					}
					if client == "codex-cli" && (got.CodexHome != a.codexCLIProfileDir || got.LocalKey != a.config.LocalKey) {
						t.Fatal("Codex profile lost")
					}
					if client == "claude" && (got.ClaudeHome != a.claudeProfileDir || got.AnthropicKey != "") {
						t.Fatal("Claude profile lost or inherited another provider's credentials")
					}
					if client == "omp" && (got.OMPHome != a.ompProfileDir || got.OMPProfile != "" || got.Stateful != "0") {
						t.Fatal("Oh My Pi profile lost")
					}
					if client == "opencode" {
						_, config, _, err := a.editorPaths("opencode")
						if err != nil || got.OpenCodeConfig != config || got.OpenCodeContent != "" {
							t.Fatal("OpenCode profile lost")
						}
					}
				})
			}
			for _, inputMode := range []string{"input-and-output", "input-literal-bom", "output-only"} {
				t.Run(shell+"/"+mode+"/opencode-pipeline-"+inputMode, func(t *testing.T) {
					invocation := "kilo-opencode " + strings.Join(literals, " ")
					wantInput := "stdin preserved\n"
					if inputMode != "output-only" {
						input := "pipeline stdin café 日本語 😀"
						if inputMode == "input-literal-bom" {
							input = "\ufeff" + input
						}
						invocation = quote(input) + " | " + invocation
						wantInput = input + "\n"
					}
					// Windows PowerShell exposes native stderr as error records. Keep
					// that stream separate while checking the native exit code; Stop
					// would turn the fixture's stderr into a terminating shell error.
					code := "$ErrorActionPreference = 'Continue'\n$PSNativeCommandUseErrorActionPreference = $false\n[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)\n$OutputEncoding = [Text.Encoding]::UTF8\n[Console]::InputEncoding = [Text.Encoding]::UTF8\n$kiloPreviousEncoding = $OutputEncoding\n$kiloPreviousInputEncoding = [Console]::InputEncoding\n$kiloPreviousInputReader = [Console]::In\n" + setup +
						"\n$kiloCaptured = @(" + invocation + " | Microsoft.PowerShell.Utility\\Write-Output)\n" +
						"$kiloResult = $LASTEXITCODE\nif (-not [Object]::ReferenceEquals($OutputEncoding, $kiloPreviousEncoding)) { throw 'Caller output encoding changed.' }\nif ($kiloCaptured.Count -ne 1) { throw 'CLI output was not returned through the pipeline.' }\n" +
						"if (-not [Console]::InputEncoding.Equals($kiloPreviousInputEncoding) -or -not [Object]::ReferenceEquals([Console]::In, $kiloPreviousInputReader)) { throw 'Caller console input changed.' }\n" +
						"[Console]::Out.WriteLine([string]$kiloCaptured[0])\n[Console]::Out.WriteLine('SHELL_ALIVE')\nexit $kiloResult\n"
					script := filepath.Join(t.TempDir(), "pipeline.ps1")
					if err := os.WriteFile(script, append([]byte{0xef, 0xbb, 0xbf}, []byte(code)...), 0600); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					command := exec.CommandContext(ctx, path, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", script)
					command.Dir = project
					command.Env = clientChildEnvironment(os.Environ(), map[string]string{"PATH": bin + string(os.PathListSeparator) + os.Getenv("PATH"), "OPENCODE_CONFIG": "wrong", "OPENCODE_CONFIG_CONTENT": `{"model":"wrong/model"}`}, nil, "windows")
					command.Stdin = strings.NewReader("stdin preserved\n")
					var output, stderr bytes.Buffer
					command.Stdout, command.Stderr = &output, &stderr
					var exit *exec.ExitError
					if err := command.Run(); !errors.As(err, &exit) || exit.ExitCode() != 23 {
						t.Fatalf("pipeline exit=%v; stdout=%s stderr=%s", err, output.String(), stderr.String())
					}
					lines := strings.Split(strings.TrimSpace(output.String()), "\n")
					if len(lines) != 2 || strings.TrimSpace(lines[1]) != "SHELL_ALIVE" {
						t.Fatal("pipeline output was lost or the shell did not wait:", output.String())
					}
					var got struct {
						Args                                        []string
						Input, Cwd, OpenCodeConfig, OpenCodeContent string
					}
					if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
						t.Fatal(err, output.String())
					}
					_, config, _, err := a.editorPaths("opencode")
					if err != nil {
						t.Fatal("cannot locate expected OpenCode profile:", err)
					}
					if !reflect.DeepEqual(got.Args, arguments) {
						t.Errorf("pipeline CLI arguments = %q; want %q", got.Args, arguments)
					}
					if strings.ReplaceAll(got.Input, "\r\n", "\n") != wantInput {
						t.Errorf("pipeline CLI stdin = %q; want %q (allowing PowerShell CRLF)", got.Input, wantInput)
					}
					assertTerminalProductionDirectory(t, got.Cwd, project)
					if got.OpenCodeConfig != config {
						t.Errorf("pipeline OpenCode profile = %q; want %q", got.OpenCodeConfig, config)
					}
					if got.OpenCodeContent != "" {
						t.Error("pipeline retained inherited OpenCode inline configuration")
					}
					if !strings.Contains(stderr.String(), "synthetic client stderr") {
						t.Errorf("pipeline CLI stderr = %q; want the synthetic client stderr marker", stderr.String())
					}
				})
			}
		}
	}
}

func assertTerminalProductionDirectory(t *testing.T, got, want string) {
	t.Helper()
	// PowerShell can expand Go's temporary 8.3 path (RUNNER~1) to the long
	// directory name. Check filesystem identity without rewriting either path.
	gotDirectory, gotErr := os.Stat(got)
	wantDirectory, wantErr := os.Stat(want)
	if gotErr != nil || wantErr != nil {
		t.Errorf("cannot inspect CLI cwd %q or expected directory %q: got error=%v; want error=%v", got, want, gotErr, wantErr)
		return
	}
	if !gotDirectory.IsDir() || !wantDirectory.IsDir() || !os.SameFile(gotDirectory, wantDirectory) {
		t.Errorf("CLI cwd %q is not the expected directory %q", got, want)
	}
}

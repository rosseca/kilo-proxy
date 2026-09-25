package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestTerminalManualAPIIndependentOfInstaller(t *testing.T) {
	a := launchTestApp(t)
	a.terminalCommandsBinary = filepath.Join(a.launcher.home, "Kilo's app", "kilo-proxy")
	a.terminalCommandsShell = "/bin/unsupported-shell"
	a.dir = filepath.Join(a.launcher.home, "uncreated settings")
	conflict := filepath.Join(a.launcher.home, ".local")
	original := []byte("user file that prevents automatic installation\n")
	if err := os.WriteFile(conflict, original, 0600); err != nil {
		t.Fatal(err)
	}
	if result := adminRequest(a, "terminal/commands", ""); result.Code != http.StatusConflict {
		t.Fatal("fixture did not block automatic installation", result.Code)
	}
	response := adminRequest(a, "terminal/manual", "")
	var result terminalManualResult
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.Supported || result.Shell != "zsh-bash" || len(result.Commands) != 4 {
		t.Fatal("manual commands were coupled to the installer", response.Code, response.Body.String())
	}
	var blocks []string
	for _, name := range []string{"kilo-codex", "kilo-claude", "kilo-omp", "kilo-opencode"} {
		block := result.Commands[name]
		if !strings.HasPrefix(block, "function "+name+" {\n  command ") || !strings.HasSuffix(block, " -- \"$@\"\n}\n") || strings.Contains(block, "exec ") {
			t.Fatal("unsafe or missing manual function", name)
		}
		blocks = append(blocks, block)
	}
	if result.All != strings.Join(blocks, "\n") {
		t.Fatal("combined snippet lost the stable command order")
	}
	for _, private := range []string{a.config.LocalKey, a.apiKey, a.adminToken} {
		if private != "" && strings.Contains(response.Body.String(), private) {
			t.Fatal("manual commands leaked a credential")
		}
	}
	if !bytes.Equal(terminalInstallerRead(t, conflict), original) {
		t.Fatal("manual setup changed an existing file")
	}
	if _, err := os.Stat(a.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("manual setup created or read the configuration directory")
	}
	if a.proxyListener != nil {
		t.Fatal("manual setup started the proxy")
	}
}

func TestTerminalManualAPIAuthenticationMethodsAndPlatforms(t *testing.T) {
	a := launchTestApp(t)
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del("Authorization") },
		func(r *http.Request) { r.Host = "evil.example" },
		func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") },
	} {
		r := httptest.NewRequest(http.MethodGet, "http://"+a.adminHost+"/api/terminal/manual", nil)
		r.Header.Set("Authorization", "Bearer "+a.adminToken)
		mutate(r)
		w := httptest.NewRecorder()
		a.adminHandler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Fatal("unauthorized manual command access", w.Code)
		}
	}
	if result := adminRequest(a, "terminal/manual", "{}"); result.Code != http.StatusMethodNotAllowed {
		t.Fatal("manual endpoint accepted a mutation")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, platform := range []string{"darwin", "macos", "linux"} {
		a.launcher.platform = platform
		response := adminRequest(a, "terminal/manual", "")
		var result terminalManualResult
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.Supported || !strings.Contains(result.All, helperShellQuote(binary)) {
			t.Fatal("supported platform did not use the application executable", platform)
		}
	}
	a.launcher.platform = "freebsd"
	a.terminalCommandsBinary = "invalid"
	response := adminRequest(a, "terminal/manual", "")
	var result map[string]any
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || !reflect.DeepEqual(result, map[string]any{"supported": false}) {
		t.Fatal("unsupported platform returned a shell snippet", response.Body.String())
	}
}

func TestTerminalManualRejectsUnsafePathsWithoutDisclosure(t *testing.T) {
	a := launchTestApp(t)
	valid := filepath.Join(a.launcher.home, "valid")
	for _, invalid := range []string{"", "relative/private-token", valid + "\nprivate-token", valid + "\rprivate-token", valid + "\x00private-token", valid + strings.Repeat("a", 8192)} {
		for _, binaryInvalid := range []bool{false, true} {
			a.dir, a.terminalCommandsBinary = valid, valid
			if binaryInvalid {
				if invalid == "" {
					continue // Empty override intentionally uses os.Executable.
				}
				a.terminalCommandsBinary = invalid
			} else {
				a.dir = invalid
			}
			response := adminRequest(a, "terminal/manual", "")
			if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), "private-token") || strings.Contains(response.Body.String(), a.launcher.home) {
				t.Fatal("invalid path accepted or disclosed", response.Code, response.Body.String())
			}
		}
	}
}

func TestTerminalManualFunctionsPreserveShellAndArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("manual functions target Bash and Zsh")
	}
	home := t.TempDir()
	binary := filepath.Join(home, "Kilo's $(touch pwned-binary) app")
	configDir := filepath.Join(home, "settings' $(touch pwned-config)")
	// A synthetic app reports argv/cwd/stdin and exits nonzero; it never opens a client.
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\000' \"$PWD\" \"$@\"\ncat\nexit 23\n"), 0700); err != nil {
		t.Fatal(err)
	}
	result, err := terminalManualCommands(binary, configDir)
	if err != nil {
		t.Fatal(err)
	}
	snippetPath := filepath.Join(home, "manual functions.zsh")
	if err := os.WriteFile(snippetPath, []byte(result.All), 0600); err != nil {
		t.Fatal(err)
	}
	canonicalHome, _ := filepath.EvalSymlinks(home)
	for _, shell := range []string{"bash", "zsh"} {
		path, err := exec.LookPath(shell)
		if err != nil {
			t.Log(shell, "is not installed; skipping its executable check")
			continue
		}
		t.Run(shell, func(t *testing.T) {
			prefix := []string{"--noprofile", "--norc", "-c"}
			if shell == "zsh" {
				prefix = []string{"-f", "-c"}
			}
			run := func(script string, args []string, input string) ([]byte, error) {
				t.Helper()
				arguments := append(append([]string(nil), prefix...), script, "manual-shell-test")
				command := exec.Command(path, append(arguments, args...)...)
				command.Dir = home
				command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "ZDOTDIR=" + home}
				command.Stdin = strings.NewReader(input)
				return command.CombinedOutput()
			}
			source := ". " + helperShellQuote(snippetPath) + "\n"
			if output, err := run(source+"printf 'loaded-only'\n", nil, ""); err != nil || string(output) != "loaded-only" {
				t.Fatal("defining functions executed an agent", err, string(output))
			}
			aliasSetup := "alias kilo-codex='printf prior-alias'\n"
			if shell == "bash" {
				aliasSetup = "shopt -s expand_aliases\n" + aliasSetup
			}
			// `function name` can be sourced safely when an old alias exists.
			// It intentionally preserves that alias; users remove it themselves.
			// Reparse this fixed name after sourcing, as a later interactive
			// command would be parsed, to apply the existing alias in both shells.
			if output, err := run(aliasSetup+source+"eval 'kilo-codex'\n", nil, ""); err != nil || string(output) != "prior-alias" {
				t.Fatal("function definitions expanded or removed an existing alias", err, string(output))
			}
			for name, client := range map[string]string{"kilo-codex": "codex-cli", "kilo-claude": "claude", "kilo-omp": "omp", "kilo-opencode": "opencode"} {
				args := []string{"run", "--continue", "-m", "provider/model", "spaces and 'quotes'", "$(touch pwned-argument)", ";touch pwned-semicolon", "", "--config-dir", "client argument"}
				// The function's status must return to the calling shell; exec would
				// skip this marker and terminate the user shell with the child.
				script := result.All + name + " \"$@\"\nmanual_exit=$?\nprintf '\\000SHELL_ALIVE:%s\\000' \"$manual_exit\"\nexit \"$manual_exit\"\n"
				output, err := run(script, args, "stdin preserved\n")
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 23 {
					t.Fatal("function lost the child exit status", name, err, string(output))
				}
				wantArgs := append([]string{canonicalHome, terminalAgentFlag, client, "--config-dir", configDir, "--"}, args...)
				want := strings.Join(wantArgs, "\x00") + "\x00stdin preserved\n\x00SHELL_ALIVE:23\x00"
				if string(output) != want {
					t.Fatalf("%s changed shell/cwd/argv/stdin: got %q want %q", name, output, want)
				}
			}
		})
	}
	for _, name := range []string{"pwned-binary", "pwned-config", "pwned-argument", "pwned-semicolon"} {
		if _, err := os.Stat(filepath.Join(home, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("shell evaluated literal path or argument content", name)
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Opt-in smoke check against an installed OpenCode CLI. It exercises the actual
// wrapper, manual functions and production executable without model inference.
func TestTerminalOpenCodeInstalledCLILoadsSharedModels(t *testing.T) {
	binary, cli := os.Getenv("KILO_TEST_TERMINAL_BINARY"), os.Getenv("KILO_TEST_OPENCODE_BINARY")
	if runtime.GOOS == "windows" || binary == "" || cli == "" {
		t.Skip("requires Unix and explicit Kilo Proxy/OpenCode executable paths")
	}
	if !filepath.IsAbs(binary) || !filepath.IsAbs(cli) {
		t.Fatal("test executable paths must be absolute")
	}
	a := terminalTestApp(t)
	a.terminalCommandsBinary = binary
	server := httptest.NewServer(a.adminHandler())
	defer server.Close()
	a.adminHost = strings.TrimPrefix(server.URL, "http://")
	cleanup, err := a.publishTerminalRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	bin := t.TempDir()
	if err := os.Symlink(cli, filepath.Join(bin, "opencode")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZDOTDIR", "")
	verify := func(t *testing.T, executable string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, executable, args...)
		command.Dir = t.TempDir()
		command.Env = clientChildEnvironment(os.Environ(), map[string]string{
			"PATH":                    bin + string(os.PathListSeparator) + os.Getenv("PATH"),
			"XDG_CONFIG_HOME":         t.TempDir(),
			"XDG_DATA_HOME":           t.TempDir(),
			"XDG_CACHE_HOME":          t.TempDir(),
			"XDG_STATE_HOME":          t.TempDir(),
			"OPENCODE_CONFIG":         "/wrong-opencode",
			"OPENCODE_CONFIG_CONTENT": `{"provider":{"kilo-local":{"models":{"stale-model":{}}}}}`,
		}, []string{"BASH_ENV", "ENV"}, runtime.GOOS)
		output, err := command.Output()
		if err != nil {
			t.Fatalf("installed OpenCode could not list the prepared models: %v", err)
		}
		if got := strings.Fields(string(output)); strings.Join(got, ",") != "kilo-local/vendor/one,kilo-local/vendor/two" {
			t.Fatalf("installed OpenCode did not load exactly the two shared models: %q", output)
		}
	}
	// Manual setup must work before the installer has created any wrappers.
	response := adminRequest(a, "terminal/manual", "")
	var manual struct {
		Commands map[string]string `json:"commands"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &manual) != nil || manual.Commands["kilo-opencode"] == "" {
		t.Fatal("could not retrieve the manual OpenCode function")
	}
	for _, shell := range []string{"zsh", "bash"} {
		t.Run("manual-"+shell, func(t *testing.T) {
			executable, err := exec.LookPath(shell)
			if err != nil {
				t.Skip("shell is not installed")
			}
			args := []string{"-f", "-c"}
			if shell == "bash" {
				args = []string{"--noprofile", "--norc", "-c"}
			}
			args = append(args, manual.Commands["kilo-opencode"]+"\nkilo-opencode --pure models kilo-local\n")
			verify(t, executable, args...)
		})
	}
	installed, err := installTerminalCommands(a.launcher.home, a.dir, binary, "zsh", runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("installed-wrapper", func(t *testing.T) {
		verify(t, installed.Commands["kilo-opencode"], "--pure", "models", "kilo-local")
	})
}

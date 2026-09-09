package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func terminalTestApp(t *testing.T) *app {
	t.Helper()
	a := launchTestApp(t)
	a.launcher.platform = "linux"
	a.launcher.terminal = func() (bool, string) { t.Fatal("terminal command tried to discover a GUI terminal"); return false, "" }
	a.launcher.resolve = func(string, string) (string, error) {
		t.Fatal("terminal command tried to discover an executable in the app")
		return "", nil
	}
	a.launcher.start = func(clientLaunchPlan) error { t.Fatal("terminal command opened a new terminal"); return nil }
	library := modelLibrary{SchemaVersion: 1, DefaultModel: "vendor/two", Models: []modelLibraryItem{{ID: "vendor/one", DisplayName: "My First", ContextWindow: 64000}, {ID: "vendor/two", DisplayName: "My Second", ContextWindow: 128000, ReasoningEffort: "high", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}}}}
	if _, err := a.modelLibrary.save(library, 0, false); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestTerminalPrepareUsesSharedLibraryAndLocalProfiles(t *testing.T) {
	a := terminalTestApp(t)
	cache, _ := json.Marshal(nativeCachedCatalog{SchemaVersion: 1, OrgID: a.config.OrgID, Models: []modelInfo{{ID: "vendor/two", Name: "Published name", InputModalities: []string{"text", "image"}}}})
	if err := atomicCatalogFile(filepath.Join(a.dir, "model-catalog.json"), cache); err != nil {
		t.Fatal(err)
	}
	for _, client := range []string{"codex-cli", "claude"} {
		request, _ := json.Marshal(terminalPrepareRequest{Client: client, Directory: a.launcher.home, ClaudeVersion: "2.1.251"})
		w := adminRequest(a, "terminal/prepare", string(request))
		if w.Code != 200 {
			t.Fatal(client, w.Code, w.Body.String())
		}
		var plan clientLaunchPlan
		if json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.Executable != "" || plan.Directory != a.launcher.home {
			t.Fatal("invalid console plan")
		}
		if strings.Contains(w.Body.String(), a.apiKey) {
			t.Fatal("upstream credential leaked to console")
		}
	}
	catalog, _ := os.ReadFile(filepath.Join(a.codexCLIProfileDir, "models.json"))
	for _, want := range []string{"My Second", "My First", `"image"`, `"high"`, `"low"`} {
		if !bytes.Contains(catalog, []byte(want)) {
			t.Fatalf("catalog lost %s", want)
		}
	}
	if _, err := os.Stat(a.codexProfileDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Codex Desktop profile touched")
	}
	state := a.modelLibrary.snapshot()
	state.Library.DefaultModel = "vendor/one"
	state.Library.Models[0].DisplayName = "Renamed in shared Models"
	if _, err := a.modelLibrary.save(state.Library, state.Revision, false); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(terminalPrepareRequest{Client: "codex-cli", Directory: a.launcher.home})
	if w := adminRequest(a, "terminal/prepare", string(request)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	catalog, _ = os.ReadFile(filepath.Join(a.codexCLIProfileDir, "models.json"))
	first, err := validateCatalog(catalog)
	if err != nil || first != "vendor/one" || !bytes.Contains(catalog, []byte("Renamed in shared Models")) {
		t.Fatal("command did not apply latest saved selection")
	}
	if a.proxyListener == nil {
		t.Fatal("command did not start the saved proxy")
	}
}

func TestTerminalPrepareRejectsUnauthenticatedAndInvalidRequests(t *testing.T) {
	a := terminalTestApp(t)
	for _, body := range []string{`{"client":"codex"}`, `{"client":"codex-cli","directory":"relative"}`, `{"client":"claude","command":"evil"}`, `{"client":"claude","claudeVersion":"bad\nheader"}`} {
		if w := adminRequest(a, "terminal/prepare", body); w.Code < 400 {
			t.Fatal("invalid request accepted", body)
		}
	}
	for _, path := range []string{"/api/terminal/prepare", "/api/terminal/commands"} {
		for _, method := range []string{"GET", "POST"} {
			r := httptest.NewRequest(method, "http://"+a.adminHost+path, strings.NewReader(`{"client":"codex-cli"}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			a.adminHandler().ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatal("missing authentication accepted", path, method)
			}
		}
	}
	if a.proxyListener != nil {
		t.Fatal("rejected request started proxy")
	}
	a.modelLibrary.mu.Lock()
	a.modelLibrary.state.RecoveryRequired = true
	a.modelLibrary.mu.Unlock()
	request, _ := json.Marshal(terminalPrepareRequest{Client: "codex-cli", Directory: a.launcher.home})
	if w := adminRequest(a, "terminal/prepare", string(request)); w.Code != 409 {
		t.Fatal("unsafe library accepted")
	}
}

func TestTerminalRuntimePrivateLifecycleAndResponseBoundary(t *testing.T) {
	a := terminalTestApp(t)
	cleanup, err := a.publishTerminalRuntime()
	if err != nil {
		t.Fatal(err)
	}
	info, err := readTerminalRuntime(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	file, _ := os.Stat(filepath.Join(a.dir, terminalRuntimeFile))
	if runtime.GOOS != "windows" && file.Mode().Perm() != 0600 {
		t.Fatal("runtime token not private")
	}
	newer := info
	newer.Token = randomKey("")
	data, _ := json.Marshal(newer)
	if err := atomicCatalogFile(filepath.Join(a.dir, terminalRuntimeFile), data); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if got, err := readTerminalRuntime(a.dir); err != nil || got.Token != newer.Token {
		t.Fatal("cleanup removed another instance's runtime")
	}
	if err := atomicCatalogFile(filepath.Join(a.dir, terminalRuntimeFile), []byte(`{"version":1,"host":"example.com:80","token":"secret"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := readTerminalRuntime(a.dir); err == nil {
		t.Fatal("nonlocal descriptor accepted")
	}
	plan := clientLaunchPlan{Client: "codex-cli", Name: "Codex CLI", Kind: "terminal", Env: map[string]string{"CODEX_HOME": info.CodexProfile, "KILO_LOCAL_API_KEY": "synthetic"}}
	if err := validateTerminalProfile(plan, info); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*clientLaunchPlan){
		func(p *clientLaunchPlan) { p.Executable = "/bin/sh" },
		func(p *clientLaunchPlan) { p.Args = []string{"-c", "malicious"} },
		func(p *clientLaunchPlan) { p.Env["LD_PRELOAD"] = "/tmp/malicious.so" },
		func(p *clientLaunchPlan) { p.Env["CODEX_HOME"] = "/tmp/other-profile" },
	} {
		copy := plan
		copy.Env = map[string]string{"CODEX_HOME": info.CodexProfile, "KILO_LOCAL_API_KEY": "synthetic"}
		mutate(&copy)
		if err := validateTerminalProfile(copy, info); err == nil {
			t.Fatal("untrusted response changed execution")
		}
	}
}

func TestTerminalRuntimeDoesNotFollowRedirects(t *testing.T) {
	a := terminalTestApp(t)
	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalled = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer server.Close()
	a.adminHost = strings.TrimPrefix(server.URL, "http://")
	cleanup, err := a.publishTerminalRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	_, err = requestTerminalPlan(context.Background(), a.dir, terminalPrepareRequest{Client: "codex-cli", Directory: a.launcher.home})
	if err == nil || targetCalled {
		t.Fatal("redirect followed or reported success")
	}
}

func TestTerminalPreservesUnsetLimitsAndRejectsControlErrors(t *testing.T) {
	choices := terminalLibraryChoices(modelLibrary{Models: []modelLibraryItem{{ID: "vendor/one"}}}, []modelInfo{{ID: "vendor/one", ContextWindow: 64000, MaxOutputTokens: 4000, InputModalities: []string{"text", "image"}}})
	if choices[0].Model.ContextWindow != 0 || choices[0].Model.MaxOutputTokens != 0 || !helperContains(choices[0].Model.InputModalities, "image") {
		t.Fatal("metadata replaced explicit unset limits or lost image support")
	}
	a := terminalTestApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { jsonError(w, 409, "unsafe\x1b]0;title\x07") }))
	defer server.Close()
	a.adminHost = strings.TrimPrefix(server.URL, "http://")
	cleanup, err := a.publishTerminalRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	_, err = requestTerminalPlan(context.Background(), a.dir, terminalPrepareRequest{Client: "codex-cli", Directory: a.launcher.home})
	if err == nil || strings.ContainsAny(err.Error(), "\x1b\x07") {
		t.Fatal("unsafe terminal control sequence reached the caller")
	}
}

func TestTerminalAgentProcessHelper(t *testing.T) {
	if os.Getenv("KILO_TERMINAL_PROCESS_TEST") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(runTerminalAgentMode(os.Args[i+1:]))
		}
	}
	os.Exit(99)
}

func TestTerminalClientProcessHelper(t *testing.T) {
	if os.Getenv("KILO_TERMINAL_PROCESS_TEST") != "1" {
		return
	}
	var args []string
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	input, _ := io.ReadAll(os.Stdin)
	directory, _ := os.Getwd()
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"args": args, "input": string(input), "cwd": directory, "codexHome": os.Getenv("CODEX_HOME"), "localKey": os.Getenv("KILO_LOCAL_API_KEY"), "claudeHome": os.Getenv("CLAUDE_CONFIG_DIR"), "anthropicKey": os.Getenv("ANTHROPIC_API_KEY"), "anthropicBase": os.Getenv("ANTHROPIC_BASE_URL")})
	os.Exit(23)
}

func TestTerminalAgentRunsInCurrentTerminal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix console commands")
	}
	a := terminalTestApp(t)
	server := httptest.NewServer(a.adminHandler())
	defer server.Close()
	a.adminHost = strings.TrimPrefix(server.URL, "http://")
	cleanup, err := a.publishTerminalRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	bin := filepath.Join(t.TempDir(), "tools with ' quotes")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	for _, name := range []string{"codex", "claude"} {
		script := "#!/bin/sh\nif [ \"$1\" = --version ]; then printf '2.1.251 (synthetic client)\\n'; exit 0; fi\nexec " + helperShellQuote(self) + " -test.run='^TestTerminalClientProcessHelper$' -- \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	project := filepath.Join(t.TempDir(), "project with ' spaces")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"resume", "with spaces", "literal$(should-not-run)", "semi;colon", "--model", "vendor/two", strings.Repeat("large prompt ", 1000)}
	for _, client := range []string{"codex-cli", "claude"} {
		binary := self
		arguments := append([]string{"-test.run=^TestTerminalAgentProcessHelper$", "--", client, "--config-dir", a.dir, "--"}, args...)
		// Optional packaging check exercises the actual distributed main/exec
		// entrypoint against this same temporary app and synthetic clients.
		if built := os.Getenv("KILO_TEST_TERMINAL_BINARY"); built != "" {
			if !filepath.IsAbs(built) {
				t.Fatal("KILO_TEST_TERMINAL_BINARY must be absolute")
			}
			binary = built
			arguments = append([]string{terminalAgentFlag, client, "--config-dir", a.dir, "--"}, args...)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, arguments...)
		command.Dir = project
		command.Env = clientChildEnvironment(os.Environ(), map[string]string{"KILO_TERMINAL_PROCESS_TEST": "1", "PATH": bin + string(os.PathListSeparator) + os.Getenv("PATH"), "DISPLAY": "", "WAYLAND_DISPLAY": "", "CODEX_HOME": "/wrong-codex", "KILO_LOCAL_API_KEY": "wrong-local", "CLAUDE_CONFIG_DIR": "/wrong-claude", "ANTHROPIC_API_KEY": "wrong-auth", "ANTHROPIC_BASE_URL": "https://wrong.example"}, nil, runtime.GOOS)
		command.Stdin = strings.NewReader("stdin preserved\n")
		var output, stderr bytes.Buffer
		command.Stdout, command.Stderr = &output, &stderr
		err := command.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 23 {
			t.Fatalf("%s: exit=%v, stderr=%s, out=%s", client, err, stderr.String(), output.String())
		}
		var got struct {
			Args                                                                     []string
			Input, Cwd, CodexHome, LocalKey, ClaudeHome, AnthropicKey, AnthropicBase string
		}
		if err := json.Unmarshal(output.Bytes(), &got); err != nil {
			t.Fatal(err, output.String())
		}
		wantArgs := args
		if client == "claude" {
			wantArgs = append([]string{"--settings", filepath.Join(a.claudeProfileDir, "settings.json")}, args...)
		}
		canonicalProject, _ := filepath.EvalSymlinks(project)
		canonicalCwd, _ := filepath.EvalSymlinks(got.Cwd)
		if !reflect.DeepEqual(got.Args, wantArgs) || got.Input != "stdin preserved\n" || canonicalCwd != canonicalProject {
			t.Fatalf("lost current terminal behavior: %+v", got)
		}
		if client == "codex-cli" && (got.CodexHome != a.codexCLIProfileDir || got.LocalKey != a.config.LocalKey) {
			t.Fatal("Codex did not use Kilo profile")
		}
		if client == "claude" && (got.ClaudeHome != a.claudeProfileDir || got.AnthropicKey != "" || got.AnthropicBase != "") {
			t.Fatal("Claude inherited another provider's auth")
		}
		if stderr.Len() != 0 {
			t.Fatal(fmt.Sprintf("unexpected launcher stderr: %s", stderr.String()))
		}
	}
}

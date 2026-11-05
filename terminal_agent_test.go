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
	library := modelLibrary{SchemaVersion: 1, DefaultModel: "vendor/two", Models: []modelLibraryItem{{ID: "vendor/one", DisplayName: "My First", ContextWindow: 128000}, {ID: "vendor/two", DisplayName: "My Second", ContextWindow: 128000, ReasoningEffort: "high", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}}}}
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
	for _, client := range []string{"codex-cli", "claude", "omp", "opencode"} {
		request, _ := json.Marshal(terminalPrepareRequest{Client: client, Directory: a.launcher.home, ClaudeVersion: "2.1.251"})
		w := adminRequest(a, "terminal/prepare", string(request))
		if w.Code != 200 {
			t.Fatal(client, w.Code, w.Body.String())
		}
		var plan clientLaunchPlan
		if json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.Executable != "" || plan.Directory != a.launcher.home {
			t.Fatal("invalid console plan")
		}
		if client == "opencode" && len(plan.Args) != 0 {
			t.Fatal("OpenCode received TUI flags that can break subcommands")
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

func TestTerminalPrepareClaude55ReasoningDefaults(t *testing.T) {
	a := terminalTestApp(t)
	library := modelLibrary{
		SchemaVersion: 1, DefaultModel: "openai/gpt-6.1-sol",
		Models: []modelLibraryItem{
			{ID: "openai/gpt-6.1-sol", ContextWindow: 200000},
			{ID: "anthropic/claude-sonnet-5.5", ReasoningEffort: "high", ContextWindow: 200000},
			{ID: "anthropic/claude-opus-5.5", ReasoningEffort: "xhigh", ContextWindow: 200000},
		},
	}
	if _, err := a.modelLibrary.save(library, a.modelLibrary.snapshot().Revision, false); err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(terminalPrepareRequest{Client: "claude", Directory: a.launcher.home, ClaudeVersion: "2.1.314"})
	if err != nil {
		t.Fatal(err)
	}
	w := adminRequest(a, "terminal/prepare", string(request))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var plan clientLaunchPlan
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.claudeProfileDir, "settings.json")
	if plan.Executable != "" || plan.Directory != a.launcher.home || plan.Env["CLAUDE_CONFIG_DIR"] != a.claudeProfileDir || !reflect.DeepEqual(plan.Args, []string{"--settings", path}) || !reflect.DeepEqual(plan.Unset, nativeClaudeResetEnv) {
		t.Fatal("invalid isolated Claude console plan")
	}
	data, err := os.ReadFile(filepath.Join(a.claudeProfileDir, "kilo-models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var selection claudeSelection
	if err := json.Unmarshal(data, &selection); err != nil {
		t.Fatal(err)
	}
	if selection.Initial != library.DefaultModel || len(selection.Models) != len(library.Models) {
		t.Fatal("terminal preparation changed the selected models")
	}
	for i, model := range library.Models {
		if selection.Models[i].ID != model.ID || selection.Models[i].Effort != model.ReasoningEffort {
			t.Fatal("terminal preparation lost exact gateway IDs or reasoning defaults")
		}
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if object(object(settings["modelSettings"])["claude-sonnet-5"])["effortLevel"] != "high" || object(object(settings["modelSettings"])["claude-opus-5"])["effortLevel"] != "xhigh" {
		t.Fatal("terminal preparation lost native reasoning defaults")
	}
	if a.proxyListener == nil {
		t.Fatal("successful preparation did not start the proxy")
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
	if choices[0].Model.ContextWindow != 64000 || choices[0].Model.MaxOutputTokens != 0 || choices[0].MaximumOutputTokens != 4000 || choices[0].ContextTokens != 0 || choices[0].ContextPreset != contextPresetRecommended || !helperContains(choices[0].Model.InputModalities, "image") {
		t.Fatal("metadata replaced saved limits, lost capacity, or lost image support")
	}
	resolved, resolveErr := contextPolicyForChoice(choices[0])
	if resolveErr != nil || resolved.ContextWindow != 64000 || resolved.MaxOutputTokens != 4000 {
		t.Fatal("unconfigured budget failed to respect catalog ceilings", resolved, resolveErr)
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
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"args": args, "input": string(input), "cwd": directory, "codexHome": os.Getenv("CODEX_HOME"), "localKey": os.Getenv("KILO_LOCAL_API_KEY"), "claudeHome": os.Getenv("CLAUDE_CONFIG_DIR"), "anthropicKey": os.Getenv("ANTHROPIC_API_KEY"), "anthropicBase": os.Getenv("ANTHROPIC_BASE_URL"), "ompHome": os.Getenv("PI_CODING_AGENT_DIR"), "ompProfile": os.Getenv("OMP_PROFILE"), "piProfile": os.Getenv("PI_PROFILE"), "stateful": os.Getenv("PI_OPENAI_STATEFUL"), "openCodeConfig": os.Getenv("OPENCODE_CONFIG"), "openCodeContent": os.Getenv("OPENCODE_CONFIG_CONTENT"), "managedTool": os.Getenv("KILO_TERMINAL_MANAGED_CLIENT"), "metaKey": os.Getenv("META_API_KEY"), "museAuthPath": os.Getenv("MUSE_AUTH_PATH"), "museNoAutoUpdate": os.Getenv("MUSE_NO_AUTO_UPDATE"), "home": os.Getenv("HOME"), "openCode2Key": os.Getenv("KILO_OPENCODE2_API_KEY")})
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
	for _, name := range []string{"codex", "claude", "omp", "opencode"} {
		version := "2.1.251 (synthetic client)"
		if name == "opencode" {
			version = "1.19.0"
		}
		script := "#!/bin/sh\nif [ \"$1\" = --version ]; then printf '%s\\n' " + helperShellQuote(version) + "; exit 0; fi\nexec " + helperShellQuote(self) + " -test.run='^TestTerminalClientProcessHelper$' -- \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Exercise installed wrappers through the runtime API and final exec, with
	// a synthetic app entrypoint unless a packaging check supplies the binary.
	binary := filepath.Join(bin, "kilo-proxy")
	entrypoint := "#!/bin/sh\nshift\nexec " + helperShellQuote(self) + " -test.run='^TestTerminalAgentProcessHelper$' -- \"$@\"\n"
	if err := os.WriteFile(binary, []byte(entrypoint), 0700); err != nil {
		t.Fatal(err)
	}
	if built := os.Getenv("KILO_TEST_TERMINAL_BINARY"); built != "" {
		if !filepath.IsAbs(built) {
			t.Fatal("KILO_TEST_TERMINAL_BINARY must be absolute")
		}
		binary = built
	}
	t.Setenv("ZDOTDIR", "")
	installed, err := installTerminalCommands(a.launcher.home, a.dir, binary, "zsh", runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project with ' spaces")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"resume", "with spaces", "literal$(should-not-run)", "semi;colon", "--model", "vendor/two", strings.Repeat("large prompt ", 1000)}
	for _, test := range []struct {
		client  string
		args    []string
		managed bool
		custom  bool
	}{
		{"codex-cli", args, false, false},
		{"claude", args, false, false},
		{"omp", args, false, false},
		{"opencode", append([]string{"run", "--continue", "-m", "kilo-local/vendor/one"}, args[1:]...), false, false},
		{"opencode", []string{"models", "kilo-local"}, false, false},
		{"opencode", []string{"--continue", "--model", "kilo-local/vendor/one"}, false, false},
		{"codex-cli", args, true, false},
		{"claude", args, true, false},
		{"claude", args, false, true},
		{"opencode", []string{"models", "kilo-local"}, false, true},
	} {
		client, arguments := test.client, test.args
		wantInstallation := ""
		if test.managed {
			wantInstallation = client
			record := managedToolRegistryFixture(t, a.dir, client, "terminal-managed")
			nativeName := map[string]string{"codex-cli": "codex", "claude": "claude"}[client]
			source, err := os.ReadFile(filepath.Join(bin, nativeName))
			if err != nil {
				t.Fatal(err)
			}
			source = bytes.Replace(source, []byte("#!/bin/sh\n"), []byte("#!/bin/sh\nexport KILO_TERMINAL_MANAGED_CLIENT="+client+"\n"), 1)
			if err := os.WriteFile(record.Executable, source, 0700); err != nil {
				t.Fatal(err)
			}
			record.Digest, err = managedToolFingerprint(record.Executable, runtime.GOOS)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeManagedTool(a.dir, record); err != nil {
				t.Fatal(err)
			}
		}
		if test.custom {
			wantInstallation = "custom-" + client
			source, err := os.ReadFile(filepath.Join(bin, client))
			if err != nil {
				t.Fatal(err)
			}
			source = bytes.Replace(source, []byte("#!/bin/sh\n"), []byte("#!/bin/sh\nexport KILO_TERMINAL_MANAGED_CLIENT="+wantInstallation+"\n"), 1)
			selected := filepath.Join(t.TempDir(), "custom "+client)
			if err := os.WriteFile(selected, source, 0700); err != nil {
				t.Fatal(err)
			}
			next, err := readSettings(a.dir)
			if err != nil {
				t.Fatal(err)
			}
			if client == "claude" {
				next.ClaudeCodeExecutable = selected
			} else {
				next.OpenCode1Executable = selected
			}
			if err := writeSettings(a.dir, next); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		name := map[string]string{"codex-cli": "kilo-codex", "claude": "kilo-claude", "omp": "kilo-omp", "opencode": "kilo-opencode"}[client]
		command := exec.CommandContext(ctx, installed.Commands[name], arguments...)
		command.Dir = project
		command.Env = clientChildEnvironment(os.Environ(), map[string]string{"KILO_TERMINAL_PROCESS_TEST": "1", "PATH": bin + string(os.PathListSeparator) + os.Getenv("PATH"), "DISPLAY": "", "WAYLAND_DISPLAY": "", "CODEX_HOME": "/wrong-codex", "KILO_LOCAL_API_KEY": "wrong-local", "CLAUDE_CONFIG_DIR": "/wrong-claude", "ANTHROPIC_API_KEY": "wrong-auth", "ANTHROPIC_BASE_URL": "https://wrong.example", "PI_CODING_AGENT_DIR": "/wrong-omp", "OMP_PROFILE": "personal", "PI_PROFILE": "work", "PI_OPENAI_STATEFUL": "1", "OPENCODE_CONFIG": "/wrong-opencode", "OPENCODE_CONFIG_CONTENT": `{"model":"wrong/model"}`}, nil, runtime.GOOS)
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
			ManagedTool                                                              string
			OMPHome, OMPProfile, PIProfile, Stateful                                 string
			OpenCodeConfig, OpenCodeContent                                          string
		}
		if err := json.Unmarshal(output.Bytes(), &got); err != nil {
			t.Fatal(err, output.String())
		}
		if got.ManagedTool != wantInstallation {
			t.Fatalf("terminal shortcut selected the wrong installation: managed=%v custom=%v marker=%q want=%q", test.managed, test.custom, got.ManagedTool, wantInstallation)
		}
		wantArgs := arguments
		if client == "claude" {
			wantArgs = append([]string{"--settings", filepath.Join(a.claudeProfileDir, "settings.json")}, args...)
		}
		if client == "omp" {
			wantArgs = append([]string{"--model", "kilo-local/vendor/two"}, arguments...)
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
		if client == "omp" && (got.OMPHome != a.ompProfileDir || got.OMPProfile != "" || got.PIProfile != "" || got.Stateful != "0") {
			t.Fatal("Oh My Pi inherited another profile or enabled stateful Responses")
		}
		if client == "opencode" {
			_, config, _, err := a.editorPaths("opencode")
			if err != nil || got.OpenCodeConfig != config || got.OpenCodeContent != "" {
				t.Fatal("OpenCode inherited another profile or inline configuration")
			}
		}
		if stderr.Len() != 0 {
			t.Fatal(fmt.Sprintf("unexpected launcher stderr: %s", stderr.String()))
		}
	}
}

// The installed kilo-opencode wrapper follows the saved OpenCode version: the
// private OpenCode 2 home, its own key and --standalone placed after the
// subcommand, without touching OpenCode 1 settings.
func TestTerminalAgentRunsOpenCode2WithStandalonePlacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix console commands")
	}
	a := terminalTestApp(t)
	next, err := readSettings(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	next.OpenCodeMajor = 2
	if err := writeSettings(a.dir, next); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.config.OpenCodeMajor = 2
	a.mu.Unlock()
	server := httptest.NewServer(a.adminHandler())
	defer server.Close()
	a.adminHost = strings.TrimPrefix(server.URL, "http://")
	cleanup, err := a.publishTerminalRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if info, err := readTerminalRuntime(a.dir); err != nil || info.OpenCode2Home == "" {
		t.Fatal("terminal runtime did not publish the OpenCode 2 home", err)
	}
	bin := filepath.Join(t.TempDir(), "tools with ' quotes")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then printf '%s\\n' '2.0.27 (synthetic OpenCode 2)'; exit 0; fi\nexec " + helperShellQuote(self) + " -test.run='^TestTerminalClientProcessHelper$' -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode2"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(bin, "kilo-proxy")
	entrypoint := "#!/bin/sh\nshift\nexec " + helperShellQuote(self) + " -test.run='^TestTerminalAgentProcessHelper$' -- \"$@\"\n"
	if err := os.WriteFile(binary, []byte(entrypoint), 0700); err != nil {
		t.Fatal(err)
	}
	if built := os.Getenv("KILO_TEST_TERMINAL_BINARY"); built != "" {
		if !filepath.IsAbs(built) {
			t.Fatal("KILO_TEST_TERMINAL_BINARY must be absolute")
		}
		binary = built
	}
	t.Setenv("ZDOTDIR", "")
	installed, err := installTerminalCommands(a.launcher.home, a.dir, binary, "zsh", runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	// A folder named like a subcommand must not change its placement.
	if err := os.Mkdir(filepath.Join(project, "models"), 0700); err != nil {
		t.Fatal(err)
	}
	home, _, _ := a.openCode2Paths()
	large := strings.Repeat("large prompt ", 1000)
	many := []string{"run", "--standalone-not-a-flag"}
	for i := 0; i < 120; i++ {
		many = append(many, fmt.Sprintf("file-%d.go", i))
	}
	for _, test := range []struct {
		args       []string
		wantArgs   []string
		wantStderr string
	}{
		{nil, []string{"--standalone"}, ""},
		{[]string{"models", "kilo-local"}, []string{"models", "--standalone", "kilo-local"}, ""},
		{[]string{"models"}, []string{"models", "--standalone"}, ""},
		{[]string{"run", "-m", "kilo-local/vendor/one", "hello"}, []string{"run", "--standalone", "-m", "kilo-local/vendor/one", "hello"}, ""},
		{[]string{"run", large}, []string{"run", "--standalone", large}, ""},
		{many, append([]string{"run", "--standalone"}, many[1:]...), ""},
		{[]string{"mcp", "list"}, []string{"mcp", "list"}, "does not accept --standalone"},
		{[]string{"frobnicate"}, []string{"frobnicate"}, "may not accept --standalone"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		command := exec.CommandContext(ctx, installed.Commands["kilo-opencode"], test.args...)
		command.Dir = project
		command.Env = clientChildEnvironment(os.Environ(), map[string]string{"KILO_TERMINAL_PROCESS_TEST": "1", "PATH": bin + string(os.PathListSeparator) + os.Getenv("PATH"), "OPENCODE_CONFIG": "/wrong-opencode", "OPENCODE_CONFIG_CONTENT": `{"model":"wrong/model"}`}, nil, runtime.GOOS)
		var output, stderr bytes.Buffer
		command.Stdout, command.Stderr = &output, &stderr
		err := command.Run()
		cancel()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 23 {
			t.Fatalf("%v: exit=%v, stderr=%s, out=%s", test.args, err, stderr.String(), output.String())
		}
		var got struct {
			Args                            []string
			Home, OpenCode2Key              string
			OpenCodeConfig, OpenCodeContent string
		}
		if err := json.Unmarshal(output.Bytes(), &got); err != nil {
			t.Fatal(err, output.String())
		}
		if !reflect.DeepEqual(got.Args, test.wantArgs) {
			t.Fatalf("%v: unexpected OpenCode 2 argv: %v", test.args, got.Args)
		}
		if got.Home != home || got.OpenCode2Key != a.config.LocalKey {
			t.Fatalf("%v: OpenCode 2 did not use its private home and key: %+v", test.args, got)
		}
		if got.OpenCodeConfig != "" || got.OpenCodeContent != "" {
			t.Fatalf("%v: OpenCode 2 inherited an OpenCode 1 override", test.args)
		}
		if test.wantStderr == "" && stderr.Len() != 0 {
			t.Fatalf("%v: unexpected launcher stderr: %s", test.args, stderr.String())
		}
		if test.wantStderr != "" && !strings.Contains(stderr.String(), test.wantStderr) {
			t.Fatalf("%v: missing OpenCode 2 warning: %s", test.args, stderr.String())
		}
	}
	// The OpenCode 1 profile stays untouched.
	if _, config, _, err := a.editorPaths("opencode"); err != nil {
		t.Fatal(err)
	} else if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatal("OpenCode 2 preparation wrote the OpenCode 1 profile", err)
	}
}

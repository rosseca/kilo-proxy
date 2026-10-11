package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Opt in to the installed Synara server and native CLIs. Every provider home is a
// disposable fixture, and every inference upstream is synthetic localhost.
func TestSynaraInstalledFourAgents(t *testing.T) {
	installed := os.Getenv("KILO_TEST_SYNARA_APP")
	if installed == "" {
		t.Skip("set KILO_TEST_SYNARA_APP to an installed Synara desktop application")
	}
	if runtime.GOOS != "darwin" || !filepath.IsAbs(installed) {
		t.Fatal("the installed Synara acceptance currently requires an absolute macOS app bundle")
	}
	synaraInstalledProcessObservationPreflight(t)
	version, err := synaraVersion(installed, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	rt := clientLaunchRuntime{platform: runtime.GOOS, home: realHome, resolve: resolveLaunchClient}
	codex, err := resolveOpenDesignCLI("codex-cli", rt)
	if err != nil {
		t.Fatal(err)
	}
	claude, err := resolveLaunchClient("claude", "")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required for the installed Synara acceptance check")
	}
	a := launchTestApp(t)
	adapterSource := os.Getenv("KILO_TEST_SYNARA_ADAPTER_SOURCE")
	if adapterSource == "" || validateOpenDesignShimBinary(adapterSource, runtime.GOOS, true) != nil {
		t.Fatal("set KILO_TEST_SYNARA_ADAPTER_SOURCE to a compiled native Kilo Proxy binary for the installed Synara acceptance")
	}
	a.synaraAdapterSource = func() (string, error) { return adapterSource, nil }
	a.config.Language = "en"
	a.config.LocalKey = "synthetic-synara-local"
	a.chatgpt.creds = chatGPTCredentials{Access: "synthetic-synara-chatgpt-access", Refresh: "synthetic-refresh", Account: "synthetic-synara-account", Expires: time.Now().Add(time.Hour).Unix()}
	a.chatgpt.state = chatGPTState{Status: "idle"}
	a.launcher.platform = runtime.GOOS
	a.launcher.resolve = func(id, custom string) (string, error) {
		if custom != "" {
			return "", errors.New("unexpected custom client in Synara fixture")
		}
		switch id {
		case "synara":
			return installed, nil
		case "codex-cli":
			return codex, nil
		case "claude":
			return claude, nil
		}
		return "", errors.New("unexpected fixture client")
	}
	a.launcher.start = func(clientLaunchPlan) error {
		t.Error("the server acceptance check must not open the desktop window")
		return errors.New("desktop launch disabled in Synara fixture")
	}
	a.synaraCheckRunning = func(string) (bool, error) { return false, nil }
	// Make prepare's environment snapshot synthetic too: its real HOME is never
	// passed to a provider, including machines that export custom XDG directories.
	for _, name := range []string{"USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(name, filepath.Join(a.launcher.home, name))
	}
	t.Setenv("CODEX_HOME", filepath.Join(a.launcher.home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(a.launcher.home, ".claude"))
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", filepath.Join(a.launcher.home, ".claude"))
	const codexModel = "vendor/synara-codex"
	const claudeModel = "anthropic/claude-opus-5.5"
	const claudeLowModel = "anthropic/claude-sonnet-5.5"
	const pickerPrompt = "Reply with the synthetic fixture response from the exact shared default."
	const chatgptModel = "chatgpt/gpt-synthetic-high"
	const responseText = "SYNTHETIC_SYNARA_OK"
	// Put the shared default after index zero so the picker acceptance can prove
	// that a stale built-in selection falls back to our configured default.
	library := modelLibrary{SchemaVersion: 1, DefaultModel: claudeModel, Models: []modelLibraryItem{
		{ID: codexModel, ReasoningEffort: "high", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, ContextWindow: 128000, MaxOutputTokens: 4096},
		// These synthetic catalog capabilities exercise the exact 5.5 IDs and
		// the saved High/Low defaults used by the reported workspace.
		{ID: claudeModel, ReasoningEffort: "high", ReasoningCustom: true, ReasoningLevels: []string{"low", "medium", "high"}, ContextWindow: 128000, MaxOutputTokens: 4096},
		{ID: chatgptModel, ReasoningEffort: "high", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, ContextWindow: 128000, MaxOutputTokens: 4096},
	}}
	catalog := []modelInfo{
		{ID: codexModel, Name: "Synthetic Codex", ContextWindow: 128000, MaxOutputTokens: 4096, ReasoningEfforts: []string{"low", "high"}},
		{ID: claudeModel, Name: "Synthetic Gateway Opus 5.5", ContextWindow: 128000, MaxOutputTokens: 4096, ReasoningEfforts: []string{"low", "medium", "high"}},
		{ID: chatgptModel, Name: "Synthetic ChatGPT", ContextWindow: 128000, MaxOutputTokens: 4096, ReasoningEfforts: []string{"low", "high"}},
	}
	{
		library.Models = append(library.Models, modelLibraryItem{ID: claudeLowModel, ReasoningEffort: "low", ReasoningCustom: true, ReasoningLevels: []string{"low", "medium", "high"}, ContextWindow: 128000, MaxOutputTokens: 4096})
		catalog = append(catalog, modelInfo{ID: claudeLowModel, Name: "Synthetic Claude Low", ContextWindow: 128000, MaxOutputTokens: 4096, ReasoningEfforts: []string{"low", "medium", "high"}})
	}
	if _, err := a.modelLibrary.save(library, 0, false); err != nil {
		t.Fatal(err)
	}
	cache, _ := json.Marshal(nativeCachedCatalog{SchemaVersion: 1, OrgID: a.catalogScopeLocked(), Models: catalog, FetchedAt: time.Now().UTC()})
	if err := os.WriteFile(filepath.Join(a.dir, "model-catalog.json"), cache, 0600); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project")
	var randomSentinel [16]byte
	if _, err := rand.Read(randomSentinel[:]); err != nil {
		t.Fatal(err)
	}
	readSentinel := fmt.Sprintf("SYNTHETIC_SYNARA_READ_%x", randomSentinel)
	var mu sync.Mutex
	counts := map[string]int{}
	toolResults := map[string]bool{}
	pickerRequests := 0
	count := func(route string) int {
		mu.Lock()
		defer mu.Unlock()
		counts[route]++
		return counts[route]
	}
	read := func(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid JSON from an installed Synara provider")
			http.Error(w, "invalid fixture request", 400)
			return nil, false
		}
		return body, true
	}
	respond := func(w http.ResponseWriter, r *http.Request, body map[string]any, route string) {
		if strings.HasSuffix(r.URL.Path, "/count_tokens") {
			jsonResponse(w, 200, map[string]any{"input_tokens": 20})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/messages") && body["stream"] != true {
			// Claude Code validates a new model with a one-token non-streaming
			// Messages request before sending the actual conversation turn.
			jsonResponse(w, 200, map[string]any{"id": "synthetic_synara_validation", "type": "message", "role": "assistant", "model": body["model"], "content": []any{map[string]any{"type": "text", "text": responseText}}, "stop_reason": "end_turn", "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}})
			return
		}
		number := count(route)
		messageID := fmt.Sprintf("msg_synara_%s_%d", strings.ReplaceAll(route, ":", "_"), number)
		callID := "call_synara_read_" + route
		if route == synaraCodexProxyID || route == synaraClaudeProxyID {
			if number == 1 {
				if route == synaraCodexProxyID {
					name, arguments := t3CodeFixtureCodexReadTool(body, filepath.Join(project, "README.md"))
					if name == "" {
						t.Errorf("installed Codex did not advertise a supported safe read tool: %v", body["tools"])
					} else {
						t3CodeFixtureResponseSSE(w, stringValue(body["model"]), map[string]any{"id": messageID, "type": "function_call", "call_id": callID, "name": name, "arguments": arguments, "status": "completed"})
						return
					}
				} else {
					found := false
					tools, _ := body["tools"].([]any)
					for _, raw := range tools {
						found = found || object(raw)["name"] == "Read"
					}
					if !found {
						t.Error("installed Claude did not advertise its safe Read tool")
					} else {
						t3CodeFixtureMessagesToolSSE(w, stringValue(body["model"]), messageID, callID, "Read", map[string]any{"file_path": filepath.Join(project, "README.md")})
						return
					}
				}
			} else if number == 2 {
				seen := t3CodeFixtureReadResult(body, callID, readSentinel, route == synaraCodexProxyID)
				if !seen {
					t.Errorf("%s did not return the actual temporary README tool result", route)
				}
				mu.Lock()
				toolResults[route] = seen
				mu.Unlock()
			}
		}
		if strings.HasSuffix(r.URL.Path, "/responses") {
			t3CodeFixtureResponseSSE(w, stringValue(body["model"]), map[string]any{"id": messageID, "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": responseText, "annotations": []any{}}}})
			return
		}
		t3CodeFixtureMessagesSSE(w, stringValue(body["model"]), responseText, messageID)
	}
	kilo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-kilo" || r.Header.Get("X-KiloCode-OrganizationId") != "synthetic-org" || r.Header.Get("ChatGPT-Account-Id") != "" {
			t.Error("Kilo credential/team isolation changed")
			http.Error(w, "wrong fixture credential", 401)
			return
		}
		body, ok := read(w, r)
		if !ok {
			return
		}
		switch r.URL.Path {
		case "/api/gateway/responses":
			if body["model"] != codexModel || object(body["reasoning"])["effort"] != "high" {
				t.Error("Kilo Codex lost its exact model or High reasoning")
			}
			respond(w, r, body, synaraCodexProxyID)
		case "/api/gateway/messages", "/api/gateway/messages/count_tokens":
			messages, _ := body["messages"].([]any)
			if body["model"] == claudeModel {
				if len(messages) > 0 && strings.HasPrefix(stringValue(object(messages[0])["content"]), "You generate concise chat thread titles.") {
					// The real UI also generates a preview and final title with
					// its chosen agent/model. Keep those distinct from inference
					// for the persisted conversation, without bypassing routing
					// or credential checks above.
					t3CodeFixtureMessagesSSE(w, claudeModel, `{"title":"Synthetic Synara UI"}`, "msg_synara_ui_title")
					return
				}
				// The picker uses the same exact model as the three-turn account
				// check. Identify its request by its real prompt, not by model ID.
				if r.URL.Path == "/api/gateway/messages" && body["stream"] == true && synaraInstalledRequestContainsText(messages, pickerPrompt) {
					if object(body["output_config"])["effort"] != "high" {
						t.Error("Synara picker lost the prepared Opus 5.5 High default")
					}
					mu.Lock()
					pickerRequests++
					mu.Unlock()
					t3CodeFixtureMessagesSSE(w, claudeModel, responseText, "msg_synara_ui_reply")
					return
				}
			}
			expectedEffort := "high"
			expectedModel := claudeModel
			mu.Lock()
			previousRequests := counts[synaraClaudeProxyID]
			mu.Unlock()
			if previousRequests == 2 {
				expectedEffort = "low"
				expectedModel = claudeLowModel
			}
			if body["model"] != expectedModel {
				t.Errorf("Kilo Claude lost exact model: expected %s, got %v", expectedModel, body["model"])
			}
			if r.URL.Path == "/api/gateway/messages" && body["stream"] == true {
				if object(body["output_config"])["effort"] != expectedEffort {
					t.Errorf("Kilo Claude reasoning mismatch at request %d: expected %s, output_config=%v thinking=%v", previousRequests+1, expectedEffort, body["output_config"], body["thinking"])
				}
			}
			respond(w, r, body, synaraClaudeProxyID)
		default:
			t.Errorf("unexpected Kilo upstream route %s", r.URL.Path)
			http.Error(w, "unexpected fixture route", 404)
		}
	}))
	defer kilo.Close()
	normalCodex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "" || r.Header.Get("X-KiloCode-OrganizationId") != "" {
			t.Error("normal Codex acquired Kilo credentials or routing")
			http.Error(w, "wrong normal Codex route/auth", 401)
			return
		}
		body, ok := read(w, r)
		if ok {
			respond(w, r, body, synaraCodexNormalID)
		}
	}))
	defer normalCodex.Close()
	normalClaude := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" && r.URL.Path != "/v1/messages/count_tokens" || r.Header.Get("Authorization") != "Bearer synthetic-t3-normal-claude" || r.Header.Get("X-KiloCode-OrganizationId") != "" {
			t.Error("normal Claude acquired Kilo credentials or routing")
			http.Error(w, "wrong normal Claude route/auth", 401)
			return
		}
		body, ok := read(w, r)
		if ok {
			respond(w, r, body, synaraClaudeNormalID)
		}
	}))
	defer normalClaude.Close()
	chatgpt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" || r.Header.Get("Authorization") != "Bearer synthetic-synara-chatgpt-access" || r.Header.Get("ChatGPT-Account-Id") != "synthetic-synara-account" || r.Header.Get("X-KiloCode-OrganizationId") != "" {
			t.Error("ChatGPT credential/account isolation changed")
			http.Error(w, "wrong ChatGPT fixture route/auth", 401)
			return
		}
		body, ok := read(w, r)
		if !ok {
			return
		}
		if body["model"] != "gpt-synthetic-high" || body["store"] != false || object(body["reasoning"])["effort"] != "high" {
			t.Error("ChatGPT adapter lost its model, High reasoning or non-storing request")
		}
		respond(w, r, body, synaraCodexProxyID+":chatgpt")
	}))
	defer chatgpt.Close()
	a.chatGPTResponsesURL = chatgpt.URL + "/codex/responses"
	setUpstream(a, kilo.URL)
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	a.transport = modelMetricTransport(func(r *http.Request) (*http.Response, error) {
		if "http://"+r.URL.Host != kilo.URL && "http://"+r.URL.Host != chatgpt.URL || r.URL.Scheme != "http" {
			return nil, errors.New("non-fixture proxy upstream blocked")
		}
		return transport.RoundTrip(r)
	})
	proxy := httptest.NewUnstartedServer(nil)
	a.config.Port = proxy.Listener.Addr().(*net.TCPAddr).Port
	proxy.Config.Handler = a.inferenceHandler(a.apiKey, a.config.OrgID, a.config.LocalKey, proxy.Listener.Addr().String())
	proxy.Start()
	defer proxy.Close()
	normalFiles := t3CodeFixtureNormalProfiles(t, a.launcher.home, library, catalog, normalCodex.URL, normalClaude.URL)
	prepareSynaraFixture(t, a)
	saved, err := a.readSynaraPrepared()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := a.planClientLaunch(clientLaunchRequest{Client: "synara"}, a.launchRuntime())
	if err != nil {
		t.Fatal(err)
	}
	paths := synaraPaths(a.dir)
	env := map[string]string{}
	for _, entry := range cgClientEnv(paths.UIHome) {
		name, value, _ := strings.Cut(entry, "=")
		env[name] = value
	}
	for _, name := range plan.Unset {
		delete(env, name)
	}
	for name, value := range plan.Env {
		env[name] = value
	}
	env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	env["DISABLE_TELEMETRY"] = "1"
	env["DISABLE_ERROR_REPORTING"] = "1"
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("Synthetic Synara acceptance project.\n"+readSentinel+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("Git is required for Synara checkpoint acceptance")
	}
	for _, args := range [][]string{{"init", "--quiet"}, {"add", "README.md"}, {"-c", "user.name=Kilo Fixture", "-c", "user.email=kilo-fixture@invalid", "commit", "--quiet", "-m", "Synthetic fixture baseline"}} {
		gitCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		command := exec.CommandContext(gitCtx, git, args...)
		command.Dir = project
		command.Env = cgClientEnv(project)
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("cannot prepare temporary Synara project: %v %s", err, output)
		}
	}
	libraryIDs := make([]string, 0, len(library.Models))
	for _, model := range library.Models {
		libraryIDs = append(libraryIDs, model.ID)
	}
	fixture := map[string]any{
		"binary": plan.Executable, "entry": filepath.Join(installed, "Contents", "Resources", "app.asar", "apps", "server", "dist", "index.mjs"),
		"version": version,
		"baseDir": paths.Data, "home": paths.UIHome, "project": project, "env": env, "expectedResponse": responseText,
		"verifyModelPicker": os.Getenv("KILO_TEST_SYNARA_PICKER") == "1", "selectionPath": paths.Selection, "settingsPath": paths.Settings,
		"composerSeedPath":   filepath.Join(paths.Electron, synaraComposerSeedName),
		"artifactsDirectory": os.Getenv("KILO_TEST_SYNARA_ARTIFACTS"),
		"libraryModels":      libraryIDs, "defaultModel": library.DefaultModel,
		"defaultDisplayName": "Synthetic Gateway Opus 5.5",
		"instances": []map[string]any{
			{"id": synaraCodexNormalID, "model": codexModel, "options": map[string]any{"reasoningEffort": "high"}},
			{"id": synaraCodexProxyID, "model": codexModel, "options": map[string]any{"reasoningEffort": "high"}},
			{"id": synaraClaudeNormalID, "model": claudeModel},
			{"id": synaraClaudeProxyID, "model": claudeModel, "options": nil, "alternateModel": claudeLowModel},
			{"id": synaraCodexProxyID, "model": chatgptModel, "options": map[string]any{"reasoningEffort": "high"}},
		},
	}
	if saved.Runtime == nil {
		t.Fatal("installed acceptance did not prepare its private account-aware runtime")
	}
	fixture["backendHook"] = filepath.Join(saved.Runtime.Root, synaraRuntimeHookName)
	data, _ := json.Marshal(fixture)
	manifest := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "scripts/smoke_synara.mjs", "--manifest", manifest)
	command.Env = cgClientEnv(t.TempDir())
	if os.Getenv("KILO_TEST_SYNARA_PICKER") == "1" {
		browsers := os.Getenv("PLAYWRIGHT_BROWSERS_PATH")
		if browsers == "" {
			browsers = filepath.Join(realHome, "Library", "Caches", "ms-playwright")
		}
		command.Env = append(command.Env, "PLAYWRIGHT_BROWSERS_PATH="+browsers)
	}
	command.WaitDelay = 3 * time.Second
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Installed Synara four-agent acceptance passed") {
		t.Fatalf("installed Synara acceptance failed: %v\n%s", err, output)
	}
	if os.Getenv("KILO_TEST_SYNARA_DESKTOP") == "1" {
		synaraInstalledDesktopStartup(t, plan, paths, env, version)
	}
	mu.Lock()
	for _, id := range []string{synaraCodexNormalID, synaraCodexProxyID, synaraClaudeNormalID, synaraClaudeProxyID, synaraCodexProxyID + ":chatgpt"} {
		if counts[id] < 3 {
			t.Errorf("%s did not reach its isolated upstream across all three turns: %d", id, counts[id])
		}
	}
	for _, id := range []string{synaraCodexProxyID, synaraClaudeProxyID} {
		if !toolResults[id] {
			t.Errorf("%s did not complete its safe file-read roundtrip", id)
		}
	}
	if os.Getenv("KILO_TEST_SYNARA_PICKER") == "1" && pickerRequests != 1 {
		t.Errorf("Opus 5.5 exact gateway requests = %d, want one exact shared-default UI turn", pickerRequests)
	}
	mu.Unlock()
	for path, before := range normalFiles {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, before) {
			t.Errorf("normal provider configuration changed: %s (%v)", path, err)
		}
	}
	if metadata := adminRequest(a, "clients/synara", ""); metadata.Code != 200 || !strings.Contains(metadata.Body.String(), `"prepared":true`) {
		t.Fatalf("Synara settings roundtrip invalidated prepared state: %d %s", metadata.Code, metadata.Body.String())
	}
	t.Log(strings.TrimSpace(string(output)))
}

// Synara's real teardown verifies owned descendants with ps. A denied host
// process-observation permission prevents this acceptance check from proving
// cleanup; it is not a reason to relax the shipped process-tree guard.
func synaraInstalledProcessObservationPreflight(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/ps", "-eo", "pid=,ppid=,lstart=,command=")
	command.Env = append(cgClientEnv(t.TempDir()), "LC_ALL=C")
	_, err := command.Output() // Discard the process inventory; never log it.
	if err == nil {
		return
	}
	var exitError *exec.ExitError
	diagnostic := strings.ToLower(err.Error())
	if errors.As(err, &exitError) {
		diagnostic += " " + strings.ToLower(string(exitError.Stderr))
	}
	if strings.Contains(diagnostic, "operation not permitted") || strings.Contains(diagnostic, "permission denied") {
		t.Skip("installed Synara acceptance requires host process-observation permission; ps is denied by the sandbox")
	}
	t.Fatalf("installed Synara process-observation preflight failed: %v", err)
}

func synaraInstalledRequestContainsText(messages []any, expected string) bool {
	for _, raw := range messages {
		message := object(raw)
		if message["role"] != "user" {
			continue
		}
		if text, ok := message["content"].(string); ok && strings.Contains(text, expected) {
			return true
		}
		content, _ := message["content"].([]any)
		for _, block := range content {
			if part := object(block); part["type"] == "text" && strings.Contains(stringValue(part["text"]), expected) {
				return true
			}
		}
	}
	return false
}

// This optional check launches a second, isolated Electron process with the
// production plan. It exercises real IPC snapshot import and graceful shutdown,
// confirming an optional quit dialog in this owned window. It does not perform
// native GUI inference.
func synaraInstalledDesktopStartup(t *testing.T, plan clientLaunchPlan, paths synaraManagedPaths, env map[string]string, version string) {
	t.Helper()
	// cgClientEnv normally uses the fixture HOME as TMPDIR. Native Synara
	// creates Unix sockets there, whose path limit is shorter than Go's test
	// directory on macOS. Use a short, owned temp directory for this fixture.
	tmp, err := os.MkdirTemp("/private/tmp", "ks-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	diagnostics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer diagnostics.Close()
	seed := filepath.Join(paths.Electron, synaraComposerSeedName)
	if _, err := os.Stat(seed); err != nil {
		t.Fatalf("native Electron smoke requires the unconsumed Prepare snapshot: %v", err)
	}
	args := append([]string{}, plan.Args...)
	args = append(args, "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0")
	command := exec.Command(plan.Executable, args...)
	command.Dir = paths.UIHome
	for name, value := range env {
		if name != "SYNARA_BETA_DIAGNOSTICS_URL" && name != "TMPDIR" {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	command.Env = append(command.Env, "SYNARA_BETA_DIAGNOSTICS_URL="+diagnostics.URL, "TMPDIR="+tmp)
	var output synaraSmokeOutput
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	runtimePath := filepath.Join(paths.Data, "userdata", "server-runtime.json")
	logPath := filepath.Join(paths.Data, "userdata", "logs", "desktop-main.log")
	backendPID, closed := 0, false
	defer func() {
		if artifacts := os.Getenv("KILO_TEST_SYNARA_ARTIFACTS"); filepath.IsAbs(artifacts) {
			log, readErr := os.ReadFile(logPath)
			if readErr == nil {
				path := filepath.Join(artifacts, version, "electron-startup.log")
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Error(err)
				} else if err := os.WriteFile(path, log, 0600); err != nil {
					t.Error(err)
				} else {
					t.Logf("Owned Electron startup log: %s", path)
				}
			}
		}
	}()
	defer func() {
		if !closed {
			_ = command.Process.Signal(syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(20 * time.Second):
				_ = command.Process.Kill()
				<-exited
			}
		}
		if backendPID > 0 {
			// This PID was read only from our private runtime file, never from
			// the normal Synara instance or an installed-app process search.
			process, _ := os.FindProcess(backendPID)
			waitStopped := func(timeout time.Duration) bool {
				deadline := time.Now().Add(timeout)
				for process != nil && process.Signal(syscall.Signal(0)) == nil && time.Now().Before(deadline) {
					time.Sleep(50 * time.Millisecond)
				}
				return process == nil || process.Signal(syscall.Signal(0)) != nil
			}
			if !waitStopped(5 * time.Second) {
				_ = process.Signal(syscall.SIGTERM)
				if !waitStopped(5 * time.Second) {
					_ = process.Kill()
					if !waitStopped(5 * time.Second) {
						t.Errorf("owned Electron backend %d did not terminate after forced cleanup", backendPID)
					}
				}
				t.Errorf("owned Electron backend %d remained after graceful quit", backendPID)
			}
		}
	}()
	ready := false
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			closed = true
			t.Fatalf("owned Electron exited before snapshot import: %v\n%s", err, output.String())
		default:
		}
		var state struct {
			PID  int    `json:"pid"`
			Host string `json:"host"`
		}
		data, err := os.ReadFile(runtimePath)
		if err == nil && json.Unmarshal(data, &state) == nil && state.PID > 0 {
			backendPID = state.PID
			if state.Host != "127.0.0.1" || state.PID == command.Process.Pid {
				t.Fatalf("native Synara backend did not use its own loopback runtime: %s", state.Host)
			}
			log, _ := os.ReadFile(logPath)
			_, seedErr := os.Stat(seed)
			if errors.Is(seedErr, os.ErrNotExist) && bytes.Contains(log, []byte("bootstrap main window created")) {
				ready = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		log, _ := os.ReadFile(logPath)
		t.Fatalf("native Electron did not import its prepared snapshot: %s\n%s", log, output.String())
	}
	endpointPattern := regexp.MustCompile(`DevTools listening on (ws://127\.0\.0\.1:[0-9]+/devtools/browser/[\w-]+)`)
	match := endpointPattern.FindStringSubmatch(output.String())
	if len(match) != 2 {
		t.Fatalf("owned Electron did not expose its temporary loopback test endpoint: %s", output.String())
	}
	quitReady := filepath.Join(tmp, "quit-helper-ready")
	fixture := map[string]string{"endpoint": match[1], "readyFile": quitReady}
	if artifacts := os.Getenv("KILO_TEST_SYNARA_ARTIFACTS"); filepath.IsAbs(artifacts) {
		directory := filepath.Join(artifacts, version)
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		fixture["screenshot"] = filepath.Join(directory, "electron-quit-confirmation.png")
	}
	encoded, _ := json.Marshal(fixture)
	quitFixture := filepath.Join(tmp, "quit-helper.json")
	if err := os.WriteFile(quitFixture, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	helper := exec.Command("node", "scripts/confirm_synara_desktop_quit.mjs", quitFixture)
	var helperOutput synaraSmokeOutput
	helper.Stdout, helper.Stderr = &helperOutput, &helperOutput
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	helperExited := make(chan error, 1)
	go func() { helperExited <- helper.Wait() }()
	helperClosed := false
	defer func() {
		if !helperClosed {
			_ = helper.Process.Kill()
			<-helperExited
		}
	}()
	deadline = time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(quitReady); err == nil {
			break
		}
		select {
		case err := <-helperExited:
			helperClosed = true
			t.Fatalf("owned Electron quit helper did not connect: %v\n%s", err, helperOutput.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("owned Electron quit helper was not ready: %s", helperOutput.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exited:
		closed = true
		if err != nil {
			t.Fatalf("owned Electron did not quit cleanly: %v\n%s", err, output.String())
		}
	case <-time.After(20 * time.Second):
		log, _ := os.ReadFile(logPath)
		t.Fatalf("owned Electron did not drain and quit: %s\n%s\n%s", log, output.String(), helperOutput.String())
	}
	select {
	case err := <-helperExited:
		helperClosed = true
		if err != nil {
			t.Fatalf("owned Electron quit helper failed: %v\n%s", err, helperOutput.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("owned Electron quit helper did not finish: %s", helperOutput.String())
	}
	if strings.Contains(helperOutput.String(), "quit confirmation clicked") {
		t.Log("Installed Synara's owned window confirmed its native quit dialog")
	}
	if _, err := os.Stat(runtimePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned Electron runtime was not cleared: %v", err)
	}
	log, _ := os.ReadFile(logPath)
	// On macOS Electron can route SIGTERM through its native before-quit event
	// before Node's signal handler runs. Both paths use the same runtime drain.
	graceful := false
	for _, reason := range []string{"SIGTERM", "before-quit"} {
		graceful = graceful || bytes.Contains(log, []byte(reason+" shutdown start")) && bytes.Contains(log, []byte(reason+" shutdown complete"))
	}
	if !graceful {
		t.Fatalf("owned Electron did not complete graceful runtime shutdown after SIGTERM: %s", log)
	}
	t.Log("Installed Synara Electron startup passed: private loopback backend, native snapshot IPC consumed, window bootstrap, SIGTERM exit 0 and runtime cleared")
}

type synaraSmokeOutput struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (output *synaraSmokeOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.data.Write(data)
}

func (output *synaraSmokeOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.data.String()
}

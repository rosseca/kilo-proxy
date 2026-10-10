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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Opt in to the installed T3 server and native CLIs. Every provider home is a
// disposable fixture, and every inference upstream is synthetic localhost.
func TestT3CodeInstalledFourAgents(t *testing.T) {
	installed := os.Getenv("KILO_TEST_T3_APP")
	if installed == "" {
		t.Skip("set KILO_TEST_T3_APP to an installed T3 desktop application")
	}
	if runtime.GOOS != "darwin" || !filepath.IsAbs(installed) {
		t.Fatal("the installed T3 acceptance currently requires an absolute macOS app bundle")
	}
	if err := t3CodeCompatibility(installed, runtime.GOOS); err != nil {
		t.Fatal(err)
	}
	installation, err := inspectT3CodeInstallation(installed, runtime.GOOS)
	version := installation.Version
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
		t.Fatal("Node is required for the installed T3 acceptance check")
	}
	a := launchTestApp(t)
	a.config.Language = "en"
	a.config.LocalKey = "synthetic-t3-local"
	a.chatgpt.creds = chatGPTCredentials{Access: "synthetic-t3-chatgpt-access", Refresh: "synthetic-refresh", Account: "synthetic-t3-account", Expires: time.Now().Add(time.Hour).Unix()}
	a.chatgpt.state = chatGPTState{Status: "idle"}
	a.launcher.platform = runtime.GOOS
	a.launcher.resolve = func(id, custom string) (string, error) {
		if custom != "" {
			return "", errors.New("unexpected custom client in T3 fixture")
		}
		switch id {
		case "t3-code":
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
		return errors.New("desktop launch disabled in T3 fixture")
	}
	a.t3CodeCheckRunning = func(string) (bool, error) { return false, nil }
	// Make prepare's environment snapshot synthetic too: its real HOME is never
	// passed to a provider, including machines that export custom XDG directories.
	for _, name := range []string{"USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(name, filepath.Join(a.launcher.home, name))
	}
	t.Setenv("CODEX_HOME", filepath.Join(a.launcher.home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(a.launcher.home, ".claude"))
	const codexModel = "vendor/t3-codex"
	const claudeModel = "anthropic/claude-opus-4-6"
	const claudePickerModel = "anthropic/claude-opus-5.5"
	const claudeLowModel = "anthropic/claude-sonnet-4-6"
	const chatgptModel = "chatgpt/gpt-synthetic-high"
	const responseText = "SYNTHETIC_T3_OK"
	// Put the shared default after index zero so the picker acceptance can prove
	// that a stale built-in selection falls back to our configured default.
	library := modelLibrary{SchemaVersion: 1, DefaultModel: claudePickerModel, Models: []modelLibraryItem{
		{ID: codexModel, ReasoningEffort: "high", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, ContextWindow: 128000, MaxOutputTokens: 4096},
		// This gateway ID reproduces the reported 5.5 picker mismatch. It has
		// no invented reasoning capabilities or effort default in the fixture.
		{ID: claudePickerModel, ContextWindow: 128000, MaxOutputTokens: 4096},
		{ID: claudeModel, ReasoningEffort: "high", ReasoningCustom: true, ReasoningLevels: []string{"low", "medium", "high"}, ContextWindow: 128000, MaxOutputTokens: 4096},
		{ID: chatgptModel, ReasoningEffort: "high", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, ContextWindow: 128000, MaxOutputTokens: 4096},
	}}
	catalog := []modelInfo{
		{ID: codexModel, Name: "Synthetic Codex", ContextWindow: 128000, MaxOutputTokens: 4096, ReasoningEfforts: []string{"low", "high"}},
		{ID: claudePickerModel, Name: "Synthetic Gateway Opus 5.5", ContextWindow: 128000, MaxOutputTokens: 4096},
		{ID: claudeModel, Name: "Synthetic Claude", ContextWindow: 128000, MaxOutputTokens: 4096, ReasoningEfforts: []string{"low", "medium", "high"}},
		{ID: chatgptModel, Name: "Synthetic ChatGPT", ContextWindow: 128000, MaxOutputTokens: 4096, ReasoningEfforts: []string{"low", "high"}},
	}
	if installation.ProtocolV2 {
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
	readSentinel := fmt.Sprintf("SYNTHETIC_T3_READ_%x", randomSentinel)
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
			t.Error("invalid JSON from an installed T3 provider")
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
		number := count(route)
		messageID := fmt.Sprintf("msg_t3_%s_%d", strings.ReplaceAll(route, ":", "_"), number)
		callID := "call_t3_read_" + route
		if route == t3CodeCodexProxyID || route == t3CodeClaudeProxyID {
			if number == 1 {
				if route == t3CodeCodexProxyID {
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
				seen := t3CodeFixtureReadResult(body, callID, readSentinel, route == t3CodeCodexProxyID)
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
			respond(w, r, body, t3CodeCodexProxyID)
		case "/api/gateway/messages", "/api/gateway/messages/count_tokens":
			if body["model"] == claudePickerModel {
				// Routing and auth are asserted for 5.5; effort is deliberately
				// unspecified, unlike the separate known High/Low regressions.
				if r.URL.Path == "/api/gateway/messages" {
					mu.Lock()
					pickerRequests++
					mu.Unlock()
				}
				respond(w, r, body, t3CodeClaudeProxyID)
				return
			}
			expectedEffort := "high"
			expectedModel := claudeModel
			mu.Lock()
			previousRequests := counts[t3CodeClaudeProxyID]
			mu.Unlock()
			if installation.ProtocolV2 && previousRequests == 2 {
				expectedEffort = "low"
				expectedModel = claudeLowModel
			}
			if body["model"] != expectedModel {
				t.Errorf("Kilo Claude lost exact model: expected %s, got %v", expectedModel, body["model"])
			}
			if r.URL.Path == "/api/gateway/messages" {
				if object(body["output_config"])["effort"] != expectedEffort {
					t.Errorf("Kilo Claude reasoning mismatch at request %d: expected %s, output_config=%v thinking=%v", previousRequests+1, expectedEffort, body["output_config"], body["thinking"])
				}
			}
			respond(w, r, body, t3CodeClaudeProxyID)
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
			respond(w, r, body, t3CodeCodexNormalID)
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
			respond(w, r, body, t3CodeClaudeNormalID)
		}
	}))
	defer normalClaude.Close()
	chatgpt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" || r.Header.Get("Authorization") != "Bearer synthetic-t3-chatgpt-access" || r.Header.Get("ChatGPT-Account-Id") != "synthetic-t3-account" || r.Header.Get("X-KiloCode-OrganizationId") != "" {
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
		respond(w, r, body, t3CodeCodexProxyID+":chatgpt")
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
	prepareT3CodeFixture(t, a)
	plan, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime())
	if err != nil {
		t.Fatal(err)
	}
	paths := t3CodePaths(a.dir)
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
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("Synthetic T3 acceptance project.\n"+readSentinel+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("Git is required for T3 checkpoint acceptance")
	}
	for _, args := range [][]string{{"init", "--quiet"}, {"add", "README.md"}, {"-c", "user.name=Kilo Fixture", "-c", "user.email=kilo-fixture@invalid", "commit", "--quiet", "-m", "Synthetic fixture baseline"}} {
		gitCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		command := exec.CommandContext(gitCtx, git, args...)
		command.Dir = project
		command.Env = cgClientEnv(project)
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("cannot prepare temporary T3 project: %v %s", err, output)
		}
	}
	claudeOptions := map[string]any{"effort": "high"}
	if installation.ProtocolV2 {
		claudeOptions = nil
	}
	libraryIDs := make([]string, 0, len(library.Models))
	for _, model := range library.Models {
		libraryIDs = append(libraryIDs, model.ID)
	}
	fixture := map[string]any{
		"binary": plan.Executable, "entry": filepath.Join(installed, "Contents", "Resources", "app.asar", "apps", "server", "dist", "bin.mjs"),
		"version": version, "protocolV2": installation.ProtocolV2,
		"baseDir": paths.Data, "home": paths.UIHome, "project": project, "env": env, "expectedResponse": responseText,
		"verifyModelPicker": os.Getenv("KILO_TEST_T3_PICKER") == "1", "clientSettingsPath": paths.ClientSettings,
		"artifactsDirectory": os.Getenv("KILO_TEST_T3_ARTIFACTS"),
		"libraryModels":      libraryIDs, "defaultModel": library.DefaultModel,
		"instances": []map[string]any{
			{"id": t3CodeCodexNormalID, "model": codexModel, "options": map[string]any{"reasoningEffort": "high"}},
			{"id": t3CodeCodexProxyID, "model": codexModel, "options": map[string]any{"reasoningEffort": "high"}},
			{"id": t3CodeClaudeNormalID, "model": claudeModel},
			{"id": t3CodeClaudeProxyID, "model": claudeModel, "options": claudeOptions, "alternateModel": claudeLowModel},
			{"id": t3CodeCodexProxyID, "model": chatgptModel, "options": map[string]any{"reasoningEffort": "high"}},
		},
	}
	data, _ := json.Marshal(fixture)
	manifest := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "scripts/smoke_t3_code.mjs", "--manifest", manifest)
	command.Env = cgClientEnv(t.TempDir())
	if os.Getenv("KILO_TEST_T3_PICKER") == "1" {
		browsers := os.Getenv("PLAYWRIGHT_BROWSERS_PATH")
		if browsers == "" {
			browsers = filepath.Join(realHome, "Library", "Caches", "ms-playwright")
		}
		command.Env = append(command.Env, "PLAYWRIGHT_BROWSERS_PATH="+browsers)
	}
	command.WaitDelay = 3 * time.Second
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Installed T3 four-agent acceptance passed") {
		t.Fatalf("installed T3 acceptance failed: %v\n%s", err, output)
	}
	mu.Lock()
	for _, id := range []string{t3CodeCodexNormalID, t3CodeCodexProxyID, t3CodeClaudeNormalID, t3CodeClaudeProxyID, t3CodeCodexProxyID + ":chatgpt"} {
		if counts[id] < 3 {
			t.Errorf("%s did not reach its isolated upstream across all three turns: %d", id, counts[id])
		}
	}
	for _, id := range []string{t3CodeCodexProxyID, t3CodeClaudeProxyID} {
		if !toolResults[id] {
			t.Errorf("%s did not complete its safe file-read roundtrip", id)
		}
	}
	if os.Getenv("KILO_TEST_T3_PICKER") == "1" && pickerRequests != 5 {
		t.Errorf("Opus 5.5 exact gateway requests = %d, want history + three UI continuations + promoted draft (5)", pickerRequests)
	}
	mu.Unlock()
	for path, before := range normalFiles {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, before) {
			t.Errorf("normal provider configuration changed: %s (%v)", path, err)
		}
	}
	if metadata := adminRequest(a, "clients/t3-code", ""); metadata.Code != 200 || !strings.Contains(metadata.Body.String(), `"prepared":true`) {
		t.Fatalf("T3 settings roundtrip invalidated prepared state: %d %s", metadata.Code, metadata.Body.String())
	}
	t.Log(strings.TrimSpace(string(output)))
}

func t3CodeFixtureNormalProfiles(t *testing.T, home string, library modelLibrary, catalog []modelInfo, codexURL, claudeURL string) map[string][]byte {
	t.Helper()
	choices := terminalLibraryChoices(library, catalog)
	models, err := buildCodexCatalog(choices, library.DefaultModel, false)
	if err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(home, ".codex")
	claudeHome := filepath.Join(home, ".claude")
	for _, dir := range []string{codexHome, claudeHome} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	config, err := toml.Marshal(map[string]any{
		"model": library.DefaultModel, "model_provider": "normal-synthetic", "model_catalog_json": filepath.Join(codexHome, "models.json"),
		"model_reasoning_effort": "high", "model_providers": map[string]any{"normal-synthetic": map[string]any{"name": "Normal fixture", "base_url": codexURL + "/v1", "wire_api": "responses", "requires_openai_auth": false}},
	})
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := json.Marshal(map[string]any{"env": map[string]string{"ANTHROPIC_BASE_URL": claudeURL, "ANTHROPIC_AUTH_TOKEN": "synthetic-t3-normal-claude", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}})
	files := map[string][]byte{
		filepath.Join(codexHome, "models.json"):    models,
		filepath.Join(codexHome, "config.toml"):    config,
		filepath.Join(claudeHome, "settings.json"): settings,
	}
	for path, data := range files {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func t3CodeFixtureMessagesSSE(w http.ResponseWriter, model, answer, messageID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(kind string, value map[string]any) {
		value["type"] = kind
		data, _ := json.Marshal(value)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	send("message_start", map[string]any{"message": map[string]any{"id": messageID, "type": "message", "role": "assistant", "model": model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 20, "output_tokens": 0}}})
	send("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
	send("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": answer}})
	send("content_block_stop", map[string]any{"index": 0})
	send("message_delta", map[string]any{"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 8}})
	send("message_stop", map[string]any{})
}

func t3CodeFixtureCodexReadTool(body map[string]any, path string) (string, string) {
	tools, _ := body["tools"].([]any)
	command := "cat " + helperShellQuote(path)
	for _, raw := range tools {
		tool := object(raw)
		name, _ := tool["name"].(string)
		var arguments map[string]any
		switch name {
		case "exec_command":
			arguments = map[string]any{"cmd": command, "max_output_tokens": 1000}
		case "shell_command":
			arguments = map[string]any{"command": command}
		case "shell":
			arguments = map[string]any{"command": []string{"/bin/cat", path}}
		default:
			continue
		}
		data, _ := json.Marshal(arguments)
		return name, string(data)
	}
	return "", ""
}

func t3CodeFixtureReadResult(body map[string]any, callID, sentinel string, responses bool) bool {
	if responses {
		input, _ := body["input"].([]any)
		for _, raw := range input {
			item := object(raw)
			if item["type"] == "function_call_output" && item["call_id"] == callID {
				data, _ := json.Marshal(item["output"])
				if bytes.Contains(data, []byte(sentinel)) {
					return true
				}
			}
		}
		return false
	}
	messages, _ := body["messages"].([]any)
	for _, raw := range messages {
		content, _ := object(raw)["content"].([]any)
		for _, block := range content {
			item := object(block)
			if item["type"] == "tool_result" && item["tool_use_id"] == callID {
				data, _ := json.Marshal(item["content"])
				if bytes.Contains(data, []byte(sentinel)) && item["is_error"] != true {
					return true
				}
			}
		}
	}
	return false
}

func t3CodeFixtureResponseSSE(w http.ResponseWriter, model string, item map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(kind string, value map[string]any) {
		value["type"] = kind
		data, _ := json.Marshal(value)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	response := map[string]any{"id": "resp_" + stringValue(item["id"]), "object": "response", "model": model, "status": "in_progress", "output": []any{}}
	send("response.created", map[string]any{"response": response})
	added := map[string]any{}
	for key, value := range item {
		added[key] = value
	}
	added["status"] = "in_progress"
	if item["type"] == "function_call" {
		added["arguments"] = ""
	} else {
		added["content"] = []any{}
	}
	send("response.output_item.added", map[string]any{"output_index": 0, "item": added})
	if item["type"] == "function_call" {
		send("response.function_call_arguments.delta", map[string]any{"output_index": 0, "item_id": item["id"], "delta": item["arguments"]})
		send("response.function_call_arguments.done", map[string]any{"output_index": 0, "item_id": item["id"], "arguments": item["arguments"]})
	} else {
		part := object(item["content"].([]any)[0])
		send("response.content_part.added", map[string]any{"output_index": 0, "content_index": 0, "item_id": item["id"], "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
		send("response.output_text.delta", map[string]any{"output_index": 0, "content_index": 0, "item_id": item["id"], "delta": part["text"]})
		send("response.output_text.done", map[string]any{"output_index": 0, "content_index": 0, "item_id": item["id"], "text": part["text"]})
		send("response.content_part.done", map[string]any{"output_index": 0, "content_index": 0, "item_id": item["id"], "part": part})
	}
	send("response.output_item.done", map[string]any{"output_index": 0, "item": item})
	response["status"] = "completed"
	response["output"] = []any{item}
	response["usage"] = map[string]any{"input_tokens": 20, "output_tokens": 8, "total_tokens": 28}
	send("response.completed", map[string]any{"response": response})
}

func t3CodeFixtureMessagesToolSSE(w http.ResponseWriter, model, messageID, callID, name string, input map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(kind string, value map[string]any) {
		value["type"] = kind
		data, _ := json.Marshal(value)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	send("message_start", map[string]any{"message": map[string]any{"id": messageID, "type": "message", "role": "assistant", "model": model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 20, "output_tokens": 0}}})
	send("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "tool_use", "id": callID, "name": name, "input": map[string]any{}}})
	data, _ := json.Marshal(input)
	send("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(data)}})
	send("content_block_stop", map[string]any{"index": 0})
	send("message_delta", map[string]any{"delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 8}})
	send("message_stop", map[string]any{})
}

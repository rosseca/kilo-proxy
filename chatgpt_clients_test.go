package main

// Opt-in interoperability checks against locally installed, unmodified CLIs.
// No inference leaves localhost: a synthetic subscription server emits a single
// read-only tool call, then verifies that the client returned the file contents.
// Run: KILO_CHATGPT_CLIENTS=claude,opencode,omp,codex go test -run TestChatGPTInstalledClients -v
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// This test intentionally consumes the explicitly connected subscription. It
// never reads a user profile or discovers credentials; every opt-in value must
// be supplied by the caller, and the target must be a loopback Kilo Proxy.
func TestChatGPTLiveClients(t *testing.T) {
	selected := os.Getenv("KILO_CHATGPT_LIVE_CLIENTS")
	if selected == "" {
		t.Skip("requires explicit live-client opt-in and a local preview proxy")
	}
	base, key, model := os.Getenv("KILO_CHATGPT_LIVE_URL"), os.Getenv("KILO_CHATGPT_LIVE_KEY"), os.Getenv("KILO_CHATGPT_LIVE_MODEL")
	u, err := url.Parse(base)
	if err != nil || u == nil {
		t.Fatal("invalid live proxy URL")
	}
	loopback := u.Hostname() == "localhost"
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if !loopback || (u.Scheme != "http" && u.Scheme != "https") || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		t.Fatal("live proxy URL must be a loopback HTTP(S) origin with an explicit port")
	}
	if key == "" {
		t.Fatal("KILO_CHATGPT_LIVE_KEY is required")
	}
	if !strings.HasPrefix(model, "chatgpt/") || !catalogID.MatchString(model) || strings.Count(model, "/") != 1 {
		t.Fatal("KILO_CHATGPT_LIVE_MODEL must select the subscription namespace")
	}
	base = strings.TrimRight(base, "/")
	for _, client := range strings.Split(selected, ",") {
		t.Run(client, func(t *testing.T) {
			if client != "claude" && client != "claude-full" && client != "opencode" && client != "omp" && client != "codex" {
				t.Fatal("unsupported live client")
			}
			binaryName := client
			if client == "claude-full" {
				binaryName = "claude"
			}
			binary, err := exec.LookPath(binaryName)
			if err != nil {
				t.Fatal("requested CLI is not installed")
			}
			root := t.TempDir()
			if canonical, err := filepath.EvalSymlinks(root); err == nil {
				root = canonical
			}
			project := filepath.Join(root, "project")
			if err := os.MkdirAll(project, 0700); err != nil {
				t.Fatal(err)
			}
			sentinel := fmt.Sprintf("KILO_LIVE_TOOL_RESULT_%x", time.Now().UnixNano())
			if err := os.WriteFile(filepath.Join(project, "sentinel.txt"), []byte(sentinel), 0600); err != nil {
				t.Fatal(err)
			}
			prompt := "Read ONLY sentinel.txt in the current directory using your read tool (or a read-only cat command). Reply with only its exact contents. Do not inspect other files, use the network, delegate, or modify anything."
			args, extra := cgClientArgsForModel(t, client, root, base, key, prompt, model, "low")
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, args...)
			command.Dir = project
			command.Env = append(cgClientEnv(root), extra...)
			command.WaitDelay = 2 * time.Second
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			runErr := command.Run()
			answer := cgClientFinalText(client, stdout.Bytes())
			if runErr != nil || !strings.Contains(answer, sentinel) {
				// Local keys may appear in a client's own diagnostic output. Never
				// print raw command environments, configuration, URLs or responses.
				diagnostic := strings.ReplaceAll(stderr.String(), key, "[redacted]")
				if len(diagnostic) > 1800 {
					diagnostic = diagnostic[len(diagnostic)-1800:]
				}
				t.Fatalf("live %s failed: process=%v finalSentinel=%t stdoutBytes=%d stderr=%s", client, runErr, strings.Contains(answer, sentinel), stdout.Len(), diagnostic)
			}
			t.Logf("%s: live subscription reply contains the file's unpredictable sentinel", client)
		})
	}
}

func cgClientEnv(root string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "USER=kilo-test", "LOGNAME=kilo-test", "TMPDIR=" + root, "TERM=dumb", "NO_COLOR=1", "CI=1", "HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "ALL_PROXY=http://127.0.0.1:9", "NO_PROXY=127.0.0.1,localhost,::1", "http_proxy=http://127.0.0.1:9", "https_proxy=http://127.0.0.1:9", "no_proxy=127.0.0.1,localhost,::1"}
	for _, name := range []string{"SYSTEMROOT", "WINDIR", "PATHEXT"} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		env = append(env, name+"="+filepath.Join(root, name))
	}
	return env
}

// Inspect only final assistant text, not tool output that would create a false
// positive if the model never received or processed the file-read result.
func cgClientFinalText(client string, output []byte) string {
	var final string
	consume := func(event map[string]any) {
		switch client {
		case "claude", "claude-full":
			if event["type"] == "result" {
				final = stringValue(event["result"])
			}
		case "opencode":
			if event["type"] == "text" {
				final += stringValue(object(event["part"])["text"])
			}
		case "codex":
			if event["type"] == "item.completed" {
				item := object(event["item"])
				if item["type"] == "agent_message" {
					final = stringValue(item["text"])
				}
			}
		case "omp":
			if event["type"] == "message_end" {
				message := object(event["message"])
				if message["role"] == "assistant" {
					parts, _ := message["content"].([]any)
					text := ""
					for _, raw := range parts {
						part := object(raw)
						if part["type"] == "text" {
							text += stringValue(part["text"])
						}
					}
					if text != "" {
						final = text
					}
				}
			}
		}
	}
	if event, err := decodeObject(output); err == nil {
		consume(event)
		return final
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), bridgeLimit)
	for scanner.Scan() {
		if event, err := decodeObject(scanner.Bytes()); err == nil {
			consume(event)
		}
	}
	return final
}

func TestChatGPTClientFinalTextExcludesToolOutput(t *testing.T) {
	for client, body := range map[string]string{
		"claude":   `{"type":"result","result":"sentinel"}`,
		"opencode": `{"type":"text","part":{"text":"sentinel"}}`,
		"omp":      `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"sentinel"}]}}`,
		"codex":    `{"type":"item.completed","item":{"type":"agent_message","text":"sentinel"}}`,
	} {
		if got := cgClientFinalText(client, []byte(body)); got != "sentinel" {
			t.Errorf("%s: %q", client, got)
		}
		if got := cgClientFinalText(client, []byte(`{"type":"tool_result","content":"sentinel"}`)); got != "" {
			t.Errorf("%s accepted tool output", client)
		}
	}
}

func TestChatGPTInstalledClients(t *testing.T) {
	selected := os.Getenv("KILO_CHATGPT_CLIENTS")
	if selected == "" {
		t.Skip("opt-in: exercises installed CLI clients with temporary profiles and synthetic upstream")
	}
	for _, client := range strings.Split(selected, ",") {
		t.Run(client, func(t *testing.T) {
			binaryName := client
			if client == "claude-full" {
				binaryName = "claude"
			}
			binary, err := exec.LookPath(binaryName)
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			if canonical, err := filepath.EvalSymlinks(root); err == nil {
				root = canonical
			}
			project := filepath.Join(root, "project")
			if err := os.MkdirAll(project, 0700); err != nil {
				t.Fatal(err)
			}
			sentinel := "KILO_SUBSCRIPTION_TOOL_ROUNDTRIP_7d692a"
			sentinelFile := filepath.Join(project, "sentinel.txt")
			if err := os.WriteFile(sentinelFile, []byte(sentinel), 0600); err != nil {
				t.Fatal(err)
			}
			a := subscriptionTestApp(t)
			var mu sync.Mutex
			var incoming, upstreamRequests []map[string]any
			var paths []string
			var sawResult bool
			var mockError string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				var request map[string]any
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					http.Error(w, "invalid JSON", 400)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				upstreamRequests = append(upstreamRequests, request)
				if len(upstreamRequests) > 12 {
					mockError = "client failed to complete within 12 requests"
					http.Error(w, mockError, 400)
					return
				}
				input, _ := request["input"].([]any)
				for _, raw := range input {
					item := object(raw)
					data, _ := json.Marshal(item["output"])
					if item["type"] == "function_call_output" && strings.Contains(string(data), sentinel) {
						sawResult = true
					}
				}
				var item map[string]any
				tools, _ := request["tools"].([]any)
				if sawResult || len(tools) == 0 {
					item = map[string]any{"id": "msg_result", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "VERIFIED " + sentinel, "annotations": []any{}}}}
				} else {
					name, args, err := cgClientReadTool(request, sentinelFile)
					if err != nil {
						mockError = err.Error()
						http.Error(w, mockError, 400)
						return
					}
					arguments, _ := json.Marshal(args)
					item = map[string]any{"id": "fc_read", "type": "function_call", "call_id": "call_read_sentinel", "name": name, "arguments": string(arguments), "status": "completed"}
				}
				cgClientResponse(w, item)
			}))
			defer upstream.Close()
			a.chatGPTResponsesURL = upstream.URL
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			proxy := httptest.NewUnstartedServer(nil)
			proxy.Listener.Close()
			proxy.Listener = listener
			gateway := a.inferenceHandler("", "", a.config.LocalKey, listener.Addr().String())
			proxy.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				r.Body.Close()
				r.Body = io.NopCloser(bytes.NewReader(body))
				var d map[string]any
				json.Unmarshal(body, &d)
				mu.Lock()
				incoming = append(incoming, d)
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				gateway.ServeHTTP(w, r)
			})
			proxy.Start()
			defer proxy.Close()
			env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "USER=kilo-test", "LOGNAME=kilo-test", "TMPDIR=" + root, "TERM=dumb", "NO_COLOR=1", "CI=1", "HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "ALL_PROXY=http://127.0.0.1:9", "NO_PROXY=127.0.0.1,localhost", "http_proxy=http://127.0.0.1:9", "https_proxy=http://127.0.0.1:9", "no_proxy=127.0.0.1,localhost"}
			for _, name := range []string{"SYSTEMROOT", "WINDIR", "PATHEXT"} {
				if v := os.Getenv(name); v != "" {
					env = append(env, name+"="+v)
				}
			}
			for _, name := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
				env = append(env, name+"="+filepath.Join(root, name))
			}
			prompt := "Read sentinel.txt using the read tool, then repeat the exact contents. Do not change any file."
			args, extra := cgClientArgs(t, client, root, proxy.URL, a.config.LocalKey, prompt)
			env = append(env, extra...)
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, args...)
			command.Dir = project
			command.Env = env
			command.WaitDelay = 2 * time.Second
			output, err := command.CombinedOutput()
			mu.Lock()
			defer mu.Unlock()
			if err != nil || !sawResult {
				text := string(output)
				if len(text) > 8000 {
					text = text[len(text)-8000:]
				}
				keys := []string{}
				if len(incoming) > 0 {
					for k := range incoming[0] {
						keys = append(keys, k)
					}
				}
				t.Fatalf("client=%s error=%v sawResult=%v requests=%d upstream=%d requestKeys=%v mock=%s\n%s", client, err, sawResult, len(incoming), len(upstreamRequests), keys, mockError, text)
			}
			if len(upstreamRequests) < 2 || !strings.Contains(string(output), "VERIFIED "+sentinel) {
				t.Fatalf("client did not consume terminal reply: %s", output)
			}
			// Explicit capture mode is for refreshing small replay fixtures. It
			// contains synthetic requests only, never headers or credentials.
			if directory := os.Getenv("KILO_CHATGPT_CAPTURE_DIR"); directory != "" {
				for i, request := range incoming {
					data, _ := json.Marshal(request)
					if strings.Contains(string(data), sentinel) {
						fixture := cgClientFixture(request, root)
						data, _ = json.MarshalIndent(map[string]any{"path": paths[i], "request": fixture}, "", "  ")
						if err := os.MkdirAll(directory, 0700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(directory, client+".json"), append(data, '\n'), 0600); err != nil {
							t.Fatal(err)
						}
						break
					}
				}
			}
			t.Logf("%s: %d local requests (%v), read-tool execution and submitted result verified", client, len(incoming), paths)
		})
	}
}

func cgClientFixture(request map[string]any, root string) map[string]any {
	data, _ := json.Marshal(request)
	data = []byte(strings.ReplaceAll(string(data), root, "/fixture"))
	d, _ := decodeObject(data)
	delete(d, "metadata")
	delete(d, "client_metadata")
	delete(d, "prompt_cache_key")
	delete(d, "user")
	// Keep message shape, tool calls/results, cache hints and options. Large
	// product system prompts and tool prose are immaterial to wire compatibility.
	if system, ok := d["system"].([]any); ok {
		for _, raw := range system {
			if b := object(raw); b["type"] == "text" {
				b["text"] = "Fixture system instructions."
			}
		}
	}
	if _, ok := d["instructions"].(string); ok {
		d["instructions"] = "Fixture system instructions."
	}
	for _, key := range []string{"messages", "input"} {
		if messages, ok := d[key].([]any); ok {
			for _, raw := range messages {
				m := object(raw)
				if m["role"] == "system" || m["role"] == "developer" {
					m["content"] = "Fixture system instructions."
				}
				if m["role"] == "user" {
					if content, ok := m["content"].(string); ok && !strings.Contains(content, "KILO_SUBSCRIPTION") {
						m["content"] = "Read sentinel.txt and return its contents."
					}
					if content, ok := m["content"].([]any); ok {
						for _, raw := range content {
							part := object(raw)
							if part["type"] == "text" || part["type"] == "input_text" {
								if !strings.Contains(stringValue(part["text"]), "KILO_SUBSCRIPTION") {
									part["text"] = "Read sentinel.txt and return its contents."
								}
							}
						}
					}
				}
			}
		}
	}
	if tools, ok := d["tools"].([]any); ok {
		for _, raw := range tools {
			tool := object(raw)
			if nested := object(tool["function"]); nested != nil {
				tool = nested
			}
			cgFixtureTrimDescriptions(tool)
		}
	}
	return d
}

func cgFixtureTrimDescriptions(value any) {
	switch v := value.(type) {
	case map[string]any:
		delete(v, "description")
		for _, child := range v {
			cgFixtureTrimDescriptions(child)
		}
	case []any:
		for _, child := range v {
			cgFixtureTrimDescriptions(child)
		}
	}
}

func TestChatGPTRecordedClientRequests(t *testing.T) {
	files, err := filepath.Glob("testdata/chatgpt-clients/*.json")
	if err != nil || len(files) != 4 {
		t.Fatalf("expected four recorded clients, got %d: %v", len(files), err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := decodeObject(data)
			if err != nil {
				t.Fatal(err)
			}
			request := object(fixture["request"])
			raw, _ := json.Marshal(request)
			converted, p, err := prepareChatGPTRequest(stringValue(fixture["path"]), raw)
			if err != nil {
				t.Fatal(err)
			}
			if !p.stream {
				t.Fatal("recorded streaming request lost stream option")
			}
			if stringValue(fixture["path"]) == "/v1/responses" && !bytes.Equal(converted, raw) {
				t.Fatal("Responses-native tools and metadata changed")
			}
			body, err := decodeObject(converted)
			if err != nil {
				t.Fatal(err)
			}
			if len(body["tools"].([]any)) != len(request["tools"].([]any)) {
				t.Fatal("tool definitions lost")
			}
			found := false
			for _, raw := range body["input"].([]any) {
				item := object(raw)
				output, _ := json.Marshal(item["output"])
				if item["type"] == "function_call_output" && item["call_id"] == "call_read_sentinel" && strings.Contains(string(output), "KILO_SUBSCRIPTION_TOOL_ROUNDTRIP_7d692a") {
					found = true
				}
			}
			if !found {
				t.Fatal("client tool result was not preserved")
			}
		})
	}
}

func cgClientArgs(t *testing.T, client, root, base, key, prompt string) ([]string, []string) {
	return cgClientArgsForModel(t, client, root, base, key, prompt, "chatgpt/gpt-5.4", "")
}

func cgClientArgsForModel(t *testing.T, client, root, base, key, prompt, model, effort string) ([]string, []string) {
	t.Helper()
	write := func(path, body string) {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	switch client {
	case "claude", "claude-full":
		profile := filepath.Join(root, "claude")
		write(filepath.Join(profile, "settings.json"), `{"skipDangerousModePermissionPrompt":true}`)
		args := []string{"-p", prompt, "--model", model, "--output-format", "json", "--allowedTools", "Read", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--setting-sources", "", "--no-session-persistence", "--effort", "low"}
		env := []string{"CLAUDE_CONFIG_DIR=" + profile, "ANTHROPIC_BASE_URL=" + base, "ANTHROPIC_AUTH_TOKEN=" + key, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "CLAUDE_CODE_SKIP_PROMPT_HISTORY=1"}
		if client == "claude" {
			args = append(args, "--tools", "Read", "--system-prompt", "Read only the requested file and answer.")
			env = append(env, "CLAUDE_CODE_SIMPLE=1")
		}
		return args, env
	case "opencode":
		modelConfig := map[string]any{"name": "Subscription test", "limit": map[string]any{"context": 128000, "output": 8192}}
		if effort != "" {
			modelConfig["options"] = map[string]any{"reasoningEffort": effort}
		}
		config := map[string]any{"provider": map[string]any{"kilo-local": map[string]any{"npm": "@ai-sdk/openai-compatible", "options": map[string]any{"baseURL": base + "/v1", "apiKey": key}, "models": map[string]any{model: modelConfig}}}, "model": "kilo-local/" + model, "small_model": "kilo-local/" + model, "permission": map[string]any{"*": "deny", "read": "allow"}, "share": "disabled", "plugin": []any{}}
		data, _ := json.Marshal(config)
		path := filepath.Join(root, "opencode.json")
		write(path, string(data))
		return []string{"run", "--pure", "--format", "json", "--model", "kilo-local/" + model, prompt}, []string{"OPENCODE_CONFIG=" + path, "OPENCODE_DISABLE_AUTOUPDATE=true", "OPENCODE_DISABLE_MODELS_FETCH=true", "OPENCODE_DISABLE_DEFAULT_PLUGINS=true"}
	case "omp":
		profile := filepath.Join(root, "omp")
		selection := ompSelection{Initial: model, Models: []ompModel{{editorModel: editorModel{ID: model, Name: "Subscription test", Context: 128000, Output: 8192}}}}
		thinking := "off"
		if effort != "" {
			thinking = effort
			selection.Models[0].Reasoning = true
			selection.Models[0].Effort = effort
			selection.Models[0].ReasoningEfforts = []string{effort}
		}
		models, err := buildOMPModels(selection, base+"/v1", key)
		if err != nil {
			t.Fatal(err)
		}
		settings, err := buildOMPSettings(selection)
		if err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(profile, "models.yml"), string(models))
		write(filepath.Join(profile, "config.yml"), string(settings))
		return []string{"-p", prompt, "--model", "kilo-local/" + model, "--mode", "json", "--no-session", "--no-title", "--no-lsp", "--no-pty", "--no-extensions", "--no-skills", "--no-rules", "--tools", "read", "--thinking", thinking, "--max-time", "60", "--approval-mode", "write"}, []string{"PI_CODING_AGENT_DIR=" + profile, "PI_NO_PTY=1"}
	case "codex":
		profile := filepath.Join(root, "codex")
		write(filepath.Join(profile, "config.toml"), fmt.Sprintf("model = %q\nmodel_provider = %q\nmodel_context_window = 128000\n[model_providers.mock]\nname = %q\nbase_url = %q\nenv_key = %q\nwire_api = %q\nrequires_openai_auth = false\nsupports_websockets = false\n", model, "mock", "Local subscription test", base+"/v1", "KILO_LOCAL_API_KEY", "responses"))
		args := []string{"exec", "--json", "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only", "--ignore-rules"}
		if effort != "" {
			args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", effort))
		}
		args = append(args, prompt)
		return args, []string{"CODEX_HOME=" + profile, "KILO_LOCAL_API_KEY=" + key}
	default:
		t.Fatalf("unsupported test client %q", client)
	}
	return nil, nil
}

func cgClientReadTool(request map[string]any, path string) (string, map[string]any, error) {
	tools, _ := request["tools"].([]any)
	for _, raw := range tools {
		tool := object(raw)
		name := stringValue(tool["name"])
		properties := object(object(tool["parameters"])["properties"])
		if strings.EqualFold(name, "read") || strings.EqualFold(name, "read_file") {
			for _, field := range []string{"file_path", "filePath", "path"} {
				if _, ok := properties[field]; ok {
					return name, map[string]any{field: path}, nil
				}
			}
		}
	}
	for _, raw := range tools {
		tool := object(raw)
		name := stringValue(tool["name"])
		switch name {
		case "exec_command":
			return name, map[string]any{"cmd": "cat " + helperShellQuote(path), "max_output_tokens": 1000}, nil
		case "shell_command":
			return name, map[string]any{"command": "cat " + helperShellQuote(path)}, nil
		case "shell":
			return name, map[string]any{"command": []string{"cat", path}}, nil
		}
	}
	names := []string{}
	for _, raw := range tools {
		names = append(names, stringValue(object(raw)["name"]))
	}
	return "", nil, fmt.Errorf("no known read-only tool among %v", names)
}

func cgClientResponse(w http.ResponseWriter, item map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	response := map[string]any{"id": "resp_cli_test", "object": "response", "created_at": time.Now().Unix(), "model": "gpt-5.4", "status": "in_progress", "output": []any{}}
	sequence := 0
	send := func(kind string, fields map[string]any) {
		fields["type"] = kind
		fields["sequence_number"] = sequence
		sequence++
		data, _ := json.Marshal(fields)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	send("response.created", map[string]any{"response": response})
	added := map[string]any{}
	for k, v := range item {
		added[k] = v
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
	response["usage"] = map[string]any{"input_tokens": 100, "output_tokens": 20, "total_tokens": 120, "input_tokens_details": map[string]any{"cached_tokens": 20}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}
	send("response.completed", map[string]any{"response": response})
}

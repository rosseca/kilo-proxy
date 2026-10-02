package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt in to the installed, unmodified OpenMausBot driver. Its requests traverse
// the real helper/profile, auth, reasoning injection and protocol adapters, but
// both providers are synthetic localhost servers. No desktop is started.
func TestOpenMausBotInstalledReasoning(t *testing.T) {
	resources := os.Getenv("KILO_OPENMAUSBOT_RESOURCES")
	if resources == "" {
		t.Skip("set KILO_OPENMAUSBOT_RESOURCES to an installed distribution's resources directory")
	}
	if !filepath.IsAbs(resources) {
		t.Fatal("use an absolute resources directory")
	}
	if _, err := os.Stat(filepath.Join(resources, "server", "openmausbot.js")); err != nil {
		t.Fatal("installed OpenMausBot bundle is unavailable")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required for the installed driver check")
	}
	a := launchTestApp(t)
	a.config.Language = "en"
	a.config.LocalKey = "synthetic-openmaus-reasoning-local"
	a.chatgpt.creds = chatGPTCredentials{Access: "synthetic-chatgpt-access", Refresh: "synthetic-refresh", Account: "synthetic-account", Expires: time.Now().Add(time.Hour).Unix()}
	a.chatgpt.state = chatGPTState{Status: "idle"}
	a.launcher.platform = runtime.GOOS
	installed := filepath.Join(filepath.Dir(resources), "openmausbot")
	if runtime.GOOS == "darwin" {
		installed = filepath.Dir(filepath.Dir(resources))
	} else if runtime.GOOS == "windows" {
		installed = filepath.Join(filepath.Dir(resources), "OpenMausBot.exe")
	}
	a.launcher.resolve = func(id, custom string) (string, error) {
		if id != "openmausbot" || custom != "" {
			return "", errors.New("unexpected fixture client")
		}
		return installed, nil
	}
	a.launcher.start = func(clientLaunchPlan) error {
		t.Error("driver validation must never start the installed desktop")
		return errors.New("desktop launch disabled in driver fixture")
	}
	a.openMausBotCheckRunning = func(openMausBotPaths) (bool, error) { return false, nil }
	models := []string{"vendor/synthetic-low", "chatgpt/gpt-synthetic-high", "vendor/synthetic-max", "vendor/synthetic-none", "vendor/synthetic-auto"}
	efforts := []string{"low", "high", "max", "none", ""}
	library := modelLibrary{SchemaVersion: 1, DefaultModel: models[0]}
	catalog := []modelInfo{}
	for i, id := range models {
		item := modelLibraryItem{ID: id, ReasoningCustom: true, ReasoningEffort: efforts[i], ContextPreset: contextPresetLow}
		if efforts[i] != "" {
			item.ReasoningLevels = []string{efforts[i]}
		}
		library.Models = append(library.Models, item)
		catalog = append(catalog, modelInfo{ID: id, ContextWindow: 128000, MaxOutputTokens: 4096, ReasoningEfforts: item.ReasoningLevels})
	}
	if _, err := a.modelLibrary.save(library, 0, false); err != nil {
		t.Fatal(err)
	}
	cache, _ := json.Marshal(nativeCachedCatalog{SchemaVersion: 1, OrgID: a.catalogScopeLocked(), Models: catalog, FetchedAt: time.Now().UTC()})
	if err := os.WriteFile(filepath.Join(a.dir, "model-catalog.json"), cache, 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	counts := map[string]int{}
	count := func(model string) { mu.Lock(); counts[model]++; mu.Unlock() }
	readRequest := func(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid JSON sent by the installed driver")
			http.Error(w, "invalid fixture request", 400)
			return nil, false
		}
		return body, true
	}
	kilo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/gateway/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic-kilo" || r.Header.Get("X-KiloCode-OrganizationId") != "synthetic-org" || r.Header.Get("ChatGPT-Account-Id") != "" {
			t.Error("Kilo route, team or credential isolation changed")
			http.Error(w, "wrong fixture route/auth", 400)
			return
		}
		body, ok := readRequest(w, r)
		if !ok {
			return
		}
		model := stringValue(body["model"])
		want, known := map[string]string{models[0]: "low", models[2]: "max", models[3]: "none", models[4]: ""}[model]
		if !known {
			t.Error("unexpected Kilo model")
		}
		if want == "" {
			if _, present := body["reasoning"]; present {
				t.Error("automatic reasoning acquired a fabricated default")
			}
		} else if !reflect.DeepEqual(body["reasoning"], map[string]any{"enabled": want != "none", "effort": want}) {
			t.Errorf("Kilo model %s did not receive its canonical reasoning setting", model)
		}
		if _, present := body["reasoning_effort"]; present {
			t.Error("Kilo should receive its canonical reasoning object")
		}
		count(model)
		if body["stream"] != true {
			jsonResponse(w, 200, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "synthetic helper"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 4, "prompt_tokens_details": map[string]any{"cached_tokens": 10}, "cost": 0.00012}})
			return
		}
		tool := openMausReasoningFixtureTool(t, body, false)
		var result map[string]any
		messages, _ := body["messages"].([]any)
		for _, raw := range messages {
			if row := object(raw); row["role"] == "tool" {
				result = row
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		frame := func(payload map[string]any) { data, _ := json.Marshal(payload); fmt.Fprintf(w, "data: %s\n\n", data) }
		if result != nil {
			if result["tool_call_id"] != "synthetic-call" || !strings.Contains(strings.ToLower(stringValue(result["content"])), "denied") {
				t.Error("denied tool result lost its call ID or refusal")
			}
			matched := false
			for _, raw := range messages {
				calls, _ := object(raw)["tool_calls"].([]any)
				for _, call := range calls {
					row := object(call)
					matched = matched || row["id"] == result["tool_call_id"] && object(row["function"])["name"] == tool
				}
			}
			if !matched {
				t.Error("Kilo tool result no longer matches its assistant call")
			}
			frame(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "tool roundtrip complete"}, "finish_reason": nil}}})
			frame(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 8}})
		} else {
			frame(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "synthetic-call", "type": "function", "function": map[string]any{"name": tool, "arguments": "{"}}}}, "finish_reason": nil}}})
			frame(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": "}"}}}}, "finish_reason": nil}}})
			frame(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}, "usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 4}})
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer kilo.Close()
	setUpstream(a, kilo.URL)
	subscription := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" || r.Header.Get("Authorization") != "Bearer synthetic-chatgpt-access" || r.Header.Get("ChatGPT-Account-Id") != "synthetic-account" || r.Header.Get("X-KiloCode-OrganizationId") != "" {
			t.Error("ChatGPT route or credential isolation changed")
			http.Error(w, "wrong fixture route/auth", 400)
			return
		}
		body, ok := readRequest(w, r)
		if !ok {
			return
		}
		if body["model"] != "gpt-synthetic-high" || body["stream"] != true || body["store"] != false || !reflect.DeepEqual(body["reasoning"], map[string]any{"effort": "high"}) {
			t.Error("ChatGPT translated request lost its model or reasoning default")
		}
		if _, present := body["reasoning_effort"]; present {
			t.Error("Chat Completions reasoning was not converted to Responses")
		}
		count(models[1])
		tool := openMausReasoningFixtureTool(t, body, true)
		var result map[string]any
		input, _ := body["input"].([]any)
		for _, raw := range input {
			if row := object(raw); row["type"] == "function_call_output" && strings.Contains(stringValue(row["output"]), "KILO_OPENMAUS_SENTINEL") {
				result = row
			}
		}
		if result != nil {
			matched := false
			for _, raw := range input {
				row := object(raw)
				matched = matched || row["type"] == "function_call" && row["call_id"] == result["call_id"] && row["name"] == tool
			}
			if result["call_id"] != "synthetic-call" || !matched {
				t.Error("subscription tool result no longer matches the actual MCP call")
			}
			openMausReasoningFixtureSSE(w, map[string]any{"id": "msg_synthetic", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "tool roundtrip complete", "annotations": []any{}}}}, 30, 8)
		} else {
			openMausReasoningFixtureSSE(w, map[string]any{"id": "fc_synthetic", "type": "function_call", "call_id": "synthetic-call", "name": tool, "arguments": "{}", "status": "completed"}, 20, 4)
		}
	}))
	defer subscription.Close()
	a.chatGPTResponsesURL = subscription.URL + "/codex/responses"
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	// Fail closed even if future helpers start fetching a public model catalog.
	a.transport = modelMetricTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "http" || "http://"+r.URL.Host != kilo.URL && "http://"+r.URL.Host != subscription.URL {
			return nil, errors.New("non-fixture upstream blocked")
		}
		return transport.RoundTrip(r)
	})
	proxy := httptest.NewUnstartedServer(nil)
	a.config.Port = proxy.Listener.Addr().(*net.TCPAddr).Port
	proxy.Config.Handler = a.inferenceHandler(a.apiKey, a.config.OrgID, a.config.LocalKey, proxy.Listener.Addr().String())
	proxy.Start()
	defer proxy.Close()
	prepareOpenMausBotFixture(t, a)
	fixture := map[string]any{"fixture": "kilo-openmaus-reasoning-test-v1", "baseURL": proxy.URL + openMausBotBasePath, "localKey": a.config.LocalKey, "models": models, "configPath": openMausBotProfilePaths(a.dir).Config}
	data, _ := json.Marshal(fixture)
	manifest := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "scripts/smoke_openmausbot.mjs", "--resources", resources, "--proxy-fixture", manifest)
	command.Env = cgClientEnv(t.TempDir())
	command.WaitDelay = 2 * time.Second
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("installed driver smoke failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "PASS: real proxy fixture") {
		t.Fatalf("installed driver did not finish: %s", output)
	}
	mu.Lock()
	defer mu.Unlock()
	wantCounts := map[string]int{models[0]: 3, models[1]: 2, models[2]: 1, models[3]: 1, models[4]: 1}
	if !reflect.DeepEqual(counts, wantCounts) {
		t.Fatalf("unexpected provider/model requests: %v; want %v", counts, wantCounts)
	}
	t.Log("Installed driver passed: canonical Kilo low/max/none/auto and ChatGPT high, streamed tools on one conversation, deny/allow permissions, exact IDs/schema, usage and credential isolation.")
}

func openMausReasoningFixtureTool(t *testing.T, body map[string]any, responses bool) string {
	t.Helper()
	tools, _ := body["tools"].([]any)
	for _, raw := range tools {
		row := object(raw)
		if !responses {
			row = object(row["function"])
		}
		if name := stringValue(row["name"]); strings.HasSuffix(name, "_read_sentinel") {
			schema := object(row["parameters"])
			if schema["type"] != "object" || schema["additionalProperties"] != false || len(object(schema["properties"])) != 0 {
				t.Error("MCP tool schema changed in the proxy")
			}
			return name
		}
	}
	t.Error("actual installed driver did not expose the synthetic MCP tool")
	return "missing_fixture_tool"
}

func openMausReasoningFixtureSSE(w http.ResponseWriter, item map[string]any, input, output int) {
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(kind string, fields map[string]any) {
		fields["type"] = kind
		data, _ := json.Marshal(fields)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	response := map[string]any{"id": "resp_synthetic", "object": "response", "model": "gpt-synthetic-high", "status": "in_progress", "output": []any{}}
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
		for _, delta := range []string{"{", "}"} {
			send("response.function_call_arguments.delta", map[string]any{"output_index": 0, "item_id": item["id"], "delta": delta})
		}
		send("response.function_call_arguments.done", map[string]any{"output_index": 0, "item_id": item["id"], "arguments": item["arguments"]})
	} else {
		part := object(item["content"].([]any)[0])
		send("response.content_part.added", map[string]any{"output_index": 0, "content_index": 0, "item_id": item["id"], "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
		send("response.output_text.delta", map[string]any{"output_index": 0, "content_index": 0, "item_id": item["id"], "delta": part["text"]})
		send("response.output_text.done", map[string]any{"output_index": 0, "content_index": 0, "item_id": item["id"], "text": part["text"]})
	}
	send("response.output_item.done", map[string]any{"output_index": 0, "item": item})
	response["status"] = "completed"
	response["output"] = []any{item}
	response["usage"] = map[string]any{"input_tokens": input, "output_tokens": output, "total_tokens": input + output}
	send("response.completed", map[string]any{"response": response})
}

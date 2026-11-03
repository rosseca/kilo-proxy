package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func namespaceTestFunction(name string) map[string]any {
	return map[string]any{"type": "function", "name": name, "description": "Original function", "strict": true, "parameters": map[string]any{"type": "object", "properties": map[string]any{"marker": map[string]any{"type": "string"}}, "required": []any{"marker"}, "additionalProperties": false}}
}
func namespaceTestGroup(namespace string, tools ...any) map[string]any {
	return map[string]any{"type": "namespace", "name": namespace, "description": "Original group", "tools": tools}
}
func namespaceTestDoc(tools ...any) map[string]any {
	return map[string]any{"model": "openai/test", "tools": tools, "input": []any{}}
}
func namespaceTestPrepare(t *testing.T, doc any) (*responsesNamespaceBridge, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
	bridge, err := prepareResponsesNamespaceBridge(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if request.ContentLength != int64(len(raw)) {
		t.Fatal("request length changed incorrectly")
	}
	wire, err := decodeObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	return bridge, wire
}
func namespaceTestTool(t *testing.T, wire map[string]any, index int) map[string]any {
	t.Helper()
	tools, ok := wire["tools"].([]any)
	if !ok || index >= len(tools) {
		t.Fatalf("missing tool %d", index)
	}
	return object(tools[index])
}
func namespaceTestResponse(t *testing.T, bridge *responsesNamespaceBridge, doc any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(doc)
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	header.Set("ETag", "stale")
	header.Set("Digest", "stale")
	response := &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw))}
	if err := bridge.adaptResponse(response); err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("ETag") != "" || response.Header.Get("Digest") != "" || response.ContentLength != int64(len(raw)) {
		t.Fatal("stale response metadata")
	}
	result, err := decodeObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestResponsesNamespaceBridgeRequestHistory(t *testing.T) {
	function := namespaceTestFunction("lookup")
	custom := map[string]any{"type": "custom", "name": "patch", "format": map[string]any{"type": "text"}}
	doc := namespaceTestDoc(namespaceTestGroup("weather", function, custom, namespaceTestFunction(strings.Repeat("x", 128))), namespaceTestGroup("research", namespaceTestFunction("lookup")), namespaceTestFunction("lookup"))
	arguments := "{\"marker\":\"weather.lookup and kilo_ns_text are opaque\"}"
	doc["input"] = []any{
		map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "namespace": "weather", "name": "lookup", "arguments": arguments},
		map[string]any{"type": "function_call_output", "call_id": "call_1", "namespace": "weather", "name": "lookup", "output": "weather.lookup is opaque"},
		map[string]any{"type": "custom_tool_call", "id": "ct_1", "call_id": "call_2", "namespace": "weather", "name": "patch", "input": "opaque custom text"},
		map[string]any{"type": "function_call", "call_id": "call_3", "name": "lookup", "arguments": "{}"},
		map[string]any{"type": "function_call", "call_id": "call_4", "name": "research.lookup", "arguments": "{}"},
	}
	bridge, wire := namespaceTestPrepare(t, doc)
	if bridge == nil {
		t.Fatal("missing bridge")
	}
	tools := wire["tools"].([]any)
	if len(tools) != 5 {
		t.Fatal("lost tool")
	}
	seen := map[string]bool{}
	safe := regexp.MustCompile("^[A-Za-z0-9_-]{1,64}$")
	for _, value := range tools {
		tool := object(value)
		name := stringValue(tool["name"])
		if !safe.MatchString(name) || seen[name] || tool["type"] == "namespace" {
			t.Fatalf("unsafe or duplicate tool: %#v", tool)
		}
		seen[name] = true
	}
	if !reflect.DeepEqual(namespaceTestTool(t, wire, 0)["parameters"], function["parameters"]) || namespaceTestTool(t, wire, 0)["strict"] != true {
		t.Fatal("schema/strict changed")
	}
	if namespaceTestTool(t, wire, 4)["name"] != "lookup" {
		t.Fatal("flat homonym changed")
	}
	input := wire["input"].([]any)
	for _, index := range []int{0, 1} {
		item := object(input[index])
		if item["name"] != namespaceTestTool(t, wire, 0)["name"] || item["namespace"] != nil || item["call_id"] != "call_1" {
			t.Fatal("history identity changed")
		}
	}
	if object(input[0])["arguments"] != arguments || object(input[1])["output"] != "weather.lookup is opaque" || object(input[2])["input"] != "opaque custom text" {
		t.Fatal("opaque history content changed")
	}
	if object(input[2])["name"] != namespaceTestTool(t, wire, 1)["name"] || object(input[3])["name"] != "lookup" || object(input[4])["name"] != "research.lookup" {
		t.Fatal("custom or flat history changed")
	}
}

func TestResponsesNamespaceBridgeCollisionDeterminism(t *testing.T) {
	doc := namespaceTestDoc(namespaceTestGroup("one", namespaceTestFunction("lookup")))
	_, first := namespaceTestPrepare(t, doc)
	candidate := stringValue(namespaceTestTool(t, first, 0)["name"])
	collision := namespaceTestDoc(namespaceTestFunction(candidate), namespaceTestGroup("one", namespaceTestFunction("lookup")))
	_, second := namespaceTestPrepare(t, collision)
	_, repeated := namespaceTestPrepare(t, collision)
	if namespaceTestTool(t, second, 0)["name"] != candidate || namespaceTestTool(t, second, 1)["name"] == candidate || namespaceTestTool(t, second, 1)["name"] != namespaceTestTool(t, repeated, 1)["name"] {
		t.Fatal("collision safety or deterministic identity failed")
	}
}

func TestResponsesNamespaceBridgeChoice(t *testing.T) {
	choices := []any{"auto", "none", "required", map[string]any{"type": "function", "name": "weather.lookup"}, map[string]any{"type": "function", "namespace": "weather", "name": "lookup"}, map[string]any{"type": "allowed_tools", "mode": "required", "tools": []any{map[string]any{"type": "function", "name": "weather.lookup"}, map[string]any{"type": "custom", "name": "weather.patch"}, map[string]any{"type": "web_search"}}}}
	for _, choice := range choices {
		t.Run(stringValue(object(choice)["type"])+stringValue(choice), func(t *testing.T) {
			doc := namespaceTestDoc(namespaceTestGroup("weather", namespaceTestFunction("lookup"), map[string]any{"type": "custom", "name": "patch"}))
			doc["tool_choice"] = choice
			_, wire := namespaceTestPrepare(t, doc)
			if _, scalar := choice.(string); scalar {
				if wire["tool_choice"] != choice {
					t.Fatal("choice mode changed")
				}
				return
			}
			ref := object(wire["tool_choice"])
			if ref["type"] == "allowed_tools" {
				allowed := ref["tools"].([]any)
				if ref["mode"] != "required" || object(allowed[0])["name"] != namespaceTestTool(t, wire, 0)["name"] || object(allowed[1])["name"] != namespaceTestTool(t, wire, 1)["name"] || object(allowed[2])["type"] != "web_search" {
					t.Fatal("allowed tool selection changed")
				}
			} else if ref["name"] != namespaceTestTool(t, wire, 0)["name"] || ref["namespace"] != nil {
				t.Fatal("forced selection changed")
			}
		})
	}
	doc := namespaceTestDoc(namespaceTestGroup("weather", namespaceTestFunction("lookup")), namespaceTestFunction("weather.lookup"))
	doc["tool_choice"] = map[string]any{"type": "function", "name": "weather.lookup"}
	_, wire := namespaceTestPrepare(t, doc)
	if object(wire["tool_choice"])["name"] != "weather.lookup" {
		t.Fatal("genuine dotted flat name changed")
	}
}

func TestResponsesNamespaceBridgeJSONIdentityAndEcho(t *testing.T) {
	doc := namespaceTestDoc(namespaceTestGroup("weather", namespaceTestFunction("lookup"), map[string]any{"type": "custom", "name": "patch"}), namespaceTestFunction("flat"), map[string]any{"type": "web_search"})
	bridge, wire := namespaceTestPrepare(t, doc)
	functionAlias := stringValue(namespaceTestTool(t, wire, 0)["name"])
	customAlias := stringValue(namespaceTestTool(t, wire, 1)["name"])
	arguments := "{\"marker\":\"" + functionAlias + "\"}"
	got := namespaceTestResponse(t, bridge, map[string]any{
		"id": "resp_1", "usage": map[string]any{"input_tokens": 5},
		"tools":       wire["tools"].([]any)[:3],
		"tool_choice": map[string]any{"type": "function", "name": functionAlias},
		"output":      []any{map[string]any{"type": "function_call", "name": functionAlias, "id": "fc_1", "call_id": "call_1", "arguments": arguments}, map[string]any{"type": "custom_tool_call", "name": customAlias, "call_id": "call_2", "input": "opaque custom"}, map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": functionAlias}}}},
	})
	output := got["output"].([]any)
	if object(output[0])["name"] != "lookup" || object(output[0])["namespace"] != "weather" || object(output[0])["arguments"] != arguments || object(output[0])["call_id"] != "call_1" {
		t.Fatal("function identity/content not restored")
	}
	if object(output[1])["name"] != "patch" || object(output[1])["namespace"] != "weather" || object(output[1])["input"] != "opaque custom" {
		t.Fatal("custom identity/content not restored")
	}
	if object(object(output[2])["content"].([]any)[0])["text"] != functionAlias {
		t.Fatal("message content changed")
	}
	tools := got["tools"].([]any)
	if len(tools) != 2 || object(tools[0])["type"] != "namespace" || object(tools[1])["name"] != "flat" {
		t.Fatal("grouping failed or filtered hosted tool resurrected")
	}
	group := object(tools[0])
	children := group["tools"].([]any)
	if group["description"] != "Original group" || len(children) != 2 || object(children[0])["description"] != "Original function" {
		t.Fatal("tool descriptions changed")
	}
	if _, exists := object(children[1])["description"]; exists {
		t.Fatal("wire hint leaked")
	}
	if object(got["tool_choice"])["name"] != "weather.lookup" {
		t.Fatal("choice echo not restored")
	}
}

func TestResponsesNamespaceBridgeSSE(t *testing.T) {
	bridge, wire := namespaceTestPrepare(t, namespaceTestDoc(namespaceTestGroup("weather", namespaceTestFunction("lookup"), map[string]any{"type": "custom", "name": "patch"})))
	alias := stringValue(namespaceTestTool(t, wire, 0)["name"])
	custom := stringValue(namespaceTestTool(t, wire, 1)["name"])
	events := []map[string]any{
		{"type": "response.created", "sequence_number": 9, "response": map[string]any{"id": "resp_sse", "tools": wire["tools"]}},
		{"type": "response.in_progress", "sequence_number": 10, "response": map[string]any{"tools": wire["tools"]}},
		{"type": "response.output_item.added", "sequence_number": 11, "item": map[string]any{"type": "function_call", "id": "fc_1", "name": alias, "arguments": ""}},
		{"type": "response.function_call_arguments.delta", "sequence_number": 12, "item_id": "fc_1", "delta": alias},
		{"type": "response.function_call_arguments.done", "sequence_number": 13, "item_id": "fc_1", "name": alias, "arguments": "{}"},
		{"type": "response.output_item.done", "sequence_number": 14, "item": map[string]any{"type": "custom_tool_call", "id": "ct_1", "name": custom, "input": custom}},
		{"type": "response.custom_tool_call_input.delta", "sequence_number": 15, "item_id": "ct_1", "delta": custom},
		{"type": "response.custom_tool_call_input.done", "sequence_number": 16, "item_id": "ct_1", "name": custom, "input": custom},
		{"type": "response.completed", "sequence_number": 17, "response": map[string]any{"output": []any{map[string]any{"type": "function_call", "id": "fc_1", "name": alias, "arguments": "{}"}}}},
		{"type": "response.incomplete", "sequence_number": 18, "response": map[string]any{"output": []any{map[string]any{"type": "custom_tool_call", "id": "ct_1", "name": custom, "input": custom}}}},
		{"type": "response.failed", "sequence_number": 19, "response": map[string]any{"tools": wire["tools"]}},
	}
	var source strings.Builder
	source.WriteString(": keepalive\n\n")
	for _, event := range events {
		raw, _ := json.Marshal(event)
		source.WriteString("id: preserved\nretry: 1500\nevent: " + stringValue(event["type"]) + "\ndata: " + string(raw) + "\n\n")
	}
	source.WriteString("data: [DONE]\n\n")
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(source.String()))}
	if err := bridge.adaptResponse(response); err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "id: preserved") != len(events) || strings.Count(string(raw), "retry: 1500") != len(events) || !strings.Contains(string(raw), ": keepalive\n\n") || !strings.Contains(string(raw), "data: [DONE]\n\n") {
		t.Fatal("SSE metadata/framing changed")
	}
	var got []map[string]any
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "data: {") {
			value, err := decodeObject([]byte(strings.TrimPrefix(line, "data: ")))
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, value)
		}
	}
	if len(got) != len(events) {
		t.Fatal("SSE event count changed")
	}
	for i, event := range got {
		expected, _ := json.Marshal(events[i]["sequence_number"])
		if event["type"] != events[i]["type"] || event["sequence_number"].(json.Number).String() != string(expected) {
			t.Fatalf("event type/sequence changed at %d", i)
		}
	}
	if object(got[2]["item"])["name"] != "lookup" || object(got[2]["item"])["namespace"] != "weather" || got[3]["delta"] != alias || got[4]["name"] != "lookup" || got[6]["delta"] != custom || got[7]["name"] != "patch" {
		t.Fatal("SSE identity changed opaque deltas")
	}
	for _, index := range []int{0, 1, 10} {
		if object(object(got[index]["response"])["tools"].([]any)[0])["type"] != "namespace" {
			t.Fatal("lifecycle tool echo leaks private name")
		}
	}
}

func TestResponsesNamespaceBridgeUnrelatedExact(t *testing.T) {
	for _, raw := range []string{" { \"model\":\"openai/test\", \"tools\":[{\"type\":\"function\",\"name\":\"flat\"}],\"previous_response_id\":\"resp_old\" } ", "{\"model\":\"openai/test\",\"input\":\"hello\"}", "{\"model\":\"openai/test\",\"input\":\"hello\",\"conversation\":\"conv_flat\"}", "not JSON"} {
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(raw))
		request.Header.Set("X-Private-Test", "unchanged")
		length := request.ContentLength
		bridge, err := prepareResponsesNamespaceBridge(request)
		if err != nil || bridge != nil {
			t.Fatal("unrelated request adapted", err)
		}
		body, _ := io.ReadAll(request.Body)
		if string(body) != raw || request.ContentLength != length || request.Header.Get("X-Private-Test") != "unchanged" {
			t.Fatal("unrelated bytes/headers changed")
		}
	}
}

func TestResponsesNamespaceBridgeRejectUnsupported(t *testing.T) {
	cases := []map[string]any{
		namespaceTestDoc(namespaceTestGroup("group", map[string]any{"type": "web_search"})),
		namespaceTestDoc(namespaceTestGroup("group", namespaceTestGroup("inner", namespaceTestFunction("x")))),
		namespaceTestDoc(namespaceTestGroup("group", namespaceTestFunction("x"), namespaceTestFunction("x"))),
		namespaceTestDoc(namespaceTestGroup("group", namespaceTestFunction("x")), namespaceTestGroup("group", namespaceTestFunction("y"))),
		namespaceTestDoc(namespaceTestGroup("", namespaceTestFunction("x"))),
		namespaceTestDoc(namespaceTestGroup("group")),
		namespaceTestDoc(namespaceTestGroup("group", namespaceTestFunction("x")), map[string]any{"type": "tool_search"}),
	}
	previous := namespaceTestDoc(namespaceTestGroup("group", namespaceTestFunction("x")))
	previous["previous_response_id"] = "resp_saved"
	cases = append(cases, previous)
	dynamic := namespaceTestDoc(namespaceTestGroup("group", namespaceTestFunction("x")))
	dynamic["input"] = []any{map[string]any{"type": "tool_search_output", "tools": []any{}}}
	cases = append(cases, dynamic)
	bare := namespaceTestDoc(namespaceTestGroup("group", namespaceTestFunction("x")))
	bare["tool_choice"] = map[string]any{"type": "function", "name": "x"}
	cases = append(cases, bare)
	for _, doc := range cases {
		raw, _ := json.Marshal(doc)
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
		if bridge, err := prepareResponsesNamespaceBridge(request); err == nil || bridge != nil {
			t.Fatalf("unsupported request accepted: %s", raw)
		}
	}
}

func TestResponsesNamespaceBridgeResponseGuards(t *testing.T) {
	bridge, wire := namespaceTestPrepare(t, namespaceTestDoc(namespaceTestGroup("g", namespaceTestFunction("x"))))
	alias := namespaceTestTool(t, wire, 0)["name"]
	unknown := "kilo_ns_not_registered"
	got := namespaceTestResponse(t, bridge, map[string]any{"output": []any{map[string]any{"type": "function_call", "name": unknown, "arguments": "{}"}}})
	if object(got["output"].([]any)[0])["name"] != unknown {
		t.Fatal("unknown name guessed")
	}
	for _, item := range []map[string]any{{"type": "custom_tool_call", "name": alias, "input": ""}, {"type": "function_call", "name": alias, "namespace": "wrong", "arguments": "{}"}} {
		raw, _ := json.Marshal(map[string]any{"output": []any{item}})
		response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw))}
		if err := bridge.adaptResponse(response); err == nil {
			t.Fatal("conflicting response identity accepted")
		}
	}
	for _, status := range []int{400, 429, 500} {
		response := &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("opaque error"))}
		if err := bridge.adaptResponse(response); err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if string(raw) != "opaque error" {
			t.Fatal("error body changed")
		}
	}
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Encoding": {"br"}}, Body: io.NopCloser(strings.NewReader("encoded bytes"))}
	if err := bridge.adaptResponse(response); err == nil {
		t.Fatal("encoded success could leak wire identities")
	}
}

func TestResponsesNamespaceBridgeRetiredHistory(t *testing.T) {
	doc := namespaceTestDoc(namespaceTestGroup("current", namespaceTestFunction("new")))
	doc["input"] = []any{map[string]any{"type": "function_call", "call_id": "old_1", "namespace": "retired", "name": "old", "arguments": "{}"}, map[string]any{"type": "function_call_output", "call_id": "old_1", "output": "unchanged"}}
	bridge, wire := namespaceTestPrepare(t, doc)
	if bridge == nil || object(wire["input"].([]any)[0])["namespace"] != nil || object(wire["input"].([]any)[1])["output"] != "unchanged" {
		t.Fatal("retired explicit history not safely represented")
	}
}

func TestResponsesNamespaceBridgeRetiredFlatHistory(t *testing.T) {
	doc := namespaceTestDoc(namespaceTestGroup("weather", namespaceTestFunction("lookup")))
	doc["input"] = []any{
		map[string]any{"type": "function_call", "call_id": "retired_1", "name": "lookup", "arguments": "{}"},
		map[string]any{"type": "function_call_output", "call_id": "retired_1", "name": "lookup", "output": "opaque"},
		map[string]any{"type": "function_call", "call_id": "retired_2", "name": "weather.lookup", "arguments": "{}"},
	}
	_, wire := namespaceTestPrepare(t, doc)
	for i, name := range []string{"lookup", "lookup", "weather.lookup"} {
		if object(wire["input"].([]any)[i])["name"] != name || object(wire["input"].([]any)[i])["namespace"] != nil {
			t.Fatal("retired flat history reinterpreted as current namespace")
		}
	}
}
func TestResponsesNamespaceBridgeOriginalNamesRemainModelVisible(t *testing.T) {
	doc := namespaceTestDoc(namespaceTestGroup("files",
		map[string]any{"type": "function", "name": "read", "parameters": map[string]any{"type": "object"}},
		map[string]any{"type": "function", "name": "delete", "parameters": map[string]any{"type": "object"}}))
	_, wire := namespaceTestPrepare(t, doc)
	for i, name := range []string{"files.read", "files.delete"} {
		if !strings.Contains(stringValue(namespaceTestTool(t, wire, i)["description"]), name) {
			t.Fatal("original tool identity lost from model-visible context")
		}
	}
}
func TestResponsesNamespaceBridgeTypedChoiceAndOriginalEcho(t *testing.T) {
	doc := namespaceTestDoc(namespaceTestGroup("files", map[string]any{"type": "custom", "name": "patch"}), namespaceTestFunction("files.patch"))
	doc["tool_choice"] = map[string]any{"type": "custom", "namespace": "files", "name": "patch"}
	bridge, wire := namespaceTestPrepare(t, doc)
	alias := namespaceTestTool(t, wire, 0)["name"]
	if object(wire["tool_choice"])["name"] != alias {
		t.Fatal("typed namespace choice shadowed by unrelated flat type")
	}
	got := namespaceTestResponse(t, bridge, map[string]any{"tool_choice": wire["tool_choice"], "output": []any{}})
	if !reflect.DeepEqual(got["tool_choice"], doc["tool_choice"]) {
		t.Fatal("original qualified choice representation not preserved")
	}
}

func TestResponsesNamespaceBridgeIdentityEncoding(t *testing.T) {
	for _, encoding := range []string{"identity", " Identity "} {
		raw, _ := json.Marshal(namespaceTestDoc(namespaceTestGroup("files", namespaceTestFunction("read"))))
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
		request.Header.Set("Content-Encoding", encoding)
		bridge, err := prepareResponsesNamespaceBridge(request)
		if err != nil || bridge == nil {
			t.Fatal("identity encoding did not adapt namespace", err)
		}
	}
	raw := []byte("opaque encoded body")
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
	request.Header.Set("Content-Encoding", "gzip")
	bridge, err := prepareResponsesNamespaceBridge(request)
	if err != nil || bridge != nil {
		t.Fatal("nonidentity encoding was interpreted")
	}
	got, _ := io.ReadAll(request.Body)
	if !bytes.Equal(got, raw) {
		t.Fatal("encoded payload changed")
	}
}
func TestResponsesNamespaceBridgeEmptyToolsEcho(t *testing.T) {
	bridge, _ := namespaceTestPrepare(t, namespaceTestDoc(namespaceTestGroup("files", namespaceTestFunction("read"))))
	got := namespaceTestResponse(t, bridge, map[string]any{"output": []any{}, "tools": []any{}})
	if tools, ok := got["tools"].([]any); !ok || len(tools) != 0 {
		t.Fatal("empty JSON tools array became null")
	}
	event, err := bridge.transformEvent([]byte("{\"type\":\"response.created\",\"response\":{\"tools\":[]}}"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, _ := decodeObject(event)
	if tools, ok := object(decoded["response"])["tools"].([]any); !ok || len(tools) != 0 {
		t.Fatal("empty SSE tools array became null")
	}
}

func TestResponsesNamespaceBridgeConversationStateGuard(t *testing.T) {
	for _, state := range []any{"conv_1", map[string]any{"id": "conv_1"}} {
		doc := namespaceTestDoc(namespaceTestGroup("files", namespaceTestFunction("read")))
		doc["conversation"] = state
		raw, _ := json.Marshal(doc)
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
		bridge, err := prepareResponsesNamespaceBridge(request)
		if bridge != nil || err == nil || !strings.Contains(err.Error(), "full input history") {
			t.Fatal("opaque conversation state accepted with adapted namespaces")
		}
	}
}

func TestResponsesNamespaceBridgeHistoryWithoutGroupsExact(t *testing.T) {
	cases := []struct{ name, tools string }{
		{"absent", ""},
		{"empty", `, "tools": []`},
		{"flat", `, "tools": [{"type":"function","name":"read","parameters":{"type":"object"}}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(` { "model": "openai/test", "previous_response_id": "resp_old", "input": [{"type":"function_call","namespace":"codex_app","name":"create_thread","call_id":"old_1","arguments":"{}"},{"type":"function_call_output","call_id":"old_1","output":"opaque ñ"}], "metadata":{"n":9007199254740993}` + tc.tools + ` } `)
			r := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Caller", "keep")
			headers, length := r.Header.Clone(), r.ContentLength
			bridge, err := prepareResponsesNamespaceBridge(r)
			if bridge != nil || err != nil {
				t.Fatalf("history without declared namespace groups must pass through: bridge=%v err=%v", bridge, err)
			}
			got, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Equal(got, raw) || r.ContentLength != length || !reflect.DeepEqual(r.Header, headers) {
				t.Fatal("history-only request bytes, length, or headers changed")
			}
		})
	}
}

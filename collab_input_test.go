package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const collabReportedArguments = `{"fork_context": false, "items": [], "message": "Averigua qué hora es ahora en Europe/Madrid y responde con la hora y la fecha exactas.", "model": "openai/gpt-6-luna", "reasoning_effort": "low"}`
const collabRepairedArguments = `{"fork_context":false,"message":"Averigua qué hora es ahora en Europe/Madrid y responde con la hora y la fecha exactas.","model":"openai/gpt-6-luna","reasoning_effort":"low"}`
const collabEmptyMessageArguments = `{"fork_context":false,"items":[{"type":"text","text":"Inspect the repository"}],"message":"","model":"openai/gpt-6-luna","reasoning_effort":"low"}`
const collabRepairedItemsArguments = `{"fork_context":false,"items":[{"type":"text","text":"Inspect the repository"}],"model":"openai/gpt-6-luna","reasoning_effort":"low"}`

func collabTool(name, namespace string) map[string]any {
	function := map[string]any{
		"type": "function", "name": name, "description": "Send an agent its task.", "strict": false,
		"parameters": map[string]any{"type": "object", "properties": map[string]any{
			"message": map[string]any{"type": "string"},
			"items":   map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
		}},
	}
	if namespace == "" {
		return function
	}
	return map[string]any{"type": "namespace", "name": namespace, "tools": []any{function}}
}

func collabCall(id, name, namespace, arguments string) map[string]any {
	item := map[string]any{"type": "function_call", "id": id, "call_id": "call_" + id, "name": name, "arguments": arguments}
	if namespace != "" {
		item["namespace"] = namespace
	}
	return item
}

func collabPrepare(t *testing.T, model string, tools []any, input []any) (*schemaBridge, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"model": model, "tools": tools, "input": input})
	if err != nil {
		t.Fatal(err)
	}
	b, _, doc := bridgedRequest(t, string(body))
	return b, doc
}

func collabAdapt(t *testing.T, b *schemaBridge, body, contentType string) string {
	t.Helper()
	r := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body))}
	if b != nil {
		if err := b.adaptResponse(r); err != nil {
			t.Fatal(err)
		}
	}
	defer r.Body.Close()
	result, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(result)
}

func collabJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func collabAssertArguments(t *testing.T, actual any, expected string) {
	t.Helper()
	got, ok := actual.(string)
	if !ok {
		t.Fatalf("arguments must remain a string, got %T", actual)
	}
	gotObject, gotErr := decodeObject([]byte(got))
	wantObject, wantErr := decodeObject([]byte(expected))
	if gotErr != nil || wantErr != nil || !reflect.DeepEqual(gotObject, wantObject) {
		t.Fatalf("arguments mismatch\ngot:  %s\nwant: %s", got, expected)
	}
}

func TestCollabInputJSONRepairsOnlyEmptyItemsAcrossProviders(t *testing.T) {
	for _, model := range []string{"openai/gpt-6-luna", "anthropic/claude-fable-5.1", "z-ai/glm-5.3"} {
		for _, name := range []string{"spawn_agent", "send_input"} {
			for _, dotted := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/dotted=%t", model, name, dotted), func(t *testing.T) {
					namespace := "multi_agent_v1"
					if dotted {
						name, namespace = namespace+"."+name, ""
					}
					tool := collabTool(name, namespace)
					prior := collabCall("prior", name, namespace, collabReportedArguments)
					b, doc := collabPrepare(t, model, []any{tool}, []any{prior})
					if b == nil {
						t.Fatal("declared Codex V1 input tool was not registered")
					}
					// Previously executed tool calls are historical facts, not new requests.
					if object(doc["input"].([]any)[0])["arguments"] != collabReportedArguments {
						t.Fatal("historical arguments changed")
					}
					body := collabJSON(t, map[string]any{"id": "resp", "output": []any{collabCall("new", name, namespace, collabReportedArguments)}})
					result, err := decodeObject([]byte(collabAdapt(t, b, body, "application/json")))
					if err != nil {
						t.Fatal(err)
					}
					item := object(result["output"].([]any)[0])
					collabAssertArguments(t, item["arguments"], collabRepairedArguments)
					if item["id"] != "new" || item["call_id"] != "call_new" || item["name"] != name || stringValue(item["namespace"]) != namespace {
						t.Fatal("call identity changed", item)
					}
				})
			}
		}
	}
}

func TestCollabInputJSONRepairsOnlyEmptyMessageAcrossProviders(t *testing.T) {
	for _, model := range []string{"openai/gpt-6-luna", "anthropic/claude-fable-5.1", "z-ai/glm-5.3"} {
		for _, name := range []string{"spawn_agent", "send_input"} {
			for _, dotted := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/dotted=%t", model, name, dotted), func(t *testing.T) {
					namespace := "multi_agent_v1"
					if dotted {
						name, namespace = namespace+"."+name, ""
					}
					b, doc := collabPrepare(t, model, []any{collabTool(name, namespace)}, []any{collabCall("prior", name, namespace, collabEmptyMessageArguments)})
					if b == nil || object(doc["input"].([]any)[0])["arguments"] != collabEmptyMessageArguments {
						t.Fatal("missing bridge or historical arguments changed")
					}
					body := collabJSON(t, map[string]any{"output": []any{collabCall("new", name, namespace, collabEmptyMessageArguments)}})
					result, err := decodeObject([]byte(collabAdapt(t, b, body, "application/json")))
					if err != nil {
						t.Fatal(err)
					}
					collabAssertArguments(t, object(result["output"].([]any)[0])["arguments"], collabRepairedItemsArguments)
				})
			}
		}
	}
}

func TestCollabInputPreservesAmbiguousMalformedAndMeaningfulArguments(t *testing.T) {
	for _, arguments := range []string{
		`{"message":"task","items":[{"type":"text","text":"another task"}]}`,
		`{"message":"","items":[]}`, `{"message":" \n\t ","items":[]}`,
		`{"message":null,"items":[]}`, `{"message":42,"items":[]}`,
		`{"message":"task","items":null}`, `{"message":"task","items":{}}`,
		`{"message":"task","items":"[]"}`, `{"message":"task","items":[null]}`,
		`{"message":"task"}`, `{"items":[]}`, `{"items":[{"type":"text","text":"task"}]}`,
		`{"message":" \n\t ","items":[{"type":"text","text":"task"}]}`,
		`{"message":null,"items":[{"type":"text","text":"task"}]}`,
		`{"message":"","items":[null]}`, `{"message":"","items":[42]}`,
		`{"message":"","items":[{}]}`, `{"message":"","items":["text"]}`,
		`{"message":"first","message":"second","items":[]}`,
		`{"message":"task","items":[{"type":"text","text":"keep me"}],"items":[]}`,
		`{"message":"task","items":[],"items":[{"type":"text","text":"keep me"}]}`,
		` { "message": "task", "items": []`, `{"message":"task","items":[]} {}`,
		`null`, `[]`, `"string"`, ``,
	} {
		t.Run(arguments, func(t *testing.T) {
			b, _ := collabPrepare(t, "openai/model", []any{collabTool("spawn_agent", "multi_agent_v1")}, nil)
			body := collabJSON(t, map[string]any{"output": []any{collabCall("one", "spawn_agent", "multi_agent_v1", arguments)}})
			result, err := decodeObject([]byte(collabAdapt(t, b, body, "application/json")))
			if err != nil {
				t.Fatal(err)
			}
			if got := object(result["output"].([]any)[0])["arguments"]; got != arguments {
				t.Fatalf("non-repairable arguments changed: %q => %q", arguments, got)
			}
			b, _ = collabPrepare(t, "openai/model", []any{collabTool("spawn_agent", "multi_agent_v1")}, nil)
			stream := collabAdapt(t, b, collabSSE(t, collabStreamEvents(t, []map[string]any{collabCall("one", "spawn_agent", "multi_agent_v1", arguments)})), "text/event-stream")
			var deltas string
			var completed bool
			for _, line := range strings.Split(stream, "\n") {
				if !strings.HasPrefix(line, "data: {") {
					continue
				}
				event, err := decodeObject([]byte(strings.TrimPrefix(line, "data: ")))
				if err != nil {
					t.Fatal(err)
				}
				if event["type"] == "response.function_call_arguments.delta" {
					deltas += stringValue(event["delta"])
				}
				if event["type"] == "response.function_call_arguments.done" {
					completed = true
					if event["arguments"] != arguments {
						t.Fatal("non-repairable streamed arguments changed", event)
					}
				}
			}
			if !completed || deltas != arguments {
				t.Fatalf("non-repairable streamed fragments changed: %q => %q", arguments, deltas)
			}
		})
	}
}

func TestCollabInputPreservesNumericPrecisionWhenRepairing(t *testing.T) {
	b, _ := collabPrepare(t, "openai/model", []any{collabTool("spawn_agent", "multi_agent_v1")}, nil)
	arguments := `{"message":"task","items":[],"exact":9007199254740993,"nested":{"limit":9007199254740995}}`
	body := collabJSON(t, map[string]any{"output": []any{collabCall("one", "spawn_agent", "multi_agent_v1", arguments)}})
	result, err := decodeObject([]byte(collabAdapt(t, b, body, "application/json")))
	if err != nil {
		t.Fatal(err)
	}
	collabAssertArguments(t, object(result["output"].([]any)[0])["arguments"], `{"message":"task","exact":9007199254740993,"nested":{"limit":9007199254740995}}`)
}

func TestCollabInputPreservesNumericPrecisionWhenRepairingEmptyMessage(t *testing.T) {
	b, _ := collabPrepare(t, "openai/model", []any{collabTool("spawn_agent", "multi_agent_v1")}, nil)
	arguments := `{"message":"","items":[{"type":"text","text":"task","exact":9007199254740993}],"nested":{"limit":9007199254740995}}`
	body := collabJSON(t, map[string]any{"output": []any{collabCall("one", "spawn_agent", "multi_agent_v1", arguments)}})
	result, err := decodeObject([]byte(collabAdapt(t, b, body, "application/json")))
	if err != nil {
		t.Fatal(err)
	}
	collabAssertArguments(t, object(result["output"].([]any)[0])["arguments"], `{"items":[{"type":"text","text":"task","exact":9007199254740993}],"nested":{"limit":9007199254740995}}`)
}

func TestCollabInputRecognizesGatewayNamespaceRepresentations(t *testing.T) {
	for _, dottedDeclaration := range []bool{false, true} {
		t.Run(fmt.Sprintf("dotted-declaration=%t", dottedDeclaration), func(t *testing.T) {
			name, namespace := "spawn_agent", "multi_agent_v1"
			if dottedDeclaration {
				name, namespace = "multi_agent_v1.spawn_agent", ""
			}
			b, _ := collabPrepare(t, "openai/model", []any{collabTool(name, namespace)}, nil)
			body := collabJSON(t, map[string]any{"output": []any{
				collabCall("namespace", "spawn_agent", "multi_agent_v1", collabReportedArguments),
				collabCall("dotted", "multi_agent_v1.spawn_agent", "", collabReportedArguments),
			}})
			result, err := decodeObject([]byte(collabAdapt(t, b, body, "application/json")))
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range result["output"].([]any) {
				collabAssertArguments(t, object(value)["arguments"], collabRepairedArguments)
			}
		})
	}
}

func TestCollabInputRequiresMatchingV1Declaration(t *testing.T) {
	v2 := collabTool("spawn_agent", "")
	delete(object(object(v2["parameters"])["properties"]), "items")
	wrongType := collabTool("spawn_agent", "multi_agent_v1")
	wrongFunction := object(wrongType["tools"].([]any)[0])
	object(object(wrongFunction["parameters"])["properties"])["items"] = map[string]any{"type": "string"}
	for _, test := range []struct {
		name, callName, namespace string
		tools                     []any
	}{
		{"undeclared", "spawn_agent", "multi_agent_v1", nil},
		{"different-namespace-declaration", "spawn_agent", "other", []any{collabTool("spawn_agent", "other")}},
		{"different-namespace-response", "spawn_agent", "other", []any{collabTool("spawn_agent", "multi_agent_v1")}},
		{"undeclared-send-input", "send_input", "multi_agent_v1", []any{collabTool("spawn_agent", "multi_agent_v1")}},
		{"unqualified-collision", "spawn_agent", "", []any{collabTool("spawn_agent", "multi_agent_v1")}},
		{"other-tool", "other_tool", "multi_agent_v1", []any{collabTool("other_tool", "multi_agent_v1")}},
		{"v2", "spawn_agent", "", []any{v2}},
		{"unqualified-same-schema", "spawn_agent", "", []any{collabTool("spawn_agent", "")}},
		{"wrong-items-schema", "spawn_agent", "multi_agent_v1", []any{wrongType}},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, _ := collabPrepare(t, "openai/model", test.tools, nil)
			body := collabJSON(t, map[string]any{"output": []any{collabCall("one", test.callName, test.namespace, collabReportedArguments)}})
			result, err := decodeObject([]byte(collabAdapt(t, b, body, "application/json")))
			if err != nil {
				t.Fatal(err)
			}
			if object(result["output"].([]any)[0])["arguments"] != collabReportedArguments {
				t.Fatal("unrelated or undeclared tool was normalized")
			}
		})
	}
}

func collabSSE(t *testing.T, events []map[string]any) string {
	t.Helper()
	var stream strings.Builder
	stream.WriteString(": keepalive\n\n")
	for i, event := range events {
		event["sequence_number"] = i
		fmt.Fprintf(&stream, "event: %s\r\ndata: %s\r\n\r\n", event["type"], collabJSON(t, event))
	}
	stream.WriteString("data: [DONE]\n\n")
	return stream.String()
}

func collabStreamEvents(t *testing.T, calls []map[string]any) []map[string]any {
	t.Helper()
	events := []map[string]any{{"type": "response.created", "response": map[string]any{"id": "resp"}}}
	for _, call := range calls {
		added := make(map[string]any)
		for key, value := range call {
			added[key] = value
		}
		added["arguments"] = ""
		events = append(events, map[string]any{"type": "response.output_item.added", "item": added})
	}
	// Interleave fragments from multiple calls to catch cross-call buffering mistakes.
	for part := 0; part < 2; part++ {
		for _, call := range calls {
			arguments := call["arguments"].(string)
			start, end := 0, len(arguments)/2
			if part == 1 {
				start, end = end, len(arguments)
			}
			events = append(events, map[string]any{"type": "response.function_call_arguments.delta", "item_id": call["id"], "delta": arguments[start:end]})
		}
	}
	var output []any
	for _, call := range calls {
		events = append(events,
			map[string]any{"type": "response.function_call_arguments.done", "item_id": call["id"], "arguments": call["arguments"]},
			map[string]any{"type": "response.output_item.done", "item": call},
		)
		output = append(output, call)
	}
	return append(events, map[string]any{"type": "response.completed", "response": map[string]any{"output": output}})
}

func TestCollabInputStreamingKeepsEveryArgumentRepresentationConsistent(t *testing.T) {
	for _, model := range []string{"openai/model", "anthropic/model"} {
		t.Run(model, func(t *testing.T) {
			b, _ := collabPrepare(t, model, []any{collabTool("spawn_agent", "multi_agent_v1"), collabTool("send_input", "multi_agent_v1")}, nil)
			if b == nil {
				t.Fatal("missing collaboration bridge")
			}
			calls := []map[string]any{
				collabCall("spawn", "spawn_agent", "multi_agent_v1", collabReportedArguments),
				collabCall("send", "send_input", "multi_agent_v1", `{"target":"child","message":"Continue","items":[]}`),
				collabCall("items", "spawn_agent", "multi_agent_v1", collabEmptyMessageArguments),
				collabCall("other", "spawn_agent", "other_namespace", collabReportedArguments),
				collabCall("both", "spawn_agent", "multi_agent_v1", `{"message":"text","items":[{"type":"text","text":"keep me"}]}`),
			}
			want := map[string]string{"spawn": collabRepairedArguments, "send": `{"target":"child","message":"Continue"}`, "items": collabRepairedItemsArguments, "other": collabReportedArguments, "both": calls[4]["arguments"].(string)}
			stream := collabAdapt(t, b, collabSSE(t, collabStreamEvents(t, calls)), "text/event-stream")
			if !strings.Contains(stream, ": keepalive") || !strings.Contains(stream, "data: [DONE]") {
				t.Fatal("stream framing lost")
			}
			deltas, counts, finished := map[string]string{}, map[string]int{}, map[string]int{}
			sequence := int64(0)
			for _, line := range strings.Split(stream, "\n") {
				if !strings.HasPrefix(line, "data: {") {
					continue
				}
				event, err := decodeObject([]byte(strings.TrimPrefix(line, "data: ")))
				if err != nil {
					t.Fatal(err)
				}
				gotSequence, err := event["sequence_number"].(json.Number).Int64()
				if err != nil || gotSequence != sequence {
					t.Fatal("SSE sequence is not contiguous", event)
				}
				sequence++
				switch event["type"] {
				case "response.function_call_arguments.delta":
					id := stringValue(event["item_id"])
					deltas[id] += stringValue(event["delta"])
					counts[id]++
				case "response.function_call_arguments.done":
					id := stringValue(event["item_id"])
					collabAssertArguments(t, event["arguments"], want[id])
					finished[id]++
				case "response.output_item.done":
					item := object(event["item"])
					id := stringValue(item["id"])
					collabAssertArguments(t, item["arguments"], want[id])
					finished[id]++
				case "response.completed":
					for _, value := range object(event["response"])["output"].([]any) {
						item := object(value)
						id := stringValue(item["id"])
						collabAssertArguments(t, item["arguments"], want[id])
						finished[id]++
					}
				}
			}
			for id, arguments := range want {
				collabAssertArguments(t, deltas[id], arguments)
				if finished[id] != 3 {
					t.Fatalf("missing completed arguments for %s: %d", id, finished[id])
				}
			}
			if counts["spawn"] != 1 || counts["send"] != 1 || counts["items"] != 1 || counts["other"] != 2 {
				t.Fatal("repairable calls must buffer fragments without buffering unrelated calls", counts)
			}
		})
	}
}

func TestCollabInputStreamingNonemptyAddedArguments(t *testing.T) {
	b, _ := collabPrepare(t, "openai/model", []any{collabTool("spawn_agent", "multi_agent_v1")}, nil)
	events := collabStreamEvents(t, []map[string]any{collabCall("one", "spawn_agent", "multi_agent_v1", collabReportedArguments)})
	object(events[1]["item"])["arguments"] = collabReportedArguments
	stream := collabAdapt(t, b, collabSSE(t, events), "text/event-stream")
	var accumulated string
	var added, completed int
	for _, line := range strings.Split(stream, "\n") {
		if strings.HasPrefix(line, "data: {") {
			event, err := decodeObject([]byte(strings.TrimPrefix(line, "data: ")))
			if err != nil {
				t.Fatal(err)
			}
			switch event["type"] {
			case "response.output_item.added":
				added++
				arguments := stringValue(object(event["item"])["arguments"])
				if arguments != "" {
					t.Fatal("added must defer full arguments until the normalized delta")
				}
				accumulated += arguments
			case "response.function_call_arguments.delta":
				accumulated += stringValue(event["delta"])
			case "response.function_call_arguments.done":
				completed++
				collabAssertArguments(t, event["arguments"], collabRepairedArguments)
			case "response.output_item.done":
				completed++
				collabAssertArguments(t, object(event["item"])["arguments"], collabRepairedArguments)
			case "response.completed":
				completed++
				collabAssertArguments(t, object(object(event["response"])["output"].([]any)[0])["arguments"], collabRepairedArguments)
			}
		}
	}
	if added != 1 || completed != 3 {
		t.Fatalf("stream events disappeared: added=%d completed=%d", added, completed)
	}
	collabAssertArguments(t, accumulated, collabRepairedArguments)
}

func TestCollabInputCoexistsWithAnthropicSchemaEnvelope(t *testing.T) {
	for _, dottedDeclaration := range []bool{false, true} {
		for _, dottedResponse := range []bool{false, true} {
			t.Run(fmt.Sprintf("declaration-dotted=%t/response-dotted=%t", dottedDeclaration, dottedResponse), func(t *testing.T) {
				newBridge := func() *schemaBridge {
					name, namespace := "spawn_agent", "multi_agent_v1"
					if dottedDeclaration {
						name, namespace = "multi_agent_v1.spawn_agent", ""
					}
					tool := collabTool(name, namespace)
					function := tool
					if namespace != "" {
						function = object(tool["tools"].([]any)[0])
					}
					object(function["parameters"])["allOf"] = []any{map[string]any{"required": []any{"message"}}}
					b, doc := collabPrepare(t, "anthropic/model", []any{tool}, nil)
					if b == nil {
						t.Fatal("missing combined bridge")
					}
					wrappedTool := object(doc["tools"].([]any)[0])
					if namespace != "" {
						wrappedTool = object(wrappedTool["tools"].([]any)[0])
					}
					if object(object(wrappedTool["parameters"])["properties"])[toolEnvelope] == nil {
						t.Fatal("Anthropic envelope was not applied")
					}
					return b
				}
				name, namespace := "spawn_agent", "multi_agent_v1"
				if dottedResponse {
					name, namespace = "multi_agent_v1.spawn_agent", ""
				}
				wrapped := `{"kilo_tool_input":` + collabReportedArguments + `}`
				call := collabCall("wrapped", name, namespace, wrapped)
				body := collabJSON(t, map[string]any{"output": []any{call}})
				result, err := decodeObject([]byte(collabAdapt(t, newBridge(), body, "application/json")))
				if err != nil {
					t.Fatal(err)
				}
				collabAssertArguments(t, object(result["output"].([]any)[0])["arguments"], collabRepairedArguments)
				stream := collabAdapt(t, newBridge(), collabSSE(t, collabStreamEvents(t, []map[string]any{call})), "text/event-stream")
				if strings.Contains(stream, toolEnvelope) {
					t.Fatal("wrapper leaked into collaboration arguments")
				}
				for _, line := range strings.Split(stream, "\n") {
					if !strings.HasPrefix(line, "data: {") {
						continue
					}
					event, _ := decodeObject([]byte(strings.TrimPrefix(line, "data: ")))
					if event["type"] == "response.function_call_arguments.done" {
						collabAssertArguments(t, event["arguments"], collabRepairedArguments)
						return
					}
				}
				t.Fatal("combined streamed arguments disappeared")
			})
		}
	}
}

func TestCollabInputProxyEndToEnd(t *testing.T) {
	for _, test := range []struct{ name, arguments, expected string }{
		{"empty-items", collabReportedArguments, collabRepairedArguments},
		{"empty-message", collabEmptyMessageArguments, collabRepairedItemsArguments},
	} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", test.name, streaming), func(t *testing.T) {
				call := collabCall("one", "spawn_agent", "multi_agent_v1", test.arguments)
				upstreamBody, contentType := collabJSON(t, map[string]any{"output": []any{call}}), "application/json"
				if streaming {
					upstreamBody, contentType = collabSSE(t, collabStreamEvents(t, []map[string]any{call})), "text/event-stream"
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", contentType)
					io.WriteString(w, upstreamBody)
				}))
				defer upstream.Close()
				target, _ := url.Parse(upstream.URL)
				a := &app{upstream: target, transport: http.DefaultTransport.(*http.Transport).Clone()}
				body := collabJSON(t, map[string]any{"model": "openai/model", "tools": []any{collabTool("spawn_agent", "multi_agent_v1")}, "input": "Inspect this repository", "stream": streaming})
				r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8877/v1/responses", bytes.NewBufferString(body))
				r.Header.Set("Authorization", "Bearer local-key")
				w := httptest.NewRecorder()
				a.inferenceHandler("kilo-key", "team", "local-key", "127.0.0.1:8877").ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatal(w.Code, w.Body.String())
				}
				if streaming {
					for _, line := range strings.Split(w.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data: {") {
							continue
						}
						event, _ := decodeObject([]byte(strings.TrimPrefix(line, "data: ")))
						if event["type"] == "response.function_call_arguments.done" {
							collabAssertArguments(t, event["arguments"], test.expected)
							return
						}
					}
					t.Fatal("proxy lost arguments.done")
				}
				result, err := decodeObject(w.Body.Bytes())
				if err != nil {
					t.Fatal(err)
				}
				collabAssertArguments(t, object(result["output"].([]any)[0])["arguments"], test.expected)
			})
		}
	}
}

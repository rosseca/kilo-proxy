package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const unionRequest = `{"model":"anthropic/claude-fable-5.1","input":[{"type":"function_call","call_id":"prior","name":"choice","arguments":"{\"mode\":\"read\"}"},{"type":"function_call_output","call_id":"prior","output":"unchanged result"}],"tools":[{"type":"function","name":"choice","parameters":{"oneOf":[{"type":"object","properties":{"mode":{"const":"read"}},"required":["mode"],"additionalProperties":false},{"type":"object","properties":{"mode":{"const":"write"},"value":{"type":"string"}},"required":["mode","value"],"additionalProperties":false}]}},{"type":"function","name":"plain","parameters":{"type":"object","properties":{"id":{"anyOf":[{"type":"string"},{"type":"null"}]}}}}],"reasoning":{"effort":"xhigh"},"metadata":{"exact":9007199254740993}}`

func bridgedRequest(t *testing.T, body string) (*schemaBridge, *http.Request, map[string]any) {
	t.Helper()
	r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/responses", strings.NewReader(body))
	b, err := prepareSchemaBridge(r)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(raw))
	doc, err := decodeObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	return b, r, doc
}
func TestSchemaBridgePreservesUnionAndHistory(t *testing.T) {
	b, r, doc := bridgedRequest(t, unionRequest)
	if b == nil {
		t.Fatal("missing bridge")
	}
	if object(doc["reasoning"])["effort"] != "xhigh" {
		t.Fatal("reasoning effort changed")
	}
	tools := doc["tools"].([]any)
	schema := object(object(tools[0])["parameters"])
	if needsEnvelope(schema) {
		t.Fatal("top-level combinator retained")
	}
	original, _ := decodeObject([]byte(unionRequest))
	originalSchema := object(original["tools"].([]any)[0])["parameters"]
	want, _ := json.Marshal(originalSchema)
	got, _ := json.Marshal(object(schema["properties"])[toolEnvelope])
	if !bytes.Equal(want, got) {
		t.Fatal("original constraints changed")
	}
	before, _ := json.Marshal(original["tools"].([]any)[1])
	after, _ := json.Marshal(tools[1])
	if !bytes.Equal(before, after) {
		t.Fatal("nested-only union changed")
	}
	if object(doc["metadata"])["exact"].(json.Number).String() != "9007199254740993" {
		t.Fatal("number precision lost")
	}
	history := doc["input"].([]any)
	args, err := unwrapArguments(object(history[0])["arguments"])
	if err != nil || args != `{"mode":"read"}` {
		t.Fatalf("history: %s %v", args, err)
	}
	if object(history[1])["output"] != "unchanged result" {
		t.Fatal("tool result changed")
	}
	raw, _ := io.ReadAll(r.Body)
	if r.ContentLength != int64(len(raw)) {
		t.Fatal("content length not updated")
	}
}
func TestSchemaBridgeLeavesOtherModelsAndRoutesUntouched(t *testing.T) {
	for _, model := range []string{"openai/gpt-test", "anthropic-test/model", "vendor/claude"} {
		body := strings.Replace(unionRequest, "anthropic/claude-fable-5.1", model, 1)
		b, r, _ := bridgedRequest(t, body)
		if b != nil {
			t.Fatal("unexpected bridge", model)
		}
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != body {
			t.Fatal("body changed")
		}
	}
	for _, path := range []string{"/v1/messages", "/v1/chat/completions"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(unionRequest))
		b, err := prepareSchemaBridge(r)
		if err != nil || b != nil {
			t.Fatal("other route changed")
		}
	}
}
func TestSchemaBridgeRefsAndNamespaces(t *testing.T) {
	body := `{"model":"anthropic/model","tools":[{"type":"namespace","name":"team","tools":[{"type":"function","name":"pick","parameters":{"$defs":{"x":{"type":"object"}},"anyOf":[{"$ref":"#/$defs/x"}]}}]}],"input":[]}`
	b, _, doc := bridgedRequest(t, body)
	if !b.tools[toolKey("team", "pick")] || b.tools[toolKey("", "pick")] {
		t.Fatal("namespace lost")
	}
	raw, _ := json.Marshal(doc)
	if !bytes.Contains(raw, []byte(`#/properties/kilo_tool_input/$defs/x`)) {
		t.Fatal("ref not relocated")
	}
	if b.matches(map[string]any{"type": "function_call", "name": "pick", "namespace": "different"}) {
		t.Fatal("namespace collision")
	}
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(strings.Replace(body, "#/$defs/x", "https://example.com/schema", 1)))
	if _, err := prepareSchemaBridge(r); err == nil {
		t.Fatal("external ref accepted without resolver")
	}
}
func TestSchemaBridgeStreamingToolCalls(t *testing.T) {
	b, _, _ := bridgedRequest(t, unionRequest)
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp"}},
		{"type": "response.output_item.added", "item": map[string]any{"type": "function_call", "id": "fc1", "name": "choice", "arguments": ""}},
		{"type": "response.function_call_arguments.delta", "item_id": "fc1", "delta": "{\"kilo_tool_input\":"},
		{"type": "response.function_call_arguments.delta", "item_id": "fc1", "delta": "{\"mode\":\"read\"}}"},
		{"type": "response.function_call_arguments.done", "item_id": "fc1", "arguments": `{"kilo_tool_input":{"mode":"read"}}`},
		{"type": "response.output_item.done", "item": map[string]any{"type": "function_call", "id": "fc1", "name": "choice", "arguments": `{"kilo_tool_input":{"mode":"read"}}`}},
		{"type": "response.completed", "response": map[string]any{"output": []any{map[string]any{"type": "function_call", "id": "fc1", "name": "choice", "arguments": `{"kilo_tool_input":{"mode":"read"}}`}}}},
	}
	var input, output bytes.Buffer
	input.WriteString(": keepalive\n\n")
	for _, e := range events {
		raw, _ := json.Marshal(e)
		input.WriteString("event: " + stringValue(e["type"]) + "\r\ndata: " + string(raw) + "\r\n\r\n")
	}
	input.WriteString("data: [DONE]\n\n")
	if err := b.transformSSE(&input, &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	var sequence int64
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		event, err := decodeObject([]byte(strings.TrimPrefix(line, "data: ")))
		if err != nil {
			t.Fatal(err)
		}
		value, err := event["sequence_number"].(json.Number).Int64()
		if err != nil || value != sequence {
			t.Fatal("non-contiguous SSE sequence", event)
		}
		sequence++
	}
	if strings.Contains(text, toolEnvelope) {
		t.Fatal("envelope leaked to client")
	}
	if strings.Count(text, "event: response.function_call_arguments.delta") != 1 {
		t.Fatal("expected one validated delta")
	}
	if !strings.Contains(text, `"delta":"{\"mode\":\"read\"}"`) {
		t.Fatal("wrong arguments delta", text)
	}
	if !strings.Contains(text, ": keepalive") || !strings.Contains(text, "data: [DONE]") {
		t.Fatal("SSE control data lost")
	}
}
func TestSchemaBridgeRejectsInvalidArguments(t *testing.T) {
	for _, args := range []string{`{"mode":"read"}`, `{"kilo_tool_input":null}`, `{"kilo_tool_input":{},"extra":true}`, `{"kilo_tool_input":[]}`, `not json`} {
		if _, err := unwrapArguments(args); err == nil {
			t.Fatal("invalid envelope accepted", args)
		}
	}
	b, _, _ := bridgedRequest(t, unionRequest)
	b.calls["fc1"] = true
	out, err := b.transformEvent([]byte(`{"type":"response.function_call_arguments.done","item_id":"fc1","arguments":"{}"}`))
	if err == nil || len(out) > 0 {
		t.Fatal("invalid tool call exposed")
	}
}
func TestSchemaBridgeEndToEndJSON(t *testing.T) {
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		received, _ = decodeObject(raw)
		if needsEnvelope(object(object(received["tools"].([]any)[0])["parameters"])) {
			http.Error(w, "top-level union", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp","output":[{"type":"function_call","id":"fc1","call_id":"call1","name":"choice","arguments":"{\"kilo_tool_input\":{\"mode\":\"read\"}}"}]}`)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	a := &app{upstream: target, transport: http.DefaultTransport.(*http.Transport).Clone()}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/responses", strings.NewReader(unionRequest))
	r.Header.Set("Authorization", "Bearer local-key")
	w := httptest.NewRecorder()
	a.inferenceHandler("kilo-key", "team", "local-key", "127.0.0.1:8877").ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	doc, err := decodeObject(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	args := object(doc["output"].([]any)[0])["arguments"]
	if args != `{"mode":"read"}` {
		t.Fatal(args)
	}
}
func TestSchemaBridgeStreamingCloseCancelsRead(t *testing.T) {
	b, _, _ := bridgedRequest(t, unionRequest)
	source, writer := io.Pipe()
	defer writer.Close()
	r := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: source, Request: httptest.NewRequest("POST", "/", nil).WithContext(context.Background())}
	if err := b.adaptResponse(r); err != nil {
		t.Fatal(err)
	}
	if err := r.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("data: {}\n\n")); err == nil {
		t.Fatal("upstream body not closed")
	}
}

func TestSchemaBridgeAllCombinatorsAndLiteralData(t *testing.T) {
	for _, keyword := range []string{"oneOf", "anyOf", "allOf"} {
		schema := map[string]any{keyword: []any{map[string]any{"type": "object", "properties": map[string]any{"$id": map[string]any{"const": map[string]any{"$ref": "literal data", "$id": "literal ID"}}}}}}
		doc := map[string]any{"model": "anthropic/test", "tools": []any{map[string]any{"type": "function", "name": "literal", "parameters": schema}}}
		raw, _ := json.Marshal(doc)
		_, _, adapted := bridgedRequest(t, string(raw))
		wrapped := object(object(adapted["tools"].([]any)[0])["parameters"])
		got, _ := json.Marshal(object(wrapped["properties"])[toolEnvelope])
		want, _ := json.Marshal(schema)
		if !bytes.Equal(got, want) {
			t.Fatal("literal data changed", keyword, string(got))
		}
	}
}
func TestSchemaBridgeLeavesUnrelatedStreamingCallsAlone(t *testing.T) {
	b, _, _ := bridgedRequest(t, unionRequest)
	raw := []byte(`{"type":"response.function_call_arguments.delta","item_id":"unrelated","delta":"partial"}`)
	out, err := b.transformEvent(raw)
	if err != nil || len(out) != 1 {
		t.Fatal(err)
	}
	got, _ := decodeObject(out[0])
	if got["delta"] != "partial" {
		t.Fatal("unrelated call buffered")
	}
	r := &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"upstream"}`))}
	if err := b.adaptResponse(r); err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(r.Body)
	if string(data) != `{"error":"upstream"}` {
		t.Fatal("upstream error changed")
	}
}

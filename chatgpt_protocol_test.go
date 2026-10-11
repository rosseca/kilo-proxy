package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func cgPrepareTest(t *testing.T, path, body string) (map[string]any, *chatGPTProtocol) {
	t.Helper()
	data, p, err := prepareChatGPTRequest(path, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	d, err := decodeObject(data)
	if err != nil {
		t.Fatal(err)
	}
	return d, p
}

func TestChatGPTProtocolMessagesOrderedToolsAndImages(t *testing.T) {
	d, p := cgPrepareTest(t, "/v1/messages", `{"model":"gpt-test","stream":true,"system":[{"type":"text","text":"system","cache_control":{"type":"ephemeral"}}],"thinking":{"type":"adaptive"},"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"messages":[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]},{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"opaque"},{"type":"text","text":"before"},{"type":"tool_use","id":"call_a","name":"first","input":{"x":1}},{"type":"text","text":"between"},{"type":"tool_use","id":"call_b","name":"second","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_a","content":[{"type":"image","source":{"type":"url","url":"https://example.test/result.png"}}]},{"type":"tool_result","tool_use_id":"call_b","content":"failed","is_error":true},{"type":"text","text":"after"}]}],"tools":[{"type":"custom","name":"first","input_schema":{"type":"object"}}],"tool_choice":{"type":"any","disable_parallel_tool_use":true}}`)
	if !p.stream || d["store"] != false || d["tool_choice"] != "required" || d["parallel_tool_calls"] != false {
		t.Fatalf("flags: %#v", d)
	}
	input := d["input"].([]any)
	if len(input) != 9 {
		t.Fatalf("ordered input: %#v", input)
	}
	if object(input[3])["call_id"] != "call_a" || object(input[5])["call_id"] != "call_b" || object(input[6])["call_id"] != "call_a" || object(input[7])["output"] != "Tool error: failed" {
		t.Fatalf("lost tool order: %#v", input)
	}
	user := object(input[1])["content"].([]any)
	if object(user[1])["image_url"] != "data:image/png;base64,AA==" {
		t.Fatalf("image: %#v", user)
	}
	toolImage := object(input[6])["output"].([]any)
	if object(toolImage[0])["image_url"] != "https://example.test/result.png" {
		t.Fatalf("tool result image: %#v", toolImage)
	}
	if object(d["tools"].([]any)[0])["type"] != "function" {
		t.Fatal("tool was not converted")
	}
}

func TestChatGPTProtocolChatRoundtripRequest(t *testing.T) {
	d, _ := cgPrepareTest(t, "/v1/chat/completions", `{"model":"gpt-test","messages":[{"role":"system","content":"rules"},{"role":"user","content":"read"},{"role":"assistant","content":null,"tool_calls":[{"type":"function","id":"a","function":{"name":"read","arguments":"{\"path\":\"a\"}"}},{"type":"function","id":"b","function":{"name":"read","arguments":"{\"path\":\"b\"}"}}]},{"role":"tool","tool_call_id":"b","content":"B"},{"role":"tool","tool_call_id":"a","content":"A"}],"tools":[{"type":"function","function":{"name":"read","description":"Read","parameters":{"type":"object"},"strict":false}}],"tool_choice":{"type":"function","function":{"name":"read"}},"reasoning_effort":"low","response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object"},"strict":true}}}`)
	input := d["input"].([]any)
	if len(input) != 6 || object(input[0])["role"] != "developer" || object(input[4])["call_id"] != "b" || object(input[5])["output"] != "A" {
		t.Fatalf("input: %#v", input)
	}
	if object(d["tool_choice"])["name"] != "read" || object(d["reasoning"])["effort"] != "low" || object(object(d["text"])["format"])["name"] != "answer" {
		t.Fatalf("options: %#v", d)
	}
}

func TestChatGPTProtocolRejectsUnsupported(t *testing.T) {
	for _, extra := range []string{
		`"stop":["STOP"]`, `"n":2`, `"context_management":{"edits":[{"type":"clear_tool_uses_20250919"}]}`, `"tools":[{"type":"web_search_20250305","name":"web_search"}]`,
	} {
		if _, _, err := prepareChatGPTRequest("/v1/messages", []byte(`{"model":"test","messages":[],`+extra+`}`)); err == nil {
			t.Errorf("accepted %s", extra)
		}
	}
	for _, content := range []string{`[{"type":"document","source":{"type":"url","url":"x"}}]`, `[{"type":"input_audio","input_audio":{}}]`, `{}`} {
		if _, _, err := prepareChatGPTRequest("/v1/messages", []byte(`{"model":"test","messages":[{"role":"user","content":`+content+`}]}`)); err == nil {
			t.Errorf("accepted content %s", content)
		}
	}
}

func TestChatGPTProtocolPreservesToolStrictness(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/chat/completions"} {
		for _, strict := range []any{nil, false, true} {
			tool := map[string]any{"name": "read", "parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "limit": map[string]any{"type": "number"}}, "required": []any{"path"}}}
			if strict != nil {
				tool["strict"] = strict
			}
			var wireTool any = tool
			if path == "/v1/messages" {
				tool["input_schema"] = tool["parameters"]
				delete(tool, "parameters")
			} else {
				wireTool = map[string]any{"type": "function", "function": tool}
			}
			request, _ := json.Marshal(map[string]any{"model": "chatgpt/test", "messages": []any{}, "tools": []any{wireTool}})
			converted, _, err := prepareChatGPTRequest(path, request)
			if err != nil {
				t.Fatal(err)
			}
			d, _ := decodeObject(converted)
			got := object(d["tools"].([]any)[0])
			want := strict
			if want == nil {
				want = false
			}
			if got["strict"] != want {
				t.Fatalf("%s strict=%v became %v", path, strict, got["strict"])
			}
			if len(object(got["parameters"])["required"].([]any)) != 1 {
				t.Fatal("optional tool argument became required")
			}
		}
	}
	raw := []byte(`{"model":"chatgpt/test","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}],"input":[]}`)
	converted, _, err := prepareChatGPTRequest("/v1/responses", raw)
	if err != nil || string(converted) != string(raw) {
		t.Fatalf("native Responses strict semantics changed: %s %v", converted, err)
	}
}

func cgTestResponse() map[string]any {
	return map[string]any{"id": "resp_123", "created_at": int64(123), "status": "completed", "output": []any{
		map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Read files."}}},
		map[string]any{"id": "fc_a", "type": "function_call", "call_id": "call_a", "name": "read", "arguments": `{"path":"a"}`},
		map[string]any{"id": "fc_b", "type": "function_call", "call_id": "call_b", "name": "read", "arguments": `{"path":"b"}`},
	}, "usage": map[string]any{"input_tokens": 100, "output_tokens": 20, "input_tokens_details": map[string]any{"cached_tokens": 40}, "output_tokens_details": map[string]any{"reasoning_tokens": 5}}}
}
func cgFrame(event map[string]any) string {
	b, _ := json.Marshal(event)
	return "data: " + string(b) + "\n\n"
}
func cgTestSSE() string {
	r := cgTestResponse()
	output := r["output"].([]any)
	var s strings.Builder
	s.WriteString(": keepalive\n\n")
	s.WriteString(cgFrame(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_123", "created_at": 123}}))
	s.WriteString(cgFrame(map[string]any{"type": "response.output_text.delta", "item_id": "msg_1", "content_index": 0, "delta": "Read "}))
	s.WriteString(cgFrame(map[string]any{"type": "response.output_text.delta", "item_id": "msg_1", "content_index": 0, "delta": "files."}))
	s.WriteString(cgFrame(map[string]any{"type": "response.content_part.done", "item_id": "msg_1", "content_index": 0}))
	for _, raw := range output[1:] {
		item := object(raw)
		s.WriteString(cgFrame(map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": item["id"], "type": "function_call", "call_id": item["call_id"], "name": item["name"], "arguments": ""}}))
	}
	// Parallel calls deliberately interleave their fragments.
	for _, raw := range output[1:] {
		item := object(raw)
		args := stringValue(item["arguments"])
		s.WriteString(cgFrame(map[string]any{"type": "response.function_call_arguments.delta", "item_id": item["id"], "delta": args[:8]}))
	}
	for _, raw := range output[1:] {
		item := object(raw)
		args := stringValue(item["arguments"])
		s.WriteString(cgFrame(map[string]any{"type": "response.function_call_arguments.delta", "item_id": item["id"], "delta": args[8:]}))
		s.WriteString(cgFrame(map[string]any{"type": "response.output_item.done", "item": item}))
	}
	s.WriteString(cgFrame(map[string]any{"type": "response.completed", "response": r}))
	return s.String()
}
func cgServeTest(t *testing.T, p *chatGPTProtocol, body string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	w := httptest.NewRecorder()
	err := p.serve(w, httptest.NewRequest("POST", p.path, nil), &http.Response{Body: io.NopCloser(strings.NewReader(body))})
	return w, err
}

func TestChatGPTProtocolBufferedResponses(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/v1/responses"} {
		t.Run(path, func(t *testing.T) {
			w, err := cgServeTest(t, &chatGPTProtocol{path: path, model: "gpt-test"}, cgTestSSE())
			if err != nil {
				t.Fatal(err)
			}
			d, err := decodeObject(w.Body.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if d["id"] != "resp_123" {
				t.Fatal(d)
			}
			switch path {
			case "/v1/messages":
				if d["stop_reason"] != "tool_use" || len(d["content"].([]any)) != 3 || cgNumber(object(d["usage"])["input_tokens"]) != 60 || cgNumber(object(d["usage"])["cache_read_input_tokens"]) != 40 {
					t.Fatal(d)
				}
			case "/v1/chat/completions":
				choice := object(d["choices"].([]any)[0])
				message := object(choice["message"])
				if choice["finish_reason"] != "tool_calls" || message["content"] != "Read files." || len(message["tool_calls"].([]any)) != 2 {
					t.Fatal(d)
				}
			case "/v1/responses":
				if len(d["output"].([]any)) != 3 {
					t.Fatal(d)
				}
			}
		})
	}
}

func TestChatGPTProtocolBufferedEmptyTerminalOutput(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"} {
		t.Run(path, func(t *testing.T) {
			response := cgTestResponse()
			output := response["output"].([]any)
			response["output"] = []any{}
			// Completion order may differ from output order for parallel tools.
			body := cgFrame(map[string]any{"type": "response.output_item.done", "output_index": 2, "item": output[2]}) + cgFrame(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": output[0]}) + cgFrame(map[string]any{"type": "response.output_item.done", "output_index": 1, "item": output[1]}) + cgFrame(map[string]any{"type": "response.completed", "response": response})
			w, err := cgServeTest(t, &chatGPTProtocol{path: path, model: "test"}, body)
			if err != nil {
				t.Fatal(err)
			}
			d, _ := decodeObject(w.Body.Bytes())
			if !strings.Contains(w.Body.String(), "Read files.") || !strings.Contains(w.Body.String(), "call_a") || !strings.Contains(w.Body.String(), "call_b") {
				t.Fatal("lost completed output", w.Body.String())
			}
			if path == "/v1/responses" {
				items := d["output"].([]any)
				if object(items[0])["id"] != "msg_1" || object(items[1])["call_id"] != "call_a" {
					t.Fatal("lost output order")
				}
			}
		})
	}
}

func TestChatGPTProtocolBufferedNativeItemsAndAuthoritativeTerminal(t *testing.T) {
	for _, authoritative := range []bool{false, true} {
		reasoning := map[string]any{"id": "rs_synthetic", "type": "reasoning", "encrypted_content": "synthetic-opaque"}
		custom := map[string]any{"id": "ct_synthetic", "type": "custom_tool_call", "call_id": "call_synthetic", "name": "apply_patch", "input": "synthetic patch"}
		response := map[string]any{"id": "resp_synthetic", "status": "completed", "output": []any{}}
		if authoritative {
			response["output"] = []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "authoritative"}}}}
		}
		body := cgFrame(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": reasoning}) + cgFrame(map[string]any{"type": "response.output_item.done", "output_index": 1, "item": custom}) + cgFrame(map[string]any{"type": "response.completed", "response": response})
		w, err := cgServeTest(t, &chatGPTProtocol{path: "/v1/responses", model: "test"}, body)
		if err != nil {
			t.Fatal(err)
		}
		d, _ := decodeObject(w.Body.Bytes())
		items := d["output"].([]any)
		if authoritative {
			if len(items) != 1 || !strings.Contains(w.Body.String(), "authoritative") {
				t.Fatal("overwrote populated final output")
			}
		} else {
			if len(items) != 2 || object(items[0])["encrypted_content"] != "synthetic-opaque" || object(items[1])["input"] != "synthetic patch" {
				t.Fatal("lost native output items")
			}
		}
	}
}

func TestChatGPTProtocolStreamingResponses(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
		t.Run(path, func(t *testing.T) {
			w, err := cgServeTest(t, &chatGPTProtocol{path: path, model: "gpt-test", stream: true, includeUsage: true}, cgTestSSE())
			if err != nil {
				t.Fatal(err)
			}
			var events []map[string]any
			if err := cgReadSSE(strings.NewReader(w.Body.String()), func(b []byte) error { d, e := decodeObject(b); events = append(events, d); return e }); err != nil {
				t.Fatal(err)
			}
			args := map[int64]string{}
			ids := map[int64]string{}
			text := ""
			stop := ""
			for _, e := range events {
				if path == "/v1/messages" {
					index := cgNumber(e["index"])
					block := object(e["content_block"])
					if block["type"] == "tool_use" {
						ids[index] = stringValue(block["id"])
					}
					delta := object(e["delta"])
					text += stringValue(delta["text"])
					args[index] += stringValue(delta["partial_json"])
					if s := stringValue(delta["stop_reason"]); s != "" {
						stop = s
					}
				} else {
					choices, _ := e["choices"].([]any)
					if len(choices) == 0 {
						continue
					}
					choice := object(choices[0])
					delta := object(choice["delta"])
					text += stringValue(delta["content"])
					calls, _ := delta["tool_calls"].([]any)
					for _, raw := range calls {
						call := object(raw)
						index := cgNumber(call["index"])
						if id := stringValue(call["id"]); id != "" {
							ids[index] = id
						}
						args[index] += stringValue(object(call["function"])["arguments"])
					}
					if s := stringValue(choice["finish_reason"]); s != "" {
						stop = s
					}
				}
			}
			if text != "Read files." || len(ids) != 2 || (stop != "tool_calls" && stop != "tool_use") {
				t.Fatalf("text=%s ids=%v stop=%s\n%s", text, ids, stop, w.Body.String())
			}
			for i, id := range ids {
				want := `{"path":"a"}`
				if id == "call_b" {
					want = `{"path":"b"}`
				}
				if args[i] != want {
					t.Errorf("%s args=%s", id, args[i])
				}
			}
		})
	}
}

func TestChatGPTProtocolFinalOnlyAndRawPassthrough(t *testing.T) {
	final := cgFrame(map[string]any{"type": "response.completed", "response": cgTestResponse()})
	w, err := cgServeTest(t, &chatGPTProtocol{path: "/v1/messages", model: "test", stream: true}, final)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Body.String(), `"text":"Read files."`) || !strings.Contains(w.Body.String(), `"partial_json":"{\"path\":\"a\"}"`) {
		t.Fatal(w.Body.String())
	}
	raw := `{"model":"gpt-test","stream":true,"tools":[{"type":"custom","name":"apply_patch"}],"input":[]}`
	prepared, p, err := prepareChatGPTRequest("/v1/responses", []byte(raw))
	if err != nil || string(prepared) != raw {
		t.Fatalf("raw preparation: %s %v", prepared, err)
	}
	w, err = cgServeTest(t, p, cgTestSSE())
	if err != nil || w.Body.String() != cgTestSSE() {
		t.Fatalf("passthrough: %v", err)
	}
}

func TestChatGPTProtocolRawStreamRequiresTerminalEvent(t *testing.T) {
	for _, stream := range []string{
		cgFrame(map[string]any{"type": "response.created", "response": map[string]any{"id": "x"}}),
		cgFrame(map[string]any{"type": "response.failed", "response": map[string]any{"status": "failed", "error": map[string]any{"message": "failed"}}}),
		cgFrame(map[string]any{"type": "response.done", "response": map[string]any{"status": "cancelled"}}),
		cgFrame(map[string]any{"type": "response.completed"}),
	} {
		w, err := cgServeTest(t, &chatGPTProtocol{path: "/v1/responses", model: "test", stream: true}, stream)
		if err == nil {
			t.Fatal("invalid raw stream marked successful")
		}
		if w.Body.String() != stream {
			t.Fatal("raw stream bytes changed")
		}
	}
	for _, kind := range []string{"response.completed", "response.incomplete", "response.done"} {
		stream := cgFrame(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "input": "patch"}}) + cgFrame(map[string]any{"type": kind, "response": map[string]any{"id": "resp_ok"}})
		w, err := cgServeTest(t, &chatGPTProtocol{path: "/v1/responses", model: "test", stream: true}, stream)
		if err != nil || w.Body.String() != stream {
			t.Fatalf("terminal %s: %v", kind, err)
		}
	}
}

func TestChatGPTProtocolFailuresAndIncomplete(t *testing.T) {
	for _, body := range []string{`data: {"type":"response.failed","response":{"error":{"message":"quota exhausted"}}}` + "\n\n", `data: {"type":"response.created","response":{"id":"x"}}` + "\n\n"} {
		for _, stream := range []bool{false, true} {
			w, err := cgServeTest(t, &chatGPTProtocol{path: "/v1/messages", model: "test", stream: stream}, body)
			if err == nil {
				t.Fatal("accepted failed/truncated stream")
			}
			if stream && !strings.Contains(w.Body.String(), "event: error") {
				t.Fatal("missing streaming error", w.Body.String())
			}
			if !stream && w.Body.Len() != 0 {
				t.Fatal("wrote successful buffered response")
			}
		}
	}
	r := cgTestResponse()
	r["status"] = "incomplete"
	w, err := cgServeTest(t, &chatGPTProtocol{path: "/v1/messages", model: "test"}, cgFrame(map[string]any{"type": "response.incomplete", "response": r}))
	if err != nil || !strings.Contains(w.Body.String(), `"stop_reason":"max_tokens"`) {
		t.Fatalf("incomplete: %v %s", err, w.Body.String())
	}
}

func TestChatGPTProtocolClaudeAliasAndCancellation(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	r = r.WithContext(context.WithValue(r.Context(), claudeDesktopAliasContextKey{}, claudeDesktopAliasRoute{Alias: "claude-kilo-alias", Model: "gpt-test"}))
	w := httptest.NewRecorder()
	p := &chatGPTProtocol{path: "/v1/messages", model: "gpt-test"}
	err := p.serve(w, r, &http.Response{Body: io.NopCloser(strings.NewReader(cgTestSSE()))})
	if err != nil || !strings.Contains(w.Body.String(), `"model":"claude-kilo-alias"`) || p.model != "gpt-test" {
		t.Fatalf("alias: %s %v", w.Body.String(), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		done <- p.serve(httptest.NewRecorder(), httptest.NewRequest("POST", p.path, nil).WithContext(ctx), &http.Response{Body: reader})
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close upstream")
	}
}

func TestChatGPTProtocolCompletesBeforeUpstreamEOF(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	done := make(chan error, 1)
	p := &chatGPTProtocol{path: "/v1/messages", model: "test", stream: true}
	go func() {
		done <- p.serve(httptest.NewRecorder(), httptest.NewRequest("POST", p.path, nil), &http.Response{Body: reader})
	}()
	if _, err := io.WriteString(writer, cgFrame(map[string]any{"type": "response.completed", "response": cgTestResponse()})); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("waited for EOF after terminal event")
	}
}

func TestChatGPTProtocolDoneTerminalAlias(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, path := range []string{"/v1/messages", "/v1/chat/completions"} {
			w, err := cgServeTest(t, &chatGPTProtocol{path: path, model: "test", stream: stream}, cgFrame(map[string]any{"type": "response.done", "response": cgTestResponse()}))
			if err != nil || !strings.Contains(w.Body.String(), "Read files.") {
				t.Fatalf("%s stream=%t: %v %s", path, stream, err, w.Body.String())
			}
		}
	}
	w, err := cgServeTest(t, &chatGPTProtocol{path: "/v1/messages", model: "test", stream: true}, cgFrame(map[string]any{"type": "response.done", "response": map[string]any{"status": "failed", "error": map[string]any{"message": "quota exhausted"}}}))
	if err == nil || !strings.Contains(w.Body.String(), "quota exhausted") || strings.Contains(w.Body.String(), "message_stop") {
		t.Fatalf("failed done response: %v %s", err, w.Body.String())
	}
}

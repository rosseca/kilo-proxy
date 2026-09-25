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
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Sanitized shape from the GLM Messages probe: concurrent text/thinking/tool
// blocks, reversed block stops, complete input JSON, but an incorrect end_turn.
const messagesGLMStream = `event: message_start
data: {"type":"message_start","message":{"type":"message","role":"assistant","model":"zai/glm-5.3-flash","content":[],"stop_reason":null,"usage":{"input_tokens":0,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Checking."}}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"Synthetic reasoning."}}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call_synthetic","name":"echo","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"value\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"OK\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
id: synthetic-terminal
retry: 250
: retained comment
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":169,"output_tokens":34,"cache_read_input_tokens":11,"cache_creation_input_tokens":7},"provider_metadata":{"gateway":{"marketCost":"0.000042","cost":"0"}},"exact":9007199254740993}

event: message_stop
data: {"type":"message_stop"}

`

const messagesGLMJSON = `{"type":"message","role":"assistant","model":"zai/glm-5.3-flash","content":[{"type":"text","text":"Checking."},{"type":"thinking","thinking":"Synthetic reasoning."},{"type":"tool_use","id":"call_synthetic","name":"echo","input":{"value":"OK","exact":9007199254740993}}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":169,"output_tokens":34,"cache_read_input_tokens":11,"cache_creation_input_tokens":7},"provider_metadata":{"gateway":{"marketCost":"0.000042","cost":"0"}},"unknown":{"keep":true}}`

func messagesTestResponse(body string, stream bool) *http.Response {
	kind := "application/json"
	if stream {
		kind = "text/event-stream; charset=utf-8"
	}
	return &http.Response{StatusCode: 200, Request: httptest.NewRequest("POST", "http://upstream.example/api/gateway/messages", nil), Header: http.Header{"Content-Type": {kind}, "Content-Length": {fmt.Sprint(len(body))}, "ETag": {"stale"}, "Digest": {"stale"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
}

func messagesTestNormalized(t *testing.T, raw string, stream bool) string {
	t.Helper()
	r := messagesTestResponse(raw, stream)
	normalizeMessagesToolStop(r)
	defer r.Body.Close()
	out, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if r.ContentLength != -1 || r.Header.Get("Content-Length") != "" || r.Header.Get("ETag") != "" || r.Header.Get("Digest") != "" {
		t.Fatal("stale response integrity headers")
	}
	return string(out)
}

func TestMessagesCompatibilityJSONRepairAndPreservation(t *testing.T) {
	got := messagesTestNormalized(t, messagesGLMJSON, false)
	want := strings.Replace(messagesGLMJSON, `"stop_reason":"end_turn"`, `"stop_reason":"tool_use"`, 1)
	var gotJSON, wantJSON bytes.Buffer
	if err := jsonCompactMessages(&gotJSON, []byte(got)); err != nil {
		t.Fatal(err)
	}
	if err := jsonCompactMessages(&wantJSON, []byte(want)); err != nil {
		t.Fatal(err)
	}
	if gotJSON.String() != wantJSON.String() {
		t.Fatalf("unexpected field or numeric changes:\n%s\n%s", gotJSON.String(), wantJSON.String())
	}
	for name, raw := range map[string]string{
		"already correct": strings.Replace(messagesGLMJSON, `"end_turn"`, `"tool_use"`, 1),
		"length":          strings.Replace(messagesGLMJSON, `"end_turn"`, `"max_tokens"`, 1),
		"refusal":         strings.Replace(messagesGLMJSON, `"end_turn"`, `"refusal"`, 1),
		"error":           strings.Replace(messagesGLMJSON, `"type":"message"`, `"type":"error"`, 1),
		"no tool":         `{"type":"message","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`,
		"invalid input":   strings.Replace(messagesGLMJSON, `{"value":"OK","exact":9007199254740993}`, `"not an object"`, 1),
		"null input":      strings.Replace(messagesGLMJSON, `{"value":"OK","exact":9007199254740993}`, `null`, 1),
		"missing name":    strings.Replace(messagesGLMJSON, `"name":"echo"`, `"name":""`, 1),
		"truncated JSON":  messagesGLMJSON[:len(messagesGLMJSON)-1],
		"oversize":        strings.Replace(messagesGLMJSON, "Checking.", strings.Repeat("x", messagesCompatibilityLimit+1), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if got := messagesTestNormalized(t, raw, false); got != raw {
				t.Fatal("non-target JSON response changed")
			}
		})
	}
}

// Sort only object keys for comparison; decodeObject preserves numeric precision.
func jsonCompactMessages(out *bytes.Buffer, raw []byte) error {
	doc, err := decodeObject(raw)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(doc)
}

func TestMessagesCompatibilitySSERepairPreservesOtherBytes(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		raw := strings.ReplaceAll(messagesGLMStream, "\n", newline)
		got := messagesTestNormalized(t, raw, true)
		events := strings.Split(raw, newline+newline)
		for i, event := range events {
			if strings.HasPrefix(event, "event: message_delta"+newline) {
				events[i] = strings.TrimSuffix(string(messagesSSEStop([]byte(event+newline+newline))), newline+newline)
			}
		}
		if want := strings.Join(events, newline+newline); got != want {
			t.Fatalf("unrelated event or metadata changed:\n%s", got)
		}
		if !strings.Contains(got, `"stop_reason":"tool_use"`) || !strings.Contains(got, `"exact":9007199254740993`) {
			t.Fatal("tool stop or exact numeric metadata lost")
		}
	}
}

func TestMessagesCompatibilitySSELeavesIncompleteAndErrorStreams(t *testing.T) {
	toolStop := "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":2}\n\n"
	messageStop := "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	for name, raw := range map[string]string{
		"max tokens":           strings.Replace(messagesGLMStream, `"end_turn"`, `"max_tokens"`, 1),
		"refusal":              strings.Replace(messagesGLMStream, `"end_turn"`, `"refusal"`, 1),
		"already correct":      strings.Replace(messagesGLMStream, `"end_turn"`, `"tool_use"`, 1),
		"tool left open":       strings.Replace(messagesGLMStream, toolStop, "", 1),
		"invalid tool JSON":    strings.Replace(messagesGLMStream, `\"OK\"}`, `\"OK\"`, 1),
		"missing tool ID":      strings.Replace(messagesGLMStream, `"call_synthetic"`, `""`, 1),
		"missing message stop": strings.Replace(messagesGLMStream, messageStop, "", 1),
		"partial message stop": strings.TrimSuffix(messagesGLMStream, "\n"),
		"error after delta":    strings.Replace(messagesGLMStream, messageStop, "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"synthetic failure\"}}\n\n", 1),
		"malformed event":      strings.Replace(messagesGLMStream, "event: message_delta", "data: not-json\n\nevent: message_delta", 1),
		"oversize event":       strings.Replace(messagesGLMStream, "Checking.", strings.Repeat("x", messagesCompatibilityLimit+1), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if got := messagesTestNormalized(t, raw, true); got != raw {
				t.Fatal("unsafe stream was repaired or lost bytes")
			}
		})
	}
}

type messagesFragmentReader struct {
	reader io.Reader
	size   int
}

func (r messagesFragmentReader) Read(p []byte) (int, error) {
	if len(p) > r.size {
		p = p[:r.size]
	}
	return r.reader.Read(p)
}

func TestMessagesCompatibilityFragmentedAndMultilineSSE(t *testing.T) {
	raw := strings.Replace(messagesGLMStream, `data: {"type":"message_delta","delta":`, "data: {\"type\":\"message_delta\",\ndata: \"delta\":", 1)
	raw = strings.Replace(raw, "event: message_stop", ": between terminal events\n\nevent: ping\ndata: {\"type\":\"ping\"}\n\nevent: message_stop", 1)
	for _, size := range []int{1, 2, 7, 127} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var output bytes.Buffer
			if err := repairMessagesSSE(context.Background(), messagesFragmentReader{strings.NewReader(raw), size}, &output); err != nil {
				t.Fatal(err)
			}
			got := output.String()
			if !strings.Contains(got, `"stop_reason":"tool_use"`) || strings.Count(got, "event: message_delta") != 1 || !strings.Contains(got, ": between terminal events\n\nevent: ping\ndata: {\"type\":\"ping\"}\n\n") {
				t.Fatal("fragmented or multiline stream not preserved")
			}
		})
	}
}

func TestMessagesCompatibilityKeepsSourceErrorsAndCancellation(t *testing.T) {
	failure := errors.New("synthetic transport failure")
	for _, stream := range []bool{false, true} {
		raw := messagesGLMJSON
		if stream {
			raw = strings.TrimSuffix(messagesGLMStream, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		}
		var output bytes.Buffer
		source := io.MultiReader(strings.NewReader(raw), messagesFailureReader{failure})
		var err error
		if stream {
			err = repairMessagesSSE(context.Background(), source, &output)
		} else {
			err = repairMessagesJSON(context.Background(), source, &output)
		}
		if !errors.Is(err, failure) || output.String() != raw {
			t.Fatal("read failure lost or incomplete response changed")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		output.Reset()
		if stream {
			err = repairMessagesSSE(ctx, strings.NewReader(messagesGLMStream), &output)
			raw = messagesGLMStream
		} else {
			err = repairMessagesJSON(ctx, strings.NewReader(raw), &output)
		}
		if err != nil || output.String() != raw {
			t.Fatal("cancelled response changed")
		}
	}
}

type messagesFailureReader struct{ err error }

func (r messagesFailureReader) Read([]byte) (int, error) { return 0, r.err }

type messagesTrackedBody struct {
	io.ReadCloser
	closes atomic.Int32
}

func (r *messagesTrackedBody) Close() error { r.closes.Add(1); return r.ReadCloser.Close() }

func TestMessagesCompatibilityCloseAndContextUnblockSource(t *testing.T) {
	for _, cancelContext := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelContext), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reader, writer := io.Pipe()
			defer writer.Close()
			source := &messagesTrackedBody{ReadCloser: reader}
			r := messagesTestResponse("", true)
			r.Request = r.Request.WithContext(ctx)
			r.Body = source
			normalizeMessagesToolStop(r)
			done := make(chan error, 1)
			go func() { _, err := io.ReadAll(r.Body); done <- err }()
			if cancelContext {
				cancel()
			} else {
				_ = r.Body.Close()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("closed response unexpectedly completed")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("body read remained blocked")
			}
			_ = r.Body.Close()
			if source.closes.Load() != 1 {
				t.Fatalf("source close count = %d", source.closes.Load())
			}
			if _, err := writer.Write([]byte("data: {}\n\n")); err == nil {
				t.Fatal("upstream remained open")
			}
		})
	}
}

func TestMessagesCompatibilityScope(t *testing.T) {
	for _, change := range []func(*http.Response){
		func(r *http.Response) { r.StatusCode = 400 },
		func(r *http.Response) { r.Request.Method = "GET" },
		func(r *http.Response) { r.Request.URL.Path = "/api/gateway/responses" },
		func(r *http.Response) { r.Header.Set("Content-Type", "text/plain") },
		func(r *http.Response) { r.Header.Set("Content-Encoding", "gzip") },
	} {
		r := messagesTestResponse(messagesGLMJSON, false)
		change(r)
		original := r.Body
		normalizeMessagesToolStop(r)
		if r.Body != original {
			t.Fatal("non-target response was wrapped")
		}
		_ = r.Body.Close()
	}
}

func TestMessagesCompatibilityMultipleToolsRequireEveryInputComplete(t *testing.T) {
	for _, second := range []struct {
		name, input   string
		close, repair bool
	}{
		{"valid second tool", `{"value":"SECOND"}`, true, true},
		{"empty arguments", `{}`, true, true},
		{"invalid second input", `null`, true, false},
		{"unfinished second tool", `{}`, false, false},
	} {
		t.Run(second.name, func(t *testing.T) {
			start := fmt.Sprintf("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":3,\"content_block\":{\"type\":\"tool_use\",\"id\":\"second_synthetic\",\"name\":\"echo\",\"input\":%s}}\n\n", second.input)
			if second.close {
				start += "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":3}\n\n"
			}
			raw := strings.Replace(messagesGLMStream, "event: message_delta", start+"event: message_delta", 1)
			got := messagesTestNormalized(t, raw, true)
			if second.repair {
				if !strings.Contains(got, `"stop_reason":"tool_use"`) || !strings.Contains(got, start) {
					t.Fatal("complete tools were changed or not repaired")
				}
			} else if got != raw {
				t.Fatal("partly invalid tool set was repaired")
			}
		})
	}
}

func TestMessagesCompatibilityStreamsBeforeTerminalEvent(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	r := messagesTestResponse("", true)
	r.Body = reader
	normalizeMessagesToolStop(r)
	defer r.Body.Close()
	parts := strings.SplitN(messagesGLMStream, "event: message_delta", 2)
	go func() { _, _ = io.WriteString(writer, parts[0]) }()
	done := make(chan error, 1)
	got := make([]byte, len(parts[0]))
	go func() { _, err := io.ReadFull(r.Body, got); done <- err }()
	select {
	case err := <-done:
		if err != nil || string(got) != parts[0] {
			t.Fatal("content changed before terminal event", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("text/tool deltas were buffered until completion")
	}
	go func() { _, _ = io.WriteString(writer, "event: message_delta"+parts[1]); _ = writer.Close() }()
	tail, err := io.ReadAll(r.Body)
	if err != nil || !bytes.Contains(tail, []byte(`"stop_reason":"tool_use"`)) {
		t.Fatal("terminal event not repaired", err)
	}
}

func TestMessagesCompatibilityProxyUsageAndRequestIntegrity(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			a := testApp(t)
			a.captureEnabled = true
			body, contentType := messagesGLMJSON, "application/json"
			if stream {
				body, contentType = messagesGLMStream, "text/event-stream"
			}
			requestBody := `{"model":"z-ai/glm-5.3-flash","messages":[{"role":"user","content":"Synthetic prompt"}],"tools":[{"name":"echo","input_schema":{"type":"object"}}]}`
			a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
				got, _ := io.ReadAll(r.Body)
				if string(got) != requestBody || r.URL.Path != "/api/gateway/messages" {
					t.Error("outbound Messages request changed")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1, Request: r}, nil
			})
			r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/messages", strings.NewReader(requestBody))
			r.Header.Set("Authorization", "Bearer local-secret")
			w := httptest.NewRecorder()
			a.inferenceHandler("upstream-secret", "synthetic-org", "local-secret", "127.0.0.1:8877").ServeHTTP(w, r)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"stop_reason":"tool_use"`) {
				t.Fatal("proxy hook not applied", w.Code)
			}
			if len(a.events) != 1 {
				t.Fatal("missing activity")
			}
			u := a.events[0].Usage
			if u == nil || !u.Complete || u.Input == nil || *u.Input != 169 || u.Output == nil || *u.Output != 34 || u.Cached == nil || *u.Cached != 11 || u.CacheWrite == nil || *u.CacheWrite != 7 || u.CostUSD == nil || *u.CostUSD != "0.000042000" {
				t.Fatalf("usage/cost/cache changed: %+v", u)
			}
			trace := a.traces[a.events[0].ID]
			if trace == nil || trace.UpstreamResponse.Body != body || !strings.Contains(trace.Response.Body, `"stop_reason":"tool_use"`) {
				t.Fatal("upstream and downstream traces lost their separate payloads")
			}
		})
	}
}

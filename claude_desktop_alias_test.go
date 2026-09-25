package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func aliasTestApp(t *testing.T) *app {
	t.Helper()
	a, paths := desktopProfileTestApp(t)
	a.config.ClaudeDesktopExperimentalModels = true
	s := editorSelection{Models: []editorModel{{ID: "openai/gpt-4.1-nano", Name: "GPT Nano"}, {ID: "z-ai/glm-5.3-flash", Name: "GLM Flash"}, {ID: "anthropic/claude-fable-5.1", Name: "Fable"}}, Initial: "openai/gpt-4.1-nano"}
	if _, err := a.saveClaudeDesktopProfile(s, paths); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestClaudeDesktopAliasProxyPreservesToolsHistoryUsageAndTraces(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			a := aliasTestApp(t)
			a.captureEnabled = true
			alias := claudeDesktopAlias("z-ai/glm-5.3-flash")
			request := `{"model":"` + alias + `","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"Synthetic reasoning."},{"type":"tool_use","id":"call_existing","name":"echo","input":{"value":"OK","exact":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_existing","content":"OK"}]}],"system":[{"type":"text","text":"Synthetic prompt","cache_control":{"type":"ephemeral"}}],"tools":[{"name":"echo","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}],"thinking":{"type":"disabled"},"output_config":{"effort":"low","format":{"type":"json_schema","schema":{"type":"object"}}}}`
			body, kind := messagesGLMJSON, "application/json"
			if stream {
				body, kind = messagesGLMStream, "text/event-stream"
			}
			a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(r.Body)
				var want, got bytes.Buffer
				_ = jsonCompactMessages(&want, []byte(strings.Replace(request, alias, "z-ai/glm-5.3-flash", 1)))
				_ = jsonCompactMessages(&got, raw)
				if got.String() != want.String() {
					t.Error("changed fields other than model", got.String())
				}
				if r.Header.Get("Anthropic-Beta") != "interleaved-thinking-2025-05-14" {
					t.Error("beta header changed")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}, "Content-Encoding": {"identity"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1, Request: r}, nil
			})
			r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/messages", strings.NewReader(request))
			r.Header.Set("Authorization", "Bearer local-secret")
			r.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
			r.Header.Set("Accept-Encoding", "gzip, deflate, br")
			w := httptest.NewRecorder()
			a.inferenceHandler("upstream-secret", "synthetic-org", "local-secret", "127.0.0.1:8877").ServeHTTP(w, r)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"model":"`+alias+`"`) || !strings.Contains(w.Body.String(), `"stop_reason":"tool_use"`) {
				t.Fatal(w.Code, w.Body.String())
			}
			if stream {
				first := messagesSSEPayload([]byte(strings.SplitN(w.Body.String(), "\n\n", 2)[0]))
				var event struct {
					Type    string
					Message struct{ Model, Type string }
				}
				if json.Unmarshal(first, &event) != nil || event.Type != "message_start" || event.Message.Type != "message" || event.Message.Model != alias {
					t.Fatal("invalid start envelope", string(first))
				}
			}
			if len(a.events) != 1 {
				t.Fatal("missing usage")
			}
			u := a.events[0].Usage
			if u == nil || u.Model != "zai/glm-5.3-flash" || !u.Complete || u.Input == nil || *u.Input != 169 || u.Cached == nil || *u.Cached != 11 || u.CacheWrite == nil || *u.CacheWrite != 7 || u.CostUSD == nil || *u.CostUSD != "0.000042000" {
				t.Fatalf("alias corrupted actual accounting: %+v", u)
			}
			trace := a.traces[a.events[0].ID]
			if trace == nil || trace.UpstreamResponse.Body != body || !strings.Contains(trace.Request.Body, alias) || strings.Contains(trace.UpstreamRequest.Body, alias) || !strings.Contains(trace.Response.Body, alias) {
				t.Fatal("trace stages lost real/alias identity")
			}
		})
	}
}

func TestClaudeDesktopAliasRejectsDisabledUnknownAndDuplicateRequests(t *testing.T) {
	for _, kind := range []string{"disabled", "unknown", "duplicate-model", "duplicate-tool-field"} {
		t.Run(kind, func(t *testing.T) {
			a := aliasTestApp(t)
			alias := claudeDesktopAlias("openai/gpt-4.1-nano")
			if kind == "disabled" {
				a.config.ClaudeDesktopExperimentalModels = false
			}
			if kind == "unknown" {
				alias = claudeDesktopAlias("vendor/unselected")
			}
			body := `{"model":"` + alias + `","messages":[]}`
			if kind == "duplicate-model" {
				body = `{"model":"other","model":"` + alias + `","messages":[]}`
			}
			if kind == "duplicate-tool-field" {
				body = `{"model":"` + alias + `","tools":[{"name":"echo","name":"other"}]}`
			}
			called := false
			a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
				called = true
				return nil, fmt.Errorf("unexpected request")
			})
			r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/messages", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer local-secret")
			w := httptest.NewRecorder()
			a.inferenceHandler("upstream", "org", "local-secret", "127.0.0.1:8877").ServeHTTP(w, r)
			if w.Code != 400 || called {
				t.Fatal("alias was forwarded despite invalid state", w.Code, w.Body.String())
			}
		})
	}
}

func TestClaudeDesktopAliasNativeRequestsRemainByteExact(t *testing.T) {
	a := aliasTestApp(t)
	for _, body := range []string{`{ "model": "anthropic/claude-fable-5.1", "messages": [ ], "exact":9007199254740993 }`, `{"model":"openai/gpt-4.1-nano","messages":[]}`, `invalid JSON`, `{"model":"claude-kilo-v1-not-registered"}`} {
		r := httptest.NewRequest("POST", "http://localhost/v1/messages", strings.NewReader(body))
		r.Header.Set("Accept-Encoding", "gzip, deflate, br")
		out, err := a.prepareClaudeDesktopAlias(r)
		if strings.Contains(body, "not-registered") {
			if err == nil {
				t.Fatal("unknown alias accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(out.Body)
		if string(got) != body || out.Context().Value(claudeDesktopAliasContextKey{}) != nil {
			t.Fatal("non-alias request changed")
		}
		if out.Header.Get("Accept-Encoding") != "gzip, deflate, br" {
			t.Fatal("non-alias compression negotiation changed")
		}
	}
}

func TestClaudeDesktopAliasSSEFragmentsAndUntouchedEvents(t *testing.T) {
	alias := claudeDesktopAlias("openai/gpt-4.1-nano")
	for _, newline := range []string{"\n", "\r\n"} {
		raw := strings.ReplaceAll(": ping\nevent: message_start\nid: test\nretry: 250\ndata: {\"type\":\"message_start\",\ndata: \"message\":{\"type\":\"message\",\"model\":\"actual\",\"content\":[],\"exact\":9007199254740993}}\n\n"+messagesGLMStream[strings.Index(messagesGLMStream, "event: content_block_start"):], "\n", newline)
		var out bytes.Buffer
		if err := rewriteClaudeDesktopAliasSSE(iotest.OneByteReader(strings.NewReader(raw)), &out, alias); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.Contains(got, `"model":"`+alias+`"`) || !strings.Contains(got, `9007199254740993`) || !strings.Contains(got, "id: test"+newline) {
			t.Fatal("start metadata lost", got)
		}
		if strings.SplitN(got, newline+newline, 2)[1] != strings.SplitN(raw, newline+newline, 2)[1] {
			t.Fatal("content/thinking/tool/terminal events changed")
		}
	}
}

func TestClaudeDesktopAliasSSEDecodesEventType(t *testing.T) {
	alias := claudeDesktopAlias("openai/gpt-4.1-nano")
	raw := "data: " + `{"type":"message\u005fstart","message":{"type":"message","model":"actual"}}` + "\n\n"
	untouched := "data: " + `{"type":"content_block_delta","delta":{"type":"text_delta","text":"message_start"}}` + "\n\n" + "data: \"message_start\"\n\n"
	var out bytes.Buffer
	if err := rewriteClaudeDesktopAliasSSE(strings.NewReader(raw+untouched), &out, alias); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"model":"`+alias+`"`) || !strings.HasSuffix(out.String(), untouched) {
		t.Fatal("escaped event type or unrelated data handled incorrectly", out.String())
	}
}

func TestClaudeDesktopAliasResponseCancellation(t *testing.T) {
	for _, cancelContext := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		reader, writer := io.Pipe()
		source := &messagesTrackedBody{ReadCloser: reader}
		r := messagesTestResponse("", true)
		r.Request = r.Request.WithContext(context.WithValue(ctx, claudeDesktopAliasContextKey{}, claudeDesktopAliasRoute{claudeDesktopAlias("openai/gpt-4.1-nano"), "openai/gpt-4.1-nano"}))
		r.Body = source
		if err := adaptClaudeDesktopAliasResponse(r); err != nil {
			t.Fatal(err)
		}
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
				t.Fatal("cancellation completed successfully")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cancellation blocked")
		}
		_ = r.Body.Close()
		_ = writer.Close()
		cancel()
		if source.closes.Load() != 1 {
			t.Fatal("upstream close count", source.closes.Load())
		}
	}
}

func TestClaudeDesktopAliasErrorsRemainUntouched(t *testing.T) {
	r := messagesTestResponse(`{"error":{"type":"rate_limit_error","message":"retry later"}}`, false)
	r.StatusCode = 429
	r.Request = r.Request.WithContext(context.WithValue(r.Request.Context(), claudeDesktopAliasContextKey{}, claudeDesktopAliasRoute{claudeDesktopAlias("openai/gpt-4.1-nano"), "openai/gpt-4.1-nano"}))
	original := r.Body
	if err := adaptClaudeDesktopAliasResponse(r); err != nil || r.Body != original {
		t.Fatal("upstream error changed", err)
	}
}

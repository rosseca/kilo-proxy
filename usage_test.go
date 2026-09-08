package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func observeUsage(t *testing.T, sse bool, body string) *usageObserver {
	t.Helper()
	u := newUsageObserver(httptest.NewRequest("POST", "http://localhost/v1/responses", nil), "org")
	if sse {
		u.configure(&http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}})
	}
	// Fragment at arbitrary byte boundaries, including JSON numbers and SSE delimiters.
	for len(body) > 0 {
		n := min(7, len(body))
		u.feed([]byte(body[:n]))
		body = body[n:]
	}
	u.eof()
	return u
}
func TestUsageProtocolsAndCumulativeSnapshots(t *testing.T) {
	tests := []struct {
		name, body    string
		sse           bool
		input, output int64
		cost          string
	}{
		{"chat JSON", `{"model":"m","usage":{"prompt_tokens":10,"completion_tokens":4,"cost":0.000025}}`, false, 10, 4, "0.000025000"},
		{"Responses JSON", `{"model":"m","usage":{"input_tokens":10,"output_tokens":4,"cost_microdollars":25}}`, false, 10, 4, "0.000025000"},
		{"chat SSE", "data: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4,\"cost\":0.000025}}\n\ndata: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4,\"cost\":0.000025}}\n\ndata: [DONE]\n\n", true, 10, 4, "0.000025000"},
		{"Responses SSE", "event: response.completed\r\ndata: {\"type\":\"response.completed\",\"response\":{\"model\":\"m\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"cost\":0.000025}}}\r\n\r\n", true, 10, 4, "0.000025000"},
		{"Messages SSE", "data: {\"type\":\"message_start\",\"message\":{\"model\":\"m\",\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4,\"cost\":0.000025}}\n\ndata: {\"type\":\"message_stop\"}\n\n", true, 10, 4, "0.000025000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := observeUsage(t, tt.sse, tt.body)
			if u.usage.Input == nil || *u.usage.Input != tt.input || u.usage.Output == nil || *u.usage.Output != tt.output || u.usage.CostUSD == nil || *u.usage.CostUSD != tt.cost || !u.usage.Complete {
				t.Fatalf("wrong usage: %+v", u.usage)
			}
		})
	}
}
func TestUsageUnknownFreeCacheAndPreciseMoney(t *testing.T) {
	u := observeUsage(t, false, `{"usage":{"input_tokens":12,"output_tokens":5,"input_tokens_details":{"cached_tokens":8},"output_tokens_details":{"reasoning_tokens":3}}}`)
	if u.usage.CostUSD != nil || *u.usage.Cached != 8 || *u.usage.Reasoning != 3 {
		t.Fatalf("missing cost treated as free or token details lost: %+v", u.usage)
	}
	u = observeUsage(t, false, `{"usage":{"cost":0,"cost_details":{"upstream_inference_cost":99}}}`)
	if u.usage.CostUSD == nil || *u.usage.CostUSD != "0.000000000" {
		t.Fatal("zero cost replaced with provider/BYOK charges")
	}
	for _, bad := range []string{"-1", "1e10000", "null", "\"0.12\""} {
		u = observeUsage(t, false, `{"usage":{"cost":`+bad+`}}`)
		if u.usage.CostUSD != nil {
			t.Fatalf("accepted invalid cost %s", bad)
		}
	}
	for value, want := range map[string]int64{"0.1": 100000000, "0.000000001": 1, "1e-9": 1, "0.0000000015": 2} {
		n, ok := money(json.Number(value), false)
		if !ok || n != want {
			t.Fatalf("money %s: %d %v", value, n, ok)
		}
	}
	n, ok := money(json.Number("2500000"), true)
	if !ok || n != 2500000000 {
		t.Fatal("microdollar costs over one dollar rejected")
	}
	var s usageSummary
	for range 10 {
		s.add(observeUsage(t, false, `{"usage":{"cost":0.1}}`))
	}
	if s.CostUSD != "1.000000000" {
		t.Fatal("money accumulation lost precision")
	}
}
func TestUsageIndependentOfTraceLimitsPauseAndClear(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"" + strings.Repeat("x", traceBodyLimit+100) + "\"}}]}\n\ndata: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3,\"cost\":0.01}}\n\ndata: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, header := range []string{"Thread-Id", "Session-Id", "X-Claude-Code-Session-Id", "X-Kilo-Local-Session"} {
			if r.Header.Get(header) != "" {
				t.Error("session tracking header leaked upstream")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, body)
	}))
	defer upstream.Close()
	a := testApp(t)
	setUpstream(a, upstream.URL)
	a.captureEnabled = false
	handler := a.inferenceHandler("upstream-key", "org", "local-key", "127.0.0.1:8877")
	for range 35 {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
		r.Header.Set("Authorization", "Bearer local-key")
		r.Header.Set("Thread-Id", "task-1")
		r.Header.Set("Session-Id", "instance-1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != body {
			t.Fatal("observation altered upstream bytes")
		}
	}
	if a.usageTotal.Requests != 35 || a.usageTotal.Priced != 35 || a.usageTotal.CostUSD != "0.350000000" || len(a.events) != 30 || len(a.traces) != 0 || len(a.usageSessions) != 1 {
		t.Fatalf("usage did not survive trace limits: %+v", a.usageTotal)
	}
	a.clearActivity(httptest.NewRecorder())
	if a.usageTotal.Requests != 35 || len(a.usageSessions) != 1 || len(a.events) != 0 {
		t.Fatal("clearing captures reset accounting")
	}
	for _, s := range a.usageSessions {
		if s.Source != "codex-thread" {
			t.Fatal("instance session took priority over task")
		}
	}
}
func TestUsageTruncationCancellationAndBoundedSessions(t *testing.T) {
	u := observeUsage(t, true, "data: {\"usage\":{\"cost\":1}}")
	if u.usage.Complete || u.usage.CostUSD != nil {
		t.Fatal("unterminated SSE frame counted as complete")
	}
	u = observeUsage(t, true, "data: "+strings.Repeat("x", usageBufferLimit+10)+"\n\ndata: {\"usage\":{\"cost\":0.1}}\n\ndata: [DONE]\n\n")
	if !u.usage.Limited || u.usage.CostUSD == nil || *u.usage.CostUSD != "0.100000000" {
		t.Fatal("parser did not recover after oversized event")
	}
	u = observeUsage(t, false, strings.Repeat("x", usageBufferLimit+1))
	if !u.usage.Limited || u.usage.Complete {
		t.Fatal("oversized JSON not marked incomplete")
	}
	a := testApp(t)
	for i := range usageSessionLimit + 20 {
		r := httptest.NewRequest("POST", "http://localhost", nil)
		r.Header.Set("Thread-Id", jsonNumber(i))
		a.recordUsage(newUsageObserver(r, "org"))
	}
	if len(a.usageSessions) != usageSessionLimit+1 || a.usageSessions["overflow"].Requests != 20 {
		t.Fatal("session aggregation is unbounded")
	}
	r := httptest.NewRequest("POST", "http://localhost", nil)
	r.Header.Set("Thread-Id", "same-task")
	if newUsageObserver(r, "org-a").usage.Session == newUsageObserver(r, "org-b").usage.Session {
		t.Fatal("organizations merged")
	}
}
func TestUsageSnapshotDuringStreaming(t *testing.T) {
	u := newUsageObserver(httptest.NewRequest("POST", "http://localhost", nil), "org")
	u.sse = true
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 1000 {
			u.feed([]byte("data: {\"usage\":{\"cost\":0.1}}\n\n"))
		}
		u.eof()
	})
	for range 1000 {
		_ = u.snapshot()
	}
	wg.Wait()
}

func TestUsageCountsCanceledRequestAfterHistoryClear(t *testing.T) {
	started := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":20}}}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer upstream.Close()
	a := testApp(t)
	setUpstream(a, upstream.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/messages", strings.NewReader(`{"model":"m"}`)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer local")
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.inferenceHandler("key", "org", "local", "127.0.0.1:8877").ServeHTTP(httptest.NewRecorder(), r)
	}()
	<-started
	a.clearActivity(httptest.NewRecorder())
	cancel()
	<-done
	if a.usageTotal.Requests != 1 || a.usageTotal.Incomplete != 1 || a.usageTotal.Priced != 0 || len(a.events) != 0 {
		t.Fatalf("canceled request lost or reported as free: %+v", a.usageTotal)
	}
}

func TestBrowserModulesAreServed(t *testing.T) {
	a := testApp(t)
	entries, err := assets.ReadDir("ui")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".js") && !strings.HasSuffix(entry.Name(), ".mjs") {
			continue
		}
		r := httptest.NewRequest("GET", "http://"+a.adminHost+"/"+entry.Name(), nil)
		w := httptest.NewRecorder()
		a.adminHandler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Errorf("browser module %s returned %d", entry.Name(), w.Code)
		}
	}
}

func TestCachePercentageAcrossProtocols(t *testing.T) {
	responses := observeUsage(t, false, `{"usage":{"input_tokens":100,"output_tokens":2,"input_tokens_details":{"cached_tokens":80,"cache_write_tokens":10}}}`)
	messages := observeUsage(t, true, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":80,\"cache_creation_input_tokens\":10}}}\n\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":5}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
	for _, u := range []*usageObserver{responses, messages} {
		if u.usage.Prompt == nil || *u.usage.Prompt != 100 {
			t.Fatal("incorrect input denominator", u.usage.Prompt)
		}
	}
	var s usageSummary
	s.add(responses)
	s.add(messages)
	if s.CacheRatioInput != 200 || s.CacheRatioRead != 160 || s.CacheRatioRequests != 2 || s.Cached != 160 || s.CacheWrite != 20 || s.Prompt != 200 {
		t.Fatalf("%+v", s)
	}
	// An unreported cache must not turn a known 80% ratio into 40%.
	s.add(observeUsage(t, false, `{"usage":{"input_tokens":200,"output_tokens":1}}`))
	if s.CacheRatioInput != 200 || s.CacheRatioRead != 160 || s.WithCacheRead != 2 || s.Requests != 3 || s.LastCache.Read != nil {
		t.Fatalf("missing cache counted as zero: %+v", s)
	}
}
func TestCacheUnknownZeroAndPartial(t *testing.T) {
	tests := []struct {
		body    string
		covered int64
		read    *int64
	}{
		{`{"usage":{"input_tokens":100}}`, 0, nil},
		{`{"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":0}}}`, 1, new(int64(0))},
		{`{"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":101}}}`, 0, new(int64(101))},
		{`{"type":"message","usage":{"input_tokens":10,"cache_read_input_tokens":80}}`, 0, new(int64(80))},
		{`{"type":"message","usage":{"input_tokens":10,"cache_read_input_tokens":80,"cache_creation_input_tokens":0}}`, 1, new(int64(80))},
	}
	for _, tt := range tests {
		var s usageSummary
		s.add(observeUsage(t, false, tt.body))
		if s.CacheRatioRequests != tt.covered || (s.LastCache.Read == nil) != (tt.read == nil) {
			t.Fatal(tt.body, s)
		}
	}
	partial := observeUsage(t, true, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":80,\"cache_creation_input_tokens\":10}}}\n\n")
	var s usageSummary
	s.add(partial)
	if s.WithCacheRead != 1 || s.CacheRatioRequests != 0 || s.LastCache.Complete {
		t.Fatal("partial response counted as complete")
	}
}

func TestUsageValidCostFallbackAndPriority(t *testing.T) {
	for _, invalid := range []string{"null", "-1", `"25"`, "false", "{}", "1e10000"} {
		t.Run(invalid, func(t *testing.T) {
			u := observeUsage(t, false, `{"usage":{"cost_microdollars":`+invalid+`,"cost":0.25,"cost_details":{"upstream_inference_cost":99}}}`)
			if u.usage.CostUSD == nil || *u.usage.CostUSD != "0.250000000" || u.usage.CostSource != "usage.cost" {
				t.Fatalf("invalid microdollars hid valid USD: %+v", u.usage)
			}
		})
	}
	for _, tc := range []struct{ micro, want string }{{"0", "0.000000000"}, {"25", "0.000025000"}} {
		u := observeUsage(t, false, `{"usage":{"cost_microdollars":`+tc.micro+`,"cost":99}}`)
		if u.usage.CostUSD == nil || *u.usage.CostUSD != tc.want || u.usage.CostSource != "usage.cost_microdollars" {
			t.Fatalf("valid microdollars lost priority: %+v", u.usage)
		}
	}
	for _, fields := range []string{`"cost_microdollars":null`, `"cost_microdollars":null,"cost":"0.25"`, `"cost_microdollars":-1,"cost":-1`} {
		u := observeUsage(t, false, `{"usage":{`+fields+`,"cost_details":{"upstream_inference_cost":99}}}`)
		if u.usage.CostUSD != nil || u.usage.CostSource != "" {
			t.Fatal("invalid gateway costs became a provider charge")
		}
	}
}

type usageMemoryTransport func(*http.Request) (*http.Response, error)

func (f usageMemoryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type usageCancelAtEOF struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *usageCancelAtEOF) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.cancel()
	}
	return n, err
}
func (r *usageCancelAtEOF) Close() error { return nil }

func TestUsageCancellationBeforeAndAfterTerminalEvent(t *testing.T) {
	completed := "data: {\"type\":\"response.completed\",\"response\":{\"model\":\"openai/test\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"cost_microdollars\":25}}}\n\n"
	for _, tc := range []struct {
		name, body         string
		complete, limited  bool
		status             int
		priced, incomplete int64
	}{
		{name: "before terminal", body: "data: {\"type\":\"response.in_progress\",\"response\":{\"model\":\"openai/test\",\"usage\":{\"input_tokens\":10}}}\n\n", status: 499, incomplete: 1},
		{name: "after completed", body: completed, complete: true, status: 200, priced: 1},
		{name: "after incomplete terminal", body: strings.Replace(completed, "response.completed", "response.incomplete", 1), complete: true, status: 200, priced: 1},
		{name: "after terminal with limited earlier event", body: "data: " + strings.Repeat("x", usageBufferLimit+10) + "\n\n" + completed, complete: true, limited: true, status: 200, priced: 1, incomplete: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// The complete production handler and usage reader run against memory,
			// with no sockets, real credentials, or changes to a running proxy.
			a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: &usageCancelAtEOF{Reader: strings.NewReader(tc.body), cancel: cancel}, ContentLength: -1, Request: r}, nil
			})
			request := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/responses", strings.NewReader(`{"model":"openai/test","stream":true}`)).WithContext(ctx)
			request.Header.Set("Authorization", "Bearer local-test-key")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			a.inferenceHandler("synthetic-upstream-key", "synthetic-org", "local-test-key", "127.0.0.1:8877").ServeHTTP(response, request)
			if ctx.Err() != context.Canceled || len(a.events) != 1 || response.Body.String() != tc.body {
				t.Fatal("fixture did not cancel after the intended response bytes")
			}
			entry := a.events[0]
			if entry.Status != tc.status || entry.Usage == nil || entry.Usage.Complete != tc.complete || entry.Usage.Limited != tc.limited {
				t.Fatalf("incorrect terminal accounting: %+v usage=%+v", entry, entry.Usage)
			}
			if a.usageTotal.Priced != tc.priced || a.usageTotal.Incomplete != tc.incomplete || a.usageTotal.Requests != 1 || a.active != 0 {
				t.Fatalf("incorrect totals after cancellation: %+v", a.usageTotal)
			}
			if tc.priced > 0 && a.usageTotal.CostUSD != "0.000025000" {
				t.Fatal("terminal gateway cost was lost or counted twice")
			}
			if (tc.status == 499) != (a.failures == 1) {
				t.Fatal("late close changed the request error count")
			}
		})
	}
}

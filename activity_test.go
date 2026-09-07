package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func activityRequest(a *app, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/responses", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer local-secret")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Cookie", "session=private-cookie")
	r.Header.Set("X-Api-Key", "another-secret")
	w := httptest.NewRecorder()
	a.inferenceHandler("upstream-secret", "team-id", "local-secret", "127.0.0.1:8877").ServeHTTP(w, r)
	return w
}
func TestActivityShowsBothSidesOfSchemaAdaptation(t *testing.T) {
	a := testApp(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Set-Cookie", "private-cookie")
		w.Header().Set("X-Debug", "upstream-secret")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"output":[],"debug":"upstream-secret"}`)
	}))
	defer upstream.Close()
	setUpstream(a, upstream.URL)
	response := activityRequest(a, unionRequest)
	if response.Code != 200 {
		t.Fatal(response.Code)
	}
	if len(a.events) != 1 || !a.events[0].HasDetails {
		t.Fatal("missing trace")
	}
	trace := a.traces[a.events[0].ID]
	if !strings.Contains(trace.Request.Body, `"oneOf"`) || strings.Contains(trace.Request.Body, toolEnvelope) {
		t.Fatal("original request changed")
	}
	if !strings.Contains(trace.UpstreamRequest.Body, toolEnvelope) {
		t.Fatal("adapted request missing")
	}
	if trace.UpstreamRequest.Headers.Get("X-KiloCode-OrganizationId") != "team-id" {
		t.Fatal("missing organization")
	}
	if trace.UpstreamRequest.Headers.Get("Authorization") != "[REDACTED]" {
		t.Fatal("auth not masked")
	}
	if trace.UpstreamResponse.Headers.Get("Set-Cookie") != "[REDACTED]" || trace.Response.Headers.Get("Set-Cookie") != "" {
		t.Fatal("response stages not distinguished")
	}
	encoded, _ := json.Marshal(trace)
	for _, secret := range []string{"upstream-secret", "local-secret", "private-cookie", "another-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("credential leaked")
		}
	}
	if !strings.Contains(response.Body.String(), "upstream-secret") {
		t.Fatal("redaction changed actual response")
	}
	if w := adminRequest(a, "activity/"+trace.ID, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("GET", "http://"+a.adminHost+"/api/activity/"+trace.ID, nil)
	w := httptest.NewRecorder()
	a.adminHandler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("trace endpoint not authenticated")
	}
	state := adminRequest(a, "state", "")
	if strings.Contains(state.Body.String(), `"oneOf"`) {
		t.Fatal("bodies leaked into polled state")
	}
}
func TestActivityBoundsRedactsAndRetainsRecentEntries(t *testing.T) {
	a := testApp(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); io.WriteString(w, "ok") }))
	defer upstream.Close()
	setUpstream(a, upstream.URL)
	for i := 0; i < 35; i++ {
		activityRequest(a, `{"model":"test/model"}`)
	}
	if len(a.events) != 30 || len(a.traces) != 30 || a.traces["1"] != nil {
		t.Fatal("unbounded history")
	}
	if w := adminRequest(a, "activity/config", `{"enabled":false}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	activityRequest(a, `{"model":"test/model"}`)
	if a.events[0].HasDetails {
		t.Fatal("pause ignored")
	}
	if w := adminRequest(a, "activity/clear", `{}`); w.Code != 200 || len(a.events) != 0 || len(a.traces) != 0 {
		t.Fatal("history not cleared")
	}
	r := httptest.NewRequest("POST", "http://localhost", nil)
	c := newTraceCapture(r, []string{"upstream-secret"})
	c.response.write([]byte(strings.Repeat("a", traceBodyLimit-3) + "upstream-secret" + strings.Repeat("z", 10000)))
	part := c.part(nil, &c.response)
	if !part.Truncated || len(part.Body) > traceBodyLimit || strings.Contains(part.Body, "ups") || len(c.response.data) > traceBodyLimit+len("upstream-secret") {
		t.Fatal("capture limit or boundary redaction failed")
	}
	c.response.write([]byte{0xff, 0xfe})
	_ = c.part(nil, &c.response)
}
func TestActivityClearDoesNotRestoreInFlightBodies(t *testing.T) {
	a := testApp(t)
	started, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-release; io.WriteString(w, "complete") }))
	defer upstream.Close()
	setUpstream(a, upstream.URL)
	finished := make(chan struct{})
	go func() { activityRequest(a, `{"model":"test/model"}`); close(finished) }()
	<-started
	adminRequest(a, "activity/clear", `{}`)
	close(release)
	<-finished
	if len(a.events) != 0 || len(a.traces) != 0 || a.activeTraces != 0 {
		t.Fatal("in-flight trace restored after clear")
	}
}

func TestActivityCapturesSSEWithoutChangingResponse(t *testing.T) {
	a := testApp(t)
	payload := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\ndata: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, payload)
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()
	setUpstream(a, upstream.URL)
	response := activityRequest(a, `{"model":"test/model","stream":true}`)
	trace := a.traces[a.events[0].ID]
	if response.Body.String() != payload || trace.UpstreamResponse.Body != payload || trace.Response.Body != payload {
		t.Fatal("SSE changed or incomplete")
	}
	c := newTraceCapture(httptest.NewRequest("GET", "http://localhost", nil), nil)
	headers := http.Header{"X-Large": []string{strings.Repeat("x", 100<<10)}}
	part := c.part(headers, &c.response)
	if !part.HeadersTruncated || len(part.Headers.Get("X-Large")) > 32<<10 {
		t.Fatal("unbounded headers")
	}
}

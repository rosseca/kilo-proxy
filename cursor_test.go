package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCursorIngressIsolation(t *testing.T) {
	calls := 0
	h := cursorIngress("cursor-secret", []string{"openai/test"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Host != "cursor.internal" {
			t.Error("internal Host missing")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"openai/test"`) {
			t.Error("body lost")
		}
		w.WriteHeader(204)
	}))
	tests := []struct {
		method, path, key, body, origin string
		status                          int
	}{
		{"GET", "/v1/models", "", "", "", 401},
		{"GET", "/v1/models", "local-secret", "", "", 401},
		{"GET", "/api/state", "cursor-secret", "", "", 404},
		{"GET", "/", "cursor-secret", "", "", 404},
		{"GET", "/v1/models?x=y", "cursor-secret", "", "", 404},
		{"GET", "/v1/models", "cursor-secret", "", "https://evil.test", 403},
		{"GET", "/v1/models", "cursor-secret", "", "", 200},
		{"POST", "/v1/responses", "cursor-secret", `{"model":"openai/test"}`, "", 404},
		{"POST", "/v1/chat/completions", "cursor-secret", `{"model":"other"}`, "", 400},
		{"POST", "/v1/chat/completions", "cursor-secret", `invalid`, "", 400},
		{"POST", "/v1/chat/completions", "cursor-secret", `{"model":"openai/test"}`, "", 204},
	}
	for _, tt := range tests {
		r := httptest.NewRequest(tt.method, "https://public.test"+tt.path, strings.NewReader(tt.body))
		r.Header.Set("Authorization", "Bearer "+tt.key)
		r.Header.Set("Origin", tt.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tt.status {
			t.Errorf("%s %s: got %d want %d", tt.method, tt.path, w.Code, tt.status)
		}
		if w.Code == 200 && !strings.Contains(w.Body.String(), `"id":"openai/test"`) {
			t.Error("selected model missing")
		}
	}
	if calls != 1 {
		t.Fatalf("unauthorized request reached inference: %d calls", calls)
	}
}

func TestCursorStreamingAndOrgCredentials(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer kilo-private" || r.Header.Get("X-KiloCode-OrganizationId") != "team-id" {
			t.Errorf("wrong upstream route or auth")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Forwarded-For") != "" {
			t.Error("untrusted header forwarded")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	a := testApp(t)
	a.upstream, _ = url.Parse(upstream.URL)
	server := httptest.NewServer(cursorIngress("cursor-key", []string{"openai/test"}, a.inferenceHandler("kilo-private", "team-id", "cursor-key", "cursor.internal")))
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"openai/test","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer cursor-key")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "private")
	req.Header.Set("X-KiloCode-OrganizationId", "attacker")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || !strings.Contains(line, "hello") {
		t.Fatalf("SSE was buffered: %q %v", line, err)
	}
}

func TestCursorTunnelURL(t *testing.T) {
	if got := cursorTunnelURL([]byte(`{"msg":"started tunnel","url":"https://example.ngrok.app"}`)); got != "https://example.ngrok.app" {
		t.Fatal(got)
	}
	for _, line := range []string{`{"msg":"error","url":"https://example.com"}`, `{"msg":"started tunnel","url":"http://localhost"}`, `{"msg":"started tunnel","url":"https://secret@example.com"}`, `{"msg":"started tunnel","url":"https://example.com?q=secret"}`, `broken`} {
		if cursorTunnelURL([]byte(line)) != "" {
			t.Fatal(line)
		}
	}
}

func TestCursorAdminAndValidation(t *testing.T) {
	a := testApp(t)
	if w := adminRequest(a, "cursor", `{"action":"start","models":["openai/test"]}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := adminRequest(a, "cursor", `{"action":"start","models":[]}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	for _, models := range [][]string{nil, {"duplicate", "duplicate"}, {"bad\nmodel"}} {
		if cursorModelsValid(models) {
			t.Fatal(models)
		}
	}
	if !cursorModelsValid([]string{"openai/gpt-test", "anthropic/claude-test"}) {
		t.Fatal("valid IDs rejected")
	}
}

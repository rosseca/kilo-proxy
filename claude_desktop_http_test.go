package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClaudeDesktopMessagesFetchMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
		status int
	}{
		{"desktop main process", func(*http.Request) {}, http.StatusOK},
		{"remote origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, http.StatusForbidden},
		{"opaque origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, http.StatusForbidden},
		{"local origin", func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:8877") }, http.StatusForbidden},
		{"empty origin present", func(r *http.Request) { r.Header.Set("Origin", "") }, http.StatusForbidden},
		{"duplicate origin", func(r *http.Request) { r.Header.Add("Origin", ""); r.Header.Add("Origin", "https://evil.example") }, http.StatusForbidden},
		{"cross-site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, http.StatusForbidden},
		{"same-site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }, http.StatusForbidden},
		{"same-origin", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-origin") }, http.StatusForbidden},
		{"duplicate site", func(r *http.Request) { r.Header.Add("Sec-Fetch-Site", "cross-site") }, http.StatusForbidden},
		{"cors mode", func(r *http.Request) { r.Header.Set("Sec-Fetch-Mode", "cors") }, http.StatusForbidden},
		{"navigation", func(r *http.Request) {
			r.Header.Set("Sec-Fetch-Mode", "navigate")
			r.Header.Set("Sec-Fetch-Dest", "document")
		}, http.StatusForbidden},
		{"missing mode", func(r *http.Request) { r.Header.Del("Sec-Fetch-Mode") }, http.StatusForbidden},
		{"duplicate mode", func(r *http.Request) { r.Header.Add("Sec-Fetch-Mode", "no-cors") }, http.StatusForbidden},
		{"image destination", func(r *http.Request) { r.Header.Set("Sec-Fetch-Dest", "image") }, http.StatusForbidden},
		{"missing destination", func(r *http.Request) { r.Header.Del("Sec-Fetch-Dest") }, http.StatusForbidden},
		{"duplicate destination", func(r *http.Request) { r.Header.Add("Sec-Fetch-Dest", "empty") }, http.StatusForbidden},
		{"wrong host", func(r *http.Request) { r.Host = "evil.example" }, http.StatusForbidden},
		{"alternate local host", func(r *http.Request) { r.Host = "localhost:8877" }, http.StatusForbidden},
		{"missing key", func(r *http.Request) { r.Header.Del("Authorization") }, http.StatusUnauthorized},
		{"wrong key", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong-key") }, http.StatusUnauthorized},
		{"get messages", func(r *http.Request) { r.Method = http.MethodGet }, http.StatusForbidden},
		{"preflight", func(r *http.Request) {
			r.Method = http.MethodOptions
			r.Header.Set("Access-Control-Request-Method", "POST")
			r.Header.Set("Access-Control-Request-Headers", "authorization")
		}, http.StatusForbidden},
		{"models route", func(r *http.Request) { r.Method = http.MethodGet; r.URL.Path = "/v1/models" }, http.StatusForbidden},
		{"responses route", func(r *http.Request) { r.URL.Path = "/v1/responses" }, http.StatusForbidden},
		{"chat route", func(r *http.Request) { r.URL.Path = "/v1/chat/completions" }, http.StatusForbidden},
		{"image route", func(r *http.Request) { r.URL.Path = "/mcp/images" }, http.StatusForbidden},
		{"encoded path", func(r *http.Request) { r.URL.RawPath = "/v1/%6dessages" }, http.StatusForbidden},
		{"query", func(r *http.Request) { r.URL.RawQuery = "beta=true" }, http.StatusForbidden},
		{"ordinary API client", func(r *http.Request) {
			r.Header.Del("Sec-Fetch-Site")
			r.Header.Del("Sec-Fetch-Mode")
			r.Header.Del("Sec-Fetch-Dest")
		}, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			forwarded := false
			a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
				forwarded = true
				for _, name := range []string{"Origin", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest"} {
					if len(r.Header.Values(name)) != 0 {
						t.Errorf("local metadata %s forwarded upstream", name)
					}
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}, "Access-Control-Allow-Origin": {"*"}}, Body: io.NopCloser(strings.NewReader(`{"type":"message","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`)), Request: r}, nil
			})
			r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8877/v1/messages", strings.NewReader(`{"model":"anthropic/test","max_tokens":1,"messages":[{"role":"user","content":"."}]}`))
			r.Header.Set("Authorization", "Bearer local-secret")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Sec-Fetch-Site", "none")
			r.Header.Set("Sec-Fetch-Mode", "no-cors")
			r.Header.Set("Sec-Fetch-Dest", "empty")
			tc.change(r)
			w := httptest.NewRecorder()
			a.inferenceHandler("upstream-secret", "synthetic-org", "local-secret", "127.0.0.1:8877").ServeHTTP(w, r)
			if w.Code != tc.status || forwarded != (tc.status == http.StatusOK) {
				t.Fatalf("status=%d forwarded=%t; want status=%d", w.Code, forwarded, tc.status)
			}
			for name := range w.Header() {
				if strings.HasPrefix(strings.ToLower(name), "access-control-") {
					t.Fatalf("unexpected CORS response header %s", name)
				}
			}
		})
	}
}

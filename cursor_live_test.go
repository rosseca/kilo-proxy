package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in: creates a real ngrok tunnel, with a synthetic upstream only.
// No Kilo credentials or user messages are read and no inference is billed.
func TestCursorLiveTunnel(t *testing.T) {
	if os.Getenv("KILO_CURSOR_LIVE_TEST") != "1" {
		t.Skip("requires explicit ngrok account/network opt-in")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-kilo-key" || r.Header.Get("X-KiloCode-OrganizationId") != "synthetic-team" {
			t.Error("wrong upstream credentials")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"synthetic hello\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	a := testApp(t)
	a.apiKey = "synthetic-kilo-key"
	a.config.OrgID = "synthetic-team"
	a.upstream, _ = url.Parse(upstream.URL)
	// Only the lifecycle marker is needed: the dedicated ingress gets its own port.
	a.proxyServer = &http.Server{}
	if err := a.startCursor([]string{"openai/synthetic-test"}); err != nil {
		t.Fatal(err)
	}
	defer a.stop()
	var endpoint, key string
	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		status := a.cursor.Status
		endpoint, key = a.cursor.URL, a.cursor.Key
		message := a.cursor.Error
		a.mu.Unlock()
		if status == "running" {
			break
		}
		if status == "error" {
			t.Fatal(message)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if endpoint == "" {
		t.Fatal("tunnel startup timed out")
	}
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range []struct {
		path, key string
		code      int
	}{{"/models", "", 401}, {"/models", key, 200}, {"/../api/state", key, 404}} {
		req, _ := http.NewRequest("GET", endpoint+p.path, nil)
		req.Header.Set("Authorization", "Bearer "+p.key)
		req.Header.Set("ngrok-skip-browser-warning", "1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != p.code {
			t.Fatalf("public %s: %d", p.path, resp.StatusCode)
		}
	}
	if w := adminRequest(a, "cursor", `{"action":"check"}`); w.Code != 200 {
		t.Fatalf("check: %s", w.Body.String())
	}
	req, _ := http.NewRequest("POST", endpoint+"/chat/completions", strings.NewReader(`{"model":"openai/synthetic-test","stream":true,"messages":[{"role":"user","content":"synthetic probe"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("ngrok-skip-browser-warning", "1")
	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "synthetic hello") || time.Since(started) > 4*time.Second {
		_ = resp.Body.Close()
		t.Fatalf("streaming first byte failed: %v", err)
	}
	rest, err := io.ReadAll(reader)
	_ = resp.Body.Close()
	if err != nil || !strings.Contains(string(rest), "[DONE]") {
		t.Fatalf("stream completion failed: %v", err)
	}
	a.mu.Lock()
	addr := a.cursor.listener.Addr().String()
	a.mu.Unlock()
	a.stop()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatal("ingress remained open after stopping")
	}
	t.Log("Public HTTPS, scoped auth, admin isolation, model list, live SSE and shutdown verified with synthetic traffic.")
}

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestE2EServer serves the real embedded UI and admin/proxy handlers to Playwright.
// It is never compiled into release binaries. Every writable profile and credential
// lives in this test's temporary directory or memory-only vault.
func TestE2EServer(t *testing.T) {
	manifest := os.Getenv("KILO_E2E_MANIFEST")
	stopFile := os.Getenv("KILO_E2E_STOP")
	if manifest == "" || stopFile == "" {
		t.Skip("started by the Playwright fixture")
	}
	root := t.TempDir()
	a, err := newApp(filepath.Join(root, "app"), &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.stop()
	defer a.cancelLogin()
	a.editorTestRoot = root
	a.codexProfileDir = filepath.Join(root, ".codex-kilo-desktop")
	a.codexCLIProfileDir = filepath.Join(root, ".codex-kilo-cli")
	a.claudeProfileDir = filepath.Join(root, ".claude-kilo")
	a.xcodeTestRoot = filepath.Join(root, "xcode")
	a.config.Language = "en"
	port, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a.config.Port = port.Addr().(*net.TCPAddr).Port
	port.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models/stats":
			jsonResponse(w, 200, []any{
				map[string]any{"openrouterId": "vendor/one", "chartData": map[string]any{"modeRankings": map[string]int{"code": 9}}, "codingIndex": 95, "speedTokensPerSec": 50},
				map[string]any{"openrouterId": "anthropic/claude-sonnet-4.6", "chartData": map[string]any{"modeRankings": map[string]int{"code": 2}}, "codingIndex": 80, "speedTokensPerSec": 100},
			})
		case "/api/gateway/models":
			jsonResponse(w, 200, map[string]any{"data": []any{
				map[string]any{"id": "vendor/one", "name": "Very Long First Model Name", "context_length": 64000, "top_provider": map[string]int{"max_completion_tokens": 4000}, "pricing": map[string]string{"prompt": "0.000001", "completion": "0.000002"}, "supported_parameters": []string{"tools", "reasoning"}, "architecture": map[string]any{"output_modalities": []string{"text"}}, "opencode": map[string]any{"variants": map[string]any{"low": map[string]any{"reasoning": map[string]string{"effort": "low"}}, "high": map[string]any{"reasoning": map[string]string{"effort": "high"}}}}},
				map[string]any{"id": "anthropic/claude-sonnet-4.6", "name": "Claude Sonnet", "context_length": 128000, "pricing": map[string]string{"prompt": "0.000003", "completion": "0.000015"}, "supported_parameters": []string{"tools"}, "architecture": map[string]any{"output_modalities": []string{"text"}}},
			}})
		case "/api/profile":
			if r.Header.Get("Authorization") != "Bearer synthetic-kilo-personal-key" {
				http.Error(w, "Invalid synthetic account key", 401)
				return
			}
			jsonResponse(w, 200, map[string]any{"organizations": []organization{{ID: "e2e-team", Name: "E2E Team"}, {ID: "other-team", Name: "Other Team"}}})
		case "/api/gateway/responses", "/api/gateway/chat/completions", "/api/gateway/messages":
			if r.Header.Get("Authorization") != "Bearer synthetic-kilo-personal-key" || r.Header.Get("X-KiloCode-OrganizationId") != "e2e-team" {
				http.Error(w, "Proxy failed to inject synthetic credentials", 401)
				return
			}
			if r.Header.Get("Thread-Id") != "" {
				http.Error(w, "Local conversation header leaked upstream", 400)
				return
			}
			var body struct {
				Model  string `json:"model"`
				Stream bool   `json:"stream"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				http.Error(w, "Invalid request", 400)
				return
			}
			usage := map[string]any{"input_tokens": 100, "output_tokens": 5, "input_tokens_details": map[string]int{"cached_tokens": 80, "cache_write_tokens": 10}, "cost": 0.0123}
			response := map[string]any{"id": "resp_e2e", "model": body.Model, "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": "SYNTHETIC_GATEWAY_REPLY"}}}}, "usage": usage}
			if body.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				data, _ := json.Marshal(map[string]any{"type": "response.completed", "response": response})
				fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", data)
			} else {
				jsonResponse(w, 200, response)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	setUpstream(a, upstream.URL)
	a.accountURL = upstream.URL
	a.modelStatsURL = upstream.URL + "/api/models/stats"
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a.adminHost = listener.Addr().String()
	server := &http.Server{Handler: a.adminHandler(), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	defer server.Close()
	data, err := json.Marshal(map[string]any{
		"url": "http://" + a.adminHost + "/#" + a.adminToken, "token": a.adminToken,
		"proxyPort": a.config.Port, "baseURL": "http://127.0.0.1:" + strconv.Itoa(a.config.Port) + "/v1", "root": root,
		"profiles": map[string]string{"codex": a.codexProfileDir, "codex-cli": a.codexCLIProfileDir, "claude": a.claudeProfileDir, "opencode": filepath.Join(root, ".opencode-kilo"), "zed": filepath.Join(root, ".config", "zed")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-ticker.C:
			if _, err := os.Stat(stopFile); err == nil {
				return
			}
		}
	}
}

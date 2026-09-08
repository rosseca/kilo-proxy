package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	launchControl := filepath.Join(root, "launch-control.json")
	launchRecords := filepath.Join(root, "launch-records.json")
	prepareWaiting := filepath.Join(root, "prepare-waiting")
	stateWaiting := filepath.Join(root, "state-waiting")
	imageRecords := filepath.Join(root, "image-records.json")
	var imageRecordMu sync.Mutex
	imageRequests := []map[string]any{}
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	imageBase64 := base64.StdEncoding.EncodeToString(imageBytes.Bytes())
	readLaunchControl := func() map[string]any {
		var control map[string]any
		data, _ := os.ReadFile(launchControl)
		_ = json.Unmarshal(data, &control)
		return control
	}
	var launchRecordMu sync.Mutex
	records := []map[string]string{}
	a.launcher = &clientLaunchRuntime{
		platform: "macos", home: root,
		resolve: func(client, custom string) (string, error) {
			if readLaunchControl()["unavailable"] == client && custom == "" {
				return "", errors.New("Synthetic application unavailable.")
			}
			if custom != "" {
				return custom, nil
			}
			return "/synthetic/" + client, nil
		},
		terminal: func() (bool, string) { return true, "" },
		start: func(plan clientLaunchPlan) error {
			if readLaunchControl()["fail"] == true {
				return errors.New("synthetic launch failure")
			}
			launchRecordMu.Lock()
			defer launchRecordMu.Unlock()
			records = append(records, map[string]string{"client": plan.Client, "directory": plan.Directory, "executable": plan.Executable, "kind": plan.Kind})
			data, _ := json.Marshal(records)
			// Playwright reads this concurrently from another process: publish a
			// complete JSON snapshot instead of exposing a truncated file.
			return atomicCatalogFile(launchRecords, data)
		},
	}
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
			models := []any{
				map[string]any{"id": "vendor/one", "name": "Very Long First Model Name", "context_length": 64000, "top_provider": map[string]int{"max_completion_tokens": 4000}, "pricing": map[string]string{"prompt": "0.000001", "completion": "0.000002"}, "supported_parameters": []string{"tools", "reasoning"}, "architecture": map[string]any{"output_modalities": []string{"text"}}, "opencode": map[string]any{"variants": map[string]any{"low": map[string]any{"reasoning": map[string]string{"effort": "low"}}, "high": map[string]any{"reasoning": map[string]string{"effort": "high"}}}}},
				map[string]any{"id": "anthropic/claude-sonnet-4.6", "name": "Claude Sonnet", "context_length": 128000, "pricing": map[string]string{"prompt": "0.000003", "completion": "0.000015"}, "supported_parameters": []string{"tools"}, "architecture": map[string]any{"output_modalities": []string{"text"}}},
			}
			if readLaunchControl()["imageModels"] == true {
				models = append(models, map[string]any{"id": "image-lab/painter", "name": "Synthetic Image Painter", "architecture": map[string]any{"input_modalities": []string{"text", "image"}, "output_modalities": []string{"image"}}})
			}
			jsonResponse(w, 200, map[string]any{"data": models})
		case "/api/gateway/images":
			if r.Header.Get("Authorization") != "Bearer synthetic-kilo-personal-key" || r.Header.Get("X-KiloCode-OrganizationId") != "e2e-team" {
				http.Error(w, "Image tool failed to inject synthetic credentials", 401)
				return
			}
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != "image-lab/painter" {
				http.Error(w, "Invalid synthetic image request", 400)
				return
			}
			imageRecordMu.Lock()
			imageRequests = append(imageRequests, body)
			data, _ := json.Marshal(imageRequests)
			err := atomicCatalogFile(imageRecords, data)
			imageRecordMu.Unlock()
			if err != nil {
				http.Error(w, "Cannot record synthetic image request", 500)
				return
			}
			jsonResponse(w, 200, map[string]any{"model": body["model"], "choices": []any{map[string]any{"message": map[string]any{"images": []any{map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:image/png;base64," + imageBase64}}}}}}, "usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3, "cost": 0.0042}})
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
	a.imageGenerationURL = upstream.URL + "/api/gateway/images"
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a.adminHost = listener.Addr().String()
	admin := a.adminHandler()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		control := readLaunchControl()
		if r.URL.Path == "/api/state" && control["holdState"] == true {
			_ = os.WriteFile(stateWaiting, []byte("waiting"), 0600)
			for readLaunchControl()["holdState"] == true {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
		}
		if r.Method == "POST" && (strings.HasSuffix(r.URL.Path, "/catalog") || strings.HasSuffix(r.URL.Path, "/profile") || strings.HasPrefix(r.URL.Path, "/api/xcode/") && r.URL.Path != "/api/xcode/info") {
			if control["holdPrepare"] == true {
				_ = os.WriteFile(prepareWaiting, []byte("waiting"), 0600)
				for readLaunchControl()["holdPrepare"] == true {
					select {
					case <-r.Context().Done():
						return
					case <-time.After(10 * time.Millisecond):
					}
				}
			}
		}
		if r.URL.Path == "/api/state" && control["cursorRunning"] == true {
			a.mu.Lock()
			a.cursor = &cursorSession{Status: "running", URL: "https://synthetic.example/v1", Key: "synthetic-cursor-local-key", Models: []string{"vendor/one"}}
			a.mu.Unlock()
		}
		admin.ServeHTTP(w, r)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	defer server.Close()
	data, err := json.Marshal(map[string]any{
		"url": "http://" + a.adminHost + "/#" + a.adminToken, "token": a.adminToken,
		"proxyPort": a.config.Port, "baseURL": "http://127.0.0.1:" + strconv.Itoa(a.config.Port) + "/v1", "root": root,
		"imageRecords": imageRecords, "imageBase64": imageBase64,
		"launchControl": launchControl, "launchRecords": launchRecords, "prepareWaiting": prepareWaiting, "stateWaiting": stateWaiting,
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

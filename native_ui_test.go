//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

type nativeRecordingBridge struct {
	mu   sync.Mutex
	Text string
	URL  string
}

func (d *nativeRecordingBridge) CopyText(s string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Text = s
	return nil
}
func (d *nativeRecordingBridge) OpenExternal(s string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.URL = s
	return nil
}

func nativeTestUI(t *testing.T) *nativeUI {
	t.Helper()
	root := t.TempDir()
	a, err := newApp(filepath.Join(root, "app"), &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	a.editorTestRoot = root
	a.zedCredentialStore = fakeZedCredentialStore
	a.codexProfileDir = filepath.Join(root, ".codex-kilo-desktop")
	a.codexCLIProfileDir = filepath.Join(root, ".codex-kilo-cli")
	a.claudeProfileDir = filepath.Join(root, ".claude-kilo")
	a.xcodeTestRoot = filepath.Join(root, "xcode")
	a.config.Language = "en"
	a.apiKey = "synthetic-kilo-personal-key"
	a.config.OrgID = "e2e-team"
	a.desktop = &nativeRecordingBridge{}
	port, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a.config.Port = port.Addr().(*net.TCPAddr).Port
	port.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/gateway/models":
			jsonResponse(w, 200, map[string]any{"data": []any{map[string]any{"id": "vendor/one", "name": "Very Long First Model Name", "context_length": 64000, "top_provider": map[string]int{"max_completion_tokens": 4000}, "pricing": map[string]string{"prompt": "0.000001", "completion": "0.000002"}, "supported_parameters": []string{"tools", "reasoning"}}, map[string]any{"id": "anthropic/claude-sonnet-4.6", "name": "Claude Sonnet", "context_length": 128000, "pricing": map[string]string{"prompt": "0.000003", "completion": "0.000015"}, "supported_parameters": []string{"tools"}}}})
		case "/api/profile":
			jsonResponse(w, 200, map[string]any{"email": "developer@example.test", "organizations": []organization{{ID: "e2e-team", Name: "Engineering"}, {ID: "other-team", Name: "Research"}}})
		case "/api/device-auth/codes":
			jsonResponse(w, 200, map[string]any{"code": "native-test-code", "verificationUrl": "https://app.kilo.ai/device", "expiresIn": 120})
		case "/api/device-auth/codes/native-test-code":
			jsonResponse(w, 202, map[string]any{"status": "pending"})
		case "/api/gateway/responses":
			if r.Header.Get("Authorization") != "Bearer synthetic-kilo-personal-key" || r.Header.Get("X-KiloCode-OrganizationId") != "e2e-team" {
				http.Error(w, "credentials not replaced", 401)
				return
			}
			var body struct {
				Stream bool `json:"stream"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			result := map[string]any{"id": "native-response", "model": "vendor/one", "output": []any{}, "usage": map[string]any{"input_tokens": 100, "output_tokens": 5, "input_tokens_details": map[string]int{"cached_tokens": 80, "cache_write_tokens": 10}, "cost": 0.0123}}
			if body.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				b, _ := json.Marshal(map[string]any{"type": "response.completed", "response": result})
				_, _ = w.Write([]byte("event: response.completed\ndata: " + string(b) + "\n\n"))
			} else {
				jsonResponse(w, 200, result)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	setUpstream(a, up.URL)
	a.accountURL = up.URL
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a.adminHost = listener.Addr().String()
	server := &http.Server{Handler: a.adminHandler()}
	go server.Serve(listener)
	t.Cleanup(func() { a.requestQuit(); a.cancelLogin(); a.stop(); server.Close(); up.Close() })
	u := newNativeUI(a, func() {})
	t.Cleanup(func() { a.requestQuit(); u.shutdownModelLibrary() })
	u.page = "connection" // Legacy connection tests opt into Settings explicitly.
	u.clientState().Claude = claudeCaps("2.1.251")
	u.clients.ClaudeChecked, u.clients.ClaudeDetectStarted = true, true
	yes := true
	u.models = []modelInfo{{ID: "vendor/one", Name: "Very Long First Model Name", ContextWindow: 64000, MaxOutputTokens: 4000, ReasoningEfforts: []string{"low", "high"}, Tools: &yes, InputPrice: ptrFloat(1), OutputPrice: ptrFloat(2)}, {ID: "anthropic/claude-sonnet-4.6", Name: "Claude Sonnet", ContextWindow: 128000, Tools: &yes, InputPrice: ptrFloat(3), OutputPrice: ptrFloat(15)}}
	nativeTestWait(t, u, func() bool { return u.authenticated })
	return u
}
func ptrFloat(value float64) *float64 { return &value }
func nativeTestFrame(t *testing.T, u *nativeUI) *op.Ops {
	t.Helper()
	var ops op.Ops
	var router input.Router
	gtx := layout.Context{Ops: &ops, Source: router.Source(), Constraints: layout.Exact(image.Pt(1280, 5000)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Now: time.Now()}
	u.Layout(gtx)
	router.Frame(&ops)
	return &ops
}
func nativeTestWait(t *testing.T, u *nativeUI, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		u.drain()
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("native UI did not reach expected state: %s", u.notice)
}

func TestNativeConnectionSaveStartStopAndEditPreservation(t *testing.T) {
	u := nativeTestUI(t)
	u.setValue("connection.org", "e2e-team")
	u.setValue("connection.key", "synthetic-kilo-personal-key")
	nativeTestFrame(t, u)
	u.clickable("connection.save-start").Click()
	nativeTestFrame(t, u)
	nativeTestWait(t, u, func() bool { return nativeBool(u.state, "running") })
	saved, err := readSettings(u.owner.dir)
	if err != nil || saved.OrgID != "e2e-team" || saved.Remember {
		t.Fatalf("settings not saved safely: %v", err)
	}
	if u.value("connection.key") != "" {
		t.Fatal("upstream secret retained in input after save")
	}
	u.clickable("connection.stop").Click()
	nativeTestFrame(t, u)
	nativeTestWait(t, u, func() bool { return !nativeBool(u.state, "running") })
	u.setValue("connection.org", "still-typing")
	u.refreshState()
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
	if u.value("connection.org") != "still-typing" {
		t.Fatal("polling overwrote unsaved input")
	}
	u.setValue("connection.port", "22")
	u.clickable("connection.save-start").Click()
	nativeTestFrame(t, u)
	if !strings.Contains(u.notice, "1024") || nativeBool(u.state, "running") {
		t.Fatal("invalid port was not rejected")
	}
}
func TestNativeLanguageClipboardTeamsAndLoginCancel(t *testing.T) {
	u := nativeTestUI(t)
	nativeTestFrame(t, u)
	u.clickable("language").Click()
	nativeTestFrame(t, u)
	nativeTestWait(t, u, func() bool { return u.language == "es" && u.SmokeSnapshot()["language-saving"] == "false" })
	saved, err := readSettings(u.owner.dir)
	if err != nil || saved.Language != "es" {
		t.Fatal("language not persisted")
	}
	u.clickable("connection.copy-url").Click()
	nativeTestFrame(t, u)
	bridge := u.owner.desktop.(*nativeRecordingBridge)
	if bridge.Text != nativeString(u.state, "baseURL") {
		t.Fatal("clipboard action did not copy actual endpoint")
	}
	u.clickable("connection.teams").Click()
	nativeTestFrame(t, u)
	nativeTestWait(t, u, func() bool { return len(nativeArray(u.state, "organizations")) == 2 })
	nativeTestFrame(t, u)
	u.clickable("team.other-team").Click()
	nativeTestFrame(t, u)
	if u.value("connection.org") != "other-team" {
		t.Fatal("team button did not update organization")
	}
	// Start through the real device endpoint without opening an external browser.
	u.call("POST", "/api/auth/start", map[string]any{}, func(json.RawMessage) { u.refreshState() })
	nativeTestWait(t, u, func() bool { return nativeString(nativeMap(u.state["auth"]), "status") == "pending" })
	nativeTestFrame(t, u)
	u.clickable("connection.cancel").Click()
	nativeTestFrame(t, u)
	nativeTestWait(t, u, func() bool { return nativeString(nativeMap(u.state["auth"]), "status") == "cancelled" })
}
func TestNativeActivityCacheCaptureAndClearKeepAccounting(t *testing.T) {
	u := nativeTestUI(t)
	if err := u.owner.start(); err != nil {
		t.Fatal(err)
	}
	for _, stream := range []bool{false, true} {
		body := `{"model":"vendor/one","input":"synthetic prompt","stream":false}`
		if stream {
			body = strings.Replace(body, "false", "true", 1)
		}
		req, _ := http.NewRequest("POST", nativeString(u.state, "baseURL")+"/responses", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+nativeString(u.state, "localKey"))
		req.Header.Set("Thread-Id", "native-conversation")
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal(response.Status)
		}
	}
	u.refreshState()
	nativeTestWait(t, u, func() bool { return nativeNumber(u.state, "requests") == 2 })
	u.page = "activity"
	nativeTestFrame(t, u)
	var total usageSummary
	nativeDecode(nativeMap(u.state["usage"])["total"], &total)
	if total.Cached != 160 || total.CacheWrite != 20 || total.CostUSD != "0.024600000" || nativeCacheRatio(total) != "80.0%" {
		t.Fatalf("unexpected accounting: %+v", total)
	}
	var events []event
	nativeDecode(u.state["events"], &events)
	if len(events) != 2 {
		t.Fatal("missing captures")
	}
	u.clickable("activity.event." + events[0].ID).Click()
	nativeTestFrame(t, u)
	nativeTestWait(t, u, func() bool { return u.trace != nil })
	data, _ := json.Marshal(u.trace)
	if strings.Contains(string(data), "synthetic-kilo-personal-key") || strings.Contains(string(data), nativeString(u.state, "localKey")) {
		t.Fatal("secret exposed in inspector")
	}
	for _, stage := range []string{"request", "upstreamRequest", "upstreamResponse", "response"} {
		u.traceStage = stage
		nativeTestFrame(t, u)
		if u.value("activity.headers") == "" {
			t.Fatal("stage headers not rendered")
		}
	}
	u.clickable("activity.clear").Click()
	nativeTestFrame(t, u)
	nativeTestWait(t, u, func() bool { return u.trace == nil && len(nativeArray(u.state, "events")) == 0 })
	var after usageSummary
	nativeDecode(nativeMap(u.state["usage"])["total"], &after)
	if after.CostUSD != total.CostUSD || after.Cached != total.Cached {
		t.Fatal("clear captures reset accounting")
	}
}
func TestNativeMoneyAndUnknownCache(t *testing.T) {
	for raw, want := range map[string]string{"": "—", "0.000000000": "$0", "100": "$100", "1.230000": "$1.23"} {
		if got := nativeMoney(raw); got != want {
			t.Errorf("%q: %s != %s", raw, got, want)
		}
	}
	if nativeCacheRatio(usageSummary{}) != "—" {
		t.Fatal("unknown cache shown as zero")
	}
}

// Opt-in pixel captures run on the same GPU renderer used by the native window.
// UI semantics and file/traffic assertions above remain mandatory without a GPU.
func TestNativeVisualSnapshots(t *testing.T) {
	output := os.Getenv("KILO_NATIVE_SCREENSHOTS")
	if output == "" {
		t.Skip("set KILO_NATIVE_SCREENSHOTS to render review images")
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		t.Fatal(err)
	}
	u := nativeTestUI(t)
	selection := nativeSeedSharedForTest(t, u)
	for i, model := range u.models {
		if err := selection.add(model, 50); err != nil {
			t.Fatal(err)
		}
		selection.Models[i].DisplayName = []string{"One", "Sonnet"}[i]
		if i == 0 {
			selection.Models[i].DefaultReasoning = "high"
		}
		u.seedClientChoice(sharedModelKey, selection.Models[i])
	}
	for _, size := range []image.Point{{1180, 820}, {780, 700}} {
		for _, page := range []string{"settings", "clients", "activity"} {
			u.page = page
			u.language = "en"
			u.notice = ""
			var ops op.Ops
			var router input.Router
			gtx := layout.Context{Ops: &ops, Source: router.Source(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Now: time.Now()}
			dims := u.Layout(gtx)
			if dims.Size != size {
				t.Fatalf("%s layout exceeds window: got %v want %v", page, dims.Size, size)
			}
			window, err := headless.NewWindow(size.X, size.Y)
			if err != nil {
				t.Fatal(err)
			}
			if err = window.Frame(&ops); err != nil {
				window.Release()
				t.Fatal(err)
			}
			pixels := image.NewRGBA(image.Rectangle{Max: size})
			if err = window.Screenshot(pixels); err != nil {
				window.Release()
				t.Fatal(err)
			}
			window.Release()
			name := filepath.Join(output, page+"-"+fmtSize(size)+".png")
			f, err := os.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, pixels)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
func fmtSize(p image.Point) string {
	return strings.Join([]string{fmt.Sprint(p.X), fmt.Sprint(p.Y)}, "x")
}

func TestNativeDelayedResponsesKeepCurrentEditsAndSelection(t *testing.T) {
	u := nativeTestUI(t)
	u.setValue("connection.key", "first-synthetic-key")
	u.submitConnection(false)
	u.setValue("connection.key", "newly-typed-synthetic-key")
	nativeTestWait(t, u, func() bool { return !u.busy["POST/api/config"] })
	if u.value("connection.key") != "newly-typed-synthetic-key" {
		t.Fatal("late save discarded newer input")
	}

	u.state["events"] = []any{map[string]any{"id": "A"}, map[string]any{"id": "B"}}
	u.traceGeneration = 2
	u.acceptTrace(2, json.RawMessage(`{"id":"B"}`))
	u.acceptTrace(1, json.RawMessage(`{"id":"A"}`))
	if u.trace == nil || u.trace.ID != "B" {
		t.Fatal("older request replaced current inspector")
	}
	u.traceGeneration++
	u.trace = nil
	u.acceptTrace(2, json.RawMessage(`{"id":"B"}`))
	if u.trace != nil {
		t.Fatal("cleared capture resurrected")
	}
	u.state["events"] = nil
	u.acceptTrace(3, json.RawMessage(`{"id":"A"}`))
	if u.trace != nil {
		t.Fatal("expired capture resurrected")
	}
}
func TestNativeRejectsEarlierOrganizationCatalog(t *testing.T) {
	u := nativeTestUI(t)
	u.owner.mu.Lock()
	u.owner.catalogRevision = 2
	u.owner.mu.Unlock()
	u.state["catalogRevision"] = float64(2)
	u.models = nil
	if u.applyModels(json.RawMessage(`{"revision":1,"models":[{"id":"old-team/model"}]}`), 1, "models") {
		t.Fatal("stale catalog accepted")
	}
	if len(u.models) != 0 {
		t.Fatal("old team models reappeared")
	}
	nativeTestWait(t, u, func() bool { return len(u.models) == 2 && !u.busy["POST/api/models"] })
	for _, model := range u.models {
		if model.ID == "old-team/model" {
			t.Fatal("stale model persisted")
		}
	}
}
func TestNativeMissingCacheIsNotZero(t *testing.T) {
	if nativeReportedCount(0, 0, 5) != "—" {
		t.Fatal("missing cache rendered as zero")
	}
	if nativeReportedCount(0, 5, 5) != "0" {
		t.Fatal("reported zero cache not preserved")
	}
}

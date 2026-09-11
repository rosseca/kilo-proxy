//go:build desktop

package main

import (
	"errors"
	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type nativeLaunchRecorder struct {
	mu             sync.Mutex
	plans          []clientLaunchPlan
	prepares       atomic.Int32
	gets           atomic.Int32
	launchRequests atomic.Int32
	entered        chan struct{}
	release        chan struct{}
	once           sync.Once
	failPrepare    bool
	failLaunch     bool
}

func (r *nativeLaunchRecorder) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.plans) }
func (r *nativeLaunchRecorder) unblock()   { r.once.Do(func() { close(r.release) }) }

func nativeLaunchTestUI(t *testing.T, key string, delay, failPrepare bool) (*nativeUI, *nativeLaunchRecorder) {
	t.Helper()
	u := nativeTestUI(t)
	openDesignBinary := filepath.Join(u.owner.editorTestRoot, "Open Design")
	if err := os.WriteFile(openDesignBinary, []byte("synthetic; never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	openCodeBinary := syntheticOpenCodeExecutable(t, u.owner.editorTestRoot)
	u.owner.openDesignCheckRunning = func(string) (bool, error) { return false, nil }
	recorder := &nativeLaunchRecorder{entered: make(chan struct{}, 1), release: make(chan struct{}), failPrepare: failPrepare}
	if !delay {
		recorder.unblock()
	}
	t.Cleanup(recorder.unblock)
	u.owner.launcher = &clientLaunchRuntime{
		platform: "macos", home: u.owner.editorTestRoot,
		resolve: func(client, customPath string) (string, error) {
			if client == "open-design" {
				return openDesignBinary, nil
			}
			if client == "opencode" {
				return openCodeBinary, nil
			}
			if customPath != "" {
				return customPath, nil
			}
			return "/fake/client", nil
		},
		terminal: func() (bool, string) { return true, "" },
		start: func(plan clientLaunchPlan) error {
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			if recorder.failLaunch {
				return errors.New("synthetic launch failure")
			}
			recorder.plans = append(recorder.plans, plan)
			return nil
		},
	}
	handler := u.owner.adminHandler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !secureEqual(r.Header.Get("Authorization"), "Bearer "+u.owner.adminToken) {
			t.Error("native request omitted admin authentication")
			jsonError(w, 401, "missing auth")
			return
		}
		if r.URL.Path == nativeLaunchEndpoint && r.Method == "GET" {
			recorder.gets.Add(1)
		}
		if r.URL.Path == nativeClientEndpoint(key) && r.Method == "POST" {
			recorder.prepares.Add(1)
			select {
			case recorder.entered <- struct{}{}:
			default:
			}
			<-recorder.release
			if recorder.failPrepare {
				jsonError(w, 400, "synthetic preparation failure")
				return
			}
		}
		if r.URL.Path == nativeLaunchEndpoint && r.Method == "POST" {
			recorder.launchRequests.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	u.owner.adminHost = strings.TrimPrefix(server.URL, "http://")
	t.Cleanup(func() { recorder.unblock(); server.Close() })
	u.client = key
	if strings.HasPrefix(key, "xcode-") {
		u.client = "xcode"
		u.clientState().Variant = strings.TrimPrefix(key, "xcode-")
		u.clients.Xcode = u.owner.xcodeInfo()
		u.clients.XcodeChecked = true
		u.clients.XcodeDetectStarted = true
	}
	if key == "claude" {
		u.setValue("clients-claude-mode", "modern")
		u.clientState().ClaudeDetectStarted = true
	}
	u.models = nativeClientModelsForTest()
	nativeSeedSharedForTest(t, u, u.models[0])
	u.sharedClientSelection(key)
	u.detectLaunchers()
	nativeTestWait(t, u, func() bool { return u.clientState().LaunchChecked })
	if key == "open-design" {
		u.detectOpenDesign()
		nativeTestWait(t, u, func() bool { return u.clientState().OpenDesignChecked })
	}
	u.page = "clients"
	return u, recorder
}

func TestNativeLaunchPreparesEveryClientAndKeepsCommandsSeparate(t *testing.T) {
	for _, key := range []string{"codex", "codex-cli", "claude", "opencode", "zed", "xcode-chat", "xcode-codex", "xcode-claude"} {
		t.Run(key, func(t *testing.T) {
			u, r := nativeLaunchTestUI(t, key, false, false)
			u.setValue("clients-platform", "windows")
			u.setValue("clients-shell", "powershell")
			u.setValue("clients-app-path", `C:\export-only\Codex.exe`)
			nativeTestFrame(t, u)
			u.clickable("client:" + key + ":launch").Click()
			nativeTestFrame(t, u)
			u.launchClient(key) // A second click before completion must not enqueue another run.
			nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
			if r.count() != 1 {
				t.Fatalf("launch did not reach the fake OS dispatcher once: %d; %s", r.count(), u.notice)
			}
			if r.prepares.Load() != 1 {
				t.Fatal("dirty selection was not prepared exactly once")
			}
			s := u.clientState().selection(key)
			if s.Saved == "" || s.Path == "" {
				t.Fatal("successful automatic preparation was not remembered")
			}
			r.mu.Lock()
			plan := r.plans[0]
			r.mu.Unlock()
			if plan.Client != key || plan.Directory != u.owner.editorTestRoot {
				t.Fatalf("launch lost client/project: %+v", plan)
			}
			if strings.Contains(strings.Join(plan.Args, " "), "export-only") {
				t.Fatal("command export settings reached native launch")
			}
			bridge := u.owner.desktop.(*nativeRecordingBridge)
			bridge.mu.Lock()
			copied := bridge.Text
			bridge.mu.Unlock()
			if copied != "" {
				t.Fatal("native launch used clipboard credentials")
			}
			u.launchClient(key)
			nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
			if r.count() != 2 || r.prepares.Load() != 2 {
				t.Fatal("repeat Open did not reapply the shared library before launch")
			}
			if r.gets.Load() != 1 {
				t.Fatal("launcher detection polled on every frame/launch")
			}
		})
	}
}

func TestNativeLaunchPreservesEditsWhilePreparing(t *testing.T) {
	for _, field := range []string{"model", "directory", "appPath"} {
		t.Run(field, func(t *testing.T) {
			u, r := nativeLaunchTestUI(t, "codex", true, false)
			nativeTestFrame(t, u)
			u.clickable("client:codex:launch").Click()
			nativeTestFrame(t, u)
			<-r.entered
			switch field {
			case "model":
				u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Newest name")
			case "directory":
				u.setValue("clients-project-directory", t.TempDir())
			case "appPath":
				u.setValue("clients-launch-app-path", "/different/Codex.app")
			}
			u.launchClient("codex")
			r.unblock()
			nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
			if r.count() != 0 || r.prepares.Load() != 1 {
				t.Fatal("stale or duplicate launch escaped pending-edit guard")
			}
			if !strings.Contains(u.notice, "changed while preparing") {
				t.Fatalf("pending edit was not explained: %s", u.notice)
			}
			if field == "model" && u.clientState().selection("codex").Models[0].DisplayName != "Newest name" {
				t.Fatal("pending name edit was overwritten")
			}
		})
	}
}

func TestNativeLaunchFailuresReleaseBusyState(t *testing.T) {
	for _, failure := range []string{"prepare", "launch"} {
		t.Run(failure, func(t *testing.T) {
			u, r := nativeLaunchTestUI(t, "opencode", false, failure == "prepare")
			r.failLaunch = failure == "launch"
			u.launchClient("opencode")
			nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
			expected := "synthetic preparation failure"
			if failure == "launch" {
				expected = "Could not open"
			}
			if r.count() != 0 || !strings.Contains(u.notice, expected) {
				t.Fatalf("failure was hidden: %s", u.notice)
			}
			if failure == "prepare" && u.clientState().selection("opencode").Saved != "" {
				t.Fatal("failed prepare enabled direct launch")
			}
			if u.busy["POST"+nativeLaunchEndpoint] || u.busy["POST"+nativeClientEndpoint("opencode")] {
				t.Fatal("failure left Launch busy")
			}
		})
	}
}

func nativeHoldLaunchLibrarySave(t *testing.T, u *nativeUI, fail bool) (<-chan struct{}, func()) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	var first, unblock sync.Once
	u.owner.modelLibrary.write = func(path string, data []byte) error {
		if filepath.Base(path) == "models.json" {
			first.Do(func() { close(entered); <-release })
			if fail {
				return errors.New("synthetic library write failure")
			}
		}
		return atomicCatalogFile(path, data)
	}
	finish := func() { unblock.Do(func() { close(release) }) }
	t.Cleanup(func() { finish(); u.flushModelLibrary() })
	return entered, finish
}

func TestNativeLaunchLibrarySaveWaitPreservesRequestedSettings(t *testing.T) {
	for _, change := range []string{"none", "model", "directory", "appPath", "connection", "raw-limit"} {
		t.Run(change, func(t *testing.T) {
			u, r := nativeLaunchTestUI(t, "codex", false, false)
			// An invalid numeric edit can still parse as the previous zero. The
			// launch boundary must compare the raw draft, not only exported JSON.
			if change == "raw-limit" {
				u.setValue(nativeClientField(sharedModelKey, "vendor/one", "output"), "0")
			}
			entered, release := nativeHoldLaunchLibrarySave(t, u, false)
			u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Requested model name")
			u.launchAgent("codex")
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("launch did not wait for its library save")
			}
			if u.clients.Launching != "codex" || u.agents.Phase != "Saving models…" || r.prepares.Load() != 0 {
				t.Fatal("profile preparation started before library persistence")
			}
			switch change {
			case "model":
				u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Newer model name")
			case "directory":
				u.setValue(agentProjectField("codex"), t.TempDir())
			case "appPath":
				u.setValue("clients-launch-app-path", "/different/codex")
			case "connection":
				u.owner.mu.Lock()
				u.owner.config.OrgID = "new-team"
				u.owner.mu.Unlock()
			case "raw-limit":
				u.setValue(nativeClientField(sharedModelKey, "vendor/one", "output"), "not a number")
			}
			u.launchAgent("codex") // A second click cannot queue a second waiter.
			release()
			nativeTestWait(t, u, func() bool { return u.clients.Launching == "" })
			if u.agents.Phase != "" {
				t.Fatal("save completion left a busy phase")
			}
			if change == "none" {
				if r.count() != 1 || r.prepares.Load() != 1 {
					t.Fatalf("unchanged saved draft did not launch once: %s", u.notice)
				}
				return
			}
			if r.count() != 0 || r.prepares.Load() != 0 || !strings.Contains(u.notice, "changed while preparing") {
				t.Fatalf("changed draft silently replaced requested launch: %s", u.notice)
			}
			if change == "model" {
				if u.value(nativeClientField(sharedModelKey, "vendor/one", "name")) != "Newer model name" {
					t.Fatal("save waiter overwrote the latest model edit")
				}
				u.launchAgent("codex")
				nativeTestWait(t, u, func() bool { return u.clients.Launching == "" })
				if r.count() != 1 || r.prepares.Load() != 1 {
					t.Fatalf("explicit retry failed: %s", u.notice)
				}
			}
		})
	}
}

func TestNativeLaunchLibrarySaveFailureStopsBeforePreparationAndCanRetry(t *testing.T) {
	u, r := nativeLaunchTestUI(t, "codex", false, false)
	entered, release := nativeHoldLaunchLibrarySave(t, u, true)
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Keep this unsaved model")
	u.launchAgent("codex")
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("launch did not reach the pending library save")
	}
	release()
	nativeTestWait(t, u, func() bool { return u.clients.Launching == "" })
	if r.count() != 0 || r.prepares.Load() != 0 || u.agents.Phase != "" || !strings.Contains(u.notice, "Could not save") {
		t.Fatalf("failed library save escaped launch gate: %s", u.notice)
	}
	if u.library.selection.Models[0].DisplayName != "Keep this unsaved model" {
		t.Fatal("library save failure lost draft")
	}
	u.owner.modelLibrary.write = atomicCatalogFile
	u.library.writer.queue(u.modelLibraryValue(), false)
	u.flushModelLibrary()
	u.launchAgent("codex")
	nativeTestWait(t, u, func() bool { return u.clients.Launching == "" })
	if r.count() != 1 || r.prepares.Load() != 1 {
		t.Fatalf("save recovery could not launch: %s", u.notice)
	}
}

func TestNativeLaunchRejectsInvalidRawLimitDuringProfilePreparation(t *testing.T) {
	u, r := nativeLaunchTestUI(t, "codex", true, false)
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "output"), "0")
	u.persistLibraryEdits()
	u.flushModelLibrary()
	u.launchAgent("codex")
	<-r.entered
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "output"), "not a number")
	r.unblock()
	nativeTestWait(t, u, func() bool { return u.clients.Launching == "" })
	if r.count() != 0 || !strings.Contains(u.notice, "changed while preparing") {
		t.Fatalf("numeric parsing hid an invalid edit: %s", u.notice)
	}
}

func TestNativeOpenReappliesLibraryAfterExternalProfileChange(t *testing.T) {
	u, r := nativeLaunchTestUI(t, "codex", false, false)
	u.launchAgent("codex")
	nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
	if r.count() != 1 {
		t.Fatalf("initial open failed: %s", u.notice)
	}
	other := &nativeClientSelection{Models: []nativeModelChoice{{Model: u.models[1]}}, Initial: u.models[1].ID}
	payload, err := nativeClientPayload("codex", other)
	if err != nil {
		t.Fatal(err)
	}
	// A different helper can prepare this generated profile while the native
	// window retains a Saved fingerprint for its unchanged shared library.
	if _, err = nativeRequest(u.owner, "POST", nativeClientEndpoint("codex"), payload); err != nil {
		t.Fatal(err)
	}
	u.launchAgent("codex")
	nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
	raw, err := nativeRequest(u.owner, "GET", nativeClientEndpoint("codex"), nil)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeNativeClientSelection("codex", raw, u.models)
	if err != nil {
		t.Fatal(err)
	}
	if r.count() != 2 || r.prepares.Load() != 3 || restored.Initial != u.library.selection.Initial || len(restored.Models) != 1 || restored.Models[0].Model.ID != u.library.selection.Models[0].Model.ID {
		t.Fatalf("Open trusted stale profile readiness: %+v, %s", restored, u.notice)
	}
}

func TestNativeLaunchDetectionAndValidation(t *testing.T) {
	u, r := nativeLaunchTestUI(t, "codex", false, false)
	c := u.clientState()
	info := c.LaunchInfo.Clients["codex"]
	info.Available = false
	c.LaunchInfo.Clients["codex"] = info
	if u.nativeLaunchAvailable("codex") {
		t.Fatal("missing client enabled launch")
	}
	u.setValue("clients-launch-app-path", filepath.Join(t.TempDir(), "Custom Codex.app"))
	if !u.nativeLaunchAvailable("codex") {
		t.Fatal("custom native Codex path could not recover detection")
	}
	u.library.selection.Models = nil
	u.library.selection.Initial = ""
	u.launchClient("codex")
	nativeTestWait(t, u, func() bool { return c.Launching == "" })
	if r.count() != 0 || r.prepares.Load() != 0 || c.Launching != "" {
		t.Fatal("empty selection dispatched a launch")
	}
}

func TestNativeCursorLaunchRequiresRunningTunnel(t *testing.T) {
	u, r := nativeLaunchTestUI(t, "cursor", false, false)
	for _, status := range []string{"disconnected", "starting"} {
		u.state["cursor"] = cursorSession{Status: status}
		nativeTestFrame(t, u)
		u.clickable("client:cursor:launch").Click()
		nativeTestFrame(t, u)
		if r.launchRequests.Load() != 0 || r.count() != 0 {
			t.Fatal("Cursor launch started before its tunnel was running")
		}
	}
	u.state["cursor"] = cursorSession{Status: "running", URL: "https://synthetic.invalid/v1"}
	nativeTestFrame(t, u)
	u.clickable("client:cursor:launch").Click()
	nativeTestFrame(t, u)
	nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
	if r.launchRequests.Load() != 1 || r.prepares.Load() != 0 {
		t.Fatal("running Cursor UI did not use launch directly")
	}
	// The backend independently rejects this UI-only tunnel fixture. No public
	// tunnel is ever created by a native Launch action or this test.
	if r.count() != 0 {
		t.Fatal("UI state bypassed backend tunnel validation")
	}
}

func TestNativeLaunchPointerProjectControls(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {720, 700}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"-"+lang, func(t *testing.T) {
				u, r := nativeLaunchTestUI(t, "codex", false, false)
				u.setLanguage(lang)
				nativeTestWait(t, u, func() bool { return u.language == lang && u.languageTarget == "" && !u.busy["POST/api/language"] })
				u.setChecked("client:codex:selected", true)
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				label := u.tr("Launch", "Abrir")
				nativeScrollClientControlIntoView(h, label, semantic.Button)
				button := h.target(label, semantic.Button)
				if !button.Desc.Bounds.In(image.Rectangle{Max: size}) {
					t.Fatal("native Launch was clipped at project controls")
				}
				if out := os.Getenv("KILO_NATIVE_SCREENSHOTS"); out != "" {
					if err := os.MkdirAll(out, 0755); err != nil {
						t.Fatal(err)
					}
					window, err := headless.NewWindow(size.X, size.Y)
					if err != nil {
						t.Fatal(err)
					}
					if err = window.Frame(&h.ops); err != nil {
						window.Release()
						t.Fatal(err)
					}
					pixels := image.NewRGBA(image.Rectangle{Max: size})
					err = window.Screenshot(pixels)
					window.Release()
					if err != nil {
						t.Fatal(err)
					}
					file, err := os.Create(filepath.Join(out, "native-launch-"+fmtSize(size)+"-"+lang+".png"))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(file, pixels)
					file.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
				h.click(label, semantic.Button)
				nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
				if r.count() != 1 {
					t.Fatalf("real pointer did not launch prepared Codex: %s", u.notice)
				}
			})
		}
	}
}

func TestNativeClosedCatalogControlsStayInsidePage(t *testing.T) {
	u, _ := nativeLaunchTestUI(t, "codex", false, false)
	u.page = "models"
	u.expanded["library.catalog"] = true
	u.models = nativeGridModels()
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(720, 700), now: time.Now()}
	h.frame()
	bounds := h.target("All labs  ▾", semantic.Button).Desc.Bounds
	u.list("page.models").Position.Offset = bounds.Min.Y + bounds.Size().Y/2 - 40
	h.frame()
	offset := u.list("page.models").Position.Offset
	center := bounds.Min.Add(bounds.Size().Div(2)).Sub(image.Pt(0, offset))
	if center.Y < 0 || center.Y >= 78 {
		t.Fatalf("fixture did not move the old trigger over the fixed header: %v", center)
	}
	p := f32.Pt(float32(center.X), float32(center.Y))
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: p})
	h.frame()
	h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: p})
	h.frame()
	h.router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: p})
	h.frame()
	h.frame()
	if u.expanded["models.lab"] {
		t.Fatal("scrolled-out filter trigger intercepted a click in the fixed header")
	}
}

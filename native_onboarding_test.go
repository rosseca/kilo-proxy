//go:build desktop

package main

import (
	"errors"
	"image"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func nativeFreshOnboarding(t *testing.T) *nativeUI {
	t.Helper()
	u := nativeTestUI(t)
	u.owner.mu.Lock()
	u.owner.apiKey, u.owner.config.OrgID = "", ""
	u.owner.keySaved, u.owner.config.Remember = false, false
	u.owner.organizations, u.owner.accountEmail = nil, ""
	u.owner.mu.Unlock()
	u.refreshState()
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] && nativeString(u.state, "orgId") == "" })
	u.setValue("connection.key", "")
	u.setChecked("connection.remember", false)
	u.beginSetup()
	return u
}

func nativeOnboardingListener(t *testing.T, u *nativeUI, want bool) {
	t.Helper()
	u.owner.mu.Lock()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(u.owner.config.Port))
	u.owner.mu.Unlock()
	conn, err := net.DialTimeout("tcp4", address, 300*time.Millisecond)
	if err == nil {
		conn.Close()
	}
	if (err == nil) != want {
		t.Fatalf("proxy listener running=%v, want %v: %v", err == nil, want, err)
	}
}

func TestNativeOnboardingStartupUsesSavedPrerequisites(t *testing.T) {
	for _, tc := range []struct {
		name, key, org string
		models         bool
		page           string
		step           int
	}{
		{"new user", "", "", false, "setup", setupConnect},
		{"missing credential", "", "test-team", true, "setup", setupConnect},
		{"missing team", "synthetic-key", "", true, "setup", setupConnect},
		{"missing models", "synthetic-key", "test-team", false, "setup", setupModels},
		{"configured", "synthetic-key", "test-team", true, "agents", setupConnect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			a.apiKey, a.config.OrgID = tc.key, tc.org
			if tc.models {
				library := modelLibrary{SchemaVersion: 1, DefaultModel: "vendor/one", Models: []modelLibraryItem{{ID: "vendor/one", DisplayName: "One"}}}
				if _, err := a.modelLibrary.save(library, a.modelLibrary.snapshot().Revision, false); err != nil {
					t.Fatal(err)
				}
			}
			u := newNativeUI(a, func() {})
			t.Cleanup(func() { a.requestQuit(); u.shutdownModelLibrary() })
			if u.page != tc.page || u.page == "setup" && u.setupStep != tc.step {
				t.Fatalf("startup page=%s step=%d, want %s step=%d", u.page, u.setupStep, tc.page, tc.step)
			}
			if u.setupNeeded() != (tc.page == "setup") {
				t.Fatal("setup prerequisite check disagrees with the startup page")
			}
		})
	}
}

func TestNativeOnboardingManualCompletionPersistsAcrossRestart(t *testing.T) {
	u := nativeFreshOnboarding(t)
	u.setValue("connection.key", "synthetic-kilo-personal-key")
	u.setValue("connection.org", "e2e-team")
	u.setChecked("connection.remember", true)
	u.setupConnectionContinue()
	nativeTestWait(t, u, func() bool {
		return u.setupStep == setupModels && !u.busy["GET/api/state"] && !u.busy["POST/api/models"] && len(u.models) == 2
	})
	if u.value("connection.key") != "" || nativeBool(u.state, "running") {
		t.Fatal("connection save retained the input secret or started the proxy before setup finished")
	}
	saved, err := readSettings(u.owner.dir)
	if err != nil || saved.OrgID != "e2e-team" || !saved.Remember {
		t.Fatalf("manual setup did not persist connection settings: %+v, %v", saved, err)
	}
	u.setupModelsContinue()
	if u.setupStep != setupModels || !strings.Contains(u.notice, "at least one model") {
		t.Fatal("empty model library advanced setup")
	}
	selection := nativeSeedSharedForTest(t, u, u.models[0])
	chosen := selection.Initial
	u.setValue(nativeClientField(sharedModelKey, chosen, "context"), "unfinished")
	u.setupModelsContinue()
	if u.setupStep != setupModels || !strings.Contains(u.notice, "whole numbers") {
		t.Fatalf("invalid model edit advanced setup: step=%d notice=%s", u.setupStep, u.notice)
	}
	u.setSharedTokenLimits(chosen, 64000, 4000)
	u.persistLibraryEdits()
	u.flushModelLibrary()
	u.setupModelsContinue()
	if u.setupStep != setupReady {
		t.Fatalf("valid, persisted selection did not reach the final step: %s", u.notice)
	}
	u.finishSetup()
	nativeTestWait(t, u, func() bool { return u.page == "agents" && nativeBool(u.state, "running") })
	nativeOnboardingListener(t, u, true)
	u.setProxyRunning(false, nil)
	nativeTestWait(t, u, func() bool { return !nativeBool(u.state, "running") })
	nativeOnboardingListener(t, u, false)

	restored, err := newApp(u.owner.dir, u.owner.vault)
	if err != nil {
		t.Fatal(err)
	}
	reopened := newNativeUI(restored, func() {})
	t.Cleanup(func() { restored.requestQuit(); reopened.shutdownModelLibrary(); restored.stop() })
	if reopened.page != "agents" || reopened.setupNeeded() || restored.apiKey != "synthetic-kilo-personal-key" || reopened.library.selection.Initial != chosen {
		t.Fatal("restart lost the remembered connection or model library, or repeated completed onboarding")
	}
}

func TestNativeOnboardingManualPointerWalkthrough(t *testing.T) {
	u := nativeFreshOnboarding(t)
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 820), now: time.Now()}
	h.frame()
	h.click("Use an API key or enter a team ID", semantic.Button)
	h.click("Leave blank to keep the current key", semantic.Editor)
	h.typeText("synthetic-kilo-personal-key")
	h.click("org_…", semantic.Editor)
	h.typeText("e2e-team")
	h.click("Remember my login in the system credential store", semantic.CheckBox)
	h.click("Save & choose models", semantic.Button)
	nativeTestWait(t, u, func() bool {
		return u.setupStep == setupModels && !u.busy["GET/api/state"] && !u.busy["POST/api/models"] && len(u.models) == 2
	})
	h.frame()
	search := h.target("provider/model", semantic.Editor).Desc.Bounds
	u.list("page.setup").Position.Offset = max(0, search.Min.Y-350)
	h.frame()
	h.click("provider/model", semantic.Editor)
	h.typeText("vendor/one")
	if u.value("client:shared:search") != "vendor/one" {
		t.Fatal("pointer focus and keyboard input did not reach the model search")
	}
	h.click("Very Long First Model Name", semantic.CheckBox)
	nativeTestWait(t, u, func() bool { _, ready := u.libraryStatus(); return ready })
	if u.library.selection.Initial != "vendor/one" {
		t.Fatal("pointer model choice did not become the shared default")
	}
	u.list("page.setup").Position.Offset = 0
	h.frame()
	h.click("Continue", semantic.Button)
	if u.setupStep != setupReady {
		t.Fatalf("pointer Continue did not finish model selection: %s", u.notice)
	}
	h.click("Start proxy & go to agents", semantic.Button)
	nativeTestWait(t, u, func() bool { return u.page == "agents" && nativeBool(u.state, "running") })
	nativeOnboardingListener(t, u, true)
	saved, err := readSettings(u.owner.dir)
	if err != nil || saved.OrgID != "e2e-team" || !saved.Remember || u.value("connection.key") != "" {
		t.Fatal("pointer walkthrough did not save the selected team and remember preference safely")
	}
	if state := newModelLibraryStore(u.owner.dir).snapshot(); state.Library.DefaultModel != "vendor/one" || len(state.Library.Models) != 1 {
		t.Fatal("pointer model selection did not reach the persisted shared library")
	}
	h.frame()
	h.click("Stop proxy", semantic.Button)
	nativeTestWait(t, u, func() bool { return !nativeBool(u.state, "running") })
	nativeOnboardingListener(t, u, false)
}

// Delay only a real API response. The app still validates settings, persists
// them and starts its actual listener, using temporary state and a fake gateway.
func nativeOnboardingGatedUI(t *testing.T, path string) (*nativeUI, *atomic.Bool, <-chan struct{}, func()) {
	t.Helper()
	root := t.TempDir()
	a, err := newApp(filepath.Join(root, "app"), &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	a.config.Language, a.apiKey, a.config.OrgID = "en", "synthetic-kilo-personal-key", "e2e-team"
	a.editorTestRoot, a.launcher = root, &clientLaunchRuntime{home: root}
	a.desktop = &nativeRecordingBridge{}
	port, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a.config.Port = port.Addr().(*net.TCPAddr).Port
	port.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/gateway/models" {
			http.NotFound(w, r)
			return
		}
		jsonResponse(w, 200, map[string]any{"data": []any{map[string]any{"id": "vendor/new-team", "name": "New team model", "context_length": 64000}}})
	}))
	setUpstream(a, upstream.URL)
	a.accountURL = upstream.URL
	api := a.adminHandler()
	var armed atomic.Bool
	started, gate := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		canHold := true
		if path == "/api/state" {
			a.mu.Lock()
			canHold = a.config.OrgID == "other-team"
			a.mu.Unlock()
		}
		if r.URL.Path == path && canHold && armed.CompareAndSwap(true, false) {
			response := httptest.NewRecorder()
			api.ServeHTTP(response, r)
			close(started)
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
			for key, values := range response.Header() {
				w.Header()[key] = append([]string(nil), values...)
			}
			w.WriteHeader(response.Code)
			_, _ = w.Write(response.Body.Bytes())
			return
		}
		api.ServeHTTP(w, r)
	}))
	a.adminHost = server.Listener.Addr().String()
	server.Start()
	u := newNativeUI(a, func() {})
	t.Cleanup(func() {
		release()
		a.requestQuit()
		u.shutdownModelLibrary()
		a.stop()
		server.Close()
		upstream.Close()
	})
	nativeTestWait(t, u, func() bool { return u.authenticated && !u.busy["GET/api/state"] })
	u.models = []modelInfo{{ID: "vendor/old-team", Name: "Previous team model", ContextWindow: 64000}}
	nativeSeedSharedForTest(t, u, u.models[0])
	u.beginSetup()
	return u, &armed, started, release
}

func TestNativeOnboardingLateStartPreservesReviewModels(t *testing.T) {
	u, armed, started, release := nativeOnboardingGatedUI(t, "/api/start")
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 820), now: time.Now()}
	h.frame()
	armed.Store(true)
	h.click("Start proxy & go to agents", semantic.Button)
	nativeTestWait(t, u, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	})
	h.click("Review models", semantic.Button)
	if u.page != "setup" || u.setupStep != setupModels {
		t.Fatal("Review models was unavailable while the start response was pending")
	}
	release()
	nativeTestWait(t, u, func() bool { return !u.busy["POST/api/start"] && nativeBool(u.state, "running") })
	if u.page != "setup" || u.setupStep != setupModels {
		t.Fatal("late start response navigated away from model review")
	}
	nativeOnboardingListener(t, u, true)
}

func TestNativeOnboardingTeamSaveClearsCatalogBeforeStateRefresh(t *testing.T) {
	u, armed, started, release := nativeOnboardingGatedUI(t, "/api/state")
	u.setupStep, u.catalogCached = setupConnect, true
	u.setValue("connection.org", "other-team")
	armed.Store(true)
	u.setupConnectionContinue()
	nativeTestWait(t, u, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	})
	if u.setupStep != setupModels || len(u.models) != 0 || u.catalogCached {
		t.Fatal("previous team catalog remained available after saving a new connection")
	}
	if nativeString(u.state, "orgId") != "e2e-team" {
		t.Fatal("test did not hold the state response after saving the new team")
	}
	release()
	nativeTestWait(t, u, func() bool {
		return !u.busy["POST/api/models"] && len(u.models) == 1 && u.models[0].ID == "vendor/new-team"
	})
}

func TestNativeOnboardingRequiresSuccessfulModelSave(t *testing.T) {
	u := nativeTestUI(t)
	u.beginSetup()
	u.owner.modelLibrary.write = func(string, []byte) error { return errors.New("synthetic disk full") }
	nativeSeedSharedForTest(t, u, u.models[0])
	u.setupModelsContinue()
	if u.setupStep != setupModels || !strings.Contains(u.notice, "Could not save:") {
		t.Fatalf("failed library save advanced setup: step=%d notice=%s", u.setupStep, u.notice)
	}
	u.finishSetup()
	if u.page != "setup" || u.setupStep != setupModels || nativeBool(u.state, "running") || !strings.Contains(u.notice, "Could not save:") {
		t.Fatal("finish bypassed a failed model save")
	}
}

func TestNativeOnboardingFailedStartKeepsReadyStepAndVisibleError(t *testing.T) {
	u := nativeTestUI(t)
	nativeSeedSharedForTest(t, u, u.models[0])
	u.beginSetup()
	occupied, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", u.value("connection.port")))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	u.finishSetup()
	nativeTestWait(t, u, func() bool { return !u.busy["POST/api/start"] && u.notice != "" })
	if u.page != "setup" || u.setupStep != setupReady || nativeBool(u.state, "running") {
		t.Fatal("failed proxy start left onboarding or reported a running proxy")
	}
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(720, 700), now: time.Now()}
	h.frame()
	visible := false
	for _, node := range h.nodes() {
		if node.Desc.Label == u.notice && !node.Desc.Bounds.Intersect(image.Rectangle{Max: h.size}).Empty() {
			visible = true
		}
	}
	if !visible {
		t.Fatalf("start failure is not visible in the setup screen: %s", u.notice)
	}
	h.target("Start proxy & go to agents", semantic.Button)
}

func TestNativeOnboardingAgentsProxyPointerControls(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {720, 700}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"/"+lang, func(t *testing.T) {
				u := nativeTestUI(t)
				nativeSeedSharedForTest(t, u, u.models[0])
				u.setLanguage(lang)
				nativeTestWait(t, u, func() bool { return u.languageTarget == "" && !u.busy["POST/api/language"] })
				u.page = "agents"
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				h.click(u.tr("Start proxy", "Arrancar proxy"), semantic.Button)
				nativeTestWait(t, u, func() bool { return nativeBool(u.state, "running") })
				nativeOnboardingListener(t, u, true)
				h.frame()
				h.click(u.tr("Stop proxy", "Detener proxy"), semantic.Button)
				nativeTestWait(t, u, func() bool { return !nativeBool(u.state, "running") })
				nativeOnboardingListener(t, u, false)
				h.now = h.now.Add(time.Second)
				h.frame()
				nativeGridCapture(t, h, "onboarding-agents-stopped-"+fmtSize(size)+"-"+lang)
				u.owner.mu.Lock()
				u.owner.apiKey = ""
				u.owner.mu.Unlock()
				h.frame()
				h.click(u.tr("Connect Kilo", "Conectar Kilo"), semantic.Button)
				if u.page != "setup" || u.setupStep != setupConnect {
					t.Fatal("Agents Connect Kilo did not open account setup")
				}
			})
		}
	}
}

func TestNativeOnboardingDeviceLoginPendingCancelledAndApproved(t *testing.T) {
	u := nativeFreshOnboarding(t)
	u.setValue("connection.key", "synthetic-pasted-key-before-sso")
	var approve atomic.Bool
	authServer(t, u.owner, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/device-auth/codes":
			beginResponse(w)
		case "/api/device-auth/codes/TEST-CODE":
			if approve.Load() {
				jsonResponse(w, 200, map[string]string{"status": "approved", "token": "synthetic-device-key", "userEmail": "developer@example.test"})
			} else {
				jsonResponse(w, 202, map[string]string{"status": "pending"})
			}
		case "/api/profile":
			jsonResponse(w, 200, map[string]any{"email": "developer@example.test", "organizations": []organization{{ID: "e2e-team", Name: "Engineering"}, {ID: "other-team", Name: "Research"}}})
		default:
			http.NotFound(w, r)
		}
	})
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 820), now: time.Now()}
	h.frame()
	h.click("Sign in with Kilo / SSO", semantic.Button)
	nativeTestWait(t, u, func() bool { return nativeString(nativeMap(u.state["auth"]), "status") == "pending" })
	bridge := u.owner.desktop.(*nativeRecordingBridge)
	nativeTestWait(t, u, func() bool {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		return bridge.URL != ""
	})
	bridge.mu.Lock()
	opened := bridge.URL
	bridge.mu.Unlock()
	if opened != "https://app.kilo.ai/device-auth?code=TEST-CODE" || !u.connectionWorking() {
		t.Fatal("device login did not expose its verification URL or mark the connection busy")
	}
	h.frame()
	h.click("Cancel login", semantic.Button)
	nativeTestWait(t, u, func() bool { return nativeString(nativeMap(u.state["auth"]), "status") == "cancelled" })
	if u.connectionHasKey() || u.setupStep != setupConnect {
		t.Fatal("cancelled login created a credential or advanced setup")
	}
	if u.value("connection.key") != "synthetic-pasted-key-before-sso" {
		t.Fatal("cancelled SSO discarded the existing manual key draft")
	}
	h.frame()
	h.click("Sign in with Kilo / SSO", semantic.Button)
	nativeTestWait(t, u, func() bool { return nativeString(nativeMap(u.state["auth"]), "status") == "pending" })
	approve.Store(true)
	waitLogin(t, u.owner, "approved")
	u.refreshState()
	nativeTestWait(t, u, func() bool {
		return nativeString(nativeMap(u.state["auth"]), "status") == "approved" && len(nativeArray(u.state, "organizations")) == 2
	})
	if u.value("connection.key") != "" {
		t.Fatal("approved SSO retained the prior pasted key and could overwrite the approved credential")
	}
	if u.value("connection.org") != "" || u.setupStep != setupConnect || !u.connectionHasKey() {
		t.Fatal("SSO approval failed to preserve explicit selection between multiple teams")
	}
	h.frame()
	h.click("Agents", semantic.Button)
	h.click("Continue setup", semantic.Button)
	if u.setupStep != setupConnect || !u.setupConnectionNeeded() {
		t.Fatal("leaving and resuming setup bypassed saving the SSO connection")
	}
	h.click("Engineering", semantic.Button)
	h.click("Save & choose models", semantic.Button)
	nativeTestWait(t, u, func() bool { return u.setupStep == setupModels && !u.busy["GET/api/state"] })
	saved, err := readSettings(u.owner.dir)
	if err != nil || saved.OrgID != "e2e-team" || saved.Remember {
		t.Fatalf("selected SSO team was not saved, or remember was enabled implicitly: %+v, %v", saved, err)
	}
	u.owner.mu.Lock()
	effectiveKey := u.owner.apiKey
	u.owner.mu.Unlock()
	if effectiveKey != "synthetic-device-key" {
		t.Fatal("saving the selected SSO team replaced its token with the stale manual draft")
	}
}

func TestNativeOnboardingAutoSelectedSSOTeamStillRequiresSave(t *testing.T) {
	u := nativeFreshOnboarding(t)
	nativeSeedSharedForTest(t, u, u.models[0])
	authServer(t, u.owner, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/device-auth/codes":
			beginResponse(w)
		case "/api/device-auth/codes/TEST-CODE":
			jsonResponse(w, 200, map[string]string{"status": "approved", "token": "synthetic-device-key"})
		case "/api/profile":
			jsonResponse(w, 200, map[string]any{"organizations": []organization{{ID: "e2e-team", Name: "Engineering"}}})
		default:
			http.NotFound(w, r)
		}
	})
	u.beginKiloLogin()
	nativeTestWait(t, u, func() bool { return !u.busy["POST/api/auth/start"] })
	waitLogin(t, u.owner, "approved")
	u.refreshState()
	nativeTestWait(t, u, func() bool { return nativeString(nativeMap(u.state["auth"]), "status") == "approved" })
	if !u.agentConnectionReady() || !u.setupConnectionNeeded() {
		t.Fatal("automatic SSO team selection was incorrectly treated as a saved connection")
	}
	reopened := newNativeUI(u.owner, func() {})
	t.Cleanup(reopened.shutdownModelLibrary)
	if reopened.page != "setup" || reopened.setupStep != setupConnect || !reopened.setupConnectionNeeded() {
		t.Fatal("reopening the native window bypassed saving the approved SSO connection")
	}
	u.page = "agents"
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(720, 700), now: time.Now()}
	h.frame()
	h.click("Continue setup", semantic.Button)
	if u.setupStep != setupConnect {
		t.Fatal("returning from Agents bypassed saving the automatically selected SSO team")
	}
	u.finishSetup()
	if u.page != "setup" || u.setupStep != setupConnect || nativeBool(u.state, "running") {
		t.Fatal("finish bypassed explicit save of the SSO connection")
	}
	// The browser settings page uses this same API without u.saveConnection.
	response := adminRequest(u.owner, "config", `{"apiKey":"","orgId":"e2e-team","port":`+u.value("connection.port")+`,"remember":false}`)
	if response.Code != http.StatusOK {
		t.Fatalf("direct connection save failed: %s", response.Body.String())
	}
	if u.setupConnectionNeeded() || reopened.setupConnectionNeeded() {
		t.Fatal("successful API save outside the native UI did not confirm the SSO connection")
	}
	u.beginSetup()
	if u.setupStep != setupReady {
		t.Fatalf("saved SSO connection and existing library did not reach ready: %s", u.notice)
	}
}

func TestNativeOnboardingVisualSnapshots(t *testing.T) {
	if os.Getenv("KILO_NATIVE_SCREENSHOTS") == "" {
		t.Skip("set KILO_NATIVE_SCREENSHOTS to render onboarding review images")
	}
	for _, size := range []image.Point{{1180, 820}, {720, 700}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"/"+lang, func(t *testing.T) {
				u := nativeFreshOnboarding(t)
				u.setLanguage(lang)
				nativeTestWait(t, u, func() bool { return u.languageTarget == "" && !u.busy["POST/api/language"] })
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				capture := func(name string) {
					h.frame()
					nativeGridCapture(t, h, "onboarding-"+name+"-"+fmtSize(size)+"-"+lang)
				}
				capture("welcome")
				u.owner.mu.Lock()
				u.owner.apiKey = "synthetic-kilo-personal-key"
				u.owner.mu.Unlock()
				u.call("POST", "/api/auth/organizations", map[string]any{}, u.acceptState)
				nativeTestWait(t, u, func() bool { return len(nativeArray(u.state, "organizations")) == 2 })
				capture("signed-in-team")
				u.setValue("connection.org", "e2e-team")
				u.setupConnectionContinue()
				nativeTestWait(t, u, func() bool {
					return u.setupStep == setupModels && !u.busy["GET/api/state"] && !u.busy["POST/api/models"] && len(u.models) == 2
				})
				capture("models")
				nativeSeedSharedForTest(t, u, u.models[0])
				u.setupModelsContinue()
				capture("ready")
			})
		}
	}
}

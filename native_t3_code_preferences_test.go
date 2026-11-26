//go:build desktop

package main

import (
	"encoding/json"
	"image"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func TestNativeT3CodeTelemetrySaveSurvivesOldStatePoll(t *testing.T) {
	a, err := newApp(filepath.Join(t.TempDir(), "app"), &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	api := a.adminHandler()
	var holdState atomic.Bool
	stateStarted, stateGate := make(chan struct{}), make(chan struct{})
	releaseState := sync.OnceFunc(func() { close(stateGate) })
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/state" && holdState.CompareAndSwap(true, false) {
			// Build the authenticated state response before the telemetry save.
			response := httptest.NewRecorder()
			api.ServeHTTP(response, r)
			close(stateStarted)
			select {
			case <-stateGate:
			case <-r.Context().Done():
				return
			}
			w.WriteHeader(response.Code)
			_, _ = w.Write(response.Body.Bytes())
			return
		}
		api.ServeHTTP(w, r)
	}))
	a.adminHost = server.Listener.Addr().String()
	server.Start()
	t.Cleanup(func() {
		releaseState()
		a.requestQuit()
		server.Close()
	})
	u := newNativeUI(a, func() {})
	disabled := func() bool { return nativeBool(u.state, "t3CodeTelemetryDisabled") }
	nativeTestWait(t, u, func() bool { return u.authenticated && !u.busy["GET/api/state"] })
	if disabled() {
		t.Fatal("new workspace should inherit T3's telemetry policy")
	}

	holdState.Store(true)
	u.refreshState()
	nativeTestWait(t, u, func() bool {
		select {
		case <-stateStarted:
			return true
		default:
			return false
		}
	})
	u.saveT3CodeTelemetry(true)
	nativeTestWait(t, u, func() bool { return !u.t3CodeTelemetrySaving() && disabled() })
	releaseState()
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
	if !disabled() {
		t.Fatal("old poll reverted the confirmed telemetry setting")
	}

	u.saveT3CodeTelemetry(false)
	nativeTestWait(t, u, func() bool { return !u.t3CodeTelemetrySaving() && !u.busy["GET/api/state"] })
	if disabled() {
		t.Fatal("saving false did not restore T3's telemetry policy")
	}
}

type nativeT3PreferencesFixture struct {
	u                      *nativeUI
	h                      *nativePointerHarness
	mu                     sync.Mutex
	response               nativeT3CodePreferencesResponse
	posts                  []nativeT3CodePreferences
	failGet, failPost      bool
	omitOpenCodeConnection bool
	postGate               chan struct{}
	profileGate            chan struct{}
	profileEntered         chan struct{}
	profile                nativeT3CodeInfo
	profilePosts           int
}

func newNativeT3PreferencesFixture(t *testing.T, language string) *nativeT3PreferencesFixture {
	t.Helper()
	u, _ := nativeLaunchTestUI(t, "t3-code", false, false)
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
	u.setLanguage(language)
	nativeTestWait(t, u, func() bool {
		return u.language == language && u.languageTarget == "" && !u.busy["POST/api/language"] && !u.busy["GET/api/state"]
	})
	u.agentSetup("t3-code")
	var prefs nativeT3CodePreferences
	data, _ := json.Marshal(normalizeT3CodePreferences(t3CodePreferences{}))
	if err := json.Unmarshal(data, &prefs); err != nil {
		t.Fatal(err)
	}
	f := &nativeT3PreferencesFixture{u: u, response: nativeT3CodePreferencesResponse{Preferences: prefs, SupportedProviders: []nativeT3CodeProvider{{"cursor", "Cursor"}, {"opencode", "OpenCode"}, {"muse", "Muse Code"}}, OpenCode2Supported: true, OpenCodeKiloSupported: true, RequiresPreparation: true}}
	upstream, err := url.Parse("http://" + u.owner.adminHost)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/clients/t3-code" && r.Method == http.MethodPost {
			f.mu.Lock()
			f.profilePosts++
			f.mu.Unlock()
		}
		if r.URL.Path == "/api/clients/t3-code" && r.Method == http.MethodGet {
			f.mu.Lock()
			gate, entered, profile := f.profileGate, f.profileEntered, f.profile
			f.mu.Unlock()
			if gate != nil {
				select {
				case entered <- struct{}{}:
				default:
				}
				select {
				case <-gate:
				case <-r.Context().Done():
					return
				}
				jsonResponse(w, 200, profile)
				return
			}
		}
		if r.URL.Path != nativeT3CodePreferencesEndpoint {
			proxy.ServeHTTP(w, r)
			return
		}
		if !secureEqual(r.Header.Get("Authorization"), "Bearer "+u.owner.adminToken) {
			t.Error("settings request omitted admin auth")
			jsonError(w, 401, "missing auth")
			return
		}
		f.mu.Lock()
		fail, gate := f.failGet, (chan struct{})(nil)
		if r.Method == http.MethodPost {
			var next nativeT3CodePreferences
			if json.NewDecoder(r.Body).Decode(&next) != nil {
				f.mu.Unlock()
				jsonError(w, 400, "bad settings")
				return
			}
			f.posts = append(f.posts, next)
			fail, gate = f.failPost, f.postGate
			if !fail {
				f.response.Preferences = next
			}
		}
		response := f.response
		omitConnection := f.omitOpenCodeConnection
		f.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		if fail {
			jsonError(w, 503, "Synthetic preferences unavailable")
			return
		}
		if omitConnection {
			data, _ := json.Marshal(response)
			var legacy map[string]any
			_ = json.Unmarshal(data, &legacy)
			delete(legacy["preferences"].(map[string]any), "openCodeConnection")
			jsonResponse(w, 200, legacy)
			return
		}
		jsonResponse(w, 200, response)
	}))
	u.owner.adminHost = strings.TrimPrefix(server.URL, "http://")
	t.Cleanup(server.Close)
	f.h = &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1000), now: time.Now()}
	f.h.frame()
	// Opening the harness starts profile and preference reads together. Finish
	// both before a test installs its own held profile response.
	nativeTestWait(t, u, func() bool {
		state := &u.agentsState().T3Code
		return state.PreferencesState.Loaded && state.Loaded && !u.busy["GET/api/clients/t3-code"]
	})
	f.h.frame()
	return f
}
func (f *nativeT3PreferencesFixture) postCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.posts)
}
func (f *nativeT3PreferencesFixture) lastPost() nativeT3CodePreferences {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts[len(f.posts)-1]
}

func TestNativeT3CodePreferencesSavePendingFailureAndRetry(t *testing.T) {
	f := newNativeT3PreferencesFixture(t, "en")
	u, h := f.u, f.h
	state := &u.agentsState().T3Code.PreferencesState
	before := state.Preferences
	f.mu.Lock()
	f.failPost = true
	f.postGate = make(chan struct{})
	gate := f.postGate
	f.mu.Unlock()
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	h.reveal("Codex", semantic.Switch)
	h.click("Codex", semantic.Switch)
	nativeTestWait(t, u, func() bool { return f.postCount() == 1 })
	if !u.t3CodePreferencesBusy() || state.Pending == nil || !nativeT3CodePreferencesEqual(state.Preferences, before) {
		t.Fatal("pending write replaced confirmed selection")
	}
	if u.uxhLaunchState("t3-code").canOpen {
		t.Fatal("Open remains enabled while preferences are unconfirmed")
	}
	h.frame()
	for _, node := range h.nodes() {
		if node.Desc.Class == semantic.Switch && node.Desc.Label == "Codex" && node.Desc.Selected {
			t.Fatal("pending checkbox jumped back before confirmation")
		}
	}
	for _, node := range h.nodes() {
		if node.Desc.Class == semantic.Switch && node.Desc.Label == "Claude" && !node.Desc.Disabled {
			t.Fatal("another change is enabled during save")
		}
	}
	finished := false
	var prepareErr error
	u.prepareClientAfter("t3-code", func(err error) { finished, prepareErr = true, err })
	nativeTestWait(t, u, func() bool { return finished })
	if prepareErr == nil {
		t.Fatal("unconfirmed settings allowed preparing")
	}
	f.mu.Lock()
	profilePosts := f.profilePosts
	f.mu.Unlock()
	if profilePosts != 0 || !u.t3CodePreferencesBusy() || state.Pending == nil {
		t.Fatal("pending settings reached profile preparation or lost their gate")
	}
	close(gate)
	nativeTestWait(t, u, func() bool { return !u.t3CodePreferencesBusy() })
	if state.Error == "" || state.Pending == nil || !nativeT3CodePreferencesEqual(state.Preferences, before) {
		t.Fatal("failed save was accepted or lost its retry policy")
	}
	if u.uxhLaunchState("t3-code").canOpen {
		t.Fatal("Open remains enabled after a failed preferences save")
	}
	f.mu.Lock()
	f.failPost = false
	f.postGate = nil
	f.mu.Unlock()
	h.frame()
	h.reveal("Retry saving", semantic.Button)
	h.click("Retry saving", semantic.Button)
	nativeTestWait(t, u, func() bool { return !u.t3CodePreferencesBusy() && state.Pending == nil })
	if f.postCount() != 2 || state.Preferences.Codex || !state.Preferences.Claude || !state.RequiresPreparation || state.Error != "" {
		t.Fatalf("retry did not confirm exact change: %+v", state)
	}
	expected := before
	expected.Codex = false
	if !nativeT3CodePreferencesEqual(f.lastPost(), expected) {
		t.Fatal("retry changed unrelated choices")
	}
}

func TestNativeT3CodePreferencesCapabilitiesAndOpenCodeVersion(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			f := newNativeT3PreferencesFixture(t, lang)
			u, h := f.u, f.h
			state := &u.agentsState().T3Code.PreferencesState
			h.reveal("Codex", semantic.Switch)
			h.target("Codex", semantic.Switch)
			h.target("Claude", semantic.Switch)
			labels := ""
			for _, node := range h.nodes() {
				labels += node.Desc.Label + "\n"
			}
			if strings.Contains(labels, "· Normal") || strings.Contains(labels, "Grok") {
				t.Fatal("duplicates or unsupported providers exposed")
			}
			nativeGridCapture(t, h, "native-t3-preferences-accounts-"+lang)
			h.reveal("OpenCode", semantic.Switch)
			h.click("OpenCode", semantic.Switch)
			nativeTestWait(t, u, func() bool { return f.postCount() == 1 && !u.t3CodePreferencesBusy() && state.Pending == nil })
			h.frame()
			h.reveal("OpenCode 2", semantic.RadioButton)
			h.click("OpenCode 2", semantic.RadioButton)
			nativeTestWait(t, u, func() bool { return f.postCount() == 2 && !u.t3CodePreferencesBusy() && state.Pending == nil })
			if !state.Preferences.OpenCode2 || !reflect.DeepEqual(state.Preferences.Providers, []string{"opencode"}) {
				t.Fatal("OpenCode 2 was not selected exclusively")
			}
			h.frame()
			nativeGridCapture(t, h, "native-t3-preferences-opencode-"+lang)
			h.reveal("OpenCode", semantic.Switch)
			h.click("OpenCode", semantic.Switch)
			nativeTestWait(t, u, func() bool { return f.postCount() == 3 && !u.t3CodePreferencesBusy() && state.Pending == nil })
			if state.Preferences.OpenCode2 || len(state.Preferences.Providers) != 0 {
				t.Fatal("disabling OpenCode retained second runtime")
			}
		})
	}
}

func TestNativeT3CodePreferencesFailedLoadAndLateResponse(t *testing.T) {
	f := newNativeT3PreferencesFixture(t, "en")
	u := f.u
	s := &u.agentsState().T3Code.PreferencesState
	f.mu.Lock()
	f.failGet = true
	f.mu.Unlock()
	s.Loaded = false
	u.refreshT3CodePreferences()
	nativeTestWait(t, u, func() bool { return !u.busy["GET"+nativeT3CodePreferencesEndpoint] })
	if s.Loaded || s.Error == "" {
		t.Fatal("unreadable preferences appeared loaded")
	}
	f.mu.Lock()
	f.failGet = false
	f.mu.Unlock()
	u.refreshT3CodePreferences()
	nativeTestWait(t, u, func() bool { return s.Loaded })
	old := s.Preferences
	u.refreshT3CodePreferences()
	s.Generation++ // A newer settings operation supersedes this GET.
	s.Preferences.Codex = false
	nativeTestWait(t, u, func() bool { return !u.busy["GET"+nativeT3CodePreferencesEndpoint] })
	if s.Preferences.Codex == old.Codex {
		t.Fatal("late GET overwrote newer preference state")
	}
}

func TestNativeT3CodePreferencesCanDisableChoicesUnavailableInNewBuild(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			f := newNativeT3PreferencesFixture(t, lang)
			u, h := f.u, f.h
			f.mu.Lock()
			f.response.Preferences.Providers = []string{"cursor", "opencode"}
			f.response.Preferences.OpenCode2 = true
			f.response.SupportedProviders = []nativeT3CodeProvider{{"opencode", "OpenCode"}, {"unknown", "Unknown provider"}}
			f.response.OpenCode2Supported = false
			f.mu.Unlock()
			u.refreshT3CodePreferences()
			nativeTestWait(t, u, func() bool { return !u.busy["GET"+nativeT3CodePreferencesEndpoint] })
			h.frame()
			cursor := u.tr("Cursor · unavailable in this build", "Cursor · no disponible en esta compilación")
			h.reveal(cursor, semantic.Switch)
			if !h.selected(cursor, semantic.Switch) {
				t.Fatal("saved unsupported choice is hidden or cannot be switched off")
			}
			h.reveal("OpenCode 1", semantic.RadioButton)
			for _, node := range h.nodes() {
				if node.Desc.Label == "Unknown provider" {
					t.Fatal("unknown provider was offered")
				}
			}
			state := &u.agentsState().T3Code.PreferencesState
			if !state.Preferences.OpenCode2 || state.OpenCode2Supported {
				t.Fatal("saved OpenCode 2 choice was removed or falsely reported as supported")
			}
			nativeGridCapture(t, h, "native-t3-preferences-unavailable-"+lang)
			h.reveal(cursor, semantic.Switch)
			h.click(cursor, semantic.Switch)
			nativeTestWait(t, u, func() bool { return f.postCount() == 1 && !u.t3CodePreferencesBusy() })
			h.frame()
			h.reveal("OpenCode 1", semantic.RadioButton)
			h.click("OpenCode 1", semantic.RadioButton)
			nativeTestWait(t, u, func() bool { return f.postCount() == 2 && !u.t3CodePreferencesBusy() })
			p := u.agentsState().T3Code.PreferencesState.Preferences
			if p.OpenCode2 || !reflect.DeepEqual(p.Providers, []string{"opencode"}) {
				t.Fatalf("unavailable choices did not clear independently: %+v", p)
			}
			h.frame()
			for _, node := range h.nodes() {
				if node.Desc.Label == cursor || node.Desc.Class == semantic.RadioButton && node.Desc.Label == "OpenCode 2" {
					t.Fatal("unavailable choices can be enabled again after removal")
				}
			}
		})
	}
}

func TestNativeT3CodeModelLimitAppliesToSharedAccountsOnly(t *testing.T) {
	u := nativeTestUI(t)
	models := make([]modelInfo, 40)
	for i := range models {
		models[i] = modelInfo{ID: "vendor/model-" + strconv.Itoa(i), Name: "Fixture model", ContextWindow: 128000}
	}
	nativeSeedSharedForTest(t, u, models...)
	payload, err := nativeClientPayload("t3-code", u.sharedClientSelection("t3-code"))
	if err != nil {
		t.Fatal("valid global library rejected", err)
	}
	library := payload.(map[string]any)["library"].(modelLibrary)
	p := nativeT3CodePreferences{Version: 1, KiloCodex: true, KiloClaude: true, CodexModels: "official", ClaudeModels: "official"}
	if err := nativeT3CodePreferenceLibraryError(p, library); err != nil {
		t.Fatal("official mode rejected unrelated shared library", err)
	}
	p.ClaudeModels = "shared"
	if nativeT3CodePreferenceLibraryError(p, library) == nil {
		t.Fatal("shared Claude account accepted more than32 models")
	}
	p.KiloClaude = false
	if err := nativeT3CodePreferenceLibraryError(p, library); err != nil {
		t.Fatal("disabled account imposed its limit", err)
	}
	p.CodexModels = "shared"
	if nativeT3CodePreferenceLibraryError(p, library) == nil {
		t.Fatal("shared Codex account accepted more than32 models")
	}
}

func TestNativeT3CodePreferencesIgnorePreparedProfileReadBeforeSettingsSave(t *testing.T) {
	f := newNativeT3PreferencesFixture(t, "en")
	u := f.u
	state := &u.agentsState().T3Code
	before := state.PreferencesState.Preferences
	library := u.modelLibraryValue()
	old := nativeT3CodeInfo{Prepared: true, Library: &library, Preferences: before}
	data, _ := json.Marshal(old)
	u.acceptT3CodeProfile(data, u.launchConnectionFingerprint())
	f.mu.Lock()
	f.profileGate = make(chan struct{})
	gate := f.profileGate
	f.profileEntered = make(chan struct{}, 1)
	f.profile = old
	f.mu.Unlock()
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	u.refreshT3CodeProfile()
	select {
	case <-f.profileEntered:
	case <-time.After(time.Second):
		t.Fatal("profile read did not start")
	}
	next := before
	next.Codex = false
	u.saveT3CodePreferences(next)
	nativeTestWait(t, u, func() bool { return !u.t3CodePreferencesBusy() && state.PreferencesState.Pending == nil })
	close(gate)
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/clients/t3-code"] })
	if state.Info.Prepared || state.PreferencesState.Preferences.Codex || u.t3CodePrepared(u.sharedClientSelection("t3-code")) {
		t.Fatal("old prepared response erased the need to reprepare")
	}
}

func TestNativeT3ControlsLiveInHarnessGearAndLegacySettingsRedirect(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			f := newNativeT3PreferencesFixture(t, lang)
			u, h := f.u, f.h
			for _, width := range []int{1120, 780} {
				h.size = image.Pt(width, 1600)
				u.page, u.settingsTab = "settings", ""
				h.frame()
				for _, section := range uxsSections {
					if section.id == "t3-code" {
						t.Fatal("T3 remains in global settings index")
					}
				}
				for _, node := range h.nodes() {
					if node.Desc.Class == semantic.Switch && (node.Desc.Label == "Codex" || node.Desc.Label == "Claude" || node.Desc.Label == "OpenCode" || node.Desc.Label == "Muse Code") {
						t.Fatal("T3 controls duplicated in global settings", node.Desc.Label)
					}
				}
				u.page = "agents"
				h.frame()
				gear := u.tr("T3 Code settings", "Ajustes de T3 Code")
				h.reveal(gear, semantic.Button)
				h.click(gear, semantic.Button)
				if u.page != "clients" || u.client != "t3-code" {
					t.Fatal("T3 gear did not open its harness detail", u.page, u.client)
				}
				h.frame()
				h.reveal("Codex", semantic.Switch)
				h.target("Codex", semantic.Switch)
				h.target("Claude", semantic.Switch)
				for _, label := range []string{"OpenCode", "Muse Code"} {
					h.reveal(label, semantic.Switch)
					h.target(label, semantic.Switch)
				}
				h.reveal("Codex", semantic.Switch)
				nativeGridCapture(t, h, "native-t3-gear-accounts-"+strconv.Itoa(width)+"-"+lang)
				h.reveal("Muse Code", semantic.Switch)
				nativeGridCapture(t, h, "native-t3-gear-providers-"+strconv.Itoa(width)+"-"+lang)
				u.page, u.settingsTab = "settings", "t3-code"
				h.frame()
				h.frame()
				if u.page != "clients" || u.client != "t3-code" || u.settingsTab != "" {
					t.Fatal("legacy T3 settings link did not redirect", u.page, u.client, u.settingsTab)
				}
				u.page = "settings"
				u.uxs().jump = "t3-code"
				h.frame()
				h.frame()
				if u.page != "clients" || u.client != "t3-code" || u.uxs().jump != "" {
					t.Fatal("pending legacy T3 jump did not redirect")
				}
			}
			if f.postCount() != 0 {
				t.Fatal("navigation changed saved provider preferences")
			}
		})
	}
}

func TestNativeT3OpenCodeConnectionCompatibilityAndGatewayGate(t *testing.T) {
	p := nativeT3CodePreferences{Version: 1, CodexModels: "official", ClaudeModels: "official", Providers: []string{"opencode"}}
	explicit := p
	explicit.OpenCodeConnection = "native"
	if !validNativeT3CodePreferences(p) || !nativeT3CodePreferencesEqual(p, explicit) || nativeT3OpenCodeUsesKilo(p) {
		t.Fatal("old preferences lost native compatibility")
	}
	p.OpenCodeConnection = "other"
	if validNativeT3CodePreferences(p) {
		t.Fatal("invalid OpenCode connection accepted")
	}
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			f := newNativeT3PreferencesFixture(t, lang)
			u := f.u
			state := &u.agentsState().T3Code.PreferencesState
			state.Preferences = explicit
			if !u.t3CodeNativeOnly() {
				t.Fatal("own OpenCode connection requires Kilo")
			}
			state.Preferences.OpenCodeConnection = "kilo"
			if u.t3CodeNativeOnly() {
				t.Fatal("Kilo OpenCode bypassed the gateway")
			}
			nativeSeedSharedForTest(t, u)
			if u.uxhLaunchState("t3-code").canOpen {
				t.Fatal("Kilo OpenCode opened without shared models")
			}
			u.owner.mu.Lock()
			u.owner.apiKey = ""
			u.owner.publishUIProjectionLocked()
			u.owner.mu.Unlock()
			nativeSeedSharedForTest(t, u, u.models[0])
			if launch := u.uxhLaunchState("t3-code"); launch.canOpen || !strings.Contains(launch.reason, u.tr("Connect Kilo or ChatGPT", "Conecta Kilo o ChatGPT")) {
				t.Fatalf("Kilo OpenCode ignored its connection: %+v", launch)
			}
		})
	}
}

func TestNativeT3OpenCodeConnectionSavesSummaryAndResponsiveLayout(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			f := newNativeT3PreferencesFixture(t, lang)
			u, h := f.u, f.h
			state := &u.agentsState().T3Code.PreferencesState
			h.reveal("OpenCode", semantic.Switch)
			h.click("OpenCode", semantic.Switch)
			nativeTestWait(t, u, func() bool { return f.postCount() == 1 && !u.t3CodePreferencesBusy() && state.Pending == nil })
			h.frame()
			kilo := u.tr("Kilo Proxy · shared models", "Kilo Proxy · modelos compartidos")
			h.reveal(kilo, semantic.RadioButton)
			h.click(kilo, semantic.RadioButton)
			nativeTestWait(t, u, func() bool { return f.postCount() == 2 && !u.t3CodePreferencesBusy() && state.Pending == nil })
			if state.Preferences.OpenCodeConnection != "kilo" || f.lastPost().OpenCodeConnection != "kilo" || state.Preferences.OpenCode2 {
				t.Fatal("connection did not save independently of major")
			}
			h.frame()
			h.reveal("OpenCode 2", semantic.RadioButton)
			h.click("OpenCode 2", semantic.RadioButton)
			nativeTestWait(t, u, func() bool {
				return f.postCount() == 3 && !u.t3CodePreferencesBusy() && state.Pending == nil && !u.busy["GET/api/clients/t3-code"]
			})
			if state.Preferences.OpenCodeConnection != "kilo" || !state.Preferences.OpenCode2 {
				t.Fatal("major change lost the connection")
			}
			gate := make(chan struct{})
			close(gate)
			f.mu.Lock()
			f.profileGate = gate
			f.profile = nativeT3CodeInfo{Preferences: state.Preferences, EffectiveOpenCodeModels: []nativeT3CodeModelSummary{{ID: "vendor/one", Name: "Shared coding model"}}}
			f.mu.Unlock()
			u.refreshT3CodeProfile()
			nativeTestWait(t, u, func() bool { return !u.busy["GET/api/clients/t3-code"] })
			for _, width := range []int{1120, 780} {
				h.size = image.Pt(width, 1400)
				h.frame()
				h.reveal(kilo, semantic.RadioButton)
				h.target(kilo, semantic.RadioButton)
				h.reveal(u.tr("Own OpenCode account", "Cuenta propia de OpenCode"), semantic.RadioButton)
				if !nativeUpdateHasText(h, "Shared coding model · vendor/one") {
					t.Fatal("confirmed OpenCode Kilo summary absent")
				}
				nativeGridCapture(t, h, "native-t3-opencode-connection-"+strconv.Itoa(width)+"-"+lang)
			}
			own := u.tr("Own OpenCode account", "Cuenta propia de OpenCode")
			h.reveal(own, semantic.RadioButton)
			h.click(own, semantic.RadioButton)
			nativeTestWait(t, u, func() bool { return f.postCount() == 4 && !u.t3CodePreferencesBusy() && state.Pending == nil })
			if state.Preferences.OpenCodeConnection != "native" || !state.Preferences.OpenCode2 {
				t.Fatal("own connection reset the selected major")
			}
			h.frame()
			if nativeUpdateHasText(h, "Shared coding model · vendor/one") {
				t.Fatal("own connection shows a stale Kilo model summary")
			}
			for _, width := range []int{1120, 780} {
				h.size = image.Pt(width, 1400)
				h.frame()
				h.reveal(own, semantic.RadioButton)
				loginNote := u.tr("OpenCode 2 has a separate profile that may need its own login.", "OpenCode 2 tiene un perfil separado que puede requerir su propio inicio de sesión.")
				explained := false
				for _, node := range h.nodes() {
					explained = explained || strings.Contains(node.Desc.Label, loginNote)
				}
				if !explained {
					t.Fatal("native OpenCode 2 login isolation was not explained")
				}
				nativeGridCapture(t, h, "native-t3-opencode-native-v2-"+strconv.Itoa(width)+"-"+lang)
			}
		})
	}
}

func TestNativeT3OpenCodeConnectionRequiresExactConfirmationAndSupportedDriver(t *testing.T) {
	f := newNativeT3PreferencesFixture(t, "en")
	u := f.u
	state := &u.agentsState().T3Code.PreferencesState
	f.mu.Lock()
	f.omitOpenCodeConnection = true
	f.mu.Unlock()
	u.refreshT3CodePreferences()
	nativeTestWait(t, u, func() bool { return !u.busy["GET"+nativeT3CodePreferencesEndpoint] })
	next := state.Preferences
	next.Providers, next.OpenCodeConnection = []string{"opencode"}, "kilo"
	u.saveT3CodePreferences(next)
	nativeTestWait(t, u, func() bool { return !u.t3CodePreferencesBusy() })
	if state.Pending == nil || state.Error == "" || nativeT3OpenCodeUsesKilo(state.Preferences) {
		t.Fatal("unconfirmed Kilo connection replaced the saved choice")
	}
	f.mu.Lock()
	f.omitOpenCodeConnection = false
	f.response.OpenCodeKiloSupported = false
	f.mu.Unlock()
	u.saveT3CodePreferences(*state.Pending)
	nativeTestWait(t, u, func() bool { return !u.t3CodePreferencesBusy() && state.Pending == nil })
	if state.Preferences.OpenCodeConnection != "kilo" || u.t3CodeOpenCodeConnectionError() == nil || u.uxhLaunchState("t3-code").canOpen {
		t.Fatal("unsupported saved Kilo choice was silently changed or launchable")
	}
	f.h.frame()
	f.h.reveal("Own OpenCode account", semantic.RadioButton)
	f.h.click("Own OpenCode account", semantic.RadioButton)
	nativeTestWait(t, u, func() bool { return f.postCount() == 3 && !u.t3CodePreferencesBusy() && state.Pending == nil })
	if state.Preferences.OpenCodeConnection != "native" || u.t3CodeOpenCodeConnectionError() != nil {
		t.Fatal("own account still depends on Kilo driver capability")
	}
}

func TestNativeT3MuseConnectionCompatibilityGatewayAndCapability(t *testing.T) {
	legacy := nativeT3CodePreferences{Version: 1, CodexModels: "official", ClaudeModels: "official", Providers: []string{"muse"}}
	explicit := legacy
	explicit.MuseConnection = "native"
	if !nativeT3CodePreferencesEqual(legacy, explicit) || !validNativeT3CodePreferences(legacy) || nativeT3MuseUsesKilo(legacy) {
		t.Fatal("legacy Muse changed its native account")
	}
	var malformed nativeT3CodePreferences
	if json.Unmarshal([]byte(`{"version":1,"museConnection":null}`), &malformed) == nil {
		t.Fatal("null Muse response was treated as legacy native")
	}
	invalid := legacy
	invalid.MuseConnection = "other"
	if validNativeT3CodePreferences(invalid) {
		t.Fatal("invalid Muse mode accepted")
	}
	for _, language := range []string{"en", "es"} {
		t.Run(language, func(t *testing.T) {
			f := newNativeT3PreferencesFixture(t, language)
			u := f.u
			s := &u.agentsState().T3Code.PreferencesState
			s.Preferences = legacy
			if !u.t3CodeNativeOnly() {
				t.Fatal("legacy Muse requires Kilo")
			}
			s.Preferences.MuseConnection = "kilo"
			s.MuseKiloSupported = true
			if u.t3CodeNativeOnly() {
				t.Fatal("Muse Kilo-only bypassed shared connection")
			}
			nativeSeedSharedForTest(t, u)
			if u.uxhLaunchState("t3-code").canOpen {
				t.Fatal("Muse Kilo opened without shared models")
			}
			s.MuseKiloSupported = false
			if u.t3CodeMuseConnectionError() == nil || u.uxhLaunchState("t3-code").canOpen {
				t.Fatal("missing Muse isolation capability was ignored")
			}
			s.Preferences.MuseConnection = "native"
			if u.t3CodeMuseConnectionError() != nil || !u.t3CodeNativeOnly() {
				t.Fatal("native Muse still depends on Kilo capability")
			}
		})
	}
}

func TestNativeT3MuseConnectionControlsKeepExactAccountAndModelSummary(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		t.Run(language, func(t *testing.T) {
			f := newNativeT3PreferencesFixture(t, language)
			u, h := f.u, f.h
			state := &u.agentsState().T3Code.PreferencesState
			f.mu.Lock()
			f.response.MuseKiloSupported = true
			f.mu.Unlock()
			u.refreshT3CodePreferences()
			nativeTestWait(t, u, func() bool { return !u.t3CodePreferencesBusy() && state.MuseKiloSupported })
			h.frame()
			h.reveal("Muse Code", semantic.Switch)
			h.click("Muse Code", semantic.Switch)
			nativeTestWait(t, u, func() bool {
				return f.postCount() == 1 && !u.t3CodePreferencesBusy() && state.Pending == nil && !u.busy["GET/api/clients/t3-code"]
			})
			if !nativeT3MuseUsesKilo(state.Preferences) || f.lastPost().MuseConnection != "kilo" {
				t.Fatal("newly enabled Muse did not explicitly save its Kilo connection")
			}
			gate := make(chan struct{})
			close(gate)
			f.mu.Lock()
			f.profileGate = gate
			f.profile = nativeT3CodeInfo{Preferences: state.Preferences, EffectiveMuseModels: []nativeT3CodeModelSummary{{ID: "vendor/one", Name: "Shared Muse model"}}}
			f.mu.Unlock()
			u.refreshT3CodeProfile()
			nativeTestWait(t, u, func() bool { return !u.busy["GET/api/clients/t3-code"] })
			own := u.tr("Own Meta account", "Cuenta propia de Meta")
			for _, width := range []int{1120, 780} {
				h.size = image.Pt(width, 1400)
				h.frame()
				h.reveal(own, semantic.RadioButton)
				h.target(own, semantic.RadioButton)
				if !nativeUpdateHasText(h, "Shared Muse model · vendor/one") {
					t.Fatal("confirmed Muse Kilo summary was not shown")
				}
				nativeGridCapture(t, h, "native-t3-muse-kilo-"+strconv.Itoa(width)+"-"+language)
			}
			h.click(own, semantic.RadioButton)
			nativeTestWait(t, u, func() bool { return f.postCount() == 2 && !u.t3CodePreferencesBusy() && state.Pending == nil })
			if state.Preferences.MuseConnection != "native" || f.lastPost().MuseConnection != "native" || !helperContains(state.Preferences.Providers, "muse") {
				t.Fatal("own Meta choice changed the provider or failed to save")
			}
			for _, width := range []int{1120, 780} {
				h.size = image.Pt(width, 1400)
				h.frame()
				h.reveal(own, semantic.RadioButton)
				if nativeUpdateHasText(h, "Shared Muse model · vendor/one") {
					t.Fatal("native Meta displayed an old Kilo model summary")
				}
				nativeGridCapture(t, h, "native-t3-muse-native-"+strconv.Itoa(width)+"-"+language)
			}
		})
	}
}

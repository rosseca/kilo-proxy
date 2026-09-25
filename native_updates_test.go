//go:build desktop

package main

import (
	"encoding/json"
	"image"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

const nativeUpdateTestURL = "https://github.com/rosseca/kilo-proxy/releases/tag/v0.51.0"

type nativeUpdateFixture struct {
	h         *nativePointerHarness
	mu        sync.Mutex
	state     releaseUpdateState
	posts     int
	failPost  bool
	postGate  <-chan struct{}
	stateGate <-chan struct{}
	stateSeen chan struct{}
}

func newNativeUpdateFixture(t *testing.T, language string, size image.Point) *nativeUpdateFixture {
	t.Helper()
	u := nativeTestUI(t)
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
	u.language = language
	u.state["language"] = language
	base, err := json.Marshal(u.state)
	if err != nil {
		t.Fatal(err)
	}
	f := &nativeUpdateFixture{state: releaseUpdateState{CurrentVersion: "0.50.0"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !secureEqual(r.Header.Get("Authorization"), "Bearer "+u.owner.adminToken) {
			t.Error("update request omitted admin authentication")
			jsonError(w, 401, "missing authentication")
			return
		}
		f.mu.Lock()
		state := f.state
		switch r.URL.Path {
		case "/api/state":
			gate, seen := f.stateGate, f.stateSeen
			f.stateGate, f.stateSeen = nil, nil
			f.mu.Unlock()
			if seen != nil {
				close(seen)
			}
			if gate != nil {
				<-gate
			}
			var result map[string]any
			_ = json.Unmarshal(base, &result)
			result["update"] = state
			jsonResponse(w, 200, result)
		case "/api/updates":
			gate, fail := f.postGate, f.failPost
			if r.Method == "POST" {
				f.posts++
			}
			f.mu.Unlock()
			if gate != nil {
				<-gate
			}
			if fail {
				jsonError(w, 503, "synthetic update service failure")
				return
			}
			jsonResponse(w, 200, state)
		default:
			f.mu.Unlock()
			t.Errorf("unexpected request in isolated update fixture: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	u.owner.adminHost = strings.TrimPrefix(server.URL, "http://")
	// Keep unrelated discovery and profile inspection out of this fixture.
	c := u.clientState()
	c.LaunchChecked, c.LaunchDetectStarted = true, true
	c.OpenDesignChecked, c.OpenDesignDetectStarted = true, true
	u.terminalCommands = &nativeTerminalCommands{Started: true, Checked: true, ManualStarted: true, ManualChecked: true}
	u.page = "settings"
	f.h = &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
	f.refresh(t)
	return f
}

func (f *nativeUpdateFixture) refresh(t *testing.T) {
	t.Helper()
	u := f.h.u
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
	u.refreshState()
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
	f.h.frame()
}

func (f *nativeUpdateFixture) setState(state releaseUpdateState) {
	f.mu.Lock()
	f.state = state
	f.mu.Unlock()
}

func (f *nativeUpdateFixture) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts
}

func nativeUpdateHasText(h *nativePointerHarness, text string) bool {
	for _, node := range h.nodes() {
		if node.Desc.Label == text {
			return true
		}
	}
	return false
}

func TestNativeUpdateStates(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		t.Run(language, func(t *testing.T) {
			f := newNativeUpdateFixture(t, language, image.Pt(780, 700))
			h, u := f.h, f.h.u
			checked := "2026-09-25T12:30:00Z"
			for _, test := range []struct {
				name    string
				state   releaseUpdateState
				en, es  string
				current bool
			}{
				{"unchecked", releaseUpdateState{CurrentVersion: "0.50.0"}, "Not checked yet", "Sin comprobar", false},
				{"checking", releaseUpdateState{CurrentVersion: "0.50.0", Checking: true}, "Checking for updates…", "Comprobando actualizaciones…", false},
				{"error", releaseUpdateState{CurrentVersion: "0.50.0", LatestVersion: "0.50.0", CheckedAt: checked, Error: "GitHub unavailable", ReleaseURL: nativeUpdateTestURL}, "Could not check for updates. Try again.", "No se han podido comprobar las actualizaciones. Inténtalo de nuevo.", false},
				{"current", releaseUpdateState{CurrentVersion: "0.50.0", LatestVersion: "0.50.0", CheckedAt: checked, ReleaseURL: "https://github.com/rosseca/kilo-proxy/releases/tag/v0.50.0"}, "Up to date", "Actualizado", true},
				{"available", releaseUpdateState{CurrentVersion: "0.50.0", LatestVersion: "0.51.0", Available: true, CheckedAt: checked, ReleaseURL: nativeUpdateTestURL}, "Version 0.51.0 is available", "La versión 0.51.0 está disponible", false},
				{"invalid-release", releaseUpdateState{CurrentVersion: "0.50.0", LatestVersion: "0.51.0", Available: true, CheckedAt: checked, ReleaseURL: "https://example.test/releases/tag/v0.51.0"}, "Not checked yet", "Sin comprobar", false},
			} {
				t.Run(test.name, func(t *testing.T) {
					f.setState(test.state)
					f.refresh(t)
					if !nativeUpdateHasText(h, u.tr(test.en, test.es)) || nativeUpdateHasText(h, u.tr("Up to date", "Actualizado")) != test.current {
						t.Fatalf("wrong update status for %+v", test.state)
					}
					if !nativeUpdateHasText(h, u.tr("Current version: 0.50.0", "Versión actual: 0.50.0")) {
						t.Fatal("current version missing")
					}
					if test.state.CheckedAt != "" && !nativeUpdateHasText(h, u.tr("Last checked: ", "Última comprobación: ")+nativeUpdatedAt(checked)) {
						t.Fatal("last-check timestamp missing")
					}
				})
			}
			if f.count() != 0 {
				t.Fatal("rendering settings triggered an update check")
			}
		})
	}
}

func TestNativeUpdateCheckRetryAndRelease(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {780, 700}} {
		for _, language := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"-"+language, func(t *testing.T) {
				f := newNativeUpdateFixture(t, language, size)
				h, u := f.h, f.h.u
				check := u.tr("Check for updates", "Comprobar actualizaciones")
				f.mu.Lock()
				f.failPost = true
				f.mu.Unlock()
				h.reveal(check, semantic.Button)
				h.click(check, semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.busy["POST/api/updates"] && u.updateRequestFailed })
				h.frame()
				if !nativeUpdateHasText(h, u.tr("Could not check for updates. Try again.", "No se han podido comprobar las actualizaciones. Inténtalo de nuevo.")) || u.notice != "" {
					t.Fatal("request failure did not stay in the update section")
				}
				nativeGridCapture(t, h, "updates-error-"+fmtSize(size)+"-"+language)
				gate := make(chan struct{})
				var release sync.Once
				unblock := func() { release.Do(func() { close(gate) }) }
				t.Cleanup(unblock)
				f.mu.Lock()
				f.failPost, f.postGate = false, gate
				f.state = releaseUpdateState{CurrentVersion: "0.50.0", Checking: true}
				f.mu.Unlock()
				h.frame()
				h.reveal(check, semantic.Button)
				h.click(check, semantic.Button)
				nativeTestWait(t, u, func() bool { return f.count() == 2 })
				h.frame()
				if !nativeUpdateHasText(h, u.tr("Checking for updates…", "Comprobando actualizaciones…")) {
					t.Fatal("pending manual request did not show checking")
				}
				// Neither another action nor a queued click can duplicate the POST.
				u.checkForUpdates()
				u.clickable("updates.check").Click()
				h.frame()
				unblock()
				nativeTestWait(t, u, func() bool { return !u.busy["POST/api/updates"] })
				if f.count() != 2 || !u.releaseUpdate().Checking {
					t.Fatal("manual checking was duplicated or did not retain backend progress")
				}
				f.setState(releaseUpdateState{CurrentVersion: "0.50.0", LatestVersion: "0.51.0", Available: true, CheckedAt: "2026-09-25T12:30:00Z", ReleaseURL: nativeUpdateTestURL})
				f.refresh(t)
				h.reveal(check, semantic.Button)
				nativeGridCapture(t, h, "updates-settings-"+fmtSize(size)+"-"+language)
				h.click(u.tr("Download update", "Descargar actualización"), semantic.Button)
				bridge := u.owner.desktop.(*nativeRecordingBridge)
				nativeTestWait(t, u, func() bool {
					bridge.mu.Lock()
					defer bridge.mu.Unlock()
					return bridge.URL == nativeUpdateTestURL
				})
				u.page = "agents"
				h.frame()
				if !nativeUpdateHasText(h, u.tr("Kilo Proxy 0.51.0 is available", "Kilo Proxy 0.51.0 está disponible")) {
					t.Fatal("Agents did not show the available-update notice")
				}
				if !h.target(u.tr("Download update", "Descargar actualización"), semantic.Button).Desc.Bounds.In(image.Rectangle{Max: size}) {
					t.Fatal("update notice action is clipped")
				}
				nativeGridCapture(t, h, "updates-agents-"+fmtSize(size)+"-"+language)
				if nativeBool(u.state, "running") || f.count() != 2 {
					t.Fatal("opening a release started the proxy or another check")
				}
			})
		}
	}
}

func TestNativeUpdateRejectsUntrustedReleaseURLs(t *testing.T) {
	f := newNativeUpdateFixture(t, "en", image.Pt(1180, 820))
	u := f.h.u
	bridge := u.owner.desktop.(*nativeRecordingBridge)
	for _, address := range []string{
		"https://example.test/releases/tag/v0.51.0",
		"https://github.com/another/kilo-proxy/releases/tag/v0.51.0",
		"https://github.com/rosseca/kilo-proxy/releases/tag/v0.51.0?download=1",
		"https://github.com/rosseca/kilo-proxy/releases/tag/v0.51.0#fragment",
		"https://github.com@evil.test/rosseca/kilo-proxy/releases/tag/v0.51.0",
		"https://github.com/rosseca/kilo-proxy/releases/tag/v0.51.0-alpha.1",
		"file:///tmp/update",
	} {
		f.setState(releaseUpdateState{CurrentVersion: "0.50.0", LatestVersion: "0.51.0", Available: true, ReleaseURL: address})
		f.refresh(t)
		u.openUpdateRelease()
		if u.updateAvailable() || nativeUpdateHasText(f.h, "Download update") {
			t.Fatalf("untrusted release action appeared: %q", address)
		}
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.URL != "" {
		t.Fatalf("untrusted release was opened: %q", bridge.URL)
	}
}

func TestNativeUpdateManualCheckIgnoresOlderPoll(t *testing.T) {
	f := newNativeUpdateFixture(t, "en", image.Pt(1180, 820))
	u := f.h.u
	gate, seen := make(chan struct{}), make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(gate) }) }
	t.Cleanup(unblock)
	f.mu.Lock()
	f.stateGate, f.stateSeen = gate, seen
	f.mu.Unlock()
	u.refreshState()
	select {
	case <-seen:
	case <-time.After(3 * time.Second):
		t.Fatal("state request did not reach fixture")
	}
	f.setState(releaseUpdateState{CurrentVersion: "0.50.0", Checking: true})
	u.checkForUpdates()
	nativeTestWait(t, u, func() bool { return !u.busy["POST/api/updates"] && u.releaseUpdate().Checking })
	unblock()
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
	if !u.releaseUpdate().Checking {
		t.Fatal("old poll erased the newer manual-check status")
	}
}

func TestNativeUpdatePollRecoversAfterManualRequestError(t *testing.T) {
	f := newNativeUpdateFixture(t, "en", image.Pt(780, 700))
	u := f.h.u
	f.mu.Lock()
	f.failPost = true
	f.mu.Unlock()
	u.checkForUpdates()
	nativeTestWait(t, u, func() bool { return !u.busy["POST/api/updates"] && u.updateRequestFailed })
	f.refresh(t)
	if !u.updateRequestFailed {
		t.Fatal("an unchanged snapshot hid the manual request failure")
	}
	f.setState(releaseUpdateState{CurrentVersion: "0.50.0", LatestVersion: "0.50.0", CheckedAt: "2026-09-25T12:30:00Z", ReleaseURL: "https://github.com/rosseca/kilo-proxy/releases/tag/v0.50.0"})
	f.refresh(t)
	if u.updateRequestFailed || !nativeUpdateHasText(f.h, "Up to date") {
		t.Fatal("a later automatic result retained the old manual request error")
	}
}

//go:build desktop

package main

import (
	"image"
	"net"
	"strings"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func TestNativeOpenDesignHelperCopiesCurrentConnectionAndExactSharedIDs(t *testing.T) {
	u, _ := nativeLaunchTestUI(t, "open-design", false, false)
	nativeSeedSharedForTest(t, u, nativeClientModelsForTest()...)
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Friendly name, not an API ID")
	u.expanded["open-design:models"] = true
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	bridge := u.owner.desktop.(*nativeRecordingBridge)
	// No user action has copied any credential or model.
	bridge.mu.Lock()
	initial := bridge.Text
	bridge.mu.Unlock()
	if initial != "" {
		t.Fatal("rendering helper wrote to the clipboard")
	}
	copyControl := func(control, want string) {
		t.Helper()
		u.clickable(control).Click()
		nativeTestFrame(t, u)
		bridge.mu.Lock()
		got := bridge.Text
		bridge.mu.Unlock()
		if got != want {
			t.Fatalf("%s copied an incorrect value", control)
		}
	}
	base, local := u.openDesignConnection()
	copyControl("open-design:copy-url", base)
	copyControl("open-design:copy-key", local)
	copyControl("open-design:copy-model", "vendor/one")
	copyControl("open-design:copy-model:anthropic/claude-sonnet-4.6", "anthropic/claude-sonnet-4.6")
	// A saved connection can change before a state refresh. Copy must use it.
	u.owner.mu.Lock()
	u.owner.config.Port = 19877
	u.owner.config.LocalKey = "kl_local_synthetic_rotated_key"
	u.owner.mu.Unlock()
	copyControl("open-design:copy-url", "http://127.0.0.1:19877/v1")
	copyControl("open-design:copy-key", "kl_local_synthetic_rotated_key")
	nativeSeedSharedForTest(t, u, nativeClientModelsForTest()[1])
	copyControl("open-design:copy-model", "anthropic/claude-sonnet-4.6")
}

func TestNativeOpenDesignLaunchStartsProxyWithoutPreparingProfile(t *testing.T) {
	u, r := nativeLaunchTestUI(t, "open-design", false, false)
	u.setValue("clients-project-directory", "/missing/ignored/project")
	u.setValue(agentProjectField("open-design"), "/another/ignored/project")
	start := u.owner.launcher.start
	u.owner.launcher.start = func(plan clientLaunchPlan) error {
		u.owner.mu.Lock()
		listener := u.owner.proxyListener
		u.owner.mu.Unlock()
		if listener == nil {
			t.Error("Open Design opened before the proxy started")
		} else {
			conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
			if err != nil {
				t.Errorf("proxy was not reachable at launch: %v", err)
			} else {
				conn.Close()
			}
		}
		return start(plan)
	}
	nativeTestFrame(t, u)
	u.clickable("client:open-design:launch").Click()
	nativeTestFrame(t, u)
	nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
	if r.count() != 1 || r.prepares.Load() != 0 || r.launchRequests.Load() != 1 {
		t.Fatalf("expected direct launch without managed-profile preparation: %s", u.notice)
	}
	r.mu.Lock()
	plan := r.plans[0]
	r.mu.Unlock()
	if len(plan.Env) != 0 || len(plan.Args) != 0 {
		t.Fatal("Open Design launch received unexpected environment or project arguments")
	}
	prefs, err := readAgentPreferences(u.owner.dir)
	if err != nil || prefs.Projects["open-design"] != "" {
		t.Fatal("Open Design remembered an unsupported project folder")
	}
	if u.clientState().selection("open-design").Saved != "" {
		t.Fatal("manual provider setup was falsely marked as automatically saved")
	}
}

func TestNativeOpenDesignHelperLaunchGates(t *testing.T) {
	for _, blocked := range []string{"credentials", "installation", "models", "unsaved-connection", "busy-connection", "port-in-use"} {
		t.Run(blocked, func(t *testing.T) {
			u, r := nativeLaunchTestUI(t, "open-design", false, false)
			switch blocked {
			case "credentials":
				u.owner.mu.Lock()
				u.owner.apiKey = ""
				u.owner.mu.Unlock()
			case "installation":
				info := u.clientState().LaunchInfo.Clients["open-design"]
				info.Available = false
				u.clientState().LaunchInfo.Clients["open-design"] = info
			case "models":
				nativeSeedSharedForTest(t, u)
			case "unsaved-connection":
				u.owner.mu.Lock()
				u.owner.connectionNeedsSave = true
				u.owner.mu.Unlock()
			case "busy-connection":
				u.busy["POST/api/config"] = true
			case "port-in-use":
				listener, err := net.Listen("tcp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { listener.Close() })
				u.owner.mu.Lock()
				u.owner.config.Port = listener.Addr().(*net.TCPAddr).Port
				u.owner.mu.Unlock()
			}
			nativeTestFrame(t, u)
			u.clickable("client:open-design:launch").Click()
			nativeTestFrame(t, u)
			nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
			if r.count() != 0 {
				t.Fatalf("launch escaped %s gate", blocked)
			}
			if blocked == "port-in-use" && u.notice == "" {
				t.Fatal("proxy startup failure was hidden")
			}
		})
	}
}

func TestNativeOpenDesignHelperPointerAndLayout(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {720, 700}} {
		for _, language := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"-"+language, func(t *testing.T) {
				u, _ := nativeLaunchTestUI(t, "open-design", false, false)
				u.language = language
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				t.Cleanup(func() {
					if t.Failed() {
						nativeGridCapture(t, h, "native-open-design-failure-"+fmtSize(size)+"-"+language)
					}
				})
				h.frame()
				nativeGridCapture(t, h, "native-open-design-"+fmtSize(size)+"-"+language)
				label := u.tr("Launch Open Design", "Abrir Open Design")
				if !h.target(label, semantic.Button).Desc.Bounds.In(image.Rectangle{Max: size}) {
					t.Fatal("launch is clipped or below the fold")
				}
				_, local := u.openDesignConnection()
				for _, node := range h.nodes() {
					if strings.Contains(node.Desc.Label, local) || strings.Contains(node.Desc.Label, "synthetic-kilo-personal-key") {
						t.Fatal("helper exposes credentials in the visible/accessible UI")
					}
					if strings.Contains(node.Desc.Label, "Project folder") || strings.Contains(node.Desc.Label, "Carpeta del proyecto") {
						t.Fatal("helper offers unsupported project-folder handoff")
					}
				}
				h.click(u.tr("← All agents", "← Todos los agentes"), semantic.Button)
				if u.page != "agents" {
					t.Fatal("back navigation did not return to agents")
				}
				u.agentSetup("open-design")
				h.frame()
				copyLabel := u.tr("Copy local API key", "Copiar API key local")
				nativeScrollClientControlIntoView(h, copyLabel, semantic.Button)
				h.click(copyLabel, semantic.Button)
				bridge := u.owner.desktop.(*nativeRecordingBridge)
				bridge.mu.Lock()
				copied := bridge.Text
				bridge.mu.Unlock()
				if copied != local {
					t.Fatal("pointer copy did not put the real local key on the clipboard")
				}
				// The dismissible copy notice sits above the scrollable page.
				h.click("×", semantic.Button)
				nativeScrollClientControlIntoView(h, u.tr("Copy model ID", "Copiar ID de modelo"), semantic.Button)
				nativeGridCapture(t, h, "native-open-design-fields-"+fmtSize(size)+"-"+language)
			})
		}
	}
}

func TestNativeOpenDesignSourceHelperCanStartProxyWithoutDesktopLaunch(t *testing.T) {
	u, r := nativeLaunchTestUI(t, "open-design", false, false)
	u.owner.launcher.platform = "linux"
	u.detectLaunchers()
	nativeTestWait(t, u, func() bool { return !u.busy["GET"+nativeLaunchEndpoint] })
	if u.nativeLaunchAvailable("open-design") {
		t.Fatal("Linux advertised an unsupported packaged desktop launcher")
	}
	nativeTestFrame(t, u)
	u.clickable("open-design:start").Click()
	nativeTestFrame(t, u)
	nativeTestWait(t, u, func() bool { return !u.busy["POST/api/start"] && nativeBool(u.state, "running") })
	if r.count() != 0 {
		t.Fatal("source helper launched a desktop app")
	}
}

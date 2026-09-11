//go:build desktop

package main

import (
	"encoding/json"
	"gioui.org/io/semantic"
	"image"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeOpenDesignLaunchPreparesSelectedEngineAndStartsProxy(t *testing.T) {
	for _, engine := range []string{"codex-cli", "claude", "opencode"} {
		t.Run(engine, func(t *testing.T) {
			u, r := nativeLaunchTestUI(t, "open-design", false, false)
			label := "Codex CLI"
			if engine == "claude" {
				label = "Claude Code"
			} else if engine == "opencode" {
				label = "OpenCode"
			}
			u.setValue("open-design-engine", label)
			u.setValue("clients-project-directory", "/ignored/project")
			before := u.owner.launcher.start
			u.owner.launcher.start = func(plan clientLaunchPlan) error {
				u.owner.mu.Lock()
				listener := u.owner.proxyListener
				u.owner.mu.Unlock()
				if listener == nil {
					t.Error("proxy missing before Open Design launch")
				} else {
					conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
					if err != nil {
						t.Error(err)
					} else {
						conn.Close()
					}
				}
				return before(plan)
			}
			nativeTestFrame(t, u)
			u.clickable("client:open-design:launch").Click()
			nativeTestFrame(t, u)
			nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
			if r.count() != 1 || r.prepares.Load() != 1 {
				t.Fatalf("launch didn't prepare and open once: %s", u.notice)
			}
			saved, err := u.owner.openDesignReadPrepared()
			if err != nil || saved.Engine != engine || saved.Library.DefaultModel != "vendor/one" {
				t.Fatalf("wrong engine/library: %+v %v", saved, err)
			}
			r.mu.Lock()
			plan := r.plans[0]
			r.mu.Unlock()
			if len(plan.Args) != 0 || plan.Env["KILO_LOCAL_API_KEY"] != u.owner.config.LocalKey || plan.Env["OD_PACKAGED_NAMESPACE"] != openDesignNamespace(u.owner.dir) {
				t.Fatal("launch lost engine profile isolation or local authentication")
			}
			bridge := u.owner.desktop.(*nativeRecordingBridge)
			bridge.mu.Lock()
			copied := bridge.Text
			bridge.mu.Unlock()
			if copied != "" {
				t.Fatal("automatic flow copied credentials")
			}
			prefs, err := readAgentPreferences(u.owner.dir)
			if err != nil || prefs.Projects["open-design"] != "" {
				t.Fatal("remembered unsupported project handoff")
			}
		})
	}
}

func TestNativeOpenDesignLaunchGates(t *testing.T) {
	for _, blocked := range []string{"credentials", "installation", "engine", "models", "unsaved-connection", "busy-connection", "port-in-use"} {
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
			case "engine":
				info := u.clientState().OpenDesign.Engines["codex-cli"]
				info.Available = false
				u.clientState().OpenDesign.Engines["codex-cli"] = info
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
		})
	}
}

func TestNativeOpenDesignChangedEngineDuringPreparationDoesNotLaunch(t *testing.T) {
	u, r := nativeLaunchTestUI(t, "open-design", true, false)
	u.launchAgent("open-design")
	<-r.entered
	u.setValue("open-design-engine", "Claude Code")
	r.unblock()
	nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
	if r.count() != 0 || !strings.Contains(u.notice, "changed while preparing") {
		t.Fatalf("stale engine launched: %s", u.notice)
	}
}

func TestNativeOpenDesignPointerLayoutAndNoManualSecrets(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {720, 700}} {
		for _, language := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"-"+language, func(t *testing.T) {
				u, r := nativeLaunchTestUI(t, "open-design", false, false)
				u.language = language
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				nativeGridCapture(t, h, "native-open-design-cli-"+fmtSize(size)+"-"+language)
				label := u.tr("Launch Open Design", "Abrir Open Design")
				if !h.target(label, semantic.Button).Desc.Bounds.In(image.Rectangle{Max: size}) {
					t.Fatal("launch clipped")
				}
				for _, node := range h.nodes() {
					if strings.Contains(node.Desc.Label, u.owner.config.LocalKey) || strings.Contains(node.Desc.Label, "Copy local") || strings.Contains(node.Desc.Label, "Project folder") {
						t.Fatal("manual key or unsupported folder shown")
					}
				}
				h.click(label, semantic.Button)
				nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
				if r.count() != 1 || r.prepares.Load() != 1 {
					t.Fatalf("pointer launch failed: %s", u.notice)
				}
			})
		}
	}
}

func TestNativeOpenDesignPreparedProfilesUseSharedUpdates(t *testing.T) {
	u, _ := nativeLaunchTestUI(t, "open-design", false, false)
	nativeSeedSharedForTest(t, u, nativeClientModelsForTest()...)
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Friendly model")
	u.persistLibraryEdits()
	u.flushModelLibrary()
	u.prepareClient("open-design")
	nativeTestWait(t, u, func() bool { return !u.busy["POST"+openDesignProfileEndpoint] })
	paths := openDesignProfilePaths(u.owner.dir)
	data, err := os.ReadFile(filepath.Join(paths.Profiles, "codex-cli", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Friendly model") {
		t.Fatal("shared display name missing")
	}
	var prefs map[string]json.RawMessage
	data, err = os.ReadFile(paths.Config)
	if err != nil || json.Unmarshal(data, &prefs) != nil {
		t.Fatal("missing Open Design preferences")
	}
	if string(prefs["agentId"]) != "\"codex\"" {
		t.Fatal("wrong selected runtime")
	}
}

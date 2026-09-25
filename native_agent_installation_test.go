//go:build desktop

package main

import (
	"errors"
	"image"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
)

var nativeCLIInstallationCases = []struct{ key, url string }{
	{"codex-cli", "https://developers.openai.com/codex/cli/"},
	{"claude", "https://code.claude.com/docs/en/setup#install-claude-code"},
	{"opencode", "https://opencode.ai/docs/#install"},
	{"omp", "https://github.com/can1357/oh-my-pi#install"},
}

type nativeAgentInstallationFixture struct {
	h                *nativePointerHarness
	recorder         *nativeLaunchRecorder
	installed        atomic.Bool
	terminal         atomic.Bool
	claudeDetections atomic.Int32
}

func newNativeAgentInstallationFixture(t *testing.T, key, language string, size image.Point, installed, terminal bool) *nativeAgentInstallationFixture {
	t.Helper()
	u, recorder := nativeLaunchTestUI(t, key, false, false)
	f := &nativeAgentInstallationFixture{recorder: recorder}
	f.installed.Store(installed)
	f.terminal.Store(terminal)
	resolve := u.owner.launcher.resolve
	u.owner.launcher.resolve = func(client, customPath string) (string, error) {
		if client == key && !f.installed.Load() {
			return "", errors.New("Synthetic CLI not found")
		}
		return resolve(client, customPath)
	}
	u.owner.launcher.terminal = func() (bool, string) {
		if !f.terminal.Load() {
			return false, "Synthetic terminal unavailable"
		}
		return true, ""
	}
	// Rechecking Claude capabilities must not execute the user's real CLI.
	// Forward other endpoints to the existing authenticated fixture, keeping
	// its preparation/launch counters and real executable discovery handler.
	upstream, err := url.Parse("http://" + u.owner.adminHost)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/claude/info" {
			if !secureEqual(r.Header.Get("Authorization"), "Bearer "+u.owner.adminToken) {
				jsonError(w, 401, "missing auth")
				return
			}
			f.claudeDetections.Add(1)
			jsonResponse(w, 200, claudeCaps("2.1.252"))
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	u.owner.adminHost = strings.TrimPrefix(server.URL, "http://")
	u.setLanguage(language)
	nativeTestWait(t, u, func() bool { return u.languageTarget == "" && !u.busy["POST/api/language"] })
	u.detectLaunchers()
	nativeTestWait(t, u, func() bool { return !u.busy["GET"+nativeLaunchEndpoint] })
	u.page = "agents"
	f.h = &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
	f.h.frame()
	u.flushModelLibrary()
	return f
}

func nativeAgentInstallationLabelCount(h *nativePointerHarness, label string) int {
	count := 0
	for _, node := range h.nodes() {
		if node.Desc.Label == label {
			count++
		}
	}
	return count
}

func TestNativeAgentCLIInstallationGuidesAndRecheck(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {780, 700}} {
		for _, language := range []string{"en", "es"} {
			for _, client := range nativeCLIInstallationCases {
				t.Run(client.key+"-"+fmtSize(size)+"-"+language, func(t *testing.T) {
					f := newNativeAgentInstallationFixture(t, client.key, language, size, false, true)
					h, u := f.h, f.h.u
					info := u.clientState().LaunchInfo.Clients[client.key]
					if info.Installed || info.Available || info.Path != "" || info.InstallURL != client.url {
						t.Fatalf("wrong missing CLI detection: %+v", info)
					}
					guide := u.tr("Installation guide", "Guía de instalación")
					check := u.tr("Check again", "Comprobar de nuevo")
					name, _ := launchClientIdentity(client.key)
					open := u.tr("Open ", "Abrir ") + name
					if nativeAgentInstallationLabelCount(h, u.tr("CLI not found", "CLI no encontrado")) != 1 || nativeAgentInstallationLabelCount(h, open) != 0 {
						t.Fatal("missing CLI did not replace Open with installation guidance")
					}
					if _, exists := u.buttons["agent:"+client.key+":folder"]; exists {
						t.Fatal("missing CLI still asks for a project folder")
					}
					h.reveal(guide, semantic.Button)
					if !h.target(guide, semantic.Button).Desc.Bounds.In(image.Rectangle{Max: size}) {
						t.Fatal("installation guide is clipped")
					}
					nativeGridCapture(t, h, "agent-cli-missing-"+client.key+"-"+fmtSize(size)+"-"+language)
					before := nativeTerminalHomeSnapshot(t, u.owner.editorTestRoot)
					h.click(guide, semantic.Button)
					bridge := u.owner.desktop.(*nativeRecordingBridge)
					nativeTestWait(t, u, func() bool {
						bridge.mu.Lock()
						defer bridge.mu.Unlock()
						return bridge.URL == client.url
					})
					if f.recorder.prepares.Load() != 0 || f.recorder.launchRequests.Load() != 0 || f.recorder.count() != 0 || nativeBool(u.state, "running") {
						t.Fatal("installation guide prepared a profile or started an application")
					}
					if !reflect.DeepEqual(before, nativeTerminalHomeSnapshot(t, u.owner.editorTestRoot)) {
						t.Fatal("installation guide changed settings or generated a profile")
					}
					gets := f.recorder.gets.Load()
					h.reveal(check, semantic.Button)
					h.click(check, semantic.Button)
					nativeTestWait(t, u, func() bool { return !u.busy["GET"+nativeLaunchEndpoint] && !u.busy["GET/api/claude/info"] })
					h.frame()
					if f.recorder.gets.Load() != gets+1 || nativeAgentInstallationLabelCount(h, guide) == 0 {
						t.Fatalf("recheck did not repeat discovery while the CLI was still absent: GETs %d -> %d, guides %d", gets, f.recorder.gets.Load(), nativeAgentInstallationLabelCount(h, guide))
					}
					if client.key == "claude" && (f.claudeDetections.Load() != 1 || u.clientState().Claude.Version != "2.1.252") {
						t.Fatal("Claude recheck did not refresh CLI capabilities")
					}
					f.installed.Store(true)
					h.click(check, semantic.Button)
					nativeTestWait(t, u, func() bool { return !u.busy["GET"+nativeLaunchEndpoint] && !u.busy["GET/api/claude/info"] })
					h.frame()
					info = u.clientState().LaunchInfo.Clients[client.key]
					if !info.Installed || !info.Available || nativeAgentInstallationLabelCount(h, guide) != 0 || nativeAgentInstallationLabelCount(h, check) != 0 {
						t.Fatalf("installed CLI retained installation guidance: %+v", info)
					}
					if nativeAgentInstallationLabelCount(h, u.tr("CLI installed", "CLI instalado")) != 4 {
						t.Fatal("installed CLI status was not shown for all four agents")
					}
					h.reveal(open, semantic.Button)
					h.target(open, semantic.Button)
					if _, exists := u.buttons["agent:"+client.key+":folder"]; !exists {
						t.Fatal("installed CLI did not restore its project picker")
					}
					nativeGridCapture(t, h, "agent-cli-installed-"+client.key+"-"+fmtSize(size)+"-"+language)
					if f.recorder.prepares.Load() != 0 || f.recorder.launchRequests.Load() != 0 || f.recorder.count() != 0 {
						t.Fatal("rechecking installation prepared or launched a CLI")
					}
				})
			}
		}
	}
}

func TestNativeAgentCLIInstalledWithoutTerminal(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		t.Run(language, func(t *testing.T) {
			f := newNativeAgentInstallationFixture(t, "claude", language, image.Pt(780, 700), true, false)
			h, u := f.h, f.h.u
			guide := u.tr("Installation guide", "Guía de instalación")
			check := u.tr("Check again", "Comprobar de nuevo")
			for _, client := range nativeCLIInstallationCases {
				info := u.clientState().LaunchInfo.Clients[client.key]
				if !info.Installed || info.Available || info.Reason != "Synthetic terminal unavailable" {
					t.Fatalf("terminal failure was confused with a missing CLI: %+v", info)
				}
				u.clickable("agent:" + client.key + ":launch").Click()
			}
			h.frame()
			if nativeAgentInstallationLabelCount(h, guide) != 0 || nativeAgentInstallationLabelCount(h, u.tr("CLI not found", "CLI no encontrado")) != 0 || nativeAgentInstallationLabelCount(h, u.tr("CLI installed", "CLI instalado")) != 4 {
				t.Fatal("terminal failure suggested installing an already installed CLI")
			}
			if f.recorder.prepares.Load() != 0 || f.recorder.launchRequests.Load() != 0 || f.recorder.count() != 0 {
				t.Fatal("unavailable terminal allowed a CLI launch")
			}
			h.reveal(check, semantic.Button)
			nativeGridCapture(t, h, "agent-cli-terminal-unavailable-"+language)
			f.terminal.Store(true)
			h.click(check, semantic.Button)
			nativeTestWait(t, u, func() bool { return !u.busy["GET"+nativeLaunchEndpoint] && !u.busy["GET/api/claude/info"] })
			h.frame()
			for _, client := range nativeCLIInstallationCases {
				info := u.clientState().LaunchInfo.Clients[client.key]
				if !info.Installed || !info.Available || info.Reason != "" {
					t.Fatalf("recheck did not recover the available terminal: %+v", info)
				}
			}
			if nativeAgentInstallationLabelCount(h, guide) != 0 || nativeAgentInstallationLabelCount(h, check) != 0 {
				t.Fatal("recovered terminal retained failure actions")
			}
		})
	}
}

func TestNativeAgentClaudeCapabilityRecheckBlocksOpen(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		t.Run(language, func(t *testing.T) {
			f := newNativeAgentInstallationFixture(t, "claude", language, image.Pt(1180, 820), true, true)
			h, u := f.h, f.h.u
			label := u.tr("Open Claude Code", "Abrir Claude Code")
			h.reveal(label, semantic.Button)
			bounds := h.target(label, semantic.Button).Desc.Bounds
			if !u.clientState().ClaudeChecked {
				t.Fatal("fixture must already have known Claude capabilities")
			}
			u.busy["GET/api/claude/info"] = true
			h.frame()
			if nativeAgentInstallationLabelCount(h, u.tr("Wait for Claude Code version detection to finish.", "Espera a que termine la detección de la versión de Claude Code.")) == 0 {
				t.Fatal("pending capability refresh did not explain why Open is disabled")
			}
			position := f32.Pt(float32(bounds.Min.X+bounds.Dx()/2), float32(bounds.Min.Y+bounds.Dy()/2))
			h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: position})
			h.frame()
			h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: position})
			h.frame()
			h.router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: position})
			h.frame()
			h.frame()
			if f.recorder.prepares.Load() != 0 || f.recorder.launchRequests.Load() != 0 || f.recorder.count() != 0 || u.clientState().Launching != "" {
				t.Fatal("Open used stale Claude capabilities while their refresh was pending")
			}
			delete(u.busy, "GET/api/claude/info")
			h.frame()
			h.target(label, semantic.Button)
		})
	}
}

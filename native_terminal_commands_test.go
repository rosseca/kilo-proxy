//go:build desktop

package main

import (
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func nativeTerminalCommandsTestUI(t *testing.T, platform string) *nativeUI {
	t.Helper()
	if runtime.GOOS == "windows" && platform != "windows" {
		t.Skip("The macOS/Linux command installer requires POSIX file permissions.")
	}
	t.Setenv("ZDOTDIR", "")
	u := nativeTestUI(t)
	u.page = "settings"
	u.owner.launcher = &clientLaunchRuntime{platform: platform, home: u.owner.editorTestRoot}
	u.owner.terminalCommandsShell = "/bin/zsh"
	u.owner.terminalCommandsBinary = filepath.Join(u.owner.editorTestRoot, "Kilo Proxy")
	if err := os.WriteFile(u.owner.terminalCommandsBinary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestNativeTerminalCommandsPointerInstallAndUpdate(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {780, 700}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"-"+lang, func(t *testing.T) {
				u := nativeTerminalCommandsTestUI(t, "macos")
				u.language = lang
				u.owner.mu.Lock()
				u.owner.config.Language = lang
				u.owner.mu.Unlock()
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				nativeTestWait(t, u, func() bool { return u.terminalCommandsState().Checked })
				h.frame()
				nativeMenuWheel(h, image.Pt(size.X-100, size.Y-100), 10000)
				h.click(u.tr("Install terminal commands", "Instalar comandos de terminal"), semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.busy["POST"+nativeTerminalCommandsEndpoint] })
				s := u.terminalCommandsState()
				if !s.Info.Installed || !s.Info.PathConfigured || s.Error != "" {
					t.Fatalf("install did not finish: %+v", s)
				}
				for _, name := range []string{"kilo-codex", "kilo-claude"} {
					want := filepath.Join(u.owner.editorTestRoot, ".local", "bin", name)
					if s.Info.Commands[name] != want {
						t.Fatalf("command path escaped test home: %q", s.Info.Commands[name])
					}
					data, err := os.ReadFile(want)
					if err != nil || !strings.Contains(string(data), terminalCommandOwner) {
						t.Fatalf("button did not install %s: %v", name, err)
					}
					for _, secret := range []string{u.owner.apiKey, u.owner.adminToken, u.owner.config.LocalKey} {
						if secret != "" && strings.Contains(string(data), secret) {
							t.Fatal("installed command contains a credential")
						}
					}
				}
				h.frame()
				h.click(u.tr("Update terminal commands", "Actualizar comandos de terminal"), semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.busy["POST"+nativeTerminalCommandsEndpoint] })
				if !s.Info.Installed || s.Error != "" {
					t.Fatalf("update did not finish: %+v", s)
				}
				h.frame()
				nativeMenuWheel(h, image.Pt(size.X-100, size.Y-100), 10000)
				nativeGridCapture(t, h, "terminal-commands-"+fmtSize(size)+"-"+lang)
			})
		}
	}
}

func TestNativeTerminalCommandsBusyFailureAndRetry(t *testing.T) {
	u := nativeTerminalCommandsTestUI(t, "linux")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var posts atomic.Int32
	handler := u.owner.adminHandler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == nativeTerminalCommandsEndpoint {
			if !secureEqual(r.Header.Get("Authorization"), "Bearer "+u.owner.adminToken) {
				t.Error("terminal command request omitted admin authentication")
				jsonError(w, 401, "missing authentication")
				return
			}
			if r.Method == "POST" && posts.Add(1) == 1 {
				close(entered)
				<-release
				jsonError(w, 409, "Synthetic installation failure.")
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	u.owner.adminHost = strings.TrimPrefix(server.URL, "http://")
	t.Cleanup(func() { unblock(); server.Close() })
	u.requestTerminalCommands("GET")
	nativeTestWait(t, u, func() bool { return u.terminalCommandsState().Checked })
	u.requestTerminalCommands("POST")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("installation never reached the API")
	}
	u.requestTerminalCommands("POST")
	u.requestTerminalCommands("GET")
	if posts.Load() != 1 || !u.busy["POST"+nativeTerminalCommandsEndpoint] || u.busy["GET"+nativeTerminalCommandsEndpoint] {
		t.Fatal("an in-progress installation did not prevent duplicate install/status requests")
	}
	unblock()
	nativeTestWait(t, u, func() bool { return !u.busy["POST"+nativeTerminalCommandsEndpoint] })
	if u.terminalCommandsState().Error != "Synthetic installation failure." || u.terminalCommandsState().Info.Installed {
		t.Fatalf("failure was not shown without claiming installation: %+v", u.terminalCommandsState())
	}
	u.requestTerminalCommands("POST")
	nativeTestWait(t, u, func() bool { return !u.busy["POST"+nativeTerminalCommandsEndpoint] })
	if posts.Load() != 2 || !u.terminalCommandsState().Info.Installed || u.terminalCommandsState().Error != "" {
		t.Fatalf("installation could not retry after failure: %+v", u.terminalCommandsState())
	}
}

func TestNativeTerminalCommandsUnsupportedPlatformHidesInstall(t *testing.T) {
	u := nativeTerminalCommandsTestUI(t, "windows")
	u.requestTerminalCommands("GET")
	nativeTestWait(t, u, func() bool { return u.terminalCommandsState().Checked })
	if u.terminalCommandsState().Info.Supported {
		t.Fatal("Windows should not offer terminal command installation")
	}
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(780, 700), now: time.Now()}
	h.frame()
	nativeMenuWheel(h, image.Pt(680, 600), 10000)
	found := false
	for _, node := range h.nodes() {
		if node.Desc.Label == "Install terminal commands" {
			t.Fatal("unsupported platform renders an installation action")
		}
		if node.Desc.Label == "kilo-codex and kilo-claude are available on macOS and Linux." {
			found = true
		}
	}
	if !found {
		t.Fatal("unsupported platform did not explain availability")
	}
	u.requestTerminalCommands("POST")
	if u.busy["POST"+nativeTerminalCommandsEndpoint] {
		t.Fatal("unsupported platform sent an installation request")
	}
	if _, err := os.Stat(filepath.Join(u.owner.editorTestRoot, ".local", "bin")); !os.IsNotExist(err) {
		t.Fatal("unsupported platform created command files")
	}
}

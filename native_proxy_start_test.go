//go:build desktop

package main

import (
	"errors"
	"image"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func TestNativeProxyStartShowsOccupiedPortSystemDiagnostic(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		t.Run(language, func(t *testing.T) {
			u := nativeTestUI(t)
			nativeSeedSharedForTest(t, u, u.models[0])
			u.setLanguage(language)
			nativeTestWait(t, u, func() bool { return u.languageTarget == "" && !u.busy["GET/api/state"] })
			occupied, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer occupied.Close()
			var mu sync.Mutex
			var systemError error
			u.owner.mu.Lock()
			u.owner.config.Port = occupied.Addr().(*net.TCPAddr).Port
			u.owner.listenProxy = func(network, address string) (net.Listener, error) {
				listener, bindError := net.Listen(network, address)
				mu.Lock()
				systemError = bindError
				mu.Unlock()
				return listener, bindError
			}
			u.owner.mu.Unlock()
			// App/CLI discovery is unrelated to the global proxy control.
			clients := u.clientState()
			clients.LaunchChecked, clients.LaunchDetectStarted = true, true
			clients.OpenDesignChecked, clients.OpenDesignDetectStarted = true, true
			u.page = "agents"
			h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 820), now: time.Now()}
			h.frame()
			h.click(u.tr("Start proxy", "Arrancar proxy"), semantic.Button)
			nativeTestWait(t, u, func() bool { return !u.busy["POST/api/start"] && u.noticeTone == nativeToneError })
			h.frame()
			mu.Lock()
			bindError := systemError
			mu.Unlock()
			var errno syscall.Errno
			if !errors.As(bindError, &errno) {
				t.Fatal("native Start control did not reach the occupied socket")
			}
			for _, detail := range []string{occupied.Addr().String(), strconv.Itoa(int(errno)), bindError.Error(), u.tr("System error", "Error del sistema")} {
				if !strings.Contains(u.notice, detail) {
					t.Fatalf("native error omitted diagnostic detail %q", detail)
				}
			}
			if nativeBool(u.state, "running") || u.page != "agents" {
				t.Fatal("failed start navigated away or reported a running proxy")
			}
			visible := false
			for _, node := range h.nodes() {
				if node.Desc.Label == u.notice && !node.Desc.Bounds.Intersect(image.Rectangle{Max: h.size}).Empty() {
					visible = true
				}
			}
			if !visible {
				t.Fatal("diagnostic exists in state but is not rendered in the native window")
			}
			h.target(u.tr("Start proxy", "Arrancar proxy"), semantic.Button)
		})
	}
}

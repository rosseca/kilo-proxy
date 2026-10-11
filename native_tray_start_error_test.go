//go:build desktop

package main

import (
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
)

func TestNativeTrayStartErrorDoesNotBlockOnFullHiddenWindowQueue(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		t.Run(language, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				owner := &app{quit: make(chan struct{})}
				defer owner.requestQuit()
				u := &nativeUI{owner: owner, language: "en", updates: make(chan func(), 128)}
				drained, invalidated := 0, 0
				u.invalidate = func() { invalidated++ }
				// Model the closed window: polling filled its queue and there is
				// no frame to consume updates until the tray controller returns.
				for range cap(u.updates) {
					u.updates <- func() { drained++ }
				}
				raw := proxyListenTestNetError(syscall.EACCES)
				problem := wrapProxyListenError("127.0.0.1:18877", raw)
				returned := make(chan struct{})
				go func() {
					u.reportProxyStartFailure(problem)
					close(returned)
				}()
				synctest.Wait()
				select {
				case <-returned:
				default:
					t.Fatal("reporting a startup failure blocked the tray controller on a full queue")
				}
				if len(u.updates) != cap(u.updates) || drained != 0 || invalidated != 0 || u.notice != "" {
					t.Fatal("startup failure bypassed the UI queue or discarded pending updates")
				}
				// Resume frame processing. The callback must use the UI's current
				// language, preserve the OS diagnostic, and display an error.
				u.language = language
				u.drain()
				synctest.Wait()
				u.drain()
				if drained != cap(u.updates) || invalidated != 1 || u.noticeTone != nativeToneError {
					t.Fatal("reopening did not deliver exactly one startup error after the queued updates")
				}
				label := "System error"
				if language == "es" {
					label = "Error del sistema"
				}
				for _, detail := range []string{"127.0.0.1:18877", label, strconv.Itoa(int(syscall.EACCES)), raw.Error()} {
					if !strings.Contains(u.notice, detail) {
						t.Fatalf("tray startup error omitted %q: %s", detail, u.notice)
					}
				}
			})
		})
	}
}

func TestNativeTrayStartErrorCancelsWhileHiddenWindowQueueIsFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owner := &app{quit: make(chan struct{})}
		defer owner.requestQuit()
		u := &nativeUI{owner: owner, language: "en", updates: make(chan func(), 128)}
		invalidated := 0
		u.invalidate = func() { invalidated++ }
		for range cap(u.updates) {
			u.updates <- func() {}
		}
		u.reportProxyStartFailure(wrapProxyListenError("127.0.0.1:18877", syscall.EACCES))
		synctest.Wait()
		owner.requestQuit()
		synctest.Wait()
		if len(u.updates) != cap(u.updates) || invalidated != 0 || u.notice != "" {
			t.Fatal("quitting dispatched a startup notice to the closed window")
		}
		// Test also requires every goroutine in this bubble to exit. A sender
		// that ignores Quit while the queue stays full fails as a deadlock.
	})
}

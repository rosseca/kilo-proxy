//go:build desktop

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// Exercise the real authenticated API and settings writes. Gates change only
// response timing, so neither the UI nor its persistence is replaced by a mock.
func TestNativeLanguageLatestSelectionSurvivesDelayedResponses(t *testing.T) {
	a, err := newApp(filepath.Join(t.TempDir(), "app"), &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	a.config.Language = "en"
	api := a.adminHandler()
	postStarted := make(chan string, 2)
	postGates := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	var postReleases [2]sync.Once
	releasePost := func(index int) { postReleases[index].Do(func() { close(postGates[index]) }) }
	var posts atomic.Int32
	var holdState atomic.Bool
	stateStarted, stateGate := make(chan struct{}), make(chan struct{})
	var stateRelease sync.Once
	releaseState := func() { stateRelease.Do(func() { close(stateGate) }) }
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/language" {
			body, readErr := io.ReadAll(r.Body)
			if readErr != nil {
				http.Error(w, "test request could not be read", 400)
				return
			}
			var request struct {
				Language string `json:"language"`
			}
			if json.Unmarshal(body, &request) != nil {
				http.Error(w, "test request is not JSON", 400)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			index := int(posts.Add(1)) - 1
			if index < len(postGates) {
				postStarted <- request.Language
				select {
				case <-postGates[index]:
				case <-r.Context().Done():
					return
				}
			}
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/state" && holdState.CompareAndSwap(true, false) {
			// Capture an authenticated response before the language change, but
			// deliver it after the newer preference has been saved successfully.
			response := httptest.NewRecorder()
			api.ServeHTTP(response, r)
			close(stateStarted)
			select {
			case <-stateGate:
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
	t.Cleanup(func() {
		releasePost(0)
		releasePost(1)
		releaseState()
		a.requestQuit()
		server.Close()
	})
	u := newNativeUI(a, func() {})
	nativeTestWait(t, u, func() bool { return u.authenticated && !u.busy["GET/api/state"] })
	if u.language != "en" {
		t.Fatal("fixture did not start in English")
	}
	awaitPost := func(want string) {
		t.Helper()
		var got string
		nativeTestWait(t, u, func() bool {
			select {
			case got = <-postStarted:
				return true
			default:
				return false
			}
		})
		if got != want {
			t.Fatalf("language request = %q, want latest choice %q", got, want)
		}
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
	u.setLanguage("en")
	awaitPost("en")
	u.setLanguage("es")
	releasePost(0)
	awaitPost("es")
	if u.language != "es" {
		t.Fatalf("older POST response replaced the last visible choice: %q", u.language)
	}
	releasePost(1)
	nativeTestWait(t, u, func() bool {
		a.mu.Lock()
		language := a.config.Language
		a.mu.Unlock()
		return language == "es" && !u.busy["POST/api/language"]
	})
	releaseState()
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
	if u.language != "es" {
		t.Fatalf("an older state poll rolled the UI back to %q", u.language)
	}
	saved, err := readSettings(a.dir)
	if err != nil || saved.Language != "es" {
		t.Fatalf("latest language was not persisted: language=%q error=%v", saved.Language, err)
	}
	if posts.Load() != 2 {
		t.Fatalf("language requests = %d, want the original and latest choice", posts.Load())
	}
}

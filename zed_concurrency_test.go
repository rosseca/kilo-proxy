package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func zedBlockedCredentialStore(t *testing.T, a *app) (<-chan struct{}, func()) {
	t.Helper()
	entered, released := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	t.Cleanup(release)
	a.zedCredentialStore = func(ctx context.Context, _, _, _ string) error {
		close(entered)
		select {
		case <-released:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return entered, release
}

func zedStartPrepare(t *testing.T, a *app, selection editorSelection) <-chan *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(selection)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- adminRequest(a, "editors/zed/profile", string(body)) }()
	return result
}

func zedWaitEntered(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("preparation did not reach the fake credential store")
	}
}

func zedWaitResponse(t *testing.T, result <-chan *httptest.ResponseRecorder, description string) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case response := <-result:
		return response
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		return nil
	}
}

func zedConcurrentFixture(t *testing.T) (*app, string, string, []byte, []byte) {
	t.Helper()
	a := testApp(t)
	a.editorTestRoot = t.TempDir()
	a.launcher = &clientLaunchRuntime{resolve: func(string, string) (string, error) { return "/synthetic/zed", nil }}
	response := zedWaitResponse(t, zedStartPrepare(t, a, exampleEditorSelection()), "initial preparation")
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	_, path, choices, err := a.editorPaths("zed")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := os.ReadFile(choices)
	if err != nil {
		t.Fatal(err)
	}
	return a, path, choices, settings, selection
}

func zedChangedSelection() editorSelection {
	selection := exampleEditorSelection()
	selection.Initial = "vendor/one"
	selection.Models[0].Name = "Changed during preparation"
	return selection
}

func TestZedCredentialPromptAllowsStateAndInference(t *testing.T) {
	a, _, _, _, _ := zedConcurrentFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/gateway/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic-upstream" {
			t.Error("concurrent inference lost its route or upstream authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer upstream.Close()
	setUpstream(a, upstream.URL)
	const host = "127.0.0.1:8877"
	key := a.config.LocalKey
	handler := a.inferenceHandler("synthetic-upstream", "synthetic-org", key, host)
	entered, release := zedBlockedCredentialStore(t, a)
	prepared := zedStartPrepare(t, a, zedChangedSelection())
	zedWaitEntered(t, entered)
	state := make(chan *httptest.ResponseRecorder, 1)
	go func() { state <- adminRequest(a, "state", "") }()
	inference := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest(http.MethodPost, "http://"+host+"/v1/chat/completions", strings.NewReader(`{"model":"vendor/one","messages":[{"role":"user","content":"synthetic request"}]}`))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		inference <- response
	}()
	if response := zedWaitResponse(t, state, "state polling during credential prompt"); response.Code != http.StatusOK {
		t.Fatalf("state polling returned %d", response.Code)
	}
	if response := zedWaitResponse(t, inference, "unrelated inference during credential prompt"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"ok"`) {
		t.Fatalf("unrelated inference returned %d: %s", response.Code, response.Body.String())
	}
	release()
	if response := zedWaitResponse(t, prepared, "preparation after credential release"); response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
}

func TestZedPreparationRejectsConnectionChangesDuringCredentialPrompt(t *testing.T) {
	for _, change := range []string{"port", "key"} {
		t.Run(change, func(t *testing.T) {
			a, path, choices, settingsBefore, choicesBefore := zedConcurrentFixture(t)
			entered, release := zedBlockedCredentialStore(t, a)
			prepared := zedStartPrepare(t, a, zedChangedSelection())
			zedWaitEntered(t, entered)
			changed := make(chan struct{})
			go func() {
				a.mu.Lock()
				if change == "port" {
					a.config.Port++
				} else {
					a.config.LocalKey = "synthetic-new-connection-key"
				}
				a.mu.Unlock()
				close(changed)
			}()
			select {
			case <-changed:
			case <-time.After(3 * time.Second):
				t.Fatal("connection change blocked behind credential prompt")
			}
			release()
			if response := zedWaitResponse(t, prepared, "preparation after connection change"); response.Code != http.StatusConflict {
				t.Fatalf("changed connection returned %d: %s", response.Code, response.Body.String())
			}
			zedAssertFile(t, path, settingsBefore)
			zedAssertFile(t, choices, choicesBefore)
		})
	}
}

func TestZedPreparationPreservesExternalEditsDuringCredentialPrompt(t *testing.T) {
	for _, target := range []string{"settings", "selection"} {
		t.Run(target, func(t *testing.T) {
			a, path, choices, settingsBefore, choicesBefore := zedConcurrentFixture(t)
			entered, release := zedBlockedCredentialStore(t, a)
			prepared := zedStartPrepare(t, a, zedChangedSelection())
			zedWaitEntered(t, entered)
			if target == "settings" {
				settingsBefore = append([]byte("// Edited in Zed while its credential store was blocked\n"), settingsBefore...)
				if err := os.WriteFile(path, settingsBefore, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				selection := exampleEditorSelection()
				selection.Models[1].Name = "External selection change"
				var err error
				choicesBefore, err = json.Marshal(selection)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(choices, choicesBefore, 0600); err != nil {
					t.Fatal(err)
				}
			}
			release()
			if response := zedWaitResponse(t, prepared, "preparation after external edit"); response.Code != http.StatusConflict {
				t.Fatalf("external edit returned %d: %s", response.Code, response.Body.String())
			}
			zedAssertFile(t, path, settingsBefore)
			zedAssertFile(t, choices, choicesBefore)
		})
	}
}

func zedAssertFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("preparation changed %s after a concurrent edit", path)
	}
}

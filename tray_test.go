package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestTrayFollowsPanelLifecycle(t *testing.T) {
	a := testApp(t)
	s := a.trayState()
	if s.running || s.configured || s.pending {
		t.Fatal("new app must require setup")
	}
	a.apiKey = "test-upstream-secret"
	a.config.OrgID = "team-123"
	a.config.Port = 0 // ephemeral port; no outbound requests in this test
	a.organizations = []organization{{ID: "team-123", Name: "Engineering"}}
	if w := adminRequest(a, "start", `{}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	s = a.trayState()
	if !s.running || !s.configured || s.team != "Equipo: Engineering" {
		t.Fatalf("tray did not follow panel start: %+v", s)
	}
	for _, secret := range []string{a.apiKey, a.adminToken, a.config.LocalKey} {
		if strings.Contains(fmt.Sprintf("%+v", s), secret) {
			t.Fatal("secret in tray state")
		}
	}
	if w := adminRequest(a, "stop", `{}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if a.trayState().running {
		t.Fatal("tray still shows running after panel stop")
	}
	a.login = &loginSession{Status: "pending"}
	if !a.trayState().pending {
		t.Fatal("pending login not reflected in tray")
	}
}

func TestQuitIsIdempotentAndPreventsRestart(t *testing.T) {
	a := testApp(t)
	a.apiKey, a.config.OrgID, a.config.Port = "test-key", "test-org", 0
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); a.requestQuit() }()
	}
	wg.Wait()
	select {
	case <-a.quit:
	default:
		t.Fatal("quit was not signalled")
	}
	if err := a.start(); err == nil {
		t.Fatal("proxy restarted while application was closing")
	}
	if w := adminRequest(a, "auth/start", `{}`); w.Code != 409 {
		t.Fatal("login accepted while application was closing")
	}
}

package main

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// All listeners, profiles and credentials in these tests belong to testApp.
// A live loopback connection at dispatch verifies startup ordering, not merely
// that a non-nil server field was assigned before opening the client.
func assertLaunchProxyListening(t *testing.T, a *app) {
	t.Helper()
	a.mu.Lock()
	listener, server := a.proxyListener, a.proxyServer
	a.mu.Unlock()
	if listener == nil || server == nil {
		t.Fatal("client became launchable without a running proxy")
	}
	connection, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("proxy was not listening before client launch: %v", err)
	}
	_ = connection.Close()
}

func TestClientLaunchStartsProxyBeforeDispatchAndPreservesRunningProxy(t *testing.T) {
	for _, client := range []string{"codex", "codex-cli"} {
		t.Run(client, func(t *testing.T) {
			a := launchTestApp(t)
			launchPrepareFixture(t, a, client)
			calls := 0
			a.launcher.start = func(plan clientLaunchPlan) error {
				calls++
				if plan.Client != client {
					t.Fatalf("dispatched %q, expected %q", plan.Client, client)
				}
				assertLaunchProxyListening(t, a)
				return nil
			}
			body, _ := json.Marshal(clientLaunchRequest{Client: client})
			if a.proxyListener != nil || a.proxyServer != nil {
				t.Fatal("fixture started the proxy before launch")
			}
			if response := adminRequest(a, "clients/launch", string(body)); response.Code != http.StatusOK {
				t.Fatalf("launch from stopped state failed: %d %s", response.Code, response.Body.String())
			}
			listener, server, started := a.proxyListener, a.proxyServer, a.started
			if response := adminRequest(a, "clients/launch", string(body)); response.Code != http.StatusOK {
				t.Fatalf("launch from running state failed: %d %s", response.Code, response.Body.String())
			}
			if calls != 2 || a.proxyListener != listener || a.proxyServer != server || a.started != started {
				t.Fatal("second launch restarted the proxy or did not dispatch exactly once")
			}
		})
	}
}

func launchProxyFailures() []struct {
	name string
	set  func(*testing.T, *app)
} {
	return []struct {
		name string
		set  func(*testing.T, *app)
	}{
		{"missing API key", func(_ *testing.T, a *app) { a.apiKey = "" }},
		{"missing organization", func(_ *testing.T, a *app) { a.config.OrgID = "" }},
		{"login starting", func(_ *testing.T, a *app) { a.login = &loginSession{Status: "starting"} }},
		{"login pending", func(_ *testing.T, a *app) { a.login = &loginSession{Status: "pending"} }},
		{"occupied local port", func(t *testing.T, a *app) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			a.config.Port = listener.Addr().(*net.TCPAddr).Port
		}},
		{"application closing", func(_ *testing.T, a *app) { close(a.quit) }},
	}
}

func TestClientLaunchProxyStartupFailureBlocksDispatch(t *testing.T) {
	for _, client := range []string{"codex", "codex-cli"} {
		for _, failure := range launchProxyFailures() {
			t.Run(client+"/"+failure.name, func(t *testing.T) {
				a := launchTestApp(t)
				a.config.Language = "en"
				failure.set(t, a)
				launchPrepareFixture(t, a, client)
				input := clientLaunchRequest{Client: client}
				if _, err := a.planClientLaunch(input, a.launchRuntime()); err != nil {
					t.Fatalf("fixture must reach proxy startup, not fail profile validation: %v", err)
				}
				calls := 0
				a.launcher.start = func(clientLaunchPlan) error { calls++; return nil }
				body, _ := json.Marshal(input)
				response := adminRequest(a, "clients/launch", string(body))
				if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "Cannot start the saved proxy") {
					t.Fatalf("startup failure was not reported: %d %s", response.Code, response.Body.String())
				}
				if calls != 0 || a.proxyListener != nil || a.proxyServer != nil {
					t.Fatal("startup failure opened a client or left a running proxy")
				}
				if strings.Contains(response.Body.String(), a.config.LocalKey) {
					t.Fatal("startup error leaked the local API key")
				}
			})
		}
	}
}

func TestTerminalPrepareStartsProxyBeforeReturningPlanAndPreservesRunningProxy(t *testing.T) {
	for _, client := range []string{"codex-cli", "claude"} {
		t.Run(client, func(t *testing.T) {
			a := terminalTestApp(t)
			body, _ := json.Marshal(terminalPrepareRequest{Client: client, Directory: a.launcher.home})
			if a.proxyListener != nil || a.proxyServer != nil {
				t.Fatal("fixture started the proxy before preparation")
			}
			for attempt := 0; attempt < 2; attempt++ {
				listener, server, started := a.proxyListener, a.proxyServer, a.started
				response := adminRequest(a, "terminal/prepare", string(body))
				if response.Code != http.StatusOK {
					t.Fatalf("preparation failed: %d %s", response.Code, response.Body.String())
				}
				var plan clientLaunchPlan
				if err := json.Unmarshal(response.Body.Bytes(), &plan); err != nil || plan.Client != client || plan.Kind != "terminal" {
					t.Fatal("preparation returned an invalid launch plan")
				}
				assertLaunchProxyListening(t, a)
				if attempt > 0 && (a.proxyListener != listener || a.proxyServer != server || a.started != started) {
					t.Fatal("preparation restarted an already running proxy")
				}
			}
		})
	}
}

func TestTerminalPrepareProxyStartupFailureWithholdsPlan(t *testing.T) {
	for _, client := range []string{"codex-cli", "claude"} {
		for _, failure := range launchProxyFailures() {
			t.Run(client+"/"+failure.name, func(t *testing.T) {
				a := terminalTestApp(t)
				failure.set(t, a)
				body, _ := json.Marshal(terminalPrepareRequest{Client: client, Directory: a.launcher.home})
				response := adminRequest(a, "terminal/prepare", string(body))
				if response.Code != http.StatusConflict {
					t.Fatalf("startup failure was not reported: %d %s", response.Code, response.Body.String())
				}
				var plan clientLaunchPlan
				if err := json.Unmarshal(response.Body.Bytes(), &plan); err != nil || plan.Client != "" || len(plan.Env) != 0 {
					t.Fatal("startup failure exposed a launchable terminal plan")
				}
				if a.proxyListener != nil || a.proxyServer != nil {
					t.Fatal("failed preparation left a running proxy")
				}
				if strings.Contains(response.Body.String(), a.config.LocalKey) {
					t.Fatal("startup error leaked the local API key")
				}
			})
		}
	}
}

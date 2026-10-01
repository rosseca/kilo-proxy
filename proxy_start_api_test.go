package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func proxyStartAPIProblem(t *testing.T, response *httptest.ResponseRecorder, status int, code string) proxyStartFailure {
	t.Helper()
	var body struct {
		Error proxyStartFailure `json:"error"`
	}
	if response.Code != status || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("unexpected startup response: status %d, want %d", response.Code, status)
	}
	if body.Error.Code != code || body.Error.Type != "kilo_local_error" || body.Error.Message == "" {
		t.Fatalf("unexpected startup error classification: %+v", body.Error)
	}
	return body.Error
}

func TestProxyStartAPIReportsConnectionStateBeforeListening(t *testing.T) {
	for _, test := range []struct {
		name, code string
		status     int
		prepare    func(*app)
	}{
		{"credentials", "proxy_credentials_missing", http.StatusBadRequest, func(a *app) { a.apiKey, a.config.OrgID = "", "" }},
		{"login", "proxy_login_pending", http.StatusConflict, func(a *app) { a.login = &loginSession{Status: "pending"} }},
		{"changing account", "proxy_authentication_changing", http.StatusConflict, func(a *app) { a.providerChanging = true }},
		{"closing", "proxy_closing", http.StatusConflict, func(a *app) { a.requestQuit() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			messages := make(map[string]string)
			for _, language := range []string{"en", "es"} {
				a := testApp(t)
				a.config.Language = language
				a.apiKey, a.config.OrgID = "private-start-test-key", "synthetic-team"
				listens := 0
				a.listenProxy = func(string, string) (net.Listener, error) {
					listens++
					return nil, errors.New("listener must not be reached")
				}
				test.prepare(a)
				response := adminRequest(a, "start", `{}`)
				problem := proxyStartAPIProblem(t, response, test.status, test.code)
				messages[language] = problem.Message
				if problem.Diagnostic != nil || listens != 0 || a.proxyServer != nil || a.proxyListener != nil {
					t.Fatal("connection state was mistaken for a bind failure or started a listener")
				}
				if strings.Contains(response.Body.String(), "private-start-test-key") || strings.Contains(response.Body.String(), a.config.LocalKey) {
					t.Fatal("connection state error disclosed a credential")
				}
			}
			if messages["en"] == messages["es"] {
				t.Fatal("startup state error ignored the selected language")
			}
		})
	}
}

func TestProxyStartAPIOccupiedPortIncludesRealSystemDiagnostic(t *testing.T) {
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	address := occupied.Addr().String()
	port := occupied.Addr().(*net.TCPAddr).Port
	for _, language := range []string{"en", "es"} {
		t.Run(language, func(t *testing.T) {
			a := testApp(t)
			a.config.Language, a.config.Port = language, port
			a.apiKey, a.config.OrgID = "private-occupied-test-key", "synthetic-team"
			var systemError error
			a.listenProxy = func(network, endpoint string) (net.Listener, error) {
				if network != "tcp4" || endpoint != address {
					t.Errorf("wrong bind target %s %s", network, endpoint)
				}
				listener, bindError := net.Listen(network, endpoint)
				systemError = bindError
				return listener, bindError
			}
			response := adminRequest(a, "start", `{}`)
			problem := proxyStartAPIProblem(t, response, http.StatusConflict, "proxy_address_in_use")
			var errno syscall.Errno
			if !errors.As(systemError, &errno) {
				t.Fatalf("real socket collision did not carry an errno: %v", systemError)
			}
			diagnostic := problem.Diagnostic
			if diagnostic == nil || diagnostic.Kind != proxyListenAddressInUse || diagnostic.Address != address || diagnostic.Port != port || diagnostic.SystemCode != int(errno) || diagnostic.SystemError != systemError.Error() || diagnostic.System == "" {
				t.Fatalf("incomplete system diagnostic: %+v", diagnostic)
			}
			if !strings.Contains(problem.Message, address) || !strings.Contains(problem.Message, systemError.Error()) || !strings.Contains(problem.Message, strconv.Itoa(int(errno))) {
				t.Fatal("user-visible diagnostic omitted endpoint, OS error, or numeric system code")
			}
			if a.proxyServer != nil || a.proxyListener != nil {
				t.Fatal("failed bind left a running proxy")
			}
			for _, secret := range []string{a.apiKey, a.config.LocalKey, a.adminToken} {
				if strings.Contains(response.Body.String(), secret) {
					t.Fatal("bind diagnostic disclosed a credential")
				}
			}
		})
	}
	// Startup must neither close someone else's listener nor retain the failed
	// bind. Once the actual owner releases it, that same address is usable.
	if err := occupied.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := net.Listen("tcp4", address)
	if err != nil {
		t.Fatal("failed startup retained the socket:", err)
	}
	_ = reopened.Close()
}

func TestProxyStartPermissionDiagnosticIsSharedByLaunchAndTerminal(t *testing.T) {
	const port = 49231
	for _, language := range []string{"en", "es"} {
		t.Run(language, func(t *testing.T) {
			var baseline *proxyStartFailure
			for _, endpoint := range []string{"start", "clients/launch", "terminal/prepare"} {
				t.Run(endpoint, func(t *testing.T) {
					var a *app
					if endpoint == "terminal/prepare" {
						a = terminalTestApp(t)
					} else {
						a = launchTestApp(t)
					}
					a.config.Language, a.config.Port = language, port
					a.config.LocalKey = "kl_local_private_permission_fixture"
					launches := 0
					a.launcher.start = func(clientLaunchPlan) error { launches++; return nil }
					if endpoint == "clients/launch" {
						launchPrepareFixture(t, a, "codex-cli")
					}
					systemError := &net.OpError{Op: "listen", Net: "tcp4", Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}, Err: os.NewSyscallError("bind", syscall.EACCES)}
					listens := 0
					a.listenProxy = func(string, string) (net.Listener, error) { listens++; return nil, systemError }
					request := `{}`
					if endpoint != "start" {
						body, err := json.Marshal(map[string]string{"client": "codex-cli", "directory": a.launcher.home})
						if err != nil {
							t.Fatal(err)
						}
						request = string(body)
					}
					response := adminRequest(a, endpoint, request)
					problem := proxyStartAPIProblem(t, response, http.StatusConflict, "proxy_permission_denied")
					if listens != 1 || launches != 0 || a.proxyServer != nil || a.proxyListener != nil {
						t.Fatal("failed bind did not stop before launching the agent")
					}
					if problem.Diagnostic == nil || problem.Diagnostic.Kind != proxyListenPermissionDenied || problem.Diagnostic.Port != port || problem.Diagnostic.SystemCode != int(syscall.EACCES) || problem.Diagnostic.SystemError != systemError.Error() {
						t.Fatalf("missing permission diagnostic: %+v", problem.Diagnostic)
					}
					var envelope map[string]json.RawMessage
					if json.Unmarshal(response.Body.Bytes(), &envelope) != nil || len(envelope) != 1 || envelope["error"] == nil {
						t.Fatal("failed startup returned a process plan")
					}
					for _, secret := range []string{a.apiKey, a.config.LocalKey, a.adminToken} {
						if strings.Contains(response.Body.String(), secret) {
							t.Fatal("failed startup exposed credentials from the withheld plan")
						}
					}
					if baseline == nil {
						baseline = &problem
					} else if !reflect.DeepEqual(*baseline, problem) {
						t.Fatal("launch path returned a different startup diagnostic")
					}
				})
			}
		})
	}
}

func TestProxyStartUnexpectedNonListenerErrorDoesNotExposeCause(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		a := testApp(t)
		a.config.Language = language
		cause := errors.New("upstream https://example.invalid/?access_token=synthetic-private-access Bearer synthetic-private-refresh")
		response := httptest.NewRecorder()
		a.writeProxyStartError(response, fmt.Errorf("account setup: %w", cause))
		problem := proxyStartAPIProblem(t, response, http.StatusInternalServerError, "proxy_start_failed")
		if problem.Diagnostic != nil || strings.Contains(response.Body.String(), "synthetic-private") || strings.Contains(response.Body.String(), "example.invalid") {
			t.Fatal("non-listener failure exposed private account or process details")
		}
	}
}

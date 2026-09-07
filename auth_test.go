package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func waitLogin(t *testing.T, a *app, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		status := ""
		if a.login != nil {
			status = a.login.Status
		}
		a.mu.Unlock()
		if status == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t.Fatalf("login did not reach %s: %+v", want, a.login)
}
func authServer(t *testing.T, a *app, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	a.accountURL = server.URL
	a.authPollInterval = time.Millisecond
	t.Cleanup(func() { a.cancelLogin(); server.Close() })
}
func beginResponse(w http.ResponseWriter) {
	jsonResponse(w, 200, map[string]any{"code": "TEST-CODE", "verificationUrl": "https://app.kilo.ai/device-auth?code=TEST-CODE", "expiresIn": 60})
}
func TestDeviceLoginAndAutomaticSingleTeam(t *testing.T) {
	a := testApp(t)
	a.apiKey = "previous-key"
	a.config.OrgID = "previous-org"
	authServer(t, a, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/device-auth/codes":
			if r.Method != "POST" {
				t.Error("wrong initiate method")
			}
			beginResponse(w)
		case "/api/device-auth/codes/TEST-CODE":
			jsonResponse(w, 200, map[string]string{"status": "approved", "token": "device-secret-token", "userEmail": "dev@example.test"})
		case "/api/profile":
			if r.Header.Get("Authorization") != "Bearer device-secret-token" {
				t.Error("profile credential incorrect")
			}
			jsonResponse(w, 200, map[string]any{"organizations": []organization{{ID: "new-org", Name: "My Team"}}})
		default:
			t.Error("unexpected auth URL")
			w.WriteHeader(404)
		}
	})
	w := adminRequest(a, "auth/start", `{}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	waitLogin(t, a, "approved")
	a.mu.Lock()
	if a.apiKey != "device-secret-token" || a.config.OrgID != "new-org" || a.keySaved {
		t.Error("login state incorrect")
	}
	a.mu.Unlock()
	state := adminRequest(a, "state", "").Body.String()
	if strings.Contains(state, "device-secret-token") || strings.Contains(state, "TEST-CODE") || !strings.Contains(state, "My Team") {
		t.Fatal("credential disclosure or teams missing")
	}
	// Remembering the device token is a separate explicit save in the panel.
	w = adminRequest(a, "config", `{"apiKey":"","orgId":"new-org","port":8877,"remember":true}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	restored, err := newApp(a.dir, a.vault)
	if err != nil || restored.apiKey != "device-secret-token" {
		t.Fatal("device token not restored from vault")
	}
}
func TestMultipleTeamsRequireSelection(t *testing.T) {
	a := testApp(t)
	a.config.OrgID = "old-org"
	authServer(t, a, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/device-auth/codes":
			beginResponse(w)
		case "/api/device-auth/codes/TEST-CODE":
			jsonResponse(w, 200, map[string]string{"status": "approved", "token": "new-key"})
		case "/api/profile":
			jsonResponse(w, 200, map[string]any{"organizations": []organization{{ID: "one", Name: "One"}, {ID: "two", Name: "Two"}}})
		}
	})
	adminRequest(a, "auth/start", `{}`)
	waitLogin(t, a, "approved")
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.OrgID != "" || len(a.organizations) != 2 {
		t.Fatal("ambiguous or previous team silently selected")
	}
}
func TestDeviceLoginRejectsUnsafeVerificationDestination(t *testing.T) {
	for _, address := range []string{"https://evil.example/login", "http://app.kilo.ai/login", "https://app.kilo.ai.evil.example/", "https://name:pass@app.kilo.ai/", "https://app.kilo.ai:8443/", "javascript:alert(1)"} {
		t.Run(address, func(t *testing.T) {
			a := testApp(t)
			authServer(t, a, func(w http.ResponseWriter, r *http.Request) {
				jsonResponse(w, 200, map[string]any{"code": "test", "verificationUrl": address, "expiresIn": 60})
			})
			w := adminRequest(a, "auth/start", `{}`)
			if w.Code != 502 {
				t.Fatal("unsafe verification link accepted")
			}
		})
	}
}
func TestCancelledLoginDoesNotReplaceCredentials(t *testing.T) {
	a := testApp(t)
	a.apiKey = "original-key"
	a.config.OrgID = "original-org"
	pollStarted := make(chan struct{})
	release := make(chan struct{})
	authServer(t, a, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			beginResponse(w)
			return
		}
		close(pollStarted)
		<-release
		jsonResponse(w, 200, map[string]string{"status": "approved", "token": "must-not-be-used"})
	})
	adminRequest(a, "auth/start", `{}`)
	select {
	case <-pollStarted:
	case <-time.After(time.Second):
		t.Fatal("poll never started")
	}
	w := adminRequest(a, "config", `{"apiKey":"changed","orgId":"changed","port":8877,"remember":false}`)
	if w.Code != 409 {
		t.Error("configuration changed during login")
	}
	adminRequest(a, "auth/cancel", `{}`)
	close(release)
	waitLogin(t, a, "cancelled")
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.apiKey != "original-key" || a.config.OrgID != "original-org" {
		t.Fatal("cancel replaced credentials")
	}
}
func TestDeniedAndExpiredLoginPreserveCredential(t *testing.T) {
	for _, code := range []int{403, 410} {
		t.Run(jsonNumber(code), func(t *testing.T) {
			a := testApp(t)
			a.apiKey = "original"
			authServer(t, a, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					beginResponse(w)
				} else {
					w.WriteHeader(code)
				}
			})
			adminRequest(a, "auth/start", `{}`)
			want := "denied"
			if code == 410 {
				want = "expired"
			}
			waitLogin(t, a, want)
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.apiKey != "original" {
				t.Fatal("failed login replaced credentials")
			}
		})
	}
}
func TestDeviceTokenSurvivesProfileFailureWithoutOldTeam(t *testing.T) {
	a := testApp(t)
	a.config.OrgID = "old"
	authServer(t, a, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			beginResponse(w)
		} else if r.URL.Path == "/api/profile" {
			w.WriteHeader(503)
		} else {
			jsonResponse(w, 200, map[string]string{"status": "approved", "token": "new-key"})
		}
	})
	adminRequest(a, "auth/start", `{}`)
	waitLogin(t, a, "approved")
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.apiKey != "new-key" || a.config.OrgID != "" || !strings.Contains(a.login.Message, "no se pudieron cargar") {
		t.Fatal("profile failure lost token or retained stale org")
	}
}
func TestMessagesBetaFlagAndArbitraryQueryRejection(t *testing.T) {
	a := testApp(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "beta=true" {
			t.Error("beta flag lost")
		}
		jsonResponse(w, 200, map[string]string{"id": "msg_test"})
	}))
	defer upstream.Close()
	setUpstream(a, upstream.URL)
	h := a.inferenceHandler("key", "org", "local", "127.0.0.1:8877")
	for _, query := range []string{"beta=true", "beta=true&api_key=evil", "beta=true&beta=false", "beta=false"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/messages?"+query, nil)
		r.Header.Set("Authorization", "Bearer local")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 404
		if query == "beta=true" {
			want = 200
		}
		if w.Code != want {
			t.Fatal("unexpected query behavior", query, w.Code)
		}
	}
}
func TestDeviceLoginStatusJSONContainsNoPrivateFields(t *testing.T) {
	session := loginSession{Status: "pending", Code: "user-code", cancel: func() {}}
	_, err := json.Marshal(session)
	if err != nil {
		t.Fatal("private cancellation leaked into JSON", err)
	}
}

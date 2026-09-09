package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeVault struct {
	values map[string]string
	fail   bool
}

func (v *fakeVault) Get(id string) (string, error) {
	if v.fail {
		return "", errors.New("locked")
	}
	value, ok := v.values[id]
	if !ok {
		return "", errors.New("missing")
	}
	return value, nil
}
func (v *fakeVault) Set(id, key string) error {
	if v.fail {
		return errors.New("locked")
	}
	v.values[id] = key
	return nil
}
func (v *fakeVault) Delete(id string) error {
	if v.fail {
		return errors.New("locked")
	}
	delete(v.values, id)
	return nil
}
func testApp(t *testing.T) *app {
	t.Helper()
	a, err := newApp(t.TempDir(), &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	a.adminHost = "127.0.0.1:9999"
	a.zedCredentialStore = fakeZedCredentialStore
	t.Cleanup(a.stop)
	return a
}
func fakeZedCredentialStore(context.Context, string, string, string) error { return nil }
func adminRequest(a *app, path, body string) *httptest.ResponseRecorder {
	method := "POST"
	if body == "" {
		method = "GET"
	}
	r := httptest.NewRequest(method, "http://"+a.adminHost+"/api/"+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+a.adminToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.adminHandler().ServeHTTP(w, r)
	return w
}
func setUpstream(a *app, address string) { a.upstream, _ = url.Parse(address + "/api/gateway") }
func TestProxyReplacesCredentialsAndPreservesPayload(t *testing.T) {
	payload := `{"model":"some/model","messages":[{"role":"user","content":"private prompt"}],"tools":[{"type":"function","function":{"name":"hello"}}]}`
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/api/gateway/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer upstream-secret" || r.Header.Get("X-KiloCode-OrganizationId") != "company-org" {
			t.Error("upstream authentication not replaced")
		}
		for _, key := range []string{"Cookie", "X-Api-Key", "X-Forwarded-For", "OpenAI-Organization", "X-Unknown"} {
			if r.Header.Get(key) != "" {
				t.Errorf("leaked header %s", key)
			}
		}
		if r.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Error("compatibility header lost")
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != payload {
			t.Error("body changed")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "secret=bad")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"message":"rate limit"}}`)
	}))
	defer upstream.Close()
	a := testApp(t)
	setUpstream(a, upstream.URL)
	h := a.inferenceHandler("upstream-secret", "company-org", "local-secret", "127.0.0.1:8877")
	r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/chat/completions", strings.NewReader(payload))
	for k, v := range map[string]string{"Authorization": "Bearer local-secret", "X-KiloCode-OrganizationId": "attacker-org", "Cookie": "secret=cookie", "X-Api-Key": "do-not-forward", "X-Forwarded-For": "1.2.3.4", "OpenAI-Organization": "wrong", "X-Unknown": "private", "Anthropic-Version": "2023-06-01"} {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !called || w.Code != 429 || !strings.Contains(w.Body.String(), "rate limit") {
		t.Fatalf("unexpected response %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Set-Cookie") != "" || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("unsafe response headers forwarded")
	}
	if a.requests != 1 || a.failures != 1 || a.active != 0 {
		t.Error("incorrect metrics")
	}
	raw, _ := json.Marshal(a.events)
	for _, secret := range []string{"upstream-secret", "local-secret", "private prompt"} {
		if strings.Contains(string(raw), secret) {
			t.Error("secret recorded")
		}
	}
}
func TestProxyRejectsUnauthenticatedBrowserAndUnexpectedRoutes(t *testing.T) {
	a := testApp(t)
	h := a.inferenceHandler("key", "org", "local-secret", "127.0.0.1:8877")
	cases := []struct {
		name, method, path, auth, host, origin, fetch string
		status                                        int
	}{
		{"missing auth", "POST", "/v1/chat/completions", "", "127.0.0.1:8877", "", "", 401},
		{"wrong auth", "GET", "/v1/models", "Bearer wrong", "127.0.0.1:8877", "", "", 401},
		{"rebinding", "GET", "/v1/models", "Bearer local-secret", "evil.example", "", "", 403},
		{"browser origin", "POST", "/v1/chat/completions", "Bearer local-secret", "127.0.0.1:8877", "https://evil.example", "", 403},
		{"browser fetch", "GET", "/v1/models", "Bearer local-secret", "127.0.0.1:8877", "", "cross-site", 403},
		{"admin route", "POST", "/api/config", "Bearer local-secret", "127.0.0.1:8877", "", "", 404},
		{"traversal", "POST", "/v1/../billing", "Bearer local-secret", "127.0.0.1:8877", "", "", 404},
		{"encoded path", "GET", "/v1/%6dodels", "Bearer local-secret", "127.0.0.1:8877", "", "", 404},
		{"query", "GET", "/v1/models?key=secret", "Bearer local-secret", "127.0.0.1:8877", "", "", 404},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "http://127.0.0.1:8877"+tt.path, nil)
			r.Host = tt.host
			r.Header.Set("Authorization", tt.auth)
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("Sec-Fetch-Site", tt.fetch)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("got %d want %d", w.Code, tt.status)
			}
		})
	}
}
func TestStreamingFlushAndCancellation(t *testing.T) {
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer upstream.Close()
	a := testApp(t)
	setUpstream(a, upstream.URL)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: a.inferenceHandler("key", "org", "local", ln.Addr().String())}
	go server.Serve(ln)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+ln.Addr().String()+"/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	req.Header.Set("Authorization", "Bearer local")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal("SSE headers did not arrive before stream ended:", err)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("first event missing: %q %v", line, err)
	}
	cancel()
	resp.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("client cancellation did not reach upstream")
	}
}
func TestCredentialsStayOutOfDiskAndState(t *testing.T) {
	a := testApp(t)
	body := `{"apiKey":"upstream-test-secret","orgId":"company-org","port":8877,"remember":true}`
	if w := adminRequest(a, "config", body); w.Code != 200 {
		t.Fatalf("save failed: %s", w.Body)
	}
	b, err := os.ReadFile(filepath.Join(a.dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "upstream-test-secret") {
		t.Fatal("upstream key persisted in plaintext")
	}
	w := adminRequest(a, "state", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "upstream-test-secret") {
		t.Fatal("state leaks credentials")
	}
	if !strings.Contains(w.Body.String(), a.config.LocalKey) {
		t.Fatal("local key missing from authorized state")
	}
	restored, err := newApp(a.dir, a.vault)
	if err != nil || restored.apiKey != "upstream-test-secret" {
		t.Fatal("keychain restore failed")
	}
	if w := adminRequest(a, "forget", `{}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if a.apiKey != "" || len(a.vault.(*fakeVault).values) != 0 {
		t.Fatal("key not forgotten")
	}
}
func TestVaultFailureDoesNotSilentlySaveOrStart(t *testing.T) {
	a := testApp(t)
	a.vault.(*fakeVault).fail = true
	w := adminRequest(a, "config", `{"apiKey":"key","orgId":"org","port":8877,"remember":true}`)
	if w.Code != 500 || a.apiKey != "" {
		t.Fatal("vault failure silently accepted")
	}
	if _, err := os.Stat(filepath.Join(a.dir, "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("configuration written on vault failure")
	}
	w = adminRequest(a, "config", `{"apiKey":"key","orgId":"org","port":8877,"remember":false}`)
	if w.Code != 200 || a.apiKey != "key" {
		t.Fatal("memory-only mode failed")
	}
}
func TestAdminAuthenticationAndOrigin(t *testing.T) {
	a := testApp(t)
	for _, tt := range []struct {
		auth, host, origin string
		status             int
	}{
		{"", a.adminHost, "", 401}, {"Bearer " + a.config.LocalKey, a.adminHost, "", 401}, {"Bearer " + a.adminToken, "evil.example", "", 403}, {"Bearer " + a.adminToken, a.adminHost, "https://evil.example", 403}, {"Bearer " + a.adminToken, a.adminHost, "http://" + a.adminHost, 200},
	} {
		r := httptest.NewRequest("GET", "http://"+a.adminHost+"/api/state", nil)
		r.Host = tt.host
		r.Header.Set("Authorization", tt.auth)
		r.Header.Set("Origin", tt.origin)
		w := httptest.NewRecorder()
		a.adminHandler().ServeHTTP(w, r)
		if w.Code != tt.status {
			t.Fatalf("got %d expected %d", w.Code, tt.status)
		}
	}
	r := httptest.NewRequest("GET", "http://"+a.adminHost+"/", nil)
	w := httptest.NewRecorder()
	a.adminHandler().ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Security-Policy") == "" || !strings.Contains(w.Body.String(), "Kilo Proxy") {
		t.Fatal("embedded UI or CSP missing")
	}
}
func TestStartStopAndPortConflict(t *testing.T) {
	a := testApp(t)
	if !errors.Is(a.start(), errMissingCredentials) {
		t.Fatal("started without credentials")
	}
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = occupied.Close() })
	a.config.Port = occupied.Addr().(*net.TCPAddr).Port
	a.apiKey = "key"
	a.config.OrgID = "org"
	if err := a.start(); err == nil {
		t.Fatal("occupied port accepted")
	}
	occupied.Close()
	if err := a.start(); err != nil {
		t.Fatal(err)
	}
	if w := adminRequest(a, "config", `{"apiKey":"new","orgId":"new-org","port":8877,"remember":false}`); w.Code != 409 {
		t.Fatal("running credentials changed")
	}
	a.stop()
	a.stop()
	ln, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", jsonNumber(a.config.Port)))
	if err != nil {
		t.Fatalf("stop did not release port: %v", err)
	}
	ln.Close()
}

// Model the interval after Listen succeeds but before the Serve goroutine runs.
// Server.Close alone cannot release a listener it has not registered yet.
func TestStopBeforeServeReleasesPort(t *testing.T) {
	a := testApp(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	a.config.Port = ln.Addr().(*net.TCPAddr).Port
	a.apiKey, a.config.OrgID = "test-key", "test-org"
	srv := &http.Server{}
	a.proxyServer, a.proxyListener = srv, ln

	a.stop()
	a.stop()
	rebound, err := net.Listen("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatalf("stop returned before releasing the unserved listener: %v", err)
	}
	if err := rebound.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.start(); err != nil {
		t.Fatalf("immediate restart failed: %v", err)
	}
	// A late Serve from the previous run must not affect the new listener.
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("late Serve returned %v, want ErrServerClosed", err)
	}
	conn, err := net.DialTimeout("tcp4", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("late Serve interfered with the restarted proxy: %v", err)
	}
	conn.Close()
	a.stop()
}

func jsonNumber(n int) string { b, _ := json.Marshal(n); return string(b) }
func TestGatewayCheckUsesOrgAndDoesNotFollowRedirect(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		t.Run(map[bool]string{false: "catalog", true: "redirect"}[redirect], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/gateway/models" || r.Header.Get("X-KiloCode-OrganizationId") != "org" {
					t.Error("invalid catalog check")
				}
				if redirect {
					w.Header().Set("Location", "https://invalid.example/steal")
					w.WriteHeader(302)
					return
				}
				io.WriteString(w, `{"data":[{"id":"model"}]}`)
			}))
			defer upstream.Close()
			a := testApp(t)
			setUpstream(a, upstream.URL)
			a.apiKey = "key"
			a.config.OrgID = "org"
			w := adminRequest(a, "check", `{}`)
			if redirect && w.Code != 502 {
				t.Fatal("redirect was followed")
			}
			if !redirect && (w.Code != 200 || !strings.Contains(w.Body.String(), "no verifica el saldo")) {
				t.Fatal("catalog test misleading")
			}
		})
	}
}
func TestUpstreamFailureAndBodyLimit(t *testing.T) {
	a := testApp(t)
	setUpstream(a, "http://127.0.0.1:1")
	h := a.inferenceHandler("secret", "org", "local", "127.0.0.1:8877")
	for _, oversize := range []bool{false, true} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/responses", strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer local")
		want := 502
		if oversize {
			r.ContentLength = 33 << 20
			want = 413
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("unexpected failure response: %d", w.Code)
		}
	}
}

func TestLongKeyCanUseMemoryButNotPortableVault(t *testing.T) {
	a := testApp(t)
	input := map[string]any{"apiKey": strings.Repeat("a", 2500), "orgId": "org", "port": 8877, "remember": true}
	body, _ := json.Marshal(input)
	if w := adminRequest(a, "config", string(body)); w.Code != 400 {
		t.Fatal("long key sent to keyring")
	}
	input["remember"] = false
	body, _ = json.Marshal(input)
	if w := adminRequest(a, "config", string(body)); w.Code != 200 {
		t.Fatal("long key rejected in memory-only mode")
	}
}

func TestAllAllowedRoutesMapWithoutProtocolTranslation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		jsonResponse(w, 200, map[string]string{"path": r.URL.Path, "method": r.Method})
	}))
	defer upstream.Close()
	a := testApp(t)
	setUpstream(a, upstream.URL)
	h := a.inferenceHandler("key", "org", "local", "127.0.0.1:8877")
	for _, path := range []string{"models", "chat/completions", "responses", "messages"} {
		method := "POST"
		if path == "models" {
			method = "GET"
		}
		r := httptest.NewRequest(method, "http://127.0.0.1:8877/v1/"+path, nil)
		r.Header.Set("Authorization", "Bearer local")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "/api/gateway/"+path) {
			t.Fatalf("route %s failed: %s", path, w.Body)
		}
	}
}

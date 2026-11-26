package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type liteLLMOAuthFixture struct {
	server                 *httptest.Server
	codes                  atomic.Int32
	refreshes              atomic.Int32
	revocations            atomic.Int32
	catalogDown            atomic.Bool
	revokeDown             atomic.Bool
	redirectToken          string
	differentIssuer        string
	differentTokenEndpoint string
}

func newLiteLLMOAuthFixture(t *testing.T) *liteLLMOAuthFixture {
	t.Helper()
	f := &liteLLMOAuthFixture{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gateway := "http://" + r.Host + "/gateway"
		switch r.URL.Path {
		case "/gateway/.well-known/litellm-cli-auth":
			issuer, endpoint := gateway, gateway+"/token"
			if f.differentIssuer != "" {
				issuer = f.differentIssuer
			}
			if f.differentTokenEndpoint != "" {
				endpoint = f.differentTokenEndpoint
			}
			jsonResponse(w, http.StatusOK, liteLLMOAuthMetadata{
				Version: 1, Issuer: issuer, Resource: "http://" + r.Host,
				AuthorizationURL: gateway + "/authorize", RegistrationURL: gateway + "/register",
				TokenURL: endpoint, RevocationURL: gateway + "/revoke",
				ResponseTypes: []string{"code"}, GrantTypes: []string{"authorization_code", "refresh_token"},
				ChallengeMethods: []string{"S256"}, TokenAuthMethods: []string{"none"}, RevocationAuthMethods: []string{"none"},
			})
		case "/gateway/register":
			var registration struct {
				RedirectURIs            []string `json:"redirect_uris"`
				TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
				GrantTypes              []string `json:"grant_types"`
			}
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&registration) != nil ||
				len(registration.RedirectURIs) != 1 || !strings.HasPrefix(registration.RedirectURIs[0], "http://127.0.0.1:") ||
				!strings.HasSuffix(registration.RedirectURIs[0], "/callback") ||
				registration.TokenEndpointAuthMethod != "none" || strings.Join(registration.GrantTypes, ",") != "authorization_code,refresh_token" {
				t.Error("Invalid public client registration")
				http.Error(w, "invalid client", http.StatusBadRequest)
				return
			}
			jsonResponse(w, http.StatusCreated, map[string]any{
				"client_id": "synthetic-dcr-client", "token_endpoint_auth_method": "none", "redirect_uris": registration.RedirectURIs,
			})
		case "/gateway/token":
			if r.Method != http.MethodPost || r.ParseForm() != nil || r.Form.Get("client_id") != "synthetic-dcr-client" ||
				r.Form.Get("resource") != "http://"+r.Host || r.Header.Get("Authorization") != "" || r.Form.Has("client_secret") {
				t.Error("Invalid token exchange")
				http.Error(w, "invalid exchange", http.StatusBadRequest)
				return
			}
			if f.redirectToken != "" {
				http.Redirect(w, r, f.redirectToken, http.StatusTemporaryRedirect)
				return
			}
			access, refresh := "synthetic-access-original", "synthetic-refresh-original"
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				verifier := r.Form.Get("code_verifier")
				challenge := sha256.Sum256([]byte(verifier))
				if len(verifier) < 43 || len(verifier) > 128 ||
					base64.RawURLEncoding.EncodeToString(challenge[:]) != r.Form.Get("code") ||
					!strings.HasPrefix(r.Form.Get("redirect_uri"), "http://127.0.0.1:") {
					t.Error("Invalid PKCE code exchange")
					http.Error(w, "invalid PKCE", http.StatusBadRequest)
					return
				}
				f.codes.Add(1)
			case "refresh_token":
				if f.refreshes.Add(1) != 1 || r.Form.Get("refresh_token") != refresh || r.Form.Has("code_verifier") {
					t.Error("The one-use refresh token was reused or incorrectly sent")
					http.Error(w, "invalid_grant", http.StatusBadRequest)
					return
				}
				access, refresh = "synthetic-access-rotated", "synthetic-refresh-rotated"
			default:
				t.Error("Unexpected token grant")
				http.Error(w, "invalid grant", http.StatusBadRequest)
				return
			}
			jsonResponse(w, http.StatusOK, map[string]any{"access_token": access, "refresh_token": refresh, "token_type": "Bearer", "expires_in": 3600})
		case "/gateway/revoke":
			if r.Method != http.MethodPost || r.ParseForm() != nil || r.Form.Get("client_id") != "synthetic-dcr-client" ||
				!strings.HasPrefix(r.Form.Get("token"), "synthetic-refresh-") {
				t.Error("Invalid token revocation")
				http.Error(w, "invalid revoke", http.StatusBadRequest)
				return
			}
			if f.revokeDown.Load() {
				http.Error(w, "temporary external secret", http.StatusServiceUnavailable)
				return
			}
			f.revocations.Add(1)
			jsonResponse(w, http.StatusOK, map[string]any{})
		case "/gateway/v1/models":
			if f.catalogDown.Load() || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer synthetic-") {
				http.Error(w, "private upstream diagnostic", http.StatusForbidden)
				return
			}
			jsonResponse(w, http.StatusOK, map[string]any{"data": []map[string]string{{"id": "example-model"}}})
		case "/gateway/v1/responses":
			if r.Header.Get("Authorization") != "Bearer synthetic-access-rotated" {
				http.Error(w, "expected rotated credential", http.StatusForbidden)
				return
			}
			jsonResponse(w, http.StatusOK, map[string]any{"id": "synthetic-response", "model": "example-model"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func beginSyntheticLiteLLMOAuth(t *testing.T, a *app, gateway string) *url.URL {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"url": gateway})
	result := adminRequest(a, "providers/litellm/login", string(body))
	var view struct {
		AuthorizationURL string           `json:"authorizationURL"`
		Login            liteLLMLoginView `json:"litellmLogin"`
	}
	if result.Code != http.StatusOK || json.Unmarshal(result.Body.Bytes(), &view) != nil ||
		view.Login.Status != "pending" || view.AuthorizationURL == "" {
		t.Fatalf("Start LiteLLM browser login failed: %d %s", result.Code, result.Body.String())
	}
	target, err := url.Parse(view.AuthorizationURL)
	if err != nil || !strings.HasPrefix(target.String(), gateway+"/authorize?") {
		t.Fatal("Authorization address is not on the configured gateway")
	}
	query := target.Query()
	if query.Get("state") == "" || query.Get("code_challenge_method") != "S256" || query.Get("response_type") != "code" ||
		query.Get("client_id") != "synthetic-dcr-client" || !strings.HasPrefix(query.Get("redirect_uri"), "http://127.0.0.1:") {
		t.Fatal("Missing PKCE authorization query")
	}
	return target
}

func sendSyntheticLiteLLMCallback(t *testing.T, authorization *url.URL, state string) (int, string) {
	t.Helper()
	query := authorization.Query()
	target := query.Get("redirect_uri") + "?" + url.Values{
		"state": {state}, "code": {query.Get("code_challenge")},
	}.Encode()
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	result, err := client.Get(target)
	if err != nil {
		t.Fatalf("LiteLLM callback: %v", err)
	}
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	return result.StatusCode, string(body)
}

func TestLiteLLMBrowserLoginRefreshAndLogout(t *testing.T) {
	f := newLiteLLMOAuthFixture(t)
	vault := &fakeVault{values: map[string]string{}}
	a, err := newApp(t.TempDir(), vault)
	if err != nil {
		t.Fatal(err)
	}
	a.adminHost = "127.0.0.1:9999"
	t.Cleanup(a.requestQuit)
	auth := beginSyntheticLiteLLMOAuth(t, a, f.server.URL+"/gateway")
	if status, _ := sendSyntheticLiteLLMCallback(t, auth, "wrong-state"); status != http.StatusBadRequest || f.codes.Load() != 0 {
		t.Fatal("Wrong state was accepted on the loopback callback")
	}
	if status, body := sendSyntheticLiteLLMCallback(t, auth, auth.Query().Get("state")); status != http.StatusOK {
		t.Fatalf("Valid PKCE callback was rejected: %d %s", status, body)
	}
	a.mu.Lock()
	connection, ready := a.liteLLMConnectionLocked()
	scope, revision := a.catalogScopeLocked(), a.catalogRevision
	rawState := a.liteLLMViewLocked()
	secret := *a.liteLLMBrowser
	now := time.Now()
	secret.RefreshAfter, secret.ExpiresAt = now.Add(-time.Minute), now.Add(-time.Second)
	a.liteLLMBrowser = &secret
	a.mu.Unlock()
	if !ready || !rawState.HasKey || !rawState.KeySaved || rawState.AuthMode != "browser" ||
		rawState.ModelsCount != 1 || f.codes.Load() != 1 {
		t.Fatal("Verified browser credential was not published")
	}

	const requests = 8
	errs := make(chan error, requests)
	for range requests {
		go func() {
			refreshed, err := a.liteLLMRequestCredential(context.Background(), connection)
			if err == nil && (refreshed.Key != "synthetic-access-rotated" || liteLLMAccountScope(refreshed) != liteLLMAccountScope(connection)) {
				t.Error("Refreshed request used the wrong credential or usage scope")
			}
			errs <- err
		}()
	}
	for range requests {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	a.mu.Lock()
	if a.catalogScopeLocked() != scope || a.catalogRevision != revision {
		a.mu.Unlock()
		t.Fatal("Credential renewal revoked model catalog scope or revision")
	}
	a.mu.Unlock()
	if f.refreshes.Load() != 1 {
		t.Fatal("Concurrent requests did not share one token rotation")
	}
	const local = "127.0.0.1:8877"
	input, _ := json.Marshal(map[string]string{"model": liteLLMNamespace(connection.Base) + "example-model"})
	request := httptest.NewRequest(http.MethodPost, "http://"+local+"/v1/responses", strings.NewReader(string(input)))
	request.Header.Set("Authorization", "Bearer "+a.config.LocalKey)
	result := httptest.NewRecorder()
	a.inferenceHandler("", "", a.config.LocalKey, local).ServeHTTP(result, request)
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), "synthetic-response") {
		t.Fatal("Browser credential did not reach local inference", result.Code, result.Body.String())
	}

	restored, err := newApp(a.dir, vault)
	if err != nil {
		t.Fatal(err)
	}
	restored.adminHost = "127.0.0.1:9999"
	t.Cleanup(restored.requestQuit)
	restoredConnection, loaded := restored.liteLLMConnectionLocked()
	if !loaded || restoredConnection.Key != "synthetic-access-rotated" || restored.catalogScopeLocked() != scope {
		t.Fatal("Rotated browser credential did not survive restart")
	}
	f.revokeDown.Store(true)
	failedLogout := adminRequest(restored, "providers/litellm/logout", "{}")
	if failedLogout.Code != http.StatusBadGateway || strings.Contains(failedLogout.Body.String(), "temporary external secret") ||
		!restored.liteLLMReadyLocked() {
		t.Fatal("Failed revocation erased or exposed credential state")
	}
	f.revokeDown.Store(false)
	logout := adminRequest(restored, "providers/litellm/logout", "{}")
	if logout.Code != http.StatusOK || f.revocations.Load() != 1 {
		t.Fatalf("Could not revoke the newest refresh token: %d %s", logout.Code, logout.Body.String())
	}
	final := restored.liteLLMViewLocked()
	if final.Enabled || final.Ready || final.HasKey || final.KeySaved || final.AuthMode != "browser" || final.URL != f.server.URL+"/gateway/v1" {
		t.Fatal("Logout did not preserve only the reconnect details")
	}
}

func TestLiteLLMBrowserLoginGuardsAndKeySwitch(t *testing.T) {
	f := newLiteLLMOAuthFixture(t)
	a, err := newApp(t.TempDir(), &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	a.adminHost = "127.0.0.1:9999"
	t.Cleanup(a.requestQuit)
	root := f.server.URL + "/gateway"
	setKey := func(url, key string) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(map[string]string{"url": url, "apiKey": key})
		return adminRequest(a, "providers/litellm", string(payload))
	}
	if r := setKey(root, "synthetic-manual"); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := adminRequest(a, "providers/litellm/login", `{"url":"http://example.invalid/gateway"}`); r.Code != http.StatusBadRequest {
		t.Fatal("Browser login allowed insecure non-loopback transport")
	}
	auth := beginSyntheticLiteLLMOAuth(t, a, root)
	if r := adminRequest(a, "providers/litellm/login/cancel", "{}"); r.Code != 200 || f.codes.Load() != 0 || a.liteLLMKey != "synthetic-manual" {
		t.Fatal("Cancel changed the previous manual key connection")
	}
	f.catalogDown.Store(true)
	auth = beginSyntheticLiteLLMOAuth(t, a, root)
	if status, body := sendSyntheticLiteLLMCallback(t, auth, auth.Query().Get("state")); status != http.StatusBadRequest ||
		strings.Contains(body, "private upstream diagnostic") || a.liteLLMKey != "synthetic-manual" {
		t.Fatal("Rejected catalog probe did not preserve the previous manual key")
	}
	f.catalogDown.Store(false)
	auth = beginSyntheticLiteLLMOAuth(t, a, root)
	if status, _ := sendSyntheticLiteLLMCallback(t, auth, auth.Query().Get("state")); status != http.StatusOK {
		t.Fatal("Browser connection could not replace manual key")
	}
	body, _ := json.Marshal(map[string]string{"url": root})
	if r := adminRequest(a, "providers/litellm", string(body)); r.Code != http.StatusBadRequest || a.config.LiteLLM.AuthMode != "browser" {
		t.Fatal("Manual form treated a browser token as a saved key")
	}
	if r := adminRequest(a, "providers/litellm/active", `{"enabled":false}`); r.Code != 200 {
		t.Fatal("Pausing browser connection failed")
	}
	if r := setKey(root, "synthetic-replacement"); r.Code != http.StatusOK || a.config.LiteLLM.Enabled ||
		a.config.LiteLLM.AuthMode != "" || a.liteLLMKey != "synthetic-replacement" || a.liteLLMBrowser != nil {
		t.Fatal("Manual key did not replace the browser session while preserving pause")
	}
}

func TestLiteLLMBrowserRejectsForeignEndpointsAndTokenRedirects(t *testing.T) {
	var foreignRequests atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreignRequests.Add(1)
	}))
	defer foreign.Close()
	for _, mode := range []string{"issuer", "token-origin", "token-redirect"} {
		t.Run(mode, func(t *testing.T) {
			f := newLiteLLMOAuthFixture(t)
			switch mode {
			case "issuer":
				f.differentIssuer = foreign.URL
			case "token-origin":
				f.differentTokenEndpoint = foreign.URL + "/token"
			case "token-redirect":
				f.redirectToken = foreign.URL + "/token"
			}
			a, err := newApp(t.TempDir(), &fakeVault{values: map[string]string{}})
			if err != nil {
				t.Fatal(err)
			}
			a.adminHost = "127.0.0.1:9999"
			t.Cleanup(a.requestQuit)
			gateway := f.server.URL + "/gateway"
			if mode == "token-redirect" {
				auth := beginSyntheticLiteLLMOAuth(t, a, gateway)
				if status, _ := sendSyntheticLiteLLMCallback(t, auth, auth.Query().Get("state")); status != http.StatusBadRequest {
					t.Fatal("Token redirect was accepted")
				}
			} else {
				payload, _ := json.Marshal(map[string]string{"url": gateway})
				start := adminRequest(a, "providers/litellm/login", string(payload))
				if start.Code != http.StatusUnprocessableEntity || !strings.Contains(start.Body.String(), "PROXY_BASE_URL") {
					t.Fatal("Unexpected issuer or endpoint was accepted without guidance")
				}
			}
			if foreignRequests.Load() != 0 || a.config.LiteLLM.URL != "" {
				t.Fatal("OAuth attempted a foreign hop or persisted an invalid login")
			}
		})
	}
}

func TestLiteLLMBrowserRecoversUncommittedRotationFromVault(t *testing.T) {
	f := newLiteLLMOAuthFixture(t)
	vault := &fakeVault{values: map[string]string{}}
	a, err := newApp(t.TempDir(), vault)
	if err != nil {
		t.Fatal(err)
	}
	a.adminHost = "127.0.0.1:9999"
	t.Cleanup(a.requestQuit)
	auth := beginSyntheticLiteLLMOAuth(t, a, f.server.URL+"/gateway")
	if status, _ := sendSyntheticLiteLLMCallback(t, auth, auth.Query().Get("state")); status != http.StatusOK {
		t.Fatal("Initial login failed")
	}
	a.mu.Lock()
	first, _ := a.liteLLMConnectionLocked()
	old := *a.liteLLMBrowser
	old.RefreshAfter, old.ExpiresAt = time.Now().Add(-time.Minute), time.Now().Add(-time.Second)
	a.liteLLMBrowser = &old
	a.mu.Unlock()

	f.catalogDown.Store(true)
	if _, err := a.liteLLMRequestCredential(context.Background(), first); err != errLiteLLMOAuthRenew {
		t.Fatalf("Invalid renewal was accepted for inference: %v", err)
	}
	a.mu.Lock()
	view := a.liteLLMViewLocked()
	a.mu.Unlock()
	if view.Ready || view.HasKey || !view.KeySaved {
		t.Fatal("Expired session with a failed renewal looked active")
	}

	restored, err := newApp(a.dir, vault)
	if err != nil {
		t.Fatal(err)
	}
	restored.adminHost = "127.0.0.1:9999"
	t.Cleanup(restored.requestQuit)
	f.catalogDown.Store(false)
	authorized, err := restored.liteLLMRequestCredential(context.Background(), first)
	if err != nil || authorized.Key != "synthetic-access-rotated" || f.refreshes.Load() != 1 {
		t.Fatalf("Restart failed to recover the staged pair without repeating refresh: %v", err)
	}
}

func TestLiteLLMBrowserLogoutClearsRevokedCredentialOnSettingsError(t *testing.T) {
	f := newLiteLLMOAuthFixture(t)
	vault := &fakeVault{values: map[string]string{}}
	a, err := newApp(t.TempDir(), vault)
	if err != nil {
		t.Fatal(err)
	}
	a.adminHost = "127.0.0.1:9999"
	t.Cleanup(a.requestQuit)
	auth := beginSyntheticLiteLLMOAuth(t, a, f.server.URL+"/gateway")
	if status, _ := sendSyntheticLiteLLMCallback(t, auth, auth.Query().Get("state")); status != http.StatusOK {
		t.Fatal("Initial login failed")
	}
	// A directory at the destination specifically fails the final settings
	// rename. The previously saved settings let us check reboot behavior.
	settingsFile := filepath.Join(a.dir, "settings.json")
	saved := filepath.Join(a.dir, "before-logout.json")
	if err := os.Rename(settingsFile, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(settingsFile, 0700); err != nil {
		t.Fatal(err)
	}
	result := adminRequest(a, "providers/litellm/logout", "{}")
	if result.Code != http.StatusInternalServerError || f.revocations.Load() != 1 ||
		a.liteLLMReadyLocked() || len(vault.values) != 0 {
		t.Fatalf("Failed local commit retained a revoked credential: %d %s", result.Code, result.Body.String())
	}
	if err := os.Remove(settingsFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, settingsFile); err != nil {
		t.Fatal(err)
	}
	restored, err := newApp(a.dir, vault)
	if err != nil {
		t.Fatal(err)
	}
	restored.adminHost = "127.0.0.1:9999"
	t.Cleanup(restored.requestQuit)
	if restored.liteLLMReadyLocked() {
		t.Fatal("Restart reused a revoked browser credential")
	}
}

func TestLiteLLMBrowserVaultChunksRoundTrip(t *testing.T) {
	vault := &fakeVault{values: map[string]string{}}
	a, err := newApp(t.TempDir(), vault)
	if err != nil {
		t.Fatal(err)
	}
	a.adminHost = "127.0.0.1:9999"
	t.Cleanup(a.requestQuit)
	base, err := normalizeLiteLLMURL("https://example.invalid/gat%C3%A9way")
	if err != nil {
		t.Fatal(err)
	}
	origin := liteLLMBrowserBase(base).String()
	cfg := liteLLMSettings{URL: base.String(), AuthMode: "browser", Enabled: true, VaultRef: randomKey("")}
	now := time.Now().UTC()
	credential := liteLLMBrowserCredential{
		ClientID: "synthetic", Resource: origin, TokenURL: origin + "/token", RevocationURL: origin + "/revoke",
		Access: strings.Repeat("a", 2600), Refresh: strings.Repeat("b", 2800),
		ExpiresAt: now.Add(time.Hour), RefreshAfter: now.Add(time.Hour - time.Minute),
	}
	if err := a.saveLiteLLMBrowserCredential(a.config.VaultID, cfg, credential); err != nil {
		t.Fatal(err)
	}
	if len(vault.values) <= 2 {
		t.Fatal("Large browser credential bypassed the portable slot limit")
	}
	for _, chunk := range vault.values {
		if len(chunk) > credentialVaultLimit(vault) || !validLiteLLMKey(chunk) {
			t.Fatal("Vault chunk cannot be represented by the portable keychain")
		}
	}
	recovered := a.loadLiteLLMBrowserCredential(a.config.VaultID, cfg)
	if recovered == nil || *recovered != credential {
		t.Fatal("Credential vault could not restore the original token pair")
	}
}

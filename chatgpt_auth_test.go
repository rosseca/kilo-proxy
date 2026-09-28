package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

type chatGPTTestVault struct {
	mu                        sync.Mutex
	values                    map[string]string
	getErr, setErr, deleteErr error
	gets                      []string
	limit                     int
}

func (v *chatGPTTestVault) Get(id string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.gets = append(v.gets, id)
	if v.getErr != nil {
		return "", v.getErr
	}
	value, ok := v.values[id]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}
func (v *chatGPTTestVault) Set(id, value string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.setErr != nil {
		return v.setErr
	}
	if v.limit > 0 && len(value) > v.limit {
		return keyring.ErrSetDataTooBig
	}
	if v.values == nil {
		v.values = map[string]string{}
	}
	v.values[id] = value
	return nil
}
func (v *chatGPTTestVault) Delete(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.deleteErr != nil {
		return v.deleteErr
	}
	delete(v.values, id)
	return nil
}

func chatGPTTestJWT(account string) string {
	data, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": account, "chatgpt_plan_type": "plus"}, "https://api.openai.com/profile": map[string]any{"email": "fixture@example.invalid"}})
	return "fixture." + base64.RawURLEncoding.EncodeToString(data) + ".signature"
}

func chatGPTTestConnection(t *testing.T, handler http.HandlerFunc, expired bool) (*chatGPTConnection, *chatGPTTestVault) {
	t.Helper()
	vault := &chatGPTTestVault{}
	vault.values = map[string]string{"fixture": "kilo-secret-untouched"}
	server := httptest.NewServer(handler)
	c := newChatGPTConnection(vault, "fixture", server.Client().Transport, t.TempDir())
	if expired {
		if err := c.saveLocked(chatGPTCredentials{Access: chatGPTTestJWT("account-one"), Refresh: "old-refresh-private", Account: "account-one", Email: "fixture@example.invalid", Plan: "plus", Expires: time.Now().Add(-time.Hour).Unix()}); err != nil {
			t.Fatal(err)
		}
	}
	c.authURL, c.backendURL, c.pollDelay = server.URL, server.URL, time.Millisecond
	t.Cleanup(func() { c.cancel(); server.Close() })
	return c, vault
}

func chatGPTTestStored(t *testing.T, c *chatGPTConnection) chatGPTCredentials {
	t.Helper()
	loaded := newChatGPTConnection(c.vault, strings.TrimSuffix(c.vaultID, "-chatgpt"), nil, c.storageDir)
	if !loaded.snapshot().Connected {
		t.Fatalf("saved credential unreadable: %+v", loaded.snapshot())
	}
	return loaded.creds
}

func waitChatGPTState(t *testing.T, c *chatGPTConnection, predicate func(chatGPTState) bool) chatGPTState {
	t.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		s := c.snapshot()
		if predicate(s) {
			return s
		}
		select {
		case <-timeout.C:
			t.Fatalf("ChatGPT state did not converge: %+v", s)
		case <-tick.C:
		}
	}
}

func TestChatGPTDeviceLoginOwnLifetimeVaultAndQuota(t *testing.T) {
	var polls atomic.Int32
	access := chatGPTTestJWT("account-one")
	c, vault := chatGPTTestConnection(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-KiloCode-OrganizationId") != "" {
			t.Error("unrelated credentials sent")
		}
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			if r.Method != "POST" || r.Header.Get("Authorization") != "" {
				t.Error("device initiation request")
			}
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["client_id"] != chatGPTClientID {
				t.Error("device client ID")
			}
			fmt.Fprint(w, `{"device_auth_id":"private-device-id","user_code":"ABCD-EFGH","interval":"1","verification_uri":"https://attacker.invalid/"}`)
		case "/api/accounts/deviceauth/token":
			if polls.Add(1) == 1 {
				w.WriteHeader(403)
				return
			}
			fmt.Fprint(w, `{"authorization_code":"private-code","code_verifier":"private-verifier"}`)
		case "/oauth/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("client_id") != chatGPTClientID || r.Form.Get("redirect_uri") != chatGPTAuthURL+"/deviceauth/callback" || r.Form.Get("code_verifier") != "private-verifier" {
				t.Error("token exchange form")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": "private-refresh", "expires_in": 3600})
		case "/wham/usage":
			if r.Header.Get("Authorization") != "Bearer "+access || r.Header.Get("ChatGPT-Account-Id") != "account-one" {
				t.Error("usage authentication")
			}
			fmt.Fprint(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":12.5,"limit_window_seconds":18000,"reset_at":2000000000},"secondary_window":{"used_percent":0,"limit_window_seconds":604800,"reset_after_seconds":42}}}`)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}, false)
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.begin(ctx); err != nil {
		t.Fatal(err)
	}
	cancel() // The completed HTTP request must not end the browser login.
	s := waitChatGPTState(t, c, func(s chatGPTState) bool { return s.Connected && s.Quota.Available })
	if s.Status != "idle" || s.Code != "" || s.VerificationURL != "" || s.Email != "fixture@example.invalid" || s.Plan != "pro" {
		t.Fatalf("unexpected connected state: %+v", s)
	}
	if *s.Quota.Primary.UsedPercent != 12.5 || *s.Quota.Primary.WindowDurationMins != 300 || *s.Quota.Secondary.UsedPercent != 0 || *s.Quota.Secondary.WindowDurationMins != 10080 {
		t.Fatalf("quota: %+v", s.Quota)
	}
	*s.Quota.Primary.UsedPercent = 99
	if *c.snapshot().Quota.Primary.UsedPercent != 12.5 {
		t.Fatal("snapshot aliases state")
	}
	data, _ := json.Marshal(c.snapshot())
	for _, secret := range []string{access, "private-refresh", "private-device-id", "private-verifier", "private-code", "account-one"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("credential in public state")
		}
	}
	stored, err := vault.Get("fixture-chatgpt")
	if err != nil || len(stored) > 128 || strings.Contains(stored, "private-refresh") || chatGPTTestStored(t, c).Refresh != "private-refresh" {
		t.Fatal("encrypted credential not saved in its own slot and file")
	}
	if c.identity() != "account-one" {
		t.Fatal("account identity unavailable")
	}
	if err := c.logout(); err != nil {
		t.Fatal(err)
	}
	if c.snapshot().Connected || c.identity() != "" {
		t.Fatal("logout retained connection")
	}
	if _, err := vault.Get("fixture-chatgpt"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatal("vault not removed")
	}
}

func TestChatGPTCancelDiscardsPendingLogin(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	c, vault := chatGPTTestConnection(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/accounts/deviceauth/usercode" {
			fmt.Fprint(w, `{"device_auth_id":"id","user_code":"CODE","interval":1}`)
			return
		}
		close(entered)
		<-release
		fmt.Fprint(w, `{"authorization_code":"code","code_verifier":"verifier"}`)
	}, false)
	if err := c.begin(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitChatGPTState(t, c, func(s chatGPTState) bool { return s.Code == "CODE" })
	if c.snapshot().VerificationURL != chatGPTVerificationURL {
		t.Fatal("untrusted login URL")
	}
	if err := c.begin(context.Background()); err == nil {
		t.Fatal("duplicate login accepted")
	}
	<-entered
	c.cancel()
	close(release)
	s := c.snapshot()
	if s.Status != "idle" || s.Code != "" || s.Connected {
		t.Fatalf("cancel state: %+v", s)
	}
	if _, err := vault.Get("fixture-chatgpt"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatal("cancel wrote credentials")
	}
}

func TestChatGPTRefreshSingleFlightRotationAndCallerCancellation(t *testing.T) {
	var requests atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	access := chatGPTTestJWT("account-one")
	c, vault := chatGPTTestConnection(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			return
		}
		if requests.Add(1) == 1 {
			close(entered)
		}
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-refresh-private" {
			t.Error("refresh form")
		}
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": "rotated-refresh-private", "expires_in": 3600})
	}, true)
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, _, err := c.credentials(ctx); first <- err }()
	<-entered
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, account, err := c.credentials(context.Background())
			if err != nil || token != access || account != "account-one" {
				t.Error("concurrent refresh failed")
			}
		}()
	}
	close(release)
	wg.Wait()
	if requests.Load() != 1 {
		t.Fatalf("refresh requests: %d", requests.Load())
	}
	if chatGPTTestStored(t, c).Refresh != "rotated-refresh-private" {
		t.Fatal("rotation not persisted")
	}
	if kilo, _ := vault.Get("fixture"); kilo != "kilo-secret-untouched" {
		t.Fatal("Kilo credential changed")
	}
}

func TestChatGPTLogoutDuringRefreshCannotRestoreCredential(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	c, vault := chatGPTTestConnection(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": chatGPTTestJWT("account-one"), "refresh_token": "new-private", "expires_in": 3600})
	}, true)
	finished := make(chan error, 1)
	go func() { _, _, err := c.credentials(context.Background()); finished <- err }()
	<-entered
	if err := c.logout(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-finished; err == nil {
		t.Fatal("stale refresh succeeded")
	}
	if c.snapshot().Connected {
		t.Fatal("stale refresh restored state")
	}
	if _, err := vault.Get("fixture-chatgpt"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatal("stale refresh restored vault")
	}
}

func TestChatGPTRefreshFailurePreservesStoredCredentialsAndRedactsErrors(t *testing.T) {
	c, vault := chatGPTTestConnection(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		fmt.Fprint(w, `{"error":"private-server-secret"}`)
	}, true)
	before, _ := vault.Get("fixture-chatgpt")
	_, _, err := c.credentials(context.Background())
	if err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe refresh error: %v", err)
	}
	after, _ := vault.Get("fixture-chatgpt")
	if before != after {
		t.Fatal("transient error mutated credentials")
	}
	data, _ := json.Marshal(c.snapshot())
	if strings.Contains(string(data), "private") {
		t.Fatal("server body leaked to state")
	}
}

func TestChatGPTConstructorMissingCorruptAndLockedVault(t *testing.T) {
	for _, tc := range []struct {
		name   string
		vault  *chatGPTTestVault
		status string
	}{
		{"missing", &chatGPTTestVault{}, "idle"},
		{"corrupt", &chatGPTTestVault{values: map[string]string{"fixture-chatgpt": "secret-not-json"}}, "error"},
		{"locked", &chatGPTTestVault{getErr: errors.New("private-keyring-diagnostic")}, "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newChatGPTConnection(tc.vault, "fixture", nil)
			s := c.snapshot()
			if s.Status != tc.status || s.Connected {
				t.Fatalf("state: %+v", s)
			}
			if !reflect.DeepEqual(tc.vault.gets, []string{"fixture-chatgpt"}) {
				t.Fatalf("read outside own vault slot: %v", tc.vault.gets)
			}
			data, _ := json.Marshal(s)
			if strings.Contains(string(data), "secret") || strings.Contains(string(data), "diagnostic") {
				t.Fatal("vault detail leaked")
			}
			if c.client.Transport.(*http.Transport).Proxy != nil {
				t.Fatal("ambient proxy enabled")
			}
		})
	}
	var c *chatGPTConnection
	if c.snapshot().Connected || c.identity() != "" {
		t.Fatal("nil connection not idle")
	}
}

func TestChatGPTModelsUseAccountCatalogAndRealCapabilities(t *testing.T) {
	c, _ := chatGPTTestConnection(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/models" || r.URL.Query().Get("client_version") != chatGPTClientVersion || r.Header.Get("ChatGPT-Account-Id") != "account-one" || r.Header.Get("OpenAI-Beta") != "responses=experimental" {
			t.Error("catalog request")
		}
		fmt.Fprint(w, `{"models":[{"slug":"gpt-fixture","display_name":"Fixture","context_window":272000,"max_output_tokens":10000,"input_modalities":["text","image"],"supported_reasoning_levels":[{"effort":"low"},{"effort":"high"},{"effort":"invalid"},{"effort":"high"}]},{"slug":"hidden","visibility":"hidden"},{"slug":"only-code","tool_mode":"code_mode_only"},{"slug":"gpt-fixture"},{"slug":"missing-limits"}]}`)
	}, true)
	c.mu.Lock()
	c.creds.Expires = time.Now().Add(time.Hour).Unix()
	c.mu.Unlock()
	models, err := c.models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "chatgpt/gpt-fixture" || models[0].Name != "Fixture · ChatGPT" || models[0].ContextWindow != 272000 || models[0].MaxOutputTokens != 10000 || models[0].InputPrice != nil || models[0].OutputPrice != nil || !reflect.DeepEqual(models[0].ReasoningEfforts, []string{"low", "high"}) {
		t.Fatalf("models: %+v", models)
	}
	if models[1].ContextWindow != 0 || models[1].MaxOutputTokens != 0 {
		t.Fatal("invented model limits")
	}
}

func TestChatGPTQuotaUnknownIsNotZeroAndFailureKeepsConnection(t *testing.T) {
	var bad atomic.Bool
	c, _ := chatGPTTestConnection(t, func(w http.ResponseWriter, r *http.Request) {
		if bad.Load() {
			w.WriteHeader(502)
			fmt.Fprint(w, "private-error")
			return
		}
		fmt.Fprint(w, `{"plan_type":"plus","rate_limit":{"primary_window":{}}}`)
	}, true)
	c.mu.Lock()
	c.creds.Expires = time.Now().Add(time.Hour).Unix()
	c.mu.Unlock()
	if err := c.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := c.snapshot()
	if s.Quota.Available || s.Quota.Primary != nil || !s.Connected || s.Quota.Error == "" {
		t.Fatalf("unknown quota: %+v", s)
	}
	bad.Store(true)
	if err := c.refresh(context.Background()); err == nil {
		t.Fatal("quota HTTP error ignored")
	}
	if !c.snapshot().Connected {
		t.Fatal("quota error disconnected account")
	}
}

func TestChatGPTRedirectNeverForwardsCredentials(t *testing.T) {
	var visited atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { visited.Add(1) }))
	defer destination.Close()
	c, _ := chatGPTTestConnection(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}, true)
	c.mu.Lock()
	c.creds.Expires = time.Now().Add(time.Hour).Unix()
	c.mu.Unlock()
	if _, err := c.models(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
	if visited.Load() != 0 {
		t.Fatal("credentials followed redirect")
	}
}

func TestChatGPTVaultWriteAndDeleteFailureAreVisibleWithoutSecrets(t *testing.T) {
	c, vault := chatGPTTestConnection(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": chatGPTTestJWT("account-one"), "refresh_token": "new-private", "expires_in": 3600})
	}, true)
	c.writeCredentials = func(string, []byte) error { return errors.New("private-write-error") }
	if _, _, err := c.credentials(context.Background()); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("save error: %v", err)
	}
	vault.mu.Lock()
	vault.deleteErr = errors.New("private-delete-error")
	vault.mu.Unlock()
	if err := c.logout(); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("delete error: %v", err)
	}
	if c.snapshot().Connected || c.snapshot().Status != "error" {
		t.Fatal("failed disconnect did not clear in-memory credential and report failure")
	}
}

func TestChatGPTRefreshPreservesMissingRotationAndRejectsAccountSwitch(t *testing.T) {
	for _, different := range []bool{false, true} {
		t.Run(fmt.Sprint(different), func(t *testing.T) {
			account := "account-one"
			if different {
				account = "account-two"
			}
			c, vault := chatGPTTestConnection(t, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": chatGPTTestJWT(account), "expires_in": 3600})
			}, true)
			before, _ := vault.Get("fixture-chatgpt")
			_, got, err := c.credentials(context.Background())
			stored, _ := vault.Get("fixture-chatgpt")
			if different {
				if err == nil || stored != before || c.identity() != "account-one" {
					t.Fatal("refresh switched accounts")
				}
				return
			}
			if err != nil || got != "account-one" || chatGPTTestStored(t, c).Refresh != "old-refresh-private" {
				t.Fatal("omitted refresh token lost existing rotation")
			}
		})
	}
}

func TestChatGPTLoginFailureHasNoUntrustedServerText(t *testing.T) {
	c, _ := chatGPTTestConnection(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"message":"private-service-diagnostic"}`)
	}, false)
	if err := c.begin(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := waitChatGPTState(t, c, func(s chatGPTState) bool { return s.Status == "error" })
	if s.Connected || s.Code != "" || s.VerificationURL != "" || strings.Contains(s.Error, "private") {
		t.Fatalf("failure state: %+v", s)
	}
}

func TestChatGPTQuotaExpiresWithoutNetworkOrErasingSnapshot(t *testing.T) {
	var requests atomic.Int32
	c, _ := chatGPTTestConnection(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, `{"rate_limit":{"primary_window":{"used_percent":40,"limit_window_seconds":18000}}}`)
	}, true)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	c.mu.Lock()
	c.now = func() time.Time { return now }
	c.creds.Expires = now.Add(time.Hour).Unix()
	c.mu.Unlock()
	if err := c.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh := c.snapshot()
	if fresh.Quota.FetchedAt != "2026-09-28T12:00:00Z" || !fresh.Quota.Available || fresh.Quota.Stale {
		t.Fatalf("fresh quota: %+v", fresh.Quota)
	}
	c.mu.Lock()
	c.now = func() time.Time { return now.Add(6 * time.Minute) }
	c.mu.Unlock()
	stale := c.snapshot()
	if stale.Quota.Available || !stale.Quota.Stale || stale.Quota.Error == "" || *stale.Quota.Primary.UsedPercent != 40 {
		t.Fatalf("stale quota: %+v", stale.Quota)
	}
	if requests.Load() != 1 {
		t.Fatal("snapshot started a network request")
	}
}

func TestChatGPTEncryptedCredentialsLargeTokensRoundTripAndPermissions(t *testing.T) {
	vault := &chatGPTTestVault{limit: 2400}
	dir := t.TempDir()
	c := newChatGPTConnection(vault, "fixture", nil, dir)
	creds := chatGPTCredentials{Access: "private-access-" + strings.Repeat("a", 60<<10), Refresh: "private-refresh-" + strings.Repeat("b", 12<<10), Account: "account-one", Email: "fixture@example.invalid", Plan: "plus", Expires: time.Now().Add(time.Hour).Unix()}
	if err := c.saveLocked(creds); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "chatgpt-credentials.enc")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("private-access")) || bytes.Contains(data, []byte("private-refresh")) || bytes.Contains(data, []byte("fixture@example.invalid")) {
		t.Fatal("plaintext credential reached disk")
	}
	stored, err := vault.Get("fixture-chatgpt")
	if err != nil || len(stored) > 128 || !strings.HasPrefix(stored, "v1:") {
		t.Fatal("vault must contain only a small encryption key")
	}
	if !reflect.DeepEqual(chatGPTTestStored(t, c), creds) {
		t.Fatal("encrypted credentials changed on reload")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("credential permissions: %v", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary credential file left behind")
	}
	if err := c.logout(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("logout retained encrypted credentials")
	}
}

func TestChatGPTEncryptedCredentialsRejectTamperAndWrongSlot(t *testing.T) {
	c, vault := chatGPTTestConnection(t, func(http.ResponseWriter, *http.Request) {}, true)
	path := filepath.Join(c.storageDir, "chatgpt-credentials.enc")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	key, err := vault.Get(c.vaultID)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Set("other-chatgpt", key); err != nil {
		t.Fatal(err)
	}
	other := newChatGPTConnection(vault, "other", nil, c.storageDir)
	if other.snapshot().Connected || other.snapshot().Status != "error" {
		t.Fatal("ciphertext moved across vault identities")
	}
	data[len(data)-1] ^= 1
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	tampered := newChatGPTConnection(vault, "fixture", nil, c.storageDir)
	if tampered.snapshot().Connected || tampered.snapshot().Status != "error" {
		t.Fatal("tampered ciphertext accepted")
	}
	if err := vault.Delete(c.vaultID); err != nil {
		t.Fatal(err)
	}
	missing := newChatGPTConnection(vault, "fixture", nil, c.storageDir)
	if missing.snapshot().Connected || missing.snapshot().Status != "error" {
		t.Fatal("missing encryption key not reported")
	}
}

func TestChatGPTEncryptedCredentialWriteFailurePreservesPriorFile(t *testing.T) {
	c, _ := chatGPTTestConnection(t, func(http.ResponseWriter, *http.Request) {}, true)
	path := filepath.Join(c.storageDir, "chatgpt-credentials.enc")
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c.writeCredentials = func(got string, data []byte) error {
		if got != path || bytes.Contains(data, []byte("replacement-refresh")) {
			t.Error("unsafe atomic writer input")
		}
		return errors.New("private-filesystem-detail")
	}
	creds := c.creds
	creds.Refresh = "replacement-refresh"
	if err := c.saveLocked(creds); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("save failure: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(old, after) {
		t.Fatal("failed write changed prior ciphertext")
	}
	if chatGPTTestStored(t, c).Refresh != "old-refresh-private" {
		t.Fatal("failed write changed prior credential")
	}
}

func TestChatGPTEncryptionKeyFailureDoesNotWriteCredentialFile(t *testing.T) {
	vault := &chatGPTTestVault{setErr: errors.New("private-vault-error")}
	dir := t.TempDir()
	c := newChatGPTConnection(vault, "fixture", nil, dir)
	creds := chatGPTCredentials{Access: "access", Refresh: "refresh", Account: "account", Expires: time.Now().Add(time.Hour).Unix()}
	if err := c.saveLocked(creds); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("key failure: %v", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatal("credential bytes written before key persistence")
	}
}

func TestChatGPTCredentialFileRejectsUnsafeDestination(t *testing.T) {
	for _, kind := range []string{"directory", "symlink"} {
		if kind == "symlink" && runtime.GOOS == "windows" {
			continue
		}
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "chatgpt-credentials.enc")
			target := filepath.Join(t.TempDir(), "untouched")
			if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			c := newChatGPTConnection(&chatGPTTestVault{}, "fixture", nil, dir)
			if err := c.saveLocked(chatGPTCredentials{Access: "access", Refresh: "refresh", Account: "account", Expires: time.Now().Add(time.Hour).Unix()}); err == nil {
				t.Fatal("unsafe destination accepted")
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != "unchanged" {
				t.Fatal("unrelated file changed")
			}
		})
	}
}

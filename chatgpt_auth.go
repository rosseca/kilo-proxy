package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

// These are the subscription endpoints used by the public Codex clients. They
// are separate from the paid OpenAI API and never receive Kilo credentials.
const (
	chatGPTAuthURL         = "https://auth.openai.com"
	chatGPTBackendURL      = "https://chatgpt.com/backend-api"
	chatGPTResponsesURL    = chatGPTBackendURL + "/codex/responses"
	chatGPTVerificationURL = chatGPTAuthURL + "/codex/device"
	chatGPTClientID        = "app_EMoamEEZ73f0CkXaXp7hrann"
	// The models endpoint gates visibility on the client_version query, not
	// just the version header. Keep this compatibility version live-validated:
	// 0.159.0 exposes Sol 6.1, which the same account's 0.155.1 query omitted.
	chatGPTClientVersion = "0.159.0"
)

type chatGPTQuotaWindow struct {
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins *float64 `json:"windowDurationMins"`
	ResetsAt           *int64   `json:"resetsAt"`
}

type chatGPTQuota struct {
	Available bool                `json:"available"`
	FetchedAt string              `json:"fetchedAt,omitempty"`
	Stale     bool                `json:"stale"`
	Primary   *chatGPTQuotaWindow `json:"primary,omitempty"`
	Secondary *chatGPTQuotaWindow `json:"secondary,omitempty"`
	Error     string              `json:"error,omitempty"`
}

type chatGPTState struct {
	Connected       bool         `json:"connected"`
	Status          string       `json:"status"`
	Email           string       `json:"email,omitempty"`
	Plan            string       `json:"plan,omitempty"`
	VerificationURL string       `json:"verificationUrl,omitempty"`
	Code            string       `json:"code,omitempty"`
	Error           string       `json:"error,omitempty"`
	Quota           chatGPTQuota `json:"quota"`
}

type chatGPTCredentials struct {
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
	Account string `json:"account"`
	Email   string `json:"email,omitempty"`
	Plan    string `json:"plan,omitempty"`
	Expires int64  `json:"expires"`
}

type chatGPTConnection struct {
	mu               sync.Mutex
	vault            credentialVault
	vaultID          string
	client           *http.Client
	storageDir       string
	encryptionKey    []byte
	writeCredentials func(string, []byte) error
	// Endpoint overrides are private test fixtures, not user-configurable URLs.
	authURL       string
	backendURL    string
	pollDelay     time.Duration
	now           func() time.Time
	creds         chatGPTCredentials
	state         chatGPTState
	generation    uint64
	loginCancel   context.CancelFunc
	refreshCancel context.CancelFunc
	refreshFlight *chatGPTRefreshFlight
}

type chatGPTRefreshFlight struct {
	done chan struct{}
	err  error
}

func newChatGPTConnection(vault credentialVault, vaultID string, transport http.RoundTripper, dirs ...string) *chatGPTConnection {
	if transport == nil {
		transport = http.DefaultTransport
	}
	if tr, ok := transport.(*http.Transport); ok {
		clone := tr.Clone()
		clone.Proxy = nil
		transport = clone
	}
	c := &chatGPTConnection{vault: vault, vaultID: vaultID + "-chatgpt", authURL: chatGPTAuthURL, backendURL: chatGPTBackendURL, now: time.Now,
		client:           &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		state:            chatGPTState{Status: "idle"},
		writeCredentials: atomicCatalogFile,
	}
	if len(dirs) > 0 {
		c.storageDir = dirs[0]
	}
	if vault == nil {
		c.state.Status, c.state.Error = "error", "Could not open the ChatGPT credential store."
		return c
	}
	data, err := vault.Get(c.vaultID)
	if errors.Is(err, keyring.ErrNotFound) || (err == nil && data == "") {
		if c.storageDir != "" {
			if _, statErr := os.Lstat(filepath.Join(c.storageDir, "chatgpt-credentials.enc")); !errors.Is(statErr, os.ErrNotExist) {
				c.state.Status, c.state.Error = "error", "The saved ChatGPT encryption key is unavailable. Connect ChatGPT again."
			}
		}
		return c
	}
	if err != nil {
		c.state.Status, c.state.Error = "error", "Could not read saved ChatGPT credentials. Unlock the credential store and try again."
		return c
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(data, "v1:"))
	if !strings.HasPrefix(data, "v1:") || err != nil || len(key) != 32 {
		c.state.Status, c.state.Error = "error", "Saved ChatGPT credentials are invalid. Connect ChatGPT again."
		return c
	}
	c.encryptionKey = key
	path, err := c.credentialPath()
	if err != nil {
		c.state.Status, c.state.Error = "error", err.Error()
		return c
	}
	encrypted, err := readCatalogFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c
	}
	if err != nil {
		c.state.Status, c.state.Error = "error", "Could not read saved ChatGPT credentials. Check the configuration folder."
		return c
	}
	plain, err := c.openCredentials(encrypted)
	if err != nil || json.Unmarshal(plain, &c.creds) != nil || !validChatGPTCredentials(c.creds) {
		c.creds = chatGPTCredentials{}
		c.state.Status, c.state.Error = "error", "Saved ChatGPT credentials could not be decrypted. Connect ChatGPT again."
	}
	return c
}

func (c *chatGPTConnection) credentialPath() (string, error) {
	if !filepath.IsAbs(c.storageDir) || !validChatGPTText(c.storageDir, 4096) {
		return "", errors.New("The ChatGPT credential folder is unavailable.")
	}
	info, err := os.Lstat(c.storageDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("The ChatGPT credential folder must be a real directory.")
	}
	return filepath.Join(c.storageDir, "chatgpt-credentials.enc"), nil
}

const chatGPTCipherHeader = "KiloChatGPT1\n"

func (c *chatGPTConnection) credentialCipher() (cipher.AEAD, error) {
	block, err := aes.NewCipher(c.encryptionKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (c *chatGPTConnection) credentialAAD() []byte { return []byte(chatGPTCipherHeader + c.vaultID) }

func (c *chatGPTConnection) openCredentials(encrypted []byte) ([]byte, error) {
	gcm, err := c.credentialCipher()
	if err != nil {
		return nil, err
	}
	if len(encrypted) > 128<<10 || len(encrypted) < len(chatGPTCipherHeader)+gcm.NonceSize()+gcm.Overhead() || !bytes.HasPrefix(encrypted, []byte(chatGPTCipherHeader)) {
		return nil, errors.New("invalid encrypted credentials")
	}
	body := encrypted[len(chatGPTCipherHeader):]
	return gcm.Open(nil, body[:gcm.NonceSize()], body[gcm.NonceSize():], c.credentialAAD())
}

func validChatGPTText(s string, limit int) bool {
	return len(s) <= limit && strings.IndexFunc(s, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

func validChatGPTCredentials(c chatGPTCredentials) bool {
	return c.Access != "" && c.Refresh != "" && c.Account != "" && c.Expires > 0 &&
		validChatGPTText(c.Access, 64<<10) && validChatGPTText(c.Refresh, 16<<10) && validChatGPTText(c.Account, 256) &&
		validChatGPTText(c.Email, 512) && validChatGPTText(c.Plan, 128)
}

func (c *chatGPTConnection) snapshot() chatGPTState {
	if c == nil {
		return chatGPTState{Status: "idle"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state
	s.Connected = validChatGPTCredentials(c.creds)
	s.Email, s.Plan = c.creds.Email, c.creds.Plan
	// Quota values are immutable after assignment, but callers receive their own
	// copy so a UI cannot mutate connection state through a snapshot.
	s.Quota.Primary = cloneChatGPTQuotaWindow(s.Quota.Primary)
	s.Quota.Secondary = cloneChatGPTQuotaWindow(s.Quota.Secondary)
	if checked, err := time.Parse(time.RFC3339Nano, s.Quota.FetchedAt); err == nil && s.Quota.Available && c.now().Sub(checked) > 5*time.Minute {
		s.Quota.Stale, s.Quota.Available = true, false
		s.Quota.Error = "ChatGPT usage limits are out of date. Refresh usage."
	}
	return s
}

func cloneChatGPTQuotaWindow(w *chatGPTQuotaWindow) *chatGPTQuotaWindow {
	if w == nil {
		return nil
	}
	n := *w
	if w.UsedPercent != nil {
		value := *w.UsedPercent
		n.UsedPercent = &value
	}
	if w.WindowDurationMins != nil {
		value := *w.WindowDurationMins
		n.WindowDurationMins = &value
	}
	if w.ResetsAt != nil {
		value := *w.ResetsAt
		n.ResetsAt = &value
	}
	return &n
}

func (c *chatGPTConnection) identity() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.creds.Account
}

func (c *chatGPTConnection) begin(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Status == "pending" {
		return errors.New("ChatGPT sign-in is already pending.")
	}
	c.invalidateLocked()
	c.state.Status, c.state.Error = "pending", ""
	c.state.VerificationURL, c.state.Code = "", ""
	// The admin request ends immediately. Closing it must not cancel the user's
	// browser sign-in; cancel/logout explicitly end this bounded operation.
	loginCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	c.loginCancel = cancel
	gen := c.generation
	go func() { defer cancel(); c.deviceLogin(loginCtx, gen) }()
	return nil
}

func (c *chatGPTConnection) invalidateLocked() {
	c.generation++
	if c.loginCancel != nil {
		c.loginCancel()
		c.loginCancel = nil
	}
	if c.refreshCancel != nil {
		c.refreshCancel()
		c.refreshCancel = nil
	}
}

func (c *chatGPTConnection) cancel() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.invalidateLocked()
	c.state.Status, c.state.Error = "idle", ""
	c.state.VerificationURL, c.state.Code = "", ""
}

func (c *chatGPTConnection) logout() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.invalidateLocked()
	c.creds = chatGPTCredentials{}
	c.state = chatGPTState{Status: "idle"}
	var removeErr error
	if c.storageDir != "" {
		var path string
		path, removeErr = c.credentialPath()
		if removeErr == nil {
			info, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) { /* Already removed. */
			} else if err != nil || !info.Mode().IsRegular() {
				removeErr = errors.New("unsafe credential file")
			} else {
				removeErr = os.Remove(path)
			}
		}
	}
	c.encryptionKey = nil
	if c.vault == nil || c.vault.Delete(c.vaultID) != nil || removeErr != nil {
		c.state.Status, c.state.Error = "error", "Could not remove saved ChatGPT credentials. Unlock the credential store and disconnect again."
		return errors.New(c.state.Error)
	}
	return nil
}

func (c *chatGPTConnection) failLogin(gen uint64, message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != gen {
		return
	}
	if c.loginCancel != nil {
		c.loginCancel()
		c.loginCancel = nil
	}
	c.state.Status, c.state.Error = "error", message
	c.state.VerificationURL, c.state.Code = "", ""
}

func (c *chatGPTConnection) requestJSON(ctx context.Context, method, endpoint, contentType string, body []byte, token, account string, target any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("Could not prepare the ChatGPT request.")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Kilo-Proxy/"+version)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("ChatGPT-Account-Id", account)
		req.Header.Set("OpenAI-Beta", "responses=experimental")
		req.Header.Set("originator", "kilo-proxy")
		req.Header.Set("version", chatGPTClientVersion)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, errors.New("Could not connect to ChatGPT. Check the network and try again.")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("ChatGPT returned HTTP %d.", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 || json.Unmarshal(data, target) != nil {
		return resp.StatusCode, errors.New("ChatGPT returned an invalid response.")
	}
	return resp.StatusCode, nil
}

func (c *chatGPTConnection) deviceLogin(ctx context.Context, gen uint64) {
	var device struct {
		ID       string          `json:"device_auth_id"`
		Code     string          `json:"user_code"`
		Interval json.RawMessage `json:"interval"`
	}
	body, _ := json.Marshal(map[string]string{"client_id": chatGPTClientID})
	_, err := c.requestJSON(ctx, "POST", c.authURL+"/api/accounts/deviceauth/usercode", "application/json", body, "", "", &device)
	if err != nil || device.ID == "" || device.Code == "" || !validChatGPTText(device.Code, 128) || !validChatGPTText(device.ID, 8192) {
		c.failLogin(gen, "Could not start ChatGPT sign-in. Try again.")
		return
	}
	delay := 5 * time.Second
	if value := strings.Trim(string(device.Interval), "\""); value != "" {
		if n, err := strconv.ParseFloat(value, 64); err == nil && n >= 1 && n <= 60 {
			delay = time.Duration(n * float64(time.Second))
		}
	}
	delay += 3 * time.Second
	if c.pollDelay > 0 {
		delay = c.pollDelay
	}
	c.mu.Lock()
	if gen != c.generation {
		c.mu.Unlock()
		return
	}
	c.state.VerificationURL, c.state.Code = chatGPTVerificationURL, device.Code
	c.mu.Unlock()
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			c.failLogin(gen, "ChatGPT sign-in expired. Try again.")
			return
		case <-timer.C:
		}
		var poll struct {
			Code     string `json:"authorization_code"`
			Verifier string `json:"code_verifier"`
		}
		body, _ = json.Marshal(map[string]string{"device_auth_id": device.ID, "user_code": device.Code})
		status, err := c.requestJSON(ctx, "POST", c.authURL+"/api/accounts/deviceauth/token", "application/json", body, "", "", &poll)
		if status == 403 || status == 404 {
			continue
		}
		if status == 429 {
			delay = min(delay+5*time.Second, time.Minute)
			continue
		}
		if err != nil || poll.Code == "" || poll.Verifier == "" || !validChatGPTText(poll.Code, 8192) || !validChatGPTText(poll.Verifier, 8192) {
			c.failLogin(gen, "ChatGPT sign-in failed. Try again.")
			return
		}
		creds, err := c.exchange(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {poll.Code}, "code_verifier": {poll.Verifier}, "redirect_uri": {chatGPTAuthURL + "/deviceauth/callback"}}, chatGPTCredentials{})
		if err != nil {
			c.failLogin(gen, err.Error())
			return
		}
		c.mu.Lock()
		if gen != c.generation {
			c.mu.Unlock()
			return
		}
		if err = c.saveLocked(creds); err != nil {
			c.mu.Unlock()
			c.failLogin(gen, err.Error())
			return
		}
		c.state = chatGPTState{Status: "idle"}
		c.loginCancel = nil
		c.mu.Unlock()
		// Quota failure is nonfatal and never changes a successful login.
		quotaCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		_ = c.refresh(quotaCtx)
		cancel()
		return
	}
}

func (c *chatGPTConnection) exchange(ctx context.Context, form url.Values, previous chatGPTCredentials) (chatGPTCredentials, error) {
	form.Set("client_id", chatGPTClientID)
	var response struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		ID      string `json:"id_token"`
		Expires int64  `json:"expires_in"`
	}
	_, err := c.requestJSON(ctx, "POST", c.authURL+"/oauth/token", "application/x-www-form-urlencoded", []byte(form.Encode()), "", "", &response)
	if err != nil {
		return chatGPTCredentials{}, errors.New("Could not refresh ChatGPT authentication. Connect ChatGPT again.")
	}
	if response.Refresh == "" {
		response.Refresh = previous.Refresh
	}
	if response.Expires <= 0 || response.Expires > 366*24*3600 {
		return chatGPTCredentials{}, errors.New("ChatGPT returned invalid authentication. Connect ChatGPT again.")
	}
	creds := chatGPTCredentials{Access: response.Access, Refresh: response.Refresh, Expires: c.now().Add(time.Duration(response.Expires) * time.Second).Unix()}
	creds.Account, creds.Email, creds.Plan = chatGPTIdentity(response.Access, response.ID)
	if creds.Account == "" {
		creds.Account = previous.Account
	}
	if previous.Account != "" && creds.Account != previous.Account {
		return chatGPTCredentials{}, errors.New("ChatGPT account changed during authentication. Connect ChatGPT again.")
	}
	if creds.Email == "" {
		creds.Email = previous.Email
	}
	if creds.Plan == "" {
		creds.Plan = previous.Plan
	}
	if !validChatGPTCredentials(creds) {
		return chatGPTCredentials{}, errors.New("ChatGPT returned invalid authentication. Connect ChatGPT again.")
	}
	return creds, nil
}

func chatGPTIdentity(tokens ...string) (account, email, plan string) {
	for _, token := range tokens {
		parts := strings.Split(token, ".")
		if len(parts) != 3 || len(parts[1]) > 128<<10 {
			continue
		}
		data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
		if err != nil {
			continue
		}
		var claims struct {
			Account string `json:"chatgpt_account_id"`
			Email   string `json:"email"`
			Auth    struct {
				Account string `json:"chatgpt_account_id"`
				Plan    string `json:"chatgpt_plan_type"`
			} `json:"https://api.openai.com/auth"`
			Profile struct {
				Email string `json:"email"`
			} `json:"https://api.openai.com/profile"`
		}
		if json.Unmarshal(data, &claims) != nil {
			continue
		}
		if account == "" {
			account = claims.Auth.Account
			if account == "" {
				account = claims.Account
			}
		}
		if email == "" {
			email = claims.Profile.Email
			if email == "" {
				email = claims.Email
			}
		}
		if plan == "" {
			plan = claims.Auth.Plan
		}
	}
	return
}

func (c *chatGPTConnection) saveLocked(creds chatGPTCredentials) error {
	path, err := c.credentialPath()
	if err != nil {
		return err
	}
	data, _ := json.Marshal(creds)
	if len(c.encryptionKey) == 0 {
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return errors.New("Could not create the ChatGPT encryption key.")
		}
		if c.vault == nil || c.vault.Set(c.vaultID, "v1:"+base64.StdEncoding.EncodeToString(key)) != nil {
			return errors.New("Could not save the ChatGPT encryption key. Unlock the credential store and connect again.")
		}
		c.encryptionKey = key
	}
	gcm, err := c.credentialCipher()
	if err != nil {
		return errors.New("Could not encrypt ChatGPT credentials.")
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return errors.New("Could not encrypt ChatGPT credentials.")
	}
	encrypted := append([]byte(chatGPTCipherHeader), nonce...)
	encrypted = gcm.Seal(encrypted, nonce, data, c.credentialAAD())
	// Only authenticated ciphertext reaches the atomic writer or its 0600
	// temporary file. The small random key stays in the system credential store,
	// avoiding platform limits on storing complete OAuth JWTs in that store.
	if c.writeCredentials(path, encrypted) != nil {
		return errors.New("Could not save encrypted ChatGPT credentials. Check the configuration folder.")
	}
	c.creds = creds
	return nil
}

func (c *chatGPTConnection) credentials(ctx context.Context) (string, string, error) {
	if c == nil {
		return "", "", errors.New("Connect ChatGPT first.")
	}
	for {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		c.mu.Lock()
		if !validChatGPTCredentials(c.creds) {
			c.mu.Unlock()
			return "", "", errors.New("Connect ChatGPT first.")
		}
		if c.creds.Expires > c.now().Add(time.Minute).Unix() {
			token, account := c.creds.Access, c.creds.Account
			c.mu.Unlock()
			return token, account, nil
		}
		flight := c.refreshFlight
		if flight == nil {
			flight = &chatGPTRefreshFlight{done: make(chan struct{})}
			c.refreshFlight = flight
			refreshCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			c.refreshCancel = cancel
			go c.refreshCredentials(refreshCtx, cancel, c.generation, c.creds, flight)
		}
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-flight.done:
		}
		if flight.err != nil {
			return "", "", flight.err
		}
	}
}

func (c *chatGPTConnection) refreshCredentials(ctx context.Context, cancel context.CancelFunc, gen uint64, old chatGPTCredentials, flight *chatGPTRefreshFlight) {
	defer cancel()
	creds, err := c.exchange(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old.Refresh}}, old)
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.generation {
		err = errors.New("ChatGPT connection changed. Try again.")
	} else {
		if err == nil {
			err = c.saveLocked(creds)
		}
		if err != nil {
			c.state.Status, c.state.Error = "error", err.Error()
		} else if c.state.Status != "pending" {
			c.state.Status, c.state.Error = "idle", ""
		}
	}
	flight.err = err
	c.refreshFlight, c.refreshCancel = nil, nil
	close(flight.done)
}

func (c *chatGPTConnection) refresh(ctx context.Context) error {
	if c == nil {
		return errors.New("Connect ChatGPT first.")
	}
	c.mu.Lock()
	gen := c.generation
	c.mu.Unlock()
	token, account, err := c.credentials(ctx)
	if err != nil {
		return err
	}
	var raw struct {
		Plan   string `json:"plan_type"`
		Limits struct {
			Primary   *chatGPTRawQuotaWindow `json:"primary_window"`
			Secondary *chatGPTRawQuotaWindow `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	_, err = c.requestJSON(ctx, "GET", c.backendURL+"/wham/usage", "", nil, token, account, &raw)
	quota := chatGPTQuota{}
	if err == nil {
		quota.FetchedAt = c.now().UTC().Format(time.RFC3339Nano)
		quota.Primary, quota.Secondary = chatGPTParseQuotaWindow(raw.Limits.Primary, c.now()), chatGPTParseQuotaWindow(raw.Limits.Secondary, c.now())
		quota.Available = quota.Primary != nil || quota.Secondary != nil
	}
	if !quota.Available {
		quota.Error = "ChatGPT usage limits are unavailable."
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.generation || c.creds.Account != account {
		return errors.New("ChatGPT connection changed. Try again.")
	}
	c.state.Quota = quota
	if err == nil && raw.Plan != "" && validChatGPTText(raw.Plan, 128) {
		c.creds.Plan = raw.Plan
	}
	return err
}

type chatGPTRawQuotaWindow struct {
	Used    *float64 `json:"used_percent"`
	Seconds *float64 `json:"limit_window_seconds"`
	Reset   *int64   `json:"reset_at"`
	After   *int64   `json:"reset_after_seconds"`
}

func chatGPTParseQuotaWindow(raw *chatGPTRawQuotaWindow, now time.Time) *chatGPTQuotaWindow {
	if raw == nil {
		return nil
	}
	w := &chatGPTQuotaWindow{}
	if raw.Used != nil && !math.IsNaN(*raw.Used) && !math.IsInf(*raw.Used, 0) && *raw.Used >= 0 {
		value := *raw.Used
		w.UsedPercent = &value
	}
	if raw.Seconds != nil && !math.IsNaN(*raw.Seconds) && !math.IsInf(*raw.Seconds, 0) && *raw.Seconds > 0 {
		value := *raw.Seconds / 60
		w.WindowDurationMins = &value
	}
	if raw.Reset != nil && *raw.Reset > 0 {
		value := *raw.Reset
		w.ResetsAt = &value
	} else if raw.After != nil && *raw.After >= 0 && *raw.After < 366*24*3600 {
		value := now.Unix() + *raw.After
		w.ResetsAt = &value
	}
	if w.UsedPercent == nil && w.WindowDurationMins == nil && w.ResetsAt == nil {
		return nil
	}
	return w
}

func (c *chatGPTConnection) models(ctx context.Context) ([]modelInfo, error) {
	if c == nil {
		return nil, errors.New("Connect ChatGPT first.")
	}
	c.mu.Lock()
	gen := c.generation
	c.mu.Unlock()
	token, account, err := c.credentials(ctx)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Models []chatGPTRawModel `json:"models"`
		Data   []chatGPTRawModel `json:"data"`
	}
	_, err = c.requestJSON(ctx, "GET", c.backendURL+"/codex/models?client_version="+chatGPTClientVersion, "", nil, token, account, &raw)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	changed := gen != c.generation || account != c.creds.Account
	c.mu.Unlock()
	if changed {
		return nil, errors.New("ChatGPT connection changed. Try again.")
	}
	if raw.Models == nil {
		raw.Models = raw.Data
	}
	models := make([]modelInfo, 0, len(raw.Models))
	seen := map[string]bool{}
	for _, m := range raw.Models {
		id := m.Slug
		if id == "" {
			id = m.ID
		}
		if id == "" || !catalogID.MatchString("chatgpt/"+id) || strings.Contains(id, "/") || seen[id] || m.Visibility == "hide" || m.Visibility == "hidden" {
			continue
		}
		seen[id] = true
		name := m.Name
		if name == "" || !validChatGPTText(name, 512) {
			name = id
		}
		model := modelInfo{ID: "chatgpt/" + id, Name: name + " · ChatGPT", Provider: "OpenAI", ContextWindow: m.Context, MaxOutputTokens: m.MaxOutput, InputModalities: m.Input, OutputModalities: []string{"text"}}
		if model.ContextWindow < 0 {
			model.ContextWindow = 0
		}
		if model.MaxOutputTokens < 0 {
			model.MaxOutputTokens = 0
		}
		for _, level := range m.Efforts {
			if effortNames[level.Effort] {
				found := false
				for _, old := range model.ReasoningEfforts {
					found = found || old == level.Effort
				}
				if !found {
					model.ReasoningEfforts = append(model.ReasoningEfforts, level.Effort)
				}
			}
		}
		reasoning := len(model.ReasoningEfforts) > 0 || (m.DefaultEffort != "" && m.DefaultEffort != "none")
		model.Reasoning = &reasoning
		// tool_mode describes the Codex client's tool orchestration, not
		// subscription access or Responses function-tool compatibility.
		// Keep visible account models available to each client's own tools.
		tools := true
		model.Tools = &tools
		models = append(models, model)
	}
	if len(models) == 0 {
		return nil, errors.New("ChatGPT did not return any compatible models for this account.")
	}
	return models, nil
}

type chatGPTRawModel struct {
	Slug          string   `json:"slug"`
	ID            string   `json:"id"`
	Name          string   `json:"display_name"`
	Visibility    string   `json:"visibility"`
	Context       int      `json:"context_window"`
	MaxOutput     int      `json:"max_output_tokens"`
	Input         []string `json:"input_modalities"`
	DefaultEffort string   `json:"default_reasoning_level"`
	Efforts       []struct {
		Effort string `json:"effort"`
	} `json:"supported_reasoning_levels"`
	ToolMode string `json:"tool_mode"`
}

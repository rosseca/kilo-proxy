package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	liteLLMLoginEndpoint       = "/api/providers/litellm/login"
	liteLLMLoginCancelEndpoint = "/api/providers/litellm/login/cancel"
	liteLLMLogoutEndpoint      = "/api/providers/litellm/logout"
	liteLLMLoginLifetime       = 5 * time.Minute
)

type liteLLMLoginView struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type liteLLMLoginAttempt struct {
	ctx             context.Context
	cancel          context.CancelFunc
	base            *url.URL
	listener        net.Listener
	callback        string
	port            int
	state, verifier string
	metadata        liteLLMOAuthMetadata
	clientID        string
	handled         atomic.Bool
}

func (a *app) liteLLMLoginViewLocked() liteLLMLoginView {
	if a.liteLLMLoginState.Status == "" {
		return liteLLMLoginView{Status: "idle"}
	}
	return a.liteLLMLoginState
}

func (a *app) finishLiteLLMLogin(flow *liteLLMLoginAttempt, status, message string) {
	a.mu.Lock()
	if a.liteLLMLogin == flow {
		a.liteLLMLogin = nil
		a.liteLLMLoginState = liteLLMLoginView{Status: status, Error: message}
	}
	a.mu.Unlock()
	flow.cancel()
}

func (a *app) startLiteLLMLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL string `json:"url"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	if _, fileVault := a.vault.(headlessVault); a.headless || fileVault {
		jsonError(w, http.StatusNotImplemented, "LiteLLM browser sign-in is available in the desktop app.")
		return
	}
	base, err := normalizeLiteLLMURL(input.URL)
	if err == nil {
		err = validateLiteLLMBrowserTransport(base)
	}
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.liteLLMUpdate.TryLock() {
		jsonError(w, http.StatusConflict, "A LiteLLM connection is already being checked.")
		return
	}
	defer a.liteLLMUpdate.Unlock()

	state, stateErr := liteLLMRandomOAuthValue()
	verifier, verifierErr := liteLLMRandomOAuthValue()
	if stateErr != nil || verifierErr != nil {
		jsonError(w, http.StatusInternalServerError, "Could not prepare LiteLLM browser sign-in.")
		return
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Could not open the local LiteLLM sign-in callback.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), liteLLMLoginLifetime)
	flow := &liteLLMLoginAttempt{
		ctx: ctx, cancel: cancel, base: base, listener: listener,
		callback: "http://" + listener.Addr().String() + "/callback", state: state, verifier: verifier,
	}
	a.mu.Lock()
	if a.liteLLMLogin != nil {
		a.mu.Unlock()
		cancel()
		_ = listener.Close()
		jsonError(w, http.StatusConflict, "A LiteLLM browser sign-in is already pending.")
		return
	}
	flow.port = a.config.Port
	a.liteLLMLogin = flow
	a.liteLLMLoginState = liteLLMLoginView{Status: "pending"}
	a.mu.Unlock()

	// Discovery is part of this HTTP request. The callback and its deadline
	// belong to the login attempt, which outlives the request starting it.
	preparing, stopPreparing := context.WithCancel(ctx)
	defer stopPreparing()
	stopOnRequest := context.AfterFunc(r.Context(), stopPreparing)
	defer stopOnRequest()
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 8 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8 << 10,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.liteLLMOAuthCallback(flow, w, r) }),
	}
	go func() {
		select {
		case <-a.quit:
			a.finishLiteLLMLogin(flow, "idle", "")
		case <-ctx.Done():
			a.finishLiteLLMLogin(flow, "error", "LiteLLM browser sign-in expired. Try again.")
		}
		_ = listener.Close()
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := server.Shutdown(closeCtx); err != nil {
			_ = server.Close()
		}
	}()

	flow.metadata, err = a.discoverLiteLLMOAuth(preparing, base)
	if err == nil {
		flow.clientID, err = a.registerLiteLLMOAuth(preparing, flow.metadata, flow.callback)
	}
	if err == nil && preparing.Err() != nil {
		err = errLiteLLMOAuthDiscover
	}
	if err != nil {
		a.finishLiteLLMLogin(flow, "error", err.Error())
		jsonError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	authorization, _ := url.Parse(flow.metadata.AuthorizationURL)
	challenge := sha256.Sum256([]byte(flow.verifier))
	authorization.RawQuery = url.Values{
		"response_type": {"code"}, "client_id": {flow.clientID},
		"redirect_uri": {flow.callback}, "state": {flow.state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"}, "resource": {flow.metadata.Resource},
	}.Encode()

	a.mu.Lock()
	// A concurrent cancel can close the listener during discovery.
	if a.liteLLMLogin != flow || ctx.Err() != nil {
		a.mu.Unlock()
		jsonError(w, http.StatusConflict, "LiteLLM browser sign-in is no longer pending.")
		return
	}
	go func() { _ = server.Serve(listener) }()
	jsonResponse(w, http.StatusOK, map[string]any{
		"authorizationURL": authorization.String(), "litellmLogin": a.liteLLMLoginViewLocked(),
	})
	a.mu.Unlock()
}

func (a *app) liteLLMOAuthCallback(flow *liteLLMLoginAttempt, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	if r.Method != http.MethodGet || r.URL.Path != "/callback" || r.URL.RawPath != "" || r.Host != flow.listener.Addr().String() {
		http.NotFound(w, r)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	state, code, denied := query["state"], query["code"], query["error"]
	if err != nil || len(state) != 1 || !secureEqual(state[0], flow.state) ||
		!(len(code) == 1 && code[0] != "" && len(code[0]) <= 4096 && len(denied) == 0 ||
			len(code) == 0 && len(denied) == 1 && denied[0] != "") {
		http.Error(w, "Invalid LiteLLM sign-in callback.", http.StatusBadRequest)
		return
	}
	if !flow.handled.CompareAndSwap(false, true) || flow.ctx.Err() != nil {
		http.Error(w, "This LiteLLM browser sign-in is no longer pending.", http.StatusConflict)
		return
	}
	if len(denied) == 1 {
		const message = "LiteLLM browser sign-in was denied or cancelled."
		a.finishLiteLLMLogin(flow, "error", message)
		http.Error(w, message, http.StatusBadRequest)
		return
	}

	if err := a.completeLiteLLMLogin(flow, code[0]); err != nil {
		a.finishLiteLLMLogin(flow, "error", err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.finishLiteLLMLogin(flow, "success", "")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("Signed in to LiteLLM. You can close this window and return to Kilo Proxy."))
}

func (a *app) completeLiteLLMLogin(flow *liteLLMLoginAttempt, code string) error {
	a.liteLLMUpdate.Lock()
	defer a.liteLLMUpdate.Unlock()
	a.mu.Lock()
	valid := a.liteLLMLogin == flow && a.config.Port == flow.port
	a.mu.Unlock()
	if !valid || flow.ctx.Err() != nil {
		return errLiteLLMOAuthExchange
	}
	if err := a.checkLiteLLMTarget(flow.ctx, flow.base); err != nil {
		return err
	}
	proposed := liteLLMBrowserCredential{
		ClientID: flow.clientID, TokenURL: flow.metadata.TokenURL, RevocationURL: flow.metadata.RevocationURL, Resource: flow.metadata.Resource,
	}
	credential, err := a.exchangeLiteLLMOAuth(flow.ctx, proposed, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {flow.callback}, "code_verifier": {flow.verifier},
	})
	if err != nil {
		return errLiteLLMOAuthExchange
	}
	committed := false
	defer func() {
		if !committed {
			a.revokeReplacedLiteLLMCredential(flow.base.String(), &credential)
		}
	}()
	models, err := a.fetchLiteLLMModels(flow.ctx, liteLLMConnection{Base: flow.base, Key: credential.Access})
	if err != nil {
		return errors.New("LiteLLM signed in, but its model catalog could not be verified. Check your gateway and team permissions.")
	}

	a.mu.Lock()
	if a.liteLLMLogin != flow || flow.ctx.Err() != nil || a.config.Port != flow.port {
		a.mu.Unlock()
		return errLiteLLMOAuthExchange
	}
	profile, previous := a.config.VaultID, a.config.LiteLLM
	previousBrowser := a.liteLLMBrowser
	if a.liteLLMUnstored != nil {
		previousBrowser = a.liteLLMUnstored
	}
	a.mu.Unlock()
	enable := previous.URL == "" || previous.Enabled || previous.AuthMode == liteLLMBrowserMode && previous.VaultRef == ""
	newConfig := liteLLMSettings{URL: flow.base.String(), Enabled: enable, AuthMode: liteLLMBrowserMode, VaultRef: randomKey("")}
	if err := a.saveLiteLLMBrowserCredential(profile, newConfig, credential); err != nil {
		return err
	}
	defer func() {
		if !committed {
			_ = a.deleteLiteLLMStoredCredential(profile, newConfig)
		}
	}()
	a.mu.Lock()
	if a.liteLLMLogin != flow || flow.ctx.Err() != nil || a.config.Port != flow.port || a.config.VaultID != profile {
		a.mu.Unlock()
		return errLiteLLMOAuthExchange
	}
	next := a.config
	next.LiteLLM = newConfig
	if err := writeSettings(a.dir, next); err != nil {
		a.mu.Unlock()
		return errors.New("Could not save the LiteLLM settings.")
	}
	a.config, a.liteLLMBrowser, a.liteLLMKey = next, &credential, ""
	a.liteLLMUnstored = nil
	a.liteLLMRefreshRetryAt = time.Time{}
	a.liteLLMModelsCount = len(models)
	a.catalogRevision++
	a.catalogWarnings = nil
	a.liteLLMLogin = nil
	a.liteLLMLoginState = liteLLMLoginView{Status: "success"}
	a.publishUIProjectionLocked()
	a.mu.Unlock()
	committed = true
	_ = a.deleteLiteLLMStoredCredential(profile, previous)
	if previousBrowser != nil && previousBrowser.Refresh != credential.Refresh {
		a.revokeReplacedLiteLLMCredential(previous.URL, previousBrowser)
	}
	return nil
}

func (a *app) revokeReplacedLiteLLMCredential(raw string, previous *liteLLMBrowserCredential) {
	if previous == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		base, err := url.Parse(raw)
		if err == nil && a.checkLiteLLMTarget(ctx, base) == nil {
			_ = a.revokeLiteLLMOAuth(ctx, *previous)
		}
	}()
}

func (a *app) cancelLiteLLMLoginHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if flow := a.liteLLMLogin; flow != nil {
		a.liteLLMLogin = nil
		a.liteLLMLoginState = liteLLMLoginView{Status: "idle"}
		flow.cancel()
	}
	a.writeConnectionViewLocked(w)
}

func (a *app) logoutLiteLLMHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	if !a.liteLLMUpdate.TryLock() {
		jsonError(w, http.StatusConflict, "A LiteLLM connection is already being checked.")
		return
	}
	defer a.liteLLMUpdate.Unlock()
	a.mu.Lock()
	cfg, profile := a.config.LiteLLM, a.config.VaultID
	credential := a.liteLLMBrowser
	if a.liteLLMUnstored != nil {
		credential = a.liteLLMUnstored
	}
	if a.liteLLMLogin != nil {
		a.mu.Unlock()
		jsonError(w, http.StatusConflict, "Cancel LiteLLM browser sign-in before signing out.")
		return
	}
	if cfg.AuthMode != liteLLMBrowserMode || cfg.VaultRef == "" {
		a.writeConnectionViewLocked(w)
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	if credential == nil {
		credential = a.loadLiteLLMBrowserCredential(profile, cfg)
		if staged := a.loadLiteLLMBrowserCredential(profile, stagedLiteLLMBrowserSettings(cfg)); staged != nil {
			credential = staged
		}
		if credential == nil {
			_, err := a.vault.Get(liteLLMBrowserVaultSlot(profile, cfg))
			if !errors.Is(err, keyring.ErrNotFound) {
				jsonError(w, http.StatusConflict, "Unlock the credential store before signing out of LiteLLM.")
				return
			}
		}
	}
	if credential != nil {
		base, err := url.Parse(cfg.URL)
		if err != nil || a.checkLiteLLMTarget(r.Context(), base) != nil || a.revokeLiteLLMOAuth(r.Context(), *credential) != nil {
			jsonError(w, http.StatusBadGateway, errLiteLLMOAuthRevoke.Error())
			return
		}
	}

	a.mu.Lock()
	next := a.config
	next.LiteLLM.Enabled, next.LiteLLM.VaultRef = false, ""
	settingsErr := writeSettings(a.dir, next)
	if settingsErr == nil {
		a.config = next
	}
	// After successful revocation, disable the runtime credential even if
	// settings cannot be saved. Remove its vault records in either case.
	a.liteLLMBrowser, a.liteLLMUnstored = nil, nil
	a.liteLLMModelsCount = 0
	a.liteLLMRefreshRetryAt = time.Time{}
	a.liteLLMLoginState = liteLLMLoginView{Status: "idle"}
	a.catalogRevision++
	a.catalogWarnings = nil
	a.publishUIProjectionLocked()
	a.mu.Unlock()
	vaultErr := a.deleteLiteLLMStoredCredential(profile, cfg)
	a.mu.Lock()
	defer a.mu.Unlock()
	message := ""
	switch {
	case settingsErr != nil:
		message = "Signed out of LiteLLM, but its local settings could not be saved."
	case vaultErr != nil:
		message = "Signed out of LiteLLM, but its old credential could not be removed from the credential store."
	}
	if message != "" {
		a.liteLLMLoginState = liteLLMLoginView{Status: "error", Error: message}
		jsonError(w, http.StatusInternalServerError, message)
		return
	}
	a.writeConnectionViewLocked(w)
}

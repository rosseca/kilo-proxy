package main

import (
	"context"
	"net/url"
	"time"
)

type liteLLMRefreshFlight struct {
	session string
	done    chan struct{}
}

// Recheck the identity after any wait. A request must not borrow credentials
// from a different sign-in while a catalog operation or refresh is in flight.
func (a *app) liteLLMRequestCredential(ctx context.Context, expected liteLLMConnection) (liteLLMConnection, error) {
	if expected.Session == "" {
		return expected, nil
	}
	for {
		a.mu.Lock()
		current, ready := a.liteLLMConnectionLocked()
		browser := a.liteLLMBrowser
		if !ready || current.Session != expected.Session || current.Base.String() != expected.Base.String() || browser == nil {
			a.mu.Unlock()
			return liteLLMConnection{}, &catalogError{409, "The LiteLLM connection changed. Try again."}
		}
		now := time.Now()
		if now.Before(browser.RefreshAfter) && a.liteLLMUnstored == nil {
			a.mu.Unlock()
			return current, nil
		}
		if now.Before(a.liteLLMRefreshRetryAt) {
			a.mu.Unlock()
			if now.Before(browser.ExpiresAt) {
				return current, nil
			}
			return liteLLMConnection{}, errLiteLLMOAuthRenew
		}
		flight := a.liteLLMRefresh
		if flight == nil || flight.session != expected.Session {
			flight = &liteLLMRefreshFlight{session: expected.Session, done: make(chan struct{})}
			a.liteLLMRefresh = flight
			go a.runLiteLLMRefresh(expected, flight)
		}
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return liteLLMConnection{}, ctx.Err()
		case <-flight.done:
		}
	}
}

func (a *app) runLiteLLMRefresh(expected liteLLMConnection, flight *liteLLMRefreshFlight) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*liteLLMOAuthRequestTimeout)
	defer cancel()
	go func() {
		select {
		case <-a.quit:
			cancel()
		case <-ctx.Done():
		}
	}()
	a.liteLLMUpdate.Lock()
	defer a.liteLLMUpdate.Unlock()
	err := a.renewLiteLLMOAuth(ctx, expected)
	a.mu.Lock()
	if a.liteLLMRefresh == flight {
		if err != nil && a.config.LiteLLM.VaultRef == expected.Session {
			a.liteLLMRefreshRetryAt = time.Now().Add(5 * time.Second)
			if browser := a.liteLLMBrowser; browser != nil && !time.Now().Before(browser.ExpiresAt) && a.liteLLMLogin == nil {
				a.liteLLMLoginState = liteLLMLoginView{Status: "error", Error: errLiteLLMOAuthRenew.Error()}
			}
		}
		a.liteLLMRefresh = nil
	}
	close(flight.done)
	a.mu.Unlock()
}

func (a *app) renewLiteLLMOAuth(ctx context.Context, expected liteLLMConnection) error {
	a.mu.Lock()
	cfg, profile, old := a.config.LiteLLM, a.config.VaultID, a.liteLLMBrowser
	pending := a.liteLLMUnstored
	a.mu.Unlock()
	if old == nil || !cfg.Enabled || cfg.VaultRef != expected.Session || cfg.URL != expected.Base.String() {
		return nil
	}
	if err := a.checkLiteLLMTarget(ctx, expected.Base); err != nil {
		return err
	}
	staged := stagedLiteLLMBrowserSettings(cfg)
	if pending == nil || !time.Now().Before(pending.ExpiresAt) {
		rotation := old
		if pending != nil {
			rotation = pending
		} else if time.Now().Before(old.RefreshAfter) {
			return nil
		}
		credential, err := a.exchangeLiteLLMOAuth(ctx, *rotation, url.Values{
			"grant_type": {"refresh_token"}, "refresh_token": {rotation.Refresh},
		})
		if err != nil {
			return err
		}
		pending = &credential
		// Stage the pair so a restart can recover it even if the catalog
		// check fails. It is usable only after a successful check and commit.
		a.mu.Lock()
		a.liteLLMUnstored = pending
		a.mu.Unlock()
	}
	if err := a.saveLiteLLMBrowserCredential(profile, staged, *pending); err != nil {
		return err
	}
	models, err := a.fetchLiteLLMModels(ctx, liteLLMConnection{Base: expected.Base, Key: pending.Access})
	if err != nil {
		return err
	}
	if err := a.saveLiteLLMBrowserCredential(profile, cfg, *pending); err != nil {
		return err
	}
	a.mu.Lock()
	a.liteLLMBrowser, a.liteLLMUnstored = pending, nil
	a.liteLLMModelsCount = len(models)
	if a.liteLLMLogin == nil && a.liteLLMLoginState.Error == errLiteLLMOAuthRenew.Error() {
		a.liteLLMLoginState = liteLLMLoginView{Status: "success"}
	}
	a.mu.Unlock()
	_ = a.deleteLiteLLMBrowserVaultSlot(profile, staged)
	return nil
}

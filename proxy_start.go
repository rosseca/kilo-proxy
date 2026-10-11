package main

import (
	"errors"
	"net/http"
)

var (
	errProxyClosing                = errors.New("application is closing")
	errProxyAuthenticationChanging = errors.New("provider authentication is changing")
	errProxyLoginPending           = errors.New("login pending")
)

type proxyStartFailure struct {
	Message    string                      `json:"message"`
	Type       string                      `json:"type"`
	Code       string                      `json:"code"`
	Diagnostic *proxyListenErrorDiagnostic `json:"diagnostic,omitempty"`
}

// Only listener errors carry system diagnostics. Errors from account setup or
// process launches may contain credentials and must never be surfaced here.
func describeProxyStartFailure(err error, language string) proxyStartFailure {
	failure := proxyStartFailure{Type: "kilo_local_error", Code: "proxy_start_failed"}
	if message, ok := proxyListenErrorMessage(err, language); ok {
		failure.Message = message
		failure.Diagnostic = proxyListenErrorDetails(err)
		failure.Code = "proxy_" + string(failure.Diagnostic.Kind)
		return failure
	}
	message := "Could not start the proxy. Try again; if it persists, restart Kilo Proxy."
	switch {
	case errors.Is(err, errMissingCredentials):
		failure.Code = "proxy_credentials_missing"
		message = "Connect Kilo or ChatGPT in Settings before starting the proxy."
	case errors.Is(err, errProxyLoginPending):
		failure.Code = "proxy_login_pending"
		message = "Finish signing in before starting the proxy."
	case errors.Is(err, errProxyAuthenticationChanging):
		failure.Code = "proxy_authentication_changing"
		message = "Wait for the account connection to finish changing, then start the proxy."
	case errors.Is(err, errProxyClosing):
		failure.Code = "proxy_closing"
		message = "Kilo Proxy is closing. Open it again to start the proxy."
	}
	failure.Message = nativeMessage(message, language)
	return failure
}

func (a *app) writeProxyStartError(w http.ResponseWriter, err error) {
	a.mu.Lock()
	language := a.config.Language
	a.mu.Unlock()
	failure := describeProxyStartFailure(err, language)
	status := http.StatusConflict
	if failure.Code == "proxy_credentials_missing" {
		status = http.StatusBadRequest
	} else if failure.Code == "proxy_start_failed" {
		status = http.StatusInternalServerError
	}
	jsonResponse(w, status, map[string]any{"error": failure})
}

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	liteLLMSettingsEndpoint = "/api/providers/litellm"
	liteLLMActiveEndpoint   = "/api/providers/litellm/active"
	kiloActiveEndpoint      = "/api/providers/kilo/active"
)

type liteLLMSettings struct {
	URL      string `json:"url"`
	Enabled  bool   `json:"enabled"`
	VaultRef string `json:"vaultRef,omitempty"`
}

type liteLLMConnection struct {
	Base *url.URL
	Key  string
}

type liteLLMView struct {
	URL         string `json:"url"`
	Enabled     bool   `json:"enabled"`
	Ready       bool   `json:"ready"`
	HasKey      bool   `json:"hasKey"`
	KeySaved    bool   `json:"keySaved"`
	ModelsCount int    `json:"modelsCount"`
}

var errLiteLLMURL = errors.New("Enter a valid HTTP or HTTPS URL for the LiteLLM API.")

func normalizeLiteLLMURL(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if len(value) > 2048 {
		return nil, errLiteLLMURL
	}
	base, err := url.Parse(value)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") ||
		base.Hostname() == "" || base.Opaque != "" || base.User != nil || base.RawQuery != "" ||
		base.ForceQuery || strings.Contains(value, "#") || base.RawPath != "" {
		return nil, errLiteLLMURL
	}
	host := strings.ToLower(base.Hostname())
	if strings.ContainsAny(host, " \\%") || strings.HasSuffix(base.Host, ":") {
		return nil, errLiteLLMURL
	}
	port := base.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, errLiteLLMURL
		}
		port = strconv.Itoa(n)
		if port == liteLLMDefaultPort(base.Scheme) {
			port = ""
		}
	}
	base.Host = host
	if port != "" {
		base.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		base.Host = "[" + host + "]"
	}

	prefix := strings.TrimRight(base.Path, "/")
	if strings.HasSuffix(prefix, "/ui") || strings.Contains(prefix+"/", "/ui/caching/") {
		return nil, errors.New("Enter the LiteLLM API URL rather than the administration page.")
	}
	if strings.Contains(prefix, "//") || strings.ContainsAny(prefix, "\\?#") ||
		strings.IndexFunc(prefix, func(r rune) bool { return r < 32 || r == 127 }) != -1 {
		return nil, errLiteLLMURL
	}
	for _, segment := range strings.Split(prefix, "/") {
		if segment == "." || segment == ".." {
			return nil, errLiteLLMURL
		}
	}
	base.Path = prefix
	if !strings.HasSuffix(prefix, "/v1") {
		base.Path += "/v1"
	}
	return base, nil
}

func liteLLMDefaultPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

func validateLiteLLMSettings(cfg liteLLMSettings) error {
	const message = "Invalid LiteLLM settings. Restore a valid configuration."
	if cfg.URL == "" {
		if cfg.Enabled || cfg.VaultRef != "" {
			return errors.New(message)
		}
		return nil
	}
	base, err := normalizeLiteLLMURL(cfg.URL)
	if err != nil || base.String() != cfg.URL {
		return errors.New(message)
	}
	if cfg.VaultRef != "" {
		if decoded, err := hex.DecodeString(cfg.VaultRef); err != nil || len(decoded) != 32 {
			return errors.New(message)
		}
	}
	return nil
}

// Slots are unique per key change, scoped to both the profile and exact URL.
// Editing settings.json cannot move a saved credential to a different server.
func liteLLMVaultSlot(profile string, cfg liteLLMSettings) string {
	digest := sha256.Sum256([]byte(profile + "\x00" + cfg.URL + "\x00" + cfg.VaultRef))
	return "litellm_" + hex.EncodeToString(digest[:])
}

func validLiteLLMKey(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return r <= ' ' || r >= 127 }) == -1
}

func (a *app) loadLiteLLMKey() {
	cfg := a.config.LiteLLM
	if cfg.VaultRef == "" {
		return
	}
	key, err := a.vault.Get(liteLLMVaultSlot(a.config.VaultID, cfg))
	if err == nil && validLiteLLMKey(key) {
		a.liteLLMKey = key
	}
}

func (a *app) liteLLMConnectionLocked() (liteLLMConnection, bool) {
	if !a.liteLLMReadyLocked() {
		return liteLLMConnection{}, false
	}
	base, err := url.Parse(a.config.LiteLLM.URL)
	if err != nil {
		return liteLLMConnection{}, false
	}
	return liteLLMConnection{Base: base, Key: a.liteLLMKey}, true
}

func (a *app) liteLLMViewLocked() liteLLMView {
	cfg := a.config.LiteLLM
	return liteLLMView{
		URL: cfg.URL, Enabled: cfg.Enabled, Ready: a.liteLLMReadyLocked(),
		HasKey: a.liteLLMKey != "", KeySaved: cfg.VaultRef != "", ModelsCount: a.liteLLMModelsCount,
	}
}

func (a *app) liteLLMReadyLocked() bool {
	cfg := a.config.LiteLLM
	return cfg.Enabled && cfg.URL != "" && (cfg.VaultRef == "" || a.liteLLMKey != "")
}

// Kilo Proxy listens only on loopback. Check same-port addresses, including
// DNS names that resolve to loopback, without disallowing a local LiteLLM.
// Callers must not hold app.mu while resolving a name.
func (a *app) checkLiteLLMTarget(ctx context.Context, base *url.URL) error {
	port := base.Port()
	if port == "" {
		port = liteLLMDefaultPort(base.Scheme)
	}
	a.mu.Lock()
	proxyPort := strconv.Itoa(a.config.Port)
	_, adminPort, _ := net.SplitHostPort(a.adminHost)
	a.mu.Unlock()
	if port != proxyPort && port != adminPort {
		return nil
	}
	reflexive := errors.New("The LiteLLM URL points to this Kilo Proxy instance.")
	host := strings.TrimSuffix(base.Hostname(), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return reflexive
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsUnspecified() {
			return reflexive
		}
		return nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return errors.New("Could not resolve the LiteLLM server. Check the URL and network connection.")
	}
	for _, address := range addresses {
		if address.IP.IsLoopback() || address.IP.IsUnspecified() {
			return reflexive
		}
	}
	return nil
}

func (a *app) writeConnectionViewLocked(w http.ResponseWriter) {
	jsonResponse(w, http.StatusOK, map[string]any{
		"litellm": a.liteLLMViewLocked(), "kiloEnabled": !a.config.KiloPaused,
		"kiloReady": a.kiloReadyLocked(), "connectionReady": a.connectionReadyLocked(),
		"catalogRevision": a.catalogRevision,
	})
}

func (a *app) connectionSettingsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		jsonError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	if r.URL.Path == liteLLMSettingsEndpoint {
		a.saveLiteLLMConnection(w, r)
		return
	}
	a.setConnectionEnabled(w, r)
}

func (a *app) saveLiteLLMConnection(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL    string  `json:"url"`
		APIKey *string `json:"apiKey"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	base, err := normalizeLiteLLMURL(input.URL)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.liteLLMUpdate.TryLock() {
		jsonError(w, http.StatusConflict, "A LiteLLM connection is already being checked.")
		return
	}
	defer a.liteLLMUpdate.Unlock()

	a.mu.Lock()
	previous, previousKey := a.config.LiteLLM, a.liteLLMKey
	checkedPort, localKey := a.config.Port, a.config.LocalKey
	a.mu.Unlock()
	key := ""
	switch {
	case input.APIKey != nil:
		key = strings.TrimSpace(*input.APIKey)
	case previous.VaultRef != "":
		if previous.URL != base.String() || previousKey == "" {
			jsonError(w, http.StatusBadRequest, "Enter the virtual key for this LiteLLM server, or explicitly choose to use no key.")
			return
		}
		key = previousKey
	}
	if len(key) > credentialVaultLimit(a.vault) || !validLiteLLMKey(key) {
		jsonError(w, http.StatusBadRequest, "The virtual key is too long or contains spaces or invalid characters.")
		return
	}
	if key == localKey {
		jsonError(w, http.StatusBadRequest, "Enter the LiteLLM virtual key instead of the Kilo Proxy local key.")
		return
	}
	models, err := a.fetchLiteLLMModels(r.Context(), liteLLMConnection{Base: base, Key: key})
	if err != nil {
		var problem *catalogError
		if errors.As(err, &problem) {
			switch problem.status {
			case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
				jsonError(w, http.StatusUnprocessableEntity, problem.message)
				return
			case http.StatusBadRequest:
				jsonError(w, http.StatusBadRequest, problem.message)
				return
			}
		}
		jsonError(w, http.StatusBadGateway, "Could not load the LiteLLM model catalog. Check the API URL and network connection.")
		return
	}
	if r.Context().Err() != nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if checkedPort != a.config.Port {
		jsonError(w, http.StatusConflict, "The local port changed during the LiteLLM check. Try again.")
		return
	}
	next := a.config
	next.LiteLLM = liteLLMSettings{URL: base.String(), Enabled: previous.URL == "" || previous.Enabled}
	var addedSlot string
	if key != "" {
		if previous.URL == next.LiteLLM.URL && previousKey == key && previous.VaultRef != "" {
			next.LiteLLM.VaultRef = previous.VaultRef
		} else {
			next.LiteLLM.VaultRef = randomKey("")
			addedSlot = liteLLMVaultSlot(next.VaultID, next.LiteLLM)
			if err := a.vault.Set(addedSlot, key); err != nil {
				jsonError(w, http.StatusInternalServerError, "Could not save the LiteLLM key in the credential store.")
				return
			}
		}
	}
	if err := writeSettings(a.dir, next); err != nil {
		if addedSlot != "" {
			_ = a.vault.Delete(addedSlot)
		}
		jsonError(w, http.StatusInternalServerError, "Could not save the LiteLLM settings.")
		return
	}
	a.config, a.liteLLMKey, a.liteLLMModelsCount = next, key, len(models)
	if previous.VaultRef != "" && previous.VaultRef != next.LiteLLM.VaultRef {
		_ = a.vault.Delete(liteLLMVaultSlot(next.VaultID, previous))
	}
	a.catalogRevision++
	a.catalogWarnings = nil
	a.publishUIProjectionLocked()
	a.writeConnectionViewLocked(w)
}

func (a *app) setConnectionEnabled(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	if input.Enabled == nil {
		jsonError(w, http.StatusBadRequest, "Choose whether to enable this connection.")
		return
	}
	enabled := *input.Enabled
	liteLLM := r.URL.Path == liteLLMActiveEndpoint
	if liteLLM {
		if !a.liteLLMUpdate.TryLock() {
			jsonError(w, http.StatusConflict, "A LiteLLM connection is already being checked.")
			return
		}
		defer a.liteLLMUpdate.Unlock()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	next := a.config
	if liteLLM {
		if enabled && (next.LiteLLM.URL == "" || (next.LiteLLM.VaultRef != "" && a.liteLLMKey == "")) {
			jsonError(w, http.StatusConflict, "Configure and verify LiteLLM before enabling it.")
			return
		}
		next.LiteLLM.Enabled = enabled
	} else {
		next.KiloPaused = !enabled
	}
	if next.LiteLLM != a.config.LiteLLM || next.KiloPaused != a.config.KiloPaused {
		if err := writeSettings(a.dir, next); err != nil {
			jsonError(w, http.StatusInternalServerError, "Could not save the connection setting.")
			return
		}
		a.config = next
		a.catalogRevision++
		a.catalogWarnings = nil
		a.publishUIProjectionLocked()
	}
	a.writeConnectionViewLocked(w)
}

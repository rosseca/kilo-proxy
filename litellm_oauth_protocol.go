package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const liteLLMOAuthRequestTimeout = 20 * time.Second

var (
	errLiteLLMOAuthHTTPS    = errors.New("LiteLLM browser sign-in requires HTTPS, except for a loopback server.")
	errLiteLLMOAuthOrigin   = errors.New("LiteLLM advertised a different address. Ask your administrator to set PROXY_BASE_URL to the public API address.")
	errLiteLLMOAuthDiscover = errors.New("Could not discover LiteLLM browser sign-in. Check its API address and whether browser sign-in is enabled.")
	errLiteLLMOAuthRegister = errors.New("LiteLLM could not register this browser sign-in.")
	errLiteLLMOAuthExchange = errors.New("LiteLLM could not finish browser sign-in. Try signing in again.")
	errLiteLLMOAuthRenew    = errors.New("Could not renew the LiteLLM session. Try again or reconnect in Accounts.")
	errLiteLLMOAuthRevoke   = errors.New("Could not revoke the LiteLLM session. Try signing out again.")
	errLiteLLMOAuthStorage  = errors.New("Could not save the LiteLLM session in the credential store.")
)

type liteLLMOAuthMetadata struct {
	Version               int      `json:"contract_version"`
	Issuer                string   `json:"issuer"`
	Resource              string   `json:"resource"`
	AuthorizationURL      string   `json:"authorization_endpoint"`
	RegistrationURL       string   `json:"registration_endpoint"`
	TokenURL              string   `json:"token_endpoint"`
	RevocationURL         string   `json:"revocation_endpoint"`
	ResponseTypes         []string `json:"response_types_supported"`
	GrantTypes            []string `json:"grant_types_supported"`
	ChallengeMethods      []string `json:"code_challenge_methods_supported"`
	TokenAuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
	RevocationAuthMethods []string `json:"revocation_endpoint_auth_methods_supported"`
}

type liteLLMBrowserCredential struct {
	ClientID      string
	TokenURL      string
	RevocationURL string
	Resource      string
	Access        string
	Refresh       string
	ExpiresAt     time.Time
	RefreshAfter  time.Time
}

func liteLLMBrowserBase(api *url.URL) *url.URL {
	origin := *api
	origin.Path = strings.TrimSuffix(origin.Path, "/v1")
	return &origin
}

func validateLiteLLMBrowserTransport(base *url.URL) error {
	if base.Scheme == "https" || base.Hostname() == "localhost" {
		return nil
	}
	if ip := net.ParseIP(base.Hostname()); ip != nil && ip.IsLoopback() {
		return nil
	}
	return errLiteLLMOAuthHTTPS
}

// Parse exact advertised URLs without editing paths or allowing credentials,
// fragments, ambiguous encoding or query parameters. An origin includes its
// effective port. The resource and all endpoints bind to the entered server.
func liteLLMOAuthURL(raw string, base *url.URL) (*url.URL, error) {
	if len(raw) > 2048 || strings.Contains(raw, "#") {
		return nil, errLiteLLMOAuthOrigin
	}
	target, err := url.Parse(raw)
	if err != nil || target.Opaque != "" || target.User != nil || target.RawPath != "" ||
		target.ForceQuery || target.RawQuery != "" || target.Scheme != base.Scheme ||
		!strings.EqualFold(target.Hostname(), base.Hostname()) || strings.HasSuffix(target.Host, ":") {
		return nil, errLiteLLMOAuthOrigin
	}
	port := func(u *url.URL) string {
		if u.Port() == "" {
			return liteLLMDefaultPort(u.Scheme)
		}
		return u.Port()
	}
	if port(target) != port(base) || strings.ContainsAny(target.Path, "\\?#") {
		return nil, errLiteLLMOAuthOrigin
	}
	for _, part := range strings.Split(target.Path, "/") {
		if part == "." || part == ".." {
			return nil, errLiteLLMOAuthOrigin
		}
	}
	return target, nil
}

func (a *app) liteLLMOAuthRequest(ctx context.Context, method, target, contentType string, payload []byte, into any) error {
	ctx, cancel := context.WithTimeout(ctx, liteLLMOAuthRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Kilo-Local/"+version)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	client := &http.Client{Transport: a.transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("LiteLLM OAuth HTTP %d", response.StatusCode)
	}
	if into == nil {
		return nil
	}
	const limit = 64 << 10
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(data) > limit {
		return errors.New("Invalid LiteLLM OAuth response")
	}
	return json.Unmarshal(data, into)
}

func (a *app) discoverLiteLLMOAuth(ctx context.Context, api *url.URL) (liteLLMOAuthMetadata, error) {
	if err := validateLiteLLMBrowserTransport(api); err != nil {
		return liteLLMOAuthMetadata{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, liteLLMOAuthRequestTimeout)
	defer cancel()
	if err := a.checkLiteLLMTarget(ctx, api); err != nil {
		return liteLLMOAuthMetadata{}, err
	}
	base := liteLLMBrowserBase(api)
	discovery := *base
	discovery.Path += "/.well-known/litellm-cli-auth"
	var metadata liteLLMOAuthMetadata
	err := a.liteLLMOAuthRequest(ctx, http.MethodGet, discovery.String(), "", nil, &metadata)
	if err != nil || metadata.Version != 1 || !slices.Contains(metadata.ChallengeMethods, "S256") ||
		!slices.Contains(metadata.ResponseTypes, "code") || !slices.Contains(metadata.GrantTypes, "authorization_code") ||
		!slices.Contains(metadata.GrantTypes, "refresh_token") || !slices.Contains(metadata.TokenAuthMethods, "none") ||
		!slices.Contains(metadata.RevocationAuthMethods, "none") {
		return liteLLMOAuthMetadata{}, errLiteLLMOAuthDiscover
	}
	issuer, err := liteLLMOAuthURL(metadata.Issuer, base)
	if err != nil || strings.TrimRight(issuer.Path, "/") != base.Path {
		return liteLLMOAuthMetadata{}, errLiteLLMOAuthOrigin
	}
	for _, endpoint := range []string{metadata.Resource, metadata.AuthorizationURL, metadata.RegistrationURL, metadata.TokenURL, metadata.RevocationURL} {
		if _, err = liteLLMOAuthURL(endpoint, base); err != nil {
			return liteLLMOAuthMetadata{}, err
		}
	}
	return metadata, nil
}

func liteLLMRandomOAuthValue() (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random[:]), nil
}

func (a *app) registerLiteLLMOAuth(ctx context.Context, metadata liteLLMOAuthMetadata, callback string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"client_name": "Kilo Proxy", "redirect_uris": []string{callback}, "token_endpoint_auth_method": "none",
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
	})
	var result struct {
		ClientID     string   `json:"client_id"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	err := a.liteLLMOAuthRequest(ctx, http.MethodPost, metadata.RegistrationURL, "application/json", body, &result)
	if err != nil || result.ClientID == "" || len(result.ClientID) > 1024 || !validLiteLLMKey(result.ClientID) ||
		result.AuthMethod != "none" || !slices.Equal(result.RedirectURIs, []string{callback}) {
		return "", errLiteLLMOAuthRegister
	}
	return result.ClientID, nil
}

func (a *app) exchangeLiteLLMOAuth(ctx context.Context, credential liteLLMBrowserCredential, fields url.Values) (liteLLMBrowserCredential, error) {
	fields.Set("client_id", credential.ClientID)
	fields.Set("resource", credential.Resource)
	var tokens struct {
		Access    string `json:"access_token"`
		Refresh   string `json:"refresh_token"`
		Type      string `json:"token_type"`
		ExpiresIn int64  `json:"expires_in"`
	}
	err := a.liteLLMOAuthRequest(ctx, http.MethodPost, credential.TokenURL, "application/x-www-form-urlencoded", []byte(fields.Encode()), &tokens)
	if err != nil || tokens.Access == "" || tokens.Refresh == "" || !validLiteLLMKey(tokens.Access) || !validLiteLLMKey(tokens.Refresh) ||
		!strings.EqualFold(tokens.Type, "Bearer") || tokens.ExpiresIn <= 0 || tokens.ExpiresIn > int64((10*365*24*time.Hour)/time.Second) {
		if err == nil {
			err = errors.New("Invalid LiteLLM token response")
		}
		return liteLLMBrowserCredential{}, err
	}
	credential.Access, credential.Refresh = tokens.Access, tokens.Refresh
	life := time.Duration(tokens.ExpiresIn) * time.Second
	credential.ExpiresAt = time.Now().UTC().Add(life)
	credential.RefreshAfter = credential.ExpiresAt.Add(-min(time.Minute, life/10))
	return credential, nil
}

func (a *app) revokeLiteLLMOAuth(ctx context.Context, credential liteLLMBrowserCredential) error {
	fields := url.Values{"token": {credential.Refresh}, "client_id": {credential.ClientID}}
	if err := a.liteLLMOAuthRequest(ctx, http.MethodPost, credential.RevocationURL, "application/x-www-form-urlencoded", []byte(fields.Encode()), nil); err != nil {
		return errLiteLLMOAuthRevoke
	}
	return nil
}

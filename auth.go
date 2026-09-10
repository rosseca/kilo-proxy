package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Uses the same device flow as Kilo's public CLI. There is no client secret,
// refresh-token exchange, browser-cookie extraction, or team-wide API key.
const kiloAccountURL = "https://api.kilo.ai"

type organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type loginSession struct {
	Status          string    `json:"status"`
	Code            string    `json:"code,omitempty"`
	VerificationURL string    `json:"verificationUrl,omitempty"`
	ExpiresAt       time.Time `json:"expiresAt"`
	Message         string    `json:"message,omitempty"`
	cancel          context.CancelFunc
}
type kiloProfile struct {
	User struct {
		Email string `json:"email"`
	} `json:"user"`
	Email         string         `json:"email"`
	Organizations []organization `json:"organizations"`
}

func (a *app) authPending() bool {
	return a.login != nil && (a.login.Status == "starting" || a.login.Status == "pending")
}
func validVerificationURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	return u.Host == "app.kilo.ai" || u.Host == "kilo.ai" || u.Host == "kilocode.ai"
}
func (a *app) accountRequest(ctx context.Context, method, path, key string, result any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, a.accountURL+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Kilo-Local/"+version)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Transport: a.transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return 0, errors.New("No se pudo conectar con el login de Kilo.")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 && result != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result); err != nil {
			return resp.StatusCode, errors.New("Kilo devolvió una respuesta de login no válida.")
		}
	}
	return resp.StatusCode, nil
}
func (a *app) beginLogin(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	select {
	case <-a.quit:
		a.mu.Unlock()
		jsonError(w, 409, "La aplicación se está cerrando.")
		return
	default:
	}
	if a.proxyServer != nil || a.authPending() {
		a.mu.Unlock()
		jsonError(w, 409, "Detén el proxy o cancela el login actual antes de conectar otra cuenta.")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &loginSession{Status: "starting", cancel: cancel}
	a.catalogRevision++
	a.login = session
	a.mu.Unlock()
	var data struct {
		Code            string `json:"code"`
		VerificationURL string `json:"verificationUrl"`
		ExpiresIn       int    `json:"expiresIn"`
	}
	status, err := a.accountRequest(ctx, "POST", "/api/device-auth/codes", "", &data)
	if err != nil || status != 200 || !validVerificationURL(data.VerificationURL) || data.Code == "" || len(data.Code) > 128 || strings.ContainsAny(data.Code, "/\\?#\r\n\t ") || data.ExpiresIn <= 0 || data.ExpiresIn > 1800 {
		message := "No se pudo iniciar el login de Kilo. Puedes usar tu API key manualmente."
		if status == 429 {
			message = "Hay demasiados logins pendientes en Kilo. Espera un momento y vuelve a intentarlo."
		}
		a.finishLogin(session, "error", message)
		jsonError(w, 502, message)
		return
	}
	a.mu.Lock()
	if a.login != session || session.Status != "starting" {
		a.mu.Unlock()
		cancel()
		jsonError(w, 409, "Login cancelado.")
		return
	}
	session.Status = "pending"
	session.Code = data.Code
	session.VerificationURL = data.VerificationURL
	session.ExpiresAt = time.Now().Add(time.Duration(data.ExpiresIn) * time.Second)
	// Copy values while locked. The poller may update the session immediately.
	response := *session
	a.mu.Unlock()
	go a.pollLogin(ctx, session, data.Code, response.ExpiresAt)
	jsonResponse(w, 200, response)
}
func (a *app) finishLogin(session *loginSession, status, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.login != session || !a.authPending() {
		return
	}
	session.Status = status
	session.Message = message
	session.Code = ""
	session.VerificationURL = ""
	session.cancel()
}
func (a *app) cancelLogin() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.authPending() {
		a.login.Status = "cancelled"
		a.login.Code = ""
		a.login.VerificationURL = ""
		a.login.cancel()
	}
}
func (a *app) pollLogin(ctx context.Context, session *loginSession, code string, deadline time.Time) {
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	ticker := time.NewTicker(a.authPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			a.finishLogin(session, "expired", "El código ha caducado. Vuelve a conectar con Kilo.")
			return
		case <-ticker.C:
			var data struct {
				Status    string `json:"status"`
				Token     string `json:"token"`
				UserEmail string `json:"userEmail"`
			}
			status, err := a.accountRequest(ctx, "GET", "/api/device-auth/codes/"+url.PathEscape(code), "", &data)
			if ctx.Err() != nil {
				a.finishLogin(session, "expired", "El código ha caducado. Vuelve a conectar con Kilo.")
				return
			}
			if err != nil {
				a.finishLogin(session, "error", "Se perdió la conexión con Kilo. Vuelve a iniciar el login.")
				return
			}
			if status == 202 || (status == 200 && data.Status == "pending") {
				continue
			}
			if status == 429 {
				ticker.Reset(2 * a.authPollInterval)
				continue
			}
			if status == 403 || data.Status == "denied" {
				a.finishLogin(session, "denied", "No se ha autorizado el acceso en Kilo.")
				return
			}
			if status == 410 || data.Status == "expired" {
				a.finishLogin(session, "expired", "El código ha caducado. Vuelve a conectar con Kilo.")
				return
			}
			if status != 200 || data.Status != "approved" || data.Token == "" || strings.ContainsAny(data.Token, "\r\n\t ") || len(data.Token) > 8192 {
				a.finishLogin(session, "error", "Kilo no ha devuelto una credencial válida.")
				return
			}
			profile, profileErr := a.fetchOrganizations(ctx, data.Token)
			a.mu.Lock()
			if a.login != session || session.Status != "pending" {
				a.mu.Unlock()
				return
			}
			// Clear the previous organization's context when switching accounts.
			a.catalogRevision++
			a.apiKey = data.Token
			a.keySaved = false
			a.connectionNeedsSave = true
			a.accountEmail = data.UserEmail
			a.organizations = nil
			a.config.OrgID = ""
			if profileErr == nil {
				a.organizations = profile.Organizations
				if profile.User.Email != "" {
					a.accountEmail = profile.User.Email
				} else if profile.Email != "" {
					a.accountEmail = profile.Email
				}
				if len(a.organizations) == 1 {
					a.config.OrgID = a.organizations[0].ID
					a.catalogRevision++
				}
			}
			session.Status = "approved"
			session.Code = ""
			session.VerificationURL = ""
			session.Message = "Sesión conectada. Selecciona tu equipo y pulsa Guardar y arrancar."
			if len(a.organizations) == 1 {
				session.Message = "Sesión conectada. Hemos seleccionado tu único equipo; pulsa Guardar y arrancar."
			}
			if profileErr != nil {
				session.Message = "Sesión conectada, pero no se pudieron cargar tus equipos. Pulsa Cargar mis equipos o introduce el ID manualmente."
			} else if len(a.organizations) == 0 {
				session.Message = "Sesión conectada. Kilo no devuelve ninguna organización para esta cuenta."
			}
			session.cancel()
			a.mu.Unlock()
			return
		}
	}
}
func (a *app) fetchOrganizations(ctx context.Context, key string) (kiloProfile, error) {
	var profile kiloProfile
	status, err := a.accountRequest(ctx, "GET", "/api/profile", key, &profile)
	if err != nil || status != 200 {
		return profile, fmt.Errorf("no se pudieron cargar las organizaciones (HTTP %d)", status)
	}
	for _, org := range profile.Organizations {
		if org.ID == "" || len(org.ID) > 128 || strings.ContainsAny(org.ID, "\r\n\t /\\") {
			return kiloProfile{}, errors.New("organización no válida")
		}
	}
	return profile, nil
}
func (a *app) loadOrganizations(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	key := a.apiKey
	if a.proxyServer != nil || a.authPending() {
		a.mu.Unlock()
		jsonError(w, 409, "Detén el proxy antes de cambiar de equipo.")
		return
	}
	a.mu.Unlock()
	if key == "" {
		jsonError(w, 400, "Conecta con Kilo o guarda una API key primero.")
		return
	}
	profile, err := a.fetchOrganizations(r.Context(), key)
	if err != nil {
		jsonError(w, 502, "No se pudieron cargar tus equipos. Revisa la conexión o vuelve a iniciar sesión.")
		return
	}
	a.mu.Lock()
	if a.apiKey != key || a.authPending() || a.proxyServer != nil {
		a.mu.Unlock()
		jsonError(w, 409, "La conexión ha cambiado. Vuelve a cargar los equipos.")
		return
	}
	a.organizations = profile.Organizations
	a.accountEmail = profile.User.Email
	if a.accountEmail == "" {
		a.accountEmail = profile.Email
	}
	if a.config.OrgID == "" && len(a.organizations) == 1 {
		a.config.OrgID = a.organizations[0].ID
		a.connectionNeedsSave = true
		a.catalogRevision++
	}
	a.mu.Unlock()
	a.state(w)
}

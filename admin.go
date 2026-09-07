package main

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed ui/*
var assets embed.FS

var errMissingCredentials = errors.New("introduce tu API key y el ID de organización")

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func jsonError(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]any{"error": map[string]string{"message": message, "type": "kilo_local_error"}})
}

func decodeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		jsonError(w, 415, "Se requiere JSON.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(into); err != nil {
		jsonError(w, 400, "Petición JSON no válida.")
		return false
	}
	if d.Decode(&struct{}{}) != io.EOF {
		jsonError(w, 400, "Petición JSON no válida.")
		return false
	}
	return true
}

func (a *app) adminHandler() http.Handler {
	root, _ := fs.Sub(assets, "ui")
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		if !localHostMatches(r, a.adminHost) {
			jsonError(w, 403, "Origen no permitido.")
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method != "GET" || (r.URL.Path != "/" && r.URL.Path != "/app.js" && r.URL.Path != "/editor-helper.mjs" && r.URL.Path != "/xcode-helper.mjs" && r.URL.Path != "/activity-helper.mjs" && r.URL.Path != "/usage-helper.mjs" && r.URL.Path != "/codex-catalog.mjs" && r.URL.Path != "/model-helper.mjs" && r.URL.Path != "/client-config.mjs" && r.URL.Path != "/claude-helper.mjs" && r.URL.Path != "/i18n.mjs" && r.URL.Path != "/style.css" && r.URL.Path != "/icon.svg") {
				http.NotFound(w, r)
				return
			}
			files.ServeHTTP(w, r)
			return
		}
		if !secureEqual(r.Header.Get("Authorization"), "Bearer "+a.adminToken) {
			jsonError(w, 401, "Abre el panel desde la aplicación para recuperar el acceso.")
			return
		}
		if (r.URL.Path == "/api/editors/zed/profile" || r.URL.Path == "/api/editors/opencode/profile") && (r.Method == "GET" || r.Method == "POST") {
			a.editorProfile(w, r)
			return
		}
		if r.URL.Path == "/api/cursor" && (r.Method == "GET" || r.Method == "POST") {
			a.cursorAPI(w, r)
			return
		}
		if (r.Method == "GET" && r.URL.Path == "/api/xcode/info") || ((r.Method == "GET" || r.Method == "POST") && (r.URL.Path == "/api/xcode/chat" || r.URL.Path == "/api/xcode/codex" || r.URL.Path == "/api/xcode/claude")) {
			a.xcodeAPI(w, r)
			return
		}
		if (r.Method == "GET" && r.URL.Path == "/api/claude/info") || ((r.Method == "GET" || r.Method == "POST") && r.URL.Path == "/api/claude/profile") {
			a.claudeProfile(w, r)
			return
		}
		if (r.Method == "GET" || r.Method == "POST") && (r.URL.Path == "/api/codex/catalog" || r.URL.Path == "/api/codex-cli/catalog") {
			a.codexCatalog(w, r)
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/activity/") {
			a.activityDetail(w, r)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/state" {
			a.state(w)
			return
		}
		if r.Method != "POST" {
			jsonError(w, 405, "Método no permitido.")
			return
		}
		switch r.URL.Path {
		case "/api/activity/config":
			a.activityConfig(w, r)
		case "/api/activity/clear":
			a.clearActivity(w)
		case "/api/language":
			a.saveLanguage(w, r)
		case "/api/auth/start":
			a.beginLogin(w, r)
		case "/api/auth/cancel":
			a.cancelLogin()
			a.state(w)
		case "/api/auth/organizations":
			a.loadOrganizations(w, r)
		case "/api/config":
			a.saveConfig(w, r)
		case "/api/start":
			if err := a.start(); err != nil {
				if errors.Is(err, errMissingCredentials) {
					jsonError(w, 400, err.Error())
				} else {
					jsonError(w, 409, "No se pudo abrir el puerto. Puede estar ocupado por otra instancia; elige otro puerto.")
				}
				return
			}
			a.state(w)
		case "/api/stop":
			a.stop()
			a.state(w)
		case "/api/models":
			a.models(w, r)
		case "/api/check":
			a.check(w, r)
		case "/api/forget":
			a.forget(w)
		case "/api/quit":
			jsonResponse(w, 200, map[string]bool{"ok": true})
			a.requestQuit()
		default:
			http.NotFound(w, r)
		}
	})
}

func (a *app) state(w http.ResponseWriter) {
	a.mu.Lock()
	defer a.mu.Unlock()
	uptime := int64(0)
	if a.proxyServer != nil {
		uptime = int64(time.Since(a.started).Seconds())
	}
	jsonResponse(w, 200, map[string]any{
		"cursor": a.cursor, "language": a.config.Language, "catalogRevision": a.catalogRevision,
		"auth": a.login, "organizations": a.organizations, "accountEmail": a.accountEmail, "keySaved": a.keySaved,
		"version": version, "port": a.config.Port, "orgId": a.config.OrgID,
		"localKey": a.config.LocalKey, "hasKey": a.apiKey != "", "remember": a.config.Remember,
		"running": a.proxyServer != nil, "baseURL": "http://127.0.0.1:" + strconv.Itoa(a.config.Port) + "/v1",
		"requests": a.requests, "failures": a.failures, "active": a.active, "uptime": uptime,
		"usage":          a.usageSnapshot(),
		"captureEnabled": a.captureEnabled, "activityEpoch": a.activityEpoch,
		"events": a.events, "warning": a.vaultWarning,
	})
}

// Language is independent of connection settings and can change while running.
func (a *app) saveLanguage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Language string `json:"language"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	if input.Language != "en" && input.Language != "es" {
		jsonError(w, 400, "Idioma no compatible.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg := a.config
	cfg.Language = input.Language
	if err := writeSettings(a.dir, cfg); err != nil {
		jsonError(w, 500, "No se pudo guardar el idioma. Revisa los permisos de la carpeta.")
		return
	}
	a.config = cfg
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

func (a *app) saveConfig(w http.ResponseWriter, r *http.Request) {
	var input struct {
		APIKey   string `json:"apiKey"`
		OrgID    string `json:"orgId"`
		Port     int    `json:"port"`
		Remember bool   `json:"remember"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	input.APIKey = strings.TrimSpace(input.APIKey)
	input.OrgID = strings.TrimSpace(input.OrgID)
	if input.Port < 1024 || input.Port > 65535 {
		jsonError(w, 400, "El puerto debe estar entre 1024 y 65535.")
		return
	}
	if input.OrgID == "" || len(input.OrgID) > 128 || strings.ContainsAny(input.OrgID, "\r\n\t /\\") {
		jsonError(w, 400, "Introduce el ID de organización, no su nombre ni la URL.")
		return
	}
	if strings.ContainsAny(input.APIKey, "\r\n\t ") || len(input.APIKey) > 8192 {
		jsonError(w, 400, "La API key no puede contener espacios ni saltos de línea.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proxyServer != nil || a.authPending() {
		jsonError(w, 409, "Detén el proxy o cancela el login antes de cambiar la conexión.")
		return
	}
	key := input.APIKey
	if key == "" {
		key = a.apiKey
	}
	if key == "" {
		jsonError(w, 400, "Introduce tu API key personal de Kilo.")
		return
	}
	cfg := a.config
	cfg.Port, cfg.OrgID, cfg.Remember = input.Port, input.OrgID, input.Remember
	if cfg.Remember {
		if len(key) > 2400 {
			jsonError(w, 400, "Esta clave supera el tamaño portable del almacén de credenciales. Desmarca Recordar para usarla durante esta sesión.")
			return
		}
		if err := a.vault.Set(cfg.VaultID, key); err != nil {
			jsonError(w, 500, "No se pudo guardar en el almacén del sistema. Desmarca Recordar para usar la clave solo durante esta sesión.")
			return
		}
	} else if a.config.Remember {
		if err := a.vault.Delete(cfg.VaultID); err != nil {
			jsonError(w, 500, "No se pudo borrar la clave guardada. Desbloquea el almacén del sistema y vuelve a intentarlo.")
			return
		}
	}
	if err := writeSettings(a.dir, cfg); err != nil {
		jsonError(w, 500, "No se pudo guardar la configuración local. Revisa los permisos de la carpeta.")
		return
	}
	if input.APIKey != "" && input.APIKey != a.apiKey {
		a.organizations = nil
		a.accountEmail = ""
		a.login = nil
	}
	a.catalogRevision++
	a.config, a.apiKey, a.vaultWarning = cfg, key, ""
	a.keySaved = cfg.Remember
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

func (a *app) forget(w http.ResponseWriter) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proxyServer != nil || a.authPending() {
		jsonError(w, 409, "Detén el proxy o cancela el login antes de olvidar la clave.")
		return
	}
	if a.config.Remember {
		if err := a.vault.Delete(a.config.VaultID); err != nil {
			jsonError(w, 500, "No se pudo borrar la clave del almacén del sistema.")
			return
		}
	}
	cfg := a.config
	cfg.Remember = false
	if err := writeSettings(a.dir, cfg); err != nil {
		jsonError(w, 500, "No se pudo guardar la configuración local.")
		return
	}
	a.catalogRevision++
	a.config, a.apiKey, a.vaultWarning = cfg, "", ""
	a.keySaved = false
	a.accountEmail = ""
	a.organizations = nil
	a.login = nil
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

// This intentionally reads the catalog only; it does not imply that organization
// credits or model policy were checked. Inference remains a deliberate client action.
func (a *app) check(w http.ResponseWriter, r *http.Request) {
	models, revision, err := a.fetchModels(r.Context(), true)
	if err != nil {
		catalogFailure(w, err)
		return
	}
	jsonResponse(w, 200, map[string]any{"models": len(models), "catalog": models, "revision": revision, "fetchedAt": time.Now().UTC().Format(time.RFC3339), "message": "Gateway accesible. El catálogo no verifica el saldo ni los permisos de generación de tu organización."})
}

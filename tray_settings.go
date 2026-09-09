package main

import "net/http"

const (
	trayDisplayIcon  = "icon"
	trayDisplaySpend = "spend"
)

func normalizeTrayDisplay(display string) string {
	if display == trayDisplaySpend {
		return trayDisplaySpend
	}
	return trayDisplayIcon
}

type traySettingsResponse struct {
	Display string `json:"display"`
}

func (a *app) traySettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.mu.Lock()
		display := normalizeTrayDisplay(a.config.TrayDisplay)
		a.mu.Unlock()
		jsonResponse(w, http.StatusOK, traySettingsResponse{Display: display})
		return
	}
	if r.Method != http.MethodPut {
		jsonError(w, http.StatusMethodNotAllowed, "Método no permitido.")
		return
	}
	var input traySettingsResponse
	if !decodeBody(w, r, &input) {
		return
	}
	if input.Display != trayDisplayIcon && input.Display != trayDisplaySpend {
		jsonError(w, http.StatusBadRequest, "Selecciona icon o spend para la bandeja.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg := a.config
	cfg.TrayDisplay = input.Display
	if err := writeSettings(a.dir, cfg); err != nil {
		jsonError(w, http.StatusInternalServerError, "No se pudo guardar la apariencia. Revisa los permisos de la carpeta.")
		return
	}
	a.config = cfg
	jsonResponse(w, http.StatusOK, input)
}

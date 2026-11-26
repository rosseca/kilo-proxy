package main

import "net/http"

const (
	t3CodeTelemetryEndpoint = "/api/clients/t3-code/telemetry"
	t3CodeTelemetryDomain   = "ingest.t3.codes"
)

func (a *app) t3CodeTelemetryAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		jsonError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	var input struct {
		Disabled *bool `json:"disabled"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	if input.Disabled == nil {
		jsonError(w, http.StatusBadRequest, "A telemetry preference is required.")
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	disabled := *input.Disabled
	if disabled != a.config.T3CodeTelemetryDisabled {
		next := a.config
		next.T3CodeTelemetryDisabled = disabled
		if err := writeSettings(a.dir, next); err != nil {
			jsonError(w, http.StatusInternalServerError, "Could not save the T3 Code telemetry setting.")
			return
		}
		a.config = next
	}
	jsonResponse(w, http.StatusOK, map[string]bool{"disabled": disabled})
}

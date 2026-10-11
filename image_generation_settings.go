package main

import "net/http"

// Image requests snapshot this preference before choosing their provider. Saving
// a new preference affects later requests without stopping the proxy or changing
// an existing agent profile. Keep unavailable providers selected for reconnect.
func (a *app) imageGenerationSettingsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	var input *imageGenerationSettings
	if !decodeBody(w, r, &input) {
		return
	}
	if input == nil {
		jsonError(w, http.StatusBadRequest, "Image generation settings must be a JSON object.")
		return
	}
	if err := validateImageGenerationSettings(*input); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg := a.config
	cfg.ImageGeneration = *input
	if err := writeSettings(a.dir, cfg); err != nil {
		jsonError(w, http.StatusInternalServerError, "Could not save image generation settings. Check the configuration folder permissions.")
		return
	}
	a.config = cfg
	jsonResponse(w, http.StatusOK, map[string]any{"ok": true, "imageGeneration": cfg.ImageGeneration})
}

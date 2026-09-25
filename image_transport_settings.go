package main

import (
	"errors"
	"net/http"
)

type imageTransportSettings struct {
	Mode         string `json:"mode"`
	Profile      string `json:"profile"`
	LitterboxTTL string `json:"litterboxTTL"`
}

func normalizeImageTransportSettings(value imageTransportSettings) imageTransportSettings {
	if value.Mode == "" {
		value.Mode = "cloudflare"
	}
	if value.Profile == "" {
		value.Profile = "high"
	}
	if value.LitterboxTTL == "" {
		value.LitterboxTTL = "1h"
	}
	return value
}

func validateImageTransportSettings(value imageTransportSettings) error {
	switch value.Mode {
	case "off", "compress", "upload", "cloudflare", "litterbox", "tailscale":
	default:
		return errors.New("Choose off, local compression, Kilo upload, Cloudflare, Litterbox, or Tailscale for large images.")
	}
	if value.Profile != "high" && value.Profile != "balanced" && value.Profile != "small" {
		return errors.New("Choose high, balanced, or small for image compression.")
	}
	// Missing TTL is accepted for settings saved by older versions.
	switch value.LitterboxTTL {
	case "", "1h", "12h", "24h", "72h":
	default:
		return errors.New("Choose 1h, 12h, 24h, or 72h for Litterbox expiry.")
	}
	return nil
}

// These choices apply to new requests without restarting the connection.
// Changing mode does not interrupt cleanup for earlier uploads.
func (a *app) imageTransportSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.mu.Lock()
		settings := normalizeImageTransportSettings(a.config.ImageTransport)
		a.mu.Unlock()
		jsonResponse(w, http.StatusOK, settings)
		return
	}
	if r.Method != http.MethodPut {
		jsonError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	var input imageTransportSettings
	if !decodeBody(w, r, &input) {
		return
	}
	if err := validateImageTransportSettings(input); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg := a.config
	input = normalizeImageTransportSettings(input)
	cfg.ImageTransport = input
	if err := writeSettings(a.dir, cfg); err != nil {
		jsonError(w, http.StatusInternalServerError, "Could not save the image preference. Check the configuration folder permissions.")
		return
	}
	a.config = cfg
	jsonResponse(w, http.StatusOK, input)
}

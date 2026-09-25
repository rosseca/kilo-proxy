package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

// This changes only Kilo's opt-in. Preparing Desktop remains a separate action;
// turning the option off preserves the saved real-ID selection and config files.
func (a *app) claudeDesktopOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	var input struct {
		ExperimentalModels *bool `json:"experimentalModels"`
	}
	if r.Method == http.MethodPost {
		data, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, catalogLimit))
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		object, objectErr := decodeClaudeDesktopObject(data)
		mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if readErr != nil || objectErr != nil || len(object) != 1 || object["experimentalModels"] == nil || mediaErr != nil || mediaType != "application/json" || decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || input.ExperimentalModels == nil {
			jsonError(w, http.StatusBadRequest, "Provide experimentalModels as a JSON boolean.")
			return
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.Method == http.MethodPost && a.config.ClaudeDesktopExperimentalModels != *input.ExperimentalModels {
		if !safeLaunchDir(a.dir, a.dir) {
			jsonError(w, http.StatusConflict, "Cannot safely access the Kilo settings directory.")
			return
		}
		if info, err := os.Lstat(filepath.Join(a.dir, "settings.json")); err != nil && !os.IsNotExist(err) || err == nil && !info.Mode().IsRegular() {
			jsonError(w, http.StatusConflict, "Cannot safely write Kilo settings.")
			return
		}
		cfg := a.config
		cfg.ClaudeDesktopExperimentalModels = *input.ExperimentalModels
		if err := writeSettings(a.dir, cfg); err != nil {
			jsonError(w, http.StatusInternalServerError, "Could not save Claude Desktop options. Check the settings directory permissions.")
			return
		}
		a.config = cfg
	}
	jsonResponse(w, http.StatusOK, map[string]bool{"experimentalModels": a.config.ClaudeDesktopExperimentalModels})
}

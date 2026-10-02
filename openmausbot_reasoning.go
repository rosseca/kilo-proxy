package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const openMausBotBasePath = "/openmausbot/v1"

type openMausBotRequestContextKey struct{}

// Snapshot the same supported vocabulary and preferred level used by the
// shared helpers. A catalog refresh must not hot-change an active workspace.
func openMausBotReasoning(library modelLibrary, catalog []modelInfo) map[string]string {
	result := map[string]string{}
	for _, choice := range terminalLibraryChoices(library, catalog) {
		levels, effort := nativeReasoningFor(choice)
		if choice.ReasoningCustom && len(choice.ReasoningLevels) == 0 {
			continue
		}
		// Explicit user overrides can include a none-only policy. A known
		// non-reasoning model must not receive an inferred automatic default.
		if choice.Model.Reasoning != nil && !*choice.Model.Reasoning && !choice.ReasoningCustom {
			continue
		}
		if effortNames[effort] && helperContains(levels, effort) {
			result[choice.Model.ID] = effort
		}
	}
	return result
}

func validOpenMausBotReasoning(library modelLibrary, efforts map[string]string) bool {
	if efforts == nil || len(efforts) > len(library.Models) {
		return false
	}
	models := map[string]modelLibraryItem{}
	for _, item := range library.Models {
		models[item.ID] = item
	}
	for id, effort := range efforts {
		item, ok := models[id]
		if !ok || !effortNames[effort] || item.ReasoningCustom && !helperContains(item.ReasoningLevels, effort) {
			return false
		}
	}
	return true
}

// Exact routing comes after local authentication. The context marker cannot
// be supplied by a header and survives normalization for the shared pipeline.
func openMausBotInferencePath(r *http.Request) (string, bool) {
	if r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
		return "", false
	}
	if r.Method == http.MethodGet && r.URL.Path == openMausBotBasePath+"/models" {
		return "/v1/models", true
	}
	if r.Method == http.MethodPost && r.URL.Path == openMausBotBasePath+"/chat/completions" {
		return "/v1/chat/completions", true
	}
	return "", false
}

func (a *app) openMausBotInferenceSelection() (openMausBotPrepared, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	saved, err := a.readOpenMausBotPrepared()
	if err != nil || !a.openMausBotInferenceReady(saved) {
		return openMausBotPrepared{}, errors.New("Prepare OpenMausBot again before using its managed models and reasoning defaults.")
	}
	return saved, nil
}

func (a *app) serveOpenMausBotModels(w http.ResponseWriter) {
	saved, err := a.openMausBotInferenceSelection()
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	ids := []string{saved.Library.DefaultModel}
	for _, item := range saved.Library.Models {
		if item.ID != saved.Library.DefaultModel {
			ids = append(ids, item.ID)
		}
	}
	models := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		models = append(models, map[string]any{"id": id, "object": "model", "owned_by": "kilo-proxy"})
	}
	jsonResponse(w, http.StatusOK, map[string]any{"object": "list", "data": models})
}

// Run after the trace reader and bounded upload reader, before provider routing.
// RawMessage preserves exact model IDs, schemas, numbers and message content;
// unmodified requests retain their original body bytes and explicit settings.
func (a *app) prepareOpenMausBotReasoning(r *http.Request) error {
	if managed, _ := r.Context().Value(openMausBotRequestContextKey{}).(bool); !managed || r.Method != http.MethodPost {
		return nil
	}
	if r.Body == nil {
		return errors.New("Invalid OpenMausBot request JSON.")
	}
	raw, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		return err
	}
	restore := func(body []byte) {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.GetBody = nil
		r.ContentLength = int64(len(body))
		r.Header.Del("Content-Length")
	}
	restore(raw)
	request, err := decodeClaudeDesktopObject(raw)
	if err != nil {
		return errors.New("OpenMausBot requests must contain valid JSON with unique keys.")
	}
	var model string
	if json.Unmarshal(request["model"], &model) != nil || !catalogID.MatchString(model) {
		return errors.New("Choose an exact model from the prepared OpenMausBot selection.")
	}
	saved, err := a.openMausBotInferenceSelection()
	if err != nil {
		return err
	}
	selected := false
	for _, item := range saved.Library.Models {
		if item.ID == model {
			selected = true
			break
		}
	}
	if !selected {
		return errors.New("This model is not selected in the prepared OpenMausBot profile. Prepare it again after updating your models.")
	}
	effort := saved.ReasoningEfforts[model]
	if effort == "" {
		return nil
	}
	for _, key := range []string{"reasoning", "reasoning_effort", "thinking"} {
		if _, exists := request[key]; exists {
			return nil
		}
	}
	if raw, exists := request["output_config"]; exists {
		var output map[string]json.RawMessage
		if json.Unmarshal(raw, &output) != nil {
			return nil
		} // Leave invalid explicit options to the provider.
		if _, exists := output["effort"]; exists {
			return nil
		}
	}
	if strings.HasPrefix(model, "chatgpt/") {
		request["reasoning_effort"], _ = json.Marshal(effort)
	} else {
		request["reasoning"], _ = json.Marshal(map[string]any{"enabled": effort != "none", "effort": effort})
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return errors.New("Cannot apply the prepared OpenMausBot reasoning default.")
	}
	if len(encoded) > 32<<20 {
		return &http.MaxBytesError{Limit: 32 << 20}
	}
	restore(encoded)
	return nil
}

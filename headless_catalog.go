package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"time"
)

type headlessCatalogState struct {
	Models    []modelInfo `json:"models"`
	Revision  uint64      `json:"revision"`
	Complete  bool        `json:"complete"`
	FetchedAt time.Time   `json:"fetchedAt"`
	Warnings  []string    `json:"warnings,omitempty"`
}

// Console users need the same scoped capabilities as the native Models view.
// Persist metadata here so a separate CLI process can prepare agents later.
func (a *app) headlessCatalogAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		jsonError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	a.mu.Lock()
	scope, revision := a.catalogScopeLocked(), a.catalogRevision
	cache := readNativeCachedCatalog(a.dir, scope)
	a.mu.Unlock()
	if cache.Models == nil {
		cache.Models = []modelInfo{}
	}
	if r.Method == http.MethodGet {
		jsonResponse(w, http.StatusOK, headlessCatalogState{Models: cache.Models, Revision: revision, Complete: !cache.FetchedAt.IsZero(), FetchedAt: cache.FetchedAt})
		return
	}
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	models, fetchedRevision, complete, err := a.fetchModelsWithCompleteness(r.Context(), true)
	if err != nil {
		catalogFailure(w, err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.catalogScopeLocked() != scope || a.catalogRevision != fetchedRevision {
		jsonError(w, http.StatusConflict, "A connection changed while loading models. Refresh the catalog.")
		return
	}
	// Partial provider results do not erase previously known capabilities for
	// the unavailable provider, or make its last complete query appear recent.
	if !complete {
		seen := make(map[string]bool, len(models))
		for _, model := range models {
			seen[model.ID] = true
		}
		for _, model := range cache.Models {
			if !seen[model.ID] {
				models = append(models, model)
			}
		}
	}
	if len(models) > 10000 {
		jsonError(w, http.StatusBadGateway, "The model catalog exceeds the size limit.")
		return
	}
	for _, model := range models {
		if !catalogID.MatchString(model.ID) {
			jsonError(w, http.StatusBadGateway, "The provider returned an invalid model ID.")
			return
		}
	}
	fetchedAt := time.Time{}
	if complete {
		fetchedAt = time.Now().UTC()
	}
	data, err := json.Marshal(nativeCachedCatalog{SchemaVersion: 1, OrgID: scope, Models: models, FetchedAt: fetchedAt})
	if err != nil || len(data) > 4<<20 {
		jsonError(w, http.StatusBadGateway, "The model catalog exceeds the size limit.")
		return
	}
	if err := atomicCatalogFile(filepath.Join(a.dir, "model-catalog.json"), data); err != nil {
		jsonError(w, http.StatusInternalServerError, "Cannot save the private model catalog.")
		return
	}
	jsonResponse(w, http.StatusOK, headlessCatalogState{Models: models, Revision: fetchedRevision, Complete: complete, FetchedAt: fetchedAt, Warnings: append([]string(nil), a.catalogWarnings...)})
}

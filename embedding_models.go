package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

type embeddingModelInfo struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Dimension      *int     `json:"dimension,omitempty"`
	DimensionMode  string   `json:"dimensionMode,omitempty"`
	Note           string   `json:"note,omitempty"`
	ScoreThreshold *float64 `json:"scoreThreshold,omitempty"`
}

type embeddingCatalog struct {
	DefaultModel string               `json:"defaultModel"`
	Models       []embeddingModelInfo `json:"models"`
	Aliases      map[string]string    `json:"aliases"`
}

type embeddingCatalogError struct {
	Status                    int
	Code, Message, RetryAfter string
}

func (e *embeddingCatalogError) Error() string { return e.Message }

func embeddingCatalogFailure(status int, code, message string) *embeddingCatalogError {
	return &embeddingCatalogError{Status: status, Code: code, Message: message}
}

func writeEmbeddingCatalogError(w http.ResponseWriter, err error) {
	var e *embeddingCatalogError
	if !errors.As(err, &e) {
		e = embeddingCatalogFailure(502, "embedding_catalog_unavailable", "Kilo embedding model discovery is unavailable.")
	}
	if len(e.RetryAfter) <= 256 && !strings.ContainsAny(e.RetryAfter, "\r\n") && e.RetryAfter != "" {
		w.Header().Set("Retry-After", e.RetryAfter)
	}
	embeddingError(w, e.Status, e.Code, e.Message)
}

func embeddingCatalogID(id string) bool {
	return id != "" && len(id) <= 256 && strings.IndexFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

func parseEmbeddingCatalog(data []byte) (embeddingCatalog, error) {
	invalid := func() (embeddingCatalog, error) {
		return embeddingCatalog{}, embeddingCatalogFailure(502, "invalid_embedding_catalog", "Kilo returned an invalid embedding model catalog.")
	}
	var source struct {
		DefaultModel string                `json:"defaultModel"`
		Models       *[]embeddingModelInfo `json:"models"`
		Aliases      map[string]string     `json:"aliases"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if d.Decode(&source) != nil || source.Models == nil || len(*source.Models) > 10000 || len(source.Aliases) > 10000 {
		return invalid()
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return invalid()
	}
	seen := make(map[string]bool, len(*source.Models))
	for i := range *source.Models {
		m := &(*source.Models)[i]
		if !embeddingCatalogID(m.ID) || seen[m.ID] {
			return invalid()
		}
		seen[m.ID] = true
		if m.Dimension != nil && (*m.Dimension < 1 || *m.Dimension > 1000000) {
			return invalid()
		}
		if m.ScoreThreshold != nil && (*m.ScoreThreshold < 0 || *m.ScoreThreshold > 1) {
			return invalid()
		}
		m.Name = strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, m.Name))
		if m.Name == "" {
			m.Name = m.ID
		}
		if runes := []rune(m.Name); len(runes) > 200 {
			m.Name = string(runes[:200])
		}
	}
	if source.DefaultModel != "" && !seen[source.DefaultModel] {
		return invalid()
	}
	for alias, id := range source.Aliases {
		if !embeddingCatalogID(alias) || !seen[id] {
			return invalid()
		}
	}
	if source.Aliases == nil {
		source.Aliases = make(map[string]string)
	}
	return embeddingCatalog{DefaultModel: source.DefaultModel, Models: *source.Models, Aliases: source.Aliases}, nil
}

func fetchEmbeddingCatalog(ctx context.Context, base *url.URL, transport http.RoundTripper, key, orgID string) (embeddingCatalog, error) {
	if status, code, message := validateEmbeddingProvider("", key, orgID); status != 0 {
		return embeddingCatalog{}, embeddingCatalogFailure(status, code, message)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	endpoint := *base
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/embedding-models"
	endpoint.RawPath = ""
	endpoint.RawQuery = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return embeddingCatalog{}, embeddingCatalogFailure(502, "embedding_catalog_unavailable", "Kilo embedding model discovery is unavailable.")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("X-KiloCode-OrganizationId", orgID)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Kilo-Local/"+version)
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	transportFailure := func() (embeddingCatalog, error) {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return embeddingCatalog{}, embeddingCatalogFailure(504, "embedding_catalog_timeout", "Kilo embedding model discovery timed out.")
		}
		return embeddingCatalog{}, embeddingCatalogFailure(502, "embedding_catalog_unavailable", "Kilo embedding model discovery is unavailable.")
	}
	if err != nil {
		return transportFailure()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode <= 599 {
		e := embeddingCatalogFailure(resp.StatusCode, "embedding_catalog_rejected", fmt.Sprintf("Kilo rejected embedding model discovery (HTTP %d).", resp.StatusCode))
		e.RetryAfter = resp.Header.Get("Retry-After")
		return embeddingCatalog{}, e
	}
	if resp.StatusCode != http.StatusOK {
		return embeddingCatalog{}, embeddingCatalogFailure(502, "embedding_catalog_unavailable", "Kilo embedding model discovery is unavailable.")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return transportFailure()
	}
	if len(data) > 8<<20 {
		return embeddingCatalog{}, embeddingCatalogFailure(502, "embedding_catalog_too_large", "Kilo returned an embedding catalog that exceeds the size limit.")
	}
	return parseEmbeddingCatalog(data)
}

func (a *app) serveEmbeddingModels(w http.ResponseWriter, r *http.Request, key, orgID string) {
	a.mu.Lock()
	base, transport := *a.upstream, a.transport
	a.mu.Unlock()
	catalog, err := fetchEmbeddingCatalog(r.Context(), &base, transport, key, orgID)
	if err != nil {
		writeEmbeddingCatalogError(w, err)
		return
	}
	jsonResponse(w, 200, catalog)
}

func (a *app) embeddingModelsAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawPath != "" || r.URL.RawQuery != "" {
		jsonError(w, 404, "Unsupported embedding discovery path.")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		jsonError(w, 405, "Use GET for embedding model discovery.")
		return
	}
	a.mu.Lock()
	key, org, revision, base, transport := a.apiKey, a.config.OrgID, a.catalogRevision, *a.upstream, a.transport
	ready := a.kiloReadyLocked() && !a.providerChanging
	a.mu.Unlock()
	if !ready {
		status, code, message := validateEmbeddingProvider("", "", "")
		embeddingError(w, status, code, message)
		return
	}
	catalog, err := fetchEmbeddingCatalog(r.Context(), &base, transport, key, org)
	a.mu.Lock()
	changed := a.catalogRevision != revision || a.apiKey != key || a.config.OrgID != org || !a.kiloReadyLocked() || a.providerChanging
	a.mu.Unlock()
	if changed {
		writeEmbeddingCatalogError(w, embeddingCatalogFailure(409, "embedding_connection_changed", "The Kilo connection changed. Retry embedding model discovery."))
		return
	}
	if err != nil {
		writeEmbeddingCatalogError(w, err)
		return
	}
	jsonResponse(w, 200, catalog)
}

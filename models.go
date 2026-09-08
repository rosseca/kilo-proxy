package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type modelInfo struct {
	CodeModeRank     *float64 `json:"codeModeRank"`
	CodingIndex      *float64 `json:"codingIndex"`
	Speed            *float64 `json:"speed"`
	ReasoningEfforts []string `json:"reasoningEfforts,omitempty"`
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Provider         string   `json:"provider"`
	ContextWindow    int      `json:"contextWindow,omitempty"`
	MaxOutputTokens  int      `json:"maxOutputTokens,omitempty"`
	InputModalities  []string `json:"inputModalities,omitempty"`
	OutputModalities []string `json:"outputModalities,omitempty"`
	Tools            *bool    `json:"tools"`
	Reasoning        *bool    `json:"reasoning"`
	InputPrice       *float64 `json:"inputPrice"`
	OutputPrice      *float64 `json:"outputPrice"`
	MayTrain         *bool    `json:"mayTrain"`
	ExpirationDate   string   `json:"expirationDate,omitempty"`
}
type catalogError struct {
	status  int
	message string
}

func (e *catalogError) Error() string { return e.message }
func catalogFailure(w http.ResponseWriter, err error) {
	var problem *catalogError
	if errors.As(err, &problem) {
		jsonError(w, problem.status, problem.message)
		return
	}
	jsonError(w, 502, "Kilo no responde. Comprueba tu conexión.")
}

func (a *app) fetchModels(ctx context.Context, requireAccount bool) ([]modelInfo, uint64, error) {
	a.mu.Lock()
	key, org, revision, base := a.apiKey, a.config.OrgID, a.catalogRevision, *a.upstream
	pending := a.authPending()
	a.mu.Unlock()
	if pending {
		return nil, revision, &catalogError{409, "Espera a que termine el login para cargar los modelos."}
	}
	if requireAccount && (key == "" || org == "") {
		return nil, revision, &catalogError{400, errMissingCredentials.Error()}
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, "GET", base.String(), nil)
	if err != nil {
		return nil, revision, err
	}
	if key != "" && org != "" {
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("X-KiloCode-OrganizationId", org)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Kilo-Local/"+version)
	client := &http.Client{Transport: a.transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, revision, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, revision, &catalogError{502, fmt.Sprintf("Kilo devolvió HTTP %d. Revisa la clave y la organización.", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return nil, revision, err
	}
	if len(body) > 8<<20 {
		return nil, revision, &catalogError{502, "El catálogo de Kilo supera el tamaño permitido."}
	}
	models, err := parseModels(body)
	if err != nil {
		return nil, revision, &catalogError{502, "Kilo devolvió un catálogo no válido."}
	}
	// Public rankings enrich available gateway models; they never determine
	// availability, model identity, organization pricing, or capabilities.
	metrics := a.publicModelMetrics(ctx)
	for i := range models {
		if metric, ok := metrics[models[i].ID]; ok {
			models[i].CodeModeRank = metric.codeModeRank
			models[i].CodingIndex = metric.codingIndex
			models[i].Speed = metric.speed
		}
	}
	a.mu.Lock()
	changed := a.catalogRevision != revision
	a.mu.Unlock()
	if changed {
		return nil, revision, &catalogError{409, "La conexión ha cambiado. Vuelve a cargar los modelos."}
	}
	return models, revision, nil
}

const modelStatsEndpoint = "https://kilo.ai/api/models/stats"
const modelStatsTimeout = 3 * time.Second

type modelMetric struct {
	codeModeRank, codingIndex, speed *float64
}

// Protected by app.mu. Maps become immutable once published to callers.
type modelStatsCache struct {
	endpoint  string
	expiresAt time.Time
	data      map[string]modelMetric
	loading   chan struct{}
}

func (a *app) publicModelMetrics(ctx context.Context) map[string]modelMetric {
	a.mu.Lock()
	endpoint := a.modelStatsURL
	if endpoint == "" && a.upstream.Scheme == "https" && a.upstream.Host == "api.kilo.ai" {
		endpoint = modelStatsEndpoint
	}
	// Synthetic/custom gateways stay self-contained unless their owner supplies
	// an explicit public metadata endpoint. No endpoint is accepted from the UI.
	if endpoint == "" {
		a.mu.Unlock()
		return nil
	}
	cache := &a.modelStatsCache
	if cache.endpoint == endpoint && time.Now().Before(cache.expiresAt) {
		data := cache.data
		a.mu.Unlock()
		return data
	}
	if cache.endpoint == endpoint && cache.loading != nil {
		pending, previous := cache.loading, cache.data
		a.mu.Unlock()
		select {
		case <-pending:
			return a.publicModelMetrics(ctx)
		case <-ctx.Done():
			return previous
		}
	}
	var previous map[string]modelMetric
	if cache.endpoint == endpoint {
		previous = cache.data
	}
	pending := make(chan struct{})
	*cache = modelStatsCache{endpoint: endpoint, data: previous, loading: pending}
	transport := a.transport
	a.mu.Unlock()

	metrics, err := fetchPublicModelMetrics(ctx, endpoint, transport)
	if err != nil {
		metrics = previous
	}
	a.mu.Lock()
	if cache.loading == pending {
		cache.data = metrics
		cache.loading = nil
		ttl := 5 * time.Minute
		if err != nil {
			ttl = 30 * time.Second
		}
		cache.expiresAt = time.Now().Add(ttl)
	}
	close(pending)
	a.mu.Unlock()
	return metrics
}

func fetchPublicModelMetrics(ctx context.Context, endpoint string, transport http.RoundTripper) (map[string]modelMetric, error) {
	ctx, cancel := context.WithTimeout(ctx, modelStatsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if req.URL.User != nil {
		return nil, errors.New("public model statistics must not contain credentials")
	}
	// This is a separate unauthenticated request, never a clone of the gateway
	// request: no personal key, organization header, local key, or cookies.
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Kilo-Proxy/"+version)
	client := &http.Client{Transport: transport, Timeout: modelStatsTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("public model statistics unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(body) > 4<<20 {
		return nil, errors.New("invalid public model statistics response")
	}
	return parsePublicModelMetrics(body)
}

func parsePublicModelMetrics(body []byte) (map[string]modelMetric, error) {
	var rows []json.RawMessage
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, err
	}
	if rows == nil || len(rows) > 10000 {
		return nil, errors.New("missing or oversized model statistics")
	}
	metrics := make(map[string]modelMetric, len(rows))
	for _, row := range rows {
		var fields map[string]json.RawMessage
		if json.Unmarshal(row, &fields) != nil || fields == nil {
			continue
		}
		var id string
		if json.Unmarshal(fields["openrouterId"], &id) != nil || id == "" || len(id) > 256 {
			continue
		}
		if _, duplicate := metrics[id]; duplicate {
			continue
		}
		if string(fields["isActive"]) == "false" {
			continue
		}
		benchmarks := modelMetadataObject(fields["benchmarks"])
		artificialAnalysis := modelMetadataObject(benchmarks["artificialAnalysis"])
		chart := modelMetadataObject(fields["chartData"])
		rankings := modelMetadataObject(chart["modeRankings"])
		metric := modelMetric{
			codeModeRank: modelMetricNumber(rankings["code"]),
			codingIndex:  firstModelMetric(fields["codingIndex"], benchmarks["artificial_analysis_coding_index"], artificialAnalysis["codingIndex"]),
			speed:        firstModelMetric(fields["speedTokensPerSec"], benchmarks["median_output_tokens_per_second"]),
		}
		if rank := metric.codeModeRank; rank != nil && (*rank < 1 || math.Trunc(*rank) != *rank) {
			metric.codeModeRank = nil
		}
		metrics[id] = metric
	}
	return metrics, nil
}

func modelMetadataObject(raw json.RawMessage) map[string]json.RawMessage {
	var object map[string]json.RawMessage
	_ = json.Unmarshal(raw, &object)
	return object
}

func firstModelMetric(values ...json.RawMessage) *float64 {
	for _, raw := range values {
		if value := modelMetricNumber(raw); value != nil {
			return value
		}
	}
	return nil
}

func modelMetricNumber(raw json.RawMessage) *float64 {
	if len(raw) == 0 {
		return nil
	}
	value := string(raw)
	if raw[0] == '"' && json.Unmarshal(raw, &value) != nil {
		return nil
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number < 0 || math.IsNaN(number) || math.IsInf(number, 0) {
		return nil
	}
	return &number
}

func (a *app) models(w http.ResponseWriter, r *http.Request) {
	models, revision, err := a.fetchModels(r.Context(), false)
	if err != nil {
		catalogFailure(w, err)
		return
	}
	jsonResponse(w, 200, map[string]any{"models": models, "revision": revision, "fetchedAt": time.Now().UTC().Format(time.RFC3339)})
}

func parseModels(body []byte) ([]modelInfo, error) {
	var source struct {
		Data *[]struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Context int    `json:"context_length"`
			Top     struct {
				Context int `json:"context_length"`
				Output  int `json:"max_completion_tokens"`
			} `json:"top_provider"`
			Architecture struct {
				Input  []string `json:"input_modalities"`
				Output []string `json:"output_modalities"`
			} `json:"architecture"`
			OpenCode   json.RawMessage            `json:"opencode"`
			Parameters []string                   `json:"supported_parameters"`
			Pricing    map[string]json.RawMessage `json:"pricing"`
			MayTrain   *bool                      `json:"mayTrainOnYourPrompts"`
			Expiration string                     `json:"expiration_date"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &source); err != nil {
		return nil, err
	}
	if source.Data == nil || len(*source.Data) > 10000 {
		return nil, errors.New("missing or oversized model list")
	}
	result := make([]modelInfo, 0, len(*source.Data))
	seen := map[string]bool{}
	for _, raw := range *source.Data {
		if raw.ID == "" || len(raw.ID) > 256 || strings.IndexFunc(raw.ID, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 || seen[raw.ID] {
			continue
		}
		seen[raw.ID] = true
		name := strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, raw.Name))
		if name == "" {
			name = raw.ID
		}
		if runes := []rune(name); len(runes) > 200 {
			name = string(runes[:200])
		}
		context := raw.Context
		if context <= 0 {
			context = raw.Top.Context
		}
		if context < 1024 || context > 100000000 {
			context = 0
		}
		output := raw.Top.Output
		if output <= 0 || output > 100000000 {
			output = 0
		}
		m := modelInfo{ID: raw.ID, Name: name, Provider: strings.SplitN(raw.ID, "/", 2)[0], ContextWindow: context, MaxOutputTokens: output, InputModalities: modalities(raw.Architecture.Input), OutputModalities: modalities(raw.Architecture.Output), InputPrice: tokenPrice(raw.Pricing["prompt"]), OutputPrice: tokenPrice(raw.Pricing["completion"]), MayTrain: raw.MayTrain}
		m.ReasoningEfforts = catalogReasoningEfforts(raw.OpenCode)
		if raw.Parameters != nil {
			tools, reasoning := false, false
			for _, p := range raw.Parameters {
				if p == "tools" || p == "tool_choice" {
					tools = true
				}
				if p == "reasoning" || p == "reasoning_effort" || p == "include_reasoning" {
					reasoning = true
				}
			}
			m.Tools, m.Reasoning = &tools, &reasoning
		}
		if _, err := time.Parse("2006-01-02", raw.Expiration); err == nil {
			m.ExpirationDate = raw.Expiration
		}
		result = append(result, m)
	}
	if len(*source.Data) > 0 && len(result) == 0 {
		return nil, errors.New("no valid models")
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}
func modalities(raw []string) []string {
	var result []string
	for _, s := range raw {
		switch s {
		case "text", "image", "audio", "video", "pdf":
			result = append(result, s)
		}
	}
	return result
}
func tokenPrice(raw json.RawMessage) *float64 {
	if len(raw) == 0 {
		return nil
	}
	text := string(raw)
	if raw[0] == '"' {
		if json.Unmarshal(raw, &text) != nil {
			return nil
		}
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || n < 0 || math.IsNaN(n) || math.IsInf(n, 0) || n > 1000 {
		return nil
	}
	n *= 1000000
	return &n
}

// Read only explicit effort values, never arbitrary variant names or budgets.
// Malformed optional metadata must not discard the rest of the model catalog.
func catalogReasoningEfforts(raw json.RawMessage) []string {
	var metadata struct {
		Variants map[string]json.RawMessage `json:"variants"`
	}
	if json.Unmarshal(raw, &metadata) != nil {
		return nil
	}
	found := map[string]bool{}
	for _, variant := range metadata.Variants {
		var value struct {
			Reasoning struct {
				Effort  string `json:"effort"`
				Enabled *bool  `json:"enabled"`
			} `json:"reasoning"`
		}
		if json.Unmarshal(variant, &value) != nil {
			continue
		}
		effort := value.Reasoning.Effort
		if !effortNames[effort] {
			continue
		}
		if value.Reasoning.Enabled != nil && !*value.Reasoning.Enabled && effort != "none" {
			continue
		}
		found[effort] = true
	}
	var levels []string
	for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"} {
		if found[effort] {
			levels = append(levels, effort)
		}
	}
	return levels
}

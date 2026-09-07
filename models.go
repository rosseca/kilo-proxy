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
	a.mu.Lock()
	changed := a.catalogRevision != revision
	a.mu.Unlock()
	if changed {
		return nil, revision, &catalogError{409, "La conexión ha cambiado. Vuelve a cargar los modelos."}
	}
	return models, revision, nil
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

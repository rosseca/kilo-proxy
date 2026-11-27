package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type t3LiteLLMCatalog struct {
	Models       []modelInfo
	Families     map[string]string
	AccountScope string
}

type t3LiteLLMAccessGroup struct {
	Name   string   `json:"access_group_name"`
	Models []string `json:"access_model_names"`
}

type t3LiteLLMDeployment struct {
	Name   string `json:"model_name"`
	Params struct {
		Model    string `json:"model"`
		Provider string `json:"custom_llm_provider"`
	} `json:"litellm_params"`
	Info struct {
		Metadata map[string]json.RawMessage `json:"custom_metadata"`
	} `json:"model_info"`
}

func (a *app) loadT3LiteLLMCatalog(ctx context.Context) (t3LiteLLMCatalog, error) {
	a.mu.Lock()
	connection, ready := a.liteLLMConnectionLocked()
	a.mu.Unlock()
	if !ready {
		return t3LiteLLMCatalog{}, errors.New("Connect and enable LiteLLM in Accounts before choosing T3 Code models.")
	}
	connection, err := a.liteLLMRequestCredential(ctx, connection)
	if err != nil {
		return t3LiteLLMCatalog{}, err
	}
	models, err := a.fetchLiteLLMModels(ctx, connection)
	if err != nil {
		return t3LiteLLMCatalog{}, err
	}
	var groups []t3LiteLLMAccessGroup
	if !a.fetchT3LiteLLMJSON(ctx, connection, "/v1/access_group", nil, &groups) {
		groups = nil
	}
	prefix := liteLLMNamespace(connection.Base)
	var families map[string]string
	if len(groups) == 0 && ctx.Err() == nil {
		// A caller with no listed access groups may still be able to read
		// its own deployment metadata.
		var rows struct {
			Data []t3LiteLLMDeployment `json:"data"`
		}
		if a.fetchT3LiteLLMJSON(ctx, connection, "/v1/model/info", url.Values{"include_team_models": {"true"}}, &rows) {
			families = t3LiteLLMDeploymentFamilies(models, rows.Data, prefix)
		}
	}
	if families == nil {
		families = t3LiteLLMGroupFamilies(models, groups, prefix)
	}
	if err := ctx.Err(); err != nil {
		return t3LiteLLMCatalog{}, err
	}
	scope := liteLLMAccountScope(connection)
	a.mu.Lock()
	current, ready := a.liteLLMConnectionLocked()
	a.mu.Unlock()
	if !ready || liteLLMAccountScope(current) != scope {
		return t3LiteLLMCatalog{}, errors.New("The LiteLLM connection changed. Try again.")
	}
	return t3LiteLLMCatalog{
		Models:       models,
		Families:     families,
		AccountScope: scope,
	}, nil
}

func (a *app) fetchT3LiteLLMJSON(ctx context.Context, connection liteLLMConnection, route string, query url.Values, result any) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	endpoint := liteLLMAPIURL(connection.Base, route)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Kilo-Local/"+version)
	if connection.Key != "" {
		req.Header.Set("Authorization", "Bearer "+connection.Key)
	}
	client := &http.Client{Transport: a.transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(req)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	const limit = 2 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	return err == nil && len(data) <= limit && json.Unmarshal(data, result) == nil
}

func t3LiteLLMVisibleIDs(models []modelInfo) map[string]bool {
	visible := make(map[string]bool, len(models))
	for _, model := range models {
		visible[model.ID] = true
	}
	return visible
}

func t3LiteLLMGroupFamilies(models []modelInfo, groups []t3LiteLLMAccessGroup, prefix string) map[string]string {
	visible := t3LiteLLMVisibleIDs(models)
	families := make(map[string]string)
	for _, group := range groups {
		var family string
		switch strings.ToLower(strings.TrimSpace(group.Name)) {
		case "gpt", "openai":
			family = "openai"
		case "claude", "anthropic":
			family = "claude"
		default:
			continue
		}
		for _, alias := range group.Models {
			id := prefix + alias
			if !visible[id] {
				continue
			}
			t3LiteLLMRecordFamily(families, id, family)
		}
	}
	return t3LiteLLMKnownFamilies(families)
}

func t3LiteLLMDeploymentFamilies(models []modelInfo, deployments []t3LiteLLMDeployment, prefix string) map[string]string {
	visible := t3LiteLLMVisibleIDs(models)
	families := make(map[string]string)
	for _, deployment := range deployments {
		id := prefix + deployment.Name
		if visible[id] {
			t3LiteLLMRecordFamily(families, id, deployment.family())
		}
	}
	return t3LiteLLMKnownFamilies(families)
}

func (d t3LiteLLMDeployment) family() string {
	target, targetFamily := d.Params.Model, ""
	for _, prefix := range []string{"bedrock/", "bedrock_converse/"} {
		if model, ok := strings.CutPrefix(target, prefix); ok {
			target = model
			break
		}
	}
	if target != d.Params.Model || d.Params.Provider == "bedrock" || d.Params.Provider == "bedrock_converse" {
		target = strings.TrimPrefix(strings.TrimPrefix(target, "converse/"), "invoke/")
		if region, model, ok := strings.Cut(target, "."); ok && (region == "global" || region == "us" || region == "eu") {
			target = model
		}
		switch {
		case strings.HasPrefix(target, "openai.gpt-"):
			targetFamily = "openai"
		case strings.HasPrefix(target, "anthropic.claude-"):
			targetFamily = "claude"
		}
	}
	value, declared := d.Info.Metadata["kilo_model_family"]
	if !declared {
		return targetFamily
	}
	var family string
	if json.Unmarshal(value, &family) != nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(family)) {
	case "openai":
		family = "openai"
	case "anthropic", "claude":
		family = "claude"
	default:
		return ""
	}
	if targetFamily != "" && targetFamily != family {
		return ""
	}
	return family
}

func t3LiteLLMRecordFamily(families map[string]string, id, family string) {
	if previous, ok := families[id]; ok && previous != family {
		// Keep ambiguous aliases unknown even if a later row repeats them.
		families[id] = ""
	} else {
		families[id] = family
	}
}

func t3LiteLLMKnownFamilies(families map[string]string) map[string]string {
	for id, family := range families {
		if family == "" {
			delete(families, id)
		}
	}
	return families
}

package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
)

type t3CodeRequestContextKey struct{}
type t3LiteLLMRequestScopeKey struct{}
type t3LiteLLMRequestAccountKey struct{}
type t3LiteLLMRequestModelKey struct{}

const t3LiteLLMProxyPrefix = "/t3-code/litellm/"

func t3LiteLLMProxyToken(localKey, scope string) string {
	mac := hmac.New(sha256.New, []byte(localKey))
	mac.Write([]byte("kilo-t3-litellm-v1:" + scope))
	return hex.EncodeToString(mac.Sum(nil))
}

func t3LiteLLMProxyScope(token, localKey string) string {
	if localKey == "" {
		return ""
	}
	for _, scope := range []string{"codex", "claude"} {
		if secureEqual(token, t3LiteLLMProxyToken(localKey, scope)) {
			return scope
		}
	}
	return ""
}

func t3LiteLLMInferencePath(r *http.Request, scope string) (string, bool) {
	if scope != "codex" && scope != "claude" || r.URL.RawPath != "" || r.URL.ForceQuery {
		return "", false
	}
	base := t3LiteLLMProxyPrefix + scope + "/v1/"
	if r.Method == http.MethodGet && r.URL.Path == base+"models" && r.URL.RawQuery == "" {
		return "/v1/models", true
	}
	if r.Method == http.MethodPost {
		if scope == "codex" && r.URL.Path == base+"responses" && r.URL.RawQuery == "" {
			return "/v1/responses", true
		}
		if scope == "claude" && r.URL.Path == base+"messages" && (r.URL.RawQuery == "" || r.URL.RawQuery == "beta=true") {
			return "/v1/messages", true
		}
	}
	return "", false
}

func t3CodeInferencePath(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == "/t3-code/v1/messages" && r.URL.RawPath == "" && (r.URL.RawQuery == "" || r.URL.RawQuery == "beta=true") && !r.URL.ForceQuery
}

func t3CodeResolveModel(library modelLibrary, routes map[string]string, requested string) (string, error) {
	if err := validateT3CodeClaudeRoutes(library, routes); err != nil {
		return "", err
	}
	for _, model := range library.Models {
		if model.ID == requested {
			return requested, nil
		}
	}
	if target := routes[requested]; target != "" {
		return target, nil
	}
	return "", errors.New("This model is not selected for Claude · Kilo in the prepared T3 Code profile. Choose an available model or prepare T3 Code again.")
}

func (a *app) prepareT3CodeRequest(r *http.Request) (*http.Request, error) {
	managed, _ := r.Context().Value(t3CodeRequestContextKey{}).(bool)
	scope, _ := r.Context().Value(t3LiteLLMRequestScopeKey{}).(string)
	if !managed && scope == "" {
		return r, nil
	}
	if r.Body == nil {
		return r, errors.New("Invalid T3 Code request JSON.")
	}
	raw, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		return r, err
	}
	restore := func(data []byte) {
		r.Body = io.NopCloser(bytes.NewReader(data))
		r.GetBody = nil
		r.ContentLength = int64(len(data))
		r.Header.Del("Content-Length")
	}
	restore(raw)
	request, err := decodeClaudeDesktopObject(raw)
	if err != nil {
		return r, errors.New("T3 Code requests must contain valid JSON with unique keys.")
	}
	var requested string
	if json.Unmarshal(request["model"], &requested) != nil || requested == "" {
		return r, errors.New("Choose a model in the prepared T3 Code selection.")
	}
	saved, err := a.t3CodePreparedForRequest()
	if err != nil {
		return r, err
	}
	if scope != "" {
		library, err := a.t3LiteLLMPreparedLibrary(saved, scope)
		if err != nil {
			return r, err
		}
		for _, model := range library.Models {
			if model.ID == requested {
				ctx := context.WithValue(r.Context(), t3LiteLLMRequestAccountKey{}, saved.LiteLLMAccount)
				ctx = context.WithValue(ctx, t3LiteLLMRequestModelKey{}, requested)
				return r.Clone(ctx), nil
			}
		}
		return r, errors.New("Choose a model in the prepared LiteLLM selection for this T3 Code account.")
	}
	model, err := t3CodeResolveModel(saved.ClaudeLibrary, saved.ClaudeRoutes, requested)
	if err != nil {
		return r, err
	}
	if model == requested {
		return r, nil
	}
	request["model"], _ = json.Marshal(model)
	encoded, err := json.Marshal(request)
	if err != nil {
		return r, err
	}
	// Only the native client-facing response uses the selected slug. Upstream
	// requests, accounting and traces retain the exact prepared gateway model.
	r = r.Clone(context.WithValue(r.Context(), claudeDesktopAliasContextKey{}, claudeDesktopAliasRoute{Alias: requested, Model: model}))
	restore(encoded)
	if observer, _ := r.Context().Value(usageContextKey{}).(*usageObserver); observer != nil {
		observer.mu.Lock()
		observer.usage.Model = model
		observer.mu.Unlock()
	}
	return r, nil
}

func (a *app) t3CodePreparedForRequest() (t3CodePrepared, error) {
	a.mu.Lock()
	input, rt := a.t3CodeValidationInputLocked(), a.launchRuntime()
	a.mu.Unlock()

	// A validation uses one coherent set of resolved routes even when several
	// profile planners ask for the same executable. Re-resolve every observed
	// route before accepting the result so a concurrent selection is stale.
	type routeKey struct{ client, custom string }
	type routeResult struct {
		path string
		err  error
	}
	routes := map[routeKey]routeResult{}
	resolve := rt.resolve
	rt.resolve = func(client, custom string) (string, error) {
		key := routeKey{client, custom}
		result, ok := routes[key]
		if !ok {
			result.path, result.err = resolve(client, custom)
			routes[key] = result
		}
		return result.path, result.err
	}
	binary, resolveErr := rt.resolve("t3-code", "")
	saved, readErr := readT3CodePreparedAt(input.Dir)
	ready := resolveErr == nil && readErr == nil
	if ready {
		installation, inspectErr := inspectT3CodeInstallation(binary, rt.platform)
		ready = inspectErr == nil && t3CodeReadyFrom(input, saved, binary, installation, input.catalog(), rt, true)
	}
	if ready {
		for key, previous := range routes {
			path, err := resolve(key.client, key.custom)
			if path != previous.path || (err == nil) != (previous.err == nil) || err != nil && err.Error() != previous.err.Error() {
				ready = false
				break
			}
		}
	}
	a.mu.Lock()
	current := a.t3CodeValidationInputLocked()
	a.mu.Unlock()
	ready = ready && reflect.DeepEqual(input, current)
	if !ready {
		return t3CodePrepared{}, errors.New("Prepare T3 Code again before using its managed models.")
	}
	return saved, nil
}

func (a *app) t3LiteLLMPreparedLibrary(saved t3CodePrepared, scope string) (modelLibrary, error) {
	fail := errors.New("Prepare T3 Code again with this LiteLLM connection and model selection.")
	var library modelLibrary
	switch {
	case scope == "codex" && saved.Preferences.LiteLLMCodex:
		library = saved.LiteLLMCodexLibrary
	case scope == "claude" && saved.Preferences.LiteLLMClaude:
		library = saved.LiteLLMClaudeLibrary
	default:
		return library, fail
	}
	a.mu.Lock()
	connection, ready := a.liteLLMConnectionLocked()
	a.mu.Unlock()
	if !ready || saved.LiteLLMAccount != liteLLMAccountScope(connection) || len(library.Models) == 0 {
		return modelLibrary{}, fail
	}
	prefix := liteLLMNamespace(connection.Base)
	for _, model := range library.Models {
		if alias, matches := strings.CutPrefix(model.ID, prefix); !matches || alias == "" {
			return modelLibrary{}, fail
		}
	}
	return library, nil
}

func (a *app) serveT3LiteLLMModels(w http.ResponseWriter, scope string) {
	saved, err := a.t3CodePreparedForRequest()
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	library, err := a.t3LiteLLMPreparedLibrary(saved, scope)
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	models := make([]map[string]any, 0, len(library.Models))
	for _, model := range library.Models {
		name := model.DisplayName
		if name == "" {
			name = model.ID
		}
		models = append(models, map[string]any{"id": model.ID, "object": "model", "type": "model", "owned_by": "litellm", "name": name, "display_name": name, "is_default": model.ID == library.DefaultModel})
	}
	jsonResponse(w, http.StatusOK, map[string]any{"object": "list", "data": models, "has_more": false})
}

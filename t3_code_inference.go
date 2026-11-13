package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
)

type t3CodeRequestContextKey struct{}

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
	if managed, _ := r.Context().Value(t3CodeRequestContextKey{}).(bool); !managed {
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
		return r, errors.New("Prepare T3 Code again before using its managed models.")
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

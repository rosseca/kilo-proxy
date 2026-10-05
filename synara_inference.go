package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
)

type synaraRequestContextKey struct{}

func synaraInferencePath(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == "/synara/v1/messages" && r.URL.RawPath == "" && (r.URL.RawQuery == "" || r.URL.RawQuery == "beta=true") && !r.URL.ForceQuery
}

// Synara's built-in picker retains native Claude IDs alongside custom gateway
// IDs. Only a unique, version-qualified selected model may acquire that native
// spelling; aliases do not leak into the general /v1 API or normal accounts.
var synaraClaudeID = regexp.MustCompile(`^anthropic/(claude-(?:opus|sonnet|haiku|fable)-[0-9]+(?:[.-][0-9]+)?(?:-[0-9]{8})?)$`)

func synaraResolveModel(library modelLibrary, requested string) (string, error) {
	for _, item := range library.Models {
		if item.ID == requested {
			return requested, nil
		}
	}
	target := ""
	for _, item := range library.Models {
		match := synaraClaudeID.FindStringSubmatch(item.ID)
		if match == nil || strings.ReplaceAll(match[1], ".", "-") != requested {
			continue
		}
		if target != "" {
			return "", errors.New("This Claude model matches multiple prepared gateway IDs. Choose the exact ID in the Synara picker.")
		}
		target = item.ID
	}
	if target == "" {
		return "", errors.New("This model is not selected in the prepared Synara profile. Choose a shared gateway model or prepare Synara again.")
	}
	return target, nil
}

func (a *app) prepareSynaraRequest(r *http.Request) (*http.Request, error) {
	if managed, _ := r.Context().Value(synaraRequestContextKey{}).(bool); !managed {
		return r, nil
	}
	if r.Body == nil {
		return r, errors.New("Invalid Synara request JSON.")
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
		return r, errors.New("Synara requests must contain valid JSON with unique keys.")
	}
	var requested string
	if json.Unmarshal(request["model"], &requested) != nil {
		return r, errors.New("Choose a model in the prepared Synara selection.")
	}
	a.mu.Lock()
	rt := a.launchRuntime()
	binary, resolveErr := rt.resolve("synara", "")
	saved, readErr := a.readSynaraPrepared()
	ready := resolveErr == nil && readErr == nil && a.synaraReady(saved, binary, rt)
	a.mu.Unlock()
	if !ready {
		return r, errors.New("Prepare Synara again before using its managed models.")
	}
	model, err := synaraResolveModel(saved.Library, requested)
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
	// The existing Messages adapter restores the native ID only on the client
	// response. Gateway traces/accounting retain the exact selected provider ID.
	r = r.Clone(context.WithValue(r.Context(), claudeDesktopAliasContextKey{}, claudeDesktopAliasRoute{Alias: requested, Model: model}))
	restore(encoded)
	if observer, _ := r.Context().Value(usageContextKey{}).(*usageObserver); observer != nil {
		observer.mu.Lock()
		observer.usage.Model = model
		observer.mu.Unlock()
	}
	return r, nil
}

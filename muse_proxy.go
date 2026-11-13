package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
)

type museRequestScopeKey struct{}

// Scope credentials are derived from the existing local key, so rotating that
// key revokes them. They never authorize the general proxy or reach upstream.
func museProxyToken(localKey, scope string) string {
	mac := hmac.New(sha256.New, []byte(localKey))
	mac.Write([]byte("kilo-muse-v1:" + scope))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *app) museScopeLibrary(scope string) (modelLibrary, []modelInfo, error) {
	fail := errors.New("Prepare Muse Code again in Kilo Proxy before using this connection.")
	state := a.modelLibrary.snapshot()
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.connectionReadyLocked() {
		return modelLibrary{}, nil, fail
	}
	rt := a.launchRuntime()
	catalog := readNativeCatalogCache(a.dir, a.catalogScopeLocked())
	switch scope {
	case "terminal":
		if state.RecoveryRequired || state.Warning != "" {
			return modelLibrary{}, nil, fail
		}
		if normalizeMusePreferences(a.config.MuseCode).Connection != "kilo" {
			return modelLibrary{}, nil, fail
		}
		binary, err := rt.resolve("muse", "")
		if err != nil || a.museTerminalPrepared(binary, state.Library) != nil {
			return modelLibrary{}, nil, fail
		}
		return state.Library, catalog, nil
	case "t3":
		if !t3CodeMuseKiloEnabled(a.config.T3Code) {
			return modelLibrary{}, nil, fail
		}
		saved, err := a.readT3CodePrepared()
		library := state.Library
		if !t3CodeNeedsSavedLibrary(a.config.T3Code) {
			library = t3CodeOptionalSavedLibrary(state)
		} else if state.RecoveryRequired || state.Warning != "" {
			return modelLibrary{}, nil, fail
		}
		if err != nil || !reflect.DeepEqual(saved.Library, library) {
			return modelLibrary{}, nil, fail
		}
		binary, err := rt.resolve("t3-code", "")
		if err != nil || !a.t3CodeReady(saved, binary, rt) {
			return modelLibrary{}, nil, fail
		}
		return saved.MuseLibrary, catalog, nil
	}
	return modelLibrary{}, nil, fail
}

func museProxyScope(bearer, localKey string) string {
	if localKey == "" {
		return ""
	}
	for _, scope := range []string{"terminal", "t3"} {
		if secureEqual(bearer, museProxyToken(localKey, scope)) {
			return scope
		}
	}
	return ""
}

func museInferencePath(r *http.Request, scope string) bool {
	path := map[string]string{"terminal": "/muse/v1/responses", "t3": "/t3-code/muse/v1/responses"}[scope]
	return path != "" && r.Method == http.MethodPost && r.URL.Path == path && r.URL.RawPath == "" && r.URL.RawQuery == ""
}

// Muse's configured catalogue remains authoritative. Discovery has a distinct
// origin-root URL in supported Muse versions; never substitute a remote or
// broader catalogue that would lose the saved reasoning/default selection.
func museCatalogPath(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/muse-code/models" && r.URL.RawPath == "" && r.URL.RawQuery == ""
}

func prepareMuseModelRequest(r *http.Request, library modelLibrary, catalog []modelInfo) error {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	var body map[string]json.RawMessage
	if museManagedDecode(data, &body) != nil || body == nil {
		return errors.New("Muse Code returned an invalid Responses request.")
	}
	var id string
	if json.Unmarshal(body["model"], &id) != nil {
		return errors.New("Choose a model from the prepared Muse Code selection.")
	}
	var selected *nativeModelChoice
	for _, choice := range terminalLibraryChoices(library, catalog) {
		if choice.Model.ID == id {
			copy := choice
			selected = &copy
			break
		}
	}
	if selected == nil {
		return errors.New("Choose a model from the prepared Muse Code selection.")
	}
	levels, initial := nativeReasoningFor(*selected)
	var reasoning map[string]json.RawMessage
	if raw, present := body["reasoning"]; present && !bytes.Equal(raw, []byte("null")) {
		if json.Unmarshal(raw, &reasoning) != nil || reasoning == nil {
			return errors.New("Muse Code returned an invalid reasoning selection.")
		}
	}
	if len(levels) == 0 {
		// Muse may retain its previous session effort when switching to a model
		// with an explicit empty list. Never send that effort to the provider.
		reasoning = nil
	} else {
		effort := initial
		if raw, present := reasoning["effort"]; present {
			if json.Unmarshal(raw, &effort) != nil || !helperContains(nativeReasoningLevels, effort) {
				return errors.New("Choose a reasoning level enabled for this Muse Code model.")
			}
			if !helperContains(levels, effort) {
				// Native reminder and approval helpers choose their own efforts.
				// Keep those calls within the user's enabled levels without
				// disabling helpers or changing the exact model identity.
				effort = initial
			}
		}
		if effort != "" {
			if reasoning == nil {
				reasoning = map[string]json.RawMessage{}
			}
			reasoning["effort"], _ = json.Marshal(effort)
		}
	}
	if len(reasoning) == 0 {
		delete(body, "reasoning")
	} else {
		body["reasoning"], _ = json.Marshal(reasoning)
	}
	data, err = json.Marshal(body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	return nil
}

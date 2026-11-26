package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sort"
)

const t3CodePreferencesEndpoint = "/api/clients/t3-code/preferences"

// Preferences describe the private T3 workspace. They contain no credentials,
// and changing them never edits a provider's normal home or a T3 conversation.
type t3CodePreferences struct {
	Version            int      `json:"version"`
	Codex              bool     `json:"codex"`
	Claude             bool     `json:"claude"`
	KiloCodex          bool     `json:"kiloCodex"`
	KiloClaude         bool     `json:"kiloClaude"`
	CodexModels        string   `json:"codexModels"`
	ClaudeModels       string   `json:"claudeModels"`
	Providers          []string `json:"providers"`
	OpenCode2          bool     `json:"openCode2"`
	OpenCodeConnection string   `json:"openCodeConnection"`
	MuseConnection     string   `json:"museConnection"`
	MuseModels         string   `json:"museModels"`
}

type t3CodeProviderSupport struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

var t3CodeOptionalProviders = []t3CodeProviderSupport{
	{"cursor", "Cursor"}, {"grok", "Grok"}, {"opencode", "OpenCode"},
	{"antigravity", "Antigravity"}, {"muse", "Muse Code"}, {"pi", "Pi"},
}

func normalizeT3CodePreferences(p t3CodePreferences) t3CodePreferences {
	if p.Version == 0 {
		return t3CodePreferences{Version: 1, Codex: true, Claude: true, KiloCodex: true, KiloClaude: true, CodexModels: "official", ClaudeModels: "official", Providers: []string{}, OpenCodeConnection: "native", MuseConnection: "native", MuseModels: "official"}
	}
	if p.OpenCodeConnection == "" {
		p.OpenCodeConnection = "native"
	}
	if p.MuseConnection == "" {
		p.MuseConnection = "native"
	}
	if p.MuseModels == "" {
		// Existing Kilo Muse accounts used the complete shared selection.
		// Preserve that availability; new accounts choose Meta models.
		p.MuseModels = "official"
		if p.MuseConnection == "kilo" {
			p.MuseModels = "shared"
		}
	}
	if p.Providers == nil {
		p.Providers = []string{}
	} else {
		p.Providers = append([]string{}, p.Providers...)
		sort.Strings(p.Providers)
	}
	return p
}

func validateT3CodePreferences(p t3CodePreferences) error {
	if p.Version != 1 || (p.CodexModels != "official" && p.CodexModels != "shared") || (p.ClaudeModels != "official" && p.ClaudeModels != "shared") {
		return errors.New("Invalid T3 Code provider or model settings.")
	}
	if p.OpenCodeConnection != "" && p.OpenCodeConnection != "native" && p.OpenCodeConnection != "kilo" {
		return errors.New("Choose the native OpenCode account or Kilo Proxy models.")
	}
	if p.MuseConnection != "" && p.MuseConnection != "native" && p.MuseConnection != "kilo" {
		return errors.New("Choose the native Meta account or Kilo Proxy models for Muse Code.")
	}
	if p.MuseModels != "" && p.MuseModels != "official" && p.MuseModels != "shared" {
		return errors.New("Choose Meta models or all shared models for Muse Code.")
	}
	seen := map[string]bool{}
	for _, id := range p.Providers {
		valid := id == t3CodeKiloACPOption
		for _, provider := range t3CodeOptionalProviders {
			valid = valid || id == provider.ID
		}
		if !valid || seen[id] {
			return errors.New("Choose unique supported T3 Code providers.")
		}
		seen[id] = true
	}
	if p.OpenCode2 && !seen["opencode"] {
		return errors.New("Enable OpenCode in T3 Code before choosing OpenCode 2.")
	}
	return nil
}

type t3CodePreferenceFields struct {
	OpenCodeConnection bool
	MuseConnection     bool
	MuseModels         bool
}

func decodeT3CodePreferencesDocument(raw json.RawMessage) (t3CodePreferences, t3CodePreferenceFields, error) {
	var prefs t3CodePreferences
	var present t3CodePreferenceFields
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&prefs) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return prefs, present, errors.New("Invalid T3 Code preferences.")
	}
	// Inspect raw members as well: decoding into strings alone treats null as
	// an omitted value and duplicate members can silently change a choice.
	decoder = json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return prefs, present, errors.New("Invalid T3 Code preferences.")
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return prefs, present, errors.New("Invalid T3 Code preferences.")
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return prefs, present, errors.New("Invalid T3 Code preferences.")
		}
		switch name {
		case "museModels":
			var policy string
			if json.Unmarshal(value, &policy) != nil || (policy != "official" && policy != "shared") {
				return prefs, present, errors.New("Choose Meta models or all shared models for Muse Code.")
			}
			present.MuseModels = true
		case "openCodeConnection", "museConnection":
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return prefs, present, errors.New("Choose a native account or Kilo Proxy models for each provider.")
			}
			if name == "openCodeConnection" {
				present.OpenCodeConnection = true
			} else {
				present.MuseConnection = true
			}
		}
	}
	return prefs, present, validateT3CodePreferences(prefs)
}

func (a *app) t3CodePreferencesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	var input t3CodePreferences
	var fields t3CodePreferenceFields
	if r.Method == http.MethodPost {
		var raw json.RawMessage
		if !decodeBody(w, r, &raw) {
			return
		}
		var err error
		input, fields, err = decodeT3CodePreferencesDocument(raw)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !a.launchMu.TryLock() {
			jsonError(w, http.StatusConflict, "Another agent is being prepared. Try again.")
			return
		}
		defer a.launchMu.Unlock()
	}
	// Executable discovery can scan a slow installation or PATH. Keep it out
	// of the state lock so polling and unrelated settings remain responsive.
	rt := a.launchRuntime()
	binary, resolveErr := rt.resolve("t3-code", "")
	installation, _ := inspectT3CodeInstallation(binary, rt.platform)
	a.mu.Lock()
	defer a.mu.Unlock()
	prefs := normalizeT3CodePreferences(a.config.T3Code)
	if r.Method == http.MethodPost {
		if !fields.OpenCodeConnection {
			input.OpenCodeConnection = prefs.OpenCodeConnection
		}
		if !fields.MuseConnection {
			input.MuseConnection = prefs.MuseConnection
		}
		if !fields.MuseModels {
			input.MuseModels = prefs.MuseModels
		}
		input = normalizeT3CodePreferences(input)
		if !reflect.DeepEqual(input, prefs) {
			next := a.config
			next.T3Code = input
			if err := writeSettings(a.dir, next); err != nil {
				jsonError(w, http.StatusInternalServerError, "Could not save T3 Code settings. Your previous settings are unchanged.")
				return
			}
			a.config, prefs = next, input
		}
	}
	saved, readErr := a.readT3CodePrepared()
	ready := resolveErr == nil && readErr == nil && a.t3CodeReady(saved, binary, rt)
	jsonResponse(w, http.StatusOK, map[string]any{"preferences": prefs, "requiresPreparation": !ready, "supportedProviders": installation.SupportedProviders, "openCode2Supported": installation.OpenCode2Supported, "openCodeKiloSupported": installation.OpenCodeKiloSupported, "museKiloSupported": installation.MuseKiloSupported})
}

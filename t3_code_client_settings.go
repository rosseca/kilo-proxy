package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// The packaged Claude manifests in desktop 0.0.45 and nightly
// 0.0.46-nightly.20261003.2610 contain these exact slugs. Include current and
// legacy rows: both can otherwise reach Claude Code with a non-gateway ID.
var t3CodeClaudeBuiltinModelSlugs = []string{
	"claude-opus-5-5", "claude-sonnet-5-5", "claude-fable-5-1", "claude-fable-5",
	"claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-6",
	"claude-opus-4-5", "claude-sonnet-5", "claude-sonnet-4-6", "claude-haiku-4-5",
}

var errT3CodeClientSettings = errors.New("Private T3 Code client settings are invalid; no setup changes saved.")
var t3CodeClientProviderInstanceID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

type t3CodeClientSettingsDocument struct {
	document    map[string]json.RawMessage
	settings    map[string]json.RawMessage
	preferences map[string]json.RawMessage
	claude      map[string]json.RawMessage
	wrapped     bool
	hidden      []string
	order       []string
}

func t3CodeClientSettingsObject(data []byte) (map[string]json.RawMessage, error) {
	object, err := decodeClaudeDesktopObject(data)
	if err != nil {
		return nil, errT3CodeClientSettings
	}
	return object, nil
}

func t3CodeClientSettingsStrings(object map[string]json.RawMessage, name string) ([]string, error) {
	data, exists := object[name]
	if !exists {
		return nil, nil
	}
	var values []json.RawMessage
	if json.Unmarshal(data, &values) != nil || values == nil {
		return nil, errT3CodeClientSettings
	}
	strings := make([]string, 0, len(values))
	for _, raw := range values {
		var value string
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
			return nil, errT3CodeClientSettings
		}
		strings = append(strings, value)
	}
	return strings, nil
}

func readT3CodeClientSettings(data []byte) (t3CodeClientSettingsDocument, error) {
	result := t3CodeClientSettingsDocument{
		document: map[string]json.RawMessage{}, preferences: map[string]json.RawMessage{}, claude: map[string]json.RawMessage{},
	}
	if data != nil {
		if len(data) > catalogLimit {
			return result, errT3CodeClientSettings
		}
		var err error
		result.document, err = t3CodeClientSettingsObject(data)
		if err != nil {
			return result, err
		}
	}
	result.settings = result.document
	if data, exists := result.document["settings"]; exists {
		var err error
		result.settings, err = t3CodeClientSettingsObject(data)
		if err != nil {
			return result, err
		}
		result.wrapped = true
	}
	if data, exists := result.settings["providerModelPreferences"]; exists {
		var err error
		result.preferences, err = t3CodeClientSettingsObject(data)
		if err != nil {
			return result, err
		}
	}
	// Desktop's IPC validates every instance entry before accepting this
	// document. Keep their values intact, but reject malformed preferences
	// instead of generating a policy that T3 will discard wholesale.
	for id, data := range result.preferences {
		if len(id) > 64 || !t3CodeClientProviderInstanceID.MatchString(id) {
			return result, errT3CodeClientSettings
		}
		preference, err := t3CodeClientSettingsObject(data)
		if err != nil {
			return result, err
		}
		for _, name := range []string{"hiddenModels", "modelOrder"} {
			if _, err := t3CodeClientSettingsStrings(preference, name); err != nil {
				return result, err
			}
		}
		if id == t3CodeClaudeProxyID {
			result.claude = preference
		}
	}
	var err error
	result.hidden, err = t3CodeClientSettingsStrings(result.claude, "hiddenModels")
	if err != nil {
		return result, err
	}
	result.order, err = t3CodeClientSettingsStrings(result.claude, "modelOrder")
	return result, err
}

func t3CodeClientModelPolicy(library modelLibrary, additionalBuiltins ...[]string) ([]string, []string, error) {
	if err := validateT3CodeLibrary(library); err != nil {
		return nil, nil, err
	}
	if len(library.Models) == 0 {
		return nil, nil, errors.New("Save at least one model before preparing T3 Code.")
	}
	order := []string{library.DefaultModel}
	selected := map[string]bool{library.DefaultModel: true}
	for _, model := range library.Models {
		selected[model.ID] = true
		if model.ID != library.DefaultModel {
			order = append(order, model.ID)
		}
	}
	hidden := []string{}
	seen := map[string]bool{}
	for _, builtins := range append([][]string{t3CodeClaudeBuiltinModelSlugs}, additionalBuiltins...) {
		for _, id := range builtins {
			if !strings.HasPrefix(id, "claude-") || !catalogID.MatchString(id) {
				continue
			}
			if !selected[id] && !seen[id] {
				hidden = append(hidden, id)
				seen[id] = true
			}
		}
	}
	return hidden, order, nil
}

// This file belongs to Desktop's client settings, not the server settings.
// Only Claude · Kilo's visibility/order is managed; appearance, favorites and
// all other instances' preferences retain their original JSON values.
func planT3CodeClientSettings(data []byte, library modelLibrary, additionalBuiltins ...[]string) ([]byte, error) {
	hidden, order, err := t3CodeClientModelPolicy(library, additionalBuiltins...)
	if err != nil {
		return nil, err
	}
	document, err := readT3CodeClientSettings(data)
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	for _, id := range order {
		selected[id] = true
	}
	seen := map[string]bool{}
	mergedHidden := []string{}
	for _, id := range append(document.hidden, hidden...) {
		if !selected[id] && !seen[id] {
			mergedHidden = append(mergedHidden, id)
			seen[id] = true
		}
	}
	document.claude["hiddenModels"], _ = json.Marshal(mergedHidden)
	document.claude["modelOrder"], _ = json.Marshal(order)
	document.preferences[t3CodeClaudeProxyID], _ = json.Marshal(document.claude)
	document.settings["providerModelPreferences"], _ = json.Marshal(document.preferences)
	if document.wrapped {
		document.document["settings"], _ = json.Marshal(document.settings)
	}
	encoded, err := json.MarshalIndent(document.document, "", "  ")
	if err != nil || len(encoded)+1 > catalogLimit {
		return nil, errT3CodeClientSettings
	}
	return append(encoded, '\n'), nil
}

// T3 persists other client preferences at runtime. Verify our policy rather
// than a hash of the complete client-settings document or exact hidden array.
func t3CodeClientSettingsReady(data []byte, library modelLibrary, additionalBuiltins ...[]string) bool {
	hidden, order, err := t3CodeClientModelPolicy(library, additionalBuiltins...)
	if err != nil || data == nil {
		return false
	}
	document, err := readT3CodeClientSettings(data)
	if err != nil || len(document.order) < len(order) {
		return false
	}
	hiddenSet := map[string]bool{}
	for _, id := range document.hidden {
		hiddenSet[id] = true
	}
	for _, id := range hidden {
		if !hiddenSet[id] {
			return false
		}
	}
	for i, id := range order {
		if document.order[i] != id || hiddenSet[id] {
			return false
		}
	}
	return true
}

package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func t3ClientSettingsTestLibrary() modelLibrary {
	return modelLibrary{SchemaVersion: 1, DefaultModel: "anthropic/claude-opus-5.5", Models: []modelLibraryItem{
		{ID: "anthropic/claude-sonnet-5.5"}, {ID: "anthropic/claude-opus-5.5"},
	}}
}

func t3ClientSettingsTestDocument(t *testing.T, data []byte) t3CodeClientSettingsDocument {
	t.Helper()
	document, err := readT3CodeClientSettings(data)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestT3CodeClientSettingsHideClaudeBuiltinsAndOrderSharedDefaultFirst(t *testing.T) {
	library := t3ClientSettingsTestLibrary()
	data, err := planT3CodeClientSettings(nil, library)
	if err != nil {
		t.Fatal(err)
	}
	document := t3ClientSettingsTestDocument(t, data)
	if document.wrapped || !reflect.DeepEqual(document.order, []string{library.DefaultModel, library.Models[0].ID}) {
		t.Fatalf("wrong client settings shape/order: %s", data)
	}
	if !helperContains(document.hidden, "claude-opus-5-5") || !helperContains(document.hidden, "claude-opus-4-6") || helperContains(document.hidden, library.DefaultModel) {
		t.Fatalf("current/legacy builtins must be hidden and exact gateway model visible: %s", data)
	}
	if !t3CodeClientSettingsReady(data, library) {
		t.Fatal("prepared client settings are not ready")
	}
}

func TestT3CodeClientSettingsPreserveUnmanagedValuesAndDocumentShape(t *testing.T) {
	library := t3ClientSettingsTestLibrary()
	library.Models = append(library.Models, modelLibraryItem{ID: "claude-opus-5-5"})
	settings := `{"theme":"dark","future":{"integer":9007199254740993},"providerModelPreferences":{"kilo_claude_normal":{"hiddenModels":["claude-sonnet-5"],"modelOrder":["claude-opus-5-5"]},"codex":{"future":[1,null]},"kilo_claude_proxy":{"hiddenModels":["manual/hidden","anthropic/claude-opus-5.5","claude-opus-5-5"],"modelOrder":["old/order"],"future":{"leave":true}}}}`
	for _, wrapped := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "wrapped"}[wrapped], func(t *testing.T) {
			input := []byte(settings)
			if wrapped {
				input = []byte(`{"settings":` + settings + `,"formatVersion":7,"futureDocument":"keep"}`)
			}
			before := bytes.Clone(input)
			data, err := planT3CodeClientSettings(input, library)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, input) {
				t.Fatal("planner modified input")
			}
			document := t3ClientSettingsTestDocument(t, data)
			if document.wrapped != wrapped || !helperContains(document.hidden, "manual/hidden") || helperContains(document.hidden, library.DefaultModel) || helperContains(document.hidden, "claude-opus-5-5") {
				t.Fatalf("wrong shape/visibility merge: %s", data)
			}
			previous := t3ClientSettingsTestDocument(t, input)
			for _, name := range []string{"theme", "future"} {
				if !jsonRawEquivalent(document.settings[name], previous.settings[name]) {
					t.Fatalf("unmanaged setting %s changed: %s", name, data)
				}
			}
			for _, id := range []string{t3CodeClaudeNormalID, "codex"} {
				if !jsonRawEquivalent(document.preferences[id], previous.preferences[id]) {
					t.Fatalf("normal preferences changed: %s", data)
				}
			}
			if !jsonRawEquivalent(document.claude["future"], previous.claude["future"]) || !bytes.Contains(data, []byte("9007199254740993")) {
				t.Fatalf("unknown property or exact numeric value lost: %s", data)
			}
			if wrapped && !jsonRawEquivalent(document.document["formatVersion"], previous.document["formatVersion"]) {
				t.Fatal("wrapper metadata changed")
			}
		})
	}
}

func jsonRawEquivalent(left, right json.RawMessage) bool {
	var a, b bytes.Buffer
	return json.Compact(&a, left) == nil && json.Compact(&b, right) == nil && bytes.Equal(a.Bytes(), b.Bytes())
}

func TestT3CodeClientSettingsLibraryUpdateHidesRemovedBuiltinAndReplacesOrder(t *testing.T) {
	library := t3ClientSettingsTestLibrary()
	library.DefaultModel = "claude-opus-5-5"
	library.Models = append(library.Models, modelLibraryItem{ID: library.DefaultModel})
	initial, err := planT3CodeClientSettings(nil, library)
	if err != nil {
		t.Fatal(err)
	}
	updated := t3ClientSettingsTestLibrary()
	updated.DefaultModel = updated.Models[0].ID
	data, err := planT3CodeClientSettings(initial, updated)
	if err != nil {
		t.Fatal(err)
	}
	document := t3ClientSettingsTestDocument(t, data)
	if !helperContains(document.hidden, "claude-opus-5-5") || helperContains(document.order, "claude-opus-5-5") || document.order[0] != updated.DefaultModel {
		t.Fatalf("removed builtin stayed visible/selected: %s", data)
	}
	if t3CodeClientSettingsReady(initial, updated) || !t3CodeClientSettingsReady(data, updated) {
		t.Fatal("readiness did not track the current shared selection")
	}
}

func TestT3CodeClientSettingsRejectMalformedManagedValues(t *testing.T) {
	cases := []string{
		``, ` `, `null`, `[]`, `{`, `{"settings":null}`, `{"settings":[]}`,
		`{"providerModelPreferences":null}`, `{"providerModelPreferences":[]}`,
		`{"providerModelPreferences":{"kilo_claude_proxy":null}}`,
		`{"providerModelPreferences":{"kilo_claude_proxy":[]}}`,
		`{"providerModelPreferences":{"kilo_claude_proxy":{"hiddenModels":null}}}`,
		`{"providerModelPreferences":{"kilo_claude_proxy":{"hiddenModels":["valid",null]}}}`,
		`{"providerModelPreferences":{"kilo_claude_proxy":{"hiddenModels":["valid",2]}}}`,
		`{"providerModelPreferences":{"kilo_claude_proxy":{"modelOrder":"wrong"}}}`,
		`{"providerModelPreferences":{"kilo_claude_proxy":{"modelOrder":[false]}}}`,
		`{"theme":"dark","theme":"light"}`,
		`{"settings":{"theme":"dark","theme":"light"}}`,
		`{"providerModelPreferences":{"kilo_claude_proxy":{},"kilo_claude_proxy":{}}}`,
		`{"providerModelPreferences":{"kilo_claude_proxy":{"hiddenModels":[],"hiddenModels":[]}}}`,
		`{"providerModelPreferences":{"kilo_claude_normal":{"future":{"duplicate":1,"duplicate":2}}}}`,
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			data := []byte(input)
			before := bytes.Clone(data)
			if planned, err := planT3CodeClientSettings(data, t3ClientSettingsTestLibrary()); err == nil || planned != nil {
				t.Fatalf("malformed settings accepted: %s", input)
			}
			if !bytes.Equal(data, before) || t3CodeClientSettingsReady(data, t3ClientSettingsTestLibrary()) {
				t.Fatal("malformed document modified or marked ready")
			}
		})
	}
	if _, err := planT3CodeClientSettings(nil, emptyModelLibrary()); err == nil {
		t.Fatal("empty library accepted")
	}
}

func TestT3CodeClientSettingsReadinessOnlyChecksOwnedModelPolicy(t *testing.T) {
	library := t3ClientSettingsTestLibrary()
	initial, err := planT3CodeClientSettings(nil, library)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"appearance", "normal", "extra hidden", "extra order", "show builtin", "hide shared", "wrong default"} {
		t.Run(change, func(t *testing.T) {
			document := t3ClientSettingsTestDocument(t, initial)
			ready := true
			switch change {
			case "appearance":
				document.settings["theme"] = json.RawMessage(`"light"`)
			case "normal":
				document.preferences[t3CodeClaudeNormalID] = json.RawMessage(`{"hiddenModels":["claude-opus-5-5"],"modelOrder":[]}`)
			case "extra hidden":
				document.hidden = append(document.hidden, "other/manual-model")
			case "extra order":
				document.order = append(document.order, "other/manual-model")
			case "show builtin":
				document.hidden = document.hidden[1:]
				ready = false
			case "hide shared":
				document.hidden = append(document.hidden, library.DefaultModel)
				ready = false
			case "wrong default":
				document.order[0], document.order[1] = document.order[1], document.order[0]
				ready = false
			}
			document.claude["hiddenModels"], _ = json.Marshal(document.hidden)
			document.claude["modelOrder"], _ = json.Marshal(document.order)
			document.preferences[t3CodeClaudeProxyID], _ = json.Marshal(document.claude)
			document.settings["providerModelPreferences"], _ = json.Marshal(document.preferences)
			data, _ := json.Marshal(document.document)
			if t3CodeClientSettingsReady(data, library) != ready {
				t.Fatalf("wrong readiness after %s: %s", change, data)
			}
		})
	}
}

func TestT3CodeClientSettingsRejectMalformedOtherInstancePreferences(t *testing.T) {
	library := t3ClientSettingsTestLibrary()
	initial, err := planT3CodeClientSettings(nil, library)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{t3CodeClaudeNormalID, "future_provider_instance"} {
		for _, value := range []string{
			`null`, `[]`, `"wrong"`,
			`{"hiddenModels":null}`, `{"hiddenModels":[null]}`,
			`{"hiddenModels":["valid",false]}`,
			`{"modelOrder":false}`, `{"modelOrder":["valid",2]}`,
		} {
			t.Run(id+"/"+value, func(t *testing.T) {
				document := t3ClientSettingsTestDocument(t, initial)
				document.preferences[id] = json.RawMessage(value)
				document.settings["providerModelPreferences"], _ = json.Marshal(document.preferences)
				data, _ := json.Marshal(document.document)
				before := bytes.Clone(data)
				if planned, err := planT3CodeClientSettings(data, library); err == nil || planned != nil {
					t.Fatalf("malformed other instance preferences accepted: %s", data)
				}
				if !bytes.Equal(data, before) || t3CodeClientSettingsReady(data, library) {
					t.Fatal("malformed document modified or its valid managed policy marked ready")
				}
			})
		}
	}
}

func TestT3CodeClientSettingsRejectMoreModelsThanT3Supports(t *testing.T) {
	library := emptyModelLibrary()
	for i := 0; i < t3CodeCustomModelLimit; i++ {
		library.Models = append(library.Models, modelLibraryItem{ID: "gateway/model-" + strconv.Itoa(i)})
	}
	library.DefaultModel = library.Models[0].ID
	prepared, err := planT3CodeClientSettings(nil, library)
	if err != nil || !t3CodeClientSettingsReady(prepared, library) {
		t.Fatalf("maximum supported selection rejected: %v", err)
	}
	library.Models = append(library.Models, modelLibraryItem{ID: "gateway/one-too-many"})
	if planned, err := planT3CodeClientSettings(prepared, library); err == nil || planned != nil {
		t.Fatal("selection beyond T3's model limit accepted")
	}
	if t3CodeClientSettingsReady(prepared, library) {
		t.Fatal("selection beyond T3's model limit marked ready")
	}
}

func TestT3CodeClientSettingsValidateProviderInstanceIDs(t *testing.T) {
	library := t3ClientSettingsTestLibrary()
	initial, err := planT3CodeClientSettings(nil, library)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "bad/id", "1numeric", "space id", " accented", "ClaudeÉ", strings.Repeat("a", 65), "ClaudeAgent_2-" + strings.Repeat("a", 50)} {
		t.Run(id, func(t *testing.T) {
			document := t3ClientSettingsTestDocument(t, initial)
			document.preferences[id] = json.RawMessage(`{"hiddenModels":[],"modelOrder":[],"future":true}`)
			document.settings["providerModelPreferences"], _ = json.Marshal(document.preferences)
			data, _ := json.Marshal(document.document)
			valid := id == "ClaudeAgent_2-"+strings.Repeat("a", 50)
			planned, err := planT3CodeClientSettings(data, library)
			if !valid && (err == nil || planned != nil || t3CodeClientSettingsReady(data, library)) {
				t.Fatal("invalid provider instance ID accepted")
			}
			if valid && (err != nil || !t3CodeClientSettingsReady(planned, library)) {
				t.Fatalf("valid 64-character mixed-case provider instance rejected: %v", err)
			}
			if valid && !jsonRawEquivalent(t3ClientSettingsTestDocument(t, planned).preferences[id], document.preferences[id]) {
				t.Fatal("valid unmanaged preference changed")
			}
		})
	}
}

func TestT3CodeClientSettingsMergeAdditionalBuiltinModels(t *testing.T) {
	library := t3ClientSettingsTestLibrary()
	additional := []string{"claude-future-native", "claude-opus-5-5", library.DefaultModel, "claude-future-native", "other/provider", "../../not-a-model"}
	data, err := planT3CodeClientSettings(nil, library, additional)
	if err != nil {
		t.Fatal(err)
	}
	document := t3ClientSettingsTestDocument(t, data)
	if !helperContains(document.hidden, "claude-future-native") || helperContains(document.hidden, library.DefaultModel) || helperContains(document.hidden, "other/provider") || helperContains(document.hidden, "../../not-a-model") {
		t.Fatalf("additional builtin policy is wrong: %s", data)
	}
	seen := map[string]bool{}
	for _, id := range document.hidden {
		if seen[id] {
			t.Fatalf("duplicate hidden slug: %s", id)
		}
		seen[id] = true
	}
	if !t3CodeClientSettingsReady(data, library, additional) || t3CodeClientSettingsReady(data, library, []string{"claude-newer-native"}) {
		t.Fatal("additional builtin policy ignored by readiness")
	}
}

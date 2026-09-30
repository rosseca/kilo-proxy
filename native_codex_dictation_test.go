//go:build desktop

package main

import (
	"encoding/json"
	"image"
	"testing"

	"gioui.org/io/semantic"
	"github.com/pelletier/go-toml/v2"
)

func TestNativeCodexDictationCheckbox(t *testing.T) {
	h := newNativePointerHarness(t, image.Pt(1180, 820))
	h.u.agentSetup("codex")
	h.frame()
	label := "Use ChatGPT dictation (experimental)"
	h.reveal(label, semantic.CheckBox)
	for _, enabled := range []bool{true, false} {
		h.click(label, semantic.CheckBox)
		selection := h.u.clientState().selection("codex")
		if selection.ChatGPTDictation == nil || *selection.ChatGPTDictation != enabled || h.selected(label, semantic.CheckBox) != enabled {
			t.Fatal("pointer did not update dictation preference", enabled)
		}
	}
}

func TestNativeCodexDictationLoadEditExportAndIsolation(t *testing.T) {
	u := nativeTestUI(t)
	u.state["codexChatGPTDictation"] = true
	s := u.clientState().selection("codex")
	for _, m := range nativeClientModelsForTest() {
		if err := s.add(m, 50); err != nil {
			t.Fatal(err)
		}
	}
	u.syncClientSelection("codex", s)
	if s.ChatGPTDictation == nil || !*s.ChatGPTDictation {
		t.Fatal("saved opt-in not restored")
	}
	payload, err := nativeClientPayload("codex", s)
	if err != nil || payload.(map[string]any)["chatgptDictation"] != true {
		t.Fatal(payload, err)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := decodeNativeClientSelection("codex", body, u.models)
	if err != nil || loaded.ChatGPTDictation == nil || !*loaded.ChatGPTDictation {
		t.Fatal("opt-in not loaded", err)
	}
	assertExport := func(enabled bool) {
		t.Helper()
		text, err := u.clientExport("codex", s, false)
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		if err := toml.Unmarshal([]byte(text), &config); err != nil {
			t.Fatal(err)
		}
		flag, _ := tomlAt(config, []string{"model_providers", "kilo-local", "requires_openai_auth"})
		key, _ := tomlAt(config, []string{"model_providers", "kilo-local", "env_key"})
		if flag != enabled || key != "KILO_LOCAL_API_KEY" {
			t.Fatal("unexpected export", text)
		}
	}
	assertExport(true)
	before := nativeSelectionFingerprint("codex", s, "local", "key", claudeCapabilities{})
	disabled := false
	s.ChatGPTDictation = &disabled
	u.syncClientSelection("codex", s)
	if *s.ChatGPTDictation {
		t.Fatal("state refresh overwrote unsaved edit")
	}
	if before == nativeSelectionFingerprint("codex", s, "local", "key", claudeCapabilities{}) {
		t.Fatal("dictation edit did not invalidate readiness")
	}
	assertExport(false)
	for _, key := range []string{"codex-cli", "xcode-codex"} {
		payload, err := nativeClientPayload(key, loaded)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := payload.(map[string]any)["chatgptDictation"]; exists {
			t.Fatal("desktop option leaked to", key)
		}
	}
}

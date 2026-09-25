//go:build desktop

package main

import (
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func TestNativeClaudeDesktopSharedModelsAndImport(t *testing.T) {
	u := nativeTestUI(t)
	u.models = nativeClientModelsForTest()
	nativeSeedSharedForTest(t, u, u.models...)
	u.setValue(nativeClientField(sharedModelKey, u.models[1].ID, "name"), "Daily Claude")
	u.setSharedTokenLimits(u.models[1].ID, 32000, 2048)
	s := u.sharedClientSelection("claude-desktop")
	payload, err := nativeClientPayload("claude-desktop", s)
	if err != nil {
		t.Fatal(err)
	}
	selection := payload.(editorSelection)
	if selection.Initial != u.models[1].ID || len(selection.Models) != 1 || selection.Models[0].Name != "Daily Claude" || selection.Models[0].Context != 0 || selection.Models[0].Output != 0 {
		t.Fatalf("Claude-only selection or app-managed limits were not applied: %#v", selection)
	}
	if u.library.selection.Initial != "vendor/one" || len(u.library.selection.Models) != 2 || len(u.sharedClientSelection("codex").Models) != 2 {
		t.Fatal("Desktop filtering changed the library or another agent")
	}
	if note := u.claudeDesktopSelectionNote(s); !strings.Contains(note, "1 shared model omitted") || !strings.Contains(note, "first Claude model") {
		t.Fatalf("filtering/default fallback was not explained: %s", note)
	}
	data, _ := json.Marshal(map[string]any{"selection": selection, "configPath": "temporary-Kilo.json"})
	loaded, err := decodeNativeClientSelection("claude-desktop", data, nil)
	if err != nil || loaded.Initial != selection.Initial || loaded.Models[0].DisplayName != "Daily Claude" || loaded.Path != "temporary-Kilo.json" || loaded.Saved != "" || loaded.Models[0].ContextPreset != contextPresetRecommended {
		t.Fatalf("saved Desktop configuration lost its names/default: %#v %v", loaded, err)
	}
	if u.clientCaps("claude-desktop") != (claudeCapabilities{}) {
		t.Fatal("Desktop inherited Claude Code capabilities")
	}
}

func TestNativeClaudeDesktopRejectsLibraryWithoutClaudeModels(t *testing.T) {
	u := nativeTestUI(t)
	nativeSeedSharedForTest(t, u, nativeClientModelsForTest()[0])
	s := u.sharedClientSelection("claude-desktop")
	if len(s.Models) != 0 || s.Initial != "" {
		t.Fatalf("unsupported model escaped Desktop filtering: %#v", s)
	}
	if _, err := nativeClientPayload("claude-desktop", s); err == nil {
		t.Fatal("Desktop accepted an empty compatible selection")
	}
}

func TestNativeClaudeDesktopExperimentalOptionPreservesSharedLibrary(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			u, recorder := nativeLaunchTestUI(t, "claude-desktop", false, false)
			u.language = lang
			u.owner.mu.Lock()
			u.owner.config.Language = lang
			u.owner.mu.Unlock()
			u.agentSetup("claude-desktop")
			source, _ := json.Marshal(u.library.selection)
			initial := u.library.selection.Initial
			before := u.sharedClientSelection("claude-desktop")
			base, key, _ := u.clientBase()
			fingerprint := nativeSelectionFingerprint("claude-desktop", before, base, key, claudeCapabilities{})
			if before.DesktopExperimentalModels || len(before.Models) != 1 {
				t.Fatal("experimental models should default to off")
			}
			h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1500), now: time.Now()}
			label := u.tr("Experimental: use models from other providers", "Experimental: usar modelos de otros proveedores")
			h.frame()
			h.reveal(label, semantic.CheckBox)
			h.click(label, semantic.CheckBox)
			nativeTestWait(t, u, func() bool { return !u.busy["POST/api/claude-desktop/options"] })
			u.owner.mu.Lock()
			enabled, requests := u.owner.config.ClaudeDesktopExperimentalModels, u.owner.requests
			u.owner.mu.Unlock()
			if !enabled || !u.claudeDesktopExperimentalModels() {
				t.Fatalf("experimental option was not saved: %s", u.notice)
			}
			s := u.sharedClientSelection("claude-desktop")
			payload, err := nativeClientPayload("claude-desktop", s)
			if err != nil {
				t.Fatal(err)
			}
			selection := payload.(editorSelection)
			if len(selection.Models) != len(u.library.selection.Models) || selection.Initial != initial || selection.Models[0].ID != initial || selection.Models[0].Context != 0 || selection.Models[0].Output != 0 {
				t.Fatalf("experimental selection lost real IDs, shared default or app-managed limits: %#v", selection)
			}
			if fingerprint == nativeSelectionFingerprint("claude-desktop", s, base, key, claudeCapabilities{}) {
				t.Fatal("option did not invalidate prepared selection")
			}
			if !strings.Contains(u.claudeDesktopModelSummary(s), u.tr("Experimental models", "Modelos experimentales")) {
				t.Fatal("agent card did not identify experimental models")
			}
			envelope, _ := json.Marshal(map[string]any{"selection": selection, "experimentalModels": true})
			imported, err := decodeNativeClientSelection("claude-desktop", envelope, u.models)
			if err != nil || imported == nil || !imported.DesktopExperimentalModels || imported.Initial != initial {
				t.Fatalf("experimental import lost real IDs or mode: %#v %v", imported, err)
			}
			h.frame()
			nativeGridCapture(t, h, "native-claude-desktop-experimental-settings-"+lang)
			u.page = "agents"
			h.frame()
			nativeGridCapture(t, h, "native-claude-desktop-experimental-agents-"+lang)
			u.agentSetup("claude-desktop")
			h.frame()
			h.reveal(label, semantic.CheckBox)
			h.click(label, semantic.CheckBox)
			nativeTestWait(t, u, func() bool { return !u.busy["POST/api/claude-desktop/options"] })
			s = u.sharedClientSelection("claude-desktop")
			if s.DesktopExperimentalModels || len(s.Models) != 1 || s.Initial == initial {
				t.Fatalf("disabling did not restore Claude filtering: %#v", s)
			}
			after, _ := json.Marshal(u.library.selection)
			if !reflect.DeepEqual(source, after) {
				t.Fatal("toggle changed shared library")
			}
			if requests != 0 || recorder.count() != 0 || recorder.prepares.Load() != 0 {
				t.Fatal("option toggle made inference or launch/preparation requests")
			}
			data, err := os.ReadFile(filepath.Join(u.owner.dir, "settings.json"))
			var config settings
			if err != nil || json.Unmarshal(data, &config) != nil || config.ClaudeDesktopExperimentalModels {
				t.Fatalf("option did not persist disabled: %s %v", data, err)
			}
		})
	}
}

func TestNativeClaudeDesktopExperimentalModeFingerprintAndReservedIDs(t *testing.T) {
	u := nativeTestUI(t)
	nativeSeedSharedForTest(t, u, nativeClientModelsForTest()[1])
	s := u.sharedClientSelection("claude-desktop")
	before := nativeSelectionFingerprint("claude-desktop", s, "base", "key", claudeCapabilities{})
	u.state["claudeDesktopExperimentalModels"] = true
	s = u.sharedClientSelection("claude-desktop")
	if before == nativeSelectionFingerprint("claude-desktop", s, "base", "key", claudeCapabilities{}) {
		t.Fatal("Claude-only profile readiness ignored changed experimental mode")
	}
	for _, id := range []string{"claude-kilo-v1-0123", "anthropic/claude-kilo-v1-0123", "bad id", strings.Repeat("x", 201)} {
		if nativeClaudeDesktopModelAllowed(id, true) {
			t.Fatalf("reserved or invalid ID allowed: %s", id)
		}
	}
}

func TestNativeClaudeDesktopExperimentalSaveFailureRestoresMode(t *testing.T) {
	u, recorder := nativeLaunchTestUI(t, "claude-desktop", false, false)
	// A directory at the settings-file path makes this fixture's save fail.
	path := filepath.Join(u.owner.dir, "settings.json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	u.setClaudeDesktopExperimentalModels(true)
	// A state refresh during saving can display the pending option. A failed
	// write must restore the last persisted value rather than keeping this draft.
	u.state["claudeDesktopExperimentalModels"] = true
	nativeTestWait(t, u, func() bool { return !u.busy["POST/api/claude-desktop/options"] })
	s := u.sharedClientSelection("claude-desktop")
	if s.DesktopExperimentalModels || len(s.Models) != 1 || len(u.library.selection.Models) != 2 || recorder.count() != 0 || recorder.prepares.Load() != 0 || !strings.Contains(u.notice, "Cannot safely write Kilo settings") {
		t.Fatalf("failed option save changed effective mode or launched: selection=%#v notice=%s", s, u.notice)
	}
}

func TestNativeClaudeDesktopAgentFullRowBelowCodex(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			u, recorder := nativeLaunchTestUI(t, "claude-desktop", false, false)
			u.page, u.language = "agents", lang
			u.owner.mu.Lock()
			u.owner.config.Language = lang
			u.owner.mu.Unlock()
			// An unrelated invalid project must never affect this desktop launcher.
			u.setValue(agentProjectField("claude-desktop"), "/does-not-exist/unused")
			h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1500), now: time.Now()}
			h.frame()
			codex := h.target(u.tr("Open Codex", "Abrir Codex"), semantic.Button).Desc.Bounds
			desktop := h.target(u.tr("Open Claude Desktop", "Abrir Claude Desktop"), semantic.Button).Desc.Bounds
			var desktopHeader, codeHeader image.Rectangle
			for _, node := range h.nodes() {
				if node.Desc.Label == "Claude Desktop" {
					desktopHeader = node.Desc.Bounds
				}
				if node.Desc.Label == "Claude Code" {
					codeHeader = node.Desc.Bounds
				}
			}
			if desktop.Min.X != codex.Min.X || desktop.Min.Y <= codex.Max.Y || desktopHeader.Empty() || codeHeader.Empty() || desktop.Max.Y >= codeHeader.Min.Y {
				t.Fatalf("Desktop is not a distinct row after Codex and before Claude Code: codex=%v desktop=%v desktopHeader=%v codeHeader=%v", codex, desktop, desktopHeader, codeHeader)
			}
			// The full-width card's status sits at the same right edge as Codex's.
			var installed []image.Rectangle
			for _, node := range h.nodes() {
				if node.Desc.Label == u.tr("Installed · Desktop", "Instalado · Escritorio") {
					installed = append(installed, node.Desc.Bounds)
				}
			}
			if len(installed) < 2 || installed[0].Max.X != installed[1].Max.X {
				t.Fatalf("Desktop card did not span the Codex row width: %v", installed)
			}
			if _, exists := u.buttons["agent:claude-desktop:folder"]; exists {
				t.Fatal("Desktop exposed an unsupported folder picker")
			}
			nativeGridCapture(t, h, "native-claude-desktop-agents-"+lang)
			h.click(u.tr("Open Claude Desktop", "Abrir Claude Desktop"), semantic.Button)
			nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
			if recorder.count() != 1 || recorder.prepares.Load() != 1 {
				t.Fatalf("Desktop did not prepare and launch: %s", u.notice)
			}
			if u.agentsState().Preferences.Projects["claude-desktop"] != "" {
				t.Fatal("Desktop incorrectly remembered a project folder")
			}
			u.agentSetup("claude-desktop")
			h.frame()
			h.target(u.tr("Prepare without opening", "Preparar sin abrir"), semantic.Button)
			for _, node := range h.nodes() {
				if node.Desc.Label == u.tr("Project folder", "Carpeta del proyecto") || strings.Contains(node.Desc.Label, "alias (empty") {
					t.Fatalf("Desktop exposed CLI or project settings: %s", node.Desc.Label)
				}
			}
			nativeGridCapture(t, h, "native-claude-desktop-settings-"+lang)
		})
	}
}

func TestNativeClaudeDesktopFailedPreparationNeverLaunches(t *testing.T) {
	u, recorder := nativeLaunchTestUI(t, "claude-desktop", false, true)
	u.launchAgent("claude-desktop")
	nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
	if recorder.count() != 0 || recorder.prepares.Load() != 1 || !strings.Contains(u.notice, "synthetic preparation failure") {
		t.Fatalf("failed preparation was not surfaced: %s", u.notice)
	}
}

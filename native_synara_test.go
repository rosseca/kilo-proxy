//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"image"
	"reflect"
	"strings"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func TestNativeSynaraUsesSharedLibrarySnapshot(t *testing.T) {
	u := nativeTestUI(t)
	u.models = nativeClientModelsForTest()
	library := nativeSeedSharedForTest(t, u, u.models...)
	library.Initial = u.models[1].ID
	u.setValue(nativeClientField(sharedModelKey, u.models[0].ID, "name"), "My coding model")
	u.setValue(nativeClientField(sharedModelKey, u.models[0].ID, "reasoning"), "high")
	payload, err := nativeClientPayload("synara", u.sharedClientSelection("synara"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var request struct{ Library modelLibrary }
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request.Library.DefaultModel != library.Initial || len(request.Library.Models) != 2 || request.Library.Models[0].DisplayName != "My coding model" || request.Library.Models[0].ReasoningEffort != "high" {
		t.Fatalf("shared snapshot lost choices: %s", data)
	}
	if nativeClientEndpoint("synara") != "/api/clients/synara" || strings.Contains(string(data), u.owner.apiKey) || strings.Contains(string(data), u.owner.config.LocalKey) {
		t.Fatal("wrong endpoint or credentials in shared payload")
	}
}

func TestNativeSynaraWideCardAndIntegrationSettings(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			u, recorder := nativeLaunchTestUI(t, "synara", false, false)
			nativeSeedSharedForTest(t, u, u.models...)
			// This UI fixture never executes the fake CLI. Keep Claude automatic;
			// installed-driver tests exercise versioned per-model effort separately.
			u.setValue(nativeClientField(sharedModelKey, u.models[1].ID, "reasoning"), "")
			u.persistLibraryEdits()
			u.flushModelLibrary()
			u.page, u.language = "agents", lang
			u.owner.mu.Lock()
			u.owner.config.Language = lang
			u.owner.mu.Unlock()
			u.setValue(agentProjectField("synara"), "/does-not-exist/ignored")
			h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 2200), now: time.Now()}
			h.frame()
			codex := h.target(u.tr("Open Codex", "Abrir Codex"), semantic.Button).Desc.Bounds
			t3 := h.target(u.tr("Open T3 Code", "Abrir T3 Code"), semantic.Button).Desc.Bounds
			synara := h.target(u.tr("Open Synara", "Abrir Synara"), semantic.Button).Desc.Bounds
			maus := h.target(u.tr("Open OpenMausBot", "Abrir OpenMausBot"), semantic.Button).Desc.Bounds
			// Synara leads the multi-agent group, after the desktop apps and before T3 Code and the bots.
			if synara.Max.X != codex.Max.X || synara.Min.Y <= codex.Max.Y || synara.Max.Y >= t3.Min.Y || t3.Max.Y >= maus.Min.Y {
				t.Fatalf("Synara row not in the multi-agent group: codex=%v synara=%v t3=%v maus=%v", codex, synara, t3, maus)
			}
			if _, exists := u.buttons["agent:synara:folder"]; exists {
				t.Fatal("Synara exposes an unsupported project picker")
			}
			nativeGridCapture(t, h, "native-synara-agents-"+lang)
			h.click(u.tr("Open Synara", "Abrir Synara"), semantic.Button)
			nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
			if recorder.count() != 1 || recorder.prepares.Load() != 1 {
				t.Fatalf("pointer launch did not prepare once: %s", u.notice)
			}
			if u.agentsState().Preferences.Projects["synara"] != "" {
				t.Fatal("Synara remembered an unsupported project")
			}
			u.agentsState().Synara.Info.Version = "9.4.2-beta.8"
			u.agentSetup("synara")
			h.size = image.Pt(720, 2200)
			h.frame()
			h.reveal(u.tr("Prepare", "Preparar"), semantic.Button)
			h.target(u.tr("Prepare", "Preparar"), semantic.Button)
			h.reveal(u.tr("How this works", "Cómo funciona"), semantic.Button)
			h.click(u.tr("How this works", "Cómo funciona"), semantic.Button)
			labels := ""
			for _, node := range h.nodes() {
				labels += node.Desc.Label + "\n"
			}
			for _, expected := range []string{"Codex · Normal", u.tr("Existing Codex CLI login", "Sesión existente de Codex CLI"), "Claude · Normal", u.tr("Existing Claude Code login", "Sesión existente de Claude Code"), "Kilo Proxy · Codex", "Kilo Proxy · Claude", u.tr("green account indicators", "indicadores verdes de cuenta"), u.tr("Choose one of these four options for each new chat.", "Elige una de estas cuatro opciones para cada chat nuevo."), u.tr("exact gateway ID", "ID exacto del gateway"), u.tr("check the selected model before continuing", "revisa el modelo elegido antes de continuar"), u.tr("Supports Synara Beta. Requires native Codex CLI and Claude Code.", "Admite Synara Beta. Requiere Codex CLI nativo y Claude Code."), u.tr("Detected: 9.4.2-beta.8", "Detectado: 9.4.2-beta.8"), "Claude Code 2.1.251", "keyring/auto"} {
				if !strings.Contains(labels, expected) {
					t.Fatalf("missing agent guidance: %q", expected)
				}
			}
			if strings.Contains(labels, u.owner.config.LocalKey) || strings.Contains(labels, u.owner.apiKey) || strings.Contains(labels, "\n"+u.tr("Folder", "Carpeta")+"\n") {
				t.Fatal("integration settings expose credentials or unsupported project picker")
			}
			if lang == "es" && (strings.Contains(labels, "Synara opened with") || strings.Contains(labels, "Synara prepared with")) {
				t.Fatal("backend preparation or launch notices were not translated")
			}
			nativeGridCapture(t, h, "native-synara-settings-"+lang)
		})
	}
}

func TestNativeSynaraPreparationFailureAndEditsNeverLaunch(t *testing.T) {
	for _, failure := range []string{"prepare", "library", "connection"} {
		t.Run(failure, func(t *testing.T) {
			u, recorder := nativeLaunchTestUI(t, "synara", true, failure == "prepare")
			before := append([]modelLibraryItem(nil), u.modelLibraryValue().Models...)
			u.launchAgent("synara")
			<-recorder.entered
			switch failure {
			case "library":
				u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Changed during preparation")
			case "connection":
				u.owner.mu.Lock()
				u.owner.config.OrgID = "changed-team"
				u.owner.mu.Unlock()
			}
			recorder.unblock()
			nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
			if recorder.count() != 0 || u.notice == "" {
				t.Fatalf("unsafe launch after %s: %s", failure, u.notice)
			}
			if failure != "library" && !reflect.DeepEqual(before, u.modelLibraryValue().Models) {
				t.Fatal("failed preparation changed the common library")
			}
		})
	}
}

func TestNativeSynaraPreparedStatusIsFresh(t *testing.T) {
	u := nativeTestUI(t)
	nativeSeedSharedForTest(t, u, nativeClientModelsForTest()[0])
	s := u.sharedClientSelection("synara")
	payload, _ := nativeClientPayload("synara", s)
	library := payload.(map[string]any)["library"].(modelLibrary)
	data, _ := json.Marshal(map[string]any{"prepared": true, "library": library, "version": "9.4.2-beta.8"})
	u.acceptSynaraProfile(data, u.launchConnectionFingerprint())
	if !u.synaraPrepared(s) {
		t.Fatal("valid prepared metadata was not accepted")
	}
	u.setValue(nativeClientField(sharedModelKey, s.Models[0].Model.ID, "name"), "Pending edit")
	if u.synaraPrepared(u.sharedClientSelection("synara")) {
		t.Fatal("pending library edit displays a stale prepared state")
	}
	u.acceptSynaraProfile(data, [32]byte{})
	if u.synaraPrepared(s) {
		t.Fatal("changed connection displays a stale prepared state")
	}
	u.acceptSynaraProfile(json.RawMessage(`{"prepared":`), u.launchConnectionFingerprint())
	if u.synaraPrepared(s) || u.agentsState().Synara.Error == "" {
		t.Fatal("malformed metadata kept a prepared state or omitted the refresh error")
	}
}

func TestNativeSynaraRejectsMoreThan32SharedModels(t *testing.T) {
	s := &nativeClientSelection{Initial: "vendor/model-32"}
	for i := 0; i < 33; i++ {
		s.Models = append(s.Models, nativeModelChoice{Model: modelInfo{ID: fmt.Sprintf("vendor/model-%d", i)}})
	}
	if _, err := nativeClientPayload("synara", s); err == nil {
		t.Fatal("oversized library can lose its initial Synara model")
	}
}

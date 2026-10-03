//go:build desktop

package main

import (
	"encoding/json"
	"image"
	"reflect"
	"strings"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func TestNativeT3CodeUsesSharedLibrarySnapshot(t *testing.T) {
	u := nativeTestUI(t)
	u.models = nativeClientModelsForTest()
	library := nativeSeedSharedForTest(t, u, u.models...)
	library.Initial = u.models[1].ID
	u.setValue(nativeClientField(sharedModelKey, u.models[0].ID, "name"), "My coding model")
	u.setValue(nativeClientField(sharedModelKey, u.models[0].ID, "reasoning"), "high")
	payload, err := nativeClientPayload("t3-code", u.sharedClientSelection("t3-code"))
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
	if nativeClientEndpoint("t3-code") != "/api/clients/t3-code" || strings.Contains(string(data), u.owner.apiKey) || strings.Contains(string(data), u.owner.config.LocalKey) {
		t.Fatal("wrong endpoint or credentials in shared payload")
	}
}

func TestNativeT3CodeWideCardAndIntegrationSettings(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			u, recorder := nativeLaunchTestUI(t, "t3-code", false, false)
			nativeSeedSharedForTest(t, u, u.models...)
			u.page, u.language = "agents", lang
			u.owner.mu.Lock()
			u.owner.config.Language = lang
			u.owner.mu.Unlock()
			u.setValue(agentProjectField("t3-code"), "/does-not-exist/ignored")
			h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1900), now: time.Now()}
			h.frame()
			codex := h.target(u.tr("Open Codex", "Abrir Codex"), semantic.Button).Desc.Bounds
			claude := h.target(u.tr("Open Claude Desktop · Kilo", "Abrir Claude Desktop · Kilo"), semantic.Button).Desc.Bounds
			t3 := h.target(u.tr("Open T3 Code · Kilo", "Abrir T3 Code · Kilo"), semantic.Button).Desc.Bounds
			maus := h.target(u.tr("Open OpenMausBot", "Abrir OpenMausBot"), semantic.Button).Desc.Bounds
			if t3.Min.X != codex.Min.X || t3.Min.Y <= claude.Max.Y || t3.Max.Y >= maus.Min.Y {
				t.Fatalf("T3 card not wide below desktop apps: codex=%v claude=%v t3=%v maus=%v", codex, claude, t3, maus)
			}
			if _, exists := u.buttons["agent:t3-code:folder"]; exists {
				t.Fatal("T3 exposes an unsupported project picker")
			}
			nativeGridCapture(t, h, "native-t3-code-agents-"+lang)
			h.click(u.tr("Open T3 Code · Kilo", "Abrir T3 Code · Kilo"), semantic.Button)
			nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
			if recorder.count() != 1 || recorder.prepares.Load() != 1 {
				t.Fatalf("pointer launch did not prepare once: %s", u.notice)
			}
			if u.agentsState().Preferences.Projects["t3-code"] != "" {
				t.Fatal("T3 remembered an unsupported project")
			}
			u.agentSetup("t3-code")
			h.size = image.Pt(720, 1500)
			h.frame()
			h.target(u.tr("Prepare without opening", "Preparar sin abrir"), semantic.Button)
			labels := ""
			for _, node := range h.nodes() {
				labels += node.Desc.Label + "\n"
			}
			for _, expected := range []string{u.tr("Codex · Existing Codex CLI login", "Codex · Sesión existente de Codex CLI"), u.tr("Claude · Existing Claude Code login", "Claude · Sesión existente de Claude Code"), u.tr("Choose one of these four options for each new chat.", "Elige una de estas cuatro opciones para cada chat nuevo.")} {
				if !strings.Contains(labels, expected) {
					t.Fatalf("missing agent guidance: %q", expected)
				}
			}
			if strings.Contains(labels, u.owner.config.LocalKey) || strings.Contains(labels, u.owner.apiKey) || strings.Contains(labels, u.tr("Project folder", "Carpeta del proyecto")) {
				t.Fatal("integration settings expose credentials or unsupported project picker")
			}
			nativeGridCapture(t, h, "native-t3-code-settings-"+lang)
		})
	}
}

func TestNativeT3CodePreparationFailureAndEditsNeverLaunch(t *testing.T) {
	for _, failure := range []string{"prepare", "library", "connection"} {
		t.Run(failure, func(t *testing.T) {
			u, recorder := nativeLaunchTestUI(t, "t3-code", true, failure == "prepare")
			before := append([]modelLibraryItem(nil), u.modelLibraryValue().Models...)
			u.launchAgent("t3-code")
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

func TestNativeT3CodePreparedStatusIsFresh(t *testing.T) {
	u := nativeTestUI(t)
	nativeSeedSharedForTest(t, u, nativeClientModelsForTest()[0])
	s := u.sharedClientSelection("t3-code")
	payload, _ := nativeClientPayload("t3-code", s)
	library := payload.(map[string]any)["library"].(modelLibrary)
	data, _ := json.Marshal(map[string]any{"prepared": true, "library": library, "version": "0.0.45"})
	u.acceptT3CodeProfile(data, u.launchConnectionFingerprint())
	if !u.t3CodePrepared(s) {
		t.Fatal("valid prepared metadata was not accepted")
	}
	u.setValue(nativeClientField(sharedModelKey, s.Models[0].Model.ID, "name"), "Pending edit")
	if u.t3CodePrepared(u.sharedClientSelection("t3-code")) {
		t.Fatal("pending library edit displays a stale prepared state")
	}
	u.acceptT3CodeProfile(data, [32]byte{})
	if u.t3CodePrepared(s) {
		t.Fatal("changed connection displays a stale prepared state")
	}
}

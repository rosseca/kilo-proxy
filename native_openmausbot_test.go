//go:build desktop

package main

import (
	"encoding/json"
	"image"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func TestNativeOpenMausBotUsesSharedLibrarySnapshot(t *testing.T) {
	u := nativeTestUI(t)
	u.models = nativeClientModelsForTest()
	library := nativeSeedSharedForTest(t, u, u.models...)
	library.Initial = u.models[1].ID
	u.setValue(nativeClientField(sharedModelKey, u.models[0].ID, "name"), "My coding model")
	u.setValue(nativeClientField(sharedModelKey, u.models[0].ID, "reasoning"), "high")
	s := u.sharedClientSelection("openmausbot")
	payload, err := nativeClientPayload("openmausbot", s)
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
	if request.Library.DefaultModel != library.Initial || len(request.Library.Models) != 2 || request.Library.Models[0].ID != u.models[0].ID || request.Library.Models[0].DisplayName != "My coding model" || request.Library.Models[0].ReasoningEffort != "high" {
		t.Fatalf("shared snapshot lost library choices: %s", data)
	}
	if nativeClientEndpoint("openmausbot") != "/api/clients/openmausbot" || strings.Contains(string(data), u.owner.apiKey) || strings.Contains(string(data), u.owner.config.LocalKey) {
		t.Fatal("wrong endpoint or credentials in shared payload")
	}
	if !strings.Contains(u.agentCompatibility("openmausbot"), "Kilo Proxy applies each model’s supported reasoning level saved in Models") {
		t.Fatal("proxy reasoning behavior was not explained")
	}
}

func TestNativeOpenMausBotWideCardAfterDesktopApps(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			u, recorder := nativeLaunchTestUI(t, "openmausbot", false, false)
			nativeSeedSharedForTest(t, u, u.models...)
			u.page, u.language = "agents", lang
			u.owner.mu.Lock()
			u.owner.config.Language = lang
			u.owner.mu.Unlock()
			u.setValue(agentProjectField("openmausbot"), "/does-not-exist/ignored")
			h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1700), now: time.Now()}
			h.frame()
			codex := h.target(u.tr("Open Codex", "Abrir Codex"), semantic.Button).Desc.Bounds
			claude := h.target(u.tr("Open Claude Desktop · Kilo", "Abrir Claude Desktop · Kilo"), semantic.Button).Desc.Bounds
			maus := h.target(u.tr("Open OpenMausBot", "Abrir OpenMausBot"), semantic.Button).Desc.Bounds
			var code image.Rectangle
			for _, node := range h.nodes() {
				if node.Desc.Label == "Claude Code" {
					code = node.Desc.Bounds
				}
			}
			if maus.Min.X != codex.Min.X || maus.Min.Y <= claude.Max.Y || maus.Max.Y >= code.Min.Y {
				t.Fatalf("OpenMausBot not between desktop apps and CLI row: codex=%v claude=%v maus=%v code=%v", codex, claude, maus, code)
			}
			var installed []image.Rectangle
			for _, node := range h.nodes() {
				if node.Desc.Label == u.tr("Installed · Desktop", "Instalado · Escritorio") {
					installed = append(installed, node.Desc.Bounds)
				}
			}
			if len(installed) < 3 || installed[0].Max.X != installed[2].Max.X {
				t.Fatalf("OpenMausBot card is not full width: %v", installed)
			}
			if _, exists := u.buttons["agent:openmausbot:folder"]; exists {
				t.Fatal("OpenMausBot exposes an unsupported project picker")
			}
			nativeGridCapture(t, h, "native-openmausbot-agents-"+lang)
			h.click(u.tr("Open OpenMausBot", "Abrir OpenMausBot"), semantic.Button)
			nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
			if recorder.count() != 1 || recorder.prepares.Load() != 1 {
				t.Fatalf("pointer launch did not prepare and open once: %s", u.notice)
			}
			if u.agentsState().Preferences.Projects["openmausbot"] != "" {
				t.Fatal("OpenMausBot remembered an unsupported project")
			}
			u.agentSetup("openmausbot")
			h.size = image.Pt(720, 900)
			h.frame()
			h.target(u.tr("Prepare without opening", "Preparar sin abrir"), semantic.Button)
			for _, node := range h.nodes() {
				if node.Desc.Label == u.tr("Project folder", "Carpeta del proyecto") || strings.Contains(node.Desc.Label, u.owner.config.LocalKey) {
					t.Fatal("integration settings show unsupported project or local credentials")
				}
			}
			nativeGridCapture(t, h, "native-openmausbot-settings-"+lang)
		})
	}
}

func TestNativeOpenMausBotLaunchReappliesSharedModels(t *testing.T) {
	u, recorder := nativeLaunchTestUI(t, "openmausbot", false, false)
	for run := 0; run < 2; run++ {
		u.launchAgent("openmausbot")
		nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
		if recorder.count() != run+1 || recorder.prepares.Load() != int32(run+1) {
			t.Fatalf("Open did not reapply library: %s", u.notice)
		}
	}
	recorder.mu.Lock()
	plan := recorder.plans[0]
	recorder.mu.Unlock()
	if plan.Client != "openmausbot" || plan.Kind != "desktop" || plan.Directory != u.owner.editorTestRoot {
		t.Fatalf("wrong desktop launch: %+v", plan)
	}
}

func TestNativeOpenMausBotPreparationFailureAndEditsNeverLaunch(t *testing.T) {
	for _, failure := range []string{"prepare", "library", "connection"} {
		t.Run(failure, func(t *testing.T) {
			u, recorder := nativeLaunchTestUI(t, "openmausbot", true, failure == "prepare")
			before := append([]modelLibraryItem(nil), u.modelLibraryValue().Models...)
			u.launchAgent("openmausbot")
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

func TestNativeOpenMausBotPreparedReasoningIsReadOnlyAndFresh(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			u, _ := nativeLaunchTestUI(t, "openmausbot", false, false)
			u.language = lang
			models := append(nativeClientModelsForTest(), modelInfo{ID: "vendor/automatic", Name: "Automatic model", ContextWindow: 128000})
			shared := nativeSeedSharedForTest(t, u, models...)
			for i := range shared.Models {
				choice := &shared.Models[i]
				choice.ReasoningCustom = true
				switch i {
				case 0:
					choice.ReasoningLevels, choice.DefaultReasoning = []string{"low", "high"}, "high"
				case 1:
					choice.ReasoningLevels, choice.DefaultReasoning = []string{"none"}, "none"
				default:
					choice.ReasoningLevels, choice.DefaultReasoning = []string{}, ""
				}
				u.seedClientChoice(sharedModelKey, *choice)
			}
			u.persistLibraryEdits()
			u.flushModelLibrary()
			u.prepareClient("openmausbot")
			nativeTestWait(t, u, func() bool { return !u.busy["POST/api/clients/openmausbot"] })
			selection := u.sharedClientSelection("openmausbot")
			if !u.openMausBotReasoningReady(selection) {
				t.Fatalf("prepared metadata was not accepted: info=%+v notice=%s", u.agentsState().OpenMausBot.Info, u.notice)
			}
			efforts := u.agentsState().OpenMausBot.Info.ReasoningEfforts
			if efforts[models[0].ID] != "high" || efforts[models[1].ID] != "none" || efforts[models[2].ID] != "" {
				t.Fatalf("effective levels were inferred incorrectly: %v", efforts)
			}
			u.agentSetup("openmausbot")
			h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1500), now: time.Now()}
			h.frame()
			labels := map[string]bool{}
			for _, node := range h.nodes() {
				labels[node.Desc.Label] = true
			}
			for _, row := range []string{models[0].ID + " · " + u.tr("High", "Alto"), models[1].ID + " · " + u.tr("None", "Sin razonamiento"), models[2].ID + " · " + u.tr("Automatic", "Automático")} {
				if !labels[row] {
					t.Fatalf("missing prepared reasoning row %q", row)
				}
			}
			nativeGridCapture(t, h, "native-openmausbot-reasoning-"+lang)
			u.setValue(nativeClientField(sharedModelKey, models[0].ID, "reasoning"), "low")
			if u.openMausBotReasoningReady(u.sharedClientSelection("openmausbot")) {
				t.Fatal("changed draft displayed stale applied reasoning")
			}
			h.size = image.Pt(720, 900)
			h.frame()
			nativeGridCapture(t, h, "native-openmausbot-reasoning-pending-"+lang)
			u.setValue(nativeClientField(sharedModelKey, models[0].ID, "reasoning"), "high")
			selection = u.sharedClientSelection("openmausbot")
			u.agentsState().OpenMausBot.Info.Prepared = false
			if u.openMausBotReasoningReady(selection) {
				t.Fatal("invalid prepared profile displayed applied reasoning")
			}
			u.agentsState().OpenMausBot.Info.Prepared = true
			u.owner.mu.Lock()
			u.owner.config.OrgID = "changed-team"
			u.owner.mu.Unlock()
			if u.openMausBotReasoningReady(selection) {
				t.Fatal("changed connection displayed stale applied reasoning")
			}
		})
	}
}

func TestNativeOpenMausBotLateMetadataDoesNotReplacePreparedReasoning(t *testing.T) {
	u := nativeTestUI(t)
	nativeSeedSharedForTest(t, u, nativeClientModelsForTest()[0])
	payload, _ := nativeClientPayload("openmausbot", u.sharedClientSelection("openmausbot"))
	library := payload.(map[string]any)["library"].(modelLibrary)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		jsonResponse(w, 200, nativeOpenMausBotInfo{Prepared: true, Library: &library, ReasoningEfforts: map[string]string{"vendor/one": "low"}})
	}))
	t.Cleanup(func() { unblock(); server.Close() })
	u.owner.adminHost = strings.TrimPrefix(server.URL, "http://")
	u.refreshOpenMausBotProfile()
	<-entered
	prepared, _ := json.Marshal(nativeOpenMausBotInfo{Prepared: true, Library: &library, ReasoningEfforts: map[string]string{"vendor/one": "high"}})
	u.acceptOpenMausBotProfile(prepared, u.launchConnectionFingerprint())
	unblock()
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/clients/openmausbot"] })
	if u.agentsState().OpenMausBot.Info.ReasoningEfforts["vendor/one"] != "high" {
		t.Fatal("late metadata GET replaced the newer prepared response")
	}
}

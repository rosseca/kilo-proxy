//go:build desktop

package main

import (
	"encoding/json"
	"image"
	"net/http"
	"net/http/httptest"
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
			u.owner.publishUIProjectionLocked()
			u.owner.mu.Unlock()
			u.setValue(agentProjectField("t3-code"), "/does-not-exist/ignored")
			h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1900), now: time.Now()}
			h.frame()
			// Opening T3 now confirms whether its saved accounts need Kilo before
			// enabling Open, including native-only workspaces with no shared models.
			nativeTestWait(t, u, func() bool {
				return u.agentsState().T3Code.PreferencesState.Loaded && u.agentsState().Synara.PreferencesState.Loaded
			})
			h.frame()
			synara := h.target(u.tr("Open Synara", "Abrir Synara"), semantic.Button).Desc.Bounds
			claude := h.target(u.tr("Open Claude Desktop", "Abrir Claude Desktop"), semantic.Button).Desc.Bounds
			t3 := h.target(u.tr("Open T3 Code", "Abrir T3 Code"), semantic.Button).Desc.Bounds
			maus := h.target(u.tr("Open OpenMausBot", "Abrir OpenMausBot"), semantic.Button).Desc.Bounds
			// T3 Code follows Synara in the multi-agent group, below the desktop apps and above the bots.
			if t3.Max.X != synara.Max.X || synara.Min.Y <= claude.Max.Y || t3.Min.Y <= synara.Max.Y || t3.Max.Y >= maus.Min.Y {
				t.Fatalf("T3 row not in the multi-agent group: claude=%v synara=%v t3=%v maus=%v", claude, synara, t3, maus)
			}
			if _, exists := u.buttons["agent:t3-code:folder"]; exists {
				t.Fatal("T3 exposes an unsupported project picker")
			}
			nativeGridCapture(t, h, "native-t3-code-agents-"+lang)
			h.click(u.tr("Open T3 Code", "Abrir T3 Code"), semantic.Button)
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
			h.reveal(u.tr("Prepare", "Preparar"), semantic.Button)
			h.target(u.tr("Prepare", "Preparar"), semantic.Button)
			h.reveal(u.tr("How this works", "Cómo funciona"), semantic.Button)
			h.click(u.tr("How this works", "Cómo funciona"), semantic.Button)
			labels := ""
			for _, node := range h.nodes() {
				labels += node.Desc.Label + "\n"
			}
			for _, expected := range []string{u.tr("Accounts and models", "Cuentas y modelos"), u.tr("Choose the accounts T3 offers. Changes save automatically.", "Elige las cuentas que ofrece T3. Los cambios se guardan solos."), u.tr("green KP badge", "insignia verde KP"), u.tr("Claude models", "Modelos de Claude"), u.tr("native reasoning selector", "selector nativo de razonamiento"), u.tr("check the selected account and model", "revisa la cuenta y el modelo elegidos")} {
				if !strings.Contains(labels, expected) {
					t.Fatalf("missing agent guidance: %q", expected)
				}
			}
			if strings.Contains(labels, u.owner.config.LocalKey) || strings.Contains(labels, u.owner.apiKey) || strings.Contains(labels, "\n"+u.tr("Folder", "Carpeta")+"\n") {
				t.Fatal("integration settings expose credentials or unsupported project picker")
			}
			nativeGridCapture(t, h, "native-t3-code-settings-"+lang)
		})
	}
}

func TestNativeT3CodePreparationFailureAndEditsNeverLaunch(t *testing.T) {
	for _, failure := range []string{"prepare", "library", "connection", "preferences"} {
		t.Run(failure, func(t *testing.T) {
			u, recorder := nativeLaunchTestUI(t, "t3-code", true, failure == "prepare")
			before := append([]modelLibraryItem(nil), u.modelLibraryValue().Models...)
			u.launchAgent("t3-code")
			nativeTestWait(t, u, func() bool { return recorder.prepares.Load() == 1 })
			switch failure {
			case "library":
				u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Changed during preparation")
			case "preferences":
				u.agentsState().T3Code.PreferencesState.Preferences.Codex = false
			case "connection":
				u.owner.mu.Lock()
				u.owner.config.OrgID = "changed-team"
				u.owner.publishUIProjectionLocked()
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

func TestNativeT3CodeProviderCatalogsWithoutSharedModels(t *testing.T) {
	for _, provider := range []string{"claude", "muse"} {
		t.Run(provider, func(t *testing.T) {
			u, _ := nativeLaunchTestUI(t, "t3-code", false, false)
			nativeSeedSharedForTest(t, u)
			saved := u.owner.modelLibrary.snapshot()
			prefs := nativeT3CodePreferences{Version: 1, CodexModels: "official", ClaudeModels: "official"}
			if provider == "claude" {
				prefs.KiloClaude = true
			} else {
				prefs.Providers = []string{"muse"}
				prefs.MuseConnection, prefs.MuseModels = "kilo", "official"
			}
			state := &u.agentsState().T3Code
			state.Requested = true
			state.PreferencesState.Requested, state.PreferencesState.Loaded = true, true
			state.PreferencesState.Preferences, state.PreferencesState.MuseKiloSupported = prefs, true
			requests := make(chan map[string]json.RawMessage, 2)
			launches := make(chan string, 1)
			handler := u.owner.adminHandler()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					handler.ServeHTTP(w, r)
					return
				}
				switch r.URL.Path {
				case nativeLaunchEndpoint:
					var request struct{ Client string }
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						jsonError(w, http.StatusBadRequest, "invalid test launch")
						return
					}
					select {
					case launches <- request.Client:
					default:
						t.Error("unexpected repeated launch")
					}
					jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
					return
				case nativeClientEndpoint("t3-code"):
				default:
					handler.ServeHTTP(w, r)
					return
				}
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					jsonError(w, http.StatusBadRequest, "invalid test request")
					return
				}
				select {
				case requests <- payload:
				default:
					t.Error("unexpected repeated preparation")
				}
				jsonResponse(w, http.StatusOK, map[string]any{
					"prepared": true, "library": saved.Library, "preferences": prefs, "profileDir": "/synthetic/t3",
				})
			}))
			t.Cleanup(server.Close)
			u.owner.adminHost = strings.TrimPrefix(server.URL, "http://")

			selection := u.sharedClientSelection("t3-code")
			if u.t3CodeNativeOnly() || u.t3CodeNeedsSavedLibrary() || len(selection.Models) != 0 {
				t.Fatal("official Kilo catalog unexpectedly requires shared models")
			}
			if detail := u.t3CodeDetail(selection); !*detail.canPrepare {
				t.Fatal("Prepare is disabled with a connected official catalog and empty shared library")
			}
			if launch := u.uxhLaunchState("t3-code"); !launch.canOpen {
				t.Fatalf("Open is disabled for the connected official catalog: %s", launch.reason)
			}
			var finished bool
			var prepErr error
			u.prepareClientAfter("t3-code", func(err error) {
				finished, prepErr = true, err
			})
			nativeTestWait(t, u, func() bool { return finished })
			if prepErr != nil {
				t.Fatalf("Prepare failed: %v", prepErr)
			}
			var request map[string]json.RawMessage
			select {
			case request = <-requests:
			default:
				t.Fatal("Prepare did not post to T3")
			}
			var sent nativeT3CodePreferences
			if len(request) != 1 || json.Unmarshal(request["preferences"], &sent) != nil || !nativeT3CodePreferencesEqual(sent, prefs) {
				t.Fatal("Prepare did not send exactly the confirmed T3 preferences")
			}
			if !u.t3CodePrepared(selection) || u.owner.modelLibrary.snapshot().Revision != saved.Revision {
				t.Fatal("Official catalog preparation did not stay prepared or rewrote the library")
			}
			u.launchAgent("t3-code")
			nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
			select {
			case client := <-launches:
				if client != "t3-code" {
					t.Fatalf("Open used the wrong client: %s", client)
				}
			default:
				t.Fatalf("Open did not reach the launch route: %s", u.notice)
			}
			select {
			case request = <-requests:
				if len(request) != 1 || json.Unmarshal(request["preferences"], &sent) != nil || !nativeT3CodePreferencesEqual(sent, prefs) {
					t.Fatal("Open did not prepare with exactly the confirmed T3 preferences")
				}
			default:
				t.Fatal("Open did not prepare the provider catalog")
			}

			shared := prefs
			shared.ClaudeModels, shared.MuseModels = "shared", "shared"
			state.PreferencesState.Preferences = shared
			if !u.t3CodeNeedsSavedLibrary() || *u.t3CodeDetail(selection).canPrepare || u.uxhLaunchState("t3-code").canOpen {
				t.Fatal("Shared models unexpectedly work with an empty library")
			}

			state.PreferencesState.Preferences = prefs
			u.owner.mu.Lock()
			u.owner.apiKey = ""
			u.owner.publishUIProjectionLocked()
			u.owner.mu.Unlock()
			if *u.t3CodeDetail(selection).canPrepare || u.uxhLaunchState("t3-code").canOpen || u.t3CodePrepared(selection) {
				t.Fatal("Official Kilo catalog unexpectedly works without Kilo connected")
			}
		})
	}
}

func TestNativeT3CodeNightlyGuidance(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			u, _ := nativeLaunchTestUI(t, "t3-code", false, false)
			nativeSeedSharedForTest(t, u, u.models...)
			u.language = lang
			u.agentSetup("t3-code")
			h := &nativePointerHarness{t: t, u: u, size: image.Pt(720, 1900), now: time.Now()}
			h.frame()
			nativeTestWait(t, u, func() bool { return u.agentsState().T3Code.Loaded })
			u.agentsState().T3Code.Info.Version = "9.0.0-beta.2"
			u.agentsState().T3Code.Info.ProtocolV2 = true
			u.expanded["client:t3-code:notes"] = true
			h.frame()
			labels := ""
			for _, node := range h.nodes() {
				labels += node.Desc.Label + "\n"
			}
			for _, text := range []string{"9.0.0-beta.2", u.tr("change agents between turns", "cambiar de agente entre turnos"), u.tr("attachments are not transferred", "ni los adjuntos")} {
				if !strings.Contains(labels, text) {
					t.Fatalf("missing nightly guidance %q", text)
				}
			}
			if strings.Contains(labels, u.tr("start a new chat to switch", "inicia uno nuevo para cambiar")) {
				t.Fatal("nightly shows stable-only chat restriction")
			}
			nativeGridCapture(t, h, "native-t3-nightly-settings-"+lang)
		})
	}
}

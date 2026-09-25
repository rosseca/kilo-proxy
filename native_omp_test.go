//go:build desktop

package main

import (
	"encoding/json"
	"image"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func TestNativeOMPSharedModelsPreserveCapabilitiesAndDisabledReasoning(t *testing.T) {
	u := nativeTestUI(t)
	u.models = nativeClientModelsForTest()
	u.models[0].InputModalities = []string{"text", "image"}
	u.models[1].Reasoning = new(bool) // An explicit false overrides known Claude presets.
	nativeSeedSharedForTest(t, u, u.models...)
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Daily model")
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "reasoning"), "high")
	u.setSharedTokenLimits("vendor/one", 64000, 8192)
	u.library.selection.Initial = u.models[1].ID
	s := u.sharedClientSelection("omp")
	u.syncClientSelection("omp", s)
	payload, err := nativeClientPayload("omp", s)
	if err != nil {
		t.Fatal(err)
	}
	selection := payload.(ompSelection)
	if selection.Initial != u.models[1].ID || len(selection.Models) != 2 {
		t.Fatalf("shared order or default lost: %#v", selection)
	}
	m := selection.Models[0]
	// The saved 8K preference is capped by the published 4K output ceiling.
	if m.ID != "vendor/one" || m.Name != "Daily model" || m.Context != 64000 || m.Output != 4000 || !m.Reasoning || m.Effort != "high" || !reflect.DeepEqual(m.ReasoningEfforts, []string{"low", "high"}) || !reflect.DeepEqual(m.InputModalities, []string{"text", "image"}) || m.InputPrice == nil || *m.InputPrice != 1 {
		t.Fatalf("shared capabilities did not reach Oh My Pi: %#v", m)
	}
	if m := selection.Models[1]; m.Reasoning || m.Effort != "" || len(m.ReasoningEfforts) != 0 {
		t.Fatalf("explicitly unsupported reasoning enabled: %#v", m)
	}
	selection.Models[0].InputModalities[0] = "corrupted"
	selection.Models[0].ReasoningEfforts[0] = "corrupted"
	if u.models[0].InputModalities[0] != "text" || s.Models[0].Model.ReasoningEfforts[0] != "low" {
		t.Fatal("export aliases catalog capabilities")
	}
}

func TestNativeOMPImportPreservesSavedModelsWithoutCatalog(t *testing.T) {
	source := ompSelection{Initial: "vendor/custom", Models: []ompModel{{editorModel: editorModel{ID: "vendor/custom", Name: "Private name", Context: 64000, Output: 4096}, Reasoning: true, ReasoningEfforts: []string{"low", "high"}, Effort: "high", InputModalities: []string{"text", "image"}}}}
	path := filepath.Join(t.TempDir(), "models.yml")
	raw, _ := json.Marshal(map[string]any{"selection": source, "configPath": path})
	loaded, err := decodeNativeClientSelection("omp", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := nativeClientPayload("omp", loaded)
	if err != nil || !reflect.DeepEqual(payload, source) || loaded.Path != path || loaded.Saved != "" {
		t.Fatalf("import changed saved capabilities: %#v, %v", payload, err)
	}
	if _, err := decodeNativeClientSelection("omp", []byte(`{"selection":{"models":[]}}`), nil); err == nil {
		t.Fatal("invalid empty saved selection accepted")
	}
}

func TestNativeOMPExportMasksKeyAndLaunchUsesProfileDirectory(t *testing.T) {
	u := nativeTestUI(t)
	u.models = nativeClientModelsForTest()
	nativeSeedSharedForTest(t, u, u.models...)
	s := u.sharedClientSelection("omp")
	s.Path = filepath.Join(t.TempDir(), ".omp-kilo", "models.yml")
	_, key, _ := u.clientBase()
	preview, err := u.clientExport("omp", s, false)
	if err != nil {
		t.Fatal(err)
	}
	full, err := u.clientExport("omp", s, true)
	if err != nil || strings.Contains(preview, key) || !strings.Contains(full, key) || !strings.Contains(full, "openai-responses") {
		t.Fatalf("configuration export did not mask preview/preserve credentials: %v", err)
	}
	command, err := u.clientLaunch("omp", s, true)
	if err != nil || strings.Contains(command, s.Path) || strings.Contains(command, key) || !strings.Contains(command, filepath.Dir(s.Path)) || !strings.Contains(command, "PI_CODING_AGENT_DIR") {
		t.Fatalf("launch command did not target the isolated profile: %q, %v", command, err)
	}
}

func TestNativeOMPAgentCardOpensPreparedSharedProfile(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			u, recorder := nativeLaunchTestUI(t, "omp", false, false)
			entered, releaseDetection := nativeHoldOMPAgentDetection(t, u)
			u.setLanguage(lang)
			nativeTestWait(t, u, func() bool { return u.languageTarget == "" && !u.busy["POST/api/language"] })
			u.page = "agents"
			h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1200), now: time.Now()}
			h.frame()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("Agents did not request the delayed engine detection")
			}
			label := u.tr("Open Oh My Pi", "Abrir Oh My Pi")
			h.reveal(label, semantic.Button)
			beforeDetection := h.target(label, semantic.Button).Desc.Bounds
			// Reproduce the CI ordering: engine detection finishes after the
			// initial scroll. Removing its hint can shrink a bottom-anchored
			// list and move OMP between pointer Move and Press. Drain all Agents
			// detections and lay out their result before locating the click.
			releaseDetection()
			nativeTestWait(t, u, func() bool {
				c := u.clientState()
				return c.LaunchChecked && c.ClaudeChecked && c.OpenDesignChecked &&
					!u.busy["GET"+nativeLaunchEndpoint] && !u.busy["GET/api/claude/info"] && !u.busy["GET"+openDesignProfileEndpoint]
			})
			h.frame()
			afterDetection := h.target(label, semantic.Button).Desc.Bounds
			t.Logf("OMP button after delayed detection: %v -> %v", beforeDetection, afterDetection)
			h.reveal(label, semantic.Button)
			nativeGridCapture(t, h, "native-omp-agents-"+lang)
			h.click(label, semantic.Button)
			nativeTestWait(t, u, func() bool {
				return u.clientState().Launching == "" && (recorder.prepares.Load() > 0 || u.notice != "")
			})
			if recorder.count() != 1 || recorder.prepares.Load() != 1 {
				t.Fatalf("Oh My Pi card did not prepare and launch: prepares=%d launches=%d notice=%q", recorder.prepares.Load(), recorder.count(), u.notice)
			}
			prepared := u.clientState().selection("omp")
			profileDir := filepath.Join(u.owner.editorTestRoot, ".omp-kilo")
			if prepared.Path != filepath.Join(profileDir, "models.yml") {
				t.Fatalf("Oh My Pi preparation escaped the fixture: %q", prepared.Path)
			}
			for _, name := range []string{"models.yml", "config.yml", "mcp.json", "kilo-models.json"} {
				if info, err := os.Stat(filepath.Join(profileDir, name)); err != nil || !info.Mode().IsRegular() {
					t.Fatalf("Oh My Pi did not prepare %s inside the fixture: %v", name, err)
				}
			}
			if data, err := os.ReadFile(prepared.Path); err != nil || !strings.Contains(string(data), "vendor/one") {
				t.Fatalf("shared model missing from prepared profile: %v", err)
			}
			u.agentSetup("omp")
			if u.page != "clients" || u.client != "omp" {
				t.Fatal("Oh My Pi settings did not open")
			}
			u.page = "models"
			u.importModelLibrary("omp")
			nativeTestWait(t, u, func() bool { return u.library.pendingImport != nil })
			if got := u.library.pendingImport; got.Initial != "vendor/one" || len(got.Models) != 1 {
				t.Fatalf("saved profile could not be loaded: %#v", got)
			}
		})
	}
}

// Hold only the unrelated engine detection. Profile preparation and launch
// still use the original authenticated fixture and its real API counters.
func nativeHoldOMPAgentDetection(t *testing.T, u *nativeUI) (<-chan struct{}, func()) {
	t.Helper()
	upstream, err := url.Parse("http://" + u.owner.adminHost)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == openDesignProfileEndpoint {
			enteredOnce.Do(func() { close(entered) })
			<-release
		}
		proxy.ServeHTTP(w, r)
	}))
	u.owner.adminHost = strings.TrimPrefix(server.URL, "http://")
	t.Cleanup(func() { unblock(); server.Close() })
	return entered, unblock
}

func TestNativeOMPFailedPreparationNeverLaunches(t *testing.T) {
	u, recorder := nativeLaunchTestUI(t, "omp", false, true)
	u.launchAgent("omp")
	nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
	if recorder.count() != 0 || recorder.prepares.Load() != 1 || !strings.Contains(u.notice, "synthetic preparation failure") {
		t.Fatalf("failed preparation was not surfaced safely: %s", u.notice)
	}
}

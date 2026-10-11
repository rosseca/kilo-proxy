//go:build desktop

package main

import (
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func nativeRecommendedModelsForTest() []modelInfo {
	models := nativeGridModels()[:4]
	models[0].Recommendation = &modelRecommendation{Rank: 2, NoteEN: "Hard tasks", NoteES: "Tareas difíciles"}
	models[1].Recommendation = &modelRecommendation{Rank: 1, Default: true, Reasoning: "high", NoteEN: "Balanced coding", NoteES: "Código equilibrado"}
	models[3].Recommendation = &modelRecommendation{Rank: 3, Reasoning: "ultra", NoteEN: "Cheap", NoteES: "Barato"}
	return models
}

func TestNativeRecommendedModelsAddAllUsesPublishedDefault(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {720, 700}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"/"+lang, func(t *testing.T) {
				u := nativeTestUI(t)
				u.language, u.page = lang, "models"
				u.models = nativeRecommendedModelsForTest()
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				nativeGridCapture(t, h, "recommended-empty-"+fmtSize(size)+"-"+lang)
				h.click(u.tr("Add all (3)", "Añadir todos (3)"), semantic.Button)
				s := u.library.selection
				// Library order follows the published list, not the catalog order.
				if want := []string{"anthropic/claude-sonnet-4.6", "openai/gpt-5.6-sol", "z-ai/glm-5"}; !slices.Equal(s.ids(), want) {
					t.Fatalf("added %v, want %v", s.ids(), want)
				}
				if s.Initial != "anthropic/claude-sonnet-4.6" {
					t.Fatalf("empty library did not start with the published default: %s", s.Initial)
				}
				if got := u.value(nativeClientField(sharedModelKey, "anthropic/claude-sonnet-4.6", "reasoning")); got != "high" {
					t.Fatalf("published reasoning not applied: %q", got)
				}
				// A level the model does not offer is never forced onto it.
				if got := u.value(nativeClientField(sharedModelKey, "z-ai/glm-5", "reasoning")); got == "ultra" {
					t.Fatal("unsupported published reasoning was applied")
				}
			})
		}
	}
}

func TestNativeRecommendedModelsKeepChosenDefault(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	u.models = nativeRecommendedModelsForTest()
	nativeSeedSharedForTest(t, u, u.models[2])
	u.expanded["models.recommended"] = true
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 820), now: time.Now()}
	h.frame()
	h.click(u.tr("Add all (3)", "Añadir todos (3)"), semantic.Button)
	s := u.library.selection
	if len(s.Models) != 4 || s.Initial != u.models[2].ID {
		t.Fatalf("adding recommendations replaced the chosen default: %s %v", s.Initial, s.ids())
	}
	// Unchecking a recommendation removes only that model.
	h.frame()
	h.reveal("Claude Sonnet 4.6", semantic.CheckBox)
	h.click("Claude Sonnet 4.6", semantic.CheckBox)
	if s.choice("anthropic/claude-sonnet-4.6") != nil || len(s.Models) != 3 {
		t.Fatalf("unchecking did not remove the recommendation: %v", s.ids())
	}
}

// A published list longer than a scrolling grid must be reachable in setup,
// and setup must offer a default choice although it shows no library cards.
func TestNativeSetupShowsEveryRecommendedModelAndChoosesDefault(t *testing.T) {
	u := nativeTestUI(t)
	u.page, u.setupStep = "setup", setupModels
	u.expanded["library.catalog"] = true
	u.models = nativeGridModels()
	for i := range u.models {
		u.models[i].Recommendation = &modelRecommendation{Rank: i + 1, Default: i == 0}
	}
	last := u.models[len(u.models)-1]
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1500), now: time.Now()}
	h.frame()
	h.click("Filters", semantic.Button)
	u.clickable("client:shared:select-all").Click()
	h.frame()
	s := u.library.selection
	if len(s.Models) != 12 || s.Initial != u.models[0].ID {
		t.Fatalf("add all: %v default %s", s.ids(), s.Initial)
	}
	h.frame()
	// Back on the compact list, the star on a row is the only way to choose the default.
	h.click("Done", semantic.Button)
	if _, ok := u.buttons["setup.default:"+u.models[2].ID]; !ok {
		t.Fatal("setup list offers no default action for a selected model")
	}
	u.clickable("setup.default:" + u.models[2].ID).Click()
	h.frame()
	if s.Initial != u.models[2].ID {
		t.Fatalf("setup default star did not change the default: %s", s.Initial)
	}
	// The last recommended card is laid out in the page, not hidden in a nested scroll.
	u.expanded["library.catalog"] = true
	h.frame()
	h.reveal(last.Name, semantic.CheckBox)
	h.click(last.Name, semantic.CheckBox)
	if s.choice(last.ID) != nil {
		t.Fatal("the last recommended model could not be reached")
	}
}

// Walks the real connection step: saving the account opens the catalog, loads
// the published list over HTTP and must still offer it above the catalog.
func TestNativeOnboardingShowsRecommendedModelsAfterConnection(t *testing.T) {
	list := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"schemaVersion":1,"models":[{"id":"vendor/one","note":{"en":"Everyday"}},{"id":"anthropic/claude-sonnet-4.6","default":true,"note":{"en":"Balanced"}},{"id":"vendor/absent"}]}`)
	}))
	t.Cleanup(list.Close)
	u := nativeFreshOnboarding(t)
	u.owner.mu.Lock()
	u.owner.recommendedModelsURL = list.URL
	u.owner.mu.Unlock()
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 820), now: time.Now()}
	h.frame()
	h.click("Use an API key or team ID instead", semantic.Button)
	h.click("Leave blank to keep the current key", semantic.Editor)
	h.typeText("synthetic-kilo-personal-key")
	h.click("org_…", semantic.Editor)
	h.typeText("e2e-team")
	h.click("Save & choose models", semantic.Button)
	nativeTestWait(t, u, func() bool {
		return u.setupStep == setupModels && !u.busy["GET/api/state"] && !u.busy["POST/api/models"] && len(u.models) == 2
	})
	h.frame()
	if u.expanded["library.catalog"] || !h.selected("Recommended", semantic.RadioButton) {
		t.Fatal("setup does not start from the recommended set after the connection step")
	}
	nativeGridCapture(t, h, "setup-recommended-after-connection")
	// Two of the three published IDs exist in this team's catalog.
	h.click("Use recommended & continue", semantic.Button)
	nativeTestWait(t, u, func() bool {
		h.frame()
		return u.setupStep == setupReady
	})
	s := u.library.selection
	if !slices.Equal(s.ids(), []string{"vendor/one", "anthropic/claude-sonnet-4.6"}) || s.Initial != "anthropic/claude-sonnet-4.6" || u.library.startingPack != modelLibraryStartRecommended {
		t.Fatalf("wizard recommendations added %v with default %s from %q", s.ids(), s.Initial, u.library.startingPack)
	}
}

// One click accepts the recommendations, keeps the published default and
// advances once the library is saved.
func TestNativeSetupUseRecommendedAndContinue(t *testing.T) {
	u := nativeTestUI(t)
	u.page, u.setupStep = "setup", setupModels
	u.models = nativeGridModels()
	for i := range u.models[:4] {
		u.models[i].Recommendation = &modelRecommendation{Rank: i + 1, Default: i == 1}
	}
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 820), now: time.Now()}
	h.frame()
	h.click("Use recommended & continue", semantic.Button)
	nativeTestWait(t, u, func() bool {
		h.frame()
		return u.setupStep == setupReady
	})
	s := u.library.selection
	if len(s.Models) != 4 || s.Initial != u.models[1].ID {
		t.Fatalf("recommended set not applied: %v default %s", s.ids(), s.Initial)
	}
}

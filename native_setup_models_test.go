//go:build desktop

package main

import (
	"image"
	"slices"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

// Choosing a pack in onboarding copies its models, default and roles into the
// shared list and records it as the starting point. No harness gets the pack.
func TestUxmSetupPackStartCopiesModelsWithoutAssigning(t *testing.T) {
	u := nativeTestUI(t)
	u.page, u.setupStep = "setup", setupModels
	u.models = uxmPackCatalogForTest(nativeGridModels())
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1600), now: time.Now()}
	h.frame()
	if !h.selected("QA · Bug Hunter", semantic.RadioButton) || u.uxmSetupStart() != "qa-bug-hunter" {
		t.Fatal("without recommendations the first usable pack is not the default starting point")
	}
	h.click("Full Stack Dev", semantic.RadioButton)
	if u.uxmSetupStart() != "full-stack-dev" {
		t.Fatalf("pack radio did not select the pack: %q", u.uxmSetupStart())
	}
	h.click("Use “Full Stack Dev” & continue", semantic.Button)
	nativeTestWait(t, u, func() bool {
		h.frame()
		return u.setupStep == setupReady
	})
	s := u.library.selection
	want := []string{"openai/gpt-6-sol", "deepseek/deepseek-v4.1-flash", "anthropic/claude-sonnet-5.5"}
	if !slices.Equal(s.ids(), want) || s.Initial != "openai/gpt-6-sol" {
		t.Fatalf("pack copy: %v default %s", s.ids(), s.Initial)
	}
	if u.library.startingPack != "full-stack-dev" || u.library.roles["openai/gpt-6-sol"] == "" {
		t.Fatalf("starting point not recorded: %q %v", u.library.startingPack, u.library.roles)
	}
	if len(u.packsState().file.Assignments) != 0 {
		t.Fatalf("onboarding assigned a pack to a harness: %v", u.packsState().file.Assignments)
	}
	state := newModelLibraryStore(u.owner.dir).snapshot()
	if state.Library.StartingPack != "full-stack-dev" || len(state.Library.Models) != 3 {
		t.Fatalf("starting pack not persisted: %+v", state.Library)
	}
}

// Packs whose models are missing from the catalog are greyed and cannot be chosen.
func TestUxmSetupUnavailablePackCannotBeChosen(t *testing.T) {
	u := nativeTestUI(t)
	u.page, u.setupStep = "setup", setupModels
	u.models = nativeGridModels()
	u.models[0].Recommendation = &modelRecommendation{Rank: 1, Default: true}
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1600), now: time.Now()}
	h.frame()
	pack := builtinModelPacks()[0]
	disabled := false
	for _, n := range h.nodes() {
		if n.Desc.Class == semantic.RadioButton && n.Desc.Label == pack.Name.EN {
			disabled = n.Desc.Disabled
		}
	}
	if !disabled || !h.selected("Recommended", semantic.RadioButton) {
		t.Fatal("a pack missing from the catalog is selectable, or recommended is not the default")
	}
	u.clickable("setup.models.option." + pack.ID).Click()
	nativeTestFrame(t, u)
	if u.uxmSetupStart() != modelLibraryStartRecommended {
		t.Fatalf("an unavailable pack became the starting point: %q", u.uxmSetupStart())
	}
	found := false
	for _, n := range h.nodes() {
		if n.Desc.Label == "Not available in your catalog" {
			found = true
		}
	}
	if !found {
		t.Fatal("unavailable packs do not explain why")
	}
}

// With no usable starting point the step falls back to the catalog.
func TestUxmSetupFallsBackToCatalog(t *testing.T) {
	u := nativeTestUI(t)
	u.page, u.setupStep = "setup", setupModels
	u.models = nativeGridModels()
	nativeTestFrame(t, u)
	if !u.uxmSetupShowsCatalog() {
		t.Fatal("no starting point available, but the catalog is not shown")
	}
	if _, ok := u.buttons["client:shared:refresh"]; !ok {
		t.Fatal("the catalog was not laid out")
	}
}

// An existing list is kept by default; its star changes the default and the
// footer continues without replacing the list.
func TestUxmSetupKeepsCurrentList(t *testing.T) {
	u := nativeTestUI(t)
	u.page, u.setupStep = "setup", setupModels
	u.models = uxmPackCatalogForTest(nativeGridModels())
	for _, m := range u.models[:2] {
		if !u.uxmCatalogAdd(m) {
			t.Fatal("seed add failed")
		}
	}
	nativeTestFrame(t, u)
	if u.uxmSetupStart() != "current" {
		t.Fatalf("existing list is not the default choice: %q", u.uxmSetupStart())
	}
	u.clickable("setup.default:" + u.models[1].ID).Click()
	nativeTestFrame(t, u)
	if u.library.selection.Initial != u.models[1].ID {
		t.Fatal("setup star did not change the default")
	}
	u.clickable("setup.remove:" + u.models[0].ID).Click()
	nativeTestFrame(t, u)
	if len(u.library.selection.Models) != 1 {
		t.Fatal("setup remove did not remove the model")
	}
	nativeTestWait(t, u, func() bool { _, ready := u.libraryStatus(); return ready })
	u.clickable("primary.setup.models-next").Click()
	nativeTestWait(t, u, func() bool { nativeTestFrame(t, u); return u.setupStep == setupReady })
	if len(u.library.selection.Models) != 1 || u.library.startingPack != "" {
		t.Fatal("continuing with the current list replaced it")
	}
}

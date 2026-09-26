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

func nativeContextModels() []modelInfo {
	return []modelInfo{
		{ID: "openai/large", Name: "Large context", ContextWindow: 1050000, MaxOutputTokens: 128000},
		{ID: "vendor/small", Name: "Small context", ContextWindow: 200000, MaxOutputTokens: 16384},
	}
}

func TestNativeContextPresetsAutosaveAndRestart(t *testing.T) {
	u := nativeTestUI(t)
	u.models = nativeContextModels()
	u.page = "models"
	nativeSeedSharedForTest(t, u, u.models...)
	nativeTestFrame(t, u)
	for _, choice := range u.library.selection.Models {
		if choice.ContextPreset != contextPresetRecommended {
			t.Fatalf("new model did not start at Recommended: %#v", choice)
		}
	}
	u.clickable("models.context.low").Click()
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	for _, saved := range u.owner.modelLibrary.snapshot().Library.Models {
		if saved.ContextPreset != contextPresetLow || saved.ContextWindow != 0 {
			t.Fatalf("bulk preset did not save policy independently of capacity: %#v", saved)
		}
	}
	// A per-model Custom override is an ordinary auto-saved editor. It retains
	// the requested budget even when that budget is capped by model capacity.
	u.clickable("client:shared:edit:openai/large").Click()
	nativeTestFrame(t, u)
	u.clickable(nativeClientField(sharedModelKey, "openai/large", "context-preset:custom")).Click()
	nativeTestFrame(t, u)
	u.setValue(nativeClientField(sharedModelKey, "openai/large", "context"), "400000")
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	saved := u.owner.modelLibrary.snapshot().Library
	large := nativeSavedLibraryItem(t, saved, "openai/large")
	if large.ContextPreset != contextPresetCustom || large.ContextWindow != 400000 {
		t.Fatalf("custom edit was not autosaved: %#v", large)
	}
	owner, err := newApp(u.owner.dir, &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	reopened := newNativeUI(owner, func() {})
	t.Cleanup(func() { owner.requestQuit(); reopened.shutdownModelLibrary() })
	if got := reopened.modelLibraryValue(); !reflect.DeepEqual(got, saved) {
		t.Fatalf("restart changed context policy: got %#v want %#v", got, saved)
	}
	reopened.models = nativeContextModels()
	reopened.syncClientSelection(sharedModelKey, reopened.library.selection)
	limits, err := contextPolicyForChoice(*reopened.library.selection.choice("openai/large"))
	if err != nil || limits.ContextWindow != 400000 || limits.MaximumContextWindow != 1050000 {
		t.Fatalf("restart confused custom budget with maximum: %#v %v", limits, err)
	}
}

func TestNativeContextCatalogRefreshKeepsPolicyAndResolvesEachClient(t *testing.T) {
	u := nativeTestUI(t)
	u.models = nativeContextModels()
	nativeSeedSharedForTest(t, u, u.models...)
	u.models[0].ContextWindow = 250000
	u.models[0].MaxOutputTokens = 16000
	u.syncClientSelection(sharedModelKey, u.library.selection)
	choice := u.library.selection.choice("openai/large")
	if choice.ContextPreset != contextPresetRecommended || choice.ContextTokens != 0 || choice.Model.ContextWindow != 250000 {
		t.Fatalf("catalog refresh replaced policy or retained old capacity: %#v", choice)
	}
	for _, client := range []string{"codex", "codex-cli", "opencode", "omp", "zed", "claude"} {
		t.Run(client, func(t *testing.T) {
			selection := u.sharedClientSelection(client)
			payload, err := nativeClientPayload(client, selection)
			if err != nil {
				t.Fatal(err)
			}
			var got int
			switch typed := payload.(type) {
			case editorSelection:
				got = typed.Models[0].Context
				if typed.Models[0].Output != 16000 {
					t.Fatal("gateway output cap not applied")
				}
			case ompSelection:
				got = typed.Models[0].Context
			case claudeSelection:
				got = typed.Models[0].Context
			case map[string]any:
				var catalog struct {
					Models []struct {
						Context int `json:"context_window"`
					} `json:"models"`
				}
				if err := json.Unmarshal(typed["catalog"].(json.RawMessage), &catalog); err != nil {
					t.Fatal(err)
				}
				got = catalog.Models[0].Context
			default:
				t.Fatalf("unexpected payload %T", payload)
			}
			if got != 250000 {
				t.Fatalf("agent received %d tokens instead of capped Recommended", got)
			}
		})
	}
}

func TestNativeContextMaximumUnknownNeverPartiallyApplies(t *testing.T) {
	u := nativeTestUI(t)
	u.models = append(nativeContextModels(), modelInfo{ID: "private/unknown", Name: "Private model"})
	nativeSeedSharedForTest(t, u, u.models...)
	u.page = "models"
	before := u.modelLibraryValue()
	if u.applySharedContext(contextPresetMaximum, 0) {
		t.Fatal("Maximum accepted invented catalog capacity")
	}
	if !reflect.DeepEqual(before, u.modelLibraryValue()) {
		t.Fatal("failed bulk action changed part of the library")
	}
	nativeTestFrame(t, u)
	u.clickable("models.context.maximum").Click()
	nativeTestFrame(t, u)
	if !reflect.DeepEqual(before, u.modelLibraryValue()) {
		t.Fatal("disabled Maximum button changed the library")
	}
	unknown := u.library.selection.choice("private/unknown")
	if unknown.Model.ContextWindow != 0 || !strings.Contains(u.contextChoiceSummary(*unknown), "unknown") {
		t.Fatal("unknown model advertised fabricated maximum")
	}
}

func TestNativeContextBlockersRequireConfirmationAndAutosave(t *testing.T) {
	u := nativeTestUI(t)
	u.page, u.language = "models", "es"
	known := nativeContextModels()[0]
	unpublished := modelInfo{ID: "provider/unpublished", Name: "Unpublished"}
	absent := modelInfo{ID: "provider/absent", Name: "Absent"}
	u.models = []modelInfo{known, unpublished}
	s := nativeSeedSharedForTest(t, u, known, unpublished, absent)
	nativeTestFrame(t, u)
	if got := u.unknownContextMaximumIDs(); !reflect.DeepEqual(got, []string{unpublished.ID, absent.ID}) {
		t.Fatalf("blockers = %v", got)
	}
	if got := u.contextMaximumBlockerReason(unpublished.ID); got != "Sin máximo publicado en el catálogo" {
		t.Fatalf("known catalog entry misdiagnosed: %s", got)
	}
	if got := u.contextMaximumBlockerReason(absent.ID); got != "No aparece en el catálogo actual" {
		t.Fatalf("missing catalog entry misdiagnosed: %s", got)
	}
	before := u.modelLibraryValue()
	u.clickable("models.context.remove." + unpublished.ID).Click()
	nativeTestFrame(t, u)
	if !reflect.DeepEqual(before, u.modelLibraryValue()) || !reflect.DeepEqual(u.contextRemovalIDs, []string{unpublished.ID}) {
		t.Fatal("opening individual removal changed the library before confirmation")
	}
	u.clickable("models.context.remove.cancel").Click()
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	if !reflect.DeepEqual(before, u.owner.modelLibrary.snapshot().Library) || len(u.contextRemovalIDs) > 0 {
		t.Fatal("cancelling removed a saved model")
	}
	u.clickable("models.context.remove-all").Click()
	nativeTestFrame(t, u)
	if !reflect.DeepEqual(before, u.modelLibraryValue()) || len(u.contextRemovalIDs) != 2 {
		t.Fatal("bulk removal changed the library before confirmation")
	}
	u.clickable("models.context.remove.confirm").Click()
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	saved := u.owner.modelLibrary.snapshot().Library
	if len(saved.Models) != 1 || saved.Models[0].ID != known.ID || saved.DefaultModel != known.ID || len(u.unknownContextMaximumIDs()) != 0 || len(u.contextRemovalIDs) != 0 {
		t.Fatalf("confirm did not remove just the blockers: %#v", saved)
	}
	if s.Initial != known.ID || !u.expanded["models.context.removed"] {
		t.Fatal("remaining default or completion state was lost")
	}
	u.clickable("models.context.maximum").Click()
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	if got := nativeSavedLibraryItem(t, u.owner.modelLibrary.snapshot().Library, known.ID).ContextPreset; got != contextPresetMaximum {
		t.Fatalf("Maximum stayed blocked after removing both unknown models: %s", got)
	}
}

func TestNativeContextRemovalOfDefaultAndCatalogRefresh(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	known := nativeContextModels()[0]
	absent := modelInfo{ID: "provider/absent", Name: "Absent"}
	u.models = []modelInfo{known}
	s := nativeSeedSharedForTest(t, u, known, absent)
	s.Initial = absent.ID
	nativeTestFrame(t, u)
	u.requestContextRemoval([]string{absent.ID})
	// A new catalog response can resolve the problem before the user confirms.
	u.models = append(u.models, modelInfo{ID: absent.ID, Name: "Found", ContextWindow: 180000, MaxOutputTokens: 8192})
	u.syncClientSelection(sharedModelKey, s)
	u.confirmContextRemoval()
	if s.choice(absent.ID) == nil || s.Initial != absent.ID || len(u.contextRemovalIDs) != 0 {
		t.Fatal("stale confirmation removed a model whose maximum is now known")
	}
	// A separate unknown default (with no earlier cached metadata) falls back.
	v := nativeTestUI(t)
	v.page = "models"
	v.models = []modelInfo{known}
	selection := nativeSeedSharedForTest(t, v, known, absent)
	selection.Initial = absent.ID
	v.requestContextRemoval([]string{absent.ID})
	v.confirmContextRemoval()
	nativeTestFrame(t, v)
	v.flushModelLibrary()
	saved := v.owner.modelLibrary.snapshot().Library
	if len(saved.Models) != 1 || saved.DefaultModel != known.ID {
		t.Fatalf("removing the unknown default left an invalid library: %#v", saved)
	}
}

func TestNativeContextFiftyModelsCanRemoveThenChooseReplacements(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	models := make([]modelInfo, 0, 50)
	for i := 0; i < 48; i++ {
		model := modelInfo{ID: fmt.Sprintf("provider/known-%02d", i), ContextWindow: 200000, MaxOutputTokens: 8192}
		u.models = append(u.models, model)
		models = append(models, model)
	}
	models = append(models, modelInfo{ID: "provider/old-one"}, modelInfo{ID: "provider/old-two"})
	s := nativeSeedSharedForTest(t, u, models...)
	if len(s.Models) != 50 || len(u.unknownContextMaximumIDs()) != 2 {
		t.Fatal("test library did not reproduce the 50-model limit and two blockers")
	}
	u.requestContextRemoval(u.unknownContextMaximumIDs())
	u.confirmContextRemoval()
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	if got := len(u.owner.modelLibrary.snapshot().Library.Models); got != 48 {
		t.Fatalf("bulk confirmation retained %d models, want 48", got)
	}
	// A user can then choose replacements through the existing catalog picker;
	// removal never silently picks these models for them.
	for i := 0; i < 2; i++ {
		model := modelInfo{ID: fmt.Sprintf("provider/replacement-%d", i), ContextWindow: 128000, MaxOutputTokens: 8192}
		u.models = append(u.models, model)
		if err := s.add(model, 50); err != nil {
			t.Fatal(err)
		}
		u.seedClientChoice(sharedModelKey, *s.choice(model.ID))
	}
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	if len(u.owner.modelLibrary.snapshot().Library.Models) != 50 || !u.applySharedContext(contextPresetMaximum, 0) {
		t.Fatal("known replacement models could not fill the library and use Maximum")
	}
}

func TestNativeContextCatalogUnavailableDoesNotOfferMassRemoval(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	u.models = nil
	model := modelInfo{ID: "provider/offline", Name: "Offline"}
	nativeSeedSharedForTest(t, u, model)
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 900), now: time.Now()}
	h.frame()
	if !strings.Contains(u.contextMaximumBlockerReason(model.ID), "Not in the current catalog") {
		t.Fatal("unknown model lost its catalog status")
	}
	foundRefresh, foundRemove := false, false
	for _, node := range h.nodes() {
		if node.Desc.Label == "Refresh catalog" {
			foundRefresh = true
		}
		if node.Desc.Label == "Remove the model from my library" || node.Desc.Label == "Remove" {
			foundRemove = true
		}
	}
	if !foundRefresh || foundRemove {
		t.Fatal("offline library suggested deleting models before refreshing the catalog")
	}
}

func TestNativeContextBlockerDialogPointerConfirmation(t *testing.T) {
	for _, size := range []image.Point{{1180, 900}, {720, 700}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"-"+lang, func(t *testing.T) {
				u := nativeTestUI(t)
				u.page, u.language = "models", lang
				known := nativeContextModels()[0]
				unknown := modelInfo{ID: "provider/unknown", Name: "Unknown"}
				u.models = []modelInfo{known}
				nativeSeedSharedForTest(t, u, known, unknown)
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				label := u.tr("Remove the model from my library", "Quitar el modelo de mi biblioteca")
				h.reveal(label, semantic.Button)
				nativeGridCapture(t, h, "native-context-blockers-"+fmtSize(size)+"-"+lang)
				h.click(label, semantic.Button)
				if u.library.selection.choice(unknown.ID) == nil || len(u.contextRemovalIDs) != 1 {
					t.Fatal("pointer opened no dialog or removed a model before confirmation")
				}
				nativeGridCapture(t, h, "native-context-confirmation-"+fmtSize(size)+"-"+lang)
				// An outside click dismisses the dialog instead of reaching the page.
				h.click(u.tr("Low", "Bajo"), semantic.Button)
				if len(u.contextRemovalIDs) != 0 || u.library.selection.choice(known.ID).ContextPreset != contextPresetRecommended {
					t.Fatal("backdrop click changed the library instead of dismissing the dialog")
				}
				h.click(label, semantic.Button)
				h.click(u.tr("Remove from my library", "Quitar de mi biblioteca"), semantic.Button)
				u.flushModelLibrary()
				if u.library.selection.choice(unknown.ID) != nil || len(u.owner.modelLibrary.snapshot().Library.Models) != 1 {
					t.Fatal("pointer confirmation did not persist the removal")
				}
				h.reveal(u.tr("Add replacement models", "Añadir modelos sustitutos"), semantic.Button)
				h.click(u.tr("Add replacement models", "Añadir modelos sustitutos"), semantic.Button)
				if !u.expanded["library.catalog"] {
					t.Fatal("replacement action did not open the existing catalog picker")
				}
			})
		}
	}
}

func TestNativeContextPresetControlsFitAndPointerApply(t *testing.T) {
	for _, size := range []image.Point{{1180, 900}, {780, 900}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"-"+lang, func(t *testing.T) {
				u := nativeTestUI(t)
				u.models = nativeContextModels()[:1]
				u.page, u.language = "models", lang
				nativeSeedSharedForTest(t, u, u.models...)
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				if !h.selected(u.contextPresetLabel(contextPresetRecommended), semantic.Button) {
					t.Fatal("recommended context preset is not semantically selected")
				}
				for _, preset := range []string{contextPresetRecommended, contextPresetLow} {
					tokens := contextRecommendedTokens
					if preset == contextPresetLow {
						tokens = contextLowTokens
					}
					h.target(u.contextPresetCaption(preset, tokens), semantic.Button)
				}
				for _, label := range []string{u.contextPresetLabel(contextPresetRecommended), u.contextPresetLabel(contextPresetLow), u.contextPresetLabel(contextPresetMaximum), u.contextPresetLabel(contextPresetCustom)} {
					bounds := h.target(label, semantic.Button).Desc.Bounds
					if !bounds.In(image.Rectangle{Max: size}) {
						t.Fatalf("preset falls outside viewport: %s %v", label, bounds)
					}
				}
				h.click(u.contextPresetLabel(contextPresetLow), semantic.Button)
				if !h.selected(u.contextPresetLabel(contextPresetLow), semantic.Button) {
					t.Fatal("Low context preset is not semantically selected")
				}
				u.flushModelLibrary()
				if nativeSavedLibraryItem(t, u.owner.modelLibrary.snapshot().Library, "openai/large").ContextPreset != contextPresetLow {
					t.Fatal("pointer action did not persist Low")
				}
				nativeGridCapture(t, h, "native-context-presets-"+fmtSize(size)+"-"+lang)
			})
		}
	}
}

func TestNativeContextBulkCustomDraftAndMaximumPersistPolicies(t *testing.T) {
	u := nativeTestUI(t)
	u.models = nativeContextModels()
	u.page = "models"
	nativeSeedSharedForTest(t, u, u.models...)
	nativeTestFrame(t, u)
	u.clickable("models.context.custom").Click()
	nativeTestFrame(t, u)
	before := u.modelLibraryValue()
	u.setValue("models.context.tokens", "unfinished")
	u.clickable("models.context.apply").Click()
	nativeTestFrame(t, u)
	if !reflect.DeepEqual(before, u.modelLibraryValue()) || u.sharedContextDraftError(u.value("models.context.tokens")) == "" {
		t.Fatal("invalid bulk token draft was accepted or changed the saved model choices")
	}
	u.setValue("models.context.tokens", "400000")
	u.clickable("models.context.apply").Click()
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	for _, item := range u.owner.modelLibrary.snapshot().Library.Models {
		if item.ContextPreset != contextPresetCustom || item.ContextWindow != 400000 {
			t.Fatalf("bulk custom did not preserve requested tokens: %#v", item)
		}
	}
	limits, err := contextPolicyForChoice(*u.library.selection.choice("vendor/small"))
	if err != nil || limits.ContextWindow != 200000 {
		t.Fatalf("custom request did not respect the smaller model: %#v %v", limits, err)
	}
	u.clickable("models.context.maximum").Click()
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	for _, item := range u.owner.modelLibrary.snapshot().Library.Models {
		if item.ContextPreset != contextPresetMaximum || item.ContextWindow != 0 {
			t.Fatalf("Maximum froze catalog capacity in the saved selection: %#v", item)
		}
	}
	u.clickable("models.context.recommended").Click()
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	large, err := contextPolicyForChoice(*u.library.selection.choice("openai/large"))
	if err != nil || large.ContextWindow != 272000 {
		t.Fatalf("Recommended did not reduce the previous Maximum budget: %#v %v", large, err)
	}
}

func TestNativeModelReasoningOptionsTranslateKnownValues(t *testing.T) {
	values := []string{"", "automatic", "low", "medium", "high", "minimal", "none", "xhigh", "experimental"}
	wants := map[string][]string{
		"en": {"Automatic", "Automatic", "Low", "Medium", "High", "Minimal", "None", "Extra high", "experimental"},
		"es": {"Automático", "Automático", "Bajo", "Medio", "Alto", "Mínimo", "Ninguno", "Muy alto", "experimental"},
	}
	for language, labels := range wants {
		choices := nativeModelReasoningChoices(&nativeUI{language: language}, values)
		if len(choices) != len(values) {
			t.Fatalf("%s: got %d choices, want %d", language, len(choices), len(values))
		}
		for i, choice := range choices {
			if choice.Value != values[i] || choice.Label != labels[i] {
				t.Errorf("%s option %q: got %+v, want value %q and label %q", language, values[i], choice, values[i], labels[i])
			}
		}
	}
}

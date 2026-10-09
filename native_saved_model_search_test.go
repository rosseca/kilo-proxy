//go:build desktop

package main

import (
	"image"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

// Models navigation may refresh an empty catalog. Its completion adds a notice
// above the search and changes pointer coordinates, so render that completion
// before resolving the single real click. Never retry a missed click.
func nativeSavedModelSearchClear(h *nativePointerHarness) {
	h.t.Helper()
	nativeTestWait(h.t, h.u, func() bool { return !h.u.busy["POST/api/models"] })
	h.frame()
	h.click("Clear search", semantic.Button)
	if h.u.value("client:shared:search") != "" {
		h.t.Fatal("clear search failed")
	}
}

func TestNativeSavedModelSearchClearAfterPendingCatalogRefresh(t *testing.T) {
	h := newNativePointerHarness(t, image.Pt(720, 700))
	models := nativeClientModelsForTest()
	nativeSeedSharedForTest(t, h.u, models[0], models[1])
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/gateway/models" {
			http.NotFound(w, r)
			return
		}
		close(entered)
		<-release
		jsonResponse(w, 200, map[string]any{"data": []any{map[string]any{"id": models[0].ID, "name": models[0].Name, "context_length": models[0].ContextWindow}, map[string]any{"id": models[1].ID, "name": models[1].Name, "context_length": models[1].ContextWindow}}})
	}))
	t.Cleanup(up.Close)
	t.Cleanup(unblock)
	setUpstream(h.u.owner, up.URL)
	h.u.models = nil
	h.click("Models", semantic.Button)
	select {
	case <-entered:
	case <-time.After(8 * time.Second):
		t.Fatal("navigation did not start the catalog refresh")
	}
	h.reveal("Search models", semantic.Editor)
	h.click("Search models", semantic.Editor)
	h.typeText("SONNET")
	if h.u.value("client:shared:search") != "SONNET" || !h.u.busy["POST/api/models"] {
		t.Fatal("search did not remain usable while the catalog refresh was held")
	}
	h.target("Claude Sonnet", semantic.CheckBox)
	unblock()
	nativeSavedModelSearchClear(h)
	if h.u.notice != "Loaded 2 models" {
		t.Fatal("held refresh did not add the notice that moves the search")
	}
	if got := h.u.library.selection; !reflect.DeepEqual(got.ids(), []string{models[0].ID, models[1].ID}) || got.Initial != models[0].ID {
		t.Fatal("search or refresh changed the saved selection/default")
	}
}

func TestNativeSavedModelSearchPreservesOrderAndSelection(t *testing.T) {
	models := []modelInfo{
		{ID: "vendor/z", Name: "Zeta"},
		{ID: "~anthropic/claude-opus-latest", Name: "Claude Opus Latest"},
		{ID: "openai/gpt-6.1-sol", Name: "GPT-6.1 Sol"},
	}
	selection := &nativeClientSelection{Initial: models[2].ID}
	for _, m := range models {
		if err := selection.add(m, 50); err != nil {
			t.Fatal(err)
		}
	}
	selection.choice(models[0].ID).DisplayName = "Daily driver"
	before := nativeSelectionFingerprint("codex", selection, "local", "key", claudeCapabilities{})
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"", []string{models[0].ID, models[1].ID, models[2].ID}},
		{"  ", []string{models[0].ID, models[1].ID, models[2].ID}},
		{" SOL ", []string{models[2].ID}},
		{"OPENAI/GPT-6.1", []string{models[2].ID}},
		{"DAILY", []string{models[0].ID}},
		{"~anthropic", []string{models[1].ID}},
		{"no-match", nil},
	} {
		t.Run(tc.query, func(t *testing.T) {
			var ids []string
			for _, m := range nativeFilterSavedModels(models, selection, tc.query) {
				ids = append(ids, m.ID)
			}
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("got %v want %v", ids, tc.want)
			}
			if after := nativeSelectionFingerprint("codex", selection, "local", "key", claudeCapabilities{}); after != before {
				t.Fatal("search modified selection")
			}
		})
	}
}

func TestNativeSavedModelSearchPointerTypingAndClear(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {720, 700}} {
		t.Run(fmtSize(size), func(t *testing.T) {
			h := newNativePointerHarness(t, size)
			models := nativeClientModelsForTest()
			selection := nativeSeedSharedForTest(t, h.u, models[0], models[1])
			h.click("Models", semantic.Button)
			// Make this fixture offline after navigation, which otherwise starts
			// a catalog refresh and can restore it while the pointer test runs.
			h.u.models = nil
			h.frame()
			before := libraryFingerprint(h.u.modelLibraryValue())
			h.reveal("Search models", semantic.Editor)
			h.click("Search models", semantic.Editor)
			h.typeText("SONNET")
			if h.u.value("client:shared:search") != "SONNET" {
				t.Fatal("keyboard input did not reach search")
			}
			visible := nativeFilterSavedModels(h.u.sharedModelOrder(), selection, h.u.value("client:shared:search"))
			if len(visible) != 1 || visible[0].ID != models[1].ID {
				t.Fatalf("unexpected filtered models: %v", visible)
			}
			h.target("Claude Sonnet", semantic.CheckBox)
			nativeGridCapture(t, h, "saved-search-filtered-"+fmtSize(size))
			nativeSavedModelSearchClear(h)
			h.u.flushModelLibrary()
			if after := libraryFingerprint(h.u.modelLibraryValue()); after != before {
				t.Fatal("search changed the saved library")
			}
			h.target("Very Long First Model Name", semantic.CheckBox)
			h.click("Search models", semantic.Editor)
			h.typeText("not-in-saved-models")
			found := false
			for _, node := range h.nodes() {
				if node.Desc.Label == "No saved models match. Clear the search to see all saved models." {
					found = true
				}
			}
			if !found {
				t.Fatal("missing search-specific empty state")
			}
			nativeGridCapture(t, h, "saved-search-empty-"+fmtSize(size))
			nativeSavedModelSearchClear(h)
			h.u.flushModelLibrary()
			if after := libraryFingerprint(h.u.owner.modelLibrary.snapshot().Library); after != before {
				t.Fatal("empty search changed on-disk model settings")
			}
			if len(h.u.models) != 0 || h.u.busy["POST/api/models"] {
				t.Fatal("offline search fixture restored or refreshed the catalog")
			}
		})
	}
}

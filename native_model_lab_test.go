//go:build desktop

package main

import (
	"encoding/json"
	"image"
	"reflect"
	"testing"

	"gioui.org/io/key"
	"gioui.org/io/semantic"
)

func TestNativeLabFilterCombinesViewsWithoutChangingProfile(t *testing.T) {
	s := (&nativeClients{}).selection("codex")
	models := nativeClientModelsForTest()
	models = append(models, modelInfo{ID: "~anthropic/other", Name: "Other Claude"}, modelInfo{ID: "google/image", Name: "Image"})
	for _, m := range append(append([]modelInfo(nil), models[:2]...), modelInfo{ID: "manual-lab/saved", Name: "Saved model"}) {
		if err := s.add(m, 50); err != nil {
			t.Fatal(err)
		}
	}
	s.Models[1].DisplayName = "My Claude"
	s.Initial = models[0].ID
	before, _ := json.Marshal(s)
	for _, tc := range []struct {
		lab, query       string
		selected, coding bool
		want             []string
	}{
		{"anthropic", "", false, false, []string{"anthropic/claude-sonnet-4.6", "~anthropic/other"}},
		{"anthropic", "My Claude", true, true, []string{"anthropic/claude-sonnet-4.6"}},
		{"anthropic", "", false, true, []string{"anthropic/claude-sonnet-4.6"}},
		{"manual-lab", "saved", true, true, []string{"manual-lab/saved"}},
		{"google", "", true, false, []string{}},
		{"missing-lab", "", false, false, []string{}},
	} {
		view := nativeVisibleModels(models, s, tc.query, tc.selected, tc.coding, "name", tc.lab)
		ids := make([]string, len(view))
		for i, model := range view {
			ids[i] = model.ID
		}
		if !reflect.DeepEqual(ids, tc.want) {
			t.Errorf("%+v: got %v", tc, ids)
		}
	}
	after, _ := json.Marshal(s)
	if string(before) != string(after) {
		t.Fatal("lab view changed profile, custom name or initial model")
	}
	if got := nativeVisibleModels(models, s, "", true, false, "name", ""); len(got) != 3 {
		t.Fatal("All labs did not restore hidden selections")
	}
}

func TestNativeLabMenuPointerAndCrossClientState(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {780, 700}, {720, 700}} {
		t.Run(fmtSize(size), func(t *testing.T) {
			h := newNativePointerHarness(t, size)
			h.u.models = nativeClientModelsForTest()
			selection := nativeSeedSharedForTest(t, h.u, h.u.models[0])
			h.click("Models", semantic.Button)
			h.click("Add models", semantic.Button)
			before, _ := json.Marshal(selection)
			h.click("Filters", semantic.Button)
			h.click("Anthropic", semantic.Button)
			if h.u.value("models.lab") != "anthropic" || !h.u.uxmMenuOpen("models.filters") {
				t.Fatal("lab selection did not apply or closed the filters menu")
			}
			h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
			h.frame()
			h.frame()
			if h.u.uxmMenuOpen("models.filters") {
				t.Fatal("Escape did not dismiss the filters menu")
			}
			h.click("Order: Most used for code", semantic.Button)
			if !h.u.uxmMenuOpen("models.order") {
				t.Fatal("order menu did not open")
			}
			h.click("Speed", semantic.Button)
			if h.u.value("models.sort") != "speed" || h.u.uxmMenuOpen("models.order") {
				t.Fatal("order did not apply or close its menu")
			}
			h.click("Done", semantic.Button)
			h.click("Activity", semantic.Button)
			h.click("Models", semantic.Button)
			h.click("Add models", semantic.Button)
			if h.u.value("models.lab") != "anthropic" || h.u.value("models.sort") != "speed" {
				t.Fatal("navigation lost catalog preferences")
			}
			h.click("Filters · 1", semantic.Button)
			// An outside press only dismisses the menu; it never reaches the page.
			h.click("Search by name, lab or ID", semantic.Editor)
			if h.u.uxmMenuOpen("models.filters") || h.router.Source().Focused(h.u.editor("client:shared:search")) {
				t.Fatal("outside click did not just dismiss the filters menu")
			}
			h.click("Search by name, lab or ID", semantic.Editor)
			if !h.router.Source().Focused(h.u.editor("client:shared:search")) {
				t.Fatal("search did not take focus once the menu was closed")
			}
			h.u.models = nil
			h.frame()
			h.click("Filters · 1", semantic.Button)
			h.click("All labs", semantic.Button)
			if h.u.value("models.lab") != "" {
				t.Fatal("could not reset lab after catalog changed")
			}
			after, _ := json.Marshal(selection)
			if string(before) != string(after) {
				t.Fatal("filtering or navigation changed the shared library")
			}
		})
	}
}

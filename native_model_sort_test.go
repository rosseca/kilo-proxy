//go:build desktop

package main

import (
	"encoding/json"
	"image"
	"reflect"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
)

func TestNativeModelSortMenuPreservesProfile(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {780, 700}, {720, 700}} {
		t.Run(fmtSize(size), func(t *testing.T) {
			h := newNativePointerHarness(t, size)
			models, _ := modelSortFixture()
			h.u.models = models
			selection := nativeSeedSharedForTest(t, h.u, models[1])
			h.u.setValue(nativeClientField(sharedModelKey, models[1].ID, "name"), "My model")
			h.click("Models", semantic.Button)
			h.click("Add models", semantic.Button)
			before, _ := json.Marshal(selection)
			h.click("Order: Most used for code", semantic.Button)
			h.click("Speed", semantic.Button)
			if h.u.value("models.sort") != "speed" || h.u.uxmMenuOpen("models.order") {
				t.Fatal("real pointer did not select and close the sort menu")
			}
			after, _ := json.Marshal(selection)
			if string(before) != string(after) {
				t.Fatal("changing view order modified the profile selection")
			}
			visible := nativeVisibleModels(models, selection, "", false, false, "speed", "")
			ids := make([]string, len(visible))
			for i, model := range visible {
				ids[i] = model.ID
			}
			if !reflect.DeepEqual(ids, []string{"vendor/a", "vendor/z", "vendor/b", "vendor/missing"}) {
				t.Fatalf("selected model incorrectly pinned ahead of sort: %v", ids)
			}
			h.click("Order: Speed", semantic.Button)
			h.click("Price", semantic.Button)
			if h.u.value("models.sort") != "price" {
				t.Fatal("could not change sort twice")
			}
			h.click("Order: Price", semantic.Button)
			// At narrow widths the popup can cover the search center. Press its
			// exposed left edge: an outside press only dismisses the menu.
			search := h.target("Search by name, lab or ID", semantic.Editor).Desc.Bounds
			point := f32.Pt(float32(search.Min.X+10), float32(search.Min.Y+search.Dy()/2))
			h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: point})
			h.frame()
			h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: point})
			h.frame()
			h.router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: point})
			h.frame()
			h.frame()
			if h.u.uxmMenuOpen("models.order") || h.router.Source().Focused(h.u.editor("client:shared:search")) {
				t.Fatal("outside click did not just dismiss the menu")
			}
			h.click("Order: Price", semantic.Button)
			h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
			h.frame()
			h.frame()
			if h.u.uxmMenuOpen("models.order") {
				t.Fatal("Escape did not dismiss the sort menu")
			}
			h.click("Order: Price", semantic.Button)
			h.click("Order: Price", semantic.Button)
			if h.u.uxmMenuOpen("models.order") {
				t.Fatal("toggle did not dismiss its own menu")
			}
		})
	}
}

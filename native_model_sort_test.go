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
			selection := h.u.clientState().selection("codex")
			if err := selection.add(models[1], 50); err != nil {
				t.Fatal(err)
			}
			selection.choice(models[1].ID).DisplayName = "My model"
			label := "Clients & models"
			if size.X < 940 {
				label = "Clients"
			}
			h.click(label, semantic.Button)
			before, _ := json.Marshal(selection)
			h.click("Code Mode Rank  ▾", semantic.Button)
			h.click("Speed", semantic.Button)
			if h.u.value("models.sort") != "speed" || h.u.expanded["models.sort"] {
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
			h.click("Speed  ▾", semantic.Button)
			h.click("Price", semantic.Button)
			if h.u.value("models.sort") != "price" {
				t.Fatal("could not change sort twice")
			}
			h.click("Price  ▾", semantic.Button)
			// At narrow widths the popup opens above and can cover the
			// search center. Press its exposed left edge for a real outside click.
			search := h.target("provider/model", semantic.Editor).Desc.Bounds
			point := f32.Pt(float32(search.Min.X+10), float32(search.Min.Y+search.Dy()/2))
			h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: point})
			h.frame()
			h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: point})
			h.frame()
			h.router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: point})
			h.frame()
			h.frame()
			if h.u.expanded["models.sort"] || !h.router.Source().Focused(h.u.editor("client:codex:search")) {
				t.Fatal("outside click did not dismiss the menu and focus search")
			}
			h.click("Price  ▾", semantic.Button)
			h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
			h.frame()
			h.frame()
			if h.u.expanded["models.sort"] {
				t.Fatal("Escape did not dismiss the sort menu")
			}
			h.click("Price  ▾", semantic.Button)
			h.click("Price  ▾", semantic.Button)
			if h.u.expanded["models.sort"] {
				t.Fatal("toggle did not dismiss its own menu")
			}
		})
	}
}

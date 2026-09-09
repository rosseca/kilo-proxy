//go:build desktop

package main

import (
	"fmt"
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
)

func nativeMenuWheel(h *nativePointerHarness, position image.Point, amount float32) {
	p := f32.Pt(float32(position.X), float32(position.Y))
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: p})
	h.frame()
	h.router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: p, Scroll: f32.Pt(0, amount)})
	h.frame()
	h.frame()
	h.frame()
}

func TestNativeModelMenuFitsWindowAndIsolatesScrolling(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {780, 700}, {720, 560}} {
		t.Run(fmtSize(size), func(t *testing.T) {
			h := newNativePointerHarness(t, size)
			for i := 0; i < 30; i++ {
				h.u.models = append(h.u.models, modelInfo{ID: fmt.Sprintf("lab-%02d/model", i), Name: fmt.Sprintf("Model %02d", i)})
			}
			h.u.models = append(h.u.models, modelInfo{ID: "z-ai/final", Name: "Final model"})
			h.u.page = "models"
			h.u.expanded["library.catalog"] = true
			h.frame()
			before := h.target("All labs  ▾", semantic.Button).Desc.Bounds
			nativeMenuWheel(h, image.Pt(size.X-50, before.Min.Y-10), 40)
			after := h.target("All labs  ▾", semantic.Button).Desc.Bounds
			if before.Min.Y == after.Min.Y {
				t.Fatal("fixture did not scroll the parent page before opening its menu")
			}
			h.click("All labs  ▾", semantic.Button)
			pageBefore := h.u.list("page.models").Position
			first := h.target("● All labs", semantic.Button).Desc.Bounds
			nativeMenuWheel(h, first.Min.Add(image.Pt(12, 12)), 3000)
			if !h.u.expanded["models.lab"] {
				t.Fatal("scrolling labs dismissed the menu")
			}
			last := h.target("Z.ai", semantic.Button).Desc.Bounds
			if !last.In(image.Rectangle{Max: size}) {
				t.Fatalf("final lab is clipped by the window: %v in %v", last, size)
			}
			nativeMenuWheel(h, last.Min.Add(image.Pt(10, 10)), 1000)
			if !h.u.expanded["models.lab"] {
				t.Fatal("overscroll at the end of labs dismissed the menu")
			}
			pageAfter := h.u.list("page.models").Position
			if pageBefore.First != pageAfter.First || pageBefore.Offset != pageAfter.Offset {
				t.Fatal("lab scrolling leaked into the parent page")
			}
			h.click("Z.ai", semantic.Button)
			if h.u.value("models.lab") != "z-ai" {
				t.Fatal("the final lab was not selectable after scrolling")
			}
			h.click("Z.ai  ▾", semantic.Button)
			nativeMenuWheel(h, image.Pt(size.X-40, after.Min.Y-10), -30)
			if h.u.expanded["models.lab"] {
				t.Fatal("scrolling outside the menu did not dismiss it")
			}
			h.click("Z.ai  ▾", semantic.Button)
			h.size.X += 40
			h.frame()
			if h.u.expanded["models.lab"] {
				t.Fatal("window resize retained a popup with a stale anchor")
			}
		})
	}
}

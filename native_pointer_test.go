//go:build desktop

package main

import (
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// Keep the production router and frame operations across events, as an OS
// window does. These tests never invoke Clickable.Click or set editor text.
type nativePointerHarness struct {
	t      *testing.T
	u      *nativeUI
	size   image.Point
	router input.Router
	ops    op.Ops
	now    time.Time
}

func newNativePointerHarness(t *testing.T, size image.Point) *nativePointerHarness {
	h := &nativePointerHarness{t: t, u: nativeTestUI(t), size: size, now: time.Now()}
	h.frame()
	return h
}

func (h *nativePointerHarness) frame() {
	h.t.Helper()
	h.ops.Reset()
	h.now = h.now.Add(16 * time.Millisecond)
	gtx := layout.Context{Ops: &h.ops, Source: h.router.Source(), Constraints: layout.Exact(h.size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Now: h.now}
	if got := h.u.Layout(gtx).Size; got != h.size {
		h.t.Fatalf("native layout escaped window: got %v, want %v", got, h.size)
	}
	h.router.Frame(&h.ops)
}

func (h *nativePointerHarness) nodes() []input.SemanticNode {
	return h.router.AppendSemantics(nil)
}

func (h *nativePointerHarness) target(label string, class semantic.ClassOp) input.SemanticNode {
	h.t.Helper()
	nodes := h.nodes()
	for _, text := range nodes {
		if text.Desc.Label != label {
			continue
		}
		// Gio labels are child semantic nodes. Editor placeholders can be
		// siblings, so associate the label origin with its enclosing control.
		for _, node := range nodes {
			if node.Desc.Class == class && !node.Desc.Disabled && text.Desc.Bounds.Min.In(node.Desc.Bounds) && !node.Desc.Bounds.Intersect(image.Rectangle{Max: h.size}).Empty() {
				return node
			}
		}
	}
	for _, node := range nodes {
		h.t.Logf("semantic: class=%v label=%q description=%q bounds=%v gestures=%v", node.Desc.Class, node.Desc.Label, node.Desc.Description, node.Desc.Bounds, node.Desc.Gestures)
	}
	h.t.Fatalf("no visible %v semantic target %q", class, label)
	return input.SemanticNode{}
}

func (h *nativePointerHarness) click(label string, class semantic.ClassOp) {
	h.t.Helper()
	node := h.target(label, class)
	bounds := node.Desc.Bounds.Intersect(image.Rectangle{Max: h.size})
	p := f32.Pt(float32(bounds.Min.X+bounds.Dx()/2), float32(bounds.Min.Y+bounds.Dy()/2))
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: p})
	h.frame()
	h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: p})
	h.frame()
	h.router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: p})
	h.frame()
	h.frame()
}

func (h *nativePointerHarness) typeText(text string) {
	h.router.Queue(key.EditEvent{Text: text})
	h.frame()
	h.frame()
}

func TestNativePointerNavigationAndModelSelection(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {780, 700}, {720, 700}} {
		t.Run(fmtSize(size), func(t *testing.T) {
			h := newNativePointerHarness(t, size)
			h.click("Models", semantic.Button)
			if h.u.page != "models" {
				t.Fatal("pointer did not open Models")
			}
			h.click("Add models", semantic.Button)
			h.click("provider/model", semantic.Editor)
			if !h.router.Source().Focused(h.u.editor("client:shared:search")) {
				t.Fatal("real pointer did not focus shared catalog search")
			}
			h.typeText("vendor/one")
			if h.u.value("client:shared:search") != "vendor/one" {
				t.Fatal("keyboard text did not reach model search")
			}
			h.click("Very Long First Model Name", semantic.CheckBox)
			if h.u.library.selection.Initial != "vendor/one" {
				t.Fatal("pointer did not select the searched shared model")
			}
			h.click("Done", semantic.Button)
			h.click("Edit", semantic.Button)
			h.click("Very Long First Model Name", semantic.Editor)
			h.typeText("My shared model")
			if h.u.library.selection.choice("vendor/one").DisplayName != "My shared model" {
				t.Fatal("per-card Edit did not update shared display name")
			}
			for _, page := range []struct{ label, key string }{{"Agents", "agents"}, {"Activity", "activity"}, {"Settings", "settings"}, {"Models", "models"}} {
				h.click(page.label, semantic.Button)
				if h.u.page != page.key {
					t.Fatalf("pointer did not navigate to %s", page.label)
				}
			}
			if h.u.library.selection.Initial != "vendor/one" || h.u.sharedClientSelection("opencode").choice("vendor/one").DisplayName != "My shared model" {
				t.Fatal("navigation lost shared model or cross-agent propagation")
			}
		})
	}
}

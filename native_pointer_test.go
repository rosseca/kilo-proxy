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
			label := "Clients & models"
			if size.X < 940 {
				label = "Clients"
			}
			h.click(label, semantic.Button)
			if h.u.page != "clients" {
				t.Fatal("pointer did not open client page")
			}
			h.click("Codex CLI", semantic.Button)
			if h.u.client != "codex-cli" {
				t.Fatal("pointer did not activate Codex CLI pill")
			}
			if size.X == 720 {
				first := h.target("● Codex CLI", semantic.Button)
				last := h.target("Other clients", semantic.Button)
				if last.Desc.Bounds.Min.Y <= first.Desc.Bounds.Min.Y {
					t.Fatalf("minimum-width fixture did not exercise a wrapped pill row: first %v last %v", first.Desc.Bounds, last.Desc.Bounds)
				}
			}
			h.click("Other clients", semantic.Button)
			if h.u.client != "generic" {
				t.Fatal("pointer did not activate the final wrapped editor pill")
			}
			h.click("Xcode", semantic.Button)
			h.click("Claude", semantic.Button)
			if h.u.client != "xcode" || h.u.clientState().Variant != "claude" {
				t.Fatal("pointer did not activate the Xcode variant pill")
			}
			h.click("OpenCode", semantic.Button)
			if h.u.client != "opencode" {
				t.Fatal("pointer did not activate OpenCode pill")
			}
			h.click("provider/model", semantic.Editor)
			if !h.router.Source().Focused(h.u.editor("client:opencode:search")) {
				t.Fatal("real pointer press did not focus the model-search editor")
			}
			h.typeText("vendor/one")
			if h.u.value("client:opencode:search") != "vendor/one" {
				t.Fatal("pointer focus did not send keyboard text to model search")
			}
			h.click("Very Long First Model Name", semantic.CheckBox)
			if h.u.clientState().selection("opencode").Initial != "vendor/one" {
				t.Fatal("pointer did not select the searched model")
			}
		})
	}
}

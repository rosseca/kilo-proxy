//go:build desktop

package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

func TestNativeModelGridRowFitsAndLayoutsOnce(t *testing.T) {
	for _, width := range []int{260, 560, 572, 720, 864, 1180} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			columns := nativeModelGridColumns(width, 280, 12)
			calls := make([]int, columns)
			widths := make([]int, columns)
			cards := make([]layout.Widget, columns)
			for i := range cards {
				cards[i] = func(gtx layout.Context) layout.Dimensions {
					calls[i]++
					widths[i] = gtx.Constraints.Max.X
					if gtx.Constraints.Min.X != widths[i] {
						t.Fatal("card did not receive an exact column width")
					}
					return layout.Dimensions{Size: image.Pt(widths[i], 190+i*45)}
				}
			}
			var ops op.Ops
			gtx := layout.Context{Ops: &ops, Constraints: layout.Constraints{Max: image.Pt(width, 2000)}, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
			got := nativeModelGridRow(columns, 12, cards)(gtx)
			total := (columns - 1) * 12
			for i, call := range calls {
				if call != 1 {
					t.Fatal("interactive card was laid out more than once")
				}
				total += widths[i]
				if columns > 1 && widths[i] < 280 {
					t.Fatalf("column too narrow: %v", widths)
				}
			}
			if total != width || got.Size != image.Pt(width, 190+(columns-1)*45) {
				t.Fatalf("overlap, gap or clipped expanded card: widths=%v dimensions=%v", widths, got.Size)
			}
		})
	}
}

func nativeGridModels() []modelInfo {
	names := []string{"GPT-5.6 Sol", "Claude Sonnet 4.6", "Gemini 3.1 Pro", "GLM 5", "DeepSeek V3.2", "Qwen3 Coder", "Kimi K2.5", "MiniMax M2.5", "GPT-5.6 Luna", "Claude Haiku 4.5", "Gemini Flash", "Mistral Large"}
	ids := []string{"openai/gpt-5.6-sol", "anthropic/claude-sonnet-4.6", "google/gemini-3.1-pro", "z-ai/glm-5", "deepseek/deepseek-v3.2", "qwen/qwen3-coder", "moonshotai/kimi-k2.5", "minimax/minimax-m2.5", "openai/gpt-5.6-luna", "anthropic/claude-haiku-4.5", "google/gemini-flash", "mistralai/mistral-large"}
	yes := true
	models := make([]modelInfo, len(names))
	for i := range models {
		models[i] = modelInfo{ID: ids[i], Name: names[i], InputPrice: ptrFloat(0.7999999999999999 + float64(i)*.1), OutputPrice: ptrFloat(2.3999999999999995 + float64(i)*.2), Tools: &yes, ContextWindow: 200000, MaxOutputTokens: 32000, ReasoningEfforts: []string{"low", "medium", "high"}, CodeModeRank: ptrFloat(float64(i + 1))}
	}
	return models
}

func nativeGridCapture(t *testing.T, h *nativePointerHarness, name string) {
	t.Helper()
	dir := os.Getenv("KILO_NATIVE_SCREENSHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	window, err := headless.NewWindow(h.size.X, h.size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer window.Release()
	if err := window.Frame(&h.ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: h.size})
	if err := window.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestNativeModelGridPointerSettingsAndResize(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {720, 700}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"/"+lang, func(t *testing.T) {
				u := nativeTestUI(t)
				u.models = nativeGridModels()
				u.setLanguage(lang)
				nativeTestWait(t, u, func() bool { return u.language == lang && u.languageTarget == "" && !u.busy["POST/api/language"] })
				u.page = "models"
				u.expanded["library.catalog"] = true
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				// Position the first grid row below the fixed navigation before real pointer input.
				initial := h.target("GPT-5.6 Sol", semantic.CheckBox).Desc.Bounds
				anchor := 180
				if size.X < 940 {
					anchor = 240
				}
				u.list("page.models").Position.Offset = max(0, initial.Min.Y-anchor)
				h.frame()
				first := h.target("GPT-5.6 Sol", semantic.CheckBox).Desc.Bounds
				second := h.target("Claude Sonnet 4.6", semantic.CheckBox).Desc.Bounds
				if first.Min.Y != second.Min.Y || second.Min.X <= first.Max.X {
					t.Fatalf("models are not distinct columns: %v %v", first, second)
				}
				if size.X == 1180 {
					third := h.target("Gemini 3.1 Pro", semantic.CheckBox).Desc.Bounds
					if third.Min.Y != first.Min.Y || third.Min.X <= second.Max.X {
						t.Fatalf("wide catalog did not create three columns: %v", third)
					}
				}
				nativeGridCapture(t, h, "native-model-grid-"+fmtSize(size)+"-"+lang)
				h.click("Claude Sonnet 4.6", semantic.CheckBox)
				s := u.library.selection
				if s.choice("anthropic/claude-sonnet-4.6") == nil || len(s.Models) != 1 {
					t.Fatal("second-column pointer selected the wrong model")
				}
				nativeGridCapture(t, h, "native-model-grid-selected-"+fmtSize(size)+"-"+lang)
				u.list("page.models").Position.Offset = 0
				h.frame()
				h.click(u.tr("Done", "Listo"), semantic.Button)
				field := nativeClientField(sharedModelKey, "anthropic/claude-sonnet-4.6", "name")
				h.click(u.tr("Edit", "Editar"), semantic.Button)
				h.click("Claude Sonnet 4.6", semantic.Editor)
				if !h.router.Source().Focused(u.editor(field)) {
					t.Fatal("selected card input did not receive pointer focus")
				}
				u.editor(field).SetCaret(0, len([]rune(u.value(field))))
				h.typeText("My Claude")
				if s.choice("anthropic/claude-sonnet-4.6").DisplayName != "My Claude" {
					t.Fatal("keyboard edit was lost in selected card")
				}
				reasoningField := nativeClientField(sharedModelKey, "anthropic/claude-sonnet-4.6", "reasoning")
				h.click(u.value(reasoningField)+"  ▾", semantic.Button)
				h.click("low", semantic.Button)
				if u.value(nativeClientField(sharedModelKey, "anthropic/claude-sonnet-4.6", "reasoning")) != "low" {
					t.Fatal("reasoning choice did not stay inside selected card")
				}
				before := append([]string(nil), s.ids()...)
				h.size.X = 900
				h.frame()
				h.size.X = size.X
				h.frame()
				if !reflect.DeepEqual(before, s.ids()) || s.Initial != "anthropic/claude-sonnet-4.6" || s.Models[0].DisplayName != "My Claude" {
					t.Fatal("responsive relayout changed profile state")
				}

			})
		}
	}
}

func TestNativeModelGridPreservesVisibleAnchorAndVirtualizes(t *testing.T) {
	u := nativeTestUI(t)
	ids := make([]string, 150)
	cards := make([]layout.Widget, len(ids))
	laidOut := map[int]bool{}
	for i := range cards {
		ids[i] = fmt.Sprintf("lab/model-%03d", i)
		cards[i] = func(gtx layout.Context) layout.Dimensions {
			laidOut[i] = true
			return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 200)}
		}
	}
	var ops op.Ops
	draw := func(width int) {
		ops.Reset()
		gtx := layout.Context{Ops: &ops, Constraints: layout.Constraints{Max: image.Pt(width, 1000)}, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Now: time.Now()}
		got := u.modelGrid("test-grid", ids, cards)(gtx)
		if got.Size.Y > 480 {
			t.Fatalf("catalog exceeded its scroll viewport: %v", got.Size)
		}
	}
	draw(892)
	list := u.list("test-grid")
	list.ScrollTo(10)
	clear(laidOut)
	draw(892)
	oldColumns := u.clientState().Grids["test-grid"].columns
	oldFirst := list.Position.First * oldColumns
	if oldFirst == 0 || len(laidOut) > 18 {
		t.Fatalf("large catalog was not virtualized: first=%d rendered=%d", oldFirst, len(laidOut))
	}
	draw(400)
	if columns := u.clientState().Grids["test-grid"].columns; columns != 1 || list.Position.First != oldFirst {
		t.Fatalf("resize lost visible model: columns=%d first=%d want=%d", columns, list.Position.First, oldFirst)
	}
	// A refreshed/reordered catalog anchors to the same model ID, never a stale row.
	anchor := ids[oldFirst]
	ids = append(ids[20:], ids[:20]...)
	draw(400)
	if ids[list.Position.First] != anchor {
		t.Fatalf("catalog reorder lost model anchor %s: %s", anchor, ids[list.Position.First])
	}
}

func TestNativeModelGridVisualOverview(t *testing.T) {
	if os.Getenv("KILO_NATIVE_SCREENSHOTS") == "" {
		t.Skip("set KILO_NATIVE_SCREENSHOTS to render the native catalog")
	}
	u := nativeTestUI(t)
	u.models = nativeGridModels()[:6]
	nativeSeedSharedForTest(t, u, u.models[0])
	u.page = "models"
	u.expanded["library.catalog"] = true
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1160), now: time.Now()}
	h.frame()
	nativeMenuWheel(h, image.Pt(1100, 350), 70)
	nativeGridCapture(t, h, "native-model-grid")
}

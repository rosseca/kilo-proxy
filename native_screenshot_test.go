//go:build desktop

package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// TestNativeScreenshots renders every section to PNG for design review.
// Opt in with KILO_SCREENSHOT_DIR=/path go test -tags desktop -run TestNativeScreenshots.
func TestNativeScreenshots(t *testing.T) {
	dir := os.Getenv("KILO_SCREENSHOT_DIR")
	if dir == "" {
		t.Skip("set KILO_SCREENSHOT_DIR")
	}
	const scale = 2
	shoot := func(u *nativeUI, name string, w, h int) {
		t.Helper()
		win, err := headless.NewWindow(w*scale, h*scale)
		if err != nil {
			t.Fatal(err)
		}
		defer win.Release()
		var router input.Router
		var ops op.Ops
		for i := 0; i < 4; i++ { // settle async state and layout
			ops.Reset()
			gtx := layout.Context{Ops: &ops, Source: router.Source(), Constraints: layout.Exact(image.Pt(w*scale, h*scale)), Metric: unit.Metric{PxPerDp: scale, PxPerSp: scale}, Now: time.Now()}
			u.Layout(gtx)
			router.Frame(&ops)
			u.drain()
			time.Sleep(30 * time.Millisecond)
		}
		if err := win.Frame(&ops); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rectangle{Max: win.Size()})
		if err := win.Screenshot(img); err != nil {
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
	lang := os.Getenv("KILO_SCREENSHOT_LANG")
	u := nativeTestUI(t)
	if lang != "" {
		u.language = lang
	}
	u.page, u.setupStep = "setup", setupConnect
	shoot(u, "01-setup-connect", 1280, 860)
	u.setupStep = setupModels
	shoot(u, "02-setup-models", 1280, 860)
	nativeSeedSharedForTest(t, u, nativeClientModelsForTest()...)
	u.setupStep = setupReady
	shoot(u, "03-setup-ready", 1280, 860)
	u.page = "agents"
	shoot(u, "04-agents", 1280, 900)
	u.page, u.client = "clients", "codex"
	shoot(u, "05-agent-detail-codex", 1280, 1400)
	u.client = "claude"
	shoot(u, "06-agent-detail-claude", 1280, 1400)
	u.page = "models"
	shoot(u, "07-models", 1280, 1100)
	u.page = "activity"
	shoot(u, "08-activity", 1280, 1100)
	u.page = "settings"
	shoot(u, "09-settings", 1280, 1600)
	for i, tab := range []string{"images", "preferences", "terminal", "updates"} {
		u.settingsTab = tab
		shoot(u, fmt.Sprintf("09%c-settings-%s", 'a'+i, tab), 1280, 1100)
	}
	u.settingsTab = ""
	u.page = "connection"
	shoot(u, "10-connection", 1280, 1100)
	u.page = "agents"
	shoot(u, "11-agents-narrow", 860, 900)
}

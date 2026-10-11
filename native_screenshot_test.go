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

	// Model library in onboarding and on the Models page: empty, catalog open and populated.
	m := nativeTestUI(t)
	if lang != "" {
		m.language = lang
	}
	m.models = nativeGridModels()
	m.page, m.setupStep = "setup", setupModels
	shoot(m, "12-setup-models-empty", 1280, 860)
	m.expanded["library.catalog"] = true
	shoot(m, "13-setup-models-catalog", 1280, 1400)
	m.expanded["library.catalog"] = false
	m.page = "models"
	shoot(m, "14-models-empty", 1280, 900)
	m.expanded["library.catalog"] = true
	shoot(m, "15-models-catalog", 1280, 1400)
	m.expanded["library.catalog"] = false
	nativeSeedSharedForTest(t, m, nativeClientModelsForTest()...)
	m.page, m.setupStep = "setup", setupModels
	shoot(m, "16-setup-models-selected", 1280, 1100)
	m.page = "models"
	m.expanded["library.catalog"] = true
	shoot(m, "17-models-catalog-selected", 1280, 1400)

	// Onboarding with the published recommendations, as real users see it.
	r := nativeTestUI(t)
	if lang != "" {
		r.language = lang
	}
	r.models = nativeGridModels()
	notes := []string{"Flagship for everyday coding.", "Balanced quality and price.", "Strong reasoning, large context.", "Popular, fast and cheap.", "Among the most used coding models. Very cheap.", "Reference open coding model."}
	for i := range r.models[:6] {
		r.models[i].Recommendation = &modelRecommendation{Rank: i + 1, Default: i == 0, NoteEN: notes[i], NoteES: notes[i]}
	}
	r.page, r.setupStep = "setup", setupModels
	shoot(r, "18-setup-recommended", 1280, 1600)
	r.addRecommendedModels(r.recommendedModels())
	shoot(r, "19-setup-recommended-added", 1280, 1600)

	// Harnesses redesign: list, folder menu and detail pages with detection done.
	hu, _ := nativeLaunchTestUI(t, "codex", false, false)
	if lang != "" {
		hu.language = lang
	}
	// Async settings loads may reset the language; reapply it before each shot.
	hshoot := func(name string, w, h int) {
		if lang != "" {
			hu.language = lang
		}
		shoot(hu, name, w, h)
	}
	hu.detectLaunchers()
	hu.detectAgentCapabilities()
	hu.detectOpenDesign()
	nativeTestWait(t, hu, func() bool {
		c := hu.clientState()
		return c.LaunchChecked && !hu.busy["GET"+nativeLaunchEndpoint] && !hu.busy["GET/api/claude/info"]
	})
	ha := hu.agentsState()
	now := time.Now()
	ha.Preferences.noteOpened("codex", "", now.Add(-2*time.Hour))
	ha.Preferences.noteOpened("synara", "", now.Add(-30*time.Second))
	ha.Preferences.noteOpened("openmausbot", "", now.Add(-26*time.Hour))
	ha.Preferences.noteRecent(filepath.Join(hu.owner.editorTestRoot, "website"), now.Add(-72*time.Hour))
	ha.Preferences.noteRecent(filepath.Join(hu.owner.editorTestRoot, "notes"), now.Add(-25*time.Hour))
	ha.Preferences.useFolder(hu.owner.editorTestRoot, now)
	hu.page = "agents"
	hshoot("40-harnesses-list", 1280, 1300)
	hu.expanded[uxhFolderMenuID] = true
	hshoot("41-harnesses-folder", 1280, 900)
	hu.expanded[uxhFolderMenuID] = false
	hu.page, hu.client = "clients", "claude"
	hshoot("42-harnesses-claude", 1280, 1300)
	hu.client = "codex"
	hshoot("43-harnesses-codex", 1280, 1300)
	hu.page = "agents"
	hshoot("44-harnesses-narrow", 720, 1300)
	hu.page, hu.client = "clients", "synara"
	hshoot("45-harnesses-synara", 1280, 1500)
}

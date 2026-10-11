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
	lang := os.Getenv("KILO_SCREENSHOT_LANG")
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
			if lang != "" { // async settings loads must not switch the language mid-run
				u.language = lang
			}
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
	// Settings is one page: the whole page, then each block reached from the
	// index (Large images with its method list open), then Kilo's Manage area.
	u.page, u.settingsTab = "settings", "accounts"
	shoot(u, "09-settings", 1280, 1900)
	for i, tab := range []string{"images", "preferences", "terminal", "updates"} {
		u.settingsTab = tab
		u.expanded["images.change"] = tab == "images"
		shoot(u, fmt.Sprintf("09%c-settings-%s", 'a'+i, tab), 1280, 1100)
	}
	u.expanded["uxs.kilo"], u.settingsTab = true, "accounts"
	shoot(u, "09e-settings-kilo-expanded", 1280, 1100)
	u.expanded["uxs.kilo"], u.settingsTab = false, ""
	// A long team name must not squeeze the status column.
	savedTeams := u.state["organizations"]
	u.state["organizations"] = []any{map[string]any{"id": nativeString(u.state, "orgId"), "name": "Leadtech · Plataforma de desarrollo e inteligencia artificial"}}
	shoot(u, "09f-settings-long-team", 1280, 1100)
	u.state["organizations"] = savedTeams
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

	// Activity: Kilo overview, request list, inspector and the local fallback.
	act := nativeTestUI(t)
	if lang != "" {
		act.language = lang
		act.owner.mu.Lock()
		act.owner.config.Language = lang
		act.owner.mu.Unlock()
	}
	act.page = "activity"
	uxaSeedActivityForTest(t, act, true)
	shoot(act, "50-activity-overview", 1280, 1500)
	act.uxa().tab = "requests"
	shoot(act, "51-activity-requests", 1280, 900)
	var events []event
	nativeDecode(act.state["events"], &events)
	act.uxaOpenRequest(events[0])
	nativeTestWait(t, act, func() bool { return act.trace != nil })
	shoot(act, "52-activity-inspector", 1280, 1100)
	shoot(act, "53-activity-inspector-narrow", 860, 1100)
	act.uxa().tab, act.uxa().selected = "conversations", ""
	shoot(act, "54-activity-conversations", 1280, 1400)
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
	// Point detected apps at real bundles on this Mac so the shots show their icons.
	c := hu.clientState()
	for key, bundle := range map[string]string{"claude-desktop": "/Applications/Claude.app", "synara": "/Applications/Synara Beta.app", "t3-code": "/Applications/T3 Code (Nightly).app", "openmausbot": "/Applications/OpenMausBot.app", "zed": "/Applications/Zed.app", "open-design": "/Applications/Open Design.app", "xcode-chat": "/Applications/Xcode.app", "codex": "/Applications/Codex.app"} {
		if _, err := os.Stat(bundle); err == nil {
			info := c.LaunchInfo.Clients[key]
			info.Path = bundle
			c.LaunchInfo.Clients[key] = info
		}
	}
	for key := range uxhIconSources {
		hu.uxhAppIcon(key)
	}
	nativeTestWait(t, hu, func() bool {
		for _, icon := range hu.appIcons {
			if icon.loading {
				return false
			}
		}
		return true
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
	// Onboarding with every pack available, one selected and expanded.
	k := nativeTestUI(t)
	if lang != "" {
		k.language = lang
	}
	k.models = uxmPackCatalogForTest(r.models)
	// Mark the catalog with the published list so the Recommended pack has rows.
	if recommendations, err := parseRecommendedModels(embeddedRecommendedModels); err == nil {
		for i := range k.models {
			if rec, ok := recommendations[k.models[i].ID]; ok {
				k.models[i].Recommendation = &rec
			}
		}
	}
	k.page, k.setupStep = "setup", setupModels
	k.setValue("setup.models.start", "full-stack-dev")
	shoot(k, "30-models-setup-pack", 1280, 1700)

	// Packs: a built-in pack in use, its "Use in…" menu and a personal copy.
	nativeSeedSharedForTest(t, k, k.models[0])
	k.page = "models"
	p := k.packsState()
	p.tab, p.selected = "packs", "full-stack-dev"
	k.assignPack("full-stack-dev", "claude")
	k.assignPack("full-stack-dev", "codex-cli")
	k.setNotice(nativeToneInfo, "")
	shoot(k, "31-models-packs", 1280, 1100)
	k.uxmOpenMenu("packs.use.full-stack-dev", "")
	shoot(k, "32-models-packs-use", 1280, 1100)
	k.setValue("uxm.menu", "")
	p.selected = modelLibraryStartRecommended
	shoot(k, "31b-models-packs-recommended", 1280, 1300)
	p.selected = "full-stack-dev"
	k.setValue("uxm.menu", "")
	k.createPersonalPack("full-stack-dev")
	k.assignPack(p.selected, "opencode")
	k.setNotice(nativeToneInfo, "")
	shoot(k, "33-models-mypacks", 1280, 1100)
	k.uxmOpenMenu("packs.my.menu", "")
	shoot(k, "34-models-mypacks-menu", 1280, 1100)
	k.setValue("uxm.menu", "")
}

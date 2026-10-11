//go:build desktop

package main

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func uxhTestDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func uxhLastPlanDirectory(t *testing.T, r *nativeLaunchRecorder) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.plans) == 0 {
		t.Fatal("no launch plan recorded")
	}
	return r.plans[len(r.plans)-1].Directory
}

func uxhLabelY(h *nativePointerHarness, label string) int {
	for _, node := range h.nodes() {
		if strings.EqualFold(node.Desc.Label, label) {
			return node.Desc.Bounds.Min.Y
		}
	}
	h.t.Fatalf("no label %q", label)
	return 0
}

func TestNativeHarnessesFolderMenuSetsGeneralFolder(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			u, r := nativeLaunchTestUI(t, "codex-cli", false, false)
			u.page, u.language = "agents", lang
			a := u.agentsState()
			now := time.Now()
			a.now = func() time.Time { return now }
			current, other, typed, picked := uxhTestDir(t, "current"), uxhTestDir(t, "other"), uxhTestDir(t, "typed"), uxhTestDir(t, "picked")
			a.Preferences.noteRecent(other, now.Add(-25*time.Hour))
			a.Preferences.useFolder(current, now.Add(-time.Hour))
			h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 900), now: now}
			h.frame()
			change := u.tr("Change", "Cambiar")
			h.click(change, semantic.Button)
			if !u.expanded[uxhFolderMenuID] || !h.selected(uxhShortPath(current), semantic.Button) || h.selected(uxhShortPath(other), semantic.Button) {
				t.Fatal("folder menu did not mark the current folder")
			}
			if !nativeUpdateHasText(h, u.tr("yesterday", "ayer")) || !nativeUpdateHasText(h, u.tr("1 h ago", "hace 1 h")) {
				t.Fatal("recent folders lost their relative times")
			}
			nativeGridCapture(t, h, "uxh-folder-menu-"+lang)
			h.click(uxhShortPath(other), semantic.Button)
			if a.Preferences.Folder != other || u.expanded[uxhFolderMenuID] {
				t.Fatalf("recent folder was not applied: %q", a.Preferences.Folder)
			}

			// A typed path must be an existing absolute folder.
			h.click(change, semantic.Button)
			u.setValue(uxhFolderMenuID+":path", "relative/project")
			h.click(u.tr("Use", "Usar"), semantic.Button)
			if a.Preferences.Folder != other || a.Error == "" {
				t.Fatal("invalid typed folder was accepted")
			}
			u.setValue(uxhFolderMenuID+":path", typed)
			h.click(u.tr("Use", "Usar"), semantic.Button)
			if a.Preferences.Folder != typed || a.Error != "" || u.expanded[uxhFolderMenuID] {
				t.Fatalf("typed folder was not applied: %q %s", a.Preferences.Folder, a.Error)
			}
			if saved, err := readAgentPreferences(u.owner.dir); err != nil || saved.Folder != typed {
				t.Fatalf("general folder was not persisted: %+v %v", saved, err)
			}

			// The system chooser, then an outside click closes the menu.
			a.chooseFolder = func(string) (string, error) { return picked, nil }
			h.click(change, semantic.Button)
			h.click(u.tr("Choose another folder…", "Elegir otra carpeta…"), semantic.Button)
			nativeTestWait(t, u, func() bool { return a.FolderBusy == "" && a.Preferences.Folder == picked })
			h.frame()
			h.click(change, semantic.Button)
			u.clickable(uxhFolderMenuID + ":scrim").Click()
			h.frame()
			if u.expanded[uxhFolderMenuID] {
				t.Fatal("outside click did not close the folder menu")
			}

			// Coding harnesses without their own folder open in the general one.
			u.launchAgent("codex-cli")
			nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
			if r.count() != 1 || uxhLastPlanDirectory(t, r) != picked {
				t.Fatalf("launch ignored the general folder: %s", u.notice)
			}
			if a.Preferences.LastOpened["codex-cli"] != now.Unix() || u.uxhLastUsed("codex-cli") != u.tr("Opened just now", "Abierto ahora") {
				t.Fatal("launch was not recorded as last used")
			}
			if _, own := a.Preferences.Projects["codex-cli"]; own {
				t.Fatal("opening in the general folder created an override")
			}
		})
	}
}

func TestNativeHarnessesPerHarnessFolderOverride(t *testing.T) {
	u, r := nativeLaunchTestUI(t, "codex-cli", false, false)
	a := u.agentsState()
	general, own := uxhTestDir(t, "general"), uxhTestDir(t, "own")
	if err := u.uxhUseGlobalFolder(general); err != nil {
		t.Fatal(err)
	}
	u.agentSetup("codex-cli")
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1400), now: time.Now()}
	h.frame()
	if !nativeUpdateHasText(h, " · the general one") || nativeUpdateHasText(h, "Use the general one") {
		t.Fatal("folder row did not show the general folder")
	}
	a.chooseFolder = func(string) (string, error) { return own, nil }
	h.click("Use another only here", semantic.Button)
	nativeTestWait(t, u, func() bool { return a.FolderBusy == "" })
	h.frame()
	if a.Preferences.Projects["codex-cli"] != own || !nativeUpdateHasText(h, " · only here") {
		t.Fatalf("override was not stored: %+v", a.Preferences.Projects)
	}
	if u.agentProject("opencode") != general {
		t.Fatal("an override leaked into another harness")
	}
	h.click("Open Codex CLI", semantic.Button)
	nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
	if r.count() != 1 || uxhLastPlanDirectory(t, r) != own {
		t.Fatalf("launch ignored the override: %s", u.notice)
	}
	h.frame()
	h.click("Use the general one", semantic.Button)
	if _, exists := a.Preferences.Projects["codex-cli"]; exists || u.agentProject("codex-cli") != general {
		t.Fatal("override was not cleared")
	}
	if saved, err := readAgentPreferences(u.owner.dir); err != nil || saved.Projects["codex-cli"] != "" || saved.Folder != general {
		t.Fatalf("cleared override was not persisted: %+v %v", saved, err)
	}
	u.launchAgent("codex-cli")
	nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
	if r.count() != 2 || uxhLastPlanDirectory(t, r) != general {
		t.Fatalf("launch did not return to the general folder: %s", u.notice)
	}
	h.click("← Harnesses", semantic.Button)
	if u.page != "agents" {
		t.Fatal("back link did not return to the Harnesses list")
	}
}

func TestNativeHarnessesListGroupsAndMissingHarnesses(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			u, _ := nativeLaunchTestUI(t, "codex", false, false)
			u.page, u.language = "agents", lang
			c := u.clientState()
			info := c.LaunchInfo.Clients["zed"]
			info.Installed, info.Available, info.Path = false, false, ""
			c.LaunchInfo.Clients["zed"] = info
			now := time.Now()
			u.agentsState().now = func() time.Time { return now }
			u.agentsState().Preferences.noteOpened("codex", "", now.Add(-3*time.Hour))
			h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 2400), now: time.Now()}
			h.frame()
			order := []string{u.tr("Desktop apps", "Apps de escritorio"), u.tr("Multi-agent", "Multiagente"), "Bots", u.tr("Coding harnesses", "Harnesses de código"), u.tr("Not installed · 1", "No instalados · 1")}
			previous := -1
			for _, title := range order {
				y := uxhLabelY(h, title)
				if y <= previous {
					t.Fatalf("group %q is out of order", title)
				}
				previous = y
			}
			if uxhLabelY(h, "Zed") <= previous {
				t.Fatal("missing harness is not listed under Not installed")
			}
			if _, ok := nativeAgentRowFind(h, "Zed", u.tr("How to install", "Cómo instalarlo")); !ok {
				t.Fatal("missing harness has no install guidance")
			}
			if _, ok := nativeAgentRowFind(h, "Zed", u.tr("Check again", "Comprobar de nuevo")); !ok {
				t.Fatal("missing harness cannot be checked again")
			}
			for _, node := range h.nodes() {
				if node.Desc.Label == u.tr("Zed settings", "Ajustes de Zed") || node.Desc.Label == u.tr("Open Zed", "Abrir Zed") {
					t.Fatalf("missing harness offers %q", node.Desc.Label)
				}
			}
			if !nativeUpdateHasText(h, u.tr("Used 3 h ago", "Usado hace 3 h")) || nativeUpdateHasText(h, u.tr("Opened just now", "Abierto ahora")) {
				t.Fatal("last used caption does not follow the launch history")
			}
			h.click(u.tr("Codex settings", "Ajustes de Codex"), semantic.Button)
			if u.page != "clients" || u.client != "codex" {
				t.Fatal("settings button did not open the harness page")
			}
			h.frame()
			h.target(u.tr("Open Codex", "Abrir Codex"), semantic.Button)
			for _, block := range []string{u.tr("When opening", "Al abrir"), u.tr("If you type while Codex works", "Si escribes mientras Codex trabaja"), u.tr("Advanced", "Avanzado")} {
				uxhLabelY(h, block)
			}
			h.click(u.tr("Add to the current task", "Añadir a la tarea actual"), semantic.Button)
			if u.value("client:codex:queue-mode") != codexQueueModeSteer || !h.selected(u.tr("Add to the current task", "Añadir a la tarea actual"), semantic.Button) {
				t.Fatal("queue option card did not select steering")
			}
		})
	}
}

func TestNativeHarnessesRelativeTimes(t *testing.T) {
	u := nativeTestUI(t)
	now := time.Unix(1_800_000_000, 0)
	a := u.agentsState()
	a.now = func() time.Time { return now }
	cases := []struct {
		ago    time.Duration
		en, es string
	}{
		{30 * time.Second, "Opened just now", "Abierto ahora"},
		{20 * time.Minute, "Used 20 min ago", "Usado hace 20 min"},
		{5 * time.Hour, "Used 5 h ago", "Usado hace 5 h"},
		{30 * time.Hour, "Yesterday", "Ayer"},
		{4 * 24 * time.Hour, "Used 4 days ago", "Usado hace 4 días"},
	}
	for _, c := range cases {
		a.Preferences.noteOpened("codex", "", now.Add(-c.ago))
		for _, lang := range []string{"en", "es"} {
			u.language = lang
			if got := u.uxhLastUsed("codex"); got != u.tr(c.en, c.es) {
				t.Fatalf("%v in %s: %q", c.ago, lang, got)
			}
		}
	}
	delete(a.Preferences.LastOpened, "codex")
	if u.uxhLastUsed("codex") != "" {
		t.Fatal("never opened harness shows a last use")
	}
}

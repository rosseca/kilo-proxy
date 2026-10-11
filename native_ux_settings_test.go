//go:build desktop

package main

import (
	"image"
	"net/http"
	"testing"
	"time"

	"gioui.org/io/key"
	"gioui.org/io/semantic"
)

// nativeUxsSavedConnection saves the fixture's Kilo key and team so later
// changes start from a ready connection.
func nativeUxsSavedConnection(t *testing.T, u *nativeUI) {
	t.Helper()
	nativeTestFrame(t, u)
	u.setValue("connection.org", "e2e-team")
	u.setValue("connection.key", "synthetic-kilo-personal-key")
	u.uxsApplyConnection()
	nativeUxsSettle(t, u, func() bool { return nativeBool(u.state, "kiloReady") && !u.uxsConnectionBusy() })
}

func nativeUxsProxyServer(u *nativeUI) *http.Server {
	u.owner.mu.Lock()
	defer u.owner.mu.Unlock()
	return u.owner.proxyServer
}

func TestUxsTeamChangePersistsAndRestartsRunningProxy(t *testing.T) {
	u := nativeTestUI(t)
	nativeUxsSavedConnection(t, u)
	u.call("POST", "/api/start", map[string]any{}, u.acceptState)
	nativeTestWait(t, u, func() bool { return nativeBool(u.state, "running") })
	before := nativeUxsProxyServer(u)
	// Loading teams needs a stopped connection; the proxy comes back afterwards.
	u.clickable("uxs.team.toggle").Click()
	nativeTestFrame(t, u)
	u.clickable("connection.teams").Click()
	nativeTestFrame(t, u)
	nativeUxsSettle(t, u, func() bool {
		return len(nativeArray(u.state, "organizations")) == 2 && !u.uxsConnectionBusy() && nativeBool(u.state, "running")
	})
	u.clickable("team.other-team").Click()
	nativeTestFrame(t, u)
	nativeUxsSettle(t, u, func() bool {
		saved, err := readSettings(u.owner.dir)
		return err == nil && saved.OrgID == "other-team" && nativeBool(u.state, "running") && !u.uxsConnectionBusy()
	})
	if nativeString(u.state, "orgId") != "other-team" || !nativeBool(u.state, "kiloReady") {
		t.Fatalf("team change was not applied: org=%q ready=%v", nativeString(u.state, "orgId"), nativeBool(u.state, "kiloReady"))
	}
	if after := nativeUxsProxyServer(u); after == nil || after == before {
		t.Fatal("the running proxy was not restarted with the new team")
	}
	if u.expanded["uxs.team"] {
		t.Fatal("choosing a team left the team menu open")
	}
}

func TestUxsAutoFieldAppliesOnEnter(t *testing.T) {
	u := nativeTestUI(t)
	nativeUxsSavedConnection(t, u)
	u.page = "settings"
	u.expanded["uxs.kilo"] = true
	u.setValue("connection.org", "")
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 820), now: time.Now()}
	h.frame()
	label := "org_…" // the field placeholder labels its editor
	h.reveal(label, semantic.Editor)
	h.click(label, semantic.Editor)
	if !h.router.Source().Focused(u.editor("connection.org")) {
		t.Fatal("pointer did not focus the organization field")
	}
	u.setValue("connection.org", "other-team")
	h.frame()
	if saved, _ := readSettings(u.owner.dir); saved.OrgID != "e2e-team" || u.busy["POST/api/config"] {
		t.Fatal("typing saved before Enter or blur")
	}
	h.router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.frame()
	h.frame()
	nativeTestWait(t, u, func() bool {
		saved, err := readSettings(u.owner.dir)
		return err == nil && saved.OrgID == "other-team" && !u.uxsConnectionBusy()
	})
	if nativeBool(u.state, "running") {
		t.Fatal("saving a stopped connection started the proxy")
	}
}

func TestUxsRememberToggleSaves(t *testing.T) {
	u := nativeTestUI(t)
	nativeUxsSavedConnection(t, u)
	u.page = "settings"
	u.expanded["uxs.kilo"] = true
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 820), now: time.Now()}
	h.frame()
	label := "Remember the key in the system keychain"
	h.reveal(label, semantic.Switch)
	if h.selected(label, semantic.Switch) {
		t.Fatal("remember started on although the fixture saved it off")
	}
	h.click(label, semantic.Switch)
	if !u.checked("connection.remember") {
		t.Fatal("pointer did not turn remember on")
	}
	nativeTestWait(t, u, func() bool { return !u.uxsConnectionBusy() && !u.busy["GET/api/state"] })
	saved, err := readSettings(u.owner.dir)
	if err != nil || !saved.Remember || saved.OrgID != "e2e-team" {
		t.Fatalf("remember choice did not save itself: %+v %v", saved, err)
	}
}

func TestUxsLegacyTabJumpsAndIndexFollows(t *testing.T) {
	u := nativeTestUI(t)
	u.terminalCommands = &nativeTerminalCommands{Started: true, Checked: true, ManualStarted: true, ManualChecked: true}
	u.page = "settings"
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 820), now: time.Now()}
	h.frame()
	if !h.selected("Accounts", semantic.Button) {
		t.Fatal("the index does not start at Accounts")
	}
	// Other pages still ask for a former tab; it becomes a jump to that block.
	u.settingsTab = "images"
	h.frame()
	h.frame()
	list := u.list("page.settings")
	// Near the end the list clamps the offset; the block is then on screen.
	if u.settingsTab != "" || list.Position.Offset != u.uxs().blockY["images"] && list.Position.BeforeEnd {
		t.Fatalf("legacy images tab did not jump: offset=%d want %d", list.Position.Offset, u.uxs().blockY["images"])
	}
	if !h.selected("Large images", semantic.Button) {
		t.Fatal("the index did not follow the jump")
	}
	h.click("Connection", semantic.Button)
	h.frame()
	if list.Position.Offset != u.uxs().blockY["connection"] || !h.selected("Connection", semantic.Button) || h.selected("Large images", semantic.Button) {
		t.Fatalf("index click did not jump to Connection: offset=%d", list.Position.Offset)
	}
	// The last block cannot reach the top; the index still marks it.
	h.click("Updates", semantic.Button)
	h.frame()
	if !h.selected("Updates", semantic.Button) {
		t.Fatal("jumping to Updates at the end of the page did not mark it")
	}
}

func TestUxsAgo(t *testing.T) {
	u := &nativeUI{language: "es"}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	for value, want := range map[string]string{
		"2026-10-11T11:59:40Z": "hace un momento",
		"2026-10-11T11:55:00Z": "hace 5 min",
		"2026-10-11T09:00:00Z": "hace 3 h",
		"2026-10-08T12:00:00Z": "hace 3 días",
		"not a time":           "",
	} {
		if got := u.uxsAgo(value, now); got != want {
			t.Errorf("%q: %q, want %q", value, got, want)
		}
	}
	u.language = "en"
	if got := u.uxsAgo("2026-10-11T11:55:00Z", now); got != "5 min ago" {
		t.Errorf("english: %q", got)
	}
}

func TestUxsChatGPTChangePausesAndRestartsProxy(t *testing.T) {
	u := nativeTestUI(t)
	nativeUxsSavedConnection(t, u)
	u.call("POST", "/api/start", map[string]any{}, u.acceptState)
	nativeTestWait(t, u, func() bool { return nativeBool(u.state, "running") })
	before := nativeUxsProxyServer(u)
	u.uxsChatGPT("logout")
	nativeUxsSettle(t, u, func() bool {
		return !u.uxsConnectionBusy() && nativeBool(u.state, "running") && nativeUxsProxyServer(u) != before
	})
	if u.uxs().resume {
		t.Fatal("the proxy restart is still pending")
	}
}

// An unknown team name shows a short ID instead of the full UUID; a saved
// name wins as soon as the state carries it.
func TestUxsTeamNameFallsBackToShortID(t *testing.T) {
	u := nativeTestUI(t)
	id := "f9733db8-31ff-4096-8136-841b2502f280"
	u.state["orgId"], u.state["hasKey"], u.state["organizations"], u.state["orgName"] = id, false, []any{}, ""
	if got := u.proxyTeam(); got != "f9733db8…" {
		t.Fatalf("short id = %q", got)
	}
	u.state["orgName"] = "Rosseca"
	if got, menu := u.proxyTeam(), u.uxsTeamName(id); got != "Rosseca" || menu != "Rosseca" {
		t.Fatalf("saved name = %q / %q", got, menu)
	}
	if got := u.teamName("short-id"); got != "short-id" {
		t.Fatalf("short ids stay whole: %q", got)
	}
}

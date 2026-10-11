//go:build desktop

package main

import (
	"strings"
	"testing"
)

// "Use in…" assigns the pack to one harness, a second click returns it to
// the shared list, and Claude Desktop is refused for packs without Claude.
func TestUxmPackUseMenuAssignsTogglesAndRestricts(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	u.models = nativePackModelsForTest()
	nativeSeedSharedForTest(t, u, u.models[0])
	p := u.packsState()
	p.tab, p.selected = "packs", "full-stack-dev"
	nativeTestFrame(t, u)
	menu := "packs.use.full-stack-dev"
	uxmClickMenuItem(t, u, menu, menu+".claude")
	if p.file.Assignments["claude"] != "full-stack-dev" {
		t.Fatalf("Use in… did not assign the pack: %v", p.file.Assignments)
	}
	if !strings.Contains(u.notice, "Claude Code will use “Full Stack Dev”") {
		t.Fatalf("missing assignment toast: %q", u.notice)
	}
	if users := u.uxmPackUsers("full-stack-dev"); len(users) != 1 || users[0] != "Claude Code" {
		t.Fatalf("pack users: %v", users)
	}
	uxmClickMenuItem(t, u, menu, menu+".claude")
	if p.file.Assignments["claude"] != "" {
		t.Fatal("choosing the harness again did not return it to your models")
	}
	// Claude Desktop accepts this pack (it holds Claude Sonnet 5.5) …
	uxmClickMenuItem(t, u, menu, menu+".claude-desktop")
	if p.file.Assignments["claude-desktop"] != "full-stack-dev" {
		t.Fatal("Claude Desktop could not use a pack with a Claude model")
	}
	// … but not a personal pack without Claude models.
	u.createPersonalPack("")
	id := p.file.Personal[0].ID
	if !u.uxmAddPackModel(id, u.models[0]) {
		t.Fatal("could not add a model to the personal pack")
	}
	if u.uxmPackClaudeOK(id) {
		t.Fatal("a pack without Claude models was accepted for Claude Desktop")
	}
	uxmClickMenuItem(t, u, "packs.use."+id, "packs.use."+id+".claude-desktop")
	if p.file.Assignments["claude-desktop"] != "full-stack-dev" {
		t.Fatal("a disabled Claude Desktop row changed the assignment")
	}
}

// The personal pack header menu duplicates a pack; row menus reorder and the
// role label edits in place.
func TestUxmPersonalPackDuplicateReorderAndRole(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	u.models = nativePackModelsForTest()
	nativeSeedSharedForTest(t, u, u.models[0])
	u.createPersonalPack("qa-bug-hunter")
	p := u.packsState()
	id := p.file.Personal[0].ID
	nativeTestFrame(t, u)
	first, second := p.file.Personal[0].Library.Models[0].ID, p.file.Personal[0].Library.Models[1].ID
	uxmClickMenuItem(t, u, "packs.my.row."+second, "packs.my.up."+second)
	if got := p.file.personalPack(id).Library.Models; got[0].ID != second || got[1].ID != first {
		t.Fatalf("Move up did not reorder: %v", got)
	}
	uxmClickMenuItem(t, u, "packs.my.row."+second, "packs.my.down."+second)
	if got := p.file.personalPack(id).Library.Models; got[0].ID != first {
		t.Fatalf("Move down did not reorder: %v", got)
	}
	roleID := "packs.my.role." + id + "." + first
	u.clickable(roleID).Click()
	nativeTestFrame(t, u)
	u.setValue(roleID+".text", "Triage")
	u.clickable(roleID + ".save").Click()
	nativeTestFrame(t, u)
	if p.file.personalPack(id).Roles[first] != "Triage" || u.expanded[roleID] {
		t.Fatal("inline role edit was not saved and closed")
	}
	uxmClickMenuItem(t, u, "packs.my.menu", "packs.my.duplicate."+id)
	if len(p.file.Personal) != 2 || p.selected == id {
		t.Fatalf("Duplicate did not create and select a copy: %+v", p.file.Personal)
	}
	clone := p.file.personalPack(p.selected)
	if clone.BasedOn != "qa-bug-hunter" || len(clone.Library.Models) != 3 || clone.Roles[first] != "Triage" || !strings.HasSuffix(clone.Name, "(copy)") {
		t.Fatalf("duplicate lost data: %+v", clone)
	}
	clone.Roles[first] = "changed"
	if p.file.personalPack(id).Roles[first] != "Triage" {
		t.Fatal("duplicate shares roles with the original")
	}
}

// A personal pack holds at most 50 models and only catalog models.
func TestUxmPersonalPackLimitAndCatalogOnly(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	u.models = nativePackModelsForTest()
	u.createPersonalPack("")
	p := u.packsState()
	id := p.file.Personal[0].ID
	if u.uxmAddPackModel(id, modelInfo{ID: "vendor/missing"}) {
		t.Fatal("a model missing from the catalog was added to a pack")
	}
	for i := 0; i < 50; i++ {
		m := modelInfo{ID: "vendor/extra-" + string(rune('a'+i/26)) + string(rune('a'+i%26)), Name: "Extra"}
		u.models = append(u.models, m)
		if !u.uxmAddPackModel(id, m) {
			t.Fatalf("add %d failed: %s", i, u.notice)
		}
	}
	if u.uxmAddPackModel(id, u.models[0]) || len(p.file.personalPack(id).Library.Models) != 50 {
		t.Fatal("a pack accepted a 51st model")
	}
}

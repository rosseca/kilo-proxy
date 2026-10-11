package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestAgentPreferencesLoadRetiredIntegrationWithoutLosingOtherProjects(t *testing.T) {
	dir := t.TempDir()
	project, oldProject := filepath.Join(dir, "active-project"), filepath.Join(dir, "old-project")
	previous := agentPreferences{
		Projects:     map[string]string{"cursor": oldProject, "claude": project, "opencode": project},
		Recent:       []string{project, oldProject},
		CodexAppPath: filepath.Join(dir, "Codex.app"),
	}
	data, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-preferences.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := readAgentPreferences(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Version 1 remembered a folder per agent; migration keeps every agent
	// opening where it did, through the global folder plus overrides only.
	if loaded.Version != agentPreferencesVersion || loaded.Folder != project || len(loaded.Projects) != 0 ||
		!reflect.DeepEqual(loaded.Recent, previous.Recent) || loaded.CodexAppPath != previous.CodexAppPath {
		t.Fatalf("retired integration discarded other preferences: %+v", loaded)
	}
	if err := writeAgentPreferences(dir, loaded); err != nil {
		t.Fatal(err)
	}
	reloaded, err := readAgentPreferences(dir)
	if err != nil || !reflect.DeepEqual(reloaded, loaded) {
		t.Fatalf("migrated preferences did not survive restart: %+v %v", reloaded, err)
	}
}

func TestAgentPreferencesMigrationKeepsDifferentFoldersAsOverrides(t *testing.T) {
	dir := t.TempDir()
	shared, own := filepath.Join(dir, "shared"), filepath.Join(dir, "own")
	data, err := json.Marshal(agentPreferences{Projects: map[string]string{"claude": shared, "opencode": own, "zed": ""}, Recent: []string{shared, own}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-preferences.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := readAgentPreferences(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Folder != shared || !reflect.DeepEqual(loaded.Projects, map[string]string{"opencode": own}) {
		t.Fatalf("migration lost a per-agent folder: %+v", loaded)
	}
	// Without recents, the first remembered folder by agent name becomes global.
	p := agentPreferences{Projects: map[string]string{"opencode": own, "claude": shared}}
	p.migrate()
	if p.Folder != shared || !reflect.DeepEqual(p.Projects, map[string]string{"opencode": own}) {
		t.Fatalf("migration without recents: %+v", p)
	}
	// Migrated preferences are not migrated again.
	p.Projects["claude"] = shared
	p.migrate()
	if p.Projects["claude"] != shared {
		t.Fatal("version 2 overrides were rewritten")
	}
}

func TestAgentPreferencesGlobalFolderHistory(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	now := time.Unix(1_800_000_000, 0)
	p := agentPreferences{}
	p.useFolder(first, now.Add(-time.Hour))
	p.noteOpened("claude", second, now)
	p.noteOpened("codex", "", now)
	if p.Folder != first || !reflect.DeepEqual(p.Recent, []string{second, first}) {
		t.Fatalf("recent folders: %+v", p)
	}
	if p.RecentUsed[first] != now.Add(-time.Hour).Unix() || p.RecentUsed[second] != now.Unix() || p.LastOpened["codex"] != now.Unix() || p.LastOpened["claude"] != now.Unix() {
		t.Fatalf("history times: %+v", p)
	}
	p.rememberProject("claude", second)
	p.rememberProject("claude", "")
	if _, ok := p.Projects["claude"]; ok {
		t.Fatal("an empty folder did not clear the override")
	}
	if err := writeAgentPreferences(dir, p); err != nil {
		t.Fatal(err)
	}
	loaded, err := readAgentPreferences(dir)
	if err != nil || loaded.Folder != first || loaded.LastOpened["codex"] != now.Unix() || loaded.RecentUsed[first] != now.Add(-time.Hour).Unix() {
		t.Fatalf("history did not survive restart: %+v %v", loaded, err)
	}
	p.LastOpened["retired"] = 1
	if err := writeAgentPreferences(dir, p); err == nil {
		t.Fatal("unknown harness history persisted")
	}
}

func TestAgentPreferencesRememberPerAgentAndRecentProjects(t *testing.T) {
	dir := t.TempDir()
	p, err := readAgentPreferences(dir)
	if err != nil || len(p.Projects) != 0 {
		t.Fatalf("empty preferences: %+v %v", p, err)
	}
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	p.rememberProject("codex", first)
	p.rememberProject("claude", second)
	p.rememberProject("codex", first)
	if err := writeAgentPreferences(dir, p); err != nil {
		t.Fatal(err)
	}
	loaded, err := readAgentPreferences(dir)
	if err != nil || !reflect.DeepEqual(loaded, p) {
		t.Fatalf("preferences did not survive restart: %+v %v", loaded, err)
	}
	if loaded.Projects["codex"] != first || loaded.Projects["claude"] != second || !reflect.DeepEqual(loaded.Recent, []string{first, second}) {
		t.Fatalf("projects mixed across agents: %+v", loaded)
	}
	info, err := os.Stat(filepath.Join(dir, "agent-preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("preferences permissions: %v", info.Mode())
	}
}

func TestAgentPreferencesAtomicValidationAndRecentLimit(t *testing.T) {
	dir := t.TempDir()
	p := agentPreferences{}
	for i := 0; i < 10; i++ {
		p.rememberProject("codex", filepath.Join(dir, string(rune('a'+i))))
	}
	if len(p.Recent) != agentRecentLimit {
		t.Fatal("recent list was not bounded")
	}
	if err := writeAgentPreferences(dir, p); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "agent-preferences.json"))
	p.Projects["codex"] = "relative/path"
	if err := writeAgentPreferences(dir, p); err == nil {
		t.Fatal("invalid path persisted")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "agent-preferences.json"))
	if string(before) != string(after) {
		t.Fatal("failed update replaced previous preferences")
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".agent-preferences-*"))
	if len(files) != 0 {
		t.Fatal("temporary files leaked")
	}
}

func TestAgentPreferencesCorruptionIsReported(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agent-preferences.json"), []byte(`{"projects":`), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := readAgentPreferences(dir)
	if err == nil || p.Projects == nil {
		t.Fatal("corruption was hidden or returned unsafe state")
	}
}

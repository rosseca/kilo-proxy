package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

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

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTerminalModelLibraryUsesAssignedPackAndSharedFallback(t *testing.T) {
	dir := t.TempDir()
	a, err := newApp(dir, &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.requestQuit)
	a.config.OrgID = "test-team"
	catalog := nativeCachedCatalog{SchemaVersion: 1, OrgID: "test-team", Models: []modelInfo{
		{ID: "vendor/qa", Name: "QA", ContextWindow: 200000, MaxOutputTokens: 8192},
		{ID: "vendor/other", Name: "Other", ContextWindow: 128000, MaxOutputTokens: 4096},
	}}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model-catalog.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	shared := modelLibrary{SchemaVersion: 1, DefaultModel: "vendor/other", Models: []modelLibraryItem{{ID: "vendor/other", ContextPreset: contextPresetRecommended}}}
	packs := emptyModelPacksFile()
	packs.Personal = []personalModelPack{{
		ID: "mine-qa", Name: "My QA", Library: modelLibrary{SchemaVersion: 1, DefaultModel: "vendor/qa", Models: []modelLibraryItem{{ID: "vendor/qa", ContextPreset: contextPresetRecommended}}},
	}}
	packs.Assignments["codex-cli"] = "mine-qa"
	if err := newModelPacksStore(dir).save(packs, false); err != nil {
		t.Fatal(err)
	}
	got, err := a.terminalModelLibrary("codex-cli", shared)
	if err != nil || got.DefaultModel != "vendor/qa" || len(got.Models) != 1 || got.Models[0].ID != "vendor/qa" {
		t.Fatalf("terminal command ignored assigned pack: %+v, %v", got, err)
	}
	fallback, err := a.terminalModelLibrary("claude", shared)
	if err != nil || !reflect.DeepEqual(fallback, shared) {
		t.Fatalf("unassigned terminal command changed its shared selection: %+v, %v", fallback, err)
	}
	if shared.DefaultModel != "vendor/other" {
		t.Fatal("pack switch changed the shared library")
	}
	packs.Assignments["codex-cli"] = "full-stack-dev"
	if err := newModelPacksStore(dir).save(packs, false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.terminalModelLibrary("codex-cli", shared); err == nil || !strings.Contains(err.Error(), "Refresh Models") {
		t.Fatalf("builtin pack with missing team models was accepted: %v", err)
	}
}

func TestTerminalModelLibraryDoesNotIgnoreCorruptPackFile(t *testing.T) {
	dir := t.TempDir()
	a, err := newApp(dir, &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.requestQuit)
	if err := os.WriteFile(filepath.Join(dir, "model-packs.json"), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.terminalModelLibrary("codex-cli", emptyModelLibrary()); err == nil || !strings.Contains(err.Error(), "recovery") {
		t.Fatalf("corrupt assignments silently fell back to shared library: %v", err)
	}
}

func TestTerminalPrepareHTTPUsesAssignedPackAndSwitchesBack(t *testing.T) {
	a := terminalTestApp(t)
	cache, err := json.Marshal(nativeCachedCatalog{SchemaVersion: 1, OrgID: a.config.OrgID, Models: []modelInfo{
		{ID: "vendor/one", Name: "First", ContextWindow: 128000, MaxOutputTokens: 4096},
		{ID: "vendor/two", Name: "Second", ContextWindow: 128000, MaxOutputTokens: 4096},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicCatalogFile(filepath.Join(a.dir, "model-catalog.json"), cache); err != nil {
		t.Fatal(err)
	}
	store := newModelPacksStore(a.dir)
	file := emptyModelPacksFile()
	file.Personal = []personalModelPack{{ID: "mine-terminal", Name: "My Terminal", Library: modelLibrary{
		SchemaVersion: 1, DefaultModel: "vendor/one", Models: []modelLibraryItem{{ID: "vendor/one", ContextPreset: contextPresetRecommended}},
	}}}
	file.Assignments["codex-cli"] = "mine-terminal"
	if err := store.save(file, false); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(terminalPrepareRequest{Client: "codex-cli", Directory: a.launcher.home})
	if w := adminRequest(a, "terminal/prepare", string(request)); w.Code != 200 {
		t.Fatalf("assigned terminal pack was rejected: %d %s", w.Code, w.Body.String())
	}
	catalog, err := os.ReadFile(filepath.Join(a.codexCLIProfileDir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if first, err := validateCatalog(catalog); err != nil || first != "vendor/one" || strings.Contains(string(catalog), `"vendor/two"`) {
		t.Fatalf("terminal profile ignored assigned pack: %s, %v", first, err)
	}
	delete(file.Assignments, "codex-cli")
	if err := newModelPacksStore(a.dir).save(file, false); err != nil {
		t.Fatal(err)
	}
	if w := adminRequest(a, "terminal/prepare", string(request)); w.Code != 200 {
		t.Fatalf("shared-library terminal command was rejected: %d %s", w.Code, w.Body.String())
	}
	catalog, err = os.ReadFile(filepath.Join(a.codexCLIProfileDir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if first, err := validateCatalog(catalog); err != nil || first != "vendor/two" || !strings.Contains(string(catalog), `"vendor/one"`) {
		t.Fatalf("terminal command did not restore shared models: %s, %v", first, err)
	}
	if got := a.modelLibrary.snapshot().Library.DefaultModel; got != "vendor/two" {
		t.Fatalf("pack switch changed saved shared default: %s", got)
	}
}

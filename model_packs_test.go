package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func packFixture() personalModelPack {
	return personalModelPack{
		ID: "mine-example", Name: "My QA", BasedOn: "qa-bug-hunter",
		Library: modelLibrary{SchemaVersion: 1, DefaultModel: "vendor/test", Models: []modelLibraryItem{{ID: "vendor/test", ContextPreset: contextPresetRecommended}}},
	}
}

func TestCuratedPacksHaveValidDistinctDefaultModels(t *testing.T) {
	catalog, err := decodeCuratedPacks(embeddedModelPacks)
	if err != nil || len(catalog.Packs) != 5 {
		t.Fatalf("curated packs: %d, %v", len(catalog.Packs), err)
	}
	seen := map[string]bool{}
	for _, pack := range catalog.Packs {
		if seen[pack.ID] || len(pack.Models) != 3 || len(pack.Optional) != 1 {
			t.Fatalf("duplicate or incomplete pack: %#v", pack)
		}
		seen[pack.ID] = true
		found := false
		for _, model := range pack.Models {
			found = found || model.ID == pack.DefaultModel
		}
		if !found {
			t.Fatalf("default outside core models: %s", pack.ID)
		}
	}
	bad := strings.Replace(string(embeddedModelPacks), `"schemaVersion": 1`, `"schemaVersion": 2`, 1)
	if _, err := decodeCuratedPacks([]byte(bad)); err == nil {
		t.Fatal("accepted an unsupported curated schema")
	}
}

func TestPersonalPacksValidateWithoutTouchingSharedLibrary(t *testing.T) {
	file := emptyModelPacksFile()
	file.Personal = []personalModelPack{packFixture()}
	file.Assignments["codex"] = file.Personal[0].ID
	if err := validateModelPacksFile(file); err != nil {
		t.Fatal(err)
	}
	copy := cloneModelPacksFile(file)
	copy.Personal[0].Library.Models[0].ID = "vendor/changed"
	copy.Assignments["codex"] = "full-stack-dev"
	if file.Personal[0].Library.Models[0].ID != "vendor/test" || file.Assignments["codex"] != "mine-example" {
		t.Fatal("pack clone mutates original selections or assignment")
	}
	for _, change := range []struct {
		name   string
		modify func(*modelPacksFile)
	}{
		{"unknown agent", func(f *modelPacksFile) { f.Assignments["arbitrary"] = "mine-example" }},
		{"empty assigned pack", func(f *modelPacksFile) { f.Personal[0].Library = emptyModelLibrary() }},
		{"duplicate personal ID", func(f *modelPacksFile) { f.Personal = append(f.Personal, f.Personal[0]) }},
		{"invalid name", func(f *modelPacksFile) { f.Personal[0].Name = "bad\nname" }},
		{"invalid model", func(f *modelPacksFile) { f.Personal[0].Library.Models[0].ID = "bad id" }},
		{"role for a removed model", func(f *modelPacksFile) { f.Personal[0].Roles = map[string]string{"vendor/absent": "Reviewer"} }},
	} {
		t.Run(change.name, func(t *testing.T) {
			bad := cloneModelPacksFile(file)
			change.modify(&bad)
			if err := validateModelPacksFile(bad); err == nil {
				t.Fatal("invalid saved pack accepted")
			}
		})
	}
}

func TestPersonalPackStorePreservesBackupAndRejectsConflicts(t *testing.T) {
	dir := t.TempDir()
	store := newModelPacksStore(dir)
	file := emptyModelPacksFile()
	file.Personal = []personalModelPack{packFixture()}
	file.Assignments["codex"] = "mine-example"
	if err := store.save(file, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "model-packs.json")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("saved pack permissions: %#v, %v", info, err)
	}
	// On Windows Go reports permission bits derived from file attributes, not
	// the inherited ACL. CreateTemp uses the private test/config directory ACL.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("saved pack permissions: %v", info.Mode().Perm())
	}
	other := newModelPacksStore(dir)
	if len(other.value.Personal) != 1 || other.value.Assignments["codex"] != "mine-example" {
		t.Fatal("personal pack or agent assignment lost on restart")
	}
	changed := cloneModelPacksFile(file)
	changed.Personal[0].Name = "Different name"
	if err := other.save(changed, false); err != nil {
		t.Fatal(err)
	}
	if err := store.save(file, false); !errors.Is(err, errModelPacksConflict) {
		t.Fatalf("external edit was overwritten: %v", err)
	}
	backup, err := readModelPacksFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeModelPacksFile(backup)
	if err != nil || restored.Personal[0].Name != "My QA" {
		t.Fatalf("backup did not preserve preceding pack: %+v, %v", restored, err)
	}
}

func TestPersonalPackRecoveryNeverOverwritesDamagedOriginal(t *testing.T) {
	dir := t.TempDir()
	store := newModelPacksStore(dir)
	file := emptyModelPacksFile()
	file.Personal = []personalModelPack{packFixture()}
	if err := store.save(file, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "model-packs.json")
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened := newModelPacksStore(dir)
	if !reopened.recovery || len(reopened.value.Personal) != 1 {
		t.Fatal("damaged primary did not show last good backup")
	}
	if err := reopened.save(reopened.value, false); !errors.Is(err, errModelPacksRecovery) {
		t.Fatalf("damaged file overwritten without recovery: %v", err)
	}
	if err := reopened.save(reopened.value, true); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(path + ".corrupt-*.json")
	if err != nil || len(matches) != 1 {
		t.Fatalf("damaged original was not archived: %v, %v", matches, err)
	}
	if current := newModelPacksStore(dir); current.recovery || len(current.value.Personal) != 1 {
		t.Fatal("recovered packs cannot be reloaded")
	}
}

func TestPersonalPacksWithoutValidBackupCannotBeResetSilently(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model-packs.json")
	for _, file := range []string{path, path + ".bak"} {
		if err := os.WriteFile(file, []byte("{broken"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store := newModelPacksStore(dir)
	if !store.recovery || store.recoverable {
		t.Fatal("damaged primary and backup were presented as recoverable")
	}
	if err := store.save(emptyModelPacksFile(), true); err == nil {
		t.Fatal("a missing valid backup allowed an empty overwrite")
	}
	for _, file := range []string{path, path + ".bak"} {
		data, err := os.ReadFile(file)
		if err != nil || string(data) != "{broken" {
			t.Fatalf("damaged original changed: %s %q %v", file, data, err)
		}
	}
}

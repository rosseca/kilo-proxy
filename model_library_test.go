package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func testModelLibrary() modelLibrary {
	return modelLibrary{SchemaVersion: 1, DefaultModel: "vendor/one", Models: []modelLibraryItem{
		{ID: "vendor/two", DisplayName: "Fast ✨", ReasoningCustom: true},
		{ID: "vendor/one", DisplayName: "Daily coding", ReasoningEffort: "high", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, ContextWindow: 200000, MaxOutputTokens: 32000},
	}}
}

func libraryRequest(a *app, method string, value any) *httptest.ResponseRecorder {
	var body bytes.Buffer
	if value != nil {
		json.NewEncoder(&body).Encode(value)
	}
	r := httptest.NewRequest(method, "http://"+a.adminHost+"/api/model-library", &body)
	r.Header.Set("Authorization", "Bearer "+a.adminToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.adminHandler().ServeHTTP(w, r)
	return w
}

func decodeLibraryResponse(t *testing.T, response *httptest.ResponseRecorder) modelLibraryState {
	t.Helper()
	var state modelLibraryState
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestModelLibraryRestartPreservesSelectionAndPreferences(t *testing.T) {
	a := testApp(t)
	a.apiKey = "upstream-secret-must-not-persist"
	a.config.LocalKey = "local-secret-must-not-persist"
	a.config.OrgID = "organization-must-not-persist"
	initial := libraryRequest(a, http.MethodGet, nil)
	if initial.Code != http.StatusOK || !strings.Contains(initial.Body.String(), `"models":[]`) {
		t.Fatalf("initial response: %d %s", initial.Code, initial.Body.String())
	}
	library := testModelLibrary()
	response := libraryRequest(a, http.MethodPut, map[string]any{"library": library, "revision": 0})
	if response.Code != http.StatusOK {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	state := decodeLibraryResponse(t, response)
	if state.Revision != 1 || !reflect.DeepEqual(state.Library, library) || state.Warning != "" {
		t.Fatalf("saved state: %#v", state)
	}
	restarted, err := newApp(a.dir, a.vault)
	if err != nil {
		t.Fatal(err)
	}
	if restored := restarted.modelLibrary.snapshot(); !reflect.DeepEqual(restored.Library, library) || restored.RecoveryRequired {
		t.Fatalf("restored state: %#v", restored)
	}
	for _, name := range []string{"models.json", "models.json.bak"} {
		data, err := os.ReadFile(filepath.Join(a.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{a.apiKey, a.config.LocalKey, a.config.OrgID, "apiKey", "inputPrice", "outputPrice", "provider", "reasoningEfforts", "inputModalities"} {
			if bytes.Contains(data, []byte(forbidden)) {
				t.Fatalf("%s contains unrelated data: %q", name, forbidden)
			}
		}
		info, _ := os.Stat(filepath.Join(a.dir, name))
		if info.Mode().Perm() != 0600 {
			t.Fatalf("file permissions = %v", info.Mode())
		}
	}
	// Input and output slices must not alias the authoritative stored revision.
	library.Models[1].ReasoningLevels[0] = "ultra"
	state.Library.Models[1].DisplayName = "Mutated after saving"
	state.Library.Models[1].ReasoningLevels[0] = "max"
	if stored := a.modelLibrary.snapshot(); stored.Library.Models[1].DisplayName != "Daily coding" || stored.Library.Models[1].ReasoningLevels[0] != "low" {
		t.Fatalf("store mutated through client-owned memory: %#v", stored)
	}
}

func TestModelLibraryValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*modelLibrary)
	}{
		{"schema", func(s *modelLibrary) { s.SchemaVersion = 2 }},
		{"duplicate model", func(s *modelLibrary) { s.Models = append(s.Models, s.Models[0]) }},
		{"invalid ID", func(s *modelLibrary) { s.Models[0].ID = "bad id" }},
		{"too many", func(s *modelLibrary) {
			for i := 0; i < 49; i++ {
				s.Models = append(s.Models, modelLibraryItem{ID: fmt.Sprintf("vendor/%d", i)})
			}
		}},
		{"missing default", func(s *modelLibrary) { s.DefaultModel = "" }},
		{"unselected default", func(s *modelLibrary) { s.DefaultModel = "vendor/other" }},
		{"empty stale default", func(s *modelLibrary) { s.Models = nil }},
		{"long name", func(s *modelLibrary) { s.Models[0].DisplayName = strings.Repeat("é", 81) }},
		{"control in name", func(s *modelLibrary) { s.Models[0].DisplayName = "one\ntwo" }},
		{"unknown effort", func(s *modelLibrary) { s.Models[1].ReasoningEffort = "extreme" }},
		{"unknown level", func(s *modelLibrary) { s.Models[1].ReasoningLevels = []string{"extreme"} }},
		{"duplicate level", func(s *modelLibrary) { s.Models[1].ReasoningLevels = []string{"high", "high"} }},
		{"disabled initial effort", func(s *modelLibrary) { s.Models[1].ReasoningLevels = nil }},
		{"small context", func(s *modelLibrary) { s.Models[0].ContextWindow = 1023 }},
		{"negative context", func(s *modelLibrary) { s.Models[0].ContextWindow = -1 }},
		{"large context", func(s *modelLibrary) { s.Models[0].ContextWindow = 100000001 }},
		{"negative output", func(s *modelLibrary) { s.Models[0].MaxOutputTokens = -1 }},
		{"large output", func(s *modelLibrary) { s.Models[0].MaxOutputTokens = 100000001 }},
		{"output exceeds context", func(s *modelLibrary) { s.Models[1].MaxOutputTokens = 200001 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			library := testModelLibrary()
			tc.change(&library)
			if err := validateModelLibrary(library); err == nil {
				t.Fatalf("accepted %#v", library)
			}
		})
	}
	for _, library := range []modelLibrary{emptyModelLibrary(), testModelLibrary(), {SchemaVersion: 1, DefaultModel: "new/unavailable", Models: []modelLibraryItem{{ID: "new/unavailable", ReasoningEffort: "high", MaxOutputTokens: 100000000}}}} {
		if err := validateModelLibrary(library); err != nil {
			t.Fatalf("valid selection: %v", err)
		}
	}
}

func TestModelLibrarySaveFailuresKeepLastSavedRevision(t *testing.T) {
	for _, failAt := range []string{"models.json", "models.json.bak"} {
		t.Run(failAt, func(t *testing.T) {
			store := newModelLibraryStore(t.TempDir())
			first, err := store.save(testModelLibrary(), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			original, _ := os.ReadFile(filepath.Join(store.dir, "models.json"))
			pending := cloneModelLibrary(first.Library)
			pending.Models[1].DisplayName = "Unsaved edit"
			store.write = func(path string, data []byte) error {
				if filepath.Base(path) == failAt {
					return errors.New("simulated full disk")
				}
				return atomicCatalogFile(path, data)
			}
			got, err := store.save(pending, first.Revision, false)
			if err == nil || !reflect.DeepEqual(got, first) || !reflect.DeepEqual(store.snapshot(), first) {
				t.Fatalf("failed save changed memory: %#v %v", got, err)
			}
			current, _ := os.ReadFile(filepath.Join(store.dir, "models.json"))
			if !bytes.Equal(current, original) || pending.Models[1].DisplayName != "Unsaved edit" {
				t.Fatal("failed save changed the disk or caller's pending edits")
			}
			if restart := newModelLibraryStore(store.dir).snapshot(); !reflect.DeepEqual(restart.Library, first.Library) {
				t.Fatal("restart lost the last successful save")
			}
			store.write = atomicCatalogFile
			if saved, err := store.save(pending, first.Revision, false); err != nil || saved.Revision != first.Revision+1 || saved.Library.Models[1].DisplayName != "Unsaved edit" {
				t.Fatalf("retry failed: %#v %v", saved, err)
			}
		})
	}
}

func TestModelLibraryAtomicReplacementFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := atomicCatalogFile(path, []byte("new library")); err == nil {
		t.Fatal("unsafe target accepted")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatal("unsafe target was changed")
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".models-*.tmp"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files leaked: %v", leftovers)
	}
}

func TestModelLibraryConcurrentRevisionConflict(t *testing.T) {
	store := newModelLibraryStore(t.TempDir())
	var group sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"first", "second"} {
		group.Go(func() {
			library := testModelLibrary()
			library.Models[0].DisplayName = name
			_, err := store.save(library, 0, false)
			results <- err
		})
	}
	group.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, errModelLibraryConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 || store.snapshot().Revision != 1 {
		t.Fatalf("successful saves %d, conflicts %d, state %#v", success, conflict, store.snapshot())
	}
}

func TestModelLibraryCorruptionRequiresExplicitRecovery(t *testing.T) {
	for _, backupValid := range []bool{true, false} {
		t.Run(fmt.Sprintf("backup-valid-%t", backupValid), func(t *testing.T) {
			dir := t.TempDir()
			store := newModelLibraryStore(dir)
			first, err := store.save(testModelLibrary(), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			second := cloneModelLibrary(first.Library)
			second.Models[0].DisplayName = "second version"
			if _, err := store.save(second, first.Revision, false); err != nil {
				t.Fatal(err)
			}
			corrupt := []byte(`{"schemaVersion":1,"models":[`)
			os.WriteFile(filepath.Join(dir, "models.json"), corrupt, 0600)
			if !backupValid {
				os.WriteFile(filepath.Join(dir, "models.json.bak"), corrupt, 0600)
			}
			restored := newModelLibraryStore(dir)
			state := restored.snapshot()
			if !state.RecoveryRequired || state.Warning == "" {
				t.Fatalf("corruption hidden: %#v", state)
			}
			if backupValid && !reflect.DeepEqual(state.Library, first.Library) {
				t.Fatalf("last good backup not restored: %#v", state.Library)
			}
			if !backupValid && len(state.Library.Models) != 0 {
				t.Fatal("corrupt files fabricated a selection")
			}
			if _, err := restored.save(state.Library, state.Revision, false); !errors.Is(err, errModelLibraryRecovery) {
				t.Fatalf("ordinary save did not require recovery: %v", err)
			}
			stillCorrupt, _ := os.ReadFile(filepath.Join(dir, "models.json"))
			if !bytes.Equal(stillCorrupt, corrupt) {
				t.Fatal("startup or ordinary save overwrote corrupted original")
			}
			recovered, err := restored.save(state.Library, state.Revision, true)
			if err != nil || recovered.RecoveryRequired || recovered.Warning != "" {
				t.Fatalf("explicit recovery failed: %#v %v", recovered, err)
			}
			archives, _ := filepath.Glob(filepath.Join(dir, "models.json.corrupt-*.json"))
			if len(archives) != 1 {
				t.Fatalf("corrupted original not preserved: %v", archives)
			}
			archived, _ := os.ReadFile(archives[0])
			if !bytes.Equal(archived, corrupt) {
				t.Fatal("archive content changed")
			}
			if afterRestart := newModelLibraryStore(dir).snapshot(); afterRestart.RecoveryRequired || !reflect.DeepEqual(afterRestart.Library, recovered.Library) {
				t.Fatalf("recovery not durable: %#v", afterRestart)
			}
		})
	}
}

func TestModelLibraryDoesNotOverwriteExternalChanges(t *testing.T) {
	store := newModelLibraryStore(t.TempDir())
	state, err := store.save(testModelLibrary(), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	external := []byte(`{"schemaVersion":1,"defaultModel":"","models":[]}`)
	path := filepath.Join(store.dir, "models.json")
	os.WriteFile(path, external, 0600)
	if _, err := store.save(state.Library, state.Revision, false); !errors.Is(err, errModelLibraryConflict) {
		t.Fatalf("external edit not detected: %v", err)
	}
	current, _ := os.ReadFile(path)
	if !bytes.Equal(current, external) {
		t.Fatal("external edit overwritten")
	}
	reloaded := store.snapshot()
	if len(reloaded.Library.Models) != 0 || reloaded.Revision != state.Revision+1 {
		t.Fatalf("reload did not expose external choices: %#v", reloaded)
	}
	if _, err := store.save(reloaded.Library, reloaded.Revision, false); err != nil {
		t.Fatalf("saving after an explicit reload failed: %v", err)
	}
}

func TestModelLibraryMissingPrimaryRetainsBackup(t *testing.T) {
	store := newModelLibraryStore(t.TempDir())
	if _, err := store.save(testModelLibrary(), 0, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.dir, "models.json")); err != nil {
		t.Fatal(err)
	}
	restarted := newModelLibraryStore(store.dir)
	state := restarted.snapshot()
	if !state.RecoveryRequired || !reflect.DeepEqual(state.Library, testModelLibrary()) {
		t.Fatalf("backup lost after primary disappeared: %#v", state)
	}
	if _, err := os.Stat(filepath.Join(store.dir, "models.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("startup automatically created the missing primary")
	}
	if _, err := restarted.save(state.Library, state.Revision, true); err != nil {
		t.Fatalf("explicit restore of missing primary failed: %v", err)
	}
}

func TestModelLibraryAdminAPIValidationAndConflict(t *testing.T) {
	a := testApp(t)
	valid := map[string]any{"library": testModelLibrary(), "revision": 0}
	if response := libraryRequest(a, http.MethodPut, valid); response.Code != http.StatusOK {
		t.Fatalf("save failed: %s", response.Body.String())
	}
	conflict := libraryRequest(a, http.MethodPut, valid)
	if conflict.Code != http.StatusConflict || decodeLibraryResponse(t, conflict).Revision != 1 || !strings.Contains(conflict.Body.String(), `"error"`) {
		t.Fatalf("conflict response: %d %s", conflict.Code, conflict.Body.String())
	}
	for _, body := range []any{
		map[string]any{"library": testModelLibrary()},
		map[string]any{"library": testModelLibrary(), "revision": 1, "apiKey": "secret"},
		map[string]any{"library": map[string]any{"schemaVersion": 1, "models": []any{}, "apiKey": "secret"}, "revision": 1},
		map[string]any{"library": testModelLibrary(), "revision": -1},
	} {
		if response := libraryRequest(a, http.MethodPut, body); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid body accepted: %d %s", response.Code, response.Body.String())
		}
	}
	if response := libraryRequest(a, http.MethodPost, valid); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST accepted: %d", response.Code)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		r := httptest.NewRequest(method, "http://"+a.adminHost+"/api/model-library", nil)
		w := httptest.NewRecorder()
		a.adminHandler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s accepted: %d", method, w.Code)
		}
	}
	if response := libraryRequest(a, http.MethodPut, map[string]any{"library": emptyModelLibrary(), "revision": 1}); response.Code != http.StatusOK || len(decodeLibraryResponse(t, response).Library.Models) != 0 {
		t.Fatalf("clearing selection failed: %d %s", response.Code, response.Body.String())
	}
}

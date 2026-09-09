package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const modelLibraryLimit = 128 << 10

// This is a user's ordered selection, independent of generated client profiles
// and of the live catalog. Never add credentials, prices or inferred capabilities.
type modelLibrary struct {
	SchemaVersion int                `json:"schemaVersion"`
	DefaultModel  string             `json:"defaultModel"`
	Models        []modelLibraryItem `json:"models"`
}

type modelLibraryItem struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"displayName,omitempty"`
	ReasoningEffort string   `json:"reasoningEffort,omitempty"`
	ReasoningLevels []string `json:"reasoningLevels,omitempty"`
	ReasoningCustom bool     `json:"reasoningCustom,omitempty"`
	ContextWindow   int      `json:"contextWindow,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
}

type modelLibraryState struct {
	Library          modelLibrary `json:"library"`
	Revision         uint64       `json:"revision"`
	Warning          string       `json:"warning,omitempty"`
	RecoveryRequired bool         `json:"recoveryRequired,omitempty"`
}

var (
	errModelLibraryConflict = errors.New("The model library changed. Reload the saved library before retrying; your edits have not been saved.")
	errModelLibraryRecovery = errors.New("The model library needs recovery. Review the restored choices and explicitly recover before saving.")
)

type modelLibraryStore struct {
	mu        sync.Mutex
	dir       string
	state     modelLibraryState
	disk      []byte
	diskError error
	write     func(string, []byte) error
}

func emptyModelLibrary() modelLibrary {
	return modelLibrary{SchemaVersion: 1, Models: []modelLibraryItem{}}
}

func validateModelLibrary(library modelLibrary) error {
	if library.SchemaVersion != 1 {
		return errors.New("Unsupported model library schema version.")
	}
	if len(library.Models) > 50 {
		return errors.New("Choose at most 50 models.")
	}
	seen := make(map[string]bool, len(library.Models))
	for _, model := range library.Models {
		if !catalogID.MatchString(model.ID) || seen[model.ID] {
			return errors.New("Use unique, valid exact model IDs.")
		}
		seen[model.ID] = true
		if !utf8.ValidString(model.DisplayName) || utf8.RuneCountInString(model.DisplayName) > 80 || strings.IndexFunc(model.DisplayName, unicode.IsControl) >= 0 {
			return errors.New("Model names must have at most 80 characters and no control characters.")
		}
		if (model.ContextWindow != 0 && (model.ContextWindow < 1024 || model.ContextWindow > 100000000)) || model.MaxOutputTokens < 0 || model.MaxOutputTokens > 100000000 || (model.ContextWindow > 0 && model.MaxOutputTokens > model.ContextWindow) {
			return errors.New("Use a context limit of 1,024–100,000,000 tokens and an output limit no greater than the context, or leave limits unset.")
		}
		if model.ReasoningEffort != "" && !effortNames[model.ReasoningEffort] {
			return errors.New("Use a supported reasoning effort.")
		}
		levels := make(map[string]bool, len(model.ReasoningLevels))
		for _, effort := range model.ReasoningLevels {
			if !effortNames[effort] || levels[effort] {
				return errors.New("Use unique, supported reasoning levels.")
			}
			levels[effort] = true
		}
		if model.ReasoningCustom && model.ReasoningEffort != "" && !levels[model.ReasoningEffort] {
			return errors.New("Choose the default reasoning effort from the enabled custom levels.")
		}
	}
	if (len(library.Models) == 0 && library.DefaultModel != "") || (len(library.Models) > 0 && !seen[library.DefaultModel]) {
		return errors.New("Choose an initial model from your library; an empty library must have no initial model.")
	}
	return nil
}

func cloneModelLibrary(library modelLibrary) modelLibrary {
	copy := library
	copy.Models = make([]modelLibraryItem, len(library.Models))
	for i, item := range library.Models {
		copy.Models[i] = item
		copy.Models[i].ReasoningLevels = append([]string(nil), item.ReasoningLevels...)
	}
	return copy
}

func decodeModelLibrary(data []byte) (modelLibrary, error) {
	var library modelLibrary
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&library); err != nil {
		return library, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return library, errors.New("Invalid model library JSON.")
	}
	if err := validateModelLibrary(library); err != nil {
		return library, err
	}
	return cloneModelLibrary(library), nil
}

func readModelLibraryFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > modelLibraryLimit {
		return nil, errors.New("The model library must be a regular file smaller than 128 KiB.")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, modelLibraryLimit+1))
	if len(data) > modelLibraryLimit {
		return nil, errors.New("The model library exceeds 128 KiB.")
	}
	return data, err
}

// A missing or damaged primary file never silently replaces a user's choices.
// The valid backup is shown immediately, but writing requires explicit recovery.
func newModelLibraryStore(dir string) *modelLibraryStore {
	store := &modelLibraryStore{dir: dir, state: modelLibraryState{Library: emptyModelLibrary()}, write: atomicCatalogFile}
	data, err := readModelLibraryFile(filepath.Join(dir, "models.json"))
	store.disk, store.diskError = data, err
	if err == nil {
		if library, parseErr := decodeModelLibrary(data); parseErr == nil {
			store.state.Library, store.state.Revision = library, 1
			return store
		}
	}
	backup, backupErr := readModelLibraryFile(filepath.Join(dir, "models.json.bak"))
	if errors.Is(err, os.ErrNotExist) && errors.Is(backupErr, os.ErrNotExist) {
		return store
	}
	store.state.RecoveryRequired = true
	store.state.Warning = "The saved model library cannot be read. Your files are unchanged. Review your choices, then recover the library to enable saving."
	if backupErr == nil {
		if library, parseErr := decodeModelLibrary(backup); parseErr == nil {
			store.state.Library, store.state.Revision = library, 1
			store.state.Warning = "The saved model library cannot be read. The last good backup is shown. Review these choices, then recover the library to enable saving."
		}
	}
	return store
}

func (store *modelLibraryStore) snapshotLocked() modelLibraryState {
	state := store.state
	state.Library = cloneModelLibrary(state.Library)
	return state
}

func (store *modelLibraryStore) snapshot() modelLibraryState {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.snapshotLocked()
}

// Revisions are scoped to this app process, as is the admin access token. All
// readers receive independent copies; only a successful atomic write advances it.
func (store *modelLibraryStore) save(library modelLibrary, expected uint64, recover bool) (modelLibraryState, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if expected != store.state.Revision {
		return store.snapshotLocked(), errModelLibraryConflict
	}
	if err := validateModelLibrary(library); err != nil {
		return store.snapshotLocked(), err
	}
	if store.state.RecoveryRequired && !recover {
		return store.snapshotLocked(), errModelLibraryRecovery
	}
	path := filepath.Join(store.dir, "models.json")
	current, readErr := readModelLibraryFile(path)
	missing := errors.Is(readErr, os.ErrNotExist)
	wasMissing := errors.Is(store.diskError, os.ErrNotExist)
	if readErr != nil && !missing {
		return store.snapshotLocked(), errors.New("Cannot read models.json safely. Check the configuration folder permissions before retrying.")
	}
	if (missing != wasMissing) || (!missing && (store.diskError != nil || !bytes.Equal(current, store.disk))) {
		// Make a subsequent reload useful, while still rejecting the pending
		// write. Never silently merge an external file into the caller's edits.
		reloaded := newModelLibraryStore(store.dir)
		reloaded.state.Revision = store.state.Revision + 1
		store.state, store.disk, store.diskError = reloaded.state, reloaded.disk, reloaded.diskError
		return store.snapshotLocked(), errModelLibraryConflict
	}
	library = cloneModelLibrary(library)
	data, err := json.MarshalIndent(library, "", "  ")
	if err != nil {
		return store.snapshotLocked(), err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(store.dir, 0700); err != nil {
		return store.snapshotLocked(), errors.New("Cannot create the configuration folder. Your model edits have not been saved.")
	}
	if store.state.RecoveryRequired {
		// Archive invalid originals under unique names only on explicit recovery.
		// Never replace the sole copy of a damaged file with an empty selection.
		for _, source := range []string{path, path + ".bak"} {
			original, err := readModelLibraryFile(source)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return store.snapshotLocked(), errors.New("Cannot preserve the damaged model library. Check the configuration folder before recovering.")
			}
			if _, err := decodeModelLibrary(original); err != nil {
				if err := archiveModelLibrary(source, original); err != nil {
					return store.snapshotLocked(), errors.New("Cannot preserve the damaged model library. Your files have not been replaced.")
				}
			}
		}
	}
	backup := data
	if store.state.Revision > 0 {
		backup, _ = json.MarshalIndent(store.state.Library, "", "  ")
		backup = append(backup, '\n')
	}
	if err := store.write(path+".bak", backup); err != nil {
		return store.snapshotLocked(), errors.New("Cannot save the model library backup. Check the configuration folder permissions or free space, then retry.")
	}
	if err := store.write(path, data); err != nil {
		return store.snapshotLocked(), errors.New("Cannot save the model library. Check the configuration folder permissions or free space, then retry. Your edits remain unsaved.")
	}
	store.disk, store.diskError = data, nil
	store.state = modelLibraryState{Library: library, Revision: store.state.Revision + 1}
	return store.snapshotLocked(), nil
}

func archiveModelLibrary(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".corrupt-*.json")
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		file.Close()
		if !complete {
			os.Remove(file.Name())
		}
	}()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}

func (a *app) modelLibraryAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		jsonResponse(w, http.StatusOK, a.modelLibrary.snapshot())
		return
	}
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		jsonError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		jsonError(w, http.StatusUnsupportedMediaType, "JSON required.")
		return
	}
	var input struct {
		Library  modelLibrary `json:"library"`
		Revision *uint64      `json:"revision"`
		Recover  bool         `json:"recover,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, modelLibraryLimit))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || input.Revision == nil {
		jsonError(w, http.StatusBadRequest, "Invalid library JSON; include the library and its current revision.")
		return
	}
	if err := validateModelLibrary(input.Library); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	state, err := a.modelLibrary.save(input.Library, *input.Revision, input.Recover)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errModelLibraryConflict) || errors.Is(err, errModelLibraryRecovery) {
			status = http.StatusConflict
		}
		jsonResponse(w, status, struct {
			modelLibraryState
			Error map[string]string `json:"error"`
		}{state, map[string]string{"message": err.Error(), "type": "kilo_local_error"}})
		return
	}
	jsonResponse(w, http.StatusOK, state)
}

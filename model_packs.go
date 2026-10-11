package main

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:embed model-packs.json
var embeddedModelPacks []byte

const modelPacksLimit = 256 << 10

var (
	modelPackIDPattern    = regexp.MustCompile(`^[a-z][a-z0-9-]{1,47}$`)
	errModelPacksConflict = errors.New("Model packs changed outside this window. Reload the saved packs before saving again.")
	errModelPacksRecovery = errors.New("Saved model packs need recovery. Review the restored backup before writing.")
)

type packText struct {
	EN string `json:"en"`
	ES string `json:"es"`
}
type packModelSpec struct {
	ID   string   `json:"id"`
	Role packText `json:"role"`
}
type curatedModelPack struct {
	ID           string          `json:"id"`
	Name         packText        `json:"name"`
	Description  packText        `json:"description"`
	DefaultModel string          `json:"defaultModel"`
	Models       []packModelSpec `json:"models"`
	Optional     []packModelSpec `json:"optional,omitempty"`
}
type curatedPackCatalog struct {
	SchemaVersion int                `json:"schemaVersion"`
	Packs         []curatedModelPack `json:"packs"`
}

type personalModelPack struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	BasedOn string            `json:"basedOn,omitempty"`
	Library modelLibrary      `json:"library"`
	Roles   map[string]string `json:"roles,omitempty"`
}
type modelPacksFile struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Personal      []personalModelPack `json:"personal,omitempty"`
	Assignments   map[string]string   `json:"assignments,omitempty"`
}

var packAgentNames = map[string]string{
	"codex": "Codex Desktop", "codex-cli": "Codex CLI", "claude": "Claude Code",
	"claude-desktop": "Claude Desktop", "opencode": "OpenCode", "omp": "Oh My Pi", "zed": "Zed",
}

func validPackText(text string, limit int) bool {
	return text != "" && utf8.ValidString(text) && utf8.RuneCountInString(text) <= limit && strings.IndexFunc(text, unicode.IsControl) < 0
}

func decodeCuratedPacks(data []byte) (curatedPackCatalog, error) {
	var catalog curatedPackCatalog
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return catalog, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return catalog, errors.New("Trailing content in curated packs")
	}
	if catalog.SchemaVersion != 1 || len(catalog.Packs) == 0 || len(catalog.Packs) > 30 {
		return catalog, errors.New("Invalid curated pack catalog")
	}
	seen := map[string]bool{}
	for _, p := range catalog.Packs {
		if !modelPackIDPattern.MatchString(p.ID) || seen[p.ID] || !validPackText(p.Name.EN, 80) || !validPackText(p.Name.ES, 80) || !validPackText(p.Description.EN, 240) || !validPackText(p.Description.ES, 240) || len(p.Models) == 0 || len(p.Models) > 50 || len(p.Optional) > 20 {
			return catalog, fmt.Errorf("Invalid curated pack %q", p.ID)
		}
		seen[p.ID] = true
		ids := map[string]bool{}
		defaultInCore := false
		for _, m := range append(append([]packModelSpec(nil), p.Models...), p.Optional...) {
			if !catalogID.MatchString(m.ID) || ids[m.ID] || !validPackText(m.Role.EN, 80) || !validPackText(m.Role.ES, 80) {
				return catalog, fmt.Errorf("Invalid model in pack %q", p.ID)
			}
			ids[m.ID] = true
		}
		for _, m := range p.Models {
			defaultInCore = defaultInCore || m.ID == p.DefaultModel
		}
		if !defaultInCore {
			return catalog, fmt.Errorf("Missing default model in pack %q", p.ID)
		}
	}
	return catalog, nil
}

func builtinModelPacks() []curatedModelPack {
	catalog, err := decodeCuratedPacks(embeddedModelPacks)
	if err != nil {
		return nil
	}
	return catalog.Packs
}

func emptyModelPacksFile() modelPacksFile {
	return modelPacksFile{SchemaVersion: 1, Personal: []personalModelPack{}, Assignments: map[string]string{}}
}

func cloneModelPacksFile(file modelPacksFile) modelPacksFile {
	copy := file
	copy.Personal = make([]personalModelPack, len(file.Personal))
	for i, p := range file.Personal {
		copy.Personal[i] = p
		copy.Personal[i].Library = cloneModelLibrary(p.Library)
		copy.Personal[i].Roles = make(map[string]string, len(p.Roles))
		for id, role := range p.Roles {
			copy.Personal[i].Roles[id] = role
		}
	}
	copy.Assignments = make(map[string]string, len(file.Assignments))
	for k, v := range file.Assignments {
		copy.Assignments[k] = v
	}
	return copy
}

func (file modelPacksFile) personalPack(id string) *personalModelPack {
	for i := range file.Personal {
		if file.Personal[i].ID == id {
			return &file.Personal[i]
		}
	}
	return nil
}

func (file modelPacksFile) hasPack(id string) bool {
	if file.personalPack(id) != nil {
		return true
	}
	for _, p := range builtinModelPacks() {
		if p.ID == id {
			return true
		}
	}
	return false
}

// Resolve an independent pack into a client-ready library without modifying
// the user's shared models.json. Optional curated models are never implicit.
func modelPackLibrary(file modelPacksFile, id string, catalog []modelInfo) (modelLibrary, error) {
	if personal := file.personalPack(id); personal != nil {
		return cloneModelLibrary(personal.Library), nil
	}
	for _, pack := range builtinModelPacks() {
		if pack.ID != id {
			continue
		}
		known := make(map[string]modelInfo, len(catalog))
		for _, model := range catalog {
			known[model.ID] = model
		}
		library := modelLibrary{SchemaVersion: 1, DefaultModel: pack.DefaultModel, Models: make([]modelLibraryItem, 0, len(pack.Models))}
		for _, model := range pack.Models {
			item := modelLibraryItem{ID: model.ID, ContextPreset: contextPresetRecommended}
			item.MaxOutputTokens = known[model.ID].MaxOutputTokens
			library.Models = append(library.Models, item)
		}
		return library, nil
	}
	return modelLibrary{}, errors.New("The assigned model pack is no longer available. Choose another pack in Models.")
}

func validateModelPacksFile(file modelPacksFile) error {
	if file.SchemaVersion != 1 || len(file.Personal) > 30 {
		return errors.New("Invalid model packs version or count")
	}
	ids := map[string]bool{}
	for _, p := range file.Personal {
		if !modelPackIDPattern.MatchString(p.ID) || !strings.HasPrefix(p.ID, "mine-") || ids[p.ID] || !validPackText(p.Name, 80) {
			return errors.New("Use unique personal pack names and IDs")
		}
		ids[p.ID] = true
		if p.BasedOn != "" && !modelPackIDPattern.MatchString(p.BasedOn) {
			return errors.New("Invalid pack template ID")
		}
		if err := validateModelLibrary(p.Library); err != nil {
			return fmt.Errorf("Invalid personal pack %q: %w", p.ID, err)
		}
		selected := map[string]bool{}
		for _, model := range p.Library.Models {
			selected[model.ID] = true
		}
		for id, role := range p.Roles {
			if !selected[id] || !validPackText(role, 80) {
				return errors.New("Invalid personal pack model role")
			}
		}
	}
	for key, id := range file.Assignments {
		if _, ok := packAgentNames[key]; !ok || !modelPackIDPattern.MatchString(id) {
			return errors.New("Invalid model pack assignment")
		}
		if p := file.personalPack(id); p != nil && len(p.Library.Models) == 0 {
			return errors.New("Cannot assign an empty personal pack")
		}
	}
	return nil
}

func decodeModelPacksFile(data []byte) (modelPacksFile, error) {
	var file modelPacksFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return file, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return file, errors.New("Trailing content in model packs")
	}
	if err := validateModelPacksFile(file); err != nil {
		return file, err
	}
	return cloneModelPacksFile(file), nil
}

func readModelPacksFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > modelPacksLimit {
		return nil, errors.New("Model packs must be a regular file smaller than 256 KiB")
	}
	data, err := os.ReadFile(path)
	if len(data) > modelPacksLimit {
		return nil, errors.New("Model packs exceed 256 KiB")
	}
	return data, err
}

type modelPacksStore struct {
	dir         string
	value       modelPacksFile
	disk        []byte
	diskErr     error
	recovery    bool
	recoverable bool
	warning     string
}

func newModelPacksStore(dir string) *modelPacksStore {
	s := &modelPacksStore{dir: dir, value: emptyModelPacksFile()}
	path := filepath.Join(dir, "model-packs.json")
	data, err := readModelPacksFile(path)
	s.disk, s.diskErr = data, err
	if err == nil {
		if v, parse := decodeModelPacksFile(data); parse == nil {
			s.value = v
			return s
		}
	}
	backup, backErr := readModelPacksFile(path + ".bak")
	if errors.Is(err, os.ErrNotExist) && errors.Is(backErr, os.ErrNotExist) {
		return s
	}
	s.recovery = true
	s.warning = "Saved model packs need review. Existing files have not been changed."
	if backErr == nil {
		if v, parse := decodeModelPacksFile(backup); parse == nil {
			s.value = v
			s.recoverable = true
			s.warning = "The last good model packs backup is shown. Review and recover before editing."
		}
	}
	return s
}

func (s *modelPacksStore) save(file modelPacksFile, recover bool) error {
	if err := validateModelPacksFile(file); err != nil {
		return err
	}
	if s.recovery && !recover {
		return errModelPacksRecovery
	}
	if s.recovery && !s.recoverable {
		return errors.New("No valid model packs backup is available. The damaged files have not been replaced.")
	}
	path := filepath.Join(s.dir, "model-packs.json")
	current, err := readModelPacksFile(path)
	missing := errors.Is(err, os.ErrNotExist)
	wasMissing := errors.Is(s.diskErr, os.ErrNotExist)
	if missing != wasMissing || !missing && (err != nil || !bytes.Equal(current, s.disk)) {
		return errModelPacksConflict
	}
	if err != nil && !missing && !recover {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > modelPacksLimit {
		return errors.New("Model packs exceed 256 KiB")
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	if recover {
		for _, source := range []string{path, path + ".bak"} {
			original, readErr := readModelPacksFile(source)
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			if readErr != nil {
				return errors.New("Cannot preserve damaged model packs")
			}
			if _, parse := decodeModelPacksFile(original); parse != nil {
				if err := archiveModelLibrary(source, original); err != nil {
					return err
				}
			}
		}
	}
	backup := s.disk
	if s.recovery || s.diskErr != nil {
		backup = data
	}
	if err := atomicCatalogFile(path+".bak", backup); err != nil {
		return fmt.Errorf("Cannot save model packs backup: %w", err)
	}
	if err := atomicCatalogFile(path, data); err != nil {
		return fmt.Errorf("Cannot save model packs: %w", err)
	}
	s.value = cloneModelPacksFile(file)
	s.disk, s.diskErr = data, nil
	s.recovery = false
	s.recoverable = false
	s.warning = ""
	return nil
}

func newPersonalPackID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "mine-" + hex.EncodeToString(b[:]), nil
}

package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const catalogLimit = 2 << 20

var catalogID = regexp.MustCompile(`^[A-Za-z0-9~][A-Za-z0-9._:/~+-]{0,199}$`)
var effortNames = map[string]bool{"none": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true, "ultra": true}

// The destination is fixed to the isolated profile, never supplied by a browser.
func (a *app) codexCatalog(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	dir := a.codexProfileDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			jsonError(w, 500, "Cannot locate the home directory.")
			return
		}
		dir = filepath.Join(home, ".codex-kilo-desktop")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		jsonError(w, 409, "Create ~/.codex-kilo-desktop and save config.toml there first.")
		return
	}
	path := filepath.Join(dir, "models.json")
	if r.Method == "GET" {
		data, err := readCatalogFile(path)
		if err != nil {
			jsonError(w, 404, "Cannot read models.json in the isolated Codex Kilo profile.")
			return
		}
		first, err := validateCatalog(data)
		if err != nil {
			jsonError(w, 409, "The saved catalog is invalid or exceeds 50 models.")
			return
		}
		jsonResponse(w, 200, map[string]any{"catalog": json.RawMessage(data), "defaultModel": first})
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		jsonError(w, 415, "JSON required.")
		return
	}
	var input struct {
		Catalog json.RawMessage `json:"catalog"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, catalogLimit))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		jsonError(w, 400, "Invalid catalog JSON.")
		return
	}
	if _, err := validateCatalog(input.Catalog); err != nil {
		jsonError(w, 400, "Invalid catalog: use 1–50 unique models and valid reasoning levels.")
		return
	}
	// Require an existing regular config, but leave all its settings untouched.
	if info, err := os.Lstat(filepath.Join(dir, "config.toml")); err != nil || !info.Mode().IsRegular() {
		jsonError(w, 409, "Save config.toml in the isolated Codex Kilo profile first.")
		return
	}
	if old, err := readCatalogFile(path); err == nil {
		if err = atomicCatalogFile(path+".bak", old); err != nil {
			jsonError(w, 500, "Cannot back up models.json; no changes saved.")
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		jsonError(w, 409, "Cannot safely replace models.json.")
		return
	}
	if err := atomicCatalogFile(path, append(input.Catalog, '\n')); err != nil {
		jsonError(w, 500, "Cannot save models.json. Check profile permissions.")
		return
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

func readCatalogFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > catalogLimit {
		return nil, errors.New("unsafe catalog file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, catalogLimit+1))
	if len(data) > catalogLimit {
		return nil, errors.New("catalog too large")
	}
	return data, err
}
func atomicCatalogFile(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("unsafe destination")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".models-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
func validateCatalog(data []byte) (string, error) {
	var catalog struct {
		Models []struct {
			Slug    string  `json:"slug"`
			Default *string `json:"default_reasoning_level"`
			Levels  []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	invalid := errors.New("invalid catalog")
	if len(data) > catalogLimit || json.Unmarshal(data, &catalog) != nil || len(catalog.Models) < 1 || len(catalog.Models) > 50 {
		return "", invalid
	}
	seen := map[string]bool{}
	for _, model := range catalog.Models {
		if !catalogID.MatchString(model.Slug) || seen[model.Slug] {
			return "", invalid
		}
		seen[model.Slug] = true
		levels := map[string]bool{}
		for _, level := range model.Levels {
			if !effortNames[level.Effort] || levels[level.Effort] {
				return "", invalid
			}
			levels[level.Effort] = true
		}
		if model.Default != nil && !levels[*model.Default] {
			return "", invalid
		}
		if len(levels) > 0 && model.Default == nil {
			return "", invalid
		}
	}
	return catalog.Models[0].Slug, nil
}

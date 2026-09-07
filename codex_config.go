package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Parse TOML instead of matching lines: quoted keys, inline tables, and arrays
// must retain their values even when unrelated to the generated provider.
func mergeCodexConfig(data, catalog []byte, port int) ([]byte, error) {
	config := map[string]any{}
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, errors.New("config.toml is not valid TOML; no profile changes saved")
	}
	var models struct {
		Models []struct {
			Slug   string  `json:"slug"`
			Effort *string `json:"default_reasoning_level"`
		} `json:"models"`
	}
	if _, err := validateCatalog(catalog); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(catalog, &models); err != nil {
		return nil, err
	}
	selected := models.Models[0]
	updateModel := func(table map[string]any) {
		table["model"] = selected.Slug
		table["model_provider"] = "kilo-local"
		table["model_catalog_json"] = "models.json"
		if selected.Effort != nil {
			table["model_reasoning_effort"] = *selected.Effort
		} else {
			delete(table, "model_reasoning_effort")
		}
	}
	updateModel(config)
	config["cli_auth_credentials_store"] = "file"
	// A selected legacy named profile overrides top-level defaults. Keep its
	// unrelated settings while synchronizing the same managed model choices.
	if profile, ok := config["profile"].(string); ok && profile != "" {
		profiles, err := configTable(config, "profiles")
		if err != nil {
			return nil, err
		}
		active, err := configTable(profiles, profile)
		if err != nil {
			return nil, err
		}
		updateModel(active)
	}
	providers, err := configTable(config, "model_providers")
	if err != nil {
		return nil, err
	}
	provider, err := configTable(providers, "kilo-local")
	if err != nil {
		return nil, err
	}
	for key, value := range map[string]any{
		"name": "Kilo Local", "base_url": "http://127.0.0.1:" + strconv.Itoa(port) + "/v1",
		"env_key": "KILO_LOCAL_API_KEY", "env_key_instructions": "Launch Codex Kilo with the command from the Kilo Local Codex helper.",
		"wire_api": "responses", "requires_openai_auth": false, "supports_websockets": false,
	} {
		provider[key] = value
	}
	// These alternate authentication modes override or conflict with env_key.
	delete(provider, "experimental_bearer_token")
	delete(provider, "auth")
	// Arbitrary provider query parameters are rejected by the local Responses endpoint.
	delete(provider, "query_params")
	for _, key := range []string{"http_headers", "env_http_headers"} {
		if raw, exists := provider[key]; exists {
			headers, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("model_providers.kilo-local.%s must be a table; no profile changes saved", key)
			}
			for name := range headers {
				if strings.EqualFold(name, "authorization") {
					delete(headers, name)
				}
			}
		}
	}
	var before map[string]any
	if err := toml.Unmarshal(data, &before); err != nil {
		return nil, err
	}
	return editCodexTOML(data, before, config)
}
func configTable(parent map[string]any, key string) (map[string]any, error) {
	if raw, exists := parent[key]; exists {
		table, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("Codex provider/profile settings must be TOML tables; no profile changes saved")
		}
		return table, nil
	}
	table := map[string]any{}
	parent[key] = table
	return table, nil
}

type profileFile struct {
	path            string
	old, new        []byte
	exists, changed bool
}

func prepareProfileFile(path string, data []byte) (profileFile, error) {
	f := profileFile{path: path, new: data}
	old, err := readCatalogFile(path)
	if err == nil {
		f.old = old
		f.exists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return f, errors.New("Cannot safely read " + filepath.Base(path) + "; no profile changes saved")
	}
	f.changed = !f.exists || !bytes.Equal(old, data)
	if f.changed && f.exists {
		if info, err := os.Lstat(path + ".bak"); err == nil {
			if !info.Mode().IsRegular() {
				return f, errors.New("Unsafe " + filepath.Base(path) + ".bak; no profile changes saved")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return f, errors.New("Cannot inspect the profile backup destination")
		}
	}
	return f, nil
}
func saveCodexProfile(dir string, catalog []byte, port int) (bool, bool, error) {
	return saveCodexProfileWithToken(dir, catalog, port, "")
}
func saveCodexProfileWithToken(dir string, catalog []byte, port int, token string) (bool, bool, error) {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return false, false, errors.New("Cannot create the isolated Codex Kilo profile directory")
		}
		info, err = os.Lstat(dir)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, false, errors.New("The isolated Codex Kilo profile must be a real directory, not a symbolic link")
	}
	configPath := filepath.Join(dir, "config.toml")
	oldConfig, err := readCatalogFile(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, false, errors.New("Cannot safely read config.toml; no profile changes saved")
	}
	config, err := mergeCodexConfig(oldConfig, catalog, port)
	if err != nil {
		return false, false, err
	}
	if token != "" {
		config, err = codexXcodeAuth(config, token)
		if err != nil {
			return false, false, err
		}
	}
	// Validate every destination before writing either file or either backup.
	models, err := prepareProfileFile(filepath.Join(dir, "models.json"), append(bytes.TrimSpace(catalog), '\n'))
	if err != nil {
		return false, false, err
	}
	settings, err := prepareProfileFile(configPath, config)
	if err != nil {
		return false, false, err
	}
	for _, f := range []profileFile{models, settings} {
		if f.changed && f.exists {
			if err := atomicCatalogFile(f.path+".bak", f.old); err != nil {
				return false, false, errors.New("Cannot back up " + filepath.Base(f.path) + "; no profile changes saved")
			}
		}
	}
	if models.changed {
		if err := atomicCatalogFile(models.path, models.new); err != nil {
			return false, false, errors.New("Cannot save models.json; config.toml was not changed")
		}
	}
	if settings.changed {
		if err := atomicCatalogFile(settings.path, settings.new); err != nil {
			if models.changed {
				var rollback error
				if models.exists {
					rollback = atomicCatalogFile(models.path, models.old)
				} else {
					rollback = os.Remove(models.path)
				}
				if rollback != nil {
					return false, false, errors.New("Cannot save config.toml or restore models.json; recover the previous catalog from models.json.bak if present")
				}
			}
			return false, false, errors.New("Cannot save config.toml; the previous catalog was restored")
		}
	}
	return settings.changed, models.changed, nil
}

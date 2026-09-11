package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The caller chooses a directory below Kilo's private Open Design root and
// serializes preparation with launch. Never import a personal CLI profile:
// existing custom settings and alias choices are read only from this directory.
func prepareOpenDesignEngineProfile(dir, engine string, library modelLibrary, catalog []modelInfo, caps claudeCapabilities, port int, key string, images imageGenerationSettings) error {
	if err := validateOpenDesignProfileInputs(engine, dir, port, key); err != nil {
		return err
	}
	if err := validateModelLibrary(library); err != nil {
		return err
	}
	if len(library.Models) == 0 {
		return errors.New("Choose shared models before preparing Open Design.")
	}
	if engine == "codex-cli" {
		models, err := buildCodexCatalog(terminalLibraryChoices(library, catalog), library.DefaultModel, false)
		if err != nil {
			return err
		}
		// The packaged daemon forwards inherited *_API_KEY variables. Keep the
		// normal env_key contract for both Responses and the managed images MCP.
		_, _, err = saveCodexProfileOptions(dir, models, port, "", &images, nil)
		return err
	}
	if engine == "opencode" {
		selection := editorSelection{Initial: library.DefaultModel}
		metadata := map[string]modelInfo{}
		for _, model := range catalog {
			metadata[model.ID] = model
		}
		for _, model := range library.Models {
			name := model.DisplayName
			if name == "" {
				name = model.ID
			}
			context := model.ContextWindow
			if context == 0 {
				context = metadata[model.ID].ContextWindow
			}
			if context < 1024 || context > 100000000 {
				context = 200000
			}
			selection.Models = append(selection.Models, editorModel{ID: model.ID, Name: name, Context: context, Output: model.MaxOutputTokens})
		}
		if err := validateEditorSelection(selection); err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return errors.New("Cannot create the private OpenCode profile.")
		}
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("The private OpenCode profile must be a real directory.")
		}
		path := filepath.Join(dir, "opencode.json")
		old, err := readCatalogFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("Cannot safely read private OpenCode settings.")
		}
		config, err := mergeEditorSettings(old, "opencode", selection, "http://127.0.0.1:"+strconv.Itoa(port)+"/v1", key)
		if err != nil {
			return err
		}
		settings, err := prepareProfileFile(path, config)
		if err != nil {
			return err
		}
		data, _ := json.MarshalIndent(selection, "", "  ")
		choices, err := prepareProfileFile(filepath.Join(dir, "kilo-models.json"), append(data, '\n'))
		if err != nil {
			return err
		}
		_, err = saveEditorFiles([]profileFile{settings, choices})
		return err
	}

	selection := claudeSelection{Initial: library.DefaultModel, Mode: "installed", Aliases: map[string]string{}}
	ids := make(map[string]bool, len(library.Models))
	for _, model := range library.Models {
		effort := model.ReasoningEffort
		if !validClaudeEffort(model.ID, effort) || !caps.PerModelEffort && (model.ID != library.DefaultModel || effort == "xhigh") {
			effort = ""
		}
		selection.Models = append(selection.Models, claudeModel{ID: model.ID, DisplayName: model.DisplayName, Effort: effort})
		ids[model.ID] = true
	}
	if old, err := readCatalogFile(filepath.Join(dir, "kilo-models.json")); err == nil {
		var previous claudeSelection
		if json.Unmarshal(old, &previous) == nil && validateClaudeSelection(previous) == nil {
			for alias, id := range previous.Aliases {
				if ids[id] {
					selection.Aliases[alias] = id
				}
			}
		}
	}
	_, err := saveClaudeProfile(dir, selection, caps, port, key)
	return err
}

// Open Design v0.22.2 accepts these names in agentCliEnv. The caller maps
// Kilo's codex-cli identity to Open Design's codex agent and passes the local
// key to the packaged process as KILO_LOCAL_API_KEY for that engine.
func openDesignEnginePreferences(engine, dir, binary string, port int, key string) (map[string]string, error) {
	if err := validateOpenDesignProfileInputs(engine, dir, port, key); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(binary) || strings.ContainsAny(binary, "\x00\r\n") {
		return nil, errors.New("Open Design requires a detected local CLI executable.")
	}
	if engine == "codex-cli" {
		return map[string]string{"CODEX_HOME": dir, "CODEX_BIN": binary}, nil
	}
	if engine == "opencode" {
		return map[string]string{"OPENCODE_BIN": openDesignOpenCodeShimPath(dir)}, nil
	}
	return map[string]string{
		"CLAUDE_CONFIG_DIR":    dir,
		"CLAUDE_BIN":           binary,
		"ANTHROPIC_BASE_URL":   "http://127.0.0.1:" + strconv.Itoa(port),
		"ANTHROPIC_AUTH_TOKEN": key,
	}, nil
}

func validateOpenDesignProfileInputs(engine, dir string, port int, key string) error {
	if engine != "codex-cli" && engine != "claude" && engine != "opencode" {
		return errors.New("Choose Codex CLI, Claude Code or OpenCode for Open Design.")
	}
	if !filepath.IsAbs(dir) || strings.ContainsAny(dir, "\x00\r\n") {
		return errors.New("Open Design requires an absolute private profile directory.")
	}
	if port < 1024 || port > 65535 || key == "" || strings.ContainsAny(key, "\x00\r\n") {
		return errors.New("Open Design requires a valid local proxy port and key.")
	}
	return nil
}

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// Caller holds a.mu, as does the editor profile API. Reuse the scoped OpenCode
// profile and its managed provider; preserve other settings and MCP servers.
func (a *app) prepareTerminalOpenCodeProfile(library modelLibrary) error {
	root, path, savedPath, err := a.editorPaths("opencode")
	if err != nil {
		return errors.New("Cannot locate the Kilo OpenCode profile.")
	}
	selection := editorSelection{Initial: library.DefaultModel}
	for _, model := range terminalLibraryChoices(library, readNativeCatalogCache(a.dir, a.config.OrgID)) {
		name := model.DisplayName
		if name == "" {
			name = model.Model.Name
		}
		if name == "" {
			name = model.Model.ID
		}
		limits, err := contextPolicyForChoice(model)
		if err != nil {
			return err
		}
		selection.Models = append(selection.Models, editorModel{ID: model.Model.ID, Name: name, Context: limits.ContextWindow, Output: limits.MaxOutputTokens})
	}
	if err := validateEditorSelection(selection); err != nil {
		return err
	}
	if err := safeEditorDir(root, filepath.Dir(path)); err != nil {
		return err
	}
	old, err := readCatalogFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("Cannot safely read OpenCode settings; nothing saved.")
	}
	config, err := mergeEditorSettings(old, "opencode", selection, "http://127.0.0.1:"+strconv.Itoa(a.config.Port)+"/v1", a.config.LocalKey)
	if err != nil {
		return err
	}
	config, err = mergeOpenDesignOpenCodeImages(config, a.config.ImageGeneration, a.config.Port, a.config.LocalKey)
	if err != nil {
		return err
	}
	settings, err := prepareProfileFile(path, config)
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(selection, "", "  ")
	saved, err := prepareProfileFile(savedPath, append(data, '\n'))
	if err != nil {
		return err
	}
	_, err = saveEditorFiles([]profileFile{saved, settings})
	return err
}

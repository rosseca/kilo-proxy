//go:build desktop

package main

import (
	"errors"
	"fmt"
	"strconv"
)

const (
	modelSmokeFirst   = "test/shared-first"
	modelSmokeDefault = "test/shared-default"
	modelSmokeName    = "Shared smoke default"
	modelSmokeRenamed = "Shared smoke renamed"
)

// These actions are reached only by the explicit native diagnostic controller.
// They operate on its fresh profile and never prepare or launch an agent.
func (u *nativeUI) modelSmokeAction(action, value string) (bool, error) {
	switch action {
	case "seed-shared-models":
		u.initModelLibrary()
		s := &nativeClientSelection{Initial: modelSmokeDefault, Aliases: map[string]string{}, Mode: "installed"}
		for i, id := range []string{modelSmokeFirst, modelSmokeDefault} {
			name := "Shared smoke first"
			if i == 1 {
				name = modelSmokeName
			}
			choice := nativeModelChoice{Model: modelInfo{ID: id, Name: id, ContextWindow: 64000, MaxOutputTokens: 4096}, DisplayName: name, ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, DefaultReasoning: "high"}
			s.Models = append(s.Models, choice)
			u.seedClientChoice(sharedModelKey, choice)
		}
		u.library.selection = s
		u.page = "models"
		u.persistLibraryEdits()
		return true, nil
	case "navigate-primary":
		if value != "agents" && value != "models" && value != "activity" && value != "settings" {
			return true, errors.New("unknown primary page")
		}
		u.page = value
		return true, nil
	case "edit-shared-name":
		u.initModelLibrary()
		if u.library.selection.choice(modelSmokeDefault) == nil {
			return true, errors.New("synthetic shared model is missing")
		}
		u.setValue(nativeClientField(sharedModelKey, modelSmokeDefault, "name"), value)
		u.persistLibraryEdits()
		return true, nil
	case "verify-shared-agents":
		for _, key := range []string{"codex", "codex-cli", "claude", "opencode", "zed"} {
			s := u.sharedClientSelection(key)
			u.syncClientSelection(key, s)
			choice := s.choice(modelSmokeDefault)
			if len(s.Models) != 2 || s.Models[0].Model.ID != modelSmokeFirst || s.Initial != modelSmokeDefault || choice == nil || choice.DisplayName != value {
				return true, fmt.Errorf("%s did not inherit shared models, order, name and default", key)
			}
			if key == "codex" || key == "codex-cli" {
				if _, initial := nativeReasoningFor(*choice); initial != "high" {
					return true, fmt.Errorf("%s lost the shared reasoning preference", key)
				}
			}
			if _, err := nativeClientPayload(key, s); err != nil {
				return true, fmt.Errorf("%s shared selection is not exportable: %w", key, err)
			}
			if s.Saved != "" || s.Path != "" || u.clientState().Launching != "" {
				return true, errors.New("editing shared models unexpectedly prepared or launched an agent")
			}
		}
		return true, nil
	}
	return false, nil
}

func (u *nativeUI) modelSmokeSnapshot() map[string]string {
	u.initModelLibrary()
	s := u.library.selection
	_, ready := u.libraryStatus()
	state := map[string]string{"page": u.page, "shared-model-count": strconv.Itoa(len(s.Models)), "shared-default": s.Initial, "shared-model-saving": strconv.FormatBool(!ready)}
	if choice := s.choice(s.Initial); choice != nil {
		state["shared-default-name"] = choice.DisplayName
		state["shared-default-reasoning"] = choice.DefaultReasoning
	}
	return state
}

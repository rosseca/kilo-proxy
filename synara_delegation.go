package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
)

// The private gateway correction is selected by its account and model contract,
// independently of upstream version labels or release hashes. Installed
// application files are never rewritten.
const synaraDelegationRevision = 1

//go:embed synara_delegation_helpers.js
var synaraDelegationHelpers string

//go:embed synara_delegation_patches.json
var synaraDelegationPatchesJSON []byte

type synaraDelegationPatch struct {
	Before string `json:"before"`
	After  string `json:"after"`
	Count  int    `json:"count"`
}

func synaraDelegationModels(options synaraProfileOptions) (map[string]any, error) {
	if err := validateSynaraLibrary(options.Library); err != nil {
		return nil, err
	}
	if len(options.Library.Models) == 0 {
		return nil, errors.New("Choose a shared model library before preparing Synara delegation.")
	}
	codex, claude := []any{}, []any{}
	for _, choice := range terminalLibraryChoices(options.Library, options.Catalog) {
		name := choice.Model.Name
		if name == "" {
			name = choice.Model.ID
		}
		model := map[string]any{"slug": choice.Model.ID, "name": name}
		levels, initial := nativeReasoningFor(choice)
		if len(levels) > 0 {
			efforts := []any{}
			for _, level := range levels {
				efforts = append(efforts, map[string]any{"value": level, "description": level})
			}
			model["supportedReasoningEfforts"] = efforts
			if initial != "" {
				model["defaultReasoningEffort"] = initial
			}
		}
		codex = append(codex, model)
		// Synara Claude's custom IDs use the private per-model defaults. Their
		// delegation contract must not advertise a per-turn selector that can
		// contradict those defaults or switch to the normal account silently.
		claudeModel := map[string]any{"slug": choice.Model.ID, "name": name, "kiloEffortSource": "Kilo Proxy Models; close and reopen the workspace after changing defaults"}
		effortOptions := []any{}
		// Match planT3CodeProfiles, which supplies Synara's private Claude
		// settings: the saved preference is applied only after resolving the
		// model's native levels, and that resolution can select another level.
		if choice.DefaultReasoning != "" && validClaudeEffort(choice.Model.ID, choice.DefaultReasoning) && initial != "" && validClaudeEffort(choice.Model.ID, initial) {
			if !claudeEffortCompatible(choice.Model.ID, options.ClaudeCaps) {
				return nil, managedClaudeEffortVersionError(choice.Model.ID, "Synara")
			}
			claudeModel["kiloSavedEffort"] = initial
			claudeModel["supportedReasoningEfforts"] = []any{map[string]any{"value": initial, "description": "Applied private Claude default"}}
			effortOptions = append(effortOptions, map[string]any{"id": initial, "label": "Applied private Claude default"})
		}
		// An empty select is meaningful in the managed gateway: it suppresses
		// the driver's fallback effort values when no override was prepared.
		claudeModel["optionDescriptors"] = []any{map[string]any{"id": "effort", "label": "Reasoning effort", "type": "select", "options": effortOptions}}
		claude = append(claude, claudeModel)
	}
	return map[string]any{"codex": codex, "claudeAgent": claude, "defaultModel": options.Library.DefaultModel}, nil
}

func patchSynaraDelegationServer(source []byte, options synaraProfileOptions) ([]byte, error) {
	if len(source) == 0 || len(source) > synaraRuntimeSourceLimit {
		return nil, errors.New("Invalid Synara server source size.")
	}
	return applySynaraDelegationPatches(source, options)
}

func applySynaraDelegationPatches(source []byte, options synaraProfileOptions) ([]byte, error) {
	models, err := synaraDelegationModels(options)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(models)
	if err != nil {
		return nil, err
	}
	var patches []synaraDelegationPatch
	if json.Unmarshal(synaraDelegationPatchesJSON, &patches) != nil || len(patches) == 0 {
		return nil, errors.New("Invalid private Synara delegation contract.")
	}
	text := string(source)
	for _, patch := range patches {
		if patch.Before == "" || patch.Count < 1 || strings.Count(text, patch.Before) != patch.Count {
			return nil, errors.New("This Synara server does not match the verified delegation contract; no private changes saved.")
		}
		after := patch.After
		if after == "__KILO_SYNARA_DELEGATION_HELPERS__" {
			after = strings.Replace(synaraDelegationHelpers, "__KILO_SYNARA_GATEWAY_MODELS__", string(encoded), 1) + "\n" + patch.Before
		}
		text = strings.ReplaceAll(text, patch.Before, after)
	}
	return []byte(text), nil
}

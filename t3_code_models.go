package main

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

type t3CodeModelPolicy struct {
	CodexLibrary, ClaudeLibrary, OpenCodeLibrary, MuseLibrary modelLibrary
	ClaudeRoutes                                              map[string]string
	OpenCodeVariantLevels                                     map[string][]string
	MuseReasoningLevels                                       map[string][]string
	MuseReasoningDefaults                                     map[string]string
}

var t3CodeClaudeGatewayID = regexp.MustCompile(`^anthropic/(claude-(?:opus|sonnet|haiku|fable)-[0-9]+(?:[.-][0-9]+)?)(-[0-9]{8})?$`)

var t3CodeOfficialClaudeModels = []struct{ Slug, Name string }{
	{"claude-opus-5-5", "Claude Opus 5.5"},
	{"claude-sonnet-5-5", "Claude Sonnet 5.5"},
	{"claude-fable-5-1", "Claude Fable 5.1"},
}

// Effective selections belong to individual T3 instances. Deriving them never
// rewrites the shared library or the selections used by other integrations.
func deriveT3CodeModelPolicy(prefs t3CodePreferences, library modelLibrary, catalog []modelInfo, installed ...t3CodeInstallation) (t3CodeModelPolicy, error) {
	var installation t3CodeInstallation
	if len(installed) > 0 {
		installation = installed[0]
	}
	return deriveT3CodeCatalogPolicy(prefs, library, catalog, installation, nil)
}

func deriveT3CodeCatalogPolicy(prefs t3CodePreferences, library modelLibrary, catalog []modelInfo, installation t3CodeInstallation, prepared *t3CodePrepared) (t3CodeModelPolicy, error) {
	slugs := installation.ClaudeCatalogSlugs
	if len(slugs) == 0 {
		slugs = t3CodeClaudeBuiltinModelSlugs
	}
	return deriveT3CodeModelPolicyWithClaude(prefs, library, catalog, prepared, func() (modelLibrary, error) {
		claudeCatalog := catalog
		if prepared != nil {
			claudeCatalog = t3CodePreparedCatalog(catalog, prepared.ClaudeLibrary)
		}
		return t3CodeClaudeCatalogLibrary(library, claudeCatalog, slugs)
	})
}

// Select current metadata for the exact prepared IDs. An additive catalog
// refresh must not expand or interrupt a running T3 workspace.
func t3CodePreparedCatalog(catalog []modelInfo, library modelLibrary) []modelInfo {
	ids := make(map[string]bool, len(library.Models))
	for _, model := range library.Models {
		ids[model.ID] = true
	}
	models := make([]modelInfo, 0, len(ids))
	for _, model := range catalog {
		if ids[model.ID] {
			models = append(models, model)
		}
	}
	return models
}

func deriveT3CodeModelPolicyWithClaude(prefs t3CodePreferences, library modelLibrary, catalog []modelInfo, prepared *t3CodePrepared, officialClaude func() (modelLibrary, error)) (t3CodeModelPolicy, error) {
	result := t3CodeModelPolicy{CodexLibrary: emptyModelLibrary(), ClaudeLibrary: emptyModelLibrary(), OpenCodeLibrary: emptyModelLibrary(), MuseLibrary: emptyModelLibrary(), ClaudeRoutes: map[string]string{}, OpenCodeVariantLevels: map[string][]string{}, MuseReasoningLevels: map[string][]string{}, MuseReasoningDefaults: map[string]string{}}
	prefs = normalizeT3CodePreferences(prefs)
	if err := validateT3CodePreferences(prefs); err != nil {
		return result, err
	}
	if err := validateModelLibrary(library); err != nil {
		return result, err
	}
	if t3CodeOpenCodeKiloEnabled(prefs) {
		if len(library.Models) == 0 {
			return result, errors.New("Choose shared models before preparing OpenCode with Kilo Proxy.")
		}
		result.OpenCodeLibrary = cloneModelLibrary(library)
		for _, choice := range terminalLibraryChoices(library, catalog) {
			levels, _ := nativeReasoningFor(choice)
			result.OpenCodeVariantLevels[choice.Model.ID] = append([]string{}, levels...)
		}
	}
	if t3CodeMuseKiloEnabled(prefs) {
		if prefs.MuseModels == "official" {
			metaCatalog := catalog
			if prepared != nil {
				metaCatalog = t3CodePreparedCatalog(catalog, prepared.MuseLibrary)
			}
			result.MuseLibrary = t3CodeMetaCatalogLibrary(library, metaCatalog)
			if len(result.MuseLibrary.Models) == 0 {
				return result, errors.New("No Meta models are available in the connected Kilo catalog. Refresh the catalog, choose all shared models, or disable Muse Code · Kilo.")
			}
			if err := validateModelLibrary(result.MuseLibrary); err != nil {
				return result, err
			}
		} else {
			if len(library.Models) == 0 {
				return result, errors.New("Choose shared models before preparing Muse Code with Kilo Proxy.")
			}
			result.MuseLibrary = cloneModelLibrary(library)
		}
		for _, choice := range terminalLibraryChoices(result.MuseLibrary, catalog) {
			levels, initial := nativeReasoningFor(choice)
			result.MuseReasoningLevels[choice.Model.ID] = append([]string{}, levels...)
			result.MuseReasoningDefaults[choice.Model.ID] = initial
		}
	}
	if prefs.KiloCodex {
		if prefs.CodexModels == "shared" {
			result.CodexLibrary = cloneModelLibrary(library)
		} else {
			for _, model := range library.Models {
				if !strings.HasPrefix(model.ID, "openai/") && !strings.HasPrefix(model.ID, "chatgpt/") {
					continue
				}
				model.ReasoningLevels = append([]string(nil), model.ReasoningLevels...)
				result.CodexLibrary.Models = append(result.CodexLibrary.Models, model)
			}
			result.CodexLibrary.DefaultModel = t3CodeEffectiveDefault(result.CodexLibrary, library.DefaultModel)
			if len(result.CodexLibrary.Models) == 0 {
				return result, errors.New("Select at least one OpenAI or ChatGPT model in Models, choose shared models, or disable Codex · Kilo.")
			}
		}
		if err := validateT3CodeEffectiveLibrary(result.CodexLibrary, "Codex", prefs.CodexModels); err != nil {
			return result, err
		}
	}
	if prefs.KiloClaude {
		if prefs.ClaudeModels == "shared" {
			result.ClaudeLibrary = cloneModelLibrary(library)
		} else {
			var err error
			result.ClaudeLibrary, err = officialClaude()
			if err != nil {
				return result, err
			}
			if len(result.ClaudeLibrary.Models) == 0 {
				return result, errors.New("No Claude models are available in both T3 Code and the connected Kilo catalog. Refresh the catalog, choose shared models, or disable Claude · Kilo.")
			}
		}
		if err := validateT3CodeEffectiveLibrary(result.ClaudeLibrary, "Claude", prefs.ClaudeModels); err != nil {
			return result, err
		}
		var err error
		result.ClaudeRoutes, err = t3CodeBuildClaudeRoutes(result.ClaudeLibrary)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// Keep T3's native model order and expose only exact gateway identities
// that the installed driver can present. Prefer a single undated ID over
// dated snapshots of that version when choosing a native route.
func t3CodeClaudeCatalogLibrary(library modelLibrary, catalog []modelInfo, slugs []string) (modelLibrary, error) {
	result := emptyModelLibrary()
	matches := make(map[string][]modelInfo)
	for _, model := range catalog {
		if model.Connection != "" && model.Connection != "kilo" {
			continue
		}
		if slug := t3CodeNativeClaudeModelID(model.ID); slug != "" {
			matches[slug] = append(matches[slug], model)
		}
	}
	for _, slug := range slugs {
		candidates := matches[slug]
		if len(candidates) == 0 {
			continue
		}
		if len(candidates) > 1 {
			var undated []modelInfo
			for _, model := range candidates {
				if t3CodeClaudeGatewayID.FindStringSubmatch(model.ID)[2] == "" {
					undated = append(undated, model)
				}
			}
			if len(undated) != 1 {
				return result, fmt.Errorf("T3 Code Claude has multiple gateway IDs for %s. Choose an exact shared selection instead.", slug)
			}
			candidates = undated
		}
		result.Models = append(result.Models, t3CodeCatalogLibraryItem(candidates[0], library))
	}
	result.DefaultModel = t3CodeEffectiveDefault(result, library.DefaultModel)
	return result, nil
}

func t3CodeMetaPublisher(id string) bool {
	return strings.HasPrefix(id, "meta/") || strings.HasPrefix(id, "meta-llama/")
}

func t3CodeMetaCatalogLibrary(library modelLibrary, catalog []modelInfo) modelLibrary {
	result := emptyModelLibrary()
	for _, model := range catalog {
		if model.Connection != "" && model.Connection != "kilo" {
			continue
		}
		// Use the gateway ID, since the local Muse transport labels every
		// row as Meta regardless of its actual publisher.
		if t3CodeMetaPublisher(model.ID) {
			result.Models = append(result.Models, t3CodeCatalogLibraryItem(model, library))
		}
	}
	sort.Slice(result.Models, func(i, j int) bool {
		a, b := result.Models[i].ID, result.Models[j].ID
		museA, museB := strings.HasPrefix(a, "meta/muse-"), strings.HasPrefix(b, "meta/muse-")
		if museA != museB {
			return museA
		}
		return a < b
	})
	result.DefaultModel = t3CodeEffectiveDefault(result, library.DefaultModel)
	return result
}

func validateT3CodeEffectiveLibrary(library modelLibrary, provider, mode string) error {
	if len(library.Models) == 0 {
		return fmt.Errorf("No %s models are available for T3 Code in %s mode. Refresh the catalog, choose shared models, or disable %s · Kilo.", provider, mode, provider)
	}
	if len(library.Models) > t3CodeCustomModelLimit {
		return fmt.Errorf("T3 Code %s supports at most %d models. Choose a shared selection with fewer models instead.", provider, t3CodeCustomModelLimit)
	}
	return validateT3CodeLibrary(library)
}

func t3CodeCatalogLibraryItem(model modelInfo, library modelLibrary) modelLibraryItem {
	for _, selected := range library.Models {
		if selected.ID == model.ID {
			selected.ReasoningLevels = append([]string(nil), selected.ReasoningLevels...)
			return selected
		}
	}
	// Catalog capabilities remain metadata, resolved by terminalLibraryChoices.
	// Do not invent a reasoning default or turn advertised levels into overrides.
	return modelLibraryItem{ID: model.ID, ContextPreset: contextPresetRecommended}
}

func t3CodeEffectiveDefault(library modelLibrary, preferred string) string {
	for _, model := range library.Models {
		if model.ID == preferred {
			return preferred
		}
	}
	if len(library.Models) > 0 {
		return library.Models[0].ID
	}
	return ""
}

func t3CodeNativeClaudeModelID(id string) string {
	parts := t3CodeClaudeGatewayID.FindStringSubmatch(id)
	if parts == nil {
		return ""
	}
	return strings.ReplaceAll(parts[1], ".", "-")
}

// A version alias can only resolve to one prepared gateway ID. In particular,
// dotted/hyphenated IDs and different dated snapshots may not silently collide.
func t3CodeBuildClaudeRoutes(library modelLibrary) (map[string]string, error) {
	routes := map[string]string{}
	selected := map[string]bool{}
	for _, model := range library.Models {
		selected[model.ID] = true
	}
	for _, model := range library.Models {
		parts := t3CodeClaudeGatewayID.FindStringSubmatch(model.ID)
		if parts == nil {
			continue
		}
		native := t3CodeNativeClaudeModelID(model.ID)
		for _, alias := range []string{native, native + parts[2]} {
			if selected[alias] && alias != model.ID || routes[alias] != "" && routes[alias] != model.ID {
				return nil, errors.New("T3 Code Claude has ambiguous native model IDs. Choose one exact gateway ID per Claude family/version.")
			}
			routes[alias] = model.ID
		}
	}
	return routes, nil
}

func validateT3CodeClaudeRoutes(library modelLibrary, routes map[string]string) error {
	if err := validateT3CodeLibrary(library); err != nil {
		return err
	}
	want, err := t3CodeBuildClaudeRoutes(library)
	if err != nil {
		return err
	}
	if len(want) != len(routes) || len(want) > 0 && !reflect.DeepEqual(want, routes) {
		return errors.New("The prepared T3 Code Claude model routes are invalid. Prepare T3 Code again.")
	}
	return nil
}

package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func t3CodeOfficialCatalogFixture() []modelInfo {
	return []modelInfo{
		{ID: "anthropic/claude-opus-5.5", Name: "Claude Opus 5.5", ContextWindow: 1000000, MaxOutputTokens: 64000, ReasoningEfforts: []string{"low", "medium", "high"}},
		{ID: "anthropic/claude-sonnet-5.5", Name: "Claude Sonnet 5.5", ContextWindow: 1000000, MaxOutputTokens: 64000, ReasoningEfforts: []string{"low", "medium", "high"}},
		{ID: "anthropic/claude-fable-5.1", Name: "Claude Fable 5.1", ContextWindow: 1000000, MaxOutputTokens: 64000},
		{ID: "openai/gpt-catalog-only", Name: "Unselected OpenAI"},
		{ID: "vendor/claude-opus-5.5", Name: "Claude Opus 5.5"},
	}
}

func t3CodePolicyFixturePreferences() t3CodePreferences {
	return normalizeT3CodePreferences(t3CodePreferences{})
}

func TestT3CodeModelPolicyScopesOfficialModelsPerInstance(t *testing.T) {
	library := modelLibrary{SchemaVersion: 1, DefaultModel: "vendor/default", Models: []modelLibraryItem{
		{ID: "vendor/default"},
		{ID: "openai/gpt-selected", ReasoningEffort: "high", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}},
		{ID: "chatgpt/gpt-selected", ContextPreset: contextPresetLow},
		{ID: "vendor/openai/fake"},
		{ID: "anthropic/claude-sonnet-5.5", DisplayName: "My Sonnet", ReasoningEffort: "low", ContextWindow: 128000},
	}}
	before := cloneModelLibrary(library)
	policy, err := deriveT3CodeModelPolicy(t3CodePolicyFixturePreferences(), library, t3CodeOfficialCatalogFixture())
	if err != nil {
		t.Fatal(err)
	}
	wantCodex := []modelLibraryItem{library.Models[1], library.Models[2]}
	if !reflect.DeepEqual(policy.CodexLibrary.Models, wantCodex) || policy.CodexLibrary.DefaultModel != "openai/gpt-selected" {
		t.Fatalf("Codex official selection escaped the shared OpenAI/ChatGPT subset: %+v", policy.CodexLibrary)
	}
	wantClaude := []string{"anthropic/claude-opus-5.5", "anthropic/claude-sonnet-5.5", "anthropic/claude-fable-5.1"}
	for i, id := range wantClaude {
		if policy.ClaudeLibrary.Models[i].ID != id {
			t.Fatalf("Claude official order/identity: %+v", policy.ClaudeLibrary)
		}
	}
	if policy.ClaudeLibrary.DefaultModel != wantClaude[0] || !reflect.DeepEqual(policy.ClaudeLibrary.Models[1], library.Models[4]) {
		t.Fatal("Claude used an unrelated shared default or lost saved model preferences", policy.ClaudeLibrary)
	}
	if policy.ClaudeLibrary.Models[0].ReasoningEffort != "" || policy.ClaudeLibrary.Models[2].ReasoningCustom {
		t.Fatal("catalog models acquired invented reasoning defaults")
	}
	wantRoutes := map[string]string{"claude-opus-5-5": wantClaude[0], "claude-sonnet-5-5": wantClaude[1], "claude-fable-5-1": wantClaude[2]}
	if !reflect.DeepEqual(policy.ClaudeRoutes, wantRoutes) || !reflect.DeepEqual(before, library) {
		t.Fatal("wrong per-family routes or shared library mutation", policy.ClaudeRoutes)
	}
	policy.CodexLibrary.Models[0].ReasoningLevels[0] = "max"
	if library.Models[1].ReasoningLevels[0] != "low" {
		t.Fatal("effective selection shares mutable reasoning data")
	}
}

func TestT3CodeModelPolicySharedSelectionPreservesExactModelsAndDefaults(t *testing.T) {
	prefs := t3CodePolicyFixturePreferences()
	prefs.CodexModels, prefs.ClaudeModels = "shared", "shared"
	library := modelLibrary{SchemaVersion: 1, DefaultModel: "vendor/default", Models: []modelLibraryItem{{ID: "vendor/default"}, {ID: "anthropic/claude-opus-5.5"}}, Roles: map[string]string{"vendor/default": "coding"}}
	policy, err := deriveT3CodeModelPolicy(prefs, library, nil)
	if err != nil || !reflect.DeepEqual(policy.CodexLibrary, library) || !reflect.DeepEqual(policy.ClaudeLibrary, library) {
		t.Fatalf("shared selection changed: %+v %v", policy, err)
	}
	for requested, want := range map[string]string{"vendor/default": "vendor/default", "anthropic/claude-opus-5.5": "anthropic/claude-opus-5.5", "claude-opus-5-5": "anthropic/claude-opus-5.5"} {
		got, err := t3CodeResolveModel(policy.ClaudeLibrary, policy.ClaudeRoutes, requested)
		if err != nil || got != want {
			t.Fatalf("%s routed to %s: %v", requested, got, err)
		}
	}
	for _, id := range []string{"opus", "sonnet", "claude-sonnet-5-5", "anthropic/claude-opus-5-5", "vendor/other"} {
		if _, err := t3CodeResolveModel(policy.ClaudeLibrary, policy.ClaudeRoutes, id); err == nil {
			t.Fatalf("unselected %s silently fell back", id)
		}
	}
}

func TestT3CodeModelPolicyUnavailableAmbiguousAndDisabled(t *testing.T) {
	library := modelLibrary{SchemaVersion: 1, DefaultModel: "openai/gpt-selected", Models: []modelLibraryItem{{ID: "openai/gpt-selected"}}}
	for _, tc := range []struct {
		name string
		edit func([]modelInfo) []modelInfo
		want string
	}{
		{"empty intersection", func(c []modelInfo) []modelInfo { return c[3:] }, "No Claude models are available"},
		{"ambiguous spelling", func(c []modelInfo) []modelInfo { return append(c, modelInfo{ID: "anthropic/claude-opus-5-5"}) }, "multiple gateway IDs"},
		{"ambiguous date", func(c []modelInfo) []modelInfo {
			return append(append(c[:1], c[2:]...), modelInfo{ID: "anthropic/claude-sonnet-5-5-20261012"}, modelInfo{ID: "anthropic/claude-sonnet-5-5-20261013"})
		}, "multiple gateway IDs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := deriveT3CodeModelPolicy(t3CodePolicyFixturePreferences(), library, tc.edit(t3CodeOfficialCatalogFixture())); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
		})
	}
	prefs := t3CodePolicyFixturePreferences()
	prefs.KiloClaude = false
	policy, err := deriveT3CodeModelPolicy(prefs, library, nil)
	if err != nil || len(policy.ClaudeLibrary.Models) != 0 || len(policy.CodexLibrary.Models) != 1 {
		t.Fatal("disabled Claude blocked Codex/direct providers", policy, err)
	}
	prefs.KiloCodex = false
	if _, err := deriveT3CodeModelPolicy(prefs, emptyModelLibrary(), nil); err != nil {
		t.Fatal("direct providers required shared or connected models", err)
	}
}

func TestT3CodeModelPolicyDoesNotSilentlyTruncate(t *testing.T) {
	library := emptyModelLibrary()
	for i := 0; i <= t3CodeCustomModelLimit; i++ {
		library.Models = append(library.Models, modelLibraryItem{ID: fmt.Sprintf("openai/model-%d", i)})
	}
	library.DefaultModel = library.Models[0].ID
	prefs := t3CodePolicyFixturePreferences()
	prefs.KiloClaude = false
	if _, err := deriveT3CodeModelPolicy(prefs, library, nil); err == nil || !strings.Contains(err.Error(), "at most 32") {
		t.Fatal("too many official models were silently truncated", err)
	}
}

func TestT3CodeClaudeRoutesRejectAmbiguousAndModifiedDestinations(t *testing.T) {
	library := modelLibrary{SchemaVersion: 1, DefaultModel: "anthropic/claude-opus-5.5", Models: []modelLibraryItem{{ID: "anthropic/claude-opus-5.5"}}}
	routes, err := t3CodeBuildClaudeRoutes(library)
	if err != nil || validateT3CodeClaudeRoutes(library, routes) != nil {
		t.Fatal("valid route rejected", err)
	}
	routes["claude-opus-5-5"] = "vendor/other"
	if _, err := t3CodeResolveModel(library, routes, "claude-opus-5-5"); err == nil {
		t.Fatal("modified route accepted")
	}
	for _, duplicate := range []string{"anthropic/claude-opus-5-5", "anthropic/claude-opus-5-5-20261012", "claude-opus-5-5"} {
		copy := cloneModelLibrary(library)
		copy.Models = append(copy.Models, modelLibraryItem{ID: duplicate})
		if _, err := t3CodeBuildClaudeRoutes(copy); err == nil {
			t.Fatalf("ambiguous %s acquired a route", duplicate)
		}
	}
}

func TestT3CodeClaudeRoutesKeepNativeVersionForUniqueDatedGatewayModel(t *testing.T) {
	library := modelLibrary{SchemaVersion: 1, DefaultModel: "anthropic/claude-opus-5.5-20261012", Models: []modelLibraryItem{{ID: "anthropic/claude-opus-5.5-20261012"}}}
	routes, err := t3CodeBuildClaudeRoutes(library)
	if err != nil || t3CodeNativeClaudeModelID(library.DefaultModel) != "claude-opus-5-5" {
		t.Fatal("dated gateway ID lost its native builtin model identity", routes, err)
	}
	for _, request := range []string{"claude-opus-5-5", "claude-opus-5-5-20261012", library.DefaultModel} {
		if got, err := t3CodeResolveModel(library, routes, request); err != nil || got != library.DefaultModel {
			t.Fatalf("%s routed to %s: %v", request, got, err)
		}
	}
	if _, err := t3CodeResolveModel(library, routes, "claude-opus-5-5-20261013"); err == nil {
		t.Fatal("unselected dated snapshot acquired a route")
	}
}

func TestT3CodeClaudeCatalogMatchesBundledModelsAndKeepsNativeOrder(t *testing.T) {
	server := []byte(`const unrelated = "claude-opus-6";
// src/provider/model-manifest.json
var model_manifest_default = {
	version: 1,
	providers: {
		"claudeAgent": {
			"models": [
				{"slug": "claude-opus-5-5"},
				{"slug": "claude-fable-5-2"},
				{"slug": "claude-sonnet-5-5"},
				{"slug": "claude-haiku-5-5"},
				{"slug": "claude-opus-4-5"}
			]
		},
		"codex": {"models": [{"slug": "claude-opus-6"}]}
	}
};
`)
	slugs := t3CodeBundledClaudeSlugs(server)
	if want := []string{"claude-opus-5-5", "claude-fable-5-2", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-opus-4-5"}; !reflect.DeepEqual(slugs, want) {
		t.Fatal("unexpected bundled Claude models", slugs)
	}
	if duplicate := t3CodeBundledClaudeSlugs(append(append([]byte{}, server...), server...)); duplicate != nil {
		t.Fatal("ambiguous manifest accepted", duplicate)
	}
	prefs := t3CodePolicyFixturePreferences()
	prefs.KiloCodex = false
	library := emptyModelLibrary()
	library.DefaultModel = "anthropic/claude-haiku-5.5"
	library.Models = []modelLibraryItem{{ID: library.DefaultModel, DisplayName: "My Haiku"}}
	catalog := []modelInfo{
		{ID: "anthropic/claude-haiku-5.5"},
		{ID: "anthropic/claude-opus-6"},
		{ID: "anthropic/claude-opus-5-5-20261010"},
		{ID: "vendor/claude-sonnet-5.5", Name: "Claude Sonnet 5.5"},
		{ID: "anthropic/claude-fable-5.2"},
		{ID: "anthropic/claude-opus-5.5"},
		{ID: "anthropic/claude-opus-4.5", Connection: "chatgpt"},
	}
	policy, err := deriveT3CodeModelPolicy(prefs, library, catalog, t3CodeInstallation{ClaudeCatalogSlugs: slugs})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, model := range policy.ClaudeLibrary.Models {
		ids = append(ids, model.ID)
	}
	want := []string{"anthropic/claude-opus-5.5", "anthropic/claude-fable-5.2", library.DefaultModel}
	if !reflect.DeepEqual(ids, want) || policy.ClaudeLibrary.DefaultModel != library.DefaultModel || !reflect.DeepEqual(policy.ClaudeLibrary.Models[2], library.Models[0]) {
		t.Fatal("Claude policy lost native order, exact IDs or saved override", policy.ClaudeLibrary)
	}
	for slug, id := range map[string]string{"claude-opus-5-5": want[0], "claude-fable-5-2": want[1], "claude-haiku-5-5": want[2]} {
		if got, err := t3CodeResolveModel(policy.ClaudeLibrary, policy.ClaudeRoutes, slug); err != nil || got != id {
			t.Fatal("native alias did not route to its exact ID", slug, got, err)
		}
	}
	for _, id := range []string{"claude-opus-6", "claude-sonnet-5-5", "anthropic/claude-opus-5-5-20261010", "claude-opus-4-5"} {
		if _, err := t3CodeResolveModel(policy.ClaudeLibrary, policy.ClaudeRoutes, id); err == nil {
			t.Fatal("unexpected Claude route", id)
		}
	}
}

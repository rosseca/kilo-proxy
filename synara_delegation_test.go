package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func synaraDelegationContractFixture(t *testing.T) []byte {
	t.Helper()
	var patches []synaraDelegationPatch
	if err := json.Unmarshal(synaraDelegationPatchesJSON, &patches); err != nil {
		t.Fatal(err)
	}
	text := ""
	// Retain the largest upstream contracts first; shorter anchors occur inside
	// them and must not be duplicated by this portable source fixture.
	for _, patch := range patches {
		contained := false
		for _, other := range patches {
			if len(other.Before) > len(patch.Before) && strings.Contains(other.Before, patch.Before) {
				contained = true
				break
			}
		}
		if !contained {
			text += strings.Repeat(patch.Before+"\n", patch.Count)
		}
	}
	return []byte(text)
}

func TestSynaraDelegationRejectsUnvalidatedServerAndChangedContracts(t *testing.T) {
	options := synaraProfileTestOptions(t)
	source := synaraDelegationContractFixture(t)
	if _, err := patchSynaraDelegationServer(source, options); err == nil {
		t.Fatal("an unverified server was patched")
	}
	patched, err := applySynaraDelegationPatches(source, options)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(patched), "__KILO_SYNARA_") {
		t.Fatal("an unresolved private runtime template remained")
	}
	for _, changed := range []string{
		strings.Replace(string(source), "function loadAgentGatewayProviderCatalog(input)", "function loadAgentGatewayProviderCatalog(other)", 1),
		string(source) + "\nfunction loadAgentGatewayProviderCatalog(input) {",
	} {
		if result, err := applySynaraDelegationPatches([]byte(changed), options); err == nil || result != nil {
			t.Fatal("a changed or ambiguous upstream contract was partly accepted")
		}
	}
}

func TestSynaraDelegationAccountsAndModelDefaults(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the private Synara gateway contract check")
	}
	options := synaraProfileTestOptions(t)
	options.Library = modelLibrary{SchemaVersion: 1, DefaultModel: "chatgpt/gpt-6-astra", Models: []modelLibraryItem{
		{ID: "chatgpt/gpt-6-astra", DisplayName: "GPT-6 Astra", ReasoningCustom: true, ReasoningEffort: "high", ReasoningLevels: []string{"low", "high"}},
		{ID: "anthropic/claude-opus-5", DisplayName: "Claude Opus 5", ReasoningEffort: "high"},
		// An unknown native family must not acquire invented effort metadata.
		{ID: "vendor/unknown-claude", DisplayName: "Unknown native Claude", ReasoningEffort: "high"},
		{ID: "vendor/no-native-effort", ReasoningEffort: "none"},
	}}
	models, err := synaraDelegationModels(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range models["claudeAgent"].([]any) {
		model := raw.(map[string]any)
		if model["slug"] == "vendor/unknown-claude" || model["slug"] == "vendor/no-native-effort" {
			if _, exists := model["kiloSavedEffort"]; exists {
				t.Fatal("non-native Claude effort was advertised as a supported override")
			}
		}
	}
	public, _ := json.Marshal(models)
	if options.LocalKey != "" && strings.Contains(string(public), options.LocalKey) {
		t.Fatal("a private proxy credential leaked into delegation capabilities")
	}
	empty := options
	empty.Library = emptyModelLibrary()
	if _, err := synaraDelegationModels(empty); err == nil {
		t.Fatal("empty prepared models would enable the native Kilo catalog fallback")
	}
	var patches []synaraDelegationPatch
	if err := json.Unmarshal(synaraDelegationPatchesJSON, &patches); err != nil {
		t.Fatal(err)
	}
	fixture := map[string]any{"models": models, "helpers": synaraDelegationHelpers}
	for _, patch := range patches {
		for key, prefix := range map[string]string{
			"catalog": "function loadAgentGatewayProviderCatalog(input) {",
			"resolve": "function resolveAgentGatewayTarget(input) {",
			"schema":  "const MODEL_SELECTION_INPUT_SCHEMA = {",
			"decode":  "function readModelSelectionArg(args, name) {",
		} {
			if strings.HasPrefix(patch.After, prefix) {
				fixture[key] = patch.After
			}
		}
	}
	data, _ := json.Marshal(fixture)
	path := filepath.Join(t.TempDir(), "gateway-contract.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, "scripts/verify_synara_delegation.mjs", path)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("private gateway behavior failed: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

func TestSynaraDelegationInstalledSource(t *testing.T) {
	path := os.Getenv("KILO_TEST_SYNARA_SERVER_SOURCE")
	if path == "" {
		t.Skip("set KILO_TEST_SYNARA_SERVER_SOURCE to the extracted official beta server")
	}
	source, err := readOpenDesignShimFile(path, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	options := synaraProfileTestOptions(t)
	patched, err := patchSynaraDelegationServer(source, options)
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "patched-server.mjs")
	if err := os.WriteFile(path, patched, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("official private snapshot syntax: %v\n%s", err, output)
	}
	prepared := strings.Index(string(patched), "const prepared = yield* Effect.forEach(input.threads")
	reserved := strings.Index(string(patched), "const reservation = yield* operationStore.reserve(")
	if prepared < 0 || reserved <= prepared {
		t.Fatal("batch preflight no longer precedes durable creation reservation")
	}
}

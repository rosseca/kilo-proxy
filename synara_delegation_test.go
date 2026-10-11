package main

import (
	"bytes"
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

func TestSynaraDelegationAcceptsCompatibleBuildsAndRejectsChangedContracts(t *testing.T) {
	options := synaraProfileTestOptions(t)
	source := synaraDelegationContractFixture(t)
	patched, err := patchSynaraDelegationServer(source, options)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(patched), "__KILO_SYNARA_") {
		t.Fatal("an unresolved private runtime template remained")
	}
	for _, extra := range []string{"// new compatible Beta release\n", "const unrelatedFutureCapability = true;\n"} {
		if result, err := patchSynaraDelegationServer(append([]byte(extra), source...), options); err != nil || !bytes.HasPrefix(result, []byte(extra)) {
			t.Fatal("a compatible source with another digest was rejected or rewritten outside its contracts", err)
		}
	}
	for _, changed := range []string{
		strings.Replace(string(source), "function loadAgentGatewayProviderCatalog(input)", "function loadAgentGatewayProviderCatalog(other)", 1),
		string(source) + "\nfunction loadAgentGatewayProviderCatalog(input) {",
	} {
		if result, err := patchSynaraDelegationServer([]byte(changed), options); err == nil || result != nil {
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
	fixture := map[string]any{"models": models, "helpers": synaraDelegationHelpers, "modelRules": synaraDelegationModelRulesContract}
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
	if strings.Count(string(source), synaraDelegationModelRulesContract) != 1 {
		t.Fatal("the installed model option guidance no longer matches the required capability contract")
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

func TestSynaraDelegationClaudeEffortMatchesPreparedProfile(t *testing.T) {
	for _, test := range []struct {
		name, id, saved, expected string
		catalog, custom           []string
		useCustom                 bool
	}{
		{name: "resolved native default differs", id: "anthropic/claude-opus-5", saved: "high", catalog: []string{"low"}, expected: "low"},
		{name: "no native levels", id: "anthropic/claude-opus-5.5", saved: "high"},
		{name: "automatic", id: "anthropic/claude-opus-5", catalog: []string{"low", "high"}},
		{name: "none", id: "anthropic/claude-opus-5", saved: "none", useCustom: true, custom: []string{"none", "low", "high"}},
		{name: "explicit empty levels", id: "anthropic/claude-opus-5", useCustom: true},
		{name: "non-native level", id: "anthropic/claude-opus-5", saved: "max", catalog: []string{"high", "max"}},
		{name: "selected native level", id: "anthropic/claude-opus-5.5", saved: "high", useCustom: true, custom: []string{"low", "high"}, expected: "high"},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := synaraProfileTestOptions(t)
			options.ClaudeCaps = claudeCaps("2.1.288")
			options.Library = modelLibrary{SchemaVersion: 1, DefaultModel: test.id, Models: []modelLibraryItem{{ID: test.id, ContextWindow: 200000, ReasoningEffort: test.saved, ReasoningCustom: test.useCustom, ReasoningLevels: test.custom}}}
			options.Catalog = []modelInfo{{ID: test.id, ReasoningEfforts: test.catalog}}
			plan, err := planSynaraProfiles(options)
			if err != nil {
				t.Fatal(err)
			}
			var settings map[string]any
			for _, file := range plan.Files {
				if file.path == filepath.Join(plan.ClaudeHome, "settings.json") {
					if err := json.Unmarshal(file.new, &settings); err != nil {
						t.Fatal(err)
					}
				}
			}
			applied := stringValue(object(object(settings["modelSettings"])[claudeEffortKey(test.id)])["effortLevel"])
			if applied != test.expected {
				t.Fatalf("prepared Claude default = %q, want %q", applied, test.expected)
			}
			models, err := synaraDelegationModels(options)
			if err != nil {
				t.Fatal(err)
			}
			descriptor := models["claudeAgent"].([]any)[0].(map[string]any)
			if descriptor["slug"] != test.id || stringValue(descriptor["kiloSavedEffort"]) != applied {
				t.Fatal("delegation changed the gateway ID or advertised another Claude default", descriptor)
			}
			effortOptions := descriptor["optionDescriptors"].([]any)[0].(map[string]any)["options"].([]any)
			if applied == "" {
				if len(effortOptions) != 0 || descriptor["supportedReasoningEfforts"] != nil {
					t.Fatal("a model without a prepared override advertised driver fallback levels", descriptor)
				}
			} else if len(effortOptions) != 1 || effortOptions[0].(map[string]any)["id"] != applied {
				t.Fatal("advertised options differ from the prepared native default", descriptor)
			}
		})
	}
}

// The upstream capability function is exercised in the portable Node fixture
// and checked against the installed server in the opt-in source test.
const synaraDelegationModelRulesContract = `function modelTargetOptionRules(provider, model) {
	const rules = providerTargetOptionRules(provider).map(({ key, valueType, allowedValues, allowedValuesSource, allowsCustomValue }) => ({
		key,
		valueType,
		allowedValues,
		allowedValuesSource,
		...allowsCustomValue === void 0 ? {} : { allowsCustomValue }
	}));
	const replaceAllowedValues = (key, values, allowEmpty = false) => {
		if (values.length === 0 && !allowEmpty) return;
		const index = rules.findIndex((rule) => rule.key === key);
		if (index < 0) return;
		rules[index] = {
			...rules[index],
			allowedValues: values,
			allowedValuesSource: "model-discovery",
			...rules[index].allowsCustomValue === true ? { allowsCustomValue: false } : {}
		};
	};
	const discoveredEfforts = model.supportedReasoningEfforts?.map((entry) => entry.value) ?? [];
	const primaryOptionKey = providerPrimaryOptionKey(provider);
	if (rules.find((rule) => rule.key === primaryOptionKey)?.allowsCustomValue !== true) replaceAllowedValues(primaryOptionKey, discoveredEfforts);
	for (const descriptor of model.optionDescriptors ?? []) {
		const spec = providerOptionRuleSpec(provider, descriptor.id);
		if (spec?.advertised === "when-discovered") rules.push({
			key: spec.key,
			valueType: spec.valueType,
			allowedValues: [],
			allowedValuesSource: "model-discovery"
		});
		const rule = rules.find((candidate) => candidate.key === descriptor.id);
		if (!rule) continue;
		if (descriptor.type === "select") replaceAllowedValues(descriptor.id, descriptor.options.map((option) => convertDiscoveredOptionValue(option.id, rule.valueType)).filter((value) => value !== null), true);
		else if (descriptor.type === "boolean") replaceAllowedValues(descriptor.id, [true, false]);
	}
	return rules;
}`

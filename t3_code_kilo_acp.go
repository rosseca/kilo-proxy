package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

const (
	t3CodeKiloACPOption     = "kilo-acp"
	t3CodeKiloACPInstanceID = "kilo_code_acp"
	t3CodeACPRegistryDriver = "acpRegistry"
)

var t3CodeKiloACPSupport = t3CodeProviderSupport{t3CodeKiloACPOption, "Kilo Code (ACP)"}

var (
	t3CodeACPRegistryEntry   = regexp.MustCompile(`(?:^|,)\s*AcpRegistryDriver\s*(?:,|$)`)
	t3CodeACPRegistryFactory = regexp.MustCompile(`(?m)^const\s+AcpRegistryDriver\s*=\s*\{\s*driverKind:\s*([\w$]+)\s*,\s*metadata:\s*\{[^{}]*\bsupportsMultipleInstances:\s*true\b[^{}]*\}\s*,\s*configSchema:\s*AcpRegistrySettings\s*,\s*defaultConfig:[^\n]*,\s*create:\s*`)
)

func t3CodeSupportsKiloACP(source, registry string) bool {
	if !t3CodeACPRegistryEntry.MatchString(registry) {
		return false
	}
	factory := t3CodeACPRegistryFactory.FindStringSubmatch(source)
	if len(factory) != 2 {
		return false
	}
	binding := regexp.MustCompile(`(?m)^const\s+` + regexp.QuoteMeta(factory[1]) + `\s*=\s*(?:ProviderDriverKind\.make\("acpRegistry"\)|ACP_REGISTRY_DRIVER_KIND)\s*;`)
	return binding.MatchString(source)
}

func t3CodeIsKiloACP(raw any) bool {
	instance, ok := raw.(map[string]any)
	if !ok || instance["driver"] != t3CodeACPRegistryDriver {
		return false
	}
	config, ok := instance["config"].(map[string]any)
	return ok && config["agentId"] == "kilo" && (config["source"] == nil || config["source"] == "registry")
}

func t3CodeEnabledProvider(raw any) bool {
	instance, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	enabled, exists := instance["enabled"]
	return !exists || enabled == true
}

func validT3CodeKiloACPPlan(preferences t3CodePreferences, providers map[string]any) bool {
	want := 0
	if helperContains(preferences.Providers, t3CodeKiloACPOption) {
		want = 1
	}
	count := 0
	for id, provider := range providers {
		if !t3CodeIsKiloACP(provider) {
			continue
		}
		if !t3CodeEnabledProvider(provider) || (id != t3CodeKiloACPInstanceID && t3CodeReservedACPInstanceID(id)) {
			return false
		}
		count++
	}
	return count == want
}

// Only record the fields needed to verify the selected registry account.
// An existing T3 instance keeps its ID, environment and account settings.
func planT3CodeKiloACP(privateRoot string) (string, map[string]any, error) {
	if !filepath.IsAbs(privateRoot) {
		return "", nil, errors.New("Cannot resolve the private T3 Code workspace for Kilo Code ACP.")
	}
	path := filepath.Join(privateRoot, "data", "userdata", "settings.json")
	data, err := readCatalogFile(path)
	var document struct {
		Providers map[string]any `json:"providerInstances"`
	}
	if err == nil {
		if err := json.Unmarshal(data, &document); err != nil {
			return "", nil, errors.New("Cannot read the private T3 Code accounts for Kilo Code ACP.")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, errors.New("Cannot safely read the private T3 Code accounts for Kilo Code ACP.")
	}
	var active []string
	for id, provider := range document.Providers {
		if !t3CodeReservedACPInstanceID(id) && t3CodeIsKiloACP(provider) && t3CodeEnabledProvider(provider) {
			active = append(active, id)
		}
	}
	id := t3CodeKiloACPInstanceID
	// Keep a previously created instance and its chat identity. For a new
	// configuration, reuse an already active manual registry instance.
	if len(active) > 0 && !t3CodeIsKiloACP(document.Providers[id]) {
		sort.Strings(active)
		id = active[0]
	}
	expected := map[string]any{
		"driver": t3CodeACPRegistryDriver, "enabled": true,
		"config": map[string]any{"agentId": "kilo"},
	}
	if id != t3CodeKiloACPInstanceID {
		if _, explicit := document.Providers[id].(map[string]any)["enabled"]; !explicit {
			delete(expected, "enabled")
		}
	}
	return id, expected, nil
}

func t3CodeReservedACPInstanceID(id string) bool {
	switch id {
	case "", t3CodeKiloACPInstanceID, t3CodeCodexNormalID, t3CodeCodexProxyID, t3CodeClaudeNormalID,
		t3CodeClaudeProxyID, t3CodeOpenCodeProxyID, t3CodeMuseProxyID, "codex", "claudeAgent":
		return true
	}
	for _, provider := range t3CodeOptionalProviders {
		if provider.ID == id {
			return true
		}
	}
	return false
}

func applyT3CodeKiloACP(providers map[string]any, id string) error {
	if current, exists := providers[id]; exists {
		if !t3CodeIsKiloACP(current) || (id != t3CodeKiloACPInstanceID && !t3CodeEnabledProvider(current)) {
			return errors.New("The private T3 Code ACP account changed. Reload the workspace settings and try again.")
		}
		if id == t3CodeKiloACPInstanceID {
			current.(map[string]any)["enabled"] = true
		}
		return nil
	}
	if id != t3CodeKiloACPInstanceID {
		return errors.New("The private T3 Code ACP account was removed. Reload the workspace settings and try again.")
	}
	providers[id] = map[string]any{
		"driver": t3CodeACPRegistryDriver, "displayName": t3CodeKiloACPSupport.Label,
		"enabled": true, "environment": []any{},
		"config": map[string]any{"source": "registry", "agentId": "kilo", "distribution": "auto"},
	}
	return nil
}

// Deregister an opt-out only when the previous Kilo plan owned this slot.
// Other ACP accounts and T3's runtime data stay outside this operation.
func removeT3CodeManagedACP(selection string, providers, planned map[string]any) {
	if planned[t3CodeKiloACPInstanceID] != nil || !t3CodeIsKiloACP(providers[t3CodeKiloACPInstanceID]) {
		return
	}
	data, err := readCatalogFile(selection)
	if err != nil {
		return
	}
	var previous struct {
		Providers map[string]any `json:"providers"`
	}
	if json.Unmarshal(data, &previous) == nil && t3CodeIsKiloACP(previous.Providers[t3CodeKiloACPInstanceID]) {
		delete(providers, t3CodeKiloACPInstanceID)
	}
}

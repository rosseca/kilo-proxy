package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const t3CodeOpenCodeProxyID = "kilo_opencode_proxy"
const t3CodeOpenCodeKey = "KILO_T3_OPENCODE_API_KEY"

func t3CodeOpenCodeKiloEnabled(prefs t3CodePreferences) bool {
	prefs = normalizeT3CodePreferences(prefs)
	if prefs.OpenCodeConnection != "kilo" {
		return false
	}
	for _, id := range prefs.Providers {
		if id == "opencode" {
			return true
		}
	}
	return false
}

func t3CodeOpenCodePaths(root string, second bool) (home, config string) {
	major := "1"
	if second {
		major = "2"
	}
	home = filepath.Join(root, "profiles", "opencode-kilo-"+major)
	return home, filepath.Join(home, ".config", "opencode", "opencode.json")
}

// Record only filesystem identities. Normal OpenCode settings, databases and
// credentials are neither opened nor copied into the Kilo account.
func t3CodeOpenCodeNormalPaths(root string, rt clientLaunchRuntime) []string {
	normal := t3CodeNormalEnvironment(rt.home, os.Environ(), rt.platform)
	paths := []string{filepath.Join(rt.home, ".opencode"), filepath.Join(root, "profiles", "opencode2")}
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "APPDATA", "LOCALAPPDATA"} {
		if value := normal[name]; value != "" {
			paths = append(paths, filepath.Join(value, "opencode"))
		}
	}
	for _, name := range []string{"OPENCODE_CONFIG", "OPENCODE_CONFIG_DIR"} {
		if value := os.Getenv(name); value != "" {
			paths = append(paths, value)
		}
	}
	return paths
}

func t3CodeOpenCodeIsolation(root string, second bool, normalPaths []string) ([]string, error) {
	home, _ := t3CodeOpenCodePaths(root, second)
	private, err := t3CodeCanonicalPath(home)
	if err != nil {
		return nil, errors.New("Cannot resolve the private T3 OpenCode account directory.")
	}
	identities := []string{private}
	for _, path := range normalPaths {
		path = filepath.Clean(path)
		if !packageUpdateAbsolute(path) || strings.ContainsAny(path, "\x00\r\n") {
			return nil, errors.New("Normal OpenCode configuration paths must be absolute before preparing its Kilo account.")
		}
		normal, err := t3CodeCanonicalPath(path)
		if err != nil {
			return nil, errors.New("Cannot resolve a normal OpenCode configuration directory.")
		}
		if withinT3CodePath(normal, private) || withinT3CodePath(private, normal) {
			return nil, errors.New("The private T3 OpenCode account must be separate from the normal OpenCode configuration and data.")
		}
		identities = append(identities, normal)
	}
	return identities, nil
}

// These are OpenCode's own provider models. T3 discovers them over the native
// driver, including the kilo-local prefix; gateway model IDs stay unchanged.
func t3CodeOpenCodePickerLibrary(library modelLibrary) modelLibrary {
	result := cloneModelLibrary(library)
	if result.DefaultModel != "" {
		result.DefaultModel = "kilo-local/" + result.DefaultModel
	}
	for i := range result.Models {
		result.Models[i].ID = "kilo-local/" + result.Models[i].ID
	}
	return result
}

func t3CodeOpenCodeConfig(options t3CodeProfileOptions) ([]byte, error) {
	return t3CodeOpenCodeConfigWithKey(options, t3CodeOpenCodeKey)
}

func t3CodeOpenCodeConfigWithKey(options t3CodeProfileOptions, keyName string) ([]byte, error) {
	if err := validateModelLibrary(options.Library); err != nil || len(options.Library.Models) == 0 {
		return nil, errors.New("Choose shared models before preparing OpenCode with Kilo Proxy.")
	}
	if options.Port < 1024 || options.Port > 65535 {
		return nil, errors.New("OpenCode requires a valid local Kilo Proxy port.")
	}
	prefs := normalizeT3CodePreferences(*options.Preferences)
	baseURL := "http://127.0.0.1:" + strconv.Itoa(options.Port) + "/v1"
	choices := terminalLibraryChoices(options.Library, options.Catalog)
	var config map[string]any
	if !prefs.OpenCode2 {
		selection := editorSelection{Initial: options.Library.DefaultModel}
		for _, choice := range choices {
			limits, err := contextPolicyForChoice(choice)
			if err != nil {
				return nil, err
			}
			levels, initial := nativeReasoningFor(choice)
			selection.Models = append(selection.Models, editorModel{ID: choice.Model.ID, Name: nativeCodexDisplayName(choice), Context: limits.ContextWindow, Output: limits.MaxOutputTokens, Variants: levels, ReasoningEffort: initial})
		}
		data, err := mergeEditorSettings(nil, "opencode", selection, baseURL, "{env:"+keyName+"}")
		if err != nil || json.Unmarshal(data, &config) != nil {
			return nil, errors.New("Cannot build the private T3 OpenCode configuration.")
		}
		config["enabled_providers"] = []string{"kilo-local"}
		config["autoupdate"] = false
	} else {
		models := map[string]any{}
		for _, choice := range choices {
			limits, err := contextPolicyForChoice(choice)
			if err != nil {
				return nil, err
			}
			name := nativeCodexDisplayName(choice)
			key := "{env:" + keyName + "}"
			body := map[string]any{"model": choice.Model.ID}
			model := map[string]any{"modelID": choice.Model.ID, "name": name,
				"package":  "@opencode/ai/providers/openai-compatible",
				"settings": map[string]any{"baseURL": baseURL, "apiKey": key},
				"headers":  map[string]string{"Authorization": "Bearer " + key},
				"body":     body}
			if capabilities := t3CodeOpenCodeCapabilities(choice.Model); capabilities != nil {
				model["capabilities"] = capabilities
			}
			variants := []map[string]any{}
			levels, initial := nativeReasoningFor(choice)
			for _, level := range levels {
				variants = append(variants, map[string]any{"id": level,
					"settings": map[string]any{"baseURL": baseURL, "apiKey": key},
					"headers":  map[string]string{"Authorization": "Bearer " + key},
					"body":     map[string]any{"model": choice.Model.ID, "reasoning_effort": level}})
			}
			model["variants"] = variants
			limit := map[string]int{}
			if limits.ContextWindow > 0 {
				limit["context"] = limits.ContextWindow
			}
			if limits.MaxOutputTokens > 0 {
				limit["output"] = limits.MaxOutputTokens
			}
			if len(limit) > 0 {
				model["limit"] = limit
			}
			if initial != "" && !helperContains(levels, choice.DefaultReasoning) {
				body["reasoning_effort"] = initial
			} else if choice.DefaultReasoning != "" {
				body["reasoning_effort"] = choice.DefaultReasoning
			}
			models[choice.Model.ID] = model
		}
		config = map[string]any{"$schema": "https://opencode.ai/config.json", "model": "kilo-local/" + options.Library.DefaultModel,
			"experimental": map[string]any{"policies": []map[string]string{
				{"effect": "deny", "action": "provider.use", "resource": "*"},
				{"effect": "allow", "action": "provider.use", "resource": "kilo-local"},
			}},
			"update": "disable", "providers": map[string]any{"kilo-local": map[string]any{
				"name": "Kilo Proxy", "env": []string{keyName}, "package": "@opencode/ai/providers/openai-compatible",
				"settings": map[string]any{"baseURL": baseURL}, "models": models,
			}}}
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil || len(data) > catalogLimit {
		return nil, errors.New("The private T3 OpenCode configuration exceeds the size limit.")
	}
	return append(data, '\n'), nil
}

// OpenCode 2 requires all three fields when capabilities are supplied. Unknown
// catalog modalities must not turn a partial tools flag into invented support.
func t3CodeOpenCodeCapabilities(model modelInfo) map[string]any {
	if model.Tools == nil || len(model.InputModalities) == 0 || len(model.OutputModalities) == 0 {
		return nil
	}
	valid := func(values []string) bool {
		for _, value := range values {
			if value == "" || strings.ContainsAny(value, "\x00\r\n") {
				return false
			}
		}
		return true
	}
	if !valid(model.InputModalities) || !valid(model.OutputModalities) {
		return nil
	}
	return map[string]any{"tools": *model.Tools, "input": append([]string(nil), model.InputModalities...), "output": append([]string(nil), model.OutputModalities...)}
}

// Pure planning: no discovery, process, directory creation or credential read.
// The caller commits this profile with T3 settings, its redacted environment
// secret and the preparation record in one existing file transaction.
func planT3CodeOpenCode(options t3CodeProfileOptions) (map[string]any, string, []byte, error) {
	return planT3CodeOpenCodeWithKey(options, t3CodeOpenCodeKey)
}

func planT3CodeOpenCodeWithKey(options t3CodeProfileOptions, keyName string) (map[string]any, string, []byte, error) {
	native, exists := options.AdditionalProviders["opencode"].(map[string]any)
	if !exists || !options.OpenCodeKiloSupported {
		return nil, "", nil, nil
	}
	provider := map[string]any{"driver": "opencode", "displayName": "Kilo Proxy · OpenCode", "enabled": false,
		"config": native["config"], "environment": []map[string]any{}, "accentColor": t3CodeProxyAccentColor}
	if options.Preferences == nil || !t3CodeOpenCodeKiloEnabled(*options.Preferences) {
		return provider, "", nil, nil
	}
	if options.LocalKey == "" || strings.ContainsAny(options.LocalKey, "\x00\r\n") || !packageUpdateAbsolute(options.RootDir) {
		return nil, "", nil, errors.New("OpenCode requires a valid private T3 profile and local proxy key.")
	}
	prefs := normalizeT3CodePreferences(*options.Preferences)
	home, path := t3CodeOpenCodePaths(options.RootDir, prefs.OpenCode2)
	if _, err := t3CodeOpenCodeIsolation(options.RootDir, prefs.OpenCode2, options.NormalOpenCodePaths); err != nil {
		return nil, "", nil, err
	}
	data, err := t3CodeOpenCodeConfigWithKey(options, keyName)
	if err != nil {
		return nil, "", nil, err
	}
	environment := t3CodeOptionalProviderEnvironment(home, nil, options.Platform)
	for _, entry := range []struct{ name, value string }{{"OPENCODE_CONFIG", path}, {"OPENCODE_CONFIG_DIR", filepath.Dir(path)}, {"OPENCODE_CONFIG_CONTENT", string(data)}} {
		environment = append(environment, map[string]any{"name": entry.name, "value": entry.value, "sensitive": false})
	}
	environment = append(environment, map[string]any{"name": keyName, "value": options.LocalKey, "sensitive": true})
	provider["environment"], provider["enabled"] = environment, true
	return provider, path, data, nil
}

func addT3CodeOpenCodeProfile(options t3CodeProfileOptions, result *t3CodeProfiles) error {
	provider, path, data, err := planT3CodeOpenCode(options)
	if err != nil || provider == nil {
		return err
	}
	result.Providers[t3CodeOpenCodeProxyID] = provider
	if path == "" {
		return nil
	}
	if err := safeEditorDir(options.RootDir, filepath.Dir(path)); err != nil {
		return err
	}
	file, err := prepareProfileFile(path, data)
	if err != nil {
		return err
	}
	result.Files = append(result.Files, file)
	return nil
}

func redactT3CodeProvider(provider map[string]any) map[string]any {
	result := normalizeT3CodeProviders(map[string]any{"provider": provider})["provider"].(map[string]any)
	for _, item := range result["environment"].([]any) {
		variable := item.(map[string]any)
		if variable["sensitive"] == true {
			variable["value"], variable["valueRedacted"] = "", true
		}
	}
	return result
}

func validateT3CodeOpenCodeManifest(saved t3CodePrepared, paths t3CodeManagedPaths) error {
	enabled := t3CodeOpenCodeKiloEnabled(saved.Preferences)
	secret := filepath.Join("data", "userdata", "secrets", t3CodeSecretName(t3CodeOpenCodeProxyID, t3CodeOpenCodeKey))
	expected := map[string]bool{secret: enabled}
	for _, second := range []bool{false, true} {
		_, config := t3CodeOpenCodePaths(paths.Root, second)
		relative, err := filepath.Rel(paths.Root, config)
		if err != nil {
			return err
		}
		expected[relative] = enabled && saved.Preferences.OpenCode2 == second
	}
	for relative, required := range expected {
		digest, exists := saved.Files[relative]
		if required && (!exists || len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "") || !required && exists {
			return errors.New("Prepare the selected T3 OpenCode account again.")
		}
	}
	count := 0
	if saved.Preferences.KiloCodex {
		count += 3
	}
	if saved.Preferences.KiloClaude {
		count += 3
	}
	if saved.Preferences.LiteLLMCodex {
		count += 3
	}
	if saved.Preferences.LiteLLMClaude {
		count += 3
	}
	if enabled {
		count += 2
	}
	if t3CodeMuseKiloEnabled(saved.Preferences) {
		count += 3
	}
	if len(saved.Files) != count {
		return errors.New("The T3 profile integrity manifest contains unselected account files.")
	}
	if native, ok := saved.Providers["opencode"].(map[string]any); ok {
		selected := false
		for _, id := range saved.Preferences.Providers {
			selected = selected || id == "opencode"
		}
		kilo, ok := saved.Providers[t3CodeOpenCodeProxyID].(map[string]any)
		if native["driver"] != "opencode" || native["enabled"] != (selected && !enabled) || !ok && enabled || ok && (kilo["driver"] != "opencode" || kilo["enabled"] != enabled) {
			return errors.New("Invalid private T3 OpenCode account selection.")
		}
	} else if enabled || saved.Providers[t3CodeOpenCodeProxyID] != nil {
		return errors.New("The selected T3 OpenCode driver is unavailable.")
	}
	return nil
}

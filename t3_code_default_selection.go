package main

import "strings"

// Only Kilo instances have a fixed prepared model policy. Direct and optional
// instances retain T3's own native catalog and stored model options.
func t3CodeDefaultSelection(current any, providers map[string]any, policy t3CodeModelPolicy) any {
	enabled := func(id string) bool {
		provider, ok := providers[id].(map[string]any)
		if !ok {
			return false
		}
		value, exists := provider["enabled"]
		return !exists || value == true
	}
	contains := func(library modelLibrary, id string) bool {
		for _, model := range library.Models {
			if model.ID == id {
				return true
			}
		}
		return false
	}
	claudePicker := t3CodeClaudePickerLibrary(policy.ClaudeLibrary, policy.ClaudeRoutes)
	openCodePicker := t3CodeOpenCodePickerLibrary(policy.OpenCodeLibrary)
	if selected, ok := current.(map[string]any); ok {
		id, _ := selected["instanceId"].(string)
		model, _ := selected["model"].(string)
		if enabled(id) && model != "" {
			switch id {
			case t3CodeOpenCodeProxyID:
				if contains(openCodePicker, model) {
					return t3CodeOpenCodeSelectionVariants(selected, policy.OpenCodeVariantLevels)
				}
			case t3CodeMuseProxyID:
				if contains(policy.MuseLibrary, model) {
					return t3CodeMuseSelectionReasoning(selected, policy.MuseReasoningLevels, policy.MuseReasoningDefaults)
				}
			case t3CodeLiteLLMCodexID:
				if contains(policy.LiteLLMCodexLibrary, model) {
					return current
				}
				return map[string]any{"instanceId": id, "model": policy.LiteLLMCodexLibrary.DefaultModel}
			case t3CodeLiteLLMClaudeID:
				if contains(policy.LiteLLMClaudeLibrary, model) {
					return current
				}
				return map[string]any{"instanceId": id, "model": policy.LiteLLMClaudeLibrary.DefaultModel}
			case t3CodeCodexProxyID:
				if contains(policy.CodexLibrary, model) {
					return current
				}
			case t3CodeClaudeProxyID:
				if contains(claudePicker, model) {
					return current
				}
				// A previous profile could store the exact gateway ID. Retain the
				// account and turn options while upgrading its native picker identity.
				target := policy.ClaudeRoutes[model]
				if target == "" && contains(policy.ClaudeLibrary, model) {
					target = model
				}
				native := t3CodeNativeClaudeModelID(target)
				if native != "" && contains(claudePicker, native) {
					result := map[string]any{}
					for key, value := range selected {
						result[key] = value
					}
					result["model"] = native
					return result
				}
			default:
				return current
			}
		}
	}
	for _, candidate := range []struct {
		id      string
		library modelLibrary
	}{{t3CodeCodexProxyID, policy.CodexLibrary}, {t3CodeClaudeProxyID, claudePicker}, {t3CodeOpenCodeProxyID, openCodePicker}, {t3CodeMuseProxyID, policy.MuseLibrary}, {t3CodeLiteLLMCodexID, policy.LiteLLMCodexLibrary}, {t3CodeLiteLLMClaudeID, policy.LiteLLMClaudeLibrary}} {
		if enabled(candidate.id) && len(candidate.library.Models) > 0 {
			selected := map[string]any{"instanceId": candidate.id, "model": candidate.library.DefaultModel}
			if candidate.id == t3CodeMuseProxyID {
				return t3CodeMuseSelectionReasoning(selected, policy.MuseReasoningLevels, policy.MuseReasoningDefaults)
			}
			return selected
		}
	}
	// Null delegates direct/optional model choice to T3's enabled-instance
	// resolver and native driver defaults. It also represents all accounts off.
	return nil
}

// Only remove stale OpenCode variant values, preserving the account, model,
// other options and every conversation. T3 accepts canonical option arrays and
// its legacy object shape; both can be retained by previous settings.
func t3CodeOpenCodeSelectionVariants(selected map[string]any, levels map[string][]string) any {
	model, _ := selected["model"].(string)
	allowed := levels[strings.TrimPrefix(model, "kilo-local/")]
	valid := func(value any) bool {
		variant, ok := value.(string)
		return ok && helperContains(allowed, variant)
	}
	var next any
	switch options := selected["options"].(type) {
	case map[string]any:
		value, exists := options["variant"]
		if !exists || valid(value) {
			return selected
		}
		copy := map[string]any{}
		for name, value := range options {
			if name != "variant" {
				copy[name] = value
			}
		}
		next = copy
	case []any:
		copy := []any{}
		changed := false
		for _, raw := range options {
			option, ok := raw.(map[string]any)
			if ok && option["id"] == "variant" && !valid(option["value"]) {
				changed = true
				continue
			}
			copy = append(copy, raw)
		}
		if !changed {
			return selected
		}
		next = copy
	default:
		return selected
	}
	result := map[string]any{}
	for key, value := range selected {
		result[key] = value
	}
	result["options"] = next
	return result
}

// Remove only stale Muse reasoning choices. Other options, native accounts and
// every conversation remain untouched; valid saved per-turn choices persist.
func t3CodeMuseSelectionReasoning(selected map[string]any, levels map[string][]string, defaults map[string]string) any {
	model, _ := selected["model"].(string)
	allowed := levels[model]
	initial := defaults[model]
	if !helperContains(allowed, initial) {
		initial = ""
	}
	valid := func(value any) bool { effort, ok := value.(string); return ok && helperContains(allowed, effort) }
	var next any
	switch options := selected["options"].(type) {
	case map[string]any:
		value, exists := options["reasoningEffort"]
		if (exists && valid(value)) || (!exists && initial == "") {
			return selected
		}
		copy := map[string]any{}
		for key, value := range options {
			if key != "reasoningEffort" {
				copy[key] = value
			}
		}
		if initial != "" {
			copy["reasoningEffort"] = initial
		}
		next = copy
	case []any:
		copy := []any{}
		changed, kept := false, false
		for _, raw := range options {
			option, ok := raw.(map[string]any)
			if ok && option["id"] == "reasoningEffort" {
				if !valid(option["value"]) {
					changed = true
					continue
				}
				kept = true
			}
			copy = append(copy, raw)
		}
		if !kept && initial != "" {
			copy = append(copy, map[string]any{"id": "reasoningEffort", "value": initial})
			changed = true
		}
		if !changed {
			return selected
		}
		next = copy
	case nil:
		if initial == "" {
			return selected
		}
		next = []any{map[string]any{"id": "reasoningEffort", "value": initial}}
	default:
		return selected
	}
	result := map[string]any{}
	for key, value := range selected {
		result[key] = value
	}
	result["options"] = next
	return result
}

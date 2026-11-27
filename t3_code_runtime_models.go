package main

func t3CodeRuntimeOpenCodeVariants(policy t3CodeModelPolicy) map[string][]string {
	result := map[string][]string{}
	for id, levels := range policy.OpenCodeVariantLevels {
		result["kilo-local/"+id] = append([]string{}, levels...)
	}
	return result
}

func t3CodeRuntimeModelAllowlist(policy t3CodeModelPolicy) map[string][]string {
	result := map[string][]string{t3CodeCodexProxyID: {}, t3CodeClaudeProxyID: {}}
	for _, model := range policy.CodexLibrary.Models {
		result[t3CodeCodexProxyID] = append(result[t3CodeCodexProxyID], model.ID)
	}
	for _, model := range t3CodeClaudePickerLibrary(policy.ClaudeLibrary, policy.ClaudeRoutes).Models {
		result[t3CodeClaudeProxyID] = append(result[t3CodeClaudeProxyID], model.ID)
	}
	for _, model := range policy.LiteLLMCodexLibrary.Models {
		result[t3CodeLiteLLMCodexID] = append(result[t3CodeLiteLLMCodexID], model.ID)
	}
	for _, model := range policy.LiteLLMClaudeLibrary.Models {
		result[t3CodeLiteLLMClaudeID] = append(result[t3CodeLiteLLMClaudeID], model.ID)
	}
	if len(policy.OpenCodeLibrary.Models) > 0 {
		for _, model := range t3CodeOpenCodePickerLibrary(policy.OpenCodeLibrary).Models {
			result[t3CodeOpenCodeProxyID] = append(result[t3CodeOpenCodeProxyID], model.ID)
		}
	}
	if len(policy.MuseLibrary.Models) > 0 {
		for _, model := range policy.MuseLibrary.Models {
			result[t3CodeMuseProxyID] = append(result[t3CodeMuseProxyID], model.ID)
		}
	}
	normalized, _ := normalizeT3CodeRuntimeModelAllowlist(result)
	return normalized
}

func t3CodeRuntimeModelDefaults(policy t3CodeModelPolicy) map[string]string {
	result := map[string]string{}
	if len(policy.CodexLibrary.Models) > 0 {
		result[t3CodeCodexProxyID] = policy.CodexLibrary.DefaultModel
	}
	claude := t3CodeClaudePickerLibrary(policy.ClaudeLibrary, policy.ClaudeRoutes)
	if len(claude.Models) > 0 {
		result[t3CodeClaudeProxyID] = claude.DefaultModel
	}
	if len(policy.LiteLLMCodexLibrary.Models) > 0 {
		result[t3CodeLiteLLMCodexID] = policy.LiteLLMCodexLibrary.DefaultModel
	}
	if len(policy.LiteLLMClaudeLibrary.Models) > 0 {
		result[t3CodeLiteLLMClaudeID] = policy.LiteLLMClaudeLibrary.DefaultModel
	}
	if len(policy.OpenCodeLibrary.Models) > 0 {
		result[t3CodeOpenCodeProxyID] = "kilo-local/" + policy.OpenCodeLibrary.DefaultModel
	}
	if len(policy.MuseLibrary.Models) > 0 {
		result[t3CodeMuseProxyID] = policy.MuseLibrary.DefaultModel
	}
	return result
}

func t3CodeRuntimeMusePolicy(policy t3CodeModelPolicy) t3CodeMuseRuntimePolicy {
	result := t3CodeMuseRuntimePolicy{Levels: map[string][]string{}, Defaults: map[string]string{}}
	for id, levels := range policy.MuseReasoningLevels {
		result.Levels[id] = append([]string{}, levels...)
	}
	for id, initial := range policy.MuseReasoningDefaults {
		result.Defaults[id] = initial
	}
	return result
}

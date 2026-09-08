package main

import (
	"regexp"
	"sort"
	"strings"
)

type modelLabOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

var modelLabNames = map[string]string{
	"openai": "OpenAI", "anthropic": "Anthropic", "google": "Google", "deepseek": "DeepSeek",
	"meta-llama": "Meta", "meta": "Meta", "mistralai": "Mistral AI", "mistral": "Mistral AI",
	"qwen": "Qwen", "x-ai": "xAI", "xai": "xAI", "z-ai": "Z.ai", "zai": "Z.ai",
	"minimax": "MiniMax", "moonshotai": "Moonshot AI", "moonshot": "Moonshot AI",
}

var modelLabSeparator = regexp.MustCompile(`[-_]+`)
var modelLabWordStart = regexp.MustCompile(`\b\w`)

func normalizeModelLab(value string) string {
	return strings.ToLower(strings.TrimLeft(strings.TrimSpace(value), "~"))
}

// A lab is the gateway's publisher namespace. Normalization only affects the
// filter; exact model IDs and the upstream routing remain unchanged.
func modelLab(model modelInfo) string {
	provider := strings.TrimSpace(model.Provider)
	if provider == "" {
		provider = strings.SplitN(model.ID, "/", 2)[0]
	}
	return normalizeModelLab(provider)
}

func modelLabLabel(value string) string {
	if label := modelLabNames[value]; label != "" {
		return label
	}
	label := strings.TrimSpace(modelLabSeparator.ReplaceAllString(value, " "))
	if label == "" {
		label = value
	}
	return modelLabWordStart.ReplaceAllStringFunc(label, strings.ToUpper)
}

func modelLabOptions(models []modelInfo, language string) []modelLabOption {
	seen := map[string]bool{}
	labs := []modelLabOption{}
	for _, model := range models {
		value := modelLab(model)
		if value != "" && !seen[value] {
			seen[value] = true
			labs = append(labs, modelLabOption{value, modelLabLabel(value)})
		}
	}
	sort.Slice(labs, func(i, j int) bool {
		a, b := strings.ToLower(labs[i].Label), strings.ToLower(labs[j].Label)
		if a == b {
			return labs[i].Value < labs[j].Value
		}
		return a < b
	})
	all := "All labs"
	if language == "es" {
		all = "Todos los laboratorios"
	}
	return append([]modelLabOption{{Value: "", Label: all}}, labs...)
}

func filterModelLab(models []modelInfo, lab string) []modelInfo {
	key := normalizeModelLab(lab)
	out := make([]modelInfo, 0, len(models))
	for _, model := range models {
		if key == "" || modelLab(model) == key {
			out = append(out, model)
		}
	}
	return out
}

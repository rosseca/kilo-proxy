package main

import (
	"encoding/json"
	"strconv"
	"strings"
)

func t3CodeFallbackLibrary(selected, fallback modelLibrary) modelLibrary {
	if len(selected.Models) == 0 {
		return fallback
	}
	return selected
}

func t3CodeModelSummaries(library modelLibrary, catalog []modelInfo) []t3CodeModelSummary {
	models := []t3CodeModelSummary{}
	for _, choice := range terminalLibraryChoices(library, catalog) {
		models = append(models, t3CodeModelSummary{ID: choice.Model.ID, Name: nativeCodexDisplayName(choice), ReasoningEffort: choice.DefaultReasoning})
	}
	return models
}

// T3 owns the native Claude selection and its effort controls. Preserve that
// identity in the CLI; only the scoped HTTP adapter translates it to Kilo.
func t3CodeNativeClaudeSettings(data []byte, policy t3CodeModelPolicy, initial string, port int) ([]byte, error) {
	settings, err := decodeClaudeSettings(data)
	if err != nil {
		return nil, err
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(settings["env"], &env); err != nil {
		return nil, err
	}
	for name := range env {
		if strings.HasPrefix(name, "ANTHROPIC_DEFAULT_") {
			delete(env, name)
		}
	}
	set := func(name, value string) { env[name], _ = json.Marshal(value) }
	set("ANTHROPIC_BASE_URL", "http://127.0.0.1:"+strconv.Itoa(port)+"/t3-code")
	for _, family := range []string{"opus", "sonnet", "fable", "haiku"} {
		// The first selected model in each family is deterministic. Every
		// version-qualified slug still routes through its exact prepared ID.
		for _, model := range policy.ClaudeLibrary.Models {
			native := t3CodeNativeClaudeModelID(model.ID)
			if strings.HasPrefix(native, "claude-"+family+"-") {
				set("ANTHROPIC_DEFAULT_"+strings.ToUpper(family)+"_MODEL", native)
				break
			}
		}
	}
	initial = t3CodeClaudePickerModelID(initial, policy.ClaudeRoutes)
	set("ANTHROPIC_MODEL", initial)
	settings["model"], _ = json.Marshal(initial)
	settings["env"], _ = json.Marshal(env)
	encoded, err := json.MarshalIndent(settings, "", "  ")
	return append(encoded, '\n'), err
}

func t3CodeClaudePickerModelID(id string, routes map[string]string) string {
	if native := t3CodeNativeClaudeModelID(id); native != "" && routes[native] == id {
		return native
	}
	return id
}

func t3CodeClaudePickerLibrary(library modelLibrary, routes map[string]string) modelLibrary {
	result := cloneModelLibrary(library)
	for i := range result.Models {
		id := result.Models[i].ID
		result.Models[i].ID = t3CodeClaudePickerModelID(id, routes)
		if result.DefaultModel == id {
			result.DefaultModel = result.Models[i].ID
		}
	}
	// Roles are informational and keyed on shared IDs, not picker identities.
	result.Roles = nil
	return result
}

package main

import (
	"errors"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Desktop gates its composer dictation on ChatGPT auth. The provider's env_key
// still authenticates inference; this flag exposes the separate ChatGPT login.
func mergeCodexChatGPTDictation(data []byte, enabled bool) ([]byte, error) {
	var before, after map[string]any
	if toml.Unmarshal(data, &before) != nil || toml.Unmarshal(data, &after) != nil {
		return nil, errors.New("Invalid Codex TOML; dictation settings were not saved")
	}
	if after == nil {
		after = map[string]any{}
	}
	providers, err := configTable(after, "model_providers")
	if err != nil {
		return nil, err
	}
	provider, err := configTable(providers, "kilo-local")
	if err != nil {
		return nil, err
	}
	provider["requires_openai_auth"] = enabled
	return editCodexTOML(data, before, after)
}

func codexChatGPTDictationFromConfig(dir string) bool {
	if dir == "" {
		return false
	}
	data, err := readCatalogFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		return false
	}
	var config map[string]any
	if toml.Unmarshal(data, &config) != nil {
		return false
	}
	value, _ := tomlAt(config, []string{"model_providers", "kilo-local", "requires_openai_auth"})
	return value == true
}

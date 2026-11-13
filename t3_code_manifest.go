package main

import (
	"encoding/json"
	"path/filepath"
	"regexp"
)

// T3 can refresh its public model manifest without changing its Desktop
// version. Include cached Claude rows when preparing its private picker.
// This cache is optional; the packaged manifests remain the baseline.
func t3CodeCachedClaudeSlugs(paths t3CodeManagedPaths) []string {
	data, err := readCatalogFile(filepath.Join(paths.Data, "userdata", "model-manifest.json"))
	if err != nil {
		return nil
	}
	var cache struct {
		Manifest struct {
			CurrentModels struct {
				Claude []string `json:"claudeAgent"`
			} `json:"currentModels"`
			Providers struct {
				Claude struct {
					Models []struct {
						Slug string `json:"slug"`
					} `json:"models"`
				} `json:"claudeAgent"`
			} `json:"providers"`
		} `json:"manifest"`
	}
	if json.Unmarshal(data, &cache) != nil {
		return nil
	}
	models := cache.Manifest.CurrentModels.Claude
	for _, model := range cache.Manifest.Providers.Claude.Models {
		models = append(models, model.Slug)
	}
	return models
}

// Recent builds embed this JSON manifest as a JavaScript object literal. Read
// only the bounded manifest and the provider's declared rows, never run JS
// or infer membership from arbitrary strings elsewhere in the bundle.
var t3CodeBundledModelManifest = regexp.MustCompile("(?m)^// src/provider/model-manifest\\.json\r?\n(?:var|const) (?:model_manifest_default|modelManifest_default) = (\\{(?s:.*?)^\\});\r?$")
var t3CodeManifestBareKey = regexp.MustCompile("(?m)^([ \t]*)([A-Za-z_][A-Za-z_0-9]*)[ \t]*:")

func t3CodeBundledClaudeSlugs(server []byte) []string {
	matches := t3CodeBundledModelManifest.FindAllSubmatchIndex(server, 2)
	if len(matches) != 1 {
		return nil
	}
	start, end := matches[0][2], matches[0][3]
	if end-start > 512<<10 {
		return nil
	}
	data := t3CodeManifestBareKey.ReplaceAll(server[start:end], []byte(`${1}"${2}":`))
	var manifest struct {
		Version   int `json:"version"`
		Providers struct {
			Claude struct {
				Models []struct {
					Slug string `json:"slug"`
				} `json:"models"`
			} `json:"claudeAgent"`
		} `json:"providers"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Version != 1 {
		return nil
	}
	var slugs []string
	seen := map[string]bool{}
	for _, model := range manifest.Providers.Claude.Models {
		if model.Slug == "" || seen[model.Slug] || t3CodeNativeClaudeModelID("anthropic/"+model.Slug) != model.Slug {
			continue
		}
		seen[model.Slug] = true
		slugs = append(slugs, model.Slug)
	}
	return slugs
}

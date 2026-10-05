package main

import (
	"encoding/json"
	"path/filepath"
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

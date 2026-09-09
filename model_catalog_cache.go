package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

type nativeCachedCatalog struct {
	SchemaVersion int         `json:"schemaVersion"`
	OrgID         string      `json:"orgId"`
	Models        []modelInfo `json:"models"`
}

// Shared by the native window and terminal profile preparation. Metadata is
// optional and scoped to the current organization; it never replaces choices.
func readNativeCatalogCache(dir, org string) []modelInfo {
	path := filepath.Join(dir, "model-catalog.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil || len(b) > 4<<20 {
		return nil
	}
	var cache nativeCachedCatalog
	if json.Unmarshal(b, &cache) != nil || cache.SchemaVersion != 1 || cache.OrgID != org || len(cache.Models) > 10000 {
		return nil
	}
	for _, m := range cache.Models {
		if !catalogID.MatchString(m.ID) {
			return nil
		}
	}
	return cache.Models
}

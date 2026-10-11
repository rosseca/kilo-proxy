package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

const modelCatalogTTL = time.Hour

type nativeCachedCatalog struct {
	SchemaVersion int         `json:"schemaVersion"`
	OrgID         string      `json:"orgId"`
	Models        []modelInfo `json:"models"`
	FetchedAt     time.Time   `json:"fetchedAt,omitempty"`
}

func modelCatalogNeedsRefresh(fetchedAt, now time.Time) bool {
	// Older caches have no date. A future date can result from a clock change;
	// neither should prevent the user from discovering new models.
	return fetchedAt.IsZero() || fetchedAt.After(now) || now.Sub(fetchedAt) >= modelCatalogTTL
}

// Shared by the native window and terminal profile preparation. Metadata is
// optional and scoped to the current organization; it never replaces choices.
func readNativeCatalogCache(dir, org string) []modelInfo {
	return readNativeCachedCatalog(dir, org).Models
}

func readNativeCachedCatalog(dir, org string) nativeCachedCatalog {
	path := filepath.Join(dir, "model-catalog.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return nativeCachedCatalog{}
	}
	f, err := os.Open(path)
	if err != nil {
		return nativeCachedCatalog{}
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil || len(b) > 4<<20 {
		return nativeCachedCatalog{}
	}
	var cache nativeCachedCatalog
	if json.Unmarshal(b, &cache) != nil || cache.SchemaVersion != 1 || cache.OrgID != org || len(cache.Models) > 10000 {
		return nativeCachedCatalog{}
	}
	for _, m := range cache.Models {
		if !catalogID.MatchString(m.ID) {
			return nativeCachedCatalog{}
		}
	}
	return cache
}

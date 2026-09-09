//go:build desktop

package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Catalog data is a disposable cache, not a source of user selections. Scoping
// it to the team prevents a saved catalog being mistaken for another team's.
type nativeCatalogCache struct {
	mu      sync.Mutex
	written uint64
}
type nativeCachedCatalog struct {
	SchemaVersion int         `json:"schemaVersion"`
	OrgID         string      `json:"orgId"`
	Models        []modelInfo `json:"models"`
}

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
func (u *nativeUI) cacheModels(org string) {
	u.owner.mu.Lock()
	dir := u.owner.dir
	u.owner.mu.Unlock()
	b, err := json.Marshal(nativeCachedCatalog{SchemaVersion: 1, OrgID: org, Models: u.models})
	if err != nil || len(b) > 4<<20 {
		return
	}
	u.catalogGeneration++
	generation := u.catalogGeneration
	go func() {
		u.catalogCache.mu.Lock()
		defer u.catalogCache.mu.Unlock()
		select {
		case <-u.owner.quit:
			return
		default:
		}
		if generation <= u.catalogCache.written {
			return
		}
		if os.MkdirAll(dir, 0700) == nil && atomicCatalogFile(filepath.Join(dir, "model-catalog.json"), b) == nil {
			u.catalogCache.written = generation
		}
	}()
}

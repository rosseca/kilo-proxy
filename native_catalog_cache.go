//go:build desktop

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Catalog data is a disposable cache, not a source of user selections. Scoping
// it to the team prevents a saved catalog being mistaken for another team's.
type nativeCatalogCache struct {
	mu      sync.Mutex
	written uint64
}

func (u *nativeUI) cacheModels(org string) {
	u.owner.mu.Lock()
	dir := u.owner.dir
	u.owner.mu.Unlock()
	b, err := json.Marshal(nativeCachedCatalog{SchemaVersion: 1, OrgID: org, Models: u.models, FetchedAt: u.catalogFetchedAt})
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

func (u *nativeUI) openModelCatalog() {
	u.expanded["library.catalog"] = true
	if len(u.models) == 0 || modelCatalogNeedsRefresh(u.catalogFetchedAt, time.Now()) {
		// Keep the last catalog available while fetching. Failed requests do
		// not change its age, so another Add action can retry.
		u.refreshModels()
	}
}

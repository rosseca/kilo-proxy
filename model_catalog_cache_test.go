package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestModelCatalogRefreshAge(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		fetchedAt time.Time
		refresh   bool
	}{
		{"legacy without date", time.Time{}, true},
		{"just fetched", now, false},
		{"less than an hour", now.Add(-time.Hour + time.Nanosecond), false},
		{"one hour", now.Add(-time.Hour), true},
		{"more than an hour", now.Add(-61 * time.Minute), true},
		{"clock moved backwards", now.Add(time.Minute), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := modelCatalogNeedsRefresh(test.fetchedAt, now); got != test.refresh {
				t.Fatalf("refresh = %v, want %v", got, test.refresh)
			}
		})
	}
}

func TestModelCatalogCacheRetainsFetchAgeAndLegacyModels(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for _, legacy := range []bool{false, true} {
		name := "timestamped"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cache := nativeCachedCatalog{SchemaVersion: 1, OrgID: "team-one", Models: []modelInfo{{ID: "vendor/old-model", Name: "Saved model"}}, FetchedAt: now.Add(-2 * time.Hour)}
			data, err := json.Marshal(cache)
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				// This is the schema used by releases before 0.51.3.
				data = []byte(`{"schemaVersion":1,"orgId":"team-one","models":[{"id":"vendor/old-model","name":"Saved model"}]}`)
				cache.FetchedAt = time.Time{}
			}
			if err := atomicCatalogFile(filepath.Join(dir, "model-catalog.json"), data); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				got := readNativeCachedCatalog(dir, "team-one")
				if len(got.Models) != 1 || got.Models[0].ID != cache.Models[0].ID || !got.FetchedAt.Equal(cache.FetchedAt) {
					t.Fatalf("reading changed the saved catalog or its age: %+v", got)
				}
				if !modelCatalogNeedsRefresh(got.FetchedAt, now) {
					t.Fatal("reading an old catalog made it fresh")
				}
			}
			if got := readNativeCatalogCache(dir, "team-one"); len(got) != 1 {
				t.Fatal("terminal profile preparation lost cached models")
			}
			if got := readNativeCachedCatalog(dir, "team-two"); len(got.Models) != 0 || !got.FetchedAt.IsZero() {
				t.Fatal("catalog or freshness leaked to another team")
			}
		})
	}
}

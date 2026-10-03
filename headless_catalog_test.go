package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestHeadlessCatalogRefreshPersistsAndKeepsCacheOnFailure(t *testing.T) {
	a := testApp(t)
	a.apiKey, a.config.OrgID = "synthetic-key", "synthetic-team"
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Error("wrong catalog authentication")
		}
		if fail.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		jsonResponse(w, 200, map[string]any{"data": []any{map[string]any{"id": "vendor/one", "name": "Model one", "context_length": 100000}}})
	}))
	defer server.Close()
	setUpstream(a, server.URL)
	w := adminRequest(a, "headless/catalog", "{}")
	var state headlessCatalogState
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || !state.Complete || state.FetchedAt.IsZero() || len(state.Models) != 1 {
		t.Fatalf("refresh failed: status %d", w.Code)
	}
	path := filepath.Join(a.dir, "model-catalog.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	w = adminRequest(a, "headless/catalog", "{}")
	if w.Code != 502 {
		t.Fatalf("failed provider returned %d", w.Code)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("failed refresh changed cache")
	}
	w = adminRequest(a, "headless/catalog", "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || len(state.Models) != 1 || !state.Complete {
		t.Fatal("persisted catalog lost")
	}
	a.config.OrgID = "another-team"
	w = adminRequest(a, "headless/catalog", "")
	if json.Unmarshal(w.Body.Bytes(), &state) != nil || len(state.Models) != 0 || state.Complete {
		t.Fatal("cache crossed account scope")
	}
}

func TestHeadlessCatalogPartialKeepsOtherProviderAndExpires(t *testing.T) {
	a := subscriptionTestApp(t)
	a.apiKey, a.config.OrgID = "synthetic-key", "synthetic-team"
	var failed atomic.Bool
	kilo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failed.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		jsonResponse(w, 200, map[string]any{"data": []any{map[string]any{"id": "vendor/one", "name": "Kilo model"}}})
	}))
	defer kilo.Close()
	subscription := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]any{"models": []any{map[string]any{"slug": "gpt-test", "display_name": "Subscription model"}}})
	}))
	defer subscription.Close()
	setUpstream(a, kilo.URL)
	a.chatgpt.backendURL = subscription.URL
	w := adminRequest(a, "headless/catalog", "{}")
	if w.Code != 200 {
		t.Fatalf("full fetch: %d", w.Code)
	}
	failed.Store(true)
	w = adminRequest(a, "headless/catalog", "{}")
	var state headlessCatalogState
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || state.Complete || !state.FetchedAt.IsZero() || len(state.Models) != 2 {
		t.Fatalf("partial result did not retain cache and invalidate date: %d", w.Code)
	}
	cached := readNativeCachedCatalog(a.dir, a.catalogScopeLocked())
	if !cached.FetchedAt.IsZero() || len(cached.Models) != 2 {
		t.Fatal("partial result marked cache recent")
	}
}

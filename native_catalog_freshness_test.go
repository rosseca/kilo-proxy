//go:build desktop

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func nativeCatalogFreshnessUI(t *testing.T, models http.HandlerFunc) *nativeUI {
	t.Helper()
	u := nativeTestUI(t)
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stats":
			jsonResponse(w, http.StatusOK, map[string]any{"data": []any{}})
			return
		case "/recommended":
			jsonResponse(w, http.StatusOK, map[string]any{"schemaVersion": 1, "models": []any{}})
			return
		}
		if r.URL.Path != "/api/gateway/models" {
			t.Errorf("unexpected catalog request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		models(w, r)
	}))
	t.Cleanup(server.Close)
	u.owner.mu.Lock()
	// Exercise the real admin endpoint, but keep every upstream dependency local.
	setUpstream(u.owner, server.URL)
	u.owner.modelStatsURL = server.URL + "/stats"
	u.owner.recommendedModelsURL = server.URL + "/recommended"
	u.owner.mu.Unlock()
	u.page = "models"
	u.catalogCached = true
	return u
}

func nativeCatalogFreshnessResponse(w http.ResponseWriter) {
	jsonResponse(w, http.StatusOK, map[string]any{"data": []any{
		map[string]any{"id": "vendor/new", "name": "Newly published model", "context_length": 128000, "supported_parameters": []string{"tools"}},
	}})
}

func nativeCatalogFreshnessClick(t *testing.T, u *nativeUI, id string) {
	t.Helper()
	u.clickable(id).Click()
	nativeTestFrame(t, u)
	nativeTestFrame(t, u)
}

func TestNativeCatalogFreshnessAddClick(t *testing.T) {
	for _, test := range []struct {
		name    string
		age     time.Duration
		legacy  bool
		refresh bool
	}{
		{name: "fresh", age: 59 * time.Minute},
		{name: "expired", age: 61 * time.Minute, refresh: true},
		{name: "legacy without timestamp", legacy: true, refresh: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			u := nativeCatalogFreshnessUI(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				nativeCatalogFreshnessResponse(w)
			})
			u.catalogFetchedAt = time.Time{}
			if !test.legacy {
				u.catalogFetchedAt = time.Now().UTC().Add(-test.age)
			}
			previous := u.catalogFetchedAt
			started := time.Now()
			nativeCatalogFreshnessClick(t, u, "primary.models.add")
			if !u.expanded["library.catalog"] {
				t.Fatal("Add models did not open the catalog")
			}
			if !test.refresh {
				if u.busy["POST/api/models"] || calls.Load() != 0 || !u.catalogFetchedAt.Equal(previous) {
					t.Fatal("opening a fresh catalog started a refresh or changed its age")
				}
				return
			}
			nativeTestWait(t, u, func() bool { return calls.Load() == 1 && !u.busy["POST/api/models"] })
			if len(u.models) != 1 || u.models[0].ID != "vendor/new" || u.catalogCached || u.catalogFetchedAt.Before(started) {
				t.Fatal("Add models did not replace the stale catalog with the newly fetched one")
			}
		})
	}
}

func TestNativeCatalogFreshnessFailureRetryAndConcurrentAdd(t *testing.T) {
	var calls atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	gate := make(chan struct{})
	var release sync.Once
	u := nativeCatalogFreshnessUI(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			http.Error(w, "synthetic catalog unavailable", http.StatusServiceUnavailable)
			return
		}
		select {
		case <-gate:
			nativeCatalogFreshnessResponse(w)
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { release.Do(func() { close(gate) }) })
	nativeSeedSharedForTest(t, u, u.models[0], modelInfo{ID: "vendor/retired", Name: "Saved retired model"})
	choice := u.library.selection.choice("vendor/one")
	choice.DisplayName, choice.DefaultReasoning = "My preferred model", "max"
	choice.ReasoningCustom, choice.ReasoningLevels = true, []string{"high", "max"}
	choice.ContextPreset, choice.ContextTokens = contextPresetCustom, 32000
	u.seedClientChoice(sharedModelKey, *choice)
	u.library.selection.Initial = "vendor/one"
	u.persistLibraryEdits()
	u.flushModelLibrary()
	selection := libraryFingerprint(u.modelLibraryValue())
	previousModels := append([]modelInfo(nil), u.models...)
	u.catalogFetchedAt = time.Now().UTC().Add(-2 * time.Hour)
	previousDate := u.catalogFetchedAt

	nativeCatalogFreshnessClick(t, u, "primary.models.add")
	nativeTestWait(t, u, func() bool { return calls.Load() == 1 && !u.busy["POST/api/models"] })
	if !reflect.DeepEqual(u.models, previousModels) || !u.catalogFetchedAt.Equal(previousDate) || !u.catalogCached {
		t.Fatal("failed refresh discarded the saved catalog or renewed its timestamp")
	}
	if libraryFingerprint(u.modelLibraryValue()) != selection {
		t.Fatal("failed refresh changed the saved selection")
	}

	fail.Store(false)
	nativeCatalogFreshnessClick(t, u, "models.done")
	nativeCatalogFreshnessClick(t, u, "primary.models.add")
	nativeTestWait(t, u, func() bool { return calls.Load() == 2 })
	if !reflect.DeepEqual(u.models, previousModels) || !u.catalogFetchedAt.Equal(previousDate) {
		t.Fatal("refresh cleared the visible catalog before receiving a response")
	}
	// Close and reopen through the actual controls while the same fetch is
	// blocked. Repeated Add actions must share the pending request.
	nativeCatalogFreshnessClick(t, u, "models.done")
	nativeCatalogFreshnessClick(t, u, "primary.models.add")
	if !u.busy["POST/api/models"] || calls.Load() != 2 {
		t.Fatal("reopening the catalog duplicated or abandoned the pending refresh")
	}
	release.Do(func() { close(gate) })
	nativeTestWait(t, u, func() bool { return !u.busy["POST/api/models"] })
	nativeTestFrame(t, u)
	if calls.Load() != 2 || len(u.models) != 1 || u.models[0].ID != "vendor/new" || !u.catalogFetchedAt.After(previousDate) {
		t.Fatal("retry failed to publish the new catalog exactly once")
	}
	if libraryFingerprint(u.modelLibraryValue()) != selection {
		t.Fatal("refresh changed selected IDs, names, reasoning, context, or the default")
	}
	u.flushModelLibrary()
	if libraryFingerprint(u.owner.modelLibrary.snapshot().Library) != selection {
		t.Fatal("refresh changed the persisted model selection")
	}
}

func TestNativeCatalogFreshnessPersistsAcrossWindowReload(t *testing.T) {
	var calls atomic.Int32
	u := nativeCatalogFreshnessUI(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		nativeCatalogFreshnessResponse(w)
	})
	nativeSeedSharedForTest(t, u, u.models[0])
	u.catalogFetchedAt = time.Now().UTC().Add(-2 * time.Hour)
	nativeCatalogFreshnessClick(t, u, "primary.models.add")
	nativeTestWait(t, u, func() bool {
		u.catalogCache.mu.Lock()
		defer u.catalogCache.mu.Unlock()
		return calls.Load() == 1 && !u.busy["POST/api/models"] && u.catalogGeneration > 0 && u.catalogCache.written == u.catalogGeneration
	})
	fetched := u.catalogFetchedAt
	saved := readNativeCachedCatalog(u.owner.dir, "e2e-team")
	if !saved.FetchedAt.Equal(fetched) || len(saved.Models) != 1 || saved.Models[0].ID != "vendor/new" {
		t.Fatal("successful refresh did not persist its catalog and original fetch time")
	}
	again := newNativeUI(u.owner, func() {})
	t.Cleanup(again.shutdownModelLibrary)
	nativeTestWait(t, again, func() bool { return again.authenticated })
	again.page = "models"
	if !again.catalogCached || !again.catalogFetchedAt.Equal(fetched) {
		t.Fatal("opening a new window discarded or rejuvenated the cached timestamp")
	}
	nativeCatalogFreshnessClick(t, again, "primary.models.add")
	if again.busy["POST/api/models"] || calls.Load() != 1 || !again.catalogFetchedAt.Equal(fetched) {
		t.Fatal("opening the freshly persisted catalog made an unnecessary request")
	}
}

func TestNativeCatalogFreshnessRejectedRevisionKeepsAge(t *testing.T) {
	u := nativeTestUI(t)
	u.catalogFetchedAt = time.Now().UTC().Add(-2 * time.Hour)
	previousDate, previousGeneration := u.catalogFetchedAt, u.catalogGeneration
	previousModels := append([]modelInfo(nil), u.models...)
	revision := nativeNumber(u.state, "catalogRevision")
	raw, err := json.Marshal(map[string]any{"revision": revision - 1, "models": []modelInfo{{ID: "old-team/model"}}})
	if err != nil {
		t.Fatal(err)
	}
	if u.applyModels(raw, revision-1, "models") {
		t.Fatal("accepted a response from an earlier connection")
	}
	if !u.catalogFetchedAt.Equal(previousDate) || u.catalogGeneration != previousGeneration || !reflect.DeepEqual(u.models, previousModels) {
		t.Fatal("rejected response changed catalog data, freshness, or the pending disk snapshot")
	}
}

func TestNativeCatalogFreshnessPartialRefreshRetriesRecoveredProvider(t *testing.T) {
	var kiloCalls, subscriptionCalls atomic.Int32
	var failed atomic.Bool
	failed.Store(true)
	u := nativeCatalogFreshnessUI(t, func(w http.ResponseWriter, _ *http.Request) {
		kiloCalls.Add(1)
		nativeCatalogFreshnessResponse(w)
	})
	subscription := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/models" {
			t.Errorf("unexpected subscription request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		subscriptionCalls.Add(1)
		if failed.Load() {
			http.Error(w, "synthetic subscription unavailable", http.StatusServiceUnavailable)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"models": []any{map[string]any{"slug": "gpt-new", "display_name": "New subscription model"}}})
	}))
	t.Cleanup(subscription.Close)
	u.owner.mu.Lock()
	u.owner.chatgpt.mu.Lock()
	u.owner.chatgpt.creds = chatGPTCredentials{Access: "synthetic-access", Refresh: "synthetic-refresh", Account: "synthetic-catalog-account", Expires: time.Now().Add(time.Hour).Unix()}
	u.owner.chatgpt.state = chatGPTState{Status: "idle"}
	u.owner.chatgpt.backendURL = subscription.URL
	u.owner.chatgpt.mu.Unlock()
	u.owner.chatGPTLastIdentity = "synthetic-catalog-account"
	scope := u.owner.catalogScopeLocked()
	u.owner.mu.Unlock()
	nativeSeedSharedForTest(t, u, u.models[0])
	selection := libraryFingerprint(u.modelLibraryValue())
	u.catalogFetchedAt = time.Now().UTC().Add(-10 * time.Minute)
	nativeCatalogFreshnessClick(t, u, "primary.models.add")
	if u.busy["POST/api/models"] || kiloCalls.Load() != 0 || subscriptionCalls.Load() != 0 {
		t.Fatal("fresh catalog unexpectedly fetched before a manual refresh")
	}
	nativeCatalogFreshnessClick(t, u, "client:shared:refresh")
	nativeTestWait(t, u, func() bool {
		u.catalogCache.mu.Lock()
		defer u.catalogCache.mu.Unlock()
		return subscriptionCalls.Load() == 1 && !u.busy["POST/api/models"] && u.catalogGeneration > 0 && u.catalogCache.written == u.catalogGeneration
	})
	if len(u.models) != 1 || u.models[0].ID != "vendor/new" || !u.catalogFetchedAt.IsZero() {
		t.Fatal("partial success was discarded or retained the previously fresh timestamp")
	}
	if saved := readNativeCachedCatalog(u.owner.dir, scope); len(saved.Models) != 1 || !saved.FetchedAt.IsZero() {
		t.Fatal("partial catalog was persisted as fresh")
	}

	failed.Store(false)
	nativeCatalogFreshnessClick(t, u, "models.done")
	nativeCatalogFreshnessClick(t, u, "primary.models.add")
	nativeTestWait(t, u, func() bool { return subscriptionCalls.Load() == 2 && !u.busy["POST/api/models"] })
	nativeTestFrame(t, u)
	if len(u.models) != 2 || kiloCalls.Load() != 2 || u.catalogFetchedAt.IsZero() {
		t.Fatal("Add models did not immediately recover the missing provider")
	}
	found := false
	for _, model := range u.models {
		found = found || model.ID == "chatgpt/gpt-new"
	}
	if !found || libraryFingerprint(u.modelLibraryValue()) != selection {
		t.Fatal("provider recovery lost the new model or changed the user's selection")
	}
}

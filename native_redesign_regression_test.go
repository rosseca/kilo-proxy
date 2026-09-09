//go:build desktop

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeShutdownCapturesInFlightFrameBeforeDraining(t *testing.T) {
	u := nativeTestUI(t)
	nativeSeedSharedForTest(t, u, nativeClientModelsForTest()[0])
	u.frameMu.Lock() // A real frame owns this lock while it handles the last input.
	done := make(chan struct{})
	go func() { u.shutdownModelLibrary(); close(done) }()
	select {
	case <-done:
		t.Fatal("shutdown overtook the frame")
	case <-time.After(10 * time.Millisecond):
	}
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Final visible edit")
	u.persistLibraryEdits()
	u.frameMu.Unlock()
	<-done
	got := newModelLibraryStore(u.owner.dir).snapshot().Library
	if len(got.Models) != 1 || got.Models[0].DisplayName != "Final visible edit" {
		t.Fatalf("lost final edit: %+v", got)
	}
	if !u.closing {
		t.Fatal("shutdown did not seal later frames")
	}
}

func TestNativeSharedImagesApplyToEitherCodexLaunch(t *testing.T) {
	u := nativeTestUI(t)
	nativeSeedSharedForTest(t, u, nativeClientModelsForTest()[0])
	gui := u.sharedClientSelection("codex")
	cli := u.sharedClientSelection("codex-cli")
	if gui.ImageGeneration == nil || cli.ImageGeneration == nil {
		t.Fatal("image state not seeded")
	}
	nativeEditClientImages(gui, func(value *imageGenerationSettings) { value.Enabled = true; value.Model = "openai/gpt-5-image" })
	cli = u.sharedClientSelection("codex-cli")
	payload, err := nativeClientPayload("codex-cli", cli)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(payload)
	var exported struct {
		Images imageGenerationSettings `json:"imageGeneration"`
	}
	if json.Unmarshal(b, &exported) != nil || !exported.Images.Enabled || exported.Images.Model != "openai/gpt-5-image" {
		t.Fatalf("CLI ignored visible image settings: %s", b)
	}
	nativeEditClientImages(gui, func(value *imageGenerationSettings) { value.Enabled = false })
	if u.sharedClientSelection("codex-cli").ImageGeneration.Enabled {
		t.Fatal("CLI ignored disabling the shared tool")
	}
}

func TestNativeCatalogCachePreservesReasoningWithoutSavingPricesInLibrary(t *testing.T) {
	u := nativeTestUI(t)
	models := nativeClientModelsForTest()
	nativeSeedSharedForTest(t, u, models[0])
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "reasoning"), "high")
	u.persistLibraryEdits()
	u.flushModelLibrary()
	u.cacheModels("e2e-team")
	nativeTestWait(t, u, func() bool {
		u.catalogCache.mu.Lock()
		defer u.catalogCache.mu.Unlock()
		return u.catalogCache.written == u.catalogGeneration
	})
	cached := readNativeCatalogCache(u.owner.dir, "e2e-team")
	if len(cached) == 0 {
		t.Fatal("missing saved catalog")
	}
	if len(readNativeCatalogCache(u.owner.dir, "other-team")) != 0 {
		t.Fatal("catalog crossed teams")
	}
	again := newNativeUI(u.owner, func() {})
	levels, initial := nativeReasoningFor(again.sharedClientSelection("codex").Models[0])
	if initial != "high" || !helperContains(levels, "high") {
		t.Fatalf("lost published reasoning on restart: %v %s", levels, initial)
	}
	data, err := os.ReadFile(filepath.Join(u.owner.dir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		t.Fatal("invalid library")
	}
	item := raw["models"].([]any)[0].(map[string]any)
	if _, ok := item["inputPrice"]; ok {
		t.Fatal("live price saved in selection")
	}
}

func TestNativeCatalogCallbackRejectsChangedBackendTeam(t *testing.T) {
	u := nativeTestUI(t)
	u.owner.mu.Lock()
	u.owner.config.OrgID = "new-team"
	u.owner.catalogRevision++
	u.owner.mu.Unlock()
	data, _ := json.Marshal(map[string]any{"revision": 0, "models": []modelInfo{{ID: "old-team/private-model", Name: "Old catalog"}}})
	if u.applyModels(data, 0, "models") {
		t.Fatal("old response accepted under new team")
	}
	if len(readNativeCatalogCache(u.owner.dir, "new-team")) > 0 {
		t.Fatal("stale model prices cached under new team")
	}
}

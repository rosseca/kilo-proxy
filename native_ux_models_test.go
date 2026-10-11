//go:build desktop

package main

import (
	"strings"
	"testing"
)

// uxmClickMenuItem opens a Models popover and clicks one of its items, as a
// pointer would: the item exists only while its menu is open.
func uxmClickMenuItem(t *testing.T, u *nativeUI, menuID, itemID string) {
	t.Helper()
	u.uxmOpenMenu(menuID, "")
	nativeTestFrame(t, u)
	u.clickable(itemID).Click()
	nativeTestFrame(t, u)
}

// uxmShowLibrarySearch shows the saved-model search for a short test list.
func uxmShowLibrarySearch(t *testing.T) {
	previous := uxmLibrarySearchMinimum
	uxmLibrarySearchMinimum = 1
	t.Cleanup(func() { uxmLibrarySearchMinimum = previous })
}

// uxmPackCatalogForTest adds every pack model (with prices) to a catalog.
func uxmPackCatalogForTest(base []modelInfo) []modelInfo {
	models := append([]modelInfo(nil), base...)
	seen := map[string]bool{}
	for _, m := range models {
		seen[m.ID] = true
	}
	yes := true
	for _, pack := range builtinModelPacks() {
		for _, spec := range append(append([]packModelSpec(nil), pack.Models...), pack.Optional...) {
			if seen[spec.ID] {
				continue
			}
			seen[spec.ID] = true
			price := 0.1 + float64(len(models)%7)*0.4
			models = append(models, modelInfo{ID: spec.ID, Name: spec.ID[strings.LastIndex(spec.ID, "/")+1:], InputPrice: ptrFloat(price), OutputPrice: ptrFloat(price * 4), Tools: &yes, ContextWindow: 400000, MaxOutputTokens: 64000, ReasoningEfforts: []string{"low", "medium", "high"}})
		}
	}
	return models
}

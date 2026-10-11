//go:build desktop

package main

import "testing"

func TestNativeClaude55CapabilityUpgradePreservesSharedPreference(t *testing.T) {
	u := nativeTestUI(t)
	u.page, u.client = "clients", "claude"
	u.setValue("clients-claude-mode", "installed")
	opus, legacy := "anthropic/claude-opus-5.5", "anthropic/claude-sonnet-4.6"
	u.models = []modelInfo{{ID: opus, Name: "Claude Opus 5.5", ContextWindow: 200000}, {ID: legacy, Name: "Claude Sonnet 4.6", ContextWindow: 200000}}
	nativeSeedSharedForTest(t, u, u.models...)
	for _, id := range []string{opus, legacy} {
		u.setValue(nativeClientField(sharedModelKey, id, "reasoning"), "high")
	}
	u.persistLibraryEdits()
	u.flushModelLibrary()
	for _, version := range []string{"2.1.251", "2.1.266", "2.1.267"} {
		caps := claudeCaps(version)
		u.clientState().Claude, u.clientState().Xcode.Claude = caps, caps
		nativeTestFrame(t, u)
		for _, key := range []string{"claude", "xcode-claude"} {
			derived := u.sharedClientSelection(key)
			payload, err := nativeClientPayload(key, derived)
			if err != nil {
				t.Fatal(err)
			}
			selection := payload.(claudeSelection)
			efforts := map[string]string{}
			for _, model := range selection.Models {
				efforts[model.ID] = model.Effort
			}
			want := ""
			if version == "2.1.267" {
				want = "high"
			}
			if efforts[opus] != want || efforts[legacy] != "high" {
				t.Fatalf("%s %s payload lost capability isolation: %#v", key, version, efforts)
			}
		}
		if saved := nativeSavedLibraryItem(t, u.owner.modelLibrary.snapshot().Library, opus); saved.ReasoningEffort != "high" {
			t.Fatal("CLI capabilities changed the user's shared reasoning preference")
		}
	}
}

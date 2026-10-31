//go:build desktop

package main

import (
	"reflect"
	"testing"
	"time"
)

func TestNativeT3CodePackMenuPreparesSelectedModels(t *testing.T) {
	u, recorder := nativeLaunchTestUI(t, "t3-code", false, false)
	shared := cloneModelLibrary(u.modelLibraryValue())
	u.page = "models"
	u.createPersonalPack("")
	packs := u.packsState()
	id := packs.file.Personal[0].ID
	if !u.changePersonalPack(id, func(pack *personalModelPack) error {
		pack.Library = modelLibrary{SchemaVersion: 1, DefaultModel: u.models[1].ID, Models: []modelLibraryItem{{ID: u.models[1].ID, DisplayName: "My T3 model", ContextPreset: contextPresetRecommended}}}
		return nil
	}) {
		t.Fatal("could not save personal pack")
	}
	nativeTestFrame(t, u)
	menu := "packs.use." + id
	uxmClickMenuItem(t, u, menu, menu+".t3-code")
	if packs.file.Assignments["t3-code"] != id {
		t.Fatal("T3 Code is missing from the pack assignment menu")
	}
	if users := u.uxmPackUsers(id); !reflect.DeepEqual(users, []string{"T3 Code"}) {
		t.Fatalf("T3 Code missing from pack users: %v", users)
	}
	u.launchAgent("t3-code")
	nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
	if recorder.count() != 1 || recorder.prepares.Load() != 1 {
		t.Fatalf("assigned T3 pack did not prepare and launch: %s", u.notice)
	}
	saved, err := u.owner.readT3CodePrepared()
	if err != nil {
		t.Fatal(err)
	}
	for name, library := range map[string]modelLibrary{"selection": saved.Library, "codex": saved.CodexLibrary, "claude": saved.ClaudeLibrary} {
		if len(library.Models) != 1 || library.DefaultModel != u.models[1].ID || library.Models[0].DisplayName != "My T3 model" {
			t.Fatalf("%s did not receive the assigned pack: %+v", name, library)
		}
	}
	if !reflect.DeepEqual(shared, u.modelLibraryValue()) || u.sharedClientSelection("codex").Initial != shared.DefaultModel {
		t.Fatal("T3 assignment changed the shared library or another app")
	}
	uxmClickMenuItem(t, u, menu, menu+".t3-code")
	if packs.file.Assignments["t3-code"] != "" {
		t.Fatal("T3 Code could not return to shared models")
	}
	// The launch recorder starts no process. Model closing the private window
	// after the launch grace period, as required before changing its selection.
	u.owner.mu.Lock()
	u.owner.t3CodeLaunchUntil = time.Time{}
	u.owner.mu.Unlock()
	u.launchAgent("t3-code")
	nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
	saved, err = u.owner.readT3CodePrepared()
	if err != nil || recorder.count() != 2 || saved.Library.DefaultModel != shared.DefaultModel || len(saved.Library.Models) != len(shared.Models) {
		t.Fatalf("T3 Code did not reprepare shared models: %+v, %v; launches=%d prepares=%d notice=%s", saved.Library, err, recorder.count(), recorder.prepares.Load(), u.notice)
	}
}

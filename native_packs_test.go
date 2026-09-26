//go:build desktop

package main

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func nativePackModelsForTest() []modelInfo {
	tools := true
	price := func(value float64) *float64 { return &value }
	return []modelInfo{
		{ID: "vendor/one", Name: "Shared first", ContextWindow: 200000, MaxOutputTokens: 8192, Tools: &tools, InputPrice: price(1), OutputPrice: price(2)},
		{ID: "anthropic/claude-sonnet-4.6", Name: "Shared second", ContextWindow: 200000, MaxOutputTokens: 8192, Tools: &tools, InputPrice: price(2), OutputPrice: price(10)},
		{ID: "deepseek/deepseek-v4.1-flash", Name: "DeepSeek Flash", ContextWindow: 1048576, MaxOutputTokens: 8192, Tools: &tools, InputPrice: price(.3), OutputPrice: price(1.2)},
		{ID: "openai/gpt-5.6-sol", Name: "GPT Sol", ContextWindow: 1050000, MaxOutputTokens: 8192, Tools: &tools, InputPrice: price(4), OutputPrice: price(20)},
		{ID: "anthropic/claude-opus-5.5", Name: "Claude Opus", ContextWindow: 1000000, MaxOutputTokens: 8192, Tools: &tools, InputPrice: price(4), OutputPrice: price(20)},
		{ID: "vendor/new-model", Name: "New model", ContextWindow: 128000, MaxOutputTokens: 4096, Tools: &tools, InputPrice: price(.2), OutputPrice: price(.5)},
		{ID: "openai/gpt-6-sol", Name: "GPT-6 Sol", ContextWindow: 1050000, MaxOutputTokens: 8192, Tools: &tools, InputPrice: price(2), OutputPrice: price(10)},
		{ID: "anthropic/claude-sonnet-5", Name: "Claude Sonnet 5", ContextWindow: 1000000, MaxOutputTokens: 8192, Tools: &tools, InputPrice: price(2), OutputPrice: price(10)},
	}
}

func TestNativePacksCopyEditAssignSwitchAndRestore(t *testing.T) {
	u := nativeTestUI(t)
	u.models = nativePackModelsForTest()
	u.page = "models"
	original := nativeSeedSharedForTest(t, u, u.models[0], u.models[1])
	before := cloneModelLibrary(u.modelLibraryValue())
	u.createPersonalPack("qa-bug-hunter")
	p := u.packsState()
	if p.tab != "mine" || len(p.file.Personal) != 1 || p.file.Personal[0].BasedOn != "qa-bug-hunter" || len(p.file.Personal[0].Library.Models) != 3 {
		t.Fatalf("built-in pack copy did not become an independent personal pack: %+v", p.file)
	}
	id := p.file.Personal[0].ID
	if !strings.Contains(p.file.Personal[0].Name, "QA") || p.file.Personal[0].Library.DefaultModel != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("copied pack lost name or default: %+v", p.file.Personal[0])
	}
	u.assignPack(id, "codex")
	if p.file.Assignments["codex"] != id || !reflect.DeepEqual(before, u.modelLibraryValue()) {
		t.Fatal("assigning a pack changed shared models.json")
	}
	if got := u.sharedClientSelection("codex"); len(got.Models) != 3 || got.Initial != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("Codex did not use its pack: %+v", got)
	}
	if got := u.sharedClientSelection("zed"); len(got.Models) != 2 || got.Initial != original.Initial {
		t.Fatalf("another agent's shared selection changed: %+v", got)
	}
	u.expanded["packs.my.add"] = true
	u.setValue("packs.my.search", "vendor/new-model")
	nativeTestFrame(t, u)
	u.clickable("packs.my.add.vendor/new-model").Click()
	nativeTestFrame(t, u)
	if got := p.file.personalPack(id); got == nil || len(got.Library.Models) != 4 || got.Library.Models[3].ID != "vendor/new-model" {
		t.Fatalf("user could not add a new catalog model to their own pack: %+v", got)
	}
	u.clickable("packs.my.default.vendor/new-model").Click()
	nativeTestFrame(t, u)
	if got := p.file.personalPack(id); got.Library.DefaultModel != "vendor/new-model" {
		t.Fatal("custom default not saved")
	}
	if got := u.sharedClientSelection("codex"); len(got.Models) != 4 || got.Initial != "vendor/new-model" {
		t.Fatalf("agent did not pick up its edited personal pack: %+v", got)
	}
	u.clickable("packs.my.remove.vendor/new-model").Click()
	nativeTestFrame(t, u)
	if got := p.file.personalPack(id); len(got.Library.Models) != 3 || got.Library.DefaultModel != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("removing a custom default did not restore an existing model: %+v", got)
	}
	u.assignPack("full-stack-dev", "codex")
	if got := u.sharedClientSelection("codex"); got.Initial != "openai/gpt-6-sol" || len(got.Models) != 3 {
		t.Fatalf("switching to another pack did not change only Codex: %+v", got)
	}
	if !reflect.DeepEqual(before, u.modelLibraryValue()) {
		t.Fatal("pack switch rewrote shared library")
	}
	u.flushModelLibrary()
	if got := u.owner.modelLibrary.snapshot().Library; !reflect.DeepEqual(got, before) {
		t.Fatalf("pack switch wrote models.json: %+v", got)
	}
	owner, err := newApp(u.owner.dir, &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	reopened := newNativeUI(owner, func() {})
	t.Cleanup(func() { owner.requestQuit(); reopened.shutdownModelLibrary() })
	reopened.models = nativePackModelsForTest()
	if got := reopened.sharedClientSelection("codex"); got.Initial != "openai/gpt-6-sol" || len(got.Models) != 3 {
		t.Fatalf("agent pack assignment did not survive restart: %+v", got)
	}
	if got := reopened.packsState().file.personalPack(id); got == nil || len(got.Library.Models) != 3 {
		t.Fatalf("editable personal pack did not survive restart: %+v", got)
	}
	reopened.clearPackAssignment("codex")
	if got := reopened.sharedClientSelection("codex"); len(got.Models) != 2 || got.Initial != original.Initial {
		t.Fatalf("switching back to shared library did not restore it: %+v", got)
	}
}

func TestNativePackSwitchKeepsExistingFiftyModelLibrary(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	u.models = nativePackModelsForTest()
	shared := []modelInfo{u.models[0], u.models[1]}
	for i := 0; i < 48; i++ {
		model := modelInfo{ID: fmt.Sprintf("vendor/saved-%02d", i), Name: "Saved model", ContextWindow: 200000, MaxOutputTokens: 8192}
		shared = append(shared, model)
		u.models = append(u.models, model)
	}
	nativeSeedSharedForTest(t, u, shared...)
	before := u.owner.modelLibrary.snapshot().Library
	u.assignPack("qa-bug-hunter", "codex")
	if len(u.sharedClientSelection("codex").Models) != 3 || len(u.sharedClientSelection("zed").Models) != 50 {
		t.Fatal("pack assignment changed the size of another agent's selection")
	}
	u.clearPackAssignment("codex")
	u.flushModelLibrary()
	if len(u.sharedClientSelection("codex").Models) != 50 || !reflect.DeepEqual(before, u.owner.modelLibrary.snapshot().Library) {
		t.Fatal("switching away from the pack did not preserve all 50 shared models")
	}
}

func TestNativePacksUnavailableAndClaudeDesktopRestrictions(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	u.models = nativePackModelsForTest()[:2]
	nativeSeedSharedForTest(t, u, u.models...)
	u.assignPack("qa-bug-hunter", "codex")
	if assigned := u.packsState().file.Assignments["codex"]; assigned != "" {
		t.Fatal("assigned a pack with unavailable team models")
	}
	u.models = nativePackModelsForTest()
	u.createPersonalPack("qa-bug-hunter")
	id := u.packsState().file.Personal[0].ID
	u.assignPack(id, "claude-desktop")
	if got := u.sharedClientSelection("claude-desktop"); len(got.Models) != 1 || got.Initial != "anthropic/claude-opus-5.5" {
		t.Fatalf("Claude Desktop did not filter non-Claude choices: %+v", got)
	}
	u.changePersonalPack(id, func(pack *personalModelPack) error {
		pack.Library.Models = pack.Library.Models[:1]
		pack.Library.DefaultModel = pack.Library.Models[0].ID
		pack.Roles = map[string]string{}
		return nil
	})
	u.assignPack(id, "claude-desktop")
	if got := u.packsState().file.Assignments["claude-desktop"]; got != id {
		t.Fatal("existing Claude Desktop assignment changed on rejected update")
	}
	// A new assignment from an unsupported pack is rejected, not silently
	// prepared as an empty model list.
	u.clearPackAssignment("claude-desktop")
	u.assignPack(id, "claude-desktop")
	if got := u.packsState().file.Assignments["claude-desktop"]; got != "" {
		t.Fatal("non-Claude-only pack was assigned to Claude Desktop")
	}
}

func TestNativePersonalPackCreateRenameRoleDeleteAndAgentFallback(t *testing.T) {
	u := nativeTestUI(t)
	u.page, u.language = "models", "es"
	u.models = nativePackModelsForTest()
	nativeSeedSharedForTest(t, u, u.models[0])
	u.createPersonalPack("")
	p := u.packsState()
	if len(p.file.Personal) != 1 || len(p.file.Personal[0].Library.Models) != 0 {
		t.Fatal("creating an empty own pack changed the library")
	}
	id := p.file.Personal[0].ID
	u.assignPack(id, "codex")
	if p.file.Assignments["codex"] != "" {
		t.Fatal("empty pack was assigned to an agent")
	}
	u.expanded["packs.my.add"] = true
	u.setValue("packs.my.search", "vendor/new-model")
	nativeTestFrame(t, u)
	u.clickable("packs.my.add.vendor/new-model").Click()
	nativeTestFrame(t, u)
	if got := p.file.personalPack(id); len(got.Library.Models) != 1 || got.Library.DefaultModel != "vendor/new-model" {
		t.Fatalf("first model was not saved as pack default: %+v", got)
	}
	u.setValue("packs.name", "Mi QA propio")
	u.clickable("packs.my.rename." + id).Click()
	nativeTestFrame(t, u)
	if got := p.file.personalPack(id).Name; got != "Mi QA propio" {
		t.Fatalf("personal name was not persisted: %s", got)
	}
	roleID := "packs.my.role." + id + ".vendor/new-model"
	u.clickable(roleID).Click()
	nativeTestFrame(t, u)
	nativeTestFrame(t, u)
	u.setValue(roleID+".text", "Pruebas de regresión")
	u.clickable(roleID + ".save").Click()
	nativeTestFrame(t, u)
	if got := p.file.personalPack(id).Roles["vendor/new-model"]; got != "Pruebas de regresión" {
		t.Fatalf("personal model role not saved: %s", got)
	}
	u.assignPack(id, "codex")
	if p.file.Assignments["codex"] != id {
		t.Fatal("nonempty own pack could not be assigned")
	}
	u.clickable("packs.my.delete." + id).Click()
	nativeTestFrame(t, u)
	if p.pendingDelete != id || len(p.file.Personal) != 1 {
		t.Fatal("delete action skipped confirmation")
	}
	u.clickable("packs.my.cancel-delete").Click()
	nativeTestFrame(t, u)
	if p.pendingDelete != "" || len(p.file.Personal) != 1 {
		t.Fatal("cancel deleted a pack")
	}
	u.clickable("packs.my.delete." + id).Click()
	nativeTestFrame(t, u)
	u.clickable("packs.my.confirm-delete").Click()
	nativeTestFrame(t, u)
	if len(p.file.Personal) != 0 || p.file.Assignments["codex"] != "" || len(u.sharedClientSelection("codex").Models) != 1 {
		t.Fatal("deleting own pack did not restore the shared selection")
	}
}

func TestNativePackCatalogChangeBlocksAgentPreparation(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	u.models = nativePackModelsForTest()
	nativeSeedSharedForTest(t, u, u.models[0])
	u.assignPack("qa-bug-hunter", "codex")
	if err := u.packReadinessError("codex"); err != nil {
		t.Fatal(err)
	}
	u.models = u.models[:2]
	if err := u.packReadinessError("codex"); err == nil || !strings.Contains(err.Error(), "Refresh the catalog") {
		t.Fatalf("missing pack models were not detected: %v", err)
	}
	called := false
	u.prepareClientAfter("codex", func(err error) { called = err != nil })
	if !called {
		t.Fatal("client preparation accepted a missing pack model")
	}
	if u.owner.modelLibrary.snapshot().Library.DefaultModel != "vendor/one" {
		t.Fatal("catalog change modified the shared library")
	}
}

func TestNativePackAssignmentPreparesAndLaunchesTheSelectedProfile(t *testing.T) {
	u, recorder := nativeLaunchTestUI(t, "codex", false, false)
	u.createPersonalPack("")
	id := u.packsState().file.Personal[0].ID
	if !u.changePersonalPack(id, func(pack *personalModelPack) error {
		pack.Library.Models = []modelLibraryItem{{ID: u.models[1].ID, ContextPreset: contextPresetRecommended}}
		pack.Library.DefaultModel = u.models[1].ID
		return nil
	}) {
		t.Fatal("personal pack was not saved")
	}
	u.assignPack(id, "codex")
	u.launchAgent("codex")
	nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
	if recorder.count() != 1 || recorder.prepares.Load() != 1 {
		t.Fatalf("assigned pack did not launch: %s", u.notice)
	}
	raw, err := nativeRequest(u.owner, "GET", nativeClientEndpoint("codex"), nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := decodeNativeClientSelection("codex", raw, u.models)
	if err != nil || len(prepared.Models) != 1 || prepared.Initial != u.models[1].ID {
		t.Fatalf("prepared GUI profile did not contain assigned pack: %+v, %v", prepared, err)
	}
	u.clearPackAssignment("codex")
	u.launchAgent("codex")
	nativeTestWait(t, u, func() bool { return u.clientState().Launching == "" })
	raw, err = nativeRequest(u.owner, "GET", nativeClientEndpoint("codex"), nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err = decodeNativeClientSelection("codex", raw, u.models)
	if err != nil || recorder.count() != 2 || len(prepared.Models) != 1 || prepared.Initial != u.models[0].ID {
		t.Fatalf("switching back to the shared library did not reprepare the agent: %+v, %v", prepared, err)
	}
}

func TestNativePackUiPointerNavigationAndOwnCopy(t *testing.T) {
	for _, size := range []image.Point{{1180, 900}, {720, 700}} {
		t.Run(fmtSize(size), func(t *testing.T) {
			u := nativeTestUI(t)
			// The native fixture starts in English. Keep its saved preference in
			// sync with the Spanish UI, so the periodic state refresh cannot
			// restore English during slower Windows/ARM pointer interactions.
			u.owner.mu.Lock()
			u.owner.config.Language = "es"
			u.owner.mu.Unlock()
			u.languageRevision++
			u.page, u.language = "models", "es"
			u.models = nativePackModelsForTest()
			nativeSeedSharedForTest(t, u, u.models[0], u.models[1])
			h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
			h.frame()
			h.click("Packs", semantic.Button)
			if u.packsState().tab != "packs" {
				t.Fatal("pointer did not open packs")
			}
			nativeGridCapture(t, h, "native-packs-gallery-"+fmtSize(size))
			h.reveal("QA · Cazabugs", semantic.Button)
			h.click("QA · Cazabugs", semantic.Button)
			h.reveal("Crear mi versión editable", semantic.Button)
			nativeGridCapture(t, h, "native-packs-catalog-"+fmtSize(size))
			h.click("Crear mi versión editable", semantic.Button)
			if u.packsState().tab != "mine" || len(u.packsState().file.Personal) != 1 {
				t.Fatal("pointer could not create a personal copy")
			}
			if u.language != "es" {
				t.Fatalf("saved test language changed during pack navigation: %s", u.language)
			}
			nativeGridCapture(t, h, "native-packs-own-start-"+fmtSize(size))
			h.reveal("Añadir modelo del catálogo", semantic.Button)
			h.click("Añadir modelo del catálogo", semantic.Button)
			u.setValue("packs.my.search", "vendor/new-model")
			h.frame()
			h.reveal("Añadir a mi pack", semantic.Button)
			nativeGridCapture(t, h, "native-packs-own-"+fmtSize(size))
			h.click("Añadir a mi pack", semantic.Button)
			if got := len(u.packsState().file.Personal[0].Library.Models); got != 4 {
				t.Fatalf("pointer add did not persist fourth model: %d", got)
			}
		})
	}
}

func TestNativePacksExternalEditDoesNotUseUnsavedAssignment(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	u.models = nativePackModelsForTest()
	nativeSeedSharedForTest(t, u, u.models[0])
	u.packsState()
	path := filepath.Join(u.owner.dir, "model-packs.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"personal":[],"assignments":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	u.assignPack("qa-bug-hunter", "codex")
	if got := u.packsState().file.Assignments["codex"]; got != "" {
		t.Fatal("agent started using pack whose save conflicted with external file")
	}
}

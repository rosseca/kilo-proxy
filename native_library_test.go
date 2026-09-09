//go:build desktop

package main

import (
	"errors"
	"image"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func nativeSavedLibraryItem(t *testing.T, library modelLibrary, id string) modelLibraryItem {
	t.Helper()
	for _, model := range library.Models {
		if model.ID == id {
			return model
		}
	}
	t.Fatalf("saved library does not contain %s: %#v", id, library)
	return modelLibraryItem{}
}

func TestNativeLibraryModelActionsAutosaveAndRestore(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	u.expanded["library.catalog"] = true
	u.models = nativeClientModelsForTest()
	nativeTestFrame(t, u)
	for _, model := range u.models {
		u.setValue("client:shared:manual", model.ID)
		u.clickable("client:shared:add").Click()
		nativeTestFrame(t, u)
		u.flushModelLibrary()
		if got := nativeSavedLibraryItem(t, u.owner.modelLibrary.snapshot().Library, model.ID); got.ID != model.ID {
			t.Fatal("adding a model did not save immediately")
		}
	}
	u.clickable("models.done").Click()
	nativeTestFrame(t, u)
	u.clickable("client:shared:edit:vendor/one").Click()
	nativeTestFrame(t, u)
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "My daily model")
	reasoning := nativeClientField(sharedModelKey, "vendor/one", "reasoning")
	u.clickable(reasoning + ".toggle").Click()
	nativeTestFrame(t, u)
	u.clickable(reasoning + ".option.high").Click()
	nativeTestFrame(t, u)
	u.clickable("client:shared:initial:anthropic/claude-sonnet-4.6").Click()
	nativeTestFrame(t, u)
	u.clickable("client:shared:edit:anthropic/claude-sonnet-4.6").Click()
	nativeTestFrame(t, u)
	u.clickable("client:shared:up:anthropic/claude-sonnet-4.6").Click()
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	state := u.owner.modelLibrary.snapshot()
	if state.Library.DefaultModel != "anthropic/claude-sonnet-4.6" || state.Library.Models[0].ID != state.Library.DefaultModel {
		t.Fatalf("initial model or order did not autosave: %#v", state)
	}
	first := nativeSavedLibraryItem(t, state.Library, "vendor/one")
	if first.DisplayName != "My daily model" || first.ReasoningEffort != "high" {
		t.Fatalf("name/reasoning changes did not autosave: %#v", first)
	}
	if text, saved := u.libraryStatus(); !saved || !strings.Contains(text, "Saved") {
		t.Fatalf("successful latest save not reflected in status: %s", text)
	}
	// Restart the application store and native UI, without loading a generated
	// agent profile or making the live catalog available.
	owner, err := newApp(u.owner.dir, &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	reopened := newNativeUI(owner, func() {})
	t.Cleanup(func() { owner.requestQuit(); reopened.shutdownModelLibrary() })
	reopened.initModelLibrary()
	if got := reopened.modelLibraryValue(); !reflect.DeepEqual(got, state.Library) {
		t.Fatalf("native startup changed saved choices: got %#v want %#v", got, state.Library)
	}
	reopened.models = nil
	reopened.page = "models"
	nativeTestFrame(t, reopened)
	reopened.flushModelLibrary()
	if got := reopened.modelLibraryValue(); !reflect.DeepEqual(got, state.Library) {
		t.Fatalf("unavailable catalog changed saved choices: %#v", got)
	}
	// A real pointer action on Your models removes exactly the selected choice.
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 820), now: time.Now()}
	h.frame()
	h.click("My daily model", semantic.CheckBox)
	u.flushModelLibrary()
	removed := u.owner.modelLibrary.snapshot().Library
	if len(removed.Models) != 1 || removed.Models[0].ID != "anthropic/claude-sonnet-4.6" || removed.DefaultModel != removed.Models[0].ID {
		t.Fatalf("removing a nondefault model did not preserve the default: %#v", removed)
	}
}

func TestNativeLibraryLatestQueuedEditSurvivesFlushWithoutFrames(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	nativeSeedSharedForTest(t, u, u.models[0])
	store := u.owner.modelLibrary
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	store.write = func(path string, data []byte) error {
		if filepath.Base(path) == "models.json" {
			once.Do(func() { close(entered); <-release })
		}
		return atomicCatalogFile(path, data)
	}
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(release) }); u.flushModelLibrary() })
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "First edit being saved")
	nativeTestFrame(t, u)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first autosave did not start")
	}
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Final visible edit before closing")
	nativeTestFrame(t, u)
	if text, saved := u.libraryStatus(); saved || !strings.Contains(text, "Saving") {
		t.Fatalf("pending newest revision shown as saved: %s", text)
	}
	// Window closure no longer supplies Layout/drain callbacks. The disk writer
	// must independently finish the newest edit after the current save completes.
	unblock.Do(func() { close(release) })
	u.shutdownModelLibrary()
	if got := newModelLibraryStore(store.dir).snapshot().Library; nativeSavedLibraryItem(t, got, "vendor/one").DisplayName != "Final visible edit before closing" {
		t.Fatalf("latest edit was dropped after closing: %#v", got)
	}
	if _, saved := u.libraryStatus(); !saved {
		t.Fatal("flush did not settle the newest queued revision")
	}
}

func TestNativeLibrarySaveErrorRetainsVisibleEditsAndRetry(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	nativeSeedSharedForTest(t, u, u.models[0])
	before := u.owner.modelLibrary.snapshot()
	u.owner.modelLibrary.write = func(string, []byte) error { return errors.New("synthetic disk full") }
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Keep this unsaved edit")
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	if _, saved := u.libraryStatus(); saved {
		t.Fatal("failed save reported Saved")
	}
	if got := u.owner.modelLibrary.snapshot(); !reflect.DeepEqual(got, before) {
		t.Fatalf("failed autosave replaced the last good library: %#v", got)
	}
	if got := u.library.selection.choice("vendor/one").DisplayName; got != "Keep this unsaved edit" {
		t.Fatalf("failed autosave discarded the visible edit: %s", got)
	}
	u.owner.modelLibrary.write = atomicCatalogFile
	nativeTestFrame(t, u)
	u.clickable("models.retry").Click()
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	if got := u.owner.modelLibrary.snapshot(); got.Revision <= before.Revision || nativeSavedLibraryItem(t, got.Library, "vendor/one").DisplayName != "Keep this unsaved edit" {
		t.Fatalf("retry failed to save the retained edit: %#v", got)
	}
}

func TestNativeLibraryIncompleteTokenEditCannotReportSaved(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	nativeSeedSharedForTest(t, u, u.models[0])
	before := u.owner.modelLibrary.snapshot()
	field := nativeClientField(sharedModelKey, "vendor/one", "context")
	u.setValue(field, "not finished")
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Name edited with token limit")
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	if text, saved := u.libraryStatus(); saved || !strings.Contains(text, "whole numbers") {
		t.Fatalf("incomplete numeric edit was reported saved: %s", text)
	}
	if !reflect.DeepEqual(u.owner.modelLibrary.snapshot(), before) || u.value(field) != "not finished" {
		t.Fatal("invalid numeric edit changed disk or was discarded")
	}
	// Retry used to bypass text validation and persist Atoi's zero fallback.
	nativeTestFrame(t, u)
	u.clickable("models.retry").Click()
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	if !reflect.DeepEqual(u.owner.modelLibrary.snapshot(), before) {
		t.Fatal("Retry saved the incomplete context as an unspecified limit")
	}
	// The callback must also guard a newly invalid editor value before another
	// layout has updated the disabled state and displayed validation message.
	u.library.validation = ""
	u.retryModelLibrary(false)
	u.flushModelLibrary()
	if !reflect.DeepEqual(u.owner.modelLibrary.snapshot(), before) || u.value(field) != "not finished" {
		t.Fatal("retry handler bypassed validation of the visible editor draft")
	}
	u.setValue(field, "128000")
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	got := nativeSavedLibraryItem(t, u.owner.modelLibrary.snapshot().Library, "vendor/one")
	if got.ContextWindow != 128000 || got.DisplayName != "Name edited with token limit" {
		t.Fatalf("completing the limit did not save the retained draft: %#v", got)
	}
}

func TestNativeLibraryRecoveryRequiresVisibleAction(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	nativeSeedSharedForTest(t, u, u.models[0])
	path := filepath.Join(u.owner.dir, "models.json")
	corrupt := []byte("invalid saved library")
	if err := os.WriteFile(path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	u.owner.modelLibrary = newModelLibraryStore(u.owner.dir)
	u.library = nil
	u.initModelLibrary()
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	if text, saved := u.libraryStatus(); saved || !strings.Contains(text, "recovery") {
		t.Fatalf("damaged saved library was shown as saved: %s", text)
	}
	if current, _ := os.ReadFile(path); string(current) != string(corrupt) {
		t.Fatal("startup replaced the damaged file without explicit recovery")
	}
	u.clickable("models.retry").Click()
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	if _, saved := u.libraryStatus(); !saved || u.owner.modelLibrary.snapshot().RecoveryRequired || len(u.library.selection.Models) != 1 {
		t.Fatal("explicit recovery did not preserve and save the visible backup selection")
	}
	archives, _ := filepath.Glob(path + ".corrupt-*.json")
	if len(archives) != 1 {
		t.Fatal("explicit recovery did not preserve the damaged original")
	}
}

func TestNativeLibraryConflictReviewPreservesDraftUntilAccepted(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	nativeSeedSharedForTest(t, u, u.models[0])
	before := u.owner.modelLibrary.snapshot()
	external := cloneModelLibrary(before.Library)
	external.Models[0].DisplayName = "Saved from another editor"
	if _, err := u.owner.modelLibrary.save(external, before.Revision, false); err != nil {
		t.Fatal(err)
	}
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Keep my conflicting draft")
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	if _, saved := u.libraryStatus(); saved || u.library.selection.choice("vendor/one").DisplayName != "Keep my conflicting draft" {
		t.Fatal("conflict discarded a draft or reported Saved")
	}
	nativeTestFrame(t, u)
	u.clickable("models.review-saved").Click()
	nativeTestFrame(t, u)
	if u.library.pendingImport == nil || u.library.pendingImport.choice("vendor/one").DisplayName != "Saved from another editor" || u.library.selection.choice("vendor/one").DisplayName != "Keep my conflicting draft" {
		t.Fatal("review did not keep the saved version separate from the conflicting draft")
	}
	u.clickable("models.import.accept").Click()
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	if _, saved := u.libraryStatus(); !saved || u.library.selection.choice("vendor/one").DisplayName != "Saved from another editor" {
		t.Fatal("accepting the reviewed revision did not resolve the conflict")
	}
}

func TestNativeLibrarySharedNamesDefaultsAndSafeClaudeEffort(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	s := nativeSeedSharedForTest(t, u, nativeClientModelsForTest()...)
	s.Initial = "anthropic/claude-sonnet-4.6"
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "My daily model")
	u.setValue(nativeClientField(sharedModelKey, "vendor/one", "reasoning"), "high")
	u.setValue(nativeClientField(sharedModelKey, s.Initial, "name"), "My Claude")
	u.setValue(nativeClientField(sharedModelKey, s.Initial, "reasoning"), "high")
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	for _, key := range []string{"codex", "codex-cli", "opencode", "zed", "claude"} {
		u.setValue("clients-claude-mode", "modern")
		derived := u.sharedClientSelection(key)
		if !reflect.DeepEqual(derived.ids(), s.ids()) || derived.Initial != s.Initial || derived.choice("vendor/one").DisplayName != "My daily model" || derived.choice(s.Initial).DisplayName != "My Claude" {
			t.Fatalf("%s lost shared identity/order/name/default: %#v", key, derived)
		}
		if key == "claude" && (derived.choice("vendor/one").ClaudeEffort != "" || derived.choice(s.Initial).ClaudeEffort != "high") {
			t.Fatalf("Claude effort was not checked against model support: %#v", derived.Models)
		}
		if _, err := nativeClientPayload(key, derived); err != nil {
			t.Fatalf("shared library cannot prepare %s: %v", key, err)
		}
		// Preparing the derived profile must not mutate the shared preference.
		derived.Models[0].DisplayName = "agent-local mutation"
		derived.Models[0].ReasoningLevels = append(derived.Models[0].ReasoningLevels, "ultra")
		if s.Models[0].DisplayName == "agent-local mutation" || s.Models[0].DefaultReasoning != "high" {
			t.Fatal("derived client profile aliases the shared choice")
		}
	}
	// An unsupported shared request remains saved, but must never be emitted as
	// a Claude effort merely because that model appears in the library.
	u.setValue(nativeClientField(sharedModelKey, s.Initial, "reasoning"), "ultra")
	u.persistLibraryEdits()
	u.flushModelLibrary()
	if derived := u.sharedClientSelection("claude"); derived.choice(s.Initial).ClaudeEffort != "" || s.choice(s.Initial).DefaultReasoning != "ultra" {
		t.Fatal("unsupported Claude effort was emitted or the shared preference was discarded")
	}
}

func TestNativeLibraryUnknownOfflineModelRemainsVisible(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "models"
	nativeSeedSharedForTest(t, u, modelInfo{ID: "unavailable/selected", Name: "Unavailable model", ContextWindow: 240000})
	u.setValue(nativeClientField(sharedModelKey, "unavailable/selected", "name"), "Saved offline choice")
	u.models = nil
	nativeTestFrame(t, u)
	u.flushModelLibrary()
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(780, 700), now: time.Now()}
	h.frame()
	h.target("Saved offline choice", semantic.CheckBox)
	warning := false
	for _, node := range h.nodes() {
		warning = warning || strings.Contains(node.Desc.Label, "catalog unavailable")
	}
	if !warning || len(u.library.selection.Models) != 1 || u.library.selection.Initial != "unavailable/selected" {
		t.Fatal("offline catalog cleared or concealed the saved choice")
	}
}

func TestNativeLibraryFlowSnapshots(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {780, 700}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"-"+lang, func(t *testing.T) {
				u, recorder := nativeLaunchTestUI(t, "codex", false, false)
				u.language, u.page = lang, "agents"
				// Stable synthetic values keep review images readable on every runner.
				nativeSeedSharedForTest(t, u, nativeClientModelsForTest()...)
				u.agentsState()
				for _, key := range launchClients {
					u.setValue(agentProjectField(key), "/workspaces/shop-dashboard")
				}
				u.setValue(nativeClientField(sharedModelKey, "vendor/one", "name"), "Daily coding")
				u.setValue(nativeClientField(sharedModelKey, "anthropic/claude-sonnet-4.6", "name"), "Sonnet")
				nativeTestFrame(t, u)
				u.flushModelLibrary()
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				launch := h.target(u.tr("Open Codex", "Abrir Codex"), semantic.Button)
				if !launch.Desc.Bounds.In(image.Rectangle{Max: size}) {
					t.Fatalf("launch control falls below the first viewport: %v", launch.Desc.Bounds)
				}
				nativeGridCapture(t, h, "shared-agents-"+fmtSize(size)+"-"+lang)
				h.click(u.tr("Edit models", "Editar modelos"), semantic.Button)
				if u.page != "models" {
					t.Fatal("agent home did not open shared Models")
				}
				h.target(u.tr("Add models", "Añadir modelos"), semantic.Button)
				nativeGridCapture(t, h, "shared-your-models-"+fmtSize(size)+"-"+lang)
				h.click(u.tr("Add models", "Añadir modelos"), semantic.Button)
				if !u.expanded["library.catalog"] {
					t.Fatal("Add models did not show the catalog")
				}
				h.target(u.tr("Done", "Listo"), semantic.Button)
				nativeGridCapture(t, h, "shared-add-models-"+fmtSize(size)+"-"+lang)
				if recorder.count() != 0 {
					t.Fatal("navigating the model library launched an agent")
				}
			})
		}
	}
}

func TestNativeLibraryWriterOwnsQueuedValues(t *testing.T) {
	store := newModelLibraryStore(t.TempDir())
	value := testModelLibrary()
	w := &nativeLibraryWriter{store: store}
	w.queue(value, false)
	value.Models[1].DisplayName = "mutation outside queue"
	value.Models[1].ReasoningLevels[0] = "ultra"
	w.flush()
	got := store.snapshot().Library
	if !reflect.DeepEqual(got, testModelLibrary()) {
		t.Fatalf("queued save changed through caller memory: %#v", got)
	}
	if _, err := os.Stat(filepath.Join(store.dir, "models.json")); err != nil {
		t.Fatal(err)
	}
}

//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"gioui.org/layout"
)

const sharedModelKey = "shared"

// A single writer owns disk revisions. It continues saving the newest queued
// edit even when the window is hidden and does not depend on frame callbacks.
type nativeLibraryWriter struct {
	mu                                 sync.Mutex
	store                              *modelLibraryStore
	latest                             modelLibrary
	revision, generation, saved        uint64
	running, recoveryRequired, recover bool
	warning                            string
	err                                error
	done                               chan struct{}
	invalidate                         func()
}

func (w *nativeLibraryWriter) queue(value modelLibrary, recover bool) {
	w.mu.Lock()
	w.latest = cloneModelLibrary(value)
	w.generation++
	w.recover = recover
	w.err = nil
	if w.recoveryRequired && !recover {
		w.mu.Unlock()
		return
	}
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.done = make(chan struct{})
	w.mu.Unlock()
	go w.run()
}
func (w *nativeLibraryWriter) run() {
	for {
		w.mu.Lock()
		value, generation, revision, recover := w.latest, w.generation, w.revision, w.recover
		w.mu.Unlock()
		result, err := w.store.save(value, revision, recover)
		w.mu.Lock()
		w.err = err
		if err == nil {
			w.revision, w.saved = result.Revision, generation
			w.warning, w.recoveryRequired, w.recover = result.Warning, result.RecoveryRequired, false
		}
		again := err == nil && generation != w.generation
		if !again {
			w.running = false
			close(w.done)
		}
		w.mu.Unlock()
		if w.invalidate != nil {
			w.invalidate()
		}
		if !again {
			return
		}
	}
}
func (w *nativeLibraryWriter) flush() {
	for {
		w.mu.Lock()
		running, done := w.running, w.done
		w.mu.Unlock()
		if !running {
			return
		}
		<-done
	}
}

type nativeLibrary struct {
	validation    string
	pendingState  *modelLibraryState
	selection     *nativeClientSelection
	writer        *nativeLibraryWriter
	last          string
	pendingImport *nativeClientSelection
	importSource  string
}

func (u *nativeUI) initModelLibrary() {
	if u.library != nil {
		return
	}
	state := u.owner.modelLibrary.snapshot()
	s := &nativeClientSelection{Aliases: map[string]string{}, Mode: "installed", Initial: state.Library.DefaultModel}
	for _, item := range state.Library.Models {
		choice := nativeModelChoice{Model: modelInfo{ID: item.ID, Name: item.ID, ContextWindow: item.ContextWindow, MaxOutputTokens: item.MaxOutputTokens}, DisplayName: item.DisplayName, DefaultReasoning: item.ReasoningEffort, ReasoningCustom: item.ReasoningCustom, ReasoningLevels: slices.Clone(item.ReasoningLevels)}
		s.Models = append(s.Models, choice)
		u.seedClientChoice(sharedModelKey, choice)
		// Keep the requested preference even while catalog metadata is unavailable.
		u.setValue(nativeClientField(sharedModelKey, item.ID, "reasoning"), item.ReasoningEffort)
	}
	w := &nativeLibraryWriter{store: u.owner.modelLibrary, latest: state.Library, revision: state.Revision, warning: state.Warning, recoveryRequired: state.RecoveryRequired, invalidate: u.invalidate}
	u.library = &nativeLibrary{selection: s, writer: w}
	u.library.last = libraryFingerprint(u.modelLibraryValue())
}
func libraryFingerprint(value modelLibrary) string { b, _ := json.Marshal(value); return string(b) }
func (u *nativeUI) modelLibraryValue() modelLibrary {
	s := u.library.selection
	value := modelLibrary{SchemaVersion: 1, DefaultModel: s.Initial, Models: []modelLibraryItem{}}
	for _, m := range s.Models {
		value.Models = append(value.Models, modelLibraryItem{ID: m.Model.ID, DisplayName: m.DisplayName, ReasoningEffort: m.DefaultReasoning, ReasoningLevels: slices.Clone(m.ReasoningLevels), ReasoningCustom: m.ReasoningCustom, ContextWindow: m.Model.ContextWindow, MaxOutputTokens: m.Model.MaxOutputTokens})
	}
	return value
}
func (u *nativeUI) validatedModelLibraryValue() (modelLibrary, bool) {
	u.syncClientSelection(sharedModelKey, u.library.selection)
	u.library.validation = ""
	for _, model := range u.library.selection.Models {
		for _, field := range []string{"context", "output"} {
			if _, err := strconv.Atoi(u.value(nativeClientField(sharedModelKey, model.Model.ID, field))); err != nil {
				u.library.validation = u.tr("Token limits must be whole numbers.", "Los límites de tokens deben ser números enteros.")
				return modelLibrary{}, false
			}
		}
	}
	value := u.modelLibraryValue()
	if err := validateModelLibrary(value); err != nil {
		u.library.validation = err.Error()
		return modelLibrary{}, false
	}
	return value, true
}
func (u *nativeUI) persistLibraryEdits() {
	if u.library == nil {
		return
	}
	value, valid := u.validatedModelLibraryValue()
	if !valid {
		return
	}
	signature := libraryFingerprint(value)
	if signature != u.library.last {
		u.library.last = signature
		u.library.writer.queue(value, false)
	}
}
func (u *nativeUI) retryModelLibrary(recover bool) {
	// Revalidate the editor text in the handler as well as disabling the button.
	// A partial numeric edit must never be persisted as Atoi's zero fallback.
	if value, valid := u.validatedModelLibraryValue(); valid {
		u.library.last = libraryFingerprint(value)
		u.library.writer.queue(value, recover)
	}
}
func (u *nativeUI) flushModelLibrary() {
	if u.library != nil {
		u.library.writer.flush()
	}
}

func (u *nativeUI) libraryStatus() (text string, ready bool) {
	u.initModelLibrary()
	if u.library.validation != "" {
		return u.library.validation, false
	}
	w := u.library.writer
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.recoveryRequired {
		return u.tr("Saved file needs recovery. Your models are kept here.", "El archivo necesita recuperación. Tus modelos se mantienen aquí."), false
	}
	if w.err != nil {
		return u.tr("Could not save: ", "No se pudo guardar: ") + w.err.Error(), false
	}
	if w.running || w.saved != w.generation {
		return u.tr("Saving…", "Guardando…"), false
	}
	return u.tr("Saved automatically", "Guardado automáticamente"), true
}

// Client profiles are derived from the common library. Their prepared file
// paths and fingerprints remain client-specific, as do integration-only aliases.
func (u *nativeUI) sharedClientSelection(key string) *nativeClientSelection {
	u.initModelLibrary()
	source := u.library.selection
	u.syncClientSelection(sharedModelKey, source)
	s := u.clientState().selection(key)
	models := make([]nativeModelChoice, 0, len(source.Models))
	for _, value := range source.Models {
		m := value
		m.ReasoningLevels = slices.Clone(value.ReasoningLevels)
		if key == "claude" || key == "xcode-claude" {
			if validClaudeEffort(m.Model.ID, m.DefaultReasoning) && (u.clientCaps(key).PerModelEffort || m.Model.ID == source.Initial) {
				m.ClaudeEffort = m.DefaultReasoning
				if m.ClaudeEffort == "xhigh" && !u.clientCaps(key).PerModelEffort {
					m.ClaudeEffort = ""
				}
			} else {
				m.ClaudeEffort = ""
			}
		}
		models = append(models, m)
	}
	if !reflect.DeepEqual(s.Models, models) || s.Initial != source.Initial {
		s.Models, s.Initial = models, source.Initial
		for _, m := range s.Models {
			u.seedClientChoice(key, m)
		}
		for alias, id := range s.Aliases {
			if id != "" && source.choice(id) == nil {
				delete(s.Aliases, alias)
				u.setValue("client:"+key+":alias:"+alias, "")
			}
		}
	}
	u.seedClientImages(key, s)
	if key == "codex-cli" {
		gui := u.clientState().selection("codex")
		u.seedClientImages("codex", gui)
		s.ImageGeneration = cloneClientImageSettings(gui.ImageGeneration)
		s.imageGenerationBaseline = cloneClientImageSettings(gui.imageGenerationBaseline)
	}
	return s
}

func (u *nativeUI) sharedModelSummary() string {
	u.initModelLibrary()
	s := u.library.selection
	if len(s.Models) == 0 {
		return u.tr("No models yet", "Aún no hay modelos")
	}
	initial := s.choice(s.Initial)
	name := s.Initial
	if initial != nil {
		name = nativeCodexDisplayName(*initial)
	}
	return fmt.Sprintf(u.tr("%d shared models · Starts with %s", "%d modelos compartidos · Empieza con %s"), len(s.Models), name)
}

func (u *nativeUI) modelsPanel() layout.Widget {
	u.initModelLibrary()
	s := u.library.selection
	u.syncClientSelection(sharedModelKey, s)
	status, _ := u.libraryStatus()
	top := u.actionRow(u.column(u.heading(u.tr("Your models", "Tus modelos")), u.note(status)), u.button("primary.models.add", u.tr("Add models", "Añadir modelos"), func() {
		u.expanded["library.catalog"] = true
		if len(u.models) == 0 {
			u.refreshModels()
		}
	}))
	if u.expanded["library.catalog"] {
		top = u.actionRow(u.column(u.heading(u.tr("Add models", "Añadir modelos")), u.note(u.tr("Select once. Every agent uses this library.", "Elige una vez. Todos los agentes usan esta biblioteca."))), u.button("models.done", u.tr("Done", "Listo"), func() { u.expanded["library.catalog"] = false }))
	}
	widgets := []layout.Widget{top}
	if u.catalogCached {
		widgets = append(widgets, u.note(u.tr("Using the saved catalog. Refresh to check current prices and availability.", "Usando el catálogo guardado. Actualiza para comprobar precios y disponibilidad.")))
	}
	w := u.library.writer
	w.mu.Lock()
	recovery, failed, warning := w.recoveryRequired, w.err != nil, w.warning
	w.mu.Unlock()
	if recovery || failed || u.library.validation != "" {
		label := u.tr("Retry save", "Reintentar guardado")
		if recovery {
			label = u.tr("Recover this selection", "Recuperar esta selección")
		}
		widgets = append(widgets, u.card(u.note(status), u.note(warning), u.pills(u.disabled(u.library.validation == "", u.button("models.retry", label, func() { u.retryModelLibrary(recovery) })), u.button("models.review-saved", u.tr("Review saved version", "Revisar versión guardada"), func() { u.reviewSavedLibrary() }))))
	}
	if len(s.Models) == 0 && !u.expanded["library.catalog"] {
		widgets = append(widgets, u.card(u.heading(u.tr("A model library for all your agents", "Una biblioteca para todos tus agentes")), u.note(u.tr("Add the models you use, give them short names and choose a default. Your selection stays here next time.", "Añade tus modelos, ponles nombres cortos y elige uno inicial. Tu selección seguirá aquí la próxima vez."))))
	} else {
		widgets = append(widgets, u.clientPicker(sharedModelKey, s))
	}
	if !u.expanded["library.catalog"] {
		widgets = append(widgets, u.note(u.tr("Changes apply the next time you open an agent. Already-open agents may need reopening.", "Los cambios se aplican al volver a abrir un agente. Los agentes abiertos pueden necesitar reiniciarse.")))
		widgets = append(widgets, u.pills(u.button("models.import.toggle", u.tr("Import an existing agent selection", "Importar la selección de un agente"), func() { u.expanded["library.import"] = !u.expanded["library.import"] })))
		if u.expanded["library.import"] {
			imports := []layout.Widget{u.note(u.tr("Choose which saved profile to import. You can review before replacing your shared models.", "Elige un perfil guardado para importar. Puedes revisarlo antes de sustituir los modelos compartidos."))}
			for _, option := range []struct{ key, name string }{{"codex", "Codex Desktop"}, {"codex-cli", "Codex CLI"}, {"claude", "Claude Code"}, {"opencode", "OpenCode"}, {"zed", "Zed"}} {
				key, name := option.key, option.name
				imports = append(imports, u.button("models.import."+key, name, func() { u.importModelLibrary(key) }))
			}
			widgets = append(widgets, u.card(imports...))
		}
		if candidate := u.library.pendingImport; candidate != nil {
			names := []string{}
			for _, m := range candidate.Models {
				names = append(names, nativeCodexDisplayName(m))
			}
			widgets = append(widgets, u.card(u.heading(u.tr("Review imported models", "Revisar modelos importados")), u.note(strings.Join(names, " · ")), u.pills(u.button("models.import.accept", u.tr("Use this selection for all agents", "Usar esta selección para todos"), func() {
				u.library.selection = candidate
				if state := u.library.pendingState; state != nil {
					w.mu.Lock()
					w.revision = state.Revision
					w.recoveryRequired = state.RecoveryRequired
					w.warning = state.Warning
					w.err = nil
					w.mu.Unlock()
					u.library.last = ""
					u.library.pendingState = nil
				}
				for _, m := range candidate.Models {
					u.seedClientChoice(sharedModelKey, m)
				}
				u.library.pendingImport = nil
				u.expanded["library.import"] = false
			}), u.button("models.import.cancel", u.tr("Cancel", "Cancelar"), func() { u.library.pendingImport = nil }))))
		}
		widgets = append(widgets, u.pills(u.button("models.images.toggle", u.tr("Image generation for Codex", "Generación de imágenes para Codex"), func() { u.expanded["library.images"] = !u.expanded["library.images"] })))
		if u.expanded["library.images"] {
			widgets = append(widgets, u.clientImagesPanel("codex", u.sharedClientSelection("codex")))
		}
	}
	return u.column(widgets...)
}
func (u *nativeUI) importModelLibrary(key string) {
	u.clientRequest("GET", nativeClientEndpoint(key), nil, func(raw json.RawMessage, err error) {
		if err != nil {
			u.notice = nativeMessage(err.Error(), u.language)
			return
		}
		candidate, err := decodeNativeClientSelection(key, raw, u.models)
		if err != nil {
			u.notice = err.Error()
			return
		}
		if key == "claude" {
			for i := range candidate.Models {
				candidate.Models[i].DefaultReasoning = candidate.Models[i].ClaudeEffort
			}
		}
		u.library.pendingImport, u.library.importSource = candidate, key
		u.library.pendingState = nil
	})
}

func (u *nativeUI) moveSharedModel(id string, delta int) {
	s := u.library.selection
	for i, m := range s.Models {
		if m.Model.ID == id && i+delta >= 0 && i+delta < len(s.Models) {
			s.Models[i], s.Models[i+delta] = s.Models[i+delta], s.Models[i]
			return
		}
	}
}

func (u *nativeUI) sharedModelOrder() []modelInfo {
	result := []modelInfo{}
	for _, choice := range u.library.selection.Models {
		result = append(result, choice.Model)
	}
	return result
}

func (u *nativeUI) setSharedTokenLimits(id string, context, output int) {
	u.setValue(nativeClientField(sharedModelKey, id, "context"), strconv.Itoa(context))
	u.setValue(nativeClientField(sharedModelKey, id, "output"), strconv.Itoa(output))
}

func (u *nativeUI) reviewSavedLibrary() {
	state := u.owner.modelLibrary.snapshot()
	candidate := &nativeClientSelection{Initial: state.Library.DefaultModel, Aliases: map[string]string{}, Mode: "installed"}
	for _, item := range state.Library.Models {
		candidate.Models = append(candidate.Models, nativeModelChoice{Model: modelInfo{ID: item.ID, Name: item.ID, ContextWindow: item.ContextWindow, MaxOutputTokens: item.MaxOutputTokens}, DisplayName: item.DisplayName, DefaultReasoning: item.ReasoningEffort, ReasoningLevels: item.ReasoningLevels, ReasoningCustom: item.ReasoningCustom})
	}
	u.library.pendingImport, u.library.pendingState = candidate, &state
	u.expanded["library.catalog"] = false
}

// Seal frame-driven edits before draining the writer. No later frame can queue
// a save between this barrier and process exit; the last visible edit survives.
func (u *nativeUI) shutdownModelLibrary() {
	u.frameMu.Lock()
	defer u.frameMu.Unlock()
	u.closing = true
	u.flushModelLibrary()
	u.catalogCache.mu.Lock()
	u.catalogCache.mu.Unlock()
}

func (u *nativeUI) sharedModelControls(choice *nativeModelChoice) layout.Widget {
	key, id := sharedModelKey, choice.Model.ID
	prefix := "client:" + key + ":"
	s := u.library.selection
	label := u.tr("Use by default", "Usar por defecto")
	if s.Initial == id {
		label = u.tr("★ Default", "★ Predeterminado")
	}
	children := []layout.Widget{u.pills(u.button(prefix+"initial:"+id, label, func() { s.Initial = id }), u.button(prefix+"edit:"+id, u.tr("Edit", "Editar"), func() { u.expanded[prefix+"edit:"+id] = !u.expanded[prefix+"edit:"+id] }))}
	levels, _ := nativeReasoningFor(*choice)
	if !choice.ReasoningCustom && choice.DefaultReasoning != "" && !helperContains(levels, choice.DefaultReasoning) {
		levels = append(levels, choice.DefaultReasoning)
	}
	if len(levels) > 0 {
		children = append(children, u.selectField(nativeClientField(key, id, "reasoning"), u.tr("Reasoning", "Razonamiento"), levels))
	} else {
		if choice.ReasoningCustom {
			u.setValue(nativeClientField(key, id, "reasoning"), "")
		}
		children = append(children, u.note(u.tr("Reasoning: automatic", "Razonamiento: automático")))
	}
	if u.expanded[prefix+"edit:"+id] {
		children = append(children, u.field(nativeClientField(key, id, "name"), u.tr("Display name", "Nombre visible"), choice.Model.Name, false))
		index := slices.Index(s.ids(), id)
		children = append(children, u.pills(u.disabled(index > 0, u.button(prefix+"up:"+id, u.tr("Move up", "Subir"), func() { u.moveSharedModel(id, -1) })), u.disabled(index < len(s.Models)-1, u.button(prefix+"down:"+id, u.tr("Move down", "Bajar"), func() { u.moveSharedModel(id, 1) }))))
		if u.checked(prefix + "advanced") {
			children = append(children, u.check(nativeClientField(key, id, "custom"), u.tr("Custom reasoning levels", "Niveles de razonamiento personalizados"), func(bool) {}))
			if u.checked(nativeClientField(key, id, "custom")) {
				children = append(children, u.field(nativeClientField(key, id, "levels"), u.tr("Levels separated by commas", "Niveles separados por comas"), "low,medium,high", false))
			}
			children = append(children, u.button(prefix+"suggest:"+id, u.tr("Restore suggested levels", "Restaurar niveles sugeridos"), func() {
				choice.ReasoningCustom = false
				choice.ReasoningLevels = nil
				choice.DefaultReasoning = ""
				u.setChecked(nativeClientField(key, id, "custom"), false)
				_, effort := nativeReasoningFor(*choice)
				u.setValue(nativeClientField(key, id, "reasoning"), effort)
			}), u.field(nativeClientField(key, id, "context"), u.tr("Context tokens", "Tokens de contexto"), "200000", false), u.field(nativeClientField(key, id, "output"), u.tr("Max output tokens", "Tokens máximos de salida"), "0", false))
		}
	}
	return u.column(children...)
}

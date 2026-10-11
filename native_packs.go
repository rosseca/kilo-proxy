//go:build desktop

package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"gioui.org/layout"
)

type nativePackUI struct {
	store         *modelPacksStore
	file          modelPacksFile
	tab, selected string
	nameEditing   string
	pendingDelete string
	saveError     error
}

func (u *nativeUI) packsState() *nativePackUI {
	if u.packs == nil {
		store := newModelPacksStore(u.owner.dir)
		u.packs = &nativePackUI{store: store, file: cloneModelPacksFile(store.value), tab: "library", selected: modelLibraryStartRecommended}
	}
	return u.packs
}

func (u *nativeUI) localizedPackText(value packText) string { return u.tr(value.EN, value.ES) }

// curatedPacks lists the Recommended pack first, then the built-in packs.
func (u *nativeUI) curatedPacks() []curatedModelPack { return curatedModelPacks(u.models) }

func (u *nativeUI) packName(id string) string {
	p := u.packsState()
	if personal := p.file.personalPack(id); personal != nil {
		return personal.Name
	}
	for _, builtin := range u.curatedPacks() {
		if builtin.ID == id {
			return u.localizedPackText(builtin.Name)
		}
	}
	return ""
}

func (u *nativeUI) assignedPackName(key string) string {
	if _, ok := packAgentNames[key]; !ok {
		return ""
	}
	return u.packName(u.packsState().file.Assignments[key])
}

func (u *nativeUI) packCatalogModel(id string) (modelInfo, bool) {
	for _, model := range u.models {
		if model.ID == id {
			return model, true
		}
	}
	return modelInfo{}, false
}

func (u *nativeUI) packLibrary(id string) (modelLibrary, bool) {
	library, err := modelPackLibrary(u.packsState().file, id, u.models)
	return library, err == nil
}

func (u *nativeUI) missingPackModels(id string) []string {
	library, ok := u.packLibrary(id)
	if !ok {
		return nil
	}
	var missing []string
	for _, item := range library.Models {
		if _, exists := u.packCatalogModel(item.ID); !exists {
			missing = append(missing, item.ID)
		}
	}
	return missing
}

// A pack is resolved when the agent is prepared. It never rewrites models.json
// or silently changes another agent's selected models.
func (u *nativeUI) packSelectionForAgent(key string) *nativeClientSelection {
	if _, allowed := packAgentNames[key]; !allowed {
		return nil
	}
	id := u.packsState().file.Assignments[key]
	if id == "" {
		return nil
	}
	library, ok := u.packLibrary(id)
	if !ok {
		return nil
	}
	selection := &nativeClientSelection{Initial: library.DefaultModel, Aliases: map[string]string{}, Mode: "installed"}
	for _, item := range library.Models {
		selection.Models = append(selection.Models, contextChoiceFromLibrary(item, u.contextCatalogModel(item.ID)))
	}
	return selection
}

// Persist a complete candidate before exposing it in this window or to agent
// preparation. An I/O or external-edit failure never starts using an unsaved pack.
func (u *nativeUI) changePacks(change func(*modelPacksFile) error) bool {
	p := u.packsState()
	candidate := cloneModelPacksFile(p.file)
	if err := change(&candidate); err != nil {
		p.saveError = err
		u.noticeError(err)
		return false
	}
	if err := p.store.save(candidate, false); err != nil {
		p.saveError = err
		u.noticeError(err)
		return false
	}
	p.file = candidate
	p.saveError = nil
	u.setNotice(nativeToneSuccess, u.tr("Model packs saved. Reopen the agent to apply changes.", "Packs guardados. Vuelve a abrir el agente para aplicar los cambios."))
	return true
}

func (u *nativeUI) createPersonalPack(basedOn string) {
	var library modelLibrary
	name := u.tr("My pack", "Mi pack")
	roles := map[string]string{}
	if basedOn != "" {
		var builtin *curatedModelPack
		for _, spec := range u.curatedPacks() {
			if spec.ID == basedOn {
				copy := spec
				builtin = &copy
				break
			}
		}
		if builtin == nil {
			u.noticeError(errors.New("Unknown pack template"))
			return
		}
		if len(u.models) == 0 {
			u.noticeError(errors.New(u.tr("Refresh the catalog before copying a pack.", "Actualiza el catálogo antes de copiar un pack.")))
			return
		}
		if missing := u.missingPackModels(basedOn); len(missing) > 0 {
			u.noticeError(errors.New(fmt.Sprintf(u.tr("Pack models unavailable in your team catalog: %s. Refresh the catalog or create a pack from scratch.", "Modelos del pack no disponibles en el catálogo del equipo: %s. Actualiza el catálogo o crea un pack desde cero."), strings.Join(missing, ", "))))
			return
		}
		name = u.tr("My ", "Mi ") + u.localizedPackText(builtin.Name)
		if basedOn == modelLibraryStartRecommended {
			name = u.tr("My recommended", "Mis recomendados")
		}
		library = modelLibrary{SchemaVersion: 1, Models: []modelLibraryItem{}}
		for _, spec := range builtin.Models {
			model, _ := u.packCatalogModel(spec.ID)
			library.Models = append(library.Models, modelLibraryItem{ID: spec.ID, ContextPreset: contextPresetRecommended, MaxOutputTokens: model.MaxOutputTokens})
			if role := u.localizedPackText(spec.Role); role != "" {
				roles[spec.ID] = role
			}
		}
		if len(library.Models) == 0 {
			u.noticeError(errors.New(u.tr("None of this pack's models are in your team's catalog.", "Ningún modelo de este pack figura en el catálogo de tu equipo.")))
			return
		}
		library.DefaultModel = builtin.DefaultModel
		if !slices.ContainsFunc(library.Models, func(item modelLibraryItem) bool { return item.ID == library.DefaultModel }) {
			library.DefaultModel = library.Models[0].ID
		}
	} else {
		library = emptyModelLibrary()
	}
	id, err := newPersonalPackID()
	if err != nil {
		u.noticeError(err)
		return
	}
	if !u.changePacks(func(file *modelPacksFile) error {
		file.Personal = append(file.Personal, personalModelPack{ID: id, Name: name, BasedOn: basedOn, Library: library, Roles: roles})
		return nil
	}) {
		return
	}
	p := u.packsState()
	p.tab, p.selected, p.nameEditing = "mine", id, ""
}

func (u *nativeUI) changePersonalPack(id string, change func(*personalModelPack) error) bool {
	return u.changePacks(func(file *modelPacksFile) error {
		pack := file.personalPack(id)
		if pack == nil {
			return errors.New("Personal pack not found")
		}
		if err := change(pack); err != nil {
			return err
		}
		if len(pack.Library.Models) == 0 {
			for _, assigned := range file.Assignments {
				if assigned == id {
					return errors.New(u.tr("Switch agents back to the shared library before removing the last pack model.", "Devuelve los agentes a la biblioteca compartida antes de quitar el último modelo del pack."))
				}
			}
		}
		return nil
	})
}

func (u *nativeUI) assignPack(id, key string) {
	if _, ok := packAgentNames[key]; !ok {
		u.noticeError(errors.New("Unknown agent"))
		return
	}
	library, ok := u.packLibrary(id)
	if !ok || len(library.Models) == 0 {
		u.noticeError(errors.New(u.tr("Choose a pack with models.", "Elige un pack con modelos.")))
		return
	}
	if len(u.models) == 0 || len(u.missingPackModels(id)) > 0 {
		u.noticeError(errors.New(u.tr("Refresh the catalog and replace unavailable models before assigning this pack.", "Actualiza el catálogo y sustituye los modelos no disponibles antes de asignar este pack.")))
		return
	}
	if key == "claude-desktop" && !slices.ContainsFunc(library.Models, func(item modelLibraryItem) bool {
		return nativeClaudeDesktopModelAllowed(item.ID, u.claudeDesktopExperimentalModels())
	}) {
		u.noticeError(errors.New(u.tr("Claude Desktop needs a supported Claude model in this pack.", "Claude Desktop necesita un modelo Claude compatible en este pack.")))
		return
	}
	u.changePacks(func(file *modelPacksFile) error { file.Assignments[key] = id; return nil })
}

func (u *nativeUI) packReadinessError(key string) error {
	if _, supported := packAgentNames[key]; !supported {
		return nil
	}
	packs := u.packsState()
	if packs.store.recovery || packs.saveError != nil {
		return errors.New(u.tr("Review saved model packs in Models before opening this agent.", "Revisa los packs guardados en Modelos antes de abrir este agente."))
	}
	id := packs.file.Assignments[key]
	if id == "" {
		return nil
	}
	library, ok := u.packLibrary(id)
	if !ok || len(library.Models) == 0 {
		return errors.New(u.tr("The selected model pack is unavailable or empty.", "El pack seleccionado no está disponible o está vacío."))
	}
	if len(u.models) == 0 || len(u.missingPackModels(id)) > 0 {
		return errors.New(u.tr("Refresh the catalog and review missing pack models before opening this agent.", "Actualiza el catálogo y revisa los modelos ausentes del pack antes de abrir este agente."))
	}
	if key == "claude-desktop" && !slices.ContainsFunc(library.Models, func(item modelLibraryItem) bool {
		return nativeClaudeDesktopModelAllowed(item.ID, u.claudeDesktopExperimentalModels())
	}) {
		return errors.New(u.tr("Claude Desktop needs a compatible Claude model in the assigned pack.", "Claude Desktop necesita un modelo Claude compatible en el pack asignado."))
	}
	return nil
}

func (u *nativeUI) clearPackAssignment(key string) {
	u.changePacks(func(file *modelPacksFile) error { delete(file.Assignments, key); return nil })
}

func (u *nativeUI) packStoreNotice() []layout.Widget {
	p := u.packsState()
	if p.store.recovery {
		warning := u.tr("Saved model packs cannot be read. The existing files have not been changed.", "No se pueden leer los packs guardados. Los archivos existentes no se han cambiado.")
		if p.store.recoverable {
			warning = u.tr("The last good model packs backup is shown. Review it before recovering.", "Se muestra la última copia válida de los packs. Revísala antes de recuperarla.")
		}
		widgets := []layout.Widget{u.message(nativeToneWarning, warning)}
		if p.store.recoverable {
			widgets = append(widgets, u.button("packs.recover", u.tr("Recover reviewed packs", "Recuperar packs revisados"), func() {
				if err := p.store.save(p.file, true); err != nil {
					p.saveError = err
					u.noticeError(err)
				} else {
					p.saveError = nil
					u.setNotice(nativeToneSuccess, u.tr("Packs recovered", "Packs recuperados"))
				}
			}))
		}
		return []layout.Widget{u.card(widgets...)}
	}
	if p.saveError != nil {
		return []layout.Widget{u.card(u.message(nativeToneError, p.saveError.Error()), u.button("packs.reload", u.tr("Reload saved packs", "Recargar packs guardados"), func() {
			p.store = newModelPacksStore(u.owner.dir)
			p.file = cloneModelPacksFile(p.store.value)
			p.saveError = nil
		}))}
	}
	return nil
}

// packsPanel is the content of the Packs and My packs tabs.
func (u *nativeUI) packsPanel() layout.Widget { return u.uxmPacksPanel() }

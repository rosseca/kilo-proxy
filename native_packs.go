//go:build desktop

package main

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
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
		selected := ""
		if builtins := builtinModelPacks(); len(builtins) > 0 {
			selected = builtins[0].ID
		}
		u.packs = &nativePackUI{store: store, file: cloneModelPacksFile(store.value), tab: "library", selected: selected}
	}
	return u.packs
}

func (u *nativeUI) localizedPackText(value packText) string { return u.tr(value.EN, value.ES) }

func (u *nativeUI) packContextTokens(tokens int) string {
	if tokens <= 0 {
		return u.tr("unknown", "desconocido")
	}
	value := strconv.Itoa(tokens)
	separator := u.tr(",", ".")
	for i := len(value) - 3; i > 0; i -= 3 {
		value = value[:i] + separator + value[i:]
	}
	return value
}

func (u *nativeUI) packName(id string) string {
	p := u.packsState()
	if personal := p.file.personalPack(id); personal != nil {
		return personal.Name
	}
	for _, builtin := range builtinModelPacks() {
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
		for _, spec := range builtinModelPacks() {
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
		library = modelLibrary{SchemaVersion: 1, Models: []modelLibraryItem{}}
		for _, spec := range builtin.Models {
			model, _ := u.packCatalogModel(spec.ID)
			library.Models = append(library.Models, modelLibraryItem{ID: spec.ID, ContextPreset: contextPresetRecommended, MaxOutputTokens: model.MaxOutputTokens})
			roles[spec.ID] = u.localizedPackText(spec.Role)
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

func packAgentChoices() []nativeChoice {
	return []nativeChoice{
		{Value: "codex", Label: "Codex Desktop"}, {Value: "codex-cli", Label: "Codex CLI"},
		{Value: "claude", Label: "Claude Code"}, {Value: "claude-desktop", Label: "Claude Desktop"},
		{Value: "opencode", Label: "OpenCode"}, {Value: "omp", Label: "Oh My Pi"}, {Value: "zed", Label: "Zed"},
	}
}

func (u *nativeUI) packAssignmentControls(id string) []layout.Widget {
	key := u.value("packs.agent")
	if _, ok := packAgentNames[key]; !ok {
		key = "codex"
	}
	assigned := u.packsState().file.Assignments[key]
	current := u.tr("Shared library", "Biblioteca compartida")
	if assigned != "" {
		current = u.packName(assigned)
		if current == "" {
			current = assigned
		}
	}
	missing := u.missingPackModels(id)
	library, exists := u.packLibrary(id)
	canAssign := exists && len(library.Models) > 0 && len(missing) == 0 && len(u.models) > 0 && !u.packsState().store.recovery && u.packsState().saveError == nil
	widgets := []layout.Widget{
		u.selectField("packs.agent", u.tr("Agent", "Agente"), packAgentChoices()),
		u.note(fmt.Sprintf(u.tr("Current selection: %s", "Selección actual: %s"), current)),
	}
	if len(missing) > 0 {
		widgets = append(widgets, u.message(nativeToneWarning, fmt.Sprintf(u.tr("Unavailable in your catalog: %s. Refresh it or edit a personal copy.", "No disponibles en tu catálogo: %s. Actualízalo o edita una copia propia."), strings.Join(missing, ", "))))
	}
	widgets = append(widgets, u.pills(
		u.disabled(canAssign, u.primaryButton("packs.use."+id, u.tr("Use this pack for this agent", "Usar este pack en este agente"), func() { u.assignPack(id, u.value("packs.agent")) })),
		u.disabled(assigned != "", u.button("packs.shared."+id, u.tr("Use shared library", "Usar biblioteca compartida"), func() { u.clearPackAssignment(u.value("packs.agent")) })),
	), u.note(u.tr("This changes the next preparation or launch, not an already-open agent. Catalog presence does not prove inference protocol compatibility.", "Se aplicará en la siguiente preparación o apertura, no a un agente ya abierto. Aparecer en el catálogo no prueba compatibilidad de inferencia.")))
	return widgets
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

func (u *nativeUI) packsPanel() layout.Widget {
	p := u.packsState()
	widgets := u.packStoreNotice()
	if p.tab == "packs" {
		widgets = append(widgets, u.curatedPacksPanel())
	} else {
		widgets = append(widgets, u.personalPacksPanel())
	}
	return u.column(widgets...)
}

func (u *nativeUI) curatedPacksPanel() layout.Widget {
	p := u.packsState()
	builtins := builtinModelPacks()
	widgets := []layout.Widget{
		u.actionRow(u.column(u.heading(u.tr("Packs by task", "Packs por tarea")), u.note(u.tr("Candidate selections, not a universal quality ranking. Review availability before using one.", "Selecciones candidatas, no un ranking universal de calidad. Revisa la disponibilidad antes de usar uno."))),
			u.button("packs.new", u.tr("Create my pack", "Crear mi pack"), func() { u.createPersonalPack("") })),
	}
	buttons := make([]layout.Widget, 0, len(builtins))
	selected := p.selected
	for _, spec := range builtins {
		id := spec.ID
		buttons = append(buttons, u.buttonKind("packs.pick."+id, u.localizedPackText(spec.Name), nativeButtonSecondary, nil, id == selected, func() { p.selected = id }))
	}
	widgets = append(widgets, u.pills(buttons...))
	var chosen *curatedModelPack
	for _, spec := range builtins {
		if spec.ID == selected {
			copy := spec
			chosen = &copy
			break
		}
	}
	if chosen == nil && len(builtins) > 0 {
		chosen = &builtins[0]
		p.selected = chosen.ID
	}
	if chosen == nil {
		return u.column(widgets...)
	}
	rows := []layout.Widget{
		u.heading(u.localizedPackText(chosen.Name)),
		u.note(u.localizedPackText(chosen.Description)),
		u.message(nativeToneInfo, u.tr("Candidate models; task-specific editorial, design and QA evaluations remain to be done.", "Modelos candidatos; faltan evaluaciones específicas de documentación, diseño y QA.")),
	}
	for _, item := range chosen.Models {
		model, ok := u.packCatalogModel(item.ID)
		name := item.ID
		if ok && model.Name != "" {
			name = model.Name
		}
		status := u.tr("Not in the current catalog", "No figura en el catálogo actual")
		if ok {
			status = fmt.Sprintf(u.tr("%s / %s USD per 1M · context %s tokens", "%s / %s USD por 1M · contexto %s tokens"), nativeTokenPrice(model.InputPrice), nativeTokenPrice(model.OutputPrice), u.packContextTokens(model.ContextWindow))
		}
		rows = append(rows, u.column(u.subheading(u.localizedPackText(item.Role)), u.label(name), u.note(item.ID), u.note(status)))
	}
	if len(chosen.Optional) > 0 {
		labels := make([]string, 0, len(chosen.Optional))
		for _, m := range chosen.Optional {
			labels = append(labels, m.ID)
		}
		rows = append(rows, u.note(u.tr("Optional models you can add to a personal copy: ", "Modelos opcionales que puedes añadir a una copia propia: ")+strings.Join(labels, ", ")))
	}
	rows = append(rows, u.pills(u.disabled(len(u.models) > 0 && len(u.missingPackModels(chosen.ID)) == 0,
		u.button("packs.copy."+chosen.ID, u.tr("Make my editable version", "Crear mi versión editable"), func() { u.createPersonalPack(chosen.ID) }))))
	rows = append(rows, u.note(u.tr("Built-in packs follow app releases. A personal copy keeps your selection unchanged when a built-in changes.", "Los packs preparados siguen las versiones de la app. Una copia propia conserva tu selección aunque cambie el pack preparado.")))
	rows = append(rows, u.packAssignmentControls(chosen.ID)...)
	widgets = append(widgets, u.card(rows...))
	return u.column(widgets...)
}

func (u *nativeUI) personalPacksPanel() layout.Widget {
	p := u.packsState()
	widgets := []layout.Widget{u.actionRow(u.column(u.heading(u.tr("My packs", "Mis packs")), u.note(u.tr("Create from scratch or customize a curated pack. Only your copy changes.", "Crea desde cero o personaliza un pack preparado. Solo cambia tu copia."))),
		u.button("packs.my.new", u.tr("Create empty pack", "Crear pack vacío"), func() { u.createPersonalPack("") }))}
	if len(p.file.Personal) == 0 {
		widgets = append(widgets, u.card(u.note(u.tr("No personal packs yet. Create one or copy a curated pack.", "Aún no hay packs propios. Crea uno o copia un pack preparado."))))
		return u.column(widgets...)
	}
	if p.file.personalPack(p.selected) == nil {
		p.selected = p.file.Personal[0].ID
	}
	buttons := make([]layout.Widget, 0, len(p.file.Personal))
	for _, personal := range p.file.Personal {
		id := personal.ID
		buttons = append(buttons, u.buttonKind("packs.my.pick."+id, personal.Name, nativeButtonSecondary, nil, id == p.selected, func() { p.selected = id }))
	}
	widgets = append(widgets, u.pills(buttons...), u.personalPackEditor(p.selected))
	return u.column(widgets...)
}

func (u *nativeUI) personalPackEditor(id string) layout.Widget {
	p := u.packsState()
	pack := p.file.personalPack(id)
	if pack == nil {
		return u.note(u.tr("Pack not found", "Pack no encontrado"))
	}
	if p.nameEditing != id {
		p.nameEditing = id
		u.setValue("packs.name", pack.Name)
	}
	widgets := []layout.Widget{
		u.heading(pack.Name),
		u.note(u.tr("Your personal copy: add models, remove them, reorder them and change the default without affecting the shared library.", "Tu copia propia: añade, quita y ordena modelos y cambia el predeterminado sin tocar la biblioteca compartida.")),
		u.actionRow(u.field("packs.name", u.tr("Pack name", "Nombre del pack"), u.tr("My pack", "Mi pack"), false),
			u.button("packs.my.rename."+id, u.tr("Save name", "Guardar nombre"), func() {
				name := strings.TrimSpace(u.value("packs.name"))
				u.changePersonalPack(id, func(pack *personalModelPack) error {
					if !validPackText(name, 80) {
						return errors.New(u.tr("Use a pack name of 1–80 characters without control characters.", "Usa un nombre de pack de 1 a 80 caracteres sin caracteres de control."))
					}
					pack.Name = name
					return nil
				})
			})),
	}
	for index, item := range pack.Library.Models {
		index, idModel := index, item.ID
		role := pack.Roles[idModel]
		if role == "" {
			role = u.tr("My model", "Modelo propio")
		}
		name := idModel
		if model, ok := u.packCatalogModel(idModel); ok && model.Name != "" {
			name = model.Name
		}
		controls := []layout.Widget{}
		if pack.Library.DefaultModel != idModel {
			controls = append(controls, u.button("packs.my.default."+idModel, u.tr("Set as default", "Usar por defecto"), func() {
				u.changePersonalPack(id, func(pack *personalModelPack) error { pack.Library.DefaultModel = idModel; return nil })
			}))
		}
		if index > 0 {
			controls = append(controls, u.button("packs.my.up."+idModel, u.tr("Move up", "Subir"), func() {
				u.changePersonalPack(id, func(pack *personalModelPack) error {
					pack.Library.Models[index-1], pack.Library.Models[index] = pack.Library.Models[index], pack.Library.Models[index-1]
					return nil
				})
			}))
		}
		if index < len(pack.Library.Models)-1 {
			controls = append(controls, u.button("packs.my.down."+idModel, u.tr("Move down", "Bajar"), func() {
				u.changePersonalPack(id, func(pack *personalModelPack) error {
					pack.Library.Models[index+1], pack.Library.Models[index] = pack.Library.Models[index], pack.Library.Models[index+1]
					return nil
				})
			}))
		}
		controls = append(controls, u.button("packs.my.remove."+idModel, u.tr("Remove", "Quitar"), func() {
			u.changePersonalPack(id, func(pack *personalModelPack) error {
				pack.Library.Models = slices.DeleteFunc(pack.Library.Models, func(item modelLibraryItem) bool { return item.ID == idModel })
				delete(pack.Roles, idModel)
				if pack.Library.DefaultModel == idModel {
					pack.Library.DefaultModel = ""
					if len(pack.Library.Models) > 0 {
						pack.Library.DefaultModel = pack.Library.Models[0].ID
					}
				}
				return nil
			})
		}))
		rows := []layout.Widget{u.actionRow(u.column(u.subheading(role), u.label(name), u.note(idModel)), u.pills(controls...))}
		roleID := "packs.my.role." + id + "." + idModel
		rows = append(rows, u.disclosure(roleID, u.tr("Edit role", "Editar función")))
		if u.expanded[roleID] {
			field := roleID + ".text"
			if !u.expanded[roleID+".initialized"] {
				u.setValue(field, role)
				u.expanded[roleID+".initialized"] = true
			}
			rows = append(rows, u.actionRow(u.field(field, u.tr("Role in this pack", "Función en este pack"), role, false),
				u.button(roleID+".save", u.tr("Save role", "Guardar función"), func() {
					value := strings.TrimSpace(u.value(field))
					u.changePersonalPack(id, func(pack *personalModelPack) error {
						if !validPackText(value, 80) {
							return errors.New(u.tr("Use a role of 1–80 characters without control characters.", "Usa una función de 1 a 80 caracteres sin caracteres de control."))
						}
						if pack.Roles == nil {
							pack.Roles = map[string]string{}
						}
						pack.Roles[idModel] = value
						return nil
					})
				})))
		}
		widgets = append(widgets, u.column(rows...))
	}
	widgets = append(widgets, u.disclosure("packs.my.add", u.tr("Add model from catalog", "Añadir modelo del catálogo")))
	if u.expanded["packs.my.add"] {
		widgets = append(widgets, u.personalPackCatalogPicker(id))
	}
	widgets = append(widgets, u.packAssignmentControls(id)...)
	widgets = append(widgets, u.button("packs.my.delete."+id, u.tr("Delete my pack", "Eliminar mi pack"), func() { p.pendingDelete = id }))
	if p.pendingDelete == id {
		widgets = append(widgets, u.message(nativeToneWarning, u.tr("Delete this personal pack? Agents using it will return to the shared library.", "¿Eliminar este pack propio? Los agentes que lo usan volverán a la biblioteca compartida.")), u.pills(
			u.button("packs.my.cancel-delete", u.tr("Cancel", "Cancelar"), func() { p.pendingDelete = "" }),
			u.dangerButton("packs.my.confirm-delete", u.tr("Delete pack", "Eliminar pack"), func() {
				if u.changePacks(func(file *modelPacksFile) error {
					file.Personal = slices.DeleteFunc(file.Personal, func(pack personalModelPack) bool { return pack.ID == id })
					for agent, assigned := range file.Assignments {
						if assigned == id {
							delete(file.Assignments, agent)
						}
					}
					return nil
				}) {
					p.pendingDelete = ""
					p.selected = ""
				}
			}),
		))
	}
	return u.card(widgets...)
}

func (u *nativeUI) personalPackCatalogPicker(id string) layout.Widget {
	pack := u.packsState().file.personalPack(id)
	if pack == nil {
		return u.note(u.tr("Pack not found", "Pack no encontrado"))
	}
	query := strings.ToLower(strings.TrimSpace(u.value("packs.my.search")))
	widgets := []layout.Widget{u.actionRow(
		u.modelSearchField("packs.my.search", u.tr("Search by name or exact ID", "Buscar por nombre o ID exacto")),
		u.disabled(!u.busy["POST/api/models"], u.button("packs.my.refresh", u.tr("Refresh catalog", "Actualizar catálogo"), u.refreshModels)),
	)}
	if len(u.models) == 0 {
		return u.column(widgets...)
	}
	if len(pack.Library.Models) >= 50 {
		widgets = append(widgets, u.note(u.tr("This pack already has 50 models. Remove one before adding another.", "Este pack ya tiene 50 modelos. Quita uno antes de añadir otro.")))
		return u.column(widgets...)
	}
	count := 0
	for _, m := range u.models {
		if pack.Library.DefaultModel == m.ID || slices.ContainsFunc(pack.Library.Models, func(item modelLibraryItem) bool { return item.ID == m.ID }) {
			continue
		}
		if query == "" && m.Recommendation == nil {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(m.ID+" "+m.Name), query) {
			continue
		}
		model := m
		widgets = append(widgets, u.actionRow(u.column(u.subheading(model.Name), u.note(model.ID), u.note(nativeModelPriceLabel(u, model))),
			u.button("packs.my.add."+model.ID, u.tr("Add to my pack", "Añadir a mi pack"), func() {
				u.changePersonalPack(id, func(pack *personalModelPack) error {
					if len(pack.Library.Models) >= 50 {
						return errors.New("Choose at most 50 models")
					}
					if slices.ContainsFunc(pack.Library.Models, func(item modelLibraryItem) bool { return item.ID == model.ID }) {
						return nil
					}
					pack.Library.Models = append(pack.Library.Models, modelLibraryItem{ID: model.ID, ContextPreset: contextPresetRecommended, MaxOutputTokens: model.MaxOutputTokens})
					if pack.Library.DefaultModel == "" {
						pack.Library.DefaultModel = model.ID
					}
					if pack.Roles == nil {
						pack.Roles = map[string]string{}
					}
					pack.Roles[model.ID] = u.tr("My model", "Modelo propio")
					return nil
				})
			}),
		))
		count++
		if count >= 8 {
			break
		}
	}
	if count == 0 {
		widgets = append(widgets, u.note(u.tr("No matching catalog models to add. Try another name or refresh the catalog.", "No hay modelos del catálogo coincidentes para añadir. Prueba otro nombre o actualiza el catálogo.")))
	}
	return u.column(widgets...)
}

func nativeModelPriceLabel(u *nativeUI, model modelInfo) string {
	return fmt.Sprintf(u.tr("Input %s · output %s USD / 1M", "Entrada %s · salida %s USD / 1M"), nativeTokenPrice(model.InputPrice), nativeTokenPrice(model.OutputPrice))
}

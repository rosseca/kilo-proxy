//go:build desktop

package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"gioui.org/layout"
)

// The starting point of the shared list: the pack (or the recommended set)
// chosen in onboarding or with "Change to another pack". It is informational;
// editing the list never changes a pack.

// uxmLibrarySnapshot is a complete copy of the shared list, kept so a pack
// switch can be undone.
type uxmLibrarySnapshot struct {
	selection    *nativeClientSelection
	startingPack string
	roles        map[string]string
}

func uxmCloneSelection(s *nativeClientSelection) *nativeClientSelection {
	clone := &nativeClientSelection{Initial: s.Initial, Mode: s.Mode, Aliases: map[string]string{}}
	for alias, id := range s.Aliases {
		clone.Aliases[alias] = id
	}
	for _, m := range s.Models {
		m.ReasoningLevels = slices.Clone(m.ReasoningLevels)
		clone.Models = append(clone.Models, m)
	}
	return clone
}

func uxmCloneRoles(roles map[string]string) map[string]string {
	if len(roles) == 0 {
		return nil
	}
	clone := make(map[string]string, len(roles))
	for id, role := range roles {
		clone[id] = role
	}
	return clone
}

func (u *nativeUI) uxmSnapshotLibrary() *uxmLibrarySnapshot {
	u.syncClientSelection(sharedModelKey, u.library.selection)
	return &uxmLibrarySnapshot{selection: uxmCloneSelection(u.library.selection), startingPack: u.library.startingPack, roles: uxmCloneRoles(u.library.roles)}
}

// uxmRestoreLibrary replaces the shared list with snapshot and reseeds the
// row editors so the next sync reads the restored values.
func (u *nativeUI) uxmRestoreLibrary(snapshot *uxmLibrarySnapshot) {
	u.library.selection = uxmCloneSelection(snapshot.selection)
	u.library.startingPack, u.library.roles = snapshot.startingPack, uxmCloneRoles(snapshot.roles)
	for _, m := range u.library.selection.Models {
		u.seedClientChoice(sharedModelKey, m)
	}
}

// uxmStartingPackName is the localized name of a starting point.
func (u *nativeUI) uxmStartingPackName(id string) string {
	if id == modelLibraryStartRecommended {
		return u.tr("Recommended", "Recomendados")
	}
	if name := u.packName(id); name != "" {
		return name
	}
	return id
}

// uxmStartingCandidate builds the list a starting point would produce: the
// recommended set, or a copy of a pack with its roles and default.
func (u *nativeUI) uxmStartingCandidate(id string) (*uxmLibrarySnapshot, error) {
	candidate := &nativeClientSelection{Aliases: map[string]string{}, Mode: "installed"}
	if id == modelLibraryStartRecommended {
		recommended := u.recommendedModels()
		if len(recommended) == 0 {
			return nil, errors.New(u.tr("Refresh the catalog to see the recommended models.", "Actualiza el catálogo para ver los modelos recomendados."))
		}
		for _, m := range recommended {
			if err := candidate.add(m, 50); err != nil {
				return nil, err
			}
			choice := candidate.choice(m.ID)
			if levels, _ := nativeReasoningFor(*choice); m.Recommendation.Reasoning != "" && helperContains(levels, m.Recommendation.Reasoning) {
				choice.DefaultReasoning = m.Recommendation.Reasoning
			}
			if m.Recommendation.Default {
				candidate.Initial = m.ID
			}
		}
		if candidate.Initial == "" {
			candidate.Initial = candidate.Models[0].Model.ID
		}
		return &uxmLibrarySnapshot{selection: candidate, startingPack: id}, nil
	}
	if len(u.models) == 0 {
		return nil, errors.New(u.tr("Refresh the catalog before using a pack.", "Actualiza el catálogo antes de usar un pack."))
	}
	if missing := u.missingPackModels(id); len(missing) > 0 {
		return nil, fmt.Errorf(u.tr("Not available in your catalog: %s", "No disponible en tu catálogo: %s"), strings.Join(missing, ", "))
	}
	library, ok := u.packLibrary(id)
	if !ok || len(library.Models) == 0 {
		return nil, errors.New(u.tr("Choose a pack with models.", "Elige un pack con modelos."))
	}
	for _, item := range library.Models {
		candidate.Models = append(candidate.Models, contextChoiceFromLibrary(item, u.contextCatalogModel(item.ID)))
	}
	candidate.Initial = library.DefaultModel
	return &uxmLibrarySnapshot{selection: candidate, startingPack: id, roles: uxmCloneRoles(u.uxmPackRoles(id))}, nil
}

// uxmPackRoles returns the localized "what for" label of every pack model.
func (u *nativeUI) uxmPackRoles(id string) map[string]string {
	roles := map[string]string{}
	if personal := u.packsState().file.personalPack(id); personal != nil {
		for model, role := range personal.Roles {
			if strings.TrimSpace(role) != "" {
				roles[model] = role
			}
		}
		return roles
	}
	for _, pack := range builtinModelPacks() {
		if pack.ID == id {
			for _, spec := range pack.Models {
				roles[spec.ID] = u.localizedPackText(spec.Role)
			}
		}
	}
	return roles
}

// uxmApplyStartingPoint replaces the shared list with a starting point. It
// never assigns a pack to an agent. The replaced list is kept for undo.
func (u *nativeUI) uxmApplyStartingPoint(id string, keepUndo bool) bool {
	candidate, err := u.uxmStartingCandidate(id)
	if err != nil {
		u.noticeError(err)
		return false
	}
	var previous *uxmLibrarySnapshot
	if keepUndo {
		previous = u.uxmSnapshotLibrary()
	}
	u.uxmRestoreLibrary(candidate)
	u.library.switchUndo = previous
	return true
}

// uxmSwitchDiff lists the display names removed from and added to the list.
func (u *nativeUI) uxmSwitchDiff(candidate *uxmLibrarySnapshot) (removed, added []string) {
	current := u.library.selection
	for _, m := range current.Models {
		if candidate.selection.choice(m.Model.ID) == nil {
			removed = append(removed, nativeCodexDisplayName(m))
		}
	}
	for _, m := range candidate.selection.Models {
		if current.choice(m.Model.ID) == nil {
			added = append(added, nativeCodexDisplayName(m))
		}
	}
	return removed, added
}

// uxmAddedByUser counts models in the list that are not part of the starting point.
func (u *nativeUI) uxmAddedByUser() int {
	start := u.library.startingPack
	if start == "" {
		return 0
	}
	candidate, err := u.uxmStartingCandidate(start)
	if err != nil {
		return 0
	}
	count := 0
	for _, m := range u.library.selection.Models {
		if candidate.selection.choice(m.Model.ID) == nil {
			count++
		}
	}
	return count
}

// uxmStartingStrip is the ivory "Starting point" strip with its switch menu.
func (u *nativeUI) uxmStartingStrip() layout.Widget {
	start := u.library.startingPack
	if start == "" || len(u.library.selection.Models) == 0 {
		return nil
	}
	name := u.uxmStartingPackName(start)
	title := fmt.Sprintf(u.tr("Pack “%s” · you chose it when you started", "Pack “%s” · lo elegiste al empezar"), name)
	if start == modelLibraryStartRecommended {
		title = u.tr("Recommended models · you chose them when you started", "Modelos recomendados · los elegiste al empezar")
	}
	added := u.uxmAddedByUser()
	description := u.tr("Changes here never touch the pack.", "Los cambios aquí no tocan el pack.")
	if added > 0 {
		description = uxmPlural(added, u.tr("Since then you added 1 model. ", "Desde entonces has añadido 1 modelo. "), u.tr("Since then you added %d models. ", "Desde entonces has añadido %d modelos. ")) + description
	}
	actions := []layout.Widget{}
	if start != modelLibraryStartRecommended && u.packName(start) != "" {
		actions = append(actions, u.uxmLink("models.start.view", u.tr("View pack", "Ver pack"), func() {
			p := u.packsState()
			p.selected, p.tab = start, "packs"
			if p.file.personalPack(start) != nil {
				p.tab = "mine"
			}
		}))
	}
	actions = append(actions, u.uxmPopover("models.switch", 380, true, u.uxmLink("models.switch.toggle", u.tr("Change to another pack…", "Cambiar a otro pack…"), func() { u.uxmToggleMenu("models.switch", "models.switch.toggle") }), u.uxmSwitchMenuItems))
	return u.uxmStrip(u.tr("Starting point", "Punto de partida"), u.uxmTitle(15, title), u.uxmSubtle(13, description), actions...)
}

// uxmStartingOptions lists the starting points: Recommended, then every pack.
func (u *nativeUI) uxmStartingOptions() []string {
	options := []string{modelLibraryStartRecommended}
	for _, pack := range builtinModelPacks() {
		options = append(options, pack.ID)
	}
	for _, pack := range u.packsState().file.Personal {
		options = append(options, pack.ID)
	}
	return options
}

func (u *nativeUI) uxmSwitchMenuItems() []layout.Widget {
	items := []layout.Widget{u.uxmMenuTitle(u.tr("Change your list to another pack", "Cambiar tu lista a otro pack"))}
	for _, id := range u.uxmStartingOptions() {
		id := id
		count := 0
		if id == modelLibraryStartRecommended {
			count = len(u.recommendedModels())
		} else if library, ok := u.packLibrary(id); ok {
			count = len(library.Models)
		}
		trailing := uxmPlural(count, u.tr("1 model", "1 modelo"), u.tr("%d models", "%d modelos"))
		current := id == u.library.startingPack
		if current {
			trailing = u.tr("✓ current", "✓ actual")
		}
		_, err := u.uxmStartingCandidate(id)
		items = append(items, u.uxmMenuItem("models.switch.option."+id, uxmMenuItemSpec{label: u.uxmStartingPackName(id), trailing: trailing, selected: current, disabled: err != nil || current}, func() {
			u.setValue("models.switch.pending", id)
		}))
	}
	items = append(items, u.uxmMenuNote(u.tr("Replaces your whole list. We show what is removed and added first, and you can undo it.", "Sustituye tu lista entera. Antes te enseñamos qué se quita y qué se añade, y puedes deshacerlo.")))
	return items
}

// uxmSwitchPanels are the confirmation (with the diff) and the undo strip.
func (u *nativeUI) uxmSwitchPanels() []layout.Widget {
	widgets := []layout.Widget{}
	if pending := u.value("models.switch.pending"); pending != "" {
		candidate, err := u.uxmStartingCandidate(pending)
		if err != nil {
			u.setValue("models.switch.pending", "")
			u.noticeError(err)
		} else {
			removed, added := u.uxmSwitchDiff(candidate)
			lines := []layout.Widget{u.eyebrow(u.tr("Change your list", "Cambiar tu lista")), u.uxmTitle(16, fmt.Sprintf(u.tr("Replace your list with “%s”?", "¿Sustituir tu lista por “%s”?"), u.uxmStartingPackName(pending)))}
			if len(removed) > 0 {
				lines = append(lines, u.uxmSubtle(13, u.tr("Removed: ", "Se quita: ")+strings.Join(removed, " · ")))
			}
			if len(added) > 0 {
				lines = append(lines, u.uxmSubtle(13, u.tr("Added: ", "Se añade: ")+strings.Join(added, " · ")))
			}
			if len(removed) == 0 && len(added) == 0 {
				lines = append(lines, u.uxmSubtle(13, u.tr("Same models; roles and the default follow the pack.", "Mismos modelos; los papeles y el predeterminado siguen al pack.")))
			}
			lines = append(lines, u.pills(
				u.primaryButton("models.switch.confirm", u.tr("Replace list", "Sustituir lista"), func() {
					u.setValue("models.switch.pending", "")
					if u.uxmApplyStartingPoint(pending, true) {
						u.setNotice(nativeToneSuccess, fmt.Sprintf(u.tr("✓ Your list now comes from “%s”. Applies the next time you open each agent.", "✓ Tu lista ahora viene de “%s”. Se aplica al volver a abrir cada agente."), u.uxmStartingPackName(pending)))
					}
				}),
				u.button("models.switch.cancel", u.tr("Cancel", "Cancelar"), func() { u.setValue("models.switch.pending", "") }),
			))
			widgets = append(widgets, uxmPanel(nativeSurface, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(16).Layout(gtx, u.column(lines...))
			}))
		}
	}
	if undo := u.library.switchUndo; undo != nil {
		widgets = append(widgets, u.uxmStrip(u.tr("List replaced", "Lista sustituida"), u.uxmSubtle(13, u.tr("You can go back to your previous list.", "Puedes volver a tu lista anterior.")), nil,
			u.uxmLink("models.switch.undo", u.tr("Undo", "Deshacer"), func() {
				u.uxmRestoreLibrary(undo)
				u.library.switchUndo = nil
				u.setNotice(nativeToneSuccess, u.tr("✓ Previous list restored.", "✓ Lista anterior restaurada."))
			}),
			u.uxmLink("models.switch.dismiss", u.tr("Dismiss", "Cerrar"), func() { u.library.switchUndo = nil }),
		))
	}
	return widgets
}

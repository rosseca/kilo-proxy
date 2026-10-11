//go:build desktop

package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
)

// My packs: personal copies edited in place.

// uxmPersonalPack returns the selected personal pack, selecting the first one.
func (u *nativeUI) uxmPersonalPack() *personalModelPack {
	p := u.packsState()
	if pack := p.file.personalPack(p.selected); pack != nil {
		return pack
	}
	if len(p.file.Personal) == 0 {
		return nil
	}
	p.selected = p.file.Personal[0].ID
	return &p.file.Personal[0]
}

func (u *nativeUI) uxmPersonalPackList() layout.Widget {
	chosen := u.uxmPersonalPack()
	p := u.packsState()
	rows := []layout.Widget{}
	for _, pack := range p.file.Personal {
		id := pack.ID
		count := uxmPlural(len(pack.Library.Models), u.tr("1 model", "1 modelo"), u.tr("%d models", "%d modelos"))
		subtitle := u.tr("Created from scratch · ", "Creado desde cero · ") + count
		if base := u.packName(pack.BasedOn); pack.BasedOn != "" && base != "" {
			subtitle = fmt.Sprintf(u.tr("Based on %s · ", "Basado en %s · "), base) + count
		}
		rows = append(rows, u.uxmPackListRow("packs.my.pick."+id, pack.Name, subtitle, u.uxmPackUsers(id), chosen != nil && chosen.ID == id, func() { u.packsState().selected = id }))
	}
	rows = append(rows, u.uxmClickRow("packs.my.new", u.tr("New empty pack", "Nuevo pack vacío"), semantic.Button, false, func() { u.createPersonalPack("") }, func(hovered bool) layout.Widget {
		background := nativeIvory
		if hovered {
			background = nativeSurface
		}
		return func(gtx layout.Context) layout.Dimensions {
			return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Inset{Top: 14, Bottom: 14, Left: 20, Right: 16}.Layout(gtx, uxmHStack(8, uxmIcon(nativeIconAdd, 18, nativeInk), u.uxmTitle(16, u.tr("New empty pack", "Nuevo pack vacío"))))
			})
		}
	}))
	return uxmPanel(nativeSurface, uxmStack(rows...))
}

func (u *nativeUI) uxmPersonalColumns() []uxmColumn {
	return []uxmColumn{{width: 40}, {label: u.tr("What for", "Para qué"), weight: 1.3, hideBelow: 560}, {label: u.tr("Model", "Modelo"), weight: 2}, {label: u.tr("Context", "Contexto"), width: 90, hideBelow: 600}, {label: u.tr("Price · 1M", "Precio · 1M"), width: 110, hideBelow: 500}, {width: 48}}
}

// uxmChangePackModels edits the model list of a personal pack and saves it.
func (u *nativeUI) uxmMovePackModel(id, model string, delta int) {
	u.changePersonalPack(id, func(pack *personalModelPack) error {
		index := slices.IndexFunc(pack.Library.Models, func(item modelLibraryItem) bool { return item.ID == model })
		next := index + delta
		if index < 0 || next < 0 || next >= len(pack.Library.Models) {
			return nil
		}
		pack.Library.Models[index], pack.Library.Models[next] = pack.Library.Models[next], pack.Library.Models[index]
		return nil
	})
}

func (u *nativeUI) uxmRemovePackModel(id, model string) {
	u.changePersonalPack(id, func(pack *personalModelPack) error {
		pack.Library.Models = slices.DeleteFunc(pack.Library.Models, func(item modelLibraryItem) bool { return item.ID == model })
		delete(pack.Roles, model)
		if pack.Library.DefaultModel == model {
			pack.Library.DefaultModel = ""
			if len(pack.Library.Models) > 0 {
				pack.Library.DefaultModel = pack.Library.Models[0].ID
			}
		}
		return nil
	})
}

func (u *nativeUI) uxmAddPackModel(id string, m modelInfo) bool {
	if _, ok := u.packCatalogModel(m.ID); !ok {
		u.setNotice(nativeToneWarning, u.tr("Not available in your catalog. Refresh it or check the exact ID.", "No disponible en tu catálogo. Actualízalo o revisa el ID exacto."))
		return false
	}
	return u.changePersonalPack(id, func(pack *personalModelPack) error {
		if slices.ContainsFunc(pack.Library.Models, func(item modelLibraryItem) bool { return item.ID == m.ID }) {
			return nil
		}
		if len(pack.Library.Models) >= uxmCatalogLimit {
			return fmt.Errorf(u.tr("A pack holds up to %d models. Remove one before adding another.", "Un pack admite hasta %d modelos. Quita uno antes de añadir otro."), uxmCatalogLimit)
		}
		pack.Library.Models = append(pack.Library.Models, modelLibraryItem{ID: m.ID, ContextPreset: contextPresetRecommended, MaxOutputTokens: m.MaxOutputTokens})
		if pack.Library.DefaultModel == "" {
			pack.Library.DefaultModel = m.ID
		}
		if pack.Roles == nil {
			pack.Roles = map[string]string{}
		}
		pack.Roles[m.ID] = u.tr("My model", "Modelo propio")
		return nil
	})
}

// uxmPackCatalogTarget lets the shared catalog edit a personal pack.
func (u *nativeUI) uxmPackCatalogTarget(id string) (uxmCatalogTarget, bool) {
	pack := u.packsState().file.personalPack(id)
	if pack == nil {
		return uxmCatalogTarget{}, false
	}
	selection := &nativeClientSelection{Initial: pack.Library.DefaultModel, Aliases: map[string]string{}, Mode: "installed"}
	for _, item := range pack.Library.Models {
		m := modelInfo{ID: item.ID, Name: item.ID}
		if found, ok := u.packCatalogModel(item.ID); ok {
			m = found
		}
		selection.Models = append(selection.Models, nativeModelChoice{Model: m})
	}
	return uxmCatalogTarget{
		prefix: "packs.my.", selection: selection, limit: uxmCatalogLimit,
		countText: u.tr("%d in this pack", "%d en este pack"),
		add:       func(m modelInfo) bool { return u.uxmAddPackModel(id, m) },
		remove:    func(model string) { u.uxmRemovePackModel(id, model) },
		done:      func() { u.expanded["packs.my.add"] = false },
		doneText:  u.tr("Done", "Listo"),
		title:     fmt.Sprintf(u.tr("Add models to “%s”", "Añadir modelos a “%s”"), pack.Name),
	}, true
}

func (u *nativeUI) uxmSavePackName(id string) {
	name := strings.TrimSpace(u.value("packs.name"))
	if u.changePersonalPack(id, func(pack *personalModelPack) error {
		if !validPackText(name, 80) {
			return errors.New(u.tr("Use a pack name of 1–80 characters without control characters.", "Usa un nombre de pack de 1 a 80 caracteres sin caracteres de control."))
		}
		pack.Name = name
		return nil
	}) {
		u.expanded["packs.my.renaming"] = false
	}
}

func (u *nativeUI) uxmDeletePersonalPack(id string) {
	p := u.packsState()
	if u.changePacks(func(file *modelPacksFile) error {
		file.Personal = slices.DeleteFunc(file.Personal, func(pack personalModelPack) bool { return pack.ID == id })
		for agent, assigned := range file.Assignments {
			if assigned == id {
				delete(file.Assignments, agent)
			}
		}
		return nil
	}) {
		p.pendingDelete, p.selected = "", ""
	}
}

func (u *nativeUI) uxmPersonalPackDetail() layout.Widget {
	p := u.packsState()
	pack := u.uxmPersonalPack()
	if pack == nil {
		return uxmPanel(nativeSurface, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(24).Layout(gtx, uxmStack(u.uxmTitle(18, u.tr("No personal packs yet", "Aún no hay packs propios")), uxmGap(6), u.uxmSubtle(14, u.tr("Create an empty one, or copy a pack in Packs with “Copy and edit”.", "Crea uno vacío o copia un pack en Packs con «Copiar y editar»."))))
		})
	}
	id := pack.ID
	if p.nameEditing != id {
		p.nameEditing = id
		u.setValue("packs.name", pack.Name)
		u.expanded["packs.my.renaming"] = false
	}
	startRename := func() {
		u.setValue("packs.name", pack.Name)
		u.expanded["packs.my.renaming"] = true
	}
	title := uxmHStack(8, u.textStyle(26, pack.Name, nativeInk, font.Bold), u.uxmIconButton("packs.my.rename-toggle."+id, u.tr("Rename pack", "Renombrar pack"), uxmIconEdit, 18, false, startRename))
	if u.expanded["packs.my.renaming"] {
		title = u.actionRow(u.field("packs.name", u.tr("Pack name", "Nombre del pack"), u.tr("My pack", "Mi pack"), false),
			u.primaryButton("packs.my.rename."+id, u.tr("Save name", "Guardar nombre"), func() { u.uxmSavePackName(id) }),
			u.button("packs.my.rename-cancel."+id, u.tr("Cancel", "Cancelar"), func() { u.expanded["packs.my.renaming"] = false }))
	}
	menuID := "packs.my.menu"
	menu := u.uxmPopover(menuID, 260, true, u.uxmMoreButton(menuID, u.tr("Pack actions", "Acciones del pack")), func() []layout.Widget {
		return []layout.Widget{
			u.uxmMenuItem("packs.my.menu-rename."+id, uxmMenuItemSpec{icon: uxmIconEdit, label: u.tr("Rename pack", "Renombrar pack")}, startRename),
			u.uxmMenuItem("packs.my.duplicate."+id, uxmMenuItemSpec{icon: uxmIconCopy, label: u.tr("Duplicate", "Duplicar")}, func() { u.uxmDuplicatePersonalPack(id) }),
			uxmMenuSeparator,
			u.uxmMenuItem("packs.my.delete."+id, uxmMenuItemSpec{icon: nativeIconClose, label: u.tr("Delete pack", "Eliminar pack"), danger: true}, func() { p.pendingDelete = id }),
		}
	})
	subtitle := u.tr("Created from scratch. Only this pack changes; your main list is untouched.", "Creado desde cero. Solo cambia aquí; tu lista principal no se toca.")
	if base := u.packName(pack.BasedOn); pack.BasedOn != "" && base != "" {
		subtitle = fmt.Sprintf(u.tr("Your copy of “%s”. Only this pack changes; your main list is untouched.", "Tu copia de “%s”. Solo cambia aquí; tu lista principal no se toca."), base)
	}
	header := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Flexed(1, u.uxmPackHeader(title, u.textStyle(15, subtitle, nativeText, font.Normal))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 22, Right: 24}.Layout(gtx, menu)
			}),
		)
	}
	body := []layout.Widget{header}
	if p.pendingDelete == id {
		body = append(body, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: 24, Right: 24, Bottom: 16}.Layout(gtx, u.column(
				u.message(nativeToneWarning, u.tr("Delete this personal pack? Harnesses using it will return to your models.", "¿Eliminar este pack propio? Los harnesses que lo usan volverán a tus modelos.")),
				u.pills(u.dangerButton("packs.my.confirm-delete", u.tr("Delete pack", "Eliminar pack"), func() { u.uxmDeletePersonalPack(id) }),
					u.button("packs.my.cancel-delete", u.tr("Cancel", "Cancelar"), func() { p.pendingDelete = "" })),
			))
		})
	}
	body = append(body, uxmHRule(nativeLine, nativeInk), u.uxmPersonalTable(pack))
	if missing := u.missingPackModels(id); len(missing) > 0 && len(u.models) > 0 {
		body = append(body, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(16).Layout(gtx, u.message(nativeToneWarning, fmt.Sprintf(u.tr("Unavailable in your catalog: %s. Remove them or refresh the catalog.", "No disponibles en tu catálogo: %s. Quítalos o actualiza el catálogo."), strings.Join(missing, ", "))))
		})
	}
	body = append(body, u.uxmPackFooter(id, u.uxmUseMenu(id)))
	return uxmPanel(nativeSurface, uxmStack(body...))
}

func (u *nativeUI) uxmPersonalTable(pack *personalModelPack) layout.Widget {
	id := pack.ID
	columns := u.uxmPersonalColumns()
	rows := []layout.Widget{u.uxmTableHeader(columns)}
	if len(pack.Library.Models) == 0 {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 14, Bottom: 14, Left: 18, Right: 18}.Layout(gtx, u.uxmSubtle(14, u.tr("This pack has no models yet.", "Este pack aún no tiene modelos.")))
		})
	}
	for index, item := range pack.Library.Models {
		index, model := index, item.ID
		isDefault := pack.Library.DefaultModel == model
		star := u.uxmIconButton("packs.my.default."+model, u.tr("Use by default", "Usar por defecto"), nativeIconStarOutline, 18, false, func() {
			u.changePersonalPack(id, func(pack *personalModelPack) error { pack.Library.DefaultModel = model; return nil })
		})
		if isDefault {
			star = func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(4).Layout(gtx, uxmIcon(nativeIconStar, 18, nativeInk))
			}
		}
		role := pack.Roles[model]
		if role == "" {
			role = u.tr("My model", "Modelo propio")
		}
		roleID := "packs.my.role." + id + "." + model
		roleCell := u.uxmDashedLink(roleID, u.tr("Change what it is for: ", "Cambiar para qué: ")+role, role, func() {
			u.expanded[roleID] = !u.expanded[roleID]
			u.setValue(roleID+".text", role)
		})
		rowMenu := "packs.my.row." + model
		name := model
		if m, ok := u.packCatalogModel(model); ok {
			name = uxmSetupModelName(m)
		}
		count := len(pack.Library.Models)
		more := u.uxmPopover(rowMenu, 240, true, u.uxmMoreButton(rowMenu, u.tr("More actions for ", "Más acciones para ")+name), func() []layout.Widget {
			return []layout.Widget{
				u.uxmMenuItem("packs.my.up."+model, uxmMenuItemSpec{icon: uxmIconUp, label: u.tr("Move up", "Subir"), disabled: index == 0}, func() { u.uxmMovePackModel(id, model, -1) }),
				u.uxmMenuItem("packs.my.down."+model, uxmMenuItemSpec{icon: uxmIconDown, label: u.tr("Move down", "Bajar"), disabled: index >= count-1}, func() { u.uxmMovePackModel(id, model, 1) }),
				uxmMenuSeparator,
				u.uxmMenuItem("packs.my.remove."+model, uxmMenuItemSpec{icon: nativeIconClose, label: u.tr("Remove from the pack", "Quitar del pack"), danger: true}, func() { u.uxmRemovePackModel(id, model) }),
			}
		})
		rows = append(rows, u.uxmTableRow(columns, isDefault, nativeSurface, star, roleCell, u.uxmPackModelCell(model, isDefault),
			u.textStyle(14, u.uxmPackContext(model), nativeInk, font.Normal),
			u.textStyle(14, u.uxmPackPrice(model), nativeInk, font.Medium), more))
		if u.expanded[roleID] {
			field := roleID + ".text"
			rows = append(rows, func(gtx layout.Context) layout.Dimensions {
				return nativeBox(gtx, nativeIvory, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Top: 12, Bottom: 12, Left: 58, Right: 18}.Layout(gtx, u.actionRow(u.field(field, u.tr("What it is for in this pack", "Para qué sirve en este pack"), role, false),
						u.button(roleID+".save", u.tr("Save", "Guardar"), func() {
							value := strings.TrimSpace(u.value(field))
							if u.changePersonalPack(id, func(pack *personalModelPack) error {
								if !validPackText(value, 80) {
									return errors.New(u.tr("Use a label of 1–80 characters without control characters.", "Usa una etiqueta de 1 a 80 caracteres sin caracteres de control."))
								}
								if pack.Roles == nil {
									pack.Roles = map[string]string{}
								}
								pack.Roles[model] = value
								return nil
							}) {
								u.expanded[roleID] = false
							}
						})))
				})
			}, uxmHRule(1, nativeInk))
		}
	}
	rows = append(rows, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 12, Bottom: 12, Left: 18, Right: 18}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			add := u.iconButton("packs.my.add", u.tr("Add models", "Añadir modelos"), nativeButtonLink, nativeIconAdd, func() {
				u.expanded["packs.my.add"] = true
				if len(u.models) == 0 {
					u.refreshModels()
				}
			})
			hint := u.uxmSubtle(13, u.tr("Click “What for” to change the label · ☆ for the default", "Pulsa en “Para qué” para cambiar la etiqueta · ☆ para el predeterminado"))
			if gtx.Constraints.Max.X < gtx.Dp(560) {
				return uxmStack(uxmHStack(0, add, nil), uxmGap(6), hint)(gtx)
			}
			return uxmHStack(16, add, nil, hint)(gtx)
		})
	})
	return uxmStack(rows...)
}

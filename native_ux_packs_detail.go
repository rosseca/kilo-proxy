//go:build desktop

package main

import (
	"fmt"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
)

// uxmPacksPanel is the content of the Packs and My packs tabs.
func (u *nativeUI) uxmPacksPanel() layout.Widget {
	p := u.packsState()
	widgets := u.packStoreNotice()
	if len(u.models) == 0 {
		widgets = append(widgets, u.message(nativeToneInfo, u.tr("Refresh the catalog to check pack models, prices and availability.", "Actualiza el catálogo para comprobar los modelos, precios y disponibilidad de los packs.")))
	}
	if p.tab == "mine" {
		if target, ok := u.uxmPackCatalogTarget(p.selected); ok && u.expanded["packs.my.add"] {
			widgets = append(widgets, u.uxmCatalogPageFor(target))
		} else {
			u.expanded["packs.my.add"] = false
			widgets = append(widgets, uxmPacksSplit(u.uxmPersonalPackList(), u.uxmPersonalPackDetail()))
		}
	} else {
		widgets = append(widgets, uxmPacksSplit(u.uxmCuratedPackList(), u.uxmCuratedPackDetail()))
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, uxmSpaced(16, widgets)...)
	}
}

// uxmCuratedPack returns the selected built-in pack, selecting the first one.
func (u *nativeUI) uxmCuratedPack() *curatedModelPack {
	p := u.packsState()
	builtins := u.curatedPacks()
	for i := range builtins {
		if builtins[i].ID == p.selected {
			return &builtins[i]
		}
	}
	if len(builtins) == 0 {
		return nil
	}
	p.selected = builtins[0].ID
	return &builtins[0]
}

func (u *nativeUI) uxmCuratedPackList() layout.Widget {
	chosen := u.uxmCuratedPack()
	rows := []layout.Widget{}
	for _, pack := range u.curatedPacks() {
		id := pack.ID
		rows = append(rows, u.uxmPackListRow("packs.pick."+id, u.localizedPackText(pack.Name), u.localizedPackText(pack.Description), u.uxmPackUsers(id), chosen != nil && chosen.ID == id, func() { u.packsState().selected = id }))
	}
	return uxmPanel(nativeSurface, uxmStack(rows...))
}

func (u *nativeUI) uxmCuratedColumns() []uxmColumn {
	return []uxmColumn{{width: 40}, {label: u.tr("What for", "Para qué"), weight: 1.3, hideBelow: 560}, {label: u.tr("Model", "Modelo"), weight: 2}, {label: u.tr("Context", "Contexto"), width: 90, hideBelow: 580}, {label: u.tr("Price · 1M", "Precio · 1M"), width: 110}}
}

func (u *nativeUI) uxmCuratedPackDetail() layout.Widget {
	chosen := u.uxmCuratedPack()
	if chosen == nil {
		return u.note(u.tr("No packs are available in this version.", "No hay packs disponibles en esta versión."))
	}
	pack := *chosen
	columns := u.uxmCuratedColumns()
	rows := []layout.Widget{u.uxmTableHeader(columns)}
	if len(pack.Models) == 0 {
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(18).Layout(gtx, u.note(u.tr("None of the recommended models are in your team catalog yet. Refresh the catalog to check again.", "Ninguno de los modelos recomendados figura todavía en el catálogo de tu equipo. Actualiza el catálogo para volver a comprobarlo.")))
		})
	}
	for _, spec := range pack.Models {
		isDefault := spec.ID == pack.DefaultModel
		star := uxmIcon(nativeIconStarOutline, 18, nativeInk)
		if isDefault {
			star = uxmIcon(nativeIconStar, 18, nativeInk)
		}
		rows = append(rows, u.uxmTableRow(columns, isDefault, nativeSurface,
			star,
			u.textStyle(14, u.localizedPackText(spec.Role), nativeInk, font.Normal),
			u.uxmPackModelCell(spec.ID, isDefault),
			u.textStyle(14, u.uxmPackContext(spec.ID), nativeInk, font.Normal),
			u.textStyle(14, u.uxmPackPrice(spec.ID), nativeInk, font.Medium),
		))
	}
	if len(pack.Optional) > 0 {
		chips := []layout.Widget{u.uxmSubtle(14, u.tr("Optional:", "Opcional:"))}
		for _, spec := range pack.Optional {
			m := u.uxmSetupPackModel(spec.ID)
			label := uxmSetupModelName(m)
			if price := u.uxmPrice(m); price != "—" {
				label += " · " + price
			}
			chips = append(chips, u.uxmChip("", label, false, nil))
		}
		chips = append(chips, u.uxmSubtle(14, u.tr("— you can add it in your copy", "— puedes añadirlo en tu copia")))
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 12, Bottom: 12, Left: 18, Right: 18}.Layout(gtx, u.pills(chips...))
		})
	}
	copyButton := u.disabled(len(pack.Models) > 0 && len(u.models) > 0 && len(u.missingPackModels(pack.ID)) == 0, u.button("packs.copy."+pack.ID, u.tr("Copy and edit", "Copiar y editar"), func() { u.createPersonalPack(pack.ID) }))
	caption := u.tr("Candidate selection, not a ranking. Built-in packs update with the app; to change something, make your copy.", "Selección candidata, no un ranking. Los packs preparados se actualizan con la app; si quieres cambiar algo, haz tu copia.")
	if pack.ID == modelLibraryStartRecommended {
		caption = u.tr("Follows the published recommendations and refreshes with your catalog; to change something, make your copy.", "Sigue las recomendaciones publicadas y se actualiza con tu catálogo; si quieres cambiar algo, haz tu copia.")
	}
	header := u.uxmPackHeader(u.textStyle(26, u.localizedPackText(pack.Name), nativeInk, font.Bold),
		u.textStyle(15, u.localizedPackText(pack.Description), nativeText, font.Normal),
		u.uxmSubtle(13, caption),
	)
	body := []layout.Widget{header, uxmHRule(nativeLine, nativeInk), uxmStack(rows...)}
	if missing := u.missingPackModels(pack.ID); len(missing) > 0 && len(u.models) > 0 {
		body = append(body, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(16).Layout(gtx, u.message(nativeToneWarning, fmt.Sprintf(u.tr("Unavailable in your catalog: %s. Refresh it or edit a personal copy.", "No disponibles en tu catálogo: %s. Actualízalo o edita una copia propia."), strings.Join(missing, ", "))))
		})
	}
	body = append(body, u.uxmPackFooter(pack.ID, copyButton, u.uxmUseMenu(pack.ID)))
	return uxmPanel(nativeSurface, uxmStack(body...))
}

//go:build desktop

package main

import (
	"fmt"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
)

// setupModelsStep keeps the first model choice to one decision: start with
// the published recommendations or pick models yourself. Reasoning, context
// windows and short names stay on the Models page.
func (u *nativeUI) setupModelsStep() layout.Widget {
	s := u.library.selection
	recommended := u.recommendedModels()
	if u.expanded["library.catalog"] {
		count := len(s.Models)
		tone := nativeToneWarning
		if count > 0 {
			tone = nativeToneSuccess
		}
		badge := u.statusBadge(tone, fmt.Sprintf(u.tr("%d selected", "%d seleccionados"), count))
		return u.column(u.section(u.tr("Choose your models", "Elige tus modelos"), u.tr("Your agents will share this model list.", "Tus agentes compartirán esta lista de modelos."), badge), u.modelsPanel())
	}
	if len(s.Models) == 0 {
		if len(recommended) > 0 {
			return u.setupRecommendedStart(recommended)
		}
		return u.section(u.tr("Choose your models", "Elige tus modelos"), u.tr("Add the models your agents will share. You can change them any time.", "Añade los modelos que compartirán tus agentes. Puedes cambiarlos cuando quieras."),
			u.pills(u.iconButton("setup.models.choose", u.tr("Add models", "Añadir modelos"), nativeButtonPrimary, nativeIconAdd, u.openSetupCatalog)))
	}
	return u.setupChosenModels()
}

func (u *nativeUI) openSetupCatalog() {
	u.expanded["library.catalog"] = true
	if len(u.models) == 0 {
		u.refreshModels()
	}
}

// setupUseRecommended adds the whole list and advances once the library is saved.
func (u *nativeUI) setupUseRecommended() {
	u.addRecommendedModels(u.recommendedModels())
	// Queue the write now so setup waits for this selection, not an older save.
	u.persistLibraryEdits()
	u.setupContinueWhenSaved = true
}

// setupRecommendedStart shows the recommendations as one compact, read-only
// list: a single primary action accepts them, a link opens the full catalog.
func (u *nativeUI) setupRecommendedStart(models []modelInfo) layout.Widget {
	defaultName := ""
	rows := []layout.Widget{}
	for i, m := range models {
		name := m.Name
		if name == "" {
			name = m.ID
		}
		if m.Recommendation.Default && defaultName == "" {
			defaultName = name
		}
		note := m.Recommendation.NoteEN
		if u.language == "es" && m.Recommendation.NoteES != "" {
			note = m.Recommendation.NoteES
		}
		meta := modelLabLabel(modelLab(m))
		if note != "" {
			meta += " · " + note
		}
		title := u.subheading(name)
		if m.Recommendation.Default {
			title = u.pills(u.subheading(name), u.libraryDefaultBadge())
		}
		price := u.textStyle(13, nativeTokenPrice(m.InputPrice)+" / "+nativeTokenPrice(m.OutputPrice)+u.tr(" per 1M", " por 1M"), nativeTextSubtle, font.Medium)
		if i > 0 {
			rows = append(rows, func(gtx layout.Context) layout.Dimensions { return nativeRule(gtx, 1) })
		}
		rows = append(rows, nativeSetupRow(u.actionRow(u.column(title, u.note(meta)), price)))
	}
	if defaultName == "" && len(models) > 0 {
		defaultName = models[0].Name
	}
	summary := fmt.Sprintf(u.tr("%d models · Default: %s", "%d modelos · Predeterminado: %s"), len(models), defaultName)
	list := u.card(u.column(append([]layout.Widget{u.eyebrow(u.tr("Recommended to start", "Recomendados para empezar")), u.subheading(summary)}, rows...)...))
	return u.section(u.tr("Choose your models", "Elige tus modelos"), u.tr("Start with the recommended set. You can add, remove or tune models later on the Models page.", "Empieza con la selección recomendada. Luego puedes añadir, quitar o ajustar modelos en la página Modelos."),
		list,
		u.pills(u.buttonKind("setup.models.choose", u.tr("I'd rather choose them myself", "Prefiero elegirlos yo"), nativeButtonLink, nil, false, u.openSetupCatalog)),
	)
}

// setupChosenModels lists the selection with one way to pick the default:
// the star on its row. Fine tuning stays on the Models page.
func (u *nativeUI) setupChosenModels() layout.Widget {
	s := u.library.selection
	rows := []layout.Widget{}
	for i, choice := range s.Models {
		id := choice.Model.ID
		action := u.libraryDefaultBadge()
		if s.Initial != id {
			action = u.iconButton("setup.default:"+id, u.tr("Use by default", "Usar por defecto"), nativeButtonLink, nativeIconStarOutline, func() { s.Initial = id })
		}
		remove := u.buttonKind("setup.remove:"+id, u.tr("Remove", "Quitar"), nativeButtonLink, nil, false, func() { s.remove(id) })
		meta := []string{modelLabLabel(modelLab(choice.Model)), id}
		if i > 0 {
			rows = append(rows, func(gtx layout.Context) layout.Dimensions { return nativeRule(gtx, 1) })
		}
		rows = append(rows, nativeSetupRow(u.actionRow(u.column(u.subheading(nativeCodexDisplayName(choice)), u.note(strings.Join(meta, " · "))), action, remove)))
	}
	heading := fmt.Sprintf(u.tr("Your models (%d)", "Tus modelos (%d)"), len(s.Models))
	if len(s.Models) == 1 {
		heading = u.tr("Your model (1)", "Tu modelo (1)")
	}
	return u.section(u.tr("Choose your models", "Elige tus modelos"), u.tr("The starred model opens first in every agent.", "El modelo con estrella es el que abre primero cada agente."),
		u.card(u.column(append([]layout.Widget{u.subheading(heading)}, rows...)...)),
		u.pills(u.iconButton("setup.models.add-more", u.tr("Add more models", "Añadir más modelos"), nativeButtonLink, nativeIconAdd, u.openSetupCatalog)),
		u.note(u.tr("Reasoning, context window and short names can be tuned later on the Models page.", "El razonamiento, la ventana de contexto y los nombres cortos se ajustan después en la página Modelos.")),
	)
}

// nativeSetupRow gives a list row its vertical breathing room between rules.
func nativeSetupRow(row layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 10, Bottom: 10}.Layout(gtx, row)
	}
}

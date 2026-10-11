//go:build desktop

package main

import (
	"fmt"
	"image/color"
	"math"

	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/unit"
)

// setupModelsStep is the onboarding choice of a starting point: the
// recommended set or one task pack becomes the shared model list. The catalog
// stays one link away, and a list that already exists can be kept and tuned.
func (u *nativeUI) setupModelsStep() layout.Widget {
	s := u.library.selection
	if u.uxmSetupShowsCatalog() {
		return u.uxmCatalogPage()
	}
	start := u.uxmSetupStart()
	widgets := []layout.Widget{}
	if len(u.models) == 0 {
		widgets = append(widgets, u.message(nativeToneInfo, u.tr("Loading your catalog…", "Cargando tu catálogo…")))
	}
	if len(s.Models) > 0 {
		widgets = append(widgets, u.uxmSetupGroup(u.tr("Your list", "Tu lista"), u.uxmSetupOption("current", start == "current", true, u.uxmSetupCurrentHeader(), u.uxmSetupCurrentTable())))
	}
	widgets = append(widgets, u.uxmSetupGroup(u.tr("General", "General"), u.uxmSetupRecommendedOption(start)))
	if packs := builtinModelPacks(); len(packs) > 0 {
		heading := u.tr("Task packs", "Packs por tarea")
		if size := uxmSetupPackSize(packs); size > 0 {
			heading += fmt.Sprintf(u.tr(" · %d models each, each with its role", " · %d modelos cada uno, cada uno con su papel"), size)
		}
		rows := []layout.Widget{}
		for i, pack := range packs {
			if i > 0 {
				rows = append(rows, uxmHRule(1, nativeInk))
			}
			rows = append(rows, u.uxmSetupPackOption(pack, start))
		}
		widgets = append(widgets, u.uxmSetupGroup(heading, uxmStack(rows...)))
	}
	widgets = append(widgets,
		u.uxmSubtle(13, u.tr("Packs are candidate selections, not a ranking. Later, in Models → Packs, you can give each harness a different pack.", "Los packs son selecciones candidatas, no un ranking. Más adelante, en Modelos → Packs, puedes dar un pack distinto a cada harness.")),
		uxmHStack(0, u.uxmLink("setup.models.choose", u.tr("I'd rather choose them myself from the catalog", "Prefiero elegirlos yo del catálogo"), u.openSetupCatalog), nil),
	)
	body := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, uxmSpaced(18, widgets)...)
	}
	return u.section(u.tr("Which models do you start with?", "¿Con qué modelos empiezas?"), u.tr("Choose a starting point. It becomes your model list, and you can change it whenever you want.", "Elige un punto de partida. Se convierte en tu lista de modelos y luego puedes cambiarla cuando quieras."), body)
}

// uxmSetupGroup is a mono heading over a bordered list of options.
func (u *nativeUI) uxmSetupGroup(heading string, content layout.Widget) layout.Widget {
	return uxmStack(u.eyebrow(heading), uxmGap(8), uxmPanel(nativeSurface, content))
}

// uxmSetupShowsCatalog reports whether the step shows the catalog: on request,
// or when no starting point can be used with this team's catalog.
func (u *nativeUI) uxmSetupShowsCatalog() bool {
	if u.expanded["library.catalog"] {
		return true
	}
	return len(u.library.selection.Models) == 0 && len(u.models) > 0 && u.uxmSetupStart() == ""
}

func (u *nativeUI) openSetupCatalog() {
	u.expanded["library.catalog"] = true
	if len(u.models) == 0 {
		u.refreshModels()
	}
}

// uxmSetupStartUsable reports whether a starting point can be applied now.
func (u *nativeUI) uxmSetupStartUsable(id string) bool {
	if id == "current" {
		return len(u.library.selection.Models) > 0
	}
	_, err := u.uxmStartingCandidate(id)
	return err == nil
}

// uxmSetupStart is the selected starting point: the explicit choice when it is
// still usable, else the current list, the recommended set or the first pack.
func (u *nativeUI) uxmSetupStart() string {
	if id := u.value("setup.models.start"); id != "" && u.uxmSetupStartUsable(id) {
		return id
	}
	options := []string{"current", modelLibraryStartRecommended}
	for _, pack := range builtinModelPacks() {
		options = append(options, pack.ID)
	}
	for _, id := range options {
		if u.uxmSetupStartUsable(id) {
			return id
		}
	}
	return ""
}

// uxmSetupUseStart copies the starting point into the library and continues
// once the library is saved. It never assigns a pack to a harness.
func (u *nativeUI) uxmSetupUseStart(id string) {
	if id == "current" {
		u.setupModelsContinue()
		return
	}
	if !u.uxmApplyStartingPoint(id, false) {
		return
	}
	u.setValue("setup.models.start", "")
	// Queue the write now so setup waits for this selection, not an older save.
	u.persistLibraryEdits()
	u.setupContinueWhenSaved = true
}

// setupUseRecommended applies the recommended set and continues.
func (u *nativeUI) setupUseRecommended() { u.uxmSetupUseStart(modelLibraryStartRecommended) }

// uxmSetupPrimary is the footer action; its label follows the selection.
func (u *nativeUI) uxmSetupPrimary() layout.Widget {
	_, ready := u.libraryStatus()
	if u.uxmSetupShowsCatalog() {
		return u.disabled(ready && len(u.library.selection.Models) > 0, u.primaryButton("primary.setup.models-next", u.tr("Continue", "Continuar"), u.setupModelsContinue))
	}
	start := u.uxmSetupStart()
	switch start {
	case "current":
		return u.disabled(ready, u.primaryButton("primary.setup.models-next", u.tr("Continue", "Continuar"), u.setupModelsContinue))
	case modelLibraryStartRecommended:
		return u.disabled(!u.setupContinueWhenSaved, u.primaryButton("primary.setup.use-recommended", u.tr("Use recommended & continue", "Usar recomendados y continuar"), u.setupUseRecommended))
	case "":
		return u.disabled(false, u.primaryButton("primary.setup.models-next", u.tr("Continue", "Continuar"), u.setupModelsContinue))
	}
	name := u.uxmStartingPackName(start)
	return u.disabled(!u.setupContinueWhenSaved, u.primaryButton("primary.setup.use-pack", fmt.Sprintf(u.tr("Use “%s” & continue", "Usar “%s” y continuar"), name), func() { u.uxmSetupUseStart(start) }))
}

// uxmSetupPackSize is the shared model count of the packs, or 0 when it varies.
func uxmSetupPackSize(packs []curatedModelPack) int {
	size := 0
	for i, pack := range packs {
		if i > 0 && len(pack.Models) != size {
			return 0
		}
		size = len(pack.Models)
	}
	return size
}

// uxmSetupInputRange is "$min – $max" over the input prices of models.
func uxmSetupInputRange(models []modelInfo) string {
	low, high := math.Inf(1), math.Inf(-1)
	for _, m := range models {
		if m.InputPrice == nil {
			continue
		}
		low, high = min(low, *m.InputPrice), max(high, *m.InputPrice)
	}
	if math.IsInf(low, 1) {
		return ""
	}
	if low == high {
		return nativeTokenPrice(&low)
	}
	return nativeTokenPrice(&low) + " – " + nativeTokenPrice(&high)
}

// uxmSetupOptionHeader is the visible part of a starting point row.
type uxmSetupOptionHeader struct {
	title, badge, description string
	chips                     []layout.Widget
	count                     string
	price                     string
	unavailable               string
}

// uxmSetupOption is one radio row. A selected row is citrus with a black
// left bar and a filled radio; its expansion (if any) replaces the chips.
func (u *nativeUI) uxmSetupOption(id string, selected, enabled bool, header uxmSetupOptionHeader, expansion layout.Widget) layout.Widget {
	if !selected {
		expansion = nil
	}
	radio := uxmIconRadio
	if selected {
		radio = uxmIconRadioOn
	}
	row := u.uxmClickRow("setup.models.option."+id, header.title, semantic.RadioButton, selected, func() { u.setValue("setup.models.start", id) }, func(hovered bool) layout.Widget {
		fg := nativeInk
		if !enabled {
			fg = nativeTextDisabled
		}
		title := []layout.Widget{u.textStyle(16, header.title, fg, font.SemiBold)}
		if header.badge != "" {
			title = append(title, u.uxmTag(header.badge, nativeCitrus))
		}
		body := []layout.Widget{uxmHStack(10, title...)}
		if header.description != "" {
			body = append(body, uxmGap(3), u.uxmSubtle(13, header.description))
		}
		if header.unavailable != "" {
			body = append(body, uxmGap(6), u.textStyle(13, header.unavailable, nativeTextDisabled, font.Medium))
		} else if expansion == nil && len(header.chips) > 0 {
			body = append(body, uxmGap(9), u.pills(header.chips...))
		}
		right := uxmStack(u.uxmSetupRight(13, header.count, fg, font.Medium), uxmGap(3), u.uxmSetupRight(12, header.price, nativeTextSubtle, font.Normal))
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 16, Bottom: 16, Left: 18, Right: 18}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Flex{Alignment: layout.Start}.Layout(gtx,
					layout.Rigid(uxmIcon(radio, 24, fg)),
					layout.Rigid(layout.Spacer{Width: 16}.Layout),
					layout.Flexed(1, uxmStack(body...)),
					layout.Rigid(layout.Spacer{Width: 16}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = min(gtx.Dp(150), gtx.Constraints.Max.X)
						gtx.Constraints.Max.X = gtx.Constraints.Min.X
						return right(gtx)
					}),
				)
			})
		}
	})
	row = u.disabled(enabled, row)
	content := row
	if expansion != nil {
		content = uxmStack(row, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: 58, Right: 18, Bottom: 18}.Layout(gtx, expansion)
		})
	}
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		if !selected {
			return content(gtx)
		}
		return uxmLeftBar(6, nativeInk, func(gtx layout.Context) layout.Dimensions {
			return nativeBox(gtx, nativeCitrus, content)
		})(gtx)
	}
}

// uxmSetupRight is right-aligned text in the 150dp side column.
func (u *nativeUI) uxmSetupRight(size unit.Sp, text string, fg color.NRGBA, weight font.Weight) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if text == "" {
			return layout.Dimensions{}
		}
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.E.Layout(gtx, u.textStyle(size, text, fg, weight))
	}
}

func (u *nativeUI) uxmSetupRecommendedOption(start string) layout.Widget {
	recommended := u.recommendedModels()
	header := uxmSetupOptionHeader{
		title:       u.tr("Recommended", "Recomendados"),
		badge:       u.tr("Start here", "Empieza aquí"),
		description: u.tr("A balanced selection for everyday coding. The simplest choice if you are not sure.", "Una selección equilibrada para programar a diario. Lo más sencillo si no sabes qué elegir."),
	}
	for _, m := range recommended {
		header.chips = append(header.chips, u.uxmChip("", uxmSetupModelName(m), m.Recommendation.Default, nil))
	}
	if len(recommended) > 0 {
		header.count = uxmPlural(len(recommended), u.tr("1 model", "1 modelo"), u.tr("%d models", "%d modelos"))
		if prices := uxmSetupInputRange(recommended); prices != "" {
			header.price = prices + u.tr(" input", " entrada")
		}
	} else if len(u.models) > 0 {
		header.unavailable = u.tr("Not available in your catalog", "No disponible en tu catálogo")
	}
	enabled := u.uxmSetupStartUsable(modelLibraryStartRecommended)
	return u.uxmSetupOption(modelLibraryStartRecommended, start == modelLibraryStartRecommended, enabled, header, nil)
}

func uxmSetupModelName(m modelInfo) string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
}

// uxmSetupPackModel resolves a pack model from the catalog, falling back to its ID.
func (u *nativeUI) uxmSetupPackModel(id string) modelInfo {
	if m, ok := u.packCatalogModel(id); ok {
		return m
	}
	return modelInfo{ID: id, Name: id}
}

func (u *nativeUI) uxmSetupPackOption(pack curatedModelPack, start string) layout.Widget {
	header := uxmSetupOptionHeader{title: u.localizedPackText(pack.Name), description: u.localizedPackText(pack.Description)}
	models := []modelInfo{}
	for _, spec := range pack.Models {
		m := u.uxmSetupPackModel(spec.ID)
		models = append(models, m)
		header.chips = append(header.chips, u.uxmChip("", uxmSetupModelName(m), spec.ID == pack.DefaultModel, nil))
	}
	header.count = uxmPlural(len(models), u.tr("1 model", "1 modelo"), u.tr("%d models", "%d modelos"))
	if prices := uxmSetupInputRange(models); prices != "" {
		header.price = prices + u.tr(" input · 1M", " entrada · 1M")
	}
	enabled := u.uxmSetupStartUsable(pack.ID)
	if !enabled && len(u.models) > 0 {
		header.unavailable = u.tr("Not available in your catalog", "No disponible en tu catálogo")
	}
	return u.uxmSetupOption(pack.ID, start == pack.ID, enabled, header, u.uxmSetupPackTable(pack, models))
}

var uxmSetupPackColumns = []uxmColumn{{width: 200, hideBelow: 520}, {weight: 1}, {width: 110}}

// uxmSetupPackTable is the white role / model / price table of a selected pack.
func (u *nativeUI) uxmSetupPackTable(pack curatedModelPack, models []modelInfo) layout.Widget {
	rows := []layout.Widget{}
	for i, spec := range pack.Models {
		m := models[i]
		var badge layout.Widget
		if spec.ID == pack.DefaultModel {
			badge = u.uxmDefaultBadge()
		}
		price := m
		rows = append(rows, u.uxmTableRow(uxmSetupPackColumns, false, nativeSurface,
			u.textStyle(14, u.localizedPackText(spec.Role), nativeText, font.Normal),
			u.uxmModelCell(uxmSetupModelName(m), "", spec.ID, badge),
			func(gtx layout.Context) layout.Dimensions {
				return layout.E.Layout(gtx, u.textStyle(15, u.uxmPrice(price), nativeInk, font.SemiBold))
			},
		))
	}
	if len(pack.Optional) > 0 {
		chips := []layout.Widget{u.textStyle(14, u.tr("Optional, you can add it later:", "Opcional, puedes añadirlo después:"), nativeText, font.Normal)}
		for _, spec := range pack.Optional {
			chips = append(chips, u.uxmChip("", uxmSetupModelName(u.uxmSetupPackModel(spec.ID)), false, nil))
		}
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 8, Bottom: 8, Left: 18, Right: 18}.Layout(gtx, u.pills(chips...))
		})
	}
	return uxmPanel(nativeSurface, uxmStack(rows...))
}

func (u *nativeUI) uxmSetupCurrentHeader() uxmSetupOptionHeader {
	s := u.library.selection
	header := uxmSetupOptionHeader{
		title:       u.tr("Your current list", "Tu lista actual"),
		description: u.tr("Keep the models you already chose.", "Mantén los modelos que ya elegiste."),
		count:       uxmPlural(len(s.Models), u.tr("1 model", "1 modelo"), u.tr("%d models", "%d modelos")),
	}
	if start := u.library.startingPack; start != "" {
		header.description = fmt.Sprintf(u.tr("Based on “%s”. Keep it and choose the default here.", "Basada en “%s”. Quédatela y elige aquí el predeterminado."), u.uxmStartingPackName(start))
	}
	models := []modelInfo{}
	for _, m := range s.Models {
		models = append(models, m.Model)
		header.chips = append(header.chips, u.uxmChip("", nativeCodexDisplayName(m), m.Model.ID == s.Initial, nil))
	}
	if prices := uxmSetupInputRange(models); prices != "" {
		header.price = prices + u.tr(" input · 1M", " entrada · 1M")
	}
	return header
}

var uxmSetupCurrentColumns = []uxmColumn{{weight: 1}, {width: 110, hideBelow: 520}, {width: 190}}

// uxmSetupCurrentTable lists the current models; the star picks the default
// (the model each harness opens first) and Remove takes a model out.
func (u *nativeUI) uxmSetupCurrentTable() layout.Widget {
	s := u.library.selection
	rows := []layout.Widget{}
	for _, choice := range s.Models {
		id := choice.Model.ID
		var badge layout.Widget
		action := u.iconButton("setup.default:"+id, u.tr("Use by default", "Usar por defecto"), nativeButtonLink, nativeIconStarOutline, func() { s.Initial = id })
		if s.Initial == id {
			badge, action = u.uxmDefaultBadge(), nil
		}
		remove := u.uxmLink("setup.remove:"+id, u.tr("Remove", "Quitar"), func() { u.uxmCatalogRemove(id) })
		m := choice.Model
		actions := func(gtx layout.Context) layout.Dimensions {
			return layout.E.Layout(gtx, uxmHStack(12, action, remove))
		}
		if action == nil {
			actions = func(gtx layout.Context) layout.Dimensions { return layout.E.Layout(gtx, remove) }
		}
		rows = append(rows, u.uxmTableRow(uxmSetupCurrentColumns, false, nativeSurface,
			u.uxmModelCell(nativeCodexDisplayName(choice), modelLabLabel(modelLab(m)), id, badge),
			func(gtx layout.Context) layout.Dimensions {
				return layout.E.Layout(gtx, u.textStyle(14, u.uxmPrice(m), nativeInk, font.Medium))
			},
			actions,
		))
	}
	rows = append(rows, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 10, Bottom: 10, Left: 12, Right: 12}.Layout(gtx, uxmHStack(16,
			u.iconButton("setup.models.add-more", u.tr("Add more models", "Añadir más modelos"), nativeButtonLink, nativeIconAdd, u.openSetupCatalog),
			u.uxmSubtle(13, u.tr("Reasoning, context window and short names are tuned later on the Models page.", "El razonamiento, la ventana de contexto y los nombres cortos se ajustan después en la página Modelos.")),
		))
	})
	return uxmPanel(nativeSurface, uxmStack(rows...))
}

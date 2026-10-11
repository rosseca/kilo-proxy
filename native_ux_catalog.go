//go:build desktop

package main

import (
	"fmt"
	"strconv"
	"strings"

	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
)

// The catalog ("Add models"): search, a Filters menu, the sort order, compact
// clickable rows grouped as Recommended / All, add by exact ID and a bottom
// bar with the current list.

const uxmCatalogLimit = 50

// uxmCatalogAdd adds m to the shared list, reporting invalid IDs and the limit.
func (u *nativeUI) uxmCatalogAdd(m modelInfo) bool {
	s := u.library.selection
	if !catalogID.MatchString(m.ID) {
		u.setNotice(nativeToneWarning, u.tr("Enter a valid exact model ID.", "Introduce un ID de modelo exacto válido."))
		return false
	}
	if err := s.add(m, uxmCatalogLimit); err != nil {
		u.noticeError(err)
		return false
	}
	u.seedClientChoice(sharedModelKey, *s.choice(m.ID))
	return true
}

func (u *nativeUI) uxmCatalogRemove(id string) {
	u.library.selection.remove(id)
	delete(u.library.roles, id)
}

// uxmCatalogTarget is the list the catalog edits: the shared list or one of
// "My packs". prefix scopes the search, filters and row ids.
type uxmCatalogTarget struct {
	prefix    string
	selection *nativeClientSelection
	limit     int
	countText string
	add       func(modelInfo) bool
	remove    func(id string)
	clear     func()
	done      func()
	doneText  string
	title     string
}

func (u *nativeUI) uxmLibraryCatalogTarget() uxmCatalogTarget {
	s := u.library.selection
	prefix := "client:shared:"
	return uxmCatalogTarget{
		prefix: prefix, selection: s, limit: uxmCatalogLimit,
		countText: u.tr("%d in your list", "%d en tu lista"),
		add:       u.uxmCatalogAdd, remove: u.uxmCatalogRemove,
		clear: func() {
			s.Models, s.Initial, s.Aliases = nil, "", map[string]string{}
			u.library.roles = nil
			u.setChecked(prefix+"selected", false)
		},
		done:     func() { u.expanded["library.catalog"] = false },
		doneText: u.tr("Done", "Listo"),
		title:    u.tr("Add models", "Añadir modelos"),
	}
}

func (u *nativeUI) uxmSortLabel(order string) string {
	if modelSortOrder(order) == "codeModeRank" {
		return u.tr("Most used for code", "Más usados en código")
	}
	return u.modelSortLabel(order)
}

// uxmCatalogFilterCount is the number of active filters.
func (u *nativeUI) uxmCatalogFilterCount(prefix string) int {
	count := 0
	if normalizeModelLab(u.value("models.lab")) != "" {
		count++
	}
	if u.checked(prefix + "coding") {
		count++
	}
	if u.checked(prefix + "selected") {
		count++
	}
	return count
}

func (u *nativeUI) uxmFilterItems(target uxmCatalogTarget, available []modelInfo) func() []layout.Widget {
	return func() []layout.Widget {
		prefix := target.prefix
		s := target.selection
		models := append([]modelInfo(nil), u.models...)
		for _, choice := range s.Models {
			models = append(models, choice.Model)
		}
		current := normalizeModelLab(u.value("models.lab"))
		if current != "" {
			models = append(models, modelInfo{Provider: current})
		}
		box := func(on bool) uxmMenuItemSpec {
			if on {
				return uxmMenuItemSpec{icon: uxmIconBoxOn, selected: true, keep: true}
			}
			return uxmMenuItemSpec{icon: uxmIconBox, keep: true}
		}
		items := []layout.Widget{u.uxmMenuTitle(u.tr("Lab", "Laboratorio"))}
		for _, option := range modelLabOptions(models, u.language) {
			option := option
			spec := box(option.Value == current)
			spec.label = option.Label
			if option.Value == "" {
				spec.label = u.tr("All labs", "Todos")
			}
			items = append(items, u.uxmMenuItem("models.lab.option."+option.Value, spec, func() { u.setValue("models.lab", option.Value) }))
		}
		items = append(items, uxmMenuSeparator, u.uxmMenuTitle(u.tr("Show", "Mostrar")))
		for _, toggle := range []struct{ id, en, es string }{
			{prefix + "coding", "Only with tools (agents)", "Solo con herramientas (agentes)"},
			{prefix + "selected", "Only the ones in my list", "Solo los de mi lista"},
			{prefix + "advanced", "Technical details", "Detalles técnicos"},
		} {
			id := toggle.id
			spec := box(u.checked(id))
			spec.label = u.tr(toggle.en, toggle.es)
			items = append(items, u.uxmMenuItem("models.filter."+id, spec, func() { u.setChecked(id, !u.checked(id)) }))
		}
		missing := 0
		for _, m := range available {
			if s.choice(m.ID) == nil {
				missing++
			}
		}
		items = append(items, uxmMenuSeparator, u.uxmMenuTitle(u.tr("Actions", "Acciones")),
			u.uxmMenuItem(prefix+"select-all", uxmMenuItemSpec{icon: nativeIconAdd, label: fmt.Sprintf(u.tr("Add the %d results", "Añadir los %d resultados"), missing), disabled: missing == 0}, func() {
				for _, m := range available {
					if len(target.selection.Models) >= target.limit {
						u.setNotice(nativeToneWarning, fmt.Sprintf(u.tr("A list holds up to %d models.", "Una lista admite hasta %d modelos."), target.limit))
						break
					}
					if target.selection.choice(m.ID) == nil && !target.add(m) {
						break
					}
				}
			}),
		)
		return items
	}
}

func (u *nativeUI) uxmSortItems() []layout.Widget {
	items := []layout.Widget{u.uxmMenuTitle(u.tr("Order", "Orden"))}
	current := modelSortOrder(u.value("models.sort"))
	for _, order := range modelSortOrders {
		order := order
		items = append(items, u.uxmMenuItem("models.sort.option."+order, uxmMenuItemSpec{label: u.uxmSortLabel(order), selected: order == current}, func() { u.setValue("models.sort", order) }))
	}
	return append(items, u.uxmMenuNote(u.modelSortHint(current)))
}

// uxmCatalogTag is the right-hand tag of a catalog row.
func (u *nativeUI) uxmCatalogTag(m modelInfo, order string) layout.Widget {
	if r := m.Recommendation; r != nil {
		if r.Default {
			return u.uxmTag("★ "+u.tr("Suggested default", "Predeterminado sugerido"), nativeSurface)
		}
		if note := u.tr(r.NoteEN, r.NoteES); note != "" {
			return u.uxmSubtle(13, note)
		}
	}
	if modelSortOrder(order) == "codeModeRank" {
		if value := modelSortValue(m, "codeModeRank"); value != nil {
			return u.uxmSubtle(13, fmt.Sprintf(u.tr("#%s for code", "#%s en código"), strconv.FormatFloat(*value, 'f', -1, 64)))
		}
		return nil
	}
	if metric := u.modelSortMetric(m, order); metric != "" {
		return u.uxmSubtle(13, metric)
	}
	return nil
}

func (u *nativeUI) uxmModelDetails(m modelInfo) string {
	details := []string{}
	if m.ContextWindow > 0 {
		details = append(details, fmt.Sprintf(u.tr("Context: %d tokens", "Contexto: %d tokens"), m.ContextWindow))
	}
	if m.MaxOutputTokens > 0 {
		details = append(details, fmt.Sprintf(u.tr("Max output: %d tokens", "Salida máxima: %d tokens"), m.MaxOutputTokens))
	}
	if len(m.InputModalities) > 0 {
		details = append(details, u.tr("Input: ", "Entrada: ")+strings.Join(m.InputModalities, ", "))
	}
	if len(m.OutputModalities) > 0 {
		details = append(details, u.tr("Output: ", "Salida: ")+strings.Join(m.OutputModalities, ", "))
	}
	if m.Tools != nil {
		text := u.tr("Tools: unavailable", "Herramientas: no disponibles")
		if *m.Tools {
			text = u.tr("Tools: supported", "Herramientas: compatibles")
		}
		details = append(details, text)
	}
	return strings.Join(details, " · ")
}

var uxmCatalogColumns = []uxmColumn{{width: 48}, {weight: 3}, {width: 150, hideBelow: 720}, {width: 120}, {width: 230, hideBelow: 860}}

// uxmCatalogRow is one compact clickable model row. Selected rows are citrus
// with a black check box and keep their place.
func (u *nativeUI) uxmCatalogRow(target uxmCatalogTarget, m modelInfo, order string, picked bool, toggle func()) layout.Widget {
	id := m.ID
	label := m.Name
	if label == "" {
		label = id
	}
	if choice := target.selection.choice(id); choice != nil && choice.DisplayName != "" {
		label = choice.DisplayName
	}
	return u.uxmClickRow(target.prefix+"choose:"+id, label, semantic.CheckBox, picked, toggle, func(hovered bool) layout.Widget {
		background := nativeSurface
		if picked {
			background = nativeCitrus
		} else if hovered {
			background = nativeIvory
		}
		box := uxmBordered(nativeSurface, nativeLine, layout.UniformInset(4), uxmIcon(nativeIconAdd, 16, nativeInk))
		if picked {
			box = func(gtx layout.Context) layout.Dimensions {
				return nativeBox(gtx, nativeInk, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(6).Layout(gtx, uxmIcon(nativeIconCheck, 16, nativeCitrus))
				})
			}
		}
		name := []layout.Widget{u.uxmTitle(15, label), uxmGap(2), u.uxmSubtle(12, id)}
		if m.MayTrain != nil && *m.MayTrain {
			name = append(name, u.uxmSubtle(12, u.tr("Kilo reports that this model may use prompts for training.", "Kilo indica que este modelo puede usar los mensajes para entrenamiento.")))
		}
		if m.ExpirationDate != "" {
			name = append(name, u.uxmSubtle(12, u.tr("Published retirement date: ", "Fecha de retirada publicada: ")+m.ExpirationDate))
		}
		if u.checked(target.prefix + "advanced") {
			if details := u.uxmModelDetails(m); details != "" {
				name = append(name, u.uxmSubtle(12, details))
			}
		}
		tag := u.uxmCatalogTag(m, order)
		right := func(gtx layout.Context) layout.Dimensions {
			if tag == nil {
				return layout.Dimensions{}
			}
			return layout.E.Layout(gtx, tag)
		}
		price := func(gtx layout.Context) layout.Dimensions {
			return layout.E.Layout(gtx, u.textStyle(15, u.uxmPrice(m), nativeInk, font.Medium))
		}
		return u.uxmTableRow(uxmCatalogColumns, false, background, box, uxmStack(name...), u.textStyle(14, modelLabLabel(modelLab(m)), nativeInk, font.Normal), price, right)
	})
}

// uxmCatalogGroup is a mono heading over a bordered list of rows.
func (u *nativeUI) uxmCatalogGroup(heading string, rows []layout.Widget) layout.Widget {
	return uxmStack(u.eyebrow(heading), uxmGap(8), uxmPanel(nativeSurface, uxmStack(rows...)))
}

func (u *nativeUI) uxmCatalogPage() layout.Widget {
	return u.uxmCatalogPageFor(u.uxmLibraryCatalogTarget())
}

// uxmCatalogPageFor is the catalog editing target. The bottom bar is pinned
// by the page footer on the Models page and laid out inline elsewhere.
func (u *nativeUI) uxmCatalogPageFor(target uxmCatalogTarget) layout.Widget {
	s := target.selection
	prefix := target.prefix
	order := modelSortOrder(u.value("models.sort"))
	query := strings.TrimSpace(u.value(prefix + "search"))
	available := nativeVisibleModels(u.models, s, query, u.checked(prefix+"selected"), u.checked(prefix+"coding"), order, u.value("models.lab"))
	refresh := u.iconButton(prefix+"refresh", u.tr("Refresh catalog", "Actualizar catálogo"), nativeButtonLink, nativeIconRefresh, u.refreshModels)
	header := uxmHStack(16, u.textStyle(26, target.title, nativeInk, font.Bold), nil, refresh)
	filters := u.uxmCatalogFilterCount(prefix)
	filterLabel := u.tr("Filters", "Filtros")
	if filters > 0 {
		filterLabel = fmt.Sprintf("%s · %d", filterLabel, filters)
	}
	filterMenu := u.uxmPopover("models.filters", 300, true, u.uxmDropdown("models.filters", filterLabel, filters > 0), u.uxmFilterItems(target, available))
	sortMenu := u.uxmPopover("models.order", 280, true, u.uxmDropdown("models.order", u.tr("Order: ", "Orden: ")+u.uxmSortLabel(order), false), u.uxmSortItems)
	search := u.modelSearchField(prefix+"search", u.tr("Search by name, lab or ID", "Buscar por nombre, laboratorio o ID"))
	tools := func(gtx layout.Context) layout.Dimensions {
		if gtx.Constraints.Max.X < gtx.Dp(640) {
			return u.column(search, u.pills(filterMenu, sortMenu))(gtx)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, search),
			layout.Rigid(layout.Spacer{Width: 12}.Layout), layout.Rigid(filterMenu),
			layout.Rigid(layout.Spacer{Width: 12}.Layout), layout.Rigid(sortMenu),
		)
	}
	widgets := []layout.Widget{header}
	if u.catalogCached {
		widgets = append(widgets, u.message(nativeToneInfo, u.tr("Using the saved catalog. Refresh to check current prices and availability.", "Usando el catálogo guardado. Actualiza para comprobar precios y disponibilidad.")))
	}
	if len(nativeArray(u.state, "catalogWarnings")) > 0 {
		widgets = append(widgets, u.message(nativeToneWarning, u.tr("Some models could not be loaded. Check the affected connection and refresh the catalog.", "No se pudieron cargar algunos modelos. Comprueba la conexión afectada y actualiza el catálogo.")))
	}
	widgets = append(widgets, tools)
	row := func(m modelInfo) layout.Widget {
		m2 := m
		picked := s.choice(m2.ID) != nil
		return u.uxmCatalogRow(target, m2, order, picked, func() {
			if s.choice(m2.ID) != nil {
				target.remove(m2.ID)
			} else {
				target.add(m2)
			}
		})
	}
	shown := available
	if len(shown) > 150 {
		shown = shown[:150]
	}
	if len(available) == 0 {
		widgets = append(widgets, u.note(u.tr("No matches. Refresh or add an exact model ID.", "Sin resultados. Actualiza o añade un ID exacto.")))
	} else if query != "" || filters > 0 {
		heading := uxmPlural(len(available), u.tr("1 result", "1 resultado"), u.tr("%d results", "%d resultados"))
		if query != "" {
			heading += u.tr(" for «", " para «") + query + "»"
		}
		rows := []layout.Widget{}
		for _, m := range shown {
			rows = append(rows, row(m))
		}
		widgets = append(widgets, u.uxmCatalogGroup(heading, rows))
	} else {
		recommended, rest := []layout.Widget{}, []layout.Widget{}
		for _, m := range u.recommendedModels() {
			recommended = append(recommended, row(m))
		}
		count := 0
		for _, m := range shown {
			if m.Recommendation != nil {
				continue
			}
			rest = append(rest, row(m))
			count++
		}
		if len(recommended) > 0 {
			widgets = append(widgets, u.uxmCatalogGroup(fmt.Sprintf(u.tr("Recommended · %d", "Recomendados · %d"), len(recommended)), recommended))
		}
		if len(rest) > 0 {
			total := 0
			for _, m := range available {
				if m.Recommendation == nil {
					total++
				}
			}
			widgets = append(widgets, u.uxmCatalogGroup(fmt.Sprintf(u.tr("All · %d", "Todos · %d"), total), rest))
		}
	}
	if len(available) > 150 {
		widgets = append(widgets, u.note(u.tr("Showing the first 150 results. Search to narrow the catalog.", "Se muestran los primeros 150 resultados. Busca para acotar el catálogo.")))
	}
	exactID := prefix + "exact"
	widgets = append(widgets, uxmHStack(6, u.uxmSubtle(14, u.tr("Can't find it?", "¿No lo encuentras?")), u.uxmLink(exactID, u.tr("Add by exact ID", "Añadir por ID exacto"), func() { u.expanded[exactID] = !u.expanded[exactID] })))
	if u.expanded[exactID] {
		widgets = append(widgets, u.actionRow(u.field(prefix+"manual", u.tr("Exact model ID", "ID de modelo exacto"), "provider/model", false), u.button(prefix+"add", u.tr("Add model", "Añadir modelo"), func() {
			id := strings.TrimSpace(u.value(prefix + "manual"))
			m := modelInfo{ID: id, Name: id}
			if found, ok := u.packCatalogModel(id); ok {
				m = found
			}
			if target.add(m) {
				u.setValue(prefix+"manual", "")
				u.setValue(prefix+"search", "")
			}
		})))
	}
	if u.page != "models" {
		widgets = append(widgets, u.uxmCatalogBar(target))
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, uxmSpaced(16, widgets)...)
	}
}

// uxmCatalogBar is the bottom bar: the list as chips, "Remove all" and Done.
func (u *nativeUI) uxmCatalogBar(target uxmCatalogTarget) layout.Widget {
	s := target.selection
	prefix := target.prefix
	chips := []layout.Widget{u.monoStyle(13, strings.ToUpper(fmt.Sprintf(target.countText, len(s.Models))), nativeInk, font.Bold)}
	for _, m := range s.Models {
		id := m.Model.ID
		if id == s.Initial {
			chips = append(chips, u.uxmChip(prefix+"chip:"+id, nativeCodexDisplayName(m), true, nil))
			continue
		}
		chips = append(chips, u.uxmChip(prefix+"chip:"+id, nativeCodexDisplayName(m), false, func() { target.remove(id) }))
	}
	var clear layout.Widget
	if target.clear != nil {
		clear = u.disabled(len(s.Models) > 0, u.uxmLink(prefix+"clear", u.tr("Remove all", "Quitar todos"), target.clear))
	}
	done := u.primaryButton("models.done", target.doneText, target.done)
	if u.page == "setup" {
		done = u.button("models.done", target.doneText, target.done)
	}
	actions := []layout.Widget{done}
	if clear != nil {
		actions = []layout.Widget{clear, done}
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(uxmHRule(nativeLineHeavy, nativeInk)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 12, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, u.pills(chips...)),
						layout.Rigid(layout.Spacer{Width: 16}.Layout),
						layout.Rigid(uxmHStack(16, actions...)),
					)
				})
			}),
		)
	}
}

// uxmPageFooter pins the catalog bar under the Models page while it is open.
func (u *nativeUI) uxmPageFooter() layout.Widget {
	if u.library == nil {
		return nil
	}
	packs := u.packsState()
	if packs.tab == "mine" && u.expanded["packs.my.add"] {
		if target, ok := u.uxmPackCatalogTarget(packs.selected); ok {
			return u.uxmCatalogBar(target)
		}
	}
	if !u.expanded["library.catalog"] || packs.tab != "library" {
		return nil
	}
	return u.uxmCatalogBar(u.uxmLibraryCatalogTarget())
}

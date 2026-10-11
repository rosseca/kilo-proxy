//go:build desktop

package main

import (
	"fmt"
	"image"
	"strconv"
	"strings"

	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
)

// The Library tab ("Your models"): a table of the shared list with a row menu,
// the global context window line, the starting point strip and a "More"
// section for import and image generation.

func (u *nativeUI) uxmLibraryHeader() layout.Widget {
	s := u.library.selection
	status, saved := u.libraryStatus()
	w := u.library.writer
	w.mu.Lock()
	recovery, failed := w.recoveryRequired, w.err != nil
	w.mu.Unlock()
	tone, text := nativeToneInfo, status
	if saved {
		tone, text = nativeToneSuccess, u.tr("Saved", "Guardado")
	} else if recovery || failed || u.library.validation != "" {
		tone = nativeToneWarning
	}
	addKind := nativeButtonPrimary
	if u.page == "setup" || len(s.Models) == 0 && len(u.recommendedModels()) > 0 {
		addKind = nativeButtonSecondary
	}
	add := u.iconButton("primary.models.add", u.tr("Add models", "Añadir modelos"), addKind, nativeIconAdd, u.openModelCatalog)
	title := uxmHStack(12, u.textStyle(26, u.tr("Your models", "Tus modelos"), nativeInk, font.Bold), u.uxmCountBox(len(s.Models), nativeInk), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(360))
		return u.message(tone, text)(gtx)
	})
	return func(gtx layout.Context) layout.Dimensions {
		if gtx.Constraints.Max.X < gtx.Dp(560) {
			return u.column(title, add)(gtx)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Flexed(1, title), layout.Rigid(add))
	}
}

// uxmLibraryNotices are the connection, catalog, recovery and import notices.
func (u *nativeUI) uxmLibraryNotices() []layout.Widget {
	w := u.library.writer
	w.mu.Lock()
	recovery, failed, warning := w.recoveryRequired, w.err != nil, w.warning
	w.mu.Unlock()
	widgets := []layout.Widget{}
	if u.chatGPTConnected() {
		widgets = append(widgets, u.note(u.tr("One library for both connections. Models marked ChatGPT use subscription quota; other models use Kilo credit.", "Una biblioteca para ambas conexiones. Los modelos marcados ChatGPT usan la cuota de suscripción; los demás usan crédito de Kilo.")))
	}
	if len(nativeArray(u.state, "catalogWarnings")) > 0 {
		widgets = append(widgets, u.message(nativeToneWarning, u.tr("Some models could not be loaded. Check the affected connection and refresh the catalog.", "No se pudieron cargar algunos modelos. Comprueba la conexión afectada y actualiza el catálogo.")))
	}
	if recovery || failed || u.library.validation != "" {
		label := u.tr("Retry save", "Reintentar guardado")
		if recovery {
			label = u.tr("Recover this selection", "Recuperar esta selección")
		}
		widgets = append(widgets, u.card(u.note(warning), u.pills(u.disabled(u.library.validation == "", u.button("models.retry", label, func() { u.retryModelLibrary(recovery) })), u.button("models.review-saved", u.tr("Review saved version", "Revisar versión guardada"), func() { u.reviewSavedLibrary() }))))
	}
	if candidate := u.library.pendingImport; candidate != nil {
		names := []string{}
		for _, m := range candidate.Models {
			names = append(names, nativeCodexDisplayName(m))
		}
		widgets = append(widgets, u.card(u.heading(u.tr("Review imported models", "Revisar modelos importados")), u.note(strings.Join(names, " · ")), u.pills(u.button("models.import.accept", u.tr("Use this selection for all agents", "Usar esta selección para todos"), func() {
			u.library.selection = candidate
			// An imported list no longer comes from the onboarding pack.
			u.library.startingPack, u.library.roles, u.library.switchUndo = "", nil, nil
			if state := u.library.pendingState; state != nil {
				u.library.startingPack, u.library.roles = state.Library.StartingPack, cloneModelLibrary(state.Library).Roles
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
		}), u.button("models.import.cancel", u.tr("Cancel", "Cancelar"), func() { u.library.pendingImport = nil }))))
	}
	return widgets
}

// uxmSelect is a compact dropdown bound to the editor id. Its option buttons
// keep the selectField ids (id+".toggle", id+".option."+value).
func (u *nativeUI) uxmSelect(id string, options []nativeChoice) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		current := u.value(id)
		selected := nativeChoice{}
		for _, option := range options {
			if option.Value == current {
				selected = option
				break
			}
		}
		if selected.Value == "" && len(options) > 0 {
			selected = options[0]
			u.setValue(id, selected.Value)
		}
		items := func() []layout.Widget {
			widgets := []layout.Widget{}
			for _, option := range options {
				option := option
				widgets = append(widgets, u.uxmMenuItem(id+".option."+option.Value, uxmMenuItemSpec{label: option.Label, selected: option.Value == u.value(id)}, func() { u.setValue(id, option.Value) }))
			}
			return widgets
		}
		return u.uxmPopover(id, 200, false, u.uxmDropdown(id, selected.Label, false), items)(gtx)
	}
}

// uxmSegment is a joined row of options; the selected one is citrus with a check.
func (u *nativeUI) uxmSegment(prefix string, choices []nativeChoice, selected string, choose func(string)) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{}
		for i, choice := range choices {
			choice := choice
			first := i == 0
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if choice.Disabled {
					gtx = gtx.Disabled()
				}
				b := u.clickable(prefix + choice.Value)
				for b.Clicked(gtx) {
					if gtx.Enabled() && choose != nil {
						choose(choice.Value)
					}
				}
				active := choice.Value == selected
				background, fg := nativeSurface, nativeInk
				if active {
					background = nativeCitrus
				} else if b.Hovered() && gtx.Enabled() {
					background = nativeIvory
				}
				if !gtx.Enabled() {
					fg = nativeTextDisabled
				}
				return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					semantic.ClassOp(semantic.Button).Add(gtx.Ops)
					semantic.LabelOp(strings.TrimSuffix(choice.Label, "…")).Add(gtx.Ops)
					semantic.SelectedOp(active).Add(gtx.Ops)
					dims := nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: 8, Bottom: 8, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							row := []layout.Widget{}
							if active {
								row = append(row, uxmIcon(nativeIconCheck, 14, fg))
							}
							row = append(row, u.textStyle(14, choice.Label, fg, font.Medium))
							if choice.Caption != "" {
								row = append(row, u.textStyle(14, choice.Caption, fg, font.Medium))
							}
							return uxmHStack(5, row...)(gtx)
						})
					})
					if !first {
						uxmFill(gtx, nativeInk, image.Pt(gtx.Dp(nativeLine), dims.Size.Y))
					}
					return dims
				})
			}))
		}
		return widget.Border{Color: nativeInk, Width: nativeLine}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
		})
	}
}

// uxmContextState summarizes the context window shared by the list.
type uxmContextState struct {
	common           string
	maximumTokens    int
	maximumAvailable bool
	unknown          []string
	tokens           int
}

func (u *nativeUI) uxmContext() uxmContextState {
	choices := u.library.selection.Models
	state := uxmContextState{unknown: u.unknownContextMaximumIDs()}
	state.maximumAvailable = len(choices) > 0 && len(state.unknown) == 0
	if len(choices) == 0 {
		return state
	}
	state.common = nativeLibraryItem(choices[0]).ContextPreset
	state.tokens = choices[0].ContextTokens
	state.maximumTokens = choices[0].Model.ContextWindow
	for _, choice := range choices {
		item := nativeLibraryItem(choice)
		if item.ContextPreset != state.common || state.common == contextPresetCustom && choice.ContextTokens != state.tokens {
			state.common = ""
		}
		if choice.Model.ContextWindow != state.maximumTokens {
			state.maximumTokens = 0
		}
	}
	return state
}

func (u *nativeUI) uxmContextSummary(state uxmContextState) string {
	switch state.common {
	case "":
		return u.tr("Mixed · set per model", "Mixta · cambia por modelo")
	case contextPresetRecommended:
		return u.contextPresetLabel(contextPresetRecommended) + " · " + nativeContextTokens(contextRecommendedTokens) + " tokens"
	case contextPresetLow:
		return u.contextPresetLabel(contextPresetLow) + " · " + nativeContextTokens(contextLowTokens) + " tokens"
	case contextPresetCustom:
		return u.contextPresetLabel(contextPresetCustom) + " · " + nativeContextTokens(state.tokens) + " tokens"
	}
	if state.maximumTokens > 0 {
		return u.contextPresetLabel(contextPresetMaximum) + " · " + nativeContextTokens(state.maximumTokens) + " tokens"
	}
	return u.contextPresetLabel(contextPresetMaximum) + " · " + u.tr("each model's limit", "el límite de cada modelo")
}

// uxmContextLine is the "Context window · all models" panel. Collapsed it shows
// the current value and "Change"; open it shows the presets.
func (u *nativeUI) uxmContextLine() layout.Widget {
	state := u.uxmContext()
	open := u.expanded["models.context.open"]
	eyebrow := u.eyebrow(u.tr("Context window · all models", "Ventana de contexto · todos los modelos"))
	if !open {
		return uxmPanel(nativeSurface, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 12, Bottom: 12, Left: 18, Right: 18}.Layout(gtx, uxmHStack(16,
				uxmStack(eyebrow, uxmGap(4), u.uxmTitle(15, u.uxmContextSummary(state))),
				nil,
				u.uxmLink("models.context.toggle", u.tr("Change", "Cambiar"), func() { u.expanded["models.context.open"] = true }),
			))
		})
	}
	presets := []nativeChoice{
		{Value: contextPresetRecommended, Label: u.contextPresetLabel(contextPresetRecommended), Caption: nativeContextTokens(contextRecommendedTokens)},
		{Value: contextPresetLow, Label: u.contextPresetLabel(contextPresetLow), Caption: nativeContextTokens(contextLowTokens)},
		{Value: contextPresetMaximum, Label: u.contextPresetLabel(contextPresetMaximum), Disabled: !state.maximumAvailable},
		{Value: contextPresetCustom, Label: u.contextPresetLabel(contextPresetCustom) + "…"},
	}
	segment := u.uxmSegment("models.context.", presets, state.common, func(preset string) {
		if preset == contextPresetCustom {
			u.expanded["models.context.edit"] = true
			if u.value("models.context.tokens") == "" {
				tokens := contextRecommendedTokens
				if state.common == contextPresetCustom {
					tokens = state.tokens
				}
				u.setValue("models.context.tokens", strconv.Itoa(tokens))
			}
			return
		}
		if preset == contextPresetMaximum && !state.maximumAvailable {
			return
		}
		u.applySharedContext(preset, 0)
	})
	lines := []layout.Widget{func(gtx layout.Context) layout.Dimensions {
		head := uxmStack(eyebrow, uxmGap(4), u.uxmTitle(15, u.tr("Capped to what each model supports", "Se recorta a lo que admite cada modelo")))
		if gtx.Constraints.Max.X < gtx.Dp(820) {
			return u.column(head, segment)(gtx)
		}
		return uxmHStack(16, head, nil, segment)(gtx)
	}}
	if len(state.unknown) > 0 {
		lines = append(lines, u.contextMaximumBlockersPanel(state.unknown))
	} else if u.expanded["models.context.removed"] {
		lines = append(lines,
			u.message(nativeToneSuccess, u.tr("All saved models have a known maximum. You can now choose Maximum.", "Todos los modelos guardados tienen un máximo conocido. Ya puedes elegir Máximo.")),
			u.pills(u.button("models.context.add-replacements", u.tr("Add replacement models", "Añadir modelos sustitutos"), func() {
				u.expanded["library.catalog"] = true
				if len(u.models) == 0 {
					u.refreshModels()
				}
			})),
		)
	}
	if u.expanded["models.context.edit"] {
		validation := u.sharedContextDraftError(u.value("models.context.tokens"))
		row := []layout.Widget{u.field("models.context.tokens", u.tr("Custom tokens", "Tokens personalizados"), "272000", false)}
		if validation != "" {
			row = append(row, u.message(nativeToneError, validation))
		}
		row = append(row, u.pills(u.disabled(validation == "", u.button("models.context.apply", u.tr("Apply to all models", "Aplicar a todos los modelos"), func() {
			if tokens, err := strconv.Atoi(u.value("models.context.tokens")); err == nil {
				u.applySharedContext(contextPresetCustom, tokens)
			}
		}))))
		lines = append(lines, u.column(row...))
	}
	lines = append(lines, uxmHStack(12, u.uxmSubtle(12, u.tr("New models start at Recommended. Use a row's ⋯ menu for a model's own window.", "Los modelos nuevos empiezan en Recomendada. Usa el menú ⋯ de una fila para darle su propia ventana.")), nil, u.uxmLink("models.context.toggle", u.tr("Close", "Cerrar"), func() { u.expanded["models.context.open"] = false })))
	return uxmPanel(nativeSurface, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 12, Bottom: 12, Left: 18, Right: 18}.Layout(gtx, u.column(lines...))
	})
}

// uxmRowOverride reports whether a model has its own context window.
func (u *nativeUI) uxmRowOverride(choice nativeModelChoice, state uxmContextState) bool {
	if state.common != "" || len(u.library.selection.Models) < 2 {
		return false
	}
	counts := map[string]int{}
	key := func(c nativeModelChoice) string {
		item := nativeLibraryItem(c)
		return item.ContextPreset + ":" + strconv.Itoa(c.ContextTokens)
	}
	for _, c := range u.library.selection.Models {
		counts[key(c)]++
	}
	best, bestCount := "", 0
	for _, c := range u.library.selection.Models {
		if n := counts[key(c)]; n > bestCount {
			best, bestCount = key(c), n
		}
	}
	return key(choice) != best
}

func (u *nativeUI) uxmContextCell(choice nativeModelChoice, state uxmContextState) layout.Widget {
	text := "—"
	if limits, err := contextPolicyForChoice(choice); err == nil && limits.ContextWindow > 0 {
		text = nativeContextTokens(limits.ContextWindow)
	}
	if !u.uxmRowOverride(choice, state) {
		return u.textStyle(14, text, nativeInk, font.Normal)
	}
	return uxmHStack(4, u.textStyle(14, text, nativeInk, font.Normal), u.uxmSubtle(14, "· "+u.tr("own", "propia")))
}

func (u *nativeUI) uxmSetDefault(id string) {
	s := u.library.selection
	s.Initial = id
	name := id
	if choice := s.choice(id); choice != nil {
		name = nativeCodexDisplayName(*choice)
	}
	u.setNotice(nativeToneSuccess, fmt.Sprintf(u.tr("✓ %s is now the default. It applies the next time you open each agent.", "✓ %s es ahora el predeterminado. Se aplica al volver a abrir cada agente."), name))
}

// uxmEditSection opens the inline editor of a row on one section.
func (u *nativeUI) uxmEditSection(id, section string) {
	u.expanded["client:shared:edit:"+id] = true
	u.setValue("uxm.edit."+id, section)
}

func (u *nativeUI) uxmRowMenu(choice *nativeModelChoice, index int) func() []layout.Widget {
	return func() []layout.Widget {
		s := u.library.selection
		id := choice.Model.ID
		prefix := "client:shared:"
		contextValue := "—"
		if limits, err := contextPolicyForChoice(*choice); err == nil && limits.ContextWindow > 0 {
			contextValue = nativeContextTokens(limits.ContextWindow)
		}
		return []layout.Widget{
			u.uxmMenuItem(prefix+"edit:"+id, uxmMenuItemSpec{icon: uxmIconEdit, label: u.tr("Rename", "Renombrar")}, func() { u.uxmEditSection(id, "name") }),
			u.uxmMenuItem(prefix+"menu-default:"+id, uxmMenuItemSpec{icon: nativeIconStarOutline, label: u.tr("Use by default", "Usar por defecto"), disabled: s.Initial == id}, func() { u.uxmSetDefault(id) }),
			u.uxmMenuItem(prefix+"up:"+id, uxmMenuItemSpec{icon: uxmIconUp, label: u.tr("Move up", "Subir"), disabled: index == 0}, func() { u.moveSharedModel(id, -1) }),
			u.uxmMenuItem(prefix+"down:"+id, uxmMenuItemSpec{icon: uxmIconDown, label: u.tr("Move down", "Bajar"), disabled: index >= len(s.Models)-1}, func() { u.moveSharedModel(id, 1) }),
			uxmMenuSeparator,
			u.uxmMenuItem(prefix+"context:"+id, uxmMenuItemSpec{icon: uxmIconContext, label: u.tr("Own context window · ", "Ventana de contexto propia · "), bold: contextValue}, func() { u.uxmEditSection(id, "context") }),
			u.uxmMenuItem(prefix+"levels:"+id, uxmMenuItemSpec{icon: nativeIconSettings, label: u.tr("Reasoning levels…", "Niveles de razonamiento…")}, func() { u.uxmEditSection(id, "levels") }),
			u.uxmMenuItem(prefix+"output:"+id, uxmMenuItemSpec{icon: uxmIconOutput, label: u.tr("Max output…", "Salida máxima…")}, func() { u.uxmEditSection(id, "output") }),
			uxmMenuSeparator,
			u.uxmMenuItem(prefix+"remove:"+id, uxmMenuItemSpec{icon: nativeIconClose, label: u.tr("Remove from the list", "Quitar de la lista"), danger: true}, func() {
				s.remove(id)
				delete(u.library.roles, id)
			}),
		}
	}
}

// uxmRowEditor is the inline editor opened from the row menu.
func (u *nativeUI) uxmRowEditor(choice *nativeModelChoice) layout.Widget {
	key, id := sharedModelKey, choice.Model.ID
	prefix := "client:shared:"
	section := u.value("uxm.edit." + id)
	widgets := []layout.Widget{}
	if section == "" || section == "name" {
		widgets = append(widgets, u.field(nativeClientField(key, id, "name"), u.tr("Display name", "Nombre visible"), choice.Model.Name, false))
	}
	if section == "" || section == "context" {
		widgets = append(widgets, u.contextChoiceControls(key, choice), u.note(u.contextChoiceSummary(*choice)))
	}
	if section == "" || section == "levels" {
		widgets = append(widgets, u.check(nativeClientField(key, id, "custom"), u.tr("Custom reasoning levels", "Niveles de razonamiento personalizados"), func(bool) {}))
		if u.checked(nativeClientField(key, id, "custom")) {
			widgets = append(widgets, u.field(nativeClientField(key, id, "levels"), u.tr("Levels separated by commas", "Niveles separados por comas"), "low,medium,high", false))
		}
		widgets = append(widgets, u.pills(u.button(prefix+"suggest:"+id, u.tr("Restore suggested levels", "Restaurar niveles sugeridos"), func() {
			choice.ReasoningCustom = false
			choice.ReasoningLevels = nil
			choice.DefaultReasoning = ""
			u.setChecked(nativeClientField(key, id, "custom"), false)
			_, effort := nativeReasoningFor(*choice)
			u.setValue(nativeClientField(key, id, "reasoning"), effort)
		})))
	}
	if section == "" || section == "output" {
		widgets = append(widgets, u.field(nativeClientField(key, id, "output"), u.tr("Max output (0 = automatic)", "Salida máxima (0 = automática)"), "0", false))
	}
	widgets = append(widgets, u.pills(u.button(prefix+"edit-done:"+id, u.tr("Done", "Listo"), func() { u.expanded["client:shared:edit:"+id] = false })))
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return nativeBox(gtx, nativeIvory, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Top: 14, Bottom: 14, Left: 58, Right: 18}.Layout(gtx, u.column(widgets...))
				})
			}),
			layout.Rigid(uxmHRule(1, nativeInk)),
		)
	}
}

func (u *nativeUI) uxmReasoningCell(choice *nativeModelChoice) layout.Widget {
	id := choice.Model.ID
	levels, _ := nativeReasoningFor(*choice)
	if !choice.ReasoningCustom && choice.DefaultReasoning != "" && !helperContains(levels, choice.DefaultReasoning) {
		levels = append(levels, choice.DefaultReasoning)
	}
	if len(levels) == 0 {
		if choice.ReasoningCustom {
			u.setValue(nativeClientField(sharedModelKey, id, "reasoning"), "")
		}
		return u.uxmSubtle(14, u.tr("Automatic", "Automático"))
	}
	return u.uxmSelect(nativeClientField(sharedModelKey, id, "reasoning"), nativeModelReasoningChoices(u, levels))
}

// uxmShowRoles: the "What for" column appears only for a pack starting point.
func (u *nativeUI) uxmShowRoles() bool {
	start := u.library.startingPack
	return start != "" && start != modelLibraryStartRecommended && len(u.library.roles) > 0
}

func (u *nativeUI) uxmLibraryColumns() []uxmColumn {
	columns := []uxmColumn{{width: 40}, {label: u.tr("Model", "Modelo"), weight: 3}}
	if u.uxmShowRoles() {
		columns = append(columns, uxmColumn{label: u.tr("What for", "Para qué"), weight: 1.4, hideBelow: 640})
	}
	return append(columns,
		uxmColumn{label: u.tr("Reasoning", "Razonamiento"), width: 160},
		uxmColumn{label: u.tr("Context", "Contexto"), width: 120, hideBelow: 600},
		uxmColumn{label: u.tr("Price · 1M", "Precio · 1M"), width: 120, hideBelow: 700},
		uxmColumn{width: 48},
	)
}

func (u *nativeUI) uxmLibraryTable() layout.Widget {
	s := u.library.selection
	prefix := "client:shared:"
	columns := u.uxmLibraryColumns()
	state := u.uxmContext()
	query := u.value(prefix + "search")
	visible := nativeFilterSavedModels(u.sharedModelOrder(), s, query)
	rows := []layout.Widget{u.uxmTableHeader(columns)}
	for _, model := range visible {
		choice := s.choice(model.ID)
		if choice == nil {
			continue
		}
		id := choice.Model.ID
		index := 0
		for i, m := range s.Models {
			if m.Model.ID == id {
				index = i
			}
		}
		isDefault := s.Initial == id
		star := u.uxmIconButton(prefix+"initial:"+id, u.tr("Use by default", "Usar por defecto"), nativeIconStarOutline, 18, false, func() { u.uxmSetDefault(id) })
		var badge layout.Widget
		if isDefault {
			star = func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(4).Layout(gtx, uxmIcon(nativeIconStar, 18, nativeInk))
			}
			badge = u.uxmDefaultBadge()
		}
		m := choice.Model
		cell := []layout.Widget{u.uxmModelCell(nativeCodexDisplayName(*choice), modelLabLabel(modelLab(m)), id, badge)}
		if _, found := u.packCatalogModel(id); !found {
			cell = append(cell, u.uxmSubtle(12, u.tr("Saved model · catalog unavailable", "Modelo guardado · catálogo no disponible")))
		}
		if m.MayTrain != nil && *m.MayTrain {
			cell = append(cell, u.uxmSubtle(12, u.tr("Kilo reports that this model may use prompts for training.", "Kilo indica que este modelo puede usar los mensajes para entrenamiento.")))
		}
		if m.ExpirationDate != "" {
			cell = append(cell, u.uxmSubtle(12, u.tr("Published retirement date: ", "Fecha de retirada publicada: ")+m.ExpirationDate))
		}
		cells := []layout.Widget{star, uxmStack(cell...)}
		if u.uxmShowRoles() {
			role := u.uxmSubtle(14, u.tr("Added by you", "Añadido por ti"))
			if text := u.library.roles[id]; text != "" {
				role = u.textStyle(14, text, nativeInk, font.Medium)
			}
			cells = append(cells, role)
		}
		menuID := "models.row:" + id
		cells = append(cells,
			u.uxmReasoningCell(choice),
			u.uxmContextCell(*choice, state),
			u.textStyle(14, u.uxmPrice(m), nativeInk, font.Normal),
			u.uxmPopover(menuID, 300, true, u.uxmMoreButton(menuID, u.tr("More actions for ", "Más acciones para ")+nativeCodexDisplayName(*choice)), u.uxmRowMenu(choice, index)),
		)
		rows = append(rows, u.uxmTableRow(columns, isDefault, nativeSurface, cells...))
		if u.expanded["client:shared:edit:"+id] {
			rows = append(rows, u.uxmRowEditor(choice))
		}
	}
	widgets := []layout.Widget{}
	if len(s.Models) >= uxmLibrarySearchMinimum || query != "" {
		search := u.modelSearchField(prefix+"search", u.tr("Search your models", "Buscar en tus modelos"))
		toolbar := []layout.Widget{search}
		if query != "" {
			toolbar = append(toolbar, u.ghostButton(prefix+"search-clear", u.tr("Clear search", "Limpiar búsqueda"), func() { u.setValue(prefix+"search", "") }))
		}
		widgets = append(widgets, u.actionRow(toolbar[0], toolbar[1:]...))
		widgets = append(widgets, u.note(fmt.Sprintf(u.tr("%d of %d saved models", "%d de %d modelos guardados"), len(visible), len(s.Models))))
	}
	if len(visible) == 0 {
		widgets = append(widgets, u.note(u.tr("No saved models match. Clear the search to see all saved models.", "Ningún modelo guardado coincide. Limpia la búsqueda para ver todos los modelos guardados.")))
		return u.column(widgets...)
	}
	widgets = append(widgets, uxmPanel(nativeSurface, uxmStack(rows...)))
	return u.column(widgets...)
}

// uxmImportAgents are the agents whose saved selection can be imported.
var uxmImportAgents = []struct{ key, name string }{{"codex", "Codex Desktop"}, {"claude-desktop", "Claude Desktop"}, {"codex-cli", "Codex CLI"}, {"claude", "Claude Code"}, {"opencode", "OpenCode"}, {"omp", "Oh My Pi"}, {"zed", "Zed"}}

// uxmMoreSection holds import and image generation below a heavy rule.
func (u *nativeUI) uxmMoreSection() layout.Widget {
	importItems := func() []layout.Widget {
		items := []layout.Widget{u.uxmMenuTitle(u.tr("Import the list of", "Importar la lista de"))}
		for _, option := range uxmImportAgents {
			key := option.key
			items = append(items, u.uxmMenuItem("models.import."+key, uxmMenuItemSpec{label: option.name}, func() { u.importModelLibrary(key) }))
		}
		return append(items, u.uxmMenuNote(u.tr("You review it before it replaces your list.", "Lo revisas antes de que sustituya tu lista.")))
	}
	importButton := u.uxmPopover("models.import", 260, true, u.button("models.import.toggle", u.tr("Import", "Importar"), func() { u.uxmToggleMenu("models.import", "models.import.toggle") }), importItems)
	images := u.sharedClientSelection("codex")
	imageState := u.tr("Off", "Desactivada")
	if images.ImageGeneration != nil && images.ImageGeneration.Enabled {
		imageState = u.tr("On", "Activada")
	}
	configure := u.button("models.images.toggle", u.tr("Configure", "Configurar"), func() { u.expanded["models.images.toggle"] = !u.expanded["models.images.toggle"] })
	widgets := []layout.Widget{
		uxmGap(8), uxmHRule(nativeLineHeavy, nativeInk), uxmGap(4),
		u.textStyle(20, u.tr("More", "Más"), nativeInk, font.Bold),
		u.uxmOptionRow(u.tr("Import from an agent", "Importar desde un agente"), u.tr("Bring the list you already use in Codex, Claude Code, OpenCode…", "Trae la lista que ya usas en Codex, Claude Code, OpenCode…"), importButton),
		u.uxmOptionRow(u.tr("Image generation for Codex", "Generación de imágenes para Codex"), imageState, configure),
	}
	if u.expanded["models.images.toggle"] {
		widgets = append(widgets, u.card(u.clientImagesPanel("codex", images)))
	}
	return u.column(widgets...)
}

// uxmEmptyLibrary is shown before any model is chosen.
func (u *nativeUI) uxmEmptyLibrary() layout.Widget {
	if recommended := u.recommendedModels(); len(recommended) > 0 {
		return u.card(u.recommendedPanel(recommended))
	}
	return u.card(u.heading(u.tr("A model library for all your agents", "Una biblioteca para todos tus agentes")), u.note(u.tr("Add the models you use, give them short names and choose a default. Your selection stays here next time.", "Añade tus modelos, ponles nombres cortos y elige uno inicial. Tu selección seguirá aquí la próxima vez.")))
}

// uxmLibraryPage is the "Your models" tab, or the catalog while it is open.
func (u *nativeUI) uxmLibraryPage() layout.Widget {
	u.initModelLibrary()
	s := u.library.selection
	u.syncClientSelection(sharedModelKey, s)
	if u.expanded["library.catalog"] {
		return u.uxmCatalogPage()
	}
	widgets := []layout.Widget{u.uxmLibraryHeader()}
	widgets = append(widgets, u.uxmLibraryNotices()...)
	widgets = append(widgets, u.uxmSwitchPanels()...)
	if strip := u.uxmStartingStrip(); strip != nil {
		widgets = append(widgets, strip)
	}
	if len(s.Models) == 0 {
		widgets = append(widgets, u.uxmEmptyLibrary())
	} else {
		widgets = append(widgets, u.uxmContextLine(), u.uxmLibraryTable())
		if recommended := u.recommendedModels(); len(recommended) > 0 {
			widgets = append(widgets, u.pills(u.disclosure("models.recommended", u.tr("Recommended models", "Modelos recomendados"))))
			if u.expanded["models.recommended"] {
				widgets = append(widgets, u.card(u.recommendedPanel(recommended)))
			}
		}
	}
	widgets = append(widgets, u.uxmSubtle(13, u.tr("Changes apply the next time you open an agent. Already-open agents may need reopening.", "Los cambios se aplican al volver a abrir un agente. Los agentes abiertos pueden necesitar reiniciarse.")))
	widgets = append(widgets, u.uxmMoreSection())
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, uxmSpaced(16, widgets)...)
	}
}

func uxmSpaced(gap unit.Dp, widgets []layout.Widget) []layout.FlexChild {
	children := make([]layout.FlexChild, 0, 2*len(widgets))
	for i, w := range widgets {
		if i > 0 {
			children = append(children, layout.Rigid(layout.Spacer{Height: gap}.Layout))
		}
		children = append(children, layout.Rigid(w))
	}
	return children
}

// uxmLibrarySearchMinimum is the list size from which the saved-model search appears.
var uxmLibrarySearchMinimum = 8

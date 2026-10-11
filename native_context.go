//go:build desktop

package main

import (
	"fmt"
	"image"
	"image/color"
	"slices"
	"strconv"

	gioevent "gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// Keep the user's requested policy separate from published catalog capacity.
// Only Custom writes a numeric window to models.json; presets remain portable
// when the provider changes the model's capacity.
func nativeLibraryItem(choice nativeModelChoice) modelLibraryItem {
	preset, tokens := choice.ContextPreset, choice.ContextTokens
	if preset == "" {
		preset, tokens = contextPolicyFromLibrary(modelLibraryItem{ContextWindow: choice.Model.ContextWindow})
	}
	if preset != contextPresetCustom {
		tokens = 0
	}
	return modelLibraryItem{
		ID: choice.Model.ID, DisplayName: choice.DisplayName,
		ReasoningEffort: choice.DefaultReasoning, ReasoningLevels: slices.Clone(choice.ReasoningLevels), ReasoningCustom: choice.ReasoningCustom,
		ContextPreset: preset, ContextWindow: tokens, MaxOutputTokens: choice.Model.MaxOutputTokens,
	}
}

func (u *nativeUI) contextCatalogModel(id string) modelInfo {
	for _, model := range u.models {
		if model.ID == id {
			return model
		}
	}
	return modelInfo{ID: id, Name: id}
}

func nativeContextTokens(tokens int) string {
	if tokens >= 1000000 && tokens%1000 == 0 {
		return strconv.FormatFloat(float64(tokens)/1000000, 'f', -1, 64) + "M"
	}
	if tokens >= 1000 && tokens%1000 == 0 {
		return strconv.Itoa(tokens/1000) + "K"
	}
	return strconv.Itoa(tokens)
}

func (u *nativeUI) contextPresetLabel(preset string) string {
	switch preset {
	case contextPresetRecommended:
		return u.tr("Recommended", "Recomendado")
	case contextPresetLow:
		return u.tr("Low", "Bajo")
	case contextPresetMaximum:
		return u.tr("Maximum", "Máximo")
	default:
		return u.tr("Custom", "Personalizado")
	}
}

func (u *nativeUI) contextPresetCaption(preset string, tokens int) string {
	if preset == contextPresetMaximum && tokens <= 0 {
		return u.tr("Model-specific maximum", "Máximo según el modelo")
	}
	if preset == contextPresetCustom && tokens <= 0 {
		return u.tr("Enter a token count", "Introduce el número de tokens")
	}
	if tokens <= 0 {
		switch preset {
		case contextPresetRecommended:
			tokens = contextRecommendedTokens
		case contextPresetLow:
			tokens = contextLowTokens
		}
	}
	return fmt.Sprintf(u.tr("%s tokens", "%s tokens"), nativeContextTokens(tokens))
}

func (u *nativeUI) contextChoiceCaption(key string, choice nativeModelChoice, preset string) string {
	if preset == contextPresetMaximum {
		if choice.Model.ContextWindow < 1024 || choice.Model.ContextWindow > 100000000 {
			return u.tr("Refresh catalog to see the limit", "Actualiza el catálogo para ver el límite")
		}
		return u.contextPresetCaption(preset, choice.Model.ContextWindow)
	}
	tokens := contextRecommendedTokens
	switch preset {
	case contextPresetLow:
		tokens = contextLowTokens
	case contextPresetCustom:
		tokens = choice.ContextTokens
		if tokens == 0 {
			tokens, _ = strconv.Atoi(u.value(nativeClientField(key, choice.Model.ID, "context")))
		}
	}
	if choice.Model.ContextWindow >= 1024 && choice.Model.ContextWindow <= 100000000 {
		tokens = min(tokens, choice.Model.ContextWindow)
	}
	return u.contextPresetCaption(preset, tokens)
}

func nativeModelReasoningChoices(u *nativeUI, values []string) []nativeChoice {
	choices := make([]nativeChoice, 0, len(values))
	for _, value := range values {
		label := value
		switch value {
		case "", "automatic":
			label = u.tr("Automatic", "Automático")
		case "low":
			label = u.tr("Low", "Bajo")
		case "medium":
			label = u.tr("Medium", "Medio")
		case "high":
			label = u.tr("High", "Alto")
		case "minimal":
			label = u.tr("Minimal", "Mínimo")
		case "none":
			label = u.tr("None", "Ninguno")
		case "xhigh":
			label = u.tr("Extra high", "Muy alto")
		}
		choices = append(choices, nativeChoice{Value: value, Label: label})
	}
	return choices
}

func (u *nativeUI) contextChoiceSummary(choice nativeModelChoice) string {
	limits, err := contextPolicyForChoice(choice)
	if err != nil {
		if choice.ContextPreset == contextPresetMaximum && !limits.MaximumKnown {
			return u.tr("Maximum unavailable · refresh the catalog", "Máximo no disponible · actualiza el catálogo")
		}
		return u.tr("Enter a valid custom context window", "Introduce una ventana de contexto válida")
	}
	maximum := u.tr("unknown", "desconocido")
	if limits.MaximumKnown {
		maximum = nativeContextTokens(limits.MaximumContextWindow)
	}
	return fmt.Sprintf(u.tr("Working window: %s · Model maximum: %s", "Ventana de trabajo: %s · Máximo del modelo: %s"), nativeContextTokens(limits.ContextWindow), maximum)
}

func (u *nativeUI) setContextChoice(key string, choice *nativeModelChoice, preset string, tokens int) {
	choice.ContextPreset, choice.ContextTokens = preset, tokens
	if preset != contextPresetCustom {
		choice.ContextTokens = 0
	}
	u.setValue(nativeClientField(key, choice.Model.ID, "context"), strconv.Itoa(choice.ContextTokens))
}

func (u *nativeUI) applySharedContext(preset string, tokens int) bool {
	// Validate the entire operation first so a missing maximum cannot leave a
	// partially updated library. Persisted offline choices are still retained.
	for _, choice := range u.library.selection.Models {
		if _, err := resolveContextPolicy(preset, tokens, choice.Model.ContextWindow, choice.Model.MaxOutputTokens); err != nil {
			u.noticeError(err)
			return false
		}
	}
	for i := range u.library.selection.Models {
		u.setContextChoice(sharedModelKey, &u.library.selection.Models[i], preset, tokens)
	}
	u.expanded["models.context.edit"] = false
	u.setNotice(nativeToneNeutral, "")
	return true
}

func (u *nativeUI) sharedContextDraftError(raw string) string {
	tokens, err := strconv.Atoi(raw)
	if err != nil || tokens < 1024 || tokens > 100000000 {
		return u.tr("Enter a whole token count from 1,024 to 100,000,000.", "Introduce un número entero de tokens entre 1.024 y 100.000.000.")
	}
	for _, choice := range u.library.selection.Models {
		if _, err := resolveContextPolicy(contextPresetCustom, tokens, choice.Model.ContextWindow, choice.Model.MaxOutputTokens); err != nil {
			return u.tr("This token limit cannot be applied to every model.", "Este límite de tokens no se puede aplicar a todos los modelos.")
		}
	}
	return ""
}

func (u *nativeUI) unknownContextMaximumIDs() []string {
	var ids []string
	for _, choice := range u.library.selection.Models {
		if choice.Model.ContextWindow < 1024 || choice.Model.ContextWindow > 100000000 {
			ids = append(ids, choice.Model.ID)
		}
	}
	return ids
}

func (u *nativeUI) contextMaximumBlockerReason(id string) string {
	for _, model := range u.models {
		if model.ID == id {
			if model.ContextWindow > 100000000 {
				return u.tr("Published limit exceeds the supported range", "El límite publicado supera el rango admitido")
			}
			return u.tr("No maximum published in the catalog", "Sin máximo publicado en el catálogo")
		}
	}
	return u.tr("Not in the current catalog", "No aparece en el catálogo actual")
}

func (u *nativeUI) requestContextRemoval(ids []string) {
	u.contextRemovalIDs = slices.Clone(ids)
	u.expanded["models.sort"], u.expanded["models.lab"] = false, false
}

func (u *nativeUI) confirmContextRemoval() {
	if u.library == nil {
		return
	}
	if len(u.models) == 0 {
		u.contextRemovalIDs = nil
		u.setNotice(nativeToneWarning, u.tr("Catalog unavailable. Refresh it before removing models from this list.", "Catálogo no disponible. Actualízalo antes de quitar modelos de esta lista."))
		return
	}
	// A refresh can resolve the missing maximum while the dialog is open.
	// Only remove choices that still block Maximum at the moment of confirmation.
	unknown := u.unknownContextMaximumIDs()
	removed := 0
	for _, id := range u.contextRemovalIDs {
		if slices.Contains(unknown, id) {
			u.library.selection.remove(id)
			removed++
		}
	}
	u.contextRemovalIDs = nil
	if removed > 0 {
		u.expanded["models.context.removed"] = true
	} else {
		u.setNotice(nativeToneInfo, u.tr("No models were removed. Review the refreshed catalog.", "No se ha quitado ningún modelo. Revisa el catálogo actualizado."))
	}
}

func nativeContextRule(gtx layout.Context) layout.Dimensions {
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))
	paint.FillShape(gtx.Ops, nativeInk, clip.Rect{Max: size}.Op())
	return layout.Dimensions{Size: size}
}

func (u *nativeUI) contextMaximumBlockersPanel(ids []string) layout.Widget {
	if len(u.models) == 0 {
		return u.column(
			nativeContextRule,
			u.subheading(u.tr("Catalog unavailable", "Catálogo no disponible")),
			u.note(u.tr("Maximum needs catalog limits. Refresh before deciding which saved models to remove.", "Máximo necesita los límites del catálogo. Actualízalo antes de decidir qué modelos guardados quitar.")),
			u.disabled(!u.busy["POST/api/models"], u.button("models.context.refresh", u.tr("Refresh catalog", "Actualizar catálogo"), u.refreshModels)),
		)
	}
	count := len(ids)
	label := fmt.Sprintf(u.tr("%d models without a known maximum", "%d modelos sin máximo conocido"), count)
	if count == 1 {
		label = u.tr("1 model without a known maximum", "1 modelo sin máximo conocido")
	}
	children := []layout.Widget{
		nativeContextRule,
		u.subheading(label),
		u.note(u.tr("These models prevent Maximum from applying to the whole library. Refresh the catalog or remove them from your saved selection.", "Estos modelos impiden aplicar Máximo a toda la biblioteca. Actualiza el catálogo o quítalos de tu selección guardada.")),
	}
	rows := make([]layout.Widget, 0, len(ids)*2)
	for _, id := range ids {
		id := id
		rows = append(rows, u.actionRow(
			u.column(u.subheading(id), u.note(u.contextMaximumBlockerReason(id))),
			u.button("models.context.remove."+id, u.tr("Remove", "Quitar"), func() { u.requestContextRemoval([]string{id}) }),
		), nativeContextRule)
	}
	if len(ids) > 5 {
		children = append(children, u.scroll("models.context.blockers", unit.Dp(300), rows...))
	} else {
		children = append(children, rows...)
	}
	removeLabel := fmt.Sprintf(u.tr("Remove %d models from my library", "Quitar los %d de mi biblioteca"), count)
	if count == 1 {
		removeLabel = u.tr("Remove the model from my library", "Quitar el modelo de mi biblioteca")
	}
	children = append(children,
		u.pills(
			u.disabled(!u.busy["POST/api/models"], u.button("models.context.refresh", u.tr("Refresh catalog", "Actualizar catálogo"), u.refreshModels)),
			u.primaryButton("models.context.remove-all", removeLabel, func() { u.requestContextRemoval(ids) }),
		),
		u.note(u.tr("Removing a model changes only your saved selection, not your Kilo account or the catalog.", "Quitar un modelo solo cambia tu selección guardada, no tu cuenta de Kilo ni el catálogo.")),
	)
	return u.column(children...)
}

func (u *nativeUI) contextRemovalDialog() layout.Widget {
	ids := slices.Clone(u.contextRemovalIDs)
	count := len(ids)
	label := fmt.Sprintf(u.tr("Remove %d models from your library?", "¿Quitar %d modelos de tu biblioteca?"), count)
	intro := u.tr("These models will leave your shared selection:", "Estos modelos dejarán de estar en tu selección compartida:")
	if count == 1 {
		label = u.tr("Remove this model from your library?", "¿Quitar este modelo de tu biblioteca?")
		intro = u.tr("This model will leave your shared selection:", "Este modelo dejará de estar en tu selección compartida:")
	}
	items := make([]layout.Widget, 0, count)
	for _, id := range ids {
		items = append(items, u.note("•  "+id))
	}
	return u.card(
		u.heading(label),
		u.note(intro),
		u.scroll("models.context.remove.list", unit.Dp(180), items...),
		u.note(u.tr("Your Kilo account does not change. Already-open agents keep their current configuration until reopened.", "Tu cuenta de Kilo no cambia. Los agentes abiertos conservan su configuración hasta que los vuelvas a abrir.")),
		u.actionRow(layout.Spacer{}.Layout,
			u.button("models.context.remove.cancel", u.tr("Cancel", "Cancelar"), func() { u.contextRemovalIDs = nil }),
			u.primaryButton("models.context.remove.confirm", u.tr("Remove from my library", "Quitar de mi biblioteca"), u.confirmContextRemoval),
		),
	)
}

// Render the confirmation above the page so a destructive choice is never
// committed from the diagnostic row itself. The backdrop blocks page clicks.
func (u *nativeUI) layoutContextRemovalDialog(gtx layout.Context) {
	if u.page != "models" {
		u.contextRemovalIDs = nil
		return
	}
	if len(u.contextRemovalIDs) == 0 {
		return
	}
	for {
		e, ok := gtx.Event(pointer.Filter{Target: &u.contextRemovalTag, Kinds: pointer.Press}, key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		switch event := e.(type) {
		case pointer.Event:
			u.contextRemovalIDs = nil
		case key.Event:
			if event.State == key.Press {
				u.contextRemovalIDs = nil
			}
		}
	}
	if len(u.contextRemovalIDs) == 0 {
		return
	}
	recording := op.Record(gtx.Ops)
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	paint.FillShape(gtx.Ops, color.NRGBA{A: 125}, clip.Rect{Max: gtx.Constraints.Max}.Op())
	gioevent.Op(gtx.Ops, &u.contextRemovalTag)
	area.Pop()
	viewport := gtx.Constraints.Max
	width := max(0, min(viewport.X-gtx.Dp(32), gtx.Dp(620)))
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X, gtx.Constraints.Max.X = width, width
		gtx.Constraints.Min.Y = 0
		gtx.Constraints.Max.Y = max(0, viewport.Y-gtx.Dp(32))
		return u.contextRemovalDialog()(gtx)
	})
	op.Defer(gtx.Ops, recording.Stop())
}

func (u *nativeUI) sharedContextPanel() layout.Widget {
	choices := u.library.selection.Models
	common := ""
	unknownMaximum := u.unknownContextMaximumIDs()
	maximumAvailable := len(choices) > 0 && len(unknownMaximum) == 0
	maximumTokens := 0
	maximumVaries := false
	draftTokens := 0
	if len(choices) > 0 {
		common = nativeLibraryItem(choices[0]).ContextPreset
		draftTokens = choices[0].ContextTokens
		maximumTokens = choices[0].Model.ContextWindow
	}
	for _, choice := range choices {
		if nativeLibraryItem(choice).ContextPreset != common {
			common = ""
		}
		if choice.Model.ContextWindow != maximumTokens {
			maximumVaries = true
		}
	}
	if maximumVaries {
		maximumTokens = 0
	}
	if value := u.value("models.context.tokens"); value != "" {
		if tokens, err := strconv.Atoi(value); err == nil {
			draftTokens = tokens
		}
	}
	presets := []string{contextPresetRecommended, contextPresetLow, contextPresetMaximum, contextPresetCustom}
	presetChoices := make([]nativeChoice, 0, len(presets))
	for _, preset := range presets {
		tokens := 0
		switch preset {
		case contextPresetMaximum:
			tokens = maximumTokens
		case contextPresetCustom:
			if common == contextPresetCustom {
				tokens = draftTokens
			}
		}
		caption := u.contextPresetCaption(preset, tokens)
		if preset == contextPresetMaximum && len(unknownMaximum) > 0 {
			if len(u.models) == 0 {
				caption = u.tr("Catalog unavailable", "Catálogo no disponible")
			} else if len(unknownMaximum) == 1 {
				caption = u.tr("Unavailable with 1 model", "No disponible con 1 modelo")
			} else {
				caption = fmt.Sprintf(u.tr("Unavailable with %d models", "No disponible con %d modelos"), len(unknownMaximum))
			}
		}
		presetChoices = append(presetChoices, nativeChoice{Value: preset, Label: u.contextPresetLabel(preset), Caption: caption, Disabled: preset == contextPresetMaximum && !maximumAvailable})
	}
	widgets := []layout.Widget{
		u.optionCards("models.context.", presetChoices, common, len(choices) > 0, func(preset string) {
			if preset == contextPresetCustom {
				u.expanded["models.context.edit"] = true
				if u.value("models.context.tokens") == "" {
					tokens := contextRecommendedTokens
					if common == contextPresetCustom && len(choices) > 0 {
						tokens = choices[0].ContextTokens
					}
					u.setValue("models.context.tokens", strconv.Itoa(tokens))
				}
				return
			}
			if preset == contextPresetMaximum && !maximumAvailable {
				return
			}
			u.applySharedContext(preset, 0)
		}),
	}
	if len(unknownMaximum) > 0 {
		widgets = append(widgets, u.contextMaximumBlockersPanel(unknownMaximum))
	} else if u.expanded["models.context.removed"] && len(choices) > 0 {
		widgets = append(widgets,
			u.message(nativeToneSuccess, u.tr("All saved models have a known maximum. You can now choose Maximum.", "Todos los modelos guardados tienen un máximo conocido. Ya puedes elegir Máximo.")),
			u.pills(u.button("models.context.add-replacements", u.tr("Add replacement models", "Añadir modelos sustitutos"), func() {
				u.expanded["library.catalog"] = true
				if len(u.models) == 0 {
					u.refreshModels()
				}
			})),
		)
	}
	widgets = append(widgets, u.disclosure("models.context.edit", u.tr("Edit limits", "Editar límites")))
	if u.expanded["models.context.edit"] {
		draft := u.value("models.context.tokens")
		validation := u.sharedContextDraftError(draft)
		widgets = append(widgets, u.field("models.context.tokens", u.tr("Custom tokens", "Tokens personalizados"), "272000", false))
		if validation != "" {
			widgets = append(widgets, u.message(nativeToneError, validation))
		}
		widgets = append(widgets, u.pills(u.disabled(validation == "", u.button("models.context.apply", u.tr("Apply to all models", "Aplicar a todos los modelos"), func() {
			tokens, err := strconv.Atoi(u.value("models.context.tokens"))
			if err == nil {
				u.applySharedContext(contextPresetCustom, tokens)
			}
		}))))
	}
	widgets = append(widgets, u.note(u.tr("Windows are capped to model capacity. Changes apply when you reopen an agent.", "Las ventanas se ajustan a la capacidad del modelo. Los cambios se aplican al volver a abrir un agente.")))
	return u.section(u.tr("Context window", "Ventana de contexto"), u.tr("Apply to all saved models. New models start at Recommended; use Edit for a model override.", "Aplica a todos los modelos guardados. Los nuevos usan Recomendado; usa Editar para cambiar uno."), widgets...)
}

func (u *nativeUI) contextChoiceControls(key string, choice *nativeModelChoice) layout.Widget {
	preset := nativeLibraryItem(*choice).ContextPreset
	options := []string{contextPresetRecommended, contextPresetLow, contextPresetMaximum, contextPresetCustom}
	choices := make([]nativeChoice, 0, len(options))
	for _, option := range options {
		choices = append(choices, nativeChoice{Value: option, Label: u.contextPresetLabel(option), Caption: u.contextChoiceCaption(key, *choice, option)})
	}
	fieldID := nativeClientField(key, choice.Model.ID, "context")
	editID := nativeClientField(key, choice.Model.ID, "context-edit")
	widgets := []layout.Widget{u.note(u.tr("Context window", "Ventana de contexto")), u.optionCards(nativeClientField(key, choice.Model.ID, "context-preset:"), choices, preset, true, func(option string) {
		if option == contextPresetMaximum && (choice.Model.ContextWindow < 1024 || choice.Model.ContextWindow > 100000000) {
			return
		}
		tokens := 0
		if option == contextPresetCustom {
			tokens = contextRecommendedTokens
			if current, err := strconv.Atoi(u.value(fieldID)); err == nil && current >= 1024 && current <= 100000000 {
				tokens = current
			} else if limits, err := contextPolicyForChoice(*choice); err == nil {
				tokens = limits.ContextWindow
			}
		}
		u.setContextChoice(key, choice, option, tokens)
		if option == contextPresetCustom {
			u.expanded[editID] = true
		}
	})}
	if preset == contextPresetMaximum && (choice.Model.ContextWindow < 1024 || choice.Model.ContextWindow > 100000000) {
		widgets = append(widgets, u.hint(u.tr("Refresh the catalog to see this model's maximum.", "Actualiza el catálogo para ver el máximo de este modelo.")))
	}
	if preset == contextPresetCustom {
		widgets = append(widgets, u.disclosure(editID, u.tr("Edit limits", "Editar límites")))
	}
	if u.expanded[editID] && preset == contextPresetCustom {
		draft := u.value(fieldID)
		validation := ""
		if tokens, err := strconv.Atoi(draft); err != nil || tokens < 1024 || tokens > 100000000 {
			validation = u.tr("Enter a whole token count from 1,024 to 100,000,000.", "Introduce un número entero de tokens entre 1.024 y 100.000.000.")
		}
		widgets = append(widgets, u.field(fieldID, u.tr("Custom tokens", "Tokens personalizados"), "272000", false))
		if validation != "" {
			widgets = append(widgets, u.message(nativeToneError, validation))
		}
	}
	return u.column(widgets...)
}

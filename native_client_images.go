//go:build desktop

package main

import (
	"encoding/json"
	"slices"

	"gioui.org/layout"
)

func nativeImageModels(models []modelInfo) []modelInfo {
	images := make([]modelInfo, 0)
	for _, model := range models {
		if slices.Contains(model.OutputModalities, "image") {
			images = append(images, model)
		}
	}
	slices.SortFunc(images, func(a, b modelInfo) int { return compareModelOrder(a, b, "name", "", "") })
	return images
}

func nativeImageProvider(images imageGenerationSettings) string {
	if images.Provider == "" {
		return "kilo"
	}
	return images.Provider
}

func nativeImageConnectionReady(images imageGenerationSettings, state map[string]any) bool {
	if state == nil {
		return true
	}
	if nativeImageProvider(images) == "chatgpt" {
		if ready, ok := state["chatgptReady"].(bool); ok {
			return ready
		}
		account := nativeMap(state["chatgpt"])
		return nativeBool(account, "connected") && nativeString(account, "status") != "pending"
	}
	if ready, ok := state["kiloReady"].(bool); ok {
		return ready
	}
	return nativeBool(state, "hasKey") && nativeString(state, "orgId") != ""
}

func nativeClientImagesReady(s *nativeClientSelection, models []modelInfo, states ...map[string]any) bool {
	if s.ImageGeneration == nil || !s.ImageGeneration.Enabled {
		return true
	}
	if validateImageGenerationSettings(*s.ImageGeneration) != nil {
		return false
	}
	if nativeImageProvider(*s.ImageGeneration) == "chatgpt" && len(states) > 0 && !nativeImageConnectionReady(*s.ImageGeneration, states[0]) {
		return false
	}
	if nativeImageProvider(*s.ImageGeneration) == "chatgpt" {
		return true
	}
	for _, model := range nativeImageModels(models) {
		if model.ID == s.ImageGeneration.Model && catalogID.MatchString(model.ID) {
			return true
		}
	}
	return false
}

func cloneClientImageSettings(value *imageGenerationSettings) *imageGenerationSettings {
	if value == nil {
		return nil
	}
	copy := *value
	if !copy.Enabled && !catalogID.MatchString(copy.Model) {
		copy.Model = ""
	}
	return &copy
}

// Both profiles share the saved backend setting, while each can have a pending
// draft. Apply a successful save/load to untouched views without losing edits.
func (u *nativeUI) acceptClientImages(saved *imageGenerationSettings) {
	if saved == nil {
		return
	}
	for _, key := range []string{"codex", "codex-cli"} {
		s := u.clientState().selection(key)
		if s.ImageGeneration == nil || s.imageGenerationBaseline != nil && *s.ImageGeneration == *s.imageGenerationBaseline {
			s.ImageGeneration = cloneClientImageSettings(saved)
		}
		s.imageGenerationBaseline = cloneClientImageSettings(saved)
	}
}

// Initialize only when the authenticated state includes image settings. Polling
// must never replace a draft; an explicit profile load restores saved settings.
func (u *nativeUI) seedClientImages(key string, s *nativeClientSelection) {
	if key != "codex" && key != "codex-cli" || s.ImageGeneration != nil {
		return
	}
	value, ok := u.state["imageGeneration"]
	if !ok || value == nil {
		return
	}
	data, err := json.Marshal(value)
	var images imageGenerationSettings
	if err == nil && json.Unmarshal(data, &images) == nil {
		s.ImageGeneration = cloneClientImageSettings(&images)
		s.imageGenerationBaseline = cloneClientImageSettings(&images)
	}
}

func nativeEditClientImages(s *nativeClientSelection, edit func(*imageGenerationSettings)) {
	images := imageGenerationSettings{}
	if s.ImageGeneration != nil {
		images = *s.ImageGeneration
	}
	edit(&images)
	s.ImageGeneration = cloneClientImageSettings(&images)
}

func (u *nativeUI) saveClientImages(s *nativeClientSelection) {
	if s.ImageGeneration == nil || u.busy["POST/api/image-generation"] {
		return
	}
	sent := *s.ImageGeneration
	if err := validateImageGenerationSettings(sent); err != nil {
		u.noticeError(err)
		return
	}
	u.call("POST", "/api/image-generation", sent, func(raw json.RawMessage) {
		var response struct {
			Images imageGenerationSettings `json:"imageGeneration"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			u.noticeError(err)
			return
		}
		if s.ImageGeneration != nil && *s.ImageGeneration == sent {
			s.ImageGeneration = cloneClientImageSettings(&response.Images)
		}
		u.acceptClientImages(&response.Images)
		u.state["imageGeneration"] = response.Images
		u.notice, u.noticeTone = u.tr("Image settings saved. Reopen your agent to refresh its image tool.", "Ajustes de imágenes guardados. Vuelve a abrir el agente para actualizar su herramienta de imágenes."), nativeToneSuccess
	})
}

func (u *nativeUI) clientImagesPanel(key string, s *nativeClientSelection) layout.Widget {
	prefix := "client:" + key + ":images"
	images := imageGenerationSettings{}
	if s.ImageGeneration != nil {
		images = *s.ImageGeneration
	}
	models := nativeImageModels(u.models)
	u.setChecked(prefix+":enabled", images.Enabled)
	children := []layout.Widget{
		u.eyebrow(u.tr("OPTIONAL TOOL", "HERRAMIENTA OPCIONAL")),
		u.heading(u.tr("Image generation", "Generación de imágenes")),
		u.note(u.tr("Choose which account generates images. Your coding models stay independent.", "Elige qué cuenta genera las imágenes. Los modelos de programación son independientes.")),
		u.disabled(s.ImageGeneration != nil, u.check(prefix+":enabled", u.tr("Enable image generation", "Activar generación de imágenes"), func(enabled bool) {
			nativeEditClientImages(s, func(value *imageGenerationSettings) { value.Enabled = enabled })
		})),
	}
	if images.Enabled {
		provider := nativeImageProvider(images)
		label := u.tr("Kilo · account credits", "Kilo · crédito de la cuenta")
		if provider == "chatgpt" {
			label = u.tr("ChatGPT subscription · Experimental", "Suscripción de ChatGPT · Experimental")
		}
		children = append(children, u.note(u.tr("Image provider", "Proveedor de imágenes")), u.dropdownButton(prefix+":provider.toggle", label, func() { u.expanded[prefix+":provider"] = !u.expanded[prefix+":provider"] }))
		if u.expanded[prefix+":provider"] {
			for _, choice := range []struct{ id, en, es string }{{"kilo", "Kilo · account credits", "Kilo · crédito de la cuenta"}, {"chatgpt", "ChatGPT subscription · Experimental", "Suscripción de ChatGPT · Experimental"}} {
				children = append(children, u.menuItem(prefix+":provider:"+choice.id, u.tr(choice.en, choice.es), provider == choice.id, func() {
					nativeEditClientImages(s, func(value *imageGenerationSettings) { value.Provider = choice.id })
					u.expanded[prefix+":provider"] = false
				}))
			}
		}
		if !nativeImageConnectionReady(images, u.state) {
			message := u.tr("Connect your Kilo account and organization in Settings → Connection before generating images.", "Conecta tu cuenta y organización de Kilo en Ajustes → Conexión antes de generar imágenes.")
			if provider == "chatgpt" {
				message = u.tr("Sign in with ChatGPT in Settings → Connection before generating images.", "Inicia sesión con ChatGPT en Ajustes → Conexión antes de generar imágenes.")
			}
			children = append(children, u.message(nativeToneWarning, message))
		}
		if provider == "chatgpt" {
			children = append(children, u.note(u.tr("Uses ChatGPT's built-in image tool and subscription quota. Availability depends on your account. No separate image model or OpenAI API key is needed; requests never fall back to Kilo credits.", "Usa la herramienta de imágenes de ChatGPT y la cuota de tu suscripción. La disponibilidad depende de tu cuenta. No necesitas otro modelo de imágenes ni una API key de OpenAI; las peticiones nunca recurren al crédito de Kilo.")))
		} else {
			label := u.tr("Choose an image model", "Elige un modelo de imágenes")
			selected := false
			for _, model := range models {
				if model.ID == images.Model {
					label, selected = model.Name, true
					if label == "" {
						label = model.ID
					}
				}
			}
			if images.Model != "" && !selected {
				label = images.Model + u.tr(" · not in current catalog", " · fuera del catálogo actual")
			}
			children = append(children, u.note(u.tr("Image model", "Modelo de imágenes")), u.dropdownButton(prefix+":model.toggle", label, func() { u.expanded[prefix] = !u.expanded[prefix] }))
			if u.expanded[prefix] {
				choices := make([]layout.Widget, 0, len(models))
				for _, model := range models {
					model := model
					name := model.Name
					if name == "" {
						name = model.ID
					}
					choices = append(choices, u.menuItem(prefix+":model:"+model.ID, name, model.ID == images.Model, func() {
						nativeEditClientImages(s, func(value *imageGenerationSettings) { value.Model = model.ID })
						u.expanded[prefix] = false
					}))
				}
				if len(choices) > 4 {
					children = append(children, u.scroll(prefix+":options", 250, choices...))
				} else {
					children = append(children, choices...)
				}
			}
			if selected {
				children = append(children, u.note(images.Model))
			} else {
				children = append(children, u.note(u.tr("Refresh the catalog and choose a model with image output. You can disable this tool to keep using Codex without it.", "Actualiza el catálogo y elige un modelo con salida de imagen. Puedes desactivar la herramienta para seguir usando Codex sin ella.")))
			}
			children = append(children, u.button(prefix+":refresh", u.tr("Refresh image models", "Actualizar modelos de imágenes"), u.refreshModels))
			children = append(children, u.note(u.tr("Uses your configured Kilo organization. Provider or gateway charges depend on its billing setup.", "Usa tu organización de Kilo configurada. Los cargos del proveedor o gateway dependen de su facturación.")))
		}
	}
	children = append(children,
		u.note(u.tr("Original images are saved locally. Editing supports images created with this tool.", "Las imágenes originales se guardan en local. La edición admite imágenes creadas con esta herramienta.")),
		u.note(u.tr("Shared by agents using the image MCP. Save these settings, then reopen your agent to refresh its tools. No Codex profile is required to save.", "Compartido por los agentes que usan el MCP de imágenes. Guarda los ajustes y vuelve a abrir el agente para actualizar sus herramientas. No necesitas un perfil de Codex para guardarlos.")),
	)
	saveLabel := u.tr("Save image settings", "Guardar ajustes de imágenes")
	if u.busy["POST/api/image-generation"] {
		saveLabel = u.tr("Saving…", "Guardando…")
	}
	children = append(children, u.disabled(s.ImageGeneration != nil && validateImageGenerationSettings(images) == nil && !u.busy["POST/api/image-generation"], u.primaryButton(prefix+":save", saveLabel, func() { u.saveClientImages(s) })))
	return u.card(children...)
}

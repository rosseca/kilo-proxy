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

func nativeClientImagesReady(s *nativeClientSelection, models []modelInfo) bool {
	if s.ImageGeneration == nil || !s.ImageGeneration.Enabled {
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
		u.note(u.tr("Give Codex an image tool with its own model. Your coding models stay independent.", "Añade una herramienta de imágenes a Codex con su propio modelo. Los modelos de programación son independientes.")),
		u.disabled(s.ImageGeneration != nil, u.check(prefix+":enabled", u.tr("Enable image generation", "Activar generación de imágenes"), func(enabled bool) {
			nativeEditClientImages(s, func(value *imageGenerationSettings) { value.Enabled = enabled })
		})),
	}
	if images.Enabled {
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
		children = append(children, u.note(u.tr("Image model", "Modelo de imágenes")), u.button(prefix+":model.toggle", label+"  ▾", func() { u.expanded[prefix] = !u.expanded[prefix] }))
		if u.expanded[prefix] {
			choices := make([]layout.Widget, 0, len(models))
			for _, model := range models {
				name := model.Name
				if name == "" {
					name = model.ID
				}
				choices = append(choices, u.button(prefix+":model:"+model.ID, name, func() {
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
	}
	children = append(children,
		u.note(u.tr("Uses your Kilo organization's balance. Generation is billed when Codex calls the tool; editing currently supports images created with this tool.", "Usa el saldo de tu organización de Kilo. Se factura cuando Codex llama a la herramienta; la edición admite por ahora imágenes creadas con ella.")),
		u.note(u.tr("Saved with Prepare or Launch. This setting is shared by Codex GUI and CLI. Restart Codex after preparing to load the tool.", "Se guarda al Preparar o Abrir. El ajuste se comparte entre Codex GUI y CLI. Reinicia Codex después de preparar para cargar la herramienta.")),
	)
	return u.card(children...)
}

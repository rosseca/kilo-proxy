//go:build desktop

package main

import (
	"encoding/json"

	"gioui.org/layout"
	"gioui.org/widget"
)

func nativeImageDependency(state map[string]any) imageTransportDependency {
	var dependency imageTransportDependency
	raw, err := json.Marshal(state["imageTransportDependency"])
	if err == nil {
		_ = json.Unmarshal(raw, &dependency)
	}
	return dependency
}

func (u *nativeUI) imageDependency() imageTransportDependency {
	return nativeImageDependency(u.state)
}

func (u *nativeUI) acceptImageDependencyState(state map[string]any) {
	previous, next := u.imageDependency(), nativeImageDependency(state)
	// Dismissal belongs to this session and mode. A new proxy start or a
	// return to Cloudflare gives the user another chance to resolve it.
	if !next.Required || next.Installed || !previous.Required ||
		(!nativeBool(u.state, "running") && nativeBool(state, "running")) {
		u.imageDependencyDismissed = false
	}
}

func (u *nativeUI) imageDependencyNoticeVisible() bool {
	dependency := u.imageDependency()
	u.owner.mu.Lock()
	required := normalizeImageTransportSettings(u.owner.config.ImageTransport).Mode == "cloudflare"
	u.owner.mu.Unlock()
	// A state request already in flight when the preference was saved may
	// still describe the old mode. Never warn over an explicit alternative.
	return required && dependency.Required && !dependency.Installed && !u.imageDependencyDismissed
}

func (u *nativeUI) showImageSettings() {
	u.page = "settings"
	u.imageSettingsFocus = true
	u.list("page.settings").ScrollTo(0)
}

// Keep the normal Settings order while the notice link scrolls directly to
// Large images. Apply the offset after the list finishes its current layout.
func (u *nativeUI) imageSettingsConnectionPanel() layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		dims := u.connectionPanel()(gtx)
		if u.imageSettingsFocus {
			u.imageSettingsFocus = false
			offset := dims.Size.Y + gtx.Dp(8)
			u.enqueue(func() {
				if u.page == "settings" {
					u.list("page.settings").Position.Offset = offset
				}
			})
		}
		return dims
	}
}

func (u *nativeUI) imageDependencyNotice(settings bool) layout.Widget {
	dependency := u.imageDependency()
	id := "images.dependency."
	if settings {
		id += "settings."
	}
	buttons := []layout.Widget{
		u.button(id+"instructions", u.tr("Install instructions", "Instrucciones de instalación"), func() { u.open(dependency.InstallURL) }),
	}
	if dependency.InstallCommand != "" {
		buttons = append(buttons, u.button(id+"copy", u.tr("Copy install command", "Copiar comando de instalación"), func() { u.copy(dependency.InstallCommand) }))
	}
	buttons = append(buttons, u.disabled(!u.busy["GET/api/state"], u.button(id+"check", u.tr("Check again", "Comprobar de nuevo"), u.refreshState)))
	if !settings {
		buttons = append(buttons,
			u.ghostButton(id+"settings", u.tr("Image settings", "Ajustes de imágenes"), u.showImageSettings),
			u.ghostButton(id+"dismiss", u.tr("Not now", "Ahora no"), func() { u.imageDependencyDismissed = true }),
		)
	}
	content := u.column(
		u.subheading(u.tr("Install cloudflared for large images", "Instala cloudflared para imágenes grandes")),
		u.note(u.tr("Cloudflare needs cloudflared on this computer. You can start the proxy now, or choose another image method in Settings.", "Cloudflare necesita cloudflared en este equipo. Puedes arrancar el proxy ahora o elegir otro método para imágenes en Ajustes.")),
		u.pills(buttons...),
	)
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return widget.Border{Color: nativeWarningLine, Width: 1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return nativeBox(gtx, nativeWarningBG, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(12).Layout(gtx, content)
			})
		})
	}
}

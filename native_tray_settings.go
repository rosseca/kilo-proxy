//go:build desktop

package main

import (
	"encoding/json"
	"runtime"

	"gioui.org/layout"
)

func (u *nativeUI) loadTraySettings() {
	u.owner.mu.Lock()
	display := normalizeTrayDisplay(u.owner.config.TrayDisplay)
	u.owner.mu.Unlock()
	u.setValue("appearance.tray", display)
}

func (u *nativeUI) saveTraySettings(display string) {
	if display != trayDisplayIcon && display != trayDisplaySpend {
		return
	}
	u.call("PUT", "/api/tray-settings", traySettingsResponse{Display: display}, func(raw json.RawMessage) {
		var saved traySettingsResponse
		if json.Unmarshal(raw, &saved) == nil && saved.Display == display {
			u.setValue("appearance.tray", saved.Display)
		}
	})
}

func (u *nativeUI) appearancePanel() layout.Widget {
	saving := u.busy["PUT/api/tray-settings"]
	if !saving {
		u.loadTraySettings()
	}
	selected := u.value("appearance.tray")
	choices := []layout.Widget{}
	for _, choice := range []struct{ value, en, es string }{
		{trayDisplayIcon, "K icon", "Icono K"},
		{trayDisplaySpend, "Session cost", "Coste de esta sesión"},
	} {
		label := "○ " + u.tr(choice.en, choice.es)
		if selected == choice.value {
			label = "● " + u.tr(choice.en, choice.es)
		}
		choices = append(choices, u.disabled(!saving, u.button("appearance.tray."+choice.value, label, func() { u.saveTraySettings(choice.value) })))
	}
	platformNote := u.tr("The menu bar shows the amount in place of the K icon.", "La barra de menús muestra el importe en lugar del icono K.")
	switch runtime.GOOS {
	case "windows":
		platformNote = u.tr("Windows keeps the K icon. The amount appears on hover and in the tray menu.", "Windows mantiene el icono K. El importe aparece al pasar el cursor y en el menú de la bandeja.")
	case "linux":
		platformNote = u.tr("The K icon stays available. Your desktop may show the amount beside it; the tray menu always includes it.", "El icono K sigue disponible. Tu escritorio puede mostrar el importe a su lado; el menú de la bandeja siempre lo incluye.")
	}
	children := []layout.Widget{
		u.heading(u.tr("Appearance", "Apariencia")),
		u.note(u.tr("Tray display", "Mostrar en la bandeja")),
		u.row(choices...),
		u.note(platformNote),
		u.note(u.tr("Session cost covers this Kilo Proxy process, including all connected clients and images. It resets when you quit and reopen the app; stopping the proxy or clearing captures keeps it.", "El coste de esta sesión incluye todos los clientes conectados y las imágenes de este proceso de Kilo Proxy. Se reinicia al salir y volver a abrir la app; detener el proxy o borrar capturas lo mantiene.")),
		u.note(u.tr("An asterisk marks a reported subtotal when some requests have no cost. A dash means no cost has been reported. Reported inference costs can differ from your organization's Kilo charge.", "Un asterisco indica un subtotal informado cuando faltan costes de algunas peticiones. Un guion indica que no se ha informado ningún coste. El coste de inferencia informado puede diferir del cargo de tu organización en Kilo.")),
	}
	if saving {
		children = append(children, u.note(u.tr("Saving appearance…", "Guardando apariencia…")))
	}
	return u.card(children...)
}

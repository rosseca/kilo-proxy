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
	if display != trayDisplayIcon && display != trayDisplaySpend && display != trayDisplayBalance && display != trayDisplayChatGPTQuota {
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
	choices := []nativeChoice{{Value: trayDisplayIcon, Label: u.tr("K icon", "Icono K")}, {Value: trayDisplaySpend, Label: u.tr("Session cost", "Coste de esta sesión")}, {Value: trayDisplayBalance, Label: u.tr("Kilo balance", "Saldo de Kilo")}, {Value: trayDisplayChatGPTQuota, Label: u.tr("ChatGPT quota", "Cuota de ChatGPT")}}
	platformNote := u.tr("Cost and balance modes show the K icon with the amount beside it in the menu bar.", "Los modos de coste y saldo muestran el icono K con el importe a su lado en la barra de menús.")
	switch runtime.GOOS {
	case "windows":
		platformNote = u.tr("Windows keeps the K icon. The amount appears on hover and in the tray menu.", "Windows mantiene el icono K. El importe aparece al pasar el cursor y en el menú de la bandeja.")
	case "linux":
		platformNote = u.tr("The K icon stays available. Your desktop may show the amount beside it; the tray menu always includes it.", "El icono K sigue disponible. Tu escritorio puede mostrar el importe a su lado; el menú de la bandeja siempre lo incluye.")
	}
	languageChoices := []nativeChoice{{Value: "en", Label: u.tr("English", "English")}, {Value: "es", Label: u.tr("Español", "Español")}}
	children := []layout.Widget{
		u.subheading(u.tr("Language", "Idioma")),
		u.note(u.tr("Choose the language used throughout Kilo Proxy.", "Elige el idioma de Kilo Proxy.")),
		u.segmented("language.", languageChoices, u.language, true, u.setLanguage),
		u.subheading(u.tr("Tray display", "Mostrar en la bandeja")),
		u.note(platformNote),
		u.note(u.tr("ChatGPT quota shows the remaining percentage in its primary usage window; it is independent of Kilo balance and charges.", "La cuota de ChatGPT muestra el porcentaje disponible en su ventana principal; es independiente del saldo y los cargos de Kilo.")),
		u.segmented("appearance.tray.", choices, selected, !saving, u.saveTraySettings),
		u.note(u.tr("Account balance is provided by Kilo, not a separate BYOK provider. Session cost covers this app and resets when you quit; reported inference costs may differ from your organization's Kilo charge.", "Kilo proporciona el saldo de la cuenta; no incluye el de un proveedor BYOK externo. El coste de sesión corresponde a esta aplicación y se reinicia al salir; el coste de inferencia informado puede diferir del cargo de tu organización.")),
		u.note(u.tr("Closing the window keeps your proxy running. Quit from the system tray.", "Cerrar la ventana mantiene el proxy activo. Sal desde la bandeja del sistema.")),
	}
	if saving {
		children = append(children, u.note(u.tr("Saving appearance…", "Guardando apariencia…")))
	}
	return nativeSettingsPanelWithGap(u.section(u.tr("Appearance", "Apariencia"), u.tr("Language and system-tray display.", "Idioma y apariencia de la bandeja del sistema."), children...))
}

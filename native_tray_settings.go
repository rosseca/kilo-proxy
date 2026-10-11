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

// appearancePanel is the Preferences block: language, menu-bar display and
// what closing the window does.
func (u *nativeUI) appearancePanel() layout.Widget {
	saving := u.busy["PUT/api/tray-settings"]
	if !saving {
		u.loadTraySettings()
	}
	selected := u.value("appearance.tray")
	choices := []nativeChoice{{Value: trayDisplayIcon, Label: u.tr("K only", "Solo la K")}, {Value: trayDisplaySpend, Label: u.tr("Session spend", "Gasto de la sesión")}, {Value: trayDisplayBalance, Label: u.tr("Kilo balance", "Saldo Kilo")}, {Value: trayDisplayChatGPTQuota, Label: u.tr("ChatGPT quota", "Cuota ChatGPT")}}
	caveat := u.tr("Amounts show beside the K. Session spend resets when you quit; ChatGPT quota is separate from Kilo balance.", "El importe aparece junto a la K. El gasto de la sesión se reinicia al salir; la cuota de ChatGPT va aparte del saldo de Kilo.")
	switch runtime.GOOS {
	case "windows":
		caveat = u.tr("Windows keeps the K icon; the amount shows on hover and in the tray menu. ChatGPT quota is separate from Kilo balance.", "Windows mantiene la K; el importe se ve al pasar el cursor y en el menú. La cuota de ChatGPT va aparte del saldo de Kilo.")
	case "linux":
		caveat = u.tr("Your desktop may show the amount beside the K; the tray menu always does. ChatGPT quota is separate from Kilo balance.", "Tu escritorio puede mostrar el importe junto a la K; el menú siempre lo muestra. La cuota de ChatGPT va aparte del saldo de Kilo.")
	}
	if selected == trayDisplayIcon {
		caveat = ""
	}
	if saving {
		caveat = u.tr("Saving…", "Guardando…")
	}
	languageChoices := []nativeChoice{{Value: "en", Label: "English"}, {Value: "es", Label: "Español"}}
	return u.uxsBlock(u.tr("Preferences", "Preferencias"), "",
		u.uxsRow(u.tr("Language", "Idioma"), "", u.segmented("language.", languageChoices, u.language, true, u.setLanguage), nil, nil),
		u.uxsRow(u.tr("Menu bar", "Barra de menú"), u.tr("What shows next to the K", "Qué se ve junto a la K"), u.uxsValue(u.segmented("appearance.tray.", choices, selected, !saving, u.saveTraySettings), caveat), nil, nil),
		u.uxsRow(u.tr("When you close the window", "Al cerrar la ventana"), "", u.uxsValue(u.uxsPlain(u.tr("The proxy keeps running", "El proxy sigue en marcha")), u.tr("To quit completely, use the K menu.", "Para salir del todo, usa el menú de la K.")), nil, nil),
	)
}

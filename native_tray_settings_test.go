//go:build desktop

package main

import (
	"image"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func TestNativeTrayAppearancePointerAndPersistence(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {780, 700}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"-"+lang, func(t *testing.T) {
				u := nativeTestUI(t)
				u.setLanguage(lang)
				nativeTestWait(t, u, func() bool { return u.languageTarget == "" && !u.busy["GET/api/state"] })
				u.page = "settings"
				u.settingsTab = "preferences"
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				// Terminal discovery below Preferences can change the scroll extent.
				// Wait for that layout to settle, then reveal the control itself;
				// scrolling to the bottom depends on unrelated Settings sections.
				nativeTestWait(t, u, func() bool {
					return !u.busy["GET"+nativeTerminalCommandsEndpoint] && !u.busy["GET"+nativeTerminalManualEndpoint] && !u.busy["GET/api/state"]
				})
				h.frame()
				h.reveal(u.tr("Session spend", "Gasto de la sesión"), semantic.Button)
				h.click(u.tr("Session spend", "Gasto de la sesión"), semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.busy["PUT/api/tray-settings"] })
				h.frame()
				if !h.selected(u.tr("Session spend", "Gasto de la sesión"), semantic.Button) {
					t.Fatal("session cost is not semantically selected")
				}
				saved, err := readSettings(u.owner.dir)
				if err != nil || saved.TrayDisplay != trayDisplaySpend {
					t.Fatalf("tray preference did not persist through the UI: %+v %v", saved, err)
				}
				nativeGridCapture(t, h, "appearance-"+fmtSize(size)+"-"+lang)
				h.click(u.tr("Kilo balance", "Saldo Kilo"), semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.busy["PUT/api/tray-settings"] })
				saved, err = readSettings(u.owner.dir)
				if err != nil || saved.TrayDisplay != trayDisplayBalance {
					t.Fatalf("balance preference did not persist: %+v %v", saved, err)
				}
				h.frame()
				if !h.selected(u.tr("Kilo balance", "Saldo Kilo"), semantic.Button) {
					t.Fatal("account balance is not semantically selected")
				}
				h.click(u.tr("ChatGPT quota", "Cuota ChatGPT"), semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.busy["PUT/api/tray-settings"] })
				saved, err = readSettings(u.owner.dir)
				if err != nil || saved.TrayDisplay != trayDisplayChatGPTQuota {
					t.Fatalf("ChatGPT quota preference did not persist: %+v %v", saved, err)
				}
				h.frame()
				h.click(u.tr("K only", "Solo la K"), semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.busy["PUT/api/tray-settings"] })
				saved, err = readSettings(u.owner.dir)
				if err != nil || saved.TrayDisplay != trayDisplayIcon {
					t.Fatalf("restoring the icon did not persist: %+v %v", saved, err)
				}
				h.frame()
				if !h.selected(u.tr("K only", "Solo la K"), semantic.Button) {
					t.Fatal("K icon is not semantically selected")
				}
				if !nativeUpdateHasText(h, u.tr("The proxy keeps running", "El proxy sigue en marcha")) || !nativeUpdateHasText(h, u.tr("To quit completely, use the K menu.", "Para salir del todo, usa el menú de la K.")) {
					t.Fatal("closing the window no longer explains that the proxy keeps running")
				}
			})
		}
	}
}

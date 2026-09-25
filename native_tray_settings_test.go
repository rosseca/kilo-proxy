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
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				// Terminal discovery below Appearance can change the scroll extent.
				// Wait for that layout to settle, then reveal the control itself;
				// scrolling to the bottom depends on unrelated Settings sections.
				nativeTestWait(t, u, func() bool {
					return !u.busy["GET"+nativeTerminalCommandsEndpoint] && !u.busy["GET"+nativeTerminalManualEndpoint] && !u.busy["GET/api/state"]
				})
				h.frame()
				h.reveal(u.tr("Session cost", "Coste de esta sesión"), semantic.Button)
				h.click(u.tr("Session cost", "Coste de esta sesión"), semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.busy["PUT/api/tray-settings"] })
				h.frame()
				if !h.selected(u.tr("Session cost", "Coste de esta sesión"), semantic.Button) {
					t.Fatal("session cost is not semantically selected")
				}
				saved, err := readSettings(u.owner.dir)
				if err != nil || saved.TrayDisplay != trayDisplaySpend {
					t.Fatalf("tray preference did not persist through the UI: %+v %v", saved, err)
				}
				nativeGridCapture(t, h, "appearance-"+fmtSize(size)+"-"+lang)
				h.click(u.tr("Account balance", "Saldo de la cuenta"), semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.busy["PUT/api/tray-settings"] })
				saved, err = readSettings(u.owner.dir)
				if err != nil || saved.TrayDisplay != trayDisplayBalance {
					t.Fatalf("balance preference did not persist: %+v %v", saved, err)
				}
				h.frame()
				if !h.selected(u.tr("Account balance", "Saldo de la cuenta"), semantic.Button) {
					t.Fatal("account balance is not semantically selected")
				}
				h.click(u.tr("K icon", "Icono K"), semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.busy["PUT/api/tray-settings"] })
				saved, err = readSettings(u.owner.dir)
				if err != nil || saved.TrayDisplay != trayDisplayIcon {
					t.Fatalf("restoring the icon did not persist: %+v %v", saved, err)
				}
				h.frame()
				if !h.selected(u.tr("K icon", "Icono K"), semantic.Button) {
					t.Fatal("K icon is not semantically selected")
				}
			})
		}
	}
}

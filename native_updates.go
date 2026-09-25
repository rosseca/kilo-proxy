//go:build desktop

package main

import (
	"encoding/json"
	"fmt"

	"gioui.org/layout"
	"gioui.org/widget"
)

func (u *nativeUI) releaseUpdate() releaseUpdateState {
	var update releaseUpdateState
	nativeDecode(u.state["update"], &update)
	if update.CurrentVersion == "" {
		update.CurrentVersion = version
	}
	return update
}

func (u *nativeUI) updateAvailable() bool {
	update := u.releaseUpdate()
	return update.Available && update.LatestVersion != "" && validReleaseUpdateURL(update.ReleaseURL)
}

func (u *nativeUI) openUpdateRelease() {
	update := u.releaseUpdate()
	// The general desktop bridge accepts HTTPS links. Updates have a tighter
	// boundary: only a stable release tag in this application's repository.
	if !update.Available || !validReleaseUpdateURL(update.ReleaseURL) {
		return
	}
	u.open(update.ReleaseURL)
}

func (u *nativeUI) checkForUpdates() {
	if u.busy["POST/api/updates"] || u.releaseUpdate().Checking {
		return
	}
	u.updateRevision++
	u.updateRequestFailed = false
	u.clientRequest("POST", "/api/updates", map[string]any{}, func(raw json.RawMessage, err error) {
		u.updateRevision++
		var update releaseUpdateState
		if err != nil || json.Unmarshal(raw, &update) != nil {
			u.updateRequestFailed = true
			return
		}
		u.state["update"] = update
	})
}

func (u *nativeUI) updateReleaseButton(id string) layout.Widget {
	return u.iconButton(id, u.tr("Download update", "Descargar actualización"), nativeButtonSecondary, nativeIconOpenInNew, u.openUpdateRelease)
}

func (u *nativeUI) updateNotice() layout.Widget {
	update := u.releaseUpdate()
	content := u.actionRow(
		u.subheading(fmt.Sprintf(u.tr("Kilo Proxy %s is available", "Kilo Proxy %s está disponible"), update.LatestVersion)),
		u.updateReleaseButton("updates.notice.open"),
	)
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return widget.Border{Color: nativeInfoBorder, Width: 1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return nativeBox(gtx, nativeInfoBG, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(12).Layout(gtx, content)
			})
		})
	}
}

func (u *nativeUI) updatesPanel() layout.Widget {
	update := u.releaseUpdate()
	checking := update.Checking || u.busy["POST/api/updates"]
	status, tone := u.tr("Not checked yet", "Sin comprobar"), nativeToneNeutral
	switch {
	case checking:
		status, tone = u.tr("Checking for updates…", "Comprobando actualizaciones…"), nativeToneInfo
	case update.Error != "" || u.updateRequestFailed:
		status, tone = u.tr("Could not check for updates. Try again.", "No se han podido comprobar las actualizaciones. Inténtalo de nuevo."), nativeToneWarning
	case u.updateAvailable():
		status, tone = fmt.Sprintf(u.tr("Version %s is available", "La versión %s está disponible"), update.LatestVersion), nativeToneInfo
	case !update.Available && update.LatestVersion != "" && validReleaseUpdateURL(update.ReleaseURL) && nativeUpdatedAt(update.CheckedAt) != "":
		status, tone = u.tr("Up to date", "Actualizado"), nativeToneSuccess
	}
	lastCheck := nativeUpdatedAt(update.CheckedAt)
	if lastCheck == "" {
		lastCheck = u.tr("Not checked yet", "Sin comprobar")
	}
	buttons := []layout.Widget{u.disabled(!checking, u.iconButton("updates.check", u.tr("Check for updates", "Comprobar actualizaciones"), nativeButtonSecondary, nativeIconRefresh, u.checkForUpdates))}
	if u.updateAvailable() {
		buttons = append(buttons, u.updateReleaseButton("updates.settings.open"))
	}
	return u.section(u.tr("App updates", "Actualizaciones de la app"),
		u.tr("Downloads open on GitHub. Updates are never installed automatically.", "Las descargas se abren en GitHub. Las actualizaciones nunca se instalan automáticamente."),
		u.label(fmt.Sprintf(u.tr("Current version: %s", "Versión actual: %s"), update.CurrentVersion)),
		u.statusBadge(tone, status),
		u.note(u.tr("Last checked: ", "Última comprobación: ")+lastCheck),
		u.note(u.tr("Checks on startup and every 6 hours.", "Se comprueba al iniciar y cada 6 horas.")),
		u.pills(buttons...),
	)
}

//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	if u.busy["POST/api/updates"] || u.releaseUpdate().Checking || u.packageUpdateWorking() {
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

func nativePackageUpdateManager(method string) string {
	switch method {
	case "brew-cask", "brew-formula":
		return "Homebrew"
	case "apt":
		return "APT"
	}
	return ""
}

func (u *nativeUI) packageUpdateWorking() bool {
	return u.busy["POST/api/updates/install"] || u.packageUpdateStarted || u.releaseUpdate().Installing
}

func packageUpdateFailureCode(code string) bool {
	return code == "update_start_failed" || code == "package_update_failed" || code == "installation_changed" || code == "package_manager_unavailable"
}

func (u *nativeUI) packageUpdateMessage(code string) string {
	switch code {
	case "detecting_installation":
		return u.tr("Checking how Kilo Proxy was installed…", "Comprobando cómo se instaló Kilo Proxy…")
	case "package_update_failed":
		return u.tr("The last package update failed. Review its terminal output, then try again or update manually.", "La última actualización del paquete falló. Revisa su terminal e inténtalo de nuevo o actualiza manualmente.")
	case "update_start_failed":
		return u.tr("Could not open the update terminal. Try again or update manually.", "No se pudo abrir la terminal de actualización. Inténtalo de nuevo o actualiza manualmente.")
	case "run_as_user":
		return u.tr("Open Kilo Proxy as your regular user to update it.", "Abre Kilo Proxy con tu usuario habitual para actualizarlo.")
	case "installation_changed":
		return u.tr("The installation changed. Reopen Kilo Proxy before updating.", "La instalación ha cambiado. Vuelve a abrir Kilo Proxy antes de actualizar.")
	case "package_manager_unavailable":
		return u.tr("The package manager is unavailable. Update manually or download the release.", "El gestor de paquetes no está disponible. Actualiza manualmente o descarga la versión.")
	}
	return ""
}

func (u *nativeUI) canInstallUpdate() bool {
	update := u.releaseUpdate()
	return u.updateAvailable() && update.CanInstall && nativePackageUpdateManager(update.InstallMethod) != "" && !update.Checking && update.Error == "" && strings.TrimPrefix(update.ReleaseURL, releaseUpdatePagePrefix+"v") == update.LatestVersion
}

func (u *nativeUI) preparePackageUpdate() {
	if !u.canInstallUpdate() || u.packageUpdateWorking() {
		return
	}
	update := u.releaseUpdate()
	u.packageUpdateConfirmVersion, u.packageUpdateConfirmMethod = update.LatestVersion, update.InstallMethod
	u.packageUpdateFailed = false
	// The confirmation lives in the Updates block of Settings; jump there.
	u.page, u.settingsTab = "settings", "updates"
}

func (u *nativeUI) installPackageUpdate() {
	update := u.releaseUpdate()
	if !u.canInstallUpdate() || u.packageUpdateWorking() || u.packageUpdateConfirmVersion != update.LatestVersion || u.packageUpdateConfirmMethod != update.InstallMethod {
		u.packageUpdateConfirmVersion = ""
		return
	}
	u.packageUpdateConfirmVersion = ""
	u.packageUpdateFailed = false
	u.updateRevision++
	u.clientRequest("POST", "/api/updates/install", map[string]any{"version": update.LatestVersion, "confirm": true}, func(raw json.RawMessage, err error) {
		u.updateRevision++
		var result struct {
			Started bool `json:"started"`
		}
		if err != nil || json.Unmarshal(raw, &result) != nil || !result.Started {
			u.packageUpdateFailed = true
			return
		}
		u.packageUpdateStarted = true
	})
}

func (u *nativeUI) packageUpdateButton(id string) layout.Widget {
	return u.disabled(!u.packageUpdateWorking(), u.primaryButton(id, u.tr("Update and restart", "Actualizar y reiniciar"), u.preparePackageUpdate))
}

func (u *nativeUI) updateReleaseButton(id string) layout.Widget {
	return u.iconButton(id, u.tr("Download update", "Descargar actualización"), nativeButtonSecondary, nativeIconOpenInNew, u.openUpdateRelease)
}

func (u *nativeUI) updateNotice() layout.Widget {
	update := u.releaseUpdate()
	actions := []layout.Widget{u.updateReleaseButton("updates.notice.open")}
	if u.canInstallUpdate() {
		actions = append([]layout.Widget{u.packageUpdateButton("updates.notice.install")}, actions...)
	}
	content := u.actionRow(
		u.subheading(fmt.Sprintf(u.tr("Kilo Proxy %s is available", "Kilo Proxy %s está disponible"), update.LatestVersion)),
		u.pills(actions...),
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

// updatesPanel is the Updates block: one Version row with status, last check
// and "Check now", followed by the confirmed package-update flow.
func (u *nativeUI) updatesPanel() layout.Widget {
	update := u.releaseUpdate()
	if u.packageUpdateConfirmVersion != "" && (!u.canInstallUpdate() || u.packageUpdateConfirmVersion != update.LatestVersion || u.packageUpdateConfirmMethod != update.InstallMethod) {
		u.packageUpdateConfirmVersion = ""
	}
	checking := update.Checking || u.busy["POST/api/updates"]
	working := u.packageUpdateWorking()
	status, tone := u.tr("not checked yet", "sin comprobar"), nativeToneNeutral
	switch {
	case u.busy["POST/api/updates/install"]:
		status, tone = u.tr("opening the update terminal…", "abriendo la terminal de actualización…"), nativeToneInfo
	case working:
		status, tone = u.tr("waiting for the update terminal…", "esperando a la terminal de actualización…"), nativeToneInfo
	case u.packageUpdateFailed:
		status, tone = u.tr("could not start the update", "no se pudo iniciar la actualización"), nativeToneWarning
	case checking:
		status, tone = u.tr("checking…", "comprobando…"), nativeToneInfo
	case update.Error != "" || u.updateRequestFailed:
		status, tone = u.tr("could not check", "no se pudo comprobar"), nativeToneWarning
	case u.updateAvailable():
		status, tone = fmt.Sprintf(u.tr("version %s is available", "hay una nueva: %s"), update.LatestVersion), nativeToneInfo
	case !update.Available && update.LatestVersion != "" && validReleaseUpdateURL(update.ReleaseURL) && nativeUpdatedAt(update.CheckedAt) != "":
		status, tone = u.tr("up to date", "al día"), nativeToneSuccess
	}
	schedule := u.tr("Checks on startup and every 6 h. Never installs without asking.", "Se revisa al abrir y cada 6 h. Nunca se instala sin preguntarte.")
	if ago := u.uxsAgo(update.CheckedAt, time.Now()); ago != "" {
		schedule = u.tr("Checked ", "Comprobado ") + ago + u.tr(" · checks on startup and every 6 h. Never installs without asking.", " · se revisa al abrir y cada 6 h. Nunca se instala sin preguntarte.")
	}
	small := []string{schedule}
	if u.packageUpdateFailed && !working {
		small = append(small, u.tr("Try again or download it manually.", "Inténtalo de nuevo o descárgala manualmente."))
	}
	versionLine := u.uxsPlain(u.tr("Version ", "Versión ") + update.CurrentVersion)
	statusLine := u.uxsStatus(tone, status, "")
	line := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(versionLine),
			layout.Rigid(layout.Spacer{Width: 10}.Layout),
			layout.Rigid(statusLine),
		)
	}
	value := u.uxsValue(line, small...)
	buttons := []layout.Widget{}
	if u.updateAvailable() {
		if u.canInstallUpdate() {
			buttons = append(buttons, u.packageUpdateButton("updates.settings.install"))
		}
		buttons = append(buttons, u.updateReleaseButton("updates.settings.open"))
	}
	buttons = append(buttons, u.disabled(!checking && !working, u.iconButton("updates.check", u.tr("Check now", "Buscar ahora"), nativeButtonSecondary, nativeIconRefresh, u.checkForUpdates)))
	below := []layout.Widget{}
	if u.canInstallUpdate() && !working && u.packageUpdateConfirmVersion == update.LatestVersion && u.packageUpdateConfirmMethod == update.InstallMethod {
		manager := nativePackageUpdateManager(update.InstallMethod)
		below = append(below, u.message(nativeToneWarning, fmt.Sprintf(u.tr("Update to %s with %s? This closes Kilo Proxy and interrupts active requests. A terminal opens; APT may ask for your administrator password there. Kilo Proxy restarts after a successful update.", "¿Actualizar a %s con %s? Se cerrará Kilo Proxy y se interrumpirán las peticiones activas. Se abrirá una terminal; APT puede pedirte allí la contraseña de administrador. Kilo Proxy se reiniciará si la actualización termina correctamente."), update.LatestVersion, manager)), u.pills(
			u.primaryButton("updates.confirm", u.tr("Confirm update and restart", "Confirmar actualización y reinicio"), u.installPackageUpdate),
			u.button("updates.cancel", u.tr("Cancel", "Cancelar"), func() { u.packageUpdateConfirmVersion = "" }),
		))
	}
	if message := u.packageUpdateMessage(update.InstallMessage); message != "" && !working {
		below = append(below, u.note(message))
	}
	if manager := nativePackageUpdateManager(update.InstallMethod); manager != "" {
		below = append(below, u.note(fmt.Sprintf(u.tr("Installed with %s. Updates run in a visible terminal after your confirmation.", "Instalado con %s. Las actualizaciones se ejecutan en una terminal visible tras tu confirmación."), manager)))
		if !update.CanInstall && !working {
			below = append(below, u.note(u.tr("Package update is unavailable for this installation. Use your package manager manually or download the release.", "La actualización del paquete no está disponible para esta instalación. Usa el gestor manualmente o descarga la versión.")))
		}
	}
	var area layout.Widget
	if len(below) > 0 {
		area = u.column(below...)
	}
	return u.uxsBlock(u.tr("Updates", "Actualizaciones"), "", u.uxsRow(u.tr("Version", "Versión"), "Kilo Proxy", value, u.uxsActions(buttons...), area))
}

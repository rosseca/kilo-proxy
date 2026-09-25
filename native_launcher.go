//go:build desktop

package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gioui.org/layout"
)

const nativeLaunchEndpoint = "/api/clients/launch"

type nativeLaunchClient struct {
	Available  bool   `json:"available"`
	Installed  bool   `json:"installed"`
	InstallURL string `json:"installURL,omitempty"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	Reason     string `json:"reason"`
}

func nativeLaunchClientInstalled(info nativeLaunchClient) bool {
	return info.Installed || info.Available || info.Path != ""
}

type nativeLaunchInfo struct {
	Platform  string                        `json:"platform"`
	Directory string                        `json:"directory"`
	Clients   map[string]nativeLaunchClient `json:"clients"`
}

// Requests use the same authenticated local API as manual profile preparation.
// Keeping a completion callback for errors lets a multi-step launch always
// release its busy state, including when preparation fails.
func (u *nativeUI) clientRequest(method, path string, payload any, done func(json.RawMessage, error)) {
	id := method + path
	if u.busy[id] {
		done(nil, errors.New(u.tr("An editor request is already running.", "Ya hay una operación del editor en curso.")))
		return
	}
	u.busy[id] = true
	go func() {
		raw, err := nativeRequest(u.owner, method, path, payload)
		u.enqueue(func() { delete(u.busy, id); done(raw, err) })
	}()
}

func (u *nativeUI) detectLaunchers() {
	c := u.clientState()
	if u.busy["GET"+nativeLaunchEndpoint] {
		return
	}
	c.LaunchDetectStarted = true
	before := u.value("clients-project-directory")
	u.clientRequest("GET", nativeLaunchEndpoint, nil, func(raw json.RawMessage, err error) {
		var detected nativeLaunchInfo
		if err == nil {
			err = json.Unmarshal(raw, &detected)
		}
		if err != nil {
			c.LaunchError = nativeMessage(err.Error(), u.language)
			return
		}
		c.LaunchInfo = detected
		if !c.LaunchChecked && before == "" && u.value("clients-project-directory") == before {
			u.setValue("clients-project-directory", c.LaunchInfo.Directory)
		}
		c.LaunchChecked = true
		c.LaunchError = ""
	})
}

func (u *nativeUI) nativeLaunchAvailable(key string) bool {
	c := u.clientState()
	if !c.LaunchChecked {
		return false
	}
	return c.LaunchInfo.Clients[key].Available || key == "codex" && strings.TrimSpace(u.value("clients-launch-app-path")) != ""
}

func (u *nativeUI) clientLauncherPanel(key string, s *nativeClientSelection, canPrepare bool) layout.Widget {
	c := u.clientState()
	info := c.LaunchInfo.Clients[key]
	working := c.Launching != "" || u.busy["POST"+nativeClientEndpoint(key)] || u.busy["GET"+nativeClientEndpoint(key)]
	launchable := u.nativeLaunchAvailable(key)
	connectionReady := u.agentConnectionReady() && !u.setupConnectionNeeded()
	connectionWorking := u.connectionWorking()
	enabled := launchable && canPrepare && connectionReady && !connectionWorking && !working
	label := u.tr("Open", "Abrir")
	if c.Launching == key {
		label = u.tr("Opening…", "Abriendo…")
	}
	widgets := []layout.Widget{}
	if key == "codex" {
		widgets = append(widgets, u.field("clients-launch-app-path", u.tr("Codex application path (optional)", "Ruta de la aplicación Codex (opcional)"), info.Path, false))
	}
	openButton := u.disabled(enabled, u.primaryButton("client:"+key+":launch", label, func() { u.launchClient(key) }))
	if !clientLaunchUsesProject(key) {
		widgets = append(widgets, u.pills(openButton))
	} else {
		widgets = append(widgets, u.actionRow(u.field("clients-project-directory", u.tr("Project folder", "Carpeta del proyecto"), c.LaunchInfo.Directory, false), openButton))
	}
	widgets = append(widgets,
		u.pills(u.disabled(!u.busy["GET"+nativeLaunchEndpoint], u.iconButton("clients-launch-detect", u.tr("Refresh installed apps", "Actualizar aplicaciones instaladas"), nativeButtonGhost, nativeIconRefresh, u.detectLaunchers))),
	)
	installTone, installStatus := nativeToneInfo, u.tr("Checking installation…", "Comprobando instalación…")
	if c.LaunchChecked {
		if launchable || terminalClientSupported(key) && nativeLaunchClientInstalled(info) {
			installTone, installStatus = nativeToneSuccess, u.tr("Installed", "Instalado")
		} else {
			installTone, installStatus = nativeToneNeutral, u.tr("Not found", "No encontrado")
		}
	}
	widgets = append(widgets, u.statusBadge(installTone, installStatus))
	switch {
	case !c.LaunchChecked:
		widgets = append(widgets, u.hint(u.tr("Wait for installed-app detection to finish.", "Espera a que termine la detección de aplicaciones instaladas.")))
	case !launchable:
		reason := nativeMessage(info.Reason, u.language)
		if reason == "" {
			name, _ := launchClientIdentity(key)
			reason = fmt.Sprintf(u.tr("Install %s, then refresh installed apps.", "Instala %s y actualiza las aplicaciones instaladas."), name)
		}
		widgets = append(widgets, u.hint(reason))
	case !connectionReady:
		widgets = append(widgets, u.hint(u.tr("Save your Kilo connection before opening this app.", "Guarda tu conexión de Kilo antes de abrir esta aplicación.")))
	case connectionWorking:
		widgets = append(widgets, u.hint(u.tr("Wait for the Kilo connection update to finish.", "Espera a que termine la actualización de la conexión de Kilo.")))
	case working:
		widgets = append(widgets, u.hint(u.tr("Wait for the current profile operation to finish.", "Espera a que termine la operación actual del perfil.")))
	}
	if c.LaunchError != "" {
		widgets = append(widgets, u.message(nativeToneError, c.LaunchError))
	}
	return u.column(widgets...)
}

func (u *nativeUI) launchClient(key string) {
	u.launchClientFrom(key, "clients-project-directory")
}

func (u *nativeUI) launchAgent(key string) {
	u.agentsState()
	if !clientLaunchUsesProject(key) {
		u.launchClientFrom(key, "")
		return
	}
	field := agentProjectField(key)
	if u.value(field) == "" {
		u.setValue(field, u.agentProject(key))
	}
	u.launchClientFrom(key, field)
}

// Include the saved connection as well as displayed state: a connection save
// may finish before the next UI state refresh arrives.
func (u *nativeUI) launchConnectionFingerprint() [32]byte {
	u.owner.mu.Lock()
	data, _ := json.Marshal([]any{u.owner.config.Port, u.owner.config.OrgID, u.owner.config.LocalKey, u.owner.apiKey})
	u.owner.mu.Unlock()
	return sha256.Sum256(data)
}

// A save is part of the launch transaction. Snapshot the actual draft as well
// as its exported payload so neither a newer library nor a malformed numeric
// edit can silently replace what the user asked to open while disk IO is busy.
func (u *nativeUI) launchSettingsFingerprint(key, directoryField string) [32]byte {
	s := u.sharedClientSelection(key)
	u.syncClientSelection(key, s)
	base, local, _ := u.clientBase()
	limits := make([]string, 0, len(u.library.selection.Models)*2)
	for _, model := range u.library.selection.Models {
		limits = append(limits, u.value(nativeClientField(sharedModelKey, model.Model.ID, "context")), u.value(nativeClientField(sharedModelKey, model.Model.ID, "output")))
	}
	appPath := ""
	if key == "codex" {
		appPath = u.value("clients-launch-app-path")
	}
	data, _ := json.Marshal([]any{nativeSelectionFingerprint(key, s, base, local, u.clientCaps(key)), u.modelLibraryValue(), limits, u.value(directoryField), appPath, u.launchConnectionFingerprint()})
	return sha256.Sum256(data)
}

func (u *nativeUI) launchSettingsChangedMessage() string {
	return u.tr("Your launch settings changed while preparing. Launch again to use the latest changes.", "Los ajustes cambiaron durante la preparación. Vuelve a abrir para aplicar los últimos cambios.")
}

func (u *nativeUI) launchClientFrom(key, directoryField string) {
	if !clientLaunchUsesProject(key) {
		directoryField = "" // Desktop apps manage their own project selection.
	}
	a := u.agentsState()
	c := u.clientState()
	if c.Launching != "" || key == "claude-desktop" && u.busy["POST/api/claude-desktop/options"] {
		return
	}
	u.persistLibraryEdits()
	if status, ready := u.libraryStatus(); !ready {
		w := u.library.writer
		w.mu.Lock()
		saving := w.running
		w.mu.Unlock()
		if saving {
			pending := u.launchSettingsFingerprint(key, directoryField)
			c.Launching = key
			a.Phase = u.tr("Saving models…", "Guardando modelos…")
			go func() {
				w.flush()
				u.enqueue(func() {
					c.Launching, a.Phase = "", ""
					if pending != u.launchSettingsFingerprint(key, directoryField) {
						u.setNotice(nativeToneWarning, u.launchSettingsChangedMessage())
						return
					}
					u.launchClientFrom(key, directoryField)
				})
			}()
		} else {
			u.setNotice(nativeToneWarning, status)
		}
		return
	}
	if !u.nativeLaunchAvailable(key) {
		u.setNotice(nativeToneWarning, u.tr("Refresh installed apps or set a valid Codex application path.", "Actualiza las aplicaciones instaladas o indica una ruta válida de Codex."))
		return
	}
	s := u.sharedClientSelection(key)
	u.syncClientSelection(key, s)
	if _, err := nativeClientPayload(key, s); err != nil || len(s.Models) == 0 {
		u.setNotice(nativeToneWarning, u.tr("Choose valid models before launching.", "Elige modelos válidos antes de abrir."))
		if err != nil {
			u.noticeError(err)
		}
		return
	}
	directory, appPath := u.value(directoryField), u.value("clients-launch-app-path")
	if !clientLaunchUsesProject(key) {
		directory = ""
	}
	resolvedDirectory := ""
	if clientLaunchUsesProject(key) {
		var err error
		resolvedDirectory, err = launchPath(directory, c.LaunchInfo.Directory)
		if err != nil {
			u.noticeError(err)
			return
		}
	}
	prepared := u.launchSettingsFingerprint(key, directoryField)
	c.Launching = key
	a.Phase = u.tr("Preparing…", "Preparando…")
	finish := func(err error) {
		c.Launching = ""
		a.Phase = ""
		if err != nil {
			u.noticeError(err)
		}
	}
	launch := func(err error) {
		if err != nil {
			finish(err)
			return
		}
		current := u.sharedClientSelection(key)
		if current != s || prepared != u.launchSettingsFingerprint(key, directoryField) {
			finish(errors.New(u.launchSettingsChangedMessage()))
			return
		}
		payload := map[string]string{"client": key, "directory": directory}
		if !clientLaunchUsesProject(key) {
			delete(payload, "directory")
		}
		if key == "open-design" {
			payload["engine"] = u.openDesignEngine()
		}
		a.Phase = u.tr("Opening…", "Abriendo…")
		if key == "codex" {
			payload["appPath"] = appPath
		}
		u.clientRequest("POST", nativeLaunchEndpoint, payload, func(raw json.RawMessage, err error) {
			if err != nil {
				finish(err)
				return
			}
			var result struct {
				OK      bool   `json:"ok"`
				Message string `json:"message"`
			}
			if err = json.Unmarshal(raw, &result); err != nil {
				finish(err)
				return
			}
			if !result.OK {
				finish(errors.New(u.tr("The editor did not confirm launch.", "El editor no confirmó el arranque.")))
				return
			}
			finish(nil)
			if key != "open-design" && key != "claude-desktop" {
				u.rememberAgentProject(key, resolvedDirectory)
			}
			u.setNotice(nativeToneWarning, result.Message)
			if u.notice == "" {
				u.setNotice(nativeToneSuccess, u.tr("Editor launched.", "Editor abierto."))
			}
			u.refreshState()
		})
	}
	// A managed profile can be edited externally between launches. Reapply the
	// shared library each time; the backend preserves unrelated preferences.
	u.prepareClientAfter(key, launch)
}

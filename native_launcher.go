//go:build desktop

package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"

	"gioui.org/layout"
)

const nativeLaunchEndpoint = "/api/clients/launch"

type nativeLaunchClient struct {
	Available bool   `json:"available"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	Reason    string `json:"reason"`
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
	enabled := u.nativeLaunchAvailable(key) && canPrepare && !working
	label := u.tr("Launch", "Abrir")
	if c.Launching == key {
		label = u.tr("Launching…", "Abriendo…")
	}
	widgets := []layout.Widget{}
	if key == "codex" {
		widgets = append(widgets, u.field("clients-launch-app-path", u.tr("Codex application (optional custom path)", "Aplicación Codex (ruta personalizada opcional)"), info.Path, false))
	}
	widgets = append(widgets, u.actionRow(u.field("clients-project-directory", u.tr("Project folder (optional)", "Carpeta del proyecto (opcional)"), c.LaunchInfo.Directory, false), u.disabled(enabled, u.button("client:"+key+":launch", label, func() { u.launchClient(key) }))), u.pills(u.disabled(!u.busy["GET"+nativeLaunchEndpoint], u.button("clients-launch-detect", u.tr("Refresh installed apps", "Actualizar aplicaciones instaladas"), u.detectLaunchers))))
	message := u.tr("Checking installed applications…", "Comprobando aplicaciones instaladas…")
	if c.LaunchError != "" {
		message = c.LaunchError
	} else if c.LaunchChecked {
		if !u.nativeLaunchAvailable(key) {
			message = info.Reason
			if message == "" {
				message = u.tr("This client was not found on this computer.", "No se encontró este cliente en este equipo.")
			}
		} else {
			message = u.tr("Launch prepares unsaved changes and starts the saved proxy on this computer.", "Abrir prepara los cambios pendientes e inicia el proxy guardado en este equipo.")
			if key == "cursor" {
				message = u.tr("Launch opens Cursor using the existing HTTPS tunnel.", "Abrir inicia Cursor con el túnel HTTPS existente.")
			}
		}
	}
	widgets = append(widgets, u.note(message))
	return u.column(widgets...)
}

func (u *nativeUI) launchClient(key string) {
	u.launchClientFrom(key, "clients-project-directory")
}

func (u *nativeUI) launchAgent(key string) {
	u.agentsState()
	if key == "open-design" {
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
	if key == "open-design" {
		directoryField = "" // Open Design has no supported project-folder launch argument.
	}
	a := u.agentsState()
	c := u.clientState()
	if c.Launching != "" {
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
						u.notice = u.launchSettingsChangedMessage()
						return
					}
					u.launchClientFrom(key, directoryField)
				})
			}()
		} else {
			u.notice = status
		}
		return
	}
	if !u.nativeLaunchAvailable(key) {
		u.notice = u.tr("Refresh installed apps or set a valid Codex application path.", "Actualiza las aplicaciones instaladas o indica una ruta válida de Codex.")
		return
	}
	s := u.sharedClientSelection(key)
	u.syncClientSelection(key, s)
	if key == "cursor" {
		var session cursorSession
		data, _ := json.Marshal(u.state["cursor"])
		_ = json.Unmarshal(data, &session)
		if session.Status != "running" {
			u.notice = u.tr("Connect the Cursor HTTPS tunnel first.", "Conecta primero el túnel HTTPS de Cursor.")
			return
		}
	} else if _, err := nativeClientPayload(key, s); err != nil || len(s.Models) == 0 {
		u.notice = u.tr("Choose valid models before launching.", "Elige modelos válidos antes de abrir.")
		if err != nil {
			u.notice = err.Error()
		}
		return
	}
	directory, appPath := u.value(directoryField), u.value("clients-launch-app-path")
	if key == "open-design" {
		directory = ""
	}
	resolvedDirectory, err := launchPath(directory, c.LaunchInfo.Directory)
	if err != nil {
		u.notice = nativeMessage(err.Error(), u.language)
		return
	}
	prepared := u.launchSettingsFingerprint(key, directoryField)
	c.Launching = key
	a.Phase = u.tr("Preparing…", "Preparando…")
	finish := func(err error) {
		c.Launching = ""
		a.Phase = ""
		if err != nil {
			u.notice = nativeMessage(err.Error(), u.language)
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
			if key != "open-design" {
				u.rememberAgentProject(key, resolvedDirectory)
			}
			u.notice = result.Message
			if u.notice == "" {
				u.notice = u.tr("Editor launched.", "Editor abierto.")
			}
			u.refreshState()
		})
	}
	// A managed profile can be edited externally between launches. Reapply the
	// shared library each time; the backend preserves unrelated preferences.
	if key == "cursor" {
		launch(nil)
	} else {
		u.prepareClientAfter(key, launch)
	}
}

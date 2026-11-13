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
	c.LaunchRefreshPending = false
	c.LaunchDetectStarted = true
	u.clientRequest("GET", nativeLaunchEndpoint, nil, func(raw json.RawMessage, err error) {
		if c.LaunchRefreshPending {
			u.detectLaunchers()
			return
		}
		var detected nativeLaunchInfo
		if err == nil {
			err = json.Unmarshal(raw, &detected)
		}
		if err != nil {
			c.LaunchError = nativeMessage(err.Error(), u.language)
			return
		}
		c.LaunchInfo = detected
		c.LaunchChecked = true
		c.LaunchError = ""
	})
}

// A saved account policy can change availability while an older detection is
// still in flight. Discard that response and refresh after it completes.
func (u *nativeUI) refreshOmnigentLaunchDiscovery() { u.refreshSynaraLaunchDiscovery() }

func (u *nativeUI) refreshSynaraLaunchDiscovery() {
	c := u.clientState()
	c.LaunchChecked = false
	c.LaunchRefreshPending = true
	if !u.busy["GET"+nativeLaunchEndpoint] {
		u.detectLaunchers()
	}
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
		widgets = append(widgets, u.actionRow(u.field(agentProjectField(key), u.tr("Project folder (empty = the general one)", "Carpeta del proyecto (vacía = la general)"), u.uxhGlobalFolder(), false), openButton))
	}
	widgets = append(widgets,
		u.pills(u.disabled(!u.busy["GET"+nativeLaunchEndpoint], u.iconButton("clients-launch-detect", u.tr("Refresh installed apps", "Actualizar aplicaciones instaladas"), nativeButtonGhost, nativeIconRefresh, u.detectLaunchers))),
	)
	installTone, installStatus := nativeToneInfo, u.tr("Checking installation…", "Comprobando instalación…")
	if c.LaunchChecked {
		if launchable || nativeLaunchClientInstalled(info) {
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
		widgets = append(widgets, u.hint(u.tr("Connect Kilo or ChatGPT before opening this app.", "Conecta Kilo o ChatGPT antes de abrir esta aplicación.")))
	case connectionWorking:
		widgets = append(widgets, u.hint(u.tr("Wait for the connection update to finish.", "Espera a que termine la actualización de la conexión.")))
	case working:
		widgets = append(widgets, u.hint(u.tr("Wait for the current profile operation to finish.", "Espera a que termine la operación actual del perfil.")))
	}
	if c.LaunchError != "" {
		widgets = append(widgets, u.message(nativeToneError, c.LaunchError))
	}
	return u.column(widgets...)
}

func (u *nativeUI) launchClient(key string) {
	u.launchAgent(key)
}

// launchAgent opens a harness in its own folder override or, when it has none,
// the general project folder.
func (u *nativeUI) launchAgent(key string) {
	u.agentsState()
	u.launchClientFrom(key, agentProjectField(key))
}

// uxhLaunchDirectory is the folder a launch uses. An empty harness override
// falls back to the general folder, which is read at launch time so a change
// made while preparing is detected like any other launch setting.
func (u *nativeUI) uxhLaunchDirectory(key, directoryField string) string {
	if !clientLaunchUsesProject(key) || directoryField == "" {
		return ""
	}
	directory := strings.TrimSpace(u.value(directoryField))
	if directory == "" && directoryField == agentProjectField(key) {
		directory = u.uxhGlobalFolder()
	}
	return directory
}

// Include the saved connection as well as displayed state: a connection save
// may finish before the next UI state refresh arrives.
func (u *nativeUI) launchConnectionFingerprint() [32]byte {
	return u.owner.readUIProjection().Fingerprint
}

// A save is part of the launch transaction. Snapshot the actual draft as well
// as its exported payload so neither a newer library nor a malformed numeric
// edit can silently replace what the user asked to open while disk IO is busy.
func (u *nativeUI) launchSettingsFingerprint(key, directoryField string) [32]byte {
	return u.launchSettingsFingerprintWithPreferences(key, directoryField, true)
}

func (u *nativeUI) launchSettingsFingerprintWithPreferences(key, directoryField string, includePreferences bool) [32]byte {
	if key == "muse" {
		values := []any{u.uxhLaunchDirectory(key, directoryField), u.clientState().LaunchInfo.Directory, u.clientState().LaunchInfo.Clients[key].Path}
		if includePreferences {
			prefs := &u.clientState().CLIPreferences
			values = append(values, prefs.Loaded, prefs.Preferences, prefs.Pending, prefs.Generation, u.value(nativeCLIPathField("codex-cli")), u.value(nativeCLIPathField("claude")), u.value(nativeCLIPathField("muse")), u.value(nativeCLIPathField("synara")), u.value(nativeCLIPathField("omnigent")))
			muse := &u.clientState().Muse
			values = append(values, muse.Loaded, muse.Preferences, muse.Pending, muse.Generation)
			if !u.museNative() {
				u.initModelLibrary()
				values = append(values, u.modelLibraryValue(), u.launchConnectionFingerprint())
			}
		}
		data, _ := json.Marshal(values)
		return sha256.Sum256(data)
	}
	if key == "omnigent" && includePreferences && u.omnigentNativeOnly() {
		paths, prefs := &u.clientState().CLIPreferences, &u.agentsState().Omnigent.PreferencesState
		data, _ := json.Marshal([]any{paths.Loaded, paths.Preferences, paths.Pending, paths.Generation, u.value(nativeCLIPathField("codex-cli")), u.value(nativeCLIPathField("claude")), u.value(nativeCLIPathField("omnigent")), u.clientState().LaunchInfo.Clients[key].Path, prefs.Loaded, prefs.Preferences, prefs.Pending, prefs.Generation})
		return sha256.Sum256(data)
	}
	if key == "t3-code" && includePreferences && !u.t3CodeNeedsSavedLibrary() {
		paths := &u.clientState().CLIPreferences
		openCode := &u.clientState().OpenCodePreferences
		prefs := &u.agentsState().T3Code.PreferencesState
		values := []any{paths.Loaded, paths.Preferences, paths.Pending, paths.Generation,
			u.value(nativeCLIPathField("codex-cli")), u.value(nativeCLIPathField("claude")), u.value(nativeCLIPathField("muse")),
			openCode.Loaded, openCode.Preferences, openCode.Pending, openCode.Generation, u.value(nativeOpenCodePathField(1)), u.value(nativeOpenCodePathField(2)),
			prefs.Loaded, prefs.Preferences, prefs.Pending, prefs.Generation}
		if !u.t3CodeNativeOnly() {
			connection := u.owner.readUIProjection()
			values = append(values, connection.Fingerprint, connection.KiloReady, t3CodeOptionalSavedLibrary(u.owner.modelLibrary.snapshot()))
		}
		data, _ := json.Marshal(values)
		return sha256.Sum256(data)
	}
	if key == "synara" && includePreferences && u.synaraNativeOnly() {
		paths := &u.clientState().CLIPreferences
		openCode := &u.clientState().OpenCodePreferences
		prefs := &u.agentsState().Synara.PreferencesState
		data, _ := json.Marshal([]any{paths.Loaded, paths.Preferences, paths.Pending, paths.Generation,
			u.value(nativeCLIPathField("codex-cli")), u.value(nativeCLIPathField("claude")), u.value(nativeCLIPathField("synara")),
			openCode.Loaded, openCode.Preferences, openCode.Pending, openCode.Generation, u.value(nativeOpenCodePathField(1)), u.value(nativeOpenCodePathField(2)),
			prefs.Loaded, prefs.Preferences, prefs.Pending, prefs.Generation})
		return sha256.Sum256(data)
	}
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
	values := []any{nativeSelectionFingerprint(key, s, base, local, u.clientCaps(key)), u.modelLibraryValue(), limits, u.uxhLaunchDirectory(key, directoryField), appPath, u.launchConnectionFingerprint()}
	if nativeCLIPathClient(key) && includePreferences {
		prefs := &u.clientState().CLIPreferences
		values = append(values, prefs.Loaded, prefs.Preferences, prefs.Pending, prefs.Generation, u.value(nativeCLIPathField("codex-cli")), u.value(nativeCLIPathField("claude")), u.value(nativeCLIPathField("muse")), u.value(nativeCLIPathField("synara")), u.value(nativeCLIPathField("omnigent")))
	}
	if (key == "t3-code" || key == "synara") && includePreferences {
		paths := &u.clientState().OpenCodePreferences
		values = append(values, paths.Loaded, paths.Preferences.Executable1, paths.Preferences.Executable2, u.value(nativeOpenCodePathField(1)), u.value(nativeOpenCodePathField(2)))
	}
	if key == "t3-code" && includePreferences {
		prefs := &u.agentsState().T3Code.PreferencesState
		values = append(values, prefs.Loaded, prefs.Preferences, prefs.Pending)
	}
	if key == "omnigent" && includePreferences {
		prefs := &u.agentsState().Omnigent.PreferencesState
		values = append(values, prefs.Loaded, prefs.Preferences, prefs.Pending, u.clientState().LaunchInfo.Clients[key].Path)
	}
	if key == "synara" && includePreferences {
		prefs := &u.agentsState().Synara.PreferencesState
		values = append(values, prefs.Loaded, prefs.Preferences, prefs.Pending)
	}
	if (key == "opencode" || key == "opencode2") && includePreferences {
		prefs := &u.clientState().OpenCodePreferences
		values = append(values, prefs.Loaded, prefs.Preferences, prefs.Pending, prefs.Generation)
	}
	data, _ := json.Marshal(values)
	return sha256.Sum256(data)
}

func (u *nativeUI) launchSettingsChangedMessage() string {
	return u.tr("Your launch settings changed while preparing. Launch again to use the latest changes.", "Los ajustes cambiaron durante la preparación. Vuelve a abrir para aplicar los últimos cambios.")
}

func (u *nativeUI) launchClientFrom(key, directoryField string) {
	if key == "muse" {
		u.launchMuseFrom(directoryField)
		return
	}
	if !clientLaunchUsesProject(key) {
		directoryField = "" // Desktop apps manage their own project selection.
	}
	a := u.agentsState()
	c := u.clientState()
	if c.Launching != "" || key == "claude-desktop" && u.busy["POST/api/claude-desktop/options"] {
		return
	}
	if (key == "synara" || key == "omnigent") && c.LaunchChecked && !u.nativeLaunchAvailable(key) {
		reason := nativeMessage(c.LaunchInfo.Clients[key].Reason, u.language)
		if reason == "" {
			reason = u.tr("Refresh installed apps before opening this tool.", "Actualiza las aplicaciones instaladas antes de abrir esta herramienta.")
		}
		u.setNotice(nativeToneWarning, reason)
		return
	}
	if nativeCLIPathClient(key) {
		prefs := &c.CLIPreferences
		if u.cliPreferencesBusy() || prefs.Pending != nil || u.cliPathDirty() {
			u.setNotice(nativeToneWarning, u.tr("Load or finish saving CLI executable settings before opening.", "Carga o termina de guardar las rutas de las CLI antes de abrir."))
			return
		}
		if !prefs.Loaded {
			before := u.launchSettingsFingerprintWithPreferences(key, directoryField, false)
			c.Launching = key
			u.refreshCLIPreferencesAfter(func(err error) {
				c.Launching = ""
				if err != nil {
					u.noticeError(err)
					return
				}
				if before != u.launchSettingsFingerprintWithPreferences(key, directoryField, false) {
					u.noticeError(errors.New(u.launchSettingsChangedMessage()))
					return
				}
				u.launchClientFrom(key, directoryField)
			})
			return
		}
		if (key == "t3-code" || key == "synara") && !c.OpenCodePreferences.Loaded {
			before := u.launchSettingsFingerprintWithPreferences(key, directoryField, false)
			c.Launching = key
			u.refreshOpenCodePreferencesAfter(func(err error) {
				c.Launching = ""
				if err != nil {
					u.noticeError(err)
					return
				}
				if before != u.launchSettingsFingerprintWithPreferences(key, directoryField, false) {
					u.noticeError(errors.New(u.launchSettingsChangedMessage()))
					return
				}
				u.launchClientFrom(key, directoryField)
			})
			return
		}
	}
	if key == "opencode" || key == "opencode2" {
		prefs := &c.OpenCodePreferences
		if u.openCodePreferencesBusy() || prefs.Pending != nil {
			u.setNotice(nativeToneWarning, u.tr("Wait for OpenCode settings to finish, or retry the unconfirmed version choice.", "Espera a que terminen los ajustes de OpenCode o reintenta la versión no confirmada."))
			return
		}
		if !prefs.Loaded {
			before := u.launchSettingsFingerprintWithPreferences(key, directoryField, false)
			c.Launching, a.Phase = key, u.tr("Loading OpenCode preferences…", "Cargando preferencias de OpenCode…")
			u.refreshOpenCodePreferencesAfter(func(err error) {
				c.Launching, a.Phase = "", ""
				if err != nil {
					u.noticeError(err)
					return
				}
				if before != u.launchSettingsFingerprintWithPreferences(key, directoryField, false) {
					u.setNotice(nativeToneWarning, u.launchSettingsChangedMessage())
					return
				}
				u.launchClientFrom(key, directoryField)
			})
			return
		}
		if key == "opencode" && u.openCodeClientKey() == "opencode2" {
			u.launchClientFrom("opencode2", directoryField)
			return
		}
		if key != u.openCodeClientKey() {
			u.setNotice(nativeToneWarning, u.launchSettingsChangedMessage())
			return
		}
	}
	if key == "omnigent" {
		prefs := &a.Omnigent.PreferencesState
		if u.omnigentPreferencesBusy() || prefs.Pending != nil || u.busy["GET"+nativeOmnigentPreferencesEndpoint] {
			u.setNotice(nativeToneWarning, u.tr("Load or finish saving Omnigent settings before opening.", "Carga o termina de guardar los ajustes de Omnigent antes de abrir."))
			return
		}
		if !prefs.Loaded {
			before := u.launchSettingsFingerprintWithPreferences(key, directoryField, false)
			c.Launching, a.Phase = key, u.tr("Loading Omnigent settings…", "Cargando ajustes de Omnigent…")
			u.refreshOmnigentPreferencesAfter(func(err error) {
				c.Launching, a.Phase = "", ""
				if err != nil {
					u.noticeError(err)
					return
				}
				if before != u.launchSettingsFingerprintWithPreferences(key, directoryField, false) {
					u.noticeError(errors.New(u.launchSettingsChangedMessage()))
					return
				}
				u.launchClientFrom(key, directoryField)
			})
			return
		}
	}
	if key == "t3-code" {
		prefs := &a.T3Code.PreferencesState
		if u.t3CodePreferencesBusy() || prefs.Pending != nil || u.busy["GET"+nativeT3CodePreferencesEndpoint] {
			u.setNotice(nativeToneWarning, u.tr("Wait for T3 Code settings to finish, or retry the unconfirmed change in Settings.", "Espera a que terminen los ajustes de T3 Code o reintenta el cambio no confirmado en Ajustes."))
			return
		}
		if !prefs.Loaded {
			before := u.launchSettingsFingerprintWithPreferences(key, directoryField, false)
			c.Launching, a.Phase = key, u.tr("Loading T3 Code preferences…", "Cargando preferencias de T3 Code…")
			u.refreshT3CodePreferencesAfter(func(err error) {
				c.Launching, a.Phase = "", ""
				if err != nil {
					u.noticeError(err)
					return
				}
				if before != u.launchSettingsFingerprintWithPreferences(key, directoryField, false) {
					u.setNotice(nativeToneWarning, u.launchSettingsChangedMessage())
					return
				}
				u.launchClientFrom(key, directoryField)
			})
			return
		}
	}
	if key == "synara" {
		prefs := &a.Synara.PreferencesState
		if u.synaraPreferencesBusy() || prefs.Pending != nil || u.busy["GET"+nativeSynaraPreferencesEndpoint] {
			u.setNotice(nativeToneWarning, u.tr("Wait for Synara settings to finish, or retry the unconfirmed change in Settings.", "Espera a que terminen los ajustes de Synara o reintenta el cambio no confirmado en Ajustes."))
			return
		}
		if !prefs.Loaded {
			before := u.launchSettingsFingerprintWithPreferences(key, directoryField, false)
			c.Launching, a.Phase = key, u.tr("Loading Synara preferences…", "Cargando preferencias de Synara…")
			u.refreshSynaraPreferencesAfter(func(err error) {
				c.Launching, a.Phase = "", ""
				if err != nil {
					u.noticeError(err)
					return
				}
				if before != u.launchSettingsFingerprintWithPreferences(key, directoryField, false) {
					u.setNotice(nativeToneWarning, u.launchSettingsChangedMessage())
					return
				}
				u.launchClientFrom(key, directoryField)
			})
			return
		}
	}
	if key == "t3-code" {
		if err := u.t3CodeProviderCatalogConnectionError(); err != nil {
			u.noticeError(err)
			return
		}
		if err := u.t3CodeOpenCodeConnectionError(); err != nil {
			u.noticeError(err)
			return
		}
		if err := u.t3CodeMuseConnectionError(); err != nil {
			u.noticeError(err)
			return
		}
	}
	if key == "synara" {
		if err := u.synaraProviderSelectionError(); err != nil {
			u.noticeError(err)
			return
		}
	}
	needsSavedModels := !(key == "t3-code" && !u.t3CodeNeedsSavedLibrary() || key == "synara" && u.synaraNativeOnly() || key == "omnigent" && u.omnigentNativeOnly())
	if err := u.packReadinessError(key); needsSavedModels && err != nil {
		u.noticeError(err)
		return
	}
	if needsSavedModels {
		u.persistLibraryEdits()
	}
	if status, ready := u.libraryStatus(); needsSavedModels && !ready {
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
		reason := nativeMessage(c.LaunchInfo.Clients[key].Reason, u.language)
		if reason == "" {
			if key == "codex" {
				reason = u.tr("Refresh installed apps or set a valid Codex application path.", "Actualiza las aplicaciones instaladas o indica una ruta válida de Codex.")
			} else {
				reason = u.tr("Refresh installed apps before opening this tool.", "Actualiza las aplicaciones instaladas antes de abrir esta herramienta.")
			}
		}
		u.setNotice(nativeToneWarning, reason)
		return
	}
	s := u.sharedClientSelection(key)
	u.syncClientSelection(key, s)
	if _, err := nativeClientPayload(key, s); needsSavedModels && (err != nil || len(s.Models) == 0) {
		u.setNotice(nativeToneWarning, u.tr("Choose valid models before launching.", "Elige modelos válidos antes de abrir."))
		if err != nil {
			u.noticeError(err)
		}
		return
	}
	directory, appPath := u.uxhLaunchDirectory(key, directoryField), u.value("clients-launch-app-path")
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
		if needsSavedModels && current != s || prepared != u.launchSettingsFingerprint(key, directoryField) {
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
			u.uxhNoteOpened(key, resolvedDirectory)
			u.setNotice(nativeToneWarning, result.Message)
			if u.notice == "" {
				u.setNotice(nativeToneSuccess, u.tr("Editor launched.", "Editor abierto."))
			}
			if key == "omnigent" {
				u.omnigentLaunchConfirmed()
			}
			u.refreshState()
		})
	}
	// Reapply the current model and account settings each time because a
	// managed profile can be edited externally between launches.
	u.prepareClientAfter(key, launch)
}

// Confirm Muse's connection and selected executable before and after preparing
// its Kilo profile. Native Meta keeps its own profile and bypasses the proxy.
func (u *nativeUI) launchMuseFrom(directoryField string) {
	const key = "muse"
	a, c := u.agentsState(), u.clientState()
	if c.Launching != "" || u.busy["POST"+nativeLaunchEndpoint] {
		return
	}
	if u.cliPreferencesBusy() || c.CLIPreferences.Pending != nil || u.cliPathDirty() {
		u.setNotice(nativeToneWarning, u.tr("Load or finish saving CLI executable settings before opening.", "Carga o termina de guardar las rutas de las CLI antes de abrir."))
		return
	}
	if !c.CLIPreferences.Loaded {
		before := u.launchSettingsFingerprintWithPreferences(key, directoryField, false)
		c.Launching, a.Phase = key, u.tr("Loading CLI paths…", "Cargando rutas de las CLI…")
		u.refreshCLIPreferencesAfter(func(err error) {
			c.Launching, a.Phase = "", ""
			if err != nil {
				u.noticeError(err)
				return
			}
			if before != u.launchSettingsFingerprintWithPreferences(key, directoryField, false) {
				u.noticeError(errors.New(u.launchSettingsChangedMessage()))
				return
			}
			u.launchMuseFrom(directoryField)
		})
		return
	}
	if c.Muse.Pending != nil || u.busy["GET"+nativeMusePreferencesEndpoint] || u.busy["POST"+nativeMusePreferencesEndpoint] {
		u.noticeError(errors.New(u.launchSettingsChangedMessage()))
		return
	}
	if !c.Muse.Loaded {
		before := u.launchSettingsFingerprintWithPreferences(key, directoryField, false)
		c.Launching = key
		u.refreshMusePreferencesAfter(func(err error) {
			c.Launching = ""
			if err != nil {
				u.noticeError(err)
				return
			}
			if before != u.launchSettingsFingerprintWithPreferences(key, directoryField, false) {
				u.noticeError(errors.New(u.launchSettingsChangedMessage()))
				return
			}
			u.launchMuseFrom(directoryField)
		})
		return
	}
	if reason := u.museReadinessReason(); reason != "" {
		u.noticeError(errors.New(reason))
		return
	}
	if !u.nativeLaunchAvailable(key) {
		reason := nativeMessage(c.LaunchInfo.Clients[key].Reason, u.language)
		if reason == "" {
			reason = u.tr("Install Muse Code or choose its executable path in CLI settings, then refresh detection.", "Instala Muse Code o elige su ejecutable en los ajustes de las CLI y actualiza la detección.")
		}
		u.setNotice(nativeToneWarning, reason)
		return
	}
	directory := u.uxhLaunchDirectory(key, directoryField)
	resolvedDirectory, err := launchPath(directory, c.LaunchInfo.Directory)
	if err != nil {
		u.noticeError(err)
		return
	}
	executable := c.LaunchInfo.Clients[key].Path
	if executable == "" {
		u.setNotice(nativeToneWarning, u.tr("Refresh Muse Code installation detection before opening.", "Actualiza la detección de la instalación de Muse Code antes de abrir."))
		return
	}
	prepared, expected := u.launchSettingsFingerprint(key, directoryField), c.CLIPreferences.Preferences
	c.Launching, a.Phase = key, u.tr("Checking executable…", "Comprobando ejecutable…")
	finish := func(err error) {
		c.Launching, a.Phase = "", ""
		if err != nil {
			u.noticeError(err)
		}
	}
	musePreferences := c.Muse.Preferences
	verify := func(done func(error)) {
		u.verifyMusePreferencesAfter(musePreferences, func(err error) {
			if err != nil {
				done(err)
				return
			}
			u.verifyCLIPreferencesAfter(expected, func(err error) {
				if err == nil && prepared != u.launchSettingsFingerprint(key, directoryField) {
					err = errors.New(u.launchSettingsChangedMessage())
				}
				done(err)
			})
		})
	}
	launch := func() {
		a.Phase = u.tr("Opening…", "Abriendo…")
		u.clientRequest("POST", nativeLaunchEndpoint, map[string]string{"client": key, "directory": directory, "museExecutable": executable, "museConnection": musePreferences.Connection}, func(raw json.RawMessage, err error) {
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
				finish(errors.New(u.tr("Muse Code did not confirm launch.", "Muse Code no confirmó el arranque.")))
				return
			}
			finish(nil)
			u.uxhNoteOpened(key, resolvedDirectory)
			u.setNotice(nativeToneWarning, result.Message)
			if u.notice == "" {
				u.setNotice(nativeToneSuccess, u.museOpenedMessage())
			}
		})
	}
	verify(func(err error) {
		if err != nil {
			finish(err)
			return
		}
		a.Phase = u.tr("Preparing Muse…", "Preparando Muse…")
		u.prepareMuseAfter(executable, func(err error) {
			if err != nil {
				finish(err)
				return
			}
			verify(func(err error) {
				if err != nil {
					finish(err)
					return
				}
				launch()
			})
		})
	})
}

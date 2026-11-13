//go:build desktop

package main

// uxhInstall is a harness's detected installation state.
type uxhInstall uint8

const (
	uxhChecking uxhInstall = iota
	uxhInstalled
	uxhMissing
)

// uxhLaunch is everything the Harnesses page needs to show an Open control.
type uxhLaunch struct {
	install uxhInstall
	canOpen bool
	// reason explains why Open is disabled. local is true when the reason is
	// specific to this harness rather than shared by every row (connection,
	// library, detection still running, another launch in progress).
	reason string
	local  bool
	label  string
}

func (u *nativeUI) uxhInstallState(key string) uxhInstall {
	if key == "opencode" {
		key = u.openCodeClientKey()
	}
	c := u.clientState()
	if !c.LaunchChecked {
		return uxhChecking
	}
	if u.nativeLaunchAvailable(key) || nativeLaunchClientInstalled(c.LaunchInfo.Clients[key]) {
		return uxhInstalled
	}
	return uxhMissing
}

// Only the confirmed native Muse connection bypasses shared Kilo readiness.
func (u *nativeUI) uxhMuseLaunchState() uxhLaunch {
	c, a := u.clientState(), u.agentsState()
	if !c.Muse.Requested {
		u.refreshMusePreferencesAfter(nil)
	}
	if c.Muse.ProfileRequested && c.Muse.ProfilePath != c.LaunchInfo.Clients["muse"].Path {
		c.Muse.ProfileRequested = false
		c.Muse.ProfileLoaded = false
		c.Muse.Info.Prepared = false
		c.Muse.ProfileGeneration++
	}
	if c.Muse.Loaded && !c.Muse.ProfileRequested {
		u.refreshMuseProfile()
	}
	l := uxhLaunch{install: u.uxhInstallState("muse"), label: u.tr("Open Muse Code", "Abrir Muse Code")}
	l.canOpen = u.nativeLaunchAvailable("muse") && u.cliPreferencesReady() && u.museReadinessReason() == "" && c.Launching == "" && !u.busy["POST"+nativeLaunchEndpoint]
	switch {
	case !c.LaunchChecked:
		l.reason = u.tr("Wait for installation detection to finish.", "Espera a que termine la detección de la instalación.")
	case !u.nativeLaunchAvailable("muse"):
		l.reason, l.local = nativeMessage(c.LaunchInfo.Clients["muse"].Reason, u.language), true
		if l.reason == "" {
			l.reason = u.tr("Install Muse Code or choose its executable in CLI settings, then check again.", "Instala Muse Code o elige su ejecutable en los ajustes de las CLI y vuelve a comprobarlo.")
		}
	case !u.cliPreferencesReady():
		l.reason, l.local = u.tr("Load or finish saving CLI executable settings before opening.", "Carga o termina de guardar las rutas de las CLI antes de abrir."), true
	case c.Launching != "" || u.busy["POST"+nativeLaunchEndpoint]:
		l.reason = u.tr("Wait for the current app operation to finish.", "Espera a que termine la operación actual de la app.")
	}
	if l.reason == "" {
		if reason := u.museReadinessReason(); reason != "" {
			l.reason, l.local = reason, true
		}
	}
	if c.Launching == "muse" && a.Phase != "" {
		l.label = a.Phase
	}
	return l
}

// uxhLaunchState mirrors the readiness checks performed by launchClientFrom so
// Open is only enabled when a launch can succeed, and says why otherwise.
func (u *nativeUI) uxhLaunchState(key string) uxhLaunch {
	a, c := u.agentsState(), u.clientState()
	if nativeCLIPathClient(key) && !c.CLIPreferences.Requested {
		u.refreshCLIPreferencesAfter(nil)
	}
	if key == "muse" {
		return u.uxhMuseLaunchState()
	}
	if key == "t3-code" && !a.T3Code.PreferencesState.Requested {
		u.refreshT3CodePreferences()
	}
	if key == "omnigent" && !a.Omnigent.PreferencesState.Requested {
		u.refreshOmnigentPreferencesAfter(nil)
	}
	if key == "synara" && !a.Synara.PreferencesState.Requested {
		u.refreshSynaraPreferences()
	}
	if key == "opencode" {
		if !c.OpenCodePreferences.Requested {
			u.refreshOpenCodePreferencesAfter(nil)
		}
		key = u.openCodeClientKey()
	}
	l := uxhLaunch{install: u.uxhInstallState(key)}
	available := u.nativeLaunchAvailable(key)
	s := u.sharedClientSelection(key)
	packIssue := u.packReadinessError(key)
	_, validation := nativeClientPayload(key, s)
	libraryStatus, libraryReady := u.libraryStatus()
	connectionReady := u.agentConnectionReady() && !u.connectionWorking() && !u.setupConnectionNeeded()
	nativeAccounts := key == "t3-code" && u.t3CodeNativeOnly() || key == "synara" && u.synaraNativeOnly() || key == "omnigent" && u.omnigentNativeOnly()
	needsSavedModels := !nativeAccounts && (key != "t3-code" || u.t3CodeNeedsSavedLibrary())
	if !needsSavedModels {
		packIssue, validation, libraryReady = nil, nil, true
	}
	if nativeAccounts {
		connectionReady = true
	}
	busy := c.Launching != "" || u.busy["POST"+nativeClientEndpoint(key)] || key == "claude-desktop" && u.busy["POST/api/claude-desktop/options"]
	l.canOpen = available && libraryReady && connectionReady && (!needsSavedModels || len(s.Models) > 0) && validation == nil && packIssue == nil && !busy
	if key == "codex" || key == "codex-cli" {
		l.canOpen = l.canOpen && nativeClientImagesReady(s, u.models, u.state)
	}
	if key == "claude" && (!c.ClaudeChecked || u.busy["GET/api/claude/info"]) {
		l.canOpen = false
	}
	if key == "open-design" {
		l.canOpen = l.canOpen && u.openDesignEngineAvailable()
	}
	local := func(reason string) { l.reason, l.local = reason, true }
	switch {
	case !c.LaunchChecked:
		l.reason = u.tr("Wait for installation detection to finish.", "Espera a que termine la detección de la instalación.")
	case l.install == uxhMissing && terminalClientSupported(key):
		local(u.tr("Install the CLI, then check again to open it with your Kilo models.", "Instala el CLI y vuelve a comprobarlo para abrirlo con tus modelos de Kilo."))
	case !available:
		reason := nativeMessage(c.LaunchInfo.Clients[key].Reason, u.language)
		if reason == "" {
			reason = u.tr("Install this app, then check again.", "Instala esta aplicación y vuelve a comprobarlo.")
		}
		local(reason)
	case !connectionReady && key == "open-design":
		l.reason = u.tr("Connect Kilo or ChatGPT first.", "Conecta primero Kilo o ChatGPT.")
	case !connectionReady:
		l.reason = u.tr("Connect Kilo or ChatGPT before opening this app.", "Conecta Kilo o ChatGPT antes de abrir esta aplicación.")
	case !libraryReady:
		l.reason = libraryStatus
	case packIssue != nil:
		local(packIssue.Error())
	case key == "claude" && (!c.ClaudeChecked || u.busy["GET/api/claude/info"]):
		local(u.tr("Wait for Claude Code version detection to finish.", "Espera a que termine la detección de la versión de Claude Code."))
	case key == "open-design" && !c.OpenDesignChecked:
		local(u.tr("Wait for CLI engine detection to finish.", "Espera a que termine la detección del motor CLI."))
	case key == "open-design" && !u.openDesignEngineAvailable():
		reason := nativeMessage(c.OpenDesign.Engines[u.openDesignEngine()].Reason, u.language)
		if reason == "" {
			reason = u.tr("Install the selected CLI engine before opening Open Design.", "Instala el motor CLI seleccionado antes de abrir Open Design.")
		}
		local(reason)
	case len(s.Models) == 0 && key == "claude-desktop":
		local(u.claudeDesktopModelSummary(s))
	case len(s.Models) == 0 && needsSavedModels:
		l.reason = u.tr("Add at least one shared model before opening an app.", "Añade al menos un modelo compartido antes de abrir una app.")
	case validation != nil:
		local(nativeMessage(validation.Error(), u.language))
	case (key == "codex" || key == "codex-cli") && !nativeClientImagesReady(s, u.models, u.state):
		local(u.tr("Review image generation settings in Models before opening Codex.", "Revisa la generación de imágenes en Modelos antes de abrir Codex."))
	case busy:
		l.reason = u.tr("Wait for the current app operation to finish.", "Espera a que termine la operación actual de la app.")
	}
	if (key == "opencode" || key == "opencode2") && !u.openCodePreferencesReady() {
		l.canOpen = false
		l.reason, l.local = u.tr("Load or finish saving the OpenCode version choice before opening.", "Carga o termina de guardar la versión de OpenCode antes de abrir."), true
	}
	if nativeCLIPathClient(key) && !u.cliPreferencesReady() {
		l.canOpen = false
		l.reason, l.local = u.tr("Load or finish saving CLI executable settings before opening.", "Carga o termina de guardar las rutas de las CLI antes de abrir."), true
	}
	if key == "t3-code" {
		if u.clientState().OpenCodePreferences.Loaded && (u.openCodePathDirty() || u.openCodePreferencesBusy() || u.clientState().OpenCodePreferences.Pending != nil) {
			l.canOpen = false
			l.reason, l.local = u.tr("Finish saving the OpenCode paths before opening T3 Code.", "Termina de guardar las rutas de OpenCode antes de abrir T3 Code."), true
		}
		preferences := &a.T3Code.PreferencesState
		if preferences.Pending != nil || u.t3CodePreferencesBusy() || u.busy["GET"+nativeT3CodePreferencesEndpoint] || preferences.Requested && !preferences.Loaded {
			l.canOpen = false
			l.reason, l.local = u.tr("Load or finish saving T3 Code settings before opening.", "Carga o termina de guardar los ajustes de T3 Code antes de abrir."), true
		}
		if err := u.t3CodeProviderCatalogConnectionError(); err != nil {
			l.canOpen = false
			l.reason, l.local = err.Error(), false
		}
		if err := u.t3CodeOpenCodeConnectionError(); err != nil {
			l.canOpen = false
			l.reason, l.local = err.Error(), true
		}
		if err := u.t3CodeMuseConnectionError(); err != nil {
			l.canOpen = false
			l.reason, l.local = err.Error(), true
		}
		if preferences.Loaded {
			if payload, err := nativeClientPayload(key, s); err == nil {
				if err := nativeT3CodePreferenceLibraryError(preferences.Preferences, payload.(map[string]any)["library"].(modelLibrary)); err != nil {
					l.canOpen = false
					l.reason, l.local = nativeMessage(err.Error(), u.language), true
				}
			}
		}
	}
	if key == "omnigent" {
		preferences := &a.Omnigent.PreferencesState
		if preferences.Loaded && !preferences.Preferences.Codex && !preferences.Preferences.Claude {
			l.canOpen = false
			l.reason, l.local = u.tr("Enable Codex or Claude before preparing Omnigent.", "Activa Codex o Claude antes de preparar Omnigent."), true
		}
		if !preferences.Loaded || preferences.Pending != nil || u.omnigentPreferencesBusy() || u.busy["GET"+nativeOmnigentPreferencesEndpoint] {
			l.canOpen = false
			l.reason, l.local = u.tr("Load or finish saving Omnigent settings before opening.", "Carga o termina de guardar los ajustes de Omnigent antes de abrir."), true
		}
	}
	if key == "synara" && available {
		if u.clientState().OpenCodePreferences.Loaded && (u.openCodePathDirty() || u.openCodePreferencesBusy() || u.clientState().OpenCodePreferences.Pending != nil) {
			l.canOpen = false
			l.reason, l.local = u.tr("Finish saving the OpenCode paths before opening Synara.", "Termina de guardar las rutas de OpenCode antes de abrir Synara."), true
		}
		preferences := &a.Synara.PreferencesState
		if preferences.Pending != nil || u.synaraPreferencesBusy() || u.busy["GET"+nativeSynaraPreferencesEndpoint] || preferences.Requested && !preferences.Loaded {
			l.canOpen = false
			l.reason, l.local = u.tr("Load or finish saving Synara settings before opening.", "Carga o termina de guardar los ajustes de Synara antes de abrir."), true
		}
		if err := u.synaraProviderSelectionError(); err != nil {
			l.canOpen = false
			l.reason, l.local = err.Error(), true
		}
		if preferences.Loaded {
			if payload, err := nativeClientPayload(key, s); err == nil {
				if err := nativeSynaraPreferenceLibraryError(preferences.Preferences, payload.(map[string]any)["library"].(modelLibrary)); err != nil {
					l.canOpen = false
					l.reason, l.local = nativeMessage(err.Error(), u.language), true
				}
			}
		}
	}
	l.label = u.tr("Open ", "Abrir ") + uxhName(key)
	if key == "open-design" {
		l.label = u.tr("Launch Open Design", "Abrir Open Design")
	}
	if c.Launching == key && a.Phase != "" {
		l.label = a.Phase
	}
	return l
}

// uxhSharedReason is shared by Kilo-backed harnesses. Only confirmed native
// connection choices can bypass this connection and model library.
func (u *nativeUI) uxhSharedReason() string {
	libraryStatus, libraryReady := u.libraryStatus()
	u.initModelLibrary()
	switch {
	case !(u.agentConnectionReady() && !u.connectionWorking() && !u.setupConnectionNeeded()):
		return u.tr("Connect Kilo or ChatGPT to use apps connected to Kilo.", "Conecta Kilo o ChatGPT para usar las apps conectadas a Kilo.")
	case !libraryReady:
		return libraryStatus
	case len(u.library.selection.Models) == 0:
		return u.tr("Add shared models for apps connected to Kilo.", "Añade modelos compartidos para las apps conectadas a Kilo.")
	}
	return ""
}

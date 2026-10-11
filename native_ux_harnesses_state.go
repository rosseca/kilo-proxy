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
	c := u.clientState()
	if !c.LaunchChecked {
		return uxhChecking
	}
	if u.nativeLaunchAvailable(key) || terminalClientSupported(key) && nativeLaunchClientInstalled(c.LaunchInfo.Clients[key]) {
		return uxhInstalled
	}
	return uxhMissing
}

// uxhLaunchState mirrors the readiness checks performed by launchClientFrom so
// Open is only enabled when a launch can succeed, and says why otherwise.
func (u *nativeUI) uxhLaunchState(key string) uxhLaunch {
	a, c := u.agentsState(), u.clientState()
	l := uxhLaunch{install: u.uxhInstallState(key)}
	available := u.nativeLaunchAvailable(key)
	s := u.sharedClientSelection(key)
	packIssue := u.packReadinessError(key)
	_, validation := nativeClientPayload(key, s)
	libraryStatus, libraryReady := u.libraryStatus()
	connectionReady := u.agentConnectionReady() && !u.connectionWorking() && !u.setupConnectionNeeded()
	busy := c.Launching != "" || u.busy["POST"+nativeClientEndpoint(key)] || key == "claude-desktop" && u.busy["POST/api/claude-desktop/options"]
	l.canOpen = available && libraryReady && connectionReady && len(s.Models) > 0 && validation == nil && packIssue == nil && !busy
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
	case len(s.Models) == 0:
		l.reason = u.tr("Add at least one shared model before opening a harness.", "Añade al menos un modelo compartido antes de abrir un harness.")
	case validation != nil:
		local(nativeMessage(validation.Error(), u.language))
	case (key == "codex" || key == "codex-cli") && !nativeClientImagesReady(s, u.models, u.state):
		local(u.tr("Review image generation settings in Models before opening Codex.", "Revisa la generación de imágenes en Modelos antes de abrir Codex."))
	case busy:
		l.reason = u.tr("Wait for the current harness operation to finish.", "Espera a que termine la operación actual del harness.")
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

// uxhSharedReason is the reason no harness can open right now, shown once
// above the list instead of on every row.
func (u *nativeUI) uxhSharedReason() string {
	libraryStatus, libraryReady := u.libraryStatus()
	u.initModelLibrary()
	switch {
	case !(u.agentConnectionReady() && !u.connectionWorking() && !u.setupConnectionNeeded()):
		return u.tr("Connect Kilo or ChatGPT first.", "Conecta primero Kilo o ChatGPT.")
	case !libraryReady:
		return libraryStatus
	case len(u.library.selection.Models) == 0:
		return u.tr("Add at least one shared model before opening a harness.", "Añade al menos un modelo compartido antes de abrir un harness.")
	}
	return ""
}

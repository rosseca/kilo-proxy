//go:build desktop

package main

import (
	"encoding/json"

	"gioui.org/layout"
)

const t3CodeDownloadURL = "https://github.com/pingdotgg/t3code/releases/tag/v" + t3CodeNightlyVersion

type nativeT3CodeInfo struct {
	Prepared bool          `json:"prepared"`
	Library  *modelLibrary `json:"library"`
	Version  string        `json:"version"`
	Message  string        `json:"message"`
}

type nativeT3CodeState struct {
	Info       nativeT3CodeInfo
	Requested  bool
	Loaded     bool
	Generation uint64
	Connection [32]byte
	Error      string
}

func (u *nativeUI) acceptT3CodeProfile(data json.RawMessage, connection [32]byte) {
	state := &u.agentsState().T3Code
	state.Generation++
	state.Requested, state.Loaded, state.Connection = true, true, connection
	state.Info, state.Error = nativeT3CodeInfo{}, ""
	if err := json.Unmarshal(data, &state.Info); err != nil {
		state.Error = u.tr("Could not read T3 Code settings. Refresh to try again.", "No se pudieron leer los ajustes de T3 Code. Actualiza para reintentarlo.")
	}
}

func (u *nativeUI) refreshT3CodeProfile() {
	const endpoint = "/api/clients/t3-code"
	if u.busy["GET"+endpoint] {
		return
	}
	state := &u.agentsState().T3Code
	state.Requested = true
	state.Generation++
	generation, connection := state.Generation, u.launchConnectionFingerprint()
	u.clientRequest("GET", endpoint, nil, func(data json.RawMessage, err error) {
		if state.Generation != generation {
			return
		}
		if err != nil {
			state.Info, state.Loaded = nativeT3CodeInfo{}, true
			state.Error = u.tr("Could not read T3 Code settings. Refresh to try again.", "No se pudieron leer los ajustes de T3 Code. Actualiza para reintentarlo.")
			return
		}
		u.acceptT3CodeProfile(data, connection)
	})
}

func (u *nativeUI) t3CodePrepared(s *nativeClientSelection) bool {
	state := &u.agentsState().T3Code
	if !state.Loaded || !state.Info.Prepared || state.Info.Library == nil || state.Connection != u.launchConnectionFingerprint() {
		return false
	}
	payload, err := nativeClientPayload("t3-code", s)
	if err != nil {
		return false
	}
	library := payload.(map[string]any)["library"].(modelLibrary)
	return libraryFingerprint(library) == libraryFingerprint(*state.Info.Library)
}

func (u *nativeUI) t3CodeClientPanel(s *nativeClientSelection) layout.Widget {
	const key = "t3-code"
	state := &u.agentsState().T3Code
	if !state.Requested {
		u.refreshT3CodeProfile()
	}
	_, validation := nativeClientPayload(key, s)
	libraryStatus, libraryReady := u.libraryStatus()
	working := u.clientState().Launching != "" || u.busy["POST"+nativeClientEndpoint(key)]
	canPrepare := libraryReady && u.agentConnectionReady() && !u.setupConnectionNeeded() && !u.connectionWorking() && len(s.Models) > 0 && validation == nil && !working
	status := u.tr("Prepare or open T3 Code · Kilo to apply the current shared models.", "Prepara o abre T3 Code · Kilo para aplicar los modelos compartidos actuales.")
	if u.t3CodePrepared(s) {
		status = u.tr("Four agents prepared for the current shared models.", "Cuatro agentes preparados con los modelos compartidos actuales.")
	}
	widgets := []layout.Widget{
		u.clientLauncherPanel(key, s, canPrepare),
		u.pills(u.disabled(canPrepare, u.button("client:t3-code:prepare", u.tr("Prepare without opening", "Preparar sin abrir"), func() { u.prepareClient(key) }))),
		u.note(status),
	}
	if validation != nil {
		widgets = append(widgets, u.hint(nativeMessage(validation.Error(), u.language)))
	}
	if !libraryReady {
		widgets = append(widgets, u.hint(libraryStatus))
	}
	if state.Error != "" {
		widgets = append(widgets, u.hint(state.Error))
	} else if state.Info.Message != "" {
		widgets = append(widgets, u.note(nativeMessage(state.Info.Message, u.language)))
	}
	chatHelp := u.tr("In T3 Code 0.0.45, existing chats keep their agent; start a new chat to switch between normal and Kilo.", "En T3 Code 0.0.45, los chats existentes conservan su agente; inicia uno nuevo para cambiar entre normal y Kilo.")
	if state.Info.Version == t3CodeNightlyVersion {
		chatHelp = u.tr("This nightly can change agents between turns in the same chat. T3 transfers a summary when switching providers; previous reasoning, tool results and attachments are not transferred. Each agent keeps its own CLI profile. This nightly ignores Claude effort options for custom IDs, so Claude · Kilo uses compatible levels saved in Kilo Models. Close and reopen the Kilo workspace after changing them; Claude Code 2.1.251 or newer is required for these levels.", "Este nightly permite cambiar de agente entre turnos en el mismo chat. T3 transfiere un resumen al cambiar de proveedor; no transfiere el razonamiento anterior, los resultados de herramientas ni los adjuntos. Cada agente conserva su propio perfil CLI. Este nightly ignora las opciones de esfuerzo de Claude para IDs personalizados, por lo que Claude · Kilo usa los niveles compatibles guardados en Modelos de Kilo. Cierra y reabre el espacio Kilo tras cambiarlos; requieren Claude Code 2.1.251 o posterior.")
	}
	requirements := u.tr("Supports T3 Code 0.0.45 and nightly 0.0.46-nightly.20261003.2610. Requires Codex CLI and Claude Code.", "Admite T3 Code 0.0.45 y nightly 0.0.46-nightly.20261003.2610. Requiere Codex CLI y Claude Code.")
	if state.Info.Version != "" {
		requirements += u.tr(" Detected: ", " Detectado: ") + state.Info.Version + "."
	}
	return u.column(
		u.section(u.tr("Launch", "Arranque"), u.tr("Starts the proxy and opens a separate T3 Code workspace.", "Arranca el proxy y abre un espacio T3 Code separado."), widgets...),
		u.section(u.tr("Agents", "Agentes"), u.tr("Choose one of these four options for each new chat.", "Elige una de estas cuatro opciones para cada chat nuevo."),
			u.note(u.tr("Codex · Existing Codex CLI login", "Codex · Sesión existente de Codex CLI")),
			u.note(u.tr("Codex · Kilo · Shared models through the proxy", "Codex · Kilo · Modelos compartidos a través del proxy")),
			u.note(u.tr("Claude · Existing Claude Code login", "Claude · Sesión existente de Claude Code")),
			u.note(u.tr("Claude · Kilo · Compatible shared models through the proxy", "Claude · Kilo · Modelos compartidos compatibles a través del proxy"))),
		u.section(u.tr("Options", "Opciones"), requirements,
			u.note(u.agentCompatibility(key)),
			u.note(chatHelp),
			u.note(u.tr("In Claude · Kilo, choose a prepared model by its exact gateway ID. T3 also lists built-in Claude models, which may not be available through your provider.", "En Claude · Kilo, elige un modelo preparado por su ID exacto del gateway. T3 también muestra modelos de Claude incluidos en la app, que pueden no estar disponibles a través de tu proveedor.")),
			u.note(u.tr("Your regular T3 Code workspace and its chats stay separate and can remain open. Normal agents use their existing CLI sessions, not the Desktop apps. Quit only T3 Code · Kilo before changing models or the connection, then reopen it.", "Tu espacio T3 Code habitual y sus chats siguen separados y pueden permanecer abiertos. Los agentes normales usan sus sesiones CLI existentes, no las apps Desktop. Cierra solo T3 Code · Kilo antes de cambiar los modelos o la conexión y vuelve a abrirlo.")),
			u.pills(u.disabled(!u.busy["GET/api/clients/t3-code"], u.button("client:t3-code:refresh", u.tr("Refresh prepared settings", "Actualizar ajustes preparados"), u.refreshT3CodeProfile)),
				u.iconButton("client:t3-code:install", u.tr("Get T3 Code", "Obtener T3 Code"), nativeButtonGhost, nativeIconOpenInNew, func() { u.open(t3CodeDownloadURL) }))),
	)
}

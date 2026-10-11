//go:build desktop

package main

import (
	"encoding/json"

	"gioui.org/layout"
)

const t3CodeDownloadURL = "https://github.com/pingdotgg/t3code/releases"

type nativeT3CodeInfo struct {
	Prepared            bool          `json:"prepared"`
	Library             *modelLibrary `json:"library"`
	Version             string        `json:"version"`
	ProtocolV2          bool          `json:"protocolV2"`
	ClaudeModelDefaults bool          `json:"claudeModelDefaults"`
	Message             string        `json:"message"`
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

// t3CodeDetail adds T3 Code's agent list, prepared-state checks and notes.
func (u *nativeUI) t3CodeDetail(s *nativeClientSelection) uxhDetail {
	const key = "t3-code"
	state := &u.agentsState().T3Code
	if !state.Requested {
		u.refreshT3CodeProfile()
	}
	_, validation := nativeClientPayload(key, s)
	libraryStatus, libraryReady := u.libraryStatus()
	working := u.clientState().Launching != "" || u.busy["POST"+nativeClientEndpoint(key)]
	canPrepare := libraryReady && u.agentConnectionReady() && !u.setupConnectionNeeded() && !u.connectionWorking() && len(s.Models) > 0 && validation == nil && !working
	d := uxhDetail{canPrepare: &canPrepare}
	d.prepareNote = u.tr("Prepare or open T3 Code · Kilo to apply the current shared models.", "Prepara o abre T3 Code · Kilo para aplicar los modelos compartidos actuales.")
	if u.t3CodePrepared(s) {
		d.prepareNote = u.tr("Four agents prepared for the current shared models.", "Cuatro agentes preparados con los modelos compartidos actuales.")
	}
	if validation != nil {
		d.status = append(d.status, u.hint(nativeMessage(validation.Error(), u.language)))
	}
	if !libraryReady {
		d.status = append(d.status, u.hint(libraryStatus))
	}
	if state.Error != "" {
		d.status = append(d.status, u.hint(state.Error))
	} else if state.Info.Message != "" {
		d.status = append(d.status, u.note(nativeMessage(state.Info.Message, u.language)))
	}
	chatHelp := u.tr("Start a new chat to switch between normal and Kilo when this T3 build keeps an existing chat’s agent.", "Inicia un chat nuevo para cambiar entre normal y Kilo si esta compilación de T3 conserva el agente de un chat existente.")
	if state.Info.ProtocolV2 {
		chatHelp = u.tr("This T3 build can change agents between turns in the same chat. T3 transfers a summary when switching providers; previous reasoning, tool results and attachments are not transferred. Each agent keeps its own CLI profile. This build uses its built-in catalog for Claude effort, so Claude · Kilo uses compatible levels saved in Kilo Models. Close and reopen the Kilo workspace after changing them; Claude Code 2.1.251 or newer is required for these levels.", "Esta compilación de T3 permite cambiar de agente entre turnos en el mismo chat. T3 transfiere un resumen al cambiar de proveedor; no transfiere el razonamiento anterior, los resultados de herramientas ni los adjuntos. Cada agente conserva su propio perfil CLI. Esta compilación usa el catálogo incluido para el esfuerzo de Claude, por lo que Claude · Kilo usa los niveles compatibles guardados en Modelos de Kilo. Cierra y reabre el espacio Kilo tras cambiarlos; requieren Claude Code 2.1.251 o posterior.")
	}
	d.blocks = append(d.blocks, u.uxhBlock(u.tr("Agents", "Agentes"), u.tr("Choose one of these four options for each new chat.", "Elige una de estas cuatro opciones para cada chat nuevo."), uxhMultiAgentRows(u, u.tr("Kilo agents show a green KP badge on T3's provider rail and composer icons.", "Los agentes Kilo muestran una insignia verde KP en los iconos de la barra de proveedores y del compositor de T3."))...))
	requirements := u.tr("Detects installed T3 Code release channels, including alpha, beta and nightly. Preparation checks the installed integration contract. Requires Codex CLI and Claude Code.", "Detecta canales instalados de T3 Code, incluidos alpha, beta y nightly. La preparación comprueba el contrato de integración instalado. Requiere Codex CLI y Claude Code.")
	if state.Info.Version != "" {
		requirements += u.tr(" Detected: ", " Detectado: ") + state.Info.Version + "."
	}
	notesID := "client:t3-code:notes"
	notes := []layout.Widget{u.disclosure(notesID, u.tr("How this works", "Cómo funciona"))}
	if u.expanded[notesID] {
		notes = append(notes,
			u.note(chatHelp),
			u.note(u.tr("Claude · Kilo starts with your prepared shared models. Preparation hides the built-in Claude entries. An older chat using a hidden model falls back to your shared default; check the selected model before continuing.", "Claude · Kilo empieza con tus modelos compartidos preparados. La preparación oculta las entradas de Claude incluidas en T3. Un chat antiguo que usaba un modelo oculto pasa a tu modelo compartido inicial; revisa el modelo elegido antes de continuar.")),
			u.note(u.tr("Your regular T3 Code workspace and its chats stay separate and can remain open. Normal agents use their existing CLI sessions, not the Desktop apps. Quit only T3 Code · Kilo before changing models or the connection, then reopen it.", "Tu espacio T3 Code habitual y sus chats siguen separados y pueden permanecer abiertos. Los agentes normales usan sus sesiones CLI existentes, no las apps Desktop. Cierra solo T3 Code · Kilo antes de cambiar los modelos o la conexión y vuelve a abrirlo.")),
		)
	}
	d.advanced = append(d.advanced,
		u.uxhAdvancedRow(u.tr("Prepared settings", "Ajustes preparados"), requirements, notes,
			u.disabled(!u.busy["GET/api/clients/t3-code"], u.button("client:t3-code:refresh", u.tr("Refresh", "Actualizar"), u.refreshT3CodeProfile))))
	return d
}

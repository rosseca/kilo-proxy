//go:build desktop

package main

import (
	"encoding/json"

	"gioui.org/layout"
)

const synaraDownloadURL = "https://github.com/Emanuele-web04/synara/releases"

type nativeSynaraInfo struct {
	Prepared bool          `json:"prepared"`
	Library  *modelLibrary `json:"library"`
	Version  string        `json:"version"`
	Message  string        `json:"message"`
}

type nativeSynaraState struct {
	Info       nativeSynaraInfo
	Requested  bool
	Loaded     bool
	Generation uint64
	Connection [32]byte
	Error      string
}

func (u *nativeUI) acceptSynaraProfile(data json.RawMessage, connection [32]byte) {
	state := &u.agentsState().Synara
	state.Generation++
	state.Requested, state.Loaded, state.Connection = true, true, connection
	state.Info, state.Error = nativeSynaraInfo{}, ""
	if err := json.Unmarshal(data, &state.Info); err != nil {
		state.Error = u.tr("Could not read Synara settings. Refresh to try again.", "No se pudieron leer los ajustes de Synara. Actualiza para reintentarlo.")
	}
}

func (u *nativeUI) refreshSynaraProfile() {
	const endpoint = "/api/clients/synara"
	if u.busy["GET"+endpoint] {
		return
	}
	state := &u.agentsState().Synara
	state.Requested = true
	state.Generation++
	generation, connection := state.Generation, u.launchConnectionFingerprint()
	u.clientRequest("GET", endpoint, nil, func(data json.RawMessage, err error) {
		if state.Generation != generation {
			return
		}
		if err != nil {
			state.Info, state.Loaded = nativeSynaraInfo{}, true
			state.Error = u.tr("Could not read Synara settings. Refresh to try again.", "No se pudieron leer los ajustes de Synara. Actualiza para reintentarlo.")
			return
		}
		u.acceptSynaraProfile(data, connection)
	})
}

func (u *nativeUI) synaraPrepared(s *nativeClientSelection) bool {
	state := &u.agentsState().Synara
	if !state.Loaded || !state.Info.Prepared || state.Info.Library == nil || state.Connection != u.launchConnectionFingerprint() {
		return false
	}
	payload, err := nativeClientPayload("synara", s)
	if err != nil {
		return false
	}
	library := payload.(map[string]any)["library"].(modelLibrary)
	return libraryFingerprint(library) == libraryFingerprint(*state.Info.Library)
}

// synaraDetail adds Synara's agent list, prepared-state checks and notes.
func (u *nativeUI) synaraDetail(s *nativeClientSelection) uxhDetail {
	const key = "synara"
	state := &u.agentsState().Synara
	if !state.Requested {
		u.refreshSynaraProfile()
	}
	_, validation := nativeClientPayload(key, s)
	libraryStatus, libraryReady := u.libraryStatus()
	working := u.clientState().Launching != "" || u.busy["POST"+nativeClientEndpoint(key)]
	canPrepare := libraryReady && u.agentConnectionReady() && !u.setupConnectionNeeded() && !u.connectionWorking() && len(s.Models) > 0 && validation == nil && !working
	d := uxhDetail{canPrepare: &canPrepare}
	d.prepareNote = u.tr("Prepare or open Synara · Kilo to apply the current shared models.", "Prepara o abre Synara · Kilo para aplicar los modelos compartidos actuales.")
	if u.synaraPrepared(s) {
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
	} else if state.Info.Message != "" && (!state.Info.Prepared || u.synaraPrepared(s)) {
		d.status = append(d.status, u.note(nativeMessage(state.Info.Message, u.language)))
	}
	d.blocks = append(d.blocks, u.uxhBlock(u.tr("Agents", "Agentes"), u.tr("Choose one of these four options for each new chat.", "Elige una de estas cuatro opciones para cada chat nuevo."), uxhMultiAgentRows(u, u.tr("Kilo Proxy names and green account indicators identify Kilo agents.", "Los nombres Kilo Proxy y los indicadores verdes de cuenta identifican los agentes Kilo."))...))
	requirements := u.tr("Supports Synara Beta. Requires native Codex CLI and Claude Code.", "Admite Synara Beta. Requiere Codex CLI nativo y Claude Code.")
	if state.Info.Version != "" {
		requirements += u.tr(" Detected: ", " Detectado: ") + state.Info.Version + "."
	}
	notesID := "client:synara:notes"
	notes := []layout.Widget{u.disclosure(notesID, u.tr("How this works", "Cómo funciona"))}
	if u.expanded[notesID] {
		notes = append(notes,
			u.note(u.tr("Existing chats keep their account; start a new chat to switch between normal and Kilo. For custom Claude gateway IDs, compatible reasoning defaults come from Kilo Models. Close and reopen Synara · Kilo after changing them; saved levels require Claude Code 2.1.251 or newer (2.1.267 or newer for Opus/Sonnet 5.5).", "Los chats existentes conservan su cuenta; inicia uno nuevo para cambiar entre normal y Kilo. Para IDs Claude personalizados del gateway, el razonamiento compatible inicial viene de Modelos de Kilo. Cierra y reabre Synara · Kilo tras cambiarlos; los niveles guardados requieren Claude Code 2.1.251 o posterior (2.1.267 o posterior para Opus/Sonnet 5.5).")),
			u.note(u.tr("You can disable unused accounts without blocking the other agents. Voice notes require Codex · Normal enabled and signed in with ChatGPT through codex login, even in Claude Kilo chats. Allow Synara microphone access when prompted.", "Puedes desactivar las cuentas que no uses sin bloquear los demás agentes. Las notas de voz requieren Codex · Normal activado y autenticado con ChatGPT mediante codex login, incluso en chats Claude Kilo. Concede acceso al micrófono a Synara cuando lo solicite.")),
			u.note(u.tr("Select the exact gateway ID shown in the prepared model list and check the selected model before continuing. A built-in Claude choice maps to your exact gateway ID when its family and version match one prepared model; other built-ins may be unavailable.", "Selecciona el ID exacto del gateway mostrado en la lista preparada y revisa el modelo elegido antes de continuar. Una opción Claude incluida se asigna a tu ID exacto del gateway cuando su familia y versión coinciden con un modelo preparado; las demás pueden no estar disponibles.")),
			u.note(u.tr("Your regular Synara workspace and its chats stay separate and can remain open. Normal agents use their existing CLI sessions, not the Desktop apps. Codex · Normal requires file-based CLI authentication; Synara does not support Codex keyring/auto authentication. Quit only Synara · Kilo before changing models or the connection, then reopen it.", "Tu espacio Synara habitual y sus chats siguen separados y pueden permanecer abiertos. Los agentes normales usan sus sesiones CLI existentes, no las apps Desktop. Codex · Normal requiere autenticación CLI en archivo; Synara no admite la autenticación keyring/auto de Codex. Cierra solo Synara · Kilo antes de cambiar los modelos o la conexión y vuelve a abrirlo.")),
		)
	}
	d.advanced = append(d.advanced,
		u.uxhAdvancedRow(u.tr("Prepared settings", "Ajustes preparados"), requirements, notes,
			u.disabled(!u.busy["GET/api/clients/synara"], u.button("client:synara:refresh", u.tr("Refresh", "Actualizar"), u.refreshSynaraProfile))))
	return d
}

// uxhMultiAgentRows lists the four agent options of Synara and T3 Code.
func uxhMultiAgentRows(u *nativeUI, footer string) []layout.Widget {
	row := func(name, account string) layout.Widget {
		return u.uxhKV(name, u.uxhValue(account))
	}
	return []layout.Widget{
		row("Codex · Normal", u.tr("Existing Codex CLI login", "Sesión existente de Codex CLI")),
		row("Kilo Proxy · Codex", u.tr("Shared models through the proxy", "Modelos compartidos a través del proxy")),
		row("Claude · Normal", u.tr("Existing Claude Code login", "Sesión existente de Claude Code")),
		row("Kilo Proxy · Claude", u.tr("Compatible shared models through the proxy", "Modelos compartidos compatibles a través del proxy")),
		uxhCell(u.note(footer)),
	}
}

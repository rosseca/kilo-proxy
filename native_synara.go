//go:build desktop

package main

import (
	"encoding/json"

	"gioui.org/layout"
)

const synaraDownloadURL = "https://github.com/Emanuele-web04/synara/releases/tag/v1.0.0-beta.1"

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

func (u *nativeUI) synaraClientPanel(s *nativeClientSelection) layout.Widget {
	const key = "synara"
	state := &u.agentsState().Synara
	if !state.Requested {
		u.refreshSynaraProfile()
	}
	_, validation := nativeClientPayload(key, s)
	libraryStatus, libraryReady := u.libraryStatus()
	working := u.clientState().Launching != "" || u.busy["POST"+nativeClientEndpoint(key)]
	canPrepare := libraryReady && u.agentConnectionReady() && !u.setupConnectionNeeded() && !u.connectionWorking() && len(s.Models) > 0 && validation == nil && !working
	status := u.tr("Prepare or open Synara · Kilo to apply the current shared models.", "Prepara o abre Synara · Kilo para aplicar los modelos compartidos actuales.")
	if u.synaraPrepared(s) {
		status = u.tr("Four agents prepared for the current shared models.", "Cuatro agentes preparados con los modelos compartidos actuales.")
	}
	widgets := []layout.Widget{
		u.clientLauncherPanel(key, s, canPrepare),
		u.pills(u.disabled(canPrepare, u.button("client:synara:prepare", u.tr("Prepare without opening", "Preparar sin abrir"), func() { u.prepareClient(key) }))),
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
	} else if state.Info.Message != "" && (!state.Info.Prepared || u.synaraPrepared(s)) {
		widgets = append(widgets, u.note(nativeMessage(state.Info.Message, u.language)))
	}
	chatHelp := u.tr("Existing chats keep their account; start a new chat to switch between normal and Kilo. For custom Claude gateway IDs, compatible reasoning defaults come from Kilo Models. Close and reopen Synara · Kilo after changing them; saved levels require Claude Code 2.1.251 or newer.", "Los chats existentes conservan su cuenta; inicia uno nuevo para cambiar entre normal y Kilo. Para IDs Claude personalizados del gateway, el razonamiento compatible inicial viene de Modelos de Kilo. Cierra y reabre Synara · Kilo tras cambiarlos; los niveles guardados requieren Claude Code 2.1.251 o posterior.")
	requirements := u.tr("Supports Synara Beta 1.0.0-beta.1. Requires native Codex CLI and Claude Code.", "Admite Synara Beta 1.0.0-beta.1. Requiere Codex CLI nativo y Claude Code.")
	if state.Info.Version != "" {
		requirements += u.tr(" Detected: ", " Detectado: ") + state.Info.Version + "."
	}
	return u.column(
		u.section(u.tr("Launch", "Arranque"), u.tr("Starts the proxy and opens a separate Synara workspace.", "Arranca el proxy y abre un espacio Synara separado."), widgets...),
		u.section(u.tr("Agents", "Agentes"), u.tr("Choose one of these four options for each new chat. Kilo Proxy names and green account indicators identify Kilo agents.", "Elige una de estas cuatro opciones para cada chat nuevo. Los nombres Kilo Proxy y los indicadores verdes de cuenta identifican los agentes Kilo."),
			u.note(u.tr("Codex · Normal · Existing Codex CLI login", "Codex · Normal · Sesión existente de Codex CLI")),
			u.note(u.tr("Kilo Proxy · Codex · Shared models through the proxy", "Kilo Proxy · Codex · Modelos compartidos a través del proxy")),
			u.note(u.tr("Claude · Normal · Existing Claude Code login", "Claude · Normal · Sesión existente de Claude Code")),
			u.note(u.tr("Kilo Proxy · Claude · Compatible shared models through the proxy", "Kilo Proxy · Claude · Modelos compartidos compatibles a través del proxy"))),
		u.section(u.tr("Options", "Opciones"), requirements,
			u.note(u.agentCompatibility(key)),
			u.note(chatHelp),
			u.note(u.tr("Select the exact gateway ID shown in the prepared model list and check the selected model before continuing. A built-in Claude choice maps to your exact gateway ID when its family and version match one prepared model; other built-ins may be unavailable.", "Selecciona el ID exacto del gateway mostrado en la lista preparada y revisa el modelo elegido antes de continuar. Una opción Claude incluida se asigna a tu ID exacto del gateway cuando su familia y versión coinciden con un modelo preparado; las demás pueden no estar disponibles.")),
			u.note(u.tr("Your regular Synara workspace and its chats stay separate and can remain open. Normal agents use their existing CLI sessions, not the Desktop apps. Codex · Normal requires file-based CLI authentication; Synara does not support Codex keyring/auto authentication. Quit only Synara · Kilo before changing models or the connection, then reopen it.", "Tu espacio Synara habitual y sus chats siguen separados y pueden permanecer abiertos. Los agentes normales usan sus sesiones CLI existentes, no las apps Desktop. Codex · Normal requiere autenticación CLI en archivo; Synara no admite la autenticación keyring/auto de Codex. Cierra solo Synara · Kilo antes de cambiar los modelos o la conexión y vuelve a abrirlo.")),
			u.pills(u.disabled(!u.busy["GET/api/clients/synara"], u.button("client:synara:refresh", u.tr("Refresh prepared settings", "Actualizar ajustes preparados"), u.refreshSynaraProfile)),
				u.iconButton("client:synara:install", u.tr("Get Synara", "Obtener Synara"), nativeButtonGhost, nativeIconOpenInNew, func() { u.open(synaraDownloadURL) }))),
	)
}

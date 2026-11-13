//go:build desktop

package main

import (
	"encoding/json"
	"errors"

	"gioui.org/layout"
)

const t3CodeDownloadURL = "https://github.com/pingdotgg/t3code/releases"

type nativeT3CodeInfo struct {
	Prepared                bool                       `json:"prepared"`
	Library                 *modelLibrary              `json:"library"`
	Version                 string                     `json:"version"`
	ProtocolV2              bool                       `json:"protocolV2"`
	ClaudeModelDefaults     bool                       `json:"claudeModelDefaults"`
	Message                 string                     `json:"message"`
	Preferences             nativeT3CodePreferences    `json:"preferences"`
	EffectiveCodexModels    []nativeT3CodeModelSummary `json:"effectiveCodexModels"`
	EffectiveClaudeModels   []nativeT3CodeModelSummary `json:"effectiveClaudeModels"`
	EffectiveOpenCodeModels []nativeT3CodeModelSummary `json:"effectiveOpenCodeModels"`
	EffectiveMuseModels     []nativeT3CodeModelSummary `json:"effectiveMuseModels"`
}

type nativeT3CodeState struct {
	Info             nativeT3CodeInfo
	Requested        bool
	Loaded           bool
	Generation       uint64
	Connection       [32]byte
	Error            string
	PreferencesState nativeT3CodePreferencesState
}

// Only confirmed, current preferences can opt out of the Kilo model gateway.
// Older or unloaded responses retain the existing Kilo readiness checks.
func (u *nativeUI) t3CodeNativeOnly() bool {
	prefs := &u.agentsState().T3Code.PreferencesState
	return prefs.Loaded && validNativeT3CodePreferences(prefs.Preferences) && !prefs.Preferences.KiloCodex && !prefs.Preferences.KiloClaude && !nativeT3OpenCodeUsesKilo(prefs.Preferences) && !nativeT3MuseUsesKilo(prefs.Preferences)
}

// Only confirmed preferences can opt out of the saved shared library.
func (u *nativeUI) t3CodeNeedsSavedLibrary() bool {
	state := &u.agentsState().T3Code.PreferencesState
	return !state.Loaded || !validNativeT3CodePreferences(state.Preferences) ||
		t3CodeNeedsSavedLibrary(t3CodePreferences(state.Preferences))
}

func (u *nativeUI) t3CodeProviderCatalogConnectionError() error {
	state := &u.agentsState().T3Code.PreferencesState
	p := state.Preferences
	if state.Loaded && validNativeT3CodePreferences(p) &&
		t3CodeUsesProviderCatalog(t3CodePreferences(p)) && !u.owner.readUIProjection().KiloReady {
		return errors.New(u.tr("Connect Kilo to use the Claude or Meta catalog in T3 Code.", "Conecta Kilo para usar el catálogo de Claude o Meta en T3 Code."))
	}
	return nil
}

func (u *nativeUI) acceptT3CodeProfile(data json.RawMessage, connection [32]byte) {
	state := &u.agentsState().T3Code
	state.Generation++
	state.Requested, state.Loaded, state.Connection = true, true, connection
	state.Info, state.Error = nativeT3CodeInfo{}, ""
	if err := json.Unmarshal(data, &state.Info); err != nil {
		state.Error = u.tr("Could not read T3 Code settings. Refresh to try again.", "No se pudieron leer los ajustes de T3 Code. Actualiza para reintentarlo.")
	}
	if state.Info.Prepared && nativeT3CodePreferencesEqual(state.PreferencesState.Preferences, state.Info.Preferences) {
		state.PreferencesState.RequiresPreparation = false
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
		if state.Generation != generation || connection != u.launchConnectionFingerprint() {
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
	if !state.Loaded || !state.Info.Prepared || u.t3CodePreferencesBusy() || state.PreferencesState.Pending != nil {
		return false
	}
	if prefs := &state.PreferencesState; prefs.Loaded && state.Info.Preferences.Version == 1 && !nativeT3CodePreferencesEqual(prefs.Preferences, state.Info.Preferences) {
		return false
	}
	if u.t3CodeNativeOnly() {
		return nativeT3CodePreferencesEqual(state.PreferencesState.Preferences, state.Info.Preferences)
	}
	if state.Info.Library == nil || state.Connection != u.launchConnectionFingerprint() || u.t3CodeProviderCatalogConnectionError() != nil {
		return false
	}
	if !u.t3CodeNeedsSavedLibrary() {
		library := t3CodeOptionalSavedLibrary(u.owner.modelLibrary.snapshot())
		return nativeT3CodePreferencesEqual(state.PreferencesState.Preferences, state.Info.Preferences) &&
			libraryFingerprint(library) == libraryFingerprint(*state.Info.Library)
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
	if !state.PreferencesState.Requested {
		u.refreshT3CodePreferences()
	}
	var validation error
	libraryStatus, libraryReady := "", true
	needsSavedModels := u.t3CodeNeedsSavedLibrary()
	if needsSavedModels {
		payload, err := nativeClientPayload(key, s)
		validation = err
		if validation == nil && state.PreferencesState.Loaded {
			validation = nativeT3CodePreferenceLibraryError(state.PreferencesState.Preferences, payload.(map[string]any)["library"].(modelLibrary))
		}
		libraryStatus, libraryReady = u.libraryStatus()
	}
	working := u.clientState().Launching != "" || u.busy["POST"+nativeClientEndpoint(key)] || u.t3CodePreferencesBusy() || u.busy["GET"+nativeT3CodePreferencesEndpoint] || !state.PreferencesState.Loaded || state.PreferencesState.Pending != nil
	canPrepare := libraryReady && u.agentConnectionReady() && !u.setupConnectionNeeded() && !u.connectionWorking() &&
		(!needsSavedModels || len(s.Models) > 0) && validation == nil && !working
	if u.t3CodeNativeOnly() {
		canPrepare = !working
	}
	if err := u.t3CodeProviderCatalogConnectionError(); err != nil {
		validation, canPrepare = err, false
	}
	if err := u.t3CodeOpenCodeConnectionError(); err != nil {
		validation, canPrepare = err, false
	}
	if err := u.t3CodeMuseConnectionError(); err != nil {
		validation, canPrepare = err, false
	}
	d := uxhDetail{canPrepare: &canPrepare}
	d.prepareNote = u.tr("Prepare or open T3 Code · Kilo to apply your account and model settings.", "Prepara o abre T3 Code · Kilo para aplicar tus ajustes de cuentas y modelos.")
	if u.t3CodePrepared(s) {
		d.prepareNote = u.tr("Your T3 accounts and models are prepared.", "Las cuentas y los modelos de T3 están preparados.")
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
	if state.PreferencesState.Error != "" {
		d.status = append(d.status, u.hint(state.PreferencesState.Error))
	}
	chatHelp := u.tr("Start a new chat to switch accounts when this T3 build keeps an existing chat’s agent.", "Inicia un chat nuevo para cambiar de cuenta si esta compilación de T3 conserva el agente de un chat existente.")
	if state.Info.ProtocolV2 {
		chatHelp = u.tr("This T3 build can change agents between turns in the same chat. T3 transfers a summary when switching providers; previous reasoning, tool results and attachments are not transferred. Each agent keeps its own CLI profile.", "Esta compilación de T3 permite cambiar de agente entre turnos en el mismo chat. T3 transfiere un resumen al cambiar de proveedor; no transfiere el razonamiento anterior, los resultados de herramientas ni los adjuntos. Cada agente conserva su propio perfil CLI.")
	}
	d.blocks = append(d.blocks, u.t3CodePreferencesPanel())
	requirements := u.tr("Detects installed T3 Code release channels, including alpha, beta and nightly. Preparation checks the installed integration contract and the CLIs required by your enabled accounts.", "Detecta canales instalados de T3 Code, incluidos alpha, beta y nightly. La preparación comprueba el contrato instalado y los CLI que requieren las cuentas activadas.")
	if state.Info.Version != "" {
		requirements += u.tr(" Detected: ", " Detectado: ") + state.Info.Version + "."
	}
	notesID := "client:t3-code:notes"
	notes := []layout.Widget{u.disclosure(notesID, u.tr("How this works", "Cómo funciona"))}
	if u.expanded[notesID] {
		notes = append(notes,
			u.note(chatHelp),
			u.note(u.tr("Claude Kilo uses Claude models from your connected Kilo catalog, with T3's native reasoning selector. You can choose shared models in Accounts and models. Available levels depend on the installed T3 and Claude Code versions.", "Claude Kilo usa los modelos de Claude del catálogo de tu conexión Kilo, con el selector nativo de razonamiento de T3. Puedes elegir los modelos compartidos en Cuentas y modelos. Los niveles disponibles dependen de las versiones instaladas de T3 y Claude Code.")),
			u.note(u.tr("Your regular T3 Code workspace and its chats stay separate and can remain open. Codex and Claude use their existing CLI sessions, not Desktop app logins. Close only T3 Code · Kilo, then prepare again after changing accounts or models. Your chats are kept; check the selected account and model before continuing an older chat.", "Tu espacio T3 Code habitual y sus chats siguen separados y pueden permanecer abiertos. Codex y Claude usan sus sesiones CLI existentes, no los inicios de sesión de las apps Desktop. Cierra solo T3 Code · Kilo y prepara de nuevo tras cambiar cuentas o modelos. Tus chats se conservan; revisa la cuenta y el modelo elegidos antes de continuar un chat antiguo.")),
		)
	}
	d.advanced = append(d.advanced,
		u.uxhAdvancedRow(u.tr("Prepared settings", "Ajustes preparados"), requirements, notes,
			u.disabled(!u.busy["GET/api/clients/t3-code"], u.button("client:t3-code:refresh", u.tr("Refresh", "Actualizar"), u.refreshT3CodeProfile))))
	return d
}

//go:build desktop

package main

import (
	"encoding/json"

	"gioui.org/layout"
)

const openMausBotDownloadURL = "https://github.com/milind-soni/OpenMausBot/releases/latest"

type nativeOpenMausBotInfo struct {
	Prepared         bool              `json:"prepared"`
	Library          *modelLibrary     `json:"library"`
	ReasoningEfforts map[string]string `json:"reasoningEfforts"`
}

type nativeOpenMausBotState struct {
	Info       nativeOpenMausBotInfo
	Requested  bool
	Loaded     bool
	Generation uint64
	Connection [32]byte
	Error      string
}

func (u *nativeUI) acceptOpenMausBotProfile(data json.RawMessage, connection [32]byte) {
	state := &u.agentsState().OpenMausBot
	state.Generation++ // A late GET must not replace a newer prepared response.
	state.Requested, state.Loaded, state.Connection = true, true, connection
	state.Info = nativeOpenMausBotInfo{}
	state.Error = ""
	if err := json.Unmarshal(data, &state.Info); err != nil {
		state.Error = u.tr("Could not read the prepared reasoning settings. Refresh to try again.", "No se pudieron leer los ajustes de razonamiento preparados. Actualiza para reintentarlo.")
	}
}

func (u *nativeUI) refreshOpenMausBotProfile() {
	const endpoint = "/api/clients/openmausbot"
	if u.busy["GET"+endpoint] {
		return
	}
	state := &u.agentsState().OpenMausBot
	state.Requested = true
	state.Generation++
	generation, connection := state.Generation, u.launchConnectionFingerprint()
	u.clientRequest("GET", endpoint, nil, func(data json.RawMessage, err error) {
		if state.Generation != generation {
			return
		}
		if err != nil {
			state.Info, state.Loaded = nativeOpenMausBotInfo{}, true
			state.Error = u.tr("Could not read the prepared reasoning settings. Refresh to try again.", "No se pudieron leer los ajustes de razonamiento preparados. Actualiza para reintentarlo.")
			return
		}
		u.acceptOpenMausBotProfile(data, connection)
	})
}

func (u *nativeUI) openMausBotReasoningReady(s *nativeClientSelection) bool {
	state := &u.agentsState().OpenMausBot
	if !state.Loaded || !state.Info.Prepared || state.Info.Library == nil || state.Info.ReasoningEfforts == nil || state.Connection != u.launchConnectionFingerprint() {
		return false
	}
	payload, err := nativeClientPayload("openmausbot", s)
	if err != nil {
		return false
	}
	library := payload.(map[string]any)["library"].(modelLibrary)
	return libraryFingerprint(library) == libraryFingerprint(*state.Info.Library)
}

func (u *nativeUI) openMausBotReasoningLabel(effort string) string {
	if effort == "none" {
		return u.tr("None", "Sin razonamiento")
	}
	if effort == "max" {
		return u.tr("Max", "Máximo")
	}
	if effort == "ultra" {
		return "Ultra"
	}
	return nativeModelReasoningChoices(u, []string{effort})[0].Label
}

func (u *nativeUI) openMausBotReasoningPanel(s *nativeClientSelection) layout.Widget {
	state := &u.agentsState().OpenMausBot
	if !state.Requested {
		u.refreshOpenMausBotProfile()
	}
	widgets := []layout.Widget{u.eyebrow(u.tr("PREPARED REASONING", "RAZONAMIENTO PREPARADO"))}
	switch {
	case state.Error != "":
		widgets = append(widgets, u.hint(state.Error))
	case !state.Loaded:
		widgets = append(widgets, u.note(u.tr("Checking prepared reasoning…", "Comprobando el razonamiento preparado…")))
	case !u.openMausBotReasoningReady(s):
		widgets = append(widgets, u.hint(u.tr("Prepare or open OpenMausBot to confirm the reasoning applied to these models.", "Prepara o abre OpenMausBot para confirmar el razonamiento aplicado a estos modelos.")))
	default:
		for _, model := range s.Models {
			widgets = append(widgets, u.note(model.Model.ID+" · "+u.openMausBotReasoningLabel(state.Info.ReasoningEfforts[model.Model.ID])))
		}
	}
	widgets = append(widgets,
		u.note(u.tr("Automatic means the proxy sends no fixed reasoning level. OpenMausBot has no separate level picker; edit Models, then quit and reopen its Kilo instance to apply changes.", "Automático significa que el proxy no envía un nivel de razonamiento fijo. OpenMausBot no tiene otro selector de nivel; edita Modelos, cierra su instancia Kilo y vuelve a abrirla para aplicar los cambios.")),
		u.pills(u.disabled(!u.busy["GET/api/clients/openmausbot"], u.button("client:openmausbot:reasoning-refresh", u.tr("Refresh prepared settings", "Actualizar ajustes preparados"), u.refreshOpenMausBotProfile))))
	return u.column(widgets...)
}

func (u *nativeUI) openMausBotClientPanel(s *nativeClientSelection) layout.Widget {
	const key = "openmausbot"
	_, validation := nativeClientPayload(key, s)
	libraryStatus, libraryReady := u.libraryStatus()
	working := u.clientState().Launching != "" || u.busy["POST"+nativeClientEndpoint(key)]
	canPrepare := libraryReady && u.agentConnectionReady() && !u.setupConnectionNeeded() && !u.connectionWorking() && len(s.Models) > 0 && validation == nil && !working
	widgets := []layout.Widget{
		u.clientLauncherPanel(key, s, canPrepare),
		u.pills(u.disabled(canPrepare, u.button("client:openmausbot:prepare", u.tr("Prepare without opening", "Preparar sin abrir"), func() { u.prepareClient(key) }))),
	}
	if s.Path != "" {
		widgets = append(widgets, u.note(s.Path))
	}
	if validation != nil {
		widgets = append(widgets, u.hint(nativeMessage(validation.Error(), u.language)))
	}
	if !libraryReady {
		widgets = append(widgets, u.hint(libraryStatus))
	} else if len(s.Models) == 0 {
		widgets = append(widgets, u.hint(u.tr("Add at least one shared model before opening OpenMausBot.", "Añade al menos un modelo compartido antes de abrir OpenMausBot.")))
	}
	return u.column(
		u.section(u.tr("Launch", "Arranque"), u.tr("Applies your shared models and starts the proxy before opening OpenMausBot.", "Aplica tus modelos compartidos y arranca el proxy antes de abrir OpenMausBot."), widgets...),
		u.section(u.tr("Options", "Opciones"), u.tr("Local connection and application setup.", "Conexión local y configuración de la aplicación."),
			u.note(u.agentCompatibility(key)),
			u.openMausBotReasoningPanel(s),
			u.note(u.tr("Manage models in Models. Quit the OpenMausBot Kilo instance before changing models or the connection, then open it again. Only the local proxy key is saved in its separate profile.", "Gestiona los modelos en Modelos. Cierra la instancia Kilo de OpenMausBot antes de cambiar los modelos o la conexión y vuelve a abrirla. Solo se guarda la clave local del proxy en su perfil independiente.")),
			u.pills(u.iconButton("client:openmausbot:install", u.tr("Get OpenMausBot", "Obtener OpenMausBot"), nativeButtonGhost, nativeIconOpenInNew, func() { u.open(openMausBotDownloadURL) }))),
	)
}

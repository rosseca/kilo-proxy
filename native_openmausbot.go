//go:build desktop

package main

import "gioui.org/layout"

const openMausBotDownloadURL = "https://github.com/milind-soni/OpenMausBot/releases/latest"

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
			u.note(u.tr("Manage models in Models. Quit the OpenMausBot Kilo instance before changing models or the connection, then open it again. Only the local proxy key is saved in its separate profile.", "Gestiona los modelos en Modelos. Cierra la instancia Kilo de OpenMausBot antes de cambiar los modelos o la conexión y vuelve a abrirla. Solo se guarda la clave local del proxy en su perfil independiente.")),
			u.pills(u.iconButton("client:openmausbot:install", u.tr("Get OpenMausBot", "Obtener OpenMausBot"), nativeButtonGhost, nativeIconOpenInNew, func() { u.open(openMausBotDownloadURL) }))),
	)
}

//go:build desktop

package main

import (
	"fmt"
	"strconv"

	"gioui.org/layout"
)

const openDesignDownloadURL = "https://github.com/nexu-io/open-design/releases/latest"

func (u *nativeUI) openDesignInstallURL() string {
	if u.clientState().LaunchInfo.Platform == "linux" {
		return "https://github.com/nexu-io/open-design#-run-from-source"
	}
	return openDesignDownloadURL
}

// Read saved settings at click time: a completed connection save may precede
// the next UI refresh. Never expose the upstream Kilo credential here.
func (u *nativeUI) openDesignConnection() (string, string) {
	u.owner.mu.Lock()
	defer u.owner.mu.Unlock()
	return "http://127.0.0.1:" + strconv.Itoa(u.owner.config.Port) + "/v1", u.owner.config.LocalKey
}

func (u *nativeUI) openDesignClientPanel(s *nativeClientSelection) layout.Widget {
	c := u.clientState()
	base, local := u.openDesignConnection()
	connectionReady := u.agentConnectionReady() && !u.setupConnectionNeeded() && !u.connectionWorking()
	_, libraryReady := u.libraryStatus()
	_, validation := nativeClientPayload("open-design", s)
	canLaunch := connectionReady && libraryReady && len(s.Models) > 0 && validation == nil && u.nativeLaunchAvailable("open-design") && c.Launching == ""
	launchLabel := u.tr("Launch Open Design", "Abrir Open Design")
	if c.Launching == "open-design" {
		launchLabel = u.tr("Opening…", "Abriendo…")
	}
	running := nativeBool(u.state, "running")
	startLabel := u.tr("Start proxy", "Arrancar proxy")
	if running {
		startLabel = u.tr("Proxy running", "Proxy activo")
	}
	widgets := []layout.Widget{
		u.card(u.heading(u.tr("Connect once, then launch", "Conecta una vez y abre la aplicación")),
			u.note(u.tr("Launch starts the proxy before opening the installed app. On first use, enter the connection below in Open Design on this computer.", "Abrir inicia el proxy antes de abrir la aplicación instalada. La primera vez, introduce la conexión de abajo en Open Design en este equipo.")),
			u.pills(u.disabled(canLaunch, u.button("client:open-design:launch", launchLabel, func() { u.launchAgent("open-design") })), u.disabled(connectionReady && !running && c.Launching == "", u.button("open-design:start", startLabel, func() { u.setProxyRunning(true, nil) })), u.button("open-design:detect", u.tr("Refresh detection", "Actualizar detección"), u.detectLaunchers))),
	}
	if !connectionReady {
		widgets = append(widgets, u.actionRow(u.note(u.tr("Save your Kilo connection before continuing.", "Guarda tu conexión de Kilo antes de continuar.")), u.button("open-design:connect", u.tr("Connect Kilo", "Conectar Kilo"), u.beginSetup)))
	}
	if c.LaunchError != "" {
		widgets = append(widgets, u.note(c.LaunchError))
	}
	if c.LaunchChecked && !u.nativeLaunchAvailable("open-design") {
		widgets = append(widgets, u.note(c.LaunchInfo.Clients["open-design"].Reason), u.button("open-design:install", u.tr("Installation instructions", "Instrucciones de instalación"), func() { u.open(u.openDesignInstallURL()) }))
	}
	fields := []layout.Widget{
		u.heading(u.tr("1. Add an OpenAI-compatible provider", "1. Añade un proveedor compatible con OpenAI")),
		u.note(u.tr("In Open Design: Settings → Models & providers → API providers → OpenAI. Then set Provider preset to Custom provider.", "En Open Design: Settings → Models & providers → API providers → OpenAI. Después elige Custom provider en Provider preset.")),
		u.actionRow(u.column(u.eyebrow("BASE URL"), u.label(base)), u.button("open-design:copy-url", u.tr("Copy base URL", "Copiar URL base"), func() { value, _ := u.openDesignConnection(); u.copy(value) })),
		u.actionRow(u.column(u.eyebrow("API KEY"), u.label("kl_local_••••••••••••••••")), u.disabled(local != "" && !u.connectionWorking(), u.button("open-design:copy-key", u.tr("Copy local API key", "Copiar API key local"), func() { _, value := u.openDesignConnection(); u.copy(value) }))),
		u.note(u.tr("The copy button includes the real local key. Your personal Kilo key stays in Kilo Proxy.", "El botón copia la clave local real. Tu clave personal de Kilo permanece en Kilo Proxy.")),
	}
	widgets = append(widgets, u.card(fields...))
	models := []layout.Widget{u.heading(u.tr("2. Choose a model and test", "2. Elige un modelo y prueba la conexión")), u.note(u.tr("Paste an exact model ID in Model. If needed, select Custom (type below)… and fill Custom model id. Click Test, then wait for All changes saved.", "Pega el ID exacto en Model. Si hace falta, selecciona Custom (type below)… y rellena Custom model id. Pulsa Test y espera a All changes saved."))}
	if len(s.Models) == 0 {
		models = append(models, u.note(u.tr("Add a model in your shared library first.", "Añade primero un modelo a tu biblioteca compartida.")))
	} else {
		models = append(models, u.actionRow(u.column(u.eyebrow(u.tr("DEFAULT MODEL", "MODELO PREDETERMINADO")), u.label(s.Initial)), u.button("open-design:copy-model", u.tr("Copy model ID", "Copiar ID de modelo"), func() { u.copy(u.sharedClientSelection("open-design").Initial) })))
		if len(s.Models) > 1 {
			models = append(models, u.button("open-design:models", fmt.Sprintf(u.tr("Other shared models (%d)  ▾", "Otros modelos compartidos (%d)  ▾"), len(s.Models)-1), func() { u.expanded["open-design:models"] = !u.expanded["open-design:models"] }))
			if u.expanded["open-design:models"] {
				for _, choice := range s.Models {
					id := choice.Model.ID
					if id == s.Initial {
						continue
					}
					name := choice.DisplayName
					if name == "" {
						name = choice.Model.Name
					}
					models = append(models, u.actionRow(u.column(u.label(name), u.note(id)), u.button("open-design:copy-model:"+id, u.tr("Copy model ID", "Copiar ID de modelo"), func() { u.copy(id) })))
				}
			}
		}
	}
	models = append(models, u.note(u.tr("These IDs come from your shared library. Open Design manages its own model selection; library edits are not synced automatically. Update its settings when the URL, key or model changes.", "Estos IDs vienen de tu biblioteca compartida. Open Design gestiona su selección de modelos; los cambios no se sincronizan automáticamente. Actualiza sus ajustes si cambia la URL, clave o modelo.")))
	widgets = append(widgets, u.card(models...), u.note(u.agentCompatibility("open-design")))
	return u.column(widgets...)
}

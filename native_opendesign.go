//go:build desktop

package main

import (
	"encoding/json"

	"gioui.org/layout"
)

const openDesignDownloadURL = "https://github.com/nexu-io/open-design/releases/latest"

func (u *nativeUI) openDesignInstallURL() string {
	if u.clientState().LaunchInfo.Platform == "linux" {
		return "https://github.com/nexu-io/open-design#-run-from-source"
	}
	return openDesignDownloadURL
}

type nativeOpenDesignInfo struct {
	Engine   string                        `json:"engine"`
	Engines  map[string]nativeLaunchClient `json:"engines"`
	Prepared bool                          `json:"prepared"`
}

func (u *nativeUI) openDesignEngine() string {
	if u.value("open-design-engine") == "Claude Code" {
		return "claude"
	}
	if u.value("open-design-engine") == "OpenCode" {
		return "opencode"
	}
	return "codex-cli"
}

func (u *nativeUI) detectOpenDesign() {
	c := u.clientState()
	if u.busy["GET"+openDesignProfileEndpoint] {
		return
	}
	c.OpenDesignDetectStarted = true
	before := u.value("open-design-engine")
	u.clientRequest("GET", openDesignProfileEndpoint, nil, func(data json.RawMessage, err error) {
		if err == nil {
			err = json.Unmarshal(data, &c.OpenDesign)
		}
		if err != nil {
			c.LaunchError = nativeMessage(err.Error(), u.language)
			return
		}
		c.OpenDesignChecked = true
		if before == "" && u.value("open-design-engine") == "" {
			label := "Codex CLI"
			if c.OpenDesign.Engine == "claude" {
				label = "Claude Code"
			}
			if c.OpenDesign.Engine == "opencode" {
				label = "OpenCode"
			}
			u.setValue("open-design-engine", label)
		}
	})
}

func (u *nativeUI) openDesignEngineAvailable() bool {
	c := u.clientState()
	return c.OpenDesignChecked && c.OpenDesign.Engines[u.openDesignEngine()].Available
}

func (u *nativeUI) openDesignClientPanel(s *nativeClientSelection) layout.Widget {
	c := u.clientState()
	if !c.OpenDesignDetectStarted {
		u.detectOpenDesign()
	}
	if u.value("open-design-engine") == "" && c.OpenDesignChecked {
		u.setValue("open-design-engine", "Codex CLI")
	}
	connectionReady := u.agentConnectionReady() && !u.setupConnectionNeeded() && !u.connectionWorking()
	_, libraryReady := u.libraryStatus()
	_, validation := nativeClientPayload("open-design", s)
	canLaunch := connectionReady && libraryReady && len(s.Models) > 0 && validation == nil && u.nativeLaunchAvailable("open-design") && u.openDesignEngineAvailable() && c.Launching == ""
	label := u.tr("Launch Open Design", "Abrir Open Design")
	if c.Launching == "open-design" {
		label = u.agentsState().Phase
	}
	engine := u.openDesignEngine()
	info := c.OpenDesign.Engines[engine]
	status := u.tr("Checking CLI installation…", "Comprobando instalación del CLI…")
	if c.OpenDesignChecked {
		status = u.tr("Installed · runs inside Open Design", "Instalado · se ejecuta dentro de Open Design")
		if !info.Available {
			status = info.Reason
		}
	}
	widgets := []layout.Widget{
		u.card(u.heading(u.tr("Choose the engine behind Open Design", "Elige el motor de Open Design")),
			u.note(u.tr("Open Design provides the design workspace. Codex CLI, Claude Code or OpenCode does the work using your Kilo models and its file tools.", "Open Design pone el espacio de diseño. Codex CLI, Claude Code u OpenCode trabaja con tus modelos de Kilo y sus herramientas de archivos.")),
			u.disabled(c.Launching == "", u.selectField("open-design-engine", u.tr("CLI engine", "Motor CLI"), []string{"Codex CLI", "Claude Code", "OpenCode"})),
			u.note(status),
			u.pills(u.disabled(canLaunch, u.button("client:open-design:launch", label, func() { u.launchAgent("open-design") })), u.button("open-design:detect", u.tr("Refresh detection", "Actualizar detección"), func() { u.detectLaunchers(); u.detectOpenDesign() })),
			u.note(u.tr("Launch prepares a private Kilo profile, starts the proxy, then opens Open Design. No key or command to copy.", "Abrir prepara un perfil Kilo privado, arranca el proxy y abre Open Design. No hay que copiar claves ni comandos."))),
		u.card(u.heading(u.tr("Your models, ready for the CLI", "Tus modelos, listos para el CLI")), u.label(u.sharedModelSummary()),
			u.note(u.tr("Open Design's CLI default model uses the CLI profile's shared default. Its own picker depends on the selected engine; you can also enter an exact model ID there.", "El modelo CLI default de Open Design usa el predeterminado del perfil CLI. Su selector depende del motor elegido; también puedes introducir allí un ID exacto.")),
			u.note(u.tr("The engine applies the reasoning settings it supports. Open Design may supply its own reasoning choice. Separate image or media providers remain Open Design settings.", "El motor aplica el razonamiento que admite. Open Design puede enviar su propio nivel. Los proveedores de imágenes o multimedia se configuran aparte en Open Design."))),
		u.card(u.heading(u.tr("An Open Design workspace for Kilo", "Un espacio de Open Design para Kilo")),
			u.note(u.tr("This opens a separate Kilo workspace and keeps your ordinary Open Design setup intact. Complete Open Design's welcome flow if shown. Use Models & providers → Local CLI if you changed its mode to API providers.", "Se abre un espacio Kilo separado y se conserva tu configuración habitual de Open Design. Completa su bienvenida si aparece. Usa Models & providers → Local CLI si cambiaste el modo a API providers.")),
			u.note(u.tr("Quit the Open Design Kilo instance before applying a different engine, model library or connection. Update the installed Open Design app normally; this managed workspace does not run its own updater.", "Sal de la instancia Open Design Kilo antes de aplicar otro motor, biblioteca o conexión. Actualiza la aplicación Open Design instalada de forma habitual; este espacio gestionado no ejecuta su propio actualizador."))),
	}
	if !connectionReady {
		widgets = append([]layout.Widget{u.actionRow(u.note(u.tr("Save your Kilo connection first.", "Guarda primero tu conexión de Kilo.")), u.button("open-design:connect", u.tr("Connect Kilo", "Conectar Kilo"), u.beginSetup))}, widgets...)
	}
	if c.LaunchError != "" {
		widgets = append([]layout.Widget{u.note(c.LaunchError)}, widgets...)
	}
	if c.LaunchChecked && !u.nativeLaunchAvailable("open-design") {
		widgets = append([]layout.Widget{u.actionRow(u.note(c.LaunchInfo.Clients["open-design"].Reason), u.button("open-design:install", u.tr("Get Open Design", "Obtener Open Design"), func() { u.open(u.openDesignInstallURL()) }))}, widgets...)
	}
	if c.OpenDesignChecked && !info.Available {
		url := "https://developers.openai.com/codex/cli/"
		if engine == "claude" {
			url = "https://code.claude.com/docs/en/overview"
		}
		if engine == "opencode" {
			url = "https://opencode.ai/"
		}
		widgets = append(widgets, u.button("open-design:install-engine", u.tr("Install CLI engine", "Instalar motor CLI"), func() { u.open(url) }))
	}
	return u.column(widgets...)
}

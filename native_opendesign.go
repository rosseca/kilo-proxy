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
	libraryStatus, libraryReady := u.libraryStatus()
	_, validation := nativeClientPayload("open-design", s)
	appAvailable := u.nativeLaunchAvailable("open-design")
	engine := u.openDesignEngine()
	info := c.OpenDesign.Engines[engine]
	engineAvailable := u.openDesignEngineAvailable()
	canLaunch := connectionReady && libraryReady && len(s.Models) > 0 && validation == nil && appAvailable && engineAvailable && c.Launching == ""
	appTone, appStatus := nativeToneInfo, u.tr("Checking installation…", "Comprobando instalación…")
	if c.LaunchChecked {
		if appAvailable {
			appTone, appStatus = nativeToneSuccess, u.tr("Open Design installed", "Open Design instalado")
		} else {
			appTone, appStatus = nativeToneNeutral, u.tr("Open Design not found", "Open Design no encontrado")
		}
	}
	engineTone, engineStatus := nativeToneInfo, u.tr("Checking CLI engine…", "Comprobando motor CLI…")
	if c.OpenDesignChecked {
		if info.Available {
			engineTone, engineStatus = nativeToneSuccess, u.tr("CLI engine installed", "Motor CLI instalado")
		} else {
			engineTone, engineStatus = nativeToneWarning, u.tr("CLI engine not found", "Motor CLI no encontrado")
		}
	}
	disabledReason := ""
	switch {
	case !c.LaunchChecked:
		disabledReason = u.tr("Wait for Open Design installation detection to finish.", "Espera a que termine la detección de Open Design.")
	case !appAvailable:
		disabledReason = nativeMessage(c.LaunchInfo.Clients["open-design"].Reason, u.language)
		if disabledReason == "" {
			disabledReason = u.tr("Install Open Design, then refresh detection.", "Instala Open Design y actualiza la detección.")
		}
	case !connectionReady:
		disabledReason = u.tr("Connect Kilo or ChatGPT first.", "Conecta primero Kilo o ChatGPT.")
	case !libraryReady:
		disabledReason = libraryStatus
	case len(s.Models) == 0:
		disabledReason = u.tr("Add at least one shared model before opening Open Design.", "Añade al menos un modelo compartido antes de abrir Open Design.")
	case validation != nil:
		disabledReason = nativeMessage(validation.Error(), u.language)
	case !c.OpenDesignChecked:
		disabledReason = u.tr("Wait for CLI engine detection to finish.", "Espera a que termine la detección del motor CLI.")
	case !engineAvailable:
		disabledReason = nativeMessage(info.Reason, u.language)
		if disabledReason == "" {
			disabledReason = u.tr("Install the selected CLI engine before opening Open Design.", "Instala el motor CLI seleccionado antes de abrir Open Design.")
		}
	case c.Launching != "":
		disabledReason = u.tr("Wait for the current launch to finish.", "Espera a que termine el arranque actual.")
	}
	label := u.tr("Launch Open Design", "Abrir Open Design")
	if c.Launching == "open-design" {
		label = u.agentsState().Phase
	}
	launchWidgets := []layout.Widget{
		u.selectField("open-design-engine", u.tr("CLI engine", "Motor CLI"), nativeChoices([]string{"Codex CLI", "Claude Code", "OpenCode"})),
		u.pills(u.statusBadge(appTone, appStatus), u.statusBadge(engineTone, engineStatus)),
		u.pills(
			u.disabled(canLaunch, u.primaryButton("client:open-design:launch", label, func() { u.launchAgent("open-design") })),
			u.iconButton("open-design:detect", u.tr("Refresh detection", "Actualizar detección"), nativeButtonGhost, nativeIconRefresh, func() { u.detectLaunchers(); u.detectOpenDesign() }),
		),
	}
	if disabledReason != "" {
		launchWidgets = append(launchWidgets, u.hint(disabledReason))
	}
	if c.LaunchError != "" {
		launchWidgets = append(launchWidgets, u.message(nativeToneError, c.LaunchError))
	}
	if !connectionReady {
		launchWidgets = append(launchWidgets, u.pills(u.button("open-design:connect", u.tr("Connect an account", "Conectar una cuenta"), u.beginSetup)))
	}
	if c.LaunchChecked && !appAvailable {
		launchWidgets = append(launchWidgets, u.pills(u.iconButton("open-design:install", u.tr("Get Open Design", "Obtener Open Design"), nativeButtonGhost, nativeIconOpenInNew, func() { u.open(u.openDesignInstallURL()) })))
	}
	if c.OpenDesignChecked && !info.Available {
		url := "https://developers.openai.com/codex/cli/"
		if engine == "claude" {
			url = "https://code.claude.com/docs/en/overview"
		}
		if engine == "opencode" {
			url = "https://opencode.ai/"
		}
		launchWidgets = append(launchWidgets, u.pills(u.iconButton("open-design:install-engine", u.tr("Install CLI engine", "Instalar motor CLI"), nativeButtonGhost, nativeIconOpenInNew, func() { u.open(url) })))
	}
	advancedWidgets := []layout.Widget{
		u.disclosure("open-design:about", u.tr("How this works", "Cómo funciona")),
	}
	if u.expanded["open-design:about"] {
		advancedWidgets = append(advancedWidgets,
			u.note(u.tr("The managed Kilo workspace keeps your ordinary Open Design setup separate. Complete its welcome flow if shown; choose Models & providers → Local CLI if you use local CLI mode.", "El espacio Kilo gestionado mantiene aparte tu configuración habitual de Open Design. Completa la bienvenida si aparece; elige Models & providers → Local CLI si usas el modo CLI local.")),
			u.note(u.tr("The selected engine applies its supported reasoning levels; media providers remain Open Design settings. Quit the Kilo workspace before changing its engine, models or connection, and update the installed app normally.", "El motor seleccionado aplica los niveles de razonamiento que admite; los proveedores multimedia se configuran en Open Design. Cierra el espacio Kilo antes de cambiar el motor, los modelos o la conexión y actualiza la aplicación instalada de forma habitual.")),
		)
	}
	launch := u.section(u.tr("Launch", "Arranque"), u.tr("Open a managed workspace using the selected CLI engine.", "Abre un espacio gestionado con el motor CLI seleccionado."), launchWidgets...)
	advanced := u.section(u.tr("Advanced", "Avanzado"), u.tr("Learn about the managed Open Design workspace.", "Más información sobre el espacio Open Design gestionado."), advancedWidgets...)
	return u.column(launch, advanced)
}

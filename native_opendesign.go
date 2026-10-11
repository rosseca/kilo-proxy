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

// openDesignDetail adds the CLI engine choice and its installation state.
func (u *nativeUI) openDesignDetail(s *nativeClientSelection) uxhDetail {
	c := u.clientState()
	if !c.OpenDesignDetectStarted {
		u.detectOpenDesign()
	}
	if u.value("open-design-engine") == "" && c.OpenDesignChecked {
		u.setValue("open-design-engine", "Codex CLI")
	}
	connectionReady := u.agentConnectionReady() && !u.setupConnectionNeeded() && !u.connectionWorking()
	engine := u.openDesignEngine()
	info := c.OpenDesign.Engines[engine]
	engineTone, engineStatus := nativeToneInfo, u.tr("Checking CLI engine…", "Comprobando motor CLI…")
	if c.OpenDesignChecked {
		if info.Available {
			engineTone, engineStatus = nativeToneSuccess, u.tr("CLI engine installed", "Motor CLI instalado")
		} else {
			engineTone, engineStatus = nativeToneWarning, u.tr("CLI engine not found", "Motor CLI no encontrado")
		}
	}
	d := uxhDetail{noPrepare: true}
	engineValue := []layout.Widget{u.uxhSelect("open-design-engine", []nativeChoice{{Value: "Codex CLI", Label: "Codex CLI"}, {Value: "Claude Code", Label: "Claude Code"}, {Value: "OpenCode", Label: "OpenCode"}}), u.pills(u.statusBadge(engineTone, engineStatus))}
	actions := []layout.Widget{}
	if c.OpenDesignChecked && !info.Available {
		url := "https://developers.openai.com/codex/cli/"
		if engine == "claude" {
			url = "https://code.claude.com/docs/en/overview"
		}
		if engine == "opencode" {
			url = "https://opencode.ai/"
		}
		actions = append(actions, u.buttonWidget("open-design:install-engine", u.tr("Install CLI engine", "Instalar motor CLI"), nativeButtonLink, nativeIconOpenInNew, true, false, func() { u.open(url) }))
	}
	d.onOpen = append(d.onOpen, u.uxhKV(u.tr("CLI engine", "Motor CLI"), u.column(engineValue...), actions...))
	if !connectionReady {
		d.status = append(d.status, u.pills(u.button("open-design:connect", u.tr("Connect an account", "Conectar una cuenta"), u.beginSetup)))
	}
	notesID := "open-design:about"
	notes := []layout.Widget{u.disclosure(notesID, u.tr("How this works", "Cómo funciona"))}
	if u.expanded[notesID] {
		notes = append(notes,
			u.note(u.tr("The managed Kilo workspace keeps your ordinary Open Design setup separate. Complete its welcome flow if shown; choose Models & providers → Local CLI if you use local CLI mode.", "El espacio Kilo gestionado mantiene aparte tu configuración habitual de Open Design. Completa la bienvenida si aparece; elige Models & providers → Local CLI si usas el modo CLI local.")),
			u.note(u.tr("The selected engine applies its supported reasoning levels; media providers remain Open Design settings. Quit the Kilo workspace before changing its engine, models or connection, and update the installed app normally.", "El motor seleccionado aplica los niveles de razonamiento que admite; los proveedores multimedia se configuran en Open Design. Cierra el espacio Kilo antes de cambiar el motor, los modelos o la conexión y actualiza la aplicación instalada de forma habitual.")),
		)
	}
	d.advanced = append(d.advanced, u.uxhAdvancedRow(u.tr("Managed workspace", "Espacio gestionado"), u.tr("Open Design prepares its Kilo workspace each time it opens.", "Open Design prepara su espacio Kilo cada vez que se abre."), notes))
	return d
}

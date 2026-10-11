//go:build desktop

package main

import (
	"fmt"
	"strings"

	"gioui.org/layout"
)

// uxhProfileReady reports whether the prepared profile matches the current selection.
func (u *nativeUI) uxhProfileReady(key string, s *nativeClientSelection) bool {
	base, local, _ := u.clientBase()
	return s.Saved != "" && s.Saved == nativeSelectionFingerprint(key, s, base, local, u.clientCaps(key))
}

func (u *nativeUI) uxhProfileWorking(key string) bool {
	return u.busy["POST"+nativeClientEndpoint(key)] || u.busy["GET"+nativeClientEndpoint(key)] || u.clientState().Launching != "" ||
		key == "claude-desktop" && u.busy["POST/api/claude-desktop/options"]
}

// uxhDefaultCanPrepare matches the checks the profile endpoint applies.
func (u *nativeUI) uxhDefaultCanPrepare(key string, s *nativeClientSelection) bool {
	_, validation := nativeClientPayload(key, s)
	can := len(s.Models) > 0 && validation == nil && !u.uxhProfileWorking(key)
	if (key == "codex" || key == "codex-cli") && !nativeClientImagesReady(s, u.models, u.state) {
		can = false
	}
	return can
}

// uxhHarnessPage is the detail page of a harness that Kilo Proxy launches.
func (u *nativeUI) uxhHarnessPage(key string, s *nativeClientSelection, d uxhDetail) layout.Widget {
	a, c := u.agentsState(), u.clientState()
	l := u.uxhLaunchState(key)
	detail := ""
	switch key {
	case "claude":
		if c.ClaudeChecked && c.Claude.Version != "" {
			detail = u.tr("version ", "versión ") + c.Claude.Version
		}
	case "codex":
		detail = strings.TrimSpace(u.value("clients-launch-app-path"))
		if detail == "" {
			detail = c.LaunchInfo.Clients[key].Path
		}
	}
	widgets := []layout.Widget{u.uxhBackLink(), u.uxhDetailHeader(key, l, detail)}
	if l.reason != "" && l.install != uxhMissing {
		widgets = append(widgets, u.hint(l.reason))
	}
	if c.LaunchError != "" {
		widgets = append(widgets, u.message(nativeToneError, c.LaunchError))
	}
	if a.Error != "" {
		widgets = append(widgets, u.message(nativeToneError, a.Error))
	}
	if key == "claude" && a.ClaudeError != "" {
		widgets = append(widgets, u.message(nativeToneError, a.ClaudeError))
	}
	widgets = append(widgets, d.status...)

	onOpen := []layout.Widget{}
	if clientLaunchUsesProject(key) {
		onOpen = append(onOpen, u.uxhFolderRow(key))
	}
	summary := u.uxhModelsSummary()
	if key == "claude-desktop" {
		summary = u.claudeDesktopModelSummary(s)
	}
	onOpen = append(onOpen, u.uxhModelsRow(key, summary))
	onOpen = append(onOpen, d.onOpen...)
	widgets = append(widgets, u.uxhBlock(u.tr("When opening", "Al abrir"), "", onOpen...))
	widgets = append(widgets, d.blocks...)
	advanced := append([]layout.Widget{u.uxhCompatibilityRow(key)}, d.advanced...)
	if !d.noPrepare {
		advanced = append(advanced, u.uxhPrepareRow(key, s, d))
	}
	advanced = append(advanced, u.uxhCommandRows(key, s)...)
	advanced = append(advanced, u.uxhExportRows(key, s)...)
	if terminalClientSupported(key) {
		advanced = append(advanced, u.uxhAdvancedRow(u.tr("Terminal commands", "Comandos de terminal"), u.tr("Open this harness by name from any terminal.", "Abre este harness por su nombre desde cualquier terminal."), nil,
			u.button("agent:"+key+":terminal-commands", u.tr("Open in Settings", "Abrir en Ajustes"), func() { u.page, u.settingsTab = "settings", "terminal" })))
	}
	if url := u.uxhInstallURL(key); url != "" {
		advanced = append(advanced, u.uxhAdvancedRow(u.tr("Installation", "Instalación"), u.tr("Official download and setup instructions.", "Descarga e instrucciones oficiales."), nil,
			u.buttonWidget("client:"+key+":install", u.tr("How to install", "Cómo instalarlo"), nativeButtonSecondary, nativeIconOpenInNew, true, false, func() { u.open(url) })))
	}
	widgets = append(widgets, u.uxhBlock(u.tr("Advanced", "Avanzado"), "", advanced...))
	return u.column(widgets...)
}

// uxhCompatibilityRow explains how this harness talks to the proxy and
// rechecks its installation.
func (u *nativeUI) uxhCompatibilityRow(key string) layout.Widget {
	c := u.clientState()
	description := u.agentCompatibility(key)
	extra := []layout.Widget{}
	detect := func() {
		u.refreshAgentInstallation(key)
		switch key {
		case "synara":
			u.refreshSynaraProfile()
		case "t3-code":
			u.refreshT3CodeProfile()
		case "openmausbot":
			u.refreshOpenMausBotProfile()
		case "open-design":
			u.detectOpenDesign()
		}
	}
	if key == "claude" {
		if u.value("clients-claude-mode") == "" {
			u.setValue("clients-claude-mode", "installed")
		}
		switch {
		case u.busy["GET/api/claude/info"] || !c.ClaudeChecked:
			description = u.tr("Detecting the installed Claude Code version…", "Detectando la versión instalada de Claude Code…")
		case u.clientCaps(key).Picker:
			description = u.tr("Detected automatically: modern (2.1.251+). Model picker and per-model effort are active.", "Detectada automáticamente: moderna (2.1.251+). Selector de modelos y esfuerzo por modelo activos.")
		case c.Claude.Version == "":
			description = u.tr("Claude Code was not detected; basic aliases are used.", "No se detectó Claude Code; se usan alias básicos.")
		default:
			description = fmt.Sprintf(u.tr("Detected automatically: %s. This version uses model aliases; the model picker needs 2.1.251+.", "Detectada automáticamente: %s. Esta versión usa alias; el selector de modelos requiere 2.1.251+."), c.Claude.Version)
		}
		choices := []nativeChoice{{Value: "installed", Label: u.tr("Use detected version", "Usar versión detectada")}, {Value: "modern", Label: u.tr("Force modern (2.1.251+)", "Forzar moderna (2.1.251+)")}}
		extra = append(extra, u.segmented("clients-claude-mode.", choices, u.value("clients-claude-mode"), true, func(mode string) { u.setValue("clients-claude-mode", mode) }))
		detect = func() { u.refreshAgentInstallation(key) }
	}
	return u.uxhAdvancedRow(u.tr("Compatibility", "Compatibilidad"), description, extra,
		u.disabled(!u.busy["GET"+nativeLaunchEndpoint] && !u.busy["GET/api/claude/info"], u.buttonWidget("agent:"+key+":detect", u.tr("Detect again", "Volver a detectar"), nativeButtonLink, nil, false, false, detect)))
}

// uxhPrepareRow writes the profile without launching the harness.
func (u *nativeUI) uxhPrepareRow(key string, s *nativeClientSelection, d uxhDetail) layout.Widget {
	can := u.uxhDefaultCanPrepare(key, s)
	if d.canPrepare != nil {
		can = *d.canPrepare
	}
	name := uxhName(key)
	description := fmt.Sprintf(u.tr("Writes the Kilo profile without launching %s.", "Escribe el perfil de Kilo sin lanzar %s."), name)
	if key == "codex" {
		description += u.tr(" Your regular Codex profile is not touched.", " Tu perfil normal de Codex no se toca.")
	}
	extra := []layout.Widget{}
	note := d.prepareNote
	if note == "" {
		switch {
		case u.uxhProfileReady(key, s):
			note = u.tr("Profile ready", "Perfil listo") + " · " + s.Path
		case s.Saved != "":
			note = u.tr("Changes need preparing", "Hay cambios por preparar")
		default:
			note = u.tr("Not prepared yet", "Aún sin preparar")
		}
	}
	extra = append(extra, u.note(note))
	if u.uxhProfileWorking(key) {
		extra = append(extra, u.hint(u.tr("Wait for the current profile operation to finish.", "Espera a que termine la operación actual del perfil.")))
	}
	return u.uxhAdvancedRow(u.tr("Prepare without opening", "Preparar sin abrir"), description, extra,
		u.disabled(can, u.button("client:"+key+":prepare", u.tr("Prepare", "Preparar"), func() { u.prepareClient(key) })))
}

// uxhCopyLaunch copies launch details once the current profile is prepared.
func (u *nativeUI) uxhCopyLaunch(key string, s *nativeClientSelection) {
	u.syncClientSelection(key, s)
	if !u.uxhProfileReady(key, s) {
		u.setNotice(nativeToneWarning, u.tr("Prepare the changed profile before copying its launch details.", "Prepara el perfil modificado antes de copiar los datos de arranque."))
		return
	}
	if text, err := u.clientLaunch(key, s, true); err != nil {
		u.noticeError(err)
	} else {
		u.copy(text)
	}
}

// uxhCommandRows are the "Launch command" rows for terminal-friendly harnesses.
func (u *nativeUI) uxhCommandRows(key string, s *nativeClientSelection) []layout.Widget {
	ready := u.uxhProfileReady(key, s)
	switch key {
	case "zed":
		extra := []layout.Widget{u.note(u.tr("Preparing stores the local key in the system credential store. Choose a model in Zed's Agent panel; open projects stay open.", "Preparar guarda la clave local en el almacén de credenciales del sistema. Elige un modelo en el panel Agent de Zed; tus proyectos siguen abiertos."))}
		if ready {
			if text, err := u.clientLaunch(key, s, false); err == nil && text != "" {
				extra = append(extra, u.code("client:zed:launch-preview", text))
			}
		}
		return []layout.Widget{u.uxhAdvancedRow(u.tr("Local key for Zed", "Clave local para Zed"), u.tr("Recovery only, if Zed lost its saved key.", "Solo para recuperación, si Zed perdió la clave guardada."), extra,
			u.disabled(ready, u.iconButton("client:zed:copy-launch", u.tr("Copy key", "Copiar clave"), nativeButtonSecondary, nativeIconCopy, func() { u.uxhCopyLaunch(key, s) })))}
	case "codex", "codex-cli", "claude", "opencode", "omp":
	default:
		return nil
	}
	description := u.tr("To open it yourself from any terminal.", "Para abrirlo tú desde cualquier terminal.")
	if key == "codex" {
		description = u.tr("To open it yourself from the terminal.", "Para abrirlo tú desde la terminal.")
	}
	if !ready {
		description += u.tr(" Prepare the profile first.", " Prepara antes el perfil.")
	}
	showID := "client:" + key + ":show-command"
	extra := []layout.Widget{u.disclosure(showID, u.tr("Command options", "Opciones del comando"))}
	if u.expanded[showID] {
		if key == "codex" {
			extra = append(extra, u.row(u.selectField("clients-platform", u.tr("Command operating system", "Sistema operativo del comando"), nativeChoices([]string{"macos", "windows", "linux"})), u.field("clients-app-path", u.tr("Command application path", "Ruta de aplicación del comando"), "/Applications/ChatGPT.app", false)))
		} else {
			extra = append(extra, u.selectField("clients-shell", u.tr("Command shell", "Shell del comando"), nativeChoices([]string{"unix", "powershell"})))
		}
		if ready {
			if text, err := u.clientLaunch(key, s, false); err == nil && text != "" {
				extra = append(extra, u.code("client:"+key+":launch-preview", text))
			}
		}
	}
	return []layout.Widget{u.uxhAdvancedRow(u.tr("Launch command", "Comando de lanzamiento"), description, extra,
		u.disabled(ready, u.iconButton("client:"+key+":copy-launch", u.tr("Copy", "Copiar"), nativeButtonSecondary, nativeIconCopy, func() { u.uxhCopyLaunch(key, s) })))}
}

// uxhCopyExport copies a configuration export with the real local key.
func (u *nativeUI) uxhCopyExport(key string, s *nativeClientSelection) {
	u.syncClientSelection(key, s)
	text, err := u.clientExport(key, s, true)
	if err != nil {
		u.noticeError(err)
		return
	}
	u.copy(text)
}

// uxhExportRows are the "Export configuration" rows.
func (u *nativeUI) uxhExportRows(key string, s *nativeClientSelection) []layout.Widget {
	descriptions := map[string][2]string{
		"codex":     {"models.json and config.toml.", "models.json y config.toml."},
		"codex-cli": {"models.json and config.toml.", "models.json y config.toml."},
		"claude":    {"settings.json for other machines. Your settings are kept and a .bak copy is saved.", "settings.json para otras máquinas. Tus ajustes se conservan y se guarda copia .bak."},
		"opencode":  {"opencode.json for other machines. Your settings are kept and a .bak copy is saved.", "opencode.json para otras máquinas. Tus ajustes se conservan y se guarda copia .bak."},
		"zed":       {"Zed settings. Copying does not save credentials; use Prepare or set the local key in Zed.", "Ajustes de Zed. Copiar no guarda las credenciales; usa Preparar o configura la clave local en Zed."},
		"omp":       {"models.yml with the Kilo provider and config.yml with the default model.", "models.yml con el proveedor Kilo y config.yml con el modelo inicial."},
	}
	text, ok := descriptions[key]
	if !ok {
		return nil
	}
	showID := "client:" + key + ":show-config"
	extra := []layout.Widget{u.disclosure(showID, u.tr("Show configuration", "Ver configuración"))}
	if key == "claude" {
		extra = append(extra, u.note(u.tr("Claude Code uses the smallest selected context window for the session; the active model may cap it further.", "Claude Code usa la menor ventana seleccionada para la sesión; el modelo activo puede limitarla aún más.")))
	}
	if key == "codex" || key == "codex-cli" {
		extra = append(extra, u.note(u.tr("The normal Codex profile stays separate; restart the Kilo instance after preparing changes.", "El perfil normal de Codex queda separado; reinicia la instancia Kilo tras preparar cambios.")))
	}
	if u.expanded[showID] && len(s.Models) > 0 {
		if preview, err := u.clientExport(key, s, false); err == nil {
			extra = append(extra, u.code("client:"+key+":config", preview))
		} else {
			extra = append(extra, u.message(nativeToneError, nativeMessage(err.Error(), u.language)))
		}
	}
	actions := []layout.Widget{}
	switch key {
	case "codex", "codex-cli":
		actions = append(actions,
			u.disabled(len(s.Models) > 0, u.iconButton("client:"+key+":catalog-copy", u.tr("Copy JSON", "Copiar JSON"), nativeButtonSecondary, nativeIconCopy, func() {
				u.syncClientSelection(key, s)
				data, err := buildCodexCatalog(s.Models, s.Initial, false)
				if err != nil {
					u.noticeError(err)
				} else {
					u.copy(string(data))
				}
			})),
			u.disabled(len(s.Models) > 0, u.iconButton("client:"+key+":config-copy", u.tr("Copy TOML", "Copiar TOML"), nativeButtonSecondary, nativeIconCopy, func() { u.uxhCopyExport(key, s) })))
	case "omp":
		actions = append(actions, u.disabled(len(s.Models) > 0, u.iconButton("client:omp:config-copy", u.tr("Copy models.yml", "Copiar models.yml"), nativeButtonSecondary, nativeIconCopy, func() { u.uxhCopyExport(key, s) })))
		settings := func() ([]byte, error) {
			selection, err := nativeOMPSelection(s)
			if err != nil {
				return nil, err
			}
			return buildOMPSettings(selection)
		}
		actions = append(actions, u.disabled(len(s.Models) > 0, u.iconButton("client:omp:settings-copy", u.tr("Copy config.yml", "Copiar config.yml"), nativeButtonSecondary, nativeIconCopy, func() {
			if data, err := settings(); err == nil {
				u.copy(string(data))
			} else {
				u.noticeError(err)
			}
		})))
		if u.expanded[showID] && len(s.Models) > 0 {
			if data, err := settings(); err == nil {
				extra = append(extra, u.note("config.yml"), u.code("client:omp:settings", string(data)))
			}
		}
	default:
		actions = append(actions, u.disabled(len(s.Models) > 0, u.iconButton("client:"+key+":config-copy", u.tr("Copy JSON", "Copiar JSON"), nativeButtonSecondary, nativeIconCopy, func() { u.uxhCopyExport(key, s) })))
	}
	return []layout.Widget{u.uxhAdvancedRow(u.tr("Export configuration", "Exportar configuración"), u.tr(text[0], text[1]), extra, actions...)}
}

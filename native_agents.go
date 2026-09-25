//go:build desktop

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gioui.org/font"
	"gioui.org/layout"
)

type nativeAgents struct {
	Preferences       agentPreferences
	Error             string
	ClaudeError       string
	FolderBusy        string
	Phase             string
	chooseFolder      func(string) (string, error)
	chooseApplication func() (string, error)
}

func agentProjectField(key string) string { return "agent:" + key + ":directory" }

func (u *nativeUI) agentsState() *nativeAgents {
	if u.agents == nil {
		prefs, err := readAgentPreferences(u.owner.dir)
		u.agents = &nativeAgents{Preferences: prefs, chooseFolder: chooseNativeFolder, chooseApplication: chooseNativeApplication}
		if err != nil {
			u.agents.Error = u.tr("Could not load remembered project folders.", "No se pudieron cargar las carpetas recordadas.")
		}
		for _, key := range launchClients {
			u.setValue(agentProjectField(key), prefs.Projects[key])
		}
		if u.value("clients-launch-app-path") == "" {
			u.setValue("clients-launch-app-path", prefs.CodexAppPath)
		}
	}
	return u.agents
}

func (u *nativeUI) locateCodexApplication() {
	a := u.agentsState()
	if a.FolderBusy != "" {
		return
	}
	a.FolderBusy = "codex-app"
	before := u.value("clients-launch-app-path")
	go func() {
		path, err := a.chooseApplication()
		if err == nil {
			path, err = resolveLaunchClient("codex", path)
		}
		u.enqueue(func() {
			a.FolderBusy = ""
			if errors.Is(err, errNativePickerCancelled) {
				return
			}
			if err != nil {
				a.Error = nativeMessage(err.Error(), u.language)
				return
			}
			if before != u.value("clients-launch-app-path") {
				a.Error = u.tr("The application path changed while choosing. Locate Codex again.", "La ruta cambió durante la selección. Vuelve a localizar Codex.")
				return
			}
			u.setValue("clients-launch-app-path", path)
			u.rememberAgentProject("codex", u.agentProject("codex"))
		})
	}()
}

func (u *nativeUI) agentProject(key string) string {
	u.agentsState()
	if directory := u.value(agentProjectField(key)); directory != "" {
		return directory
	}
	return u.clientState().LaunchInfo.Directory
}

func (u *nativeUI) rememberAgentProject(key, directory string) {
	a := u.agentsState()
	if clientLaunchUsesProject(key) {
		a.Preferences.rememberProject(key, directory)
	}
	a.Preferences.CodexAppPath = strings.TrimSpace(u.value("clients-launch-app-path"))
	if err := writeAgentPreferences(u.owner.dir, a.Preferences); err != nil {
		a.Error = u.tr("The project folder could not be remembered. You can still open the agent.", "No se pudo recordar la carpeta. Puedes abrir el agente igualmente.")
		return
	}
	a.Error = ""
}

func (u *nativeUI) chooseAgentProject(key string) {
	a := u.agentsState()
	if a.FolderBusy != "" {
		return
	}
	a.FolderBusy = key
	before, initial := u.value(agentProjectField(key)), u.agentProject(key)
	go func() {
		path, err := a.chooseFolder(initial)
		u.enqueue(func() {
			a.FolderBusy = ""
			if errors.Is(err, errNativePickerCancelled) {
				return
			}
			if err != nil {
				a.Error = nativeMessage(err.Error(), u.language)
				return
			}
			if before != u.value(agentProjectField(key)) {
				a.Error = u.tr("The project folder changed while the chooser was open. Choose the folder again.", "La carpeta cambió con el selector abierto. Vuelve a elegirla.")
				return
			}
			path, err = launchPath(path, u.clientState().LaunchInfo.Directory)
			if err != nil {
				a.Error = nativeMessage(err.Error(), u.language)
				return
			}
			u.setValue(agentProjectField(key), path)
			u.rememberAgentProject(key, path)
		})
	}()
}

func (u *nativeUI) agentSetup(key string) {
	if key == "other" {
		key = "generic"
	}
	u.client = key
	if strings.HasPrefix(key, "xcode-") {
		u.client = "xcode"
		u.clientState().Variant = strings.TrimPrefix(key, "xcode-")
	}
	u.page = "clients"
}

func (u *nativeUI) agentConnectionReady() bool {
	u.owner.mu.Lock()
	defer u.owner.mu.Unlock()
	return strings.TrimSpace(u.owner.apiKey) != "" && strings.TrimSpace(u.owner.config.OrgID) != ""
}

func (u *nativeUI) detectAgentCapabilities() {
	c, a := u.clientState(), u.agentsState()
	if c.ClaudeDetectStarted {
		return
	}
	c.ClaudeDetectStarted = true
	a.ClaudeError = ""
	u.clientRequest("GET", "/api/claude/info", nil, func(data json.RawMessage, err error) {
		if err == nil {
			err = json.Unmarshal(data, &c.Claude)
		}
		if err != nil {
			a.ClaudeError = u.tr("Could not check the Claude Code version. Retry detection in Options.", "No se pudo comprobar la versión de Claude Code. Reintenta la detección en Opciones.")
			return
		}
		c.ClaudeChecked = true
	})
}

func (u *nativeUI) agentCompatibility(key string) string {
	switch key {
	case "codex", "codex-cli":
		return u.tr("Uses Responses through an isolated Kilo profile, with the library's names, default and supported reasoning levels. Model support depends on the gateway.", "Usa Responses con un perfil Kilo separado: nombres, modelo inicial y niveles de razonamiento compatibles de la biblioteca. La compatibilidad depende del gateway.")
	case "claude":
		return u.tr("Uses Anthropic Messages. Applies only reasoning levels supported by each model and installed Claude Code version. Gateway support is also required.", "Usa Anthropic Messages. Aplica solo niveles de razonamiento compatibles con cada modelo y la versión de Claude Code. También requiere compatibilidad del gateway.")
	case "claude-desktop":
		return u.tr("Uses the Kilo third-party configuration for Chat, Cowork and Code. Close Claude before opening to apply changes. Other providers require the experimental option in these settings.", "Usa la configuración de terceros Kilo para Chat, Cowork y Code. Cierra Claude antes de abrir para aplicar cambios. Los demás proveedores requieren la opción experimental de estos ajustes.")
	case "opencode":
		return u.tr("Uses Chat Completions with shared names and default model; reasoning stays automatic. Opens a terminal in your project. Local OpenCode settings can override this profile.", "Usa Chat Completions con nombres y modelo inicial compartidos; el razonamiento sigue automático. Abre una terminal en tu proyecto. Los ajustes de OpenCode pueden prevalecer.")
	case "omp":
		return u.tr("Uses Responses with your shared models in an isolated Oh My Pi profile. Opens a terminal in your project; choose another model with /model. Reasoning follows each model's supported levels.", "Usa Responses con tus modelos compartidos en un perfil separado de Oh My Pi. Abre una terminal en tu proyecto; cambia de modelo con /model. El razonamiento sigue los niveles compatibles de cada modelo.")
	case "zed":
		return u.tr("Uses Chat Completions with shared names and default model; reasoning stays automatic. Open Zed saves the local key in the system credential store and updates its models.", "Usa Chat Completions con nombres y modelo inicial compartidos; el razonamiento sigue automático. Abrir Zed guarda la clave local en el almacén de credenciales del sistema y actualiza sus modelos.")
	case "open-design":
		return u.tr("Uses Codex CLI, Claude Code or OpenCode as its engine, with a private Kilo profile and the shared model library.", "Usa Codex CLI, Claude Code u OpenCode como motor, con un perfil Kilo privado y la biblioteca de modelos compartida.")
	default:
		return u.tr("Set up Xcode Chat or an Xcode agent integration. Availability depends on the installed Xcode version.", "Configura Xcode Chat o un agente de Xcode. La disponibilidad depende de la versión instalada.")
	}
}

func (u *nativeUI) agentModelSummary() layout.Widget {
	s := u.sharedClientSelection("codex")
	text := u.tr("Add models to your shared library to get started.", "Añade modelos a tu biblioteca compartida para empezar.")
	if len(s.Models) > 0 {
		name := s.Initial
		if choice := s.choice(s.Initial); choice != nil {
			if choice.DisplayName != "" {
				name = choice.DisplayName
			} else if choice.Model.Name != "" {
				name = choice.Model.Name
			}
		}
		text = fmt.Sprintf(u.tr("%d shared models · Default: %s", "%d modelos compartidos · Predeterminado: %s"), len(s.Models), name)
		if len(s.Models) == 1 {
			text = fmt.Sprintf(u.tr("1 shared model · Default: %s", "1 modelo compartido · Predeterminado: %s"), name)
		}
	}
	strip := u.card(u.actionRow(u.column(u.eyebrow(u.tr("YOUR MODELS", "TUS MODELOS")), u.label(text)), u.button("agents:edit-models", u.tr("Edit models", "Editar modelos"), func() { u.page = "models" })))
	widgets := []layout.Widget{strip}
	if status, ready := u.libraryStatus(); !ready {
		widgets = append(widgets, u.note(status))
	}
	if u.setupNeeded() {
		setupCard := u.card(u.actionRow(u.column(u.heading(u.tr("Finish setting up your workspace", "Termina de configurar tu espacio")), u.note(u.tr("Connect Kilo and choose at least one model. We'll guide you through it.", "Conecta Kilo y elige al menos un modelo. Te guiamos paso a paso."))), u.primaryButton("primary.agents.setup", u.tr("Continue setup", "Continuar configuración"), u.beginSetup)))
		widgets = append(widgets, setupCard)
	}
	return u.column(widgets...)
}

func agentProjectLabel(path string) string {
	const maxRunes = 26
	if utf8.RuneCountInString(path) <= maxRunes {
		return path
	}
	runes := []rune(path)
	keep := (maxRunes - 1) / 2
	return string(runes[:keep]) + "…" + string(runes[len(runes)-keep:])
}

func (u *nativeUI) agentProjectPicker(key string) layout.Widget {
	a := u.agentsState()
	label := u.tr("Choose folder", "Elegir carpeta")
	if a.FolderBusy == key {
		label = u.tr("Choosing…", "Eligiendo…")
	}
	project := u.agentProject(key)
	projectLabel := u.note(u.tr("Default project folder", "Carpeta de proyecto predeterminada"))
	if project != "" {
		projectLabel = u.textStyle(14, agentProjectLabel(project), nativeText, font.Normal)
	}
	return u.actionRow(u.column(u.note(u.tr("Project folder", "Carpeta del proyecto")), projectLabel), u.disabled(a.FolderBusy == "", u.iconButton("agent:"+key+":folder", label, nativeButtonSecondary, nativeIconFolder, func() { u.chooseAgentProject(key) })))
}

func (u *nativeUI) agentMonogram(key string) layout.Widget {
	initials := ""
	switch key {
	case "codex":
		initials = "CD"
	case "codex-cli":
		initials = "CC"
	case "claude", "claude-desktop":
		initials = "CL"
	case "opencode":
		initials = "OC"
	case "omp":
		initials = "OM"
	case "zed":
		initials = "ZE"
	case "open-design":
		initials = "OD"
	}
	return func(gtx layout.Context) layout.Dimensions {
		side := gtx.Dp(40)
		gtx.Constraints = layout.Constraints{Min: image.Pt(side, side), Max: image.Pt(side, side)}
		return nativeBox(gtx, nativeInk, func(gtx layout.Context) layout.Dimensions {
			// Stack children start with a zero minimum; fill the tile so the initials center on it.
			gtx.Constraints.Min = image.Pt(side, side)
			return layout.Center.Layout(gtx, u.monoStyle(16, initials, nativeSurface, font.Bold))
		})
	}
}

func (u *nativeUI) agentPurpose(key string) string {
	switch key {
	case "codex":
		return u.tr("Desktop app with shared Kilo models.", "App de escritorio con modelos Kilo.")
	case "codex-cli":
		return u.tr("Terminal with shared Kilo models.", "Terminal con modelos Kilo compartidos.")
	case "claude":
		return u.tr("Claude Code with compatible models.", "Claude Code con modelos compatibles.")
	case "claude-desktop":
		return u.tr("Chat, Cowork and Code with your Kilo models.", "Chat, Cowork y Code con tus modelos de Kilo.")
	case "opencode":
		return u.tr("OpenCode in your project terminal.", "OpenCode en tu terminal de proyecto.")
	case "omp":
		return u.tr("Oh My Pi with an isolated profile.", "Oh My Pi con un perfil aislado.")
	case "zed":
		return u.tr("Zed Agent with shared models.", "Zed Agent con modelos compartidos.")
	case "open-design":
		return u.tr("Visual coding workspace.", "Espacio visual de programación.")
	default:
		return ""
	}
}

func (u *nativeUI) agentOptions(key string) layout.Widget {
	a, c := u.agentsState(), u.clientState()
	if key == "open-design" {
		return u.column(u.note(u.agentCompatibility(key)), u.pills(u.button("agent:open-design:detect", u.tr("Refresh detection", "Actualizar detección"), func() { u.detectLaunchers(); u.detectOpenDesign() }), u.button("agent:open-design:install", u.tr("Installation instructions", "Instrucciones de instalación"), func() { u.open(u.openDesignInstallURL()) })))
	}
	if key == "claude-desktop" {
		return u.column(u.note(u.agentCompatibility(key)), u.pills(
			u.button("agent:claude-desktop:setup", u.tr("Integration settings", "Ajustes de integración"), func() { u.agentSetup(key) }),
			u.button("agent:claude-desktop:detect", u.tr("Refresh detection", "Actualizar detección"), u.detectLaunchers),
		))
	}
	widgets := []layout.Widget{u.note(u.agentCompatibility(key))}
	if clientLaunchUsesProject(key) {
		widgets = append(widgets, u.field(agentProjectField(key), u.tr("Project folder path", "Ruta de la carpeta del proyecto"), c.LaunchInfo.Directory, false))
	}
	if clientLaunchUsesProject(key) && len(a.Preferences.Recent) > 0 {
		recent := []layout.Widget{}
		for i, directory := range a.Preferences.Recent {
			directory := directory
			recent = append(recent, u.button(fmt.Sprintf("agent:%s:recent:%d", key, i), filepath.Base(directory), func() {
				u.setValue(agentProjectField(key), directory)
				u.rememberAgentProject(key, directory)
			}))
		}
		widgets = append(widgets, u.note(u.tr("Recent folders", "Carpetas recientes")), u.pills(recent...))
	}
	if key == "codex" {
		widgets = append(widgets, u.field("clients-launch-app-path", u.tr("Codex application path (optional)", "Ruta de la aplicación Codex (opcional)"), c.LaunchInfo.Clients[key].Path, false))
		widgets = append(widgets, u.disabled(a.FolderBusy == "", u.button("agent:codex:locate-options", u.tr("Locate Codex", "Localizar Codex"), u.locateCodexApplication)))
	}
	if terminalClientSupported(key) {
		widgets = append(widgets, u.button("agent:"+key+":terminal-commands", u.tr("Terminal commands in Settings", "Comandos de terminal en Ajustes"), func() { u.page = "settings" }))
	}
	widgets = append(widgets, u.pills(u.button("agent:"+key+":setup", u.tr("Integration settings", "Ajustes de integración"), func() { u.agentSetup(key) }), u.button("agent:"+key+":detect", u.tr("Refresh detection", "Actualizar detección"), func() { u.refreshAgentInstallation(key) })))
	if !u.nativeLaunchAvailable(key) {
		if reason := nativeMessage(c.LaunchInfo.Clients[key].Reason, u.language); reason != "" {
			widgets = append(widgets, u.note(reason))
		}
		// CLI installation guidance is already visible in the card, and a
		// missing terminal must never suggest reinstalling an existing CLI.
		url := map[string]string{"codex": "https://openai.com/codex/", "zed": "https://zed.dev/download"}[key]
		if url != "" {
			widgets = append(widgets, u.button("agent:"+key+":install", u.tr("Installation instructions", "Instrucciones de instalación"), func() { u.open(url) }))
		}
	}
	return u.column(widgets...)
}

func (u *nativeUI) agentCard(key string) layout.Widget {
	a, c := u.agentsState(), u.clientState()
	name, kind := launchClientIdentity(key)
	if key == "codex" {
		name = "Codex"
	}
	available := u.nativeLaunchAvailable(key)
	cli := terminalClientSupported(key)
	installed := nativeLaunchClientInstalled(c.LaunchInfo.Clients[key])
	missingCLI := cli && c.LaunchChecked && !installed
	status, statusTone := u.tr("Checking installation…", "Comprobando instalación…"), nativeToneInfo
	if c.LaunchChecked {
		if available {
			statusTone = nativeToneSuccess
			status = u.tr("Installed · Desktop", "Instalado · Escritorio")
			if kind == "terminal" {
				status = u.tr("Installed · Terminal", "Instalado · Terminal")
			}
			if key == "codex" && !c.LaunchInfo.Clients[key].Available {
				status = u.tr("Installed · Custom path", "Instalado · Ruta personalizada")
			}
		} else {
			status, statusTone = u.tr("Not found", "No encontrado"), nativeToneNeutral
		}
		if cli {
			if installed {
				status, statusTone = u.tr("CLI installed", "CLI instalado"), nativeToneSuccess
			} else {
				status, statusTone = u.tr("CLI not found", "CLI no encontrado"), nativeToneNeutral
			}
		}
	}
	s := u.sharedClientSelection(key)
	_, validation := nativeClientPayload(key, s)
	libraryStatus, libraryReady := u.libraryStatus()
	connectionReady := u.agentConnectionReady() && !u.connectionWorking() && !u.setupConnectionNeeded()
	canOpen := available && libraryReady && connectionReady && len(s.Models) > 0 && validation == nil && c.Launching == "" && !u.busy["POST"+nativeClientEndpoint(key)]
	if key == "claude-desktop" {
		canOpen = canOpen && !u.busy["POST/api/claude-desktop/options"]
	}
	if key == "codex" || key == "codex-cli" {
		canOpen = canOpen && nativeClientImagesReady(s, u.models)
	}
	if key == "claude" && (!c.ClaudeChecked || u.busy["GET/api/claude/info"]) {
		canOpen = false
	}
	if key == "open-design" {
		canOpen = canOpen && u.openDesignEngineAvailable()
	}
	disabledReason := ""
	switch {
	case !c.LaunchChecked:
		disabledReason = u.tr("Wait for installation detection to finish.", "Espera a que termine la detección de la instalación.")
	case missingCLI:
		disabledReason = u.tr("Install the CLI, then check again to open it with your Kilo models.", "Instala el CLI y vuelve a comprobarlo para abrirlo con tus modelos de Kilo.")
	case !available:
		disabledReason = nativeMessage(c.LaunchInfo.Clients[key].Reason, u.language)
		if disabledReason == "" {
			disabledReason = u.tr("Install this app, then refresh installed apps.", "Instala esta aplicación y actualiza las aplicaciones instaladas.")
		}
	case !connectionReady:
		disabledReason = u.tr("Finish setting up and saving your Kilo connection first.", "Termina de configurar y guardar tu conexión de Kilo.")
	case !libraryReady:
		disabledReason = libraryStatus
	case key == "claude" && (!c.ClaudeChecked || u.busy["GET/api/claude/info"]):
		disabledReason = u.tr("Wait for Claude Code version detection to finish.", "Espera a que termine la detección de la versión de Claude Code.")
	case key == "open-design" && !c.OpenDesignChecked:
		disabledReason = u.tr("Wait for CLI engine detection to finish.", "Espera a que termine la detección del motor CLI.")
	case key == "open-design" && !u.openDesignEngineAvailable():
		disabledReason = nativeMessage(c.OpenDesign.Engines[u.openDesignEngine()].Reason, u.language)
		if disabledReason == "" {
			disabledReason = u.tr("Install the selected CLI engine before opening Open Design.", "Instala el motor CLI seleccionado antes de abrir Open Design.")
		}
	case len(s.Models) == 0 && key == "claude-desktop":
		disabledReason = u.claudeDesktopModelSummary(s)
	case len(s.Models) == 0:
		disabledReason = u.tr("Add at least one shared model before opening this agent.", "Añade al menos un modelo compartido antes de abrir este agente.")
	case validation != nil:
		disabledReason = nativeMessage(validation.Error(), u.language)
	case (key == "codex" || key == "codex-cli") && !nativeClientImagesReady(s, u.models):
		disabledReason = u.tr("Review image generation settings in Models before opening Codex.", "Revisa la generación de imágenes en Modelos antes de abrir Codex.")
	case c.Launching != "" || u.busy["POST"+nativeClientEndpoint(key)] || key == "claude-desktop" && u.busy["POST/api/claude-desktop/options"]:
		disabledReason = u.tr("Wait for the current agent operation to finish.", "Espera a que termine la operación actual del agente.")
	}
	label := u.tr("Open ", "Abrir ") + name
	if key == "open-design" {
		label = u.tr("Launch Open Design", "Abrir Open Design")
	}
	if c.Launching == key {
		label = a.Phase
	}
	optionsID := "agent:" + key + ":options"
	controls := []layout.Widget{
		u.disabled(canOpen, u.primaryButton("agent:"+key+":launch", label, func() { u.launchAgent(key) })),
	}
	if missingCLI {
		controls = []layout.Widget{u.iconButton("agent:"+key+":install", u.tr("Installation guide", "Guía de instalación"), nativeButtonPrimary, nativeIconOpenInNew, func() { u.open(launchClientInstallURL(key)) })}
	}
	if cli && c.LaunchChecked && !available {
		controls = append(controls, u.disabled(!u.busy["GET"+nativeLaunchEndpoint], u.iconButton("agent:"+key+":detect-visible", u.tr("Check again", "Comprobar de nuevo"), nativeButtonGhost, nativeIconRefresh, func() { u.refreshAgentInstallation(key) })))
	}
	controls = append(controls, u.buttonWidget(optionsID, u.tr("Options", "Opciones"), nativeButtonGhost, nativeIconChevronRight, true, false, func() { u.expanded[optionsID] = !u.expanded[optionsID] }))
	if key == "codex" && !available && c.LaunchChecked {
		controls = append(controls, u.disabled(a.FolderBusy == "", u.button("agent:codex:locate", u.tr("Locate Codex", "Localizar Codex"), u.locateCodexApplication)))
	}
	widgets := []layout.Widget{
		u.actionRow(u.topRow(u.agentMonogram(key), u.column(u.subheading(name), u.note(u.agentPurpose(key)))), u.statusBadge(statusTone, status)),
		layout.Spacer{Height: 4}.Layout,
	}
	if key == "open-design" {
		engineName, _ := launchClientIdentity(u.openDesignEngine())
		widgets = append(widgets, u.actionRow(u.column(u.note(u.tr("Engine", "Motor")), u.label(engineName)), u.ghostButton("agent:open-design:setup", u.tr("Engine settings", "Ajustes del motor"), func() { u.agentSetup(key) })))
	} else if clientLaunchUsesProject(key) && !missingCLI {
		widgets = append(widgets, u.agentProjectPicker(key))
	}
	if key == "claude-desktop" {
		widgets = append(widgets, u.note(u.claudeDesktopModelSummary(s)), u.note(u.claudeDesktopSelectionNote(s)))
	}
	widgets = append(widgets, layout.Spacer{Height: 4}.Layout, u.pills(controls...))
	if disabledReason != "" {
		widgets = append(widgets, u.hint(disabledReason))
	}
	if key == "claude" && a.ClaudeError != "" {
		widgets = append(widgets, u.message(nativeToneError, a.ClaudeError))
	}
	if len(s.Models) > 0 && validation != nil {
		widgets = append(widgets, u.message(nativeToneError, nativeMessage(validation.Error(), u.language)))
	}
	if u.expanded[optionsID] {
		widgets = append(widgets, u.agentOptions(key))
	}
	return u.card(widgets...)
}

func (u *nativeUI) agentsPanel() layout.Widget {
	a, c := u.agentsState(), u.clientState()
	if !c.LaunchDetectStarted {
		u.detectLaunchers()
	}
	u.detectAgentCapabilities()
	if !c.OpenDesignDetectStarted {
		u.detectOpenDesign()
	}
	widgets := []layout.Widget{u.agentModelSummary(), u.agentCard("codex"), u.agentCard("claude-desktop"), u.topRow(u.agentCard("claude"), u.agentCard("opencode")), u.topRow(u.agentCard("omp"), u.agentCard("codex-cli")), u.topRow(u.agentCard("zed"), u.agentCard("open-design"))}
	if a.Error != "" {
		widgets = append([]layout.Widget{u.message(nativeToneError, a.Error)}, widgets...)
	}
	if c.LaunchError != "" {
		widgets = append([]layout.Widget{u.message(nativeToneError, c.LaunchError)}, widgets...)
	}
	widgets = append(widgets, u.card(u.heading(u.tr("More integrations", "Más integraciones")), u.pills(u.button("agents:xcode", u.tr("Set up Xcode", "Configurar Xcode"), func() { u.agentSetup("xcode-chat") }), u.button("agents:other", u.tr("Other clients", "Otros clientes"), func() { u.agentSetup("other") })), u.note(u.tr("Xcode needs provider setup. Other clients can use the local OpenAI-compatible endpoint.", "Xcode requiere configurar el proveedor. Otros clientes pueden usar el endpoint local compatible con OpenAI."))))
	return u.column(widgets...)
}

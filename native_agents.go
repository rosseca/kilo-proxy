//go:build desktop

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

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
	a.Preferences.rememberProject(key, directory)
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

func (u *nativeUI) agentTunnelReady() bool {
	var session cursorSession
	data, _ := json.Marshal(u.state["cursor"])
	_ = json.Unmarshal(data, &session)
	return session.Status == "running"
}

func (u *nativeUI) agentCompatibility(key string) string {
	switch key {
	case "codex", "codex-cli":
		return u.tr("Uses Responses through an isolated Kilo profile, with the library's names, default and supported reasoning levels. Model support depends on the gateway.", "Usa Responses con un perfil Kilo separado: nombres, modelo inicial y niveles de razonamiento compatibles de la biblioteca. La compatibilidad depende del gateway.")
	case "claude":
		return u.tr("Uses Anthropic Messages. Applies only reasoning levels supported by each model and installed Claude Code version. Gateway support is also required.", "Usa Anthropic Messages. Aplica solo niveles de razonamiento compatibles con cada modelo y la versión de Claude Code. También requiere compatibilidad del gateway.")
	case "opencode":
		return u.tr("Uses Chat Completions with shared names and default model; reasoning stays automatic. Opens a terminal in your project. Local OpenCode settings can override this profile.", "Usa Chat Completions con nombres y modelo inicial compartidos; el razonamiento sigue automático. Abre una terminal en tu proyecto. Los ajustes de OpenCode pueden prevalecer.")
	case "zed":
		return u.tr("Uses Chat Completions with shared names and default model; reasoning stays automatic. Paste the local provider key once in Zed; Zed stores it in its keychain.", "Usa Chat Completions con nombres y modelo inicial compartidos; el razonamiento sigue automático. Pega la clave del proveedor una vez en Zed; Zed la guarda en su llavero.")
	case "cursor":
		return u.tr("Requires a connected HTTPS tunnel and one-time provider setup in Cursor. Cursor uses the tunnel's published model list.", "Requiere un túnel HTTPS conectado y configurar el proveedor en Cursor. Usa los modelos publicados en el túnel.")
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
	}
	widgets := []layout.Widget{u.actionRow(u.column(u.eyebrow(u.tr("YOUR MODELS", "TUS MODELOS")), u.label(text)), u.button("agents:edit-models", u.tr("Edit models", "Editar modelos"), func() { u.page = "models" }))}
	if status, ready := u.libraryStatus(); !ready {
		widgets = append(widgets, u.note(status))
	}
	if !u.agentConnectionReady() {
		widgets = append(widgets, u.actionRow(u.note(u.tr("Connect your Kilo account and choose an organization before opening an agent.", "Conecta tu cuenta Kilo y elige una organización antes de abrir un agente.")), u.button("agents:connection", u.tr("Open settings", "Abrir ajustes"), func() { u.page = "settings" })))
	}
	return u.column(widgets...)
}

func (u *nativeUI) agentProjectPicker(key string) layout.Widget {
	a := u.agentsState()
	label := u.tr("Choose folder", "Elegir carpeta")
	if a.FolderBusy == key {
		label = u.tr("Choosing…", "Eligiendo…")
	}
	project := u.agentProject(key)
	if project == "" {
		project = u.tr("Default project folder", "Carpeta de proyecto predeterminada")
	}
	return u.actionRow(u.column(u.note(u.tr("Project folder", "Carpeta del proyecto")), u.label(project)), u.disabled(a.FolderBusy == "", u.button("agent:"+key+":folder", label, func() { u.chooseAgentProject(key) })))
}

func (u *nativeUI) agentOptions(key string) layout.Widget {
	a, c := u.agentsState(), u.clientState()
	widgets := []layout.Widget{u.note(u.agentCompatibility(key)), u.field(agentProjectField(key), u.tr("Project folder path", "Ruta de la carpeta del proyecto"), c.LaunchInfo.Directory, false)}
	if key != "codex" {
		widgets = append(widgets, u.agentProjectPicker(key))
	}
	if len(a.Preferences.Recent) > 0 {
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
	widgets = append(widgets, u.pills(u.button("agent:"+key+":setup", u.tr("Integration settings", "Ajustes de integración"), func() { u.agentSetup(key) }), u.button("agent:"+key+":detect", u.tr("Refresh detection", "Actualizar detección"), func() {
		u.detectLaunchers()
		if key == "claude" && !u.busy["GET/api/claude/info"] {
			c.ClaudeDetectStarted = false
			u.detectAgentCapabilities()
		}
	})))
	if !u.nativeLaunchAvailable(key) {
		if reason := c.LaunchInfo.Clients[key].Reason; reason != "" {
			widgets = append(widgets, u.note(reason))
		}
		url := map[string]string{"codex": "https://openai.com/codex/", "codex-cli": "https://developers.openai.com/codex/cli/", "claude": "https://code.claude.com/docs/en/overview", "opencode": "https://opencode.ai/", "zed": "https://zed.dev/download", "cursor": "https://cursor.com/download"}[key]
		if url != "" {
			widgets = append(widgets, u.button("agent:"+key+":install", u.tr("Installation instructions", "Instrucciones de instalación"), func() { u.open(url) }))
		}
	}
	return u.column(widgets...)
}

func (u *nativeUI) agentCard(key string, primary bool) layout.Widget {
	a, c := u.agentsState(), u.clientState()
	name, kind := launchClientIdentity(key)
	if key == "codex" {
		name = "Codex"
	}
	status := u.tr("Checking installation…", "Comprobando instalación…")
	available := u.nativeLaunchAvailable(key)
	if c.LaunchChecked {
		status = u.tr("Installed · Desktop", "Instalado · Escritorio")
		if kind == "terminal" {
			status = u.tr("Installed · Terminal", "Instalado · Terminal")
		}
		if !available {
			status = u.tr("Not available on this computer", "No disponible en este equipo")
		} else if !c.LaunchInfo.Clients[key].Available && key == "codex" {
			status = u.tr("Custom application · Desktop", "Aplicación personalizada · Escritorio")
		}
	}
	s := u.sharedClientSelection(key)
	_, err := nativeClientPayload(key, s)
	_, libraryReady := u.libraryStatus()
	canOpen := available && libraryReady && u.agentConnectionReady() && len(s.Models) > 0 && err == nil && c.Launching == "" && !u.busy["POST"+nativeClientEndpoint(key)]
	if key == "codex" || key == "codex-cli" {
		canOpen = canOpen && nativeClientImagesReady(s, u.models)
	}
	if key == "cursor" {
		canOpen = available && libraryReady && u.agentConnectionReady() && u.agentTunnelReady() && c.Launching == ""
		if available && !u.agentTunnelReady() {
			status = u.tr("Installed · HTTPS tunnel required", "Instalado · Requiere túnel HTTPS")
		}
	}
	if key == "claude" && !c.ClaudeChecked {
		canOpen = false
		if available {
			status = u.tr("Installed · Checking version…", "Instalado · Comprobando versión…")
		}
		if a.ClaudeError != "" {
			status = a.ClaudeError
		}
	}
	label := u.tr("Open ", "Abrir ") + name
	if c.Launching == key {
		label = a.Phase
	}
	controls := []layout.Widget{u.disabled(canOpen, u.button("agent:"+key+":launch", label, func() { u.launchAgent(key) })), u.button("agent:"+key+":options", u.tr("Options", "Opciones")+"  ▾", func() { u.expanded["agent:"+key+":options"] = !u.expanded["agent:"+key+":options"] })}
	if key == "cursor" && !u.agentTunnelReady() {
		controls = append(controls, u.button("agent:cursor:tunnel", u.tr("Set up tunnel", "Configurar túnel"), func() { u.agentSetup(key) }))
	}
	if key == "codex" && !available && c.LaunchChecked {
		controls = append(controls, u.disabled(a.FolderBusy == "", u.button("agent:codex:locate", u.tr("Locate Codex", "Localizar Codex"), u.locateCodexApplication)))
	}
	widgets := []layout.Widget{u.actionRow(u.column(u.heading(name), u.note(status)), controls...)}
	if primary {
		widgets = append(widgets, u.agentProjectPicker(key))
	} else if project := u.agentProject(key); project != "" {
		widgets = append(widgets, u.note(u.tr("Project: ", "Proyecto: ")+filepath.Base(project)))
	}
	if !available && c.LaunchChecked {
		reason := c.LaunchInfo.Clients[key].Reason
		if reason != "" {
			widgets = append(widgets, u.note(reason))
		}
	}
	if key == "zed" {
		widgets = append(widgets, u.note(u.tr("One-time provider key setup in Zed may be needed.", "Puede requerir configurar la clave del proveedor una vez en Zed.")))
	}
	if len(s.Models) > 0 && err != nil && key != "cursor" {
		widgets = append(widgets, u.note(nativeMessage(err.Error(), u.language)))
	}
	if (key == "codex" || key == "codex-cli") && !nativeClientImagesReady(s, u.models) {
		widgets = append(widgets, u.note(u.tr("Review image generation settings in Models before opening Codex.", "Revisa la generación de imágenes en Modelos antes de abrir Codex.")))
	}
	if u.expanded["agent:"+key+":options"] {
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
	widgets := []layout.Widget{u.agentModelSummary(), u.agentCard("codex", true), u.topRow(u.agentCard("claude", false), u.agentCard("opencode", false)), u.topRow(u.agentCard("codex-cli", false), u.agentCard("zed", false)), u.agentCard("cursor", false)}
	if a.Error != "" {
		widgets = append([]layout.Widget{u.note(a.Error)}, widgets...)
	}
	if c.LaunchError != "" {
		widgets = append([]layout.Widget{u.note(c.LaunchError)}, widgets...)
	}
	widgets = append(widgets, u.card(u.heading(u.tr("More integrations", "Más integraciones")), u.pills(u.button("agents:xcode", u.tr("Set up Xcode", "Configurar Xcode"), func() { u.agentSetup("xcode-chat") }), u.button("agents:other", u.tr("Other clients", "Otros clientes"), func() { u.agentSetup("other") })), u.note(u.tr("Xcode needs provider setup. Other clients can use the local OpenAI-compatible endpoint.", "Xcode requiere configurar el proveedor. Otros clientes pueden usar el endpoint local compatible con OpenAI."))))
	return u.column(widgets...)
}

//go:build desktop

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gioui.org/layout"
)

type nativeAgents struct {
	OpenMausBot       nativeOpenMausBotState
	T3Code            nativeT3CodeState
	Synara            nativeSynaraState
	Preferences       agentPreferences
	Error             string
	ClaudeError       string
	FolderBusy        string
	Phase             string
	chooseFolder      func(string) (string, error)
	chooseApplication func() (string, error)
	now               func() time.Time
}

// agentProjectField holds a harness's own folder override. Empty means the
// harness opens in the global project folder.
func agentProjectField(key string) string { return "agent:" + key + ":directory" }

// uxhGlobalFolderBusy marks the global folder chooser in FolderBusy.
const uxhGlobalFolderBusy = "agents:folder"

func (u *nativeUI) agentsState() *nativeAgents {
	if u.agents == nil {
		prefs, err := readAgentPreferences(u.owner.dir)
		u.agents = &nativeAgents{Preferences: prefs, chooseFolder: chooseNativeFolder, chooseApplication: chooseNativeApplication, now: time.Now}
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
			u.uxhSavePreferences()
		})
	}()
}

// uxhGlobalFolder is the folder every harness opens in unless it has its own.
func (u *nativeUI) uxhGlobalFolder() string {
	if folder := u.agentsState().Preferences.Folder; folder != "" {
		return folder
	}
	return u.clientState().LaunchInfo.Directory
}

// uxhProjectOverride is the folder chosen for this harness only, if any.
func (u *nativeUI) uxhProjectOverride(key string) string {
	u.agentsState()
	return strings.TrimSpace(u.value(agentProjectField(key)))
}

// agentProject is the folder a harness opens in: its override, else the global folder.
func (u *nativeUI) agentProject(key string) string {
	if directory := u.uxhProjectOverride(key); directory != "" {
		return directory
	}
	return u.uxhGlobalFolder()
}

func (u *nativeUI) uxhSavePreferences() {
	a := u.agentsState()
	a.Preferences.CodexAppPath = strings.TrimSpace(u.value("clients-launch-app-path"))
	if err := writeAgentPreferences(u.owner.dir, a.Preferences); err != nil {
		a.Error = u.tr("The project folder could not be remembered. You can still open the harness.", "No se pudo recordar la carpeta. Puedes abrir el harness igualmente.")
		return
	}
	a.Error = ""
}

// uxhNoteOpened records a successful launch for "last used" and recent folders.
func (u *nativeUI) uxhNoteOpened(key, directory string) {
	a := u.agentsState()
	if !clientLaunchUsesProject(key) {
		directory = ""
	} else if override := u.uxhProjectOverride(key); override != "" && directory != "" {
		// A folder typed only for this harness becomes its remembered override.
		a.Preferences.rememberProject(key, directory)
	}
	a.Preferences.noteOpened(key, directory, a.now())
	u.uxhSavePreferences()
}

// uxhSetProject stores (or, when empty, clears) a harness's own folder.
func (u *nativeUI) uxhSetProject(key, directory string) {
	a := u.agentsState()
	u.setValue(agentProjectField(key), directory)
	if clientLaunchUsesProject(key) {
		a.Preferences.rememberProject(key, directory)
	}
	u.uxhSavePreferences()
}

// uxhUseGlobalFolder makes directory the default folder for every harness.
func (u *nativeUI) uxhUseGlobalFolder(directory string) error {
	a := u.agentsState()
	path, err := launchPath(directory, u.clientState().LaunchInfo.Directory)
	if err != nil {
		return err
	}
	a.Preferences.useFolder(path, a.now())
	u.uxhSavePreferences()
	return nil
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
			u.uxhSetProject(key, path)
		})
	}()
}

// uxhChooseGlobalFolder opens the system chooser for the default project folder.
func (u *nativeUI) uxhChooseGlobalFolder() {
	a := u.agentsState()
	if a.FolderBusy != "" {
		return
	}
	a.FolderBusy = uxhGlobalFolderBusy
	before, initial := a.Preferences.Folder, u.uxhGlobalFolder()
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
			if before != a.Preferences.Folder {
				a.Error = u.tr("The project folder changed while the chooser was open. Choose the folder again.", "La carpeta cambió con el selector abierto. Vuelve a elegirla.")
				return
			}
			if err = u.uxhUseGlobalFolder(path); err != nil {
				a.Error = nativeMessage(err.Error(), u.language)
			}
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
	if u.chatGPTReady() {
		return true
	}
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
	case "openmausbot":
		return u.tr("Uses the local OpenAI-compatible connection with your shared models, listing the default first. OpenMausBot shows exact model IDs instead of custom names. Kilo Proxy applies each model’s supported reasoning level saved in Models. Generation and tools depend on the model and provider.", "Usa la conexión local compatible con OpenAI con tus modelos compartidos, mostrando primero el predeterminado. OpenMausBot muestra los ID exactos en lugar de nombres personalizados. Kilo Proxy aplica el nivel de razonamiento compatible de cada modelo guardado en Modelos. La generación y las herramientas dependen del modelo y del proveedor.")
	case "synara":
		return u.tr("Opens a separate Synara workspace with four Codex and Claude agents. Normal agents use existing CLI sessions; Kilo agents use shared models through the proxy. Start a new chat to change accounts and select the prepared exact gateway model ID.", "Abre un espacio Synara separado con cuatro agentes Codex y Claude. Los agentes normales usan sesiones CLI existentes; los agentes Kilo usan modelos compartidos a través del proxy. Inicia un chat nuevo para cambiar de cuenta y elige el ID exacto de modelo del gateway preparado.")
	case "t3-code":
		return u.tr("Opens a separate T3 Code workspace with Codex, Codex · Kilo, Claude and Claude · Kilo. Normal agents use your existing CLI login; Kilo agents use your shared models through the proxy. Choose the agent when creating a new chat. Codex supports the model's reasoning levels; Claude offers only levels supported by its CLI and model.", "Abre un espacio T3 Code separado con Codex, Codex · Kilo, Claude y Claude · Kilo. Los agentes normales usan tu sesión CLI existente; los agentes Kilo usan tus modelos compartidos a través del proxy. Elige el agente al crear un chat nuevo. Codex admite los niveles de razonamiento del modelo; Claude ofrece solo los compatibles con su CLI y modelo.")
	case "claude-desktop":
		return u.tr("Opens a separate Kilo data profile. Regular Claude can stay open; use its usual icon to open it. Quit only the Kilo window before applying changes or opening it again. Other providers require the experimental option in these settings.", "Abre un perfil de datos Kilo separado. Claude normal puede seguir abierto; ábrelo desde su icono habitual. Cierra solo la ventana Kilo antes de aplicar cambios o volver a abrirla. Los demás proveedores requieren la opción experimental de estos ajustes.")
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

func (u *nativeUI) agentPurpose(key string) string {
	switch key {
	case "codex":
		return u.tr("OpenAI desktop app", "App de escritorio de OpenAI")
	case "claude-desktop":
		return u.tr("Chat, Cowork and Code · your regular Claude can stay open", "Chat, Cowork y Code · tu Claude normal puede seguir abierto")
	case "t3-code", "synara":
		return u.tr("One workspace, four Codex and Claude agent options", "Un espacio, cuatro opciones de agentes Codex y Claude")
	case "openmausbot":
		return u.tr("Desktop assistant with your shared models", "Asistente de escritorio con tus modelos compartidos")
	case "claude":
		c := u.clientState()
		if c.ClaudeChecked && c.Claude.Version != "" {
			if u.clientCaps("claude").Picker {
				return fmt.Sprintf(u.tr("Terminal · version %s, compatible", "Terminal · versión %s, compatible"), c.Claude.Version)
			}
			return fmt.Sprintf(u.tr("Terminal · version %s, basic aliases", "Terminal · versión %s, alias básicos"), c.Claude.Version)
		}
		return u.tr("Terminal", "Terminal")
	case "codex-cli", "opencode":
		return u.tr("Terminal", "Terminal")
	case "omp":
		return u.tr("Terminal with an isolated profile", "Terminal con perfil aislado")
	case "zed":
		return u.tr("Editor with Zed Agent", "Editor con Zed Agent")
	case "open-design":
		return u.tr("Visual coding workspace", "Espacio visual de programación")
	default:
		return ""
	}
}

func (u *nativeUI) agentsPanel() layout.Widget {
	c := u.clientState()
	if !c.LaunchDetectStarted {
		u.detectLaunchers()
	}
	u.detectAgentCapabilities()
	if !c.OpenDesignDetectStarted {
		u.detectOpenDesign()
	}
	return u.uxhListPage()
}

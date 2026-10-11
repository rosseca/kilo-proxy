//go:build desktop

package main

import (
	"encoding/json"
	"runtime"
	"strings"

	"gioui.org/layout"
	"gioui.org/widget/material"
)

const nativeTerminalCommandsEndpoint = "/api/terminal/commands"
const nativeTerminalManualEndpoint = "/api/terminal/manual"

var nativeTerminalCommandNames = []string{"kilo-codex", "kilo-claude", "kilo-omp", "kilo-opencode"}

type nativeTerminalCommandsInfo struct {
	Supported      bool              `json:"supported"`
	Installed      bool              `json:"installed"`
	Directory      string            `json:"directory"`
	Commands       map[string]string `json:"commands"`
	PathConfigured bool              `json:"pathConfigured"`
	Shell          string            `json:"shell"`
	StartupFiles   []string          `json:"startupFiles"`
	Message        string            `json:"message"`
}

type nativeTerminalCommands struct {
	Info    nativeTerminalCommandsInfo
	Started bool
	Checked bool
	Error   string

	Manual         nativeTerminalManualInfo
	ManualStarted  bool
	ManualChecked  bool
	ManualError    string
	ManualSelected string
}

type nativeTerminalManualInfo struct {
	Supported bool              `json:"supported"`
	Shell     string            `json:"shell"`
	Commands  map[string]string `json:"commands"`
	All       string            `json:"all"`
}

func (u *nativeUI) terminalCommandsState() *nativeTerminalCommands {
	if u.terminalCommands == nil {
		u.terminalCommands = &nativeTerminalCommands{}
	}
	return u.terminalCommands
}

func (u *nativeUI) requestTerminalCommands(method string) {
	if method != "GET" && method != "POST" {
		return
	}
	if u.busy["GET"+nativeTerminalCommandsEndpoint] || u.busy["POST"+nativeTerminalCommandsEndpoint] {
		return
	}
	s := u.terminalCommandsState()
	if method == "POST" && (!s.Checked || !s.Info.Supported) {
		return
	}
	s.Started, s.Error = true, ""
	key := method + nativeTerminalCommandsEndpoint
	u.busy[key] = true
	go func() {
		raw, err := nativeRequest(u.owner, method, nativeTerminalCommandsEndpoint, nil)
		u.enqueue(func() {
			delete(u.busy, key)
			if err != nil {
				s.Error = err.Error()
				return
			}
			var info nativeTerminalCommandsInfo
			if json.Unmarshal(raw, &info) != nil {
				s.Error = "Could not read terminal command status."
				return
			}
			s.Info, s.Checked = info, true
		})
	}()
}

func (u *nativeUI) requestTerminalManual() {
	key := "GET" + nativeTerminalManualEndpoint
	if u.busy[key] {
		return
	}
	s := u.terminalCommandsState()
	s.ManualStarted, s.ManualError = true, ""
	u.busy[key] = true
	go func() {
		raw, err := nativeRequest(u.owner, "GET", nativeTerminalManualEndpoint, nil)
		u.enqueue(func() {
			delete(u.busy, key)
			if err != nil {
				s.ManualError = err.Error()
				return
			}
			var info nativeTerminalManualInfo
			if json.Unmarshal(raw, &info) != nil || !validNativeTerminalManualInfo(info) {
				s.ManualError = "Could not read manual terminal functions."
				return
			}
			s.Manual, s.ManualChecked = info, true
			if info.Commands[s.ManualSelected] == "" {
				s.ManualSelected = ""
			}
		})
	}()
}

func validNativeTerminalManualInfo(info nativeTerminalManualInfo) bool {
	if !info.Supported {
		return true
	}
	if (info.Shell != "zsh-bash" && info.Shell != "powershell") || strings.TrimSpace(info.All) == "" {
		return false
	}
	for _, name := range nativeTerminalCommandNames {
		if strings.TrimSpace(info.Commands[name]) == "" {
			return false
		}
	}
	return true
}

func (s *nativeTerminalCommands) powerShell() bool {
	return s.Info.Shell == "powershell" || s.Manual.Shell == "powershell" || runtime.GOOS == "windows"
}

func nativeTerminalCommandsMessage(message, language string) string {
	if language == "es" {
		translations := map[string]string{
			"Cannot locate the PowerShell profile safely. Open PowerShell and check $PROFILE.CurrentUserAllHosts.":                               "No se puede localizar el perfil de PowerShell de forma segura. Abre PowerShell y comprueba $PROFILE.CurrentUserAllHosts.",
			"Install Windows PowerShell or PowerShell 7 to install terminal commands automatically.":                                             "Instala Windows PowerShell o PowerShell 7 para instalar los comandos de terminal automáticamente.",
			"The PowerShell profile encoding is invalid; nothing was changed.":                                                                   "La codificación del perfil de PowerShell no es válida; no se ha cambiado nada.",
			"This PowerShell profile is signed. Add the commands manually and sign it again.":                                                    "Este perfil de PowerShell está firmado. Añade los comandos manualmente y vuelve a firmarlo.",
			"The Kilo Proxy PowerShell block is incomplete or duplicated. Repair that block before installing terminal commands.":                "El bloque de PowerShell de Kilo Proxy está incompleto o duplicado. Repáralo antes de instalar los comandos de terminal.",
			"An existing PowerShell command uses a Kilo command name. Rename it before installing terminal commands.":                            "Un comando existente de PowerShell usa un nombre de comando de Kilo. Cámbiale el nombre antes de instalar los comandos de terminal.",
			"The PowerShell profile would exceed 1 MiB; nothing was changed.":                                                                    "El perfil de PowerShell superaría 1 MiB; no se ha cambiado nada.",
			"Could not read manual terminal functions.":                                                                                          "No se pudieron leer las funciones de terminal para la configuración manual.",
			"Cannot prepare manual terminal commands. Check the Kilo Proxy application and configuration paths.":                                 "No se pueden preparar los comandos manuales de terminal. Revisa las rutas de la aplicación Kilo Proxy y de su configuración.",
			"Could not read terminal command status.":                                                                                            "No se pudo leer el estado de los comandos de terminal.",
			"Another launch or installation is being prepared.":                                                                                  "Se está preparando otro inicio o instalación.",
			"Automatic terminal command installation supports Zsh, Bash and Fish. Choose one of these as your login shell.":                      "La instalación automática de comandos admite Zsh, Bash y Fish. Elige uno como shell de inicio de sesión.",
			"ZDOTDIR must be an absolute directory inside your home to install terminal commands automatically.":                                 "ZDOTDIR debe ser una ruta absoluta dentro de tu carpeta personal para instalar los comandos automáticamente.",
			"XDG_CONFIG_HOME must be an absolute directory inside your home to install Fish terminal commands automatically.":                    "XDG_CONFIG_HOME debe ser una ruta absoluta dentro de tu carpeta personal para instalar los comandos de Fish automáticamente.",
			"Cannot inspect the Bash login startup file.":                                                                                        "No se puede comprobar el archivo de inicio de sesión de Bash.",
			"Cannot locate your home directory.":                                                                                                 "No se encuentra tu carpeta personal.",
			"The Kilo Proxy PATH block is incomplete or duplicated. Repair that block before installing terminal commands.":                      "El bloque PATH de Kilo Proxy está incompleto o duplicado. Repáralo antes de instalar los comandos de terminal.",
			"An unrelated kilo-proxy.fish already exists. Move it before installing terminal commands.":                                          "Ya existe un kilo-proxy.fish ajeno a Kilo Proxy. Muévelo antes de instalar los comandos de terminal.",
			"Could not finish installing terminal commands. Existing files were restored; check your home folder permissions and retry.":         "No se pudo completar la instalación de los comandos de terminal. Se restauraron los archivos existentes; revisa los permisos de tu carpeta personal y vuelve a intentarlo.",
			"Could not finish installing terminal commands or restore every changed file. Restore the saved .kilo-backup files before retrying.": "No se pudo completar la instalación ni restaurar todos los archivos modificados. Restaura las copias .kilo-backup guardadas antes de volver a intentarlo.",
		}
		if translated := translations[message]; translated != "" {
			return translated
		}
		for _, name := range nativeTerminalCommandNames {
			if message == "An unrelated "+name+" already exists. Move or rename it before installing terminal commands." {
				return "Ya existe un " + name + " ajeno a Kilo Proxy. Muévelo o cámbiale el nombre antes de instalar los comandos de terminal."
			}
		}
	}
	return nativeMessage(message, language)
}

// terminalCommandsPanel is the Terminal commands block: one Shortcuts row with
// install/check, installed details and the manual setup behind a link.
func (u *nativeUI) terminalCommandsPanel() layout.Widget {
	s := u.terminalCommandsState()
	if !s.Started {
		u.requestTerminalCommands("GET")
	}
	if !s.ManualStarted {
		u.requestTerminalManual()
	}
	title := u.tr("Terminal commands", "Comandos de terminal")
	names := strings.Join(nativeTerminalCommandNames, ", ")
	checking, installing := u.busy["GET"+nativeTerminalCommandsEndpoint], u.busy["POST"+nativeTerminalCommandsEndpoint]
	if s.Checked && !s.Info.Supported {
		return u.uxsBlock(title, "", u.uxsRow(u.tr("Shortcuts", "Atajos"), names, u.uxsValue(u.uxsPlain(u.tr("Not available here", "No disponibles aquí")), u.tr("They are available on macOS, Windows and Linux.", "Están disponibles en macOS, Windows y Linux.")), nil, nil))
	}
	powerShell := s.powerShell()
	explain := u.tr("Open any CLI from your terminal with your models. They install to ~/.local/bin.", "Abre cualquier CLI desde tu terminal con tus modelos. Se instalan en ~/.local/bin.")
	if powerShell {
		explain = u.tr("Open any CLI from PowerShell with your models. They're added as functions to your PowerShell profile.", "Abre cualquier CLI desde PowerShell con tus modelos. Se añaden como funciones a tu perfil de PowerShell.")
	}
	status, tone := u.tr("Not installed", "No instalados"), nativeToneNeutral
	switch {
	case installing:
		status, tone = u.tr("Installing…", "Instalando…"), nativeToneInfo
	case checking:
		status, tone = u.tr("Checking terminal commands…", "Comprobando comandos de terminal…"), nativeToneInfo
	case s.Info.Installed:
		status, tone = u.tr("Installed", "Instalados"), nativeToneSuccess
	case !s.Checked:
		status = u.tr("Not checked yet", "Sin comprobar")
	}
	value := []layout.Widget{u.uxsValue(u.uxsStatus(tone, status, ""), explain)}
	if s.Error != "" {
		value = append(value, u.message(nativeToneError, nativeTerminalCommandsMessage(s.Error, u.language)))
	}
	ready := s.Checked && s.Info.Supported && !checking && !installing
	var main layout.Widget
	if s.Info.Installed {
		main = u.disabled(!checking && !installing, u.button("terminal-commands.refresh", u.tr("Check", "Comprobar"), func() { u.requestTerminalCommands("GET") }))
		value = append(value, u.disclosure("terminal-commands.details", u.tr("Commands and paths", "Comandos y rutas")))
	} else {
		main = u.disabled(ready, u.primaryButton("terminal-commands.install", u.tr("Install", "Instalar"), func() { u.requestTerminalCommands("POST") }))
	}
	actions := u.uxsActions(main, u.uxsLink("terminal-commands.manual.toggle", u.tr("Manual", "Manual"), func() {
		u.expanded["terminal-commands.manual.toggle"] = !u.expanded["terminal-commands.manual.toggle"]
	}))
	below := []layout.Widget{}
	if s.Info.Installed && u.expanded["terminal-commands.details"] {
		below = append(below, u.terminalInstalledWidgets(installing, checking)...)
	}
	if u.expanded["terminal-commands.manual.toggle"] {
		below = append(below, u.terminalManualWidgets()...)
	}
	var area layout.Widget
	if len(below) > 0 {
		area = u.column(below...)
	}
	return u.uxsBlock(title, "", u.uxsRow(u.tr("Shortcuts", "Atajos"), names, u.column(value...), actions, area))
}

// terminalInstalledWidgets lists installed commands with copy buttons, paths
// and the action that rewrites them for the current app location.
func (u *nativeUI) terminalInstalledWidgets(installing, checking bool) []layout.Widget {
	s := u.terminalCommandsState()
	powerShell := s.powerShell()
	children := []layout.Widget{u.note(u.tr("Run these from any project terminal. They pass arguments through, use your latest saved models and start the saved connection if the proxy is stopped. Install Codex CLI, Claude Code, Oh My Pi or OpenCode separately.", "Ejecútalos desde cualquier terminal de proyecto. Pasan los argumentos, usan tus últimos modelos guardados e inician la conexión guardada si el proxy está detenido. Instala Codex CLI, Claude Code, Oh My Pi u OpenCode por separado."))}
	label := u.tr("Update terminal commands", "Actualizar comandos de terminal")
	if powerShell {
		label = u.tr("Update PowerShell functions", "Actualizar funciones de PowerShell")
		children = append(children, u.note(u.tr("Your execution policy must allow PowerShell profiles; Kilo Proxy does not change it.", "Tu política de ejecución debe permitir los perfiles de PowerShell; Kilo Proxy no la cambia.")))
	}
	if installing {
		label = u.tr("Installing terminal commands…", "Instalando comandos de terminal…")
	}
	if s.Info.Directory != "" && !powerShell {
		children = append(children, u.note(u.tr("Installed in: ", "Instalados en: ")+s.Info.Directory))
	}
	if powerShell && len(s.Info.StartupFiles) > 0 {
		children = append(children, u.note(u.tr("PowerShell profiles:", "Perfiles de PowerShell:")))
		for _, path := range s.Info.StartupFiles {
			children = append(children, u.note(path))
		}
	}
	if powerShell {
		children = append(children, u.note(u.tr("Open a new PowerShell window to load the functions from your profile.", "Abre una ventana nueva de PowerShell para cargar las funciones de tu perfil.")))
	} else if s.Info.PathConfigured {
		children = append(children, u.note(u.tr("Open a new terminal to use the commands from PATH.", "Abre una terminal nueva para usar los comandos desde PATH.")))
	} else {
		children = append(children, u.note(u.tr("Add the install location to your shell's PATH, or use the full paths below.", "Añade la carpeta de instalación al PATH del shell o usa las rutas completas de abajo.")))
	}
	if !powerShell && len(s.Info.StartupFiles) > 0 {
		children = append(children, u.note(u.tr("Shell startup files: ", "Archivos de inicio del shell: ")+strings.Join(s.Info.StartupFiles, ", ")))
	}
	for _, name := range nativeTerminalCommandNames {
		if path := s.Info.Commands[name]; path != "" {
			command := name
			if !powerShell && !s.Info.PathConfigured {
				command = helperShellQuote(path)
			}
			children = append(children, u.actionRow(
				u.terminalCommandCode("terminal-commands.command."+name, command),
				u.iconButton("terminal-commands.copy."+name, u.tr("Copy ", "Copiar ")+name, nativeButtonSecondary, nativeIconCopy, func() { u.copyTerminalCommand(command) }),
			))
		}
	}
	children = append(children, u.pills(u.disabled(s.Info.Supported && !checking && !installing, u.button("terminal-commands.install", label, func() { u.requestTerminalCommands("POST") }))))
	return children
}

func (u *nativeUI) terminalManualWidgets() []layout.Widget {
	s := u.terminalCommandsState()
	if s.ManualChecked && !s.Manual.Supported {
		return nil
	}
	const toggleID = "terminal-commands.manual.toggle"
	title := u.tr("Manual setup · Zsh / Bash", "Configuración manual · Zsh / Bash")
	instructions := u.tr("Paste these functions into ~/.zshrc for Zsh or ~/.bashrc for Bash, then open a new terminal. No installer or PATH changes are needed.", "Pega estas funciones en ~/.zshrc para Zsh o ~/.bashrc para Bash y abre una terminal nueva. No hace falta usar el instalador ni cambiar el PATH.")
	if s.powerShell() {
		title = u.tr("Manual setup · PowerShell", "Configuración manual · PowerShell")
		instructions = u.tr("Paste these functions into $PROFILE in the PowerShell edition you use (Windows PowerShell 5.1 or PowerShell 7), then open a new PowerShell window. No installer or PATH changes are needed.", "Pega estas funciones en $PROFILE en la edición de PowerShell que uses (Windows PowerShell 5.1 o PowerShell 7) y abre una ventana nueva de PowerShell. No hace falta usar el instalador ni cambiar el PATH.")
	}
	// The row's Manual link (toggleID) opens this section.
	_ = toggleID
	children := []layout.Widget{u.subheading(title)}
	loading := u.busy["GET"+nativeTerminalManualEndpoint]
	ready := s.ManualChecked && s.Manual.Supported && s.ManualError == "" && !loading
	children = append(children,
		u.note(instructions),
		u.note(u.tr("Keep Kilo Proxy open. If you move the app or change its settings folder, refresh these functions and replace the copies in your shell file.", "Mantén Kilo Proxy abierto. Si mueves la aplicación o cambias su carpeta de configuración, actualiza estas funciones y sustituye las copias en el archivo de inicio del shell.")),
	)
	if s.powerShell() {
		children = append(children, u.note(u.tr("Your execution policy must allow PowerShell profiles; Kilo Proxy does not change it.", "Tu política de ejecución debe permitir los perfiles de PowerShell; Kilo Proxy no la cambia.")))
	}
	actions := []layout.Widget{
		u.disabled(ready, u.iconButton("terminal-commands.manual.copy-all", u.tr("Copy all", "Copiar todo"), nativeButtonSecondary, nativeIconCopy, func() { u.copyTerminalCommand(s.Manual.All) })),
		u.disabled(!loading, u.button("terminal-commands.manual.refresh", u.tr("Refresh manual setup", "Actualizar configuración manual"), u.requestTerminalManual)),
	}
	if s.ManualSelected != "" {
		name := s.ManualSelected
		actions = append(actions, u.disabled(ready, u.iconButton("terminal-commands.manual.copy."+name, u.tr("Copy "+name+" function", "Copiar función "+name), nativeButtonSecondary, nativeIconCopy, func() { u.copyTerminalCommand(s.Manual.Commands[name]) })))
	}
	children = append(children, u.pills(actions...))
	if loading {
		children = append(children, u.statusBadge(nativeToneInfo, u.tr("Loading manual functions…", "Cargando funciones manuales…")))
	}
	if s.ManualError != "" {
		children = append(children, u.message(nativeToneError, nativeTerminalCommandsMessage(s.ManualError, u.language)))
	}
	if s.ManualChecked && s.Manual.Supported {
		choices := []layout.Widget{u.buttonKind("terminal-commands.manual.select-all", u.tr("All functions", "Todas las funciones"), nativeButtonSecondary, nil, s.ManualSelected == "", func() {
			s.ManualSelected = ""
			u.list("terminal-commands.manual.scroll").ScrollTo(0)
		})}
		for _, name := range nativeTerminalCommandNames {
			choices = append(choices, u.buttonKind("terminal-commands.manual.select."+name, name, nativeButtonSecondary, nil, s.ManualSelected == name, func() {
				s.ManualSelected = name
				u.list("terminal-commands.manual.scroll").ScrollTo(0)
			}))
		}
		value := s.Manual.All
		if s.ManualSelected != "" {
			value = s.Manual.Commands[s.ManualSelected]
		}
		preview := u.terminalCommandCode("terminal-commands.manual.preview", value)
		if s.powerShell() {
			preview = u.scroll("terminal-commands.manual.scroll", 300, preview)
		}
		children = append(children, u.pills(choices...), preview)
	}
	return children
}

func (u *nativeUI) terminalCommandCode(id, value string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		e := u.editor(id)
		e.ReadOnly = true
		e.SingleLine = false
		e.MaxLen = 0
		e.Mask = 0
		if e.Text() != value {
			e.SetText(value)
		}
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return nativeBox(gtx, nativeSurfaceAlt, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				style := material.Editor(u.theme, e, "")
				style.Font.Typeface = nativeMonoFace
				style.TextSize = 13
				return style.Layout(gtx)
			})
		})
	}
}

func (u *nativeUI) copyTerminalCommand(value string) {
	u.copy(value)
	if u.noticeTone == nativeToneSuccess {
		u.setNotice(nativeToneSuccess, u.tr("Copied", "Copiado"))
	}
}

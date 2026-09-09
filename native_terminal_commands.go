//go:build desktop

package main

import (
	"encoding/json"
	"strings"

	"gioui.org/layout"
)

const nativeTerminalCommandsEndpoint = "/api/terminal/commands"

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

func nativeTerminalCommandsMessage(message, language string) string {
	if language == "es" {
		translations := map[string]string{
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
		for _, name := range []string{"kilo-codex", "kilo-claude"} {
			if message == "An unrelated "+name+" already exists. Move or rename it before installing terminal commands." {
				return "Ya existe un " + name + " ajeno a Kilo Proxy. Muévelo o cámbiale el nombre antes de instalar los comandos de terminal."
			}
		}
	}
	return nativeMessage(message, language)
}

func (u *nativeUI) terminalCommandsPanel() layout.Widget {
	s := u.terminalCommandsState()
	if !s.Started {
		u.requestTerminalCommands("GET")
	}
	checking, installing := u.busy["GET"+nativeTerminalCommandsEndpoint], u.busy["POST"+nativeTerminalCommandsEndpoint]
	children := []layout.Widget{u.heading(u.tr("Terminal commands", "Comandos de terminal"))}
	if s.Checked && !s.Info.Supported {
		return u.card(append(children, u.note(u.tr("kilo-codex and kilo-claude are available on macOS and Linux.", "kilo-codex y kilo-claude están disponibles en macOS y Linux.")))...)
	}
	children = append(children,
		u.note(u.tr("Use kilo-codex or kilo-claude in your current terminal and project. They forward arguments and use your latest saved shared models.", "Usa kilo-codex o kilo-claude en tu terminal y proyecto actuales. Pasan tus argumentos al CLI y usan los últimos modelos compartidos guardados.")),
		u.note(u.tr("Keep Kilo Proxy open, including in the tray. The commands start its saved connection if stopped.", "Mantén Kilo Proxy abierto, también en la bandeja. Los comandos inician su conexión guardada si está detenida.")),
		u.note(u.tr("Installs to ~/.local/bin and configures PATH for zsh, bash or fish. Install Codex CLI or Claude Code separately.", "Se instalan en ~/.local/bin y configuran PATH para zsh, bash o fish. Instala Codex CLI o Claude Code por separado.")),
	)
	label := u.tr("Install terminal commands", "Instalar comandos de terminal")
	if s.Info.Installed {
		label = u.tr("Update terminal commands", "Actualizar comandos de terminal")
	}
	if installing {
		label = u.tr("Installing terminal commands…", "Instalando comandos de terminal…")
	}
	children = append(children, u.pills(
		u.disabled(s.Checked && s.Info.Supported && !checking && !installing, u.button("terminal-commands.install", label, func() { u.requestTerminalCommands("POST") })),
		u.disabled(!checking && !installing, u.button("terminal-commands.refresh", u.tr("Check installation", "Comprobar instalación"), func() { u.requestTerminalCommands("GET") })),
	))
	if checking {
		children = append(children, u.note(u.tr("Checking terminal commands…", "Comprobando comandos de terminal…")))
	}
	if s.Error != "" {
		children = append(children, u.note(nativeTerminalCommandsMessage(s.Error, u.language)))
	}
	if s.Checked && s.Info.Directory != "" {
		prefix := u.tr("Install location: ", "Carpeta de instalación: ")
		if s.Info.Installed {
			prefix = u.tr("Installed in: ", "Instalados en: ")
		}
		children = append(children, u.note(prefix+s.Info.Directory))
	}
	if s.Info.Installed {
		if s.Info.PathConfigured {
			children = append(children, u.note(u.tr("Open a new terminal to use the commands from PATH.", "Abre una terminal nueva para usar los comandos desde PATH.")))
		} else {
			children = append(children, u.note(u.tr("Add the install location to your shell's PATH, or run the commands using their full paths below.", "Añade la carpeta de instalación al PATH de tu shell, o ejecuta los comandos con las rutas completas que aparecen abajo.")))
		}
		if len(s.Info.StartupFiles) > 0 {
			children = append(children, u.note(u.tr("Shell startup files: ", "Archivos de inicio del shell: ")+strings.Join(s.Info.StartupFiles, ", ")))
		}
		paths := []string{}
		for _, name := range []string{"kilo-codex", "kilo-claude"} {
			if path := s.Info.Commands[name]; path != "" {
				paths = append(paths, path)
			}
		}
		if !s.Info.PathConfigured && len(paths) > 0 {
			children = append(children, u.code("terminal-commands.paths", strings.Join(paths, "\n")))
		}
	}
	return u.card(children...)
}

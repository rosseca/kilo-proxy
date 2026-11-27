package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The API accepts client identities, never commands, environment maps or profile paths.
type clientLaunchRequest struct {
	Client         string `json:"client"`
	Directory      string `json:"directory,omitempty"`
	AppPath        string `json:"appPath,omitempty"`
	Engine         string `json:"engine,omitempty"`
	MuseExecutable string `json:"museExecutable,omitempty"`
	MuseConnection string `json:"museConnection,omitempty"`
}
type clientLaunchPlan struct {
	Client, Name, Kind, Executable, Directory string
	Args                                      []string
	Env                                       map[string]string
	Unset                                     []string
	// Set only by local preparation, never by a controller response or API input.
	skipProxy bool
}
type clientLaunchRuntime struct {
	platform, home string
	resolve        func(string, string) (string, error)
	terminal       func() (bool, string)
	start          func(clientLaunchPlan) error
}
type clientLaunchAvailability struct {
	Installed  bool   `json:"installed"`
	Available  bool   `json:"available"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	Reason     string `json:"reason"`
	InstallURL string `json:"installURL,omitempty"`
}

var launchClients = []string{"codex", "claude-desktop", "codex-cli", "claude", "muse", "opencode", "opencode2", "omp", "open-design", "openmausbot", "t3-code", "synara", "omnigent", "zed", "xcode-chat", "xcode-codex", "xcode-claude"}

func clientLaunchUsesProject(client string) bool {
	return client != "codex" && client != "claude-desktop" && client != "open-design" && client != "openmausbot" && client != "t3-code" && client != "synara" && client != "omnigent"
}

func launchClientIdentity(id string) (string, string) {
	switch id {
	case "codex":
		return "Codex Desktop", "desktop"
	case "codex-cli":
		return "Codex CLI", "terminal"
	case "claude-desktop":
		return "Claude Desktop", "desktop"
	case "claude":
		return "Claude Code", "terminal"
	case "muse":
		return "Muse Code", "terminal"
	case "opencode":
		return "OpenCode", "terminal"
	case "opencode2":
		return "OpenCode 2", "terminal"
	case "omp":
		return "Oh My Pi", "terminal"
	case "open-design":
		return "Open Design", "desktop"
	case "openmausbot":
		return "OpenMausBot", "desktop"
	case "t3-code":
		return "T3 Code · Kilo", "desktop"
	case "synara":
		return "Synara · Kilo", "desktop"
	case "omnigent":
		return "Omnigent · Kilo", "desktop"
	case "zed":
		return "Zed", "desktop"
	case "xcode-chat", "xcode-codex", "xcode-claude":
		return "Xcode", "desktop"
	}
	return "", ""
}

func launchClientPlatformReason(id, platform string) string {
	if platform == "darwin" {
		platform = "macos"
	}
	if strings.HasPrefix(id, "xcode-") && platform != "macos" {
		return "Xcode is available on macOS only."
	}
	if id == "open-design" && platform != "macos" && platform != "windows" {
		return "Open Design desktop launch is available on macOS and Windows. Linux currently requires a source build."
	}
	if id == "openmausbot" && platform != "macos" && platform != "windows" && platform != "linux" {
		return "OpenMausBot desktop is supported on macOS, Windows and Linux."
	}
	if id == "omnigent" && platform != "macos" && platform != "linux" {
		return "Omnigent · Kilo currently requires its Python CLI on macOS or Linux."
	}
	return ""
}
func (a *app) launchRuntime() clientLaunchRuntime {
	home, _ := os.UserHomeDir()
	platform := runtime.GOOS
	if platform == "darwin" {
		platform = "macos"
	}
	rt := clientLaunchRuntime{platform: platform, home: home, resolve: a.resolveManagedTool, terminal: clientTerminalAvailable, start: startClientLaunch}
	if x := a.launcher; x != nil {
		if x.platform != "" {
			rt.platform = x.platform
		}
		if x.home != "" {
			rt.home = x.home
		}
		if x.resolve != nil {
			rt.resolve = x.resolve
		}
		if x.terminal != nil {
			rt.terminal = x.terminal
		}
		if x.start != nil {
			rt.start = x.start
		}
	}
	resolve := rt.resolve
	rt.resolve = func(client, custom string) (string, error) {
		if custom == "" && client == "omnigent" {
			if path := a.cliExecutable(client); path != "" {
				if err := a.validateSelectedClientExecutable(client, path); err != nil {
					return "", err
				}
				return path, nil
			}
		}
		if custom == "" && client == "synara" {
			if path := a.cliExecutable(client); path != "" {
				if err := a.validateSelectedSynaraExecutable(path, rt.platform); err != nil {
					return "", err
				}
				return path, nil
			}
		}
		if custom == "" && (client == "codex-cli" || client == "claude" || client == "muse") {
			if path := a.cliExecutable(client); path != "" {
				if err := a.validateSelectedClientExecutable(client, path); err != nil {
					return "", err
				}
				if client == "muse" {
					native, err := resolveMuseWindowsLauncher(path, rt.platform)
					if err != nil {
						return "", err
					}
					if err := a.validateSelectedClientExecutable(client, native); err != nil {
						return "", err
					}
					return native, nil
				}
				if client == "codex-cli" {
					// Resolve only this npm installation's native dependency. An
					// unrelated Desktop bundle must never replace an explicit CLI.
					native, err := resolveOpenDesignCodex(path, rt.platform, runtime.GOARCH, nil)
					if err != nil {
						return "", err
					}
					if err := a.validateSelectedClientExecutable(client, native); err != nil {
						return "", err
					}
					return native, nil
				}
				return path, nil
			}
		}
		if custom == "" && (client == "opencode" || client == "opencode2") {
			if path := a.openCodeExecutable(client); path != "" {
				if err := a.validateSelectedClientExecutable(client, path); err != nil {
					return "", err
				}
				return path, nil
			}
		}
		return resolve(client, custom)
	}
	return rt
}
func (a *app) clientsLaunch(w http.ResponseWriter, r *http.Request) {
	rt := a.launchRuntime()
	if r.Method == "GET" {
		clients := map[string]clientLaunchAvailability{}
		terminal, reason := rt.terminal()
		for _, id := range launchClients {
			name, kind := launchClientIdentity(id)
			info := clientLaunchAvailability{Name: name, Kind: kind, InstallURL: launchClientInstallURL(id)}
			if reason := launchClientPlatformReason(id, rt.platform); reason != "" {
				info.Reason = reason
			} else if path, err := rt.resolve(id, ""); err != nil {
				info.Reason = err.Error()
			} else {
				info.Path = path
				info.Installed = true
				info.Available = true
				if id == "opencode2" {
					// A fallback command named opencode can still be major 1.
					// Verify only on explicit discovery, not in path resolution or
					// the per-request T3 readiness fingerprint.
					if err := a.checkOpenCodeVersion(path, 2); err != nil {
						info.Installed, info.Available = false, false
						info.Reason = err.Error()
					}
				}
				if id == "open-design" {
					if err := openDesignCompatibility(path, rt.platform); err != nil {
						info.Available = false
						info.Reason = err.Error()
					}
				}
				if id == "openmausbot" {
					if err := openMausBotCompatibility(path, rt.platform); err != nil {
						info.Available = false
						info.Reason = err.Error()
					}
				}
				if id == "t3-code" {
					a.mu.Lock()
					prefs := normalizeT3CodePreferences(a.config.T3Code)
					a.mu.Unlock()
					if err := a.t3CodeAvailability(path, rt, prefs); err != nil {
						info.Available = false
						info.Reason = err.Error()
					}
				}
				if id == "synara" {
					if err := a.synaraAvailability(path, rt); err != nil {
						info.Available = false
						info.Reason = err.Error()
					}
				}
				if id == "omnigent" {
					if _, err := inspectOmnigentInstallation(path, rt.platform); err != nil {
						info.Available = false
						info.Reason = err.Error()
					}
				}
			}
			if kind == "terminal" && info.Available && !terminal {
				info.Available = false
				info.Reason = reason
			}
			info.Reason = a.clientLaunchMessage(info.Reason)
			clients[id] = info
		}
		jsonResponse(w, 200, map[string]any{"platform": rt.platform, "directory": rt.home, "clients": clients})
		return
	}
	if r.Method != "POST" {
		a.clientLaunchError(w, 405, "Method not allowed.")
		return
	}
	var input clientLaunchRequest
	if !decodeBody(w, r, &input) {
		return
	}
	if name, _ := launchClientIdentity(input.Client); name == "" {
		a.clientLaunchError(w, 400, "Unknown launch client.")
		return
	}
	if !a.launchMu.TryLock() {
		a.clientLaunchError(w, 409, "Another launch is already being prepared.")
		return
	}
	defer a.launchMu.Unlock()
	plan, err := a.planClientLaunch(input, rt)
	if err != nil {
		a.clientLaunchError(w, 409, err.Error())
		return
	}
	if !plan.skipProxy {
		err = a.start()
	}
	if err != nil {
		if input.Client == "t3-code" {
			a.closeT3CodeAssets()
		}
		a.writeProxyStartError(w, err)
		return
	}
	if err = rt.start(plan); err != nil {
		if input.Client == "t3-code" {
			a.closeT3CodeAssets()
		}
		// Process errors can contain arguments or environment values. Keep them out of API responses and logs.
		message := "Could not open " + plan.Name + ". Check that the application and a terminal are available, then try again."
		if plan.Kind == "desktop" {
			message = "Could not open " + plan.Name + ". Check that the application is available, then try again."
		}
		a.clientLaunchError(w, 500, message)
		return
	}
	message := plan.Name + " opened."
	if input.Client == "claude-desktop" {
		message = "Claude Desktop opened with its separate Kilo profile."
	}
	if input.Client == "zed" {
		message = "Zed opened. Local credentials and models are ready; existing projects stay open."
	}
	if input.Client == "open-design" {
		message = "Open Design opened with its Kilo CLI profile. Use Local CLI mode in Open Design."
		a.mu.Lock()
		a.openDesignLaunchUntil = time.Now().Add(30 * time.Second)
		a.mu.Unlock()
	}
	if input.Client == "openmausbot" {
		message = "OpenMausBot opened with its private Kilo configuration."
		a.mu.Lock()
		a.openMausBotLaunchUntil = time.Now().Add(30 * time.Second)
		a.mu.Unlock()
	}
	if input.Client == "t3-code" {
		message = "T3 Code opened with your selected providers in its separate Kilo workspace."
		a.mu.Lock()
		a.t3CodeLaunchUntil = time.Now().Add(30 * time.Second)
		a.mu.Unlock()
	}
	if input.Client == "synara" {
		message = "Synara opened with your selected accounts in its separate Kilo workspace."
		a.mu.Lock()
		a.synaraLaunchUntil = time.Now().Add(30 * time.Second)
		a.mu.Unlock()
	}
	if input.Client == "omnigent" {
		message = "Omnigent is starting its private Kilo workspace. Its browser opens when the server and host are ready."
		a.mu.Lock()
		a.omnigentLaunchUntil = time.Now().Add(15 * time.Second)
		a.mu.Unlock()
	}
	if strings.HasPrefix(input.Client, "xcode-") {
		message = "Xcode opened. Existing projects stay open; complete its provider setup if needed."
	}
	jsonResponse(w, 200, map[string]any{"ok": true, "client": input.Client, "name": plan.Name, "kind": plan.Kind, "message": a.clientLaunchMessage(message)})
}
func launchPath(value, home string) (string, error) {
	if value == "" {
		value = home
	}
	if value == "~" {
		value = home
	} else if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		value = filepath.Join(home, value[2:])
	}
	if len(value) > 8192 || strings.ContainsAny(value, "\x00\r\n") || !filepath.IsAbs(value) {
		return "", errors.New("Choose an absolute path to an existing project folder.")
	}
	path := filepath.Clean(value)
	st, err := os.Stat(path)
	if err != nil || !st.IsDir() {
		return "", errors.New("The project folder does not exist or is not a directory.")
	}
	return path, nil
}
func (a *app) planClientLaunch(input clientLaunchRequest, rt clientLaunchRuntime) (clientLaunchPlan, error) {
	name, kind := launchClientIdentity(input.Client)
	p := clientLaunchPlan{Client: input.Client, Name: name, Kind: kind, Env: map[string]string{}}
	if name == "" {
		return p, errors.New("Unknown launch client.")
	}
	if input.Engine != "" && (input.Client != "open-design" || !validOpenDesignEngine(input.Engine)) {
		return p, errors.New("Choose a supported Open Design CLI engine.")
	}
	if input.AppPath != "" && input.Client != "codex" {
		return p, errors.New("A custom application path is supported only for Codex Desktop.")
	}
	if reason := launchClientPlatformReason(input.Client, rt.platform); reason != "" {
		return p, errors.New(reason)
	}
	if input.Client == "muse" {
		if input.MuseExecutable == "" || validateCLIExecutable(input.MuseExecutable) != nil {
			return p, errors.New("Refresh the installed Muse Code executable before opening it.")
		}
		a.mu.Lock()
		prefs := normalizeMusePreferences(a.config.MuseCode)
		a.mu.Unlock()
		if input.MuseConnection != prefs.Connection {
			return p, errors.New("The Muse Code connection changed. Reload its settings before opening it.")
		}
	} else if input.MuseExecutable != "" || input.MuseConnection != "" {
		return p, errors.New("A Muse Code executable confirmation is supported only for Muse Code.")
	}
	var err error
	directory := input.Directory
	if !clientLaunchUsesProject(input.Client) {
		// Let these desktop clients manage their own workspace. Ignore old
		// remembered folders, while retaining a valid internal working directory.
		directory = ""
	}
	p.Directory, err = launchPath(directory, rt.home)
	if err != nil {
		return p, err
	}
	p.Executable, err = rt.resolve(input.Client, input.AppPath)
	if err != nil {
		return p, err
	}
	if input.Client == "claude-desktop" {
		paths, pathsErr := a.claudeDesktopPaths()
		if pathsErr != nil {
			return p, pathsErr
		}
		running, checkErr := a.claudeDesktopProfileRunning(paths)
		if checkErr != nil {
			return p, errors.New("Cannot check whether the Kilo Claude Desktop window is running. Close that window and try again.")
		}
		if running {
			return p, errors.New("Quit the Kilo Claude Desktop window, then open it here again. Your regular Claude session can stay open.")
		}
	}
	if kind == "terminal" {
		if ok, why := rt.terminal(); !ok {
			return p, errors.New(why)
		}
	}
	if input.Client == "muse" {
		if filepath.Clean(input.MuseExecutable) != filepath.Clean(p.Executable) {
			return p, errors.New("The Muse Code executable changed. Refresh installed apps and open it again.")
		}
		if input.MuseConnection == "native" {
			applyMuseNativeLaunch(&p)
			return p, nil
		}
		state := a.modelLibrary.snapshot()
		if state.RecoveryRequired || state.Warning != "" {
			return p, errors.New("Open Models in Kilo Proxy and save a valid model selection first.")
		}
		a.mu.Lock()
		err = a.museTerminalPrepared(p.Executable, state.Library)
		a.mu.Unlock()
		if err != nil {
			return p, err
		}
		applyMuseKiloLaunch(&p, museTerminalHome(a.dir))
		return p, nil
	}
	a.mu.Lock()
	if input.Client == "open-design" {
		err = a.applyOpenDesignProfile(&p, input.Engine, rt)
	} else if input.Client == "openmausbot" {
		err = a.applyOpenMausBotLaunch(&p, rt.platform)
	} else if input.Client == "t3-code" {
		err = a.applyT3CodeLaunch(&p, rt)
		p.skipProxy = !t3CodeUsesLocalProxy(a.config.T3Code)
	} else if input.Client == "synara" {
		err = a.applySynaraLaunch(&p, rt)
		if err == nil {
			prefs, prefsErr := a.synaraPreferencesSnapshotLocked()
			err = prefsErr
			p.skipProxy = !synaraUsesKilo(prefs)
		}
	} else if input.Client == "omnigent" {
		err = a.applyOmnigentLaunch(&p, rt)
	} else {
		err = a.launchProfile(&p, rt.home)
	}
	a.mu.Unlock()
	if err != nil {
		return p, err
	}
	if input.Client == "codex" {
		var ui string
		switch rt.platform {
		case "macos":
			ui = filepath.Join(rt.home, "Library", "Application Support", "Codex Kilo")
		case "windows":
			base := os.Getenv("LOCALAPPDATA")
			if base == "" {
				base = filepath.Join(rt.home, "AppData", "Local")
			}
			ui = filepath.Join(base, "Codex Kilo")
		default:
			base := os.Getenv("XDG_CONFIG_HOME")
			if !filepath.IsAbs(base) {
				base = filepath.Join(rt.home, ".config")
			}
			ui = filepath.Join(base, "codex-kilo-desktop")
		}
		if err = os.MkdirAll(ui, 0700); err != nil || !safeLaunchDir(ui, rt.home) {
			return p, errors.New("Cannot create the isolated Codex window profile.")
		}
		p.Env["CODEX_ELECTRON_USER_DATA_PATH"] = ui
		p.Args = []string{"--user-data-dir=" + ui}
		if rt.platform == "macos" && strings.HasSuffix(p.Executable, ".app") {
			binary := managedToolBundleExecutable(p.Executable)
			if binary == "" {
				return p, errors.New("The Codex application bundle is invalid.")
			}
			p.Executable = binary
		}
	} else if input.Client == "claude-desktop" {
		paths, pathsErr := a.claudeDesktopPaths()
		if pathsErr != nil {
			return p, pathsErr
		}
		p.Args = []string{"--user-data-dir=" + paths.UserDataDir}
		p.Env["CLAUDE_CONFIG_DIR"] = paths.ClaudeConfigDir
		p.Env["CLAUDE_SECURESTORAGE_CONFIG_DIR"] = paths.ClaudeConfigDir
		p.Unset = claudeDesktopInheritedEnvironment(os.Environ())
		if rt.platform == "windows" {
			// The vendor migrates legacy 3P data from APPDATA and places current
			// 3P data under LOCALAPPDATA. Both overrides belong only to this child.
			p.Env["LOCALAPPDATA"] = paths.LocalAppData
			p.Env["APPDATA"] = paths.AppData
		}
		// LaunchServices may reuse the regular Claude process. Start the native
		// executable directly so the separate data directory takes effect.
		if rt.platform == "macos" && strings.HasSuffix(p.Executable, ".app") {
			binary := managedToolBundleExecutable(p.Executable)
			if binary == "" {
				return p, errors.New("The Claude Desktop application bundle is invalid.")
			}
			p.Executable = binary
		}
	} else if input.Client == "open-design" || input.Client == "openmausbot" || input.Client == "t3-code" || input.Client == "synara" || input.Client == "omnigent" {
		// These clients already selected their isolated native executable.
	} else if kind == "desktop" {
		if rt.platform == "macos" && strings.HasSuffix(p.Executable, ".app") {
			p.Args = []string{"-a", p.Executable}
			// Opening an arbitrary home folder in Xcode creates an unwanted workspace chooser.
			if input.Directory != "" && p.Directory != rt.home || !strings.HasPrefix(input.Client, "xcode-") {
				p.Args = append(p.Args, p.Directory)
			}
			p.Executable = "/usr/bin/open"
		} else {
			p.Args = []string{p.Directory}
		}
	}
	return p, nil
}
func profileLaunchError(client string) error {
	return fmt.Errorf("Prepare %s again: its saved profile is missing, unsafe or no longer matches the proxy connection.", client)
}

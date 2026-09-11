package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The API accepts client identities, never commands, environment maps or profile paths.
type clientLaunchRequest struct {
	Client    string `json:"client"`
	Directory string `json:"directory,omitempty"`
	AppPath   string `json:"appPath,omitempty"`
}
type clientLaunchPlan struct {
	Client, Name, Kind, Executable, Directory string
	Args                                      []string
	Env                                       map[string]string
	Unset                                     []string
}
type clientLaunchRuntime struct {
	platform, home string
	resolve        func(string, string) (string, error)
	terminal       func() (bool, string)
	start          func(clientLaunchPlan) error
}
type clientLaunchAvailability struct {
	Available bool   `json:"available"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	Reason    string `json:"reason"`
}

var launchClients = []string{"codex", "codex-cli", "claude", "opencode", "open-design", "zed", "cursor", "xcode-chat", "xcode-codex", "xcode-claude"}

func launchClientIdentity(id string) (string, string) {
	switch id {
	case "codex":
		return "Codex Desktop", "desktop"
	case "codex-cli":
		return "Codex CLI", "terminal"
	case "claude":
		return "Claude Code", "terminal"
	case "opencode":
		return "OpenCode", "terminal"
	case "open-design":
		return "Open Design", "desktop"
	case "zed":
		return "Zed", "desktop"
	case "cursor":
		return "Cursor", "desktop"
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
	return ""
}
func (a *app) launchRuntime() clientLaunchRuntime {
	home, _ := os.UserHomeDir()
	platform := runtime.GOOS
	if platform == "darwin" {
		platform = "macos"
	}
	rt := clientLaunchRuntime{platform: platform, home: home, resolve: resolveLaunchClient, terminal: clientTerminalAvailable, start: startClientLaunch}
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
	return rt
}
func (a *app) clientsLaunch(w http.ResponseWriter, r *http.Request) {
	rt := a.launchRuntime()
	if r.Method == "GET" {
		clients := map[string]clientLaunchAvailability{}
		terminal, reason := rt.terminal()
		for _, id := range launchClients {
			name, kind := launchClientIdentity(id)
			info := clientLaunchAvailability{Name: name, Kind: kind}
			if reason := launchClientPlatformReason(id, rt.platform); reason != "" {
				info.Reason = reason
			} else if path, err := rt.resolve(id, ""); err != nil {
				info.Reason = err.Error()
			} else {
				info.Path = path
				info.Available = true
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
	if err = a.start(); err != nil {
		a.clientLaunchError(w, 409, "Cannot start the saved proxy. Check the Kilo credentials, organization and local port.")
		return
	}
	if err = rt.start(plan); err != nil {
		// Process errors can contain arguments or environment values. Keep them out of API responses and logs.
		a.clientLaunchError(w, 500, "Could not open "+plan.Name+". Check that the application and a terminal are available, then try again.")
		return
	}
	message := plan.Name + " opened."
	if input.Client == "zed" {
		message = "Zed opened. Local credentials and models are ready; existing projects stay open."
	}
	if input.Client == "cursor" {
		message = "Cursor opened. Connect its provider to the existing tunnel if needed."
	}
	if input.Client == "open-design" {
		message = "Open Design opened. Complete its one-time custom provider setup with the Kilo Proxy connection details if needed."
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
	if input.AppPath != "" && input.Client != "codex" {
		return p, errors.New("A custom application path is supported only for Codex Desktop.")
	}
	if reason := launchClientPlatformReason(input.Client, rt.platform); reason != "" {
		return p, errors.New(reason)
	}
	var err error
	directory := input.Directory
	if input.Client == "open-design" {
		// Open Design restores its own workspace; it has no project-folder launch contract.
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
	if kind == "terminal" {
		if ok, why := rt.terminal(); !ok {
			return p, errors.New(why)
		}
	}
	a.mu.Lock()
	err = a.launchProfile(&p, rt.home)
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
		p.Args = []string{"--user-data-dir=" + ui, p.Directory}
		if rt.platform == "macos" && strings.HasSuffix(p.Executable, ".app") {
			binary := plistValue(filepath.Join(p.Executable, "Contents", "Info.plist"), "CFBundleExecutable")
			if binary == "" || filepath.Base(binary) != binary {
				return p, errors.New("The Codex application bundle is invalid.")
			}
			p.Executable = filepath.Join(p.Executable, "Contents", "MacOS", binary)
		}
	} else if input.Client == "open-design" {
		if rt.platform == "macos" && strings.HasSuffix(p.Executable, ".app") {
			p.Args = []string{"-a", p.Executable}
			p.Executable = "/usr/bin/open"
		}
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

//go:build desktop

package main

import (
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
)

func nativeTerminalCommandsTestUI(t *testing.T, platform string) *nativeUI {
	t.Helper()
	if runtime.GOOS == "windows" && (platform == "macos" || platform == "linux") {
		t.Skip("The macOS/Linux command installer requires POSIX file permissions.")
	}
	t.Setenv("ZDOTDIR", "")
	u := nativeTestUI(t)
	u.page = "settings"
	u.owner.launcher = &clientLaunchRuntime{platform: platform, home: u.owner.editorTestRoot}
	u.owner.terminalCommandsShell = "/bin/zsh"
	u.owner.terminalCommandsBinary = filepath.Join(u.owner.editorTestRoot, "Kilo Proxy")
	if platform == "windows" {
		u.owner.terminalCommandsBinary += ".exe"
		u.owner.terminalCommandsProfiles = []string{
			filepath.Join(u.owner.editorTestRoot, "Documents", "WindowsPowerShell", "profile.ps1"),
			filepath.Join(u.owner.editorTestRoot, "Documents", "PowerShell", "profile.ps1"),
		}
	}
	if err := os.WriteFile(u.owner.terminalCommandsBinary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestNativeTerminalCommandsPointerInstallAndUpdate(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {780, 700}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"-"+lang, func(t *testing.T) {
				u := nativeTerminalCommandsTestUI(t, "macos")
				u.language = lang
				u.owner.mu.Lock()
				u.owner.config.Language = lang
				u.owner.mu.Unlock()
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				nativeTestWait(t, u, func() bool { return u.terminalCommandsState().Checked })
				h.frame()
				nativeMenuWheel(h, image.Pt(size.X-100, size.Y-100), 10000)
				h.click(u.tr("Install terminal commands", "Instalar comandos de terminal"), semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.busy["POST"+nativeTerminalCommandsEndpoint] })
				s := u.terminalCommandsState()
				if !s.Info.Installed || !s.Info.PathConfigured || s.Error != "" {
					t.Fatalf("install did not finish: %+v", s)
				}
				if len(s.Info.Commands) != 4 {
					t.Fatalf("installer did not report all four commands: %#v", s.Info.Commands)
				}
				for _, name := range []string{"kilo-codex", "kilo-claude", "kilo-omp", "kilo-opencode"} {
					want := filepath.Join(u.owner.editorTestRoot, ".local", "bin", name)
					if s.Info.Commands[name] != want {
						t.Fatalf("command path escaped test home: %q", s.Info.Commands[name])
					}
					data, err := os.ReadFile(want)
					if err != nil || !strings.Contains(string(data), terminalCommandOwner) {
						t.Fatalf("button did not install %s: %v", name, err)
					}
					for _, secret := range []string{u.owner.apiKey, u.owner.adminToken, u.owner.config.LocalKey} {
						if secret != "" && strings.Contains(string(data), secret) {
							t.Fatal("installed command contains a credential")
						}
					}
				}
				h.frame()
				h.reveal(u.tr("Update terminal commands", "Actualizar comandos de terminal"), semantic.Button)
				h.click(u.tr("Update terminal commands", "Actualizar comandos de terminal"), semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.busy["POST"+nativeTerminalCommandsEndpoint] })
				if !s.Info.Installed || s.Error != "" {
					t.Fatalf("update did not finish: %+v", s)
				}
				h.frame()
				nativeMenuWheel(h, image.Pt(size.X-100, size.Y-100), 10000)
				nativeGridCapture(t, h, "terminal-commands-"+fmtSize(size)+"-"+lang)
			})
		}
	}
}

func TestNativeTerminalCommandsCopyWithAndWithoutPATH(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		for _, configured := range []bool{true, false} {
			name := lang + "-full-path"
			if configured {
				name = lang + "-command-name"
			}
			t.Run(name, func(t *testing.T) {
				u := nativeTerminalCommandsTestUI(t, "macos")
				u.language = lang
				u.owner.mu.Lock()
				u.owner.config.Language = lang
				u.owner.mu.Unlock()
				directory := filepath.Join(u.owner.editorTestRoot, "tools ' and spaces")
				s := u.terminalCommandsState()
				s.Started, s.Checked = true, true
				s.Info = nativeTerminalCommandsInfo{Supported: true, Installed: true, Directory: directory, PathConfigured: configured, Commands: map[string]string{}}
				for _, command := range []string{"kilo-codex", "kilo-claude", "kilo-omp", "kilo-opencode"} {
					s.Info.Commands[command] = filepath.Join(directory, command)
				}
				h := &nativePointerHarness{t: t, u: u, size: image.Pt(780, 700), now: time.Now()}
				h.frame()
				nativeMenuWheel(h, image.Pt(680, 600), 10000)
				for _, command := range []string{"kilo-omp", "kilo-opencode"} {
					label := u.tr("Copy ", "Copiar ") + command
					h.reveal(label, semantic.Button)
					h.click(label, semantic.Button)
					bridge := u.owner.desktop.(*nativeRecordingBridge)
					bridge.mu.Lock()
					copied := bridge.Text
					bridge.mu.Unlock()
					want := command
					if !configured {
						want = helperShellQuote(s.Info.Commands[command])
					}
					if copied != want {
						t.Fatalf("copy returned %q, want runnable command %q", copied, want)
					}
					if u.notice != u.tr("Copied", "Copiado") || u.noticeTone != nativeToneSuccess {
						t.Fatalf("copy feedback is missing or not successful: %q (%v)", u.notice, u.noticeTone)
					}
				}
				nativeGridCapture(t, h, "terminal-commands-copy-"+name)
			})
		}
	}
}

func TestNativeTerminalCommandsPowerShellInstallUpdateAndCopy(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			u := nativeTerminalCommandsTestUI(t, "windows")
			u.language = lang
			u.owner.mu.Lock()
			u.owner.config.Language = lang
			u.owner.mu.Unlock()
			const original = "# Existing PowerShell preferences\r\n$global:UserPreference = 'keep'\r\n"
			for _, profile := range u.owner.terminalCommandsProfiles {
				if err := os.MkdirAll(filepath.Dir(profile), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(profile, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
			}
			h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1000), now: time.Now()}
			h.frame()
			s := u.terminalCommandsState()
			nativeTestWait(t, u, func() bool { return s.Checked && s.ManualChecked })
			if !s.Info.Supported || s.Info.Shell != "powershell" || s.Info.Installed || !reflect.DeepEqual(s.Info.StartupFiles, u.owner.terminalCommandsProfiles) {
				t.Fatalf("PowerShell installation status did not describe the test profiles: %+v", s.Info)
			}
			h.frame()
			label := u.tr("Install PowerShell functions", "Instalar funciones de PowerShell")
			h.reveal(label, semantic.Button)
			h.click(label, semantic.Button)
			nativeTestWait(t, u, func() bool { return !u.busy["POST"+nativeTerminalCommandsEndpoint] })
			if !s.Info.Installed || s.Error != "" || len(s.Info.Commands) != 4 {
				t.Fatalf("PowerShell installation did not finish: %+v", s)
			}
			installed := map[string]string{}
			for _, profile := range u.owner.terminalCommandsProfiles {
				data, err := os.ReadFile(profile)
				if err != nil || !strings.Contains(string(data), original) {
					t.Fatalf("PowerShell installation lost unrelated preferences: %v", err)
				}
				installed[profile] = string(data)
				for _, name := range nativeTerminalCommandNames {
					if !strings.Contains(string(data), "function "+name) {
						t.Fatalf("PowerShell profile omitted %s", name)
					}
				}
			}
			h.frame()
			label = u.tr("Update PowerShell functions", "Actualizar funciones de PowerShell")
			h.reveal(label, semantic.Button)
			h.click(label, semantic.Button)
			nativeTestWait(t, u, func() bool { return !u.busy["POST"+nativeTerminalCommandsEndpoint] })
			if s.Error != "" || !s.Info.Installed {
				t.Fatalf("PowerShell update failed: %+v", s)
			}
			for profile, want := range installed {
				data, err := os.ReadFile(profile)
				if err != nil || string(data) != want {
					t.Fatalf("repeated PowerShell install changed or duplicated its profile block: %v", err)
				}
			}
			if _, err := os.Stat(filepath.Join(u.owner.editorTestRoot, ".local", "bin")); !os.IsNotExist(err) {
				t.Fatal("PowerShell installer created Unix wrappers")
			}
			h.frame()
			for _, name := range nativeTerminalCommandNames {
				label = u.tr("Copy ", "Copiar ") + name
				h.reveal(label, semantic.Button)
				h.click(label, semantic.Button)
				bridge := u.owner.desktop.(*nativeRecordingBridge)
				bridge.mu.Lock()
				copied := bridge.Text
				bridge.mu.Unlock()
				if copied != name {
					t.Fatalf("PowerShell installed shortcut copied %q instead of %q", copied, name)
				}
			}
			nativeMenuWheel(h, image.Pt(1080, 900), 10000)
			for _, node := range h.nodes() {
				if strings.Contains(node.Desc.Label, "~/.local/bin") || strings.Contains(node.Desc.Label, "from PATH") || strings.Contains(node.Desc.Label, "desde PATH") {
					t.Fatalf("PowerShell UI showed Unix or PATH setup advice: %q", node.Desc.Label)
				}
			}
			nativeGridCapture(t, h, "terminal-powershell-installed-"+lang)
		})
	}
}

func TestNativeTerminalCommandsBusyFailureAndRetry(t *testing.T) {
	u := nativeTerminalCommandsTestUI(t, "linux")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var posts atomic.Int32
	handler := u.owner.adminHandler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == nativeTerminalCommandsEndpoint {
			if !secureEqual(r.Header.Get("Authorization"), "Bearer "+u.owner.adminToken) {
				t.Error("terminal command request omitted admin authentication")
				jsonError(w, 401, "missing authentication")
				return
			}
			if r.Method == "POST" && posts.Add(1) == 1 {
				close(entered)
				<-release
				jsonError(w, 409, "Synthetic installation failure.")
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	u.owner.adminHost = strings.TrimPrefix(server.URL, "http://")
	t.Cleanup(func() { unblock(); server.Close() })
	u.requestTerminalCommands("GET")
	nativeTestWait(t, u, func() bool { return u.terminalCommandsState().Checked })
	u.requestTerminalCommands("POST")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("installation never reached the API")
	}
	u.requestTerminalCommands("POST")
	u.requestTerminalCommands("GET")
	if posts.Load() != 1 || !u.busy["POST"+nativeTerminalCommandsEndpoint] || u.busy["GET"+nativeTerminalCommandsEndpoint] {
		t.Fatal("an in-progress installation did not prevent duplicate install/status requests")
	}
	unblock()
	nativeTestWait(t, u, func() bool { return !u.busy["POST"+nativeTerminalCommandsEndpoint] })
	if u.terminalCommandsState().Error != "Synthetic installation failure." || u.terminalCommandsState().Info.Installed {
		t.Fatalf("failure was not shown without claiming installation: %+v", u.terminalCommandsState())
	}
	u.requestTerminalCommands("POST")
	nativeTestWait(t, u, func() bool { return !u.busy["POST"+nativeTerminalCommandsEndpoint] })
	if posts.Load() != 2 || !u.terminalCommandsState().Info.Installed || u.terminalCommandsState().Error != "" {
		t.Fatalf("installation could not retry after failure: %+v", u.terminalCommandsState())
	}
}

func TestNativeTerminalCommandsUnsupportedPlatformHidesInstall(t *testing.T) {
	u := nativeTerminalCommandsTestUI(t, "unsupported")
	u.requestTerminalCommands("GET")
	nativeTestWait(t, u, func() bool { return u.terminalCommandsState().Checked })
	if u.terminalCommandsState().Info.Supported {
		t.Fatal("unsupported platform offered terminal command installation")
	}
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(780, 700), now: time.Now()}
	h.frame()
	nativeMenuWheel(h, image.Pt(680, 600), 10000)
	u.requestTerminalCommands("POST")
	if u.busy["POST"+nativeTerminalCommandsEndpoint] {
		t.Fatal("unsupported platform sent an installation request")
	}
	if _, err := os.Stat(filepath.Join(u.owner.editorTestRoot, ".local", "bin")); !os.IsNotExist(err) {
		t.Fatal("unsupported platform created command files")
	}
	found := false
	for _, node := range h.nodes() {
		if strings.Contains(node.Desc.Label, "kilo-opencode") {
			found = true
		}
	}
	if !found {
		t.Fatal("unsupported-platform guidance omitted kilo-opencode")
	}
}

func TestNativeTerminalCommandsOpenCodeCollisionIsLocalized(t *testing.T) {
	message := "An unrelated kilo-opencode already exists. Move or rename it before installing terminal commands."
	if got := nativeTerminalCommandsMessage(message, "es"); got != "Ya existe un kilo-opencode ajeno a Kilo Proxy. Muévelo o cámbiale el nombre antes de instalar los comandos de terminal." {
		t.Fatalf("OpenCode collision was not localized: %s", got)
	}
	if got := nativeTerminalCommandsMessage(message, "en"); got != message {
		t.Fatalf("English collision changed: %s", got)
	}
}

func nativeTerminalHomeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += "\n" + string(data)
		}
		files[rel] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestNativeTerminalManualAvailableWithoutInstallationOrHomeWrites(t *testing.T) {
	for _, platform := range []string{"macos", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			u := nativeTerminalCommandsTestUI(t, platform)
			for _, name := range []string{".zshrc", ".bashrc"} {
				if err := os.WriteFile(filepath.Join(u.owner.editorTestRoot, name), []byte("# Existing user shell settings\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := nativeTerminalHomeSnapshot(t, u.owner.editorTestRoot)
			u.requestTerminalManual()
			nativeTestWait(t, u, func() bool { return u.terminalCommandsState().ManualChecked })
			s := u.terminalCommandsState()
			wantShell, argumentForwarding := "zsh-bash", `"$@"`
			if platform == "windows" {
				wantShell, argumentForwarding = "powershell", "--encoded-args"
			}
			if !s.Manual.Supported || s.Manual.Shell != wantShell || s.ManualError != "" || s.Info.Installed {
				t.Fatalf("manual setup should be available before installing: %+v", s)
			}
			if len(s.Manual.Commands) != 4 || s.Manual.All == "" {
				t.Fatalf("manual setup omitted functions: %+v", s.Manual)
			}
			for _, name := range []string{"kilo-codex", "kilo-claude", "kilo-omp", "kilo-opencode"} {
				function := s.Manual.Commands[name]
				declaresFunction := strings.Contains(function, name+"()") || strings.Contains(function, "function "+name+" {")
				if !declaresFunction || !strings.Contains(function, "--terminal-agent") || !strings.Contains(function, argumentForwarding) || !strings.Contains(s.Manual.All, function) {
					t.Fatalf("manual setup omitted a complete forwarding function for %s: %q", name, function)
				}
			}
			for _, secret := range []string{u.owner.apiKey, u.owner.adminToken, u.owner.config.LocalKey} {
				if secret != "" && strings.Contains(s.Manual.All, secret) {
					t.Fatal("manual setup contains a credential")
				}
			}
			if after := nativeTerminalHomeSnapshot(t, u.owner.editorTestRoot); !reflect.DeepEqual(before, after) {
				t.Fatal("reading manual setup changed files or permissions in the user's home")
			}
			u.owner.mu.Lock()
			running := u.owner.proxyServer != nil
			u.owner.mu.Unlock()
			if running {
				t.Fatal("reading manual setup started the proxy")
			}
		})
	}
}

func TestNativeTerminalManualPointerClipboardAndPreview(t *testing.T) {
	for _, platform := range []string{"macos", "windows"} {
		for _, size := range []image.Point{{1180, 1000}, {780, 1000}} {
			for _, lang := range []string{"en", "es"} {
				t.Run(platform+"-"+fmtSize(size)+"-"+lang, func(t *testing.T) {
					u := nativeTerminalCommandsTestUI(t, platform)
					u.language = lang
					u.owner.mu.Lock()
					u.owner.config.Language = lang
					u.owner.mu.Unlock()
					before := nativeTerminalHomeSnapshot(t, u.owner.editorTestRoot)
					h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
					h.frame()
					nativeTestWait(t, u, func() bool { return u.terminalCommandsState().ManualChecked && u.terminalCommandsState().Checked })
					h.frame()
					toggle := u.tr("Manual setup · Zsh / Bash", "Configuración manual · Zsh / Bash")
					if platform == "windows" {
						toggle = u.tr("Manual setup · PowerShell", "Configuración manual · PowerShell")
					}
					h.reveal(toggle, semantic.Button)
					h.click(toggle, semantic.Button)
					if !u.expanded["terminal-commands.manual.toggle"] {
						t.Fatal("pointer did not expand manual setup")
					}
					s := u.terminalCommandsState()
					preview := u.editor("terminal-commands.manual.preview")
					if s.ManualSelected != "" || preview.Text() != s.Manual.All || !preview.ReadOnly || preview.SingleLine {
						t.Fatal("manual setup did not start with a selectable, read-only multiline preview of all functions")
					}
					copyAndCheck := func(label, want string) {
						t.Helper()
						h.reveal(label, semantic.Button)
						h.click(label, semantic.Button)
						bridge := u.owner.desktop.(*nativeRecordingBridge)
						bridge.mu.Lock()
						copied := bridge.Text
						bridge.mu.Unlock()
						if copied != want {
							t.Fatalf("%q copied %q, want complete function %q", label, copied, want)
						}
						if u.notice != u.tr("Copied", "Copiado") || u.noticeTone != nativeToneSuccess {
							t.Fatalf("manual copy omitted localized success feedback: %q", u.notice)
						}
					}
					copyAndCheck(u.tr("Copy all", "Copiar todo"), s.Manual.All)
					for _, name := range []string{"kilo-codex", "kilo-claude", "kilo-omp", "kilo-opencode"} {
						h.reveal(name, semantic.Button)
						h.click(name, semantic.Button)
						if s.ManualSelected != name || preview.Text() != s.Manual.Commands[name] {
							t.Fatalf("selecting %s did not update the manual preview", name)
						}
						copyAndCheck(u.tr("Copy "+name+" function", "Copiar función "+name), s.Manual.Commands[name])
					}
					nativeMenuWheel(h, image.Pt(size.X-100, size.Y-100), 10000)
					if platform == "windows" {
						u.list("terminal-commands.manual.scroll").ScrollTo(0)
						h.frame()
					}
					nativeGridCapture(t, h, "terminal-manual-selected-"+platform+"-"+fmtSize(size)+"-"+lang)
					all := u.tr("All functions", "Todas las funciones")
					h.reveal(all, semantic.Button)
					h.click(all, semantic.Button)
					if s.ManualSelected != "" || preview.Text() != s.Manual.All {
						t.Fatal("all-functions selector did not restore the combined preview")
					}
					nativeMenuWheel(h, image.Pt(size.X-100, size.Y-100), 10000)
					if platform == "windows" {
						u.list("terminal-commands.manual.scroll").ScrollTo(0)
						h.frame()
					}
					nativeGridCapture(t, h, "terminal-manual-all-"+platform+"-"+fmtSize(size)+"-"+lang)
					if s.Info.Installed || !reflect.DeepEqual(before, nativeTerminalHomeSnapshot(t, u.owner.editorTestRoot)) {
						t.Fatal("viewing or copying manual functions installed commands or changed home files")
					}
				})
			}
		}
	}
}

func TestNativeTerminalManualSurvivesInstallerConflict(t *testing.T) {
	u := nativeTerminalCommandsTestUI(t, "linux")
	path := filepath.Join(u.owner.editorTestRoot, ".local", "bin", "kilo-opencode")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf user-owned\n"), 0700); err != nil {
		t.Fatal(err)
	}
	before := nativeTerminalHomeSnapshot(t, u.owner.editorTestRoot)
	u.requestTerminalCommands("GET")
	u.requestTerminalManual()
	nativeTestWait(t, u, func() bool {
		return !u.busy["GET"+nativeTerminalCommandsEndpoint] && u.terminalCommandsState().ManualChecked
	})
	s := u.terminalCommandsState()
	if !strings.Contains(s.Error, "unrelated kilo-opencode") || !s.Manual.Supported || s.ManualError != "" || s.Manual.Commands["kilo-opencode"] == "" {
		t.Fatalf("installer conflict blocked independent manual setup: %+v", s)
	}
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(780, 700), now: time.Now()}
	h.frame()
	h.reveal("Manual setup · Zsh / Bash", semantic.Button)
	h.click("Manual setup · Zsh / Bash", semantic.Button)
	h.reveal("Copy all", semantic.Button)
	h.click("Copy all", semantic.Button)
	bridge := u.owner.desktop.(*nativeRecordingBridge)
	bridge.mu.Lock()
	copied := bridge.Text
	bridge.mu.Unlock()
	if copied != s.Manual.All || !reflect.DeepEqual(before, nativeTerminalHomeSnapshot(t, u.owner.editorTestRoot)) {
		t.Fatal("manual copy failed or changed the pre-existing installation conflict")
	}
}

func TestNativeTerminalManualBusyFailureAndPointerRetry(t *testing.T) {
	u := nativeTerminalCommandsTestUI(t, "linux")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var gets atomic.Int32
	handler := u.owner.adminHandler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/terminal/manual" {
			if r.Method != "GET" || !secureEqual(r.Header.Get("Authorization"), "Bearer "+u.owner.adminToken) {
				t.Error("manual setup request changed method or omitted admin authentication")
				jsonError(w, 401, "missing authentication")
				return
			}
			if gets.Add(1) == 1 {
				close(entered)
				<-release
				jsonError(w, 503, "Synthetic manual setup failure.")
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	u.owner.adminHost = strings.TrimPrefix(server.URL, "http://")
	t.Cleanup(func() { unblock(); server.Close() })
	u.requestTerminalManual()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("manual setup never reached the API")
	}
	u.requestTerminalManual()
	u.requestTerminalCommands("GET")
	nativeTestWait(t, u, func() bool { return u.terminalCommandsState().Checked })
	if gets.Load() != 1 || !u.busy["GET/api/terminal/manual"] {
		t.Fatal("manual busy guard allowed duplicate requests or blocked independent installation status")
	}
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(780, 700), now: time.Now()}
	h.frame()
	h.reveal("Manual setup · Zsh / Bash", semantic.Button)
	h.click("Manual setup · Zsh / Bash", semantic.Button)
	nativeMenuWheel(h, image.Pt(680, 600), 10000)
	var copyPoint image.Point
	for _, node := range h.nodes() {
		if node.Desc.Label == "Copy all" && node.Desc.Bounds.Min.In(image.Rectangle{Max: h.size}) {
			copyPoint = node.Desc.Bounds.Min.Add(image.Pt(8, 8))
			break
		}
	}
	if copyPoint == (image.Point{}) {
		t.Fatal("manual copy control is not visible during loading")
	}
	bridge := u.owner.desktop.(*nativeRecordingBridge)
	bridge.mu.Lock()
	bridge.Text = "Keep the previous clipboard"
	bridge.mu.Unlock()
	position := f32.Pt(float32(copyPoint.X), float32(copyPoint.Y))
	for _, kind := range []pointer.Kind{pointer.Move, pointer.Press, pointer.Release} {
		h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: position})
		h.frame()
	}
	h.frame()
	bridge.mu.Lock()
	gotClipboard := bridge.Text
	bridge.mu.Unlock()
	if gotClipboard != "Keep the previous clipboard" {
		t.Fatal("clicking manual copy during loading replaced the clipboard")
	}
	unblock()
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/terminal/manual"] })
	s := u.terminalCommandsState()
	if s.ManualError != "Synthetic manual setup failure." || s.ManualChecked || s.Error != "" {
		t.Fatalf("manual failure was not independent of installation status: %+v", s)
	}
	h.frame()
	h.reveal("Refresh manual setup", semantic.Button)
	h.click("Refresh manual setup", semantic.Button)
	nativeTestWait(t, u, func() bool { return s.ManualChecked && !u.busy["GET/api/terminal/manual"] })
	if gets.Load() != 2 || !s.Manual.Supported || s.ManualError != "" || s.Info.Installed {
		t.Fatalf("manual setup did not recover via its own refresh button: %+v", s)
	}
}

func TestNativeTerminalManualHiddenOnUnsupportedPlatform(t *testing.T) {
	u := nativeTerminalCommandsTestUI(t, "unsupported")
	u.requestTerminalCommands("GET")
	u.requestTerminalManual()
	nativeTestWait(t, u, func() bool { return u.terminalCommandsState().Checked && u.terminalCommandsState().ManualChecked })
	if u.terminalCommandsState().Manual.Supported {
		t.Fatal("unsupported platform advertised manual functions")
	}
	h := &nativePointerHarness{t: t, u: u, size: image.Pt(780, 700), now: time.Now()}
	h.frame()
	nativeMenuWheel(h, image.Pt(680, 600), 10000)
	for _, node := range h.nodes() {
		if node.Desc.Label == "Manual setup · Zsh / Bash" || node.Desc.Label == "Copy all" || strings.HasPrefix(node.Desc.Label, "Copy kilo-") {
			t.Fatalf("unsupported platform rendered a manual control: %q", node.Desc.Label)
		}
	}
}

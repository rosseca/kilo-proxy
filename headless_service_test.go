package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type headlessServiceTestManager struct {
	calls           [][]string
	loaded, running bool
	fail            string
}

func (m *headlessServiceTestManager) run(name string, args ...string) ([]byte, error) {
	m.calls = append(m.calls, append([]string{name}, args...))
	if m.fail != "" && strings.Contains(strings.Join(args, " "), m.fail) {
		return nil, errors.New("synthetic manager error")
	}
	if name == "systemctl" {
		switch args[1] {
		case "start":
			m.loaded, m.running = true, true
		case "stop", "disable":
			m.running = false
		case "show":
			if m.running {
				return []byte("LoadState=loaded\nActiveState=active\nSubState=running\n"), nil
			}
			return []byte("LoadState=loaded\nActiveState=inactive\nSubState=dead\n"), nil
		}
	} else {
		switch args[0] {
		case "print":
			if !m.loaded {
				return nil, errors.New("synthetic not loaded")
			}
			if m.running {
				return []byte("\tstate = running\n\tpid = 123\n"), nil
			}
		case "bootstrap", "kickstart":
			m.loaded, m.running = true, true
		case "bootout":
			m.loaded, m.running = false, false
		}
	}
	return nil, nil
}

func headlessServiceFixture(t *testing.T, platform string) (string, headlessServiceRuntime, *headlessServiceTestManager) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("User service file ownership is a Unix contract")
	}
	home := t.TempDir()
	profile := filepath.Join(home, "headless profile ' % ${USER} $ literal")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(home, "kilo-proxy-headless ' % ${USER} $ literal")
	if err := os.WriteFile(binary, []byte("synthetic executable"), 0700); err != nil {
		t.Fatal(err)
	}
	manager := &headlessServiceTestManager{}
	rt := headlessServiceRuntime{platform: platform, home: home, configHome: filepath.Join(home, ".config"), binary: binary,
		path: "/synthetic/${HOME}/bin:/usr/bin:/bin", shell: "/bin/zsh", uid: os.Getuid(), run: manager.run}
	return profile, rt, manager
}

func headlessServiceTestCommand(t *testing.T, dir, command string, rt headlessServiceRuntime) (int, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := runHeadlessServiceCommand(dir, []string{command}, rt, &out, &stderr)
	return code, out.String() + stderr.String()
}

func TestHeadlessServiceLifecycleOwnsOnlyItsProfileAndNeverStartsOnInstall(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			dir, rt, manager := headlessServiceFixture(t, platform)
			settings := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(settings, []byte("preserve configuration"), 0600); err != nil {
				t.Fatal(err)
			}
			if code, output := headlessServiceTestCommand(t, dir, "install", rt); code != 0 {
				t.Fatal(output)
			}
			if manager.running || manager.loaded {
				t.Fatal("Installation started the service")
			}
			if platform == "linux" {
				want := [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", headlessServiceLabel(dir) + ".service"}}
				if !reflect.DeepEqual(manager.calls, want) {
					t.Fatalf("Unexpected install commands: %v", manager.calls)
				}
			} else if len(manager.calls) != 0 {
				t.Fatal("macOS installation changed a launchd domain")
			}
			path, domain, _ := headlessServicePath(dir, rt)
			content, err := headlessServiceReadPrivate(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(content, []byte("serve")) || bytes.Contains(content, []byte("--key")) || bytes.Contains(content, []byte("API_KEY")) || bytes.Contains(content, []byte("ANTHROPIC")) {
				t.Fatal("Unsafe service arguments or credential environment")
			}
			if !bytes.Contains(content, []byte("SHELL")) || !bytes.Contains(content, []byte(rt.shell)) {
				t.Fatal("Service did not preserve the invoking shell for terminal command installation")
			}
			if bytes.Contains(content, []byte("ZDOTDIR")) {
				t.Fatal("Service exported an empty ZDOTDIR instead of preserving normal shell startup")
			}
			if platform == "linux" && !bytes.Contains(content, []byte("%%")) {
				t.Fatal("systemd percent specifier was not escaped")
			}
			if platform == "linux" && (!bytes.Contains(content, []byte(`ExecStart=":/`)) || !bytes.Contains(content, []byte(`%% ${USER} $ literal" --config-dir`)) || !bytes.Contains(content, []byte(`Environment="PATH=/synthetic/${HOME}/bin:/usr/bin:/bin"`)) || bytes.Contains(content, []byte("$${"))) {
				t.Fatal("systemd command expansion changed literal executable/profile paths or Environment values")
			}
			if platform == "linux" && !bytes.Contains(content, []byte("TimeoutStopSec=75")) || platform == "darwin" && !bytes.Contains(content, []byte("<key>ExitTimeOut</key><integer>75</integer>")) {
				t.Fatal("Service stop timeout does not leave time for request drain and image upload cleanup")
			}
			if platform == "darwin" && (!strings.HasPrefix(domain, "user/") || strings.Contains(path, "LaunchAgents") || !bytes.Contains(content, []byte("SuccessfulExit"))) {
				t.Fatal("launchd service did not remain in its private user domain")
			}
			for _, command := range []string{"start", "start", "status", "stop", "uninstall"} {
				if code, output := headlessServiceTestCommand(t, dir, command, rt); code != 0 {
					t.Fatalf("%s failed: %s", command, output)
				}
			}
			for _, call := range manager.calls {
				if strings.Contains(strings.Join(call, " "), "sudo") || strings.Contains(strings.Join(call, " "), " -k ") || strings.Contains(strings.Join(call, " "), "linger") {
					t.Fatal("Service changed privilege/login policy or killed an existing instance")
				}
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("Uninstall left the owned service definition")
			}
			data, _ := os.ReadFile(settings)
			if string(data) != "preserve configuration" {
				t.Fatal("Uninstall changed application configuration")
			}
		})
	}
}

func TestHeadlessServiceRejectsUnrelatedModifiedPublicAndSymlinkFiles(t *testing.T) {
	for _, mode := range []string{"unrelated", "modified", "public", "symlink", "manifest", "parent-link"} {
		t.Run(mode, func(t *testing.T) {
			dir, rt, manager := headlessServiceFixture(t, "linux")
			path, _, _ := headlessServicePath(dir, rt)
			if mode == "unrelated" {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("someone else's service"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if mode == "parent-link" {
				if err := os.Mkdir(rt.configHome, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(rt.configHome, "systemd")); err != nil {
					t.Fatal(err)
				}
			} else {
				if code, output := headlessServiceTestCommand(t, dir, "install", rt); code != 0 {
					t.Fatal(output)
				}
				switch mode {
				case "modified":
					_ = os.WriteFile(path, []byte("modified service"), 0600)
				case "public":
					_ = os.Chmod(path, 0644)
				case "symlink":
					_ = os.Remove(path)
					_ = os.Symlink(filepath.Join(dir, "settings.json"), path)
				case "manifest":
					_ = os.WriteFile(filepath.Join(dir, headlessServiceManifestFile), []byte(`{"version":1,"profile":"other"}`), 0600)
				}
			}
			manager.calls = nil
			command := "start"
			if mode == "unrelated" || mode == "parent-link" {
				command = "install"
			}
			if code, _ := headlessServiceTestCommand(t, dir, command, rt); code == 0 || len(manager.calls) != 0 {
				t.Fatal("Unowned or unsafe service was accepted or contacted its manager")
			}
		})
	}
}

func TestHeadlessServiceInstallRefusesLiveProfileAndManagerFailureIsActionable(t *testing.T) {
	dir, rt, manager := headlessServiceFixture(t, "linux")
	release, err := acquireProfileLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := headlessServiceTestCommand(t, dir, "install", rt); code == 0 || len(manager.calls) != 0 {
		t.Fatal("Install changed a profile owned by another process")
	}
	release()
	manager.fail = "daemon-reload"
	if code, output := headlessServiceTestCommand(t, dir, "install", rt); code == 0 || !strings.Contains(output, "no sudo") {
		t.Fatal("Manager failure was hidden or unactionable")
	}
	if _, _, err := headlessReadOwnedService(dir, rt, false); err != nil {
		t.Fatal("Retryable manager failure left an unowned service")
	}
}

func TestHeadlessServiceStatusAndIdentityAreProfileSpecific(t *testing.T) {
	dir, rt, manager := headlessServiceFixture(t, "darwin")
	if code, output := headlessServiceTestCommand(t, dir, "status", rt); code != 0 || !strings.Contains(output, `"installed":false`) {
		t.Fatal("Missing service status failed")
	}
	if code, output := headlessServiceTestCommand(t, dir, "install", rt); code != 0 {
		t.Fatal(output)
	}
	manager.loaded, manager.running = true, true
	code, output := headlessServiceTestCommand(t, dir, "status", rt)
	var state map[string]any
	if code != 0 || json.Unmarshal([]byte(output), &state) != nil || state["running"] != true || state["loaded"] != true {
		t.Fatal("Loaded service status was not reported")
	}
	if headlessServiceLabel(dir) == headlessServiceLabel(dir+"-other") {
		t.Fatal("Different profiles share a service identity")
	}
	badRuntime := rt
	badRuntime.binary += "\n-injected"
	if _, err := headlessServiceContent(dir, badRuntime); err == nil {
		t.Fatal("Service arguments allowed newline injection")
	}
	badRuntime = rt
	badRuntime.shell = "/bin/zsh\n-injected"
	if _, err := headlessServiceContent(dir, badRuntime); err == nil {
		t.Fatal("Service environment allowed shell newline injection")
	}
	for _, suffix := range []string{"\t", "\x01", "\xff"} {
		badRuntime = rt
		badRuntime.binary += suffix
		if _, err := headlessServiceContent(dir, badRuntime); err == nil {
			t.Fatal("Service accepted an executable path that cannot be serialized exactly")
		}
	}
	badRuntime = rt
	badRuntime.zdotDir = "relative/zsh"
	if _, err := headlessServiceContent(dir, badRuntime); err == nil {
		t.Fatal("Service accepted a relative shell startup directory")
	}
	if _, err := headlessServiceContent(dir, headlessServiceRuntime{platform: "windows", binary: rt.binary, home: rt.home}); err == nil {
		t.Fatal("Unsupported Windows service was accepted")
	}
}

func TestHeadlessServicePreservesCustomZshAndFishStartupDirectories(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		for _, shell := range []string{"zsh", "fish"} {
			t.Run(platform+"/"+shell, func(t *testing.T) {
				dir, rt, _ := headlessServiceFixture(t, platform)
				rt.shell = "/bin/" + shell
				rt.configHome = filepath.Join(rt.home, "custom fish ${HOME}")
				rt.zdotDir = filepath.Join(rt.home, "custom zsh ${HOME}")
				t.Setenv("XDG_CONFIG_HOME", rt.configHome)
				t.Setenv("ZDOTDIR", rt.zdotDir)
				_, startup, err := terminalShellFiles(rt.home, rt.shell)
				if err != nil {
					t.Fatal(err)
				}
				want := []string{filepath.Join(rt.configHome, "fish", "conf.d", "kilo-proxy.fish")}
				if shell == "zsh" {
					want = []string{filepath.Join(rt.zdotDir, ".zshrc")}
				}
				if !reflect.DeepEqual(startup, want) {
					t.Fatalf("Terminal installer ignored the custom startup directory: %v", startup)
				}
				if code, output := headlessServiceTestCommand(t, dir, "install", rt); code != 0 {
					t.Fatal(output)
				}
				saved, content, err := headlessReadOwnedService(dir, rt, false)
				if err != nil || saved.ConfigHome != rt.configHome || saved.ZdotDir != rt.zdotDir || saved.Shell != rt.shell {
					t.Fatal("Service ownership record did not preserve shell startup context")
				}
				if platform == "linux" {
					if !bytes.Contains(content, []byte(`Environment="XDG_CONFIG_HOME=`+rt.configHome+`"`)) || !bytes.Contains(content, []byte(`Environment="ZDOTDIR=`+rt.zdotDir+`"`)) {
						t.Fatal("User unit omitted or expanded shell startup directories")
					}
				} else if !bytes.Contains(content, []byte("<key>XDG_CONFIG_HOME</key><string>"+rt.configHome+"</string>")) || !bytes.Contains(content, []byte("<key>ZDOTDIR</key><string>"+rt.zdotDir+"</string>")) {
					t.Fatal("Launchd definition omitted shell startup directories")
				}
			})
		}
	}
}

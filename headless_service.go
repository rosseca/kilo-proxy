package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const headlessServiceManifestFile = "headless-service.json"

type headlessServiceRuntime struct {
	platform, home, configHome, binary, path, shell, zdotDir string
	uid                                                      int
	run                                                      func(string, ...string) ([]byte, error)
	lockWait                                                 time.Duration
}

type headlessServiceManifest struct {
	Version     int    `json:"version"`
	Platform    string `json:"platform"`
	Profile     string `json:"profile"`
	Label       string `json:"label"`
	Domain      string `json:"domain,omitempty"`
	ServiceFile string `json:"serviceFile"`
	Binary      string `json:"binary"`
	Home        string `json:"home"`
	Path        string `json:"path"`
	Shell       string `json:"shell,omitempty"`
	ConfigHome  string `json:"configHome"`
	ZdotDir     string `json:"zdotDir,omitempty"`
	SHA256      string `json:"sha256"`
}

func runHeadlessServiceCLI(dir string, args []string, stdout, stderr io.Writer) int {
	rt, err := newHeadlessServiceRuntime()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return runHeadlessServiceCommand(dir, args, rt, stdout, stderr)
}

func newHeadlessServiceRuntime() (headlessServiceRuntime, error) {
	rt := headlessServiceRuntime{platform: runtime.GOOS, uid: os.Getuid(), path: os.Getenv("PATH"), shell: os.Getenv("SHELL"), zdotDir: os.Getenv("ZDOTDIR"), lockWait: 75 * time.Second}
	if rt.platform != "darwin" && rt.platform != "linux" {
		return rt, errors.New("User services are supported on macOS and Linux.")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return rt, errors.New("Cannot locate the user service directory.")
	}
	rt.home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return rt, errors.New("Cannot resolve the user service directory.")
	}
	rt.configHome = os.Getenv("XDG_CONFIG_HOME")
	if rt.configHome == "" {
		rt.configHome = filepath.Join(rt.home, ".config")
	}
	rt.binary, err = os.Executable()
	if err == nil {
		rt.binary, err = filepath.EvalSymlinks(rt.binary)
	}
	if err != nil {
		return rt, errors.New("Cannot locate the headless executable.")
	}
	rt.run = func(name string, args ...string) ([]byte, error) {
		// User service managers may wait for the daemon's complete request and
		// upload cleanup when stopping it. Outlast the generated 75-second unit
		// timeout without lengthening ordinary manager discovery/start calls.
		budget := 20 * time.Second
		for _, arg := range args {
			if arg == "stop" || arg == "disable" || arg == "bootout" {
				budget = 90 * time.Second
				break
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
	return rt, nil
}

func headlessServiceLabel(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return "ai.kilo.headless." + hex.EncodeToString(sum[:8])
}

func headlessServicePath(dir string, rt headlessServiceRuntime) (string, string, error) {
	label := headlessServiceLabel(dir)
	switch rt.platform {
	case "linux":
		if !headlessServiceAbsolute(rt.configHome) {
			return "", "", errors.New("XDG_CONFIG_HOME must be an absolute service configuration path.")
		}
		return filepath.Join(rt.configHome, "systemd", "user", label+".service"), "", nil
	case "darwin":
		if rt.uid < 0 {
			return "", "", errors.New("Cannot identify the current launchd user.")
		}
		// This explicitly belongs to the user domain, including SSH sessions. It
		// is not placed in LaunchAgents, which may load in a different GUI domain.
		return filepath.Join(dir, "service", label+".plist"), "user/" + strconv.Itoa(rt.uid), nil
	default:
		return "", "", errors.New("User services are supported on macOS and Linux.")
	}
}

func headlessServiceAbsolute(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 4096 && utf8.ValidString(path) && strings.IndexFunc(path, unicode.IsControl) == -1
}

func headlessSystemdQuote(value string) string {
	value = strings.ReplaceAll(value, "%", "%%")
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}

func headlessXMLString(value string) string {
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(value))
	return "<string>" + escaped.String() + "</string>"
}

func headlessServiceContent(dir string, rt headlessServiceRuntime) ([]byte, error) {
	if !headlessServiceAbsolute(dir) || !headlessServiceAbsolute(rt.binary) || !headlessServiceAbsolute(rt.home) || !headlessServiceAbsolute(rt.configHome) || len(rt.path) > 8192 || !utf8.ValidString(rt.path) || strings.IndexFunc(rt.path, unicode.IsControl) >= 0 || (rt.shell != "" && !headlessServiceAbsolute(rt.shell)) || (rt.zdotDir != "" && !headlessServiceAbsolute(rt.zdotDir)) {
		return nil, errors.New("Service executable, profile and home must be valid absolute paths.")
	}
	label := headlessServiceLabel(dir)
	var shellSystemd, shellPlist string
	for _, pair := range [][2]string{{"SHELL", rt.shell}, {"ZDOTDIR", rt.zdotDir}} {
		// In particular, an empty exported ZDOTDIR changes Zsh startup-file
		// behavior compared with an absent variable. Preserve its absence.
		if pair[1] != "" {
			shellSystemd += "Environment=" + headlessSystemdQuote(pair[0]+"="+pair[1]) + "\n"
			shellPlist += "<key>" + pair[0] + "</key>" + headlessXMLString(pair[1])
		}
	}
	switch rt.platform {
	case "linux":
		content := "# Managed by Kilo Proxy headless for " + label + "\n" +
			"[Unit]\nDescription=Kilo Proxy headless\nAfter=network.target\n\n" +
			// ':' disables systemd's environment expansion in command arguments.
			// '%' specifiers still need escaping, including in Environment values.
			"[Service]\nType=simple\nExecStart=" + headlessSystemdQuote(":"+rt.binary) +
			" --config-dir " + headlessSystemdQuote(dir) + " serve\n" +
			"Environment=" + headlessSystemdQuote("HOME="+rt.home) + "\n" +
			"Environment=" + headlessSystemdQuote("PATH="+rt.path) + "\n" +
			"Environment=" + headlessSystemdQuote("XDG_CONFIG_HOME="+rt.configHome) + "\n" +
			shellSystemd +
			"Restart=on-failure\nRestartSec=5\nTimeoutStopSec=75\nUMask=0077\n" +
			"StandardOutput=journal\nStandardError=journal\n\n[Install]\nWantedBy=default.target\n"
		return []byte(content), nil
	case "darwin":
		content := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
			"<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n" +
			"<plist version=\"1.0\"><dict>\n<key>Label</key>" + headlessXMLString(label) +
			"\n<key>ProgramArguments</key><array>" + headlessXMLString(rt.binary) +
			headlessXMLString("--config-dir") + headlessXMLString(dir) + headlessXMLString("serve") + "</array>\n" +
			"<key>EnvironmentVariables</key><dict><key>HOME</key>" + headlessXMLString(rt.home) +
			"<key>PATH</key>" + headlessXMLString(rt.path) + "<key>XDG_CONFIG_HOME</key>" + headlessXMLString(rt.configHome) + shellPlist + "</dict>\n" +
			"<key>RunAtLoad</key><true/>\n<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>\n" +
			"<key>ThrottleInterval</key><integer>5</integer>\n<key>ExitTimeOut</key><integer>75</integer>\n" +
			"<key>Umask</key><integer>63</integer>\n<key>StandardOutPath</key>" + headlessXMLString(filepath.Join(dir, "service.stdout.log")) +
			"\n<key>StandardErrorPath</key>" + headlessXMLString(filepath.Join(dir, "service.stderr.log")) + "\n</dict></plist>\n"
		return []byte(content), nil
	default:
		return nil, errors.New("User services are supported on macOS and Linux.")
	}
}

// Reject links in the managed path before creating any directory or loading a
// service. The home/profile passed here has already been resolved privately.
func headlessServiceParent(path, base string, create bool) error {
	parent := filepath.Dir(path)
	rel, err := filepath.Rel(base, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("The managed service directory is outside its user configuration root.")
	}
	current := base
	for _, part := range append([]string{""}, strings.Split(rel, string(filepath.Separator))...) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return errors.New("Service directories must not contain symbolic links.")
		}
		if err == nil && (info.Mode().Perm()&0022 != 0 || !headlessFileOwned(info)) {
			return errors.New("User service directories must be owned by you and not writable by others.")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("Cannot inspect the user service directory.")
		}
	}
	if create {
		if err := os.MkdirAll(parent, 0700); err != nil {
			return errors.New("Cannot create the user service directory.")
		}
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 || !headlessFileOwned(info) {
		return errors.New("The user service directory must be owned by you and not writable by others.")
	}
	return nil
}

func headlessServiceReadPrivate(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !headlessFileOwned(info) || info.Size() > 64<<10 {
		return nil, errors.New("Service files must be private regular files owned by you, without symbolic links.")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("Cannot safely read the private user service file.")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm()&0077 != 0 || !headlessFileOwned(opened) {
		return nil, errors.New("The private user service file changed while opening it.")
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return nil, errors.New("Cannot safely read the private user service file.")
	}
	return data, nil
}

func headlessReadOwnedService(dir string, rt headlessServiceRuntime, allowMissing bool) (headlessServiceManifest, []byte, error) {
	var saved headlessServiceManifest
	data, err := headlessServiceReadPrivate(filepath.Join(dir, headlessServiceManifestFile))
	if err != nil {
		return saved, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&saved) != nil || decoder.Decode(new(any)) != io.EOF {
		return saved, nil, errors.New("Invalid Kilo Proxy service ownership record; nothing changed.")
	}
	path, domain, err := headlessServicePath(dir, rt)
	if err != nil || saved.Version != 1 || saved.Platform != rt.platform || saved.Profile != dir || saved.Label != headlessServiceLabel(dir) || saved.ServiceFile != path || saved.Domain != domain {
		return saved, nil, errors.New("This service belongs to another profile or platform; nothing changed.")
	}
	oldRuntime := rt
	oldRuntime.binary, oldRuntime.home, oldRuntime.path, oldRuntime.shell = saved.Binary, saved.Home, saved.Path, saved.Shell
	oldRuntime.configHome, oldRuntime.zdotDir = saved.ConfigHome, saved.ZdotDir
	expected, err := headlessServiceContent(dir, oldRuntime)
	if err != nil {
		return saved, nil, err
	}
	sum := sha256.Sum256(expected)
	if saved.SHA256 != hex.EncodeToString(sum[:]) {
		return saved, nil, errors.New("The service ownership record has changed; nothing changed.")
	}
	base := dir
	if rt.platform == "linux" {
		base = rt.configHome
	}
	if err := headlessServiceParent(path, base, false); err != nil {
		return saved, nil, err
	}
	actual, err := headlessServiceReadPrivate(path)
	if allowMissing && errors.Is(err, os.ErrNotExist) {
		return saved, nil, nil
	}
	if err != nil || !bytes.Equal(actual, expected) {
		return saved, nil, errors.New("The managed service file has changed; nothing changed.")
	}
	return saved, actual, nil
}

func headlessServiceManager(rt headlessServiceRuntime, name string, args ...string) ([]byte, error) {
	output, err := rt.run(name, args...)
	if err != nil {
		return nil, fmt.Errorf("Cannot manage the current user's Kilo Proxy service with %s. Check the user service manager; no sudo or login policy change was attempted.", filepath.Base(name))
	}
	return output, nil
}

func runHeadlessServiceCommand(dir string, args []string, rt headlessServiceRuntime, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "help") {
		fmt.Fprintln(stdout, "Usage: kilo-proxy-headless [--config-dir DIR] service install|start|stop|status|uninstall")
		return 0
	}
	if len(args) != 1 || !map[string]bool{"install": true, "start": true, "stop": true, "status": true, "uninstall": true}[args[0]] {
		fmt.Fprintln(stderr, "Usage: kilo-proxy-headless [--config-dir DIR] service install|start|stop|status|uninstall")
		return 2
	}
	profile, err := headlessConfigDir(dir)
	if err == nil {
		err = secureHeadlessProfile(profile)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if args[0] == "install" {
		err = installHeadlessService(profile, rt, stdout)
	} else {
		var saved headlessServiceManifest
		saved, _, err = headlessReadOwnedService(profile, rt, false)
		if errors.Is(err, os.ErrNotExist) && args[0] == "status" {
			fmt.Fprintln(stdout, `{"installed":false,"loaded":false,"running":false}`)
			return 0
		}
		if err == nil {
			err = headlessServiceAction(profile, args[0], saved, rt, stdout)
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func installHeadlessService(dir string, rt headlessServiceRuntime, stdout io.Writer) error {
	release, err := acquireProfileLock(dir)
	if err != nil {
		return err
	}
	defer release()
	servicePath, domain, err := headlessServicePath(dir, rt)
	if err != nil {
		return err
	}
	content, err := headlessServiceContent(dir, rt)
	if err != nil {
		return err
	}
	base := dir
	if rt.platform == "linux" {
		base = rt.configHome
	}
	if err = headlessServiceParent(servicePath, base, true); err != nil {
		return err
	}
	manifestPath := filepath.Join(dir, headlessServiceManifestFile)
	_, err = headlessServiceReadPrivate(manifestPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var oldService []byte
	if err == nil {
		_, oldService, err = headlessReadOwnedService(dir, rt, true)
		if err != nil {
			return err
		}
	} else if _, err = os.Lstat(servicePath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("An unrelated service already uses this path; nothing changed.")
	}
	sum := sha256.Sum256(content)
	saved := headlessServiceManifest{Version: 1, Platform: rt.platform, Profile: dir, Label: headlessServiceLabel(dir), Domain: domain,
		ServiceFile: servicePath, Binary: rt.binary, Home: rt.home, Path: rt.path, Shell: rt.shell, ConfigHome: rt.configHome, ZdotDir: rt.zdotDir, SHA256: hex.EncodeToString(sum[:])}
	manifest, _ := json.MarshalIndent(saved, "", "  ")
	if err = atomicCatalogFile(servicePath, content); err != nil {
		return errors.New("Cannot save the private user service definition.")
	}
	if err = atomicCatalogFile(manifestPath, manifest); err != nil {
		if oldService == nil {
			_ = os.Remove(servicePath)
		} else {
			_ = atomicCatalogFile(servicePath, oldService)
		}
		return errors.New("Cannot save the user service ownership record; the service definition was restored.")
	}
	if rt.platform == "linux" {
		if _, err = headlessServiceManager(rt, "systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if _, err = headlessServiceManager(rt, "systemctl", "--user", "enable", saved.Label+".service"); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Installed and enabled the current user's Kilo Proxy service. Run service start to start it. Logout persistence requires your administrator's existing linger policy.")
	} else {
		fmt.Fprintln(stdout, "Installed a private launchd user service. Run service start to load it in this user's domain; automatic startup before login or reboot requires separate administrator configuration.")
	}
	return nil
}

func headlessServiceAction(dir, action string, saved headlessServiceManifest, rt headlessServiceRuntime, stdout io.Writer) error {
	unit, target := saved.Label+".service", saved.Domain+"/"+saved.Label
	switch action {
	case "start":
		if rt.platform == "linux" {
			if _, err := headlessServiceManager(rt, "systemctl", "--user", "start", unit); err != nil {
				return err
			}
		} else if _, err := rt.run("/bin/launchctl", "print", target); err == nil {
			if _, err := headlessServiceManager(rt, "/bin/launchctl", "kickstart", "-p", target); err != nil {
				return err
			}
		} else if _, err := headlessServiceManager(rt, "/bin/launchctl", "bootstrap", saved.Domain, saved.ServiceFile); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Started the current user's Kilo Proxy service. Use status to check the proxy connection.")
	case "stop", "uninstall":
		if rt.platform == "linux" {
			command := "stop"
			arguments := []string{"--user", command, unit}
			if action == "uninstall" {
				arguments = []string{"--user", "disable", "--now", unit}
			}
			if _, err := headlessServiceManager(rt, "systemctl", arguments...); err != nil {
				return err
			}
		} else {
			_, loadedErr := rt.run("/bin/launchctl", "print", target)
			if loadedErr == nil {
				if _, err := headlessServiceManager(rt, "/bin/launchctl", "bootout", target); err != nil {
					return err
				}
			} else if action == "stop" {
				return errors.New("The managed launchd user service is not loaded or the user domain is unavailable.")
			}
		}
		if action == "uninstall" {
			deadline := time.Now().Add(rt.lockWait)
			var release func()
			var err error
			for {
				release, err = acquireProfileLock(dir)
				if err == nil || !errors.Is(err, errProfileLocked) || !time.Now().Before(deadline) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if err != nil {
				return err
			}
			defer release()
			// Recheck identity after waiting for the daemon to release its lock.
			if _, _, err := headlessReadOwnedService(dir, rt, false); err != nil {
				return err
			}
			if err := os.Remove(saved.ServiceFile); err != nil {
				return errors.New("Cannot remove the managed user service definition.")
			}
			if err := os.Remove(filepath.Join(dir, headlessServiceManifestFile)); err != nil {
				return errors.New("Cannot remove the managed user service ownership record.")
			}
			if rt.platform == "linux" {
				if _, err := headlessServiceManager(rt, "systemctl", "--user", "daemon-reload"); err != nil {
					return err
				}
			}
			fmt.Fprintln(stdout, "Uninstalled the owned Kilo Proxy user service. Configuration, credentials, models and agent sessions were preserved.")
		} else {
			fmt.Fprintln(stdout, "Stopped the current user's Kilo Proxy service.")
		}
	case "status":
		state := map[string]any{"installed": true, "platform": rt.platform, "service": saved.Label, "loaded": false, "running": false}
		if rt.platform == "linux" {
			output, err := headlessServiceManager(rt, "systemctl", "--user", "show", unit, "--property=LoadState", "--property=ActiveState", "--property=SubState")
			if err != nil {
				return err
			}
			values := map[string]string{}
			for _, line := range strings.Split(string(output), "\n") {
				if key, value, ok := strings.Cut(line, "="); ok {
					values[key] = value
				}
			}
			state["loaded"], state["running"] = values["LoadState"] == "loaded", values["ActiveState"] == "active" && values["SubState"] == "running"
		} else if output, err := rt.run("/bin/launchctl", "print", target); err == nil {
			state["loaded"] = true
			state["running"] = regexp.MustCompile(`(?m)^\s*pid = [1-9][0-9]*\s*$`).Match(output)
		}
		return json.NewEncoder(stdout).Encode(state)
	}
	return nil
}

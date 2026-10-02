package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var errOpenMausBotCompatibility = errors.New("Install OpenMausBot 0.1.92 or newer with managed model support, then refresh installed apps.")

func resolveOpenMausBot(platform, home, localAppData string) (string, error) {
	return resolveOpenMausBotWith(platform, home, localAppData, launchLookPath)
}

func resolveOpenMausBotWith(platform, home, localAppData string, lookup func(string) string) (string, error) {
	var candidates []string
	switch platform {
	case "macos", "darwin":
		for _, base := range []string{"/Applications", filepath.Join(home, "Applications")} {
			bundle := filepath.Join(base, "OpenMausBot.app")
			if launchExecutable(filepath.Join(bundle, "Contents", "MacOS", "OpenMausBot")) {
				return bundle, nil
			}
		}
	case "windows":
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		for _, name := range []string{"OpenMausBot", "openmausbot"} {
			candidates = append(candidates, filepath.Join(localAppData, "Programs", name, "OpenMausBot.exe"))
		}
	case "linux":
		// Only desktop binaries with the packaged runtime beside them qualify;
		// never confuse npm's same-name command with an Electron application.
		if path := lookup("openmausbot"); path != "" {
			if real, err := filepath.EvalSymlinks(path); err == nil {
				candidates = append(candidates, real)
			}
		}
		candidates = append(candidates, "/opt/OpenMausBot/openmausbot", "/opt/OpenMausBot/OpenMausBot", "/opt/openmausbot/openmausbot", filepath.Join(home, ".local", "share", "OpenMausBot", "openmausbot"))
	default:
		return "", errors.New("OpenMausBot desktop is supported on macOS, Windows and Linux.")
	}
	for _, path := range candidates {
		if filepath.IsAbs(path) && launchExecutable(path) && openMausBotCompatibility(path, platform) == nil {
			return path, nil
		}
	}
	return "", errors.New("Install OpenMausBot desktop on this computer, then refresh installed apps.")
}

// Electron packs its own manifest in ASAR, but ships the driver as a bounded
// regular file on every platform. Verify that contract without executing it or
// introducing a shell/ASAR extractor. macOS also exposes a bundle version.
func openMausBotCompatibility(executable, platform string) error {
	if !validOpenDesignShimPath(executable) {
		return errOpenMausBotCompatibility
	}
	root := filepath.Dir(executable)
	resources := "resources"
	if platform == "macos" || platform == "darwin" {
		if !strings.HasSuffix(executable, ".app") {
			return errOpenMausBotCompatibility
		}
		root = executable
		resources = filepath.Join("Contents", "Resources")
		data, err := readOpenDesignShimFile(filepath.Join(root, "Contents", "Info.plist"), 64<<10)
		if err != nil || !openMausBotVersionSupported(openMausBotPlistVersion(data)) {
			return errOpenMausBotCompatibility
		}
		if !launchExecutable(filepath.Join(root, "Contents", "MacOS", "OpenMausBot")) {
			return errOpenMausBotCompatibility
		}
	}
	dir := filepath.Join(root, resources, "server", "server", "drivers")
	if !safeLaunchDir(dir, root) {
		return errOpenMausBotCompatibility
	}
	data, err := readOpenDesignShimFile(filepath.Join(dir, "openai-compat.js"), 1<<20)
	if err != nil {
		return errOpenMausBotCompatibility
	}
	for _, capability := range []string{"managedModels", "apiKeyEnv", "input.environment"} {
		if !bytes.Contains(data, []byte(capability)) {
			return errOpenMausBotCompatibility
		}
	}
	return nil
}

func openMausBotPlistVersion(data []byte) string {
	d := xml.NewDecoder(bytes.NewReader(data))
	key := ""
	for {
		token, err := d.Token()
		if err != nil {
			return ""
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "key" {
			if d.DecodeElement(&key, &start) != nil {
				return ""
			}
		} else if start.Name.Local == "string" {
			var value string
			if d.DecodeElement(&value, &start) != nil {
				return ""
			}
			if key == "CFBundleShortVersionString" {
				return value
			}
			key = ""
		}
	}
}

func openMausBotVersionSupported(version string) bool {
	if len(version) > 128 {
		return false
	}
	parts := openDesignVersionPattern.FindStringSubmatch(version)
	if parts == nil {
		return false
	}
	minimum := [3]int{0, 1, 92}
	for i, want := range minimum {
		got, err := strconv.Atoi(parts[i+1])
		if err != nil {
			return false
		}
		if got != want {
			return got > want
		}
	}
	return parts[4] == ""
}

var errOpenMausBotRunning = errors.New("Cannot verify whether the managed OpenMausBot workspace is closed.")

func openMausBotRunning(paths openMausBotPaths) (bool, error) {
	return openMausBotRunningWith(paths, openMausBotProcessAlive, openMausBotWindowRunning)
}

func openMausBotRunningWith(paths openMausBotPaths, alive func(int) (bool, error), window func(context.Context, string) (bool, error)) (bool, error) {
	if !filepath.IsAbs(paths.Data) || !filepath.IsAbs(paths.UI) {
		return false, errOpenMausBotRunning
	}
	host, err := os.Hostname()
	if err != nil {
		return false, errOpenMausBotRunning
	}
	for _, name := range []string{"openmausbot-server.lease", filepath.Join(".openmausbot-server-child", "openmausbot-server.lease")} {
		path := filepath.Join(paths.Data, name)
		// Missing profiles are expected on first use. Existing symlink ancestors
		// are rejected before reading even a non-secret PID from a lease.
		if _, err := os.Lstat(filepath.Dir(path)); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if !safeLaunchDir(filepath.Dir(path), paths.Root) {
			return false, errOpenMausBotRunning
		}
		data, err := readOpenDesignShimFile(path, 16<<10)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, errOpenMausBotRunning
		}
		var lease struct {
			Version   int    `json:"version"`
			PID       int    `json:"pid"`
			Host      string `json:"host"`
			CreatedAt int64  `json:"createdAt"`
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		if decoder.Decode(&lease) != nil || decoder.Decode(&struct{}{}) != io.EOF || lease.Version != 1 || lease.PID <= 0 || int64(lease.PID) > 2147483647 || lease.Host != host || lease.CreatedAt <= 0 {
			return false, errOpenMausBotRunning
		}
		running, err := alive(lease.PID)
		if err != nil {
			return false, errOpenMausBotRunning
		}
		if running {
			return true, nil
		}
		// Do not delete stale leases. Upstream verifies boot/process identities
		// when reclaiming them; a reused live PID conservatively forbids writes.
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	running, err := window(ctx, paths.UI)
	if err != nil {
		return false, errOpenMausBotRunning
	}
	return running, nil
}

func openMausBotWindowCommandMatches(command, ui string) bool {
	// ps/CIM quote argv differently. Both forms are accepted, but a suffix
	// belonging to another profile must never match our exact directory.
	for _, prefix := range []string{"--user-data-dir=", "--user-data-dir=\"", "--user-data-dir='"} {
		needle := prefix + ui
		for offset := 0; offset < len(command); {
			i := strings.Index(command[offset:], needle)
			if i < 0 {
				break
			}
			i += offset
			end := i + len(needle)
			if (i == 0 || strings.ContainsRune(" \t\"'", rune(command[i-1]))) && (end == len(command) || strings.ContainsRune(" \t\r\n\"'", rune(command[end]))) {
				return true
			}
			offset = end
		}
	}
	return false
}

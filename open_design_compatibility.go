package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const openDesignMinimumVersion = "0.22.2"
const openDesignPackagedConfigLimit int64 = 64 << 10

var errOpenDesignCompatibility = errors.New("Update Open Design to version 0.22.2 or newer before using its Kilo CLI workspace; the installed package version is older or could not be verified.")
var openDesignVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// Read the packaged appVersion, which also supplies Electron's bundle version.
// This does not start the app, consult its personal profile, or follow updates.
func openDesignCompatibility(executable, platform string) error {
	if reason := launchClientPlatformReason("open-design", platform); reason != "" {
		return errors.New(reason)
	}
	if !validOpenDesignShimPath(executable) {
		return errOpenDesignCompatibility
	}
	var root string
	var components []string
	if platform == "windows" {
		root, components = filepath.Dir(executable), []string{"resources"}
	} else {
		root = filepath.Clean(executable)
		if !strings.HasSuffix(root, ".app") {
			macos := filepath.Dir(root)
			contents := filepath.Dir(macos)
			bundle := filepath.Dir(contents)
			if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(bundle, ".app") {
				// Actual macOS discovery only returns .app bundles. Injected
				// native fixture executables have no packaged metadata to read.
				return nil
			}
			root = bundle
		}
		components = []string{"Contents", "Resources"}
	}
	current := root
	for _, part := range append([]string{""}, components...) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errOpenDesignCompatibility
		}
	}
	data, err := readOpenDesignShimFile(filepath.Join(current, "open-design-config.json"), openDesignPackagedConfigLimit)
	if err != nil {
		return errOpenDesignCompatibility
	}
	var config struct {
		Version string `json:"appVersion"`
	}
	if json.Unmarshal(data, &config) != nil || !openDesignVersionSupported(config.Version) {
		return errOpenDesignCompatibility
	}
	return nil
}

func openDesignVersionSupported(version string) bool {
	if len(version) > 128 {
		return false
	}
	parts := openDesignVersionPattern.FindStringSubmatch(version)
	if parts == nil {
		return false
	}
	var numbers [3]int
	for i := range numbers {
		n, err := strconv.Atoi(parts[i+1])
		if err != nil {
			return false
		}
		numbers[i] = n
	}
	minimum := [3]int{0, 22, 2}
	for i, n := range numbers {
		if n != minimum[i] {
			return n > minimum[i]
		}
	}
	return parts[4] == "" // A prerelease sorts before the stable minimum.
}

package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type terminalManualResult struct {
	Supported bool              `json:"supported"`
	Shell     string            `json:"shell,omitempty"`
	Commands  map[string]string `json:"commands,omitempty"`
	All       string            `json:"all,omitempty"`
}

var errTerminalManualPaths = errors.New("Cannot prepare manual terminal commands. Check the Kilo Proxy application and configuration paths.")

func terminalManualCommands(binary, configDir string) (terminalManualResult, error) {
	for _, path := range []string{binary, configDir} {
		if !filepath.IsAbs(path) || len(path) > 8192 || strings.ContainsAny(path, "\x00\r\n") {
			return terminalManualResult{}, errTerminalManualPaths
		}
	}
	result := terminalManualResult{Supported: true, Shell: "zsh-bash", Commands: make(map[string]string, 4)}
	var blocks []string
	for _, command := range []struct{ name, client string }{{"kilo-codex", "codex-cli"}, {"kilo-claude", "claude"}, {"kilo-omp", "omp"}, {"kilo-opencode", "opencode"}} {
		// These functions run inside the user's shell. Never use exec here:
		// returning from the CLI must leave the shell open with its exit status.
		block := "function " + command.name + " {\n  command " + helperShellQuote(binary) + " " + terminalAgentFlag + " " + command.client + " --config-dir " + helperShellQuote(configDir) + " -- \"$@\"\n}\n"
		result.Commands[command.name] = block
		blocks = append(blocks, block)
	}
	result.All = strings.Join(blocks, "\n")
	return result, nil
}

// Manual setup is independent of installer conflicts and login-shell support.
// Generate text only; do not read profiles, discover clients, or modify files.
func (a *app) terminalManualAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	platform := a.launchRuntime().platform
	if !terminalPlatformSupported(platform) {
		jsonResponse(w, http.StatusOK, terminalManualResult{})
		return
	}
	binary := a.terminalCommandsBinary
	if binary == "" {
		var err error
		binary, err = os.Executable()
		if err != nil {
			jsonError(w, http.StatusConflict, errTerminalManualPaths.Error())
			return
		}
	}
	var result terminalManualResult
	var err error
	if platform == "windows" {
		result, err = terminalPowerShellManualCommands(binary, a.dir)
	} else {
		result, err = terminalManualCommands(binary, a.dir)
	}
	if err != nil {
		jsonError(w, http.StatusConflict, errTerminalManualPaths.Error())
		return
	}
	jsonResponse(w, http.StatusOK, result)
}

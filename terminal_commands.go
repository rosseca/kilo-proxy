package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const terminalAgentFlag = "--terminal-agent"

func terminalClientSupported(client string) bool {
	return client == "codex-cli" || client == "claude" || client == "omp" || client == "opencode"
}

func terminalPlatformSupported(platform string) bool {
	return platform == "darwin" || platform == "macos" || platform == "linux" || platform == "windows"
}

func (a *app) terminalCommandsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "POST" {
		jsonError(w, 405, "Method not allowed.")
		return
	}
	rt := a.launchRuntime()
	if !terminalPlatformSupported(rt.platform) {
		if r.Method == "POST" {
			jsonError(w, 409, "Terminal commands are available on macOS, Linux and Windows.")
			return
		}
		jsonResponse(w, 200, map[string]any{"supported": false, "installed": false})
		return
	}
	binary, shell := a.terminalCommandsBinary, a.terminalCommandsShell
	if binary == "" {
		binary, _ = os.Executable()
	}
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	var result terminalCommandsInstallResult
	var err error
	if r.Method == "POST" {
		if !a.launchMu.TryLock() {
			jsonError(w, 409, "Another launch or installation is being prepared.")
			return
		}
		defer a.launchMu.Unlock()
		result, err = installTerminalCommands(rt.home, a.dir, binary, shell, rt.platform, a.terminalCommandsProfiles)
	} else {
		result, err = terminalCommandsStatus(rt.home, a.dir, binary, shell, rt.platform, a.terminalCommandsProfiles)
	}
	if err != nil {
		jsonError(w, 409, err.Error())
		return
	}
	jsonResponse(w, 200, struct {
		terminalCommandsInstallResult
		Supported bool `json:"supported"`
	}{result, true})
}

type terminalPrepareRequest struct {
	Client        string `json:"client"`
	Directory     string `json:"directory"`
	ClaudeVersion string `json:"claudeVersion,omitempty"`
}

func (a *app) terminalPrepareAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, 405, "Method not allowed.")
		return
	}
	var input terminalPrepareRequest
	if !decodeBody(w, r, &input) {
		return
	}
	if !terminalClientSupported(input.Client) || len(input.ClaudeVersion) > 64 || strings.ContainsAny(input.ClaudeVersion, "\r\n\x00") {
		jsonError(w, 400, "Choose kilo-codex, kilo-claude, kilo-omp or kilo-opencode.")
		return
	}
	if !a.launchMu.TryLock() {
		jsonError(w, 409, "Another agent is being prepared. Try again.")
		return
	}
	defer a.launchMu.Unlock()
	rt := a.launchRuntime()
	if !terminalPlatformSupported(rt.platform) {
		jsonError(w, 409, "Terminal commands are available on macOS, Linux and Windows.")
		return
	}
	directory, err := launchPath(input.Directory, rt.home)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	state := a.modelLibrary.snapshot()
	if state.RecoveryRequired || state.Warning != "" || len(state.Library.Models) == 0 {
		jsonError(w, 409, "Open Models in Kilo Proxy and save a valid shared model selection first.")
		return
	}
	a.mu.Lock()
	if a.apiKey == "" || a.config.OrgID == "" {
		a.mu.Unlock()
		jsonError(w, 409, "Connect your Kilo account and organization in Kilo Proxy first.")
		return
	}
	name, _ := launchClientIdentity(input.Client)
	plan := clientLaunchPlan{Client: input.Client, Name: name, Kind: "terminal", Directory: directory, Env: map[string]string{}}
	err = a.prepareTerminalProfile(input.Client, rt.home, state.Library, claudeCaps(input.ClaudeVersion))
	if err == nil {
		err = a.launchProfile(&plan, rt.home)
		if input.Client == "opencode" {
			// The profile already names the default model. Unlike the GUI's TUI
			// launch, terminal commands also support subcommands such as models,
			// which reject --model. Forward only the user's OpenCode arguments.
			plan.Args = nil
		}
	}
	if err != nil {
		a.mu.Unlock()
		jsonError(w, 409, err.Error())
		return
	}
	if err = a.startLocked(); err != nil {
		a.mu.Unlock()
		jsonError(w, 409, "Cannot start the proxy. Check your Kilo connection and local port in the app.")
		return
	}
	a.mu.Unlock()
	// Only fixed managed arguments/environment leave the app. Client executable
	// discovery, caller arguments, cwd and exec happen in the invoking terminal.
	jsonResponse(w, 200, plan)
}

func terminalLibraryChoices(library modelLibrary, catalog []modelInfo) []nativeModelChoice {
	metadata := map[string]modelInfo{}
	for _, model := range catalog {
		metadata[model.ID] = model
	}
	choices := make([]nativeModelChoice, 0, len(library.Models))
	for _, item := range library.Models {
		choices = append(choices, contextChoiceFromLibrary(item, metadata[item.ID]))
	}
	return choices
}

// Caller holds a.mu, the same lock used by GUI profile preparation.
func (a *app) prepareTerminalProfile(client, home string, library modelLibrary, caps claudeCapabilities) error {
	if err := validateModelLibrary(library); err != nil {
		return err
	}
	if client == "opencode" {
		return a.prepareTerminalOpenCodeProfile(library)
	}
	if client == "omp" {
		selection, err := ompSelectionFromChoices(terminalLibraryChoices(library, readNativeCatalogCache(a.dir, a.config.OrgID)), library.DefaultModel)
		if err != nil {
			return err
		}
		_, err = a.saveOMPProfile(selection)
		return err
	}
	if client == "codex-cli" {
		dir := a.codexCLIProfileDir
		if dir == "" {
			dir = filepath.Join(home, ".codex-kilo-cli")
		}
		catalog, err := buildCodexCatalog(terminalLibraryChoices(library, readNativeCatalogCache(a.dir, a.config.OrgID)), library.DefaultModel, false)
		if err != nil {
			return err
		}
		_, _, err = a.saveCodexImageProfile(dir, catalog, nil)
		return err
	}
	if client != "claude" {
		return errors.New("Unsupported terminal agent.")
	}
	dir := a.claudeProfileDir
	if dir == "" {
		dir = filepath.Join(home, ".claude-kilo")
	}
	selection := claudeSelection{Initial: library.DefaultModel, Mode: "installed", Aliases: map[string]string{}}
	ids := map[string]bool{}
	choices := terminalLibraryChoices(library, readNativeCatalogCache(a.dir, a.config.OrgID))
	for i, model := range library.Models {
		context, err := contextPolicyForChoice(choices[i])
		if err != nil {
			return err
		}
		effort := model.ReasoningEffort
		if !validClaudeEffort(model.ID, effort) || !caps.PerModelEffort && (model.ID != library.DefaultModel || effort == "xhigh") {
			effort = ""
		}
		selection.Models = append(selection.Models, claudeModel{ID: model.ID, DisplayName: model.DisplayName, Effort: effort, Context: context.ContextWindow, Output: context.MaxOutputTokens})
		ids[model.ID] = true
	}
	if old, err := readCatalogFile(filepath.Join(dir, "kilo-models.json")); err == nil {
		var previous claudeSelection
		if json.Unmarshal(old, &previous) == nil && validateClaudeSelection(previous) == nil {
			for alias, id := range previous.Aliases {
				if ids[id] {
					selection.Aliases[alias] = id
				}
			}
		}
	}
	_, err := saveClaudeProfile(dir, selection, caps, a.config.Port, a.config.LocalKey)
	return err
}

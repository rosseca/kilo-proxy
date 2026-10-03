package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// Setup commands share the authenticated controller API, whether the controller
// is running in this process or in the foreground service. Output types are
// deliberately narrow: /api/state also contains local credentials.
type headlessSetupCaller interface {
	call(context.Context, string, string, any, any) error
}

type headlessSetupState struct {
	Port          int            `json:"port"`
	OrgID         string         `json:"orgId"`
	KiloReady     bool           `json:"kiloReady"`
	HasKey        bool           `json:"hasKey"`
	Organizations []organization `json:"organizations"`
	Auth          *loginSession  `json:"auth"`
	ChatGPT       chatGPTState   `json:"chatgpt"`
}

type headlessSetupCatalog struct {
	Models    []modelInfo `json:"models"`
	Complete  bool        `json:"complete"`
	FetchedAt time.Time   `json:"fetchedAt"`
	Warnings  []string    `json:"warnings,omitempty"`
}

func runHeadlessSetupCLI(dir, command string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client, err := openHeadlessClient(dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer client.close()
	if command == "commands" && len(args) == 0 {
		printHeadlessCommandSuggestion(ctx, client, dir, stdout)
		fmt.Fprintln(stdout, "Use commands install, status, or manual. Installation runs only when requested.")
		return 0
	}
	code := runHeadlessSetupCommand(ctx, client, command, args, stdin, stdout, stderr)
	if code == 0 && (command == "configure" || command == "login") {
		printHeadlessCommandSuggestion(ctx, client, dir, stdout)
	}
	return code
}

func runHeadlessSetupCommand(ctx context.Context, client headlessSetupCaller, command string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var err error
	switch command {
	case "configure":
		err = headlessConfigure(ctx, client, args, stdin, stdout)
	case "login":
		err = headlessLogin(ctx, client, args, stdout)
	case "logout":
		err = headlessLogout(ctx, client, args, stdout)
	case "models":
		err = headlessModels(ctx, client, args, stdin, stdout)
	case "commands":
		err = headlessCommands(ctx, client, args, stdout)
	case "connection":
		err = headlessConnection(ctx, client, args, stdout)
	default:
		err = errors.New("Use configure, login, logout, models, commands, or connection.")
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func headlessConnection(ctx context.Context, client headlessSetupCaller, args []string, stdout io.Writer) error {
	f := headlessSetupFlags("connection")
	asJSON, showKey := f.Bool("json", false, "Print connection JSON"), f.Bool("show-key", false, "Explicitly reveal the local proxy key")
	if f.Parse(args) != nil || f.NArg() != 0 {
		return errors.New("Use connection [--json] [--show-key].")
	}
	var state struct {
		Port     int    `json:"port"`
		Running  bool   `json:"running"`
		Version  string `json:"version"`
		LocalKey string `json:"localKey"`
	}
	if err := client.call(ctx, "GET", "/api/state", nil, &state); err != nil {
		return err
	}
	if state.Port < 1024 || state.Port > 65535 {
		return errors.New("Kilo Proxy returned an invalid local connection.")
	}
	connection := struct {
		BaseURL string `json:"baseURL"`
		Running bool   `json:"running"`
		Version string `json:"version"`
		Key     string `json:"localKey,omitempty"`
	}{BaseURL: fmt.Sprintf("http://127.0.0.1:%d/v1", state.Port), Running: state.Running, Version: state.Version}
	if *showKey {
		if len(state.LocalKey) < 32 || len(state.LocalKey) > 8192 || strings.IndexFunc(state.LocalKey, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return errors.New("Kilo Proxy returned an invalid local credential.")
		}
		connection.Key = state.LocalKey
	}
	if *asJSON {
		return headlessWriteJSON(stdout, connection)
	}
	if _, err := fmt.Fprintf(stdout, "Base URL: %s\nAuthentication: Bearer local proxy key\nRunning: %t\n", connection.BaseURL, connection.Running); err != nil {
		return err
	}
	if *showKey {
		_, err := fmt.Fprintln(stdout, "Local proxy key:", connection.Key)
		return err
	}
	return nil
}

func headlessSetupFlags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	// Flag errors can echo arbitrary argv. Never include credential material
	// accidentally supplied as an unsupported flag in diagnostics.
	f.SetOutput(io.Discard)
	return f
}

func headlessConfigure(ctx context.Context, client headlessSetupCaller, args []string, stdin io.Reader, stdout io.Writer) error {
	f := headlessSetupFlags("configure")
	org := f.String("org", "", "Kilo organization ID")
	f.StringVar(org, "org-id", "", "Kilo organization ID")
	port := f.Int("port", 0, "Local proxy port")
	keyStdin := f.Bool("key-stdin", false, "Read Kilo key from stdin")
	keyFile := f.String("key-file", "", "Read Kilo key from a private file")
	if f.Parse(args) != nil || f.NArg() != 0 || *keyStdin && *keyFile != "" {
		return errors.New("Use configure [--org ID] [--port N] [--key-stdin | --key-file PATH]. API keys cannot be supplied in argv.")
	}
	var state headlessSetupState
	if err := client.call(ctx, "GET", "/api/state", nil, &state); err != nil {
		return err
	}
	key := ""
	var err error
	if *keyStdin {
		key, err = readHeadlessKey(stdin)
	} else if *keyFile != "" {
		key, err = readHeadlessKeyFile(*keyFile)
	}
	if err != nil {
		return err
	}
	if *port == 0 {
		*port = state.Port
	}
	if *org == "" {
		*org = state.OrgID
	}
	if err := client.call(ctx, "POST", "/api/config", map[string]any{"apiKey": key, "orgId": *org, "port": *port, "remember": true}, nil); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Configuration saved in the headless profile.")
	return err
}

func readHeadlessKey(reader io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 8195))
	if err != nil || len(data) > 8194 {
		return "", errors.New("Could not read a Kilo key of at most 8,192 bytes.")
	}
	// A single terminal newline is convenient for printf/read-secret pipelines;
	// internal whitespace is invalid and is never included in the error.
	key := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if len(key) == 0 || len(key) > 8192 || strings.IndexFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", errors.New("The Kilo key must be nonempty and contain no spaces or control characters.")
	}
	return key, nil
}

func readHeadlessKeyFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8194 || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return "", errors.New("The Kilo key file must be a private regular file (mode 0600), not a symlink.")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("Could not open the private Kilo key file.")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || runtime.GOOS != "windows" && opened.Mode().Perm()&0077 != 0 {
		return "", errors.New("The private Kilo key file changed while opening it.")
	}
	return readHeadlessKey(file)
}

func headlessLogin(ctx context.Context, client headlessSetupCaller, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "kilo" && args[0] != "chatgpt" {
		return errors.New("Use login kilo [--org ID] or login chatgpt.")
	}
	provider := args[0]
	f := headlessSetupFlags("login")
	org := f.String("org", "", "Kilo organization ID")
	if f.Parse(args[1:]) != nil || f.NArg() != 0 || provider == "chatgpt" && *org != "" {
		return errors.New("Use login kilo [--org ID] or login chatgpt.")
	}
	start, cancelPath := "/api/auth/start", "/api/auth/cancel"
	if provider == "chatgpt" {
		start, cancelPath = "/api/chatgpt/login", "/api/chatgpt/cancel"
	}
	if err := client.call(ctx, "POST", start, map[string]any{}, nil); err != nil {
		return err
	}
	finished := false
	defer func() {
		if !finished {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = client.call(cancelCtx, "POST", cancelPath, map[string]any{}, nil)
		}
	}()
	lastCode := ""
	for {
		var state headlessSetupState
		if err := client.call(ctx, "GET", "/api/state", nil, &state); err != nil {
			return err
		}
		status, code, verification, message := "", "", "", ""
		if provider == "chatgpt" {
			status, code, verification, message = state.ChatGPT.Status, state.ChatGPT.Code, state.ChatGPT.VerificationURL, state.ChatGPT.Error
			if state.ChatGPT.Connected && status == "idle" {
				finished = true
				_, err := fmt.Fprintln(stdout, "ChatGPT sign-in saved in the headless profile.")
				return err
			}
		} else if state.Auth != nil {
			status, code, verification, message = state.Auth.Status, state.Auth.Code, state.Auth.VerificationURL, state.Auth.Message
			if status == "approved" {
				selected := *org
				if selected == "" && len(state.Organizations) == 1 {
					selected = state.Organizations[0].ID
				}
				if selected == "" {
					for _, organization := range state.Organizations {
						if _, err := fmt.Fprintf(stdout, "%s\t%s\n", headlessTerminalText(organization.ID), headlessTerminalText(organization.Name)); err != nil {
							return err
						}
					}
					return errors.New("Select an organization from the list and run login kilo --org ID to save it.")
				}
				found := false
				for _, organization := range state.Organizations {
					found = found || organization.ID == selected
				}
				if !found {
					return errors.New("The selected organization is not available for this Kilo account.")
				}
				if err := client.call(ctx, "POST", "/api/config", map[string]any{"apiKey": "", "orgId": selected, "port": state.Port, "remember": true}, nil); err != nil {
					return err
				}
				finished = true
				_, err := fmt.Fprintln(stdout, "Kilo sign-in and organization saved in the headless profile.")
				return err
			}
		}
		if code != "" && code != lastCode {
			if strings.IndexFunc(code+verification, unicode.IsControl) >= 0 {
				return errors.New("The provider returned invalid device verification details.")
			}
			if _, err := fmt.Fprintf(stdout, "Open %s in a browser and enter code %s.\n", verification, code); err != nil {
				return err
			}
			lastCode = code
		}
		if status != "pending" && status != "starting" {
			if message == "" {
				message = "Sign-in did not complete. Run login again."
			}
			return errors.New(headlessTerminalText(message))
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.New("Sign-in canceled.")
		case <-timer.C:
		}
	}
}

func headlessLogout(ctx context.Context, client headlessSetupCaller, args []string, stdout io.Writer) error {
	if len(args) != 1 || args[0] != "kilo" && args[0] != "chatgpt" {
		return errors.New("Use logout kilo or logout chatgpt.")
	}
	path := "/api/forget"
	if args[0] == "chatgpt" {
		path = "/api/chatgpt/logout"
	}
	if err := client.call(ctx, "POST", path, map[string]any{}, nil); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, "Saved sign-in removed from the headless profile.")
	return err
}

func headlessCatalog(ctx context.Context, client headlessSetupCaller, refresh bool) (headlessSetupCatalog, error) {
	var result headlessSetupCatalog
	method := "GET"
	if refresh {
		method = "POST"
	}
	if err := client.call(ctx, method, "/api/headless/catalog", map[string]any{}, &result); err != nil {
		return result, err
	}
	if !refresh && (len(result.Models) == 0 || modelCatalogNeedsRefresh(result.FetchedAt, time.Now())) {
		if err := client.call(ctx, "POST", "/api/headless/catalog", map[string]any{}, &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func headlessModels(ctx context.Context, client headlessSetupCaller, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("Use models catalog, list, add, remove, default, import, or export.")
	}
	if args[0] == "catalog" {
		f := headlessSetupFlags("models catalog")
		refresh, asJSON := f.Bool("refresh", false, "Refresh provider models"), f.Bool("json", false, "Print catalog JSON")
		if f.Parse(args[1:]) != nil || f.NArg() != 0 {
			return errors.New("Use models catalog [--refresh] [--json].")
		}
		catalog, err := headlessCatalog(ctx, client, *refresh)
		if err != nil {
			return err
		}
		if *asJSON {
			return headlessWriteJSON(stdout, catalog)
		}
		for _, model := range catalog.Models {
			levels, _ := nativePublishedReasoning(nativeModelChoice{Model: model})
			tools := "unknown"
			if model.Tools != nil {
				tools = fmt.Sprint(*model.Tools)
			}
			if _, err := fmt.Fprintf(stdout, "%s\t%s\tcontext=%d\toutput=%d\ttools=%s\treasoning=%s\n", headlessTerminalText(model.ID), headlessTerminalText(model.Name), model.ContextWindow, model.MaxOutputTokens, tools, strings.Join(levels, ",")); err != nil {
				return err
			}
		}
		for _, warning := range catalog.Warnings {
			if _, err := fmt.Fprintln(stdout, "Catalog warning:", headlessTerminalText(warning)); err != nil {
				return err
			}
		}
		return nil
	}
	var state modelLibraryState
	if err := client.call(ctx, "GET", "/api/model-library", nil, &state); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("Use models list. For portable JSON, use models export.")
		}
		return headlessWriteJSON(stdout, state)
	case "export":
		if len(args) > 2 {
			return errors.New("Use models export [FILE|-].")
		}
		if len(args) == 1 || args[1] == "-" {
			return headlessWriteJSON(stdout, state.Library)
		}
		data, err := json.MarshalIndent(state.Library, "", "  ")
		if err != nil {
			return err
		}
		if err := atomicCatalogFile(args[1], append(data, '\n')); err != nil {
			return errors.New("Could not write the model library export to a regular file.")
		}
		return nil
	case "import":
		f := headlessSetupFlags("models import")
		recover := f.Bool("recover", false, "Explicitly recover a damaged library")
		// Put options before the path, as with the other top-level commands.
		if f.Parse(args[1:]) != nil || f.NArg() != 1 {
			return errors.New("Use models import [--recover] FILE|-.")
		}
		reader := stdin
		if f.Arg(0) != "-" {
			info, err := os.Lstat(f.Arg(0))
			if err != nil || !info.Mode().IsRegular() || info.Size() > modelLibraryLimit {
				return errors.New("The model library import must be a regular JSON file of at most 128 KiB.")
			}
			file, err := os.Open(f.Arg(0))
			if err != nil {
				return errors.New("Could not open the model library import.")
			}
			defer file.Close()
			reader = file
		}
		data, err := io.ReadAll(io.LimitReader(reader, modelLibraryLimit+1))
		if err != nil || len(data) > modelLibraryLimit {
			return errors.New("The model library import exceeds 128 KiB or could not be read.")
		}
		library, err := decodeModelLibrary(data)
		if err != nil {
			return errors.New("The model library JSON is invalid; export an existing library for its schema.")
		}
		return headlessSaveLibrary(ctx, client, state, library, *recover, stdout)
	case "add":
		return headlessAddModel(ctx, client, state, args[1:], stdout)
	case "remove", "default":
		if len(args) != 2 || !catalogID.MatchString(args[1]) {
			return errors.New("Use models remove ID or models default ID, with an exact model ID.")
		}
		library := cloneModelLibrary(state.Library)
		found := false
		for i, model := range library.Models {
			if model.ID != args[1] {
				continue
			}
			found = true
			if args[0] == "default" {
				library.DefaultModel = model.ID
			} else {
				library.Models = append(library.Models[:i], library.Models[i+1:]...)
				if library.DefaultModel == model.ID {
					library.DefaultModel = ""
					if len(library.Models) > 0 {
						library.DefaultModel = library.Models[0].ID
					}
				}
			}
			break
		}
		if !found {
			return errors.New("That exact model ID is not in the saved library.")
		}
		return headlessSaveLibrary(ctx, client, state, library, false, stdout)
	default:
		return errors.New("Use models catalog, list, add, remove, default, import, or export.")
	}
}

func headlessAddModel(ctx context.Context, client headlessSetupCaller, state modelLibraryState, args []string, stdout io.Writer) error {
	if len(args) == 0 || !catalogID.MatchString(args[0]) {
		return errors.New("Use models add ID [--reasoning LEVEL] [--context PRESET] [--context-tokens N] [--name NAME].")
	}
	id := args[0]
	f := headlessSetupFlags("models add")
	reasoning, preset, name := f.String("reasoning", "auto", "Saved reasoning level"), f.String("context", contextPresetRecommended, "Context policy"), f.String("name", "", "Display name")
	tokens := f.Int("context-tokens", 0, "Custom context budget")
	if f.Parse(args[1:]) != nil || f.NArg() != 0 {
		return errors.New("Use models add ID [--reasoning LEVEL] [--context PRESET] [--context-tokens N] [--name NAME].")
	}
	catalog, err := headlessCatalog(ctx, client, false)
	if err != nil {
		return err
	}
	var metadata modelInfo
	for _, model := range catalog.Models {
		if model.ID == id {
			metadata = model
			break
		}
	}
	if metadata.ID == "" {
		return errors.New("That exact model ID is not in the current catalog. Refresh the catalog or import an explicit library JSON.")
	}
	if *reasoning == "auto" {
		*reasoning = ""
	}
	levels, _ := nativePublishedReasoning(nativeModelChoice{Model: metadata})
	if *reasoning != "" && !helperContains(levels, *reasoning) {
		return errors.New("That reasoning level is not supported by this model. Inspect models catalog or use an explicit library JSON override.")
	}
	library := cloneModelLibrary(state.Library)
	item := modelLibraryItem{ID: id, ContextPreset: contextPresetRecommended}
	index := -1
	for i, old := range library.Models {
		if old.ID == id {
			item = old
			index = i
			break
		}
	}
	visited := map[string]bool{}
	f.Visit(func(field *flag.Flag) { visited[field.Name] = true })
	if visited["name"] {
		item.DisplayName = *name
	}
	if visited["reasoning"] {
		item.ReasoningEffort, item.ReasoningCustom, item.ReasoningLevels = *reasoning, false, nil
	}
	if visited["context"] {
		item.ContextPreset = *preset
		if *preset != contextPresetCustom {
			item.ContextWindow = 0
		}
	}
	if visited["context-tokens"] {
		item.ContextWindow = *tokens
	}
	policy, budget := contextPolicyFromLibrary(item)
	if policy != contextPresetCustom && visited["context-tokens"] {
		return errors.New("Only Custom context accepts --context-tokens.")
	}
	if _, err := resolveContextPolicy(policy, budget, metadata.ContextWindow, item.MaxOutputTokens); err != nil {
		return err
	}
	if index >= 0 {
		library.Models[index] = item
	} else {
		library.Models = append(library.Models, item)
	}
	if library.DefaultModel == "" {
		library.DefaultModel = id
	}
	return headlessSaveLibrary(ctx, client, state, library, false, stdout)
}

func headlessSaveLibrary(ctx context.Context, client headlessSetupCaller, state modelLibraryState, library modelLibrary, recover bool, stdout io.Writer) error {
	if err := validateModelLibrary(library); err != nil {
		return err
	}
	if state.RecoveryRequired && !recover {
		return errModelLibraryRecovery
	}
	var saved modelLibraryState
	if err := client.call(ctx, "PUT", "/api/model-library", map[string]any{"library": library, "revision": state.Revision, "recover": recover}, &saved); err != nil {
		return err
	}
	return headlessWriteJSON(stdout, saved)
}

func headlessCommands(ctx context.Context, client headlessSetupCaller, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("Use commands install [--json], status, or manual.")
	}
	if args[0] == "manual" {
		if len(args) != 1 {
			return errors.New("Use commands manual to print Bash/Zsh functions without modifying files.")
		}
		var manual terminalManualResult
		if err := client.call(ctx, "GET", "/api/terminal/manual", nil, &manual); err != nil {
			return err
		}
		if !manual.Supported {
			return errors.New("Manual commands are unavailable on this platform.")
		}
		_, err := fmt.Fprint(stdout, manual.All)
		return err
	}
	method := "GET"
	asJSON := true
	if args[0] == "install" {
		method = "POST"
		f := headlessSetupFlags("commands install")
		jsonFlag := f.Bool("json", false, "Print installation JSON")
		if f.Parse(args[1:]) != nil || f.NArg() != 0 {
			return errors.New("Use commands install [--json].")
		}
		asJSON = *jsonFlag
	} else if args[0] != "status" {
		return errors.New("Use commands install [--json], status, or manual.")
	} else if len(args) != 1 {
		return errors.New("Use commands status to print installation JSON.")
	}
	var result terminalCommandsInstallResult
	if err := client.call(ctx, method, "/api/terminal/commands", map[string]any{}, &result); err != nil {
		return err
	}
	if asJSON {
		return headlessWriteJSON(stdout, result)
	}
	if _, err := fmt.Fprintln(stdout, "Installed kilo-codex, kilo-claude, kilo-opencode, and kilo-omp on this machine."); err != nil {
		return err
	}
	return headlessCommandActivation(result, stdout)
}

// Setup and serve offer instructions, never install wrappers or edit shell
// files implicitly. A broken installer does not turn a saved login into failure.
func printHeadlessCommandSuggestion(ctx context.Context, client headlessSetupCaller, dir string, stdout io.Writer) {
	binary, err := os.Executable()
	if err != nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	printHeadlessCommandSuggestionFor(ctx, client, dir, binary, home, os.Getenv("SHELL"), stdout)
}

func printHeadlessCommandSuggestionFor(ctx context.Context, client headlessSetupCaller, dir, binary, home, shell string, stdout io.Writer) {
	for _, path := range []string{dir, binary, home} {
		if !filepath.IsAbs(path) || strings.IndexFunc(path, unicode.IsControl) >= 0 {
			return
		}
	}
	statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var status terminalCommandsInstallResult
	err := client.call(statusCtx, "GET", "/api/terminal/commands", nil, &status)
	if err == nil && status.Installed && status.PathConfigured {
		return
	}
	command := helperShellQuote(binary) + " --config-dir " + helperShellQuote(dir) + " commands "
	fmt.Fprintln(stdout, "\nTo add kilo-codex, kilo-claude, kilo-opencode, and kilo-omp to this machine:")
	if err == nil && (status.Shell == "zsh" || status.Shell == "bash" || status.Shell == "fish") {
		fmt.Fprintln(stdout, "  "+command+"install")
		fmt.Fprintln(stdout, "This installs wrappers and a managed PATH block only when you run it.")
		if len(status.StartupFiles) > 0 {
			fmt.Fprintln(stdout, "Shell startup files:")
			for _, path := range status.StartupFiles {
				fmt.Fprintln(stdout, "  "+headlessTerminalText(path))
			}
		}
		fmt.Fprintln(stdout, "Then open a new "+status.Shell+" terminal; keep serve or the user service running.")
		return
	}
	// Status can fail for a wrapper conflict or an unsupported login shell.
	// Manual output is independent of the installer and contains no credentials.
	_, _, shellErr := terminalShellFiles(home, shell)
	if shellErr == nil {
		fmt.Fprintln(stdout, "  "+command+"install")
		fmt.Fprintln(stdout, "If installation reports a shell or file conflict, inspect commands status or use the manual option below.")
	} else {
		fmt.Fprintln(stdout, "Automatic installation supports Bash, Zsh, and Fish. Your login shell needs manual setup or one of these shells.")
	}
	fmt.Fprintln(stdout, "To print functions for Bash/Zsh without editing shell files:")
	fmt.Fprintln(stdout, "  "+command+"manual")
	fmt.Fprintln(stdout, "Review that output, add it to your Bash/Zsh startup file, and open a new terminal. Keep serve or the user service running.")
}

func headlessCommandActivation(result terminalCommandsInstallResult, stdout io.Writer) error {
	if _, err := fmt.Fprintln(stdout, "Updated shell startup files:"); err != nil {
		return err
	}
	for _, path := range result.StartupFiles {
		if _, err := fmt.Fprintln(stdout, "  "+headlessTerminalText(path)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(stdout, "Open a new "+headlessTerminalText(result.Shell)+" terminal, or activate this PATH in your current matching shell:"); err != nil {
		return err
	}
	if len(result.StartupFiles) > 0 {
		// Bash's first file is .bashrc; Fish's dedicated conf.d file is directly
		// sourceable. Use shell quoting rather than interpolating paths as code.
		path := result.StartupFiles[0]
		if filepath.IsAbs(path) && strings.IndexFunc(path, unicode.IsControl) == -1 {
			if _, err := fmt.Fprintln(stdout, "  source "+helperShellQuote(path)); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(stdout, "Run an agent from your project directory. Keep serve or the user service running; rerun commands install after moving the executable.")
	return err
}

func headlessWriteJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func headlessTerminalText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
}

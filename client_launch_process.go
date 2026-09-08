package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const clientLaunchRunnerFlag = "--run-client-launch"
const clientLaunchTicketLimit = 64 << 10

type clientLaunchTicket struct {
	Version int              `json:"version"`
	Expires int64            `json:"expires"`
	Plan    clientLaunchPlan `json:"plan"`
}

type clientTerminalCommand struct {
	Executable string
	Args       []string
}

// Terminal programs have different argument separators. Never interpret $TERMINAL
// as a shell command or send credentials through their command-line arguments.
func clientTerminalCommandFor(platform, binary, ticket, script string, lookup func(string) (string, error)) (clientTerminalCommand, error) {
	runner := []string{binary, clientLaunchRunnerFlag, ticket}
	if platform == "darwin" {
		return clientTerminalCommand{"/usr/bin/open", []string{"-a", "Terminal", script}}, nil
	}
	if platform != "linux" {
		return clientTerminalCommand{}, errors.New("No terminal adapter for this operating system")
	}
	for _, candidate := range []struct {
		name string
		args []string
	}{
		{"x-terminal-emulator", []string{"-e"}},
		{"gnome-terminal", []string{"--"}},
		{"konsole", []string{"-e"}},
		{"xfce4-terminal", []string{"-x"}},
		{"xterm", []string{"-e"}},
		{"kitty", nil},
		{"alacritty", []string{"-e"}},
		{"wezterm", []string{"start", "--"}},
	} {
		if executable, err := lookup(candidate.name); err == nil {
			return clientTerminalCommand{executable, append(candidate.args, runner...)}, nil
		}
	}
	return clientTerminalCommand{}, errors.New("Install a desktop terminal to launch command-line clients")
}

func clientTerminalAvailable() (bool, string) {
	if runtime.GOOS == "windows" {
		return true, "Windows console"
	}
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/System/Applications/Utilities/Terminal.app"); err == nil {
			return true, "Terminal"
		}
		return false, "Terminal is not installed"
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return false, "A desktop session is required to open a terminal"
	}
	command, err := clientTerminalCommandFor(runtime.GOOS, "", "", "", exec.LookPath)
	if err != nil {
		return false, err.Error()
	}
	return true, filepath.Base(command.Executable)
}

func clientChildEnvironment(parent []string, overrides map[string]string, unset []string, platform string) []string {
	canonical := func(name string) string {
		if platform == "windows" {
			return strings.ToUpper(name)
		}
		return name
	}
	remove := make(map[string]bool, len(unset)+len(overrides))
	for _, name := range unset {
		remove[canonical(name)] = true
	}
	keys := make([]string, 0, len(overrides))
	for name := range overrides {
		remove[canonical(name)] = true
		keys = append(keys, name)
	}
	out := make([]string, 0, len(parent)+len(overrides))
	for _, value := range parent {
		name, _, _ := strings.Cut(value, "=")
		if !remove[canonical(name)] {
			out = append(out, value)
		}
	}
	sort.Strings(keys)
	for _, name := range keys {
		out = append(out, name+"="+overrides[name])
	}
	return out
}

func clientRunnerEnvironment(parent []string, plan clientLaunchPlan) []string {
	environment := clientChildEnvironment(parent, plan.Env, plan.Unset, runtime.GOOS)
	path := ""
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		if name == "PATH" || runtime.GOOS == "windows" && strings.EqualFold(name, "PATH") {
			path = value
		}
	}
	directory := filepath.Dir(plan.Executable)
	for _, entry := range filepath.SplitList(path) {
		if entry == directory || runtime.GOOS == "windows" && strings.EqualFold(entry, directory) {
			return environment
		}
	}
	if path != "" {
		directory += string(os.PathListSeparator) + path
	}
	return clientChildEnvironment(environment, map[string]string{"PATH": directory}, nil, runtime.GOOS)
}

func validateClientProcessPlan(plan clientLaunchPlan) error {
	if plan.Kind != "terminal" && plan.Kind != "desktop" || !filepath.IsAbs(plan.Executable) || !filepath.IsAbs(plan.Directory) {
		return errors.New("Invalid client launch plan")
	}
	for _, value := range append([]string{plan.Executable, plan.Directory}, plan.Args...) {
		if strings.ContainsRune(value, 0) || len(value) > 8192 {
			return errors.New("Invalid client launch argument")
		}
	}
	if len(plan.Args) > 100 || len(plan.Env) > 100 || len(plan.Unset) > 100 {
		return errors.New("Client launch plan is too large")
	}
	for name, value := range plan.Env {
		if name == "" || strings.ContainsAny(name, "=\x00\r\n") || strings.ContainsRune(value, 0) {
			return errors.New("Invalid client environment")
		}
	}
	for _, name := range plan.Unset {
		if name == "" || strings.ContainsAny(name, "=\x00\r\n") {
			return errors.New("Invalid client environment")
		}
	}
	return nil
}

func clientLaunchScript(binary, ticket string) string {
	return "#!/bin/sh\n" + helperShellQuote(binary) + " " + clientLaunchRunnerFlag + " " + helperShellQuote(ticket) + "\nkilo_launch_status=$?\nif [ \"$kilo_launch_status\" -ne 0 ]; then\n  printf '\\nPress Return to close this launch window.\\n'\n  IFS= read -r kilo_launch_response\nfi\nexit \"$kilo_launch_status\"\n"
}

func writeClientLaunchTicket(plan clientLaunchPlan, binary, tempRoot string) (string, string, func(), error) {
	if err := validateClientProcessPlan(plan); err != nil {
		return "", "", nil, err
	}
	data, err := json.Marshal(clientLaunchTicket{Version: 1, Expires: time.Now().Add(5 * time.Minute).Unix(), Plan: plan})
	if err != nil || len(data) > clientLaunchTicketLimit {
		return "", "", nil, errors.New("Client launch plan is too large")
	}
	dir, err := os.MkdirTemp(tempRoot, "kilo-client-launch-")
	if err != nil {
		return "", "", nil, errors.New("Cannot create a private client launch ticket")
	}
	ticket, script := filepath.Join(dir, "ticket.json"), filepath.Join(dir, "bootstrap.command")
	cleanup := func() {
		_ = os.Remove(ticket)
		_ = os.Remove(script)
		_ = os.Remove(dir)
	}
	if err := os.WriteFile(ticket, data, 0600); err != nil {
		cleanup()
		return "", "", nil, errors.New("Cannot write the client launch ticket")
	}
	if err := os.WriteFile(script, []byte(clientLaunchScript(binary, ticket)), 0700); err != nil {
		cleanup()
		return "", "", nil, errors.New("Cannot write the terminal bootstrap")
	}
	return ticket, script, cleanup, nil
}

func readClientLaunchTicket(path string) (clientLaunchPlan, error) {
	var empty clientLaunchPlan
	dir := filepath.Dir(path)
	if !filepath.IsAbs(path) || filepath.Base(path) != "ticket.json" || !strings.HasPrefix(filepath.Base(dir), "kilo-client-launch-") {
		return empty, errors.New("Invalid client launch ticket path")
	}
	directory, err := os.Lstat(dir)
	if err != nil || !directory.IsDir() || directory.Mode()&os.ModeSymlink != 0 || runtime.GOOS != "windows" && directory.Mode().Perm()&0077 != 0 {
		return empty, errors.New("Unsafe client launch ticket directory")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > clientLaunchTicketLimit || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return empty, errors.New("Unsafe or expired client launch ticket")
	}
	file, err := os.Open(path)
	if err != nil {
		return empty, errors.New("Cannot open the client launch ticket")
	}
	data, readErr := io.ReadAll(io.LimitReader(file, clientLaunchTicketLimit+1))
	_ = file.Close()
	// Consume the secret before executing any client. Remove only our known files,
	// never recursively delete a directory supplied through the command line.
	if err := os.Remove(path); err != nil {
		return empty, errors.New("Cannot consume the client launch ticket")
	}
	_ = os.Remove(filepath.Join(dir, "bootstrap.command"))
	_ = os.Remove(dir)
	if readErr != nil || len(data) > clientLaunchTicketLimit {
		return empty, errors.New("Cannot read the client launch ticket")
	}
	var ticket clientLaunchTicket
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&ticket) != nil || decoder.Decode(&struct{}{}) != io.EOF || ticket.Version != 1 || ticket.Expires < time.Now().Unix() {
		return empty, errors.New("Invalid or expired client launch ticket")
	}
	if ticket.Plan.Kind != "terminal" {
		return empty, errors.New("A terminal launch ticket is required")
	}
	return ticket.Plan, validateClientProcessPlan(ticket.Plan)
}

func runClientLaunchTicket(path string, stdin io.Reader, stdout, stderr io.Writer) error {
	plan, err := readClientLaunchTicket(path)
	if err != nil {
		return err
	}
	command, cleanup, err := clientProcessCommand(plan, clientRunnerEnvironment(os.Environ(), plan))
	if err != nil {
		return err
	}
	defer cleanup()
	command.Dir = plan.Directory
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	return command.Run()
}

func runClientLaunchMode(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "A private client launch ticket is required")
		return 1
	}
	cleanup, err := prepareClientRunnerConsole()
	if err != nil {
		return 1
	}
	defer cleanup()
	if err := runClientLaunchTicket(args[0], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "Unable to launch the client:", err)
		return 1
	}
	return 0
}

func startClientLaunch(plan clientLaunchPlan) error {
	if err := validateClientProcessPlan(plan); err != nil {
		return err
	}
	if plan.Kind == "terminal" {
		binary, err := os.Executable()
		if err != nil {
			return errors.New("Cannot locate the Kilo Proxy launcher")
		}
		ticket, script, cleanup, err := writeClientLaunchTicket(plan, binary, "")
		if err != nil {
			return err
		}
		if err = startClientTerminal(binary, ticket, script); err != nil {
			cleanup()
			return err
		}
		// Also expire tickets if the terminal never executes its bootstrap.
		time.AfterFunc(5*time.Minute, cleanup)
		return nil
	}
	command, cleanup, err := clientProcessCommand(plan, clientChildEnvironment(os.Environ(), plan.Env, plan.Unset, runtime.GOOS))
	if err != nil {
		return err
	}
	command.Dir = plan.Directory
	if err := command.Start(); err != nil {
		cleanup()
		return err
	}
	go func() { _ = command.Wait(); cleanup() }()
	return nil
}

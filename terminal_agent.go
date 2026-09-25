package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func runTerminalAgentMode(args []string) int {
	cleanup, err := prepareTerminalAgentConsole()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot connect to the current terminal.")
		return 1
	}
	defer cleanup()
	if !terminalPlatformSupported(runtime.GOOS) || len(args) == 0 || !terminalClientSupported(args[0]) {
		fmt.Fprintln(os.Stderr, "Use kilo-codex, kilo-claude, kilo-omp or kilo-opencode on macOS, Linux or Windows.")
		return 1
	}
	flags := flag.NewFlagSet(terminalAgentFlag, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("config-dir", "", "Kilo Proxy configuration directory")
	encoded := flags.String("encoded-args", "", "Encoded argument array for PowerShell compatibility")
	if flags.Parse(args[1:]) != nil {
		fmt.Fprintln(os.Stderr, "Invalid terminal launcher arguments.")
		return 1
	}
	arguments := flags.Args()
	encodedSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "encoded-args" {
			encodedSet = true
		}
	})
	if encodedSet {
		if len(arguments) != 0 {
			fmt.Fprintln(os.Stderr, "Encoded terminal arguments cannot be combined with trailing arguments.")
			return 1
		}
		arguments, err = decodeTerminalArguments(*encoded)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Invalid encoded terminal arguments.")
			return 1
		}
	}
	if *dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Cannot locate Kilo Proxy settings.")
			return 1
		}
		*dir = filepath.Join(base, "kilo-proxy")
	}
	if !filepath.IsAbs(*dir) {
		fmt.Fprintln(os.Stderr, "The Kilo Proxy configuration directory must be absolute.")
		return 1
	}
	executable, err := resolveLaunchClient(args[0], "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	directory, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot read the current project directory.")
		return 1
	}
	input := terminalPrepareRequest{Client: args[0], Directory: directory}
	if input.Client == "claude" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		output, err := exec.CommandContext(ctx, executable, "--version").Output()
		cancel()
		if err == nil && len(output) < 256 {
			input.ClaudeVersion = claudeCaps(strings.TrimSpace(string(output))).Version
		}
		if len(input.ClaudeVersion) > 64 {
			input.ClaudeVersion = ""
		}
	}
	plan, err := requestTerminalPlan(context.Background(), *dir, input)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	plan.Executable = executable
	if err := validateClientProcessPlan(plan); err != nil {
		fmt.Fprintln(os.Stderr, "Invalid terminal launch parameters.")
		return 1
	}
	// The user's original arguments are already an argv vector. Preserve them
	// without applying the GUI launch-ticket size limits to prompts or lists.
	plan.Args = append(plan.Args, arguments...)
	if err := execTerminalAgent(plan); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() >= 0 {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "Could not execute the installed agent:", err)
		return 1
	}
	return 0
}

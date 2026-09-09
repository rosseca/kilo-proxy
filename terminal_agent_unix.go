//go:build !windows

package main

import (
	"os"
	"syscall"
)

func execTerminalAgent(plan clientLaunchPlan) error {
	// Replace this process: preserve the existing terminal, stdin, signals,
	// job control and the client's real exit status. No shell interprets args.
	return syscall.Exec(plan.Executable, append([]string{plan.Executable}, plan.Args...), clientRunnerEnvironment(os.Environ(), plan))
}

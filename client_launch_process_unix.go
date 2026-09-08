//go:build !windows

package main

import (
	"os/exec"
	"runtime"
)

func clientProcessCommand(plan clientLaunchPlan, environment []string) (*exec.Cmd, func(), error) {
	command := exec.Command(plan.Executable, plan.Args...)
	command.Env = environment
	return command, func() {}, nil
}

func prepareClientRunnerConsole() (func(), error) { return func() {}, nil }

func startClientTerminal(binary, ticket, script string) error {
	invocation, err := clientTerminalCommandFor(runtime.GOOS, binary, ticket, script, exec.LookPath)
	if err != nil {
		return err
	}
	command := exec.Command(invocation.Executable, invocation.Args...)
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

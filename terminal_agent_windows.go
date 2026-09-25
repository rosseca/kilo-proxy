package main

import "os"

func execTerminalAgent(plan clientLaunchPlan) error {
	command, cleanup, err := clientProcessCommand(plan, clientRunnerEnvironment(os.Environ(), plan))
	if err != nil {
		return err
	}
	defer cleanup()
	command.Dir = plan.Directory
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr

	// The child shares this console and receives its own Ctrl+C/Ctrl+Break.
	// Keep only the waiting parent alive; the inheritable ignore flag would
	// incorrectly disable the child's interrupt handling too.
	restoreInterrupts, err := preserveTerminalAgentWaiter()
	if err != nil {
		return err
	}
	defer restoreInterrupts()
	// Preserve *exec.ExitError so the terminal entrypoint can return the
	// client's actual exit status, including when it handles an interrupt.
	return command.Run()
}

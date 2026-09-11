//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
)

func execOpenDesignOpenCode(binary string, args, environment []string) (int, error) {
	cmd := exec.Command(binary, args...)
	cmd.Env = environment
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 126, err
	}
	return 0, nil
}

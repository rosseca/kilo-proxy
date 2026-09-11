//go:build !windows

package main

import "syscall"

func execOpenDesignOpenCode(binary string, args, environment []string) (int, error) {
	// Replacement preserves stdio, signals and exit status without an extra
	// process for Open Design to supervise or any shell interpretation.
	return 126, syscall.Exec(binary, append([]string{binary}, args...), environment)
}

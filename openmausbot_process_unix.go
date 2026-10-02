//go:build !windows

package main

import (
	"context"
	"errors"
	"syscall"
)

func openMausBotProcessAlive(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, err
}

func openMausBotWindowRunning(ctx context.Context, ui string) (bool, error) {
	commands, err := openDesignProcessSnapshot(ctx)
	if err != nil {
		return false, err
	}
	for _, command := range commands {
		if openMausBotWindowCommandMatches(command, ui) {
			return true, nil
		}
	}
	return false, nil
}

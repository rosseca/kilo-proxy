//go:build !windows

package main

import "context"

func synaraProcessRunning(ctx context.Context, root string) (bool, error) {
	commands, err := openDesignProcessSnapshot(ctx)
	if err != nil {
		return false, err
	}
	for _, command := range commands {
		if synaraCommandMatches(command, root) {
			return true, nil
		}
	}
	return false, nil
}

//go:build !windows

package main

import "context"

func t3CodeProcessRunning(ctx context.Context, root string) (bool, error) {
	commands, err := openDesignProcessSnapshot(ctx)
	if err != nil {
		return false, err
	}
	for _, command := range commands {
		if t3CodeCommandMatches(command, root) {
			return true, nil
		}
	}
	return false, nil
}

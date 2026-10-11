//go:build !windows

package main

import "errors"

// Native Windows process inspection is unreachable on macOS and Linux.
func claudeDesktopWindowsRunning(string) (bool, error) {
	return false, errors.New("Windows process inspection is unavailable.")
}

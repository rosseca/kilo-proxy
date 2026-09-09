//go:build desktop

package main

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

var errNativePickerCancelled = errors.New("folder selection cancelled")

func nativePickerOutput(cmd *exec.Cmd) (string, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		// Native dialogs use exit 1 (or AppleScript -128) for cancellation.
		var exit *exec.ExitError
		if errors.As(err, &exit) && (strings.Contains(stderr.String(), "(-128)") || exit.ExitCode() == 1 && filepath.Base(cmd.Path) != "osascript") {
			return "", errNativePickerCancelled
		}
		return "", errors.New("The folder chooser could not open. Enter a project folder path in Options.")
	}
	path := strings.TrimRight(string(data), "\r\n")
	if path == "" {
		return "", errNativePickerCancelled
	}
	return path, nil
}

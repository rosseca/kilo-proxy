package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"time"
)

func systemLocalePreferences() []string {
	// Both preference queries share one deadline so startup cannot hang on a
	// stalled preferences service. Neither query contains user-provided input.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	locales := appleLocalePreferences(func(key string) []byte {
		var output bytes.Buffer
		cmd := exec.CommandContext(ctx, "/usr/bin/defaults", "read", "-g", key)
		cmd.Stdout = &output
		cmd.WaitDelay = 100 * time.Millisecond
		if cmd.Run() != nil {
			return nil
		}
		return output.Bytes()
	})
	if len(locales) == 0 {
		return environmentLocalePreferences(os.Getenv)
	}
	return locales
}

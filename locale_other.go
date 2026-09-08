//go:build !darwin && !windows

package main

import "os"

func systemLocalePreferences() []string {
	return environmentLocalePreferences(os.Getenv)
}

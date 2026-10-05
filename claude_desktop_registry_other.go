//go:build !windows

package main

// The Windows branch is skipped on other platforms, including simulated
// Windows launch runtimes in portable tests. Keep native registry imports out
// of macOS and Linux builds.
func claudeDesktopWindowsManagedKeys() ([]string, error) {
	return nil, nil
}

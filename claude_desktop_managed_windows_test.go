//go:build windows

package main

import (
	"os"
	"testing"
)

// Isolated-profile acceptance tests intentionally ignore machine policies.
// Exercise the real read-only preflight separately before testing launch.
func TestClaudeDesktopWindowsManagedPreflight(t *testing.T) {
	if os.Getenv("KILO_TEST_WINDOWS_DESKTOP") != "1" {
		t.Skip("set KILO_TEST_WINDOWS_DESKTOP=1 to inspect the real Windows managed configuration")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal("cannot locate the Windows user home")
	}
	a := &app{launcher: &clientLaunchRuntime{platform: "windows", home: home}}
	if err := a.claudeDesktopManagedConfig(claudeDesktopProfilePaths{Platform: "windows"}); err != nil {
		t.Fatal("real Windows managed configuration preflight failed:", err)
	}
}

func TestClaudeDesktopWindowsManagedPreflightWithoutPowerShell(t *testing.T) {
	// GUI launches may inherit a stale PATH. Policy inspection must use the
	// native registry regardless of the availability of a shell executable.
	t.Setenv("PATH", t.TempDir())
	TestClaudeDesktopWindowsManagedPreflight(t)
}

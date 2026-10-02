package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Optional acceptance check against an already running disposable instance.
// It only inspects processes: it never starts, stops, signs in, or edits Claude.
func TestClaudeDesktopInstalledRunningProfile(t *testing.T) {
	profile := os.Getenv("KILO_TEST_CLAUDE_USER_DATA_DIR")
	if profile == "" || runtime.GOOS != "darwin" {
		t.Skip("set KILO_TEST_CLAUDE_USER_DATA_DIR to a disposable running Desktop profile")
	}
	if !filepath.IsAbs(profile) {
		t.Fatal("acceptance profile must be absolute")
	}
	if running, err := claudeDesktopRunning(profile); err != nil || !running {
		t.Fatalf("did not identify the installed private instance: running=%v err=%v", running, err)
	}
	other := filepath.Join(filepath.Dir(profile), "unopened-profile-3p")
	if running, err := claudeDesktopRunning(other); err != nil || running {
		t.Fatalf("another Claude instance blocked an unrelated profile: running=%v err=%v", running, err)
	}
}

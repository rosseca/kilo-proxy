//go:build desktop

package main

import "testing"

func TestNativeAppBundleFindsEnclosingApp(t *testing.T) {
	for path, want := range map[string]string{
		"/Applications/Claude.app":                           "/Applications/Claude.app",
		"/Applications/Codex.app/Contents/Resources/codex":   "/Applications/Codex.app",
		"/Applications/T3 Code (Nightly).app/Contents/MacOS": "/Applications/T3 Code (Nightly).app",
		"/opt/homebrew/bin/opencode":                         "",
		"relative/Zed.app":                                   "",
		"":                                                   "",
	} {
		if got := nativeAppBundle(path); got != want {
			t.Errorf("nativeAppBundle(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestUxhMonogramKeepsLettersWithoutBundle(t *testing.T) {
	u, _ := nativeLaunchTestUI(t, "codex", false, false)
	for _, key := range []string{"opencode", "omp", "generic"} {
		if bundle := u.uxhIconBundle(key); bundle != "" {
			t.Fatalf("%s should keep its monogram, got bundle %q", key, bundle)
		}
	}
	if !uxhIconBadged("claude") || !uxhIconBadged("codex-cli") || uxhIconBadged("codex") {
		t.Fatal("only the command-line harnesses carry the terminal badge")
	}
}

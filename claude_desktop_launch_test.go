package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func prepareDesktopLaunchTest(t *testing.T, a *app) {
	t.Helper()
	a.config.Language = "en"
	w := adminRequest(a, "claude-desktop/profile", `{"models":[{"id":"anthropic/claude-haiku-4.5","name":"Haiku"}],"initial":"anthropic/claude-haiku-4.5"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestClaudeDesktopLaunchUsesAppliedProfileWithoutCLIEnvironment(t *testing.T) {
	a := launchTestApp(t)
	application := filepath.Join(a.launcher.home, "Applications", "Claude.app")
	wantExecutable := "/usr/bin/open"
	wantArgs := []string{"-a", application}
	if os.PathSeparator == '\\' {
		// Process plans must use absolute paths for the host OS. A simulated
		// macOS /usr/bin/open is correctly rejected by the Windows validator.
		a.launcher.platform = "windows"
		application = filepath.Join(a.launcher.home, "Applications", "Claude.exe")
		wantExecutable, wantArgs = application, nil
	}
	a.claudeDesktopCheckRunning = func(string) (bool, error) { return false, nil }
	a.launcher.resolve = func(client, _ string) (string, error) {
		if client != "claude-desktop" {
			t.Fatal(client)
		}
		return application, nil
	}
	request := clientLaunchRequest{Client: "claude-desktop", Directory: "/not-a-supported-workspace"}
	if _, err := a.planClientLaunch(request, a.launchRuntime()); err == nil {
		t.Fatal("accepted unprepared Desktop profile")
	}
	prepareDesktopLaunchTest(t, a)
	plan, err := a.planClientLaunch(request, a.launchRuntime())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Directory != a.launcher.home || plan.Executable != wantExecutable || !reflect.DeepEqual(plan.Args, wantArgs) {
		t.Fatal("unexpected Desktop dispatch", plan)
	}
	if err := validateClientProcessPlan(plan); err != nil {
		t.Fatalf("prepared Desktop plan cannot reach the process launcher: %v", err)
	}
	if len(plan.Env) != 0 || len(plan.Unset) != 0 || plan.Kind != "desktop" {
		t.Fatal("Desktop inherited a CLI launch contract")
	}
	a.config.LocalKey = "rotated-synthetic"
	if _, err := a.planClientLaunch(request, a.launchRuntime()); err == nil {
		t.Fatal("accepted stale Desktop credential")
	}
}

func TestClaudeDesktopLaunchDoesNotCloseRunningApplication(t *testing.T) {
	for _, checkErr := range []error{nil, errors.New("synthetic inspection failure")} {
		a := launchTestApp(t)
		a.claudeDesktopCheckRunning = func(string) (bool, error) { return true, checkErr }
		launched := false
		a.launcher.start = func(clientLaunchPlan) error { launched = true; return nil }
		prepareDesktopLaunchTest(t, a)
		w := adminRequest(a, "clients/launch", `{"client":"claude-desktop"}`)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "Quit Claude Desktop") || launched || a.proxyListener != nil {
			t.Fatal("running Desktop did not stop launch safely", w.Code, w.Body.String())
		}
	}
}

func TestClaudeDesktopWindowsDiscoveryDoesNotResolveClaudeCLI(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("real Windows also checks MSIX packages")
	}
	home := t.TempDir()
	local := filepath.Join(home, "AppData", "Local")
	exe := filepath.Join(local, "AnthropicClaude", "claude.exe")
	if _, err := resolveClaudeDesktop("windows", home, local); err == nil {
		t.Fatal("accepted missing Desktop")
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("synthetic desktop"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := resolveClaudeDesktop("windows", home, local)
	if err != nil || got != exe {
		t.Fatal(got, err)
	}
}

func TestClaudeDesktopRunningMatchesOtherMacInstallations(t *testing.T) {
	// Keep the macOS process suffix while giving the fixture an absolute path
	// on the host OS, including a drive or UNC volume on Windows.
	volume := filepath.ToSlash(filepath.VolumeName(t.TempDir()))
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/Applications/Claude.app/Contents/MacOS/Claude", true},
		{"/Users/test/Applications/Claude.app/Contents/MacOS/Claude", true},
		{"/Volumes/Claude/Claude.app/Contents/MacOS/Claude", true},
		{"/Users/test/.local/bin/claude", false},
		{"/Applications/Claude.app/Contents/Frameworks/Claude Helper.app/Contents/MacOS/Claude Helper", false},
		{"/Applications/NotClaude.app/Contents/MacOS/Claude", false},
	} {
		if got := claudeDesktopProcessMatches(volume+tc.path, volume+"/Applications/Claude.app", "darwin"); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.path, got, tc.want)
		}
	}
}

func TestClaudeDesktopRunningMatchesWindowsApplication(t *testing.T) {
	application := filepath.Join(t.TempDir(), "AnthropicClaude", "Claude.exe")
	for _, tc := range []struct {
		path string
		want bool
	}{
		{application, true},
		{strings.ToUpper(application), true},
		{filepath.Join(filepath.Dir(application), "Claude Helper.exe"), false},
		{filepath.Join(filepath.Dir(application), "bin", "claude.exe"), false},
		{"claude.exe", false},
	} {
		if got := claudeDesktopProcessMatches(tc.path, application, "windows"); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.path, got, tc.want)
		}
	}
}

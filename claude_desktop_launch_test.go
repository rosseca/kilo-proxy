package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func prepareDesktopLaunchTest(t *testing.T, a *app) {
	t.Helper()
	a.config.Language = "en"
	w := adminRequest(a, "claude-desktop/profile", `{"models":[{"id":"anthropic/claude-haiku-4.5","name":"Haiku"}],"initial":"anthropic/claude-haiku-4.5"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestClaudeDesktopLaunchUsesSeparateDesktopAndCodeProfiles(t *testing.T) {
	a := launchTestApp(t)
	application := filepath.Join(a.launcher.home, "Applications", "Claude.app")
	wantExecutable := filepath.Join(application, "Contents", "MacOS", "Claude")
	switch runtime.GOOS {
	case "windows":
		a.launcher.platform = "windows"
		application = filepath.Join(a.launcher.home, "Applications", "Claude.exe")
		wantExecutable = application
	case "darwin":
		if err := os.MkdirAll(filepath.Join(application, "Contents"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(application, "Contents", "Info.plist"), []byte(`<plist><dict><key>CFBundleExecutable</key><string>Claude</string></dict></plist>`), 0600); err != nil {
			t.Fatal(err)
		}
	default:
		// Exercise the native Linux plan instead of simulating a macOS bundle,
		// whose metadata reader needs the host's PlistBuddy utility.
		a.launcher.platform = "linux"
		application = filepath.Join(a.launcher.home, "Applications", "claude-desktop")
		wantExecutable = application
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
	paths, err := a.claudeDesktopPaths()
	if err != nil {
		t.Fatal(err)
	}
	if plan.Directory != a.launcher.home || plan.Executable != wantExecutable || !reflect.DeepEqual(plan.Args, []string{"--user-data-dir=" + paths.UserDataDir}) {
		t.Fatal("unexpected Desktop dispatch", plan)
	}
	if err := validateClientProcessPlan(plan); err != nil {
		t.Fatalf("prepared Desktop plan cannot reach the process launcher: %v", err)
	}
	if plan.Env["CLAUDE_CONFIG_DIR"] != paths.ClaudeConfigDir || plan.Env["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != paths.ClaudeConfigDir || plan.Kind != "desktop" {
		t.Fatal("Desktop or embedded Code escaped the private profile")
	}
	if a.launcher.platform == "windows" && (plan.Env["LOCALAPPDATA"] != paths.LocalAppData || plan.Env["APPDATA"] != paths.AppData) {
		t.Fatal("Windows legacy or current settings escaped the private profile")
	}
	for _, name := range []string{"HOME", "CODEX_HOME", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"} {
		if _, exists := plan.Env[name]; exists {
			t.Fatal("unexpected inherited or overridden environment", name)
		}
	}
	a.config.LocalKey = "rotated-synthetic"
	if _, err := a.planClientLaunch(request, a.launchRuntime()); err == nil {
		t.Fatal("accepted stale Desktop credential")
	}
}

func TestClaudeDesktopLaunchDoesNotCloseRunningApplication(t *testing.T) {
	for _, checkErr := range []error{nil, errors.New("synthetic inspection failure")} {
		a := launchTestApp(t)
		a.claudeDesktopCheckRunning = func(string) (bool, error) { return false, nil }
		launched := false
		a.launcher.start = func(clientLaunchPlan) error { launched = true; return nil }
		prepareDesktopLaunchTest(t, a)
		a.claudeDesktopCheckRunning = func(string) (bool, error) { return true, checkErr }
		w := adminRequest(a, "clients/launch", `{"client":"claude-desktop"}`)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "Kilo Claude Desktop window") || launched || a.proxyListener != nil {
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

func TestClaudeDesktopMainExecutableIgnoresCLIAndHelpers(t *testing.T) {
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
		if got := claudeDesktopMainExecutable(volume+tc.path, "darwin"); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.path, got, tc.want)
		}
	}
}

func TestClaudeDesktopProfileArgumentScopesRunningGuard(t *testing.T) {
	for _, platform := range []string{"darwin", "windows"} {
		dir := filepath.Join(t.TempDir(), "Kilo private profile", "ui-3p")
		for _, tc := range []struct {
			command string
			want    bool
		}{
			{"Claude --user-data-dir=" + dir, true},
			{"Claude --user-data-dir=" + dir + " --other", true},
			{`Claude "--user-data-dir=` + dir + `"`, true},
			{`Claude --user-data-dir="` + dir + `"`, true},
			{"Claude", false},
			{"Claude --user-data-dir=" + filepath.Dir(dir), false},
			{"Claude --user-data-dir=" + dir + "-other", false},
			{"Claude --user-data-dir=" + dir + string(filepath.Separator) + "child", false},
			{"Claude --some-option=--user-data-dir=" + dir, false},
		} {
			if got := claudeDesktopProfileArgument(tc.command, dir, platform); got != tc.want {
				t.Fatalf("%s private profile match: got %v want %v", platform, got, tc.want)
			}
		}
		got := claudeDesktopProfileArgument(strings.ToUpper("Claude --user-data-dir="+dir), dir, platform)
		if got != (platform == "windows") {
			t.Fatal("profile case sensitivity did not follow the operating system")
		}
	}
}

func TestClaudeDesktopEnvironmentDoesNotShareRegularCredentials(t *testing.T) {
	parent := []string{"HOME=/regular/home", "PATH=/regular/bin", "CLAUDE_CONFIG_DIR=/regular/code", "CLAUDE_SECURESTORAGE_CONFIG_DIR=/regular/secure", "CLAUDE_USER_DATA_DIR=/regular/ui", "CLAUDE_CDP_AUTH=synthetic-dev-token", "ANTHROPIC_API_KEY=synthetic-api-key", "ANTHROPIC_AUTH_TOKEN=synthetic-token", "CLAUDE_CODE_OAUTH_TOKEN=synthetic-oauth", "CLAUDE_E2E_1P_USERDATA=/regular/ui", "CLAUDECODE=1", "NODE_OPTIONS=synthetic-node-options", "ELECTRON_RUN_AS_NODE=1"}
	for _, platform := range []string{"darwin", "windows"} {
		env := clientChildEnvironment(parent, map[string]string{"CLAUDE_CONFIG_DIR": "/private/code", "CLAUDE_SECURESTORAGE_CONFIG_DIR": "/private/code"}, claudeDesktopInheritedEnvironment(parent), platform)
		joined := strings.Join(env, "\n")
		if !strings.Contains(joined, "HOME=/regular/home") || !strings.Contains(joined, "PATH=/regular/bin") || !strings.Contains(joined, "CLAUDE_CONFIG_DIR=/private/code") || !strings.Contains(joined, "CLAUDE_SECURESTORAGE_CONFIG_DIR=/private/code") || strings.Contains(joined, "synthetic-") || strings.Contains(joined, "/regular/code") || strings.Contains(joined, "/regular/secure") || strings.Contains(joined, "/regular/ui") || strings.Contains(joined, "CLAUDECODE=") || strings.Contains(joined, "ELECTRON_RUN_AS_NODE=") {
			t.Fatal("regular credentials or developer overrides escaped into Desktop")
		}
	}
}

func TestClaudeDesktopCannotPrepareLivePrivateProfile(t *testing.T) {
	a, paths := desktopProfileTestApp(t)
	a.claudeDesktopCheckRunning = func(string) (bool, error) { return false, nil }
	selection := desktopProfileTestSelection()
	if _, err := a.saveClaudeDesktopProfile(selection, paths); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, checkErr := range []error{nil, errors.New("private inspection details")} {
		a.claudeDesktopCheckRunning = func(profile string) (bool, error) {
			if profile != paths.UserDataDir {
				t.Fatal("guard inspected the regular app instead of the private profile")
			}
			return true, checkErr
		}
		changed := selection
		changed.Models = append([]editorModel(nil), selection.Models...)
		changed.Models[0].Name = "Changed while open"
		if _, err := a.saveClaudeDesktopProfile(changed, paths); err == nil || strings.Contains(err.Error(), "private inspection details") {
			t.Fatal("live or unverifiable profile edit was accepted or leaked details")
		}
		if after, err := os.ReadFile(paths.ConfigPath); err != nil || !bytes.Equal(after, before) {
			t.Fatal("live profile changed")
		}
	}
}

func TestClaudeDesktopPreparationCannotRaceWithLaunch(t *testing.T) {
	a := launchTestApp(t)
	a.claudeDesktopCheckRunning = func(string) (bool, error) { return false, nil }
	prepareDesktopLaunchTest(t, a)
	paths, err := a.claudeDesktopPaths()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	starting, finish := make(chan struct{}), make(chan struct{})
	a.launcher.start = func(clientLaunchPlan) error {
		close(starting)
		<-finish
		return nil
	}
	done := make(chan int, 1)
	go func() { done <- adminRequest(a, "clients/launch", `{"client":"claude-desktop"}`).Code }()
	select {
	case <-starting:
	case <-time.After(5 * time.Second):
		close(finish)
		t.Fatal("launch did not reach the process dispatcher")
	}
	w := adminRequest(a, "claude-desktop/profile", `{"models":[{"id":"anthropic/claude-haiku-4.5","name":"Racing change"}],"initial":"anthropic/claude-haiku-4.5"}`)
	close(finish)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Another launch is already being prepared.") {
		t.Fatal("preparation raced the validated launch", w.Code, w.Body.String())
	}
	if status := <-done; status != 200 {
		t.Fatal("launch failed", status)
	}
	if after, err := os.ReadFile(paths.ConfigPath); err != nil || !bytes.Equal(after, before) {
		t.Fatal("preparation changed the launch's validated configuration")
	}
}

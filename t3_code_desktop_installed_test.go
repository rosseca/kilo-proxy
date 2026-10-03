//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This acceptance opens only a disposable T3 window. Its normal and Kilo
// provider homes all live in fixtures; real user homes and sessions are unused.
func TestT3CodeInstalledDesktop(t *testing.T) {
	installed := os.Getenv("KILO_TEST_T3_DESKTOP")
	if installed == "" {
		t.Skip("set KILO_TEST_T3_DESKTOP to an installed macOS T3 app bundle")
	}
	if runtime.GOOS != "darwin" || !filepath.IsAbs(installed) {
		t.Fatal("the desktop acceptance requires an absolute macOS app bundle")
	}
	if err := t3CodeCompatibility(installed, runtime.GOOS); err != nil {
		t.Fatal(err)
	}
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	codex, err := resolveOpenDesignCLI("codex-cli", clientLaunchRuntime{platform: runtime.GOOS, home: realHome, resolve: resolveLaunchClient})
	if err != nil {
		t.Fatal(err)
	}
	claude, err := resolveLaunchClient("claude", "")
	if err != nil {
		t.Fatal(err)
	}
	a := launchTestApp(t)
	a.config.Port = 9988
	a.config.LocalKey = "synthetic-t3-desktop-local"
	a.launcher.platform = runtime.GOOS
	a.launcher.resolve = func(id, custom string) (string, error) {
		switch id {
		case "t3-code":
			return installed, nil
		case "codex-cli":
			return codex, nil
		case "claude":
			return claude, nil
		}
		return "", errors.New("unexpected disposable T3 fixture client")
	}
	for _, name := range []string{"USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		t.Setenv(name, filepath.Join(a.launcher.home, name))
	}
	if _, err := a.modelLibrary.save(testModelLibrary(), 0, false); err != nil {
		t.Fatal(err)
	}
	prepareT3CodeFixture(t, a)
	rt := a.launchRuntime()
	plan, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, rt)
	if err != nil {
		t.Fatal(err)
	}
	paths := t3CodePaths(a.dir)
	command := exec.Command(plan.Executable, plan.Args...)
	command.Dir = plan.Directory
	command.Env = clientRunnerEnvironment(os.Environ(), plan)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	pid := command.Process.Pid
	exited := make(chan error, 1)
	go func() { exited <- command.Wait(); close(exited) }()
	backendPID := 0
	defer func() {
		// Only the process started by this acceptance may be killed, including
		// its recorded child if graceful shutdown fails. Product guards never
		// terminate processes discovered from runtime records.
		_ = t3CodeQuitOwnedDesktop(pid)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = command.Process.Kill()
			<-exited
		}
		if backendPID > 0 {
			if alive, _ := openMausBotProcessAlive(backendPID); alive {
				if process, err := os.FindProcess(backendPID); err == nil {
					_ = process.Kill()
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}()
	version, _ := t3CodeVersion(installed, runtime.GOOS)
	uiName := "t3code"
	if version == t3CodeNightlyVersion {
		uiName = "t3code-v2"
	}
	uiPath := filepath.Join(paths.UIHome, "Library", "Application Support", uiName)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := readOpenDesignShimFile(filepath.Join(paths.Data, "userdata", "server-runtime.json"), 16<<10)
		var state struct{ PID int }
		_ = json.Unmarshal(data, &state)
		backendPID = state.PID
		info, pathErr := os.Stat(uiPath)
		if backendPID > 0 && pathErr == nil && info.IsDir() {
			break
		}
		select {
		case err := <-exited:
			t.Fatal("private T3 desktop exited during startup", err)
		case <-time.After(250 * time.Millisecond):
		}
	}
	if backendPID < 1 {
		t.Fatal("private desktop backend did not record its runtime")
	}
	if info, err := os.Stat(uiPath); err != nil || !info.IsDir() {
		t.Fatal("desktop did not create the private Electron UI directory", err)
	}
	if running, err := t3CodeRunning(paths.Root); err != nil || !running {
		t.Fatal("private desktop main/backend not detected", running, err)
	}
	if commands, err := openDesignProcessSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	} else {
		matched := false
		for _, line := range commands {
			matched = matched || t3CodeCommandMatches(line, paths.Root)
		}
		if !matched {
			t.Fatal("native Electron marker not present on the private process")
		}
	}
	windowCount := 0
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		count, queryErr := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", `ObjC.import('CoreGraphics'); var windows=ObjC.deepUnwrap(ObjC.castRefToObject($.CGWindowListCopyWindowInfo(0,0))); windows.filter(function(w){return w.kCGWindowOwnerPID===`+strconv.Itoa(pid)+` && w.kCGWindowLayer===0 && w.kCGWindowBounds.Height>100;}).length;`).CombinedOutput()
		cancel()
		if queryErr != nil {
			t.Fatal("private T3 desktop window query failed", queryErr, string(count))
		}
		windowCount, _ = strconv.Atoi(strings.TrimSpace(string(count)))
		if windowCount > 0 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if windowCount < 1 {
		t.Fatal("private T3 desktop window not observed")
	}
	if err := t3CodeQuitOwnedDesktop(pid); err != nil {
		t.Fatal("owned T3 desktop did not accept a graceful quit", err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatal("owned T3 desktop exited with an error", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("owned T3 desktop did not quit")
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		alive, _ := openMausBotProcessAlive(backendPID)
		if !alive {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if alive, err := openMausBotProcessAlive(backendPID); err != nil || alive {
		t.Fatal("owned T3 backend survived graceful desktop quit", alive, err)
	}
	if running, err := t3CodeRunning(paths.Root); err != nil || running {
		t.Fatal("private T3 guard did not clear after clean quit", running, err)
	}
	if saved, err := a.readT3CodePrepared(); err != nil || !a.t3CodeReady(saved, installed, rt) {
		t.Fatal("installed desktop changed managed configuration", err)
	}
	t.Log("private Electron UI, native window, exact marker, server PID guard and graceful child shutdown verified")
}

func t3CodeQuitOwnedDesktop(pid int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", `ObjC.import('AppKit'); var app=$.NSRunningApplication.runningApplicationWithProcessIdentifier(`+strconv.Itoa(pid)+`); if (!app.isNil()) { app.terminate; }`).Run()
}

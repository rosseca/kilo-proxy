//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func windowsDiscoveryFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("synthetic Windows executable"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsCodexStoreDiscoverySeparatesDesktopAndCLI(t *testing.T) {
	base := filepath.Join(t.TempDir(), "WindowsApps", "OpenAI.Codex_1.2.3.4_x64__publisher")
	cli := filepath.Join(base, "app", "resources", "codex.exe")
	windowsDiscoveryFixture(t, cli)
	if got := windowsClientExecutable(windowsCodexDesktopPaths("", "", "", []string{base})); got != "" {
		t.Fatal("Store's terminal binary was mistaken for Codex Desktop", got)
	}
	if got := windowsClientExecutable(windowsCodexCLIPaths("", []string{base})); got != cli {
		t.Fatal("bundled CLI was not discovered", got)
	}
	desktop := filepath.Join(base, "app", "ChatGPT.exe")
	windowsDiscoveryFixture(t, desktop)
	windowsDiscoveryFixture(t, filepath.Join(base, "app", "Codex.exe"))
	if got := windowsClientExecutable(windowsCodexDesktopPaths("", "", "", []string{base})); got != desktop {
		t.Fatal("Store's manifest desktop entry point was not selected", got)
	}
	if err := os.Remove(desktop); err != nil {
		t.Fatal(err)
	}
	if got := windowsClientExecutable(windowsCodexDesktopPaths("", "", "", []string{base})); got != filepath.Join(base, "app", "Codex.exe") {
		t.Fatal("older Store's Codex.exe layout was not discovered", got)
	}
}

func TestWindowsCodexLocalDiscoveryWithoutUpdatedPATH(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, "AppData", "Local")
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("PATH", "")
	t.Setenv("ProgramFiles", "")
	t.Setenv("ProgramFiles(x86)", "")
	desktop := filepath.Join(local, "Programs", "Codex", "Codex.exe")
	windowsDiscoveryFixture(t, desktop)
	if got, err := resolveLaunchClient("codex", ""); err != nil || got != desktop {
		t.Fatal("local desktop was not discovered", got, err)
	}
	oldCLI := filepath.Join(local, "OpenAI", "Codex", "bin", "old-build", "codex.exe")
	newCLI := filepath.Join(local, "OpenAI", "Codex", "bin", "new-build", "codex.exe")
	windowsDiscoveryFixture(t, oldCLI)
	windowsDiscoveryFixture(t, newCLI)
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldCLI, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveLaunchClient("codex-cli", ""); err != nil || got != newCLI {
		t.Fatal("newest CLI installed by Desktop was not discovered", got, err)
	}
	stableCLI := filepath.Join(local, "OpenAI", "Codex", "codex.exe")
	windowsDiscoveryFixture(t, stableCLI)
	if got := resolveWindowsCodexCLI(home, ""); got != stableCLI {
		t.Fatal("stable CLI path or home fallback was not preferred", got)
	}
}

func TestWindowsCodexDiscoveryIncludesSystemInstallations(t *testing.T) {
	programFiles := t.TempDir()
	desktop := filepath.Join(programFiles, "Codex", "Codex.exe")
	windowsDiscoveryFixture(t, desktop)
	if got := windowsClientExecutable(windowsCodexDesktopPaths("", programFiles, "", nil)); got != desktop {
		t.Fatal("system-wide Codex was not discovered", got)
	}
	if paths := windowsCodexDesktopPaths("relative", "relative", "relative", []string{"relative"}); len(paths) != 0 {
		t.Fatal("relative install roots accepted", paths)
	}
}

func TestWindowsStoreDiscoveryPreservesUnicodeAndRejectsInvalidPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "José 日本語", "OpenAI.Codex")
	data, err := json.Marshal([]string{path, "relative", path + "\nextra", path + "\x00extra"})
	if err != nil {
		t.Fatal(err)
	}
	got := parseWindowsAppPackagePaths(data)
	if len(got) != 1 || got[0] != path {
		t.Fatal("Store installation paths were corrupted or unsafe", got)
	}
	for _, data := range [][]byte{[]byte(`"single-path"`), []byte(`{"path":"C:\\App"}`), []byte(strings.Repeat(" ", (16<<10)+1))} {
		if paths := parseWindowsAppPackagePaths(data); len(paths) != 0 {
			t.Fatal("invalid Store query response was accepted", paths)
		}
	}
	if paths := queryWindowsAppPackagePaths("Codex'; Start-Process something; '"); len(paths) != 0 {
		t.Fatal("arbitrary package query was accepted")
	}
}

func TestInstalledWindowsCodexDiscovery(t *testing.T) {
	if os.Getenv("KILO_TEST_INSTALLED_CODEX_DISCOVERY") != "1" {
		t.Skip("set KILO_TEST_INSTALLED_CODEX_DISCOVERY=1 to inspect installed Windows Codex")
	}
	desktop, err := resolveLaunchClient("codex", "")
	if err != nil || !launchExecutable(desktop) {
		t.Fatal("installed Codex Desktop is not discoverable", err)
	}
	if strings.Contains(strings.ToLower(desktop), `\resources\`) || strings.Contains(strings.ToLower(desktop), `\bin\`) {
		t.Fatal("CLI executable was returned for Codex Desktop")
	}
	cli, err := resolveLaunchClient("codex-cli", "")
	if err != nil || !launchExecutable(cli) || strings.EqualFold(cli, desktop) {
		t.Fatal("installed Codex CLI is not discoverable independently", err)
	}
	t.Log("Codex Desktop:", desktop)
	t.Log("Codex CLI:", cli)
}

// This opt-in acceptance check starts only a disposable Codex window. It uses
// synthetic credentials, performs no inference, and never opens normal data.
func TestInstalledWindowsCodexLaunch(t *testing.T) {
	if os.Getenv("KILO_TEST_WINDOWS_DESKTOP") != "1" {
		t.Skip("set KILO_TEST_WINDOWS_DESKTOP=1 to open installed Codex with a disposable profile")
	}
	installed, err := resolveLaunchClient("codex", "")
	if err != nil {
		t.Fatal("installed Codex Desktop is not discoverable", err)
	}
	cli, _ := resolveLaunchClient("codex-cli", "")
	a := launchTestApp(t)
	a.launcher.platform = "windows"
	a.launcher.resolve = func(string, string) (string, error) { return installed, nil }
	// Plan creation must also keep its Windows data directory in the temporary
	// home, before the explicit private profile arguments are passed to Codex.
	t.Setenv("LOCALAPPDATA", filepath.Join(a.launcher.home, "AppData", "Local"))
	launchPrepareFixture(t, a, "codex")
	plan, err := a.planClientLaunch(clientLaunchRequest{Client: "codex"}, a.launchRuntime())
	if err != nil {
		t.Fatal("cannot prepare disposable Codex launch", err)
	}
	profile := plan.Env["CODEX_ELECTRON_USER_DATA_PATH"]
	if profile == "" || !strings.HasPrefix(profile, a.launcher.home+string(filepath.Separator)) || plan.Env["CODEX_HOME"] != a.codexProfileDir {
		t.Fatal("disposable Codex launch did not isolate both profiles")
	}
	for _, name := range []string{"config.toml", "models.json"} {
		if st, err := os.Stat(filepath.Join(plan.Env["CODEX_HOME"], name)); err != nil || !st.Mode().IsRegular() {
			t.Fatal("disposable CODEX_HOME is missing its prepared profile")
		}
	}
	owned := map[uint32]windows.Handle{}
	notBefore := uint64(time.Now().UnixNano()/100 + 116444736000000000)
	t.Cleanup(func() {
		for attempt := 0; attempt < 5; attempt++ {
			if processes, err := windowsCodexAcceptanceProcesses(); err == nil {
				windowsCodexAcceptanceTrack(processes, installed, cli, profile, notBefore, owned)
			}
			for _, handle := range owned {
				_ = windows.TerminateProcess(handle, 0)
			}
			if !windowsDesktopAcceptanceAlive(owned) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		for _, handle := range owned {
			_, _ = windows.WaitForSingleObject(handle, 2000)
			_ = windows.CloseHandle(handle)
		}
	})
	if err := startClientLaunch(plan); err != nil {
		t.Fatal("cannot open disposable installed Codex", err)
	}
	deadline := time.Now().Add(40 * time.Second)
	var running, privateState, visible bool
	for time.Now().Before(deadline) {
		processes, err := windowsCodexAcceptanceProcesses()
		if err != nil {
			t.Fatal("cannot inspect disposable installed Codex", err)
		}
		windowsCodexAcceptanceTrack(processes, installed, cli, profile, notBefore, owned)
		running = windowsDesktopAcceptanceAlive(owned)
		visible = windowsDesktopAcceptanceWindow(owned)
		for _, name := range []string{"Local State", "Preferences", "Default/Preferences"} {
			if st, err := os.Stat(filepath.Join(profile, filepath.FromSlash(name))); err == nil && st.Mode().IsRegular() && st.Size() > 0 {
				privateState = true
			}
		}
		if running && privateState && visible {
			t.Log("installed Windows Codex opened its private window and wrote its own application state with a prepared synthetic CODEX_HOME")
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("installed Codex did not finish opening: process=%v private-state=%v visible-window=%v; private log summary: %s", running, privateState, visible, windowsDesktopAcceptanceLogs(a.launcher.home))
}

func windowsCodexAcceptanceProcesses() ([]windowsDesktopAcceptanceProcess, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	script := `$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); $p=@(Get-CimInstance Win32_Process -Filter "Name='ChatGPT.exe' OR Name='Codex.exe'" | ForEach-Object { [pscustomobject]@{ProcessID=$_.ProcessID; ParentProcessID=$_.ParentProcessID; ExecutablePath=$_.ExecutablePath; CommandLine=$_.CommandLine; CreationTime=$_.CreationDate.ToFileTimeUtc().ToString()} }); ConvertTo-Json -InputObject $p -Compress`
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	data, err := command.Output()
	if err != nil || len(data) > 4<<20 {
		return nil, fmt.Errorf("Windows Codex process query failed")
	}
	var processes []windowsDesktopAcceptanceProcess
	if json.Unmarshal(data, &processes) != nil {
		return nil, fmt.Errorf("Windows Codex process query returned invalid metadata")
	}
	return processes, nil
}

func windowsCodexAcceptanceTrack(processes []windowsDesktopAcceptanceProcess, executable, cli, profile string, notBefore uint64, owned map[uint32]windows.Handle) {
	for changed := true; changed; {
		changed = false
		for _, process := range processes {
			if _, exists := owned[process.ProcessID]; exists {
				continue
			}
			main := strings.EqualFold(process.ExecutablePath, executable)
			backend := strings.EqualFold(process.ExecutablePath, cli) || strings.EqualFold(process.ExecutablePath, filepath.Join(filepath.Dir(executable), "resources", "codex.exe"))
			if !main && !backend {
				continue
			}
			parent, child := owned[process.ParentProcessID]
			if child {
				var code uint32
				child = windows.GetExitCodeProcess(parent, &code) == nil && code == 259
			}
			if !child && (!main || !claudeDesktopProfileArgument(process.CommandLine, profile, "windows")) {
				continue
			}
			creation, err := strconv.ParseUint(process.CreationTime, 10, 64)
			if err != nil || creation < notBefore {
				continue
			}
			handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, process.ProcessID)
			if err != nil {
				continue
			}
			var created, exited, kernel, user windows.Filetime
			if windows.GetProcessTimes(handle, &created, &exited, &kernel, &user) != nil || (uint64(created.HighDateTime)<<32|uint64(created.LowDateTime))/10 != creation/10 {
				_ = windows.CloseHandle(handle)
				continue
			}
			owned[process.ProcessID] = handle
			changed = true
		}
	}
}

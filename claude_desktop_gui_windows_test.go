//go:build windows

package main

import (
	"bytes"
	"debug/pe"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Exercise the packaged GUI process, whose managed-policy checks use the real
// Windows home even though all mutable files belong to a disposable config-dir.
// No upstream credentials are supplied: the launch API must reach the missing-
// credentials guard. Desktop then opens the GUI-prepared profile against a
// synthetic local gateway, and the GUI API must detect that live private window.
func TestClaudeDesktopWindowsProductionGUI(t *testing.T) {
	binary := os.Getenv("KILO_TEST_WINDOWS_PROXY_BINARY")
	if os.Getenv("KILO_TEST_WINDOWS_DESKTOP") != "1" || binary == "" {
		t.Skip("set KILO_TEST_WINDOWS_DESKTOP=1 and KILO_TEST_WINDOWS_PROXY_BINARY to inspect the packaged GUI with a disposable Claude profile")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("the production GUI executable must be absolute")
	}
	program, err := pe.Open(binary)
	if err != nil {
		t.Fatal("cannot inspect the production GUI executable")
	}
	header, ok := program.OptionalHeader.(*pe.OptionalHeader64)
	gui := ok && header.Subsystem == pe.IMAGE_SUBSYSTEM_WINDOWS_GUI
	_ = program.Close()
	if !gui {
		t.Fatal("this acceptance check requires the packaged Windows GUI executable")
	}

	root := t.TempDir()
	configDir := filepath.Join(root, "GUI profile with spaces café")
	noShell := filepath.Join(root, "empty-path")
	if err := os.Mkdir(noShell, 0700); err != nil {
		t.Fatal("cannot prepare the shell-free fixture environment")
	}
	const fakeKey = "synthetic-windows-gui-desktop-acceptance-key"
	var probes atomic.Int64
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal("cannot open the synthetic GUI loopback gateway")
	}
	gateway := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+fakeKey {
			http.Error(w, "synthetic authentication rejected", http.StatusUnauthorized)
			return
		}
		var request struct {
			Model string `json:"model"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request) != nil {
			http.Error(w, "invalid synthetic request", http.StatusBadRequest)
			return
		}
		probes.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_windows_gui_acceptance", "type": "message", "role": "assistant", "model": request.Model,
			"content":     []map[string]string{{"type": "text", "text": "Synthetic GUI acceptance response"}},
			"stop_reason": "end_turn", "stop_sequence": nil,
			"usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	gateway.Listener = listener
	gateway.Start()
	t.Cleanup(gateway.Close)
	cfg, err := readSettings(configDir)
	if err != nil {
		t.Fatal("cannot initialize disposable GUI settings")
	}
	cfg.Language, cfg.LocalKey, cfg.Remember = "en", fakeKey, false
	cfg.Port = listener.Addr().(*net.TCPAddr).Port
	if err := writeSettings(configDir, cfg); err != nil {
		t.Fatal("cannot save disposable GUI settings")
	}

	command := exec.Command(binary, "--no-browser", "--config-dir", configDir)
	command.Env = clientChildEnvironment(os.Environ(), map[string]string{"PATH": noShell}, nil, "windows")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	// The control-panel URL contains its bearer token. Do not capture or emit
	// stdout/stderr; read the private authenticated runtime descriptor instead.
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		t.Fatal("cannot start the production GUI fixture")
	}
	done := make(chan struct{})
	go func() { _ = command.Wait(); close(done) }()
	var connection terminalRuntime
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}}
	t.Cleanup(func() {
		if connection.Host != "" {
			_, _ = windowsDesktopGUIRequest(client, connection, http.MethodPost, "/api/quit", map[string]any{})
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill() // The retained os.Process belongs only to this fixture.
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("the production GUI fixture did not exit")
			}
		}
		client.CloseIdleConnections()
	})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if connection, err = readTerminalRuntime(configDir); err == nil {
			break
		}
		select {
		case <-done:
			t.Fatal("the production GUI exited before publishing its private API connection")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err != nil {
		t.Fatal("the production GUI did not publish its private API connection")
	}

	fixture := &app{dir: configDir, config: cfg}
	paths, err := fixture.claudeDesktopPaths()
	if err != nil {
		t.Fatal("cannot locate the disposable GUI-prepared profile")
	}
	selection := desktopProfileTestSelection()
	prepared, err := windowsDesktopGUIRequest(client, connection, http.MethodPost, "/api/claude-desktop/profile", selection)
	if err != nil || prepared.Status != http.StatusOK || !prepared.OK || !strings.EqualFold(filepath.Clean(prepared.ConfigPath), paths.ConfigPath) || !strings.EqualFold(filepath.Clean(prepared.ProfileDir), paths.ProfileDir) {
		t.Fatalf("GUI preparation did not pass the real Windows managed and running preflight: status=%d", prepared.Status)
	}
	verified, err := windowsDesktopGUIRequest(client, connection, http.MethodGet, "/api/claude-desktop/profile", nil)
	if err != nil || verified.Status != http.StatusOK || !verified.OK {
		t.Fatalf("GUI verification did not pass the real Windows managed preflight: status=%d", verified.Status)
	}
	launch, err := windowsDesktopGUIRequest(client, connection, http.MethodPost, "/api/clients/launch", clientLaunchRequest{Client: "claude-desktop"})
	if err != nil || launch.Status != http.StatusBadRequest || launch.Error.Code != "proxy_credentials_missing" {
		t.Fatalf("GUI launch did not reach the credentials guard after managed and running checks: status=%d", launch.Status)
	}

	plan, err := fixture.planClientLaunch(clientLaunchRequest{Client: "claude-desktop"}, fixture.launchRuntime())
	if err != nil {
		t.Fatal("cannot plan the GUI-prepared private Desktop fixture")
	}
	plan.Env["PATH"] = noShell
	owned := map[uint32]windows.Handle{}
	t.Cleanup(func() {
		for attempt := 0; attempt < 5; attempt++ {
			processes, queryErr := windowsDesktopAcceptanceProcesses()
			if queryErr == nil {
				windowsDesktopAcceptanceTrack(processes, plan.Executable, paths.UserDataDir, owned)
			}
			for _, handle := range owned {
				_ = windows.TerminateProcess(handle, 0)
			}
			if queryErr == nil && !windowsDesktopAcceptanceAlive(owned) {
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
		t.Fatal("cannot open Desktop with the GUI-prepared synthetic profile")
	}
	deadline = time.Now().Add(45 * time.Second)
	var running, window bool
	for time.Now().Before(deadline) {
		processes, err := windowsDesktopAcceptanceProcesses()
		if err != nil {
			t.Fatal("cannot inspect the GUI-prepared private Desktop fixture")
		}
		windowsDesktopAcceptanceTrack(processes, plan.Executable, paths.UserDataDir, owned)
		running, window = windowsDesktopAcceptanceAlive(owned), windowsDesktopAcceptanceWindow(owned)
		if running && window && probes.Load() > 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !running || !window || probes.Load() == 0 {
		t.Fatalf("GUI-prepared Desktop did not open against the synthetic gateway: process=%v visible-window=%v authenticated-probes=%d", running, window, probes.Load())
	}
	launch, err = windowsDesktopGUIRequest(client, connection, http.MethodPost, "/api/clients/launch", clientLaunchRequest{Client: "claude-desktop"})
	if err != nil || launch.Status != http.StatusConflict || !strings.Contains(launch.Error.Message, "Quit the Kilo Claude Desktop window") {
		t.Fatalf("GUI launch did not detect its already running private Desktop window without PowerShell on PATH: status=%d", launch.Status)
	}
	prepared, err = windowsDesktopGUIRequest(client, connection, http.MethodPost, "/api/claude-desktop/profile", selection)
	if err != nil || prepared.Status != http.StatusConflict || !strings.Contains(prepared.Error.Message, "Close the Kilo Claude Desktop window") {
		t.Fatalf("GUI preparation did not reject the running private Desktop profile: status=%d", prepared.Status)
	}
	t.Log("production GUI prepared and verified real Windows policies without a shell on PATH, reached the credentials guard, and recognized its synthetic private Desktop window")
}

type windowsDesktopGUIResponse struct {
	Status     int `json:"-"`
	OK         bool
	ConfigPath string
	ProfileDir string
	Error      struct{ Code, Message string }
}

func windowsDesktopGUIRequest(client *http.Client, connection terminalRuntime, method, path string, payload any) (windowsDesktopGUIResponse, error) {
	var result windowsDesktopGUIResponse
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return result, err
		}
	}
	request, err := http.NewRequest(method, "http://"+connection.Host+path, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	request.Header.Set("Authorization", "Bearer "+connection.Token)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	result.Status = response.StatusCode
	err = json.NewDecoder(io.LimitReader(response.Body, catalogLimit)).Decode(&result)
	return result, err
}

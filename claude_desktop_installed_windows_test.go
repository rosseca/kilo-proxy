//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// This opt-in test opens an installed Claude Desktop window with disposable
// profiles and a synthetic local gateway. It never reads ordinary Claude data,
// sends inference to a remote provider, or terminates an ordinary Claude instance.
func TestClaudeDesktopInstalledWindowsLaunch(t *testing.T) {
	if os.Getenv("KILO_TEST_WINDOWS_DESKTOP") != "1" {
		t.Skip("set KILO_TEST_WINDOWS_DESKTOP=1 to open the installed Windows Desktop with a disposable profile")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal("cannot locate the Windows home directory")
	}
	preflight := &app{launcher: &clientLaunchRuntime{platform: "windows", home: home}}
	if err := preflight.claudeDesktopManagedConfig(claudeDesktopProfilePaths{Platform: "windows"}); err != nil {
		t.Fatal("installed Windows managed configuration preflight failed:", err)
	}
	installed, err := resolveClaudeDesktop("windows", home, os.Getenv("LOCALAPPDATA"))
	if err != nil {
		t.Fatal("Windows Claude Desktop discovery failed:", err)
	}

	const fakeKey = "synthetic-windows-desktop-acceptance-key"
	var probes atomic.Int64
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal("cannot open the synthetic loopback gateway")
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
			"id": "msg_windows_acceptance", "type": "message", "role": "assistant", "model": request.Model,
			"content":     []map[string]string{{"type": "text", "text": "Synthetic Windows acceptance response"}},
			"stop_reason": "end_turn", "stop_sequence": nil,
			"usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	gateway.Listener = listener
	gateway.Start()
	t.Cleanup(gateway.Close)

	a := testApp(t)
	a.config.Language = "en"
	a.config.Port = listener.Addr().(*net.TCPAddr).Port
	a.config.LocalKey = fakeKey
	a.editorTestRoot = t.TempDir()
	a.launcher = &clientLaunchRuntime{platform: "windows", home: a.editorTestRoot,
		resolve: func(client, _ string) (string, error) {
			if client != "claude-desktop" {
				return "", fmt.Errorf("unexpected acceptance client")
			}
			return installed, nil
		},
	}
	paths, err := a.claudeDesktopPaths()
	if err != nil {
		t.Fatal(err)
	}
	owned := map[uint32]windows.Handle{}
	t.Cleanup(func() {
		// A retained handle and checked creation time bind termination to the
		// actual disposable instance, even if Windows later reuses its PID.
		for attempt := 0; attempt < 5; attempt++ {
			processes, queryErr := windowsDesktopAcceptanceProcesses()
			if queryErr == nil {
				windowsDesktopAcceptanceTrack(processes, installed, paths.UserDataDir, owned)
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

	if _, err := a.saveClaudeDesktopProfile(desktopProfileTestSelection(), paths); err != nil {
		t.Fatal("cannot prepare the disposable Windows Desktop profile:", err)
	}
	plan, err := a.planClientLaunch(clientLaunchRequest{Client: "claude-desktop"}, a.launchRuntime())
	if err != nil {
		t.Fatal("cannot plan the installed Windows Desktop launch:", err)
	}
	if err := startClientLaunch(plan); err != nil {
		t.Fatal("cannot start the installed Windows Desktop:", err)
	}

	deadline := time.Now().Add(45 * time.Second)
	var running, profileWritten, window bool
	for time.Now().Before(deadline) {
		processes, err := windowsDesktopAcceptanceProcesses()
		if err != nil {
			t.Fatal("cannot inspect the disposable Windows Desktop:", err)
		}
		windowsDesktopAcceptanceTrack(processes, installed, paths.UserDataDir, owned)
		running = windowsDesktopAcceptanceAlive(owned)
		window = windowsDesktopAcceptanceWindow(owned)
		for _, name := range []string{"Local State", "Preferences", "Default/Preferences"} {
			if st, err := os.Stat(filepath.Join(paths.UserDataDir, filepath.FromSlash(name))); err == nil && st.Mode().IsRegular() && st.Size() > 0 {
				profileWritten = true
			}
		}
		if running && profileWritten && window && probes.Load() > 0 {
			if live, err := claudeDesktopRunning(paths.UserDataDir); err != nil || !live {
				t.Fatal("production running-profile detection did not identify the opened Windows Desktop")
			}
			t.Log("installed Windows Desktop opened its private window, wrote Electron state, and authenticated with the synthetic gateway")
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("installed Windows Desktop did not finish opening: process=%v private-state=%v visible-window=%v authenticated-probes=%d; private log summary: %s", running, profileWritten, window, probes.Load(), windowsDesktopAcceptanceLogs(a.dir))
}

type windowsDesktopAcceptanceProcess struct {
	ProcessID, ParentProcessID  uint32
	ExecutablePath, CommandLine string
	CreationTime                string
}

func windowsDesktopAcceptanceProcesses() ([]windowsDesktopAcceptanceProcess, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	script := `$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); $p=@(Get-CimInstance Win32_Process -Filter "Name='Claude.exe'" | ForEach-Object { [pscustomobject]@{ProcessID=$_.ProcessID; ParentProcessID=$_.ParentProcessID; ExecutablePath=$_.ExecutablePath; CommandLine=$_.CommandLine; CreationTime=$_.CreationDate.ToFileTimeUtc().ToString()} }); ConvertTo-Json -InputObject $p -Compress`
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	data, err := command.Output()
	if err != nil || len(data) > 4<<20 {
		return nil, fmt.Errorf("Windows process query failed")
	}
	var processes []windowsDesktopAcceptanceProcess
	if json.Unmarshal(data, &processes) != nil {
		return nil, fmt.Errorf("Windows process query returned invalid metadata")
	}
	return processes, nil
}

func windowsDesktopAcceptanceTrack(processes []windowsDesktopAcceptanceProcess, executable, profile string, owned map[uint32]windows.Handle) {
	for changed := true; changed; {
		changed = false
		for _, process := range processes {
			if _, exists := owned[process.ProcessID]; exists || !strings.EqualFold(process.ExecutablePath, executable) {
				continue
			}
			parent, child := owned[process.ParentProcessID]
			var parentCode uint32
			child = child && windows.GetExitCodeProcess(parent, &parentCode) == nil && parentCode == 259
			if !child && !claudeDesktopProfileArgument(process.CommandLine, profile, "windows") {
				continue
			}
			creation, err := strconv.ParseUint(process.CreationTime, 10, 64)
			if err != nil {
				continue
			}
			handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, process.ProcessID)
			if err != nil {
				continue
			}
			var created, exited, kernel, user windows.Filetime
			// CIM datetime preserves microseconds; FILETIME also has a final
			// 100 ns digit. Compare at the precision shared by both sources.
			if windows.GetProcessTimes(handle, &created, &exited, &kernel, &user) != nil {
				_ = windows.CloseHandle(handle)
				continue
			}
			actualCreation := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
			if actualCreation/10 != creation/10 {
				_ = windows.CloseHandle(handle)
				continue
			}
			owned[process.ProcessID] = handle
			changed = true
		}
	}
}

func windowsDesktopAcceptanceAlive(owned map[uint32]windows.Handle) bool {
	for _, handle := range owned {
		var code uint32
		if windows.GetExitCodeProcess(handle, &code) == nil && code == 259 {
			return true
		}
	}
	return false
}

func windowsDesktopAcceptanceWindow(owned map[uint32]windows.Handle) bool {
	user32 := windows.NewLazySystemDLL("user32.dll")
	enumWindows := user32.NewProc("EnumWindows")
	windowProcess := user32.NewProc("GetWindowThreadProcessId")
	visible := user32.NewProc("IsWindowVisible")
	windowRect := user32.NewProc("GetWindowRect")
	found := false
	callback := syscall.NewCallback(func(window, _ uintptr) uintptr {
		var pid uint32
		windowProcess.Call(window, uintptr(unsafe.Pointer(&pid)))
		if handle, owned := owned[pid]; owned {
			var code uint32
			shown, _, _ := visible.Call(window)
			var rect struct{ Left, Top, Right, Bottom int32 }
			ok, _, _ := windowRect.Call(window, uintptr(unsafe.Pointer(&rect)))
			if windows.GetExitCodeProcess(handle, &code) == nil && code == 259 && shown != 0 && ok != 0 && rect.Right-rect.Left > 100 && rect.Bottom-rect.Top > 100 {
				found = true
				return 0
			}
		}
		return 1
	})
	enumWindows.Call(callback, 0)
	return found
}

// Emit counts and known error categories only. Vendor logs can contain prompts,
// headers, and credentials, so their contents must never appear in test output.
func windowsDesktopAcceptanceLogs(root string) string {
	var summaries []string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(strings.ToLower(entry.Name()), ".log") || len(summaries) >= 10 {
			return nil
		}
		st, err := entry.Info()
		if err != nil || !st.Mode().IsRegular() || st.Size() > 1<<20 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		lower := strings.ToLower(string(data))
		var categories []string
		for category, markers := range map[string][]string{
			"file-access":  {"eacces", "eperm", "access denied"},
			"gateway-auth": {"unauthorized", "authentication", "credential"},
			"network":      {"err_connection", "econnrefused", "fetch failed"},
			"startup":      {"uncaught", "unhandled", "fatal", "failed to launch"},
		} {
			for _, marker := range markers {
				if strings.Contains(lower, marker) {
					categories = append(categories, category)
					break
				}
			}
		}
		sort.Strings(categories)
		summaries = append(summaries, fmt.Sprintf("%s bytes=%d categories=%s", entry.Name(), len(data), strings.Join(categories, ",")))
		return nil
	})
	if len(summaries) == 0 {
		return "no private log files"
	}
	return strings.Join(summaries, "; ")
}

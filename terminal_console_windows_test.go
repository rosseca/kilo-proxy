//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

type terminalWindowsTestResult struct {
	Args      []string        `json:"args"`
	Directory string          `json:"directory"`
	Input     string          `json:"input"`
	Token     string          `json:"token"`
	ExitCode  int             `json:"exitCode"`
	Console   map[string]bool `json:"console"`
}

// Every process in these tests belongs to the fixture. The control-event case
// creates a separate Win32 console so it never interrupts the test runner/user.
func TestTerminalAgentWindowsHelper(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--terminal-windows-helper" {
			separator = i
			break
		}
	}
	if separator < 0 || len(os.Args) <= separator+1 {
		return
	}
	args := os.Args[separator+1:]
	switch args[0] {
	case "stdio-child":
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(99)
		}
		directory, _ := os.Getwd()
		result := terminalWindowsTestResult{Args: args[1:], Input: string(input), Directory: directory, Token: os.Getenv("KILO_TERMINAL_TEST_TOKEN")}
		_ = json.NewEncoder(os.Stdout).Encode(result)
		_, _ = fmt.Fprint(os.Stderr, "child stderr\n")
		os.Exit(23)
	case "console-child":
		result := terminalWindowsTestResult{Console: map[string]bool{}}
		for name, file := range map[string]*os.File{"stdin": os.Stdin, "stdout": os.Stdout, "stderr": os.Stderr} {
			var mode uint32
			result.Console[name] = windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil
		}
		var interrupted atomic.Bool
		callback := windows.NewCallback(func(event uint32) uintptr {
			if event == windows.CTRL_C_EVENT {
				interrupted.Store(true)
				return 1
			}
			return 0
		})
		if ok, _, _ := terminalSetConsoleCtrlHandler.Call(callback, 1); ok == 0 {
			os.Exit(98)
		}
		if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0); err != nil {
			os.Exit(97)
		}
		deadline := time.Now().Add(3 * time.Second)
		for !interrupted.Load() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if !interrupted.Load() {
			os.Exit(96)
		}
		// Allow the waiting parent's asynchronous control handler to run too.
		time.Sleep(100 * time.Millisecond)
		data, _ := json.Marshal(result)
		if err := os.WriteFile(args[1]+".child", data, 0600); err != nil {
			os.Exit(95)
		}
		os.Exit(37)
	case "stdio-parent", "console-parent":
		if args[0] == "stdio-parent" {
			// A GUI-subsystem program starts detached. Simulate that state while
			// retaining the inherited redirected handles for this unit test.
			streams := map[uint32]*os.File{windows.STD_INPUT_HANDLE: os.Stdin, windows.STD_OUTPUT_HANDLE: os.Stdout, windows.STD_ERROR_HANDLE: os.Stderr}
			_, _, _ = terminalFreeConsole.Call()
			for id, stream := range streams {
				if err := windows.SetStdHandle(id, windows.Handle(stream.Fd())); err != nil {
					os.Exit(91)
				}
			}
		}
		cleanup, err := prepareTerminalAgentConsole()
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(94)
		}
		binary, _ := os.Executable()
		directory, _ := os.Getwd()
		childMode := strings.TrimSuffix(args[0], "parent") + "child"
		childArgs := append([]string{"-test.run=^TestTerminalAgentWindowsHelper$", "--", "--terminal-windows-helper", childMode}, args[1:]...)
		plan := clientLaunchPlan{Executable: binary, Directory: directory, Args: childArgs, Env: map[string]string{"KILO_TERMINAL_TEST_TOKEN": "synthetic-key"}}
		err = execTerminalAgent(plan)
		code := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			code = 93
		}
		if args[0] == "console-parent" {
			data, _ := json.Marshal(terminalWindowsTestResult{ExitCode: code})
			_ = os.WriteFile(args[1], data, 0600)
		}
		cleanup()
		os.Exit(code)
	default:
		os.Exit(92)
	}
}

func TestTerminalAgentWindowsPreservesRedirectedStreamsAndExitStatus(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"", "spaces and café", "quote\"literal", "line\nbreak", "%PATH%", "!^&|<>()", "a'b", "trailing\\"}
	args := append([]string{"-test.run=^TestTerminalAgentWindowsHelper$", "--", "--terminal-windows-helper", "stdio-parent"}, want...)
	command := exec.Command(binary, args...)
	command.Dir = t.TempDir()
	var output, stderr bytes.Buffer
	command.Stdin, command.Stdout, command.Stderr = strings.NewReader("terminal stdin\n"), &output, &stderr
	var exit *exec.ExitError
	if err := command.Run(); !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("exit status lost: %v; stdout=%q stderr=%q", err, output.String(), stderr.String())
	}
	var result terminalWindowsTestResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("redirected stdout unavailable: %v; %q", err, output.String())
	}
	if !reflect.DeepEqual(result.Args, want) || result.Input != "terminal stdin\n" || !strings.EqualFold(result.Directory, command.Dir) || result.Token != "synthetic-key" || stderr.String() != "child stderr\n" {
		t.Fatalf("terminal state changed: %+v; stderr=%q", result, stderr.String())
	}
}

func TestTerminalAgentWindowsSharesConsoleAndWaitsAfterInterrupt(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "terminal-report.json")
	args := []string{"-test.run=^TestTerminalAgentWindowsHelper$", "--", "--terminal-windows-helper", "console-parent", path}
	done, err := startWindowsClientConsoleProcess(binary, args, filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("terminal waiter did not exit")
	}
	var parent, child terminalWindowsTestResult
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &parent) != nil || parent.ExitCode != 37 {
		t.Fatalf("parent did not wait for the interrupted child's exit: %v; %s", err, data)
	}
	data, err = os.ReadFile(path + ".child")
	if err != nil || json.Unmarshal(data, &child) != nil {
		t.Fatalf("child report missing: %v; %s", err, data)
	}
	for _, name := range []string{"stdin", "stdout", "stderr"} {
		if !child.Console[name] {
			t.Fatalf("client lost interactive %s: %+v", name, child.Console)
		}
	}
}

//go:build windows

package main

import (
	"bufio"
	"bytes"
	"debug/pe"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestClientLaunchProcessWindowsFailureWaitsForReturn(t *testing.T) {
	input, sendInput := io.Pipe()
	output, receiveOutput := io.Pipe()
	defer input.Close()
	defer sendInput.Close()
	defer output.Close()
	defer receiveOutput.Close()
	done := make(chan struct{})
	go func() {
		waitClientRunnerFailure(input, receiveOutput)
		close(done)
	}()
	prompts := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(output)
		_, _ = reader.ReadString('\n')
		prompt, _ := reader.ReadString('\n')
		prompts <- prompt
	}()
	select {
	case prompt := <-prompts:
		if !strings.Contains(prompt, "Press Return") {
			t.Fatalf("launch failure has no visible close instruction: %q", prompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("launch failure never displayed its close instruction")
	}
	inputWritten := make(chan error, 1)
	go func() {
		_, err := io.WriteString(sendInput, "still reading the failure")
		inputWritten <- err
	}()
	select {
	case err := <-inputWritten:
		if err != nil {
			t.Fatal(err)
		}
	case <-done:
		t.Fatal("failure window closed before accepting input")
	case <-time.After(2 * time.Second):
		t.Fatal("failure window did not accept console input")
	}
	select {
	case <-done:
		t.Fatal("failure window closed before Return was pressed")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := io.WriteString(sendInput, "\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("failure window did not close after Return")
	}
}

func TestClientLaunchProcessWindowsBatchArgumentsAreNotShellSource(t *testing.T) {
	plan := clientProcessTestPlan(t)
	binary := plan.Executable
	dir := filepath.Join(t.TempDir(), "npm %PATH% !^& space")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	plan.Executable = filepath.Join(dir, "synthetic.cmd")
	if err := os.WriteFile(plan.Executable, []byte("@echo off\r\n\"%KILO_LAUNCH_TEST_BINARY%\" -test.run=TestClientLaunchProcessHelper -- %*\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plan.Args = []string{"spaces and café", "%PATH%", "!^&|<>()", "a'b", "trailing\\"}
	plan.Env = map[string]string{"KILO_LAUNCH_PROCESS_HELPER": "1", "KILO_LAUNCH_TEST_BINARY": binary, "KILO_LAUNCH_KEY_TEST": "synthetic-local-key"}
	command, cleanup, err := clientProcessCommand(plan, clientChildEnvironment(os.Environ(), plan.Env, nil, "windows"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	command.Dir = plan.Directory
	var output, stderr bytes.Buffer
	command.Stdin, command.Stdout, command.Stderr = strings.NewReader("batch input"), &output, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("synthetic batch launcher: %v\n%s\n%s", err, output.String(), stderr.String())
	}
	var result clientProcessTestResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("batch output: %v\n%s", err, output.String())
	}
	want := append([]string{"-test.run=TestClientLaunchProcessHelper", "--"}, plan.Args...)
	if !reflect.DeepEqual(result.Args, want) || result.Input != "batch input" || result.Token != "synthetic-local-key" {
		t.Fatalf("batch wrapper changed input/environment/arguments: %+v; want %q", result, want)
	}
	for _, value := range []string{"quote\" & arbitrary", "line\nbreak"} {
		plan.Args = []string{value}
		if _, _, err := clientProcessCommand(plan, nil); err == nil {
			t.Fatal("unrepresentable batch argument accepted")
		}
	}
}

func TestClientLaunchProcessConsoleHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--client-console-report" {
		return
	}
	report := map[string]any{}
	// This is the final fake client, not the bootstrap. It must inherit its
	// interactive handles from the production runner without repairing them.
	for name, file := range map[string]*os.File{"stdin": os.Stdin, "stdout": os.Stdout, "stderr": os.Stderr} {
		var mode uint32
		if err := windows.GetConsoleMode(windows.Handle(file.Fd()), &mode); err != nil {
			report[name] = err.Error()
		} else {
			report[name] = true
		}
	}
	_, _ = fmt.Fprintln(os.Stdout, "Kilo Proxy synthetic console check")
	data, _ := json.Marshal(report)
	_ = os.WriteFile(os.Args[len(os.Args)-1], data, 0600)
	os.Exit(0)
}

func clientConsoleTestExecutable(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var subsystem uint16
	switch header := file.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		subsystem = header.Subsystem
	case *pe.OptionalHeader64:
		subsystem = header.Subsystem
	default:
		t.Fatal("test binary has no Windows optional header")
	}
	if subsystem == pe.IMAGE_SUBSYSTEM_WINDOWS_CUI {
		return path
	}
	// Release builds use the GUI subsystem. Keep that bootstrap intact, but
	// make the synthetic client a console executable like Claude Code/node.
	// Windows intentionally does not attach a GUI child to its parent's TTY.
	peOffset := int(binary.LittleEndian.Uint32(data[0x3c:]))
	subsystemOffset := peOffset + 4 + 20 + 68
	binary.LittleEndian.PutUint16(data[subsystemOffset:], pe.IMAGE_SUBSYSTEM_WINDOWS_CUI)
	console := filepath.Join(t.TempDir(), "synthetic-console.exe")
	if err := os.WriteFile(console, data, 0700); err != nil {
		t.Fatal(err)
	}
	return console
}

// This launches only our test executable and checks actual Win32 console handles;
// process creation alone cannot establish that an interactive client would work.
func TestClientLaunchProcessWindowsConsoleHasInteractiveHandles(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	consoleBinary := clientConsoleTestExecutable(t, binary)
	for _, extension := range []string{".exe", ".cmd", ".bat"} {
		t.Run(extension, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "console-report.json")
			plan := clientProcessTestPlan(t)
			plan.Executable = consoleBinary
			if extension != ".exe" {
				// Exercise the complete interactive npm/shell-wrapper path. A batch
				// test with redirected pipes alone cannot prove its TTY survives.
				dir := filepath.Join(t.TempDir(), "npm %PATH% !^& space")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				plan.Executable = filepath.Join(dir, "synthetic"+extension)
				if err := os.WriteFile(plan.Executable, []byte("@echo off\r\n\"%KILO_LAUNCH_TEST_BINARY%\" %*\r\n"), 0600); err != nil {
					t.Fatal(err)
				}
				plan.Env["KILO_LAUNCH_TEST_BINARY"] = consoleBinary
			}
			plan.Args = []string{"-test.run=^TestClientLaunchProcessConsoleHelper$", "--", "--client-console-report", path}
			ticket, _, cleanup, err := writeClientLaunchTicket(plan, binary, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			done, err := startWindowsClientConsoleProcess(binary, []string{"-test.run=^TestClientLaunchProcessMainBootstrap$", "--", "--client-launch-ticket", ticket}, filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			// A report proves the child ran, but its parent still owns the working
			// directory until process exit. Wait before TempDir cleanup (also under -race).
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				t.Fatal("synthetic console bootstrap did not exit")
			}
			var report map[string]any
			data, err := os.ReadFile(path)
			if err != nil || json.Unmarshal(data, &report) != nil {
				t.Fatalf("console report missing or invalid: %v; %q", err, data)
			}
			if report["stdin"] != true || report["stdout"] != true || report["stderr"] != true {
				t.Fatalf("new console must expose interactive standard handles: %+v", report)
			}
			if _, err := os.Stat(ticket); !os.IsNotExist(err) {
				t.Fatal("console launch did not consume its private ticket")
			}
		})
	}
}

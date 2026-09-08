//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

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

// This launches only our test executable and checks actual Win32 console handles;
// process creation alone cannot establish that an interactive client would work.
func TestClientLaunchProcessWindowsConsoleHasInteractiveHandles(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "console-report.json")
	plan := clientProcessTestPlan(t)
	plan.Args = []string{"-test.run=^TestClientLaunchProcessConsoleHelper$", "--", "--client-console-report", path}
	ticket, _, cleanup, err := writeClientLaunchTicket(plan, binary, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := startWindowsClientConsole(binary, []string{"-test.run=^TestClientLaunchProcessMainBootstrap$", "--", "--client-launch-ticket", ticket}, filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	var report map[string]any
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &report) == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if report["stdin"] != true || report["stdout"] != true || report["stderr"] != true {
		t.Fatalf("new console must expose interactive standard handles: %+v", report)
	}
	if _, err := os.Stat(ticket); !os.IsNotExist(err) {
		t.Fatal("console launch did not consume its private ticket")
	}
}

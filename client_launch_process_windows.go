//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func clientProcessCommand(plan clientLaunchPlan, environment []string) (*exec.Cmd, func(), error) {
	if extension := strings.ToLower(filepath.Ext(plan.Executable)); extension != ".cmd" && extension != ".bat" {
		command := exec.Command(plan.Executable, plan.Args...)
		command.Env = environment
		return command, func() {}, nil
	}
	// cmd.exe has different parsing rules from ordinary Windows executables.
	// Expand values once from environment variables inside a generated batch file;
	// never interpolate paths, credentials, or arguments into shell source.
	values := append([]string{plan.Executable}, plan.Args...)
	for _, value := range values {
		if strings.ContainsAny(value, "\"\r\n\x00") {
			return nil, nil, errors.New("This Windows command wrapper cannot accept quotes or line breaks in arguments")
		}
	}
	dir, err := os.MkdirTemp("", "kilo-client-cmd-")
	if err != nil {
		return nil, nil, errors.New("Cannot prepare the Windows command wrapper")
	}
	path := filepath.Join(dir, "launch.cmd")
	cleanup := func() { _ = os.Remove(path); _ = os.Remove(dir) }
	overrides := map[string]string{"KILO_LAUNCH_WRAPPER": path}
	var line strings.Builder
	line.WriteString("@echo off\r\nsetlocal DisableDelayedExpansion\r\n")
	for i, value := range values {
		name := fmt.Sprintf("KILO_LAUNCH_ARG_%d", i)
		if i > 0 {
			// npm forwards %* to node.exe, whose argument decoder consumes paired
			// backslashes immediately before the enclosing closing quote.
			trailing := len(value) - len(strings.TrimRight(value, "\\"))
			value += strings.Repeat("\\", trailing)
		}
		overrides[name] = value
		if i > 0 {
			line.WriteByte(' ')
		}
		line.WriteString("\"%" + name + "%\"")
	}
	line.WriteString("\r\n")
	if err := os.WriteFile(path, []byte(line.String()), 0600); err != nil {
		cleanup()
		return nil, nil, errors.New("Cannot write the Windows command wrapper")
	}
	// Use the system shell, not an arbitrary COMSPEC inherited from the caller.
	system, err := windows.GetSystemDirectory()
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	command := exec.Command(filepath.Join(system, "cmd.exe"))
	command.SysProcAttr = &syscall.SysProcAttr{CmdLine: windows.EscapeArg(command.Path) + " /d /q /v:off /s /c \"\"%KILO_LAUNCH_WRAPPER%\"\""}
	command.Env = clientChildEnvironment(environment, overrides, nil, "windows")
	return command, cleanup, nil
}

// Go's exec.Cmd always sets STARTF_USESTDHANDLES. Creating the bootstrap with
// Win32 directly lets Windows supply real handles for its new console instead
// of inheriting the GUI application's null devices or test log pipes.
func startWindowsClientConsole(binary string, args []string, directory string) error {
	application, err := windows.UTF16PtrFromString(binary)
	if err != nil {
		return err
	}
	parts := []string{windows.EscapeArg(binary)}
	for _, arg := range args {
		parts = append(parts, windows.EscapeArg(arg))
	}
	command, err := windows.UTF16PtrFromString(strings.Join(parts, " "))
	if err != nil {
		return err
	}
	var cwd *uint16
	if directory != "" {
		cwd, err = windows.UTF16PtrFromString(directory)
		if err != nil {
			return err
		}
	}
	startup := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	var process windows.ProcessInformation
	if err := windows.CreateProcess(application, command, nil, nil, false, windows.CREATE_NEW_CONSOLE|windows.CREATE_UNICODE_ENVIRONMENT, nil, cwd, &startup, &process); err != nil {
		return err
	}
	_ = windows.CloseHandle(process.Thread)
	go func() {
		_, _ = windows.WaitForSingleObject(process.Process, windows.INFINITE)
		_ = windows.CloseHandle(process.Process)
	}()
	return nil
}

func startClientTerminal(binary, ticket, _ string) error {
	return startWindowsClientConsole(binary, []string{clientLaunchRunnerFlag, ticket}, "")
}

func prepareClientRunnerConsole() (func(), error) {
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		alloc := windows.NewLazySystemDLL("kernel32.dll").NewProc("AllocConsole")
		if ok, _, _ := alloc.Call(); ok == 0 {
			return nil, errors.New("Cannot open an interactive Windows console")
		}
		input, err = os.OpenFile("CONIN$", os.O_RDWR, 0)
	}
	if err != nil {
		return nil, errors.New("Cannot open console input")
	}
	output, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		_ = input.Close()
		return nil, errors.New("Cannot open console output")
	}
	oldInput, oldOutput, oldError := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = input, output, output
	_ = windows.SetStdHandle(windows.STD_INPUT_HANDLE, windows.Handle(input.Fd()))
	_ = windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, windows.Handle(output.Fd()))
	_ = windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(output.Fd()))
	return func() {
		os.Stdin, os.Stdout, os.Stderr = oldInput, oldOutput, oldError
		_ = windows.SetStdHandle(windows.STD_INPUT_HANDLE, windows.Handle(oldInput.Fd()))
		_ = windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, windows.Handle(oldOutput.Fd()))
		_ = windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(oldError.Fd()))
		_ = input.Close()
		_ = output.Close()
	}, nil
}

//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

type claudeDesktopWindowsProcess struct {
	pid  uint32
	name string
}

type claudeDesktopWindowsProcessReader func(uint32) (command string, exited bool, err error)

func claudeDesktopWindowsRunning(userDataDir string) (bool, error) {
	return claudeDesktopWindowsRunningWith(userDataDir, claudeDesktopWindowsProcesses, claudeDesktopWindowsReadProcess)
}

func claudeDesktopWindowsRunningWith(userDataDir string, snapshot func() ([]claudeDesktopWindowsProcess, error), read claudeDesktopWindowsProcessReader) (bool, error) {
	if !filepath.IsAbs(userDataDir) || strings.ContainsAny(userDataDir, "\x00\r\n") {
		return false, errors.New("Cannot inspect the Claude Desktop profile.")
	}
	processes, err := snapshot()
	if err != nil {
		return false, err
	}
	for _, process := range processes {
		// Toolhelp enumerates executable names only. Do not read command
		// lines for applications whose executable is not named Claude.exe.
		if process.pid == 0 || !strings.EqualFold(process.name, "Claude.exe") {
			continue
		}
		command, exited, err := read(process.pid)
		if exited {
			continue
		}
		if err != nil {
			// An inaccessible Claude process might own this profile. Never
			// allow configuration writes when its ownership is unknown.
			return false, err
		}
		args, err := windows.DecomposeCommandLine(command)
		if err != nil || len(args) == 0 {
			return false, errors.New("Cannot inspect running desktop applications.")
		}
		for i := 1; i < len(args); i++ {
			value, isProfile := strings.CutPrefix(args[i], "--user-data-dir=")
			if args[i] == "--user-data-dir" && i+1 < len(args) {
				i++
				value, isProfile = args[i], true
			}
			if isProfile && strings.EqualFold(filepath.Clean(value), filepath.Clean(userDataDir)) {
				return true, nil
			}
		}
	}
	return false, nil
}

func claudeDesktopWindowsProcesses() ([]claudeDesktopWindowsProcess, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	err = windows.Process32First(snapshot, &entry)
	var processes []claudeDesktopWindowsProcess
	for err == nil {
		processes = append(processes, claudeDesktopWindowsProcess{entry.ProcessID, windows.UTF16ToString(entry.ExeFile[:])})
		err = windows.Process32Next(snapshot, &entry)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return processes, nil
}

func claudeDesktopWindowsReadProcess(pid uint32) (string, bool, error) {
	// This query needs neither administrator rights nor remote-memory access.
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", errors.Is(err, windows.ERROR_INVALID_PARAMETER), err
	}
	defer windows.CloseHandle(process)
	// Verify the name again on the live handle in case its PID was reused
	// after the snapshot. Never query an unrelated process's command line.
	image := make([]uint16, 32768)
	size := uint32(len(image))
	if err := windows.QueryFullProcessImageName(process, 0, &image[0], &size); err != nil {
		return "", claudeDesktopWindowsProcessExited(process), err
	}
	if size == 0 || size > uint32(len(image)) {
		return "", false, errors.New("Cannot inspect running desktop applications.")
	}
	if !strings.EqualFold(filepath.Base(windows.UTF16ToString(image[:size])), "Claude.exe") {
		return "", true, nil
	}
	command, err := claudeDesktopWindowsQueryCommandLine(process)
	if err != nil {
		return "", errors.Is(err, windows.STATUS_PROCESS_IS_TERMINATING) || claudeDesktopWindowsProcessExited(process), err
	}
	return command, false, nil
}

func claudeDesktopWindowsProcessExited(process windows.Handle) bool {
	var status uint32
	return windows.GetExitCodeProcess(process, &status) == nil && status != 259 // STILL_ACTIVE
}

type claudeDesktopWindowsUnicodeString struct {
	length        uint16
	maximumLength uint16
	buffer        uintptr
}

var claudeDesktopWindowsQueryProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryInformationProcess")

func claudeDesktopWindowsQueryCommandLine(process windows.Handle) (string, error) {
	// Class 60 returns a local UNICODE_STRING plus its UTF-16 contents. The
	// caller's pointer size applies even when inspecting an emulated process.
	// x/sys binds NtQueryInformationProcess dynamically from the system DLL.
	if err := claudeDesktopWindowsQueryProcess.Find(); err != nil {
		return "", err
	}
	return claudeDesktopWindowsQueryCommandLineWith(func(buffer []byte, required *uint32) error {
		return windows.NtQueryInformationProcess(process, windows.ProcessCommandLineInformation, unsafe.Pointer(&buffer[0]), uint32(len(buffer)), required)
	})
}

func claudeDesktopWindowsQueryCommandLineWith(query func([]byte, *uint32) error) (string, error) {
	const maxSize = 128 << 10
	buffer := make([]byte, 512)
	for attempts := 0; attempts < 3; attempts++ {
		var required uint32
		err := query(buffer, &required)
		if err == nil {
			return claudeDesktopWindowsDecodeCommandLine(buffer, required)
		}
		if !errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) && !errors.Is(err, windows.STATUS_BUFFER_TOO_SMALL) {
			return "", err
		}
		if required <= uint32(len(buffer)) || required > maxSize {
			return "", errors.New("Cannot inspect running desktop applications.")
		}
		buffer = make([]byte, required)
	}
	return "", errors.New("Cannot inspect running desktop applications.")
}

func claudeDesktopWindowsDecodeCommandLine(buffer []byte, size uint32) (string, error) {
	invalid := errors.New("Cannot inspect running desktop applications.")
	headerSize := int(unsafe.Sizeof(claudeDesktopWindowsUnicodeString{}))
	if size < uint32(headerSize) || size > uint32(len(buffer)) {
		return "", invalid
	}
	length := uintptr(binary.LittleEndian.Uint16(buffer[0:2]))
	maximumLength := uintptr(binary.LittleEndian.Uint16(buffer[2:4]))
	if length == 0 || length%2 != 0 || maximumLength%2 != 0 || length > maximumLength {
		return "", invalid
	}
	pointerOffset := int(unsafe.Offsetof(claudeDesktopWindowsUnicodeString{}.buffer))
	var pointer uintptr
	if unsafe.Sizeof(uintptr(0)) == 8 {
		pointer = uintptr(binary.LittleEndian.Uint64(buffer[pointerOffset : pointerOffset+8]))
	} else {
		pointer = uintptr(binary.LittleEndian.Uint32(buffer[pointerOffset : pointerOffset+4]))
	}
	base := uintptr(unsafe.Pointer(&buffer[0]))
	if pointer < base || pointer-base < uintptr(headerSize) {
		return "", invalid
	}
	offset := pointer - base
	// Subtract before comparing to avoid overflowing attacker-controlled
	// pointers. Decode only bytes inside the buffer returned by the kernel.
	if offset > uintptr(size) || offset%2 != 0 || maximumLength > uintptr(size)-offset {
		return "", invalid
	}
	units := make([]uint16, length/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(buffer[int(offset)+i*2 : int(offset)+i*2+2])
		if units[i] == 0 {
			return "", invalid
		}
	}
	return string(utf16.Decode(units)), nil
}

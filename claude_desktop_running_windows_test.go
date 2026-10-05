//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestClaudeDesktopWindowsRunningProfiles(t *testing.T) {
	const profile = `C:\Users\Test User\AppData\Local\Kilo\claude-desktop`
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{"quoted argument", `"C:\Program Files\Claude\Claude.exe" "--user-data-dir=` + profile + `"`, true},
		{"quoted value", `Claude.exe --user-data-dir="` + profile + `"`, true},
		{"separate value", `Claude.exe --user-data-dir "` + profile + `"`, true},
		{"case insensitive path", `Claude.exe "--user-data-dir=` + strings.ToLower(profile) + `"`, true},
		{"Unicode path", `Claude.exe "--user-data-dir=C:\Users\José 😀\Kilo"`, false},
		{"normal session", `Claude.exe --no-startup-window`, false},
		{"different profile", `Claude.exe "--user-data-dir=` + profile + `-other"`, false},
		{"nested profile", `Claude.exe "--user-data-dir=` + profile + `\other"`, false},
		{"argument prefix", `Claude.exe "--other-user-data-dir=` + profile + `"`, false},
		{"profile text in another option", `Claude.exe "--title=--user-data-dir=` + profile + `"`, false},
		{"profile in executable name", `"--user-data-dir=` + profile + `"`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			running, err := claudeDesktopWindowsRunningWith(profile, func() ([]claudeDesktopWindowsProcess, error) {
				return []claudeDesktopWindowsProcess{{42, "cLaUdE.ExE"}}, nil
			}, func(pid uint32) (string, bool, error) {
				if pid != 42 {
					t.Fatal("queried an unexpected process")
				}
				return test.command, false, nil
			})
			if err != nil || running != test.want {
				t.Fatalf("running = %v, err = %v; want %v", running, err, test.want)
			}
		})
	}
}

func TestClaudeDesktopWindowsRunningOnlyQueriesClaude(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "claude")
	calls := 0
	running, err := claudeDesktopWindowsRunningWith(profile, func() ([]claudeDesktopWindowsProcess, error) {
		return []claudeDesktopWindowsProcess{{1, "other.exe"}, {2, "claude"}, {3, "claude-code.exe"}, {0, "Claude.exe"}, {4, "Claude.exe"}}, nil
	}, func(pid uint32) (string, bool, error) {
		calls++
		if pid != 4 {
			t.Fatal("read an unrelated application")
		}
		return "Claude.exe", false, nil
	})
	if running || err != nil || calls != 1 {
		t.Fatalf("running = %v, err = %v, calls = %d", running, err, calls)
	}
}

func TestClaudeDesktopWindowsRunningErrors(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "claude")
	snapshot := func() ([]claudeDesktopWindowsProcess, error) {
		return []claudeDesktopWindowsProcess{{42, "Claude.exe"}}, nil
	}
	denied := errors.New("inspection denied")
	running, err := claudeDesktopWindowsRunningWith(profile, snapshot, func(uint32) (string, bool, error) {
		return "", false, denied
	})
	if running || !errors.Is(err, denied) {
		t.Fatalf("an inaccessible Claude process must fail closed: running=%v, err=%v", running, err)
	}
	running, err = claudeDesktopWindowsRunningWith(profile, snapshot, func(uint32) (string, bool, error) {
		return "", true, windows.ERROR_INVALID_PARAMETER
	})
	if running || err != nil {
		t.Fatalf("a process exiting after the snapshot is harmless: running=%v, err=%v", running, err)
	}
	running, err = claudeDesktopWindowsRunningWith(profile, func() ([]claudeDesktopWindowsProcess, error) { return nil, denied }, nil)
	if running || !errors.Is(err, denied) {
		t.Fatalf("snapshot failure must fail closed: running=%v, err=%v", running, err)
	}
	for _, command := range []string{"", "Claude.exe\x00--user-data-dir=x"} {
		_, err = claudeDesktopWindowsRunningWith(profile, snapshot, func(uint32) (string, bool, error) { return command, false, nil })
		if err == nil {
			t.Fatal("accepted an invalid command line")
		}
	}
	for _, profile := range []string{"relative", "C:\\profile\x00", "C:\\profile\n"} {
		_, err = claudeDesktopWindowsRunningWith(profile, nil, nil)
		if err == nil {
			t.Fatal("accepted an invalid profile")
		}
	}
}

func claudeDesktopWindowsWriteTestCommand(buffer []byte, command string) uint32 {
	headerSize := int(unsafe.Sizeof(claudeDesktopWindowsUnicodeString{}))
	units := utf16.Encode([]rune(command))
	length := len(units) * 2
	binary.LittleEndian.PutUint16(buffer[0:2], uint16(length))
	binary.LittleEndian.PutUint16(buffer[2:4], uint16(length+2))
	pointerOffset := int(unsafe.Offsetof(claudeDesktopWindowsUnicodeString{}.buffer))
	pointer := uintptr(unsafe.Pointer(&buffer[headerSize]))
	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.LittleEndian.PutUint64(buffer[pointerOffset:pointerOffset+8], uint64(pointer))
	} else {
		binary.LittleEndian.PutUint32(buffer[pointerOffset:pointerOffset+4], uint32(pointer))
	}
	for i, unit := range units {
		binary.LittleEndian.PutUint16(buffer[headerSize+i*2:headerSize+i*2+2], unit)
	}
	return uint32(headerSize + length + 2)
}

func TestClaudeDesktopWindowsCommandLineBuffer(t *testing.T) {
	const command = `"C:\José 😀\Claude.exe" --user-data-dir="C:\José 😀\Kilo"`
	buffer := make([]byte, 512)
	size := claudeDesktopWindowsWriteTestCommand(buffer, command)
	got, err := claudeDesktopWindowsDecodeCommandLine(buffer, size)
	if err != nil || got != command {
		t.Fatalf("UTF-16 command line decoding failed: %v", err)
	}
	mutations := []struct {
		name string
		edit func([]byte, *uint32)
	}{
		{"short header", func(_ []byte, size *uint32) { *size = 1 }},
		{"excessive returned size", func(buffer []byte, size *uint32) { *size = uint32(len(buffer) + 1) }},
		{"odd string length", func(buffer []byte, _ *uint32) { binary.LittleEndian.PutUint16(buffer[0:2], 3) }},
		{"length exceeds maximum", func(buffer []byte, _ *uint32) { binary.LittleEndian.PutUint16(buffer[2:4], 2) }},
		{"null pointer", func(buffer []byte, _ *uint32) {
			clear(buffer[int(unsafe.Offsetof(claudeDesktopWindowsUnicodeString{}.buffer)):int(unsafe.Sizeof(claudeDesktopWindowsUnicodeString{}))])
		}},
		{"truncated string", func(_ []byte, size *uint32) { *size -= 4 }},
		{"embedded NUL", func(buffer []byte, _ *uint32) {
			binary.LittleEndian.PutUint16(buffer[int(unsafe.Sizeof(claudeDesktopWindowsUnicodeString{})):], 0)
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			buffer := make([]byte, 512)
			size := claudeDesktopWindowsWriteTestCommand(buffer, command)
			mutation.edit(buffer, &size)
			if _, err := claudeDesktopWindowsDecodeCommandLine(buffer, size); err == nil {
				t.Fatal("accepted an invalid native string buffer")
			}
		})
	}
}

func TestClaudeDesktopWindowsCommandLineQuerySizing(t *testing.T) {
	command := `Claude.exe --user-data-dir="C:\` + strings.Repeat("folder\\", 100) + `Kilo"`
	needed := uint32(unsafe.Sizeof(claudeDesktopWindowsUnicodeString{})) + uint32(len(utf16.Encode([]rune(command)))*2+2)
	calls := 0
	got, err := claudeDesktopWindowsQueryCommandLineWith(func(buffer []byte, required *uint32) error {
		calls++
		if uint32(len(buffer)) < needed {
			*required = needed
			return windows.STATUS_INFO_LENGTH_MISMATCH
		}
		*required = claudeDesktopWindowsWriteTestCommand(buffer, command)
		return nil
	})
	if err != nil || got != command || calls != 2 {
		t.Fatalf("native query resizing failed: err=%v, calls=%d", err, calls)
	}
	for _, requiredSize := range []uint32{0, 512, 128<<10 + 1} {
		_, err := claudeDesktopWindowsQueryCommandLineWith(func(_ []byte, required *uint32) error {
			*required = requiredSize
			return windows.STATUS_BUFFER_TOO_SMALL
		})
		if err == nil {
			t.Fatal("accepted an invalid required size")
		}
	}
}

func TestClaudeDesktopWindowsRunningNativeWithoutPowerShell(t *testing.T) {
	if os.Getenv("KILO_TEST_WINDOWS_DESKTOP") != "1" {
		t.Skip("set KILO_TEST_WINDOWS_DESKTOP=1 to exercise native Windows process inspection")
	}
	t.Setenv("PATH", t.TempDir())
	// Only the test's own process is read directly. This confirms the native
	// UTF-16 layout and access rights without launching or closing any app.
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(os.Getpid()))
	if err != nil {
		t.Fatal("cannot open the test process with limited query rights:", err)
	}
	defer windows.CloseHandle(process)
	command, err := claudeDesktopWindowsQueryCommandLine(process)
	if err != nil {
		t.Fatal("native command-line query failed:", err)
	}
	args, err := windows.DecomposeCommandLine(command)
	if err != nil || len(args) == 0 {
		t.Fatal("native query returned no valid executable argument")
	}
	executable, err := os.Executable()
	if err != nil || !strings.EqualFold(filepath.Base(args[0]), filepath.Base(executable)) {
		t.Fatal("native query did not return the current test executable")
	}
	running, err := claudeDesktopWindowsRunning(filepath.Join(t.TempDir(), "absent-profile"))
	if err != nil || running {
		t.Fatalf("native absent-profile query failed: running=%v, err=%v", running, err)
	}
}

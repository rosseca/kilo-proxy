//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

var terminalKernel32 = windows.NewLazySystemDLL("kernel32.dll")
var terminalAttachConsole = terminalKernel32.NewProc("AttachConsole")
var terminalFreeConsole = terminalKernel32.NewProc("FreeConsole")
var terminalSetConsoleCtrlHandler = terminalKernel32.NewProc("SetConsoleCtrlHandler")
var terminalWaiterControlHandler = windows.NewCallback(func(event uint32) uintptr {
	if event == windows.CTRL_C_EVENT || event == windows.CTRL_BREAK_EVENT {
		return 1
	}
	return 0
})

func terminalStandardHandleValid(handle windows.Handle) bool {
	if handle == 0 || handle == windows.InvalidHandle {
		return false
	}
	_, err := windows.GetFileType(handle)
	return err == nil
}

// Production Windows bundles use the GUI subsystem. Attach to the calling
// terminal without allocating a new window, and refresh Go's cached os.Files.
// AttachConsole may replace the standard handle table, so preserve inherited
// files and pipes (including explicitly redirected NUL handles) first.
func prepareTerminalAgentConsole() (func(), error) {
	ids := []uint32{windows.STD_INPUT_HANDLE, windows.STD_OUTPUT_HANDLE, windows.STD_ERROR_HANDLE}
	oldFiles := []*os.File{os.Stdin, os.Stdout, os.Stderr}
	oldHandles := make([]windows.Handle, len(ids))
	oldValid := make([]bool, len(ids))
	for i, id := range ids {
		oldHandles[i], _ = windows.GetStdHandle(id)
		oldValid[i] = terminalStandardHandleValid(oldHandles[i])
	}
	attached := false
	if ok, _, err := terminalAttachConsole.Call(uintptr(^uint32(0))); ok != 0 {
		attached = true
	} else if !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_INVALID_HANDLE) && !errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil, errors.New("Cannot attach to the current Windows terminal")
	}
	var opened []*os.File
	cleanup := func() {
		os.Stdin, os.Stdout, os.Stderr = oldFiles[0], oldFiles[1], oldFiles[2]
		if attached {
			_, _, _ = terminalFreeConsole.Call()
		}
		for i, id := range ids {
			_ = windows.SetStdHandle(id, oldHandles[i])
		}
		for _, file := range opened {
			_ = file.Close()
		}
	}
	files := make([]*os.File, len(ids))
	for i, id := range ids {
		handle := oldHandles[i]
		if !oldValid[i] {
			handle, _ = windows.GetStdHandle(id)
		}
		if terminalStandardHandleValid(handle) {
			if oldFiles[i] != nil && windows.Handle(oldFiles[i].Fd()) == handle {
				files[i] = oldFiles[i]
			} else {
				// os.NewFile owns its handle. Duplicate borrowed standard handles
				// so finalizers/cleanup cannot close a handle owned elsewhere.
				var duplicate windows.Handle
				process := windows.CurrentProcess()
				if err := windows.DuplicateHandle(process, handle, process, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
					cleanup()
					return nil, errors.New("Cannot prepare Windows terminal streams")
				}
				file := os.NewFile(uintptr(duplicate), []string{"stdin", "stdout", "stderr"}[i])
				opened = append(opened, file)
				files[i], handle = file, duplicate
			}
		} else {
			// A redirected/background invocation may have only some streams.
			// Never substitute a new console for a deliberately missing stream.
			file, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
			if err != nil {
				cleanup()
				return nil, errors.New("Cannot prepare Windows terminal streams")
			}
			opened = append(opened, file)
			files[i] = file
			handle = windows.Handle(file.Fd())
		}
		if err := windows.SetStdHandle(id, handle); err != nil {
			cleanup()
			return nil, errors.New("Cannot prepare Windows terminal streams")
		}
	}
	os.Stdin, os.Stdout, os.Stderr = files[0], files[1], files[2]
	return cleanup, nil
}

func preserveTerminalAgentWaiter() (func(), error) {
	// AttachConsole resets handlers registered during Go runtime startup.
	// Install a non-inheritable callback after attachment, before starting the
	// child. signal.Notify alone does not reinstall Go's Windows callback.
	if ok, _, _ := terminalSetConsoleCtrlHandler.Call(terminalWaiterControlHandler, 1); ok == 0 {
		return nil, errors.New("Cannot prepare Windows terminal interrupt handling")
	}
	return func() { _, _, _ = terminalSetConsoleCtrlHandler.Call(terminalWaiterControlHandler, 0) }, nil
}

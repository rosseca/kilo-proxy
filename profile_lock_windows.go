//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func openProfileLock(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) || err == nil && !before.Mode().IsRegular() {
		return nil, errors.New("The Kilo Proxy profile lock must be a regular file.")
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, errors.New("Cannot open the Kilo Proxy profile lock.")
	}
	info, err := f.Stat()
	current, currentErr := os.Lstat(path)
	if err != nil || currentErr != nil || !info.Mode().IsRegular() || !current.Mode().IsRegular() || !os.SameFile(info, current) {
		_ = f.Close()
		return nil, errors.New("Cannot safely open the Kilo Proxy profile lock.")
	}
	return f, nil
}

func lockProfileFile(f *os.File) error {
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{}); err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return errProfileLocked
		}
		return errors.New("Cannot lock the Kilo Proxy profile.")
	}
	return nil
}

func unlockProfileFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}

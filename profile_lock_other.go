//go:build !darwin && !linux && !windows

package main

import (
	"errors"
	"os"
)

func openProfileLock(string) (*os.File, error) {
	return nil, errors.New("Kilo Proxy profile locking is supported on macOS, Linux and Windows.")
}
func lockProfileFile(*os.File) error { return errors.New("Kilo Proxy profile locking is unavailable.") }
func unlockProfileFile(*os.File)     {}

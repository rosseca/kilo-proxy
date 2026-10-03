//go:build darwin || linux

package main

import (
	"errors"
	"os"
	"syscall"
)

func openProfileLock(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, errors.New("Cannot safely open the private Kilo Proxy profile lock.")
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		_ = f.Close()
		return nil, errors.New("The Kilo Proxy profile lock must be a private regular file.")
	}
	return f, nil
}

func lockProfileFile(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return errProfileLocked
		}
		return errors.New("Cannot lock the Kilo Proxy profile.")
	}
	return nil
}

func unlockProfileFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

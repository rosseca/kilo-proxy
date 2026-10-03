package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

const profileLockFile = "profile.lock"

var errProfileLocked = errors.New("This Kilo Proxy profile is already in use. Use the running instance or choose another --config-dir.")

// Keep the lock inode on disk: removing it would allow a new process to lock a
// different inode while an existing process still owns the original lock.
func acquireProfileLock(dir string) (func(), error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, errors.New("The Kilo Proxy profile directory must be absolute.")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("Cannot create the Kilo Proxy profile directory.")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("The Kilo Proxy profile must be a real directory, not a symbolic link.")
	}
	f, err := openProfileLock(filepath.Join(dir, profileLockFile))
	if err != nil {
		return nil, err
	}
	if err := lockProfileFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { unlockProfileFile(f); _ = f.Close() }) }, nil
}

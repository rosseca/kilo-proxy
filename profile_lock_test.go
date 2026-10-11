package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestProfileLockExcludesConcurrentInstancesAndRetainsInode(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireProfileLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	first, err := os.Stat(filepath.Join(dir, profileLockFile))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && first.Mode().Perm() != 0600 {
		t.Fatal("lock file is not private")
	}
	if _, err := acquireProfileLock(dir); !errors.Is(err, errProfileLocked) {
		t.Fatal("second instance bypassed the exclusive lock", err)
	}
	release()
	releaseAgain, err := acquireProfileLock(dir)
	if err != nil {
		t.Fatal("lock did not release", err)
	}
	defer releaseAgain()
	second, err := os.Stat(filepath.Join(dir, profileLockFile))
	if err != nil || !os.SameFile(first, second) {
		t.Fatal("lock inode was replaced between instances")
	}
}

func TestProfileLockRejectsSymlinksAndPublicLockFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, profileLockFile)
	if err := os.Symlink(target, path); err != nil {
		t.Skip("symlinks unavailable on this runner:", err)
	}
	if _, err := acquireProfileLock(dir); err == nil {
		t.Fatal("lock followed a symbolic link")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep" {
		t.Fatal("locking changed an unrelated file")
	}
	_ = os.Remove(path)
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(path, nil, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := acquireProfileLock(dir); err == nil {
			t.Fatal("public lock file accepted")
		}
	}
}

func TestProfileLockProcessHelper(t *testing.T) {
	if os.Getenv("KILO_PROFILE_LOCK_TEST_CHILD") != "1" {
		return
	}
	release, err := acquireProfileLock(os.Getenv("KILO_PROFILE_LOCK_TEST_DIR"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, "locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
	release()
	os.Exit(0)
}

func TestProfileLockExcludesAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProfileLockProcessHelper$")
	cmd.Env = append(os.Environ(), "KILO_PROFILE_LOCK_TEST_CHILD=1", "KILO_PROFILE_LOCK_TEST_DIR="+dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
	}()
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		if line != "locked\n" {
			t.Fatal("helper failed to acquire lock", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not acquire lock")
	}
	if _, err := acquireProfileLock(dir); !errors.Is(err, errProfileLocked) {
		t.Fatal("another process bypassed the lock", err)
	}
	_ = stdin.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not exit")
	}
	release, err := acquireProfileLock(dir)
	if err != nil {
		t.Fatal("process exit did not release lock", err)
	}
	release()
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestT3CodeProcessStartTimeIgnoresInheritedLocaleAndTimezone(t *testing.T) {
	before, err := t3CodeProcessStartTime(os.Getpid())
	if err != nil || before.IsZero() {
		t.Fatal("cannot inspect current process", before, err)
	}
	t.Setenv("LC_ALL", "fr_FR.UTF-8")
	t.Setenv("TZ", "UTC+12")
	after, err := t3CodeProcessStartTime(os.Getpid())
	if err != nil || !after.Equal(before) {
		t.Fatal("process start time depends on inherited locale or timezone", before, after, err)
	}
}

func TestT3CodeRuntimeRecordRejectsLiveNonleaderThread(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	tid := unix.Gettid()
	if tid == os.Getpid() {
		// Keep the leader occupied so this goroutine must use another thread.
		thread := make(chan int)
		release := make(chan struct{})
		defer close(release)
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			thread <- unix.Gettid()
			<-release
		}()
		tid = <-thread
	}
	if tid == os.Getpid() {
		t.Fatal("fixture did not obtain a nonleader thread")
	}
	if alive, err := openMausBotProcessAlive(tid); err != nil || !alive {
		t.Fatal("fixture thread is not alive", alive, err)
	}
	root := t.TempDir()
	parent := filepath.Join(root, "data", "userdata")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"version": 1, "pid": tid, "startedAt": time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "server-runtime.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if running, err := t3CodeRuntimeRunning(root); err != nil || running {
		t.Fatal("live nonleader thread blocked setup", running, err)
	}
}

//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestSynaraCodexNormalAdapterUnixPreservesPIDAndSignals(t *testing.T) {
	root, source, target := synaraCodexAdapterFixture(t)
	files, err := planSynaraCodexNormalShim(root, target, source, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = saveEditorFiles(files); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, files[0].path, "-test.run=^TestSynaraCodexNormalSignalChild$", "--", "app-server")
	command.Env = clientChildEnvironment(os.Environ(), map[string]string{"KILO_TEST_SYNARA_SIGNAL_CHILD": "1"}, nil, runtime.GOOS)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	var pid int
	if err = json.NewDecoder(stdout).Decode(&pid); err != nil {
		t.Fatalf("child readiness: %v %s", err, &stderr)
	}
	if pid != command.Process.Pid {
		t.Fatalf("adapter did not replace itself: native=%d adapter=%d", pid, command.Process.Pid)
	}
	if err = command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Wait(); err != nil {
		t.Fatalf("native drain: %v %s", err, &stderr)
	}
	if string(rest) != "terminated cleanly\n" {
		t.Fatalf("signal lost: %q", rest)
	}
}

func TestSynaraCodexNormalSignalChild(t *testing.T) {
	if os.Getenv("KILO_TEST_SYNARA_SIGNAL_CHILD") != "1" {
		return
	}
	executable, _ := os.Executable()
	if isSynaraCodexNormalShim(executable, runtime.GOOS) {
		_, code := runSynaraCodexNormalShim()
		os.Exit(code)
	}
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGTERM)
	_ = json.NewEncoder(os.Stdout).Encode(os.Getpid())
	<-done
	_, _ = io.WriteString(os.Stdout, "terminated cleanly\n")
	os.Exit(0)
}

//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func t3CodeProcessStartTime(pid int) (time.Time, error) {
	if runtime.GOOS == "linux" {
		status, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
		if errors.Is(err, os.ErrNotExist) {
			if alive, checkErr := openMausBotProcessAlive(pid); checkErr == nil && !alive {
				return time.Time{}, nil
			}
		}
		if err != nil {
			return time.Time{}, err
		}
		found := false
		for _, line := range strings.Split(string(status), "\n") {
			if !strings.HasPrefix(line, "Tgid:") {
				continue
			}
			tgid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Tgid:")))
			if err != nil {
				return time.Time{}, err
			}
			// kill(tid, 0) also succeeds for nonleader Linux threads. A server
			// runtime PID must identify a process, not a recycled worker TID.
			if tgid != pid {
				return time.Time{}, nil
			}
			found = true
			break
		}
		if !found {
			return time.Time{}, errors.New("Cannot verify the private T3 Code server process.")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// lstart has second precision. UTC avoids local timezone/DST ambiguity;
	// a fixed locale keeps the month and weekday names parseable on Unix.
	out, err := openDesignCommandOutput(ctx, "/bin/ps", []string{"-p", strconv.Itoa(pid), "-o", "lstart="}, []string{"LC_ALL=C", "TZ=UTC0"})
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		if alive, checkErr := openMausBotProcessAlive(pid); checkErr == nil && !alive {
			return time.Time{}, nil
		}
		return time.Time{}, errors.New("Cannot verify the private T3 Code server process.")
	}
	return time.Parse("Mon Jan _2 15:04:05 2006", strings.TrimSpace(string(out)))
}

func t3CodeProcessRunning(ctx context.Context, root string) (bool, error) {
	commands, err := openDesignProcessSnapshot(ctx)
	if err != nil {
		return false, err
	}
	for _, command := range commands {
		if t3CodeCommandMatches(command, root) {
			return true, nil
		}
	}
	return false, nil
}

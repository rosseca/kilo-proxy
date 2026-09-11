//go:build !windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func hideOpenDesignProbeWindow(*exec.Cmd) {}

func openDesignProcessSnapshot(ctx context.Context) ([]string, error) {
	// Two -w flags request untruncated argv on macOS and Linux. Avoid process
	// names: both the desktop and its supervisor can use an Electron binary.
	data, err := openDesignCommandOutput(ctx, "/bin/ps", []string{"-ww", "-axo", "args="}, nil)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(data), "\n"), nil
}

func openDesignProbeRuntime(ctx context.Context, namespace string) (bool, error) {
	principal := strconv.Itoa(os.Getuid())
	var uncertain bool
	for _, stamp := range openDesignIPCStamps(namespace) {
		path := filepath.Join(os.TempDir(), "od-sidecar-"+principal, openDesignIPCDigest(principal, stamp)+".sock")
		running, err := openDesignProbeSocket(ctx, path, stamp)
		if running {
			return true, nil
		}
		uncertain = uncertain || err != nil
	}
	if uncertain {
		return false, errOpenDesignRunningCheck
	}
	return false, nil
}

func openDesignProbeSocket(ctx context.Context, path string, expected openDesignIPCStamp) (bool, error) {
	connection, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "unix", path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return false, nil
		}
		return false, errOpenDesignRunningCheck
	}
	defer connection.Close()
	deadline := time.Now().Add(300 * time.Millisecond)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	if connection.SetDeadline(deadline) != nil {
		return false, errOpenDesignRunningCheck
	}
	if _, err := io.WriteString(connection, "{\"type\":\"sidecar:describe\"}\n"); err != nil {
		return false, errOpenDesignRunningCheck
	}
	data, err := bufio.NewReader(io.LimitReader(connection, 64<<10)).ReadBytes('\n')
	if err != nil {
		return false, errOpenDesignRunningCheck
	}
	var response struct {
		OK     bool `json:"ok"`
		Result struct {
			Stamp     openDesignIPCStamp `json:"stamp"`
			Resources struct {
				PID int `json:"pid"`
			} `json:"resources"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &response) != nil || !response.OK || response.Result.Stamp != expected || response.Result.Resources.PID <= 0 {
		return false, errOpenDesignRunningCheck
	}
	// ready:false is a live namespace still starting; it also forbids hot writes.
	return true, nil
}

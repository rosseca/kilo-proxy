//go:build !windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDesignSocketProbeRecognizesStartingAndRejectsWrongOwner(t *testing.T) {
	root, err := os.MkdirTemp("", "od-probe-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	stamp := openDesignIPCStamp{"stable", "kilo-proxy-0123456789ab", "packaged", "runtime", "desktop"}
	for _, wrong := range []bool{false, true} {
		path := filepath.Join(root, "fixture.sock")
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			defer connection.Close()
			line, _ := bufio.NewReader(connection).ReadString('\n')
			if line != "{\"type\":\"sidecar:describe\"}\n" {
				t.Error("probe sent a mutating IPC action")
			}
			actual := stamp
			if wrong {
				actual.Namespace = "normal-open-design"
			}
			_ = json.NewEncoder(connection).Encode(map[string]any{"ok": true, "result": map[string]any{"stamp": actual, "ready": false, "resources": map[string]any{"pid": 123}}})
		}()
		running, probeErr := openDesignProbeSocket(context.Background(), path, stamp)
		_ = listener.Close()
		<-done
		if running == wrong || (probeErr != nil) != wrong {
			t.Fatalf("wrong=%v running=%v error=%v", wrong, running, probeErr)
		}
	}
	if running, err := openDesignProbeSocket(context.Background(), filepath.Join(root, "absent.sock"), stamp); running || err != nil {
		t.Fatal("missing endpoint was reported as running or uncertain")
	}
}

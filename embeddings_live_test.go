package main

import (
	"bytes"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Explicit opt-in only: two tiny provider calls through the actual built binary.
// Credentials never enter argv, environment, test output, or the normal profile.
func TestEmbeddingsLiveHeadless(t *testing.T) {
	if os.Getenv("KILO_TEST_EMBEDDINGS_LIVE") != "1" {
		t.Skip("explicit embedding live-test opt-in required")
	}
	binary, profile := os.Getenv("KILO_TEST_EMBEDDINGS_BINARY"), os.Getenv("KILO_TEST_EMBEDDINGS_PROFILE")
	if !filepath.IsAbs(binary) || !filepath.IsAbs(profile) {
		t.Fatal("explicit absolute binary/profile paths required")
	}
	saved, err := readSettings(profile)
	if err != nil || saved.OrgID == "" {
		t.Fatal("cannot load the selected saved organization")
	}
	vault, err := newHeadlessVault(profile)
	if err != nil {
		t.Fatal("cannot read the selected private profile")
	}
	key, err := vault.Get(saved.VaultID)
	if err != nil || key == "" {
		t.Fatal("cannot read the selected saved Kilo credential")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal("cannot secure the disposable profile")
	}
	cfg, err := readSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Port = ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cfg.OrgID = saved.OrgID
	cfg.Remember = true
	testVault, err := newHeadlessVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = testVault.Set(cfg.VaultID, key); err != nil {
		t.Fatal("cannot prepare private test credential")
	}
	if err = writeSettings(dir, cfg); err != nil {
		t.Fatal("cannot prepare private test settings")
	}
	cmd := exec.Command(binary, "--config-dir", dir, "serve")
	if err := cmd.Start(); err != nil {
		t.Fatal("cannot start the selected test binary")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	t.Cleanup(stop)
	ready := false
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		if _, err := readTerminalRuntime(dir); err == nil {
			ready = true
			break
		}
		select {
		case <-done:
			stopped = true
			t.Fatal("test binary exited before startup")
		default:
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		t.Fatal("test binary did not publish its private runtime")
	}
	client := &http.Client{Timeout: 50 * time.Second}
	for _, m := range []struct {
		id         string
		dimensions int
	}{{"google/gemini-embedding-2", 3072}, {"openai/text-embedding-3-small", 1536}} {
		body, _ := json.Marshal(map[string]any{"model": m.id, "input": "Kilo Proxy organization embedding access test.", "encoding_format": "float"})
		r, _ := http.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(cfg.Port)+"/v1/embeddings", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+cfg.LocalKey)
		r.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(r)
		if err != nil {
			t.Fatalf("%s local endpoint request failed", m.id)
		}
		var result struct {
			Data  []struct{ Embedding []float64 }
			Usage struct {
				Prompt int64 `json:"prompt_tokens"`
				BYOK   *bool `json:"is_byok"`
			}
		}
		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || len(result.Data) != 1 || len(result.Data[0].Embedding) != m.dimensions {
			t.Fatalf("%s local acceptance failed (HTTP %d)", m.id, resp.StatusCode)
		}
		for _, n := range result.Data[0].Embedding {
			if math.IsNaN(n) || math.IsInf(n, 0) {
				t.Fatal("non-finite vector")
			}
		}
		byok := "unknown"
		if result.Usage.BYOK != nil {
			byok = strconv.FormatBool(*result.Usage.BYOK)
		}
		t.Logf("model=%s status=%d dimensions=%d inputTokens=%d BYOK=%s", m.id, resp.StatusCode, m.dimensions, result.Usage.Prompt, byok)
	}
	stop()
}

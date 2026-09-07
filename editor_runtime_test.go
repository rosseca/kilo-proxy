package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Opt-in installed-client check, using only a synthetic local gateway.
func TestInstalledOpenCode(t *testing.T) {
	binary := os.Getenv("KILO_TEST_OPENCODE")
	if binary == "" {
		t.Skip("set KILO_TEST_OPENCODE to an installed CLI")
	}
	var requests atomic.Int32
	var mainModel atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-local-key" {
			t.Error("wrong local authentication")
		}
		var body struct {
			Model string `json:"model"`
		}
		data, _ := io.ReadAll(r.Body)
		if json.Unmarshal(data, &body) != nil || (body.Model != "vendor/one" && body.Model != "vendor/two") {
			t.Error("wrong selected model")
		}
		if body.Model == "vendor/one" {
			mainModel.Store(true)
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"vendor/one\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"SYNTHETIC_EDITOR_OK\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"vendor/one\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	dir := t.TempDir()
	config := filepath.Join(dir, "opencode.json")
	data, err := mergeEditorSettings(nil, "opencode", exampleEditorSelection(), server.URL+"/v1", "synthetic-local-key")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	env := []string{}
	for _, entry := range os.Environ() {
		name := strings.SplitN(entry, "=", 2)[0]
		if strings.HasPrefix(name, "OPENCODE_") || strings.HasPrefix(name, "XDG_") || strings.HasSuffix(name, "_API_KEY") || strings.HasSuffix(name, "_TOKEN") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "OPENCODE_CONFIG="+config, "XDG_CONFIG_HOME="+filepath.Join(dir, "config"), "XDG_DATA_HOME="+filepath.Join(dir, "data"), "XDG_CACHE_HOME="+filepath.Join(dir, "cache"), "XDG_STATE_HOME="+filepath.Join(dir, "state"))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "models", "kilo-local", "--pure")
	cmd.Env = env
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "kilo-local/vendor/one") || !strings.Contains(string(out), "kilo-local/vendor/two") {
		t.Fatalf("native model listing failed: %v %s", err, out)
	}
	cmd = exec.CommandContext(ctx, binary, "run", "--pure", "--model", "kilo-local/vendor/one", "--format", "json", "Reply OK without tools.")
	cmd.Env = env
	cmd.Dir = dir
	out, err = cmd.CombinedOutput()
	if err != nil || requests.Load() == 0 || !mainModel.Load() || !strings.Contains(string(out), "SYNTHETIC_EDITOR_OK") {
		t.Fatalf("native synthetic request failed: requests=%d error=%v output=%s", requests.Load(), err, out)
	}
	t.Log("Installed OpenCode loaded both models and completed Chat Completions using the generated local credential.")
}

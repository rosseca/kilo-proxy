package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Verify native CLI defaults without any personal login or public gateway.
// Every provider home and every upstream is a temporary synthetic fixture.
func TestClaude55InstalledPerModelDefaults(t *testing.T) {
	if os.Getenv("KILO_TEST_CLAUDE_EFFORT") != "1" {
		t.Skip("set KILO_TEST_CLAUDE_EFFORT=1 for installed Claude native defaults")
	}
	binary, err := resolveLaunchClient("claude", "")
	if err != nil {
		t.Fatal(err)
	}
	if fixtureBinary := os.Getenv("KILO_TEST_CLAUDE_EFFORT_BINARY"); fixtureBinary != "" {
		binary = fixtureBinary
	}
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	opus := "anthropic/claude-opus-5.5"
	sonnet := "anthropic/claude-sonnet-5.5"
	if os.Getenv("KILO_TEST_CLAUDE_EFFORT_HYPHEN") == "1" {
		opus, sonnet = "anthropic/claude-opus-5-5", "anthropic/claude-sonnet-5-5"
	}
	opusKey, sonnetKey := "claude-opus-5-5", "claude-sonnet-5-5"
	if os.Getenv("KILO_TEST_CLAUDE_EFFORT_RAW_KEYS") == "1" {
		opusKey, sonnetKey = opus, sonnet
	}
	if os.Getenv("KILO_TEST_CLAUDE_EFFORT_FIVE_KEYS") == "1" {
		opusKey, sonnetKey = "claude-opus-5", "claude-sonnet-5"
	}
	settings := map[string]any{"modelSettings": map[string]any{opusKey: map[string]any{"effortLevel": "high"}, sonnetKey: map[string]any{"effortLevel": "low"}}}
	data, _ := json.Marshal(settings)
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	type observed struct{ Model, Effort string }
	var mu sync.Mutex
	var actual []observed
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-claude-55-token" {
			t.Logf("native CLI auxiliary unauthenticated route: %s", r.URL.Path)
			http.Error(w, "wrong auth", 401)
			return
		}
		var body map[string]any
		if json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&body) != nil {
			http.Error(w, "bad fixture body", 400)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/count_tokens") {
			jsonResponse(w, 200, map[string]any{"input_tokens": 20})
			return
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected synthetic path: %s", r.URL.Path)
			http.Error(w, "wrong route", 404)
			return
		}
		model := stringValue(body["model"])
		if body["stream"] != true {
			jsonResponse(w, 200, map[string]any{"id": "synthetic-claude55-validation", "type": "message", "role": "assistant", "model": model, "content": []any{map[string]any{"type": "text", "text": "SYNTHETIC_CLAUDE55_OK"}}, "stop_reason": "end_turn", "stop_sequence": nil, "usage": map[string]any{"input_tokens": 20, "output_tokens": 8}})
			return
		}
		mu.Lock()
		actual = append(actual, observed{model, stringValue(object(body["output_config"])["effort"])})
		number := len(actual)
		mu.Unlock()
		t3CodeFixtureMessagesSSE(w, model, "SYNTHETIC_CLAUDE55_OK", fmt.Sprintf("msg_synthetic_claude55_%d", number))
	}))
	defer server.Close()
	values := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": configDir, "CLAUDE_SECURESTORAGE_CONFIG_DIR": configDir, "ANTHROPIC_BASE_URL": server.URL, "ANTHROPIC_AUTH_TOKEN": "synthetic-claude-55-token", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "DISABLE_TELEMETRY": "1", "DISABLE_ERROR_REPORTING": "1", "DISABLE_AUTOUPDATER": "1"}
	for _, name := range []string{"APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		values[name] = filepath.Join(home, name)
	}
	environment := clientChildEnvironment(os.Environ(), values, synaraUnsetEnvironment(os.Environ()), runtime.GOOS)
	versionCtx, versionCancel := context.WithTimeout(context.Background(), 5*time.Second)
	versionCommand := exec.CommandContext(versionCtx, binary, "--version")
	versionCommand.Env, versionCommand.Dir = environment, home
	versionOutput, versionErr := versionCommand.Output()
	versionCancel()
	if versionErr != nil || !claudeCaps(strings.TrimSpace(string(versionOutput))).PerModelEffort {
		t.Fatalf("unsupported installed Claude: %v %s", versionErr, versionOutput)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "--print", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--model", opus, "--tools", "", "--setting-sources", "user", "--no-session-persistence")
	command.Env, command.Dir = environment, home
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var waitOnce sync.Once
	var waitErr error
	wait := func() { waitOnce.Do(func() { waitErr = command.Wait() }) }
	t.Cleanup(func() { _ = stdin.Close(); _ = command.Process.Kill(); wait() })
	output := make(chan map[string]any, 32)
	go func() {
		defer close(output)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64<<10), 8<<20)
		for scanner.Scan() {
			var event map[string]any
			if json.Unmarshal(scanner.Bytes(), &event) == nil {
				output <- event
			}
		}
	}()
	encoder := json.NewEncoder(stdin)
	await := func(kind, requestID string) map[string]any {
		for {
			select {
			case event, ok := <-output:
				if !ok {
					wait()
					t.Fatalf("native Claude exited before %s: %v %s", kind, waitErr, &stderr)
				}
				if event["type"] == kind && (requestID == "" || object(event["response"])["request_id"] == requestID) {
					if event["is_error"] == true || object(event["response"])["subtype"] == "error" {
						t.Fatalf("native Claude failed: %v", event)
					}
					return event
				}
			case <-ctx.Done():
				t.Fatalf("native Claude timeout awaiting %s", kind)
			}
		}
	}
	sessionID := ""
	for i, model := range []string{opus, sonnet, opus} {
		if i > 0 {
			id := fmt.Sprintf("synthetic-model-change-%d", i)
			if err := encoder.Encode(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "set_model", "model": model}}); err != nil {
				t.Fatal(err)
			}
			await("control_response", id)
		}
		if err := encoder.Encode(map[string]any{"type": "user", "session_id": sessionID, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("Synthetic native effort turn %d", i+1)}}}, "parent_tool_use_id": nil}); err != nil {
			t.Fatal(err)
		}
		result := await("result", "")
		sessionID = stringValue(result["session_id"])
	}
	_ = stdin.Close()
	wait()
	if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("native Claude shutdown: %v %s", waitErr, &stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []observed{{opus, "high"}, {sonnet, "low"}, {opus, "high"}}
	if len(actual) != len(want) {
		t.Fatalf("expected three native turns, got %+v", actual)
	}
	for i := range want {
		if actual[i] != want[i] {
			t.Fatalf("canonical 5.5 effort defaults lost: got %+v want %+v", actual, want)
		}
	}
	t.Logf("Claude %s exact native modelSettings: %+v", strings.TrimSpace(string(versionOutput)), actual)
}

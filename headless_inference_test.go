package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the server after the offline setup process exits: both credential
// formats must survive and reach only their own upstream, without a keyring.
func TestHeadlessSavedProvidersRouteAfterOfflineSetup(t *testing.T) {
	setup := headlessTestClient(t)
	dir, port := setup.dir, headlessTestPort(t)
	if err := setup.call(context.Background(), "POST", "/api/config", map[string]any{"apiKey": "synthetic-kilo", "orgId": "synthetic-org", "port": port, "remember": true}, nil); err != nil {
		t.Fatal(err)
	}
	setup.app.chatgpt.mu.Lock()
	err := setup.app.chatgpt.saveLocked(chatGPTCredentials{Access: "synthetic-subscription", Refresh: "synthetic-refresh", Account: "synthetic-account", Expires: time.Now().Add(time.Hour).Unix()})
	setup.app.chatgpt.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	setup.close()
	c, err := openHeadlessClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	if !c.app.kiloReadyLocked() || !c.app.chatGPTReadyLocked() {
		t.Fatal("saved headless providers did not reload")
	}
	var kiloCalls, subscriptionCalls atomic.Int32
	kilo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-kilo" || r.Header.Get("X-KiloCode-OrganizationId") != "synthetic-org" || r.Header.Get("ChatGPT-Account-Id") != "" {
			t.Error("Kilo request received the wrong provider credentials")
		}
		var input struct {
			Model string `json:"model"`
			Tools []any  `json:"tools"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Model != "vendor/exact-model" || len(input.Tools) != 1 {
			t.Error("Kilo request lost the exact model or tool")
		}
		kiloCalls.Add(1)
		jsonResponse(w, 200, map[string]any{"ok": "headless Kilo reply"})
	}))
	defer kilo.Close()
	subscription := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-subscription" || r.Header.Get("ChatGPT-Account-Id") != "synthetic-account" || r.Header.Get("X-KiloCode-OrganizationId") != "" {
			t.Error("ChatGPT request received the wrong provider credentials")
		}
		var input struct {
			Model     string `json:"model"`
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
			Tools []any `json:"tools"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Model != "gpt-test" || input.Reasoning.Effort != "high" || len(input.Tools) != 1 {
			t.Error("subscription request lost model, reasoning or tool")
		}
		subscriptionCalls.Add(1)
		subscriptionTextSSE(w)
	}))
	defer subscription.Close()
	setUpstream(c.app, kilo.URL)
	c.app.chatGPTResponsesURL = subscription.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { err := serveHeadless(ctx, c, false, io.Discard); c.close(); done <- err }()
	awaitHeadlessRuntime(t, dir, done)
	for _, model := range []string{"vendor/exact-model", "chatgpt/gpt-test"} {
		payload, _ := json.Marshal(map[string]any{"model": model, "input": "Hello", "reasoning": map[string]any{"effort": "high"}, "tools": []any{map[string]any{"type": "function", "name": "inspect", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}}})
		req, _ := http.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(port)+"/v1/responses", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+c.app.config.LocalKey)
		req.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		want := "headless Kilo reply"
		if strings.HasPrefix(model, "chatgpt/") {
			want = "Subscription works"
		}
		if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), want) {
			t.Fatalf("headless inference failed for %s: HTTP %d", model, response.StatusCode)
		}
	}
	if kiloCalls.Load() != 1 || subscriptionCalls.Load() != 1 {
		t.Fatal("provider requests were duplicated or misrouted")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("headless controller did not stop")
	}
}

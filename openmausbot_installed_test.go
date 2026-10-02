package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// Opt-in desktop check. The caller uses OpenMausBot's UI in this temporary
// workspace, then creates the stop file after a synthetic chat succeeds.
// All inference is local; no installed workspace or upstream credential is used.
func TestInstalledOpenMausBotDesktop(t *testing.T) {
	manifest := os.Getenv("KILO_TEST_OPENMAUSBOT_MANIFEST")
	stop := os.Getenv("KILO_TEST_OPENMAUSBOT_STOP")
	if manifest == "" || stop == "" {
		t.Skip("set KILO_TEST_OPENMAUSBOT_MANIFEST and KILO_TEST_OPENMAUSBOT_STOP for a manual installed-desktop check")
	}
	if !filepath.IsAbs(manifest) || !filepath.IsAbs(stop) || manifest == stop {
		t.Fatal("use separate absolute manifest and stop paths in a private temporary directory")
	}
	if _, err := os.Lstat(stop); !os.IsNotExist(err) {
		t.Fatal("choose a new stop path before starting the desktop check")
	}
	a := testApp(t)
	a.apiKey = "synthetic-openmausbot-upstream"
	a.config.OrgID = "synthetic-openmausbot-team"
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a.config.Port = listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/gateway/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-openmausbot-upstream" || r.Header.Get("X-KiloCode-OrganizationId") != "synthetic-openmausbot-team" {
			t.Error("wrong upstream credential or organization")
		}
		var body struct {
			Model     string `json:"model"`
			Stream    bool   `json:"stream"`
			Reasoning struct {
				Effort  string `json:"effort"`
				Enabled *bool  `json:"enabled"`
			} `json:"reasoning"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "vendor/desktop-default" {
			t.Error("OpenMausBot did not use the configured shared default")
			http.Error(w, "unexpected model", 400)
			return
		}
		if body.Reasoning.Effort != "high" || body.Reasoning.Enabled == nil || !*body.Reasoning.Enabled {
			t.Error("OpenMausBot's prepared high reasoning did not reach the gateway")
			http.Error(w, "unexpected reasoning", 400)
			return
		}
		requests.Add(1)
		if !body.Stream {
			jsonResponse(w, 200, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "OPENMAUSBOT_DESKTOP_OK"}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 4}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"omb-test\",\"object\":\"chat.completion.chunk\",\"model\":\"vendor/desktop-default\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"OPENMAUSBOT_DESKTOP_OK\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"omb-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":4,\"total_tokens\":16}}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	setUpstream(a, upstream.URL)
	library := modelLibrary{SchemaVersion: 1, DefaultModel: "vendor/desktop-default", Models: []modelLibraryItem{{ID: "chatgpt/gpt-6.1-sol", DisplayName: "Subscription test", ContextPreset: contextPresetLow, ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, ReasoningEffort: "low"}, {ID: "vendor/desktop-default", DisplayName: "Default test", ContextPreset: contextPresetLow, ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, ReasoningEffort: "high"}}}
	if _, err := a.modelLibrary.save(library, 0, false); err != nil {
		t.Fatal(err)
	}
	prepared := adminRequest(a, "clients/openmausbot", `{}`)
	if prepared.Code != 200 {
		t.Fatal("profile preparation failed", prepared.Code, prepared.Body.String())
	}
	launched := adminRequest(a, "clients/launch", `{"client":"openmausbot"}`)
	if launched.Code != 200 {
		t.Fatal("desktop launch failed", launched.Code, launched.Body.String())
	}
	data, _ := json.Marshal(map[string]any{"configDir": a.dir, "workspace": filepath.Join(a.dir, "openmausbot"), "proxyPort": strconv.Itoa(a.config.Port)})
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(15 * time.Minute)
	defer timeout.Stop()
	runningChecked := false
	for {
		select {
		case <-timeout.C:
			t.Fatal("installed desktop validation timed out; quit only the temporary OpenMausBot workspace")
		case <-ticker.C:
			if !runningChecked {
				paths := openMausBotProfilePaths(a.dir)
				live, err := openMausBotRunning(paths)
				if err != nil {
					t.Fatal("cannot inspect the running private desktop", err)
				}
				if live {
					// Clear only the launch reservation so this check exercises the
					// installed application's real lease and process identity.
					a.mu.Lock()
					a.openMausBotLaunchUntil = time.Time{}
					a.mu.Unlock()
					before, err := os.ReadFile(paths.Config)
					if err != nil {
						t.Fatal(err)
					}
					metadata := adminRequest(a, "clients/openmausbot", "")
					var info struct {
						Prepared bool `json:"prepared"`
					}
					if metadata.Code != 200 || json.Unmarshal(metadata.Body.Bytes(), &info) != nil || !info.Prepared {
						t.Fatal("live prepared metadata is incorrect")
					}
					if response := adminRequest(a, "clients/openmausbot", `{}`); response.Code != 200 {
						t.Fatal("unchanged live preparation was rejected", response.Code)
					}
					changed := library
					changed.DefaultModel = library.Models[0].ID
					payload, _ := json.Marshal(map[string]any{"library": changed})
					if response := adminRequest(a, "clients/openmausbot", string(payload)); response.Code != 409 {
						t.Fatal("changed configuration was allowed while the private desktop runs", response.Code)
					}
					after, err := os.ReadFile(paths.Config)
					if err != nil || !bytes.Equal(before, after) {
						t.Fatal("live configuration changed during an unchanged or refused preparation")
					}
					if _, err := a.planClientLaunch(clientLaunchRequest{Client: "openmausbot"}, a.launchRuntime()); err != nil {
						t.Fatal("unchanged live launch plan was rejected", err)
					}
					runningChecked = true
					t.Log("Real desktop lease verified; unchanged settings accepted and changed settings refused without writes.")
				}
			}
			if _, err := os.Stat(stop); err == nil {
				if !runningChecked || requests.Load() == 0 {
					t.Fatal("no synthetic chat completed through Kilo Proxy")
				}
				t.Log("Installed OpenMausBot launched with the generated isolated profile and completed a synthetic chat through Kilo Proxy.")
				return
			}
		}
	}
}

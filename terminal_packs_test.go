package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTerminalModelLibraryUsesAssignedPackAndSharedFallback(t *testing.T) {
	dir := t.TempDir()
	a, err := newApp(dir, &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.requestQuit)
	a.config.OrgID = "test-team"
	catalog := nativeCachedCatalog{SchemaVersion: 1, OrgID: "test-team", Models: []modelInfo{
		{ID: "vendor/qa", Name: "QA", ContextWindow: 200000, MaxOutputTokens: 8192},
		{ID: "vendor/other", Name: "Other", ContextWindow: 128000, MaxOutputTokens: 4096},
	}}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model-catalog.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	shared := modelLibrary{SchemaVersion: 1, DefaultModel: "vendor/other", Models: []modelLibraryItem{{ID: "vendor/other", ContextPreset: contextPresetRecommended}}}
	packs := emptyModelPacksFile()
	packs.Personal = []personalModelPack{{
		ID: "mine-qa", Name: "My QA", Library: modelLibrary{SchemaVersion: 1, DefaultModel: "vendor/qa", Models: []modelLibraryItem{{ID: "vendor/qa", ContextPreset: contextPresetRecommended}}},
	}}
	packs.Assignments["codex-cli"] = "mine-qa"
	if err := newModelPacksStore(dir).save(packs, false); err != nil {
		t.Fatal(err)
	}
	got, err := a.terminalModelLibrary("codex-cli", shared)
	if err != nil || got.DefaultModel != "vendor/qa" || len(got.Models) != 1 || got.Models[0].ID != "vendor/qa" {
		t.Fatalf("terminal command ignored assigned pack: %+v, %v", got, err)
	}
	fallback, err := a.terminalModelLibrary("claude", shared)
	if err != nil || !reflect.DeepEqual(fallback, shared) {
		t.Fatalf("unassigned terminal command changed its shared selection: %+v, %v", fallback, err)
	}
	if shared.DefaultModel != "vendor/other" {
		t.Fatal("pack switch changed the shared library")
	}
	packs.Assignments["codex-cli"] = "full-stack-dev"
	if err := newModelPacksStore(dir).save(packs, false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.terminalModelLibrary("codex-cli", shared); err == nil || !strings.Contains(err.Error(), "Refresh Models") {
		t.Fatalf("builtin pack with missing team models was accepted: %v", err)
	}
}

func TestTerminalModelLibraryDoesNotIgnoreCorruptPackFile(t *testing.T) {
	dir := t.TempDir()
	a, err := newApp(dir, &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.requestQuit)
	if err := os.WriteFile(filepath.Join(dir, "model-packs.json"), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.terminalModelLibrary("codex-cli", emptyModelLibrary()); err == nil || !strings.Contains(err.Error(), "recovery") {
		t.Fatalf("corrupt assignments silently fell back to shared library: %v", err)
	}
}

func TestTerminalPrepareHTTPUsesAssignedPackAndSwitchesBack(t *testing.T) {
	a := terminalTestApp(t)
	cache, err := json.Marshal(nativeCachedCatalog{SchemaVersion: 1, OrgID: a.config.OrgID, Models: []modelInfo{
		{ID: "vendor/one", Name: "First", ContextWindow: 128000, MaxOutputTokens: 4096},
		{ID: "vendor/two", Name: "Second", ContextWindow: 128000, MaxOutputTokens: 4096},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicCatalogFile(filepath.Join(a.dir, "model-catalog.json"), cache); err != nil {
		t.Fatal(err)
	}
	store := newModelPacksStore(a.dir)
	file := emptyModelPacksFile()
	file.Personal = []personalModelPack{{ID: "mine-terminal", Name: "My Terminal", Library: modelLibrary{
		SchemaVersion: 1, DefaultModel: "vendor/one", Models: []modelLibraryItem{{ID: "vendor/one", ContextPreset: contextPresetRecommended}},
	}}}
	file.Assignments["codex-cli"] = "mine-terminal"
	if err := store.save(file, false); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(terminalPrepareRequest{Client: "codex-cli", Directory: a.launcher.home})
	if w := adminRequest(a, "terminal/prepare", string(request)); w.Code != 200 {
		t.Fatalf("assigned terminal pack was rejected: %d %s", w.Code, w.Body.String())
	}
	catalog, err := os.ReadFile(filepath.Join(a.codexCLIProfileDir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if first, err := validateCatalog(catalog); err != nil || first != "vendor/one" || strings.Contains(string(catalog), `"vendor/two"`) {
		t.Fatalf("terminal profile ignored assigned pack: %s, %v", first, err)
	}
	delete(file.Assignments, "codex-cli")
	if err := newModelPacksStore(a.dir).save(file, false); err != nil {
		t.Fatal(err)
	}
	if w := adminRequest(a, "terminal/prepare", string(request)); w.Code != 200 {
		t.Fatalf("shared-library terminal command was rejected: %d %s", w.Code, w.Body.String())
	}
	catalog, err = os.ReadFile(filepath.Join(a.codexCLIProfileDir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if first, err := validateCatalog(catalog); err != nil || first != "vendor/two" || !strings.Contains(string(catalog), `"vendor/one"`) {
		t.Fatalf("terminal command did not restore shared models: %s, %v", first, err)
	}
	if got := a.modelLibrary.snapshot().Library.DefaultModel; got != "vendor/two" {
		t.Fatalf("pack switch changed saved shared default: %s", got)
	}
}

func TestTerminalPacksWithChatGPTAndEmptySharedLibrary(t *testing.T) {
	for _, both := range []bool{false, true} {
		for _, client := range []string{"codex-cli", "claude", "omp", "opencode"} {
			t.Run(fmt.Sprintf("both=%t/%s", both, client), func(t *testing.T) {
				a := terminalTestApp(t)
				state := a.modelLibrary.snapshot()
				if _, err := a.modelLibrary.save(emptyModelLibrary(), state.Revision, false); err != nil {
					t.Fatal(err)
				}
				if !both {
					a.apiKey, a.config.OrgID = "", ""
				}
				a.chatgpt.creds = chatGPTCredentials{Access: "synthetic-pack-access", Refresh: "synthetic-pack-refresh", Account: "synthetic-pack-account", Expires: time.Now().Add(time.Hour).Unix()}
				a.chatgpt.state = chatGPTState{Status: "idle"}
				pack := modelLibrary{SchemaVersion: 1, DefaultModel: "chatgpt/gpt-test", Models: []modelLibraryItem{{ID: "chatgpt/gpt-test", DisplayName: "Subscription GPT", ContextPreset: contextPresetMaximum}}}
				models := []modelInfo{{ID: "chatgpt/gpt-test", Name: "Subscription GPT", ContextWindow: 128000, MaxOutputTokens: 8192, InputModalities: []string{"text", "image"}}}
				if both {
					pack.Models = append(pack.Models, modelLibraryItem{ID: "openai/gpt-test", DisplayName: "Kilo GPT", ContextPreset: contextPresetMaximum})
					models = append(models, modelInfo{ID: "openai/gpt-test", Name: "Kilo GPT", ContextWindow: 256000, MaxOutputTokens: 16384})
				}
				file := emptyModelPacksFile()
				file.Personal = []personalModelPack{{ID: "mine-subscription", Name: "Subscription pack", Library: pack}}
				file.Assignments[client] = "mine-subscription"
				if err := newModelPacksStore(a.dir).save(file, false); err != nil {
					t.Fatal(err)
				}
				writeCatalog := func(scope string) {
					t.Helper()
					data, err := json.Marshal(nativeCachedCatalog{SchemaVersion: 1, OrgID: scope, Models: models})
					if err != nil {
						t.Fatal(err)
					}
					if err := atomicCatalogFile(filepath.Join(a.dir, "model-catalog.json"), data); err != nil {
						t.Fatal(err)
					}
				}
				request, _ := json.Marshal(terminalPrepareRequest{Client: client, Directory: a.launcher.home, ClaudeVersion: "2.1.263"})
				writeCatalog(a.config.OrgID)
				if w := adminRequest(a, "terminal/prepare", string(request)); w.Code != 409 || !strings.Contains(w.Body.String(), "Refresh Models") {
					t.Fatalf("wrong account catalog accepted: %d %s", w.Code, w.Body.String())
				}
				if a.proxyListener != nil {
					t.Fatal("rejected catalog started proxy")
				}
				writeCatalog(a.catalogScopeLocked())
				w := adminRequest(a, "terminal/prepare", string(request))
				if w.Code != 200 {
					t.Fatalf("valid pack with empty shared library rejected: %d %s", w.Code, w.Body.String())
				}
				for _, secret := range []string{a.chatgpt.creds.Access, a.chatgpt.creds.Refresh, a.chatgpt.creds.Account} {
					if strings.Contains(w.Body.String(), secret) {
						t.Fatal("terminal plan exposed subscription credentials")
					}
				}
				var path string
				switch client {
				case "codex-cli":
					path = filepath.Join(a.codexCLIProfileDir, "models.json")
				case "claude":
					path = filepath.Join(a.claudeProfileDir, "kilo-models.json")
				case "omp":
					path = filepath.Join(a.ompProfileDir, "kilo-models.json")
				case "opencode":
					_, _, path, _ = a.editorPaths("opencode")
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var saved struct {
					Initial string `json:"initial"`
					Models  []struct {
						ID      string `json:"id"`
						Slug    string `json:"slug"`
						Context int    `json:"contextWindow"`
						Window  int    `json:"context_window"`
					} `json:"models"`
				}
				if err := json.Unmarshal(data, &saved); err != nil {
					t.Fatal(err)
				}
				if len(saved.Models) != len(pack.Models) {
					t.Fatalf("profile ignored assigned pack: %s", data)
				}
				if client == "codex-cli" {
					first, err := validateCatalog(data)
					if err != nil || first != pack.DefaultModel || saved.Models[0].Window != 128000 {
						t.Fatalf("Codex lost scope metadata/default: %s %v", data, err)
					}
				} else if saved.Initial != pack.DefaultModel {
					t.Fatalf("profile lost pack default: %s", data)
				}
				for i, model := range saved.Models {
					id := model.ID
					if client == "codex-cli" {
						id = model.Slug
					}
					if id != pack.Models[i].ID {
						t.Fatalf("profile collapsed model connections: %s", data)
					}
					if (client == "omp" || client == "opencode") && model.Context != models[i].ContextWindow {
						t.Fatalf("profile lost scoped maximum: %s", data)
					}
				}
				if got := a.modelLibrary.snapshot().Library; len(got.Models) != 0 || got.DefaultModel != "" {
					t.Fatal("pack rewrote shared library")
				}
				delete(file.Assignments, client)
				if err := newModelPacksStore(a.dir).save(file, false); err != nil {
					t.Fatal(err)
				}
				if w := adminRequest(a, "terminal/prepare", string(request)); w.Code != 409 {
					t.Fatalf("empty effective library accepted: %d", w.Code)
				}
			})
		}
	}
}

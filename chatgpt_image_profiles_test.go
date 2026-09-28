package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setImageProfileTestConnections(a *app, kilo, chatgpt bool) {
	a.apiKey, a.config.OrgID = "", ""
	if kilo {
		a.apiKey, a.config.OrgID = "synthetic-profile-kilo-secret", "synthetic-profile-team"
	}
	a.chatgpt.creds = chatGPTCredentials{}
	if chatgpt {
		a.chatgpt.creds = chatGPTCredentials{Access: "synthetic-profile-chatgpt-access", Refresh: "synthetic-profile-chatgpt-refresh", Account: "synthetic-profile-account", Expires: time.Now().Add(time.Hour).Unix()}
	}
	a.chatgpt.state = chatGPTState{Status: "idle"}
}

func TestImageProfileProviderAvailabilityPreservesSettings(t *testing.T) {
	for _, provider := range []string{"", "kilo", "chatgpt"} {
		for _, connections := range []struct {
			name          string
			kilo, chatgpt bool
		}{{"none", false, false}, {"kilo", true, false}, {"chatgpt", false, true}, {"both", true, true}} {
			t.Run(provider+"/"+connections.name, func(t *testing.T) {
				a, _ := chatGPTProfileApp(t)
				setImageProfileTestConnections(a, connections.kilo, connections.chatgpt)
				for _, enabled := range []bool{false, true} {
					saved := imageGenerationSettings{Enabled: enabled, Provider: provider, Model: "vendor/preserved-image"}
					a.config.ImageGeneration = saved
					want := saved
					available := connections.kilo
					if provider == "chatgpt" {
						available = connections.chatgpt
					}
					want.Enabled = enabled && available
					if got := a.clientImageSettingsLocked(); got != want {
						t.Fatalf("effective setting = %+v, want %+v", got, want)
					}
					if a.config.ImageGeneration != saved {
						t.Fatal("availability check changed saved settings")
					}
				}
			})
		}
	}
	for _, pending := range []string{"kilo-login", "kilo-save", "chatgpt-login", "chatgpt-changing"} {
		t.Run(pending, func(t *testing.T) {
			a, _ := chatGPTProfileApp(t)
			setImageProfileTestConnections(a, true, true)
			images := imageGenerationSettings{Enabled: true, Provider: "chatgpt", Model: "vendor/preserved-image"}
			switch pending {
			case "kilo-login":
				images.Provider = "kilo"
				a.login = &loginSession{Status: "pending"}
			case "kilo-save":
				images.Provider = "kilo"
				a.connectionNeedsSave = true
			case "chatgpt-login":
				a.chatgpt.state.Status = "pending"
			case "chatgpt-changing":
				a.providerChanging = true
			}
			a.config.ImageGeneration = images
			if a.clientImageSettingsLocked().Enabled || a.config.ImageGeneration != images {
				t.Fatal("pending connection exposed image tool or erased selection")
			}
		})
	}
}

// Exercise each existing MCP exporter through its actual profile preparation
// path, then remove/reconnect the selected account while the other stays ready.
func TestImageProfileSelectedProviderGatesEveryMCPExporter(t *testing.T) {
	for _, provider := range []string{"", "kilo", "chatgpt"} {
		for _, client := range []string{"codex", "codex-cli", "omp", "opencode", "open-design-codex", "open-design-opencode"} {
			t.Run(provider+"/"+client, func(t *testing.T) {
				a, library := chatGPTProfileApp(t)
				images := imageGenerationSettings{Enabled: true, Provider: provider, Model: "vendor/preserved-image"}
				a.config.ImageGeneration = images
				catalog, err := buildCodexCatalog(terminalLibraryChoices(library, nil), library.DefaultModel, false)
				if err != nil {
					t.Fatal(err)
				}
				prepare := func() (string, string) {
					t.Helper()
					var err error
					var path, kind string
					switch client {
					case "codex":
						_, _, err = a.saveCodexImageProfile(a.codexProfileDir, catalog, nil)
						path, kind = filepath.Join(a.codexProfileDir, "config.toml"), "codex"
					case "codex-cli", "omp", "opencode":
						err = a.prepareTerminalProfile(client, a.launcher.home, library, claudeCapabilities{})
						switch client {
						case "codex-cli":
							path, kind = filepath.Join(a.codexCLIProfileDir, "config.toml"), "codex"
						case "omp":
							path, kind = filepath.Join(a.ompProfileDir, "mcp.json"), "omp"
						case "opencode":
							_, path, _, _ = a.editorPaths("opencode")
							kind = "opencode"
						}
					default:
						engine := "codex-cli"
						if client == "open-design-opencode" {
							engine = "opencode"
						}
						dir := filepath.Join(a.launcher.home, client)
						err = prepareOpenDesignEngineProfile(dir, engine, library, nil, claudeCapabilities{}, a.config.Port, a.config.LocalKey, a.clientImageSettingsLocked())
						path, kind = filepath.Join(dir, "config.toml"), "codex"
						if engine == "opencode" {
							path, kind = filepath.Join(dir, "opencode.json"), "opencode"
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					return path, kind
				}
				for _, connections := range []struct{ selected, other bool }{{true, true}, {true, false}, {false, true}, {true, true}} {
					ready := connections.selected
					if provider == "chatgpt" {
						setImageProfileTestConnections(a, connections.other, ready)
					} else {
						setImageProfileTestConnections(a, ready, connections.other)
					}
					path, kind := prepare()
					config := chatGPTProfileMap(t, a, path)
					var server map[string]any
					switch kind {
					case "codex":
						server = object(object(config["mcp_servers"])[codexImagesServer])
					case "omp":
						server = object(object(config["mcpServers"])["kilo-images"])
					case "opencode":
						server = object(object(config["mcp"])["kilo_images"])
					}
					if (server != nil) != ready {
						t.Fatalf("tool presence %v, selected provider ready %v", server != nil, ready)
					}
					if server != nil {
						if server["url"] != "http://127.0.0.1:8877/mcp/images" {
							t.Fatal("profile bypassed local image endpoint")
						}
						if kind == "codex" {
							if server["bearer_token_env_var"] != "KILO_LOCAL_API_KEY" {
								t.Fatal("Codex image auth is not local")
							}
						} else if object(server["headers"])["Authorization"] != "Bearer "+a.config.LocalKey {
							t.Fatal("image MCP auth is not local")
						}
					}
					if a.config.ImageGeneration != images {
						t.Fatal("profile generation changed saved image selection")
					}
				}
				if a.proxyServer != nil {
					t.Fatal("preparing image tools started inference server")
				}
			})
		}
	}
}

func TestCodexChatGPTImageDraftPersistsWhileDisconnected(t *testing.T) {
	for _, endpoint := range []string{"codex/catalog", "codex-cli/catalog"} {
		t.Run(endpoint, func(t *testing.T) {
			a, _ := chatGPTProfileApp(t)
			setImageProfileTestConnections(a, true, false)
			old := imageGenerationSettings{Enabled: true, Model: "vendor/keep-kilo-choice"}
			a.config.ImageGeneration = old
			// ChatGPT needs no Kilo model selection, while a prior Kilo choice
			// remains valid metadata when switching providers back and forth.
			for _, model := range []string{"", old.Model} {
				draft := imageGenerationSettings{Enabled: true, Provider: "chatgpt", Model: model}
				body := chatGPTProfileJSON(t, map[string]any{"catalog": json.RawMessage(testCatalog), "imageGeneration": draft})
				response := adminRequest(a, endpoint, body)
				if response.Code != 200 {
					t.Fatal(response.Code, response.Body.String())
				}
				if a.config.ImageGeneration != draft {
					t.Fatal("disconnected draft was not saved in memory")
				}
				persisted, err := readSettings(a.dir)
				if err != nil || persisted.ImageGeneration != draft {
					t.Fatalf("disconnected draft was not persisted: %v", err)
				}
				for _, w := range []*httptest.ResponseRecorder{response, adminRequest(a, endpoint, ""), adminRequest(a, "state", "")} {
					var state struct {
						ImageGeneration imageGenerationSettings `json:"imageGeneration"`
					}
					if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || state.ImageGeneration != draft {
						t.Fatal("API erased unavailable provider draft", w.Code, w.Body.String())
					}
				}
				dir := a.codexProfileDir
				if endpoint == "codex-cli/catalog" {
					dir = a.codexCLIProfileDir
				}
				if object(chatGPTProfileMap(t, a, filepath.Join(dir, "config.toml"))["mcp_servers"])[codexImagesServer] != nil {
					t.Fatal("unavailable ChatGPT images fell back to connected Kilo")
				}
			}
		})
	}
}

func TestCodexImageProviderDraftRollback(t *testing.T) {
	a, _ := chatGPTProfileApp(t)
	setImageProfileTestConnections(a, true, true)
	old := imageGenerationSettings{Enabled: true, Model: "vendor/old-image"}
	if _, _, err := a.saveCodexImageProfile(a.codexProfileDir, []byte(testCatalog), &old); err != nil {
		t.Fatal(err)
	}
	before := chatGPTProfileRead(t, a, filepath.Join(a.codexProfileDir, "config.toml"))
	backup := filepath.Join(a.dir, "settings.json.bak")
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Mkdir(backup, 0700); err != nil {
		t.Fatal(err)
	}
	draft := imageGenerationSettings{Enabled: true, Provider: "chatgpt", Model: old.Model}
	if _, _, err := a.saveCodexImageProfile(a.codexProfileDir, []byte(testCatalog), &draft); err == nil {
		t.Fatal("accepted unsafe settings backup")
	}
	if a.config.ImageGeneration != old || !bytes.Equal(before, chatGPTProfileRead(t, a, filepath.Join(a.codexProfileDir, "config.toml"))) {
		t.Fatal("failed provider change modified saved preference or profile")
	}
}

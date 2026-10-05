package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func synaraAccountPreferenceFixture(t *testing.T) *app {
	t.Helper()
	a := synaraTestApp(t, "macos")
	library := modelLibrary{SchemaVersion: 1, DefaultModel: "anthropic/claude-opus-5.5", Models: []modelLibraryItem{{ID: "anthropic/claude-opus-5.5"}}}
	if _, err := a.modelLibrary.save(library, a.modelLibrary.snapshot().Revision, false); err != nil {
		t.Fatal(err)
	}
	if w := adminRequest(a, "clients/synara", `{}`); w.Code != 200 {
		t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
	}
	return a
}

func changeSynaraAccountSettings(t *testing.T, a *app, change func(map[string]any)) []byte {
	t.Helper()
	path := synaraPaths(a.dir).Settings
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	change(document["settings"].(map[string]any))
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func synaraOpusPreferenceRequest() *http.Request {
	raw := `{"model":"anthropic/claude-opus-5.5","max_tokens":100,"output_config":{"effort":"high"},"messages":[{"role":"user","content":"fixture"}]}`
	return httptest.NewRequest("POST", "http://127.0.0.1/v1/messages", strings.NewReader(raw)).WithContext(context.WithValue(context.Background(), synaraRequestContextKey{}, true))
}

func TestSynaraAccountEnabledPreferencesPreserveManagedRequests(t *testing.T) {
	ids := []string{synaraCodexNormalID, synaraCodexProxyID, synaraClaudeNormalID, synaraClaudeProxyID}
	for _, disabled := range append(append([]string{}, ids...), "all") {
		t.Run(disabled, func(t *testing.T) {
			a := synaraAccountPreferenceFixture(t)
			paths := synaraPaths(a.dir)
			selectionBefore, err := os.ReadFile(paths.Selection)
			if err != nil {
				t.Fatal(err)
			}
			settingsBefore := changeSynaraAccountSettings(t, a, func(settings map[string]any) {
				instances := settings["providerInstances"].(map[string]any)
				for _, id := range ids {
					if disabled == id || disabled == "all" {
						instances[id].(map[string]any)["enabled"] = false
					}
				}
			})
			request, err := a.prepareSynaraRequest(synaraOpusPreferenceRequest())
			if err != nil {
				t.Fatalf("disabled account rejected a managed Opus request: %v", err)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Model        string `json:"model"`
				OutputConfig struct {
					Effort string `json:"effort"`
				} `json:"output_config"`
			}
			if err := json.Unmarshal(body, &payload); err != nil || payload.Model != "anthropic/claude-opus-5.5" || payload.OutputConfig.Effort != "high" {
				t.Fatalf("managed model/effort changed: %s (%v)", body, err)
			}
			saved, err := a.readSynaraPrepared()
			if err != nil {
				t.Fatal(err)
			}
			rt := a.launchRuntime()
			binary, err := rt.resolve("synara", "")
			if err != nil || !a.synaraReady(saved, binary, rt) {
				t.Fatalf("account preference invalidated readiness: %v", err)
			}
			for _, id := range ids {
				if saved.Providers[id].(map[string]any)["enabled"] != true {
					t.Fatalf("readiness mutated prepared provider %s", id)
				}
			}
			if w := adminRequest(a, "clients/synara", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"prepared":true`) {
				t.Fatalf("GET rejected preferences: %d %s", w.Code, w.Body.String())
			}
			a.synaraCheckRunning = func(string) (bool, error) { return true, nil }
			if w := adminRequest(a, "clients/synara", `{}`); w.Code != 200 {
				t.Fatalf("unchanged running prepare rejected preferences: %d %s", w.Code, w.Body.String())
			}
			a.synaraCheckRunning = func(string) (bool, error) { return false, nil }
			if _, err = a.planClientLaunch(clientLaunchRequest{Client: "synara"}, rt); err != nil {
				t.Fatalf("launch rejected preferences: %v", err)
			}
			for path, before := range map[string][]byte{paths.Settings: settingsBefore, paths.Selection: selectionBefore} {
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("request/metadata/unchanged prepare/launch rewrote %s: %v", path, err)
				}
			}
		})
	}
}

func TestSynaraAccountPreferencesStillRequireManagedBoundaries(t *testing.T) {
	for _, id := range []string{synaraCodexNormalID, synaraCodexProxyID, synaraClaudeNormalID, synaraClaudeProxyID} {
		for _, value := range []struct {
			name    string
			value   any
			missing bool
		}{{name: "missing", missing: true}, {name: "null"}, {name: "string", value: "false"}, {name: "number", value: float64(0)}} {
			t.Run(id+"/invalid-enabled/"+value.name, func(t *testing.T) {
				a := synaraAccountPreferenceFixture(t)
				changeSynaraAccountSettings(t, a, func(settings map[string]any) {
					instance := settings["providerInstances"].(map[string]any)[id].(map[string]any)
					if value.missing {
						delete(instance, "enabled")
					} else {
						instance["enabled"] = value.value
					}
				})
				if _, err := a.prepareSynaraRequest(synaraOpusPreferenceRequest()); err == nil {
					t.Fatal("missing/nonboolean enabled bypassed managed readiness")
				}
			})
		}
	}
	changes := map[string]func(*app, map[string]any){
		"missing-account": func(a *app, settings map[string]any) {
			delete(settings["providerInstances"].(map[string]any), synaraClaudeNormalID)
		},
		"driver": func(a *app, settings map[string]any) {
			settings["providerInstances"].(map[string]any)[synaraClaudeProxyID].(map[string]any)["driver"] = "codex"
		},
		"home": func(a *app, settings map[string]any) {
			settings["providerInstances"].(map[string]any)[synaraClaudeProxyID].(map[string]any)["config"].(map[string]any)["homePath"] = a.launchRuntime().home
		},
		"binary": func(a *app, settings map[string]any) {
			settings["providerInstances"].(map[string]any)[synaraClaudeProxyID].(map[string]any)["config"].(map[string]any)["binaryPath"] = "/different/claude"
		},
		"models": func(a *app, settings map[string]any) {
			settings["providerInstances"].(map[string]any)[synaraClaudeProxyID].(map[string]any)["config"].(map[string]any)["customModels"] = []any{}
		},
		"endpoint": func(a *app, settings map[string]any) {
			variables := settings["providerInstances"].(map[string]any)[synaraClaudeProxyID].(map[string]any)["environment"].([]any)
			for _, raw := range variables {
				variable := raw.(map[string]any)
				if variable["name"] == "ANTHROPIC_BASE_URL" {
					variable["value"] = "http://127.0.0.1:1/other"
				}
			}
		},
		"redaction": func(a *app, settings map[string]any) {
			variables := settings["providerInstances"].(map[string]any)[synaraClaudeProxyID].(map[string]any)["environment"].([]any)
			for _, raw := range variables {
				variable := raw.(map[string]any)
				if variable["name"] == "ANTHROPIC_AUTH_TOKEN" {
					delete(variable, "valueRedacted")
				}
			}
		},
		"managed-file": func(a *app, settings map[string]any) {
			if err := os.WriteFile(filepath.Join(synaraPaths(a.dir).Root, "profiles", "claude-kilo", "settings.json"), []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"secret": func(a *app, settings map[string]any) {
			if err := os.WriteFile(filepath.Join(synaraPaths(a.dir).Secrets, t3CodeSecretName(synaraClaudeProxyID, "ANTHROPIC_AUTH_TOKEN")), []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			a := synaraAccountPreferenceFixture(t)
			changeSynaraAccountSettings(t, a, func(settings map[string]any) {
				settings["providerInstances"].(map[string]any)[synaraClaudeNormalID].(map[string]any)["enabled"] = false
				change(a, settings)
			})
			if _, err := a.prepareSynaraRequest(synaraOpusPreferenceRequest()); err == nil {
				t.Fatal("account preference bypassed an owned profile/connection boundary")
			}
		})
	}
}

func TestSynaraAccountPreferencesSurviveChangedConnectionPreparation(t *testing.T) {
	a := synaraAccountPreferenceFixture(t)
	changeSynaraAccountSettings(t, a, func(settings map[string]any) {
		instances := settings["providerInstances"].(map[string]any)
		instances[synaraClaudeNormalID].(map[string]any)["enabled"] = false
		instances[synaraCodexNormalID].(map[string]any)["enabled"] = false
	})
	a.config.Port++
	if w := adminRequest(a, "clients/synara", `{}`); w.Code != 200 {
		t.Fatalf("changed connection prepare: %d %s", w.Code, w.Body.String())
	}
	raw, err := os.ReadFile(synaraPaths(a.dir).Settings)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Settings struct {
			Instances map[string]struct {
				Enabled bool `json:"enabled"`
			} `json:"providerInstances"`
		} `json:"settings"`
	}
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{synaraClaudeNormalID, synaraCodexNormalID} {
		if document.Settings.Instances[id].Enabled {
			t.Fatalf("repreparation enabled the unused account %s", id)
		}
	}
	for _, id := range []string{synaraClaudeProxyID, synaraCodexProxyID} {
		if !document.Settings.Instances[id].Enabled {
			t.Fatalf("repreparation disabled the active Kilo account %s", id)
		}
	}
	if _, err := a.prepareSynaraRequest(synaraOpusPreferenceRequest()); err != nil {
		t.Fatalf("preserved account preferences rejected the newly prepared connection: %v", err)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func imagesTOML(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := toml.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCodexImagesConfigLifecycle(t *testing.T) {
	original := []byte("# Keep my settings\nmodel = 'vendor/code'\n[mcp_servers.docs]\ncommand = 'my-docs-server'\nargs = ['--local']\n")
	images := imageGenerationSettings{Enabled: true, Model: "vendor/image"}
	data, err := mergeCodexImages(original, images, 8877)
	if err != nil {
		t.Fatal(err)
	}
	value := imagesTOML(t, data)
	servers := value["mcp_servers"].(map[string]any)
	managed := servers[codexImagesServer].(map[string]any)
	if managed["url"] != "http://127.0.0.1:8877/mcp/images" || managed["bearer_token_env_var"] != "KILO_LOCAL_API_KEY" || managed["tool_timeout_sec"] != int64(360) || managed["enabled"] != true {
		t.Fatalf("incorrect managed config: %v", managed)
	}
	if !bytes.Contains(data, []byte("# Keep my settings")) || servers["docs"].(map[string]any)["command"] != "my-docs-server" || value["model"] != "vendor/code" {
		t.Fatal("unrelated settings changed")
	}
	if strings.Contains(string(data), "vendor/image") || strings.Contains(string(data), "kl_local_") {
		t.Fatal("image model/credential should stay in proxy settings, not coding config")
	}
	again, err := mergeCodexImages(data, images, 8877)
	if err != nil || !bytes.Equal(data, again) {
		t.Fatalf("not idempotent: %v", err)
	}
	updated, err := mergeCodexImages(data, images, 8899)
	if err != nil || !bytes.Contains(updated, []byte("http://127.0.0.1:8899/mcp/images")) || bytes.Contains(updated, []byte(":8877/")) {
		t.Fatalf("port update failed: %v", err)
	}
	disabled, err := mergeCodexImages(updated, imageGenerationSettings{Model: "vendor/image"}, 8899)
	if err != nil {
		t.Fatal(err)
	}
	servers = imagesTOML(t, disabled)["mcp_servers"].(map[string]any)
	if servers[codexImagesServer] != nil || servers["docs"] == nil {
		t.Fatal("disable must remove only the owned MCP entry")
	}
}

func TestCodexImagesRejectsServerCollision(t *testing.T) {
	for _, entry := range []string{
		"command = 'other-server'",
		"url = 'https://example.com/mcp'\nbearer_token_env_var = 'KILO_LOCAL_API_KEY'",
		"url = 'http://127.0.0.1:8877/mcp/images?override=1'\nbearer_token_env_var = 'KILO_LOCAL_API_KEY'",
		"url = 'http://127.0.0.1:8877/mcp/images'\nbearer_token_env_var = 'KILO_LOCAL_API_KEY'\ncommand = 'unrelated-server'",
	} {
		data := []byte("[mcp_servers.kilo_images]\n" + entry + "\n")
		if _, err := mergeCodexImages(data, imageGenerationSettings{Enabled: true, Model: "vendor/image"}, 8877); err == nil {
			t.Fatal("overwrote unrelated server")
		}
		got, err := mergeCodexImages(data, imageGenerationSettings{}, 8877)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatal("disable deleted unrelated server")
		}
	}
}

func TestCodexImageProfilePreparePersistsSettings(t *testing.T) {
	for _, endpoint := range []string{"codex/catalog", "codex-cli/catalog"} {
		t.Run(endpoint, func(t *testing.T) {
			a := testApp(t)
			dir := filepath.Join(t.TempDir(), "profile")
			a.codexProfileDir, a.codexCLIProfileDir = dir, dir
			body, _ := json.Marshal(map[string]any{"catalog": json.RawMessage(testCatalog), "imageGeneration": imageGenerationSettings{Enabled: true, Model: "image-lab/painter"}})
			response := adminRequest(a, endpoint, string(body))
			if response.Code != 200 {
				t.Fatal(response.Body.String())
			}
			if !a.config.ImageGeneration.Enabled || a.config.ImageGeneration.Model != "image-lab/painter" {
				t.Fatal("in-memory settings not saved")
			}
			loaded, err := readSettings(a.dir)
			if err != nil || loaded.ImageGeneration != a.config.ImageGeneration {
				t.Fatalf("settings not persistent: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(dir, "config.toml"))
			if err != nil || !managedCodexImages(imagesTOML(t, got)["mcp_servers"].(map[string]any)[codexImagesServer]) {
				t.Fatalf("MCP not prepared: %v", err)
			}
			before, _ := os.ReadFile(filepath.Join(a.dir, "settings.json"))
			// Old clients which omit optional settings preserve the current setting.
			body, _ = json.Marshal(map[string]any{"catalog": json.RawMessage(testCatalog)})
			if w := adminRequest(a, endpoint, string(body)); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			after, _ := os.ReadFile(filepath.Join(a.dir, "settings.json"))
			if !bytes.Equal(before, after) || !a.config.ImageGeneration.Enabled {
				t.Fatal("legacy preparation disabled images")
			}
			var saved struct {
				ImageGeneration imageGenerationSettings `json:"imageGeneration"`
			}
			if err := json.Unmarshal(adminRequest(a, endpoint, "").Body.Bytes(), &saved); err != nil || saved.ImageGeneration != a.config.ImageGeneration {
				t.Fatalf("load omitted settings: %v", err)
			}
		})
	}
}

func TestCodexImageProfileInvalidSettingsChangeNothing(t *testing.T) {
	for _, invalid := range []imageGenerationSettings{{Enabled: true}, {Enabled: true, Model: "bad\nmodel"}, {Model: "model with spaces"}} {
		a := testApp(t)
		a.codexProfileDir = filepath.Join(t.TempDir(), "profile")
		body, _ := json.Marshal(map[string]any{"catalog": json.RawMessage(testCatalog), "imageGeneration": invalid})
		if w := adminRequest(a, "codex/catalog", string(body)); w.Code < 400 {
			t.Fatal("accepted invalid image setting")
		}
		if a.config.ImageGeneration.Enabled {
			t.Fatal("invalid request enabled generation")
		}
		if _, err := os.Stat(filepath.Join(a.codexProfileDir, "config.toml")); !os.IsNotExist(err) {
			t.Fatal("invalid request wrote profile")
		}
	}
}

func TestCodexImageProfileUnsafeSettingsBackupPreservesProfile(t *testing.T) {
	a := testApp(t)
	a.codexProfileDir = filepath.Join(t.TempDir(), "profile")
	if _, _, err := saveCodexProfile(a.codexProfileDir, []byte(testCatalog), a.config.Port); err != nil {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(filepath.Join(a.codexProfileDir, "config.toml"))
	if err := writeSettings(a.dir, a.config); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(a.dir, "settings.json.bak"), 0700); err != nil {
		t.Fatal(err)
	}
	_, _, err := a.saveCodexImageProfile(a.codexProfileDir, []byte(testCatalog), &imageGenerationSettings{Enabled: true, Model: "vendor/image"})
	if err == nil {
		t.Fatal("accepted unsafe settings backup")
	}
	current, _ := os.ReadFile(filepath.Join(a.codexProfileDir, "config.toml"))
	if !bytes.Equal(old, current) || a.config.ImageGeneration.Enabled {
		t.Fatal("failed settings persistence left a partial image configuration")
	}
}

func TestCodexImageLaunchVerifiesMCPConfig(t *testing.T) {
	a := testApp(t)
	home := t.TempDir()
	a.codexProfileDir = filepath.Join(home, ".codex-kilo-desktop")
	images := imageGenerationSettings{Enabled: true, Model: "vendor/image"}
	if _, _, err := a.saveCodexImageProfile(a.codexProfileDir, []byte(testCatalog), &images); err != nil {
		t.Fatal(err)
	}
	plan := clientLaunchPlan{Client: "codex", Name: "Codex Desktop", Env: map[string]string{}}
	if err := a.launchProfile(&plan, home); err != nil {
		t.Fatal(err)
	}
	if plan.Env["KILO_LOCAL_API_KEY"] != a.config.LocalKey || plan.Env["CODEX_HOME"] != a.codexProfileDir {
		t.Fatal("launch omitted isolated profile or MCP bearer environment")
	}
	path := filepath.Join(a.codexProfileDir, "config.toml")
	data, _ := os.ReadFile(path)
	data, err := mergeCodexImages(data, imageGenerationSettings{}, a.config.Port)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.launchProfile(&plan, home); err == nil {
		t.Fatal("launch accepted a profile missing its enabled image tool")
	}
}

func TestCodexImageProfileRestoresFilesWhenSettingsWriteFails(t *testing.T) {
	dir := t.TempDir()
	oldConfig := []byte("# Preserve on failed preparation\nmodel = 'vendor/old'\n")
	oldCatalog := []byte(testCatalog)
	for name, data := range map[string][]byte{"config.toml": oldConfig, "models.json": oldCatalog} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// A later write fails after the first two files have been replaced. Exercise
	// rollback across the profile and proxy-settings directories.
	extra := profileFile{path: filepath.Join(t.TempDir(), "absent", "settings.json"), new: []byte("{}"), changed: true}
	_, _, err := saveCodexProfileOptions(dir, []byte(testCatalog), 8877, "", &imageGenerationSettings{Enabled: true, Model: "vendor/image"}, []profileFile{extra})
	if err == nil {
		t.Fatal("expected failed settings write")
	}
	for name, expected := range map[string][]byte{"config.toml": oldConfig, "models.json": oldCatalog} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, expected) {
			t.Fatalf("%s was not restored after failure: %v", name, err)
		}
	}
}

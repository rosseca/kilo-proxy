package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestMergeCodexConfigPreservesSettings(t *testing.T) {
	fixtures := []string{
		"",
		"# My settings\nmodel = 'old' # chosen\nmodel_reasoning_effort = 'low'\napproval_policy = 'on-request'\n[features]\nweb_search = true\n[mcp_servers.example]\ncommand = 'node'\nargs = [\n 'server',\n]\n",
		"model_providers = { other = { name = 'Other' }, 'kilo-local' = { env_key = 'wrong', wire_api = 'chat', http_headers = { Authorization = 'old', 'X-Test' = 'keep' } } }\n",
		"model_providers.'kilo-local'.env_key = 'wrong'\n[features]\ncustom = true\n",
		"[model_providers]\n[model_providers.other]\nname = 'Other'\n",
		"[model_providers.kilo-local.http_headers]\nAuthorization = 'old'\n'X-Test' = 'keep'\n",
		"[model_providers.kilo-local]\nexperimental_bearer_token = 'old'\nquery_params = { test = 'value' }\n[model_providers.kilo-local.auth]\ncommand = 'old'\nargs = ['auth']\n[model_providers.kilo-local.env_http_headers]\nauthorization = 'OLD'\n'X-Test' = 'KEEP'\n",
		"profile = 'work'\n[profiles.work]\nmodel = 'old'\nsandbox_mode = 'workspace-write'\n[profiles.other]\nmodel = 'keep'\n",
		"instructions = '''\n[model_providers.kilo-local]\nmodel = 'keep inside string'\n'''\n[[custom]]\nname = 'one'\n[custom.options]\nenabled = true\n[[custom]]\nname = 'two'\n",
		"# Windows\r\nmodel = 'old'\r\n[model_providers.'kilo-local']",
	}
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			merged, err := mergeCodexConfig([]byte(fixture), []byte(testCatalog), 18877)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := toml.Unmarshal(merged, &got); err != nil {
				t.Fatal(err)
			}
			if got["model"] != "anthropic/claude-fable-5.1" || got["model_reasoning_effort"] != "high" {
				t.Fatal(string(merged))
			}
			provider := got["model_providers"].(map[string]any)["kilo-local"].(map[string]any)
			if provider["env_key"] != "KILO_LOCAL_API_KEY" || provider["base_url"] != "http://127.0.0.1:18877/v1" {
				t.Fatal(provider)
			}
			for _, key := range []string{"auth", "experimental_bearer_token", "query_params"} {
				if _, ok := provider[key]; ok {
					t.Fatal(key)
				}
			}
			var before map[string]any
			toml.Unmarshal([]byte(fixture), &before)
			for _, key := range []string{"instructions", "custom", "features", "mcp_servers", "approval_policy"} {
				if value, ok := before[key]; ok {
					old, _ := tomlLiteral(value)
					new, _ := tomlLiteral(got[key])
					if !bytes.Equal(old, new) {
						t.Fatalf("changed %s", key)
					}
				}
			}
			if strings.Contains(fixture, "# My settings") && !strings.Contains(string(merged), "# My settings\nmodel = 'anthropic/claude-fable-5.1' # chosen") {
				t.Fatal("comments lost", string(merged))
			}
			again, err := mergeCodexConfig(merged, []byte(testCatalog), 18877)
			if err != nil || !bytes.Equal(merged, again) {
				t.Fatal("not idempotent", err)
			}
		})
	}
}

func TestSaveCodexProfileCreatesAndUpdates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "codex-kilo")
	c, m, err := saveCodexProfile(dir, []byte(testCatalog), 8877)
	if err != nil || !c || !m {
		t.Fatal(c, m, err)
	}
	config, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	for _, name := range []string{"config.toml", "models.json"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatal(info.Mode())
		}
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(dir)
		if info.Mode().Perm() != 0700 {
			t.Fatal(info.Mode())
		}
	}
	changed := strings.ReplaceAll(testCatalog, "anthropic/claude-fable-5.1", "z-ai/glm-5.3")
	c, m, err = saveCodexProfile(dir, []byte(changed), 9988)
	if err != nil || !c || !m {
		t.Fatal(c, m, err)
	}
	backup, _ := os.ReadFile(filepath.Join(dir, "config.toml.bak"))
	if !bytes.Equal(backup, config) {
		t.Fatal("config backup")
	}
	modelsBackup, _ := os.ReadFile(filepath.Join(dir, "models.json.bak"))
	if string(bytes.TrimSpace(modelsBackup)) != testCatalog {
		t.Fatal("catalog backup")
	}
	c, m, err = saveCodexProfile(dir, []byte(changed), 9988)
	if err != nil || c || m {
		t.Fatal("no-op", c, m, err)
	}
	backup, _ = os.ReadFile(filepath.Join(dir, "config.toml.bak"))
	if !bytes.Equal(backup, config) {
		t.Fatal("no-op replaced backup")
	}
}

func TestCodexProfileInvalidSettingsStayUntouched(t *testing.T) {
	for _, config := range []string{"model = 'first'\nmodel = 'second'", "secret = 'PRIVATE\n", "model_providers = 123"} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0600)
		os.WriteFile(filepath.Join(dir, "models.json"), []byte("original"), 0600)
		_, _, err := saveCodexProfile(dir, []byte(testCatalog), 8877)
		if err == nil || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
		if string(got) != config {
			t.Fatal("config changed")
		}
		got, _ = os.ReadFile(filepath.Join(dir, "models.json"))
		if string(got) != "original" {
			t.Fatal("catalog changed")
		}
		if _, err := os.Stat(filepath.Join(dir, "config.toml.bak")); !os.IsNotExist(err) {
			t.Fatal("backup written")
		}
	}
}

func TestCodexRemovesStaleReasoning(t *testing.T) {
	got, err := mergeCodexConfig([]byte("model_reasoning_effort = 'high'\nprofile='work'\n[profiles.work]\nmodel_reasoning_effort='high'\n"), []byte(`{"models":[{"slug":"vendor/model"}]}`), 8877)
	if err != nil || strings.Contains(string(got), "model_reasoning_effort") {
		t.Fatal(err, string(got))
	}
}

func TestCodexProfileRejectsSymlinks(t *testing.T) {
	for _, name := range []string{"config.toml", "config.toml.bak", "models.json", "profile"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			external := filepath.Join(t.TempDir(), "original")
			os.WriteFile(external, []byte("original"), 0600)
			destination := filepath.Join(dir, name)
			if name == "profile" {
				destination = filepath.Join(dir, "linked")
				dir = destination
			}
			if err := os.Symlink(external, destination); err != nil {
				t.Skip(err)
			}
			if name == "config.toml.bak" {
				os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model='old'"), 0600)
			}
			_, _, err := saveCodexProfile(dir, []byte(testCatalog), 8877)
			if err == nil {
				t.Fatal("unsafe profile accepted")
			}
			got, _ := os.ReadFile(external)
			if string(got) != "original" {
				t.Fatal("symlink followed")
			}
		})
	}
}

func TestCodexSelectedProfileAndHeaders(t *testing.T) {
	config := `profile = "work"
[profiles.work]
model = "old"
sandbox_mode = "workspace-write"
[profiles.other]
model = "keep"
[model_providers.kilo-local]
http_headers = { Authorization = "old", X-Test = "keep" }
env_http_headers = { aUtHoRiZaTiOn = "OLD", X-Other = "KEEP" }
request_max_retries = 8
`
	merged, err := mergeCodexConfig([]byte(config), []byte(testCatalog), 8877)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	toml.Unmarshal(merged, &got)
	for _, check := range []struct {
		path []string
		want any
	}{
		{[]string{"profiles", "work", "model"}, "anthropic/claude-fable-5.1"},
		{[]string{"profiles", "work", "model_reasoning_effort"}, "high"},
		{[]string{"profiles", "work", "sandbox_mode"}, "workspace-write"},
		{[]string{"profiles", "other", "model"}, "keep"},
		{[]string{"model_providers", "kilo-local", "http_headers", "X-Test"}, "keep"},
		{[]string{"model_providers", "kilo-local", "env_http_headers", "X-Other"}, "KEEP"},
		{[]string{"model_providers", "kilo-local", "request_max_retries"}, int64(8)},
	} {
		value, _ := tomlAt(got, check.path)
		if value != check.want {
			t.Fatal(check.path, value)
		}
	}
	for _, key := range []string{"http_headers", "env_http_headers"} {
		value, _ := tomlAt(got, []string{"model_providers", "kilo-local", key})
		for name := range value.(map[string]any) {
			if strings.EqualFold(name, "authorization") {
				t.Fatal(name)
			}
		}
	}
}

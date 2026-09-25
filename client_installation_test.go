package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClientInstallationURLsOnlySupportedCLIs(t *testing.T) {
	want := map[string]string{
		"codex-cli": "https://developers.openai.com/codex/cli/",
		"claude":    "https://code.claude.com/docs/en/setup#install-claude-code",
		"opencode":  "https://opencode.ai/docs/#install",
		"omp":       "https://github.com/can1357/oh-my-pi#install",
	}
	for _, id := range append(append([]string{}, launchClients...), "", "unknown", "https://example.test", "CLAUDE") {
		if got := launchClientInstallURL(id); got != want[id] {
			t.Errorf("installation link for %q = %q, want %q", id, got, want[id])
		}
		encoded, _ := json.Marshal(clientLaunchAvailability{InstallURL: launchClientInstallURL(id)})
		if !strings.Contains(string(encoded), `"installed":false`) || strings.Contains(string(encoded), `"installURL"`) != (want[id] != "") {
			t.Fatal("installation fields have an inconsistent JSON contract", id)
		}
	}
}

type clientInstallationVault struct{ calls int }

func (v *clientInstallationVault) Get(string) (string, error) {
	v.calls++
	return "fixture-secret", nil
}
func (v *clientInstallationVault) Set(string, string) error { v.calls++; return nil }
func (v *clientInstallationVault) Delete(string) error      { v.calls++; return nil }

func clientInstallationTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files[relative] = info.Mode().String()
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[relative] += ":" + string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestClientInstallationDiscoveryRefreshesWithoutMutations(t *testing.T) {
	a := launchTestApp(t)
	a.config.Language = "en"
	vault := &clientInstallationVault{}
	a.vault = vault
	starts := 0
	a.launcher.start = func(clientLaunchPlan) error { starts++; return nil }
	a.zedCredentialStore = func(_ context.Context, _, _, _ string) error {
		t.Error("discovery tried to store credentials")
		return errors.New("unexpected credential write")
	}
	if err := writeSettings(a.dir, a.config); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{a.codexProfileDir, a.codexCLIProfileDir, a.claudeProfileDir, a.ompProfileDir, filepath.Join(a.launcher.home, ".config", "opencode")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "private-profile.json"), []byte(`{"token":"fixture-secret","preserve":true}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	clients := []string{"claude", "opencode", "omp", "codex-cli"}
	paths := map[string]string{}
	for _, client := range clients {
		paths[client] = filepath.Join(a.launcher.home, client+"-fixture")
	}
	resolutions := map[string]int{}
	a.launcher.resolve = func(id, custom string) (string, error) {
		resolutions[id]++
		if custom != "" {
			t.Error("discovery supplied a custom application path")
		}
		if path := paths[id]; path != "" && launchExecutable(path) {
			return path, nil
		}
		return "", errors.New("Synthetic CLI is missing.")
	}
	terminal := true
	a.launcher.terminal = func() (bool, string) {
		if !terminal {
			return false, "Synthetic terminal is unavailable."
		}
		return true, ""
	}
	beforeConfig, beforeKey := a.config, a.apiKey
	check := func(installed, available bool, reason string) {
		t.Helper()
		beforeSettings, beforeHome := clientInstallationTree(t, a.dir), clientInstallationTree(t, a.launcher.home)
		response := adminRequest(a, "clients/launch", "")
		var result struct {
			Clients map[string]clientLaunchAvailability `json:"clients"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("invalid client discovery response: %d", response.Code)
		}
		for _, id := range clients {
			got := result.Clients[id]
			if got.Installed != installed || got.Available != available || got.InstallURL != launchClientInstallURL(id) || got.Reason != reason {
				t.Fatalf("%s discovery: %+v", id, got)
			}
			if (installed && got.Path != paths[id]) || (!installed && got.Path != "") {
				t.Fatal("discovery returned a stale executable path", id)
			}
		}
		if starts != 0 || vault.calls != 0 || a.proxyServer != nil || a.config != beforeConfig || a.apiKey != beforeKey {
			t.Fatal("discovery launched a program or changed connection credentials")
		}
		if !reflect.DeepEqual(beforeSettings, clientInstallationTree(t, a.dir)) || !reflect.DeepEqual(beforeHome, clientInstallationTree(t, a.launcher.home)) {
			t.Fatal("discovery changed a file, profile, or permission")
		}
		for _, secret := range []string{a.config.LocalKey, a.apiKey, "fixture-secret"} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatal("discovery exposed credentials")
			}
		}
	}
	check(false, false, "Synthetic CLI is missing.")
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("This is a static executable fixture, not a runnable CLI.\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	check(true, true, "")
	terminal = false
	check(true, false, "Synthetic terminal is unavailable.")
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	check(false, false, "Synthetic CLI is missing.")
	for _, id := range clients {
		if resolutions[id] != 4 {
			t.Fatal("client installation status was cached instead of refreshed", id)
		}
	}
}

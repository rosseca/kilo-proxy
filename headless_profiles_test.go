//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeadlessGeneratedAgentsStayInsideTheirProfile(t *testing.T) {
	home := t.TempDir()
	normal := map[string]string{}
	for _, name := range []string{".codex-kilo-cli", ".claude-kilo", ".omp-kilo", ".opencode-kilo"} {
		dir := filepath.Join(home, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "preserved")
		if err := os.WriteFile(path, []byte("normal-unchanged"), 0600); err != nil {
			t.Fatal(err)
		}
		normal[path] = "normal-unchanged"
	}
	var apps []*app
	for range 2 {
		a := testApp(t)
		if err := os.Chmod(a.dir, 0700); err != nil {
			t.Fatal(err)
		}
		a.apiKey, a.config.OrgID = "synthetic-key", "synthetic-org"
		if err := a.setHeadlessProfilePaths(); err != nil {
			t.Fatal(err)
		}
		for _, client := range []string{"codex-cli", "claude", "omp", "opencode"} {
			if err := a.prepareTerminalProfile(client, home, testModelLibrary(), claudeCaps("2.1.251")); err != nil {
				t.Fatalf("prepare %s: %v", client, err)
			}
		}
		for _, path := range []string{a.codexCLIProfileDir, a.claudeProfileDir, a.ompProfileDir, a.openCodeProfileDir} {
			if !strings.HasPrefix(path, filepath.Join(a.dir, "profiles")+string(filepath.Separator)) {
				t.Fatal("agent escaped server profile")
			}
			if info, err := os.Stat(path); err != nil || !info.IsDir() {
				t.Fatalf("agent was not prepared: %v", err)
			}
		}
		apps = append(apps, a)
	}
	if apps[0].codexCLIProfileDir == apps[1].codexCLIProfileDir || apps[0].claudeProfileDir == apps[1].claudeProfileDir || apps[0].openCodeProfileDir == apps[1].openCodeProfileDir {
		t.Fatal("server profiles share generated agents")
	}
	for path, want := range normal {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatal("normal agent changed")
		}
		entries, err := os.ReadDir(filepath.Dir(path))
		if err != nil || len(entries) != 1 {
			t.Fatal("server wrote settings into a normal agent home")
		}
	}
}

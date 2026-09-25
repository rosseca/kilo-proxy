package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestTerminalOpenCodeRefreshesSharedModelsAndPreservesMCP(t *testing.T) {
	a := terminalTestApp(t)
	a.config.Port = 8899
	a.config.ImageGeneration = imageGenerationSettings{Enabled: true, Model: "vendor/image"}
	root, path, savedPath, err := a.editorPaths("opencode")
	if err != nil || safeEditorDir(root, filepath.Dir(path)) != nil {
		t.Fatal("cannot prepare temporary OpenCode profile", err)
	}
	original := []byte(`{
 // Keep this comment and other providers.
 "permission":{"bash":"ask"},
 "mcp":{"docs":{"type":"local","command":["my-mcp","with spaces"]}},
 "provider":{"personal":{"npm":"@ai-sdk/openai-compatible","name":"Personal"}}
}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	prepare := func() clientLaunchPlan {
		t.Helper()
		a.mu.Lock()
		defer a.mu.Unlock()
		if err := a.prepareTerminalProfile("opencode", a.launcher.home, a.modelLibrary.snapshot().Library, claudeCapabilities{}); err != nil {
			t.Fatal(err)
		}
		plan := clientLaunchPlan{Client: "opencode", Env: map[string]string{}}
		if err := a.launchProfile(&plan, a.launcher.home); err != nil {
			t.Fatal(err)
		}
		return plan
	}
	first := prepare()
	if !reflect.DeepEqual(first.Args, []string{"--model", "kilo-local/vendor/two"}) || !reflect.DeepEqual(first.Env, map[string]string{"OPENCODE_CONFIG": path}) || !reflect.DeepEqual(first.Unset, []string{"OPENCODE_CONFIG_CONTENT"}) {
		t.Fatal("OpenCode terminal plan differs from the scoped launcher", first)
	}
	if !bytes.Equal(original, terminalInstallerRead(t, path+".bak")) {
		t.Fatal("OpenCode settings did not receive an exact backup")
	}
	config := terminalInstallerRead(t, path)
	for _, expected := range []string{"Keep this comment", "my-mcp", "Personal", "permission", "My First", "My Second", "kilo_images", "http://127.0.0.1:8899/mcp/images", a.config.LocalKey} {
		if !bytes.Contains(config, []byte(expected)) {
			t.Fatalf("OpenCode profile lost %q", expected)
		}
	}
	if bytes.Contains(config, []byte(a.apiKey)) {
		t.Fatal("upstream credential written to OpenCode profile")
	}
	var selection editorSelection
	if json.Unmarshal(terminalInstallerRead(t, savedPath), &selection) != nil || len(selection.Models) != 2 || selection.Models[0].ID != "vendor/one" || selection.Models[1].ID != "vendor/two" || selection.Initial != "vendor/two" {
		t.Fatal("shared model IDs, order or default lost")
	}
	state := a.modelLibrary.snapshot()
	state.Library.DefaultModel = "vendor/one"
	state.Library.Models[0].DisplayName = "Updated from Models"
	state.Library.Models[0].ContextWindow = 0
	if _, err := a.modelLibrary.save(state.Library, state.Revision, false); err != nil {
		t.Fatal(err)
	}
	a.config.ImageGeneration.Enabled = false
	a.config.LocalKey = "changed-synthetic-local-key"
	second := prepare()
	config = terminalInstallerRead(t, path)
	if !reflect.DeepEqual(second.Args, []string{"--model", "kilo-local/vendor/one"}) || !bytes.Contains(config, []byte("Updated from Models")) || !bytes.Contains(config, []byte(a.config.LocalKey)) || bytes.Contains(config, []byte("kilo_images")) || !bytes.Contains(config, []byte("my-mcp")) {
		t.Fatal("OpenCode reused stale models, credentials or image MCP settings")
	}
	if json.Unmarshal(terminalInstallerRead(t, savedPath), &selection) != nil || selection.Models[0].Context != 272000 {
		t.Fatal("OpenCode did not apply the shared recommended context")
	}
	for _, name := range []string{path, savedPath} {
		info, err := os.Stat(name)
		if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatal("OpenCode profile is not private", err)
		}
	}
}

func TestTerminalOpenCodeRejectsUntrustedExecutionParameters(t *testing.T) {
	a := terminalTestApp(t)
	cleanup, err := a.publishTerminalRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	info, err := readTerminalRuntime(a.dir)
	_, config, _, _ := a.editorPaths("opencode")
	if err != nil || info.OpenCodeConfig != config {
		t.Fatal("missing trusted OpenCode profile location", err)
	}
	valid := func() clientLaunchPlan {
		return clientLaunchPlan{Client: "opencode", Name: "OpenCode", Kind: "terminal", Env: map[string]string{"OPENCODE_CONFIG": config}, Unset: []string{"OPENCODE_CONFIG_CONTENT"}}
	}
	if err := validateTerminalProfile(valid(), info); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*clientLaunchPlan){
		"executable":        func(p *clientLaunchPlan) { p.Executable = "/bin/sh" },
		"extra argument":    func(p *clientLaunchPlan) { p.Args = append(p.Args, "--port", "9999") },
		"injected model":    func(p *clientLaunchPlan) { p.Args = []string{"--model", "kilo-local/vendor/two"} },
		"injected command":  func(p *clientLaunchPlan) { p.Args = []string{"models", "kilo-local"} },
		"wrong profile":     func(p *clientLaunchPlan) { p.Env["OPENCODE_CONFIG"] = "/other" },
		"inline override":   func(p *clientLaunchPlan) { p.Env["OPENCODE_CONFIG_CONTENT"] = "{}" },
		"extra environment": func(p *clientLaunchPlan) { p.Env["LD_PRELOAD"] = "/tmp/injected.so" },
		"missing reset":     func(p *clientLaunchPlan) { p.Unset = nil },
		"extra reset":       func(p *clientLaunchPlan) { p.Unset = append(p.Unset, "PATH") },
	} {
		t.Run(name, func(t *testing.T) {
			plan := valid()
			mutate(&plan)
			if validateTerminalProfile(plan, info) == nil {
				t.Fatal("untrusted response accepted")
			}
		})
	}
	older := info
	older.OpenCodeConfig = ""
	if validateTerminalRuntime(older) != nil || validateTerminalProfile(valid(), older) == nil {
		t.Fatal("older descriptors must remain valid but cannot authorize OpenCode")
	}
	for _, path := range []string{"relative", "/tmp/../other", "/tmp/config\n"} {
		invalid := info
		invalid.OpenCodeConfig = path
		if validateTerminalRuntime(invalid) == nil {
			t.Fatal("invalid trusted path accepted", path)
		}
	}
}

func TestTerminalOpenCodeProtectsExistingFiles(t *testing.T) {
	for _, conflict := range []string{"settings", "selection", "directory", "malformed", "mcp conflict", "backup"} {
		t.Run(conflict, func(t *testing.T) {
			a := terminalTestApp(t)
			a.config.Port = 8899
			a.config.ImageGeneration = imageGenerationSettings{Enabled: true, Model: "vendor/image"}
			root, path, selection, _ := a.editorPaths("opencode")
			outside := t.TempDir()
			target := filepath.Join(outside, "untouched")
			original := []byte("private external file\n")
			if err := os.WriteFile(target, original, 0600); err != nil {
				t.Fatal(err)
			}
			if conflict == "directory" {
				if err := os.Symlink(outside, filepath.Dir(path)); err != nil {
					t.Skip(err)
				}
			} else {
				if err := safeEditorDir(root, filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
				switch conflict {
				case "settings", "selection", "backup":
					if conflict == "backup" {
						if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					link := map[string]string{"settings": path, "selection": selection, "backup": path + ".bak"}[conflict]
					if err := os.Symlink(target, link); err != nil {
						t.Skip(err)
					}
				case "malformed":
					if err := os.WriteFile(path, []byte(`{"provider":`), 0600); err != nil {
						t.Fatal(err)
					}
				case "mcp conflict":
					if err := os.WriteFile(path, []byte(`{"mcp":{"kilo_images":{"type":"local","command":["custom-mcp"]}}}`), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			a.mu.Lock()
			err := a.prepareTerminalProfile("opencode", a.launcher.home, a.modelLibrary.snapshot().Library, claudeCapabilities{})
			a.mu.Unlock()
			if err == nil || !bytes.Equal(original, terminalInstallerRead(t, target)) {
				t.Fatal("unsafe profile preparation succeeded or modified an external file", err)
			}
			if conflict != "selection" {
				if _, err := os.Lstat(selection); !os.IsNotExist(err) {
					t.Fatal("failed preflight changed the model selection")
				}
			}
		})
	}
}

func TestTerminalCommandsUpgradeAddsOpenCodeWithoutRewritingExistingCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix command installation")
	}
	home, configDir, binary := terminalInstallerFixture(t)
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for name, client := range map[string]string{"kilo-codex": "codex-cli", "kilo-claude": "claude", "kilo-omp": "omp"} {
		if err := os.WriteFile(filepath.Join(bin, name), terminalCommandScript(binary, configDir, client), 0700); err != nil {
			t.Fatal(err)
		}
	}
	startup := filepath.Join(home, ".zshrc")
	original := append([]byte("# Existing user preferences\n"), terminalPathBlock("zsh")...)
	if err := os.WriteFile(startup, original, 0600); err != nil {
		t.Fatal(err)
	}
	status, err := terminalCommandsStatus(home, configDir, binary, "zsh", "darwin")
	if err != nil || status.Installed || !status.PathConfigured {
		t.Fatal("old three-command install did not report a needed update", err)
	}
	result, err := installTerminalCommands(home, configDir, binary, "zsh", "darwin")
	if err != nil || !result.Installed || len(result.Commands) != 4 || len(result.Backups) != 0 || !bytes.Equal(original, terminalInstallerRead(t, startup)) {
		t.Fatal("upgrade changed existing commands or shell preferences", err)
	}
	if !bytes.Contains(terminalInstallerRead(t, result.Commands["kilo-opencode"]), []byte("--terminal-agent opencode")) {
		t.Fatal("kilo-opencode wrapper was not installed")
	}
}

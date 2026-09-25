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

func TestTerminalOMPRefreshesSharedModelsAndImageMCP(t *testing.T) {
	a := terminalTestApp(t)
	a.config.ImageGeneration = imageGenerationSettings{Enabled: true, Model: "vendor/image"}
	cache, _ := json.Marshal(nativeCachedCatalog{SchemaVersion: 1, OrgID: a.config.OrgID, Models: []modelInfo{{ID: "vendor/two", InputModalities: []string{"text", "image"}}}})
	if err := atomicCatalogFile(filepath.Join(a.dir, "model-catalog.json"), cache); err != nil {
		t.Fatal(err)
	}
	prepare := func() clientLaunchPlan {
		t.Helper()
		body, _ := json.Marshal(terminalPrepareRequest{Client: "omp", Directory: a.launcher.home})
		result := adminRequest(a, "terminal/prepare", string(body))
		var plan clientLaunchPlan
		if result.Code != 200 || json.Unmarshal(result.Body.Bytes(), &plan) != nil {
			t.Fatal(result.Code, result.Body.String())
		}
		return plan
	}
	first := prepare()
	if a.proxyListener == nil || first.Env["PI_CODING_AGENT_DIR"] != a.ompProfileDir {
		t.Fatal("command did not start the proxy with the isolated OMP profile")
	}
	models := terminalInstallerRead(t, filepath.Join(a.ompProfileDir, "models.yml"))
	for _, expected := range []string{"My Second", "defaultLevel: high", "image", a.config.LocalKey} {
		if !bytes.Contains(models, []byte(expected)) {
			t.Fatalf("OMP profile lost %s", expected)
		}
	}
	mcpPath := filepath.Join(a.ompProfileDir, "mcp.json")
	if !bytes.Contains(terminalInstallerRead(t, mcpPath), []byte("kilo-images")) {
		t.Fatal("enabled image MCP missing")
	}
	state := a.modelLibrary.snapshot()
	state.Library.DefaultModel = "vendor/one"
	state.Library.Models[0].DisplayName = "Updated from Models"
	state.Library.Models[0].ContextWindow = 0
	if _, err := a.modelLibrary.save(state.Library, state.Revision, false); err != nil {
		t.Fatal(err)
	}
	a.config.ImageGeneration.Enabled = false
	second := prepare()
	if !reflect.DeepEqual(second.Args, []string{"--model", "kilo-local/vendor/one"}) {
		t.Fatal("command did not pick up the new default model")
	}
	models = terminalInstallerRead(t, filepath.Join(a.ompProfileDir, "models.yml"))
	if !bytes.Contains(models, []byte("Updated from Models")) || !bytes.Contains(models, []byte("contextWindow: 272000")) || bytes.Contains(terminalInstallerRead(t, mcpPath), []byte("kilo-images")) {
		t.Fatal("command reused stale models, missing limit fallback, or stale image configuration")
	}
}

func TestTerminalOMPRejectsUntrustedExecutionParameters(t *testing.T) {
	a := terminalTestApp(t)
	cleanup, err := a.publishTerminalRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	info, err := readTerminalRuntime(a.dir)
	if err != nil || info.OMPProfile != a.ompProfileDir {
		t.Fatal("missing trusted OMP profile location", err)
	}
	valid := func() clientLaunchPlan {
		return clientLaunchPlan{Client: "omp", Name: "Oh My Pi", Kind: "terminal", Args: []string{"--model", "kilo-local/vendor/two"}, Env: map[string]string{"PI_CODING_AGENT_DIR": info.OMPProfile, "OMP_PROFILE": "", "PI_PROFILE": "", "PI_OPENAI_STATEFUL": "0"}}
	}
	if err := validateTerminalProfile(valid(), info); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*clientLaunchPlan){
		"executable":         func(p *clientLaunchPlan) { p.Executable = "/bin/sh" },
		"extra argument":     func(p *clientLaunchPlan) { p.Args = append(p.Args, "--extension", "/tmp/injected.js") },
		"different provider": func(p *clientLaunchPlan) { p.Args[1] = "another/vendor/model" },
		"wrong profile":      func(p *clientLaunchPlan) { p.Env["PI_CODING_AGENT_DIR"] = "/other" },
		"named profile":      func(p *clientLaunchPlan) { p.Env["OMP_PROFILE"] = "personal" },
		"missing override":   func(p *clientLaunchPlan) { delete(p.Env, "PI_PROFILE") },
		"stateful responses": func(p *clientLaunchPlan) { p.Env["PI_OPENAI_STATEFUL"] = "1" },
		"extra environment":  func(p *clientLaunchPlan) { p.Env["LD_PRELOAD"] = "/tmp/injected.so" },
		"unset environment":  func(p *clientLaunchPlan) { p.Unset = []string{"PATH"} },
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
	older.OMPProfile = ""
	if validateTerminalRuntime(older) != nil || validateTerminalProfile(valid(), older) == nil {
		t.Fatal("older descriptors must stay usable for Codex/Claude but cannot authorize OMP")
	}
}

func TestTerminalCommandsUpgradeAddsOMPWithoutRewritingExistingCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix command installation")
	}
	home, configDir, binary := terminalInstallerFixture(t)
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for name, client := range map[string]string{"kilo-codex": "codex-cli", "kilo-claude": "claude"} {
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
		t.Fatal("old two-command install did not report a needed update", err)
	}
	result, err := installTerminalCommands(home, configDir, binary, "zsh", "darwin")
	if err != nil || !result.Installed || len(result.Commands) != 4 || len(result.Backups) != 0 || !bytes.Equal(original, terminalInstallerRead(t, startup)) {
		t.Fatal("upgrade changed existing commands or shell preferences", err)
	}
	if !bytes.Contains(terminalInstallerRead(t, result.Commands["kilo-omp"]), []byte("--terminal-agent omp")) {
		t.Fatal("kilo-omp wrapper was not installed")
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func browserHelperResults(t *testing.T, input any) map[string][]json.RawMessage {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for parity with the optional browser helpers")
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	script := `import {codexCatalog} from './ui/codex-catalog.mjs';
import {launchCommand,cursorGuide} from './ui/client-config.mjs';
import {claudeLaunch} from './ui/claude-helper.mjs';
import {editorLaunch} from './ui/editor-helper.mjs';
import {xcodeSelectionPayload,xcodeChatGuide} from './ui/xcode-helper.mjs';
let data='';for await(const part of process.stdin)data+=part;const input=JSON.parse(data);
const out={catalogs:input.catalogs.map(c=>c.xcode?xcodeSelectionPayload('codex',c.models,c.initial).catalog:codexCatalog(c.models,c.initial)),commands:input.commands.map(c=>c.kind==='claude'?claudeLaunch(c.shell,c.language):c.kind==='opencode'?editorLaunch(c.appPath,c.model,c.shell):launchCommand(c)),guides:input.guides.map(c=>c.kind==='xcode'?xcodeChatGuide(c.baseURL,c.key,c.language):cursorGuide(c.models,c.language))};process.stdout.write(JSON.stringify(out));`
	cmd := exec.Command(node, "--input-type=module", "-e", script)
	cmd.Stdin = bytes.NewReader(body)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser helper fixture: %v\n%s", err, output)
	}
	var result map[string][]json.RawMessage
	if err = json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNativeHelpersMatchBrowserCatalogsAndCommands(t *testing.T) {
	type catalogCase struct {
		name    string
		models  []nativeModelChoice
		initial string
		xcode   bool
	}
	choice := func(id string) nativeModelChoice { return nativeModelChoice{Model: modelInfo{ID: id}} }
	all := []nativeModelChoice{}
	for id := range nativeKnownReasoning {
		all = append(all, choice(id))
	}
	cases := []catalogCase{
		{"all exact presets", all, "openai/gpt-5.6-sol", false},
		{"unknown, duplicate, image and context", []nativeModelChoice{{Model: modelInfo{ID: "v/one", Name: "One", ContextWindow: 123000, InputModalities: []string{"text", "image"}}}, choice("v/two"), choice("v/one"), choice("bad id")}, "v/two", false},
		{"tilde ID", []nativeModelChoice{choice("~z-ai/glm-5.3-flash")}, "", false},
		{"manual override", []nativeModelChoice{{Model: modelInfo{ID: "openai/gpt-5.6-sol", ReasoningEfforts: []string{"low", "max"}}, ReasoningCustom: true, ReasoningLevels: []string{"high", "low", "oops", "low"}, DefaultReasoning: "high"}}, "", false},
		{"manual disable", []nativeModelChoice{{Model: modelInfo{ID: "openai/gpt-5.6-sol", ReasoningEfforts: []string{"low", "max"}}, ReasoningCustom: true}}, "", false},
		{"metadata wins", []nativeModelChoice{{Model: modelInfo{ID: "openai/gpt-5.6-sol", ReasoningEfforts: []string{"none", "low", "max"}}}, {Model: modelInfo{ID: "vendor/new", ReasoningEfforts: []string{"high", "low", "invalid", "low"}}}}, "", false},
		{"labels", []nativeModelChoice{{Model: modelInfo{ID: "v/a", Name: "Original"}, DisplayName: " A\nB "}, {Model: modelInfo{ID: "v/b", Name: "Fallback"}, DisplayName: "   "}, {Model: modelInfo{ID: "v/c"}, DisplayName: strings.Repeat("é", 90)}, {Model: modelInfo{ID: "v/d"}, DisplayName: strings.Repeat("x", 79) + "🔑"}}, "", false},
		{"Xcode limits", []nativeModelChoice{choice("z-ai/glm-5.3"), choice("openai/gpt-6-astra"), choice("v/unknown")}, "z-ai/glm-5.3", true},
	}
	browserCatalogs := []map[string]any{}
	for _, c := range cases {
		models := []map[string]any{}
		for _, m := range c.models {
			data, _ := json.Marshal(m.Model)
			var value map[string]any
			_ = json.Unmarshal(data, &value)
			value["displayName"] = m.DisplayName
			if m.ReasoningCustom {
				levels := append([]string{}, m.ReasoningLevels...)
				value["reasoningLevels"] = levels
				value["defaultReasoning"] = m.DefaultReasoning
			}
			models = append(models, value)
		}
		browserCatalogs = append(browserCatalogs, map[string]any{"models": models, "initial": c.initial, "xcode": c.xcode})
	}
	commands := []map[string]any{}
	nativeCommands := []string{}
	for _, language := range []string{"en", "es"} {
		for _, catalog := range []bool{false, true} {
			for _, platform := range []string{"macos", "linux", "windows"} {
				path := "/Applications/Team's $Codex.app"
				if platform == "windows" {
					path = `C:\Team's $Codex\Codex.exe`
				}
				key := "a'b$()`\\key"
				command, err := codexLaunchCommand(true, "unix", platform, path, key, language, catalog)
				if err != nil {
					t.Fatal(err)
				}
				commands = append(commands, map[string]any{"client": "codex", "platform": platform, "appPath": path, "key": key, "language": language, "catalog": catalog})
				nativeCommands = append(nativeCommands, command)
			}
			for _, shell := range []string{"unix", "powershell"} {
				command, err := codexLaunchCommand(false, shell, "", "", "a'b$()", language, catalog)
				if err != nil {
					t.Fatal(err)
				}
				commands = append(commands, map[string]any{"client": "codex-cli", "shell": shell, "key": "a'b$()", "language": language, "catalog": catalog})
				nativeCommands = append(nativeCommands, command)
			}
		}
	}
	for _, language := range []string{"en", "es"} {
		for _, shell := range []string{"unix", "powershell"} {
			command, err := claudeLaunchCommand(shell, language)
			if err != nil {
				t.Fatal(err)
			}
			commands = append(commands, map[string]any{"kind": "claude", "shell": shell, "language": language})
			nativeCommands = append(nativeCommands, command)
		}
	}
	for _, shell := range []string{"unix", "powershell"} {
		path := "/tmp/Team's $settings.json"
		command, err := openCodeLaunchCommand(path, "vendor/model", shell)
		if err != nil {
			t.Fatal(err)
		}
		commands = append(commands, map[string]any{"kind": "opencode", "appPath": path, "model": "vendor/model", "shell": shell})
		nativeCommands = append(nativeCommands, command)
	}
	guides := []map[string]any{}
	nativeGuides := []string{}
	for _, language := range []string{"en", "es"} {
		guides = append(guides, map[string]any{"kind": "xcode", "baseURL": "http://127.0.0.1:8877/v1/", "key": "synthetic-key", "language": language})
		nativeGuides = append(nativeGuides, xcodeChatGuide("http://127.0.0.1:8877/v1/", "synthetic-key", language))
		for _, models := range [][]string{{"vendor/one", "vendor/two", "vendor/one", "bad id"}, {}} {
			guides = append(guides, map[string]any{"kind": "cursor", "models": models, "language": language})
			nativeGuides = append(nativeGuides, cursorSetupGuide(nil, models, language, false))
		}
	}
	results := browserHelperResults(t, map[string]any{"catalogs": browserCatalogs, "commands": commands, "guides": guides})
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, err := buildCodexCatalog(c.models, c.initial, c.xcode)
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if json.Unmarshal(data, &got) != nil || json.Unmarshal(results["catalogs"][i], &want) != nil {
				t.Fatal("invalid catalog fixture")
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("native catalog differs from browser:\n%s\nwant:\n%s", data, results["catalogs"][i])
			}
		})
	}
	for name, expected := range map[string][]string{"commands": nativeCommands, "guides": nativeGuides} {
		for i, got := range expected {
			var want string
			_ = json.Unmarshal(results[name][i], &want)
			if got != want {
				t.Errorf("%s case%d differs:\n%s\nwant:\n%s", name, i, got, want)
			}
		}
	}
}

func TestNativeHelperValidationAndReasoningOwnership(t *testing.T) {
	for _, models := range [][]nativeModelChoice{nil, {{Model: modelInfo{ID: "bad id"}}}} {
		if _, err := buildCodexCatalog(models, "", false); err == nil {
			t.Fatal("invalid empty catalog accepted")
		}
	}
	many := []nativeModelChoice{}
	for i := range 51 {
		many = append(many, nativeModelChoice{Model: modelInfo{ID: fmt.Sprintf("vendor/%d", i)}})
	}
	if _, err := buildCodexCatalog(many, "", false); err == nil {
		t.Fatal("more than50 models accepted")
	}
	choice := nativeModelChoice{Model: modelInfo{ID: "openai/gpt-5.6-sol"}}
	levels, _ := nativeReasoningFor(choice)
	levels[0] = "corrupted"
	if next, _ := nativeReasoningFor(choice); next[0] == "corrupted" {
		t.Fatal("caller changed shared reasoning preset")
	}
	for _, test := range []struct{ shell, platform, path, key string }{
		{"bash", "macos", "/Applications/Codex.app", "key"}, {"unix", "mobile", "/App", "key"}, {"unix", "macos", "relative.app", "key"}, {"unix", "windows", "relative.exe", "key"}, {"unix", "macos", "/App\n--args", "key"}, {"unix", "macos", "/App", ""}, {"unix", "macos", "/App", "key\x00arg"},
	} {
		if _, err := codexLaunchCommand(true, test.shell, test.platform, test.path, test.key, "en", false); err == nil {
			t.Fatalf("unsafe launcher accepted: %+v", test)
		}
	}
	if _, err := claudeLaunchCommand("cmd", "en"); err == nil {
		t.Fatal("unknown Claude shell accepted")
	}
	for _, args := range [][3]string{{"relative", "vendor/model", "unix"}, {"/tmp/config", "vendor/model\nboom", "unix"}, {"/tmp/config", "vendor/model", "cmd"}} {
		if _, err := openCodeLaunchCommand(args[0], args[1], args[2]); err == nil {
			t.Fatal("unsafe OpenCode launcher accepted")
		}
	}
	for _, path := range []string{`C:\Team\Codex.exe`, `\\server\share\Codex.exe`} {
		if _, err := codexLaunchCommand(true, "powershell", "windows", path, "test-key", "en", true); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeCursorGuideRequiresRunningTunnelAndExplicitKeyReveal(t *testing.T) {
	session := &cursorSession{Status: "running", URL: "https://synthetic.ngrok.example/v1", Key: "private-cursor-key", Models: []string{"vendor/one"}}
	masked := cursorSetupGuide(session, nil, "en", false)
	if strings.Contains(masked, session.Key) || !strings.Contains(masked, session.URL) || !strings.Contains(masked, "vendor/one") {
		t.Fatal("incorrect running tunnel guide")
	}
	if revealed := cursorSetupGuide(session, nil, "es", true); !strings.Contains(revealed, session.Key) || !strings.Contains(revealed, "Activa") {
		t.Fatal("explicit reveal/language not applied")
	}
	session.Status = "stopped"
	if stopped := cursorSetupGuide(session, nil, "en", true); strings.Contains(stopped, session.Key) || strings.Contains(stopped, session.URL) {
		t.Fatal("stopped tunnel credentials offered for reuse")
	}
}

func TestNativeUnixCommandsQuoteArgumentsAndScopeEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix execution is covered on macOS and Linux")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{".codex-kilo-desktop", ".codex-kilo-cli", ".claude-kilo"} {
		dir := filepath.Join(root, profile)
		_ = os.MkdirAll(dir, 0700)
		for _, file := range []string{"config.toml", "models.json", "settings.json"} {
			if err := os.WriteFile(filepath.Join(dir, file), []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	write := func(name, script string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write("open", `printf '%s\n' "$@"`)
	write("codex", `printf '%s\n%s\n' "$CODEX_HOME" "$KILO_LOCAL_API_KEY"`)
	write("claude", `printf '%s\n%s\n%s\n%s\n' "$CLAUDE_CONFIG_DIR" "${ANTHROPIC_AUTH_TOKEN-unset}" "$1" "$2"`)
	write("opencode", `printf '%s\n%s\n%s\n%s\n' "$OPENCODE_CONFIG" "${OPENCODE_CONFIG_CONTENT-unset}" "$1" "$2"`)
	marker := filepath.Join(root, "injected")
	key := "a'b$(touch " + marker + ")`touch " + marker + "`\\key"
	appPath := "/Applications/Team's $(touch ignored).app"
	run := func(command string) (string, error) {
		t.Helper()
		command = strings.ReplaceAll(command, "$HOME", "$KILO_HELPER_TEST_PROFILE")
		cmd := exec.Command("/bin/sh", "-c", command)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "KILO_HELPER_TEST_PROFILE="+root, "CODEX_HOME=parent-home", "KILO_LOCAL_API_KEY=parent-key", "ANTHROPIC_AUTH_TOKEN=parent-token", "OPENCODE_CONFIG_CONTENT=conflicting-inline")
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	command, err := codexLaunchCommand(true, "unix", "macos", appPath, key, "en", true)
	if err != nil {
		t.Fatal(err)
	}
	output, err := run(command)
	if err != nil {
		t.Fatalf("macOS launcher: %v %s", err, output)
	}
	for _, expected := range []string{appPath, "KILO_LOCAL_API_KEY=" + key, "CODEX_HOME=" + filepath.Join(root, ".codex-kilo-desktop")} {
		if !strings.Contains(output, expected+"\n") {
			t.Fatalf("quoted argument lost: %q in %q", expected, output)
		}
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("shell expanded a credential")
	}
	command, _ = codexLaunchCommand(false, "unix", "", "", key, "en", true)
	output, err = run(command + "\nprintf '%s\\n%s\\n' \"$CODEX_HOME\" \"$KILO_LOCAL_API_KEY\"")
	if err != nil || output != filepath.Join(root, ".codex-kilo-cli")+"\n"+key+"\nparent-home\nparent-key\n" {
		t.Fatalf("CLI environment escaped scope: %v %q", err, output)
	}
	if err = os.Remove(filepath.Join(root, ".codex-kilo-cli/models.json")); err != nil {
		t.Fatal(err)
	}
	if output, err = run(command); err == nil || !strings.Contains(output, "models.json") {
		t.Fatal("Codex started without its catalog")
	}
	command, _ = claudeLaunchCommand("unix", "en")
	output, err = run(command)
	if err != nil || output != filepath.Join(root, ".claude-kilo")+"\nunset\n--settings\n"+filepath.Join(root, ".claude-kilo/settings.json")+"\n" {
		t.Fatalf("Claude environment reset failed: %v %q", err, output)
	}
	configPath := filepath.Join(root, "Team's config.json")
	_ = os.WriteFile(configPath, []byte("{}"), 0600)
	command, _ = openCodeLaunchCommand(configPath, "vendor/model", "unix")
	output, err = run(command)
	if err != nil || output != configPath+"\nunset\n--model\nkilo-local/vendor/model\n" {
		t.Fatalf("OpenCode arguments changed: %v %q", err, output)
	}
}

func TestNativePowerShellCommandsQuoteAndRestoreEnvironment(t *testing.T) {
	powershell, err := exec.LookPath("pwsh")
	if err != nil && runtime.GOOS == "windows" {
		powershell, err = exec.LookPath("powershell")
	}
	if err != nil {
		t.Skip("PowerShell execution is covered on Windows")
	}
	root := t.TempDir()
	for _, profile := range []string{".codex-kilo-desktop", ".codex-kilo-cli", ".claude-kilo"} {
		dir := filepath.Join(root, profile)
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"config.toml", "models.json", "settings.json"} {
			if err = os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	configPath := filepath.Join(root, "Team's config.json")
	if err = os.WriteFile(configPath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	prefix := `$ErrorActionPreference='Stop'
function codex { @{home=$env:CODEX_HOME;key=$env:KILO_LOCAL_API_KEY} | ConvertTo-Json -Compress }
function Start-Process { param([string]$FilePath,[object[]]$ArgumentList) @{path=$FilePath;args=$ArgumentList;home=$env:CODEX_HOME;key=$env:KILO_LOCAL_API_KEY;ui=$env:CODEX_ELECTRON_USER_DATA_PATH} | ConvertTo-Json -Compress }
function claude { @{dir=$env:CLAUDE_CONFIG_DIR;token=$env:ANTHROPIC_AUTH_TOKEN;args=$args} | ConvertTo-Json -Compress }
function opencode { @{config=$env:OPENCODE_CONFIG;inline=$env:OPENCODE_CONFIG_CONTENT;args=$args} | ConvertTo-Json -Compress }
`
	suffix := `@{home=$env:CODEX_HOME;key=$env:KILO_LOCAL_API_KEY;ui=$env:CODEX_ELECTRON_USER_DATA_PATH;token=$env:ANTHROPIC_AUTH_TOKEN;config=$env:OPENCODE_CONFIG;inline=$env:OPENCODE_CONFIG_CONTENT} | ConvertTo-Json -Compress`
	run := func(command string) map[string]any {
		t.Helper()
		command = strings.ReplaceAll(command, "$env:USERPROFILE", "$env:KILO_HELPER_TEST_PROFILE")
		command = strings.ReplaceAll(command, "$env:LOCALAPPDATA", "$env:KILO_HELPER_TEST_PROFILE")
		cmd := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", prefix+command+"\n"+suffix)
		cmd.Env = append(os.Environ(), "KILO_HELPER_TEST_PROFILE="+root, "CODEX_HOME=parent-home", "KILO_LOCAL_API_KEY=parent-key", "CODEX_ELECTRON_USER_DATA_PATH=parent-ui", "ANTHROPIC_AUTH_TOKEN=parent-token", "OPENCODE_CONFIG=parent-config", "OPENCODE_CONFIG_CONTENT=parent-inline")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("PowerShell failed: %v\n%s", err, out)
		}
		decoder := json.NewDecoder(bytes.NewReader(out))
		var child, parent map[string]any
		if decoder.Decode(&child) != nil || decoder.Decode(&parent) != nil {
			t.Fatalf("PowerShell returned invalid fixture output: %s", out)
		}
		for name, want := range map[string]string{"home": "parent-home", "key": "parent-key", "ui": "parent-ui", "token": "parent-token", "config": "parent-config", "inline": "parent-inline"} {
			if parent[name] != want {
				t.Fatalf("PowerShell did not restore%s: %+v", name, parent)
			}
		}
		return child
	}
	key := "key'$(throw 'injection')`evil"
	command, err := codexLaunchCommand(true, "powershell", "windows", `C:\Team's $Codex\Codex.exe`, key, "en", true)
	if err != nil {
		t.Fatal(err)
	}
	child := run(command)
	if child["key"] != key || child["path"] != `C:\Team's $Codex\Codex.exe` || child["home"] != filepath.Join(root, ".codex-kilo-desktop") {
		t.Fatalf("desktop quoting changed arguments: %+v", child)
	}
	command, err = codexLaunchCommand(false, "powershell", "", "", key, "en", true)
	if err != nil {
		t.Fatal(err)
	}
	if child = run(command); child["key"] != key || child["home"] != filepath.Join(root, ".codex-kilo-cli") {
		t.Fatalf("CLI environment incorrect: %+v", child)
	}
	command, _ = claudeLaunchCommand("powershell", "en")
	if child = run(command); child["dir"] != filepath.Join(root, ".claude-kilo") || child["token"] != nil {
		t.Fatalf("Claude did not clear conflicting authentication: %+v", child)
	}
	command, err = openCodeLaunchCommand(configPath, "vendor/model", "powershell")
	if err != nil {
		t.Fatal(err)
	}
	if child = run(command); child["config"] != configPath || child["inline"] != nil {
		t.Fatalf("OpenCode did not scope configuration: %+v", child)
	}
}

func TestNativeReasoningDefaultSelectorKeepsPublishedLevels(t *testing.T) {
	choice := nativeModelChoice{Model: modelInfo{ID: "openai/gpt-5.6-sol"}, DefaultReasoning: "high"}
	levels, initial := nativeReasoningFor(choice)
	if initial != "high" || !reflect.DeepEqual(levels, nativeKnownReasoning[choice.Model.ID].Levels) {
		t.Fatalf("default selector changed levels: %v %s", levels, initial)
	}
	choice.Model.ReasoningEfforts = []string{"low", "max"}
	if _, initial = nativeReasoningFor(choice); initial != "low" {
		t.Fatal("default selector invented an unsupported effort")
	}
	choice.DefaultReasoning = "max"
	if _, initial = nativeReasoningFor(choice); initial != "max" {
		t.Fatal("default selector ignored gateway-supported choice")
	}
	choice.ReasoningCustom = true
	choice.ReasoningLevels = []string{}
	if levels, initial = nativeReasoningFor(choice); len(levels) != 0 || initial != "" {
		t.Fatal("default selector overrode explicit reasoning disable")
	}
}

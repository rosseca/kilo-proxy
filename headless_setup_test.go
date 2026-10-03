package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type headlessSetupTestClient struct {
	callFn func(context.Context, string, string, any, any) error
}

func (c headlessSetupTestClient) call(ctx context.Context, method, path string, input, output any) error {
	return c.callFn(ctx, method, path, input, output)
}

func headlessSetupTestJSON(t *testing.T, value, output any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if output != nil {
		if err := json.Unmarshal(data, output); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHeadlessSetupConfigurePreservesExistingKeyAndRedactsState(t *testing.T) {
	for _, input := range []string{"", "synthetic-private-token\n"} {
		t.Run(input, func(t *testing.T) {
			var posted map[string]any
			client := headlessSetupTestClient{func(_ context.Context, method, path string, value, output any) error {
				switch path {
				case "/api/state":
					headlessSetupTestJSON(t, map[string]any{"port": 9123, "orgId": "org-saved", "localKey": "must-not-print", "zedBaseURL": "https://secret", "hasKey": true}, output)
				case "/api/config":
					posted = value.(map[string]any)
				default:
					t.Fatalf("Unexpected %s %s", method, path)
				}
				return nil
			}}
			args := []string{}
			if input != "" {
				args = []string{"--key-stdin"}
			}
			var stdout, stderr bytes.Buffer
			if code := runHeadlessSetupCommand(context.Background(), client, "configure", args, strings.NewReader(input), &stdout, &stderr); code != 0 {
				t.Fatalf("Configure failed: %s", stderr.String())
			}
			wantKey := strings.TrimSuffix(input, "\n")
			if posted["apiKey"] != wantKey || posted["orgId"] != "org-saved" || posted["port"] != 9123 || posted["remember"] != true {
				t.Fatalf("Unexpected configuration (key omitted): org=%v port=%v remember=%v", posted["orgId"], posted["port"], posted["remember"])
			}
			if strings.Contains(stdout.String()+stderr.String(), "must-not-print") || wantKey != "" && strings.Contains(stdout.String()+stderr.String(), wantKey) {
				t.Fatal("Credential appeared in CLI output")
			}
		})
	}
}

func TestHeadlessSetupKeyInputIsPrivateBoundedAndNeverEchoed(t *testing.T) {
	for _, input := range []string{"", "SECRET with spaces", "SECRET\nsecond", "SECRET\v", strings.Repeat("x", 8193)} {
		if key, err := readHeadlessKey(strings.NewReader(input)); err == nil || key != "" || strings.Contains(err.Error(), "SECRET") {
			t.Fatal("Invalid key was accepted or disclosed")
		}
	}
	if key, err := readHeadlessKey(strings.NewReader("synthetic-key\r\n")); err != nil || key != "synthetic-key" {
		t.Fatal("One terminal newline should be accepted")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	if err := os.WriteFile(path, []byte("synthetic-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if key, err := readHeadlessKeyFile(path); err != nil || key != "synthetic-key" {
		t.Fatal("Private key file rejected")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err == nil {
		if _, err := readHeadlessKeyFile(link); err == nil {
			t.Fatal("Symlink key file accepted")
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := readHeadlessKeyFile(path); err == nil {
			t.Fatal("Public key file accepted")
		}
	}
	var stdout, stderr bytes.Buffer
	client := headlessSetupTestClient{func(context.Context, string, string, any, any) error {
		t.Fatal("Invalid flags contacted controller")
		return nil
	}}
	if runHeadlessSetupCommand(context.Background(), client, "configure", []string{"--api-key", "SECRET"}, strings.NewReader(""), &stdout, &stderr) == 0 || strings.Contains(stderr.String(), "SECRET") {
		t.Fatal("Unsupported credential argv accepted or echoed")
	}
}

func TestHeadlessSetupLoginKiloPersistsApprovedOrganization(t *testing.T) {
	var saved, canceled bool
	client := headlessSetupTestClient{func(_ context.Context, method, path string, input, output any) error {
		switch path {
		case "/api/auth/start":
		case "/api/state":
			headlessSetupTestJSON(t, headlessSetupState{Port: 8877, Auth: &loginSession{Status: "approved"}, Organizations: []organization{{ID: "org-one", Name: "One"}}}, output)
		case "/api/config":
			cfg := input.(map[string]any)
			saved = cfg["apiKey"] == "" && cfg["orgId"] == "org-one" && cfg["remember"] == true
		case "/api/auth/cancel":
			canceled = true
		default:
			t.Fatalf("Unexpected %s %s", method, path)
		}
		return nil
	}}
	var out bytes.Buffer
	if err := headlessLogin(context.Background(), client, []string{"kilo"}, &out); err != nil || !saved || canceled {
		t.Fatalf("Approved login not saved: %v", err)
	}
}

func TestHeadlessSetupLoginCancelReachesIndependentDeviceFlow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var canceled bool
	client := headlessSetupTestClient{func(callCtx context.Context, method, path string, input, output any) error {
		switch path {
		case "/api/chatgpt/login":
		case "/api/state":
			headlessSetupTestJSON(t, headlessSetupState{ChatGPT: chatGPTState{Status: "pending", Code: "USER-CODE", VerificationURL: "https://auth.openai.com/codex/device"}}, output)
			cancel()
		case "/api/chatgpt/cancel":
			canceled = callCtx.Err() == nil
		default:
			t.Fatalf("Unexpected %s %s", method, path)
		}
		return nil
	}}
	var out bytes.Buffer
	if err := headlessLogin(ctx, client, []string{"chatgpt"}, &out); err == nil || !canceled || !strings.Contains(out.String(), "USER-CODE") {
		t.Fatal("Cancel did not reach the independent device-login context")
	}
}

func TestHeadlessSetupLoginDoesNotMistakePreviousCredentialsForSuccess(t *testing.T) {
	var canceled bool
	client := headlessSetupTestClient{func(_ context.Context, method, path string, input, output any) error {
		switch path {
		case "/api/chatgpt/login":
		case "/api/state":
			headlessSetupTestJSON(t, headlessSetupState{ChatGPT: chatGPTState{Connected: true, Status: "error", Error: "New sign-in was denied."}}, output)
		case "/api/chatgpt/cancel":
			canceled = true
		default:
			t.Fatalf("Unexpected %s %s", method, path)
		}
		return nil
	}}
	var out bytes.Buffer
	if err := headlessLogin(context.Background(), client, []string{"chatgpt"}, &out); err == nil || !canceled || strings.Contains(out.String(), "saved") {
		t.Fatal("Previous credentials concealed a failed new device login")
	}
}

func TestHeadlessSetupModelEditsHonorCapabilitiesDefaultAndRevision(t *testing.T) {
	state := modelLibraryState{Library: emptyModelLibrary(), Revision: 14}
	var refreshes int
	client := headlessSetupTestClient{func(_ context.Context, method, path string, input, output any) error {
		switch path {
		case "/api/model-library":
			if method == "GET" {
				headlessSetupTestJSON(t, state, output)
			} else {
				request := input.(map[string]any)
				if request["revision"] != state.Revision {
					return errModelLibraryConflict
				}
				state.Library = request["library"].(modelLibrary)
				state.Revision++
				headlessSetupTestJSON(t, state, output)
			}
		case "/api/headless/catalog":
			if method == "POST" {
				refreshes++
			}
			headlessSetupTestJSON(t, headlessSetupCatalog{Models: []modelInfo{{ID: "vendor/one", ContextWindow: 64000, ReasoningEfforts: []string{"low", "high"}}}, Complete: true, FetchedAt: time.Now()}, output)
		default:
			t.Fatalf("Unexpected %s %s", method, path)
		}
		return nil
	}}
	var out bytes.Buffer
	if err := headlessModels(context.Background(), client, []string{"add", "vendor/one", "--reasoning", "high", "--context", "custom", "--context-tokens", "32768", "--name", "Daily"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if refreshes != 0 || state.Library.DefaultModel != "vendor/one" || state.Library.Models[0].ReasoningEffort != "high" || state.Library.Models[0].ContextWindow != 32768 {
		t.Fatal("Exact model preferences or fresh catalog were ignored")
	}
	if err := headlessModels(context.Background(), client, []string{"add", "vendor/one", "--name", "Renamed", "--context-tokens", "49152"}, strings.NewReader(""), &out); err != nil || state.Library.Models[0].ReasoningEffort != "high" || state.Library.Models[0].ContextPreset != "custom" || state.Library.Models[0].ContextWindow != 49152 || state.Library.Models[0].DisplayName != "Renamed" {
		t.Fatal("Updating supplied preferences changed another existing preference")
	}
	previous := state.Revision
	if err := headlessModels(context.Background(), client, []string{"add", "vendor/one", "--reasoning", "ultra"}, strings.NewReader(""), &out); err == nil || state.Revision != previous {
		t.Fatal("Unsupported reasoning was saved")
	}
	if err := headlessModels(context.Background(), client, []string{"remove", "vendor/one"}, strings.NewReader(""), &out); err != nil || state.Library.DefaultModel != "" || len(state.Library.Models) != 0 {
		t.Fatal("Removing the last default left an invalid library")
	}
}

func TestHeadlessSetupCatalogPartialRefreshDoesNotDelayRetry(t *testing.T) {
	var gets, refreshes int
	client := headlessSetupTestClient{func(_ context.Context, method, path string, input, output any) error {
		if path != "/api/headless/catalog" {
			t.Fatalf("Unexpected path %s", path)
		}
		if method == "GET" {
			gets++
		} else {
			refreshes++
		}
		headlessSetupTestJSON(t, headlessSetupCatalog{Models: []modelInfo{{ID: "vendor/one"}}, Complete: false}, output)
		return nil
	}}
	for range 2 {
		if _, err := headlessCatalog(context.Background(), client, false); err != nil {
			t.Fatal(err)
		}
	}
	if gets != 2 || refreshes != 2 {
		t.Fatal("An incomplete catalog suppressed the next refresh")
	}
}

func TestHeadlessSetupImportAvoidsNetworkAndRequiresExplicitRecovery(t *testing.T) {
	data := `{"schemaVersion":1,"defaultModel":"vendor/private","models":[{"id":"vendor/private","contextPreset":"custom","contextWindow":128000,"reasoningCustom":true,"reasoningLevels":["high"],"reasoningEffort":"high"}]}`
	var recovered bool
	client := headlessSetupTestClient{func(_ context.Context, method, path string, input, output any) error {
		if path != "/api/model-library" {
			t.Fatal("Import contacted model gateway")
		}
		if method == "GET" {
			headlessSetupTestJSON(t, modelLibraryState{Library: emptyModelLibrary(), Revision: 7, RecoveryRequired: true}, output)
		} else {
			request := input.(map[string]any)
			recovered = request["recover"] == true && request["revision"] == uint64(7) && request["library"].(modelLibrary).Models[0].ID == "vendor/private"
			headlessSetupTestJSON(t, modelLibraryState{Library: request["library"].(modelLibrary)}, output)
		}
		return nil
	}}
	var out bytes.Buffer
	if err := headlessModels(context.Background(), client, []string{"import", "-"}, strings.NewReader(data), &out); !errors.Is(err, errModelLibraryRecovery) || recovered {
		t.Fatal("Damaged library recovered without explicit consent")
	}
	if err := headlessModels(context.Background(), client, []string{"import", "--recover", "-"}, strings.NewReader(data), &out); err != nil || !recovered {
		t.Fatal("Explicit portable library recovery failed")
	}
}

func TestHeadlessSetupCommandsManualUsesGeneratedTextWithoutInstall(t *testing.T) {
	client := headlessSetupTestClient{func(_ context.Context, method, path string, input, output any) error {
		if method != "GET" || path != "/api/terminal/manual" {
			t.Fatal("Manual setup modified installation")
		}
		headlessSetupTestJSON(t, terminalManualResult{Supported: true, All: "function kilo-codex { safe-command; }\n"}, output)
		return nil
	}}
	var out bytes.Buffer
	if err := headlessCommands(context.Background(), client, []string{"manual"}, &out); err != nil || out.String() != "function kilo-codex { safe-command; }\n" {
		t.Fatal("Manual commands unavailable")
	}
}

func TestHeadlessSetupConnectionRevealsOnlyLocalKeyWhenExplicit(t *testing.T) {
	localKey := "synthetic-local-key-of-at-least-32-bytes"
	client := headlessSetupTestClient{func(_ context.Context, method, path string, input, output any) error {
		if method != "GET" || path != "/api/state" {
			t.Fatal("Unexpected connection request")
		}
		headlessSetupTestJSON(t, map[string]any{"port": 8878, "running": true, "version": "0.55.0", "localKey": localKey, "apiKey": "upstream-never-print", "chatgpt": map[string]any{"access": "oauth-never-print"}}, output)
		return nil
	}}
	for _, show := range []bool{false, true} {
		args := []string{"--json"}
		if show {
			args = append(args, "--show-key")
		}
		var out bytes.Buffer
		if err := headlessConnection(context.Background(), client, args, &out); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), localKey) != show || strings.Contains(out.String(), "upstream-never-print") || strings.Contains(out.String(), "oauth-never-print") || !strings.Contains(out.String(), "http://127.0.0.1:8878/v1") {
			t.Fatal("Connection output violated explicit credential disclosure")
		}
	}
}

func TestHeadlessSetupCommandSuggestionUsesExactProfileAndNeverInstalls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Headless CLI uses Unix shells only")
	}
	home := t.TempDir()
	dir := filepath.Join(home, "profiles", "server's $(not-executed) profile")
	binary := filepath.Join(home, "kilo's headless $(not-executed)")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := headlessSetupTestClient{func(_ context.Context, method, path string, input, output any) error {
		calls++
		if method != "GET" || path != "/api/terminal/commands" || input != nil {
			t.Fatal("Suggestion attempted to install or mutate settings")
		}
		headlessSetupTestJSON(t, terminalCommandsInstallResult{Shell: "bash", StartupFiles: []string{filepath.Join(home, ".bashrc"), filepath.Join(home, ".profile")}}, output)
		return nil
	}}
	var out bytes.Buffer
	printHeadlessCommandSuggestionFor(context.Background(), client, dir, binary, home, "/bin/bash", &out)
	if calls != 1 || !strings.Contains(out.String(), "kilo-codex, kilo-claude, kilo-opencode, and kilo-omp") || !strings.Contains(out.String(), filepath.Join(home, ".bashrc")) || !strings.Contains(out.String(), "only when you run it") {
		t.Fatal("Suggestion lacked commands or startup guidance")
	}
	line := ""
	for _, candidate := range strings.Split(out.String(), "\n") {
		if strings.HasSuffix(candidate, " commands install") {
			line = strings.TrimSpace(candidate)
		}
	}
	if line == "" {
		t.Fatal("Suggestion did not include an install command")
	}
	result, err := exec.Command("/bin/sh", "-c", line).CombinedOutput()
	if err != nil || string(result) != "--config-dir\n"+dir+"\ncommands\ninstall\n" {
		t.Fatalf("Suggestion changed shell arguments: %q, %v", result, err)
	}
	for _, path := range []string{filepath.Join(home, ".bashrc"), filepath.Join(home, ".local")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("Suggestion changed shell files")
		}
	}
}

func TestHeadlessSetupCommandSuggestionSuppressesInstalledAndSupportsManualFallback(t *testing.T) {
	home := t.TempDir()
	for _, scenario := range []string{"installed", "unsupported", "conflict", "incomplete"} {
		t.Run(scenario, func(t *testing.T) {
			client := headlessSetupTestClient{func(_ context.Context, method, path string, input, output any) error {
				if method != "GET" || path != "/api/terminal/commands" {
					t.Fatal("Suggestion mutated installation")
				}
				if scenario == "unsupported" || scenario == "conflict" {
					return errors.New("Installer is unavailable")
				}
				headlessSetupTestJSON(t, terminalCommandsInstallResult{Installed: true, PathConfigured: scenario == "installed", Shell: "zsh"}, output)
				return nil
			}}
			shell := "/bin/zsh"
			if scenario == "unsupported" {
				shell = "/bin/unknown-shell"
			}
			var out bytes.Buffer
			printHeadlessCommandSuggestionFor(context.Background(), client, filepath.Join(home, "profile"), filepath.Join(home, "binary"), home, shell, &out)
			switch scenario {
			case "installed":
				if out.Len() != 0 {
					t.Fatal("Already installed commands produced another suggestion")
				}
			case "unsupported":
				if !strings.Contains(out.String(), "commands manual") || strings.Contains(out.String(), "commands install") || !strings.Contains(out.String(), "Bash/Zsh") {
					t.Fatal("Unsupported shell did not receive accurate manual guidance")
				}
			case "conflict":
				if !strings.Contains(out.String(), "commands manual") || !strings.Contains(out.String(), "commands install") {
					t.Fatal("Installer conflict omitted recovery guidance")
				}
			case "incomplete":
				if !strings.Contains(out.String(), "commands install") {
					t.Fatal("Unconfigured PATH was considered installed")
				}
			}
		})
	}
}

func TestHeadlessSetupCommandsInstallReportsActivationAndPreservesJSONOption(t *testing.T) {
	home := t.TempDir()
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			path := filepath.Join(home, "config's "+shell, "startup")
			client := headlessSetupTestClient{func(_ context.Context, method, endpoint string, input, output any) error {
				if method != "POST" || endpoint != "/api/terminal/commands" {
					t.Fatal("Install did not use installer")
				}
				headlessSetupTestJSON(t, terminalCommandsInstallResult{Installed: true, PathConfigured: true, Shell: shell, StartupFiles: []string{path}}, output)
				return nil
			}}
			var out bytes.Buffer
			if err := headlessCommands(context.Background(), client, []string{"install"}, &out); err != nil || !strings.Contains(out.String(), "source "+helperShellQuote(path)) || !strings.Contains(out.String(), "new "+shell+" terminal") || !strings.Contains(out.String(), "user service running") {
				t.Fatal("Install output lacks shell activation or persistent service instructions")
			}
			out.Reset()
			if err := headlessCommands(context.Background(), client, []string{"install", "--json"}, &out); err != nil {
				t.Fatal(err)
			}
			var decoded terminalCommandsInstallResult
			if json.Unmarshal(out.Bytes(), &decoded) != nil || decoded.Shell != shell || !decoded.Installed {
				t.Fatal("Explicit JSON installer output includes unrelated text")
			}
		})
	}
}

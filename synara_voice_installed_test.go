package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Opt-in contract test, without a microphone, personal login, or inference.
// Codex reads synthetic credentials and its actual status is parsed by the
// exact official beta functions after the compiled Kilo adapter normalizes it.
func TestSynaraInstalledCodexVoiceHealth(t *testing.T) {
	installed, source := os.Getenv("KILO_TEST_SYNARA_APP"), os.Getenv("KILO_TEST_SYNARA_ADAPTER_SOURCE")
	if installed == "" || source == "" {
		t.Skip("set KILO_TEST_SYNARA_APP and KILO_TEST_SYNARA_ADAPTER_SOURCE for the installed voice health contract")
	}
	if runtime.GOOS != "darwin" || !filepath.IsAbs(installed) || !filepath.IsAbs(source) {
		t.Fatal("the installed voice contract currently requires an absolute macOS Synara bundle and compiled Kilo binary")
	}
	if _, err := synaraPackageMetadata(installed, runtime.GOOS); err != nil {
		t.Fatalf("official beta identity: %v", err)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	codex, err := resolveOpenDesignCLI("codex-cli", clientLaunchRuntime{platform: runtime.GOOS, home: userHome, resolve: resolveLaunchClient})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	files, err := planSynaraCodexNormalShim(root, codex, source, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = saveEditorFiles(files); err != nil {
		t.Fatal(err)
	}
	var results []map[string]any
	for _, kind := range []string{"chatgpt", "api-key", "not-logged-in"} {
		home := t.TempDir()
		codexHome := filepath.Join(home, ".codex")
		if err := os.MkdirAll(codexHome, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("cli_auth_credentials_store = \"file\"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if kind != "not-logged-in" {
			var auth map[string]any
			if kind == "chatgpt" {
				header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
				claims := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"synthetic-voice@invalid","exp":4102444800,"https://api.openai.com/auth":{"chatgpt_account_id":"synthetic-voice-account"}}`))
				auth = map[string]any{"auth_mode": "chatgpt", "OPENAI_API_KEY": nil, "tokens": map[string]any{"id_token": header + "." + claims + ".synthetic", "access_token": "synthetic-never-send-access", "refresh_token": "synthetic-never-send-refresh", "account_id": "synthetic-voice-account"}, "last_refresh": time.Now().UTC().Format(time.RFC3339)}
			} else {
				auth = map[string]any{"auth_mode": "apikey", "OPENAI_API_KEY": "synthetic-never-send-api-key"}
			}
			data, _ := json.Marshal(auth)
			if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		values := map[string]string{"HOME": home, "USERPROFILE": home, "CODEX_HOME": codexHome}
		for _, name := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "APPDATA", "LOCALAPPDATA"} {
			values[name] = filepath.Join(home, name)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		command := exec.CommandContext(ctx, files[0].path, "-c", "mcp_servers={}", "login", "status")
		command.Env = clientChildEnvironment(os.Environ(), values, synaraUnsetEnvironment(os.Environ()), runtime.GOOS)
		command.Dir = home
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		_ = command.Run()
		cancel()
		if command.ProcessState == nil {
			t.Fatalf("native status did not start: %s", &stderr)
		}
		code := command.ProcessState.ExitCode()
		if kind == "chatgpt" && (code != 0 || stdout.String() != "{\"authenticated\":true,\"authMethod\":\"chatgpt\"}\n" || stderr.Len() != 0) {
			t.Fatalf("compiled adapter did not normalize actual ChatGPT status: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		results = append(results, map[string]any{"kind": kind, "stdout": stdout.String(), "stderr": stderr.String(), "code": code})
	}
	// Electron's node mode reads ASAR through its native filesystem support; it
	// opens no window and only evaluates the small health-parser functions.
	archive := filepath.Join(installed, "Contents", "Resources", "app.asar")
	serverSource, _, err := synaraReadASARSource(archive, "apps/server/dist/index.mjs", synaraRuntimeSourceLimit)
	if err != nil {
		t.Fatal(err)
	}
	fixture := map[string]any{"entry": filepath.Join(archive, "apps", "server", "dist", "index.mjs"), "digest": openDesignHash(serverSource), "results": results}
	data, _ := json.Marshal(fixture)
	manifest := filepath.Join(t.TempDir(), "voice-health.json")
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "voice-health.cjs")
	if err := os.WriteFile(script, []byte(synaraVoiceInstalledParserScript), 0600); err != nil {
		t.Fatal(err)
	}
	executable := t3CodeBundleExecutable(installed)
	if executable == "" {
		t.Fatal("cannot resolve official Synara bundle executable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, script, manifest)
	command.Env = clientChildEnvironment(cgClientEnv(t.TempDir()), map[string]string{"ELECTRON_RUN_AS_NODE": "1"}, nil, runtime.GOOS)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("official beta voice parser: %v %s", err, output)
	}
	t.Logf("%s", output)
}

const synaraVoiceInstalledParserScript = `const fs = require('node:fs');
const vm = require('node:vm');
const crypto = require('node:crypto');
const fixture = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const source = fs.readFileSync(fixture.entry, 'utf8');
if (crypto.createHash('sha256').update(source).digest('hex') !== fixture.digest) throw Error('unverified beta server');
const names = ['nonEmptyTrimmed', 'detailFromResult', 'extractAuthBoolean', 'extractAuthMethod', 'resolveVoiceTranscriptionAvailability', 'parseAuthStatusFromOutput'];
const helpers = names.map(name => {
  const start = source.indexOf('function ' + name + '(');
  const end = source.indexOf('\n}', start);
  if (start < 0 || end < 0) throw Error('missing official parser helper: ' + name);
  return source.slice(start, end + 2);
}).join('\n');
for (const input of fixture.results) {
  const result = vm.runInNewContext(helpers + '\nparseAuthStatusFromOutput(input)', {input});
  if (input.kind === 'chatgpt') {
    if (result.status !== 'ready' || result.authStatus !== 'authenticated' || result.voiceTranscriptionAvailable !== true) throw Error('ChatGPT voice capability missing');
  } else if (result.voiceTranscriptionAvailable === true) throw Error('voice capability invented for ' + input.kind);
  if (input.kind === 'not-logged-in' && result.authStatus !== 'unauthenticated') throw Error('unauthenticated CLI accepted');
  console.log(JSON.stringify({kind: input.kind, result}));
}
`

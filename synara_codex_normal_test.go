package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func syntheticSynaraNativeHeader(platform string) []byte {
	data := make([]byte, 68)
	if platform == "windows" {
		copy(data, "MZ")
		binary.LittleEndian.PutUint32(data[60:64], 64)
		copy(data[64:], "PE\x00\x00")
	} else if platform == "darwin" || platform == "macos" {
		binary.LittleEndian.PutUint32(data[:4], 0xfeedfacf)
	} else {
		copy(data, "\x7fELF")
	}
	return data
}

func TestSynaraPreparationFixturesUseTargetNativeBinaries(t *testing.T) {
	for _, requested := range []string{"macos", "linux", "windows"} {
		t.Run(requested, func(t *testing.T) {
			a := synaraTestApp(t, requested)
			platform := requested
			if runtime.GOOS == "windows" {
				platform = "windows"
			}
			if a.launcher.platform != platform {
				t.Fatalf("fixture platform %s, want %s", a.launcher.platform, platform)
			}
			cli, err := a.launcher.resolve("codex-cli", "")
			if err != nil {
				t.Fatal(err)
			}
			source, err := a.synaraAdapterSource()
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{cli, source} {
				header, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(header, syntheticSynaraNativeHeader(platform)) {
					t.Fatalf("fixture binary does not match %s: %s (%v)", platform, path, err)
				}
				if err := validateOpenDesignShimBinary(path, platform, true); err != nil {
					t.Fatalf("fixture rejected by real %s native guard: %v", platform, err)
				}
			}
			prepareSynaraFixture(t, a)
			saved, err := a.readSynaraPrepared()
			appBinary, resolveErr := a.launcher.resolve("synara", "")
			if err != nil || resolveErr != nil || !a.synaraReady(saved, appBinary, *a.launcher) {
				t.Fatalf("native fixture was not prepared: %v %v", err, resolveErr)
			}
		})
	}
}

func synaraCodexAdapterFixture(t *testing.T) (root, source, target string) {
	t.Helper()
	root = t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	source, _ = os.Executable()
	target = filepath.Join(t.TempDir(), "Codex — 日本語")
	if runtime.GOOS == "windows" {
		target += ".exe"
	}
	if err := copyOpenDesignShimBinary(source, target); err != nil {
		t.Fatal(err)
	}
	return
}

func TestSynaraCodexNormalAdapterOnlyRecognizesExactChatGPTHealth(t *testing.T) {
	for _, test := range []struct {
		stdout, stderr string
		want           bool
	}{
		{"", "Logged in using ChatGPT\n", true},
		{"Logged in using ChatGPT\r\n", "", true},
		{"Logged in using an API key\n", "", false},
		{"", "Not logged in\n", false},
		{"Logged in using ChatGPT\n", "Not logged in\n", false},
		{"Logged in using ChatGPT\n", "Logged in using ChatGPT\n", false},
		{"Logged in using ChatGPT\nadditional warning\n", "", false},
		{`{"authenticated":true,"authMethod":"chatgpt"}`, "", false},
		{"Logged in using ChatGPT tokens", "", false},
		{"", "", false},
	} {
		if got := synaraCodexChatGPTStatus([]byte(test.stdout), []byte(test.stderr)); got != test.want {
			t.Fatalf("status %q/%q: %v, want %v", test.stdout, test.stderr, got, test.want)
		}
	}
	exact := []string{"-c", "mcp_servers={}", "login", "status"}
	if !synaraCodexNormalHealthArgs(exact) {
		t.Fatal("installed Synara health probe was not recognized")
	}
	for _, args := range [][]string{nil, {"login", "status"}, {"app-server"}, {"-c", "mcp_servers={}", "login", "status", "--json"}, {"-c", "mcp_servers={other=true}", "login", "status"}, {"--version"}} {
		if synaraCodexNormalHealthArgs(args) {
			t.Fatalf("unrelated Codex command intercepted: %q", args)
		}
	}
}

func TestSynaraCodexNormalAdapterHealthDelegatesWithoutInventingAuth(t *testing.T) {
	executable, _ := os.Executable()
	for _, test := range []struct {
		name, stdout, stderr string
		exit, wantExit       int
		normalize            bool
		timeout              bool
	}{
		{"ChatGPT stderr", "", "Logged in using ChatGPT\n", 0, 0, true, false},
		{"ChatGPT stdout", "Logged in using ChatGPT\n", "", 0, 0, true, false},
		{"API key", "", "Logged in using an API key\n", 0, 0, false, false},
		{"not logged in", "", "Not logged in\n", 1, 1, false, false},
		{"ChatGPT failed exit", "", "Logged in using ChatGPT\n", 7, 7, false, false},
		{"contradictory", "Not logged in\n", "Logged in using ChatGPT\n", 0, 0, false, false},
		{"future JSON", `{"authenticated":true,"authMethod":"chatgpt"}`, "", 0, 0, false, false},
		{"unknown", "", "a new status format", 0, 0, false, false},
		{"truncated", "", strings.Repeat("x", synaraCodexNormalHealthLimit+1), 0, 1, false, false},
		{"timeout", "", "", 0, 1, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := map[string]string{"KILO_TEST_SYNARA_ADAPTER_CHILD": "health", "KILO_TEST_SYNARA_ADAPTER_STDOUT": test.stdout, "KILO_TEST_SYNARA_ADAPTER_STDERR": test.stderr, "KILO_TEST_SYNARA_ADAPTER_EXIT": strconv.Itoa(test.exit)}
			if test.timeout {
				values["KILO_TEST_SYNARA_ADAPTER_WAIT"] = "1"
			}
			environment := clientChildEnvironment(os.Environ(), values, nil, runtime.GOOS)
			timeout := 5 * time.Second
			if test.timeout {
				timeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			var stdout, stderr bytes.Buffer
			code := synaraCodexNormalHealth(ctx, executable, []string{"-test.run=^TestSynaraCodexNormalAdapterChild$"}, environment, &stdout, &stderr)
			if code != test.wantExit {
				t.Fatalf("exit %d want %d; stderr=%q", code, test.wantExit, stderr.String())
			}
			if test.normalize {
				if stdout.String() != "{\"authenticated\":true,\"authMethod\":\"chatgpt\"}\n" || stderr.Len() != 0 {
					t.Fatalf("canonical ChatGPT status not normalized: %q/%q", stdout.String(), stderr.String())
				}
			} else if test.wantExit != 1 || test.name == "not logged in" {
				if stdout.String() != test.stdout || stderr.String() != test.stderr {
					t.Fatalf("native non-ChatGPT/error status changed: %q/%q", stdout.String(), stderr.String())
				}
			} else if strings.Contains(stdout.String(), `"authMethod":"chatgpt"`) {
				t.Fatal("failed/truncated status granted voice capability")
			}
		})
	}
}

func TestSynaraCodexNormalAdapterNativePassthrough(t *testing.T) {
	root, source, target := synaraCodexAdapterFixture(t)
	files, err := planSynaraCodexNormalShim(root, target, source, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saveEditorFiles(files); err != nil {
		t.Fatal(err)
	}
	shim := synaraCodexNormalShimPath(root, runtime.GOOS)
	canonicalTarget, _ := filepath.EvalSymlinks(target)
	if got, err := synaraCodexNormalShimTarget(shim, runtime.GOOS); err != nil || got != canonicalTarget {
		t.Fatalf("descriptor target: %q %v", got, err)
	}
	working := t.TempDir()
	marker := filepath.Join(working, "must-not-exist")
	args := []string{"-test.run=^TestSynaraCodexNormalAdapterChild$", "--", "app-server", "", "日本語 😀\n'quotes' \\", "$(touch " + marker + ") & | %PATH% !"}
	input := "unchanged stdin\n😀"
	for _, exit := range []int{0, 39} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, shim, args...)
		command.Dir = working
		command.Env = clientChildEnvironment(os.Environ(), map[string]string{"KILO_TEST_SYNARA_ADAPTER_CHILD": "passthrough", "KILO_TEST_SYNARA_ADAPTER_EXIT": strconv.Itoa(exit), "CODEX_HOME": "synthetic normal home", "KILO_LOCAL_API_KEY": "synthetic ignored local token"}, nil, runtime.GOOS)
		command.Stdin = strings.NewReader(input)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		if exit == 0 && err != nil || exit != 0 && (command.ProcessState == nil || command.ProcessState.ExitCode() != exit) {
			t.Fatalf("exit lost: %v %s", err, &stderr)
		}
		var actual struct {
			Args                               []string
			Input, Directory, CodexHome, Token string
		}
		if json.Unmarshal(stdout.Bytes(), &actual) != nil {
			t.Fatalf("invalid passthrough response: %q %q", stdout.String(), stderr.String())
		}
		actualDir, _ := filepath.EvalSymlinks(actual.Directory)
		wantedDir, _ := filepath.EvalSymlinks(working)
		if !reflect.DeepEqual(actual.Args, args) || actual.Input != input || actualDir != wantedDir || actual.CodexHome != "synthetic normal home" || actual.Token != "synthetic ignored local token" || stderr.String() != "codex-native-stderr\n" {
			t.Fatalf("native CLI input/output changed: %+v %q", actual, stderr.String())
		}
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("CLI input interpreted by shell")
	}
}

func TestSynaraCodexNormalAdapterChild(t *testing.T) {
	mode := os.Getenv("KILO_TEST_SYNARA_ADAPTER_CHILD")
	if mode == "" {
		return
	}
	executable, _ := os.Executable()
	if isSynaraCodexNormalShim(executable, runtime.GOOS) {
		handled, code := runSynaraCodexNormalShim()
		if !handled {
			os.Exit(125)
		}
		os.Exit(code)
	}
	if mode == "health" {
		if os.Getenv("KILO_TEST_SYNARA_ADAPTER_WAIT") != "" {
			time.Sleep(20 * time.Second)
		}
		if pidPath := os.Getenv("KILO_TEST_SYNARA_ADAPTER_DESCENDANT_PID"); pidPath != "" {
			child := exec.Command(executable, "-test.run=^TestSynaraCodexNormalAdapterChild$")
			child.Env = clientChildEnvironment(os.Environ(), map[string]string{"KILO_TEST_SYNARA_ADAPTER_CHILD": "hold-pipe"}, nil, runtime.GOOS)
			child.Stdout, child.Stderr = os.Stdout, os.Stderr
			if err := child.Start(); err != nil {
				os.Exit(124)
			}
			if err := os.WriteFile(pidPath, []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
				_ = child.Process.Kill()
				os.Exit(124)
			}
		}
		fmtOut, fmtErr := os.Getenv("KILO_TEST_SYNARA_ADAPTER_STDOUT"), os.Getenv("KILO_TEST_SYNARA_ADAPTER_STDERR")
		_, _ = io.WriteString(os.Stdout, fmtOut)
		_, _ = io.WriteString(os.Stderr, fmtErr)
	} else if mode == "hold-pipe" {
		time.Sleep(10 * time.Second)
	} else {
		input, _ := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
		directory, _ := os.Getwd()
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Args                               []string
			Input, Directory, CodexHome, Token string
		}{os.Args[1:], string(input), directory, os.Getenv("CODEX_HOME"), os.Getenv("KILO_LOCAL_API_KEY")})
		_, _ = io.WriteString(os.Stderr, "codex-native-stderr\n")
	}
	code, _ := strconv.Atoi(os.Getenv("KILO_TEST_SYNARA_ADAPTER_EXIT"))
	os.Exit(code)
}

func TestSynaraCodexNormalAdapterHealthInheritedPipesRemainBounded(t *testing.T) {
	executable, _ := os.Executable()
	pidPath := filepath.Join(t.TempDir(), "own-descendant.pid")
	t.Cleanup(func() {
		data, _ := os.ReadFile(pidPath)
		pid, _ := strconv.Atoi(string(data))
		if pid > 1 {
			if process, err := os.FindProcess(pid); err == nil {
				_ = process.Kill()
			}
		}
	})
	environment := clientChildEnvironment(os.Environ(), map[string]string{"KILO_TEST_SYNARA_ADAPTER_CHILD": "health", "KILO_TEST_SYNARA_ADAPTER_STDERR": "Logged in using ChatGPT\n", "KILO_TEST_SYNARA_ADAPTER_DESCENDANT_PID": pidPath, "KILO_TEST_SYNARA_ADAPTER_EXIT": "0"}, nil, runtime.GOOS)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := synaraCodexNormalHealth(ctx, executable, []string{"-test.run=^TestSynaraCodexNormalAdapterChild$"}, environment, &stdout, &stderr)
	if time.Since(start) > 3500*time.Millisecond {
		t.Fatal("health hung on inherited status pipes")
	}
	if code == 0 || bytes.Contains(stdout.Bytes(), []byte(`"authMethod":"chatgpt"`)) {
		t.Fatal("incomplete status granted voice capability")
	}
}

func TestSynaraCodexNormalAdapterPrivateModesRollbackAndIntegrity(t *testing.T) {
	root, source, target := synaraCodexAdapterFixture(t)
	files, err := planSynaraCodexNormalShim(root, target, source, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saveEditorFiles(files); err != nil {
		t.Fatal(err)
	}
	shim := files[0].path
	before, _ := os.ReadFile(shim)
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(shim)
		if info.Mode().Perm() != 0700 {
			t.Fatalf("adapter mode: %v", info.Mode())
		}
	}
	replacement, err := prepareSynaraAdapterBinary(shim, append(bytes.Clone(before), 0))
	if err != nil {
		t.Fatal(err)
	}
	fail := profileFile{path: filepath.Join(root, "missing", "fail.json"), new: []byte("{}"), changed: true}
	if _, err := saveEditorFiles([]profileFile{replacement, fail}); err == nil {
		t.Fatal("rollback fixture unexpectedly succeeded")
	}
	after, _ := os.ReadFile(shim)
	if !bytes.Equal(before, after) {
		t.Fatal("rollback changed adapter bytes")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(shim)
		if info.Mode().Perm() != 0700 {
			t.Fatal("rollback removed adapter executable mode")
		}
	}
	if synaraCodexNormalFileLimit(filepath.Join("adapters", filepath.Base(shim)), runtime.GOOS) != openDesignOpenCodeBinaryLimit {
		t.Fatal("adapter retains catalog limit")
	}
	for _, path := range []string{filepath.Base(shim), filepath.Join("profiles", filepath.Base(shim)), filepath.Join("adapters", synaraCodexNormalDescriptorName), filepath.Join("adapters", "arbitrary-large-file")} {
		if synaraCodexNormalFileLimit(path, runtime.GOOS) != catalogLimit {
			t.Fatalf("unrelated file received large binary allowance: %q", path)
		}
	}
}

func TestSynaraCodexNormalAdapterRejectsUnsafeDestinationsAndDescriptors(t *testing.T) {
	for _, mutation := range []string{"source symlink to invalid", "target adapter", "destination symlink", "descriptor symlink", "backup symlink", "non-native source", "non-native target"} {
		t.Run(mutation, func(t *testing.T) {
			root, source, target := synaraCodexAdapterFixture(t)
			dir := filepath.Dir(synaraCodexNormalShimPath(root, runtime.GOOS))
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			shim := synaraCodexNormalShimPath(root, runtime.GOOS)
			switch mutation {
			case "target adapter":
				target = shim
				_ = copyOpenDesignShimBinary(source, target)
			case "destination symlink", "descriptor symlink", "backup symlink":
				if runtime.GOOS == "windows" {
					t.Skip("symlink privilege not assumed")
				}
				path := shim
				if mutation == "descriptor symlink" {
					path = filepath.Join(dir, synaraCodexNormalDescriptorName)
				}
				if mutation == "backup symlink" {
					path += ".bak"
					_ = copyOpenDesignShimBinary(source, shim)
					_ = os.WriteFile(shim, append(syntheticSynaraNativeHeader(runtime.GOOS), 1), 0700)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "non-native source":
				source = filepath.Join(root, "fake-source")
				_ = os.WriteFile(source, []byte("#!/bin/sh\nexit 0\n"), 0700)
			case "non-native target":
				_ = os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0700)
			case "source symlink to invalid":
				if runtime.GOOS == "windows" {
					t.Skip("symlink privilege not assumed")
				}
				source = filepath.Join(root, "source-link")
				_ = os.Symlink(filepath.Join(root, "missing"), source)
			}
			if _, err := planSynaraCodexNormalShim(root, target, source, runtime.GOOS); err == nil {
				t.Fatal("unsafe adapter accepted")
			}
		})
	}
	root, source, target := synaraCodexAdapterFixture(t)
	files, err := planSynaraCodexNormalShim(root, target, source, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = saveEditorFiles(files); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{"version":2,"binary":"` + target + `"}`, `{"version":1,"binary":"relative"}`, `{"version":1,"binary":"` + target + `","extra":true}`, `{"version":1,"binary":"` + target + `"} {}`, `{"version":1,"binary":"relative","binary":"` + target + `"}`, `{"version":2,"version":1,"binary":"` + target + `"}`} {
		if err := os.WriteFile(files[1].path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := synaraCodexNormalShimTarget(files[0].path, runtime.GOOS); err == nil {
			t.Fatal("invalid descriptor accepted")
		}
	}
}

func TestSynaraCodexNormalAdapterPreparationBindsResolvedCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privilege not assumed")
	}
	a := synaraTestApp(t, runtime.GOOS)
	oldResolve := a.launcher.resolve
	original, _ := oldResolve("codex-cli", "")
	link := filepath.Join(a.launcher.home, "codex-current")
	if err := os.Symlink(original, link); err != nil {
		t.Fatal(err)
	}
	a.launcher.resolve = func(id, custom string) (string, error) {
		if id == "codex-cli" {
			return link, nil
		}
		return oldResolve(id, custom)
	}
	prepareSynaraFixture(t, a)
	saved, err := a.readSynaraPrepared()
	if err != nil {
		t.Fatal(err)
	}
	binary, _ := a.launcher.resolve("synara", "")
	if !a.synaraReady(saved, binary, *a.launcher) {
		t.Fatal("prepared adapter is not ready")
	}
	newCLI := filepath.Join(a.launcher.home, "codex-updated")
	if err := os.WriteFile(newCLI, syntheticSynaraNativeHeader(runtime.GOOS), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newCLI, link); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(original); err != nil {
		t.Fatal("old target should remain installed")
	}
	if a.synaraReady(saved, binary, *a.launcher) {
		t.Fatal("retargeted package-manager link launched the stale Codex CLI")
	}
}

func TestSynaraCodexNormalAdapterPreparedIdentityAndHashesRemainMandatory(t *testing.T) {
	a := synaraTestApp(t, runtime.GOOS)
	prepareSynaraFixture(t, a)
	saved, err := a.readSynaraPrepared()
	if err != nil {
		t.Fatal(err)
	}
	binary, _ := a.launcher.resolve("synara", "")
	if !a.synaraReady(saved, binary, *a.launcher) {
		t.Fatal("initial adapter not ready")
	}
	shim := synaraCodexNormalShimPath(synaraPaths(a.dir).Root, runtime.GOOS)
	relative, _ := filepath.Rel(synaraPaths(a.dir).Root, shim)
	for _, name := range []string{relative, filepath.Join("adapters", synaraCodexNormalDescriptorName)} {
		hash := saved.Files[name]
		delete(saved.Files, name)
		if a.synaraReady(saved, binary, *a.launcher) {
			t.Fatalf("missing immutable adapter hash accepted: %s", name)
		}
		saved.Files[name] = hash
	}
	config := saved.Providers[synaraCodexNormalID].(map[string]any)["config"].(map[string]any)
	config["binaryPath"] = "different-adapter"
	if a.synaraReady(saved, binary, *a.launcher) {
		t.Fatal("different normal adapter accepted")
	}
	config["binaryPath"] = shim
	data, _ := os.ReadFile(shim)
	if err := os.WriteFile(shim, append(bytes.Clone(data), 0), 0700); err != nil {
		t.Fatal(err)
	}
	if a.synaraReady(saved, binary, *a.launcher) {
		t.Fatal("modified private adapter accepted")
	}
	if err := os.WriteFile(shim, data, 0700); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(shim, 0600); err != nil {
			t.Fatal(err)
		}
		if a.synaraReady(saved, binary, *a.launcher) {
			t.Fatal("non-executable adapter accepted")
		}
	}
}

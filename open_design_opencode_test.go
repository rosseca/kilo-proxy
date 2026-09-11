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

func openDesignShimFixture(t *testing.T) (dir, source, target string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source, target = filepath.Join(t.TempDir(), "kilo.exe"), filepath.Join(t.TempDir(), "opencode.exe")
	// These files exercise preparation only; the subprocess test below uses the
	// actual native Go test executable for both the adapter and fake OpenCode.
	data := make([]byte, 128)
	if runtime.GOOS == "windows" {
		copy(data, "MZ")
		binary.LittleEndian.PutUint32(data[60:], 64)
		copy(data[64:], "PE\x00\x00")
	} else {
		copy(data, "\x7fELF")
	}
	for _, path := range []string{source, target} {
		if err := os.WriteFile(path, data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "opencode.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir, source, target
}

func TestPrepareOpenDesignOpenCodeShim(t *testing.T) {
	dir, source, target := openDesignShimFixture(t)
	t.Setenv("KILO_LOCAL_API_KEY", "synthetic-secret-not-for-descriptor")
	shim, err := prepareOpenDesignOpenCodeShimFrom(dir, target, source, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	if shim != openDesignOpenCodeShimPath(dir) {
		t.Fatalf("unexpected shim path %q", shim)
	}
	want, _ := os.ReadFile(source)
	got, err := os.ReadFile(shim)
	if err != nil || !bytes.Equal(want, got) {
		t.Fatal("native executable was not copied exactly", err)
	}
	for _, path := range []string{shim, filepath.Join(dir, openDesignOpenCodeDescriptorName)} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			t.Fatal("adapter file is not private and regular", err)
		}
	}
	info, _ := os.Stat(shim)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		t.Fatalf("native adapter permissions: %v", info.Mode())
	}
	data, _ := os.ReadFile(filepath.Join(dir, openDesignOpenCodeDescriptorName))
	var descriptor map[string]any
	canonical, _ := filepath.EvalSymlinks(target)
	if json.Unmarshal(data, &descriptor) != nil || !reflect.DeepEqual(descriptor, map[string]any{"version": float64(1), "binary": canonical, "config": "opencode.json"}) {
		t.Fatalf("unexpected descriptor: %s", data)
	}
	if bytes.Contains(data, []byte("synthetic-secret")) {
		t.Fatal("credential leaked to adapter descriptor")
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, ".opencode-adapter-*")); len(matches) != 0 {
		t.Fatal("temporary adapter copies remain")
	}
}

func TestPrepareOpenDesignOpenCodeShimRejectsUnsafeFiles(t *testing.T) {
	for _, name := range []string{"profile", "descriptor", "destination", "directory", "oversize-profile", "oversize-descriptor", "source-is-target"} {
		t.Run(name, func(t *testing.T) {
			dir, source, target := openDesignShimFixture(t)
			outside := filepath.Join(t.TempDir(), "untouched")
			if err := os.WriteFile(outside, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			symlink := func(path, destination string) {
				t.Helper()
				if err := os.Symlink(destination, path); err != nil {
					if runtime.GOOS == "windows" {
						t.Skip("symlink privilege unavailable")
					}
					t.Fatal(err)
				}
			}
			switch name {
			case "profile":
				_ = os.Remove(filepath.Join(dir, "opencode.json"))
				symlink(filepath.Join(dir, "opencode.json"), outside)
			case "descriptor":
				symlink(filepath.Join(dir, openDesignOpenCodeDescriptorName), outside)
			case "destination":
				symlink(openDesignOpenCodeShimPath(dir), outside)
			case "directory":
				link := filepath.Join(t.TempDir(), "linked")
				symlink(link, dir)
				dir = link
			case "oversize-profile", "oversize-descriptor":
				path, limit := filepath.Join(dir, "opencode.json"), int64(catalogLimit)
				if name == "oversize-descriptor" {
					path, limit = filepath.Join(dir, openDesignOpenCodeDescriptorName), openDesignOpenCodeDescriptorLimit
				}
				f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				err = f.Truncate(limit + 1)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "source-is-target":
				target = source
			}
			if _, err := prepareOpenDesignOpenCodeShimFrom(dir, target, source, runtime.GOOS); err == nil {
				t.Fatal("unsafe adapter preparation succeeded")
			}
			data, _ := os.ReadFile(outside)
			if string(data) != "untouched" {
				t.Fatal("external file was modified")
			}
			if name != "destination" {
				if _, err := os.Lstat(openDesignOpenCodeShimPath(dir)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("native adapter copied before failed preflight")
				}
			}
		})
	}
}

func TestOpenDesignOpenCodeShimWindowsRejectsShellLaunchers(t *testing.T) {
	for _, name := range []string{"opencode.cmd", "opencode.bat", "script.exe"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(path, bytes.Repeat([]byte("shell command\n"), 20), 0700); err != nil {
				t.Fatal(err)
			}
			if err := validateOpenDesignShimBinary(path, "windows", false); err == nil || !strings.Contains(err.Error(), ".exe") {
				t.Fatalf("expected a native .exe explanation, got %v", err)
			}
		})
	}
}

func TestOpenDesignOpenCodeShimInvocation(t *testing.T) {
	dir, source, target := openDesignShimFixture(t)
	shim, err := prepareOpenDesignOpenCodeShimFrom(dir, target, source, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	content := `{"mcp":{"open-design":{"url":"http://127.0.0.1:9999/mcp"}}}`
	inherited := []string{"OPENCODE_CONFIG=old", "OPENCODE_CONFIG_CONTENT=" + content, "KILO_LOCAL_API_KEY=synthetic"}
	if runtime.GOOS == "windows" {
		inherited = append(inherited, "OpenCode_Config=case-variant")
	}
	bin, environment, err := openDesignOpenCodeShimInvocation(shim, inherited, runtime.GOOS)
	canonical, _ := filepath.EvalSymlinks(target)
	if err != nil || bin != canonical || !reflect.DeepEqual(environment, []string{"OPENCODE_CONFIG_CONTENT=" + content, "KILO_LOCAL_API_KEY=synthetic", "OPENCODE_CONFIG=" + filepath.Join(dir, "opencode.json")}) {
		t.Fatalf("wrong invocation: %q %q %v", bin, environment, err)
	}
	encoded, _ := json.Marshal(openDesignOpenCodeDescriptor{1, canonical, "opencode.json"})
	valid := string(encoded)
	for _, data := range []string{
		strings.Replace(valid, `"version":1`, `"version":2`, 1),
		strings.Replace(valid, `"config":"opencode.json"`, `"config":"../opencode.json"`, 1),
		strings.TrimSuffix(valid, "}") + `,"extra":true}`,
		valid + ` {}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, openDesignOpenCodeDescriptorName), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := openDesignOpenCodeShimInvocation(shim, nil, runtime.GOOS); err == nil {
			t.Fatal("unsafe descriptor accepted")
		}
	}
}

type openDesignShimChildResult struct {
	Args                          []string
	Input, Config, Content, Token string
	Directory                     string
}

func TestOpenDesignOpenCodeShimNativeRoundTrip(t *testing.T) {
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source := os.Getenv("KILO_TEST_OPENCODE_BINARY")
	if source == "" {
		source = testExecutable
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "opencode.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "Fake OpenCode — 日本語")
	if runtime.GOOS == "windows" {
		target += ".exe"
	}
	if err := copyOpenDesignShimBinary(testExecutable, target); err != nil {
		t.Fatal(err)
	}
	shim, err := prepareOpenDesignOpenCodeShimFrom(dir, target, source, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	working := t.TempDir()
	marker := filepath.Join(working, "must-not-exist")
	args := []string{"-test.run=^TestOpenDesignOpenCodeShimChild$", "--", "run", "--format", "json", "", "Diseña 日本語 😀\n\"quotes\" 'single' \\trailing\\", "$(touch " + marker + ") & | < > %PATH% ! !", "--version"}
	input := "multiline stdin\nñ λ 😀\n"
	content := `{"mcp":{"open-design":{"command":["tool","a b"]}}}`
	for _, code := range []int{0, 37} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, shim, args...)
		cmd.Dir = working
		cmd.Env = clientChildEnvironment(os.Environ(), map[string]string{
			"KILO_TEST_OD_SHIM_CHILD": "1", "KILO_TEST_OD_SHIM_EXIT": strconv.Itoa(code),
			"OPENCODE_CONFIG": "incorrect-inherited-profile", "OPENCODE_CONFIG_CONTENT": content,
			"KILO_LOCAL_API_KEY": "synthetic-test-local-key",
		}, nil, runtime.GOOS)
		cmd.Stdin = strings.NewReader(input)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if code == 0 && err != nil || code != 0 && (cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != code) {
			t.Fatalf("exit status lost: %v; stderr=%s", err, &stderr)
		}
		var got openDesignShimChildResult
		if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
			t.Fatalf("decode child result: %v; stdout=%s stderr=%s", err, &stdout, &stderr)
		}
		gotDir, _ := filepath.EvalSymlinks(got.Directory)
		wantDir, _ := filepath.EvalSymlinks(working)
		if !reflect.DeepEqual(got.Args, args) || got.Input != input || got.Config != filepath.Join(dir, "opencode.json") || got.Content != content || got.Token != "synthetic-test-local-key" || gotDir != wantDir {
			t.Fatalf("native adapter changed CLI inputs: %+v", got)
		}
		if stderr.String() != "fake-opencode-stderr\n" {
			t.Fatalf("stderr was not preserved: %q", &stderr)
		}
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("prompt was interpreted by a shell")
	}
}

func TestOpenDesignOpenCodeShimChild(t *testing.T) {
	if os.Getenv("KILO_TEST_OD_SHIM_CHILD") != "1" {
		return
	}
	executable, _ := os.Executable()
	if isOpenDesignOpenCodeShim(executable, runtime.GOOS) {
		handled, code := runOpenDesignOpenCodeShim()
		if !handled {
			os.Exit(125)
		}
		os.Exit(code)
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
	if err != nil {
		os.Exit(124)
	}
	dir, _ := os.Getwd()
	_ = json.NewEncoder(os.Stdout).Encode(openDesignShimChildResult{Args: os.Args[1:], Input: string(input), Config: os.Getenv("OPENCODE_CONFIG"), Content: os.Getenv("OPENCODE_CONFIG_CONTENT"), Token: os.Getenv("KILO_LOCAL_API_KEY"), Directory: dir})
	_, _ = io.WriteString(os.Stderr, "fake-opencode-stderr\n")
	code, _ := strconv.Atoi(os.Getenv("KILO_TEST_OD_SHIM_EXIT"))
	os.Exit(code)
}

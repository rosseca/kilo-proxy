package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func terminalInstallerFixture(t *testing.T) (string, string, string) {
	t.Helper()
	home := t.TempDir()
	configDir := filepath.Join(home, "Kilo Proxy's settings")
	binary := filepath.Join(home, "Kilo Proxy's.app", "Contents", "MacOS", "kilo-proxy")
	for _, directory := range []string{configDir, filepath.Dir(binary)} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// A synthetic application reports the arguments and terminal working folder.
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\000' \"$PWD\" \"$@\"\ncat\nexit \"${KILO_INSTALL_TEST_EXIT:-0}\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"localKey":"synthetic-local-secret","apiKey":"synthetic-upstream-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZDOTDIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	return home, configDir, binary
}

func terminalInstallerRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTerminalCommandsInstallKeepsCurrentTerminalArgumentsAndCredentialsOutOfShims(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable scripts")
	}
	home, configDir, binary := terminalInstallerFixture(t)
	status, err := terminalCommandsStatus(home, configDir, binary, "/bin/zsh", "darwin")
	if err != nil || status.Installed || status.PathConfigured {
		t.Fatalf("new status = %+v, %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("status inspection wrote the home directory")
	}
	result, err := installTerminalCommands(home, configDir, binary, "/bin/zsh", "darwin")
	if err != nil || !result.Installed || !result.PathConfigured || result.Shell != "zsh" || len(result.Backups) != 0 {
		t.Fatalf("install = %+v, %v", result, err)
	}
	project := filepath.Join(home, "project with spaces & 'quotes'")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	physicalProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	for name, client := range map[string]string{"kilo-codex": "codex-cli", "kilo-claude": "claude"} {
		path := result.Commands[name]
		data := terminalInstallerRead(t, path)
		for _, secret := range []string{"synthetic-local-secret", "synthetic-upstream-secret", "KILO_LOCAL_API_KEY=", "ANTHROPIC_AUTH_TOKEN="} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatalf("%s embedded a credential", name)
			}
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("%s lacks private executable permissions: %v", name, err)
		}
		args := []string{"resume", "--model", "vendor/model", "argument with 'quotes' and spaces", "$(not-a-command)", "", "--config-dir", "client-value"}
		command := exec.Command(path, args...)
		command.Dir = project
		command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "KILO_INSTALL_TEST_EXIT=23"}
		command.Stdin = strings.NewReader("interactive stdin remains connected")
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err = command.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 23 || stderr.Len() != 0 {
			t.Fatalf("%s did not preserve the child exit code: %v %s", name, err, stderr.String())
		}
		want := append([]string{physicalProject, "--terminal-agent", client, "--config-dir", configDir, "--"}, args...)
		expected := strings.Join(want, "\x00") + "\x00interactive stdin remains connected"
		if stdout.String() != expected {
			t.Fatalf("%s changed cwd, argv or stdin: got %q want %q", name, stdout.String(), expected)
		}
	}
	status, err = terminalCommandsStatus(home, configDir, binary, "/bin/zsh", "macos")
	if err != nil || !status.Installed || !status.PathConfigured {
		t.Fatalf("saved status = %+v, %v", status, err)
	}
}

func TestTerminalCommandsInstallPreservesStartupAndIsIdempotent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions and executable modes; terminal commands are unsupported on Windows")
	}
	home, configDir, binary := terminalInstallerFixture(t)
	startup := filepath.Join(home, ".zshrc")
	untouched := filepath.Join(home, "must-not-exist")
	original := []byte("# My existing configuration\nexport KEEP_ME='yes'\nprintf unsafe > " + helperShellQuote(untouched))
	if err := os.WriteFile(startup, original, 0640); err != nil {
		t.Fatal(err)
	}
	first, err := installTerminalCommands(home, configDir, binary, "zsh", "linux")
	if err != nil || len(first.Backups) != 1 || !bytes.Equal(terminalInstallerRead(t, first.Backups[0]), original) {
		t.Fatalf("existing startup backup = %+v, %v", first, err)
	}
	if _, err := os.Stat(untouched); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("installer executed startup configuration")
	}
	saved := terminalInstallerRead(t, startup)
	if !bytes.HasPrefix(saved, original) || bytes.Count(saved, []byte(terminalPathBegin)) != 1 {
		t.Fatal("installer discarded startup text or duplicated the PATH block")
	}
	before, _ := os.Stat(startup)
	if before.Mode().Perm() != 0640 {
		t.Fatal("installer changed startup file permissions")
	}
	second, err := installTerminalCommands(home, configDir, binary, "zsh", "linux")
	after, _ := os.Stat(startup)
	if err != nil || len(second.Backups) != 0 || !bytes.Equal(saved, terminalInstallerRead(t, startup)) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("repeat install changed files or made backups: %+v, %v", second, err)
	}
	// Moving the app updates only its owned shims; startup configuration stays put.
	third, err := installTerminalCommands(home, configDir, filepath.Join(home, "Moved Kilo Proxy"), "zsh", "linux")
	if err != nil || len(third.Backups) != 2 || !bytes.Equal(saved, terminalInstallerRead(t, startup)) {
		t.Fatalf("app path update = %+v, %v", third, err)
	}
	for _, backup := range third.Backups {
		if !bytes.Contains(terminalInstallerRead(t, backup), []byte(helperShellQuote(binary))) {
			t.Fatal("updated command backup lost the old application path")
		}
	}
}

func TestTerminalCommandsInstallBashLoginFiles(t *testing.T) {
	for _, login := range []string{".bash_profile", ".bash_login", ".profile", ""} {
		t.Run("login"+login, func(t *testing.T) {
			home, configDir, binary := terminalInstallerFixture(t)
			if login != "" {
				if err := os.WriteFile(filepath.Join(home, login), []byte("# Keep this login setup\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := installTerminalCommands(home, configDir, binary, "/bin/bash", "linux")
			if login == "" {
				login = ".profile"
			}
			want := []string{filepath.Join(home, ".bashrc"), filepath.Join(home, login)}
			if err != nil || !reflect.DeepEqual(result.StartupFiles, want) {
				t.Fatalf("Bash startup files = %v, %v; want %v", result.StartupFiles, err, want)
			}
			for _, path := range want {
				if bytes.Count(terminalInstallerRead(t, path), []byte(terminalPathBegin)) != 1 {
					t.Fatal("Bash shell is missing its one managed PATH block")
				}
			}
		})
	}
	home, configDir, binary := terminalInstallerFixture(t)
	for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("# existing\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := installTerminalCommands(home, configDir, binary, "bash", "linux")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, ".bash_login"), filepath.Join(home, ".profile")} {
		if string(terminalInstallerRead(t, path)) != "# existing\n" {
			t.Fatal("installer changed an unused Bash login file")
		}
	}
}

func TestTerminalCommandsInstallRejectsConflictsBeforeWriting(t *testing.T) {
	for _, conflict := range []string{"unowned-command", "command-link", "directory-link", "startup-link", "malformed-block", "duplicate-block"} {
		t.Run(conflict, func(t *testing.T) {
			home, configDir, binary := terminalInstallerFixture(t)
			outside := t.TempDir()
			bin := filepath.Join(home, ".local", "bin")
			if conflict == "directory-link" {
				if err := os.Symlink(outside, filepath.Join(home, ".local")); err != nil {
					t.Skip(err)
				}
			} else if err := os.MkdirAll(bin, 0700); err != nil {
				t.Fatal(err)
			}
			startup := filepath.Join(home, ".zshrc")
			switch conflict {
			case "unowned-command":
				if err := os.WriteFile(filepath.Join(bin, "kilo-claude"), []byte("#!/bin/sh\n# Someone else's command\n"), 0700); err != nil {
					t.Fatal(err)
				}
			case "command-link":
				if err := os.Symlink(filepath.Join(outside, "target"), filepath.Join(bin, "kilo-claude")); err != nil {
					t.Skip(err)
				}
			case "startup-link":
				if err := os.Symlink(filepath.Join(outside, "startup"), startup); err != nil {
					t.Skip(err)
				}
			case "malformed-block":
				if err := os.WriteFile(startup, []byte(terminalPathBegin+"\n# no closing marker\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "duplicate-block":
				block := terminalPathBlock("zsh")
				if err := os.WriteFile(startup, append(block, block...), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := installTerminalCommands(home, configDir, binary, "zsh", "linux"); err == nil {
				t.Fatal("unsafe/conflicting installation succeeded")
			}
			if _, err := os.Stat(filepath.Join(bin, "kilo-codex")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("preflight failure wrote a partial command installation")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatal("installer followed a symbolic link")
			}
		})
	}
}

func TestTerminalCommandsInstallHonorsSafeShellConfigurationDirectories(t *testing.T) {
	for _, shell := range []string{"zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			home, configDir, binary := terminalInstallerFixture(t)
			variable := map[string]string{"zsh": "ZDOTDIR", "fish": "XDG_CONFIG_HOME"}[shell]
			t.Setenv(variable, t.TempDir())
			if _, err := installTerminalCommands(home, configDir, binary, shell, "linux"); err == nil {
				t.Fatal("external shell configuration directory accepted")
			}
			t.Setenv(variable, "relative-config")
			if _, err := terminalCommandsStatus(home, configDir, binary, shell, "linux"); err == nil {
				t.Fatal("relative shell configuration directory accepted")
			}
			root := filepath.Join(home, "custom shell configuration")
			t.Setenv(variable, root)
			result, err := installTerminalCommands(home, configDir, binary, shell, "linux")
			if err != nil || len(result.StartupFiles) != 1 || !strings.HasPrefix(result.StartupFiles[0], root+string(filepath.Separator)) {
				t.Fatalf("safe shell root was not used: %+v, %v", result, err)
			}
			if shell == "fish" {
				if !strings.HasSuffix(result.StartupFiles[0], filepath.FromSlash("fish/conf.d/kilo-proxy.fish")) || !bytes.Contains(terminalInstallerRead(t, result.StartupFiles[0]), []byte("set -gx PATH")) {
					t.Fatal("Fish received an invalid startup file")
				}
			}
		})
	}
}

func TestTerminalCommandsPathBlockAddsPathExactlyOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell")
	}
	home := filepath.Join(t.TempDir(), "home with spaces")
	for _, existing := range []string{"/usr/bin:/bin", home + "/.local/bin:/usr/bin:/bin", "/usr/bin:" + home + "/.local/bin:/bin"} {
		command := exec.Command("/bin/sh", "-c", string(terminalPathBlock("bash"))+string(terminalPathBlock("bash"))+"printf '%s' \"$PATH\"")
		command.Env = []string{"HOME=" + home, "PATH=" + existing}
		output, err := command.Output()
		want := existing
		if !strings.Contains(existing, home+"/.local/bin") {
			want = home + "/.local/bin:" + existing
		}
		if err != nil || string(output) != want {
			t.Fatalf("PATH block = %q, %v; want %q", output, err, want)
		}
	}
}

func TestTerminalCommandsInstallRejectsUnsupportedPlatformShellAndBadPaths(t *testing.T) {
	home, configDir, binary := terminalInstallerFixture(t)
	for _, input := range []struct{ home, config, binary, shell, platform string }{
		{home, configDir, binary, "zsh", "windows"},
		{home, configDir, binary, "zsh", "freebsd"},
		{home, configDir, binary, "tcsh", "linux"},
		{home, configDir, binary, "", "linux"},
		{home, "relative-config", binary, "zsh", "linux"},
		{home, configDir, "relative-binary", "zsh", "linux"},
		{home, configDir, binary + "\nignored", "zsh", "linux"},
		{"relative-home", configDir, binary, "zsh", "linux"},
	} {
		if _, err := installTerminalCommands(input.home, input.config, input.binary, input.shell, input.platform); err == nil {
			t.Fatalf("unsupported/unsafe installation accepted: %+v", input)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".local")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected installation made command folders")
	}
}

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const terminalCommandOwner = "# Managed by Kilo Proxy: terminal command v1"
const terminalPathBegin = "# >>> Kilo Proxy terminal commands >>>"
const terminalPathEnd = "# <<< Kilo Proxy terminal commands <<<"
const terminalStartupLimit = 1 << 20

type terminalCommandsInstallResult struct {
	Installed      bool              `json:"installed"`
	Directory      string            `json:"directory"`
	Commands       map[string]string `json:"commands"`
	PathConfigured bool              `json:"pathConfigured"`
	Shell          string            `json:"shell"`
	StartupFiles   []string          `json:"startupFiles,omitempty"`
	Backups        []string          `json:"backups,omitempty"`
	Message        string            `json:"message"`
}

type terminalInstallFile struct {
	path, backup string
	old, data    []byte
	oldMode      os.FileMode
	mode         os.FileMode
	exists       bool
}

func terminalCommandScript(binary, configDir, client string) []byte {
	return []byte("#!/bin/sh\n" + terminalCommandOwner + "\nexec " + helperShellQuote(binary) + " --terminal-agent " + client + " --config-dir " + helperShellQuote(configDir) + " -- \"$@\"\n")
}

func terminalPathBlock(shell string) []byte {
	body := "case \":$PATH:\" in\n  *:\"$HOME/.local/bin\":*) ;;\n  *) export PATH=\"$HOME/.local/bin:$PATH\" ;;\nesac\n"
	if shell == "fish" {
		body = "if not contains -- \"$HOME/.local/bin\" $PATH\n    set -gx PATH \"$HOME/.local/bin\" $PATH\nend\n"
	}
	return []byte(terminalPathBegin + "\n" + body + terminalPathEnd + "\n")
}

// ZDOTDIR and XDG_CONFIG_HOME are honored only inside the supplied home. This
// installer never follows an environment variable into an unrelated directory.
func terminalShellFiles(home, shell string) (string, []string, error) {
	name := filepath.Base(shell)
	switch name {
	case "zsh":
		root := home
		if value := os.Getenv("ZDOTDIR"); value != "" {
			if !terminalPathInside(home, value) {
				return name, nil, errors.New("ZDOTDIR must be an absolute directory inside your home to install terminal commands automatically.")
			}
			root = filepath.Clean(value)
		}
		return name, []string{filepath.Join(root, ".zshrc")}, nil
	case "bash":
		login := filepath.Join(home, ".profile")
		for _, candidate := range []string{".bash_profile", ".bash_login", ".profile"} {
			path := filepath.Join(home, candidate)
			_, err := os.Lstat(path)
			if err == nil {
				login = path
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return name, nil, errors.New("Cannot inspect the Bash login startup file.")
			}
		}
		return name, []string{filepath.Join(home, ".bashrc"), login}, nil
	case "fish":
		root := filepath.Join(home, ".config")
		if value := os.Getenv("XDG_CONFIG_HOME"); value != "" {
			if !terminalPathInside(home, value) {
				return name, nil, errors.New("XDG_CONFIG_HOME must be an absolute directory inside your home to install Fish terminal commands automatically.")
			}
			root = filepath.Clean(value)
		}
		return name, []string{filepath.Join(root, "fish", "conf.d", "kilo-proxy.fish")}, nil
	default:
		return name, nil, errors.New("Automatic terminal command installation supports Zsh, Bash and Fish. Choose one of these as your login shell.")
	}
}

func terminalPathInside(home, path string) bool {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	rel, err := filepath.Rel(home, filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// The home itself may be an OS alias. Every managed ancestor below it must be
// a real directory; a missing suffix is safe to create after all files pass QA.
func terminalInstallAncestors(home, path string) error {
	if !terminalPathInside(home, path) {
		return errors.New("Terminal command files must stay inside your home directory.")
	}
	rel, _ := filepath.Rel(home, filepath.Dir(path))
	current := home
	if rel == "." {
		return nil
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Terminal command folders must be real directories, not symbolic links.")
		}
	}
	return nil
}

func readTerminalInstallFile(home, path string) (terminalInstallFile, error) {
	f := terminalInstallFile{path: path, mode: 0600}
	if err := terminalInstallAncestors(home, path); err != nil {
		return f, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > terminalStartupLimit {
		return f, fmt.Errorf("Cannot safely update %s: use a regular file smaller than 1 MiB, without a symbolic link.", filepath.Base(path))
	}
	f.old, err = os.ReadFile(path)
	if err != nil || len(f.old) > terminalStartupLimit {
		return f, fmt.Errorf("Cannot read %s safely.", filepath.Base(path))
	}
	f.exists, f.oldMode, f.mode = true, info.Mode().Perm(), info.Mode().Perm()
	return f, nil
}

// Only our delimited block is replaced. Everything outside it, including shell
// commands and comments, is preserved as bytes and is never executed here.
func terminalStartupContent(old, block []byte, ownedFile bool) ([]byte, error) {
	begin, end := []byte(terminalPathBegin), []byte(terminalPathEnd)
	start, stop := bytes.Index(old, begin), bytes.Index(old, end)
	if start < 0 && stop < 0 {
		if ownedFile && len(old) != 0 {
			return nil, errors.New("An unrelated kilo-proxy.fish already exists. Move it before installing terminal commands.")
		}
		data := append([]byte(nil), old...)
		if len(data) > 0 {
			if data[len(data)-1] != '\n' {
				data = append(data, '\n')
			}
			data = append(data, '\n')
		}
		return append(data, block...), nil
	}
	lineMarker := func(index int, marker []byte) bool {
		if index < 0 || index > 0 && old[index-1] != '\n' {
			return false
		}
		after := old[index+len(marker):]
		return len(after) == 0 || after[0] == '\n' || after[0] == '\r' && (len(after) == 1 || after[1] == '\n')
	}
	if bytes.Count(old, begin) != 1 || bytes.Count(old, end) != 1 || start >= stop || !lineMarker(start, begin) || !lineMarker(stop, end) {
		return nil, errors.New("The Kilo Proxy PATH block is incomplete or duplicated. Repair that block before installing terminal commands.")
	}
	stop += len(end)
	if stop < len(old) && old[stop] == '\r' {
		stop++
	}
	if stop < len(old) && old[stop] == '\n' {
		stop++
	}
	data := append([]byte(nil), old[:start]...)
	data = append(data, block...)
	return append(data, old[stop:]...), nil
}

func terminalCommandsPlan(home, configDir, binary, shell, platform string) (terminalCommandsInstallResult, []terminalInstallFile, error) {
	result := terminalCommandsInstallResult{Directory: filepath.Join(home, ".local", "bin"), Commands: map[string]string{}}
	if platform != "darwin" && platform != "macos" && platform != "linux" {
		return result, nil, errors.New("Terminal commands are available on macOS and Linux only.")
	}
	for _, value := range []string{home, configDir, binary} {
		if !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
			return result, nil, errors.New("The home, Kilo Proxy configuration and application paths must be absolute paths without line breaks.")
		}
	}
	info, err := os.Stat(home)
	if err != nil || !info.IsDir() {
		return result, nil, errors.New("Cannot locate your home directory.")
	}
	result.Shell, result.StartupFiles, err = terminalShellFiles(home, shell)
	if err != nil {
		return result, nil, err
	}
	files := []terminalInstallFile{}
	result.Installed, result.PathConfigured = true, true
	for _, command := range []struct{ name, client string }{{"kilo-codex", "codex-cli"}, {"kilo-claude", "claude"}} {
		path := filepath.Join(result.Directory, command.name)
		result.Commands[command.name] = path
		f, err := readTerminalInstallFile(home, path)
		if err != nil {
			return result, nil, err
		}
		if f.exists && !bytes.HasPrefix(f.old, []byte("#!/bin/sh\n"+terminalCommandOwner+"\n")) {
			return result, nil, fmt.Errorf("An unrelated %s already exists. Move or rename it before installing terminal commands.", command.name)
		}
		f.mode, f.data = 0700, terminalCommandScript(binary, configDir, command.client)
		if !f.exists || !bytes.Equal(f.old, f.data) || f.oldMode != f.mode {
			result.Installed = false
		}
		files = append(files, f)
	}
	for _, path := range result.StartupFiles {
		f, err := readTerminalInstallFile(home, path)
		if err != nil {
			return result, nil, err
		}
		// Fish uses a dedicated file: even an empty unowned file is not ours.
		if result.Shell == "fish" && f.exists && !bytes.Contains(f.old, []byte(terminalPathBegin)) {
			return result, nil, errors.New("An unrelated kilo-proxy.fish already exists. Move it before installing terminal commands.")
		}
		f.data, err = terminalStartupContent(f.old, terminalPathBlock(result.Shell), result.Shell == "fish")
		if err != nil {
			return result, nil, err
		}
		if !f.exists || !bytes.Equal(f.old, f.data) {
			result.PathConfigured = false
		}
		files = append(files, f)
	}
	if result.Installed && result.PathConfigured {
		result.Message = "kilo-codex and kilo-claude are installed. Open a new terminal to use them; keep Kilo Proxy open."
	} else {
		result.Message = "Install kilo-codex and kilo-claude for your current login shell."
	}
	return result, files, nil
}

func terminalCommandsStatus(home, configDir, binary, shell, platform string) (terminalCommandsInstallResult, error) {
	result, _, err := terminalCommandsPlan(home, configDir, binary, shell, platform)
	return result, err
}

func writeTerminalInstallFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".kilo-terminal-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func backupTerminalInstallFile(f terminalInstallFile) (string, error) {
	backup, err := os.CreateTemp(filepath.Dir(f.path), filepath.Base(f.path)+".kilo-backup-*")
	if err != nil {
		return "", err
	}
	path := backup.Name()
	if _, err = backup.Write(f.old); err == nil {
		err = backup.Sync()
	}
	closeErr := backup.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// Preflight every command and startup file before making changes, back up only
// changed existing files, and restore completed writes on an ordinary IO error.
func installTerminalCommands(home, configDir, binary, shell, platform string) (terminalCommandsInstallResult, error) {
	result, files, err := terminalCommandsPlan(home, configDir, binary, shell, platform)
	if err != nil {
		return result, err
	}
	changed := []terminalInstallFile{}
	rollback := func() bool {
		ok := true
		for i := len(changed) - 1; i >= 0; i-- {
			f := changed[i]
			current, readErr := readTerminalInstallFile(home, f.path)
			if readErr != nil || !current.exists || !bytes.Equal(current.old, f.data) {
				ok = false
				continue
			}
			if f.exists {
				ok = writeTerminalInstallFile(f.path, f.old, f.oldMode) == nil && ok
			} else {
				ok = os.Remove(f.path) == nil && ok
			}
		}
		return ok
	}
	fail := func() (terminalCommandsInstallResult, error) {
		result.Installed, result.PathConfigured = false, false
		if !rollback() {
			return result, errors.New("Could not finish installing terminal commands or restore every changed file. Restore the saved .kilo-backup files before retrying.")
		}
		return result, errors.New("Could not finish installing terminal commands. Existing files were restored; check your home folder permissions and retry.")
	}
	for _, f := range files {
		if f.exists && bytes.Equal(f.old, f.data) && f.oldMode == f.mode {
			continue
		}
		current, err := readTerminalInstallFile(home, f.path)
		if err != nil || current.exists != f.exists || !bytes.Equal(current.old, f.old) || current.oldMode != f.oldMode {
			return fail()
		}
		if err := os.MkdirAll(filepath.Dir(f.path), 0700); err != nil {
			return fail()
		}
		if f.exists {
			f.backup, err = backupTerminalInstallFile(f)
			if err != nil {
				return fail()
			}
			result.Backups = append(result.Backups, f.backup)
		}
		if err := writeTerminalInstallFile(f.path, f.data, f.mode); err != nil {
			return fail()
		}
		changed = append(changed, f)
	}
	result.Installed, result.PathConfigured = true, true
	result.Message = "kilo-codex and kilo-claude are installed. Open a new terminal to use them; keep Kilo Proxy open."
	return result, nil
}

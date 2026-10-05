package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const synaraSupportedVersion = "1.0.0-beta.1"
const synaraMarker = "--kilo-proxy-synara-root="

func synaraVersionSupported(version string) bool {
	return version == synaraSupportedVersion
}

func synaraInstallationCandidates(platform, home, localAppData string) []string {
	candidates := []string{}
	switch platform {
	case "darwin", "macos":
		for _, name := range []string{"Synara Beta.app"} {
			for _, root := range []string{"/Applications", filepath.Join(home, "Applications")} {
				candidates = append(candidates, filepath.Join(root, name))
			}
		}
	case "windows":
		for _, name := range []string{"Synara Beta.exe", "synara-beta.exe"} {
			for _, root := range []string{localAppData, os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
				if !filepath.IsAbs(root) {
					continue
				}
				for _, dir := range []string{"Programs/Synara Beta", "Programs/synara-beta", "Synara Beta", "synara-beta"} {
					candidates = append(candidates, filepath.Join(root, filepath.FromSlash(dir), name))
				}
			}
		}
	default:
		for _, name := range []string{"synara-beta", "Synara Beta"} {
			candidates = append(candidates, filepath.Join("/opt/Synara Beta", name))
		}
		for _, name := range []string{"synara-beta", "synara"} {
			if path := launchLookPath(name); path != "" {
				candidates = append(candidates, path)
			}
		}
		for _, root := range []string{"/opt/synara-beta", "/opt/Synara Beta", filepath.Join(home, ".local", "share", "synara-beta")} {
			for _, name := range []string{"synara-beta", "Synara Beta", "synara"} {
				candidates = append(candidates, filepath.Join(root, name))
			}
		}
	}
	return candidates
}

func resolveSynara(platform, home, localAppData string) (string, error) {
	return resolveSynaraCandidates(synaraInstallationCandidates(platform, home, localAppData))
}

func resolveSynaraCandidates(candidates []string) (string, error) {
	for _, path := range candidates {
		if strings.HasSuffix(path, ".app") {
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				return path, nil
			}
		} else if launchExecutable(path) {
			return path, nil
		}
	}
	return "", errors.New("Install Synara Beta 1.0.0-beta.1, Codex CLI and Claude Code, then refresh installed apps. Extract an AppImage before using it on Linux.")
}

func (a *app) applySynaraLaunch(plan *clientLaunchPlan, rt clientLaunchRuntime) error {
	if rt.platform == "windows" {
		check := a.synaraCheckEnvironment
		if check == nil {
			check = synaraWindowsEnvironmentSafe
		}
		if err := check(); err != nil {
			return err
		}
	}
	saved, err := a.readSynaraPrepared()
	if err != nil || !a.synaraReady(saved, plan.Executable, rt) {
		return errors.New("Prepare Synara again: its private profiles or proxy connection changed.")
	}
	{
		for _, model := range saved.Library.Models {
			if model.ReasoningEffort == "" || !validClaudeEffort(model.ID, model.ReasoningEffort) {
				continue
			}
			claude, err := rt.resolve("claude", "")
			if err != nil || !a.synaraClaudeCapabilities(claude, rt).PerModelEffort {
				return errors.New("Synara requires Claude Code 2.1.251 or newer to apply saved per-model reasoning. Update Claude Code and prepare again.")
			}
			break
		}
	}
	paths := synaraPaths(a.dir)
	check := a.synaraCheckRunning
	if check == nil {
		check = synaraRunning
	}
	running, err := check(paths.Root)
	if err != nil {
		return errors.New("Cannot check whether the Synara Kilo window is running. Close that window and try again.")
	}
	if running || time.Now().Before(a.synaraLaunchUntil) {
		return errors.New("Quit the Synara Kilo window before opening it again. Your regular Synara window can stay open.")
	}
	for _, dir := range []string{paths.UIHome, paths.Data, paths.Electron} {
		if err := safeEditorDir(a.dir, dir); err != nil {
			return err
		}
	}
	plan.Env["SYNARA_BETA_HOME"] = paths.Data
	plan.Env["SYNARA_HOME"] = paths.Data
	plan.Env["SYNARA_DESKTOP_SMOKE_USER_DATA"] = paths.Electron
	plan.Env["HOME"] = paths.UIHome
	plan.Env["SYNARA_DISABLE_AUTO_UPDATE"] = "1"
	plan.Unset = append(plan.Unset, synaraUnsetEnvironment(os.Environ())...)
	plan.Args = []string{synaraMarker + paths.Root}
	if rt.platform == "windows" {
		plan.Env["USERPROFILE"] = paths.UIHome
		plan.Env["APPDATA"] = filepath.Join(paths.UIHome, "AppData", "Roaming")
		plan.Env["LOCALAPPDATA"] = filepath.Join(paths.UIHome, "AppData", "Local")
	} else {
		plan.Env["XDG_CONFIG_HOME"] = filepath.Join(paths.UIHome, ".config")
		plan.Env["XDG_DATA_HOME"] = filepath.Join(paths.UIHome, ".local", "share")
		plan.Env["XDG_CACHE_HOME"] = filepath.Join(paths.UIHome, ".cache")
		plan.Env["XDG_STATE_HOME"] = filepath.Join(paths.UIHome, ".local", "state")
		if rt.platform == "macos" || rt.platform == "darwin" {
			binary := t3CodeBundleExecutable(plan.Executable)
			if binary == "" {
				return errors.New("The Synara desktop application bundle is invalid.")
			}
			plan.Executable = binary
		}
	}
	plan.Directory = paths.UIHome
	return nil
}

func synaraRunning(root string) (bool, error) {
	if !filepath.IsAbs(root) || strings.ContainsAny(root, "\x00\r\n") {
		return false, errors.New("Invalid private Synara workspace.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	running, err := synaraProcessRunning(ctx, root)
	if err != nil || running {
		return running, err
	}
	// Electron passes the primary server's base directory over fd 3, not in
	// argv. Its private runtime record also protects against a server left
	// alive after a desktop crash, without inspecting process environments.
	return synaraServerRuntimeRunning(root)
}

func synaraCommandMatches(command, root string) bool {
	// Reuse the argument boundary parser, replacing only the fixed option name.
	return claudeDesktopProfileArgument(strings.ReplaceAll(command, synaraMarker, "--user-data-dir="), root, "unix")
}

package main

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

const t3CodeMuseProxyID = "kilo_muse_proxy"
const t3CodeMuseKey = "META_API_KEY"

func t3CodeMuseKiloEnabled(prefs t3CodePreferences) bool {
	prefs = normalizeT3CodePreferences(prefs)
	if prefs.MuseConnection != "kilo" {
		return false
	}
	for _, id := range prefs.Providers {
		if id == "muse" {
			return true
		}
	}
	return false
}

func t3CodeMusePaths(root string) (home, settings, auth string) {
	home = filepath.Join(root, "profiles", "muse-kilo")
	settings, auth = museKiloProfilePaths(home)
	return
}

func t3CodeMuseNormalPaths(rt clientLaunchRuntime) []string {
	return museNormalPaths(rt.home, os.Environ(), rt.platform)
}

func planT3CodeMuse(options t3CodeProfileOptions) (map[string]any, []profileFile, error) {
	native, exists := options.AdditionalProviders["muse"].(map[string]any)
	if !exists || !options.MuseKiloSupported {
		return nil, nil, nil
	}
	provider := map[string]any{"driver": "muse", "displayName": "Kilo Proxy · Muse Code", "enabled": false,
		"config": native["config"], "environment": []map[string]any{}, "accentColor": t3CodeProxyAccentColor}
	if options.Preferences == nil || !t3CodeMuseKiloEnabled(*options.Preferences) {
		return provider, nil, nil
	}
	if !packageUpdateAbsolute(options.RootDir) || options.LocalKey == "" {
		return nil, nil, errors.New("Muse Code requires a valid private T3 profile and local proxy key.")
	}
	if options.Policy == nil || len(options.Policy.MuseLibrary.Models) == 0 {
		return nil, nil, errors.New("Choose models before preparing Muse Code with Kilo Proxy.")
	}
	home, settingsPath, authPath := t3CodeMusePaths(options.RootDir)
	if _, err := museKiloIsolation(home, options.NormalMusePaths); err != nil {
		return nil, nil, err
	}
	settings, auth, err := buildMuseKiloProfile(options.Policy.MuseLibrary, options.Catalog,
		"http://127.0.0.1:"+strconv.Itoa(options.Port)+"/t3-code/muse/v1", museProxyToken(options.LocalKey, "t3"))
	if err != nil {
		return nil, nil, err
	}
	// The native Muse driver reads only this account's generated settings and
	// auth.json. No Meta credential enters the T3 provider settings or summary.
	env := museKiloEnvironment(home)
	for _, name := range museKiloInheritedEnvironment(os.Environ()) {
		if name != "MUSE_NO_AUTO_UPDATE" && name != t3CodeMuseKey {
			env[name] = ""
		}
	}
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	environment := []map[string]any{}
	for _, name := range names {
		environment = append(environment, map[string]any{"name": name, "value": env[name], "sensitive": false})
	}
	environment = append(environment, map[string]any{"name": t3CodeMuseKey, "value": museProxyToken(options.LocalKey, "t3"), "sensitive": true})
	provider["environment"], provider["enabled"] = environment, true
	files := []profileFile{}
	for _, item := range []struct {
		path string
		data []byte
	}{{settingsPath, settings}, {authPath, auth}} {
		files = append(files, profileFile{path: item.path, new: item.data})
	}
	return provider, files, nil
}

func addT3CodeMuseProfile(options t3CodeProfileOptions, result *t3CodeProfiles) error {
	provider, files, err := planT3CodeMuse(options)
	if err != nil || provider == nil {
		return err
	}
	result.Providers[t3CodeMuseProxyID] = provider
	if len(files) == 0 {
		return nil
	}
	home, _, _ := t3CodeMusePaths(options.RootDir)
	for _, dir := range museKiloStateDirectories(home) {
		if err := safeEditorDir(options.RootDir, dir); err != nil {
			return err
		}
	}
	for _, planned := range files {
		file, err := prepareMusePrivateFile(planned.path, planned.new)
		if err != nil {
			return err
		}
		result.Files = append(result.Files, file)
	}
	return nil
}

func validateT3CodeMuseManifest(saved t3CodePrepared, paths t3CodeManagedPaths) error {
	enabled := t3CodeMuseKiloEnabled(saved.Preferences)
	_, settings, auth := t3CodeMusePaths(paths.Root)
	secret := filepath.Join(paths.Secrets, t3CodeSecretName(t3CodeMuseProxyID, t3CodeMuseKey))
	for _, path := range []string{settings, auth, secret} {
		relative, err := filepath.Rel(paths.Root, path)
		if err != nil {
			return err
		}
		digest, exists := saved.Files[relative]
		if enabled && (!exists || !managedToolCatalogSHA256.MatchString(digest)) || !enabled && exists {
			return errors.New("Prepare the selected T3 Muse Code account again.")
		}
	}
	if enabled && saved.MuseBinary.Path == "" {
		return errors.New("Prepare Muse Code again with a verified executable.")
	}
	if !enabled && (saved.MuseRelease != "" || saved.MuseBinary != (museBinaryIdentity{}) || len(saved.MuseLibrary.Models) != 0) {
		return errors.New("The T3 Muse Code profile includes an unselected Kilo account.")
	}
	selected := false
	for _, id := range saved.Preferences.Providers {
		selected = selected || id == "muse"
	}
	if native, ok := saved.Providers["muse"].(map[string]any); ok {
		kilo, hasKilo := saved.Providers[t3CodeMuseProxyID].(map[string]any)
		if native["driver"] != "muse" || native["enabled"] != (selected && !enabled) || enabled && !hasKilo || hasKilo && (kilo["driver"] != "muse" || kilo["enabled"] != enabled) {
			return errors.New("Invalid private T3 Muse Code account selection.")
		}
	} else if enabled || saved.Providers[t3CodeMuseProxyID] != nil {
		return errors.New("The selected T3 Muse Code driver is unavailable.")
	}
	return nil
}

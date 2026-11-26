package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Optional accounts are native T3 drivers. Their models and authentication are
// discovered by T3 itself; no Kilo gateway credentials are assigned to them.
func planT3CodeOptionalProviders(prefs t3CodePreferences, installation t3CodeInstallation, rt clientLaunchRuntime, privateRoot string, verifyVersions ...bool) (map[string]any, error) {
	prefs = normalizeT3CodePreferences(prefs)
	if err := validateT3CodePreferences(prefs); err != nil {
		return nil, err
	}
	supported := map[string]bool{}
	for _, provider := range installation.SupportedProviders {
		supported[provider.ID] = true
	}
	selected := map[string]bool{}
	for _, id := range prefs.Providers {
		if !supported[id] {
			return nil, errors.New("This T3 Code installation does not include the selected " + id + " provider. Update T3 Code before preparing it.")
		}
		selected[id] = true
	}
	result := map[string]any{}
	if selected[t3CodeKiloACPOption] {
		id, provider, err := planT3CodeKiloACP(privateRoot)
		if err != nil {
			return nil, err
		}
		result[id] = provider
	}
	for _, provider := range t3CodeOptionalProviders {
		if !supported[provider.ID] {
			continue
		}
		config := map[string]any{"customModels": []any{}}
		switch provider.ID {
		case "grok", "muse", "pi":
			config["binaryPath"] = provider.ID
		case "opencode":
			config["binaryPath"], config["serverUrl"], config["serverPassword"] = "opencode", "", ""
		}
		environment := t3CodeOptionalProviderEnvironment(rt.home, os.Environ(), rt.platform)
		if selected[provider.ID] && provider.ID == "muse" {
			if t3CodeMuseKiloEnabled(prefs) && !installation.MuseKiloSupported {
				return nil, errors.New("This T3 Code installation cannot isolate the Muse Code Kilo account. Update T3 Code or choose its native Meta account.")
			}
			if rt.resolve == nil {
				return nil, errors.New("Cannot resolve the selected Muse Code executable.")
			}
			binary, err := rt.resolve("muse", "")
			if err != nil || !packageUpdateAbsolute(binary) || strings.ContainsAny(binary, "\x00\r\n") {
				return nil, errors.New("Install Muse Code or choose its executable in Kilo Settings before enabling its T3 Code account.")
			}
			// Keep Muse on its native driver and normal login. T3 probes this
			// exact executable over MSP and discovers its models and reasoning
			// choices; neither readiness nor preparation starts a Muse session.
			config["binaryPath"] = binary
		}
		if selected[provider.ID] && provider.ID == "opencode" {
			if t3CodeOpenCodeKiloEnabled(prefs) && !installation.OpenCodeKiloSupported {
				return nil, errors.New("This T3 Code installation cannot isolate the OpenCode Kilo account. Update T3 Code or choose its native account.")
			}
			client, major, minimum := "opencode", "1.", "1.14.19"
			if prefs.OpenCode2 {
				if !installation.OpenCode2Supported {
					return nil, errors.New("This T3 Code installation does not include the OpenCode 2 runtime. Update T3 Code first.")
				}
				if !packageUpdateAbsolute(privateRoot) || strings.ContainsAny(privateRoot, "\x00\r\n") {
					return nil, errors.New("Cannot resolve a separate OpenCode 2 profile.")
				}
				client, major, minimum = "opencode2", "2.", "2.0.18"
				// OpenCode 2 converts a 1.x database in place. Keep its entire HOME
				// and XDG state separate, rather than migrating the normal account.
				environment = t3CodeOptionalProviderEnvironment(filepath.Join(privateRoot, "profiles", "opencode2"), nil, rt.platform)
			}
			if rt.resolve == nil {
				return nil, errors.New("Cannot resolve the selected OpenCode executable.")
			}
			binary, err := rt.resolve(client, "")
			if err != nil {
				return nil, errors.New("Install OpenCode " + minimum + " or newer for the selected T3 Code account.")
			}
			// Readiness/fingerprint checks resolve identity only. Preparation and
			// launch verify the actual executable in disposable state; an API
			// request must not spawn a process to recompute its fingerprint.
			if len(verifyVersions) == 0 || verifyVersions[0] {
				version, err := managedToolProbeVersion(context.Background(), binary)
				if err != nil || !strings.HasPrefix(version, major) || strings.ContainsAny(version, "-+") || managedToolCompareNumericVersion(version, minimum) < 0 {
					return nil, errors.New("The selected T3 Code account requires OpenCode " + minimum + " or newer within major version " + major[:1] + ".")
				}
			}
			config["binaryPath"] = binary
		}
		// Writing disabled slots is deliberate: absent slots are synthesized by
		// upstream, so omission alone would not enforce the user's opt-in choice.
		enabled := selected[provider.ID] && !(provider.ID == "opencode" && t3CodeOpenCodeKiloEnabled(prefs)) && !(provider.ID == "muse" && t3CodeMuseKiloEnabled(prefs))
		result[provider.ID] = map[string]any{"driver": provider.ID, "displayName": provider.Label, "enabled": enabled, "config": config, "environment": environment}
	}
	return result, nil
}

func t3CodeOptionalProviderEnvironment(home string, parent []string, platform string) []map[string]any {
	normal := t3CodeNormalEnvironment(home, parent, platform)
	environment := []map[string]any{}
	for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		if value := normal[key]; value != "" && !strings.ContainsAny(value, "\x00\r\n") {
			environment = append(environment, map[string]any{"name": key, "value": value, "sensitive": false})
		}
	}
	return environment
}

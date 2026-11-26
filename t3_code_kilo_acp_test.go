package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

const t3CodeKiloACPContractFixture = `const DRIVER_KIND$1 = ProviderDriverKind.make("acpRegistry");
const AcpRegistryDriver = {
  driverKind: DRIVER_KIND$1,
  metadata: { supportsMultipleInstances: true, hasDefaultInstance: false },
  configSchema: AcpRegistrySettings,
  defaultConfig: () => decodeSettings({}),
  create: fn("AcpRegistryDriver.create")(function* () {})
};
const BUILT_IN_DRIVERS = [
  AcpRegistryDriver
];`

func TestT3CodeAdvertisesKiloACPOnlyWithRegistryDriver(t *testing.T) {
	const source = t3CodeKiloACPContractFixture
	supported, _ := t3CodeOptionalProviderCapabilities(source)
	if !reflect.DeepEqual(supported, []t3CodeProviderSupport{t3CodeKiloACPSupport}) {
		t.Fatalf("ACP capability missing: %v", supported)
	}
	for _, change := range [][2]string{
		{"  AcpRegistryDriver\n", "  OtherDriver\n"},
		{`make("acpRegistry")`, `make("localOnly")`},
		{"supportsMultipleInstances: true", "supportsMultipleInstances: false"},
		{"configSchema: AcpRegistrySettings", "configSchema: OtherSettings"},
		{"create: fn", "placeholder: fn"},
	} {
		supported, _ = t3CodeOptionalProviderCapabilities(strings.Replace(source, change[0], change[1], 1))
		if len(supported) != 0 {
			t.Fatalf("incomplete registry driver advertised ACP: %v", change)
		}
	}
}

func TestT3CodeKiloACPPreparesAndLaunchesWithoutProxy(t *testing.T) {
	for _, name := range []string{"generated", "existing"} {
		t.Run(name, func(t *testing.T) {
			a := t3CodeTestApp(t, runtime.GOOS)
			a.apiKey = ""
			a.config.T3Code = normalizeT3CodePreferences(t3CodePreferences{
				Version: 1, CodexModels: "official", ClaudeModels: "official",
				Providers: []string{t3CodeKiloACPOption},
			})
			binary := mustResolveT3CodeTest(t, a)
			archive, err := t3CodeOpenPackage(binary, runtime.GOOS)
			if err != nil {
				t.Fatal(err)
			}
			archivePath := archive.file.Name()
			archive.file.Close()
			files := t3CodeCacheFixtureFiles(t, archivePath)
			server := "apps/server/dist/bin.mjs"
			files[server] = append(files[server], []byte("\n"+t3CodeKiloACPContractFixture)...)
			writeT3CodeFixtureASAR(t, archivePath, files)

			instanceID := t3CodeKiloACPInstanceID
			if name == "existing" {
				instanceID = "existing-kilo-acp"
				path := t3CodePaths(a.dir).Settings
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				data := []byte(`{"providerInstances":{"existing-kilo-acp":{"driver":"acpRegistry","config":{"agentId":"kilo","distribution":"auto"},"environment":[]}}}`)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			a.launcher.resolve = func(id, _ string) (string, error) {
				if id != "t3-code" {
					t.Fatalf("unselected provider discovery: %s", id)
				}
				return binary, nil
			}
			prepareT3CodeFixture(t, a)
			saved := mustReadT3CodePrepared(t, a)
			if !t3CodeIsKiloACP(saved.Providers[instanceID]) || len(saved.Files) != 0 {
				t.Fatal("ACP preparation did not keep the native account or generated proxy files")
			}
			plan, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime())
			if err != nil || !plan.skipProxy {
				t.Fatal("ACP-only T3 launch should not require Kilo Proxy", err)
			}
			t.Cleanup(func() {
				if a.t3CodeAssets != nil {
					a.t3CodeAssets.Close()
				}
			})
			path := t3CodePaths(a.dir).Settings
			raw, err := os.ReadFile(path)
			var document map[string]any
			if err != nil || json.Unmarshal(raw, &document) != nil {
				t.Fatal("invalid prepared settings", err)
			}
			entry := document["providerInstances"].(map[string]any)[instanceID].(map[string]any)
			entry["config"].(map[string]any)["source"] = "local"
			if err := os.WriteFile(path, mustMarshalACPTest(t, document), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime()); err == nil {
				t.Fatal("the prepared registry selection accepted a local-command replacement")
			}
		})
	}
}

func TestT3CodeKiloACPPlansNativeAccountAndPreservesManualRegistration(t *testing.T) {
	prefs := t3CodePreferences{Version: 1, CodexModels: "official", ClaudeModels: "official"}
	installation := t3CodeInstallation{SupportedProviders: []t3CodeProviderSupport{t3CodeKiloACPSupport}}
	rt := clientLaunchRuntime{home: t.TempDir(), platform: "linux"}
	paths := t3CodePaths(rt.home)
	writeJSON := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	plan := func(prefs t3CodePreferences) map[string]any {
		t.Helper()
		providers, err := planT3CodeOptionalProviders(prefs, installation, rt, paths.Root, false)
		if err != nil {
			t.Fatal(err)
		}
		return providers
	}
	settings := func(providers map[string]any) map[string]any {
		t.Helper()
		files, err := planT3CodeSettings(paths, providers)
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			Providers map[string]any `json:"providerInstances"`
		}
		if len(files) != 1 || files[0].path != paths.Settings || json.Unmarshal(files[0].new, &document) != nil {
			t.Fatalf("ACP should produce one private settings file and no credentials: %d files", len(files))
		}
		return document.Providers
	}

	if got := settings(plan(prefs)); len(got) != 0 {
		t.Fatal("ACP registration must be opt-in")
	}
	selected := prefs
	selected.Providers = []string{t3CodeKiloACPOption}
	if t3CodeUsesKilo(selected) || t3CodeNeedsSavedLibrary(selected) {
		t.Fatal("native Kilo Code ACP should not depend on the Kilo Proxy connection or models")
	}
	if _, err := planT3CodeOptionalProviders(selected, t3CodeInstallation{}, rt, paths.Root, false); err == nil {
		t.Fatal("an installation with no registry driver accepted ACP")
	}
	planned := plan(selected)
	created := settings(planned)
	account, ok := created[t3CodeKiloACPInstanceID].(map[string]any)
	if len(created) != 1 || !ok || !t3CodeSubset(planned[t3CodeKiloACPInstanceID], account) ||
		!reflect.DeepEqual(account["environment"], []any{}) ||
		!reflect.DeepEqual(account["config"], map[string]any{"source": "registry", "agentId": "kilo", "distribution": "auto"}) {
		t.Fatal("new registration did not use the native registry defaults")
	}
	manual := map[string]any{
		"driver": "acpRegistry", "displayName": "My Kilo", "enabled": true,
		"config":      map[string]any{"agentId": "kilo", "distribution": "auto", "authMethodId": "existing-acp-auth"},
		"environment": []any{map[string]any{"name": "TEST_EXISTING_OVERRIDE", "value": "existing"}},
	}
	writeJSON(paths.Settings, map[string]any{"providerInstances": map[string]any{"manually-added-kilo": manual}})
	reused := plan(selected)
	if len(reused) != 1 || reused["manually-added-kilo"] == nil || strings.Contains(string(mustMarshalACPTest(t, reused)), "existing") {
		t.Fatal("the existing active instance was not reused without copying its settings")
	}
	if got := settings(reused); len(got) != 1 || !reflect.DeepEqual(got["manually-added-kilo"], manual) {
		t.Fatal("preparation did not preserve the manual instance")
	}
	created["manually-added-kilo"] = manual
	writeJSON(paths.Settings, map[string]any{"providerInstances": created})
	writeJSON(paths.Selection, map[string]any{"providers": planned})
	if got := settings(plan(selected)); !reflect.DeepEqual(got, created) {
		t.Fatal("preparation replaced a previously prepared instance or a manual account")
	}
	if got := settings(plan(prefs)); len(got) != 1 || !reflect.DeepEqual(got["manually-added-kilo"], manual) {
		t.Fatal("opt-out did not retire only the previously prepared ACP slot")
	}
}

func mustMarshalACPTest(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

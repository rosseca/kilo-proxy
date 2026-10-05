package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func syntheticT3CodeExecutable(t *testing.T, root, platform string, versions ...string) string {
	t.Helper()
	version := t3CodeSupportedVersion
	if len(versions) > 0 {
		version = versions[0]
	}
	return syntheticT3CodeVersionExecutable(t, root, platform, version)
}

func syntheticT3CodeVersionExecutable(t *testing.T, root, platform, version string) string {
	t.Helper()
	appName := "T3 Code"
	if strings.Contains(version, "-nightly.") {
		appName += " (Nightly)"
	}
	appRoot := filepath.Join(root, "T3 Code fixture")
	executable := filepath.Join(appRoot, "t3code")
	resources := filepath.Join(appRoot, "resources")
	if platform == "windows" {
		executable = filepath.Join(appRoot, appName+".exe")
	}
	if platform == "macos" || platform == "darwin" {
		appRoot = filepath.Join(root, appName+".app")
		executable = filepath.Join(appRoot, "Contents", "MacOS", appName)
		resources = filepath.Join(appRoot, "Contents", "Resources")
	}
	for _, dir := range []string{filepath.Dir(executable), resources} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(executable, []byte("synthetic native executable; never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	packageData, err := json.Marshal(map[string]string{"name": "t3code", "version": version})
	if err != nil {
		t.Fatal(err)
	}
	header, _ := json.Marshal(t3CodeASAREntry{Files: map[string]t3CodeASAREntry{"package.json": {Size: int64(len(packageData)), Offset: "0"}}})
	prefix := make([]byte, 16)
	binary.LittleEndian.PutUint32(prefix[:4], 4)
	binary.LittleEndian.PutUint32(prefix[4:8], uint32(len(header)+8))
	binary.LittleEndian.PutUint32(prefix[8:12], uint32(len(header)+4))
	binary.LittleEndian.PutUint32(prefix[12:16], uint32(len(header)))
	archive := append(append(prefix, header...), packageData...)
	if err := os.WriteFile(filepath.Join(resources, "app.asar"), archive, 0600); err != nil {
		t.Fatal(err)
	}
	if platform == "macos" || platform == "darwin" {
		if err := os.WriteFile(filepath.Join(appRoot, "Contents", "Info.plist"), []byte(`<plist><dict><key>CFBundleExecutable</key><string>`+appName+`</string></dict></plist>`), 0600); err != nil {
			t.Fatal(err)
		}
		return appRoot
	}
	return executable
}

func t3CodeTestApp(t *testing.T, platform string, versions ...string) *app {
	t.Helper()
	a := launchTestApp(t)
	t.Setenv("CODEX_HOME", filepath.Join(a.launcher.home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(a.launcher.home, ".claude"))
	a.config.Port = 9988
	a.config.Language = "en"
	binary := syntheticT3CodeExecutable(t, a.launcher.home, platform, versions...)
	cli := filepath.Join(a.launcher.home, "codex-native")
	claude := filepath.Join(a.launcher.home, "claude-native")
	for _, path := range []string{cli, claude} {
		if err := os.WriteFile(path, []byte("synthetic native CLI"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	a.launcher.platform = platform
	a.launcher.resolve = func(id, custom string) (string, error) {
		switch id {
		case "t3-code":
			return binary, nil
		case "codex-cli":
			return cli, nil
		case "claude":
			return claude, nil
		}
		return "", errors.New("not installed")
	}
	a.t3CodeCheckRunning = func(string) (bool, error) { return false, nil }
	if _, err := a.modelLibrary.save(testModelLibrary(), 0, false); err != nil {
		t.Fatal(err)
	}
	return a
}

func prepareT3CodeFixture(t *testing.T, a *app) {
	t.Helper()
	w := adminRequest(a, "clients/t3-code", `{}`)
	if w.Code != 200 {
		t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
	}
}

func TestT3CodePreparesFourAgentsWithRedactedLocalSecrets(t *testing.T) {
	a := t3CodeTestApp(t, "macos")
	paths := t3CodePaths(a.dir)
	prepareT3CodeFixture(t, a)
	data, err := os.ReadFile(paths.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(a.config.LocalKey)) {
		t.Fatal("settings leaked local key")
	}
	var settings map[string]any
	if json.Unmarshal(data, &settings) != nil {
		t.Fatal("invalid settings")
	}
	providers := settings["providerInstances"].(map[string]any)
	if len(providers) != 4 {
		t.Fatal("expected four separate agents", providers)
	}
	if initial := settings["defaultModelSelection"].(map[string]any); initial["instanceId"] != t3CodeCodexProxyID || initial["model"] != testModelLibrary().DefaultModel {
		t.Fatal("initial model is not the shared default", initial)
	}
	for _, id := range []string{"codex", "claudeAgent"} {
		if settings["providers"].(map[string]any)[id].(map[string]any)["enabled"] != false {
			t.Fatal("legacy agent enabled in new workspace", id)
		}
	}
	for _, target := range []struct{ id, name string }{{t3CodeCodexProxyID, "KILO_LOCAL_API_KEY"}, {t3CodeClaudeProxyID, "ANTHROPIC_AUTH_TOKEN"}} {
		path := filepath.Join(paths.Secrets, t3CodeSecretName(target.id, target.name))
		key, err := os.ReadFile(path)
		if err != nil || string(key) != a.config.LocalKey {
			t.Fatal("invalid local secret", err)
		}
		if os.PathSeparator != '\\' {
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0600 {
				t.Fatal("secret permissions", info.Mode())
			}
		}
	}
	w := adminRequest(a, "clients/t3-code", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"prepared":true`) || strings.Contains(w.Body.String(), a.config.LocalKey) {
		t.Fatalf("metadata: %d %s", w.Code, w.Body.String())
	}
	plan, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Env["HOME"] != paths.UIHome || plan.Env["T3CODE_HOME"] != paths.Data || plan.Env["KILO_LOCAL_API_KEY"] != "" || plan.Env["ANTHROPIC_AUTH_TOKEN"] != "" || len(plan.Args) != 1 || plan.Args[0] != t3CodeMarker+paths.Root {
		t.Fatal("unsafe T3 environment", plan)
	}
}

func TestT3CodeRuntimeDefaultsDoNotInvalidateManagedProfiles(t *testing.T) {
	a := t3CodeTestApp(t, "linux")
	prepareT3CodeFixture(t, a)
	paths := t3CodePaths(a.dir)
	data, _ := os.ReadFile(paths.Settings)
	var settings map[string]any
	_ = json.Unmarshal(data, &settings)
	settings["theme"] = "dark"
	providers := settings["providerInstances"].(map[string]any)
	providers[t3CodeCodexNormalID].(map[string]any)["accentColor"] = "#123456"
	providers[t3CodeCodexProxyID].(map[string]any)["environment"].([]any)[0].(map[string]any)["valueRedacted"] = false
	settings["defaultModelSelection"] = map[string]any{"instanceId": t3CodeClaudeNormalID, "model": "claude-sonnet-4-6"}
	data, _ = json.Marshal(settings)
	if err := os.WriteFile(paths.Settings, data, 0600); err != nil {
		t.Fatal(err)
	}
	w := adminRequest(a, "clients/t3-code", "")
	if !strings.Contains(w.Body.String(), `"prepared":true`) {
		t.Fatal("unrelated T3 runtime state invalidated profile", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "Prepare T3 Code to add") {
		t.Fatal("ready metadata still requests preparation", w.Body.String())
	}
	providers[t3CodeCodexProxyID].(map[string]any)["config"].(map[string]any)["homePath"] = a.launcher.home
	data, _ = json.Marshal(settings)
	_ = os.WriteFile(paths.Settings, data, 0600)
	w = adminRequest(a, "clients/t3-code", "")
	if !strings.Contains(w.Body.String(), `"prepared":false`) {
		t.Fatal("managed provider mutation accepted", w.Body.String())
	}
}

func TestT3CodeProxyBadgeMustBePreparedBeforeLaunch(t *testing.T) {
	for _, id := range []string{t3CodeCodexProxyID, t3CodeClaudeProxyID} {
		for _, field := range []string{"displayName", "accentColor"} {
			t.Run(id+"/"+field, func(t *testing.T) {
				a := t3CodeTestApp(t, "linux")
				prepareT3CodeFixture(t, a)
				paths := t3CodePaths(a.dir)
				data, err := os.ReadFile(paths.Settings)
				if err != nil {
					t.Fatal(err)
				}
				var settings map[string]any
				if err := json.Unmarshal(data, &settings); err != nil {
					t.Fatal(err)
				}
				provider := settings["providerInstances"].(map[string]any)[id].(map[string]any)
				if field == "displayName" {
					provider[field] = "Normal-looking agent"
				} else {
					delete(provider, field)
				}
				data, err = json.Marshal(settings)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(paths.Settings, data, 0600); err != nil {
					t.Fatal(err)
				}
				w := adminRequest(a, "clients/t3-code", "")
				if !strings.Contains(w.Body.String(), `"prepared":false`) {
					t.Fatal("missing proxy badge remained prepared", w.Body.String())
				}
				if _, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime()); err == nil {
					t.Fatal("launch accepted a missing proxy badge")
				}
				prepareT3CodeFixture(t, a)
				if _, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime()); err != nil {
					t.Fatal("preparation did not restore launchable proxy badges", err)
				}
			})
		}
	}
}

func TestT3CodeClearsInheritedLaunchOverridesAndRestoresDefaultPaths(t *testing.T) {
	parent := []string{"T3CODE_CODEX_LAUNCH_ARGS=-c model_provider=other", "t3code_custom_dev_override=unsafe", "T3CODE_DESKTOP_LAN_HOST=0.0.0.0", "ANTHROPIC_AUTH_TOKEN=normal-token", "VITE_DEV_SERVER_URL=http://127.0.0.1:9999", "PATH=/bin"}
	env := clientChildEnvironment(parent, map[string]string{"T3CODE_HOME": "/private/data"}, t3CodeUnsetEnvironment(parent), "windows")
	joined := strings.Join(env, "\n")
	for _, forbidden := range []string{"LAUNCH_ARGS", "custom_dev_override", "LAN_HOST", "normal-token", "VITE_DEV_SERVER_URL"} {
		if strings.Contains(joined, forbidden) {
			t.Fatal("inherited launch override survived", forbidden)
		}
	}
	if !strings.Contains(joined, "PATH=/bin") || !strings.Contains(joined, "T3CODE_HOME=/private/data") {
		t.Fatal("safe workspace environment lost", joined)
	}
	home := t.TempDir()
	for _, platform := range []string{"linux", "macos", "windows"} {
		env := t3CodeNormalEnvironment(home, nil, platform)
		if platform == "windows" {
			if env["USERPROFILE"] != home || env["APPDATA"] != filepath.Join(home, "AppData", "Roaming") || env["LOCALAPPDATA"] != filepath.Join(home, "AppData", "Local") {
				t.Fatal("normal Windows defaults missing", env)
			}
		} else {
			if env["XDG_CONFIG_HOME"] != filepath.Join(home, ".config") || env["XDG_DATA_HOME"] != filepath.Join(home, ".local", "share") || env["XDG_CACHE_HOME"] != filepath.Join(home, ".cache") || env["XDG_STATE_HOME"] != filepath.Join(home, ".local", "state") || env["XDG_RUNTIME_DIR"] != "" {
				t.Fatal("normal XDG defaults missing", env)
			}
		}
	}
}

func TestT3CodeRuntimeRecordProtectsAnOrphanServer(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "data", "userdata")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "server-runtime.json")
	if running, err := t3CodeRuntimeRunning(root); err != nil || running {
		t.Fatal("missing runtime record blocked setup", running, err)
	}
	write := func(pid int) {
		data, _ := json.Marshal(map[string]any{"version": 1, "pid": pid, "startedAt": time.Now().UTC().Format(time.RFC3339Nano), "port": 3773})
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(os.Getpid())
	if running, err := t3CodeRuntimeRunning(root); err != nil || !running {
		t.Fatal("live runtime PID did not guard setup", running, err)
	}
	write(1<<30 + 12345)
	if running, err := t3CodeRuntimeRunning(root); err != nil || running {
		t.Fatal("dead stale runtime PID blocked setup", running, err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"pid":-1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := t3CodeRuntimeRunning(root); err == nil {
		t.Fatal("invalid runtime record accepted")
	}
}

func TestT3CodeClaudeProbeUsesTheResolvedExecutableInPrivateHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX version fixture; native Windows launch is covered by CI")
	}
	a := t3CodeTestApp(t, runtime.GOOS)
	paths := t3CodePaths(a.dir)
	if err := safeEditorDir(a.dir, paths.UIHome); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KILO_VERSION_EXPECTED_HOME", paths.UIHome)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "must-not-be-in-probe")
	t.Setenv("T3CODE_CODEX_LAUNCH_ARGS", "must-not-be-in-probe")
	binary := filepath.Join(t.TempDir(), "selected-claude")
	data := []byte("#!/bin/sh\n[ \"$HOME\" = \"$KILO_VERSION_EXPECTED_HOME\" ] || exit 2\n[ -z \"$ANTHROPIC_AUTH_TOKEN$T3CODE_CODEX_LAUNCH_ARGS\" ] || exit 3\nprintf '2.1.251 (Claude Code)\\n'\n")
	if err := os.WriteFile(binary, data, 0700); err != nil {
		t.Fatal(err)
	}
	caps := a.t3CodeClaudeCapabilities(binary, a.launchRuntime())
	if caps.Version != "2.1.251" || !caps.PerModelEffort {
		t.Fatal("capabilities did not come from the selected private probe", caps)
	}
}

func TestT3CodeNightlyLaunchRejectsInPlaceClaudeDowngrade(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable version fixture; profile capability guards are covered on every platform")
	}
	a := t3CodeTestApp(t, runtime.GOOS, t3CodeNightlyVersion)
	paths := t3CodePaths(a.dir)
	claude, err := a.launchRuntime().resolve("claude", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KILO_VERSION_EXPECTED_HOME", paths.UIHome)
	writeVersion := func(version string) {
		t.Helper()
		script := "#!/bin/sh\n[ \"$1\" = --version ] || exit 2\n[ \"$HOME\" = \"$KILO_VERSION_EXPECTED_HOME\" ] || exit 3\nprintf '" + version + " (Claude Code)\\n'\n"
		if err := os.WriteFile(claude, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeVersion("2.1.251")
	library := modelLibrary{SchemaVersion: 1, DefaultModel: "anthropic/claude-opus-4-6", Models: []modelLibraryItem{
		{ID: "anthropic/claude-opus-4-6", ReasoningEffort: "low", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, ContextWindow: 200000, MaxOutputTokens: 4096},
	}}
	if _, err := a.modelLibrary.save(library, a.modelLibrary.snapshot().Revision, false); err != nil {
		t.Fatal(err)
	}
	prepareT3CodeFixture(t, a)
	if _, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime()); err != nil {
		t.Fatal("supported CLI could not launch its prepared nightly profile", err)
	}
	saved, err := a.readT3CodePrepared()
	if err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for relative := range saved.Files {
		path := filepath.Join(paths.Root, relative)
		before[path], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{paths.Selection, paths.Settings} {
		before[path], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	starts := 0
	a.launcher.start = func(clientLaunchPlan) error { starts++; return nil }
	writeVersion("2.1.250")
	if !a.t3CodeReady(saved, mustResolveT3CodeTest(t, a), a.launchRuntime()) {
		t.Fatal("fixture did not preserve the executable path and prepared fingerprint")
	}
	response := adminRequest(a, "clients/launch", `{"client":"t3-code"}`)
	if response.Code != 409 || !strings.Contains(response.Body.String(), "Claude Code 2.1.251 or newer") || !strings.Contains(response.Body.String(), "Update Claude Code and prepare again") {
		t.Fatal("CLI downgrade did not produce an actionable launch error", response.Code, response.Body.String())
	}
	if starts != 0 || a.proxyListener != nil || !a.t3CodeLaunchUntil.IsZero() {
		t.Fatal("rejected downgrade started a process, listener or launch lease")
	}
	for path, expected := range before {
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatal("rejected downgrade changed prepared data", path, err)
		}
	}
	writeVersion("2.1.276")
	if _, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime()); err != nil {
		t.Fatal("compatible in-place CLI update required unnecessary repreparation", err)
	}
	if starts != 0 || a.proxyListener != nil {
		t.Fatal("planning a compatible update unexpectedly dispatched a process")
	}
}

func TestT3CodeGuardsLiveChangesAndStaleConnection(t *testing.T) {
	a := t3CodeTestApp(t, "windows")
	prepareT3CodeFixture(t, a)
	a.t3CodeCheckRunning = func(string) (bool, error) { return true, nil }
	w := adminRequest(a, "clients/t3-code", `{}`)
	if w.Code != 200 {
		t.Fatal("idempotent setup should work", w.Code, w.Body.String())
	}
	a.config.LocalKey += "-rotated"
	w = adminRequest(a, "clients/t3-code", `{}`)
	if w.Code != 409 {
		t.Fatal("live changed setup accepted", w.Code, w.Body.String())
	}
	if _, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime()); err == nil {
		t.Fatal("stale connection launched")
	}
	data, _ := os.ReadFile(t3CodePaths(a.dir).Settings)
	if bytes.Contains(data, []byte(a.config.LocalKey)) {
		t.Fatal("failed setup leaked new secret")
	}
}

func TestT3CodeNormalCustomHomesAreCapturedAndInvalidateOldPreparation(t *testing.T) {
	a := t3CodeTestApp(t, "linux")
	codexHome := filepath.Join(a.launcher.home, "existing-codex-home")
	claudeHome := filepath.Join(a.launcher.home, "existing-claude-home")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	prepareT3CodeFixture(t, a)
	saved, err := a.readT3CodePrepared()
	if err != nil || !a.t3CodeReady(saved, mustResolveT3CodeTest(t, a), a.launchRuntime()) {
		t.Fatal("initial custom homes not ready", err)
	}
	for id, expected := range map[string]string{t3CodeCodexNormalID: codexHome, t3CodeClaudeNormalID: claudeHome} {
		if saved.Providers[id].(map[string]any)["config"].(map[string]any)["homePath"] != expected {
			t.Fatal("normal custom home ignored", id)
		}
	}
	t.Setenv("CODEX_HOME", filepath.Join(a.launcher.home, "changed-codex-home"))
	if a.t3CodeReady(saved, mustResolveT3CodeTest(t, a), a.launchRuntime()) {
		t.Fatal("normal home change did not invalidate preparation")
	}
	if _, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime()); err == nil {
		t.Fatal("old normal home session still launched")
	}
}

func TestT3CodeNormalHomeAliasRetargetInvalidatesPreparedSnapshot(t *testing.T) {
	a := t3CodeTestApp(t, "linux")
	target := filepath.Join(a.launcher.home, "normal-codex")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(a.launcher.home, "codex-home-alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Skip(err)
	}
	t.Setenv("CODEX_HOME", alias)
	prepareT3CodeFixture(t, a)
	saved, err := a.readT3CodePrepared()
	if err != nil || !a.t3CodeReady(saved, mustResolveT3CodeTest(t, a), a.launchRuntime()) {
		t.Fatal("safe normal alias was not prepared", err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t3CodePaths(a.dir).Root, "profiles", "codex-kilo"), alias); err != nil {
		t.Fatal(err)
	}
	if a.t3CodeReady(saved, mustResolveT3CodeTest(t, a), a.launchRuntime()) {
		t.Fatal("retargeted normal alias remained ready")
	}
	if _, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime()); err == nil {
		t.Fatal("retargeted normal alias launched an old snapshot")
	}
	if w := adminRequest(a, "clients/t3-code", `{}`); w.Code != 409 {
		t.Fatal("preparation accepted a normal home alias into the Kilo profile", w.Code)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(alias, alias); err != nil {
		t.Fatal(err)
	}
	if fingerprint := a.t3CodeFingerprint(mustResolveT3CodeTest(t, a), t3CodeSupportedVersion, saved.Library, a.launchRuntime()); fingerprint != "" {
		t.Fatal("unresolvable normal home alias did not fail closed")
	}
}

func mustResolveT3CodeTest(t *testing.T, a *app) string {
	t.Helper()
	path, err := a.launchRuntime().resolve("t3-code", "")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestT3CodeVersionAndMarkerBoundaries(t *testing.T) {
	for _, platform := range []string{"macos", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			path := syntheticT3CodeExecutable(t, t.TempDir(), platform)
			version, err := t3CodeVersion(path, platform)
			if err != nil || version != t3CodeSupportedVersion {
				t.Fatal(version, err)
			}
		})
	}
	root := filepath.Join(t.TempDir(), "profile with spaces")
	for _, command := range []string{"t3 " + t3CodeMarker + root, "t3 \"" + t3CodeMarker + root + "\"", "t3 " + t3CodeMarker + "\"" + root + "\""} {
		if !t3CodeCommandMatches(command, root) {
			t.Fatal("valid marker rejected", command)
		}
	}
	for _, command := range []string{"t3 " + t3CodeMarker + root + "-other", "t3 prefix" + t3CodeMarker + root, "t3 --unrelated=" + root} {
		if t3CodeCommandMatches(command, root) {
			t.Fatal("unrelated process matched", command)
		}
	}
}

func TestT3CodeCompatibilityAcceptsOnlyValidatedReleases(t *testing.T) {
	for _, platform := range []string{"macos", "linux", "windows"} {
		for _, version := range []string{t3CodeSupportedVersion, t3CodeNightlyVersion, "0.0.46", "0.0.46-nightly.20261003.2611", "0.0.45-nightly.20261002.2600", t3CodeNightlyVersion + "+modified"} {
			t.Run(platform+"/"+version, func(t *testing.T) {
				path := syntheticT3CodeVersionExecutable(t, t.TempDir(), platform, version)
				parsed, err := t3CodeVersion(path, platform)
				if err != nil || parsed != version {
					t.Fatal("package version could not be read", parsed, err)
				}
				wantSupported := version == t3CodeSupportedVersion || version == t3CodeNightlyVersion
				if err := t3CodeCompatibility(path, platform); (err == nil) != wantSupported {
					t.Fatal("unvalidated version accepted or validated version rejected", version, err)
				}
				if platform == "macos" && t3CodeBundleExecutable(path) != filepath.Join(path, "Contents", "MacOS", filepath.Base(strings.TrimSuffix(path, ".app"))) {
					t.Fatal("native bundle executable was not resolved")
				}
			})
		}
	}
}

func TestT3CodeNightlyPreparationReportsInstalledVersion(t *testing.T) {
	for _, platform := range []string{"macos", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			a := t3CodeTestApp(t, platform, t3CodeNightlyVersion)
			for _, body := range []string{"{}", ""} {
				response := adminRequest(a, "clients/t3-code", body)
				var metadata struct {
					Prepared bool   `json:"prepared"`
					Version  string `json:"version"`
				}
				if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &metadata) != nil || !metadata.Prepared || metadata.Version != t3CodeNightlyVersion {
					t.Fatal("nightly preparation did not report its actual version", response.Code, response.Body.String())
				}
			}
			saved, err := a.readT3CodePrepared()
			if err != nil || saved.Version != t3CodeNightlyVersion || !a.t3CodeReady(saved, mustResolveT3CodeTest(t, a), a.launchRuntime()) {
				t.Fatal("nightly snapshot could not be reopened", saved.Version, err)
			}
			if _, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime()); err != nil {
				t.Fatal("prepared nightly did not produce a launch plan", err)
			}
		})
	}
}

func TestT3CodeVersionTransitionRequiresClosedRepreparation(t *testing.T) {
	a := t3CodeTestApp(t, "linux")
	prepareT3CodeFixture(t, a)
	stable, err := a.readT3CodePrepared()
	if err != nil {
		t.Fatal(err)
	}
	// Keep the executable path unchanged, as an installed release update does.
	nightly := syntheticT3CodeExecutable(t, a.launcher.home, "linux", t3CodeNightlyVersion)
	if a.t3CodeReady(stable, nightly, a.launchRuntime()) {
		t.Fatal("stable snapshot remained ready after a nightly update")
	}
	metadata := adminRequest(a, "clients/t3-code", "")
	if metadata.Code != 200 || !strings.Contains(metadata.Body.String(), `"prepared":false`) || !strings.Contains(metadata.Body.String(), `"version":"`+t3CodeNightlyVersion+`"`) {
		t.Fatal("updated nightly did not request preparation", metadata.Code, metadata.Body.String())
	}
	if _, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime()); err == nil {
		t.Fatal("nightly launched with a stale stable snapshot")
	}
	a.t3CodeCheckRunning = func(string) (bool, error) { return true, nil }
	if response := adminRequest(a, "clients/t3-code", `{}`); response.Code != 409 {
		t.Fatal("nightly overwrote a live stable profile", response.Code, response.Body.String())
	}
	unchanged, err := a.readT3CodePrepared()
	if err != nil || unchanged.Version != t3CodeSupportedVersion {
		t.Fatal("blocked transition modified the stable snapshot", unchanged.Version, err)
	}
	a.t3CodeCheckRunning = func(string) (bool, error) { return false, nil }
	prepareT3CodeFixture(t, a)
	saved, err := a.readT3CodePrepared()
	if err != nil || saved.Version != t3CodeNightlyVersion || !a.t3CodeReady(saved, nightly, a.launchRuntime()) {
		t.Fatal("closed profile could not migrate to nightly", saved.Version, err)
	}
	if _, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime()); err != nil {
		t.Fatal("reprepared nightly did not produce a launch plan", err)
	}
}

func TestT3CodeNightlyResolutionAndStableFallback(t *testing.T) {
	for _, platform := range []string{"macos", "windows"} {
		t.Run(platform, func(t *testing.T) {
			home := t.TempDir()
			localAppData := filepath.Join(home, "AppData", "Local")
			systemRoot := filepath.Join(home, "SystemApps")
			t.Setenv("ProgramFiles", systemRoot)
			t.Setenv("ProgramFiles(x86)", "")
			install := func(version string) string {
				if platform == "macos" {
					root := filepath.Join(home, "Applications")
					if version == t3CodeSupportedVersion {
						root = systemRoot
					}
					return syntheticT3CodeVersionExecutable(t, root, platform, version)
				}
				source := syntheticT3CodeVersionExecutable(t, t.TempDir(), platform, version)
				appName := strings.TrimSuffix(filepath.Base(source), ".exe")
				root := systemRoot
				if version == t3CodeSupportedVersion {
					root = localAppData
				}
				destination := filepath.Join(root, "Programs", appName)
				if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(filepath.Dir(source), destination); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(destination, filepath.Base(source))
			}
			stable, nightly := install(t3CodeSupportedVersion), install(t3CodeNightlyVersion)
			candidates := []string{}
			for _, candidate := range t3CodeInstallationCandidates(platform, home, localAppData) {
				// Model a system install and a user install without touching the host.
				if relative, ok := strings.CutPrefix(candidate, filepath.Join("/Applications")+string(filepath.Separator)); ok {
					candidate = filepath.Join(systemRoot, relative)
				}
				if strings.HasPrefix(candidate, home+string(filepath.Separator)) {
					candidates = append(candidates, candidate)
				}
			}
			if selected, err := resolveT3CodeCandidates(candidates); err != nil || selected != nightly {
				t.Fatal("nightly did not take priority over stable", selected, err)
			}
			nightlyRoot := nightly
			if platform == "windows" {
				nightlyRoot = filepath.Dir(nightly)
			}
			if err := os.RemoveAll(nightlyRoot); err != nil {
				t.Fatal(err)
			}
			if selected, err := resolveT3CodeCandidates(candidates); err != nil || selected != stable {
				t.Fatal("missing nightly did not fall back to stable", selected, err)
			}
			unvalidated := install("0.0.46-nightly.20261003.2611")
			selected, err := resolveT3CodeCandidates(candidates)
			if err != nil || selected != unvalidated || t3CodeCompatibility(selected, platform) == nil {
				t.Fatal("unvalidated nightly silently fell back to stable", selected, err)
			}
		})
	}
	linuxCandidates := t3CodeInstallationCandidates("linux", t.TempDir(), "")
	if len(linuxCandidates) == 0 || linuxCandidates[0] != filepath.Join("/opt/T3 Code (Nightly)", "t3code") {
		t.Fatal("Linux nightly installation was not prioritized", linuxCandidates)
	}
}

func TestT3CodeSecretStoreRejectsUnsafeDestination(t *testing.T) {
	a := t3CodeTestApp(t, "linux")
	paths := t3CodePaths(a.dir)
	if err := os.MkdirAll(filepath.Dir(paths.Secrets), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), paths.Secrets); err != nil {
		t.Skip(err)
	}
	w := adminRequest(a, "clients/t3-code", `{}`)
	if w.Code != 409 {
		t.Fatal("unsafe secret directory accepted", w.Code, w.Body.String())
	}
	if _, err := os.Stat(paths.Selection); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed setup saved selection")
	}
}

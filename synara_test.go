package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const synaraTestVersion = "1.0.0-beta.1"
const synaraTestCommit = "37439ec5063892583239638deaab4ed5b1d16d56"

func synaraTestPlatform(platform string) string {
	// Windows cannot represent Unix executable mode bits. Preparation fixtures
	// must exercise its real native-binary contract rather than simulate macOS
	// with a PE test executable and non-executable regular files.
	if runtime.GOOS == "windows" {
		return "windows"
	}
	return platform
}

func synaraTestAdapterSource(t *testing.T, a *app, root, platform string) {
	t.Helper()
	source := filepath.Join(root, "kilo-proxy-native")
	if platform == "windows" {
		source += ".exe"
	}
	// Synthetic native fixtures are only copied and inspected; never started.
	if err := os.WriteFile(source, syntheticSynaraNativeHeader(platform), 0700); err != nil {
		t.Fatal(err)
	}
	a.synaraAdapterSource = func() (string, error) { return source, nil }
}

func syntheticSynaraExecutable(t *testing.T, root, platform string, versions ...string) string {
	t.Helper()
	version := synaraTestVersion
	if len(versions) > 0 {
		version = versions[0]
	}
	return syntheticSynaraVersionExecutable(t, root, platform, version)
}

func syntheticSynaraVersionExecutable(t *testing.T, root, platform, version string) string {
	t.Helper()
	return syntheticSynaraPackageExecutable(t, root, platform, map[string]string{"name": "synara-desktop-beta", "version": version, "synaraDesktopFlavor": "beta", "synaraCommitHash": synaraTestCommit})
}

func syntheticSynaraPackageExecutable(t *testing.T, root, platform string, metadata map[string]string) string {
	t.Helper()
	appName := "Synara Beta"

	appRoot := filepath.Join(root, "Synara Beta fixture")
	executable := filepath.Join(appRoot, "synara-beta")
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
	packageData, err := json.Marshal(metadata)
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

func synaraTestApp(t *testing.T, platform string, versions ...string) *app {
	t.Helper()
	platform = synaraTestPlatform(platform)
	a := launchTestApp(t)
	a.synaraRuntimeDisabled = true
	t.Setenv("CODEX_HOME", filepath.Join(a.launcher.home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(a.launcher.home, ".claude"))
	a.config.Port = 9988
	a.config.Language = "en"
	binary := syntheticSynaraExecutable(t, a.launcher.home, platform, versions...)
	cli := filepath.Join(a.launcher.home, "codex-native")
	if platform == "windows" {
		cli += ".exe"
	}
	claude := filepath.Join(a.launcher.home, "claude-native")
	for _, path := range []string{cli, claude} {
		if err := os.WriteFile(path, []byte("synthetic native CLI"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cliData := syntheticSynaraNativeHeader(platform)
	if err := os.WriteFile(cli, cliData, 0700); err != nil {
		t.Fatal(err)
	}
	synaraTestAdapterSource(t, a, a.launcher.home, platform)
	a.launcher.platform = platform
	a.launcher.resolve = func(id, custom string) (string, error) {
		switch id {
		case "synara":
			return binary, nil
		case "codex-cli":
			return cli, nil
		case "claude":
			return claude, nil
		}
		return "", errors.New("not installed")
	}
	a.synaraCheckRunning = func(string) (bool, error) { return false, nil }
	a.synaraCheckEnvironment = func() error { return nil }
	if _, err := a.modelLibrary.save(testModelLibrary(), 0, false); err != nil {
		t.Fatal(err)
	}
	return a
}

func prepareSynaraFixture(t *testing.T, a *app) {
	t.Helper()
	w := adminRequest(a, "clients/synara", `{}`)
	if w.Code != 200 {
		t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
	}
}

func TestSynaraPrepareLaunchAndRuntimeGuards(t *testing.T) {
	a := synaraTestApp(t, "macos")
	prepareSynaraFixture(t, a)
	rt := a.launchRuntime()
	binary, _ := rt.resolve("synara", "")
	saved, err := a.readSynaraPrepared()
	if err != nil || !a.synaraReady(saved, binary, rt) {
		t.Fatalf("prepared not ready: %v", err)
	}
	paths := synaraPaths(a.dir)
	plan, err := a.planClientLaunch(clientLaunchRequest{Client: "synara"}, rt)
	if err != nil {
		t.Fatal(err)
	}
	executable := binary
	if rt.platform == "macos" || rt.platform == "darwin" {
		executable = t3CodeBundleExecutable(binary)
	}
	if plan.Env["HOME"] != paths.UIHome || plan.Env["SYNARA_BETA_HOME"] != paths.Data || plan.Env["SYNARA_DESKTOP_SMOKE_USER_DATA"] != paths.Electron || len(plan.Args) != 1 || plan.Args[0] != synaraMarker+paths.Root || plan.Executable != executable {
		t.Fatalf("unsafe launch: %#v", plan)
	}
	a.synaraCheckRunning = func(string) (bool, error) { return true, nil }
	if _, err = a.planClientLaunch(clientLaunchRequest{Client: "synara"}, rt); err == nil {
		t.Fatal("running window allowed")
	}
	before, _ := os.ReadFile(paths.Selection)
	if w := adminRequest(a, "clients/synara", `{}`); w.Code != 200 {
		t.Fatalf("unchanged running prepare: %d %s", w.Code, w.Body.String())
	}
	changed := saved.Library
	changed.Models = append([]modelLibraryItem{}, changed.Models...)
	changed.DefaultModel = changed.Models[0].ID
	input, _ := json.Marshal(map[string]any{"library": changed})
	if w := adminRequest(a, "clients/synara", string(input)); w.Code != 409 {
		t.Fatalf("changed running prepare: %d %s", w.Code, w.Body.String())
	}
	after, _ := os.ReadFile(paths.Selection)
	if !bytes.Equal(before, after) {
		t.Fatal("blocked prepare changed selection")
	}
	a.synaraCheckRunning = func(string) (bool, error) { return false, nil }
	a.synaraLaunchUntil = time.Now().Add(time.Minute)
	if _, err = a.planClientLaunch(clientLaunchRequest{Client: "synara"}, rt); err == nil {
		t.Fatal("lease allowed duplicate launch")
	}
	a.synaraLaunchUntil = time.Time{}
	a.config.LocalKey = "changed"
	if a.synaraReady(saved, binary, rt) {
		t.Fatal("connection change stayed ready")
	}
}

func TestSynaraPrepareSerializesAndAcceptsBetaVersions(t *testing.T) {
	a := synaraTestApp(t, "macos")
	a.launchMu.Lock()
	w := adminRequest(a, "clients/synara", `{}`)
	a.launchMu.Unlock()
	if w.Code != 409 {
		t.Fatalf("concurrent launch/prepare accepted: %d", w.Code)
	}
	for _, version := range []string{"1.0.0", "1.0.0-beta.2", "1.0.1-beta.2", "2.8.0-beta.45", "0.0.45", "", "future-beta-build"} {
		binary := syntheticSynaraExecutable(t, t.TempDir(), "linux", version)
		if err := a.synaraAvailability(binary, clientLaunchRuntime{platform: "linux", resolve: a.launcher.resolve}); err != nil {
			t.Fatalf("Beta version %q was rejected: %v", version, err)
		}
	}
}

func TestSynaraBetaPackageIdentityIsIndependentOfVersionAndCommit(t *testing.T) {
	a := synaraTestApp(t, "linux")
	for _, metadata := range []map[string]string{
		{"name": "synara-desktop-beta", "synaraDesktopFlavor": "beta", "version": "1.0.1-beta.2", "synaraCommitHash": "new-upstream-commit"},
		{"name": "synara-desktop-beta", "synaraDesktopFlavor": "beta"},
		{"name": "synara-desktop-beta", "synaraDesktopFlavor": "beta", "version": strings.Repeat("future-build", 20)},
	} {
		binary := syntheticSynaraPackageExecutable(t, t.TempDir(), "linux", metadata)
		if err := a.synaraAvailability(binary, clientLaunchRuntime{platform: "linux", resolve: a.launcher.resolve}); err != nil {
			t.Fatalf("Beta metadata was rejected: %v", err)
		}
	}
	for _, metadata := range []map[string]string{
		{"name": "synara-desktop", "synaraDesktopFlavor": "stable", "version": synaraTestVersion},
		{"name": "other-desktop", "synaraDesktopFlavor": "beta", "version": synaraTestVersion},
		{"name": "synara-desktop-beta", "synaraDesktopFlavor": "stable", "version": synaraTestVersion},
	} {
		binary := syntheticSynaraPackageExecutable(t, t.TempDir(), "linux", metadata)
		if err := a.synaraAvailability(binary, clientLaunchRuntime{platform: "linux", resolve: a.launcher.resolve}); err == nil {
			t.Fatalf("non-Beta package accepted: %#v", metadata)
		}
	}
}

func TestSynaraBetaPrepareLaunchAndUpdatesDoNotUseVersionAllowlist(t *testing.T) {
	for _, version := range []string{"1.0.1-beta.2", "9.9.9-beta.100", ""} {
		t.Run(version, func(t *testing.T) {
			a := synaraTestApp(t, "linux", version)
			prepareSynaraFixture(t, a)
			rt := a.launchRuntime()
			binary, _ := rt.resolve("synara", "")
			saved, err := a.readSynaraPrepared()
			if err != nil || saved.Version != version || !a.synaraReady(saved, binary, rt) {
				t.Fatalf("Beta preparation failed: %v", err)
			}
			if _, err := a.planClientLaunch(clientLaunchRequest{Client: "synara"}, rt); err != nil {
				t.Fatalf("Beta launch failed: %v", err)
			}
			// Windows/Linux use the installed runtime directly: metadata changes
			// alone do not invalidate the four prepared accounts or proxy access.
			updated := syntheticSynaraPackageExecutable(t, filepath.Dir(filepath.Dir(binary)), rt.platform, map[string]string{"name": "synara-desktop-beta", "synaraDesktopFlavor": "beta", "version": "10.0.0-beta.2", "synaraCommitHash": "different"})
			if updated != binary || !a.synaraReady(saved, binary, rt) {
				t.Fatal("Beta update required preparation solely for version or commit")
			}
			if _, err := a.planClientLaunch(clientLaunchRequest{Client: "synara"}, rt); err != nil {
				t.Fatalf("Updated Beta launch failed: %v", err)
			}
		})
	}
}

func TestSynaraAliasStrictSelection(t *testing.T) {
	library := modelLibrary{Models: []modelLibraryItem{{ID: "anthropic/claude-opus-5.5"}, {ID: "anthropic/claude-sonnet-4-6"}, {ID: "anthropic/claude-haiku-4-5-20251001"}, {ID: "vendor/custom"}}}
	for requested, want := range map[string]string{"claude-opus-5-5": "anthropic/claude-opus-5.5", "claude-sonnet-4-6": "anthropic/claude-sonnet-4-6", "claude-haiku-4-5-20251001": "anthropic/claude-haiku-4-5-20251001", "vendor/custom": "vendor/custom"} {
		got, err := synaraResolveModel(library, requested)
		if err != nil || got != want {
			t.Fatalf("%s: %s %v", requested, got, err)
		}
	}
	for _, id := range []string{"opus", "claude-opus-4-6", "claude-haiku-4-5", "claude-haiku-4-5-20251101", "vendor/other", "anthropic/claude-opus-5-5"} {
		if _, err := synaraResolveModel(library, id); err == nil {
			t.Fatalf("unselected %s accepted", id)
		}
	}
	library.Models = append(library.Models, modelLibraryItem{ID: "anthropic/claude-opus-5-5"})
	if _, err := synaraResolveModel(library, "claude-opus-5-5"); err == nil {
		t.Fatal("ambiguous alias accepted")
	}
}

func TestSynaraAliasPreservesNumbersToolsAndExplicitReasoning(t *testing.T) {
	a := synaraTestApp(t, "macos")
	library := modelLibrary{SchemaVersion: 1, DefaultModel: "anthropic/claude-opus-5.5", Models: []modelLibraryItem{{ID: "anthropic/claude-opus-5.5"}}}
	encoded, _ := json.Marshal(map[string]any{"library": library})
	if w := adminRequest(a, "clients/synara", string(encoded)); w.Code != 200 {
		t.Fatalf("prepare: %s", w.Body.String())
	}
	raw := `{"model":"claude-opus-5-5","max_tokens":4096,"output_config":{"effort":"low"},"messages":[{"role":"user","content":"unchanged"}],"tools":[{"name":"read","input_schema":{"type":"object","properties":{"large":{"const":9007199254740993}}}}]}`
	request := httptest.NewRequest("POST", "http://127.0.0.1/synara/v1/messages", strings.NewReader(raw))
	if !synaraInferencePath(request) {
		t.Fatal("route not recognized")
	}
	request = request.WithContext(context.WithValue(request.Context(), synaraRequestContextKey{}, true))
	request.URL.Path = "/v1/messages"
	adapted, err := a.prepareSynaraRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(adapted.Body)
	if !bytes.Contains(body, []byte(`9007199254740993`)) || !bytes.Contains(body, []byte(`"effort":"low"`)) || !bytes.Contains(body, []byte(`"model":"anthropic/claude-opus-5.5"`)) {
		t.Fatalf("payload changed: %s", body)
	}
	var before, after map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &before)
	_ = json.Unmarshal(body, &after)
	for _, key := range []string{"messages", "tools", "output_config"} {
		if !bytes.Equal(before[key], after[key]) {
			t.Fatalf("%s altered", key)
		}
	}
	ordinary := httptest.NewRequest("POST", "http://127.0.0.1/v1/messages", strings.NewReader(raw))
	ordinary, err = a.prepareSynaraRequest(ordinary)
	if err != nil {
		t.Fatal(err)
	}
	untouched, _ := io.ReadAll(ordinary.Body)
	if string(untouched) != raw {
		t.Fatal("general API was rewritten")
	}
	a.config.LocalKey = "changed"
	request = httptest.NewRequest("POST", "http://127.0.0.1/v1/messages", strings.NewReader(raw)).WithContext(context.WithValue(context.Background(), synaraRequestContextKey{}, true))
	if _, err = a.prepareSynaraRequest(request); err == nil {
		t.Fatal("stale snapshot routed inference")
	}
}

func TestSynaraRouteAndMarkerAreExact(t *testing.T) {
	if !synaraInferencePath(httptest.NewRequest("POST", "http://localhost/synara/v1/messages?beta=true", nil)) {
		t.Fatal("the native Claude SDK beta route was rejected")
	}
	for _, url := range []string{"/synara/v1/messages?x=1", "/synara/v1/messages?", "/synara/v1/messages/", "/v1/messages", "/synara/v1/responses", "/synara/v1/%6dessages"} {
		if synaraInferencePath(httptest.NewRequest("POST", "http://localhost"+url, nil)) {
			t.Fatalf("route %s recognized", url)
		}
	}
	root := "/tmp/private Synara"
	if !synaraCommandMatches(`binary "`+synaraMarker+root+`"`, root) {
		t.Fatal("own marker missed")
	}
	for _, command := range []string{`binary "` + synaraMarker + root + ` other"`, `binary --other=` + root, `binary "` + t3CodeMarker + root + `"`} {
		if synaraCommandMatches(command, root) {
			t.Fatalf("unrelated process matched: %s", command)
		}
	}
}

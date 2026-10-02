package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func syntheticOpenMausBotExecutable(t *testing.T, dir, platform string) string {
	t.Helper()
	root := filepath.Join(dir, "OpenMausBot-fixture")
	path := filepath.Join(root, "openmausbot")
	resources := filepath.Join(root, "resources")
	if platform == "windows" {
		path = filepath.Join(root, "OpenMausBot.exe")
	}
	if platform == "macos" || platform == "darwin" {
		root = filepath.Join(dir, "OpenMausBot.app")
		path = filepath.Join(root, "Contents", "MacOS", "OpenMausBot")
		resources = filepath.Join(root, "Contents", "Resources")
	}
	for _, folder := range []string{filepath.Dir(path), filepath.Join(resources, "server", "server", "drivers")} {
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte("synthetic; never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resources, "server", "server", "drivers", "openai-compat.js"), []byte("managedModels apiKeyEnv input.environment"), 0600); err != nil {
		t.Fatal(err)
	}
	if platform == "macos" || platform == "darwin" {
		if err := os.WriteFile(filepath.Join(root, "Contents", "Info.plist"), []byte(`<plist><dict><key>CFBundleShortVersionString</key><string>0.1.92</string></dict></plist>`), 0600); err != nil {
			t.Fatal(err)
		}
		return root
	}
	return path
}

func openMausBotTestApp(t *testing.T, platform string) *app {
	t.Helper()
	a := launchTestApp(t)
	a.config.Language = "en"
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a.config.Port = listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	binary := syntheticOpenMausBotExecutable(t, a.launcher.home, platform)
	a.launcher.platform = platform
	a.launcher.resolve = func(id, custom string) (string, error) {
		if id != "openmausbot" || custom != "" {
			return "", errors.New("unexpected client")
		}
		return binary, nil
	}
	a.openMausBotCheckRunning = func(openMausBotPaths) (bool, error) { return false, nil }
	if _, err := a.modelLibrary.save(testModelLibrary(), 0, false); err != nil {
		t.Fatal(err)
	}
	return a
}

func prepareOpenMausBotFixture(t *testing.T, a *app) {
	t.Helper()
	w := adminRequest(a, "clients/openmausbot", `{}`)
	if w.Code != 200 {
		t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
	}
}

func TestOpenMausBotConfigPreservesSettingsAndAppliesSharedDefaults(t *testing.T) {
	library := testModelLibrary()
	library.DefaultModel = library.Models[len(library.Models)-1].ID
	old := []byte(`{"custom":9007199254740993,"theme":"dark","instances":{"other":{"driver":"custom","config":{"keep":true}}},"context":{"keep":123},"newBotDefaults":{"profile":{"name":"Keep","modelSelection":{"instanceId":"other","model":"before","effort":"high"}}},"defaultModelSelection":{"instanceId":"other","model":"before","effort":"high"}}`)
	updated, err := mergeOpenMausBotConfig(old, library, 8877, "local-only", 64000)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(updated, &data); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("9007199254740993")) || data["theme"] != "dark" {
		t.Fatal("unrelated settings were lost")
	}
	instances := data["instances"].(map[string]any)
	if instances["other"].(map[string]any)["driver"] != "custom" {
		t.Fatal("another instance was changed")
	}
	instance := instances[openMausBotInstance].(map[string]any)
	config := instance["config"].(map[string]any)
	models := config["managedModels"].([]any)
	if len(models) != len(library.Models) || models[0] != library.DefaultModel {
		t.Fatal("driver default did not follow shared selection")
	}
	if config["url"] != "http://127.0.0.1:8877/openmausbot/v1" || config["apiKeyEnv"] != "KILO_LOCAL_API_KEY" || config["model"] != library.DefaultModel || config["tools"] != true || config["catalog"] != nil || config["effort"] != nil {
		t.Fatal("unsupported or incorrect driver configuration")
	}
	if instance["environment"].(map[string]any)["KILO_LOCAL_API_KEY"] != "local-only" {
		t.Fatal("local authentication missing")
	}
	selection := map[string]any{"instanceId": openMausBotInstance, "model": library.DefaultModel}
	if !reflect.DeepEqual(data["defaultModelSelection"], selection) || !reflect.DeepEqual(data["newBotDefaults"].(map[string]any)["profile"].(map[string]any)["modelSelection"], selection) {
		t.Fatal("default or stale reasoning selection retained")
	}
	if !reflect.DeepEqual(data["context"], map[string]any{"keep": float64(123), "autoCompact": true, "compactAt": float64(64000)}) {
		t.Fatal("compaction fields incorrect")
	}
	rotated, err := mergeOpenMausBotConfig(updated, library, 8899, "rotated-local", 32000)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rotated, []byte("local-only")) || bytes.Contains(rotated, []byte(":8877")) {
		t.Fatal("stale local connection retained")
	}
	again, err := mergeOpenMausBotConfig(rotated, library, 8899, "rotated-local", 32000)
	if err != nil || !launchEqualJSON(rotated, again) {
		t.Fatal("merge not idempotent", err)
	}
}

func TestOpenMausBotConfigRejectsMalformedConflictingAndInvalidInputs(t *testing.T) {
	for _, old := range []string{`[]`, `null`, `{"instances":[]}`, `{"instances":{},"instances":{}}`, `{"context":null}`, `{"newBotDefaults":{"profile":[]}}`, `{"instances":{"kilo-local":{"driver":"other"}}}`, `{"instances":{"kilo-local":{"driver":"openai-compat","config":{"url":"https://example.test","apiKeyEnv":"KILO_LOCAL_API_KEY"}}}}`, `{} {}`} {
		if _, err := mergeOpenMausBotConfig([]byte(old), testModelLibrary(), 8877, "local", 1000); err == nil {
			t.Fatalf("accepted %s", old)
		}
	}
	for _, key := range []string{"", "bad\nkey", "bad\x00key"} {
		if _, err := mergeOpenMausBotConfig(nil, testModelLibrary(), 8877, key, 1000); err == nil {
			t.Fatal("unsafe key accepted")
		}
	}
	for _, limit := range []int{0, -1, 10000001} {
		if _, err := mergeOpenMausBotConfig(nil, testModelLibrary(), 8877, "local", limit); err == nil {
			t.Fatal("invalid compaction accepted")
		}
	}
}

func TestOpenMausBotMergePreservesInstanceAppearanceButRemovesCredentialOverrides(t *testing.T) {
	old := []byte(`{"instances":{"kilo-local":{"driver":"openai-compat","accentColor":"blue","environment":{"KEEP":"value","KILO_LOCAL_API_KEY":"old-local"},"config":{"url":"http://127.0.0.1:8877/v1","apiKeyEnv":"KILO_LOCAL_API_KEY","key":"wrong-key-override","catalog":"openai","customPreference":7}}}}`)
	data, err := mergeOpenMausBotConfig(old, testModelLibrary(), 8899, "new-local", 1000)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	_ = json.Unmarshal(data, &value)
	instance := value["instances"].(map[string]any)[openMausBotInstance].(map[string]any)
	config := instance["config"].(map[string]any)
	if instance["accentColor"] != "blue" || instance["environment"].(map[string]any)["KEEP"] != "value" || config["customPreference"] != float64(7) {
		t.Fatal("unrelated managed instance preferences changed")
	}
	if config["key"] != nil || config["catalog"] != nil || bytes.Contains(data, []byte("wrong-key-override")) || bytes.Contains(data, []byte("old-local")) {
		t.Fatal("stale credential/catalog override retained")
	}
}

func TestOpenMausBotDiscoveryUsesPackagedDesktopLayouts(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "Local App Data")
	fixture := syntheticOpenMausBotExecutable(t, filepath.Join(local, "Programs"), "windows")
	installedDir := filepath.Join(local, "Programs", "OpenMausBot")
	if err := os.Rename(filepath.Dir(fixture), installedDir); err != nil {
		t.Fatal(err)
	}
	got, err := resolveOpenMausBotWith("windows", root, local, func(string) string { t.Fatal("Windows discovery should use installed desktop path"); return "" })
	if err != nil || got != filepath.Join(installedDir, "OpenMausBot.exe") {
		t.Fatalf("Windows desktop discovery: %q %v", got, err)
	}
	linux := syntheticOpenMausBotExecutable(t, t.TempDir(), "linux")
	got, err = resolveOpenMausBotWith("linux", root, "", func(string) string { return linux })
	want, _ := filepath.EvalSymlinks(linux)
	if err != nil || got != want {
		t.Fatalf("Linux desktop discovery: %q %v", got, err)
	}
	if _, err := resolveOpenMausBotWith("freebsd", root, "", func(string) string { return linux }); err == nil {
		t.Fatal("unsupported desktop OS accepted")
	}
}

func TestOpenMausBotCompactionUsesSmallestSharedPolicy(t *testing.T) {
	library := modelLibrary{SchemaVersion: 1, DefaultModel: "vendor/large", Models: []modelLibraryItem{{ID: "vendor/large", ContextPreset: contextPresetCustom, ContextWindow: 256000}, {ID: "vendor/small", ContextPreset: contextPresetCustom, ContextWindow: 8000}}}
	got, err := openMausBotCompaction(library, nil)
	small, _ := resolveContextPolicy(contextPresetCustom, 8000, 0, 0)
	if err != nil || got != small.AutoCompactTokenLimit {
		t.Fatalf("budget %d %v; want %d", got, err, small.AutoCompactTokenLimit)
	}
	library.Models[1].ContextPreset = contextPresetMaximum
	library.Models[1].ContextWindow = 0
	if _, err := openMausBotCompaction(library, nil); err == nil {
		t.Fatal("invented maximum context")
	}
	metadata := []modelInfo{{ID: "vendor/small", ContextWindow: 4096}}
	got, err = openMausBotCompaction(library, metadata)
	if err != nil || got >= 4096 {
		t.Fatal("catalog context not respected", got, err)
	}
}

func TestOpenMausBotPrivateProfileLaunchOnEveryPlatform(t *testing.T) {
	for _, platform := range []string{"macos", "windows", "linux"} {
		t.Run(platform, func(t *testing.T) {
			a := openMausBotTestApp(t, platform)
			paths := openMausBotProfilePaths(a.dir)
			if _, err := a.planClientLaunch(clientLaunchRequest{Client: "openmausbot"}, a.launchRuntime()); err == nil {
				t.Fatal("missing profile allowed")
			}
			prepareOpenMausBotFixture(t, a)
			for _, path := range []string{paths.Data, paths.UI} {
				info, err := os.Lstat(path)
				if err != nil || !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
					t.Fatal("profile was not precreated privately", err)
				}
			}
			info, err := os.Stat(paths.Config)
			if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
				t.Fatal("config not private", err)
			}
			plan, err := a.planClientLaunch(clientLaunchRequest{Client: "openmausbot", Directory: "/ignored/stale/project"}, a.launchRuntime())
			if err != nil {
				t.Fatal(err)
			}
			if plan.Directory != a.launcher.home || plan.Kind != "desktop" || !reflect.DeepEqual(plan.Args, []string{"--user-data-dir=" + paths.UI}) || plan.Env["OMB_DATA_DIR"] != paths.Data || plan.Env["KILO_LOCAL_API_KEY"] != a.config.LocalKey {
				t.Fatal("launch not isolated")
			}
			if strings.Contains(strings.Join(plan.Args, " "), a.config.LocalKey) {
				t.Fatal("credential in argv")
			}
			if platform == "macos" && !strings.HasSuffix(plan.Executable, filepath.Join("Contents", "MacOS", "OpenMausBot")) {
				t.Fatal("macOS launch not direct native executable")
			}
			if err := validateClientProcessPlan(plan); err != nil {
				t.Fatal(err)
			}
			response := adminRequest(a, "clients/openmausbot", "")
			if response.Code != 200 || strings.Contains(response.Body.String(), a.config.LocalKey) || strings.Contains(response.Body.String(), a.apiKey) || !strings.Contains(response.Body.String(), `"prepared":true`) {
				t.Fatal("unsafe metadata", response.Code)
			}
			a.config.Port++
			if _, err := a.planClientLaunch(clientLaunchRequest{Client: "openmausbot"}, a.launchRuntime()); err == nil {
				t.Fatal("stale port allowed")
			}
			a.config.Port--
			a.config.LocalKey = "rotated"
			if _, err := a.planClientLaunch(clientLaunchRequest{Client: "openmausbot"}, a.launchRuntime()); err == nil {
				t.Fatal("stale key allowed")
			}
		})
	}
}

func TestOpenMausBotPreparationRunningGateAndRecovery(t *testing.T) {
	a := openMausBotTestApp(t, "macos")
	prepareOpenMausBotFixture(t, a)
	paths := openMausBotProfilePaths(a.dir)
	before, _ := os.ReadFile(paths.Config)
	a.openMausBotCheckRunning = func(openMausBotPaths) (bool, error) { return true, nil }
	prepareOpenMausBotFixture(t, a) // unchanged reopen is safe
	a.config.LocalKey = "changed"
	w := adminRequest(a, "clients/openmausbot", `{}`)
	if w.Code != 409 {
		t.Fatal("live profile changed", w.Code)
	}
	a.openMausBotCheckRunning = func(openMausBotPaths) (bool, error) { return false, errors.New("secret process details") }
	w = adminRequest(a, "clients/openmausbot", `{}`)
	if w.Code != 409 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal("uncertain process accepted or leaked")
	}
	a.openMausBotCheckRunning = func(openMausBotPaths) (bool, error) { return false, nil }
	a.openMausBotLaunchUntil = time.Now().Add(time.Minute)
	w = adminRequest(a, "clients/openmausbot", `{}`)
	if w.Code != 409 {
		t.Fatal("startup reservation ignored")
	}
	after, _ := os.ReadFile(paths.Config)
	if !bytes.Equal(before, after) {
		t.Fatal("blocked preparation wrote profile")
	}
	a.openMausBotLaunchUntil = time.Time{}
	prepareOpenMausBotFixture(t, a)
	a.modelLibrary.mu.Lock()
	a.modelLibrary.state.RecoveryRequired = true
	a.modelLibrary.mu.Unlock()
	if w := adminRequest(a, "clients/openmausbot", `{}`); w.Code != 409 {
		t.Fatal("damaged shared library accepted")
	}
}

func TestOpenMausBotAuthenticationAndAtomicUnsafeFileHandling(t *testing.T) {
	a := openMausBotTestApp(t, "macos")
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "http://"+a.adminHost+openMausBotEndpoint, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.adminHandler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("missing admin auth accepted")
		}
	}
	for _, body := range []string{`{"directory":"/tmp/other"}`, `{"command":"shell"}`, `{"library":{}}`, `{} {}`} {
		if w := adminRequest(a, "clients/openmausbot", body); w.Code < 400 {
			t.Fatal("invalid input accepted", body)
		}
	}
	a.apiKey = ""
	if w := adminRequest(a, "clients/openmausbot", `{}`); w.Code != 409 {
		t.Fatal("unconfigured connection accepted")
	}
	a.apiKey = "synthetic-kilo"
	paths := openMausBotProfilePaths(a.dir)
	if err := safeEditorDir(a.dir, paths.Data); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"keep":"untouched","instances":[]}`)
	if err := os.WriteFile(paths.Config, before, 0600); err != nil {
		t.Fatal(err)
	}
	if w := adminRequest(a, "clients/openmausbot", `{}`); w.Code != 409 {
		t.Fatal("invalid existing profile accepted")
	}
	after, _ := os.ReadFile(paths.Config)
	if !bytes.Equal(before, after) {
		t.Fatal("malformed config overwritten")
	}
	if _, err := os.Stat(paths.Selection); !os.IsNotExist(err) {
		t.Fatal("selection persisted after rejected config")
	}
	if err := os.Remove(paths.Config); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, paths.Config); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlink privilege unavailable")
		}
		t.Fatal(err)
	}
	if w := adminRequest(a, "clients/openmausbot", `{}`); w.Code != 409 {
		t.Fatal("symlink profile accepted")
	}
	untouched, _ := os.ReadFile(outside)
	if string(untouched) != `{}` {
		t.Fatal("external file modified")
	}
}

func TestOpenMausBotRunningChecksBothLeasesWithoutReadingCapabilities(t *testing.T) {
	paths := openMausBotProfilePaths(t.TempDir())
	if err := os.MkdirAll(filepath.Join(paths.Data, ".openmausbot-server-child"), 0700); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	lease := func(pid int, host string) []byte {
		data, _ := json.Marshal(map[string]any{"version": 1, "pid": pid, "host": host, "createdAt": 1, "token": "must-not-be-used"})
		return data
	}
	child := filepath.Join(paths.Data, ".openmausbot-server-child", "openmausbot-server.lease")
	if err := os.WriteFile(child, lease(123, host), 0600); err != nil {
		t.Fatal(err)
	}
	window := func(context.Context, string) (bool, error) { return false, nil }
	running, err := openMausBotRunningWith(paths, func(pid int) (bool, error) { return pid == 123, nil }, window)
	if err != nil || !running {
		t.Fatal("live delegated child ignored", err)
	}
	running, err = openMausBotRunningWith(paths, func(int) (bool, error) { return false, nil }, window)
	if err != nil || running {
		t.Fatal("stale lease blocked", err)
	}
	if _, err := os.Stat(child); err != nil {
		t.Fatal("helper deleted upstream lease")
	}
	if err := os.WriteFile(child, lease(123, "another-host"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := openMausBotRunningWith(paths, func(int) (bool, error) { return false, nil }, window); err == nil {
		t.Fatal("foreign lease accepted")
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	running, err = openMausBotRunningWith(paths, func(int) (bool, error) { t.Fatal("unexpected PID probe"); return false, nil }, func(context.Context, string) (bool, error) { return true, nil })
	if err != nil || !running {
		t.Fatal("starting window ignored", err)
	}
	if _, err := openMausBotRunningWith(paths, func(int) (bool, error) { return false, nil }, func(context.Context, string) (bool, error) { return false, errors.New("private detail") }); err != errOpenMausBotRunning {
		t.Fatal("process details leaked")
	}
}

func TestOpenMausBotProcessMarkerAndCompatibility(t *testing.T) {
	ui := filepath.Join(t.TempDir(), "Kilo Private", "ui")
	for _, command := range []string{"app --user-data-dir=" + ui, "app \"--user-data-dir=" + ui + "\" --other", "app --user-data-dir=\"" + ui + "\""} {
		if !openMausBotWindowCommandMatches(command, ui) {
			t.Fatal("managed instance missed", command)
		}
	}
	for _, command := range []string{"app --user-data-dir=" + ui + "-ordinary", "app --other=--user-data-dir=" + ui, "ordinary app"} {
		if openMausBotWindowCommandMatches(command, ui) {
			t.Fatal("other instance matched", command)
		}
	}
	for _, platform := range []string{"macos", "windows", "linux"} {
		binary := syntheticOpenMausBotExecutable(t, t.TempDir(), platform)
		if err := openMausBotCompatibility(binary, platform); err != nil {
			t.Fatal(platform, err)
		}
	}
	for _, version := range []string{"0.1.91", "0.1.92-rc.1", "0.1", "bogus"} {
		if openMausBotVersionSupported(version) {
			t.Fatal("unsupported version accepted", version)
		}
	}
	for _, version := range []string{"0.1.92", "0.1.93", "1.0.0"} {
		if !openMausBotVersionSupported(version) {
			t.Fatal("supported version rejected", version)
		}
	}
	if err := openMausBotCompatibility(filepath.Join(t.TempDir(), "npm-cli"), "linux"); err == nil {
		t.Fatal("non-desktop npm CLI accepted")
	}
}

func TestOpenMausBotLaunchStartsProxyBeforeDesktopAndRedactsErrors(t *testing.T) {
	a := openMausBotTestApp(t, "macos")
	prepareOpenMausBotFixture(t, a)
	started := false
	a.launcher.start = func(plan clientLaunchPlan) error {
		if a.proxyListener == nil {
			t.Error("desktop launched before proxy")
		}
		started = true
		return errors.New("environment secret " + a.config.LocalKey)
	}
	w := adminRequest(a, "clients/launch", `{"client":"openmausbot"}`)
	if w.Code != http.StatusInternalServerError || !started || strings.Contains(w.Body.String(), a.config.LocalKey) {
		t.Fatal("invalid launch result", w.Code)
	}
}

func TestOpenMausBotAcceptsCompleteFiftyModelLibrary(t *testing.T) {
	a := openMausBotTestApp(t, "macos")
	library := emptyModelLibrary()
	for i := 0; i < 50; i++ {
		library.Models = append(library.Models, modelLibraryItem{ID: "vendor/" + strings.Repeat("long-model-name-", 8) + strconv.Itoa(i), DisplayName: strings.Repeat("A", 80), ContextPreset: contextPresetCustom, ContextWindow: 64000, ReasoningCustom: true, ReasoningEffort: "high", ReasoningLevels: []string{"low", "medium", "high"}})
	}
	library.DefaultModel = library.Models[17].ID
	body, err := json.Marshal(map[string]any{"library": library})
	if err != nil || len(body) <= 16<<10 {
		t.Fatal("fixture must exceed generic action limit")
	}
	if w := adminRequest(a, "clients/openmausbot", string(body)); w.Code != 200 {
		t.Fatal("valid full library rejected", w.Code, w.Body.String())
	}
	saved, err := a.readOpenMausBotPrepared()
	if err != nil || len(saved.Library.Models) != 50 || saved.Library.DefaultModel != library.DefaultModel {
		t.Fatal("full selection was not persisted", err)
	}
}

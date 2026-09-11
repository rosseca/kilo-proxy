package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Preparation validates a Windows PE header even when a test injects a macOS
// launch runtime. This fixture is never executed; actual shim round trips use
// the native Go test executable instead.
func syntheticOpenCodeExecutable(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 128)
	copy(data, "MZ")
	binary.LittleEndian.PutUint32(data[60:64], 64)
	copy(data[64:], "PE\x00\x00")
	path := filepath.Join(dir, "opencode.exe")
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func openDesignTestApp(t *testing.T, platform string) *app {
	t.Helper()
	a := launchTestApp(t)
	a.config.Language = "en"
	a.launcher.platform = platform
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a.config.Port = listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	paths := map[string]string{}
	for _, id := range []string{"open-design", "codex-cli", "claude", "opencode"} {
		if id == "opencode" {
			paths[id] = syntheticOpenCodeExecutable(t, filepath.Join(a.launcher.home, "bin"))
			continue
		}
		path := filepath.Join(a.launcher.home, "bin", id)
		if id == "open-design" {
			path = filepath.Join(a.launcher.home, "Applications", "Open Design.app", "Contents", "MacOS", "Open Design")
			if platform == "windows" {
				path = filepath.Join(a.launcher.home, "Open Design.exe")
			}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		data := []byte("synthetic executable: never run")
		if err := os.WriteFile(path, data, 0700); err != nil {
			t.Fatal(err)
		}
		if id == "open-design" {
			resources := filepath.Join(filepath.Dir(filepath.Dir(path)), "Resources")
			if platform == "windows" {
				resources = filepath.Join(filepath.Dir(path), "resources")
			}
			if err := os.MkdirAll(resources, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(resources, "open-design-config.json"), []byte(`{"appVersion":"0.22.2"}`), 0600); err != nil {
				t.Fatal(err)
			}
		}
		paths[id] = path
		if id == "open-design" && platform != "windows" {
			paths[id] = filepath.Dir(filepath.Dir(filepath.Dir(path)))
		}
	}
	a.launcher.resolve = func(id, custom string) (string, error) {
		if custom != "" {
			return "", errors.New("unexpected executable override")
		}
		if path := paths[id]; path != "" {
			return path, nil
		}
		return "", errors.New("synthetic application not installed")
	}
	a.openDesignCheckRunning = func(string) (bool, error) { return false, nil }
	if _, err := a.modelLibrary.save(testModelLibrary(), 0, false); err != nil {
		t.Fatal(err)
	}
	return a
}

func openDesignPrepareFixture(t *testing.T, a *app, engine string) {
	t.Helper()
	body, _ := json.Marshal(openDesignPrepareRequest{Engine: engine})
	response := adminRequest(a, "open-design/profile", string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("prepare %s: %d %s", engine, response.Code, response.Body.String())
	}
	assertOpenDesignNoResponseKeys(t, a, response.Body.String())
}

func assertOpenDesignNoResponseKeys(t *testing.T, a *app, text string) {
	t.Helper()
	for _, key := range []string{a.apiKey, a.config.LocalKey, a.adminToken} {
		if key != "" && strings.Contains(text, key) {
			t.Fatal("Open Design response exposed a credential")
		}
	}
}

func TestOpenDesignLaunchPlatformGateAppliesToDiscoveryAndPost(t *testing.T) {
	for _, platform := range []string{"macos", "windows", "linux", "freebsd"} {
		t.Run(platform, func(t *testing.T) {
			a := openDesignTestApp(t, platform)
			supported := platform == "macos" || platform == "windows"
			if supported {
				openDesignPrepareFixture(t, a, "codex-cli")
			}
			resolved, started := 0, 0
			resolve := a.launcher.resolve
			a.launcher.resolve = func(id, custom string) (string, error) {
				if id == "open-design" {
					resolved++
				}
				return resolve(id, custom)
			}
			a.launcher.start = func(clientLaunchPlan) error { started++; return nil }
			response := adminRequest(a, "clients/launch", "")
			var result struct {
				Clients map[string]clientLaunchAvailability `json:"clients"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
				t.Fatal("discovery failed")
			}
			info := result.Clients["open-design"]
			if info.Name != "Open Design" || info.Kind != "desktop" || info.Available != supported {
				t.Fatalf("unexpected availability: %+v", info)
			}
			response = adminRequest(a, "clients/launch", `{"client":"open-design","engine":"codex-cli"}`)
			if supported {
				if response.Code != http.StatusOK || resolved != 2 || started != 1 {
					t.Fatalf("supported launch failed: %d %s", response.Code, response.Body.String())
				}
			} else {
				if response.Code != http.StatusConflict || !strings.Contains(info.Reason, "source build") || resolved != 0 || started != 0 || a.proxyListener != nil {
					t.Fatal("unsupported platform reached dispatch")
				}
				if response := adminRequest(a, "open-design/profile", `{"engine":"codex-cli"}`); response.Code != http.StatusConflict {
					t.Fatal("unsupported platform prepared packaged settings")
				}
			}
			assertOpenDesignNoResponseKeys(t, a, response.Body.String())
		})
	}
}

func TestOpenDesignLaunchUsesPreparedPrivateEngineAndStartsProxyFirst(t *testing.T) {
	for _, platform := range []string{"macos", "windows"} {
		for _, engine := range []string{"codex-cli", "claude", "opencode"} {
			t.Run(platform+"/"+engine, func(t *testing.T) {
				a := openDesignTestApp(t, platform)
				beforeHome := openDesignFixtureFiles(t, a.launcher.home)
				request := clientLaunchRequest{Client: "open-design", Engine: engine, Directory: "/nonexistent/project"}
				if _, err := a.planClientLaunch(request, a.launchRuntime()); err == nil {
					t.Fatal("unprepared engine was launchable")
				}
				openDesignPrepareFixture(t, a, engine)
				paths := openDesignProfilePaths(a.dir)
				beforeProfile := openDesignFixtureFiles(t, paths.Root)
				called := 0
				a.launcher.start = func(plan clientLaunchPlan) error {
					called++
					assertLaunchProxyListening(t, a)
					if plan.Client != "open-design" || plan.Directory != a.launcher.home || len(plan.Args) != 0 || plan.Env["KILO_LOCAL_API_KEY"] != a.config.LocalKey || plan.Env["OD_PACKAGED_NAMESPACE"] != paths.Namespace || plan.Env["OD_PACKAGED_NAMESPACE_BASE_ROOT"] != paths.NamespaceBase {
						t.Fatal("dispatch lost its managed namespace or local credential")
					}
					if strings.HasSuffix(plan.Executable, ".app") || plan.Executable == "/usr/bin/open" {
						t.Fatal("desktop launch used an environment-dropping application opener")
					}
					if !a.openDesignFilesMatch(mustOpenDesignPrepared(t, a)) {
						t.Fatal("engine files were not ready before dispatch")
					}
					return nil
				}
				body, _ := json.Marshal(request)
				started := time.Now()
				response := adminRequest(a, "clients/launch", string(body))
				if response.Code != http.StatusOK || called != 1 || !strings.Contains(response.Body.String(), "Local CLI") {
					t.Fatalf("launch failed: %d %s", response.Code, response.Body.String())
				}
				if a.openDesignLaunchUntil.Before(started.Add(29*time.Second)) || a.openDesignLaunchUntil.After(time.Now().Add(31*time.Second)) {
					t.Fatal("launch did not reserve startup for approximately thirty seconds")
				}
				assertOpenDesignNoResponseKeys(t, a, response.Body.String())
				if !reflect.DeepEqual(beforeHome, openDesignFixtureFiles(t, a.launcher.home)) || !reflect.DeepEqual(beforeProfile, openDesignFixtureFiles(t, paths.Root)) {
					t.Fatal("launch altered ordinary files or rewrote the prepared profile")
				}
				if _, err := a.planClientLaunch(clientLaunchRequest{Client: "open-design", AppPath: "/tmp/arbitrary"}, a.launchRuntime()); err == nil {
					t.Fatal("arbitrary executable override accepted")
				}
			})
		}
	}
}

func TestOpenDesignLaunchRejectsWrongEngineAndStaleProfile(t *testing.T) {
	for _, change := range []struct {
		name  string
		apply func(*testing.T, *app, *clientLaunchRequest)
	}{
		{"wrong engine", func(_ *testing.T, _ *app, r *clientLaunchRequest) { r.Engine = "claude" }},
		{"unsupported engine", func(_ *testing.T, _ *app, r *clientLaunchRequest) { r.Engine = "unsupported" }},
		{"port", func(_ *testing.T, a *app, _ *clientLaunchRequest) { a.config.Port++ }},
		{"key", func(_ *testing.T, a *app, _ *clientLaunchRequest) { a.config.LocalKey = "rotated-synthetic" }},
		{"organization", func(_ *testing.T, a *app, _ *clientLaunchRequest) { a.config.OrgID = "other-synthetic-org" }},
		{"file", func(t *testing.T, a *app, _ *clientLaunchRequest) {
			path := filepath.Join(openDesignProfilePaths(a.dir).Profiles, "codex-cli", "config.toml")
			if err := os.WriteFile(path, []byte("model = 'external/change'\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			a := openDesignTestApp(t, "macos")
			openDesignPrepareFixture(t, a, "codex-cli")
			request := clientLaunchRequest{Client: "open-design", Engine: "codex-cli"}
			change.apply(t, a, &request)
			before := openDesignFixtureFiles(t, openDesignProfilePaths(a.dir).Root)
			called := false
			a.launcher.start = func(clientLaunchPlan) error { called = true; return nil }
			body, _ := json.Marshal(request)
			response := adminRequest(a, "clients/launch", string(body))
			if response.Code != http.StatusConflict || called || a.proxyListener != nil || !reflect.DeepEqual(before, openDesignFixtureFiles(t, openDesignProfilePaths(a.dir).Root)) {
				t.Fatal("stale or wrong-engine launch started a process or wrote files")
			}
			assertOpenDesignNoResponseKeys(t, a, response.Body.String())
		})
	}
}

func TestOpenDesignLaunchFailuresDoNotDispatchOrReserveStartup(t *testing.T) {
	a := openDesignTestApp(t, "macos")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	a.config.Port = listener.Addr().(*net.TCPAddr).Port
	openDesignPrepareFixture(t, a, "codex-cli")
	called := false
	a.launcher.start = func(clientLaunchPlan) error { called = true; return nil }
	response := adminRequest(a, "clients/launch", `{"client":"open-design","engine":"codex-cli"}`)
	if response.Code != http.StatusConflict || called || a.proxyListener != nil || !a.openDesignLaunchUntil.IsZero() {
		t.Fatal("failed proxy startup dispatched or reserved Open Design")
	}
	_ = listener.Close()
	a.launcher.start = func(clientLaunchPlan) error { return errors.New("synthetic failure containing " + a.config.LocalKey) }
	response = adminRequest(a, "clients/launch", `{"client":"open-design","engine":"codex-cli"}`)
	if response.Code != http.StatusInternalServerError || !a.openDesignLaunchUntil.IsZero() {
		t.Fatal("failed process dispatch reserved startup or reported success")
	}
	assertOpenDesignNoResponseKeys(t, a, response.Body.String())
}

func TestOpenDesignDiscoveryRequiresInstalledExecutableAndNeverUsesOD(t *testing.T) {
	home, local := t.TempDir(), t.TempDir()
	for _, name := range []string{"od", "open-design", "Open Design.exe"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("not executed"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", home)
	for _, platform := range []string{"linux", "windows"} {
		if path, err := resolveOpenDesignLaunchClient(platform, home, local); err == nil || path != "" {
			t.Fatal("discovery accepted a PATH command", platform)
		}
	}
	for _, base := range []string{local, filepath.Join(home, "AppData", "Local")} {
		path := filepath.Join(base, "Programs", "Open Design", "Open Design.exe")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("not executed"), 0700); err != nil {
			t.Fatal(err)
		}
		resources := filepath.Join(filepath.Dir(path), "resources")
		if err := os.MkdirAll(resources, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(resources, "open-design-config.json"), []byte(`{"appVersion":"0.22.2"}`), 0600); err != nil {
			t.Fatal(err)
		}
		inputLocal := local
		if base != local {
			inputLocal = ""
		}
		if found, err := resolveOpenDesignLaunchClient("windows", home, inputLocal); err != nil || found != path {
			t.Fatalf("default Windows installation not found: %q %v", found, err)
		}
	}
}

func mustOpenDesignPrepared(t *testing.T, a *app) openDesignPrepared {
	t.Helper()
	saved, err := a.openDesignReadPrepared()
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func openDesignFixtureFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[path] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

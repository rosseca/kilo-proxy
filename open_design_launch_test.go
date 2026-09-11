package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOpenDesignLaunchPlatformGateAppliesToDiscoveryAndPost(t *testing.T) {
	for _, platform := range []string{"macos", "windows", "linux", "freebsd"} {
		t.Run(platform, func(t *testing.T) {
			a := launchTestApp(t)
			a.config.Language = "en"
			a.launcher.platform = platform
			resolved, started := 0, 0
			a.launcher.resolve = func(id, custom string) (string, error) {
				if id == "open-design" {
					resolved++
				}
				return "/synthetic/Open Design", nil
			}
			a.launcher.start = func(clientLaunchPlan) error { started++; return nil }
			response := adminRequest(a, "clients/launch", "")
			var result struct {
				Clients map[string]clientLaunchAvailability `json:"clients"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
				t.Fatalf("discovery failed: %d %s", response.Code, response.Body.String())
			}
			info, exists := result.Clients["open-design"]
			if !exists || info.Name != "Open Design" || info.Kind != "desktop" {
				t.Fatal("Open Design desktop identity missing from discovery")
			}
			supported := platform == "macos" || platform == "windows"
			if info.Available != supported {
				t.Fatalf("unexpected availability: %+v", info)
			}
			response = adminRequest(a, "clients/launch", `{"client":"open-design","directory":""}`)
			if supported {
				if response.Code != http.StatusOK || resolved != 2 || started != 1 {
					t.Fatalf("supported launch failed: %d %s", response.Code, response.Body.String())
				}
			} else if response.Code != http.StatusConflict || !strings.Contains(info.Reason, "source build") || resolved != 0 || started != 0 || a.proxyListener != nil {
				t.Fatal("unsupported platform reached discovery or dispatch")
			}
		})
	}
}

func TestOpenDesignLaunchUsesNormalApplicationWithoutProfileOrSecrets(t *testing.T) {
	for _, platform := range []string{"macos", "windows"} {
		t.Run(platform, func(t *testing.T) {
			a := launchTestApp(t)
			a.launcher.platform = platform
			application := filepath.Join(a.launcher.home, "Applications", "Open Design.app")
			if platform == "windows" {
				application = filepath.Join(a.launcher.home, "Open Design.exe")
			}
			a.launcher.resolve = func(string, string) (string, error) { return application, nil }
			marker := filepath.Join(a.launcher.home, "existing-open-design-profile")
			if err := os.WriteFile(marker, []byte("existing settings"), 0600); err != nil {
				t.Fatal(err)
			}
			beforeHome, beforeProxy := openDesignFixtureFiles(t, a.launcher.home), openDesignFixtureFiles(t, a.dir)
			// A selected browser/native project must not become a desktop argument.
			plan, err := a.planClientLaunch(clientLaunchRequest{Client: "open-design", Directory: "/nonexistent/project"}, a.launchRuntime())
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Env) != 0 || len(plan.Unset) != 0 || plan.Directory != a.launcher.home {
				t.Fatal("Open Design launch injected credentials or changed the profile environment")
			}
			if platform == "macos" {
				if plan.Executable != "/usr/bin/open" || !reflect.DeepEqual(plan.Args, []string{"-a", application}) {
					t.Fatalf("unexpected macOS launch: %+v", plan)
				}
			} else if plan.Executable != application || len(plan.Args) != 0 {
				t.Fatalf("unexpected Windows launch: %+v", plan)
			}
			data, err := json.Marshal(plan)
			if err != nil || strings.Contains(string(data), a.config.LocalKey) || strings.Contains(string(data), a.apiKey) {
				t.Fatal("launch plan exposed a secret")
			}
			if !reflect.DeepEqual(beforeHome, openDesignFixtureFiles(t, a.launcher.home)) || !reflect.DeepEqual(beforeProxy, openDesignFixtureFiles(t, a.dir)) {
				t.Fatal("launch preparation changed a profile or wrote credentials")
			}
			if _, err = a.planClientLaunch(clientLaunchRequest{Client: "open-design", AppPath: application}, a.launchRuntime()); err == nil {
				t.Fatal("Open Design accepted an arbitrary executable override")
			}
		})
	}
}

func TestOpenDesignLaunchStartsProxyBeforeDispatchAndExplainsManualSetup(t *testing.T) {
	a := launchTestApp(t)
	a.config.Language = "en"
	called := 0
	a.launcher.start = func(plan clientLaunchPlan) error {
		called++
		assertLaunchProxyListening(t, a)
		if plan.Client != "open-design" || len(plan.Env) != 0 || len(plan.Args) != 0 {
			t.Fatal("unexpected Open Design dispatch")
		}
		return nil
	}
	response := adminRequest(a, "clients/launch", `{"client":"open-design"}`)
	if response.Code != http.StatusOK || called != 1 || !strings.Contains(response.Body.String(), "one-time custom provider setup") {
		t.Fatalf("launch did not explain manual setup: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), a.config.LocalKey) || strings.Contains(response.Body.String(), a.apiKey) {
		t.Fatal("launch response exposed credentials")
	}
}

func TestOpenDesignLaunchProxyFailureBlocksApplication(t *testing.T) {
	for _, failure := range launchProxyFailures() {
		t.Run(failure.name, func(t *testing.T) {
			a := launchTestApp(t)
			failure.set(t, a)
			called := false
			a.launcher.start = func(clientLaunchPlan) error { called = true; return nil }
			response := adminRequest(a, "clients/launch", `{"client":"open-design"}`)
			if response.Code != http.StatusConflict || called || a.proxyListener != nil {
				t.Fatal("failed proxy startup dispatched Open Design")
			}
		})
	}
}

func TestOpenDesignDiscoveryRequiresInstalledExecutableAndNeverUsesOD(t *testing.T) {
	home, local := t.TempDir(), t.TempDir()
	// PATH contains plausible names, including the unrelated system utility name.
	for _, name := range []string{"od", "open-design", "Open Design.exe"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("not executed"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", home)
	if path, err := resolveOpenDesignLaunchClient("linux", home, local); err == nil || path != "" {
		t.Fatal("Linux launch accepted a PATH command")
	}
	if path, err := resolveOpenDesignLaunchClient("windows", home, local); err == nil || path != "" {
		t.Fatal("Windows discovery accepted a PATH command")
	}
	for _, base := range []string{local, filepath.Join(home, "AppData", "Local")} {
		path := filepath.Join(base, "Programs", "Open Design", "Open Design.exe")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("not executed"), 0700); err != nil {
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

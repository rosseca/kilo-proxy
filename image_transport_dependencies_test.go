package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func imageDependencyTestState(t *testing.T, response *httptest.ResponseRecorder) (imageTransportDependency, imageTransportSettings, bool) {
	t.Helper()
	var state struct {
		Dependency imageTransportDependency `json:"imageTransportDependency"`
		Settings   imageTransportSettings   `json:"imageTransport"`
		Running    bool                     `json:"running"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &state) != nil {
		t.Fatalf("invalid state response: status %d", response.Code)
	}
	return state.Dependency, state.Settings, state.Running
}

func TestImageTransportDependencyModesAndPlatforms(t *testing.T) {
	for _, mode := range []string{"", "cloudflare", "off", "compress", "upload", "litterbox", "tailscale"} {
		for platform, command := range map[string]string{
			"darwin": "brew install cloudflared", "macos": "brew install cloudflared",
			"windows": "winget install --id Cloudflare.cloudflared --exact", "linux": "", "freebsd": "",
		} {
			for _, installed := range []bool{false, true} {
				calls := 0
				dependency := imageTransportDependencyFor(imageTransportSettings{Mode: mode}, platform, func(tool string) string {
					calls++
					if tool != "cloudflared" {
						t.Fatal("unexpected dependency lookup", tool)
					}
					if installed {
						return "/private/example/cloudflared"
					}
					return ""
				})
				want := imageTransportDependency{Tool: "cloudflared", Required: mode == "" || mode == "cloudflare", Installed: installed, InstallCommand: command, InstallURL: imageCloudflareInstallURL}
				if calls != 1 || dependency != want {
					t.Fatalf("mode %q platform %s: got %+v, want %+v", mode, platform, dependency, want)
				}
				encoded, _ := json.Marshal(dependency)
				if strings.Contains(string(encoded), "/private/") || (command == "" && strings.Contains(string(encoded), "installCommand")) {
					t.Fatal("dependency status leaked an executable path or empty install command")
				}
			}
		}
	}
}

func TestImageTransportDependencyStateRefreshHasNoSideEffects(t *testing.T) {
	a := testApp(t)
	before := a.config
	a.imageUploadWarning = "Existing cleanup warning"
	installed, calls := false, 0
	a.imageDependencyLookup = func(tool string) string {
		calls++
		if tool != "cloudflared" {
			t.Fatal("unexpected dependency lookup")
		}
		if installed {
			return "/fixture/cloudflared"
		}
		return ""
	}
	a.imageURLBackends.deps = &imageURLBackendDeps{start: func(context.Context, string, string) (imageURLTunnelProcess, error) {
		t.Error("dependency discovery started an image tunnel")
		return nil, errors.New("unexpected tunnel startup")
	}}
	for _, found := range []bool{false, true, false} {
		installed = found
		dependency, settings, running := imageDependencyTestState(t, adminRequest(a, "state", ""))
		if dependency.Installed != found || !dependency.Required || settings.Mode != "cloudflare" || running {
			t.Fatal("state did not refresh static executable availability")
		}
	}
	if calls != 3 || !reflect.DeepEqual(a.config, before) || a.imageUploadWarning != "Existing cleanup warning" || a.imageURLBackends.ctx != nil {
		t.Fatal("dependency discovery mutated settings, cleanup warnings or image services")
	}
	if entries, err := os.ReadDir(a.dir); err != nil || len(entries) != 0 {
		t.Fatal("dependency discovery wrote files", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://"+a.adminHost+"/api/state", nil)
	response := httptest.NewRecorder()
	a.adminHandler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || calls != 3 {
		t.Fatal("unauthenticated state request performed dependency discovery")
	}
}

func TestImageTransportDependencyLookupDoesNotHoldAppLock(t *testing.T) {
	a := testApp(t)
	a.imageDependencyLookup = func(string) string {
		if !a.mu.TryLock() {
			t.Error("filesystem discovery held the application mutex")
			return ""
		}
		// A preference saved during discovery must win over its older snapshot.
		a.config.ImageTransport.Mode = "compress"
		a.mu.Unlock()
		return "/fixture/cloudflared"
	}
	dependency, settings, _ := imageDependencyTestState(t, adminRequest(a, "state", ""))
	if dependency.Required || !dependency.Installed || settings.Mode != "compress" {
		t.Fatal("dependency requirement does not match the current preference")
	}
}

func TestImageTransportMissingDependencyDoesNotBlockProxyOrSmallRequests(t *testing.T) {
	a := testApp(t)
	a.imageDependencyLookup = func(string) string { return "" }
	a.apiKey, a.config.OrgID = "synthetic-key", "synthetic-org"
	a.listenProxy = func(_, _ string) (net.Listener, error) { return net.Listen("tcp4", "127.0.0.1:0") }
	a.imageURLBackends.deps = &imageURLBackendDeps{start: func(context.Context, string, string) (imageURLTunnelProcess, error) {
		t.Error("proxy startup or a small request started an image tunnel")
		return nil, errors.New("unexpected tunnel startup")
	}}
	dependency, _, running := imageDependencyTestState(t, adminRequest(a, "start", "{}"))
	if !running || !dependency.Required || dependency.Installed {
		t.Fatal("missing cloudflared blocked proxy startup or hid the dependency")
	}
	body := ` {"model":"test/model","input":"small request"} `
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	prepared, cleanup, err := a.prepareResponseImageUploads(request, "synthetic-key", "synthetic-org")
	if err != nil || cleanup != nil {
		t.Fatal("default image mode interfered with a small request", err)
	}
	defer prepared.Body.Close()
	data, err := io.ReadAll(prepared.Body)
	if err != nil || string(data) != body || a.imageURLBackends.ctx != nil || a.imageUploadsActive != 0 {
		t.Fatal("small request changed or initialized image services")
	}
}

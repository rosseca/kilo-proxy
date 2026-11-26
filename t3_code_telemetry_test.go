package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestT3CodeTelemetryAppliesToPreparedLaunch(t *testing.T) {
	for _, platform := range []string{"linux", "macos", "windows"} {
		t.Run(platform, func(t *testing.T) {
			a := t3CodeTestApp(t, platform)
			t.Setenv("T3CODE_TELEMETRY_ENABLED", "true")
			t.Setenv("DO_NOT_TRACK", "1")
			prepareT3CodeFixture(t, a)
			paths := t3CodePaths(a.dir)
			before := clientInstallationTree(t, paths.Root)
			t.Cleanup(func() {
				if a.t3CodeAssets != nil {
					a.t3CodeAssets.Close()
				}
			})

			for _, tc := range []struct {
				body     string
				disabled bool
			}{
				{`{"disabled":true}`, true},
				{`{"disabled":false}`, false},
			} {
				w := adminRequest(a, "clients/t3-code/telemetry", tc.body)
				if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != tc.body {
					t.Fatalf("save: %d %s", w.Code, w.Body.String())
				}
				saved, err := readSettings(a.dir)
				if err != nil || saved.T3CodeTelemetryDisabled != tc.disabled {
					t.Fatalf("persisted opt-out = %v, err = %v", saved.T3CodeTelemetryDisabled, err)
				}
				if !reflect.DeepEqual(before, clientInstallationTree(t, paths.Root)) {
					t.Fatal("saving the setting modified the private workspace")
				}
				plan, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime())
				if err != nil {
					t.Fatal("telemetry preference invalidated preparation", err)
				}
				env := clientChildEnvironment(os.Environ(), plan.Env, plan.Unset, platform)
				if slices.Contains(env, "T3CODE_TELEMETRY_ENABLED=false") != tc.disabled ||
					slices.Contains(env, "T3CODE_TELEMETRY_ENABLED=true") ||
					!slices.Contains(env, "DO_NOT_TRACK=1") {
					t.Fatal("unexpected telemetry variables in child environment")
				}
				if os.Getenv("T3CODE_TELEMETRY_ENABLED") != "true" {
					t.Fatal("launch changed the parent environment")
				}
			}
		})
	}
}

func TestT3CodeTelemetryStateAndInvalidSave(t *testing.T) {
	a := testApp(t)
	a.imageDependencyLookup = func(string) string { return "" }
	state := func() bool {
		t.Helper()
		w := adminRequest(a, "state", "")
		var snapshot struct {
			TelemetryDisabled *bool `json:"t3CodeTelemetryDisabled"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &snapshot); w.Code != http.StatusOK || err != nil || snapshot.TelemetryDisabled == nil {
			t.Fatalf("missing telemetry state: HTTP %d, err = %v", w.Code, err)
		}
		return *snapshot.TelemetryDisabled
	}
	if state() {
		t.Fatal("telemetry opt-out default is on")
	}
	for _, body := range []string{`{}`, `{"disabled":null}`, `{"disabled":"true"}`} {
		if w := adminRequest(a, "clients/t3-code/telemetry", body); w.Code != http.StatusBadRequest {
			t.Fatalf("accepted invalid input %s: HTTP %d", body, w.Code)
		}
	}
	path := filepath.Join(a.dir, "settings.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if w := adminRequest(a, "clients/t3-code/telemetry", `{"disabled":true}`); w.Code != http.StatusInternalServerError || state() {
		t.Fatal("failed settings write changed the opt-out")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if w := adminRequest(a, "clients/t3-code/telemetry", `{"disabled":true}`); w.Code != http.StatusOK || !state() {
		t.Fatal("state did not reflect the saved opt-out")
	}
}

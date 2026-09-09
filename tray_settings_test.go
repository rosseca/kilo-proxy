package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func traySettingsRequest(a *app, method, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+a.adminHost+"/api/tray-settings", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.adminHandler().ServeHTTP(w, r)
	return w
}

func TestTraySettingsPersistWithoutChangingConnection(t *testing.T) {
	a := testApp(t)
	a.config.Language, a.config.OrgID = "es", "test-org"
	a.config.ImageGeneration = imageGenerationSettings{Enabled: true, Model: "openai/test-image"}
	a.recordUsage(observeUsage(t, false, `{"usage":{"cost":0.75}}`))
	before := a.config
	if w := traySettingsRequest(a, http.MethodGet, "", a.adminToken); w.Code != 200 || !strings.Contains(w.Body.String(), `"display":"icon"`) {
		t.Fatalf("default tray preference: %d %s", w.Code, w.Body.String())
	}
	w := traySettingsRequest(a, http.MethodPut, `{"display":"spend"}`, a.adminToken)
	if w.Code != 200 || w.Body.String() != "{\"display\":\"spend\"}\n" {
		t.Fatalf("save preference: %d %s", w.Code, w.Body.String())
	}
	before.TrayDisplay = trayDisplaySpend
	if !reflect.DeepEqual(a.config, before) {
		t.Fatal("appearance update changed another setting")
	}
	restarted, err := newApp(a.dir, &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.stop)
	if !reflect.DeepEqual(restarted.config, before) || restarted.trayState().display != trayDisplaySpend {
		t.Fatal("reopening lost the preference or another setting")
	}
	if restarted.usageTotal.Requests != 0 || restarted.trayState().amount != "$0.00" {
		t.Fatal("reopening must begin a new process total")
	}
}

func TestTraySettingsValidationAndAuthorization(t *testing.T) {
	a := testApp(t)
	before := a.config
	for _, tc := range []struct {
		method, body, token string
		status              int
	}{
		{http.MethodGet, "", "wrong-token", 401},
		{http.MethodPut, `{"display":"spend"}`, "wrong-token", 401},
		{http.MethodPut, `{}`, a.adminToken, 400},
		{http.MethodPut, `{"display":"balance"}`, a.adminToken, 400},
		{http.MethodPut, `{"display":"spend","port":8888}`, a.adminToken, 400},
		{http.MethodPut, `{"display":"spend"} {}`, a.adminToken, 400},
	} {
		w := traySettingsRequest(a, tc.method, tc.body, tc.token)
		if w.Code != tc.status {
			t.Fatalf("%s %s: got %d, want %d", tc.method, tc.body, w.Code, tc.status)
		}
	}
	if !reflect.DeepEqual(a.config, before) {
		t.Fatal("a rejected request changed settings")
	}
}

func TestTraySettingsFailedWriteDoesNotApply(t *testing.T) {
	a := testApp(t)
	before := a.config
	// A directory at the destination makes atomic rename fail without relying
	// on user permissions or touching the real application's configuration.
	if err := os.Mkdir(filepath.Join(a.dir, "settings.json"), 0700); err != nil {
		t.Fatal(err)
	}
	w := traySettingsRequest(a, http.MethodPut, `{"display":"spend"}`, a.adminToken)
	if w.Code != 500 || !reflect.DeepEqual(a.config, before) || a.trayState().display != trayDisplayIcon {
		t.Fatalf("failed persistence applied a preference: %d %+v", w.Code, a.config)
	}
	matches, err := filepath.Glob(filepath.Join(a.dir, ".settings-*"))
	if err != nil || len(matches) != 0 {
		t.Fatal("failed write left temporary settings behind")
	}
}

func TestTraySettingsLegacyAndUnknownValuesDefaultToIcon(t *testing.T) {
	for _, display := range []string{"", "future-mode"} {
		a := testApp(t)
		cfg := a.config
		cfg.TrayDisplay = display
		encoded, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(a.dir, "settings.json"), encoded, 0600); err != nil {
			t.Fatal(err)
		}
		loaded, err := readSettings(a.dir)
		if err != nil || loaded.TrayDisplay != trayDisplayIcon {
			t.Fatalf("legacy/future preference %q: %+v %v", display, loaded, err)
		}
	}
}

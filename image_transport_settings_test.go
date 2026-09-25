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

func imageTransportSettingsRequest(a *app, method, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+a.adminHost+"/api/image-transport-settings", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.adminHandler().ServeHTTP(w, r)
	return w
}

func TestImageTransportSettingsDefaultAndPersistence(t *testing.T) {
	a := testApp(t)
	a.config.Language, a.config.OrgID = "es", "test-org"
	a.config.ImageGeneration = imageGenerationSettings{Enabled: true, Model: "openai/test-image"}
	before := a.config
	if a.config.ImageTransport.Mode != "cloudflare" {
		t.Fatal("new profiles must default to Cloudflare image transport")
	}
	if w := imageTransportSettingsRequest(a, http.MethodGet, "", a.adminToken); w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"cloudflare","profile":"high"`) {
		t.Fatalf("default image preference: %d %s", w.Code, w.Body.String())
	}
	for _, preference := range []imageTransportSettings{{"compress", "high", "1h"}, {"compress", "balanced", "1h"}, {"compress", "small", "1h"}, {"upload", "small", "1h"}, {"cloudflare", "small", "1h"}, {"tailscale", "small", "1h"}, {"litterbox", "small", "1h"}, {"litterbox", "small", "12h"}, {"litterbox", "small", "24h"}, {"litterbox", "small", "72h"}, {"off", "small", "72h"}} {
		body, _ := json.Marshal(preference)
		w := imageTransportSettingsRequest(a, http.MethodPut, string(body), a.adminToken)
		if w.Code != 200 {
			t.Fatalf("save preference: %d %s", w.Code, w.Body.String())
		}
		before.ImageTransport = preference
		if !reflect.DeepEqual(a.config, before) {
			t.Fatal("image preference changed another setting")
		}
		restarted, err := newApp(a.dir, &fakeVault{values: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(restarted.stop)
		if !reflect.DeepEqual(restarted.config, before) {
			t.Fatal("reopening lost the image preference or another setting")
		}
		state := httptest.NewRecorder()
		a.state(state)
		var data map[string]any
		if json.Unmarshal(state.Body.Bytes(), &data) != nil || !reflect.DeepEqual(data["imageTransport"], map[string]any{"mode": preference.Mode, "profile": preference.Profile, "litterboxTTL": preference.LitterboxTTL}) {
			t.Fatal("state does not reflect the preference")
		}
	}
}

func TestImageTransportSettingsRejectInvalidInputAndAuth(t *testing.T) {
	a := testApp(t)
	before := a.config
	for _, tc := range []struct {
		method, body, token string
		status              int
	}{
		{http.MethodGet, "", "wrong-token", 401},
		{http.MethodPut, `{"mode":"compress","profile":"high"}`, "wrong-token", 401},
		{http.MethodPost, `{"mode":"compress","profile":"high"}`, a.adminToken, 405},
		{http.MethodPut, `{}`, a.adminToken, 400},
		{http.MethodPut, `{"mode":"off"}`, a.adminToken, 400},
		{http.MethodPut, `{"mode":"invalid","profile":"high"}`, a.adminToken, 400},
		{http.MethodPut, `{"mode":"upload","profile":null}`, a.adminToken, 400},
		{http.MethodPut, `{"mode":null,"profile":"high"}`, a.adminToken, 400},
		{http.MethodPut, `{"mode":"upload","profile":"invalid"}`, a.adminToken, 400},
		{http.MethodPut, `{"mode":"litterbox","profile":"high","litterboxTTL":"5h"}`, a.adminToken, 400},
		{http.MethodPut, `{"mode":"cloudflare,litterbox","profile":"high"}`, a.adminToken, 400},
		{http.MethodPut, `{"mode":"upload","profile":"high","port":8888}`, a.adminToken, 400},
		{http.MethodPut, `{"mode":"upload","profile":"high"} {}`, a.adminToken, 400},
	} {
		w := imageTransportSettingsRequest(a, tc.method, tc.body, tc.token)
		if w.Code != tc.status {
			t.Fatalf("%s %s: got %d, want %d", tc.method, tc.body, w.Code, tc.status)
		}
	}
	if !reflect.DeepEqual(a.config, before) {
		t.Fatal("a rejected request changed settings")
	}
}

func TestImageTransportSettingsFailedWriteDoesNotApply(t *testing.T) {
	a := testApp(t)
	before := a.config
	if err := os.Mkdir(filepath.Join(a.dir, "settings.json"), 0700); err != nil {
		t.Fatal(err)
	}
	w := imageTransportSettingsRequest(a, http.MethodPut, `{"mode":"compress","profile":"high"}`, a.adminToken)
	if w.Code != 500 || !reflect.DeepEqual(a.config, before) {
		t.Fatalf("failed persistence applied a preference: %d", w.Code)
	}
}

func TestImageTransportSettingsLegacyDefaultsCloudflare(t *testing.T) {
	a := testApp(t)
	encoded, _ := json.Marshal(a.config)
	var legacy map[string]any
	_ = json.Unmarshal(encoded, &legacy)
	delete(legacy, "imageTransport")
	encoded, _ = json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(a.dir, "settings.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := readSettings(a.dir)
	if err != nil || loaded.ImageTransport != (imageTransportSettings{Mode: "cloudflare", Profile: "high", LitterboxTTL: "1h"}) {
		t.Fatalf("missing legacy image settings did not use the new default: %+v %v", loaded, err)
	}
}

func TestImageTransportSettingsPreserveEveryExplicitSavedMode(t *testing.T) {
	for _, mode := range []string{"off", "compress", "upload", "cloudflare", "litterbox", "tailscale"} {
		t.Run(mode, func(t *testing.T) {
			a := testApp(t)
			a.config.ImageTransport = imageTransportSettings{Mode: mode, Profile: "balanced", LitterboxTTL: "24h"}
			if err := writeSettings(a.dir, a.config); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(a.dir, "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := readSettings(a.dir)
			if err != nil || !reflect.DeepEqual(loaded, a.config) {
				t.Fatalf("new default replaced an explicit saved preference: %+v %v", loaded, err)
			}
			after, err := os.ReadFile(filepath.Join(a.dir, "settings.json"))
			if err != nil || string(after) != string(before) {
				t.Fatal("reading existing settings rewrote the user's file")
			}
		})
	}
}

func TestImageTransportSettingsDisablingPreservesCleanupWarning(t *testing.T) {
	a := testApp(t)
	a.imageUploadWarning = "Synthetic cleanup warning"
	w := imageTransportSettingsRequest(a, http.MethodPut, `{"mode":"off","profile":"high"}`, a.adminToken)
	if w.Code != 200 || a.imageUploadWarning == "" {
		t.Fatal("disabling uploads discarded the cleanup warning")
	}
	state := httptest.NewRecorder()
	a.state(state)
	if !strings.Contains(state.Body.String(), `"imageUploadWarning":"Synthetic cleanup warning"`) {
		t.Fatal("state hid the cleanup warning")
	}
}

func TestImageTransportSettingsReadValidation(t *testing.T) {
	for _, preference := range []imageTransportSettings{{"invalid", "high", "1h"}, {"off", "invalid", "1h"}, {"litterbox", "high", "5h"}} {
		a := testApp(t)
		a.config.ImageTransport = preference
		if err := writeSettings(a.dir, a.config); err != nil {
			t.Fatal(err)
		}
		if _, err := readSettings(a.dir); err == nil {
			t.Fatal("invalid image settings accepted")
		}
	}
	a := testApp(t)
	a.config.ImageTransport = imageTransportSettings{}
	if err := writeSettings(a.dir, a.config); err != nil {
		t.Fatal(err)
	}
	loaded, err := readSettings(a.dir)
	if err != nil || loaded.ImageTransport != (imageTransportSettings{"cloudflare", "high", "1h"}) {
		t.Fatalf("empty values not normalized: %+v %v", loaded.ImageTransport, err)
	}
}

func TestImageTransportSettingsLegacyTTL(t *testing.T) {
	// Older clients omit the new field. Persist the normalized value so all
	// readers agree on the expiry after a restart.
	a := testApp(t)
	w := imageTransportSettingsRequest(a, http.MethodPut, `{"mode":"upload","profile":"balanced"}`, a.adminToken)
	want := imageTransportSettings{Mode: "upload", Profile: "balanced", LitterboxTTL: "1h"}
	if w.Code != http.StatusOK || a.config.ImageTransport != want {
		t.Fatalf("legacy update did not normalize expiry: %d %+v", w.Code, a.config.ImageTransport)
	}
	loaded, err := readSettings(a.dir)
	if err != nil || loaded.ImageTransport != want {
		t.Fatalf("legacy expiry did not persist: %+v %v", loaded.ImageTransport, err)
	}
	if !strings.Contains(w.Body.String(), `"litterboxTTL":"1h"`) {
		t.Fatal("response did not include the normalized expiry")
	}
}

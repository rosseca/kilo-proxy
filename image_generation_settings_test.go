package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func imageGenerationSettingsRequest(a *app, method, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+a.adminHost+"/api/image-generation", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+a.adminToken)
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	a.adminHandler().ServeHTTP(w, r)
	return w
}

func TestImageGenerationSettingsIndependentPersistence(t *testing.T) {
	a := testApp(t)
	a.config.Language, a.config.OrgID = "es", "synthetic-team"
	a.apiKey = "synthetic-kilo-credential"
	// No real listener or generation is needed to verify that saving preferences
	// does not close an existing server or reset active-request counters.
	server := &http.Server{}
	a.proxyServer, a.active, a.imageGenerationActive = server, 1, 1
	defer func() { a.active, a.imageGenerationActive = 0, 0 }()
	a.codexProfileDir = t.TempDir()
	profile := filepath.Join(a.codexProfileDir, "config.toml")
	oldProfile := []byte("# Preserve this open agent's profile\n")
	if err := os.WriteFile(profile, oldProfile, 0600); err != nil {
		t.Fatal(err)
	}
	before := a.config
	for _, images := range []imageGenerationSettings{
		{Enabled: true, Provider: "chatgpt", Model: "vendor/old-image"},
		{Enabled: true, Provider: "chatgpt"},
		{Enabled: true, Provider: "kilo", Model: "vendor/old-image"},
		{Enabled: false, Provider: "chatgpt", Model: "vendor/old-image"},
		{Enabled: false, Model: "vendor/old-image"},
	} {
		body, _ := json.Marshal(images)
		w := imageGenerationSettingsRequest(a, http.MethodPost, string(body), nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var response struct {
			OK              bool                    `json:"ok"`
			ImageGeneration imageGenerationSettings `json:"imageGeneration"`
		}
		if json.Unmarshal(w.Body.Bytes(), &response) != nil || !response.OK || response.ImageGeneration != images {
			t.Fatal("wrong settings response")
		}
		var keys map[string]json.RawMessage
		if json.Unmarshal(w.Body.Bytes(), &keys) != nil || len(keys) != 2 {
			t.Fatal("response exposed unrelated configuration")
		}
		for _, secret := range []string{a.apiKey, a.config.LocalKey, a.config.VaultID, a.adminToken} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("settings response exposed credentials")
			}
		}
		before.ImageGeneration = images
		if a.config != before {
			t.Fatal("saving images changed unrelated configuration")
		}
		loaded, err := readSettings(a.dir)
		if err != nil || loaded != before {
			t.Fatalf("image preference did not survive reload: %v", err)
		}
		if a.proxyServer != server || a.active != 1 || a.imageGenerationActive != 1 {
			t.Fatal("preference save interrupted active work")
		}
		currentProfile, err := os.ReadFile(profile)
		if err != nil || !bytes.Equal(currentProfile, oldProfile) {
			t.Fatal("standalone save modified an agent profile")
		}
	}
}

func TestImageGenerationSettingsPreserveDisconnectedSelection(t *testing.T) {
	for _, provider := range []string{"", "kilo", "chatgpt"} {
		t.Run(provider, func(t *testing.T) {
			a := testApp(t)
			images := imageGenerationSettings{Enabled: true, Provider: provider, Model: "vendor/kept-for-later"}
			body, _ := json.Marshal(images)
			w := imageGenerationSettingsRequest(a, http.MethodPost, string(body), nil)
			if w.Code != 200 || a.config.ImageGeneration != images {
				t.Fatal("disconnected selection was rejected or rewritten", w.Code)
			}
			if a.clientImageSettingsLocked().Enabled {
				t.Fatal("saving implicitly selected a connected provider")
			}
			loaded, err := readSettings(a.dir)
			if err != nil || loaded.ImageGeneration != images {
				t.Fatal("disconnected preference lost on reload", err)
			}
		})
	}
}

func TestImageGenerationSettingsStrictInputAndAuthentication(t *testing.T) {
	valid := `{"enabled":true,"provider":"chatgpt","model":"vendor/kept"}`
	for _, tc := range []struct {
		name, method, body string
		mutate             func(*http.Request)
		status             int
	}{
		{"missing-token", "POST", valid, func(r *http.Request) { r.Header.Del("Authorization") }, 401},
		{"wrong-token", "POST", valid, func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, 401},
		{"foreign-host", "POST", valid, func(r *http.Request) { r.Host = "evil.example" }, 403},
		{"foreign-origin", "POST", valid, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, 403},
		{"get", "GET", "", nil, 405},
		{"put", "PUT", valid, nil, 405},
		{"content-type", "POST", valid, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"null", "POST", "null", nil, 400},
		{"array", "POST", "[]", nil, 400},
		{"unknown", "POST", `{"enabled":true,"provider":"chatgpt","apiKey":"not-accepted"}`, nil, 400},
		{"trailing", "POST", valid + ` {}`, nil, 400},
		{"wrong-type", "POST", `{"enabled":"true","provider":"chatgpt"}`, nil, 400},
		{"bad-provider", "POST", `{"enabled":true,"provider":"other","model":"vendor/model"}`, nil, 400},
		{"missing-kilo-model", "POST", `{"enabled":true,"provider":"kilo"}`, nil, 400},
		{"bad-kilo-model", "POST", `{"enabled":true,"model":"bad model"}`, nil, 400},
		{"bounded", "POST", `{"enabled":true,"provider":"chatgpt","model":"` + strings.Repeat("a", 17<<10) + `"}`, nil, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			before := a.config
			w := imageGenerationSettingsRequest(a, tc.method, tc.body, tc.mutate)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
			if a.config != before {
				t.Fatal("rejected input changed settings")
			}
			if _, err := os.Stat(filepath.Join(a.dir, "settings.json")); !os.IsNotExist(err) {
				t.Fatal("rejected input wrote settings")
			}
		})
	}
}

func TestImageGenerationSettingsWriteFailureRollsBack(t *testing.T) {
	a := testApp(t)
	a.config.ImageGeneration = imageGenerationSettings{Enabled: true, Model: "vendor/old"}
	before := a.config
	// A directory at the target makes atomic replacement fail on every platform.
	if err := os.Mkdir(filepath.Join(a.dir, "settings.json"), 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(a.dir, "settings.json", "keep")
	if err := os.WriteFile(marker, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	w := imageGenerationSettingsRequest(a, "POST", `{"enabled":true,"provider":"chatgpt","model":"vendor/old"}`, nil)
	if w.Code != 500 || a.config != before {
		t.Fatal("failed persistence applied settings", w.Code)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "unchanged" {
		t.Fatal("failed save changed destination")
	}
	leftovers, err := filepath.Glob(filepath.Join(a.dir, ".settings-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatal("failed save left temporary settings files")
	}
	if strings.Contains(w.Body.String(), a.dir) || strings.Contains(w.Body.String(), a.config.LocalKey) {
		t.Fatal("persistence failure exposed internal data")
	}
}

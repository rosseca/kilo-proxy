package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type liteLLMSettingsTestTransport func(*http.Request) (*http.Response, error)

func (f liteLLMSettingsTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func liteLLMSettingsTestResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}
}

func TestLiteLLMNormalizesOnlyAPIAddresses(t *testing.T) {
	for input, expected := range map[string]string{
		" https://Gateway.invalid:443/team/ ": "https://gateway.invalid/team/v1",
		"https://gateway.invalid/team/v1/":    "https://gateway.invalid/team/v1",
		"http://[::1]:4000":                   "http://[::1]:4000/v1",
	} {
		base, err := normalizeLiteLLMURL(input)
		if err != nil || base.String() != expected {
			t.Fatalf("normalize %q: got %v, %v, want %q", input, base, err, expected)
		}
	}
	for _, invalid := range []string{
		"", "gateway.invalid", "file:///tmp/gateway", "https://user:key@gateway.invalid",
		"https://gateway.invalid/api?token=key", "https://gateway.invalid/api#", "https://gateway.invalid/team/ui/caching/",
		"https://gateway.invalid/team/../v1", "https://gateway.invalid/team%2fv1", "https://gateway.invalid:70000/",
	} {
		if _, err := normalizeLiteLLMURL(invalid); err == nil {
			t.Errorf("accepted invalid API address %q", invalid)
		}
	}
}

func TestLiteLLMConfigurationCommitsOnlyVerifiedSettings(t *testing.T) {
	a := testApp(t)
	original := "https://gateway.invalid/empresa/v1"
	var calls []liteLLMConnection
	status, body := http.StatusOK, `{"object":"list","data":[{"id":"model-a"}]}`
	a.transport = liteLLMSettingsTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/empresa/v1/models" {
			t.Fatalf("connection check requested %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-KiloCode-OrganizationId") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("LiteLLM check sent Kilo credentials or cookies")
		}
		calls = append(calls, liteLLMConnection{Base: r.URL, Key: r.Header.Get("Authorization")})
		return liteLLMSettingsTestResponse(r, status, body), nil
	})
	initial := adminRequest(a, "state", "")
	var view struct {
		LiteLLM         liteLLMView `json:"litellm"`
		KiloEnabled     bool        `json:"kiloEnabled"`
		ConnectionReady bool        `json:"connectionReady"`
	}
	if initial.Code != 200 || json.Unmarshal(initial.Body.Bytes(), &view) != nil ||
		view.LiteLLM.URL != "" || view.LiteLLM.Enabled || !view.KiloEnabled {
		t.Fatalf("expected blank optional LiteLLM settings: %d %#v", initial.Code, view)
	}

	for _, local := range []string{"http://localhost:8877", "http://127.0.0.1:9999", "http://[::1]:8877"} {
		w := adminRequest(a, "providers/litellm", `{"url":"`+local+`","apiKey":"never-send"}`)
		if w.Code != 400 || len(calls) != 0 {
			t.Fatalf("the local listener received a LiteLLM probe: %d, %v", w.Code, calls)
		}
	}
	localKey := adminRequest(a, "providers/litellm", `{"url":"https://gateway.invalid/empresa","apiKey":"`+a.config.LocalKey+`"}`)
	if localKey.Code != 400 || len(calls) != 0 {
		t.Fatal("the local client key was accepted as a remote credential")
	}
	saved := adminRequest(a, "providers/litellm", `{"url":"https://gateway.invalid/empresa/","apiKey":"first-fixture-key"}`)
	if saved.Code != 200 || json.Unmarshal(saved.Body.Bytes(), &view) != nil ||
		view.LiteLLM.URL != original || !view.LiteLLM.Enabled || !view.LiteLLM.Ready ||
		!view.LiteLLM.HasKey || !view.LiteLLM.KeySaved || view.LiteLLM.ModelsCount != 1 || !view.ConnectionReady {
		t.Fatalf("unexpected verification result %d %#v", saved.Code, view)
	}
	vault := a.vault.(*fakeVault)
	if a.config.LiteLLM.VaultRef == "" || vault.values[liteLLMVaultSlot(a.config.VaultID, a.config.LiteLLM)] != "first-fixture-key" {
		t.Fatal("LiteLLM key was not stored separately")
	}
	data, err := os.ReadFile(filepath.Join(a.dir, "settings.json"))
	if err != nil || strings.Contains(string(data), "first-fixture-key") || strings.Contains(saved.Body.String(), "first-fixture-key") {
		t.Fatal("LiteLLM key escaped the credential store")
	}
	restarted, err := newApp(a.dir, vault)
	if err != nil || restarted.liteLLMKey != "first-fixture-key" || restarted.config.LiteLLM.URL != original {
		t.Fatalf("LiteLLM settings did not survive restart: %v", err)
	}

	revision := a.catalogRevision
	before := a.config.LiteLLM
	status, body = http.StatusUnauthorized, `{"error":{"message":"private remote auth detail"}}`
	denied := adminRequest(a, "providers/litellm", `{"url":"https://other.invalid/empresa","apiKey":"bad-fixture-key"}`)
	if denied.Code != http.StatusUnprocessableEntity || strings.Contains(denied.Body.String(), "private remote auth detail") ||
		a.config.LiteLLM != before || a.catalogRevision != revision || len(vault.values) != 1 {
		t.Fatalf("unverified draft affected the active connection: %d %s", denied.Code, denied.Body)
	}
	count := len(calls)
	changedWithoutKey := adminRequest(a, "providers/litellm", `{"url":"https://other.invalid/empresa"}`)
	if changedWithoutKey.Code != 400 || len(calls) != count {
		t.Fatal("editing the destination would reuse or attempt to infer the existing key")
	}
	status, body = http.StatusOK, `{"object":"list","data":[]}`
	same := adminRequest(a, "providers/litellm", `{"url":"https://gateway.invalid:443/empresa"}`)
	if same.Code != 200 || len(vault.values) != 1 || calls[len(calls)-1].Key != "Bearer first-fixture-key" {
		t.Fatal("an equivalent URL did not retain its key")
	}
	vault.fail = true
	replace := adminRequest(a, "providers/litellm", `{"url":"https://other.invalid/empresa","apiKey":"new-fixture-key"}`)
	if replace.Code != 500 || a.config.LiteLLM.URL != original || a.liteLLMKey != "first-fixture-key" {
		t.Fatal("credential-store failure changed the working connection")
	}
	vault.fail = false
	configPath := filepath.Join(a.dir, "settings.json")
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(configPath, 0700); err != nil {
		t.Fatal(err)
	}
	replace = adminRequest(a, "providers/litellm", `{"url":"https://other.invalid/empresa","apiKey":"new-fixture-key"}`)
	if replace.Code != 500 || a.config.LiteLLM.URL != original || a.liteLLMKey != "first-fixture-key" || len(vault.values) != 1 {
		t.Fatal("settings-write failure changed or leaked the working credential")
	}
}

func TestLiteLLMAndKiloActivationPreserveAccountSettings(t *testing.T) {
	a := testApp(t)
	a.config.OrgID = "fixture-org"
	a.apiKey = "kilo-fixture-key"
	a.transport = liteLLMSettingsTestTransport(func(r *http.Request) (*http.Response, error) {
		return liteLLMSettingsTestResponse(r, http.StatusOK, `{"data":[]}`), nil
	})
	toggle := func(provider string, enabled bool) {
		t.Helper()
		input, err := json.Marshal(map[string]bool{"enabled": enabled})
		if err != nil {
			t.Fatal(err)
		}
		w := adminRequest(a, "providers/"+provider+"/active", string(input))
		if w.Code != 200 {
			t.Fatalf("toggle %s: %d %s", provider, w.Code, w.Body)
		}
	}
	toggle("kilo", false)
	if !a.config.KiloPaused || a.apiKey != "kilo-fixture-key" || a.config.OrgID != "fixture-org" {
		t.Fatal("pausing Kilo removed saved credentials or organization")
	}

	keyless := adminRequest(a, "providers/litellm", `{"url":"http://localhost:4000"}`)
	if keyless.Code != 200 || !a.liteLLMReadyLocked() {
		t.Fatalf("could not enable keyless LiteLLM on another local port: %d %s", keyless.Code, keyless.Body)
	}
	port := adminRequest(a, "config", `{"port":9877}`)
	if port.Code != 200 || a.config.Port != 9877 || a.config.OrgID != "fixture-org" || a.apiKey != "kilo-fixture-key" {
		t.Fatalf("could not edit the local port without re-entering Kilo credentials: %d %s", port.Code, port.Body)
	}
	toggle("litellm", false)
	changed := adminRequest(a, "providers/litellm", `{"url":"https://other.invalid/"}`)
	if changed.Code != 200 || a.liteLLMReadyLocked() || a.config.LiteLLM.URL != "https://other.invalid/v1" {
		t.Fatal("editing a paused connection re-enabled it")
	}
	toggle("kilo", true)
	toggle("litellm", true)
	if !a.kiloReadyLocked() || !a.liteLLMReadyLocked() {
		t.Fatal("accounts could not be enabled together")
	}
}

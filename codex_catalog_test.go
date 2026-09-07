package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testCatalog = `{"models":[{"slug":"anthropic/claude-fable-5.1","default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"low"},{"effort":"high"}]}]}`

func catalogRequest(a *app, method, body string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+a.adminHost+"/api/codex/catalog", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if auth {
		r.Header.Set("Authorization", "Bearer "+a.adminToken)
	}
	w := httptest.NewRecorder()
	a.adminHandler().ServeHTTP(w, r)
	return w
}
func TestCatalogSaveLoadAndBackup(t *testing.T) {
	a := testApp(t)
	a.adminHost = "127.0.0.1:1234"
	a.codexProfileDir = t.TempDir()
	config := []byte("model = \"anthropic/claude-fable-5.1\"\nmodel_catalog_json = \"models.json\"\n")
	os.WriteFile(filepath.Join(a.codexProfileDir, "config.toml"), config, 0600)
	body := `{"catalog":` + testCatalog + `}`
	if w := catalogRequest(a, "POST", body, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for i := 0; i < 2; i++ {
		if w := catalogRequest(a, "POST", body, true); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := catalogRequest(a, "GET", "", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"effort":"high"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, name := range []string{"models.json"} {
		data, err := os.ReadFile(filepath.Join(a.codexProfileDir, name))
		if err != nil || !bytes.Equal(bytes.TrimSpace(data), []byte(testCatalog)) {
			t.Fatal(name, err)
		}
	}
	unchanged, _ := os.ReadFile(filepath.Join(a.codexProfileDir, "config.toml.bak"))
	if !bytes.Equal(config, unchanged) {
		t.Fatal("original config not backed up")
	}
}
func TestCatalogRejectsUnsafeWrites(t *testing.T) {
	a := testApp(t)
	a.adminHost = "127.0.0.1:1234"
	a.codexProfileDir = t.TempDir()
	body := `{"catalog":` + testCatalog + `}`
	if w := catalogRequest(a, "POST", body, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	os.Remove(filepath.Join(a.codexProfileDir, "models.json"))
	os.WriteFile(filepath.Join(a.codexProfileDir, "config.toml"), []byte(""), 0600)
	external := filepath.Join(t.TempDir(), "untouched")
	os.WriteFile(external, []byte("original"), 0600)
	if err := os.Symlink(external, filepath.Join(a.codexProfileDir, "models.json")); err != nil {
		t.Skip(err)
	}
	if w := catalogRequest(a, "POST", body, true); w.Code != 409 {
		t.Fatal(w.Code)
	}
	data, _ := os.ReadFile(external)
	if string(data) != "original" {
		t.Fatal("symlink followed")
	}
	os.Remove(filepath.Join(a.codexProfileDir, "models.json"))
	os.WriteFile(filepath.Join(a.codexProfileDir, "models.json"), []byte("{}"), 0600)
	os.Symlink(external, filepath.Join(a.codexProfileDir, "models.json.bak"))
	if w := catalogRequest(a, "POST", body, true); w.Code != 409 {
		t.Fatal(w.Code)
	}
}
func TestCatalogValidation(t *testing.T) {
	for _, data := range []string{`{"models":[]}`, strings.Replace(testCatalog, `"high"`, `"invalid"`, 1), strings.Replace(testCatalog, "anthropic/claude-fable-5.1", "bad id", 1), `{"models":[{"slug":"v/a"},{"slug":"v/a"}]}`, `{"models":[{"slug":"v/a","supported_reasoning_levels":[{"effort":"low"}]}]}`, strings.Repeat(" ", catalogLimit+1)} {
		if _, err := validateCatalog([]byte(data)); err == nil {
			t.Fatal("invalid catalog accepted")
		}
	}
}

func TestCodexCLIProfileIsIndependent(t *testing.T) {
	a := testApp(t)
	a.adminHost = "127.0.0.1:1234"
	root := t.TempDir()
	a.codexProfileDir = filepath.Join(root, "desktop")
	a.codexCLIProfileDir = filepath.Join(root, "cli")
	request := func(method, body string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+a.adminHost+"/api/codex-cli/catalog", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if auth {
			r.Header.Set("Authorization", "Bearer "+a.adminToken)
		}
		w := httptest.NewRecorder()
		a.adminHandler().ServeHTTP(w, r)
		return w
	}
	body := `{"catalog":` + testCatalog + `}`
	if w := request("POST", body, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request("GET", "", true); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := catalogRequest(a, "POST", body, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	desktopConfig, _ := os.ReadFile(filepath.Join(a.codexProfileDir, "config.toml"))
	desktopCatalog, _ := os.ReadFile(filepath.Join(a.codexProfileDir, "models.json"))
	if w := request("POST", body, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	configPath := filepath.Join(a.codexCLIProfileDir, "config.toml")
	cliConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// Place an unrelated setting at the top level, outside the managed provider.
	custom := append([]byte("# retained CLI preference\napproval_policy = 'on-request'\n"), cliConfig...)
	if err := os.WriteFile(configPath, custom, 0600); err != nil {
		t.Fatal(err)
	}
	updated := strings.ReplaceAll(body, "anthropic/claude-fable-5.1", "vendor/cli-only")
	if w := request("POST", updated, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("GET", "", true); w.Code != 200 || !strings.Contains(w.Body.String(), "vendor/cli-only") {
		t.Fatal(w.Code, w.Body.String())
	}
	backup, _ := os.ReadFile(configPath + ".bak")
	if !bytes.Equal(backup, custom) {
		t.Fatal("CLI backup changed")
	}
	result, _ := os.ReadFile(configPath)
	if !strings.Contains(string(result), "retained CLI preference") || !strings.Contains(string(result), "vendor/cli-only") {
		t.Fatal("CLI update lost settings")
	}
	for name, expected := range map[string][]byte{"config.toml": desktopConfig, "models.json": desktopCatalog} {
		got, _ := os.ReadFile(filepath.Join(a.codexProfileDir, name))
		if !bytes.Equal(got, expected) {
			t.Fatal("CLI changed desktop", name)
		}
	}
}

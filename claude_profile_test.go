package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeFixture() claudeSelection {
	return claudeSelection{Models: []claudeModel{{ID: "anthropic/claude-fable-5.1", DisplayName: "Fable", Effort: "low"}, {ID: "anthropic/claude-opus-4.6", DisplayName: "Opus", Effort: "medium"}}, Initial: "anthropic/claude-fable-5.1", Aliases: map[string]string{"haiku": "anthropic/claude-opus-4.6"}, Mode: "modern"}
}
func TestClaudeVersionCapabilities(t *testing.T) {
	for _, tt := range []struct {
		v    string
		p, e bool
	}{{"", false, false}, {"2.1.39 (Claude Code)", false, false}, {"2.1.241", false, false}, {"2.1.242", true, false}, {"2.1.251", true, true}, {"2.1.263", true, true}, {"3.0.0", true, true}, {"garbage", false, false}} {
		got := claudeCaps(tt.v)
		if got.Picker != tt.p || got.PerModelEffort != tt.e {
			t.Fatal(tt, got)
		}
	}
}
func TestClaudeProfileCreatesMergesAndBacksUp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	s := claudeFixture()
	caps := claudeCaps("2.1.263")
	changed, err := saveClaudeProfile(dir, s, caps, 8877, "local-fixture")
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	path := filepath.Join(dir, "settings.json")
	data, _ := os.ReadFile(path)
	var got map[string]json.RawMessage
	json.Unmarshal(data, &got)
	got["permissions"] = json.RawMessage(`{"deny":["Read(.env)"]}`)
	got["custom"] = json.RawMessage(`{"large":9007199254740993}`)
	got["hooks"] = json.RawMessage(`{"Stop":[]}`)
	data, _ = json.MarshalIndent(got, "", "  ")
	os.WriteFile(path, data, 0600)
	s.Models[0].Effort = "high"
	changed, err = saveClaudeProfile(dir, s, caps, 9988, "rotated-local")
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	backup, _ := os.ReadFile(path + ".bak")
	if !bytes.Equal(backup, data) {
		t.Fatal("backup not exact")
	}
	current, _ := os.ReadFile(path)
	if !bytes.Contains(current, []byte("9007199254740993")) || !bytes.Contains(current, []byte("Read(.env)")) {
		t.Fatal("unrelated settings changed")
	}
	var settings struct {
		Env    map[string]string `json:"env"`
		Picker struct {
			Options []struct{ Model, Label string } `json:"options"`
		} `json:"modelPicker"`
		ModelSettings map[string]struct {
			Effort string `json:"effortLevel"`
		} `json:"modelSettings"`
	}
	json.Unmarshal(current, &settings)
	if settings.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:9988" || settings.Env["ANTHROPIC_AUTH_TOKEN"] != "rotated-local" || settings.Picker.Options[0].Label != "Fable" || settings.ModelSettings["claude-fable-5-1"].Effort != "high" {
		t.Fatal("wrong managed settings")
	}
	changed, err = saveClaudeProfile(dir, s, caps, 9988, "rotated-local")
	if err != nil || changed {
		t.Fatal("no-op", changed, err)
	}
	backup2, _ := os.ReadFile(path + ".bak")
	if !bytes.Equal(backup, backup2) {
		t.Fatal("backup replaced on no-op")
	}
}
func TestClaudeLegacyAndAuthRepair(t *testing.T) {
	s := claudeFixture()
	old := []byte(`{"env":{"ANTHROPIC_API_KEY":"stale","CLAUDE_CODE_USE_BEDROCK":"1","CLAUDE_CODE_EFFORT_LEVEL":"max","KEEP":"yes"},"apiKeyHelper":"old helper","modelPicker":{"options":[]},"effortLevel":"xhigh"}`)
	got, err := mergeClaudeSettings(old, s, claudeCaps("2.1.39"), 8877, "local")
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]json.RawMessage
	json.Unmarshal(got, &settings)
	if settings["modelPicker"] != nil || settings["modelSettings"] != nil || settings["apiKeyHelper"] != nil {
		t.Fatal(string(got))
	}
	if string(settings["effortLevel"]) != `"low"` {
		t.Fatal(string(got))
	}
	for _, value := range []string{"stale", "BEDROCK", "CLAUDE_CODE_EFFORT_LEVEL"} {
		if strings.Contains(string(got), value) {
			t.Fatal("stale auth/routing", value)
		}
	}
	if !strings.Contains(string(got), `"KEEP": "yes"`) {
		t.Fatal("env lost")
	}
}
func TestClaudeInvalidProfileIsUntouched(t *testing.T) {
	for _, value := range []string{"{", "null", "[]", `{"env":[]}`} {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")
		os.WriteFile(path, []byte(value), 0600)
		_, err := saveClaudeProfile(dir, claudeFixture(), claudeCaps("2.1.263"), 8877, "test")
		if err == nil {
			t.Fatal("invalid accepted")
		}
		data, _ := os.ReadFile(path)
		if string(data) != value {
			t.Fatal("file changed")
		}
		if _, err := os.Stat(filepath.Join(dir, "kilo-models.json")); !os.IsNotExist(err) {
			t.Fatal("partial save")
		}
	}
	dir := t.TempDir()
	original := filepath.Join(t.TempDir(), "original")
	os.WriteFile(original, []byte("{}"), 0600)
	if err := os.Symlink(original, filepath.Join(dir, "settings.json")); err != nil {
		t.Skip(err)
	}
	if _, err := saveClaudeProfile(dir, claudeFixture(), claudeCaps("2.1.263"), 8877, "test"); err == nil {
		t.Fatal("symlink followed")
	}
}
func TestClaudeProfileEndpoint(t *testing.T) {
	a := testApp(t)
	a.claudeProfileDir = filepath.Join(t.TempDir(), "profile")
	body, _ := json.Marshal(claudeFixture())
	request := func(auth bool, method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+a.adminHost+"/api/claude/profile", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if auth {
			r.Header.Set("Authorization", "Bearer "+a.adminToken)
		}
		w := httptest.NewRecorder()
		a.adminHandler().ServeHTTP(w, r)
		return w
	}
	if w := request(false, "POST", string(body)); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(true, "POST", string(body)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(true, "GET", ""); w.Code != 200 || strings.Contains(w.Body.String(), a.config.LocalKey) {
		t.Fatal(w.Code, "GET exposed credentials or failed")
	}
}
func TestClaudeRejectsInventedEfforts(t *testing.T) {
	for _, id := range []string{"openai/gpt-5.6-sol", "z-ai/glm-5.3", "anthropic/claude-haiku-4.5"} {
		if validClaudeEffort(id, "low") {
			t.Fatal(id)
		}
	}
	if validClaudeEffort("anthropic/claude-fable-5.1", "max") || validClaudeEffort("anthropic/claude-opus-4.6", "xhigh") {
		t.Fatal("unsupported persistent effort")
	}
}

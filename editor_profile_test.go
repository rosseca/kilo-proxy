package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exampleEditorSelection() editorSelection {
	return editorSelection{Models: []editorModel{{ID: "vendor/one", Name: "One", Context: 64000, Output: 4000}, {ID: "vendor/two", Name: "Two", Context: 128000}}, Initial: "vendor/two"}
}
func TestEditorJSONCPreservation(t *testing.T) {
	for _, client := range []string{"opencode", "zed"} {
		original := []byte("// private comment\n{\n  \"large\": 900719925474099312345, // exact number\n  \"theme\":\"dark\",\n  \"permission\": {\"bash\": \"ask\"},\n  \"language_models\":{\"other\":{\"enabled\":true}},\n}\n")
		updated, err := mergeEditorSettings(original, client, exampleEditorSelection(), "http://127.0.0.1:8877/v1", "local-test-key")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"// private comment", "900719925474099312345, // exact number", `"theme":"dark"`, `"bash": "ask"`, `"other":{"enabled":true}`, "vendor/one", "vendor/two"} {
			if !bytes.Contains(updated, []byte(want)) {
				t.Errorf("%s: lost %s", client, want)
			}
		}
		if bytes.Contains(updated, []byte("local-test-key")) != (client == "opencode") {
			t.Errorf("%s: wrong credential storage", client)
		}
		twice, err := mergeEditorSettings(updated, client, exampleEditorSelection(), "http://127.0.0.1:8877/v1", "local-test-key")
		if err != nil || !bytes.Equal(twice, updated) {
			t.Fatalf("%s: no-op changed formatting", client)
		}
		changed, err := mergeEditorSettings(updated, client, editorSelection{Models: exampleEditorSelection().Models[:1], Initial: "vendor/one"}, "http://127.0.0.1:8899/v1", "new-local-key")
		if err != nil || bytes.Contains(changed, []byte("vendor/two")) || !bytes.Contains(changed, []byte(":8899/v1")) {
			t.Fatalf("%s: update failed", client)
		}
	}
	for _, bad := range []string{`[]`, `null`, `{"theme":1,"theme":2}`, `{"language_models":null}`, `{"language_models":[]}`, `{"theme":`} {
		if _, err := mergeEditorSettings([]byte(bad), "zed", exampleEditorSelection(), "http://localhost/v1", "key"); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
func TestEditorPrepareLoadBackupsAndIsolation(t *testing.T) {
	a := testApp(t)
	a.editorTestRoot = t.TempDir()
	body, _ := json.Marshal(exampleEditorSelection())
	for _, client := range []string{"opencode", "zed"} {
		endpoint := "editors/" + client + "/profile"
		if w := adminRequest(a, endpoint, string(body)); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		_, path, choices, _ := a.editorPaths(client)
		first, _ := os.ReadFile(path)
		custom := append([]byte("// retained\n"), first...)
		if err := os.WriteFile(path, custom, 0600); err != nil {
			t.Fatal(err)
		}
		if w := adminRequest(a, endpoint, string(body)); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		current, _ := os.ReadFile(path)
		if !bytes.Equal(current, custom) {
			t.Fatal("no-op changed comments")
		}
		s := exampleEditorSelection()
		s.Models[0].Name = "Renamed"
		update, _ := json.Marshal(s)
		if w := adminRequest(a, endpoint, string(update)); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		backup, _ := os.ReadFile(path + ".bak")
		if !bytes.Equal(backup, custom) {
			t.Fatal("backup not exact")
		}
		if w := adminRequest(a, endpoint, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Renamed") {
			t.Fatal(w.Body.String())
		}
		if data, _ := os.ReadFile(choices); bytes.Contains(data, []byte(a.config.LocalKey)) {
			t.Fatal("selection leaks credential")
		}
		before, _ := os.ReadFile(path)
		if err := os.WriteFile(choices+".bak", []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(choices + ".bak"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(choices+".bak", 0700); err != nil {
			t.Fatal(err)
		}
		if w := adminRequest(a, endpoint, string(body)); w.Code != 409 {
			t.Fatal(w.Code)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatal("partial update on unsafe backup")
		}
	}
}
func TestEditorRejectsSymlinkAndBadInputs(t *testing.T) {
	a := testApp(t)
	a.editorTestRoot = t.TempDir()
	body, _ := json.Marshal(exampleEditorSelection())
	dest := filepath.Join(a.editorTestRoot, ".opencode-kilo")
	if err := os.Symlink(t.TempDir(), dest); err != nil {
		t.Skip("symlinks unavailable")
	}
	if w := adminRequest(a, "editors/opencode/profile", string(body)); w.Code != 409 {
		t.Fatal(w.Code)
	}
	for _, s := range []editorSelection{{}, {Models: []editorModel{{ID: "model", Context: 100}}, Initial: "model"}, {Models: exampleEditorSelection().Models, Initial: "missing"}} {
		if validateEditorSelection(s) == nil {
			t.Fatal("invalid selection accepted")
		}
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Caller holds a.mu so a concurrent Prepare cannot partially change these files.
func (a *app) launchProfile(p *clientLaunchPlan, home string) error {
	fail := profileLaunchError(p.Name)
	id := p.Client
	if id == "cursor" {
		if a.cursor == nil || a.cursor.Status != "running" || a.cursor.URL == "" {
			return errors.New("Start the Cursor HTTPS tunnel before launching Cursor.")
		}
		return nil
	}
	dir := ""
	switch id {
	case "codex":
		dir = a.codexProfileDir
		if dir == "" {
			dir = filepath.Join(home, ".codex-kilo-desktop")
		}
	case "codex-cli":
		dir = a.codexCLIProfileDir
		if dir == "" {
			dir = filepath.Join(home, ".codex-kilo-cli")
		}
	case "claude":
		dir = a.claudeProfileDir
		if dir == "" {
			dir = filepath.Join(home, ".claude-kilo")
		}
	case "xcode-codex", "xcode-claude":
		root, err := a.xcodeRootDir()
		if err != nil {
			return fail
		}
		dir = filepath.Join(root, map[string]string{"xcode-codex": "codex", "xcode-claude": "ClaudeAgentConfig"}[id])
	case "xcode-chat":
		dir = a.dir
	}
	if dir != "" && !safeLaunchDir(dir, home) {
		return fail
	}
	read := func(name string) ([]byte, error) { return readCatalogFile(filepath.Join(dir, name)) }
	switch id {
	case "codex", "codex-cli", "xcode-codex":
		catalog, e := read("models.json")
		if e != nil {
			return fail
		}
		old, e := read("config.toml")
		if e != nil {
			return fail
		}
		updated, e := mergeCodexConfig(old, catalog, a.config.Port)
		if e != nil {
			return fail
		}
		if id == "xcode-codex" {
			updated, e = codexXcodeAuth(updated, a.config.LocalKey)
			if e != nil {
				return fail
			}
		}
		var before, after map[string]any
		if toml.Unmarshal(old, &before) != nil || toml.Unmarshal(updated, &after) != nil || !reflect.DeepEqual(before, after) {
			return fail
		}
		if id != "xcode-codex" {
			p.Env["CODEX_HOME"] = dir
			p.Env["KILO_LOCAL_API_KEY"] = a.config.LocalKey
		}
	case "claude", "xcode-claude":
		choices, e := read("kilo-models.json")
		var s claudeSelection
		if e != nil || json.Unmarshal(choices, &s) != nil || validateClaudeSelection(s) != nil {
			return fail
		}
		old, e := read("settings.json")
		if e != nil {
			return fail
		}
		valid := false
		// Match any supported saved compatibility mode without executing a client to inspect its version.
		for _, caps := range []claudeCapabilities{{}, {Picker: true}, {Picker: true, PerModelEffort: true}} {
			updated, e := mergeClaudeSettings(old, s, caps, a.config.Port, a.config.LocalKey)
			if e == nil && launchEqualJSON(old, updated) {
				valid = true
				break
			}
		}
		if !valid {
			return fail
		}
		if id == "claude" {
			p.Env["CLAUDE_CONFIG_DIR"] = dir
			p.Unset = append([]string{}, nativeClaudeResetEnv...)
			p.Args = []string{"--settings", filepath.Join(dir, "settings.json")}
		}
	case "opencode", "zed":
		root, path, choices, e := a.editorPaths(id)
		if e != nil || !safeLaunchDir(filepath.Dir(path), root) {
			return fail
		}
		data, e := readCatalogFile(choices)
		var s editorSelection
		if e != nil || json.Unmarshal(data, &s) != nil || validateEditorSelection(s) != nil {
			return fail
		}
		old, e := readCatalogFile(path)
		if e != nil {
			return fail
		}
		updated, e := mergeEditorSettings(old, id, s, "http://127.0.0.1:"+strconv.Itoa(a.config.Port)+"/v1", a.config.LocalKey)
		if e != nil || !bytes.Equal(old, updated) {
			return fail
		}
		if id == "opencode" {
			p.Env["OPENCODE_CONFIG"] = path
			p.Unset = []string{"OPENCODE_CONFIG_CONTENT"}
			p.Args = []string{"--model", "kilo-local/" + s.Initial}
		}
	case "xcode-chat":
		data, e := read("xcode-chat.json")
		var s claudeSelection
		if e != nil || json.Unmarshal(data, &s) != nil || validateClaudeSelection(s) != nil {
			return fail
		}
	}
	return nil
}
func launchEqualJSON(a, b []byte) bool {
	var x, y any
	da, db := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	da.UseNumber()
	db.UseNumber()
	return da.Decode(&x) == nil && db.Decode(&y) == nil && reflect.DeepEqual(x, y)
}
func safeLaunchDir(dir, root string) bool {
	// The system home/temp root can itself be an OS alias (e.g. /var on macOS).
	// Reject links in the managed profile and every ancestor below that root.
	rel, e := filepath.Rel(root, dir)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		root = filepath.Dir(dir)
	}
	for p := filepath.Clean(dir); ; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if p == filepath.Clean(root) || filepath.Dir(p) == filepath.Clean(root) || p == filepath.Dir(p) {
			return true
		}
	}
}

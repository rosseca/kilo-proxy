package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type claudeModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
	Effort      string `json:"effort,omitempty"`
}
type claudeSelection struct {
	Models  []claudeModel     `json:"models"`
	Initial string            `json:"initial"`
	Aliases map[string]string `json:"aliases"`
	Mode    string            `json:"mode"`
}
type claudeCapabilities struct {
	Version        string `json:"version"`
	Picker         bool   `json:"picker"`
	PerModelEffort bool   `json:"perModelEffort"`
}

var claudeVersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:\s|$)`)

func claudeCaps(version string) claudeCapabilities {
	c := claudeCapabilities{Version: version}
	parts := claudeVersionPattern.FindStringSubmatch(version)
	if parts == nil {
		return c
	}
	major, _ := strconv.Atoi(parts[1])
	minor, _ := strconv.Atoi(parts[2])
	patch, _ := strconv.Atoi(parts[3])
	c.Version = strings.Join(parts[1:4], ".")
	c.Picker = major > 2 || major == 2 && (minor > 1 || minor == 1 && patch >= 242)
	c.PerModelEffort = major > 2 || major == 2 && (minor > 1 || minor == 1 && patch >= 251)
	return c
}
func installedClaude() claudeCapabilities {
	binary, err := exec.LookPath("claude")
	if err != nil {
		home, _ := os.UserHomeDir()
		candidates := []string{filepath.Join(home, ".local", "bin", "claude"), "/opt/homebrew/bin/claude", "/usr/local/bin/claude"}
		if runtime.GOOS == "windows" {
			candidates = []string{filepath.Join(home, ".local", "bin", "claude.exe")}
		}
		for _, candidate := range candidates {
			if info, e := os.Stat(candidate); e == nil && info.Mode().IsRegular() {
				binary = candidate
				break
			}
		}
	}
	if binary == "" {
		return claudeCapabilities{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil || len(output) > 256 {
		return claudeCapabilities{}
	}
	return claudeCaps(strings.TrimSpace(string(output)))
}

// Only Claude families with documented native effort are offered. Kilo IDs stay
// unchanged on requests; modelSettings uses Claude's canonical family name.
var claudeEffortID = regexp.MustCompile(`(?i)^(?:anthropic/)?(claude-(?:fable-5(?:[.-]1)?|opus-(?:5|4[.-][678])|sonnet-(?:5|4[.-]6)))(?:-\d{8})?$`)

func claudeEffortKey(id string) string {
	match := claudeEffortID.FindStringSubmatch(id)
	if match == nil {
		return ""
	}
	return strings.ReplaceAll(strings.ToLower(match[1]), ".", "-")
}
func validClaudeEffort(id, effort string) bool {
	if effort == "" {
		return true
	}
	key := claudeEffortKey(id)
	if key == "" {
		return false
	}
	if effort == "xhigh" {
		return key != "claude-opus-4-6" && key != "claude-sonnet-4-6"
	}
	return effort == "low" || effort == "medium" || effort == "high"
}
func validateClaudeSelection(s claudeSelection) error {
	if len(s.Models) < 1 || len(s.Models) > 50 || s.Mode != "installed" && s.Mode != "modern" {
		return errors.New("Choose 1–50 models and a valid Claude compatibility mode")
	}
	ids := map[string]bool{}
	nativeIDs := map[string]bool{}
	for _, m := range s.Models {
		if !catalogID.MatchString(m.ID) || ids[m.ID] || len([]rune(m.DisplayName)) > 80 || strings.IndexFunc(m.DisplayName, func(r rune) bool { return r < 32 || r == 127 }) >= 0 || !validClaudeEffort(m.ID, m.Effort) {
			return errors.New("Invalid Claude model, display name or effort")
		}
		ids[m.ID] = true
		if native := claudeEffortKey(m.ID); native != "" {
			if nativeIDs[native] {
				return errors.New("Choose one gateway ID per Claude family/version for native reasoning")
			}
			nativeIDs[native] = true
		}
	}
	if !ids[s.Initial] {
		return errors.New("Choose an initial model from your selection")
	}
	for key, id := range s.Aliases {
		if key != "sonnet" && key != "opus" && key != "haiku" || id != "" && !ids[id] {
			return errors.New("Choose alias targets from your selected models")
		}
	}
	return nil
}
func claudeManagedSettings(s claudeSelection, caps claudeCapabilities, port int, key string) map[string]any {
	nativeID := func(id string) string {
		if caps.Picker {
			if native := claudeEffortKey(id); native != "" {
				return native
			}
		}
		return id
	}
	env := map[string]any{"ANTHROPIC_BASE_URL": "http://127.0.0.1:" + strconv.Itoa(port), "ANTHROPIC_AUTH_TOKEN": key, "ANTHROPIC_MODEL": nativeID(s.Initial)}
	result := map[string]any{"env": env, "model": nativeID(s.Initial)}
	byID := map[string]claudeModel{}
	for _, m := range s.Models {
		byID[m.ID] = m
	}
	for _, alias := range []string{"sonnet", "opus", "haiku"} {
		id := s.Aliases[alias]
		if id == "" {
			id = s.Initial
		}
		prefix := "ANTHROPIC_DEFAULT_" + strings.ToUpper(alias) + "_MODEL"
		env[prefix] = nativeID(id)
		if caps.Picker {
			name := byID[id].DisplayName
			if name == "" {
				name = id
			}
			env[prefix+"_NAME"] = name
		}
	}
	if caps.Picker {
		env["ANTHROPIC_DEFAULT_FABLE_MODEL"] = nativeID(s.Initial)
		name := byID[s.Initial].DisplayName
		if name == "" {
			name = s.Initial
		}
		env["ANTHROPIC_DEFAULT_FABLE_MODEL_NAME"] = name
		options := []map[string]any{}
		for _, m := range s.Models {
			name := m.DisplayName
			if name == "" {
				name = m.ID
			}
			options = append(options, map[string]any{"model": nativeID(m.ID), "label": name})
		}
		result["modelPicker"] = map[string]any{"options": options, "replaceBuiltInOptions": true}
		overrides := map[string]string{}
		for _, m := range s.Models {
			if native := nativeID(m.ID); native != m.ID {
				overrides[native] = m.ID
			}
		}
		if len(overrides) > 0 {
			result["modelOverrides"] = overrides
		}
	}
	if caps.PerModelEffort {
		settings := map[string]any{}
		for _, m := range s.Models {
			if m.Effort != "" {
				settings[claudeEffortKey(m.ID)] = map[string]any{"effortLevel": m.Effort}
			}
		}
		result["modelSettings"] = settings
	} else if effort := byID[s.Initial].Effort; effort != "" && effort != "xhigh" {
		result["effortLevel"] = effort
	}
	return result
}
func decodeClaudeSettings(data []byte) (map[string]json.RawMessage, error) {
	result := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(data)) == 0 {
		return result, nil
	}
	if err := json.Unmarshal(data, &result); err != nil || result == nil {
		return nil, errors.New("settings.json must be a JSON object; no profile changes saved")
	}
	return result, nil
}
func mergeClaudeSettings(data []byte, s claudeSelection, caps claudeCapabilities, port int, key string) ([]byte, error) {
	result, err := decodeClaudeSettings(data)
	if err != nil {
		return nil, err
	}
	env := map[string]json.RawMessage{}
	if raw, ok := result["env"]; ok {
		if err := json.Unmarshal(raw, &env); err != nil || env == nil {
			return nil, errors.New("settings.json env must be an object; no profile changes saved")
		}
	}
	for name := range env {
		if strings.HasPrefix(name, "ANTHROPIC_DEFAULT_") || strings.HasPrefix(name, "ANTHROPIC_CUSTOM_MODEL_") {
			delete(env, name)
		}
	}
	// Clear conflicting routing/authentication only in the isolated profile.
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_CUSTOM_HEADERS", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CODE_SUBAGENT_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"} {
		delete(env, name)
	}
	for _, name := range []string{"apiKeyHelper", "modelPicker", "effortLevel", "modelOverrides"} {
		delete(result, name)
	}
	managed := claudeManagedSettings(s, caps, port, key)
	if caps.PerModelEffort {
		perModel := map[string]json.RawMessage{}
		if raw, ok := result["modelSettings"]; ok {
			if json.Unmarshal(raw, &perModel) != nil || perModel == nil {
				return nil, errors.New("modelSettings must be an object; no profile changes saved")
			}
		}
		for _, model := range s.Models {
			native := claudeEffortKey(model.ID)
			if native == "" {
				continue
			}
			entry := map[string]json.RawMessage{}
			if raw, ok := perModel[native]; ok {
				if json.Unmarshal(raw, &entry) != nil || entry == nil {
					return nil, errors.New("Invalid per-model settings; no profile changes saved")
				}
			}
			if model.Effort == "" {
				delete(entry, "effortLevel")
			} else {
				entry["effortLevel"], _ = json.Marshal(model.Effort)
			}
			if len(entry) == 0 {
				delete(perModel, native)
			} else {
				perModel[native], _ = json.Marshal(entry)
			}
		}
		result["modelSettings"], _ = json.Marshal(perModel)
	} else {
		delete(result, "modelSettings")
	}
	delete(managed, "modelSettings")

	for name, value := range managed["env"].(map[string]any) {
		env[name], _ = json.Marshal(value)
	}
	result["env"], _ = json.Marshal(env)
	for name, value := range managed {
		if name != "env" {
			result[name], _ = json.Marshal(value)
		}
	}
	updated, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, err
	}
	updated = append(updated, '\n')
	if len(updated) > catalogLimit {
		return nil, errors.New("Claude settings exceed the size limit")
	}
	// Preserve exact formatting and backups on a semantic no-op.
	var a, b any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&a) == nil {
		decoder = json.NewDecoder(bytes.NewReader(updated))
		decoder.UseNumber()
		decoder.Decode(&b)
		aa, _ := json.Marshal(a)
		bb, _ := json.Marshal(b)
		if bytes.Equal(aa, bb) {
			return data, nil
		}
	}
	return updated, nil
}
func saveClaudeProfile(dir string, s claudeSelection, caps claudeCapabilities, port int, key string) (bool, error) {
	if err := validateClaudeSelection(s); err != nil {
		return false, err
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return false, errors.New("Cannot create the Claude Kilo profile")
		}
		info, err = os.Lstat(dir)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("Claude Kilo profile must be a real directory")
	}
	path := filepath.Join(dir, "settings.json")
	old, err := readCatalogFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, errors.New("Cannot safely read Claude settings")
	}
	updated, err := mergeClaudeSettings(old, s, caps, port, key)
	if err != nil {
		return false, err
	}
	selection, _ := json.MarshalIndent(s, "", "  ")
	selection = append(selection, '\n')
	choices, err := prepareProfileFile(filepath.Join(dir, "kilo-models.json"), selection)
	if err != nil {
		return false, err
	}
	settings, err := prepareProfileFile(path, updated)
	if err != nil {
		return false, err
	}
	for _, f := range []profileFile{choices, settings} {
		if f.changed && f.exists {
			if err := atomicCatalogFile(f.path+".bak", f.old); err != nil {
				return false, errors.New("Cannot back up the Claude profile; settings unchanged")
			}
		}
	}
	if choices.changed {
		if err := atomicCatalogFile(choices.path, choices.new); err != nil {
			return false, errors.New("Cannot save Claude model selections")
		}
	}
	if settings.changed {
		if err := atomicCatalogFile(settings.path, settings.new); err != nil {
			if choices.changed {
				if choices.exists {
					err = atomicCatalogFile(choices.path, choices.old)
				} else {
					err = os.Remove(choices.path)
				}
				if err != nil {
					return false, errors.New("Cannot save Claude settings or restore choices; use the .bak files")
				}
			}
			return false, errors.New("Cannot save Claude settings; model selections restored")
		}
	}
	return choices.changed || settings.changed, nil
}
func (a *app) claudeProfile(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/claude/info" {
		jsonResponse(w, 200, installedClaude())
		return
	}
	dir := a.claudeProfileDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			jsonError(w, 500, "Cannot locate the home directory")
			return
		}
		dir = filepath.Join(home, ".claude-kilo")
	}
	if r.Method == "GET" {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			jsonError(w, 404, "No saved Claude Kilo profile yet")
			return
		}
		data, err := readCatalogFile(filepath.Join(dir, "kilo-models.json"))
		var s claudeSelection
		if err != nil || json.Unmarshal(data, &s) != nil || validateClaudeSelection(s) != nil {
			jsonError(w, 404, "No valid saved Claude model selection")
			return
		}
		jsonResponse(w, 200, s)
		return
	}
	var s claudeSelection
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, catalogLimit))
	decoder.DisallowUnknownFields()
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || decoder.Decode(&s) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		jsonError(w, 400, "Invalid Claude setup JSON")
		return
	}
	if err := validateClaudeSelection(s); err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	caps := claudeCaps("2.1.251")
	if s.Mode == "installed" {
		caps = installedClaude()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	changed, err := saveClaudeProfile(dir, s, caps, a.config.Port, a.config.LocalKey)
	if err != nil {
		jsonError(w, 409, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"ok": true, "changed": changed, "profileDir": dir, "capabilities": caps})
}

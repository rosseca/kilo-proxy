package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/pelletier/go-toml/v2"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type xcodeInstallation struct {
	Available    bool               `json:"available"`
	Version      string             `json:"version"`
	Path         string             `json:"path"`
	Claude       claudeCapabilities `json:"claude"`
	CodexVersion string             `json:"codexVersion"`
}

func plistValue(path, key string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :"+key, path).Output()
	if err != nil || len(out) > 1024 {
		return ""
	}
	return strings.TrimSpace(string(out))
}
func detectXcode() xcodeInstallation {
	if runtime.GOOS != "darwin" {
		return xcodeInstallation{}
	}
	candidates := []string{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "/usr/bin/xcode-select", "-p").Output(); err == nil {
		p := strings.TrimSuffix(strings.TrimSpace(string(out)), "/Contents/Developer")
		if strings.HasSuffix(p, ".app") {
			candidates = append(candidates, p)
		}
	}
	candidates = append(candidates, "/Applications/Xcode.app")
	matches, _ := filepath.Glob("/Applications/Xcode*.app")
	candidates = append(candidates, matches...)
	for _, p := range candidates {
		if plistValue(filepath.Join(p, "Contents/Info.plist"), "CFBundleIdentifier") != "com.apple.dt.Xcode" {
			continue
		}
		manifest := filepath.Join(p, "Contents/PlugIns/IDEIntelligenceChat.framework/Versions/A/Resources/AgentVersions.plist")
		return xcodeInstallation{true, plistValue(filepath.Join(p, "Contents/Info.plist"), "CFBundleShortVersionString"), p, claudeCaps(plistValue(manifest, "claude:version")), plistValue(manifest, "codex:version")}
	}
	return xcodeInstallation{}
}

func (a *app) xcodeRootDir() (string, error) {
	if a.xcodeTestRoot != "" {
		return a.xcodeTestRoot, nil
	}
	if runtime.GOOS != "darwin" {
		return "", errors.New("Xcode agent profiles can only be prepared on macOS")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, "Library/Developer/Xcode/CodingAssistant")
	// Reject symlinks in existing ancestors as well as the profile itself.
	for p := root; p != home; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("Xcode configuration folders must be real directories")
		}
	}
	return root, nil
}
func (a *app) xcodeInfo() xcodeInstallation {
	if a.xcodeTestRoot != "" {
		return xcodeInstallation{Available: true, Version: "26.4.1", Claude: claudeCaps("2.1.59"), CodexVersion: "0.106.0"}
	}
	return detectXcode()
}
func decodeXcodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, catalogLimit))
	decoder.DisallowUnknownFields()
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || decoder.Decode(dst) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		jsonError(w, 400, "Invalid Xcode setup JSON")
		return false
	}
	return true
}
func (a *app) xcodeAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/xcode/info" {
		jsonResponse(w, 200, a.xcodeInfo())
		return
	}
	variant := strings.TrimPrefix(r.URL.Path, "/api/xcode/")
	if variant != "chat" && variant != "codex" && variant != "claude" {
		http.NotFound(w, r)
		return
	}
	dir := a.dir
	var install xcodeInstallation
	if variant != "chat" {
		root, err := a.xcodeRootDir()
		if err != nil {
			jsonError(w, 409, err.Error())
			return
		}
		install = a.xcodeInfo()
		if !install.Available {
			jsonError(w, 409, "Install Xcode and its coding agents first")
			return
		}
		dir = filepath.Join(root, map[string]string{"codex": "codex", "claude": "ClaudeAgentConfig"}[variant])
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.Method == "GET" {
		if variant != "chat" {
			info, err := os.Lstat(dir)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				jsonError(w, 404, "No safe saved Xcode profile")
				return
			}
		}
		name := "models.json"
		if variant == "chat" {
			name = "xcode-chat.json"
		}
		if variant == "claude" {
			name = "kilo-models.json"
		}
		data, err := readCatalogFile(filepath.Join(dir, name))
		if err != nil {
			jsonError(w, 404, "No saved setup for this Xcode variant")
			return
		}
		if variant == "codex" {
			if _, err = validateCatalog(data); err != nil {
				jsonError(w, 409, "Invalid saved Xcode catalog")
				return
			}
		} else {
			var s claudeSelection
			if json.Unmarshal(data, &s) != nil || validateClaudeSelection(s) != nil {
				jsonError(w, 409, "Invalid saved Xcode selection")
				return
			}
		}
		jsonResponse(w, 200, json.RawMessage(data))
		return
	}
	if variant == "codex" {
		var input struct {
			Catalog json.RawMessage `json:"catalog"`
		}
		if !decodeXcodeBody(w, r, &input) {
			return
		}
		if err := validateXcodeCodexCatalog(input.Catalog); err != nil {
			jsonError(w, 400, "Choose 1–50 valid Codex models and reasoning levels")
			return
		}
		changed, modelsChanged, err := saveCodexProfileWithToken(dir, input.Catalog, a.config.Port, a.config.LocalKey)
		if err != nil {
			jsonError(w, 409, err.Error())
			return
		}
		jsonResponse(w, 200, map[string]any{"ok": true, "profileDir": dir, "changed": changed || modelsChanged})
		return
	}
	var selection claudeSelection
	if !decodeXcodeBody(w, r, &selection) {
		return
	}
	if err := validateClaudeSelection(selection); err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	if variant == "chat" {
		// Chat model names are gateway IDs: there is no request ID remapping.
		for _, m := range selection.Models {
			if m.Effort != "" {
				jsonError(w, 400, "Chat does not expose reasoning overrides")
				return
			}
		}
		data, _ := json.MarshalIndent(selection, "", "  ")
		data = append(data, '\n')
		if err := os.MkdirAll(dir, 0700); err != nil {
			jsonError(w, 500, "Cannot create local configuration directory")
			return
		}
		f, err := prepareProfileFile(filepath.Join(dir, "xcode-chat.json"), data)
		if err == nil && f.changed && f.exists {
			err = atomicCatalogFile(f.path+".bak", f.old)
		}
		if err == nil && f.changed {
			err = atomicCatalogFile(f.path, f.new)
		}
		if err != nil {
			jsonError(w, 409, "Cannot safely save Xcode Chat selection")
			return
		}
		jsonResponse(w, 200, map[string]any{"ok": true, "profileDir": f.path, "changed": f.changed})
		return
	}
	selection.Mode = "installed" // Browser cannot declare a newer bundled agent.
	if !install.Claude.Picker && len(selection.Models) > 3 {
		jsonError(w, 400, "This bundled Claude version supports three model aliases; choose up to three models")
		return
	}
	for _, m := range selection.Models {
		if !install.Claude.PerModelEffort && (m.Effort == "xhigh" || m.Effort != "" && m.ID != selection.Initial) {
			jsonError(w, 400, "This bundled Claude version supports only the initial model's global effort")
			return
		}
	}
	changed, err := saveClaudeProfile(dir, selection, install.Claude, a.config.Port, a.config.LocalKey)
	if err != nil {
		jsonError(w, 409, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"ok": true, "profileDir": dir, "changed": changed, "capabilities": install.Claude})
}
func (a *app) xcodeChatModels(w http.ResponseWriter) {
	a.mu.Lock()
	defer a.mu.Unlock()
	data, err := readCatalogFile(filepath.Join(a.dir, "xcode-chat.json"))
	var s claudeSelection
	if err != nil || json.Unmarshal(data, &s) != nil || validateClaudeSelection(s) != nil {
		jsonError(w, 409, "Prepare Xcode Chat in Kilo Local first")
		return
	}
	models := make([]map[string]any, 0, len(s.Models))
	// Initial model first; Xcode still owns its active model selection.
	ordered := append([]claudeModel{}, s.Models...)
	for i, m := range ordered {
		if m.ID == s.Initial {
			ordered[0], ordered[i] = ordered[i], ordered[0]
			break
		}
	}
	for _, m := range ordered {
		models = append(models, map[string]any{"id": m.ID, "object": "model", "created": 0, "owned_by": "kilo", "name": m.DisplayName})
	}
	jsonResponse(w, 200, map[string]any{"object": "list", "data": models})
}

// Codex launched by Xcode cannot rely on a variable set in a terminal. Use only
// the local proxy credential in its protected config; never the upstream key.
func codexXcodeAuth(data []byte, key string) ([]byte, error) {
	var before map[string]any
	if err := toml.Unmarshal(data, &before); err != nil {
		return nil, err
	}
	var after map[string]any
	if err := toml.Unmarshal(data, &after); err != nil {
		return nil, err
	}
	providers, err := configTable(after, "model_providers")
	if err != nil {
		return nil, err
	}
	provider, err := configTable(providers, "kilo-local")
	if err != nil {
		return nil, err
	}
	delete(provider, "env_key")
	delete(provider, "env_key_instructions")
	provider["experimental_bearer_token"] = key
	return editCodexTOML(data, before, after)
}

func validateXcodeCodexCatalog(data []byte) error {
	if _, err := validateCatalog(data); err != nil {
		return err
	}
	var c struct {
		Models []struct {
			Levels []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	for _, m := range c.Models {
		for _, l := range m.Levels {
			if l.Effort == "max" || l.Effort == "ultra" {
				return errors.New("Xcode's verified Codex catalog supports reasoning only up to xhigh")
			}
		}
	}
	return nil
}

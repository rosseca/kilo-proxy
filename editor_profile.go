package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"
)

type editorModel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Context int    `json:"contextWindow"`
	Output  int    `json:"maxOutputTokens,omitempty"`
}
type editorSelection struct {
	Models  []editorModel `json:"models"`
	Initial string        `json:"initial"`
}

func validateEditorSelection(s editorSelection) error {
	if len(s.Models) < 1 || len(s.Models) > 50 {
		return errors.New("Choose 1–50 models.")
	}
	seen := map[string]bool{}
	for _, m := range s.Models {
		if !catalogID.MatchString(m.ID) || seen[m.ID] || len([]rune(m.Name)) > 80 || strings.IndexFunc(m.Name, func(r rune) bool { return r < 32 || r == 127 }) >= 0 || m.Context < 1024 || m.Context > 100000000 || m.Output < 0 || m.Output > m.Context {
			return errors.New("Invalid model ID, name, or context/output limit.")
		}
		seen[m.ID] = true
	}
	if !seen[s.Initial] {
		return errors.New("Choose an initial model from the selection.")
	}
	return nil
}

// Reject duplicate keys instead of guessing which meaning an editor will use.
func uniqueEditorJSON(v hujson.Value) bool {
	switch x := v.Value.(type) {
	case *hujson.Object:
		seen := map[string]bool{}
		for _, m := range x.Members {
			name := m.Name.Value.(hujson.Literal).String()
			if seen[name] || !uniqueEditorJSON(m.Value) {
				return false
			}
			seen[name] = true
		}
	case *hujson.Array:
		for _, m := range x.Elements {
			if !uniqueEditorJSON(m) {
				return false
			}
		}
	}
	return true
}
func mergeEditorSettings(old []byte, client string, s editorSelection, baseURL, key string) ([]byte, error) {
	data := old
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}\n")
	}
	tree, err := hujson.Parse(data)
	if err != nil || tree.Value.Kind() != '{' || !uniqueEditorJSON(tree) {
		return nil, errors.New("Settings must be valid JSON/JSONC with unique keys; nothing saved.")
	}
	// Set only owned leaf values, preserving comments, formatting, other providers,
	// hooks, permissions, and large number literals elsewhere in the document.
	set := func(path string, value any) error {
		parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
		prefix := ""
		for _, part := range parts[:len(parts)-1] {
			prefix += "/" + part
			current := tree.Find(prefix)
			if current == nil {
				patch, _ := json.Marshal([]map[string]any{{"op": "add", "path": prefix, "value": map[string]any{}}})
				if err := tree.Patch(patch); err != nil {
					return err
				}
			} else if current.Value.Kind() != '{' {
				return errors.New("A managed settings section is not an object; nothing saved.")
			}
		}
		raw, _ := json.Marshal(value)
		if current := tree.Find(path); current != nil {
			copy := current.Clone()
			copy.Standardize()
			var a, b any
			da := json.NewDecoder(bytes.NewReader(copy.Pack()))
			da.UseNumber()
			_ = da.Decode(&a)
			db := json.NewDecoder(bytes.NewReader(raw))
			db.UseNumber()
			_ = db.Decode(&b)
			aa, _ := json.Marshal(a)
			bb, _ := json.Marshal(b)
			if bytes.Equal(aa, bb) {
				return nil
			}
		}
		patch, _ := json.Marshal([]map[string]any{{"op": "add", "path": path, "value": value}})
		return tree.Patch(patch)
	}
	var fields []struct {
		path  string
		value any
	}
	if client == "opencode" {
		models := map[string]any{}
		for _, m := range s.Models {
			entry := map[string]any{"name": m.Name}
			if m.Output > 0 {
				entry["limit"] = map[string]int{"context": m.Context, "output": m.Output}
			}
			models[m.ID] = entry
		}
		fields = []struct {
			path  string
			value any
		}{{"/provider/kilo-local/npm", "@ai-sdk/openai-compatible"}, {"/provider/kilo-local/name", "Kilo Proxy"}, {"/provider/kilo-local/options/baseURL", baseURL}, {"/provider/kilo-local/options/apiKey", key}, {"/provider/kilo-local/models", models}, {"/model", "kilo-local/" + s.Initial}, {"/small_model", "kilo-local/" + s.Initial}}
	} else {
		models := []map[string]any{}
		for _, m := range s.Models {
			entry := map[string]any{"name": m.ID, "display_name": m.Name, "max_tokens": m.Context}
			if m.Output > 0 {
				entry["max_output_tokens"] = m.Output
			}
			models = append(models, entry)
		}
		fields = []struct {
			path  string
			value any
		}{{"/language_models/openai_compatible/kilo-local/api_url", zedBaseURL(baseURL, key)}, {"/language_models/openai_compatible/kilo-local/available_models", models}, {"/agent/default_model/provider", "kilo-local"}, {"/agent/default_model/model", s.Initial}}
	}
	for _, f := range fields {
		if err := set(f.path, f.value); err != nil {
			return nil, err
		}
	}
	result := tree.Pack()
	if len(result) > catalogLimit {
		return nil, errors.New("Settings exceed the size limit.")
	}
	return result, nil
}

func safeEditorDir(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("Unsafe settings directory.")
	}
	current := root
	for _, part := range append([]string{""}, strings.Split(rel, string(filepath.Separator))...) {
		current = filepath.Join(current, part)
		st, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err = os.Mkdir(current, 0700); err != nil {
				return errors.New("Cannot create settings directory.")
			}
			continue
		}
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("Settings directories must be real directories, not symbolic links.")
		}
	}
	return nil
}
func (a *app) editorPaths(client string) (string, string, string, error) {
	home := a.editorTestRoot
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", "", "", err
		}
	}
	root := home
	dir := filepath.Join(home, ".opencode-kilo")
	name := "opencode.json"
	if client == "zed" {
		base := filepath.Join(home, ".config")
		if a.editorTestRoot == "" {
			if runtime.GOOS == "windows" {
				var err error
				base, err = os.UserConfigDir()
				if err != nil {
					return "", "", "", err
				}
				dir = filepath.Join(base, "Zed")
			} else {
				if env := os.Getenv("XDG_CONFIG_HOME"); runtime.GOOS == "linux" && filepath.IsAbs(env) {
					base = env
				}
				dir = filepath.Join(base, "zed")
			}
		} else {
			dir = filepath.Join(base, "zed")
		}
		if rel, _ := filepath.Rel(home, base); rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			root = base
		}
		name = "settings.json"
	}
	return root, filepath.Join(dir, name), filepath.Join(dir, "kilo-models.json"), nil
}
func saveEditorFiles(files []profileFile) (bool, error) {
	for _, f := range files {
		if f.changed && f.exists {
			if err := atomicCatalogFile(f.path+".bak", f.old); err != nil {
				return false, errors.New("Could not back up settings; settings unchanged.")
			}
		}
	}
	changed := false
	for i, f := range files {
		if !f.changed {
			continue
		}
		if err := atomicCatalogFile(f.path, f.new); err != nil {
			restored := true
			for _, prior := range files[:i] {
				if prior.changed {
					var e error
					if prior.exists {
						e = atomicCatalogFile(prior.path, prior.old)
					} else {
						e = os.Remove(prior.path)
					}
					if e != nil {
						restored = false
					}
				}
			}
			if !restored {
				return false, errors.New("Save and rollback failed; restore the .bak files.")
			}
			return false, errors.New("Could not save settings; earlier writes restored.")
		}
		changed = true
	}
	return changed, nil
}
func (a *app) editorProfile(w http.ResponseWriter, r *http.Request) {
	client := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/editors/"), "/profile")
	if client != "zed" && client != "opencode" {
		jsonError(w, 404, "Unknown editor.")
		return
	}
	root, path, choices, err := a.editorPaths(client)
	if err != nil {
		jsonError(w, 500, "Cannot locate editor settings.")
		return
	}
	a.editorMu.Lock()
	defer a.editorMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.Method == "GET" {
		// Validate ancestors before reading saved state as well as before writing.
		resolved, e := filepath.EvalSymlinks(filepath.Dir(path))
		if e != nil {
			jsonError(w, 404, "No saved editor profile.")
			return
		}
		expected, e := filepath.EvalSymlinks(root)
		if e != nil {
			jsonError(w, 409, "Unsafe editor profile.")
			return
		}
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		if resolved != filepath.Join(expected, rel) {
			jsonError(w, 409, "Unsafe editor profile.")
			return
		}
		data, e := readCatalogFile(choices)
		var s editorSelection
		if e != nil {
			jsonError(w, 404, "No saved model selection.")
			return
		}
		if json.Unmarshal(data, &s) != nil || validateEditorSelection(s) != nil {
			jsonError(w, 409, "Invalid saved model selection.")
			return
		}
		jsonResponse(w, 200, map[string]any{"selection": s, "configPath": path})
		return
	}
	var s editorSelection
	if !decodeXcodeBody(w, r, &s) {
		return
	}
	if err = validateEditorSelection(s); err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	if err = safeEditorDir(root, filepath.Dir(path)); err != nil {
		jsonError(w, 409, err.Error())
		return
	}
	old, err := readCatalogFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		jsonError(w, 409, "Cannot safely read settings; nothing saved.")
		return
	}
	updated, err := mergeEditorSettings(old, client, s, "http://127.0.0.1:"+strconv.Itoa(a.config.Port)+"/v1", a.config.LocalKey)
	if err != nil {
		jsonError(w, 409, err.Error())
		return
	}
	selection, _ := json.MarshalIndent(s, "", "  ")
	selection = append(selection, '\n')
	settings, err := prepareProfileFile(path, updated)
	if err != nil {
		jsonError(w, 409, err.Error())
		return
	}
	saved, err := prepareProfileFile(choices, selection)
	if err != nil {
		jsonError(w, 409, err.Error())
		return
	}
	if client == "zed" {
		// Write the credential before changing api_url: Zed reloads it as soon
		// as the settings watcher observes the new URL. Never store the Kilo
		// account credential, and never serialize the local key into settings.
		rt := a.launchRuntime()
		executable, _ := rt.resolve("zed", "")
		port, key, store := a.config.Port, a.config.LocalKey, a.zedCredentialStore
		apiURL := zedBaseURL("http://127.0.0.1:"+strconv.Itoa(port)+"/v1", key)
		// Unlocking a system vault may show an OS prompt. It must not block
		// inference, session accounting or the rest of the application UI.
		a.mu.Unlock()
		storeErr := store(r.Context(), apiURL, key, executable)
		a.mu.Lock()
		if storeErr != nil || r.Context().Err() != nil {
			jsonError(w, 409, errZedCredentialStore.Error())
			return
		}
		if a.config.Port != port || a.config.LocalKey != key {
			jsonError(w, 409, "The proxy connection changed during Zed setup. Prepare Zed again.")
			return
		}
		for _, f := range []profileFile{settings, saved} {
			current, readErr := readCatalogFile(f.path)
			if (f.exists && (readErr != nil || !bytes.Equal(current, f.old))) || (!f.exists && !errors.Is(readErr, os.ErrNotExist)) {
				jsonError(w, 409, "Zed settings changed during credential setup. Prepare Zed again to keep your latest changes.")
				return
			}
		}
	}
	changed, err := saveEditorFiles([]profileFile{saved, settings})
	if err != nil {
		jsonError(w, 409, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"ok": true, "changed": changed, "configPath": path, "selection": s})
}

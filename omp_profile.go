package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"

	"gopkg.in/yaml.v3"
)

type ompModel struct {
	editorModel
	Reasoning        bool     `json:"reasoning,omitempty"`
	Effort           string   `json:"effort,omitempty"`
	ReasoningEfforts []string `json:"reasoningEfforts,omitempty"`
	InputModalities  []string `json:"inputModalities,omitempty"`
	InputPrice       *float64 `json:"inputPrice,omitempty"`
	OutputPrice      *float64 `json:"outputPrice,omitempty"`
}

type ompSelection struct {
	Models  []ompModel `json:"models"`
	Initial string     `json:"initial"`
}

var ompEfforts = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

func validateOMPSelection(s ompSelection) error {
	editor := editorSelection{Initial: s.Initial}
	for _, m := range s.Models {
		editor.Models = append(editor.Models, m.editorModel)
		if m.Effort != "" && !effortNames[m.Effort] {
			return errors.New("Choose a supported reasoning effort.")
		}
		if len(m.ReasoningEfforts) > 10 || len(m.InputModalities) > 10 {
			return errors.New("Invalid Oh My Pi model capabilities.")
		}
		for _, effort := range m.ReasoningEfforts {
			if !effortNames[effort] {
				return errors.New("Choose supported reasoning levels.")
			}
		}
		for _, price := range []*float64{m.InputPrice, m.OutputPrice} {
			if price != nil && (math.IsNaN(*price) || math.IsInf(*price, 0) || *price < 0) {
				return errors.New("Invalid model price.")
			}
		}
	}
	return validateEditorSelection(editor)
}

func ompModelEfforts(m ompModel) []string {
	var efforts []string
	if !m.Reasoning || m.Effort == "none" {
		return efforts
	}
	for _, effort := range ompEfforts {
		if slices.Contains(m.ReasoningEfforts, effort) {
			efforts = append(efforts, effort)
		}
	}
	return efforts
}

func buildOMPModels(s ompSelection, baseURL, key string) ([]byte, error) {
	if err := validateOMPSelection(s); err != nil {
		return nil, err
	}
	models := []any{}
	for _, m := range s.Models {
		input := []string{"text"}
		if slices.Contains(m.InputModalities, "image") {
			input = append(input, "image")
		}
		output := m.Output
		if output == 0 {
			output = min(8192, m.Context)
		}
		name := m.Name
		if name == "" {
			name = m.ID
		}
		efforts := ompModelEfforts(m)
		model := map[string]any{
			"id": m.ID, "name": name, "contextWindow": m.Context, "maxTokens": output,
			"input": input, "reasoning": len(efforts) > 0, "preferWebsockets": false,
		}
		if len(efforts) > 0 {
			thinking := map[string]any{"mode": "effort", "efforts": efforts}
			if slices.Contains(efforts, m.Effort) {
				thinking["defaultLevel"] = m.Effort
			}
			model["thinking"] = thinking
		}
		// OMP requires four prices together. The Kilo catalog does not supply
		// cache read/write rates; don't invent zero prices for its estimates.
		models = append(models, model)
	}
	return marshalOMPYAML(map[string]any{"providers": map[string]any{"kilo-local": map[string]any{
		"baseUrl": baseURL, "api": "openai-responses", "apiKey": key, "authHeader": true,
		"compat": map[string]any{"supportsStore": false, "supportsReasoningSummary": false, "supportsStrictMode": false},
		"models": models,
	}}})
}

func buildOMPSettings(s ompSelection) ([]byte, error) {
	if err := validateOMPSelection(s); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(s.Models))
	for _, m := range s.Models {
		models = append(models, "kilo-local/"+m.ID)
	}
	return marshalOMPYAML(map[string]any{
		"modelRoles":    map[string]any{"default": "kilo-local/" + s.Initial},
		"enabledModels": models, "defaultThinkingLevel": "auto",
		"startup": map[string]any{"setupWizard": false},
	})
}

func marshalOMPYAML(value any) ([]byte, error) {
	var out bytes.Buffer
	e := yaml.NewEncoder(&out)
	e.SetIndent(2)
	if err := e.Encode(value); err != nil {
		return nil, err
	}
	if err := e.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Node-based merges retain unrelated settings and comments. A malformed file
// or ambiguous YAML must never be overwritten just to make launch succeed.
func readOMPYAML(data []byte) (*yaml.Node, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	d := yaml.NewDecoder(bytes.NewReader(data))
	var doc, extra yaml.Node
	if err := d.Decode(&doc); err != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("Oh My Pi settings must contain one valid YAML mapping; nothing saved.")
	}
	if d.Decode(&extra) != io.EOF {
		return nil, errors.New("Oh My Pi settings cannot contain multiple YAML documents; nothing saved.")
	}
	if !safeOMPYAMLNodes(&doc) {
		return nil, errors.New("Expand YAML anchors, aliases and merge keys in the Oh My Pi profile before preparing it; nothing saved.")
	}
	var check map[string]any
	if err := doc.Decode(&check); err != nil {
		return nil, errors.New("Oh My Pi settings contain invalid or duplicate YAML keys; nothing saved.")
	}
	return doc.Content[0], nil
}

// Replacing an anchored managed value can orphan aliases elsewhere, while
// adding an explicit mapping can shadow inherited settings. Fail without
// writing until these uncommon constructs can be merged losslessly.
func safeOMPYAMLNodes(node *yaml.Node) bool {
	if node.Anchor != "" || node.Kind == yaml.AliasNode || node.Tag == "!!merge" {
		return false
	}
	for _, child := range node.Content {
		if !safeOMPYAMLNodes(child) {
			return false
		}
	}
	return true
}

func ompYAMLValue(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}

func setOMPYAML(parent *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			parent.Content[i+1] = value
			return
		}
	}
	parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func mergeOMPYAML(old, generated []byte, models bool) ([]byte, error) {
	root, err := readOMPYAML(old)
	if err != nil {
		return nil, err
	}
	wanted, err := readOMPYAML(generated)
	if err != nil {
		return nil, err
	}
	// Replace only our provider; merge managed config fields without erasing
	// the user's theme, hotkeys, other providers or role assignments.
	paths := [][2]string{{"modelRoles", "default"}, {"startup", "setupWizard"}}
	if models {
		paths = [][2]string{{"providers", "kilo-local"}}
	} else {
		for _, key := range []string{"enabledModels", "defaultThinkingLevel"} {
			setOMPYAML(root, key, ompYAMLValue(wanted, key))
		}
	}
	for _, path := range paths {
		parent := ompYAMLValue(root, path[0])
		if parent == nil {
			parent = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setOMPYAML(root, path[0], parent)
		}
		if parent.Kind != yaml.MappingNode {
			return nil, errors.New("Oh My Pi managed settings must be YAML mappings; nothing saved.")
		}
		setOMPYAML(parent, path[1], ompYAMLValue(ompYAMLValue(wanted, path[0]), path[1]))
	}
	return marshalOMPYAML(root)
}

func equalOMPYAML(a, b []byte) bool {
	x, e := readOMPYAML(a)
	y, f := readOMPYAML(b)
	if e != nil || f != nil {
		return false
	}
	var p, q map[string]any
	return x.Decode(&p) == nil && y.Decode(&q) == nil && reflect.DeepEqual(p, q)
}

func (a *app) ompPaths() (root, dir string, err error) {
	if a.ompProfileDir != "" {
		return filepath.Dir(a.ompProfileDir), a.ompProfileDir, nil
	}
	root, err = os.UserHomeDir()
	return root, filepath.Join(root, ".omp-kilo"), err
}

func mergeOMPImages(old []byte, images imageGenerationSettings, baseURL, key string) ([]byte, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(old)) > 0 {
		var err error
		doc, err = decodeObject(old)
		if err != nil {
			return nil, errors.New("Oh My Pi MCP settings must be a valid JSON object; nothing saved.")
		}
	}
	servers, exists := doc["mcpServers"]
	if !exists {
		servers = map[string]any{}
		doc["mcpServers"] = servers
	}
	list, ok := servers.(map[string]any)
	if !ok {
		return nil, errors.New("Oh My Pi mcpServers must be a JSON object; nothing saved.")
	}
	if images.Enabled {
		list["kilo-images"] = map[string]any{"type": "http", "url": baseURL + "/mcp/images", "headers": map[string]string{"Authorization": "Bearer " + key}, "timeout": 180000}
	} else {
		delete(list, "kilo-images")
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	return append(data, '\n'), err
}

// Caller holds a.mu. The same generation/merge path is used for preparation and
// launch validation so a changed port, key or image setting cannot launch stale.
func (a *app) ompProfileFiles(dir string, s ompSelection) ([]profileFile, error) {
	base := "http://127.0.0.1:" + strconv.Itoa(a.config.Port)
	models, err := buildOMPModels(s, base+"/v1", a.config.LocalKey)
	if err != nil {
		return nil, err
	}
	settings, err := buildOMPSettings(s)
	if err != nil {
		return nil, err
	}
	files := []profileFile{}
	for _, item := range []struct {
		name string
		data []byte
	}{{"models.yml", models}, {"config.yml", settings}, {"mcp.json", nil}} {
		path := filepath.Join(dir, item.name)
		old, readErr := readCatalogFile(path)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return nil, errors.New("Cannot safely read the Oh My Pi profile; nothing saved.")
		}
		var data []byte
		if item.name == "mcp.json" {
			data, err = mergeOMPImages(old, a.clientImageSettingsLocked(), base, a.config.LocalKey)
		} else {
			data, err = mergeOMPYAML(old, item.data, item.name == "models.yml")
		}
		if err != nil {
			return nil, err
		}
		f, err := prepareProfileFile(path, data)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

func (a *app) ompProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		jsonError(w, 405, "Method not allowed.")
		return
	}
	root, dir, err := a.ompPaths()
	if err != nil {
		jsonError(w, 500, "Cannot locate the Oh My Pi profile.")
		return
	}
	a.editorMu.Lock()
	defer a.editorMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.Method == http.MethodGet {
		if !safeLaunchDir(dir, root) {
			jsonError(w, 404, "No safe saved Oh My Pi profile.")
			return
		}
		data, err := readCatalogFile(filepath.Join(dir, "kilo-models.json"))
		var selection ompSelection
		if err != nil || json.Unmarshal(data, &selection) != nil || validateOMPSelection(selection) != nil {
			jsonError(w, 404, "No valid saved Oh My Pi selection.")
			return
		}
		jsonResponse(w, 200, map[string]any{"selection": selection, "profileDir": dir, "configPath": filepath.Join(dir, "models.yml")})
		return
	}
	var s ompSelection
	if !decodeXcodeBody(w, r, &s) {
		return
	}
	if err = validateOMPSelection(s); err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	changed, err := a.saveOMPProfile(s)
	if err != nil {
		jsonError(w, 409, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]any{"ok": true, "changed": changed, "selection": s, "profileDir": dir, "configPath": filepath.Join(dir, "models.yml")})
}

// Caller holds a.mu. Both GUI and current-terminal preparation use this
// transactional save so credentials, model settings and MCP stay in sync.
func (a *app) saveOMPProfile(s ompSelection) (bool, error) {
	if err := validateOMPSelection(s); err != nil {
		return false, err
	}
	root, dir, err := a.ompPaths()
	if err != nil {
		return false, err
	}
	if err := safeEditorDir(root, dir); err != nil {
		return false, err
	}
	files, err := a.ompProfileFiles(dir, s)
	if err != nil {
		return false, err
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	selection, err := prepareProfileFile(filepath.Join(dir, "kilo-models.json"), append(data, '\n'))
	if err != nil {
		return false, err
	}
	return saveEditorFiles(append(files, selection))
}

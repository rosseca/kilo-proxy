//go:build desktop

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
)

type nativeClients struct {
	CLIPreferences              nativeCLIPreferencesState
	Muse                        nativeMuseState
	OpenCodePreferences         nativeOpenCodePreferencesState
	ManagedTools                *nativeManagedTools
	Grids                       map[string]*nativeModelGridState
	Selections                  map[string]*nativeClientSelection
	Claude                      claudeCapabilities
	ClaudeChecked               bool
	ClaudeDetectStarted         bool
	Xcode                       xcodeInstallation
	XcodeChecked                bool
	XcodeDetectStarted          bool
	LaunchInfo                  nativeLaunchInfo
	LaunchChecked               bool
	LaunchDetectStarted         bool
	LaunchRefreshPending        bool
	LaunchError                 string
	Launching                   string
	Variant                     string
	OpenDesign                  nativeOpenDesignInfo
	OpenDesignChecked           bool
	OpenDesignDetectStarted     bool
	DesktopExperimentalRevision uint64
	DesktopExperimentalTarget   *bool
}

// A selection belongs to one editor/variant. Labels never replace gateway IDs.
type nativeClientSelection struct {
	DesktopExperimentalModels bool                     `json:"-"`
	ImageGeneration           *imageGenerationSettings `json:"imageGeneration,omitempty"`
	imageGenerationBaseline   *imageGenerationSettings
	QueueMode                 string `json:"followUpQueueMode,omitempty"`
	Models                    []nativeModelChoice
	Initial                   string
	Aliases                   map[string]string
	Mode                      string
	Saved                     string
	Path                      string
}

func (c *nativeClients) selection(key string) *nativeClientSelection {
	if c.Selections == nil {
		c.Selections = map[string]*nativeClientSelection{}
	}
	if c.Selections[key] == nil {
		c.Selections[key] = &nativeClientSelection{Aliases: map[string]string{}, Mode: "installed"}
		if key == "codex" {
			c.Selections[key].QueueMode = codexQueueModeQueue
		}
	}
	return c.Selections[key]
}

func (s *nativeClientSelection) choice(id string) *nativeModelChoice {
	for i := range s.Models {
		if s.Models[i].Model.ID == id {
			return &s.Models[i]
		}
	}
	return nil
}

func (s *nativeClientSelection) add(model modelInfo, limit int) error {
	if !catalogID.MatchString(model.ID) {
		return errors.New("Enter a valid exact model ID")
	}
	if s.choice(model.ID) != nil {
		return nil
	}
	if len(s.Models) >= limit {
		return fmt.Errorf("Choose at most %d models", limit)
	}
	if model.Name == "" {
		model.Name = model.ID
	}
	s.Models = append(s.Models, nativeModelChoice{Model: model, ContextPreset: contextPresetRecommended, MaximumOutputTokens: model.MaxOutputTokens})
	if s.Initial == "" {
		s.Initial = model.ID
	}
	return nil
}

func (s *nativeClientSelection) remove(id string) {
	for i, m := range s.Models {
		if m.Model.ID == id {
			s.Models = append(s.Models[:i], s.Models[i+1:]...)
			break
		}
	}
	if s.Initial == id {
		s.Initial = ""
		if len(s.Models) > 0 {
			s.Initial = s.Models[0].Model.ID
		}
	}
	for alias, target := range s.Aliases {
		if target == id {
			delete(s.Aliases, alias)
		}
	}
}

func (s *nativeClientSelection) ids() []string {
	ids := make([]string, 0, len(s.Models))
	for _, m := range s.Models {
		ids = append(ids, m.Model.ID)
	}
	return ids
}

func nativeClientEndpoint(key string) string {
	switch key {
	case "openmausbot":
		return "/api/clients/openmausbot"
	case "t3-code":
		return "/api/clients/t3-code"
	case "opencode2":
		return "/api/clients/opencode2"
	case "synara":
		return "/api/clients/synara"
	case "omnigent":
		return "/api/clients/omnigent"
	case "open-design":
		return openDesignProfileEndpoint
	case "codex", "codex-cli":
		return "/api/" + key + "/catalog"
	case "claude":
		return "/api/claude/profile"
	case "claude-desktop":
		return "/api/claude-desktop/profile"
	case "omp":
		return "/api/omp/profile"
	case "opencode", "zed":
		return "/api/editors/" + key + "/profile"
	case "xcode-chat", "xcode-codex", "xcode-claude":
		return "/api/xcode/" + strings.TrimPrefix(key, "xcode-")
	}
	return ""
}

func nativeClientPayload(key string, s *nativeClientSelection) (any, error) {
	if key == "openmausbot" || key == "t3-code" || key == "synara" || key == "omnigent" || key == "opencode2" {
		library := modelLibrary{SchemaVersion: 1, DefaultModel: s.Initial}
		for _, m := range s.Models {
			library.Models = append(library.Models, nativeLibraryItem(m))
		}
		if key == "t3-code" {
			return map[string]any{"library": library}, validateModelLibrary(library)
		}
		if key == "synara" {
			return map[string]any{"library": library}, validateModelLibrary(library)
		}
		return map[string]any{"library": library}, validateModelLibrary(library)
	}
	if key == "omp" {
		selection, err := nativeOMPSelection(s)
		if err != nil {
			return nil, err
		}
		return selection, validateOMPSelection(selection)
	}
	if key == "open-design" {
		library := modelLibrary{SchemaVersion: 1, DefaultModel: s.Initial}
		for _, m := range s.Models {
			library.Models = append(library.Models, nativeLibraryItem(m))
		}
		engine := s.Mode
		if !validOpenDesignEngine(engine) {
			engine = "codex-cli"
		}
		return openDesignPrepareRequest{Engine: engine, Library: &library}, validateModelLibrary(library)
	}
	if key == "codex" || key == "codex-cli" || key == "xcode-codex" {
		catalog, err := buildCodexCatalog(s.Models, s.Initial, key == "xcode-codex")
		payload := map[string]any{"catalog": json.RawMessage(catalog)}
		if key != "xcode-codex" && s.ImageGeneration != nil {
			images := *s.ImageGeneration
			payload["imageGeneration"] = images
			if imageErr := validateImageGenerationSettings(images); imageErr != nil && err == nil {
				err = imageErr
			}
		}
		if key == "codex" {
			mode := s.QueueMode
			if !validCodexQueueMode(mode) {
				mode = codexQueueModeQueue
			}
			payload["followUpQueueMode"] = mode
		}
		return payload, err
	}
	if key == "claude-desktop" {
		selection := editorSelection{Initial: s.Initial}
		for _, m := range s.Models {
			name := m.DisplayName
			if name == "" {
				name = m.Model.Name
			}
			selection.Models = append(selection.Models, editorModel{ID: m.Model.ID, Name: name})
		}
		return selection, validateClaudeDesktopSelectionMode(selection, s.DesktopExperimentalModels)
	}
	if key == "opencode" || key == "zed" {
		selection := editorSelection{Initial: s.Initial}
		for _, m := range s.Models {
			limits, err := contextPolicyForChoice(m)
			if err != nil {
				return nil, err
			}
			entry := editorModel{ID: m.Model.ID, Name: nativeCodexDisplayName(m), Context: limits.ContextWindow, Output: limits.MaxOutputTokens}
			if key == "opencode" {
				entry.Variants, entry.ReasoningEffort = nativeReasoningFor(m)
			}
			selection.Models = append(selection.Models, entry)
		}
		return selection, validateEditorSelection(selection)
	}
	selection := claudeSelection{Initial: s.Initial, Mode: s.Mode, Aliases: map[string]string{}}
	for alias, id := range s.Aliases {
		selection.Aliases[alias] = id
	}
	for _, m := range s.Models {
		effort := m.ClaudeEffort
		if key == "xcode-chat" {
			effort = ""
		}
		limits, err := contextPolicyForChoice(m)
		if err != nil {
			return nil, err
		}
		selection.Models = append(selection.Models, claudeModel{ID: m.Model.ID, DisplayName: m.DisplayName, Effort: effort, Context: limits.ContextWindow, Output: limits.MaxOutputTokens, Maximum: m.Model.ContextWindow})
	}
	if strings.HasPrefix(key, "xcode-") {
		selection.Mode = "installed"
	}
	if key == "xcode-claude" {
		ids := []string{s.Initial}
		for _, id := range s.ids() {
			if id != s.Initial {
				ids = append(ids, id)
			}
		}
		for i, alias := range []string{"sonnet", "opus", "haiku"} {
			if selection.Aliases[alias] == "" {
				selection.Aliases[alias] = s.Initial
				if i < len(ids) {
					selection.Aliases[alias] = ids[i]
				}
			}
		}
	}
	return selection, validateClaudeSelection(selection)
}

func nativeSelectionFingerprint(key string, s *nativeClientSelection, base, keyValue string, caps claudeCapabilities) string {
	payload, err := nativeClientPayload(key, s)
	if err != nil {
		return "invalid:" + err.Error()
	}
	values := []any{key, base, keyValue, payload, caps}
	if key == "claude-desktop" {
		values = append(values, s.DesktopExperimentalModels)
	}
	data, _ := json.Marshal(values)
	return string(data)
}

// Filter only the saved list, retaining its manual order and offline entries.
// Searching is a view operation: it must not remove hidden choices or defaults.
func nativeFilterSavedModels(models []modelInfo, selection *nativeClientSelection, query string) []modelInfo {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return models
	}
	var out []modelInfo
	for _, model := range models {
		name := model.Name
		if choice := selection.choice(model.ID); choice != nil {
			name += " " + choice.DisplayName
		}
		if strings.Contains(strings.ToLower(model.ID+" "+name), query) {
			out = append(out, model)
		}
	}
	return out
}

func nativeVisibleModels(catalog []modelInfo, s *nativeClientSelection, query string, selectedOnly, codingOnly bool, order, lab string) []modelInfo {
	all := map[string]modelInfo{}
	for _, m := range catalog {
		all[m.ID] = m
	}
	for _, m := range s.Models {
		all[m.Model.ID] = nativeCurrentModel(m.Model, all[m.Model.ID])
	}
	lab = normalizeModelLab(lab)
	query = strings.ToLower(strings.TrimSpace(query))
	var out []modelInfo
	for _, m := range all {
		if lab != "" && modelLab(m) != lab {
			continue
		}
		choice := s.choice(m.ID)
		name := m.Name
		if choice != nil {
			name += " " + choice.DisplayName
		}
		if selectedOnly && choice == nil {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(m.ID+" "+name), query) {
			continue
		}
		if codingOnly && choice == nil {
			text := false
			for _, kind := range m.OutputModalities {
				if kind == "text" {
					text = true
				}
			}
			if m.Tools == nil || !*m.Tools || !text {
				continue
			}
		}
		out = append(out, m)
	}
	displayName := func(id string) string {
		if choice := s.choice(id); choice != nil {
			return choice.DisplayName
		}
		return ""
	}
	sort.Slice(out, func(i, j int) bool {
		return compareModelOrder(out[i], out[j], order, displayName(out[i].ID), displayName(out[j].ID)) < 0
	})
	return out
}

// Refresh published capacity while retaining the editable output preference.
// Context policy lives on nativeModelChoice, independently of catalog metadata.
func nativeCurrentModel(saved, current modelInfo) modelInfo {
	if current.ID == "" {
		return saved
	}
	current.MaxOutputTokens = saved.MaxOutputTokens
	return current
}

func (u *nativeUI) clientState() *nativeClients {
	if u.clients == nil {
		u.clients = &nativeClients{Variant: "chat"}
	}
	if u.client == "" {
		u.client = "codex"
	}
	if u.value("clients-shell") == "" {
		u.setValue("clients-shell", "unix")
	}
	if u.value("clients-platform") == "" {
		platform := "linux"
		if runtime.GOOS == "darwin" {
			platform = "macos"
		} else if runtime.GOOS == "windows" {
			platform = "windows"
			u.setValue("clients-shell", "powershell")
		}
		u.setValue("clients-platform", platform)
	}
	if u.value("clients-app-path") == "" && u.value("clients-platform") == "macos" {
		u.setValue("clients-app-path", "/Applications/ChatGPT.app")
	}
	return u.clients
}

func nativeClientField(key, id, field string) string { return "client:" + key + ":" + id + ":" + field }

func (u *nativeUI) seedClientChoice(key string, m nativeModelChoice) {
	id := m.Model.ID
	u.setValue(nativeClientField(key, id, "name"), m.DisplayName)
	_, initial := nativeReasoningFor(m)
	if key == sharedModelKey && m.DefaultReasoning != "" {
		initial = m.DefaultReasoning
	}
	u.setValue(nativeClientField(key, id, "reasoning"), initial)
	u.setValue(nativeClientField(key, id, "claude-effort"), m.ClaudeEffort)
	u.setValue(nativeClientField(key, id, "context"), strconv.Itoa(m.ContextTokens))
	u.setValue(nativeClientField(key, id, "output"), strconv.Itoa(m.Model.MaxOutputTokens))
	u.setChecked(nativeClientField(key, id, "custom"), m.ReasoningCustom)
	levels, _ := nativeReasoningFor(m)
	u.setValue(nativeClientField(key, id, "levels"), strings.Join(levels, ","))
}

func (u *nativeUI) syncClientSelection(key string, s *nativeClientSelection) {
	u.seedClientImages(key, s)
	catalog := make(map[string]modelInfo, len(u.models))
	for _, m := range u.models {
		catalog[m.ID] = m
	}
	for i := range s.Models {
		m := &s.Models[i]
		id := m.Model.ID
		if current, exists := catalog[id]; exists {
			m.MaximumOutputTokens = current.MaxOutputTokens
		}
		m.Model = nativeCurrentModel(m.Model, catalog[id])
		m.DisplayName = u.value(nativeClientField(key, id, "name"))
		if key == sharedModelKey || key == "opencode" || key == "zed" {
			m.ContextTokens, _ = strconv.Atoi(u.value(nativeClientField(key, id, "context")))
			m.Model.MaxOutputTokens, _ = strconv.Atoi(u.value(nativeClientField(key, id, "output")))
		}
		if key == sharedModelKey || key == "codex" || key == "codex-cli" || key == "xcode-codex" {
			m.ReasoningCustom = u.checked(nativeClientField(key, id, "custom"))
			if m.ReasoningCustom {
				m.ReasoningLevels = []string{}
				for _, level := range strings.Split(u.value(nativeClientField(key, id, "levels")), ",") {
					if level = strings.TrimSpace(level); level != "" {
						m.ReasoningLevels = append(m.ReasoningLevels, level)
					}
				}
			}
			m.DefaultReasoning = u.value(nativeClientField(key, id, "reasoning"))
		}
		if key == "claude" || key == "xcode-claude" {
			m.ClaudeEffort = u.value(nativeClientField(key, id, "claude-effort"))
		}
	}
	if key == "claude" {
		s.Mode = u.value("clients-claude-mode")
		if s.Mode == "" {
			s.Mode = "installed"
		}
	}
	if key == "codex" {
		s.QueueMode = u.value("client:codex:queue-mode")
		if !validCodexQueueMode(s.QueueMode) {
			s.QueueMode = codexQueueModeQueue
		}
	}
	for _, alias := range []string{"sonnet", "opus", "haiku"} {
		s.Aliases[alias] = u.value("client:" + key + ":alias:" + alias)
	}
}

func (u *nativeUI) clientCaps(key string) claudeCapabilities {
	if key == "xcode-claude" {
		return u.clientState().Xcode.Claude
	}
	if key == "claude" {
		if u.value("clients-claude-mode") == "modern" {
			return claudeCaps("2.1.251")
		}
		return u.clientState().Claude
	}
	return claudeCapabilities{}
}

func (u *nativeUI) clientBase() (string, string, int) {
	base, _ := u.state["baseURL"].(string)
	key, _ := u.state["localKey"].(string)
	port := 8877
	switch v := u.state["port"].(type) {
	case float64:
		port = int(v)
	case int:
		port = v
	}
	if base == "" {
		base = "http://127.0.0.1:" + strconv.Itoa(port) + "/v1"
	}
	return base, key, port
}

func (u *nativeUI) clientsPanel() layout.Widget {
	c := u.clientState()
	if !c.LaunchDetectStarted {
		u.detectLaunchers()
	}
	if u.client == "claude" && !c.ClaudeDetectStarted {
		u.detectAgentCapabilities()
	}
	if u.client == "xcode" && !c.XcodeDetectStarted {
		c.XcodeDetectStarted = true
		generation := c.CLIPreferences.Generation
		u.call("GET", "/api/xcode/info", nil, func(data json.RawMessage) {
			if generation != c.CLIPreferences.Generation {
				c.XcodeDetectStarted = false
				return
			}
			if json.Unmarshal(data, &c.Xcode) == nil {
				c.XcodeChecked = true
			}
		})
	}
	if key := u.client; key != "xcode" && key != "generic" {
		if key == "opencode" {
			if !c.OpenCodePreferences.Requested {
				u.refreshOpenCodePreferencesAfter(nil)
			}
			key = u.openCodeClientKey()
		}
		s := u.sharedClientSelection(key)
		u.syncClientSelection(key, s)
		return u.uxhHarnessPage(key, s, u.uxhClientDetail(key, s))
	}
	clientNames := map[string]string{"xcode": "Xcode", "generic": u.tr("Other clients", "Otros clientes")}
	widgets := []layout.Widget{
		u.uxhBackLink(),
		u.heading(clientNames[u.client]),
	}
	key := u.client
	launchSetup := []layout.Widget{}
	if key == "xcode" {
		variantChoices := []nativeChoice{{Value: "chat", Label: "Chat"}, {Value: "codex", Label: "Codex"}, {Value: "claude", Label: "Claude"}}
		launchSetup = append(launchSetup,
			u.segmented("xcode-variant:", variantChoices, c.Variant, true, func(variant string) { c.Variant = variant }),
			u.iconButton("xcode-detect", u.tr("Detect Xcode", "Detectar Xcode"), nativeButtonGhost, nativeIconRefresh, func() {
				u.call("GET", "/api/xcode/info", nil, func(data json.RawMessage) {
					if json.Unmarshal(data, &c.Xcode) == nil {
						c.XcodeChecked = true
					}
				})
			}),
		)
		if !c.XcodeChecked {
			launchSetup = append(launchSetup, u.statusBadge(nativeToneInfo, u.tr("Checking Xcode…", "Comprobando Xcode…")))
		} else if c.Xcode.Available {
			launchSetup = append(launchSetup, u.note(fmt.Sprintf("Xcode %s · Codex %s · Claude %s", c.Xcode.Version, c.Xcode.CodexVersion, c.Xcode.Claude.Version)))
		} else {
			launchSetup = append(launchSetup, u.statusBadge(nativeToneWarning, u.tr("Xcode not found", "Xcode no encontrado")))
		}
		key = "xcode-" + c.Variant
		if c.Variant != "chat" {
			launchSetup = append(launchSetup, u.hint(u.tr("Close Xcode before preparing its profile; reopen it and start a new conversation afterwards.", "Cierra Xcode antes de preparar el perfil; vuelve a abrirlo e inicia una conversación nueva.")))
		}
	}
	s := u.sharedClientSelection(key)
	u.syncClientSelection(key, s)
	protocol := u.tr("Uses Chat Completions; listing models does not verify generation or tools.", "Usa Chat Completions; mostrar modelos no verifica la generación ni las herramientas.")
	if key == "xcode-codex" {
		protocol = u.tr("Uses Responses; choose models compatible with their supported reasoning levels.", "Usa Responses; elige modelos compatibles con sus niveles de razonamiento.")
	}
	if key == "xcode-claude" {
		protocol = u.tr("Uses Anthropic Messages; selected models and task aliases must support it.", "Usa Anthropic Messages; los modelos y alias de tareas deben admitirlo.")
	}
	modelWidgets := []layout.Widget{
		u.actionRow(u.column(u.eyebrow(u.tr("Shared models", "Modelos compartidos")), u.label(u.uxhModelsSummary())), u.button("agents.models.edit", u.tr("Edit in Models", "Editar en Modelos"), func() { u.page = "models" })),
	}
	widgets = append(widgets, u.section(u.tr("Models", "Modelos"), u.tr("One shared library for this app.", "Una biblioteca compartida para esta app."), modelWidgets...))
	if key == "generic" {
		base, local, _ := u.clientBase()
		id := s.Initial
		guide := "Base URL: " + base + "\nAPI key: kl_local_••••••••\nModel: " + id
		launch := u.section(u.tr("Launch", "Arranque"), u.tr("Use these local OpenAI-compatible connection details.", "Usa estos datos de conexión local compatibles con OpenAI."), u.code("generic-guide", guide), u.pills(u.iconButton("generic-copy", u.tr("Copy connection", "Copiar conexión"), nativeButtonSecondary, nativeIconCopy, func() { u.copy("Base URL: " + base + "\nAPI key: " + local + "\nModel: " + s.Initial) })))
		advanced := u.section(u.tr("Advanced", "Avanzado"), u.tr("Details for manual client configuration.", "Datos para configurar el cliente manualmente."), u.note(u.tr("The local API key is separate from your upstream Kilo account key.", "La clave API local es distinta de la clave de tu cuenta Kilo.")))
		return u.column(append(widgets, launch, advanced)...)
	}
	if key == "xcode-claude" {
		aliases := []layout.Widget{}
		for _, alias := range []string{"sonnet", "opus", "haiku"} {
			aliases = append(aliases, u.selectField("client:"+key+":alias:"+alias, alias+u.tr(" alias (empty = initial)", " alias (vacío = inicial)"), nativeChoices(append([]string{""}, s.ids()...))))
		}
		launchSetup = append(launchSetup, u.row(aliases...))
		if !u.clientCaps(key).Picker {
			launchSetup = append(launchSetup, u.note(u.tr("This version uses model aliases; short picker names need a newer Claude.", "Esta versión usa alias; los nombres cortos requieren un Claude más reciente.")))
		}
	}
	return u.column(append(widgets, u.clientActions(key, s, protocol, launchSetup...))...)
}

func (u *nativeUI) claudeDesktopModelSummary(s *nativeClientSelection) string {
	if s.DesktopExperimentalModels {
		name := s.Initial
		if initial := s.choice(s.Initial); initial != nil {
			name = nativeCodexDisplayName(*initial)
		}
		if name == "" {
			return u.tr("Experimental models · Add a model to your shared library.", "Modelos experimentales · Añade un modelo a tu biblioteca compartida.")
		}
		if len(s.Models) == 1 {
			return fmt.Sprintf(u.tr("Experimental models · 1 selected · Starts with %s", "Modelos experimentales · 1 seleccionado · Empieza con %s"), name)
		}
		return fmt.Sprintf(u.tr("Experimental models · %d selected · Starts with %s", "Modelos experimentales · %d seleccionados · Empieza con %s"), len(s.Models), name)
	}
	if len(s.Models) == 0 {
		return u.tr("Add a Claude model to your shared library.", "Añade un modelo Claude a tu biblioteca compartida.")
	}
	name := s.Initial
	if initial := s.choice(s.Initial); initial != nil {
		name = nativeCodexDisplayName(*initial)
	}
	if len(s.Models) == 1 {
		return fmt.Sprintf(u.tr("1 Claude model · Starts with %s", "1 modelo Claude · Empieza con %s"), name)
	}
	return fmt.Sprintf(u.tr("%d Claude models · Starts with %s", "%d modelos Claude · Empieza con %s"), len(s.Models), name)
}

func (u *nativeUI) claudeDesktopSelectionNote(s *nativeClientSelection) string {
	source := u.library.selection
	omitted := len(source.Models) - len(s.Models)
	text := u.tr("Claude models are used by default. Enable experimental models in Options to use other providers.", "Por defecto se usan modelos Claude. Activa los modelos experimentales en Opciones para usar otros proveedores.")
	if s.DesktopExperimentalModels {
		text = u.tr("Experimental support for other providers is enabled. Model and tool compatibility, context and reasoning support may be limited. Preparing a configuration does not verify inference.", "El soporte experimental para otros proveedores está activado. La compatibilidad de modelos y herramientas, el contexto y el razonamiento pueden ser limitados. Preparar una configuración no verifica la inferencia.")
	}
	if omitted == 1 {
		text += " " + u.tr("1 shared model omitted; it remains available to other apps.", "1 modelo compartido omitido; sigue disponible para otras apps.")
	} else if omitted > 1 {
		text += " " + fmt.Sprintf(u.tr("%d shared models omitted; they remain available to other apps.", "%d modelos compartidos omitidos; siguen disponibles para otras apps."), omitted)
	}
	if s.Initial != "" && s.Initial != source.Initial {
		if s.DesktopExperimentalModels {
			text += " " + u.tr("The first compatible model is used because your shared default is unsupported.", "Se usa el primer modelo compatible porque el predeterminado compartido no es compatible.")
		} else {
			text += " " + u.tr("The first Claude model is used because your shared default is unsupported.", "Se usa el primer modelo Claude porque el predeterminado compartido no es compatible.")
		}
	}
	return text
}

// claudeDesktopDetail adds the experimental-models option and profile notes.
func (u *nativeUI) claudeDesktopDetail(s *nativeClientSelection) uxhDetail {
	const key = "claude-desktop"
	_, validation := nativeClientPayload(key, s)
	working := u.uxhProfileWorking(key)
	canPrepare := len(s.Models) > 0 && validation == nil && !working
	d := uxhDetail{canPrepare: &canPrepare}
	ready := u.uxhProfileReady(key, s)
	d.prepareNote = u.tr("Kilo configuration not prepared", "Configuración Kilo sin preparar")
	if ready {
		d.prepareNote = u.tr("Kilo configuration ready", "Configuración Kilo lista") + " · " + s.Path
	} else if s.Saved != "" {
		d.prepareNote = u.tr("Changes need preparing", "Hay cambios por preparar")
	}
	if len(s.Models) > 0 && validation != nil {
		d.status = append(d.status, u.message(nativeToneError, nativeMessage(validation.Error(), u.language)))
	}
	u.setChecked("client:claude-desktop:experimental", u.claudeDesktopExperimentalModels())
	experimental := u.disabled(!working, u.check("client:claude-desktop:experimental", u.tr("Experimental: use models from other providers", "Experimental: usar modelos de otros proveedores"), u.setClaudeDesktopExperimentalModels))
	if u.chatGPTConnected() {
		experimental = u.note(u.tr("Experimental aliases are required for ChatGPT models. This does not change your saved Kilo preference.", "Los alias experimentales son necesarios para los modelos de ChatGPT. No cambia tu preferencia guardada de Kilo."))
	}
	d.onOpen = append(d.onOpen, u.uxhKV(u.tr("Other providers", "Otros proveedores"), u.column(experimental, u.note(u.claudeDesktopSelectionNote(s)))))
	notesID := "client:claude-desktop:notes"
	notes := []layout.Widget{u.disclosure(notesID, u.tr("Profile details", "Detalles del perfil"))}
	if u.expanded[notesID] {
		notes = append(notes,
			u.note(u.tr("Context and output limits are managed by Claude Desktop and the model. Shared context presets are not applied.", "Claude Desktop y el modelo gestionan los límites de contexto y salida. Los preajustes de contexto compartidos no se aplican.")),
			u.note(u.tr("Application data and Code authentication are separate. Existing history and sign-in are not copied. This is a data profile, not an operating-system sandbox.", "Los datos de la app y la autenticación de Code están separados. No se copian el historial ni la sesión existentes. Es un perfil de datos, no un aislamiento del sistema operativo.")),
			u.note(u.tr("Claude disables Chrome-extension pairing with relocated data. Cowork and updates have not been verified with this profile. A real Windows Desktop session has not been tested.", "Claude desactiva el enlace con la extensión de Chrome al cambiar la ubicación de los datos. Cowork y las actualizaciones no se han verificado con este perfil. No se ha probado una sesión real de Desktop en Windows.")),
		)
	}
	d.advanced = append(d.advanced, u.uxhAdvancedRow(u.tr("Separate Kilo profile", "Perfil Kilo aparte"), u.tr("Open regular Claude from its usual icon; both can stay open.", "Abre Claude normal desde su icono habitual; ambos pueden seguir abiertos."), notes))
	return d
}

func (u *nativeUI) claudeDesktopExperimentalModels() bool {
	if u.chatGPTConnected() {
		return true
	}
	if target := u.clientState().DesktopExperimentalTarget; target != nil {
		return *target
	}
	return nativeBool(u.state, "claudeDesktopExperimentalModels")
}

func nativeClaudeDesktopModelAllowed(id string, experimental bool) bool {
	return catalogID.MatchString(id) && !claudeDesktopReservedAlias(id) && (experimental || claudeDesktopModelSupported(id))
}

func (u *nativeUI) setClaudeDesktopExperimentalModels(enabled bool) {
	if u.chatGPTConnected() {
		return
	}
	c := u.clientState()
	if u.busy["POST/api/claude-desktop/options"] || c.Launching != "" {
		return
	}
	previous := u.claudeDesktopExperimentalModels()
	c.DesktopExperimentalRevision++
	c.DesktopExperimentalTarget = &enabled
	u.clientRequest("POST", "/api/claude-desktop/options", map[string]bool{"experimentalModels": enabled}, func(data json.RawMessage, err error) {
		var response struct {
			ExperimentalModels bool `json:"experimentalModels"`
		}
		if err == nil {
			err = json.Unmarshal(data, &response)
		}
		c.DesktopExperimentalTarget = nil
		if err != nil {
			u.state["claudeDesktopExperimentalModels"] = previous
			u.sharedClientSelection("claude-desktop")
			u.noticeError(err)
			return
		}
		u.state["claudeDesktopExperimentalModels"] = response.ExperimentalModels
		u.sharedClientSelection("claude-desktop")
		u.refreshState()
	})
}

func (u *nativeUI) clientPicker(key string, s *nativeClientSelection) layout.Widget {
	prefix := "client:" + key + ":"
	shared := key == sharedModelKey
	catalog := !shared || u.expanded["library.catalog"]
	limit := 50
	if key == "generic" {
		limit = 1
	}
	if key == "xcode-claude" && !u.clientCaps(key).Picker {
		limit = 3
	}
	add := func(m modelInfo) bool {
		if !catalogID.MatchString(m.ID) {
			u.setNotice(nativeToneWarning, u.tr("Enter a valid exact model ID.", "Introduce un ID de modelo exacto válido."))
			return false
		}
		if key == "generic" && s.choice(m.ID) == nil {
			s.Models = nil
			s.Initial = ""
		}
		if err := s.add(m, limit); err != nil {
			u.noticeError(err)
			return false
		}
		u.seedClientChoice(key, *s.choice(m.ID))
		return true
	}
	order := modelSortOrder(u.value("models.sort"))
	available := nativeVisibleModels(u.models, s, u.value(prefix+"search"), u.checked(prefix+"selected"), u.checked(prefix+"coding"), order, u.value("models.lab"))
	controls := []layout.Widget{u.modelPickerToolbar(prefix, s), u.pills(u.button(prefix+"select-all", u.tr("Select results", "Marcar resultados"), func() {
		for _, m := range available {
			if len(s.Models) >= limit {
				break
			}
			if s.choice(m.ID) == nil {
				add(m)
			}
		}
	}), u.ghostButton(prefix+"clear", u.tr("Clear selection", "Vaciar selección"), func() {
		s.Models = nil
		s.Initial = ""
		s.Aliases = map[string]string{}
		u.setChecked(prefix+"selected", false)
		for _, alias := range []string{"sonnet", "opus", "haiku"} {
			u.setValue("client:"+key+":alias:"+alias, "")
		}
	})), u.note(fmt.Sprintf(u.tr("%d selected · %d matching · up to %d models", "%d seleccionados · %d resultados · hasta %d modelos"), len(s.Models), len(available), limit))}
	compactSetup := shared && catalog && u.page == "setup" && !u.expanded["setup.filters"]
	if shared && catalog && u.page == "setup" {
		label := u.tr("Filters & options", "Filtros y opciones")
		if u.checked(prefix+"selected") || u.checked(prefix+"coding") || u.value("models.lab") != "" {
			label = u.tr("Filters active", "Filtros activos")
		}
		if !compactSetup {
			label = u.tr("Hide filters", "Ocultar filtros")
		}
		toggle := u.disclosure("setup.filters", label)
		if compactSetup {
			search := u.modelSearchField(prefix+"search", u.tr("Search models", "Buscar modelos"))
			refresh := u.iconButton(prefix+"refresh", u.tr("Refresh catalog", "Actualizar catálogo"), nativeButtonGhost, nativeIconRefresh, u.refreshModels)
			toolbar := u.actionRow(search, refresh, toggle)
			if u.catalogCached {
				toolbar = u.column(u.actionRow(search, toggle), u.actionRow(u.message(nativeToneInfo, u.tr("Using the saved catalog. Refresh to check current prices and availability.", "Usando el catálogo guardado. Actualiza para comprobar precios y disponibilidad.")), refresh))
			}
			controls = []layout.Widget{toolbar, controls[len(controls)-1]}
		} else {
			controls = append([]layout.Widget{u.pills(toggle)}, controls...)
		}
	}
	if shared && !catalog {
		saved := u.sharedModelOrder()
		available = nativeFilterSavedModels(saved, s, u.value(prefix+"search"))
		search := u.modelSearchField(prefix+"search", u.tr("Search models", "Buscar modelos"))
		toolbar := []layout.Widget{search}
		if u.value(prefix+"search") != "" {
			toolbar = append(toolbar, u.ghostButton(prefix+"search-clear", u.tr("Clear search", "Limpiar búsqueda"), func() {
				u.setValue(prefix+"search", "")
			}))
		}
		controls = []layout.Widget{
			u.actionRow(toolbar[0], toolbar[1:]...),
			u.note(fmt.Sprintf(u.tr("%d of %d saved models", "%d de %d modelos guardados"), len(available), len(saved))),
			u.pills(u.disclosure(prefix+"advanced", u.tr("Advanced model options", "Opciones avanzadas de modelos"))),
		}
	} else if !compactSetup {
		controls = append(controls, u.note(u.modelSortHint(order)))
	}
	cards := []layout.Widget{}
	cardIDs := []string{}
	currentByok := u.modelByokIDs()
	for i, model := range available {
		if i >= 150 {
			break
		}
		m := model
		id := m.ID
		choice := s.choice(id)
		label := m.Name
		if choice != nil && choice.DisplayName != "" {
			label = choice.DisplayName
		}
		if label == "" {
			label = id
		}
		u.setChecked(prefix+"choose:"+id, choice != nil)
		heading := []layout.Widget{u.eyebrow(modelLabLabel(modelLab(m))), u.check(prefix+"choose:"+id, label, func(checked bool) {
			if checked {
				add(m)
			} else {
				s.remove(id)
			}
		}), u.note(id)}
		if badge := u.modelByokBadge(m, currentByok); badge != nil {
			heading = append(heading, badge)
		}
		row := []layout.Widget{u.modelCardHeading(heading...), u.modelPriceCells(m)}
		if metric := u.modelSortMetric(m, order); metric != "" && (!shared || catalog || u.expanded[prefix+"advanced"]) {
			row = append(row, u.note(metric))
		}
		if m.MayTrain != nil && *m.MayTrain {
			row = append(row, u.note(u.tr("Kilo reports that this model may use prompts for training.", "Kilo indica que este modelo puede usar los mensajes para entrenamiento.")))
		}
		if m.ExpirationDate != "" {
			row = append(row, u.note(u.tr("Published retirement date: ", "Fecha de retirada publicada: ")+m.ExpirationDate))
		}
		if u.expanded[prefix+"advanced"] {
			details := []string{}
			if m.ContextWindow > 0 {
				details = append(details, fmt.Sprintf(u.tr("Context: %d tokens", "Contexto: %d tokens"), m.ContextWindow))
			}
			if m.MaxOutputTokens > 0 {
				details = append(details, fmt.Sprintf(u.tr("Max output: %d tokens", "Salida máxima: %d tokens"), m.MaxOutputTokens))
			}
			if len(m.InputModalities) > 0 {
				details = append(details, u.tr("Input: ", "Entrada: ")+strings.Join(m.InputModalities, ", "))
			}
			if len(m.OutputModalities) > 0 {
				details = append(details, u.tr("Output: ", "Salida: ")+strings.Join(m.OutputModalities, ", "))
			}
			if m.Tools != nil {
				text := u.tr("Tools: unavailable", "Herramientas: no disponibles")
				if *m.Tools {
					text = u.tr("Tools: supported", "Herramientas: compatibles")
				}
				details = append(details, text)
			}
			if len(details) > 0 {
				row = append(row, u.note(strings.Join(details, " · ")))
			}
		}
		if choice != nil && !shared {
			defaultLabel := u.tr("Use on startup", "Usar al iniciar")
			initial := s.Initial == id
			if initial {
				defaultLabel = u.tr("Initial model", "Modelo inicial")
			}
			options := []layout.Widget{}
			primary := []layout.Widget{u.field(nativeClientField(key, id, "name"), u.tr("Display name", "Nombre visible"), m.Name, false), u.buttonKind(prefix+"initial:"+id, defaultLabel, nativeButtonSecondary, nil, initial, func() {
				s.Initial = id
				if (key == "claude" || key == "xcode-claude") && !u.clientCaps(key).PerModelEffort {
					for _, other := range s.Models {
						if other.Model.ID != id {
							u.setValue(nativeClientField(key, other.Model.ID, "claude-effort"), "")
						}
					}
				}
			})}
			if shared || key == "codex" || key == "codex-cli" || key == "xcode-codex" {
				levels, _ := nativeReasoningFor(*choice)
				if shared && !choice.ReasoningCustom && choice.DefaultReasoning != "" && !helperContains(levels, choice.DefaultReasoning) {
					levels = append(levels, choice.DefaultReasoning)
				}
				if shared && choice.ReasoningCustom && len(levels) == 0 {
					u.setValue(nativeClientField(key, id, "reasoning"), "")
				}
				if key == "xcode-codex" {
					filtered := []string{}
					for _, level := range levels {
						if level != "max" && level != "ultra" {
							filtered = append(filtered, level)
						}
					}
					levels = filtered
				}
				if len(levels) > 0 {
					primary = append(primary, u.selectField(nativeClientField(key, id, "reasoning"), u.tr("Initial reasoning", "Razonamiento inicial"), nativeModelReasoningChoices(u, levels)))
				} else {
					options = append(options, u.note(u.tr("No supported reasoning levels are configured.", "No hay niveles de razonamiento configurados.")))
				}
				if u.expanded[prefix+"advanced"] {
					options = append(options, u.check(nativeClientField(key, id, "custom"), u.tr("Customize supported reasoning levels", "Personalizar niveles de razonamiento"), func(bool) {}))
					if u.checked(nativeClientField(key, id, "custom")) {
						options = append(options, u.field(nativeClientField(key, id, "levels"), u.tr("Levels separated by commas", "Niveles separados por comas"), "low,medium,high", false))
					}
					options = append(options, u.button(prefix+"suggest:"+id, u.tr("Restore suggested levels", "Restaurar niveles sugeridos"), func() {
						if target := s.choice(id); target != nil {
							target.ReasoningCustom = false
							target.DefaultReasoning = ""
							u.setChecked(nativeClientField(key, id, "custom"), false)
							_, effort := nativeReasoningFor(*target)
							u.setValue(nativeClientField(key, id, "reasoning"), effort)
						}
					}))
				}
			}
			if key == "claude" || key == "xcode-claude" {
				caps := u.clientCaps(key)
				efforts := []string{""}
				for _, effort := range []string{"low", "medium", "high", "xhigh"} {
					if validClaudeEffort(id, effort) && (caps.PerModelEffort || effort != "xhigh") && (!claude55ID.MatchString(id) || claudeEffortCompatible(id, caps)) {
						efforts = append(efforts, effort)
					}
				}
				canEffort := len(efforts) > 1 && (caps.PerModelEffort || id == s.Initial)
				if !canEffort {
					u.setValue(nativeClientField(key, id, "claude-effort"), "")
				}
				primary = append(primary, u.disabled(canEffort, u.selectField(nativeClientField(key, id, "claude-effort"), u.tr("Reasoning (empty = automatic)", "Razonamiento (vacío = automático)"), nativeModelReasoningChoices(u, efforts))))
			}
			row = append(row, u.row(primary...))
			row = append(row, options...)
			if (key == "opencode" || key == "zed") && u.expanded[prefix+"advanced"] {
				row = append(row, u.contextChoiceControls(key, choice), u.note(u.contextChoiceSummary(*choice)), u.field(nativeClientField(key, id, "output"), u.tr("Max output (0 = automatic)", "Salida máxima (0 = automática)"), "0", false))
			}
		}
		if shared && !catalog && choice != nil {
			row[0] = u.column(heading...)
			row = append(row, u.sharedModelControls(choice))
			found := false
			for _, current := range u.models {
				if current.ID == id {
					found = true
					break
				}
			}
			if !found {
				row = append(row, u.note(u.tr("Saved model · catalog unavailable", "Modelo guardado · catálogo no disponible")))
			}
		}
		selectedCard := choice != nil
		if shared && !catalog {
			selectedCard = s.Initial == id
		}
		cards = append(cards, u.modelCard(selectedCard, row...))
		cardIDs = append(cardIDs, id)
	}
	if len(cards) == 0 {
		if shared && !catalog && strings.TrimSpace(u.value(prefix+"search")) != "" {
			controls = append(controls, u.note(u.tr("No saved models match. Clear the search to see all saved models.", "Ningún modelo guardado coincide. Limpia la búsqueda para ver todos los modelos guardados.")))
		} else {
			controls = append(controls, u.note(u.tr("No matches. Refresh or add an exact model ID.", "Sin resultados. Actualiza o añade un ID exacto.")))
		}
	} else {
		controls = append(controls, u.modelGrid(prefix+"models", cardIDs, cards))
	}
	if len(available) > 150 {
		controls = append(controls, u.note(u.tr("Showing the first 150 results. Search to narrow the catalog.", "Se muestran los primeros 150 resultados. Busca para acotar el catálogo.")))
	}
	if catalog || u.expanded[prefix+"advanced"] {
		exactID := prefix + "exact"
		controls = append(controls, u.disclosure(exactID, u.tr("Add an exact model ID", "Añadir un ID de modelo exacto")))
		if u.expanded[exactID] {
			controls = append(controls, u.actionRow(u.field(prefix+"manual", u.tr("Exact model ID", "ID de modelo exacto"), "provider/model", false), u.button(prefix+"add", u.tr("Add model", "Añadir modelo"), func() {
				id := strings.TrimSpace(u.value(prefix + "manual"))
				m := modelInfo{ID: id, Name: id}
				for _, candidate := range u.models {
					if candidate.ID == id {
						m = candidate
						break
					}
				}
				if add(m) {
					u.setValue(prefix+"manual", "")
					u.setValue(prefix+"search", "")
				}
			})))
		}
	}

	return u.column(controls...)
}

func (u *nativeUI) clientActions(key string, s *nativeClientSelection, launchDescription string, launchSetup ...layout.Widget) layout.Widget {
	base, local, _ := u.clientBase()
	fingerprint := nativeSelectionFingerprint(key, s, base, local, u.clientCaps(key))
	ready := s.Saved != "" && s.Saved == fingerprint
	_, validation := nativeClientPayload(key, s)
	working := u.busy["POST"+nativeClientEndpoint(key)] || u.busy["GET"+nativeClientEndpoint(key)] || u.clientState().Launching != ""
	canPrepare := len(s.Models) > 0 && validation == nil && !working
	if (key == "codex" || key == "codex-cli") && !nativeClientImagesReady(s, u.models, u.state) {
		canPrepare = false
	}
	if strings.HasPrefix(key, "xcode-") && key != "xcode-chat" && !u.clientState().Xcode.Available {
		canPrepare = false
	}
	profileTone, profileStatus := nativeToneNeutral, u.tr("Profile not prepared", "Perfil sin preparar")
	if s.Saved != "" && ready {
		profileTone, profileStatus = nativeToneSuccess, u.tr("Profile ready", "Perfil listo")
	} else if s.Saved != "" {
		profileTone, profileStatus = nativeToneWarning, u.tr("Changes need preparing", "Hay cambios por preparar")
	}
	// Profile state leads the section so the effect of "Open" is visible before acting.
	launchWidgets := append([]layout.Widget{u.pills(u.statusBadge(profileTone, profileStatus))}, launchSetup...)
	launchWidgets = append(launchWidgets,
		u.clientLauncherPanel(key, s, canPrepare),
		u.hint(u.tr("Open prepares this profile and starts the proxy automatically. Prepare without opening only writes the profile.", "Abrir prepara este perfil y arranca el proxy automáticamente. Preparar sin abrir solo escribe el perfil.")),
		u.pills(u.disabled(canPrepare, u.button("client:"+key+":prepare", u.tr("Prepare without opening", "Preparar sin abrir"), func() { u.prepareClient(key) }))),
	)
	if ready {
		launchWidgets = append(launchWidgets, u.note(s.Path))
	}
	if len(s.Models) == 0 {
		launchWidgets = append(launchWidgets, u.hint(u.tr("Choose at least one model to prepare this profile.", "Elige al menos un modelo para preparar este perfil.")))
	}
	if validation != nil && len(s.Models) > 0 {
		launchWidgets = append(launchWidgets, u.message(nativeToneError, nativeMessage(validation.Error(), u.language)))
	}
	if (key == "codex" || key == "codex-cli") && !nativeClientImagesReady(s, u.models, u.state) {
		launchWidgets = append(launchWidgets, u.hint(u.tr("Review image generation settings in Models before opening Codex.", "Revisa la generación de imágenes en Modelos antes de abrir Codex.")))
	}
	if strings.HasPrefix(key, "xcode-") && key != "xcode-chat" && !u.clientState().Xcode.Available {
		launchWidgets = append(launchWidgets, u.hint(u.tr("Install Xcode and refresh detection before preparing this profile.", "Instala Xcode y actualiza la detección antes de preparar este perfil.")))
	}
	if working {
		launchWidgets = append(launchWidgets, u.hint(u.tr("Wait for the current profile operation to finish.", "Espera a que termine la operación actual del perfil.")))
	}
	if key == "opencode" {
		launchWidgets = append(launchWidgets, u.note(u.tr("OpenCode opens a terminal in your project; use /models there.", "OpenCode abre un terminal en tu proyecto; usa /models allí.")))
	}
	if key == "omp" {
		launchWidgets = append(launchWidgets, u.pills(u.iconButton("client:omp:install", u.tr("Oh My Pi installation instructions", "Instrucciones de instalación de Oh My Pi"), nativeButtonGhost, nativeIconOpenInNew, func() { u.open("https://omp.sh/") })))
	}
	launch := u.section(u.tr("Launch", "Arranque"), launchDescription, launchWidgets...)

	showCommandID := "client:" + key + ":show-command"
	showCommand := u.expanded[showCommandID]
	advancedWidgets := []layout.Widget{
		u.note(u.tr("Existing preferences are preserved; changed files receive .bak backups.", "Se conservan los ajustes existentes; los archivos modificados reciben copias .bak.")),
		u.disclosure(showCommandID, u.tr("Show launch command (optional)", "Mostrar comando de arranque (opcional)")),
	}
	if showCommand {
		if key == "codex" {
			advancedWidgets = append(advancedWidgets, u.row(u.selectField("clients-platform", u.tr("Command operating system", "Sistema operativo del comando"), nativeChoices([]string{"macos", "windows", "linux"})), u.field("clients-app-path", u.tr("Command application path", "Ruta de aplicación del comando"), "/Applications/ChatGPT.app", false)))
		}
		if key == "codex-cli" || key == "claude" || key == "opencode" || key == "omp" {
			advancedWidgets = append(advancedWidgets, u.selectField("clients-shell", u.tr("Command shell", "Shell del comando"), nativeChoices([]string{"unix", "powershell"})))
		}
	}
	copyLabel := u.tr("Copy launch command", "Copiar comando de arranque")
	if key == "zed" {
		copyLabel = u.tr("Copy key for Zed (recovery)", "Copiar clave para Zed (recuperación)")
		advancedWidgets = append(advancedWidgets, u.note(u.tr("Preparation stores the local key in the system credential store. Choose a model in Zed's Agent panel; existing projects stay open.", "La preparación guarda la clave local en el almacén de credenciales del sistema. Elige un modelo en el panel Agent de Zed; tus proyectos siguen abiertos.")))
	}
	if key == "xcode-chat" {
		copyLabel = u.tr("Copy Xcode connection (one-time setup)", "Copiar conexión de Xcode (configuración inicial)")
		advancedWidgets = append(advancedWidgets, u.note(u.tr("Xcode Chat manages its own context window; shared context presets do not change it.", "Xcode Chat gestiona su propia ventana de contexto; los preajustes compartidos no la cambian.")))
	}
	if key == "claude" || key == "xcode-claude" {
		advancedWidgets = append(advancedWidgets, u.note(u.tr("Claude Code uses the smallest selected context window for the session; the active model may cap it further.", "Claude Code usa la menor ventana seleccionada para la sesión; el modelo activo puede limitarla aún más.")))
	}
	if (showCommand || key == "zed" || key == "xcode-chat") && key != "xcode-codex" && key != "xcode-claude" {
		advancedWidgets = append(advancedWidgets, u.disabled(ready, u.iconButton("client:"+key+":copy-launch", copyLabel, nativeButtonSecondary, nativeIconCopy, func() {
			u.syncClientSelection(key, s)
			if s.Saved == "" || s.Saved != nativeSelectionFingerprint(key, s, base, local, u.clientCaps(key)) {
				u.setNotice(nativeToneWarning, u.tr("Prepare the changed profile before copying its launch details.", "Prepara el perfil modificado antes de copiar los datos de arranque."))
				return
			}
			if text, err := u.clientLaunch(key, s, true); err != nil {
				u.noticeError(err)
			} else {
				u.copy(text)
			}
		})))
	}
	if key == "codex" || key == "codex-cli" {
		advancedWidgets = append(advancedWidgets,
			u.note(u.tr("The normal Codex profile stays separate; restart the Kilo instance after preparing changes.", "El perfil normal de Codex queda separado; reinicia la instancia Kilo tras preparar cambios.")),
			u.disabled(len(s.Models) > 0, u.iconButton("client:"+key+":catalog-copy", u.tr("Copy models.json", "Copiar models.json"), nativeButtonSecondary, nativeIconCopy, func() {
				u.syncClientSelection(key, s)
				data, err := buildCodexCatalog(s.Models, s.Initial, false)
				if err != nil {
					u.noticeError(err)
				} else {
					u.copy(string(data))
				}
			})),
		)
	}
	if ready && (showCommand || strings.HasPrefix(key, "xcode-") || key == "zed") {
		if text, err := u.clientLaunch(key, s, false); err == nil && text != "" {
			advancedWidgets = append(advancedWidgets, u.code("client:"+key+":launch-preview", text))
		}
	}
	advancedWidgets = append(advancedWidgets, u.disclosure("client:"+key+":show-config", u.tr("Show optional configuration export", "Mostrar exportación de configuración opcional")))
	if u.expanded["client:"+key+":show-config"] && len(s.Models) > 0 {
		if key == "omp" {
			advancedWidgets = append(advancedWidgets, u.note(u.tr("models.yml · Kilo provider and shared model list", "models.yml · proveedor Kilo y lista de modelos compartida")))
		}
		if key == "zed" {
			advancedWidgets = append(advancedWidgets, u.note(u.tr("Copying configuration does not save credentials; use Prepare or set the local key in Zed.", "Copiar la configuración no guarda las credenciales; usa Preparar o configura la clave local en Zed.")))
		}
		if text, err := u.clientExport(key, s, false); err == nil {
			copyConfigLabel := u.tr("Copy complete configuration", "Copiar configuración completa")
			if key == "omp" {
				copyConfigLabel = u.tr("Copy models.yml", "Copiar models.yml")
			}
			advancedWidgets = append(advancedWidgets, u.code("client:"+key+":config", text), u.pills(u.iconButton("client:"+key+":config-copy", copyConfigLabel, nativeButtonSecondary, nativeIconCopy, func() {
				u.syncClientSelection(key, s)
				text, err := u.clientExport(key, s, true)
				if err != nil {
					u.noticeError(err)
				} else {
					u.copy(text)
				}
			})))
		} else {
			advancedWidgets = append(advancedWidgets, u.message(nativeToneError, nativeMessage(err.Error(), u.language)))
		}
		if key == "omp" {
			settings := func() ([]byte, error) {
				selection, err := nativeOMPSelection(s)
				if err != nil {
					return nil, err
				}
				return buildOMPSettings(selection)
			}
			if data, err := settings(); err == nil {
				advancedWidgets = append(advancedWidgets, u.note(u.tr("config.yml · default model and reasoning", "config.yml · modelo inicial y razonamiento")), u.code("client:omp:settings", string(data)), u.pills(u.iconButton("client:omp:settings-copy", u.tr("Copy config.yml", "Copiar config.yml"), nativeButtonSecondary, nativeIconCopy, func() {
					if data, err := settings(); err == nil {
						u.copy(string(data))
					} else {
						u.noticeError(err)
					}
				})))
			}
		}
	}
	advanced := u.section(u.tr("Advanced", "Avanzado"), u.tr("Optional launch commands and configuration exports.", "Comandos de arranque y exportaciones opcionales."), advancedWidgets...)
	return u.column(launch, advanced)
}

func (u *nativeUI) prepareClient(key string) {
	u.prepareClientAfter(key, func(err error) {
		if err != nil {
			u.noticeError(err)
		}
	})
}

func (u *nativeUI) prepareClientAfter(key string, done func(error)) {
	if !nativeCLIPathClient(key) {
		u.prepareClientAfterCLIPathsChecked(key, done)
		return
	}
	preferences := &u.clientState().CLIPreferences
	if !preferences.Loaded && !u.cliPreferencesBusy() && preferences.Pending == nil {
		u.refreshCLIPreferencesAfter(func(err error) {
			if err != nil {
				done(err)
				return
			}
			u.prepareClientAfter(key, done)
		})
		return
	}
	if (key == "t3-code" || key == "synara") && !u.clientState().OpenCodePreferences.Loaded && !u.openCodePreferencesBusy() {
		u.refreshOpenCodePreferencesAfter(func(err error) {
			if err != nil {
				done(err)
				return
			}
			u.prepareClientAfter(key, done)
		})
		return
	}
	expected := preferences.Preferences
	expectedOpenCode := u.clientState().OpenCodePreferences.Preferences
	verify := func(after func(error)) {
		u.verifyCLIPreferencesAfter(expected, func(err error) {
			if err != nil || key != "t3-code" && key != "synara" {
				after(err)
				return
			}
			u.verifyT3OpenCodePathsAfter(expectedOpenCode, after)
		})
	}
	before := u.launchSettingsFingerprintWithPreferences(key, "", false)
	verify(func(err error) {
		if err != nil {
			done(err)
			return
		}
		if before != u.launchSettingsFingerprintWithPreferences(key, "", false) {
			done(errors.New(u.launchSettingsChangedMessage()))
			return
		}
		u.prepareClientAfterCLIPathsChecked(key, func(err error) {
			if err != nil {
				done(err)
				return
			}
			verify(func(err error) {
				if err != nil {
					u.sharedClientSelection(key).Saved = ""
					if key == "t3-code" {
						u.agentsState().T3Code.Info.Prepared = false
					}
					if key == "synara" {
						u.agentsState().Synara.Info.Prepared = false
					}
				}
				if err == nil {
					u.clientPreparedNotice(key)
				}
				done(err)
			})
		})
	})
}

func (u *nativeUI) prepareClientAfterCLIPathsChecked(key string, done func(error)) {
	if key != "opencode" && key != "opencode2" {
		u.prepareClientAfterPreferencesChecked(key, done)
		return
	}
	before := u.launchSettingsFingerprintWithPreferences(key, "", false)
	u.verifyOpenCodePreferencesAfter(key, func(err error) {
		if err != nil {
			done(err)
			return
		}
		if before != u.launchSettingsFingerprintWithPreferences(key, "", false) {
			done(errors.New(u.launchSettingsChangedMessage()))
			return
		}
		u.prepareClientAfterPreferencesChecked(key, func(err error) {
			if err != nil {
				done(err)
				return
			}
			u.verifyOpenCodePreferencesAfter(key, func(err error) {
				if err == nil {
					u.setNotice(nativeToneSuccess, u.tr("Editor profile prepared.", "Perfil del editor preparado."))
				}
				done(err)
			})
		})
	})
}

func (u *nativeUI) prepareClientAfterPreferencesChecked(key string, done func(error)) {
	if key == "omnigent" {
		u.prepareOmnigentAfter(done)
		return
	}
	if key == "opencode" || key == "opencode2" {
		prefs := &u.clientState().OpenCodePreferences
		if u.openCodePreferencesBusy() || prefs.Pending != nil {
			done(errors.New(u.tr("Finish saving the OpenCode version before preparing.", "Termina de guardar la versión de OpenCode antes de preparar.")))
			return
		}
		if !prefs.Loaded {
			u.refreshOpenCodePreferencesAfter(func(err error) {
				if err != nil {
					done(err)
					return
				}
				u.prepareClientAfter(key, done)
			})
			return
		}
		if key != u.openCodeClientKey() {
			done(errors.New(u.launchSettingsChangedMessage()))
			return
		}
	}
	var t3Preferences nativeT3CodePreferences
	if key == "t3-code" {
		prefs := &u.agentsState().T3Code.PreferencesState
		if u.t3CodePreferencesBusy() || prefs.Pending != nil {
			done(errors.New(u.tr("Finish saving T3 Code settings before preparing.", "Termina de guardar los ajustes de T3 Code antes de preparar.")))
			return
		}
		if !prefs.Loaded {
			before := u.launchSettingsFingerprintWithPreferences(key, "", false)
			u.refreshT3CodePreferencesAfter(func(err error) {
				if err != nil {
					done(err)
					return
				}
				if before != u.launchSettingsFingerprintWithPreferences(key, "", false) {
					done(errors.New(u.launchSettingsChangedMessage()))
					return
				}
				u.prepareClientAfter(key, done)
			})
			return
		}
		if !validNativeT3CodePreferences(prefs.Preferences) {
			done(errors.New(u.tr("Invalid T3 Code preferences. Reload settings before preparing.", "Preferencias de T3 Code no válidas. Carga los ajustes antes de preparar.")))
			return
		}
		t3Preferences = prefs.Preferences
		t3Preferences.Providers = append([]string{}, t3Preferences.Providers...)
	}
	var synaraPreferences nativeSynaraPreferences
	if key == "synara" {
		prefs := &u.agentsState().Synara.PreferencesState
		if u.synaraPreferencesBusy() || prefs.Pending != nil {
			done(errors.New(u.tr("Finish saving Synara settings before preparing.", "Termina de guardar los ajustes de Synara antes de preparar.")))
			return
		}
		if !prefs.Loaded {
			before := u.launchSettingsFingerprintWithPreferences(key, "", false)
			u.refreshSynaraPreferencesAfter(func(err error) {
				if err != nil {
					done(err)
					return
				}
				if before != u.launchSettingsFingerprintWithPreferences(key, "", false) {
					done(errors.New(u.launchSettingsChangedMessage()))
					return
				}
				u.prepareClientAfter(key, done)
			})
			return
		}
		if !validNativeSynaraPreferences(prefs.Preferences) {
			done(errors.New(u.tr("Invalid Synara preferences. Reload settings before preparing.", "Preferencias de Synara no válidas. Carga los ajustes antes de preparar.")))
			return
		}
		synaraPreferences = prefs.Preferences
		synaraPreferences.Providers = append([]string{}, synaraPreferences.Providers...)
	}
	if key == "claude-desktop" && u.busy["POST/api/claude-desktop/options"] {
		done(errors.New(u.tr("Wait for the current app operation to finish.", "Espera a que termine la operación actual de la app.")))
		return
	}
	if key == "t3-code" {
		if err := u.t3CodeProviderCatalogConnectionError(); err != nil {
			done(err)
			return
		}
		if !u.t3CodeNeedsSavedLibrary() {
			u.prepareT3CodeWithoutLibraryAfter(t3Preferences, done)
			return
		}
	}
	if key == "synara" && u.synaraNativeOnly() {
		u.prepareSynaraNativeAfter(synaraPreferences, done)
		return
	}
	if err := u.packReadinessError(key); err != nil {
		done(err)
		return
	}
	s := u.sharedClientSelection(key)
	u.syncClientSelection(key, s)
	payload, err := nativeClientPayload(key, s)
	if err != nil {
		done(err)
		return
	}
	if key == "t3-code" {
		library := payload.(map[string]any)["library"].(modelLibrary)
		if err := nativeT3CodePreferenceLibraryError(t3Preferences, library); err != nil {
			done(err)
			return
		}
		payload.(map[string]any)["preferences"] = t3Preferences
	}
	if key == "synara" {
		library := payload.(map[string]any)["library"].(modelLibrary)
		if err := nativeSynaraPreferenceLibraryError(synaraPreferences, library); err != nil {
			done(err)
			return
		}
		payload.(map[string]any)["preferences"] = synaraPreferences
	}
	base, local, _ := u.clientBase()
	fingerprint := nativeSelectionFingerprint(key, s, base, local, u.clientCaps(key))
	imagesSent := cloneClientImageSettings(s.ImageGeneration)
	openMausBotConnection := u.launchConnectionFingerprint()
	openCodeGeneration := u.clientState().OpenCodePreferences.Generation
	u.clientRequest(http.MethodPost, nativeClientEndpoint(key), payload, func(data json.RawMessage, err error) {
		if err != nil {
			done(err)
			return
		}
		var result struct {
			ProfileDir string `json:"profileDir"`
			ConfigPath string `json:"configPath"`
		}
		if err = json.Unmarshal(data, &result); err != nil {
			done(err)
			return
		}
		if result.ProfileDir == "" && result.ConfigPath == "" {
			done(errors.New("Prepared profile did not return a path"))
			return
		}
		if key == "t3-code" {
			prefs := &u.agentsState().T3Code.PreferencesState
			var confirmed nativeT3CodeInfo
			if json.Unmarshal(data, &confirmed) != nil || !nativeT3CodePreferencesEqual(confirmed.Preferences, t3Preferences) || !nativeT3CodePreferencesEqual(prefs.Preferences, t3Preferences) || prefs.Pending != nil || u.t3CodePreferencesBusy() || openMausBotConnection != u.launchConnectionFingerprint() {
				done(errors.New(u.launchSettingsChangedMessage()))
				return
			}
		}
		if key == "synara" {
			prefs := &u.agentsState().Synara.PreferencesState
			var confirmed nativeSynaraInfo
			if json.Unmarshal(data, &confirmed) != nil || !confirmed.Prepared || !nativeSynaraPreferencesEqual(confirmed.Preferences, synaraPreferences) || !nativeSynaraPreferencesEqual(prefs.Preferences, synaraPreferences) || prefs.Pending != nil || u.synaraPreferencesBusy() || openMausBotConnection != u.launchConnectionFingerprint() {
				done(errors.New(u.launchSettingsChangedMessage()))
				return
			}
		}
		if (key == "opencode" || key == "opencode2") && (openCodeGeneration != u.clientState().OpenCodePreferences.Generation || key != u.openCodeClientKey() || !u.openCodePreferencesReady()) {
			done(errors.New(u.launchSettingsChangedMessage()))
			return
		}
		s.Path = result.ProfileDir
		if result.ConfigPath != "" {
			s.Path = result.ConfigPath
		}
		s.Saved = fingerprint
		if key == "openmausbot" {
			u.acceptOpenMausBotProfile(data, openMausBotConnection)
		}
		if key == "t3-code" {
			u.acceptT3CodeProfile(data, openMausBotConnection)
		}
		if key == "synara" {
			u.acceptSynaraProfile(data, openMausBotConnection)
		}
		if key == "codex" || key == "codex-cli" {
			u.acceptClientImages(imagesSent)
		}
		if !nativeCLIPathClient(key) {
			u.clientPreparedNotice(key)
		}

		done(nil)
	})
}

// The server resolves optional saved model overrides and connected catalogs.
func (u *nativeUI) prepareT3CodeWithoutLibraryAfter(preferences nativeT3CodePreferences, done func(error)) {
	before := u.launchSettingsFingerprint("t3-code", "")
	var connection [32]byte
	if !u.t3CodeNativeOnly() {
		connection = u.launchConnectionFingerprint()
	}
	u.clientRequest(http.MethodPost, nativeClientEndpoint("t3-code"), map[string]any{"preferences": preferences}, func(data json.RawMessage, err error) {
		if err != nil {
			done(err)
			return
		}
		var confirmed struct {
			nativeT3CodeInfo
			ProfileDir string `json:"profileDir"`
			ConfigPath string `json:"configPath"`
		}
		if json.Unmarshal(data, &confirmed) != nil || confirmed.ProfileDir == "" && confirmed.ConfigPath == "" {
			done(errors.New("Prepared profile did not return a path"))
			return
		}
		prefs := &u.agentsState().T3Code.PreferencesState
		if !confirmed.Prepared || u.t3CodeProviderCatalogConnectionError() != nil || !nativeT3CodePreferencesEqual(confirmed.Preferences, preferences) || !nativeT3CodePreferencesEqual(prefs.Preferences, preferences) || prefs.Pending != nil || u.t3CodePreferencesBusy() || before != u.launchSettingsFingerprint("t3-code", "") {
			done(errors.New(u.launchSettingsChangedMessage()))
			return
		}
		selection := u.sharedClientSelection("t3-code")
		selection.Path = confirmed.ProfileDir
		if confirmed.ConfigPath != "" {
			selection.Path = confirmed.ConfigPath
		}
		u.acceptT3CodeProfile(data, connection)
		done(nil)
	})
}

func (u *nativeUI) prepareSynaraNativeAfter(preferences nativeSynaraPreferences, done func(error)) {
	before := u.launchSettingsFingerprint("synara", "")
	u.clientRequest(http.MethodPost, nativeClientEndpoint("synara"), map[string]any{"preferences": preferences}, func(data json.RawMessage, err error) {
		if err != nil {
			done(err)
			return
		}
		var confirmed struct {
			nativeSynaraInfo
			ProfileDir string `json:"profileDir"`
			ConfigPath string `json:"configPath"`
		}
		if json.Unmarshal(data, &confirmed) != nil || confirmed.ProfileDir == "" && confirmed.ConfigPath == "" {
			done(errors.New("Prepared profile did not return a path"))
			return
		}
		prefs := &u.agentsState().Synara.PreferencesState
		if !confirmed.Prepared || !nativeSynaraPreferencesEqual(confirmed.Preferences, preferences) || !nativeSynaraPreferencesEqual(prefs.Preferences, preferences) || prefs.Pending != nil || u.synaraPreferencesBusy() || before != u.launchSettingsFingerprint("synara", "") {
			done(errors.New(u.launchSettingsChangedMessage()))
			return
		}
		selection := u.sharedClientSelection("synara")
		selection.Path = confirmed.ProfileDir
		if confirmed.ConfigPath != "" {
			selection.Path = confirmed.ConfigPath
		}
		u.acceptSynaraProfile(data, [32]byte{})
		done(nil)
	})
}

func (u *nativeUI) clientPreparedNotice(key string) {
	if key != "opencode" && key != "opencode2" {
		u.setNotice(nativeToneSuccess, u.tr("Editor profile prepared.", "Perfil del editor preparado."))
	}
	if key == "claude-desktop" {
		u.setNotice(nativeToneSuccess, u.tr("Kilo profile prepared. Open Claude Desktop · Kilo to apply changes.", "Perfil Kilo preparado. Abre Claude Desktop · Kilo para aplicar los cambios."))
	}
	if key == "t3-code" {
		u.setNotice(nativeToneSuccess, u.tr("Your saved accounts and models are prepared. Open T3 Code · Kilo to choose an account.", "Las cuentas y modelos guardados están preparados. Abre T3 Code · Kilo para elegir una cuenta."))
	}
	if key == "omnigent" {
		u.setNotice(nativeToneSuccess, u.tr("Your saved Omnigent accounts and models are prepared.", "Las cuentas y modelos guardados de Omnigent están preparados."))
		return
	}
	if key == "synara" {
		u.setNotice(nativeToneSuccess, u.tr("Your saved accounts and models are prepared. Open Synara · Kilo to choose an account.", "Las cuentas y modelos guardados están preparados. Abre Synara · Kilo para elegir una cuenta."))
	}
}

func (u *nativeUI) loadClient(key string) {
	before := u.clientState().selection(key)
	u.syncClientSelection(key, before)
	snapshot, _ := json.Marshal(before)
	u.call(http.MethodGet, nativeClientEndpoint(key), nil, func(data json.RawMessage) {
		current := u.clientState().selection(key)
		u.syncClientSelection(key, current)
		now, _ := json.Marshal(current)
		if current != before || string(now) != string(snapshot) {
			u.setNotice(nativeToneWarning, u.tr("Your selection changed while loading. Load again to replace those edits.", "Tu selección cambió durante la carga. Carga de nuevo para sustituir esos cambios."))
			return
		}
		s, err := decodeNativeClientSelection(key, data, u.models)
		if err != nil {
			u.noticeError(err)
			return
		}
		u.clientState().Selections[key] = s
		if key == "codex" {
			u.setValue("client:codex:queue-mode", s.QueueMode)
		}
		if key == "codex" || key == "codex-cli" {
			u.acceptClientImages(s.ImageGeneration)
		}
		for _, m := range s.Models {
			u.seedClientChoice(key, m)
		}
		for _, alias := range []string{"sonnet", "opus", "haiku"} {
			u.setValue("client:"+key+":alias:"+alias, s.Aliases[alias])
		}
		if key == "claude" {
			u.setValue("clients-claude-mode", s.Mode)
		}
		u.setChecked("client:"+key+":selected", true)
		u.setValue("client:"+key+":search", "")
		u.setNotice(nativeToneSuccess, u.tr("Selection loaded. Prepare again to apply the current connection.", "Selección cargada. Prepara de nuevo para aplicar la conexión actual."))
	})
}

func sameEditorLevels(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, level := range a {
		seen[level] = true
	}
	for _, level := range b {
		if !seen[level] {
			return false
		}
	}
	return true
}

func decodeNativeClientSelection(key string, data []byte, catalog []modelInfo) (*nativeClientSelection, error) {
	if key == "omp" {
		return decodeNativeOMPSelection(data, catalog)
	}
	s := &nativeClientSelection{Aliases: map[string]string{}, Mode: "installed"}
	lookup := func(id string) modelInfo {
		for _, m := range catalog {
			if m.ID == id {
				return m
			}
		}
		return modelInfo{ID: id, Name: id}
	}
	if key == "codex" || key == "codex-cli" || key == "xcode-codex" {
		var envelope struct {
			Catalog         json.RawMessage          `json:"catalog"`
			ImageGeneration *imageGenerationSettings `json:"imageGeneration"`
			QueueMode       string                   `json:"followUpQueueMode"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return nil, err
		}
		if key != "xcode-codex" && envelope.ImageGeneration != nil {
			images := *envelope.ImageGeneration
			s.ImageGeneration = cloneClientImageSettings(&images)
			s.imageGenerationBaseline = cloneClientImageSettings(&images)
		}
		if key == "codex" {
			s.QueueMode = envelope.QueueMode
			if !validCodexQueueMode(s.QueueMode) {
				s.QueueMode = codexQueueModeQueue
			}
		}
		if len(envelope.Catalog) > 0 {
			data = envelope.Catalog
		}
		if _, err := validateCatalog(data); err != nil {
			return nil, err
		}
		var source struct {
			Models []struct {
				ID      string   `json:"slug"`
				Name    string   `json:"display_name"`
				Context int      `json:"context_window"`
				Input   []string `json:"input_modalities"`
				Effort  string   `json:"default_reasoning_level"`
				Levels  []struct {
					Effort string `json:"effort"`
				} `json:"supported_reasoning_levels"`
			} `json:"models"`
		}
		if err := json.Unmarshal(data, &source); err != nil {
			return nil, err
		}
		for _, m := range source.Models {
			model := lookup(m.ID)
			model.InputModalities = m.Input
			choice := nativeModelChoice{Model: model, DisplayName: m.Name, ReasoningCustom: true, DefaultReasoning: m.Effort, ReasoningLevels: []string{}, MaximumOutputTokens: model.MaxOutputTokens, ContextPreset: contextPresetCustom, ContextTokens: m.Context}
			if m.Context == 0 {
				choice.ContextPreset = contextPresetRecommended
			}
			for _, level := range m.Levels {
				choice.ReasoningLevels = append(choice.ReasoningLevels, level.Effort)
			}
			s.Models = append(s.Models, choice)
		}
		if len(s.Models) > 0 {
			s.Initial = s.Models[0].Model.ID
		}
		return s, nil
	}
	if key == "opencode" || key == "zed" || key == "claude-desktop" {
		var source struct {
			Selection          editorSelection `json:"selection"`
			ConfigPath         string          `json:"configPath"`
			ExperimentalModels bool            `json:"experimentalModels"`
		}
		if err := json.Unmarshal(data, &source); err != nil {
			return nil, err
		}
		validate := validateEditorSelection
		if key == "claude-desktop" {
			s.DesktopExperimentalModels = source.ExperimentalModels
			validate = func(selection editorSelection) error {
				return validateClaudeDesktopSelectionMode(selection, source.ExperimentalModels)
			}
		}
		if err := validate(source.Selection); err != nil {
			return nil, err
		}
		s.Initial = source.Selection.Initial
		s.Path = source.ConfigPath
		for _, m := range source.Selection.Models {
			model := lookup(m.ID)
			if key == "claude-desktop" {
				s.Models = append(s.Models, nativeModelChoice{Model: model, DisplayName: m.Name, ContextPreset: contextPresetRecommended, MaximumOutputTokens: model.MaxOutputTokens})
				continue
			}
			maximumOutput := model.MaxOutputTokens
			model.MaxOutputTokens = m.Output
			choice := nativeModelChoice{Model: model, DisplayName: m.Name, DefaultReasoning: m.ReasoningEffort, ContextPreset: contextPresetCustom, ContextTokens: m.Context, MaximumOutputTokens: maximumOutput}
			if len(m.Variants) > 0 && !sameEditorLevels(m.Variants, model.ReasoningEfforts) {
				choice.ReasoningLevels, choice.ReasoningCustom = append([]string(nil), m.Variants...), true
			}
			s.Models = append(s.Models, choice)
		}
		return s, nil
	}
	var source claudeSelection
	if err := json.Unmarshal(data, &source); err != nil {
		return nil, err
	}
	if err := validateClaudeSelection(source); err != nil {
		return nil, err
	}
	s.Initial, s.Mode = source.Initial, source.Mode
	for alias, id := range source.Aliases {
		s.Aliases[alias] = id
	}
	for _, m := range source.Models {
		choice := nativeModelChoice{Model: lookup(m.ID), DisplayName: m.DisplayName, ClaudeEffort: m.Effort, ContextPreset: contextPresetRecommended}
		choice.MaximumOutputTokens = choice.Model.MaxOutputTokens
		if m.Context > 0 {
			choice.ContextPreset, choice.ContextTokens = contextPresetCustom, m.Context
		}
		if m.Output > 0 {
			choice.Model.MaxOutputTokens = m.Output
		}
		s.Models = append(s.Models, choice)
	}
	return s, nil
}

func (u *nativeUI) clientLaunch(key string, s *nativeClientSelection, reveal bool) (string, error) {
	base, local, _ := u.clientBase()
	if !reveal {
		local = "kl_local_••••••••"
	}
	switch key {
	case "codex", "codex-cli":
		return codexLaunchCommand(key == "codex", u.value("clients-shell"), u.value("clients-platform"), u.value("clients-app-path"), local, u.language, len(s.Models) > 0)
	case "claude":
		return claudeLaunchCommand(u.value("clients-shell"), u.language)
	case "opencode":
		return openCodeLaunchCommand(s.Path, s.Initial, u.value("clients-shell"))
	case "omp":
		return ompLaunchCommand(nativeOMPProfileDir(s.Path), s.Initial, u.value("clients-shell")), nil
	case "zed":
		if reveal {
			return local, nil
		}
		return u.tr("Provider: kilo-local\nLocal key saved in the system credential store.\nChoose a model in Zed’s Agent panel.", "Proveedor: kilo-local\nClave local guardada en el almacén de credenciales del sistema.\nElige modelo en el panel Agent de Zed."), nil
	case "xcode-chat":
		return xcodeChatGuide(base, local, u.language), nil
	case "xcode-codex", "xcode-claude":
		return u.tr("Profile ready. Reopen Xcode, enable its coding agent in Settings → Intelligence, and start a new conversation.", "Perfil preparado. Vuelve a abrir Xcode, activa el agente en Settings → Intelligence e inicia una conversación nueva."), nil
	}
	return "", nil
}

func (u *nativeUI) clientExport(key string, s *nativeClientSelection, reveal bool) (string, error) {
	if key == "claude-desktop" {
		return "", errors.New("Prepare the Kilo configuration through Claude Desktop integration settings.")
	}
	base, local, port := u.clientBase()
	// Zed exports only a credential-versioned URL, never the local key itself.
	if !reveal && key != "zed" {
		local = "kl_local_••••••••"
	}
	payload, err := nativeClientPayload(key, s)
	if err != nil {
		return "", err
	}
	if key == "opencode" || key == "zed" {
		data, err := mergeEditorSettings(nil, key, payload.(editorSelection), base, local)
		return string(data), err
	}
	if key == "omp" {
		data, err := buildOMPModels(payload.(ompSelection), base, local)
		return string(data), err
	}
	if key == "codex" || key == "codex-cli" {
		catalog, err := buildCodexCatalog(s.Models, s.Initial, false)
		if err != nil {
			return "", err
		}
		data, err := mergeCodexConfig(nil, catalog, port)
		if err == nil && s.ImageGeneration != nil {
			data, err = mergeCodexImages(data, *s.ImageGeneration, port)
		}
		if err == nil && key == "codex" {
			mode := s.QueueMode
			if !validCodexQueueMode(mode) {
				mode = codexQueueModeQueue
			}
			data, err = mergeCodexQueueMode(data, mode)
		}
		return string(data), err
	}
	if key == "xcode-codex" {
		data, err := buildCodexCatalog(s.Models, s.Initial, true)
		return string(data), err
	}
	if key == "xcode-chat" {
		return xcodeChatGuide(base, local, u.language), nil
	}
	data, err := json.MarshalIndent(claudeManagedSettings(payload.(claudeSelection), u.clientCaps(key), port, local), "", "  ")
	return string(data), err
}

// uxhClientDetail returns what each harness adds to its detail page.
func (u *nativeUI) uxhClientDetail(key string, s *nativeClientSelection) uxhDetail {
	switch key {
	case "t3-code":
		return u.t3CodeDetail(s)
	case "synara":
		return u.synaraDetail(s)
	case "omnigent":
		return u.omnigentDetail(s)
	case "openmausbot":
		return u.openMausBotDetail(s)
	case "open-design":
		return u.openDesignDetail(s)
	case "claude-desktop":
		return u.claudeDesktopDetail(s)
	case "opencode", "opencode2":
		return u.openCodeDetail(key, s)
	}
	d := uxhDetail{}
	if key == "codex" || key == "codex-cli" {
		images := u.tr("Off", "Desactivada")
		if s.ImageGeneration != nil && s.ImageGeneration.Enabled {
			images = u.tr("On", "Activada")
			if s.ImageGeneration.Model != "" {
				images += " · " + s.ImageGeneration.Model
			}
		}
		d.onOpen = append(d.onOpen, u.uxhKV(u.tr("Image generation", "Generación de imágenes"), u.uxhValue(images),
			u.buttonWidget("agents.images.open", u.tr("Configure", "Configurar"), nativeButtonLink, nil, false, false, func() { u.page = "models"; u.expanded["models.images.toggle"] = true })))
	}
	if key == "codex" {
		if !validCodexQueueMode(s.QueueMode) {
			s.QueueMode = codexQueueModeQueue
		}
		if u.value("client:codex:queue-mode") == "" {
			u.setValue("client:codex:queue-mode", s.QueueMode)
		}
		choices := []nativeChoice{
			{Value: codexQueueModeQueue, Label: u.tr("Wait for the next turn", "Esperar al siguiente turno"), Caption: u.tr("Your message is sent when Codex finishes what it is doing.", "Tu mensaje se envía cuando Codex termine lo que está haciendo.")},
			{Value: codexQueueModeSteer, Label: u.tr("Add to the current task", "Añadir a la tarea actual"), Caption: u.tr("Codex takes it into account without stopping what it is doing.", "Codex lo tiene en cuenta sin parar lo que está haciendo.")},
		}
		d.blocks = append(d.blocks, u.uxhBlock(u.tr("If you type while Codex works", "Si escribes mientras Codex trabaja"), "",
			uxhCell(u.column(
				u.optionCards("client:codex:queue-mode.", choices, u.value("client:codex:queue-mode"), true, func(mode string) { u.setValue("client:codex:queue-mode", mode) }),
				u.note(u.tr("Applies after preparing or reopening Codex.", "Se aplica al preparar o volver a abrir Codex.")),
			))))
		a := u.agentsState()
		location := strings.TrimSpace(u.value("clients-launch-app-path"))
		description := u.tr("Detected: ", "Detectada: ") + u.clientState().LaunchInfo.Clients[key].Path
		if location != "" {
			description = u.tr("Custom: ", "Personalizada: ") + location
		} else if u.clientState().LaunchInfo.Clients[key].Path == "" {
			description = u.tr("Not detected. Choose where Codex is installed.", "No detectada. Elige dónde está instalado Codex.")
		}
		showID := "agent:codex:app-path"
		extra := []layout.Widget{u.disclosure(showID, u.tr("Type a path", "Escribir una ruta"))}
		if u.expanded[showID] {
			extra = append(extra, u.field("clients-launch-app-path", u.tr("Codex application path (empty = detected)", "Ruta de la aplicación Codex (vacía = detectada)"), u.clientState().LaunchInfo.Clients[key].Path, false))
		}
		d.advanced = append(d.advanced, u.uxhAdvancedRow(u.tr("App location", "Ubicación de la app"), description, extra,
			u.disabled(a.FolderBusy == "", u.button("agent:codex:locate-options", u.tr("Change…", "Cambiar…"), u.locateCodexApplication))))
	}
	if key == "claude" {
		rows := []layout.Widget{uxhCell(u.textStyle(14, u.tr("When Claude Code asks for “sonnet”, “opus” or “haiku” (for example in subagents), it will use these models.", "Cuando Claude Code pida «sonnet», «opus» o «haiku» (por ejemplo en subagentes), usará estos modelos."), nativeTextMuted, font.Normal))}
		initial := s.Initial
		if choice := s.choice(s.Initial); choice != nil {
			initial = nativeCodexDisplayName(*choice)
		}
		options := []nativeChoice{{Value: "", Label: initial + u.tr(" (default)", " (predeterminado)")}}
		for _, choice := range s.Models {
			options = append(options, nativeChoice{Value: choice.Model.ID, Label: nativeCodexDisplayName(choice)})
		}
		for _, alias := range []string{"sonnet", "opus", "haiku"} {
			rows = append(rows, u.uxhKV(alias+" →", u.uxhSelect("client:"+key+":alias:"+alias, options)))
		}
		if !u.clientCaps(key).Picker {
			rows = append(rows, uxhCell(u.note(u.tr("This version uses model aliases; short picker names need a newer Claude.", "Esta versión usa alias; los nombres cortos requieren un Claude más reciente."))))
		}
		d.blocks = append(d.blocks, u.uxhBlock(u.tr("Claude Code shortcuts", "Atajos de Claude Code"), u.tr("Optional", "Opcional"), rows...))
	}
	if key == "opencode" {
		d.status = append(d.status, u.note(u.tr("OpenCode opens a terminal in your project; use /models there.", "OpenCode abre un terminal en tu proyecto; usa /models allí.")))
	}
	return d
}

// uxhSelect is a compact dropdown without a separate caption.
func (u *nativeUI) uxhSelect(id string, options []nativeChoice) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		current := u.value(id)
		selected := options[0]
		for _, option := range options {
			if option.Value == current {
				selected = option
			}
		}
		children := []layout.Widget{u.pills(u.dropdownButton(id+".toggle", selected.Label, func() { u.expanded[id] = !u.expanded[id] }))}
		if u.expanded[id] {
			for _, option := range options {
				option := option
				children = append(children, u.uxhMenuRow(id+".option."+option.Value, option.Label, "", option.Value == selected.Value, func() { u.setValue(id, option.Value); u.expanded[id] = false }))
			}
		}
		return u.column(children...)(gtx)
	}
}

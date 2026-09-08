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

	"gioui.org/layout"
)

type nativeClients struct {
	Grids               map[string]*nativeModelGridState
	Selections          map[string]*nativeClientSelection
	Claude              claudeCapabilities
	ClaudeChecked       bool
	ClaudeDetectStarted bool
	Xcode               xcodeInstallation
	XcodeChecked        bool
	XcodeDetectStarted  bool
	LaunchInfo          nativeLaunchInfo
	LaunchChecked       bool
	LaunchDetectStarted bool
	LaunchError         string
	Launching           string
	Variant             string
}

// A selection belongs to one editor/variant. Labels never replace gateway IDs.
type nativeClientSelection struct {
	ImageGeneration         *imageGenerationSettings `json:"imageGeneration,omitempty"`
	imageGenerationBaseline *imageGenerationSettings
	Models                  []nativeModelChoice
	Initial                 string
	Aliases                 map[string]string
	Mode                    string
	Saved                   string
	Path                    string
}

func (c *nativeClients) selection(key string) *nativeClientSelection {
	if c.Selections == nil {
		c.Selections = map[string]*nativeClientSelection{}
	}
	if c.Selections[key] == nil {
		c.Selections[key] = &nativeClientSelection{Aliases: map[string]string{}, Mode: "installed"}
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
	if model.ContextWindow == 0 {
		model.ContextWindow = 200000
	}
	s.Models = append(s.Models, nativeModelChoice{Model: model})
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
	case "codex", "codex-cli":
		return "/api/" + key + "/catalog"
	case "claude":
		return "/api/claude/profile"
	case "opencode", "zed":
		return "/api/editors/" + key + "/profile"
	case "xcode-chat", "xcode-codex", "xcode-claude":
		return "/api/xcode/" + strings.TrimPrefix(key, "xcode-")
	}
	return ""
}

func nativeClientPayload(key string, s *nativeClientSelection) (any, error) {
	if key == "codex" || key == "codex-cli" || key == "xcode-codex" {
		catalog, err := buildCodexCatalog(s.Models, s.Initial, key == "xcode-codex")
		payload := map[string]any{"catalog": json.RawMessage(catalog)}
		if key != "xcode-codex" && s.ImageGeneration != nil {
			images := *s.ImageGeneration
			payload["imageGeneration"] = images
			if images.Enabled && !catalogID.MatchString(images.Model) && err == nil {
				err = errors.New("Choose an image model before enabling image generation.")
			}
		}
		return payload, err
	}
	if key == "opencode" || key == "zed" {
		selection := editorSelection{Initial: s.Initial}
		for _, m := range s.Models {
			name := m.DisplayName
			if name == "" {
				name = m.Model.Name
			}
			selection.Models = append(selection.Models, editorModel{ID: m.Model.ID, Name: name, Context: m.Model.ContextWindow, Output: m.Model.MaxOutputTokens})
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
		selection.Models = append(selection.Models, claudeModel{ID: m.Model.ID, DisplayName: m.DisplayName, Effort: effort})
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
	data, _ := json.Marshal([]any{key, base, keyValue, payload, caps})
	return string(data)
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

// Refresh published metadata while keeping the profile's editable token limits.
func nativeCurrentModel(saved, current modelInfo) modelInfo {
	if current.ID == "" {
		return saved
	}
	current.ContextWindow, current.MaxOutputTokens = saved.ContextWindow, saved.MaxOutputTokens
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
	u.setValue(nativeClientField(key, id, "reasoning"), initial)
	u.setValue(nativeClientField(key, id, "claude-effort"), m.ClaudeEffort)
	u.setValue(nativeClientField(key, id, "context"), strconv.Itoa(m.Model.ContextWindow))
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
		m.Model = nativeCurrentModel(m.Model, catalog[id])
		m.DisplayName = u.value(nativeClientField(key, id, "name"))
		if key == "opencode" || key == "zed" {
			m.Model.ContextWindow, _ = strconv.Atoi(u.value(nativeClientField(key, id, "context")))
			m.Model.MaxOutputTokens, _ = strconv.Atoi(u.value(nativeClientField(key, id, "output")))
		}
		if key == "codex" || key == "codex-cli" || key == "xcode-codex" {
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
		c.ClaudeDetectStarted = true
		u.call("GET", "/api/claude/info", nil, func(data json.RawMessage) {
			if json.Unmarshal(data, &c.Claude) == nil {
				c.ClaudeChecked = true
			}
		})
	}
	if u.client == "xcode" && !c.XcodeDetectStarted {
		c.XcodeDetectStarted = true
		u.call("GET", "/api/xcode/info", nil, func(data json.RawMessage) {
			if json.Unmarshal(data, &c.Xcode) == nil {
				c.XcodeChecked = true
			}
		})
	}
	tabs := []layout.Widget{}
	for _, tab := range []struct{ id, name string }{{"codex", "Codex GUI"}, {"codex-cli", "Codex CLI"}, {"claude", "Claude Code"}, {"opencode", "OpenCode"}, {"zed", "Zed"}, {"cursor", "Cursor"}, {"xcode", "Xcode"}, {"generic", u.tr("Other clients", "Otros clientes")}} {
		id, name := tab.id, tab.name
		if u.client == id {
			name = "● " + name
		}
		tabs = append(tabs, u.button("client-tab:"+id, name, func() { u.client = id }))
	}
	widgets := []layout.Widget{u.pills(tabs...)}
	key := u.client
	if key == "xcode" {
		variants := []layout.Widget{}
		for _, variant := range []string{"chat", "codex", "claude"} {
			v := variant
			name := map[string]string{"chat": "Chat", "codex": "Codex", "claude": "Claude"}[v]
			if c.Variant == v {
				name = "● " + name
			}
			variants = append(variants, u.button("xcode-variant:"+v, name, func() { c.Variant = v }))
		}
		widgets = append(widgets, u.pills(variants...), u.button("xcode-detect", u.tr("Detect Xcode", "Detectar Xcode"), func() {
			u.call("GET", "/api/xcode/info", nil, func(data json.RawMessage) {
				if json.Unmarshal(data, &c.Xcode) == nil {
					c.XcodeChecked = true
				}
			})
		}))
		if c.XcodeChecked {
			text := u.tr("Xcode was not found. Agent preparation requires macOS and Xcode.", "No se encontró Xcode. Preparar agentes requiere macOS y Xcode.")
			if c.Xcode.Available {
				text = fmt.Sprintf("Xcode %s · Codex %s · Claude %s", c.Xcode.Version, c.Xcode.CodexVersion, c.Xcode.Claude.Version)
			}
			widgets = append(widgets, u.note(text))
		}
		key = "xcode-" + c.Variant
		if c.Variant != "chat" {
			widgets = append(widgets, u.note(u.tr("Close Xcode before preparing its agent profile. Reopen Xcode and start a new conversation afterwards. Xcode controls its own model picker.", "Cierra Xcode antes de preparar el perfil del agente. Vuelve a abrirlo e inicia una conversación nueva. Xcode controla su selector de modelos.")))
		}
	}
	s := c.selection(key)
	u.syncClientSelection(key, s)
	if key == "claude" {
		if u.value("clients-claude-mode") == "" {
			u.setValue("clients-claude-mode", "installed")
		}
		widgets = append(widgets, u.row(u.selectField("clients-claude-mode", u.tr("Claude compatibility", "Compatibilidad de Claude"), []string{"installed", "modern"}), u.button("claude-detect", u.tr("Detect installed version", "Detectar versión instalada"), func() {
			u.call("GET", "/api/claude/info", nil, func(data json.RawMessage) {
				if json.Unmarshal(data, &c.Claude) == nil {
					c.ClaudeChecked = true
				}
			})
		})))
		version := u.tr("Not checked", "Sin comprobar")
		if c.ClaudeChecked {
			version = c.Claude.Version
			if version == "" {
				version = u.tr("Not detected; basic aliases", "No detectada; alias básicos")
			}
		}
		widgets = append(widgets, u.note("Claude Code: "+version+u.tr(". modern = 2.1.251+ with model picker and per-model effort.", ". modern = 2.1.251+ con lista y razonamiento por modelo.")))
	}
	protocol := u.tr("Uses Chat Completions. Model listing does not validate generation or tools.", "Usa Chat Completions. La lista no valida generación ni herramientas.")
	if key == "codex" || key == "codex-cli" || key == "xcode-codex" {
		protocol = u.tr("Uses Responses. Choose compatible models; each model exposes its supported reasoning levels.", "Usa Responses. Elige modelos compatibles; cada modelo expone sus niveles de razonamiento.")
	}
	if key == "claude" || key == "xcode-claude" {
		protocol = u.tr("Uses Anthropic Messages. All selected models and internal-task aliases must support it.", "Usa Anthropic Messages. Los modelos y los alias de tareas internas deben admitirlo.")
	}
	widgets = append(widgets, u.note(protocol))
	widgets = append(widgets, u.clientPicker(key, s))
	if key == "codex" || key == "codex-cli" {
		widgets = append(widgets, u.clientImagesPanel(key, s))
	}
	if key == "cursor" {
		widgets = append(widgets, u.cursorClientPanel(s))
		return u.column(widgets...)
	}
	if key == "generic" {
		base, local, _ := u.clientBase()
		id := s.Initial
		widgets = append(widgets, u.code("generic-guide", "Base URL: "+base+"\nAPI key: kl_local_••••••••\nModel: "+id), u.button("generic-copy", u.tr("Copy connection", "Copiar conexión"), func() { u.copy("Base URL: " + base + "\nAPI key: " + local + "\nModel: " + s.Initial) }))
		return u.column(widgets...)
	}
	if key == "claude" || key == "xcode-claude" {
		aliases := []layout.Widget{}
		for _, alias := range []string{"sonnet", "opus", "haiku"} {
			aliases = append(aliases, u.selectField("client:"+key+":alias:"+alias, alias+u.tr(" alias (empty = initial)", " alias (vacío = inicial)"), append([]string{""}, s.ids()...)))
		}
		widgets = append(widgets, u.row(aliases...))
		caps := u.clientCaps(key)
		if !caps.Picker {
			widgets = append(widgets, u.note(u.tr("This version uses model aliases; short picker names require a newer Claude. Only the initial model can set global effort.", "Esta versión usa alias; los nombres cortos requieren un Claude más reciente. Solo el modelo inicial puede fijar el razonamiento global.")))
		}
	}
	widgets = append(widgets, u.clientActions(key, s))
	return u.column(widgets...)
}

func (u *nativeUI) clientPicker(key string, s *nativeClientSelection) layout.Widget {
	prefix := "client:" + key + ":"
	limit := 50
	if key == "generic" {
		limit = 1
	}
	if key == "xcode-claude" && !u.clientCaps(key).Picker {
		limit = 3
	}
	add := func(m modelInfo) bool {
		if !catalogID.MatchString(m.ID) {
			u.notice = u.tr("Enter a valid exact model ID.", "Introduce un ID de modelo exacto válido.")
			return false
		}
		if key == "generic" && s.choice(m.ID) == nil {
			s.Models = nil
			s.Initial = ""
		}
		if err := s.add(m, limit); err != nil {
			u.notice = err.Error()
			return false
		}
		u.seedClientChoice(key, *s.choice(m.ID))
		return true
	}
	order := modelSortOrder(u.value("models.sort"))
	available := nativeVisibleModels(u.models, s, u.value(prefix+"search"), u.checked(prefix+"selected"), u.checked(prefix+"coding"), order, u.value("models.lab"))
	controls := []layout.Widget{u.modelPickerToolbar(prefix, s), u.pills(u.check(prefix+"selected", u.tr("Selected only", "Solo seleccionados"), func(bool) {}), u.check(prefix+"coding", u.tr("Text models with tools only", "Solo texto con herramientas"), func(bool) {}), u.check(prefix+"advanced", u.tr("Advanced options", "Opciones avanzadas"), func(bool) {})), u.pills(u.button(prefix+"select-all", u.tr("Select results", "Marcar resultados"), func() {
		for _, m := range available {
			if len(s.Models) >= limit {
				break
			}
			if s.choice(m.ID) == nil {
				add(m)
			}
		}
	}), u.button(prefix+"clear", u.tr("Clear selection", "Vaciar selección"), func() {
		s.Models = nil
		s.Initial = ""
		s.Aliases = map[string]string{}
		u.setChecked(prefix+"selected", false)
		for _, alias := range []string{"sonnet", "opus", "haiku"} {
			u.setValue(prefix+"alias:"+alias, "")
		}
	})), u.note(fmt.Sprintf(u.tr("%d selected · %d matching · up to %d models", "%d seleccionados · %d resultados · hasta %d modelos"), len(s.Models), len(available), limit) + u.tr(" · Prices: input / output per 1M tokens", " · Precios: entrada / salida por 1M tokens"))}
	controls = append(controls, u.note(u.modelSortHint(order)))
	cards := []layout.Widget{}
	cardIDs := []string{}
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
		row := []layout.Widget{u.modelCardHeading(heading...), u.modelPriceCells(m)}
		if metric := u.modelSortMetric(m, order); metric != "" {
			row = append(row, u.note(metric))
		}
		if m.MayTrain != nil && *m.MayTrain {
			row = append(row, u.note(u.tr("Kilo reports that this model may use prompts for training.", "Kilo indica que este modelo puede usar los mensajes para entrenamiento.")))
		}
		if m.ExpirationDate != "" {
			row = append(row, u.note(u.tr("Published retirement date: ", "Fecha de retirada publicada: ")+m.ExpirationDate))
		}
		if u.checked(prefix + "advanced") {
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
		if choice != nil {
			defaultLabel := u.tr("Use on startup", "Usar al iniciar")
			if s.Initial == id {
				defaultLabel = u.tr("★ Initial model", "★ Modelo inicial")
			}
			options := []layout.Widget{}
			primary := []layout.Widget{u.field(nativeClientField(key, id, "name"), u.tr("Display name", "Nombre visible"), m.Name, false), u.button(prefix+"initial:"+id, defaultLabel, func() {
				s.Initial = id
				if (key == "claude" || key == "xcode-claude") && !u.clientCaps(key).PerModelEffort {
					for _, other := range s.Models {
						if other.Model.ID != id {
							u.setValue(nativeClientField(key, other.Model.ID, "claude-effort"), "")
						}
					}
				}
			})}
			if key == "codex" || key == "codex-cli" || key == "xcode-codex" {
				levels, _ := nativeReasoningFor(*choice)
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
					primary = append(primary, u.selectField(nativeClientField(key, id, "reasoning"), u.tr("Initial reasoning", "Razonamiento inicial"), levels))
				} else {
					options = append(options, u.note(u.tr("No supported reasoning levels are configured.", "No hay niveles de razonamiento configurados.")))
				}
				if u.checked(prefix + "advanced") {
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
					if validClaudeEffort(id, effort) && (caps.PerModelEffort || effort != "xhigh") {
						efforts = append(efforts, effort)
					}
				}
				canEffort := len(efforts) > 1 && (caps.PerModelEffort || id == s.Initial)
				if !canEffort {
					u.setValue(nativeClientField(key, id, "claude-effort"), "")
				}
				primary = append(primary, u.disabled(canEffort, u.selectField(nativeClientField(key, id, "claude-effort"), u.tr("Reasoning (empty = automatic)", "Razonamiento (vacío = automático)"), efforts)))
			}
			row = append(row, u.row(primary...))
			row = append(row, options...)
			if (key == "opencode" || key == "zed") && u.checked(prefix+"advanced") {
				row = append(row, u.row(u.field(nativeClientField(key, id, "context"), u.tr("Context tokens", "Tokens de contexto"), "200000", false), u.field(nativeClientField(key, id, "output"), u.tr("Max output (0 = unspecified)", "Salida máxima (0 = sin especificar)"), "0", false)))
			}
		}
		cards = append(cards, u.modelCard(choice != nil, row...))
		cardIDs = append(cardIDs, id)
	}
	if len(cards) == 0 {
		controls = append(controls, u.note(u.tr("No matches. Refresh or add an exact model ID.", "Sin resultados. Actualiza o añade un ID exacto.")))
	} else {
		controls = append(controls, u.modelGrid(prefix+"models", cardIDs, cards))
	}
	if len(available) > 150 {
		controls = append(controls, u.note(u.tr("Showing the first 150 results. Search to narrow the catalog.", "Se muestran los primeros 150 resultados. Busca para acotar el catálogo.")))
	}
	controls = append(controls, u.actionRow(u.field(prefix+"manual", u.tr("Add an exact model ID", "Añadir un ID de modelo exacto"), "provider/model", false), u.button(prefix+"add", u.tr("Add model", "Añadir modelo"), func() {
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
	return u.column(controls...)
}

func (u *nativeUI) clientActions(key string, s *nativeClientSelection) layout.Widget {
	base, local, _ := u.clientBase()
	fingerprint := nativeSelectionFingerprint(key, s, base, local, u.clientCaps(key))
	ready := s.Saved != "" && s.Saved == fingerprint
	_, validation := nativeClientPayload(key, s)
	working := u.busy["POST"+nativeClientEndpoint(key)] || u.busy["GET"+nativeClientEndpoint(key)] || u.clientState().Launching != ""
	canSave := len(s.Models) > 0 && validation == nil && !working
	if (key == "codex" || key == "codex-cli") && !nativeClientImagesReady(s, u.models) {
		canSave = false
	}
	if strings.HasPrefix(key, "xcode-") && key != "xcode-chat" && !u.clientState().Xcode.Available {
		canSave = false
	}
	status := u.tr("Choose models, then launch. Your profile is prepared automatically.", "Elige modelos y abre el editor. El perfil se prepara automáticamente.")
	if s.Saved != "" {
		status = u.tr("Unsaved changes will be prepared when you launch.", "Los cambios pendientes se prepararán al abrir.")
	}
	if ready {
		status = u.tr("Saved: ", "Guardado: ") + s.Path
	}
	if validation != nil && len(s.Models) > 0 {
		status = validation.Error()
	}
	widgets := []layout.Widget{u.clientLauncherPanel(key, s, canSave), u.row(u.disabled(canSave, u.button("client:"+key+":prepare", u.tr("Prepare without launching", "Preparar sin abrir"), func() { u.prepareClient(key) })), u.disabled(!working, u.button("client:"+key+":load", u.tr("Load saved selection", "Cargar selección guardada"), func() { u.loadClient(key) }))), u.note(status), u.note(u.tr("Existing preferences are preserved. Changed files receive .bak backups. Loading a selection requires preparing it again for the current connection.", "Se conservan los ajustes existentes y se guardan copias .bak. Tras cargar una selección, prepárala de nuevo para aplicar la conexión actual."))}
	widgets = append(widgets, u.check("client:"+key+":show-command", u.tr("Show launch command (optional)", "Mostrar comando de arranque (opcional)"), func(bool) {}))
	showCommand := u.checked("client:" + key + ":show-command")
	if showCommand {
		if key == "codex" {
			widgets = append(widgets, u.row(u.selectField("clients-platform", u.tr("Command operating system", "Sistema operativo del comando"), []string{"macos", "windows", "linux"}), u.field("clients-app-path", u.tr("Command application path", "Ruta de aplicación del comando"), "/Applications/ChatGPT.app", false)))
		}
		if key == "codex-cli" || key == "claude" || key == "opencode" {
			widgets = append(widgets, u.selectField("clients-shell", u.tr("Command shell", "Shell del comando"), []string{"unix", "powershell"}))
		}
	}
	copyLabel := u.tr("Copy launch command", "Copiar comando de arranque")
	if key == "zed" {
		copyLabel = u.tr("Copy key for Zed (one-time setup)", "Copiar clave para Zed (configuración inicial)")
	}
	if key == "xcode-chat" {
		copyLabel = u.tr("Copy Xcode connection (one-time setup)", "Copiar conexión de Xcode (configuración inicial)")
	}
	if (showCommand || key == "zed" || key == "xcode-chat") && key != "xcode-codex" && key != "xcode-claude" {
		widgets = append(widgets, u.disabled(ready, u.button("client:"+key+":copy-launch", copyLabel, func() {
			u.syncClientSelection(key, s)
			if s.Saved == "" || s.Saved != nativeSelectionFingerprint(key, s, base, local, u.clientCaps(key)) {
				u.notice = u.tr("Prepare the changed profile before launching.", "Prepara el perfil modificado antes de arrancar.")
				return
			}
			if text, err := u.clientLaunch(key, s, true); err != nil {
				u.notice = err.Error()
			} else {
				u.copy(text)
			}
		})))
	}
	if key == "zed" {
		widgets = append(widgets, u.note(u.tr("In Zed, open agent: open settings, find kilo-local and paste the local key once. Zed saves it in its keychain. Select a model in the Agent panel.", "En Zed, abre agent: open settings, busca kilo-local y pega la clave local una vez. Zed la guarda en su llavero. Elige modelo en el panel Agent.")))
	}
	if key == "opencode" {
		widgets = append(widgets, u.note(u.tr("Launch opens a terminal in your project; then use /models. No /connect needed. Global/project OpenCode settings still merge and may override this profile.", "Abrir inicia una terminal en tu proyecto; después usa /models. No hace falta /connect. Los ajustes globales/del proyecto se combinan y pueden prevalecer.")))
	}
	if key == "codex" || key == "codex-cli" {
		widgets = append(widgets, u.note(u.tr("The normal Codex profile stays separate. Restart the Kilo instance after preparing changes, then choose the model and reasoning in Codex.", "El perfil normal de Codex queda separado. Reinicia la instancia Kilo tras preparar cambios y elige modelo y razonamiento en Codex.")), u.disabled(len(s.Models) > 0, u.button("client:"+key+":catalog-copy", u.tr("Copy models.json", "Copiar models.json"), func() {
			u.syncClientSelection(key, s)
			data, err := buildCodexCatalog(s.Models, s.Initial, false)
			if err != nil {
				u.notice = err.Error()
			} else {
				u.copy(string(data))
			}
		})))
	}
	if ready && (showCommand || strings.HasPrefix(key, "xcode-") || key == "zed") {
		if text, err := u.clientLaunch(key, s, false); err == nil && text != "" {
			widgets = append(widgets, u.code("client:"+key+":launch-preview", text))
		}
	}
	widgets = append(widgets, u.check("client:"+key+":show-config", u.tr("Show optional configuration export", "Mostrar exportación de configuración opcional"), func(bool) {}))
	if u.checked("client:"+key+":show-config") && len(s.Models) > 0 {
		if text, err := u.clientExport(key, s, false); err == nil {
			widgets = append(widgets, u.code("client:"+key+":config", text), u.button("client:"+key+":config-copy", u.tr("Copy complete configuration", "Copiar configuración completa"), func() {
				u.syncClientSelection(key, s)
				text, err := u.clientExport(key, s, true)
				if err != nil {
					u.notice = err.Error()
				} else {
					u.copy(text)
				}
			}))
		} else {
			widgets = append(widgets, u.note(err.Error()))
		}
	}
	return u.column(widgets...)
}

func (u *nativeUI) prepareClient(key string) {
	u.prepareClientAfter(key, func(err error) {
		if err != nil {
			u.notice = nativeMessage(err.Error(), u.language)
		}
	})
}

func (u *nativeUI) prepareClientAfter(key string, done func(error)) {
	s := u.clientState().selection(key)
	u.syncClientSelection(key, s)
	payload, err := nativeClientPayload(key, s)
	if err != nil {
		done(err)
		return
	}
	base, local, _ := u.clientBase()
	fingerprint := nativeSelectionFingerprint(key, s, base, local, u.clientCaps(key))
	imagesSent := cloneClientImageSettings(s.ImageGeneration)
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
		s.Path = result.ProfileDir
		if result.ConfigPath != "" {
			s.Path = result.ConfigPath
		}
		s.Saved = fingerprint
		if key == "codex" || key == "codex-cli" {
			u.acceptClientImages(imagesSent)
		}
		u.notice = u.tr("Editor profile prepared.", "Perfil del editor preparado.")
		done(nil)
	})
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
			u.notice = u.tr("Your selection changed while loading. Load again to replace those edits.", "Tu selección cambió durante la carga. Carga de nuevo para sustituir esos cambios.")
			return
		}
		s, err := decodeNativeClientSelection(key, data, u.models)
		if err != nil {
			u.notice = err.Error()
			return
		}
		u.clientState().Selections[key] = s
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
		u.notice = u.tr("Selection loaded. Prepare again to apply the current connection.", "Selección cargada. Prepara de nuevo para aplicar la conexión actual.")
	})
}

func decodeNativeClientSelection(key string, data []byte, catalog []modelInfo) (*nativeClientSelection, error) {
	s := &nativeClientSelection{Aliases: map[string]string{}, Mode: "installed"}
	lookup := func(id string) modelInfo {
		for _, m := range catalog {
			if m.ID == id {
				return m
			}
		}
		return modelInfo{ID: id, Name: id, ContextWindow: 200000}
	}
	if key == "codex" || key == "codex-cli" || key == "xcode-codex" {
		var envelope struct {
			Catalog         json.RawMessage          `json:"catalog"`
			ImageGeneration *imageGenerationSettings `json:"imageGeneration"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return nil, err
		}
		if key != "xcode-codex" && envelope.ImageGeneration != nil {
			images := *envelope.ImageGeneration
			s.ImageGeneration = cloneClientImageSettings(&images)
			s.imageGenerationBaseline = cloneClientImageSettings(&images)
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
			model.ContextWindow = m.Context
			model.InputModalities = m.Input
			choice := nativeModelChoice{Model: model, DisplayName: m.Name, ReasoningCustom: true, DefaultReasoning: m.Effort, ReasoningLevels: []string{}}
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
	if key == "opencode" || key == "zed" {
		var source struct {
			Selection  editorSelection `json:"selection"`
			ConfigPath string          `json:"configPath"`
		}
		if err := json.Unmarshal(data, &source); err != nil {
			return nil, err
		}
		if err := validateEditorSelection(source.Selection); err != nil {
			return nil, err
		}
		s.Initial = source.Selection.Initial
		s.Path = source.ConfigPath
		for _, m := range source.Selection.Models {
			model := lookup(m.ID)
			model.ContextWindow = m.Context
			model.MaxOutputTokens = m.Output
			s.Models = append(s.Models, nativeModelChoice{Model: model, DisplayName: m.Name})
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
		s.Models = append(s.Models, nativeModelChoice{Model: lookup(m.ID), DisplayName: m.DisplayName, ClaudeEffort: m.Effort})
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
	case "zed":
		if reveal {
			return local, nil
		}
		return u.tr("Provider: kilo-local\nPaste the copied key in Zed Agent settings.", "Proveedor: kilo-local\nPega la clave copiada en los ajustes de Zed Agent."), nil
	case "xcode-chat":
		return xcodeChatGuide(base, local, u.language), nil
	case "xcode-codex", "xcode-claude":
		return u.tr("Profile ready. Reopen Xcode, enable its coding agent in Settings → Intelligence, and start a new conversation.", "Perfil preparado. Vuelve a abrir Xcode, activa el agente en Settings → Intelligence e inicia una conversación nueva."), nil
	}
	return "", nil
}

func (u *nativeUI) clientExport(key string, s *nativeClientSelection, reveal bool) (string, error) {
	base, local, port := u.clientBase()
	if !reveal {
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
	if key == "codex" || key == "codex-cli" {
		catalog, err := buildCodexCatalog(s.Models, s.Initial, false)
		if err != nil {
			return "", err
		}
		data, err := mergeCodexConfig(nil, catalog, port)
		if err == nil && s.ImageGeneration != nil {
			data, err = mergeCodexImages(data, *s.ImageGeneration, port)
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

func (u *nativeUI) cursorClientPanel(s *nativeClientSelection) layout.Widget {
	var session *cursorSession
	if value := u.state["cursor"]; value != nil {
		data, _ := json.Marshal(value)
		_ = json.Unmarshal(data, &session)
	}
	running, _ := u.state["running"].(bool)
	connected := session != nil && (session.Status == "running" || session.Status == "starting")
	invoke := func(action string) {
		u.call("POST", "/api/cursor", map[string]any{"action": action, "models": s.ids()}, func(data json.RawMessage) {
			var result struct {
				Session *cursorSession `json:"session"`
			}
			if action != "check" && json.Unmarshal(data, &result) == nil {
				u.state["cursor"] = result.Session
			}
			if action == "check" {
				u.notice = u.tr("Public HTTPS and authentication verified. Test a chat in Cursor.", "HTTPS público y autenticación verificados. Prueba un chat en Cursor.")
			}
		})
	}
	status := u.tr("Disconnected. Start the proxy and choose models.", "Desconectado. Inicia el proxy y elige modelos.")
	if session != nil {
		status = session.Status
		if session.Error != "" {
			status = session.Error
		}
	}
	widgets := []layout.Widget{u.heading("Cursor"), u.clientLauncherPanel("cursor", s, session != nil && session.Status == "running"), u.note(u.tr("Cursor sends provider requests from its servers, so they cannot reach localhost. This helper starts a dedicated HTTPS ngrok tunnel with its own key and selected model list.", "Cursor envía las peticiones desde sus servidores y no puede alcanzar localhost. Este helper inicia un túnel HTTPS ngrok con su propia clave y lista de modelos.")), u.row(u.button("cursor-ngrok-download", u.tr("Install ngrok", "Instalar ngrok"), func() { u.open("https://ngrok.com/download") }), u.button("cursor-ngrok-account", u.tr("Get ngrok authtoken", "Obtener authtoken de ngrok"), func() { u.open("https://dashboard.ngrok.com/get-started/your-authtoken") })), u.code("cursor-ngrok-command", "ngrok config add-authtoken YOUR_NGROK_AUTHTOKEN"), u.button("cursor-copy-ngrok-command", u.tr("Copy ngrok command", "Copiar comando de ngrok"), func() { u.copy("ngrok config add-authtoken YOUR_NGROK_AUTHTOKEN") }), u.note(u.tr("The public tunnel carries prompts/responses through ngrok. Only inference is exposed; the control panel stays local. Disconnect revokes this Cursor key.", "El túnel público lleva mensajes/respuestas a través de ngrok. Solo se expone inferencia; el panel queda local. Desconectar revoca la clave de Cursor.")), u.row(u.disabled(running && !connected && len(s.Models) > 0, u.button("cursor-connect", u.tr("Connect HTTPS tunnel", "Conectar túnel HTTPS"), func() { invoke("start") })), u.disabled(session != nil, u.button("cursor-disconnect", u.tr("Disconnect", "Desconectar"), func() { invoke("stop") })), u.disabled(session != nil && session.Status == "running", u.button("cursor-check", u.tr("Test public connection", "Probar conexión pública"), func() { invoke("check") }))), u.note(status), u.button("cursor-copy-models", u.tr("Copy model IDs", "Copiar IDs de modelos"), func() { u.copy(strings.Join(s.ids(), "\n")) }), u.code("cursor-guide", cursorSetupGuide(session, s.ids(), u.language, false))}
	if session != nil && session.Status == "running" {
		widgets = append(widgets, u.row(u.button("cursor-copy-url", u.tr("Copy Cursor URL", "Copiar URL de Cursor"), func() { u.copy(session.URL) }), u.button("cursor-copy-key", u.tr("Copy Cursor key", "Copiar clave de Cursor"), func() { u.copy(session.Key) }), u.button("cursor-copy-guide", u.tr("Copy complete connection", "Copiar conexión completa"), func() { u.copy(cursorSetupGuide(session, s.ids(), u.language, true)) })))
	}
	widgets = append(widgets, u.note(u.tr("Disable the OpenAI URL/key override to return to Cursor's built-in providers. Kilo does not supply Tab or Composer; not every model supports Cursor BYOK.", "Desactiva la URL/clave OpenAI alternativa para volver a los proveedores de Cursor. Kilo no proporciona Tab ni Composer; no todos los modelos admiten BYOK de Cursor.")))
	return u.column(widgets...)
}

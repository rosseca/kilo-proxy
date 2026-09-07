package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
)

// Selection metadata is separate from the gateway's exact model identity.
// ReasoningCustom preserves an explicit empty override (disable reasoning).
type nativeModelChoice struct {
	Model            modelInfo
	DisplayName      string
	ReasoningLevels  []string
	ReasoningCustom  bool
	DefaultReasoning string
	ClaudeEffort     string
}

type nativeReasoning struct {
	Levels  []string
	Initial string
}

var nativeReasoningLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}

const nativeCodexInstructions = "You are a coding assistant working with the user in a shared workspace. Follow the user instructions and applicable repository guidance. Inspect relevant files before making changes. Use the available tools to complete the requested work, preserve unrelated user changes, and verify changes with appropriate checks. Explain results and limitations clearly. Do not claim actions or tests you did not perform."

func nativeReasoningFor(choice nativeModelChoice) ([]string, string) {
	levels, initial := nativePublishedReasoning(choice)
	// The native default selector can change independently of the advanced
	// levels override. Accept only a level already resolved for this model.
	if !choice.ReasoningCustom && choice.DefaultReasoning != "" && helperContains(levels, choice.DefaultReasoning) {
		initial = choice.DefaultReasoning
	}
	return levels, initial
}

func nativePublishedReasoning(choice nativeModelChoice) ([]string, string) {
	preset := nativeKnownReasoning[strings.TrimPrefix(choice.Model.ID, "~")]
	declared, preferred := choice.Model.ReasoningEfforts, preset.Initial
	if choice.ReasoningCustom {
		declared, preferred = choice.ReasoningLevels, choice.DefaultReasoning
	}
	if choice.ReasoningCustom || len(declared) > 0 {
		levels := []string{}
		for _, level := range nativeReasoningLevels {
			if helperContains(declared, level) {
				levels = append(levels, level)
			}
		}
		if helperContains(levels, preferred) {
			return levels, preferred
		}
		if helperContains(levels, "medium") {
			return levels, "medium"
		}
		for _, level := range levels {
			if level != "none" && level != "minimal" {
				return levels, level
			}
		}
		if len(levels) > 0 {
			return levels, levels[0]
		}
		return levels, ""
	}
	return append([]string{}, preset.Levels...), preset.Initial
}

func nativeCodexDisplayName(choice nativeModelChoice) string {
	custom := strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, choice.DisplayName))
	// Match the browser helper's 80 UTF-16-unit display-name limit.
	units := utf16.Encode([]rune(custom))
	if len(units) > 80 {
		custom = string(utf16.Decode(units[:80]))
	}
	if custom != "" {
		return custom
	}
	if choice.Model.Name != "" {
		return choice.Model.Name
	}
	return choice.Model.ID
}

func buildCodexCatalog(models []nativeModelChoice, initial string, xcode bool) ([]byte, error) {
	ordered := append([]nativeModelChoice{}, models...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Model.ID == initial && ordered[j].Model.ID != initial })
	seen := map[string]bool{}
	entries := []map[string]any{}
	for _, choice := range ordered {
		m := choice.Model
		if !helperValidModelID(m.ID) || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		levels, preferred := nativeReasoningFor(choice)
		if xcode {
			filtered := []string{}
			for _, level := range levels {
				if level != "max" && level != "ultra" {
					filtered = append(filtered, level)
				}
			}
			levels = filtered
			if !helperContains(levels, preferred) {
				preferred = ""
				if helperContains(levels, "high") {
					preferred = "high"
				} else if len(levels) > 0 {
					preferred = levels[0]
				}
			}
		}
		supported := make([]map[string]string, 0, len(levels))
		for _, level := range levels {
			supported = append(supported, map[string]string{"effort": level, "description": "Reasoning effort: " + level})
		}
		var initialEffort any
		if preferred != "" {
			initialEffort = preferred
		}
		modalities := []string{"text"}
		if helperContains(m.InputModalities, "image") {
			modalities = append(modalities, "image")
		}
		entry := map[string]any{
			"slug": m.ID, "display_name": nativeCodexDisplayName(choice), "description": "Kilo Gateway · " + m.ID,
			"default_reasoning_level": initialEffort, "supported_reasoning_levels": supported,
			"shell_type": "default", "visibility": "list", "supported_in_api": true, "priority": len(entries),
			"base_instructions": nativeCodexInstructions, "supports_reasoning_summaries": false,
			"supports_reasoning_summary_parameter": false, "support_verbosity": false, "prefer_websockets": false,
			"use_responses_lite": false, "supports_parallel_tool_calls": false, "experimental_supported_tools": []string{},
			"truncation_policy": map[string]any{"mode": "tokens", "limit": 10000}, "input_modalities": modalities,
		}
		if m.ContextWindow >= 1024 && m.ContextWindow <= 100000000 {
			entry["context_window"] = m.ContextWindow
		}
		entries = append(entries, entry)
	}
	data, err := json.MarshalIndent(map[string]any{"models": entries}, "", "  ")
	if err != nil {
		return nil, err
	}
	if _, err = validateCatalog(data); err != nil {
		return nil, errors.New("Choose 1–50 valid unique models and reasoning levels")
	}
	if xcode {
		if err = validateXcodeCodexCatalog(data); err != nil {
			return nil, err
		}
	}
	return data, nil
}

func helperContains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func helperText(language, en, es string) string {
	if language == "es" {
		return es
	}
	return en
}
func helperValidModelID(id string) bool {
	return id != "" && len(id) <= 256 && strings.IndexFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}
func helperShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
func helperPowerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
func helperShell(shell string) (string, error) {
	if shell == "" {
		shell = "unix"
	}
	if shell != "unix" && shell != "powershell" {
		return "", errors.New("Choose Unix shell or PowerShell")
	}
	return shell, nil
}
func helperCommandValue(value string) bool {
	return len(value) <= 8192 && !strings.ContainsAny(value, "\x00\r\n")
}

var helperWindowsAbsolute = regexp.MustCompile(`^(?:[A-Za-z]:[\\/]|\\\\[^\\]+\\[^\\]+)`)

func codexLaunchCommand(desktop bool, shell, platform, appPath, key, language string, catalog bool) (string, error) {
	shell, err := helperShell(shell)
	if err != nil {
		return "", err
	}
	if key == "" || !helperCommandValue(key) {
		return "", errors.New("A valid local proxy key is required")
	}
	if desktop {
		if platform != "macos" && platform != "windows" && platform != "linux" {
			return "", errors.New("Choose macOS, Windows or Linux")
		}
		if !helperCommandValue(appPath) || (platform == "windows" && !helperWindowsAbsolute.MatchString(appPath)) || (platform != "windows" && !strings.HasPrefix(appPath, "/")) {
			return "", errors.New("Enter the absolute path to the Codex desktop application")
		}
	}
	profile := ".codex-kilo-cli"
	if desktop {
		profile = ".codex-kilo-desktop"
	}
	saveFirst := helperText(language, "Save the configuration to this profile first:", "Guarda primero la configuración en este perfil:")
	saveCatalog := helperText(language, "Save models.json in the Kilo profile first.", "Guarda primero models.json en el perfil de Kilo.")
	if desktop && platform == "windows" || !desktop && shell == "powershell" {
		names := []string{"CODEX_HOME", "KILO_LOCAL_API_KEY"}
		if desktop {
			names = append(names, "CODEX_ELECTRON_USER_DATA_PATH")
		}
		for i := range names {
			names[i] = helperPowerShellQuote(names[i])
		}
		check := ""
		if catalog {
			check = "  if (!(Test-Path (Join-Path $kiloHome 'models.json'))) { throw " + helperPowerShellQuote(saveCatalog) + " }\n"
		}
		launch := "\n    codex"
		if desktop {
			launch = "\n    $kiloUI = Join-Path $env:LOCALAPPDATA 'Codex Kilo'\n    New-Item -ItemType Directory -Force -Path $kiloUI | Out-Null\n    $env:CODEX_ELECTRON_USER_DATA_PATH = $kiloUI\n    Start-Process -FilePath " + helperPowerShellQuote(appPath) + " -ArgumentList @('--user-data-dir=\"' + $kiloUI + '\"')"
		}
		return fmt.Sprintf(`& {
  $kiloHome = Join-Path $env:USERPROFILE '%s'
  if (!(Test-Path (Join-Path $kiloHome 'config.toml'))) { throw (%s + $kiloHome + '\config.toml') }
%s  $kiloPrevious = @{}
  $kiloNames = @(%s)
  foreach ($kiloName in $kiloNames) { $kiloPrevious[$kiloName] = [Environment]::GetEnvironmentVariable($kiloName, 'Process') }
  try {
    $env:CODEX_HOME = $kiloHome
    $env:KILO_LOCAL_API_KEY = %s%s
  } finally {
    foreach ($kiloName in $kiloNames) { [Environment]::SetEnvironmentVariable($kiloName, $kiloPrevious[$kiloName], 'Process') }
  }
}`, profile, helperPowerShellQuote(saveFirst+" "), check, strings.Join(names, ", "), helperPowerShellQuote(key), launch), nil
	}
	intro := fmt.Sprintf("(\n  kilo_home=\"$HOME/%s\"\n  if [ ! -f \"$kilo_home/config.toml\" ]; then\n    printf '%%s %%s\\n' %s \"$kilo_home/config.toml\" >&2\n    exit 1\n  fi", profile, helperShellQuote(saveFirst))
	if catalog {
		intro += "\n  if [ ! -f \"$kilo_home/models.json\" ]; then\n    printf '%s\\n' " + helperShellQuote(saveCatalog) + " >&2\n    exit 1\n  fi"
	}
	if !desktop {
		return intro + "\n  env CODEX_HOME=\"$kilo_home\" KILO_LOCAL_API_KEY=" + helperShellQuote(key) + " codex\n)", nil
	}
	if platform == "macos" {
		return intro + "\n  kilo_ui=\"$HOME/Library/Application Support/Codex Kilo\"\n  mkdir -p \"$kilo_ui\" || exit 1\n  open -n --env \"CODEX_HOME=$kilo_home\" \\\n    --env \"CODEX_ELECTRON_USER_DATA_PATH=$kilo_ui\" \\\n    --env " + helperShellQuote("KILO_LOCAL_API_KEY="+key) + " \\\n    " + helperShellQuote(appPath) + " --args \"--user-data-dir=$kilo_ui\"\n)", nil
	}
	return intro + "\n  kilo_ui=\"${XDG_CONFIG_HOME:-$HOME/.config}/codex-kilo-desktop\"\n  mkdir -p \"$kilo_ui\" || exit 1\n  env CODEX_HOME=\"$kilo_home\" CODEX_ELECTRON_USER_DATA_PATH=\"$kilo_ui\" \\\n    KILO_LOCAL_API_KEY=" + helperShellQuote(key) + " \\\n    " + helperShellQuote(appPath) + " \"--user-data-dir=$kilo_ui\"\n)", nil
}

var nativeClaudeResetEnv = func() []string {
	names := []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_EFFORT_LEVEL", "ANTHROPIC_CUSTOM_HEADERS", "CLAUDE_CODE_SUBAGENT_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"}
	for _, alias := range []string{"SONNET", "OPUS", "HAIKU", "FABLE"} {
		for _, suffix := range []string{"", "_NAME", "_DESCRIPTION", "_SUPPORTED_CAPABILITIES"} {
			names = append(names, "ANTHROPIC_DEFAULT_"+alias+"_MODEL"+suffix)
		}
	}
	return names
}()

func claudeLaunchCommand(shell, language string) (string, error) {
	shell, err := helperShell(shell)
	if err != nil {
		return "", err
	}
	message := helperText(language, "Prepare Claude Code in Kilo Local first.", "Prepara Claude Code desde Kilo Local primero.")
	if shell == "powershell" {
		names := append([]string{"CLAUDE_CONFIG_DIR"}, nativeClaudeResetEnv...)
		for i := range names {
			names[i] = helperPowerShellQuote(names[i])
		}
		return fmt.Sprintf(`& {
  $kiloHome = Join-Path $env:USERPROFILE '.claude-kilo'
  $kiloSettings = Join-Path $kiloHome 'settings.json'
  if (!(Test-Path -PathType Leaf $kiloSettings)) { throw %s }
  $kiloNames = @(%s)
  $kiloPrevious = @{}
  foreach ($kiloName in $kiloNames) { $kiloPrevious[$kiloName] = [Environment]::GetEnvironmentVariable($kiloName, 'Process') }
  try {
    foreach ($kiloName in $kiloNames) { [Environment]::SetEnvironmentVariable($kiloName, $null, 'Process') }
    $env:CLAUDE_CONFIG_DIR = $kiloHome
    claude --settings $kiloSettings
  } finally {
    foreach ($kiloName in $kiloNames) { [Environment]::SetEnvironmentVariable($kiloName, $kiloPrevious[$kiloName], 'Process') }
  }
}`, helperPowerShellQuote(message), strings.Join(names, ", ")), nil
	}
	return fmt.Sprintf("(\n  kilo_home=\"$HOME/.claude-kilo\"\n  if [ ! -f \"$kilo_home/settings.json\" ]; then\n    printf '%%s\\n' %s >&2\n    exit 1\n  fi\n  unset %s\n  CLAUDE_CONFIG_DIR=\"$kilo_home\" claude --settings \"$kilo_home/settings.json\"\n)", helperShellQuote(message), strings.Join(nativeClaudeResetEnv, " ")), nil
}

func openCodeLaunchCommand(configPath, initialModel, shell string) (string, error) {
	shell, err := helperShell(shell)
	if err != nil {
		return "", err
	}
	if !helperCommandValue(configPath) || (!strings.HasPrefix(configPath, "/") && !helperWindowsAbsolute.MatchString(configPath)) {
		return "", errors.New("Use an absolute OpenCode configuration path")
	}
	if !catalogID.MatchString(initialModel) {
		return "", errors.New("Choose a valid OpenCode model")
	}
	if shell == "powershell" {
		return fmt.Sprintf(`& {
  $kiloConfig = %s
  if (!(Test-Path -LiteralPath $kiloConfig -PathType Leaf)) { throw 'Prepare OpenCode first.' }
  $kiloPrevious = $env:OPENCODE_CONFIG
  $kiloInline = $env:OPENCODE_CONFIG_CONTENT
  try {
    $env:OPENCODE_CONFIG = $kiloConfig
    Remove-Item Env:OPENCODE_CONFIG_CONTENT -ErrorAction SilentlyContinue
    opencode --model %s
  } finally {
    $env:OPENCODE_CONFIG = $kiloPrevious
    $env:OPENCODE_CONFIG_CONTENT = $kiloInline
  }
}`, helperPowerShellQuote(configPath), helperPowerShellQuote("kilo-local/"+initialModel)), nil
	}
	return "(\n  kilo_config=" + helperShellQuote(configPath) + "\n  [ -f \"$kilo_config\" ] || { printf '%s\\n' 'Prepare OpenCode first.' >&2; exit 1; }\n  unset OPENCODE_CONFIG_CONTENT\n  OPENCODE_CONFIG=\"$kilo_config\" opencode --model " + helperShellQuote("kilo-local/"+initialModel) + "\n)", nil
}

// Exact fallback presets shared with the browser helper; gateway metadata wins.
var nativeKnownReasoning = map[string]nativeReasoning{
	"openai/gpt-5.6-sol-discounted":   {Levels: []string{"none", "low", "medium", "high", "xhigh", "max"}, Initial: "low"},
	"z-ai/glm-5.3":                    {Levels: []string{"low", "high", "max"}, Initial: "max"},
	"z-ai/glm-5.3-flash":              {Levels: []string{"low", "high", "max"}, Initial: "max"},
	"openai/gpt-6-astra":              {Levels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, Initial: "low"},
	"openai/gpt-5.6-sol":              {Levels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, Initial: "low"},
	"openai/gpt-5.6-terra":            {Levels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, Initial: "medium"},
	"openai/gpt-5.6-luna":             {Levels: []string{"low", "medium", "high", "xhigh", "max"}, Initial: "medium"},
	"openai/gpt-daybreak-blue-latest": {Levels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, Initial: "low"},
	"openai/gpt-daybreak-red-latest":  {Levels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, Initial: "medium"},
	"openai/gpt-5.5":                  {Levels: []string{"low", "medium", "high", "xhigh"}, Initial: "medium"},
	"openai/gpt-5.4":                  {Levels: []string{"low", "medium", "high", "xhigh"}, Initial: "medium"},
	"openai/gpt-5.4-mini":             {Levels: []string{"low", "medium", "high", "xhigh"}, Initial: "medium"},
	"openai/gpt-5.2":                  {Levels: []string{"low", "medium", "high", "xhigh"}, Initial: "medium"},
	"anthropic/claude-fable-5.1":      {Levels: []string{"low", "medium", "high", "xhigh", "max"}, Initial: "high"},
	"anthropic/claude-fable-5":        {Levels: []string{"low", "medium", "high", "xhigh", "max"}, Initial: "high"},
	"anthropic/claude-mythos-5.1":     {Levels: []string{"low", "medium", "high", "xhigh", "max"}, Initial: "high"},
	"anthropic/claude-mythos-5":       {Levels: []string{"low", "medium", "high", "xhigh", "max"}, Initial: "high"},
	"anthropic/claude-opus-5":         {Levels: []string{"low", "medium", "high", "xhigh", "max"}, Initial: "high"},
	"anthropic/claude-opus-4.8":       {Levels: []string{"low", "medium", "high", "xhigh", "max"}, Initial: "high"},
	"anthropic/claude-opus-4.7":       {Levels: []string{"low", "medium", "high", "xhigh", "max"}, Initial: "high"},
	"anthropic/claude-sonnet-5":       {Levels: []string{"low", "medium", "high", "xhigh", "max"}, Initial: "high"},
	"anthropic/claude-opus-4.6":       {Levels: []string{"low", "medium", "high", "max"}, Initial: "high"},
	"anthropic/claude-sonnet-4.6":     {Levels: []string{"low", "medium", "high", "max"}, Initial: "high"},
}

func xcodeChatGuide(baseURL, key, language string) string {
	baseURL = strings.TrimSuffix(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	guide := helperText(language, nativeXcodeGuideEN, nativeXcodeGuideES)
	return strings.NewReplacer("NATIVE_BASE_SENTINEL", baseURL, "NATIVE_KEY_SENTINEL", key).Replace(guide)
}

func cursorSetupGuide(session *cursorSession, selected []string, language string, reveal bool) string {
	if session != nil && session.Status == "running" {
		key := "••••••••••••••••"
		if reveal {
			key = session.Key
		}
		ending := helperText(language, "Enable the OpenAI key and URL override. Add each model ID, then select it in chat. Disable the override to return to Cursor built-in models. Tab and Composer are not provided by Kilo.", "Activa la clave OpenAI y la URL alternativa. Añade cada ID y selecciónalo en el chat. Desactiva la URL alternativa para volver a los modelos propios de Cursor. Kilo no proporciona Tab ni Composer.")
		return "Cursor → Settings → Models\n\nOverride OpenAI Base URL: " + session.URL + "\nOpenAI API Key: " + key + "\n\nAdd Custom Model:\n" + strings.Join(session.Models, "\n") + "\n\n" + ending
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, id := range selected {
		if helperValidModelID(id) && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	models := strings.Join(ids, "\n")
	if models == "" {
		models = helperText(language, "(Select and add models in the helper.)", "(Selecciona y añade modelos en el helper.)")
	}
	return strings.ReplaceAll(helperText(language, nativeCursorGuideEN, nativeCursorGuideES), "NATIVE_MODELS_SENTINEL", models)
}

const nativeXcodeGuideEN = "Xcode → Settings → Intelligence → Add a Chat Provider (or Add a Model Provider)\n\nChoose Internet Hosted to supply authentication for this local URL.\nURL: NATIVE_BASE_SENTINEL/xcode\nAPI Key Header: Authorization\nAPI Key: Bearer NATIVE_KEY_SENTINEL\n\nDo not append /v1: Xcode adds it. Keep Kilo Local running.\nXcode fetches the saved selection from NATIVE_BASE_SENTINEL/xcode/v1/models.\nSelect a model in Xcode. Re-add or refresh the provider if its list is stale.\nModels must support Chat Completions; listing does not verify generation access."

const nativeCursorGuideEN = "Cursor · setup guide (external HTTPS endpoint required)\n\nKilo Local is loopback-only. Cursor's servers cannot reach it.\nDo not paste its localhost URL or local key into Cursor.\n\nConnect the ngrok tunnel in the Cursor helper to obtain the public URL and Cursor key. Then:\n1. Cursor Settings > Models: enable OpenAI API Key.\n2. Override OpenAI Base URL: use that gateway's public HTTPS API URL.\n3. API key: use the credential issued for that gateway.\n4. Add Custom Model / Add model: add each exact ID below and enable it.\n5. Choose a model in Cursor's picker and verify a request.\n\nDo not remove the provider prefix or use a display name instead of the ID.\nAdding an ID does not prove protocol or organization compatibility.\nCursor Tab keeps using Cursor's own models.\n\nModel IDs (add one at a time):\nNATIVE_MODELS_SENTINEL"

const nativeXcodeGuideES = "Xcode → Settings → Intelligence → Add a Chat Provider (o Add a Model Provider)\n\nElige Internet Hosted para introducir autenticación con esta URL local.\nURL: NATIVE_BASE_SENTINEL/xcode\nAPI Key Header: Authorization\nAPI Key: Bearer NATIVE_KEY_SENTINEL\n\nNo añadas /v1: lo añade Xcode. Mantén Kilo Local abierto.\nXcode obtiene la selección guardada de NATIVE_BASE_SENTINEL/xcode/v1/models.\nElige un modelo en Xcode. Actualiza o vuelve a añadir el proveedor si la lista no cambia.\nLos modelos deben admitir Chat Completions; listar no verifica el acceso al generar."

const nativeCursorGuideES = "Cursor · guía de configuración (requiere HTTPS externo)\n\nKilo Local solo escucha en loopback. Los servidores de Cursor no pueden acceder.\nNo pegues su URL localhost ni su clave local en Cursor.\n\nConecta el túnel ngrok del helper de Cursor para obtener la URL pública y la clave de Cursor. Después:\n1. Cursor Settings > Models: activa OpenAI API Key.\n2. Override OpenAI Base URL: usa la URL HTTPS pública de la API de ese gateway.\n3. API key: usa la credencial emitida para ese gateway.\n4. Add Custom Model / Add model: añade y activa cada ID exacto de abajo.\n5. Elige un modelo en el selector de Cursor y verifica una petición.\n\nNo quites el prefijo del proveedor ni sustituyas el ID por el nombre visible.\nAñadir un ID no verifica el protocolo ni los permisos de la organización.\nCursor Tab sigue usando los modelos propios de Cursor.\n\nIDs de modelos (añadir uno a uno):\nNATIVE_MODELS_SENTINEL"

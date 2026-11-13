//go:build desktop

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"

	"gioui.org/layout"
)

const nativeT3CodePreferencesEndpoint = "/api/clients/t3-code/preferences"

type nativeT3CodePreferences struct {
	Version            int      `json:"version"`
	Codex              bool     `json:"codex"`
	Claude             bool     `json:"claude"`
	KiloCodex          bool     `json:"kiloCodex"`
	KiloClaude         bool     `json:"kiloClaude"`
	CodexModels        string   `json:"codexModels"`
	ClaudeModels       string   `json:"claudeModels"`
	Providers          []string `json:"providers"`
	OpenCode2          bool     `json:"openCode2"`
	OpenCodeConnection string   `json:"openCodeConnection"`
	MuseConnection     string   `json:"museConnection"`
	MuseModels         string   `json:"museModels,omitempty"`
}

// Missing Muse connection is the legacy native account; explicit null is not a
// valid choice and must never acknowledge a saved Kilo connection.
func (p *nativeT3CodePreferences) UnmarshalJSON(data []byte) error {
	type document nativeT3CodePreferences
	var next document
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if raw, present := fields["museConnection"]; present && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("Invalid Muse connection.")
	}
	if raw, present := fields["museModels"]; present {
		var policy string
		if json.Unmarshal(raw, &policy) != nil || (policy != "official" && policy != "shared") {
			return errors.New("Invalid Muse model policy.")
		}
	}
	*p = nativeT3CodePreferences(next)
	return nil
}

type nativeT3CodeProvider struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type nativeT3CodeProviderChoice struct {
	nativeT3CodeProvider
	Supported bool
}

func nativeT3CodeProviderChoices(p nativeT3CodePreferences, supported []nativeT3CodeProvider) []nativeT3CodeProviderChoice {
	known := map[string]string{}
	for _, provider := range t3CodeOptionalProviders {
		known[provider.ID] = provider.Label
	}
	result, seen := []nativeT3CodeProviderChoice{}, map[string]bool{}
	for _, provider := range supported {
		if known[provider.ID] != "" && !seen[provider.ID] {
			result = append(result, nativeT3CodeProviderChoice{nativeT3CodeProvider: provider, Supported: true})
			seen[provider.ID] = true
		}
	}
	for _, id := range p.Providers {
		if known[id] != "" && !seen[id] {
			result = append(result, nativeT3CodeProviderChoice{nativeT3CodeProvider: nativeT3CodeProvider{ID: id, Label: known[id]}})
			seen[id] = true
		}
	}
	return result
}

type nativeT3CodeModelSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type nativeT3CodePreferencesResponse struct {
	Preferences           nativeT3CodePreferences `json:"preferences"`
	RequiresPreparation   bool                    `json:"requiresPreparation"`
	SupportedProviders    []nativeT3CodeProvider  `json:"supportedProviders"`
	OpenCode2Supported    bool                    `json:"openCode2Supported"`
	OpenCodeKiloSupported bool                    `json:"openCodeKiloSupported"`
	MuseKiloSupported     bool                    `json:"museKiloSupported"`
}
type nativeT3CodePreferencesState struct {
	nativeT3CodePreferencesResponse
	Requested, Loaded bool
	Generation        uint64
	Error             string
	Pending           *nativeT3CodePreferences
}

func nativeT3OpenCodeConnection(p nativeT3CodePreferences) string {
	if p.OpenCodeConnection == "" {
		return "native"
	}
	return p.OpenCodeConnection
}

func nativeT3MuseConnection(p nativeT3CodePreferences) string {
	if p.MuseConnection == "" {
		return "native"
	}
	return p.MuseConnection
}
func nativeT3MuseModels(p nativeT3CodePreferences) string {
	if p.MuseModels == "" {
		if nativeT3MuseConnection(p) == "kilo" {
			return "shared"
		}
		return "official"
	}
	return p.MuseModels
}

func nativeT3MuseUsesKilo(p nativeT3CodePreferences) bool {
	return helperContains(p.Providers, "muse") && nativeT3MuseConnection(p) == "kilo"
}
func (u *nativeUI) t3CodeMuseConnectionError() error {
	s := &u.agentsState().T3Code.PreferencesState
	if s.Loaded && nativeT3MuseUsesKilo(s.Preferences) && !s.MuseKiloSupported {
		return errors.New(u.tr("This T3 build cannot use the Kilo Muse connection. Choose your Meta account, disable Muse or install a compatible T3 build.", "Esta compilación de T3 no admite la conexión Kilo de Muse. Elige tu cuenta de Meta, desactiva Muse o instala una compilación T3 compatible."))
	}
	return nil
}

func nativeT3OpenCodeUsesKilo(p nativeT3CodePreferences) bool {
	return helperContains(p.Providers, "opencode") && nativeT3OpenCodeConnection(p) == "kilo"
}

func nativeT3CodePreferencesEqual(a, b nativeT3CodePreferences) bool {
	a.OpenCodeConnection, b.OpenCodeConnection = nativeT3OpenCodeConnection(a), nativeT3OpenCodeConnection(b)
	a.MuseConnection, b.MuseConnection = nativeT3MuseConnection(a), nativeT3MuseConnection(b)
	a.MuseModels, b.MuseModels = nativeT3MuseModels(a), nativeT3MuseModels(b)
	a.Providers = append([]string(nil), a.Providers...)
	b.Providers = append([]string(nil), b.Providers...)
	sort.Strings(a.Providers)
	sort.Strings(b.Providers)
	if len(a.Providers) == 0 {
		a.Providers = nil
	}
	if len(b.Providers) == 0 {
		b.Providers = nil
	}
	return reflect.DeepEqual(a, b)
}

func validNativeT3CodePreferences(p nativeT3CodePreferences) bool {
	return p.Version == 1 && (p.CodexModels == "official" || p.CodexModels == "shared") && (p.ClaudeModels == "official" || p.ClaudeModels == "shared") && (!p.OpenCode2 || helperContains(p.Providers, "opencode")) && (nativeT3OpenCodeConnection(p) == "native" || nativeT3OpenCodeConnection(p) == "kilo") && (nativeT3MuseConnection(p) == "native" || nativeT3MuseConnection(p) == "kilo") && (nativeT3MuseModels(p) == "official" || nativeT3MuseModels(p) == "shared")
}

func nativeT3CodePreferenceLibraryError(p nativeT3CodePreferences, library modelLibrary) error {
	if len(library.Models) > 32 && (p.KiloCodex && p.CodexModels == "shared" || p.KiloClaude && p.ClaudeModels == "shared") {
		return errors.New("T3 Code supports up to 32 shared models per account. Reduce your selection in Models or choose official models.")
	}
	return nil
}

func (u *nativeUI) t3CodeOpenCodeConnectionError() error {
	s := &u.agentsState().T3Code.PreferencesState
	if s.Loaded && nativeT3OpenCodeUsesKilo(s.Preferences) && !s.OpenCodeKiloSupported {
		return errors.New(u.tr("This T3 build cannot use the Kilo OpenCode connection. Choose your own OpenCode account, disable OpenCode or install a compatible T3 build.", "Esta compilación de T3 no admite la conexión Kilo de OpenCode. Elige tu cuenta propia de OpenCode, desactiva OpenCode o instala una compilación T3 compatible."))
	}
	return nil
}

func (u *nativeUI) t3CodePreferencesBusy() bool {
	return u.busy["POST"+nativeT3CodePreferencesEndpoint]
}

func (u *nativeUI) refreshT3CodePreferences() {
	u.refreshT3CodePreferencesAfter(nil)
}

func (u *nativeUI) refreshT3CodePreferencesAfter(done func(error)) {
	finish := func(err error) {
		if done != nil {
			done(err)
		}
	}
	s := &u.agentsState().T3Code.PreferencesState
	if u.busy["GET"+nativeT3CodePreferencesEndpoint] || u.t3CodePreferencesBusy() {
		finish(errors.New(u.tr("Wait for T3 Code settings to finish loading or saving.", "Espera a que terminen de cargarse o guardarse los ajustes de T3 Code.")))
		return
	}
	s.Requested, s.Error = true, ""
	s.Generation++
	generation := s.Generation
	u.clientRequest("GET", nativeT3CodePreferencesEndpoint, nil, func(raw json.RawMessage, err error) {
		if s.Generation != generation {
			finish(errors.New(u.launchSettingsChangedMessage()))
			return
		}
		if err != nil {
			s.Error = nativeMessage(err.Error(), u.language)
			finish(err)
			return
		}
		var response nativeT3CodePreferencesResponse
		if json.Unmarshal(raw, &response) != nil || !validNativeT3CodePreferences(response.Preferences) {
			s.Error = u.tr("Could not read T3 Code preferences. Refresh to try again.", "No se pudieron leer las preferencias de T3 Code. Actualiza para reintentarlo.")
			finish(errors.New(s.Error))
			return
		}
		s.nativeT3CodePreferencesResponse, s.Loaded = response, true
		finish(nil)
	})
}

func (u *nativeUI) saveT3CodePreferences(next nativeT3CodePreferences) {
	t3 := &u.agentsState().T3Code
	s := &t3.PreferencesState
	if !s.Loaded || u.t3CodePreferencesBusy() || !validNativeT3CodePreferences(next) {
		return
	}
	next.Providers = append([]string{}, next.Providers...)
	next.OpenCodeConnection = nativeT3OpenCodeConnection(next)
	next.MuseModels = nativeT3MuseModels(next)
	next.MuseConnection = nativeT3MuseConnection(next)
	s.Pending, s.Error = &next, ""
	s.Generation++
	generation := s.Generation
	// Invalidate any profile read started before this settings operation.
	t3.Generation++
	t3.Info.Prepared = false
	u.clientRequest("POST", nativeT3CodePreferencesEndpoint, next, func(raw json.RawMessage, err error) {
		if s.Generation != generation {
			return
		}
		if err != nil {
			s.Error = nativeMessage(err.Error(), u.language)
			return
		}
		var response nativeT3CodePreferencesResponse
		if json.Unmarshal(raw, &response) != nil || !validNativeT3CodePreferences(response.Preferences) || !nativeT3CodePreferencesEqual(response.Preferences, next) {
			s.Error = u.tr("T3 Code did not confirm the saved preferences. Refresh before preparing.", "T3 Code no confirmó las preferencias guardadas. Actualiza antes de preparar.")
			return
		}
		s.nativeT3CodePreferencesResponse, s.Pending, s.Loaded = response, nil, true
		s.RequiresPreparation = true
		u.refreshT3CodeProfile()
	})
}

func nativeT3CodeToggleProvider(p nativeT3CodePreferences, id string, enabled bool) nativeT3CodePreferences {
	providers := []string{}
	for _, existing := range p.Providers {
		if existing != id {
			providers = append(providers, existing)
		}
	}
	if enabled {
		providers = append(providers, id)
	}
	p.Providers = providers
	if id == "opencode" && !enabled {
		p.OpenCode2 = false
	}
	return p
}

func (u *nativeUI) t3CodePreferencesPanel() layout.Widget {
	s := &u.agentsState().T3Code.PreferencesState
	if !s.Requested {
		u.refreshT3CodePreferences()
	}
	checking, saving := u.busy["GET"+nativeT3CodePreferencesEndpoint], u.t3CodePreferencesBusy()
	ready := s.Loaded && !checking && !saving && s.Pending == nil
	status := u.tr("Choose the accounts T3 offers. Changes save automatically.", "Elige las cuentas que ofrece T3. Los cambios se guardan solos.")
	if checking || !s.Loaded {
		status = u.tr("Loading T3 Code preferences…", "Cargando preferencias de T3 Code…")
	}
	if !checking && !s.Loaded && s.Error != "" {
		status = u.tr("T3 Code preferences are unavailable.", "Las preferencias de T3 Code no están disponibles.")
	}
	if saving {
		status = u.tr("Saving T3 Code preferences…", "Guardando preferencias de T3 Code…")
	}
	statusWidgets := []layout.Widget{u.note(status), u.note(u.tr("Kilo accounts have a green KP badge in T3.", "Las cuentas Kilo llevan una insignia verde KP en T3."))}
	if s.Error != "" {
		statusWidgets = append(statusWidgets, u.message(nativeToneError, s.Error))
		if s.Pending != nil {
			statusWidgets = append(statusWidgets, u.note(u.tr("The change is not confirmed. Retry saving or reload the saved settings.", "El cambio no está confirmado. Reintenta el guardado o carga los ajustes guardados.")), u.disabled(!saving, u.button("t3-preferences:retry", u.tr("Retry saving", "Reintentar guardado"), func() {
				if s.Pending != nil {
					u.saveT3CodePreferences(*s.Pending)
				}
			})))
		}
	}
	if s.RequiresPreparation {
		statusWidgets = append(statusWidgets, u.hint(u.tr("Close T3 Code · Kilo and prepare it again to apply these settings. Your chats are kept.", "Cierra T3 Code · Kilo y prepara de nuevo para aplicar estos ajustes. Tus chats se conservan.")))
	}
	statusWidgets = append(statusWidgets, u.note(u.tr("These settings apply only to the separate Kilo workspace. Existing CLI logins and your regular T3 workspace are kept.", "Estos ajustes solo afectan al espacio Kilo separado. Se conservan las sesiones CLI y tu espacio T3 habitual.")))
	rows := []layout.Widget{u.uxsRow(u.tr("Private workspace", "Espacio privado"), "", u.column(statusWidgets...), u.disabled(!saving && !checking, u.button("t3-preferences:refresh", u.tr("Reload settings", "Cargar ajustes"), func() { s.Pending = nil; u.refreshT3CodePreferences() })), nil)}
	p := s.Preferences
	if saving && s.Pending != nil {
		p = *s.Pending
	}
	account := func(id, label, help string, on bool, set func(*nativeT3CodePreferences, bool)) layout.Widget {
		return u.uxsRow(label, "", u.disabled(ready, u.uxsToggle("t3-preferences:"+id, label, help, on, func(value bool) { next := p; set(&next, value); u.saveT3CodePreferences(next) })), nil, nil)
	}
	rows = append(rows,
		account("codex", "Codex", u.tr("Your existing Codex CLI session", "Tu sesión existente de Codex CLI"), p.Codex, func(p *nativeT3CodePreferences, v bool) { p.Codex = v }),
		account("claude", "Claude", u.tr("Your existing Claude Code session", "Tu sesión existente de Claude Code"), p.Claude, func(p *nativeT3CodePreferences, v bool) { p.Claude = v }),
		account("kilo-codex", "Kilo Proxy · Codex", u.tr("Models through Kilo Proxy", "Modelos a través de Kilo Proxy"), p.KiloCodex, func(p *nativeT3CodePreferences, v bool) { p.KiloCodex = v }),
		u.uxsRow(u.tr("Codex Kilo models", "Modelos de Codex Kilo"), "", u.uxsRadioList("t3-preferences:codex-models:", []nativeChoice{{Value: "official", Label: u.tr("OpenAI and ChatGPT", "OpenAI y ChatGPT")}, {Value: "shared", Label: u.tr("All shared models", "Todos los modelos compartidos")}}, p.CodexModels, ready && p.KiloCodex, func(value string) { next := p; next.CodexModels = value; u.saveT3CodePreferences(next) }), nil, nil),
		account("kilo-claude", "Kilo Proxy · Claude", u.tr("Models through Kilo Proxy", "Modelos a través de Kilo Proxy"), p.KiloClaude, func(p *nativeT3CodePreferences, v bool) { p.KiloClaude = v }),
		u.uxsRow(u.tr("Claude Kilo models", "Modelos de Claude Kilo"), "", u.uxsRadioList("t3-preferences:claude-models:", []nativeChoice{{Value: "official", Label: u.tr("Claude models", "Modelos de Claude")}, {Value: "shared", Label: u.tr("All shared models", "Todos los modelos compartidos")}}, p.ClaudeModels, ready && p.KiloClaude, func(value string) { next := p; next.ClaudeModels = value; u.saveT3CodePreferences(next) }), nil, u.note(u.tr("Claude models come from your connected Kilo catalog. Choose reasoning in T3. Shared models depend on the installed driver and provider.", "Los modelos de Claude vienen del catálogo de tu conexión Kilo. Elige el razonamiento en T3. Los modelos compartidos dependen del driver y proveedor instalados."))),
	)
	for _, provider := range nativeT3CodeProviderChoices(p, s.SupportedProviders) {
		id, label := provider.ID, strings.TrimSpace(provider.Label)
		if id == "" || label == "" {
			continue
		}
		help := u.tr("Uses this provider's own installation and login", "Usa la instalación y sesión propias de este proveedor")
		if id == "opencode" && nativeT3OpenCodeUsesKilo(p) || id == "muse" && nativeT3MuseUsesKilo(p) {
			help = u.tr("Uses your saved shared models through Kilo Proxy", "Usa tus modelos compartidos guardados a través de Kilo Proxy")
			if id == "muse" && nativeT3MuseModels(p) == "official" {
				help = u.tr("Uses Meta models from your connected Kilo catalog", "Usa los modelos de Meta del catálogo de tu conexión Kilo")
			}
		}
		if !provider.Supported {
			label += u.tr(" · unavailable in this build", " · no disponible en esta compilación")
			help = u.tr("Turn off this saved choice or install a compatible T3 build before preparing.", "Desactiva esta selección guardada o instala una compilación T3 compatible antes de preparar.")
		}
		rows = append(rows, account("provider:"+id, label, help, helperContains(p.Providers, id), func(p *nativeT3CodePreferences, enabled bool) {
			if provider.Supported || !enabled {
				if id == "muse" && enabled {
					p.MuseModels = nativeT3MuseModels(*p)
					p.MuseConnection = "kilo"
				}
				*p = nativeT3CodeToggleProvider(*p, id, enabled)
			}
		}))
		if id == "muse" && helperContains(p.Providers, id) {
			connection := nativeT3MuseConnection(p)
			choices := []nativeChoice{{Value: "kilo", Label: u.tr("Kilo Proxy", "Kilo Proxy")}, {Value: "native", Label: u.tr("Own Meta account", "Cuenta propia de Meta")}}
			editable := ready && helperContains(p.Providers, id) && provider.Supported
			choose := func(value string) {
				if value == "kilo" && !s.MuseKiloSupported {
					return
				}
				next := p
				next.MuseModels = nativeT3MuseModels(p)
				next.MuseConnection = value
				u.saveT3CodePreferences(next)
			}
			controls := u.column(u.uxsRadio("t3-preferences:muse-connection:kilo", choices[0], connection == "kilo", editable && s.MuseKiloSupported, choose), u.uxsRadio("t3-preferences:muse-connection:native", choices[1], connection == "native", editable, choose))
			help := u.tr("Kilo uses the chosen model policy in a separate Muse profile. Your Meta account keeps its own login and catalog. Both use the Muse executable selected in Settings. Close T3 Code · Kilo and prepare again after changing the connection.", "Kilo usa la política de modelos elegida en un perfil Muse separado. Tu cuenta de Meta conserva su sesión y catálogo propios. Ambas usan el ejecutable Muse elegido en Ajustes. Cierra T3 Code · Kilo y prepara de nuevo tras cambiar la conexión.")
			if !s.MuseKiloSupported {
				help += " " + u.tr("This T3 build cannot use the Kilo Muse connection.", "Esta compilación de T3 no admite la conexión Kilo de Muse.")
			}
			rows = append(rows, u.uxsRow(u.tr("Muse connection", "Conexión de Muse"), "", controls, nil, u.note(help)))
			if nativeT3MuseUsesKilo(p) {
				modelChoices := []nativeChoice{{Value: "official", Label: u.tr("Meta models", "Modelos de Meta")}, {Value: "shared", Label: u.tr("All shared models", "Todos los modelos compartidos")}}
				rows = append(rows, u.uxsRow(u.tr("Muse Kilo models", "Modelos de Muse Kilo"), "", u.uxsRadioList("t3-preferences:muse-models:", modelChoices, nativeT3MuseModels(p), editable, func(value string) {
					next := p
					next.MuseModels = value
					u.saveT3CodePreferences(next)
				}), nil, u.note(u.tr("Use the Meta models from your connected Kilo catalog or choose all saved shared models. Both options use your Kilo connection.", "Usa los modelos de Meta del catálogo de tu conexión Kilo o elige todos los modelos compartidos guardados. Ambas opciones usan tu conexión Kilo."))))
			}
			if nativeT3MuseUsesKilo(p) && nativeT3CodePreferencesEqual(p, u.agentsState().T3Code.Info.Preferences) {
				models := []string{}
				for _, model := range u.agentsState().T3Code.Info.EffectiveMuseModels {
					label := model.ID
					if model.Name != "" {
						label = model.Name + " · " + model.ID
					}
					models = append(models, label)
				}
				if len(models) > 0 {
					rows = append(rows, u.uxsRow(u.tr("Muse Kilo models", "Modelos de Muse Kilo"), "", u.note(strings.Join(models, "\n")), nil, nil))
				}
			}
		}
		if id == "opencode" {
			connection := nativeT3OpenCodeConnection(p)
			choices := []nativeChoice{{Value: "native", Label: u.tr("Own OpenCode account", "Cuenta propia de OpenCode")}, {Value: "kilo", Label: u.tr("Kilo Proxy · shared models", "Kilo Proxy · modelos compartidos")}}
			choose := func(value string) {
				if value == "kilo" && !s.OpenCodeKiloSupported {
					return
				}
				next := p
				next.OpenCodeConnection = value
				u.saveT3CodePreferences(next)
			}
			editable := ready && helperContains(p.Providers, id) && provider.Supported
			connectionChoices := u.uxsRadioList("t3-preferences:opencode-connection:", choices, connection, editable, choose)
			if !s.OpenCodeKiloSupported {
				connectionChoices = u.column(u.uxsRadio("t3-preferences:opencode-connection:native", choices[0], connection == "native", editable, choose), u.uxsRadio("t3-preferences:opencode-connection:kilo", choices[1], connection == "kilo", false, nil))
			}
			help := u.tr("Your own account uses OpenCode's login and catalog. Kilo uses the models saved in Models through the proxy, with a separate profile. Close T3 Code · Kilo and prepare again after changing this connection.", "Tu cuenta propia usa la sesión y el catálogo de OpenCode. Kilo usa los modelos guardados en Modelos a través del proxy, con un perfil separado. Cierra T3 Code · Kilo y prepara de nuevo tras cambiar esta conexión.")
			if !s.OpenCodeKiloSupported {
				help = u.tr("This T3 build cannot use the Kilo OpenCode connection. Choose your own OpenCode account, disable OpenCode or install a compatible T3 build.", "Esta compilación de T3 no admite la conexión Kilo de OpenCode. Elige tu cuenta propia de OpenCode, desactiva OpenCode o instala una compilación T3 compatible.")
			}
			rows = append(rows, u.uxsRow(u.tr("OpenCode connection", "Conexión de OpenCode"), "", connectionChoices, nil, u.note(help)))
			if nativeT3OpenCodeUsesKilo(p) && nativeT3CodePreferencesEqual(p, u.agentsState().T3Code.Info.Preferences) {
				models := []string{}
				for _, model := range u.agentsState().T3Code.Info.EffectiveOpenCodeModels {
					label := model.ID
					if model.Name != "" {
						label = model.Name + " · " + model.ID
					}
					models = append(models, label)
				}
				if len(models) > 0 {
					rows = append(rows, u.uxsRow(u.tr("OpenCode Kilo models", "Modelos de OpenCode Kilo"), "", u.note(strings.Join(models, "\n")), nil, nil))
				}
			}
		}
		if id == "opencode" && (s.OpenCode2Supported || p.OpenCode2) {
			choose := func(value string) {
				if value == "2" && !s.OpenCode2Supported {
					return
				}
				next := p
				next.OpenCode2 = value == "2"
				u.saveT3CodePreferences(next)
			}
			version := u.uxsRadioList("t3-preferences:opencode-version:", []nativeChoice{{Value: "1", Label: "OpenCode 1"}, {Value: "2", Label: "OpenCode 2"}}, map[bool]string{false: "1", true: "2"}[p.OpenCode2], ready && helperContains(p.Providers, id), choose)
			versionHelp := u.tr("Choose one version for this account. OpenCode 2 requires 2.0.18 or newer; both versions are never started together with the same profile.", "Elige una versión para esta cuenta. OpenCode 2 requiere 2.0.18 o posterior; nunca se abren ambas versiones a la vez con el mismo perfil.")
			if nativeT3OpenCodeConnection(p) == "native" {
				versionHelp += u.tr(" OpenCode 2 has a separate profile that may need its own login.", " OpenCode 2 tiene un perfil separado que puede requerir su propio inicio de sesión.")
			}
			if !s.OpenCode2Supported {
				version = u.column(u.uxsRadio("t3-preferences:opencode-version:1", nativeChoice{Value: "1", Label: "OpenCode 1"}, false, ready, choose), u.uxsRadio("t3-preferences:opencode-version:2", nativeChoice{Value: "2", Label: "OpenCode 2"}, true, false, nil))
				versionHelp = u.tr("This T3 build cannot use the saved OpenCode 2 choice. Choose OpenCode 1, disable OpenCode or install a compatible T3 build.", "Esta compilación de T3 no admite la selección guardada de OpenCode 2. Elige OpenCode 1, desactiva OpenCode o instala una compilación T3 compatible.")
			}
			rows = append(rows, u.uxsRow(u.tr("OpenCode version", "Versión de OpenCode"), "", version, nil, u.note(versionHelp)))
		}
	}
	if s.Loaded && len(s.SupportedProviders) == 0 {
		rows = append(rows, u.uxsRow(u.tr("Other providers", "Otros proveedores"), "", u.note(u.tr("This T3 build does not expose additional compatible providers.", "Esta compilación de T3 no ofrece proveedores compatibles adicionales.")), nil, nil))
	}
	return u.uxsBlock(u.tr("Accounts and models", "Cuentas y modelos"), "", rows...)
}

//go:build desktop

package main

import (
	"encoding/json"
	"net/url"
	"strings"

	"gioui.org/layout"
)

func (u *nativeUI) liteLLMReady() bool {
	return nativeBool(nativeMap(u.state["litellm"]), "ready")
}

func (u *nativeUI) kiloEnabled() bool {
	enabled, present := u.state["kiloEnabled"].(bool)
	return !present || enabled
}

func (u *nativeUI) gatewayRequestBusy() bool {
	for _, path := range []string{
		liteLLMSettingsEndpoint, liteLLMActiveEndpoint, kiloActiveEndpoint,
		liteLLMLoginEndpoint, liteLLMLoginCancelEndpoint, liteLLMLogoutEndpoint,
	} {
		if u.busy["POST"+path] {
			return true
		}
	}
	return false
}

func (u *nativeUI) gatewayEditable() bool {
	return u.authenticated && !u.uxsConnectionBusy() && (u.clients == nil || u.clients.Launching == "")
}

func (u *nativeUI) updateGateway(path string, payload any, saved func(map[string]any)) {
	if !u.gatewayEditable() {
		return
	}
	u.gatewayRevision++
	u.clientRequest("POST", path, payload, func(raw json.RawMessage, err error) {
		u.gatewayRevision++
		if err != nil {
			u.noticeError(err)
		} else {
			var state map[string]any
			if json.Unmarshal(raw, &state) == nil {
				for _, key := range []string{"litellm", "litellmLogin", "kiloEnabled", "kiloReady", "connectionReady"} {
					if value, ok := state[key]; ok {
						u.state[key] = value
					}
				}
			}
			if saved != nil {
				saved(state)
			}
			u.acceptLiteLLMLogin()
		}
		u.refreshState()
	})
}

func (u *nativeUI) setGatewayActive(path string, enabled bool) {
	u.updateGateway(path, map[string]bool{"enabled": enabled}, nil)
}

func (u *nativeUI) liteLLMLoginStatus() string {
	return nativeString(nativeMap(u.state["litellmLogin"]), "status")
}

func (u *nativeUI) acceptLiteLLMLogin() {
	status := u.liteLLMLoginStatus()
	if status == "pending" || u.liteLLMAuthorizationURL == "" {
		return
	}
	u.liteLLMAuthorizationURL = ""
	if status == "success" {
		u.closeLiteLLMEditor()
	}
}

func (u *nativeUI) startLiteLLMLogin(address string) {
	if u.liteLLMLoginStatus() == "pending" {
		return
	}
	u.updateGateway(liteLLMLoginEndpoint, map[string]string{"url": address}, func(state map[string]any) {
		link, err := nativeLiteLLMLoginURL(address, nativeString(state, "authorizationURL"))
		if err != nil || u.liteLLMLoginStatus() != "pending" {
			u.setNotice(nativeToneWarning, u.tr("Could not start LiteLLM sign-in. Try again.", "No se pudo iniciar sesión en LiteLLM. Vuelve a intentarlo."))
			return
		}
		u.setNotice(nativeToneNeutral, "")
		u.liteLLMAuthorizationURL = link
		u.openValidatedExternalURL(link)
	})
}

// LiteLLM may use a loopback HTTP origin for a gateway on this machine. Keep
// the general HTTPS-only desktop opener and bind this link to the entered URL.
func nativeLiteLLMLoginURL(address, link string) (string, error) {
	base, err := normalizeLiteLLMURL(address)
	if err != nil {
		return "", err
	}
	if err := validateLiteLLMBrowserTransport(base); err != nil {
		return "", err
	}
	target, err := url.Parse(link)
	if err != nil || strings.Contains(link, "#") {
		return "", errLiteLLMOAuthOrigin
	}
	endpoint := *target
	endpoint.RawQuery, endpoint.ForceQuery = "", false
	if _, err := liteLLMOAuthURL(endpoint.String(), base); err != nil {
		return "", err
	}
	return target.String(), nil
}

func (u *nativeUI) liteLLMLoginControls(editable bool) layout.Widget {
	buttons := []layout.Widget{}
	if link := u.liteLLMAuthorizationURL; link != "" {
		buttons = append(buttons, u.disabled(editable, u.button("providers.litellm.open-login", u.tr("Open LiteLLM sign-in", "Abrir el inicio de sesión de LiteLLM"), func() {
			if u.liteLLMLoginStatus() == "pending" && u.liteLLMAuthorizationURL == link {
				u.openValidatedExternalURL(link)
			}
		})))
	}
	buttons = append(buttons, u.disabled(editable, u.button("providers.litellm.cancel-login", u.tr("Cancel sign-in", "Cancelar el inicio de sesión"), func() {
		u.updateGateway(liteLLMLoginCancelEndpoint, map[string]any{}, nil)
	})))
	return u.column(u.note(u.tr("Finish signing in on your LiteLLM server in the browser.", "Termina de iniciar sesión en tu servidor LiteLLM desde el navegador.")), u.pills(buttons...))
}

func (u *nativeUI) openLiteLLMEditor(address, mode string) {
	u.setValue("providers.litellm.url", address)
	u.setValue("providers.litellm.authMode", mode)
	u.setValue("providers.litellm.key", "")
	u.setChecked("providers.litellm.clear", false)
	u.expanded["providers.litellm"] = true
}

func (u *nativeUI) closeLiteLLMEditor() {
	u.setValue("providers.litellm.key", "")
	u.setChecked("providers.litellm.clear", false)
	u.expanded["providers.litellm"] = false
}

func (u *nativeUI) uxsLiteLLMRow() layout.Widget {
	state := nativeMap(u.state["litellm"])
	address := nativeString(state, "url")
	active := nativeBool(state, "enabled")
	saving := u.busy["POST"+liteLLMSettingsEndpoint]
	browserAuth := nativeString(state, "authMode") == liteLLMBrowserMode
	hasCredential := nativeBool(state, "hasKey") || nativeBool(state, "keySaved")
	loginStatus := u.liteLLMLoginStatus()
	pending := loginStatus == "pending"
	tone, label := nativeToneNeutral, u.tr("Not configured", "Sin configurar")
	var details []string
	var action layout.Widget
	editable := u.gatewayEditable()
	if address != "" {
		details = append(details, address)
		switch {
		case browserAuth && !hasCredential:
			label = u.tr("Signed out", "Sesión cerrada")
		case !active:
			label = u.tr("Paused", "En pausa")
		case !nativeBool(state, "ready"):
			tone, label = nativeToneWarning, u.tr("Check your connection", "Revisa la conexión")
		default:
			tone, label = nativeToneSuccess, u.tr("Connected", "Conectado")
			if count, ok := state["modelsCount"].(float64); ok {
				details = append(details, uxmPlural(int(count), u.tr("1 model available", "1 modelo disponible"), u.tr("%d models available", "%d modelos disponibles")))
			}
		}
		if browserAuth && hasCredential {
			details = append(details, u.tr("Browser sign-in", "Inicio de sesión en el navegador"))
		} else if nativeBool(state, "keySaved") {
			details = append(details, u.tr("Key saved in the system credential store", "Clave guardada en el almacén del sistema"))
		}
	}
	switch {
	case saving:
		tone, label = nativeToneInfo, u.tr("Checking connection…", "Comprobando conexión…")
	case u.busy["POST"+liteLLMLoginEndpoint]:
		tone, label = nativeToneInfo, u.tr("Starting sign-in…", "Iniciando sesión…")
	case pending:
		tone, label = nativeToneInfo, u.tr("Waiting for browser sign-in", "Esperando el inicio de sesión en el navegador")
	}
	var below []layout.Widget
	value := []layout.Widget{u.uxsValue(u.uxsStatus(tone, label, ""), details...)}
	if address != "" && (!browserAuth || hasCredential) {
		value = append(value, u.disabled(editable, u.uxsToggle("providers.litellm.active", u.tr("Use LiteLLM", "Usar LiteLLM"), "", active, func(on bool) {
			u.setGatewayActive(liteLLMActiveEndpoint, on)
		})))
	}
	if loginStatus == "error" {
		reason := nativeString(nativeMap(u.state["litellmLogin"]), "error")
		if reason == "" {
			reason = u.tr("LiteLLM sign-in failed. Try again.", "El inicio de sesión en LiteLLM falló. Vuelve a intentarlo.")
		}
		if reason = nativeMessage(reason, u.language); reason != u.notice {
			below = append(below, u.message(nativeToneWarning, reason))
		}
	}
	switch {
	case pending:
		below = append(below, u.liteLLMLoginControls(editable))
	case u.expanded["providers.litellm"]:
		below = append(below, u.liteLLMEditor(editable, address != "" && !browserAuth && hasCredential))
	default:
		editLabel := u.tr("Add LiteLLM", "Añadir LiteLLM")
		if address != "" {
			editLabel = u.tr("Edit", "Editar")
		}
		mode := "key"
		if browserAuth {
			mode = liteLLMBrowserMode
		}
		action = u.disabled(editable, u.button("providers.litellm.edit", editLabel, func() {
			u.openLiteLLMEditor(address, mode)
		}))
		if browserAuth && address != "" {
			actions := []layout.Widget{
				u.disabled(editable, u.button("providers.litellm.reconnect", u.tr("Reconnect LiteLLM", "Volver a conectar LiteLLM"), func() { u.startLiteLLMLogin(address) })),
			}
			if hasCredential {
				actions = append(actions, u.disabled(editable, u.dangerButton("providers.litellm.logout", u.tr("Disconnect LiteLLM", "Desconectar LiteLLM"), func() {
					u.updateGateway(liteLLMLogoutEndpoint, map[string]any{}, nil)
				})))
			}
			value = append(value, u.pills(actions...))
		}
	}
	var panel layout.Widget
	if len(below) > 0 {
		panel = u.column(below...)
	}
	return u.uxsRow("LiteLLM", "", u.column(value...), action, panel)
}

func (u *nativeUI) liteLLMEditor(editable, hasSavedKey bool) layout.Widget {
	mode := u.value("providers.litellm.authMode")
	if mode != liteLLMBrowserMode {
		mode = "key"
	}
	fields := []layout.Widget{
		u.disabled(editable, u.field("providers.litellm.url", u.tr("API URL", "URL de la API"), "", false)),
		u.uxsRadioList("providers.litellm.authMode:", []nativeChoice{
			{Value: "key", Label: u.tr("Virtual key", "Clave virtual")},
			{Value: liteLLMBrowserMode, Label: u.tr("Browser sign-in", "Inicio de sesión en el navegador")},
		}, mode, editable, func(next string) {
			u.setValue("providers.litellm.authMode", next)
			if next == liteLLMBrowserMode {
				u.setValue("providers.litellm.key", "")
				u.setChecked("providers.litellm.clear", false)
			}
		}),
	}
	cancel := u.disabled(editable, u.button("providers.litellm.cancel", u.tr("Cancel", "Cancelar"), u.closeLiteLLMEditor))
	if mode == liteLLMBrowserMode {
		return u.column(append(fields,
			u.note(u.tr("Enter your credentials on the LiteLLM sign-in page.", "Introduce tus credenciales en la página de acceso de LiteLLM.")),
			u.pills(u.disabled(editable, u.primaryButton("providers.litellm.login", u.tr("Sign in in your browser", "Iniciar sesión en el navegador"), func() {
				if address := u.liteLLMDraftAddress(); address != "" {
					u.startLiteLLMLogin(address)
				}
			})), cancel),
		)...)
	}
	keyHint := ""
	if hasSavedKey {
		keyHint = u.tr("Leave blank to keep the key for the saved URL", "Deja vacío para conservar la clave de la URL guardada")
	}
	previousBrowser := nativeString(nativeMap(u.state["litellm"]), "authMode") == liteLLMBrowserMode
	canClearKey := hasSavedKey || previousBrowser
	clearKey := canClearKey && u.checked("providers.litellm.clear")
	keyLabel := u.tr("Virtual key (if required)", "Clave virtual (si se requiere)")
	if previousBrowser {
		keyLabel = u.tr("Virtual key", "Clave virtual")
	}
	fields = append(fields, u.disabled(editable && !clearKey, u.field("providers.litellm.key", keyLabel, keyHint, true)))
	if canClearKey {
		fields = append(fields, u.disabled(editable, u.check("providers.litellm.clear", u.tr("Connect without a key", "Conectar sin clave"), nil)))
	}
	fields = append(fields, u.pills(
		u.disabled(editable, u.primaryButton("providers.litellm.save", u.tr("Test and save", "Probar y guardar"), func() {
			address := u.liteLLMDraftAddress()
			if address == "" {
				return
			}
			payload := map[string]string{"url": address}
			clearKey := canClearKey && u.checked("providers.litellm.clear")
			key := strings.TrimSpace(u.value("providers.litellm.key"))
			if previousBrowser && key == "" && !clearKey {
				u.setNotice(nativeToneWarning, u.tr("Enter a virtual key or choose to connect without one.", "Introduce una clave virtual o elige conectar sin clave."))
				return
			}
			if clearKey {
				payload["apiKey"] = ""
			} else if key != "" {
				payload["apiKey"] = key
			}
			u.updateGateway(liteLLMSettingsEndpoint, payload, func(_ map[string]any) {
				u.closeLiteLLMEditor()
				u.setNotice(nativeToneSuccess, u.tr("LiteLLM connection saved", "Conexión con LiteLLM guardada"))
			})
		})),
		cancel,
	), u.note(u.tr("Checks the model list and saves the connection if it succeeds.", "Comprueba el catálogo y guarda la conexión si la prueba se completa.")))
	return u.column(fields...)
}

func (u *nativeUI) liteLLMDraftAddress() string {
	address := strings.TrimSpace(u.value("providers.litellm.url"))
	if address == "" {
		u.setNotice(nativeToneWarning, u.tr("Enter the LiteLLM API URL.", "Introduce la URL de la API de LiteLLM."))
	}
	return address
}

func (u *nativeUI) litellmSetupAccount() string {
	if address := nativeString(nativeMap(u.state["litellm"]), "url"); address != "" {
		return "LiteLLM · " + address
	}
	return "LiteLLM"
}

func (u *nativeUI) uxmModelGateway(m modelInfo) string {
	if m.Connection == "litellm" || strings.HasPrefix(m.ID, liteLLMModelPrefix) {
		return "LiteLLM"
	}
	if nativeString(nativeMap(u.state["litellm"]), "url") == "" {
		return ""
	}
	if m.Connection == "chatgpt" || strings.HasPrefix(m.ID, "chatgpt/") {
		return "ChatGPT"
	}
	return "Kilo"
}

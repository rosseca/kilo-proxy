//go:build desktop

package main

import (
	"encoding/json"
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
	return u.busy["POST"+liteLLMSettingsEndpoint] || u.busy["POST"+liteLLMActiveEndpoint] || u.busy["POST"+kiloActiveEndpoint]
}

func (u *nativeUI) gatewayEditable() bool {
	return u.authenticated && !u.uxsConnectionBusy() && (u.clients == nil || u.clients.Launching == "")
}

func (u *nativeUI) updateGateway(path string, payload any, saved func()) {
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
				for _, key := range []string{"litellm", "kiloEnabled", "kiloReady", "connectionReady"} {
					if value, ok := state[key]; ok {
						u.state[key] = value
					}
				}
			}
			if saved != nil {
				saved()
			}
		}
		u.refreshState()
	})
}

func (u *nativeUI) setGatewayActive(path string, enabled bool) {
	u.updateGateway(path, map[string]bool{"enabled": enabled}, nil)
}

func (u *nativeUI) uxsLiteLLMRow() layout.Widget {
	state := nativeMap(u.state["litellm"])
	address := nativeString(state, "url")
	active := nativeBool(state, "enabled")
	saving := u.busy["POST"+liteLLMSettingsEndpoint]
	tone, label := nativeToneNeutral, u.tr("Not configured", "Sin configurar")
	var details []string
	var action layout.Widget
	editable := u.gatewayEditable()
	if address != "" {
		details = append(details, address)
		switch {
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
		if nativeBool(state, "keySaved") {
			details = append(details, u.tr("Key saved in the system credential store", "Clave guardada en el almacén del sistema"))
		}
	}
	if saving {
		tone, label = nativeToneInfo, u.tr("Checking connection…", "Comprobando conexión…")
	}
	var editor layout.Widget
	if u.expanded["providers.litellm"] {
		editor = u.liteLLMEditor(editable, address != "", nativeBool(state, "hasKey") || nativeBool(state, "keySaved"))
	} else {
		editLabel := u.tr("Add LiteLLM", "Añadir LiteLLM")
		if address != "" {
			editLabel = u.tr("Edit", "Editar")
		}
		action = u.disabled(editable, u.button("providers.litellm.edit", editLabel, func() {
			u.setValue("providers.litellm.url", address)
			u.setValue("providers.litellm.key", "")
			u.setChecked("providers.litellm.clear", false)
			u.expanded["providers.litellm"] = true
		}))
	}
	value := []layout.Widget{u.uxsValue(u.uxsStatus(tone, label, ""), details...)}
	if address != "" {
		value = append(value, u.disabled(editable, u.uxsToggle("providers.litellm.active", u.tr("Use LiteLLM", "Usar LiteLLM"), "", active, func(on bool) {
			u.setGatewayActive(liteLLMActiveEndpoint, on)
		})))
	}
	return u.uxsRow("LiteLLM", "", u.column(value...), action, editor)
}

func (u *nativeUI) liteLLMEditor(editable, configured, hasKey bool) layout.Widget {
	fields := []layout.Widget{
		u.disabled(editable, u.field("providers.litellm.url", u.tr("API URL", "URL de la API"), "", false)),
	}
	keyHint := ""
	if configured && hasKey {
		keyHint = u.tr("Leave blank to keep the key for the saved URL", "Deja vacío para conservar la clave de la URL guardada")
	}
	clearKey := configured && hasKey && u.checked("providers.litellm.clear")
	fields = append(fields, u.disabled(editable && !clearKey, u.field("providers.litellm.key", u.tr("Virtual key (if required)", "Clave virtual (si se requiere)"), keyHint, true)))
	if configured && hasKey {
		fields = append(fields, u.disabled(editable, u.check("providers.litellm.clear", u.tr("Connect without a key", "Conectar sin clave"), nil)))
	}
	closeEditor := func() {
		u.setValue("providers.litellm.key", "")
		u.setChecked("providers.litellm.clear", false)
		u.expanded["providers.litellm"] = false
	}
	fields = append(fields, u.pills(
		u.disabled(editable, u.primaryButton("providers.litellm.save", u.tr("Test and save", "Probar y guardar"), func() {
			address := strings.TrimSpace(u.value("providers.litellm.url"))
			if address == "" {
				u.setNotice(nativeToneWarning, u.tr("Enter the LiteLLM API URL.", "Introduce la URL de la API de LiteLLM."))
				return
			}
			payload := map[string]string{"url": address}
			clearKey := configured && hasKey && u.checked("providers.litellm.clear")
			if key := strings.TrimSpace(u.value("providers.litellm.key")); key != "" || clearKey {
				if clearKey {
					key = ""
				}
				payload["apiKey"] = key
			}
			u.updateGateway(liteLLMSettingsEndpoint, payload, func() {
				closeEditor()
				u.setNotice(nativeToneSuccess, u.tr("LiteLLM connection saved", "Conexión con LiteLLM guardada"))
			})
		})),
		u.disabled(editable, u.button("providers.litellm.cancel", u.tr("Cancel", "Cancelar"), closeEditor)),
	), u.note(u.tr("Checks the model list and saves the connection if it succeeds.", "Comprueba el catálogo y guarda la conexión si la prueba se completa.")))
	return u.column(fields...)
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

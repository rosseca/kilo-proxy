//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"gioui.org/layout"
)

const nativeChatGPTVerificationURL = "https://auth.openai.com/codex/device"

func (u *nativeUI) chatGPTConnected() bool {
	return nativeBool(nativeMap(u.state["chatgpt"]), "connected")
}
func (u *nativeUI) chatGPTReady() bool {
	if ready, ok := u.state["chatgptReady"].(bool); ok {
		return ready
	}
	return u.chatGPTConnected() && nativeString(nativeMap(u.state["chatgpt"]), "status") != "pending"
}

func (u *nativeUI) chatGPTRequestBusy() bool {
	for _, path := range []string{"/api/chatgpt/login", "/api/chatgpt/cancel", "/api/chatgpt/logout", "/api/chatgpt/refresh"} {
		if u.busy["POST"+path] {
			return true
		}
	}
	return false
}

func (u *nativeUI) chatGPTAction(action string) {
	if u.chatGPTRequestBusy() {
		return
	}
	if (action == "login" || action == "logout") && nativeBool(u.state, "running") {
		return
	}
	if action == "login" {
		u.chatGPTLoginRequested = true
	} else if action == "cancel" || action == "logout" {
		u.chatGPTLoginRequested = false
	}
	u.call("POST", "/api/chatgpt/"+action, map[string]any{}, func(raw json.RawMessage) {
		u.acceptState(raw)
	})
}

func (u *nativeUI) openChatGPTAuthorization() {
	if nativeString(nativeMap(u.state["chatgpt"]), "verificationUrl") == nativeChatGPTVerificationURL {
		u.open(nativeChatGPTVerificationURL)
	}
}

func (u *nativeUI) chatGPTAccountPanel() layout.Widget {
	account := nativeMap(u.state["chatgpt"])
	pending, connected := nativeString(account, "status") == "pending", nativeBool(account, "connected")
	working := u.chatGPTRequestBusy()
	editable := !working && !nativeBool(u.state, "running")
	children := []layout.Widget{u.note(u.tr("Experimental direct ChatGPT subscription connection. Use it alongside Kilo in the same agents and shared model library. ChatGPT models use subscription quota; Kilo models use Kilo credit. Reopen agents after changing the selected models.", "Conexión directa experimental con tu suscripción de ChatGPT. Úsala junto a Kilo en los mismos agentes y biblioteca compartida. Los modelos de ChatGPT usan la cuota de suscripción; los modelos de Kilo usan su crédito. Vuelve a abrir los agentes al cambiar los modelos seleccionados."))}
	if connected {
		children = append(children, u.statusBadge(nativeToneSuccess, u.tr("ChatGPT connected", "ChatGPT conectado")))
		identity := strings.TrimSpace(nativeString(account, "email"))
		if plan := nativeString(account, "plan"); plan != "" {
			if identity != "" {
				identity += " · "
			}
			identity += plan
		}
		if identity != "" {
			children = append(children, u.label(identity))
		}
		if nativeString(account, "status") == "error" {
			children = append(children, u.disabled(editable, u.primaryButton("chatgpt.login", u.tr("Reconnect ChatGPT", "Volver a conectar ChatGPT"), func() { u.chatGPTAction("login") })))
		}
		children = append(children, u.pills(u.disabled(editable, u.dangerButton("chatgpt.logout", u.tr("Disconnect ChatGPT", "Desconectar ChatGPT"), func() { u.chatGPTAction("logout") }))))
	} else if !pending {
		children = append(children, u.disabled(editable, u.primaryButton("chatgpt.login", u.tr("Sign in with ChatGPT", "Iniciar sesión con ChatGPT"), func() { u.chatGPTAction("login") })))
	}
	if pending {
		children = append(children, u.statusBadge(nativeToneInfo, u.tr("Waiting for ChatGPT authorization", "Esperando autorización de ChatGPT")))
		if code := nativeString(account, "code"); code != "" {
			children = append(children, u.label(u.tr("Enter this code: ", "Introduce este código: ")+code))
		}
		buttons := []layout.Widget{}
		if nativeString(account, "verificationUrl") == nativeChatGPTVerificationURL {
			buttons = append(buttons, u.button("chatgpt.verify", u.tr("Open ChatGPT authorization", "Abrir autorización de ChatGPT"), u.openChatGPTAuthorization))
		}
		buttons = append(buttons, u.disabled(!working, u.button("chatgpt.cancel", u.tr("Cancel ChatGPT login", "Cancelar login de ChatGPT"), func() { u.chatGPTAction("cancel") })))
		children = append(children, u.pills(buttons...))
	}
	if nativeString(account, "status") == "error" || nativeString(account, "error") != "" {
		children = append(children, u.message(nativeToneWarning, u.tr("Could not connect to ChatGPT. Try signing in again.", "No se ha podido conectar con ChatGPT. Vuelve a iniciar sesión.")))
	}
	children = append(children, u.note(u.tr("Your subscription credentials stay on this computer. Agents use only the local proxy key.", "Las credenciales de tu suscripción se quedan en este equipo. Los agentes solo usan la clave local del proxy.")))
	return u.section(u.tr("ChatGPT subscription", "Suscripción de ChatGPT"), "", children...)
}

func (u *nativeUI) chatGPTQuotaPanel() layout.Widget {
	account := nativeMap(u.state["chatgpt"])
	quota := nativeMap(account["quota"])
	children := []layout.Widget{u.note(u.tr("Subscription quota is separate from Kilo credit. Token counts describe usage, not a monetary charge.", "La cuota de suscripción es independiente del crédito de Kilo. Los tokens describen el uso, no un cargo monetario."))}
	count := 0
	for i, key := range []string{"primary", "secondary"} {
		window := nativeMap(quota[key])
		if _, ok := window["usedPercent"].(float64); !ok {
			continue
		}
		count++
		title := u.tr("Current window", "Ventana actual")
		if i == 1 {
			title = u.tr("Longer window", "Ventana ampliada")
		}
		if mins := nativeNumber(window, "windowDurationMins"); mins > 0 {
			title += fmt.Sprintf(" · %g min", mins)
		}
		used := math.Max(0, math.Min(100, nativeNumber(window, "usedPercent")))
		children = append(children, u.label(fmt.Sprintf(u.tr("%s: %.0f%% used", "%s: %.0f%% usado"), title, used)))
		if reset := nativeNumber(window, "resetsAt"); reset > 0 {
			children = append(children, u.note(u.tr("Resets: ", "Se restablece: ")+time.Unix(int64(reset), 0).Local().Format("2006-01-02 15:04")))
		}
	}
	if count > 0 && (nativeBool(quota, "stale") || quota["available"] == false) {
		children = append(children, u.message(nativeToneWarning, u.tr("Last reported usage is out of date. Refresh to check your current quota.", "El último uso informado está desactualizado. Actualiza para consultar tu cuota actual.")))
	}
	if count == 0 {
		children = append(children, u.note(u.tr("Usage limits unavailable", "Límites de uso no disponibles")))
	}
	if nativeString(quota, "error") != "" {
		children = append(children, u.message(nativeToneWarning, u.tr("Could not refresh subscription usage.", "No se ha podido actualizar el uso de la suscripción.")))
	}
	children = append(children, u.disabled(nativeBool(account, "connected") && !u.chatGPTRequestBusy(), u.button("chatgpt.refresh", u.tr("Refresh subscription usage", "Actualizar uso de la suscripción"), func() { u.chatGPTAction("refresh") })))
	if u.state["chatgptUsageHistory"] != nil {
		var history nativeUsageHistory
		nativeDecode(u.state["chatgptUsageHistory"], &history)
		children = append(children, u.subheading(u.tr("Subscription requests on this device", "Peticiones de suscripción en este equipo")))
		for _, period := range []struct {
			title string
			value usageSummary
		}{{u.tr("Today", "Hoy"), history.Today}, {u.tr("Yesterday", "Ayer"), history.Yesterday}, {u.tr("Last 7 days", "Últimos 7 días"), history.Last7Days}} {
			tokens := nativeReportedCount(period.value.Input+period.value.Output, period.value.WithTokens, period.value.Requests)
			children = append(children, u.note(fmt.Sprintf(u.tr("%s: %d requests · %s tokens", "%s: %d peticiones · %s tokens"), period.title, period.value.Requests, tokens)))
		}
		children = append(children, u.note(u.tr("Observed through this proxy only; quota also includes usage elsewhere.", "Solo lo observado a través de este proxy; la cuota también incluye uso en otros lugares.")))
		if history.Error != "" {
			children = append(children, u.message(nativeToneWarning, u.tr("Subscription history could not be saved.", "No se ha podido guardar el historial de suscripción.")))
		}
	}
	return u.section(u.tr("Subscription usage", "Uso de suscripción"), "", children...)
}

func (u *nativeUI) connectionPanel() layout.Widget {
	panels := []layout.Widget{u.kiloConnectionPanel(), u.chatGPTAccountPanel()}
	if u.chatGPTConnected() {
		panels = append(panels, u.chatGPTQuotaPanel())
	}
	return u.column(panels...)
}

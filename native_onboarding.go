//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"strings"

	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

const (
	setupConnect = iota
	setupModels
	setupReady
)

// Resume from the configuration and shared library themselves. A separate
// "completed" flag would hide setup after credentials or models are removed.
func (u *nativeUI) setupNeeded() bool {
	u.initModelLibrary()
	return u.setupConnectionNeeded() || len(u.library.selection.Models) == 0
}

func (u *nativeUI) setupConnectionNeeded() bool {
	if u.chatGPTReady() {
		return false
	}
	u.owner.mu.Lock()
	defer u.owner.mu.Unlock()
	return !u.owner.connectionReadyLocked()
}

func (u *nativeUI) setSetupStep(step int) {
	if u.setupStep != step {
		u.setupStep = step
		u.list("page.setup").ScrollTo(0)
	}
}

func (u *nativeUI) beginSetup() {
	u.page = "setup"
	u.setSetupStep(setupConnect)
	u.list("page.setup").ScrollTo(0)
	u.setNotice(nativeToneNeutral, "")
	if !u.setupConnectionNeeded() {
		u.setSetupStep(setupModels)
		if len(u.library.selection.Models) > 0 {
			u.setSetupStep(setupReady)
		}
	}
}

func (u *nativeUI) connectionHasKey() bool {
	u.owner.mu.Lock()
	defer u.owner.mu.Unlock()
	return strings.TrimSpace(u.owner.apiKey) != ""
}

func (u *nativeUI) connectionWorking() bool {
	if u.chatGPTRequestBusy() {
		return true
	}
	for _, path := range []string{"/api/config", "/api/start", "/api/stop", "/api/forget", "/api/auth/start", "/api/auth/cancel", "/api/auth/organizations"} {
		if u.busy["POST"+path] {
			return true
		}
	}
	if u.clients != nil && u.clients.Launching != "" {
		return true
	}
	auth := nativeString(nativeMap(u.state["auth"]), "status")
	if (auth == "pending" || auth == "starting") && !u.chatGPTReady() {
		return true
	}
	return nativeString(nativeMap(u.state["chatgpt"]), "status") == "pending" && !nativeBool(u.state, "kiloReady")
}
func (u *nativeUI) kiloConnectionWorking() bool {
	auth := nativeString(nativeMap(u.state["auth"]), "status")
	if auth == "pending" || auth == "starting" {
		return true
	}
	for _, path := range []string{"/api/config", "/api/start", "/api/stop", "/api/forget", "/api/auth/start", "/api/auth/cancel", "/api/auth/organizations"} {
		if u.busy["POST"+path] {
			return true
		}
	}
	return u.clients != nil && u.clients.Launching != ""
}

func (u *nativeUI) beginKiloLogin() {
	u.call("POST", "/api/auth/start", map[string]any{}, func(raw json.RawMessage) {
		var auth map[string]any
		_ = json.Unmarshal(raw, &auth)
		// Display the code immediately, including if opening the browser fails.
		u.state["auth"] = auth
		if link := nativeString(auth, "verificationUrl"); link != "" {
			u.open(link)
		}
		u.refreshState()
	})
}

func (u *nativeUI) setupConnectionContinue() {
	if u.chatGPTRequestBusy() || u.chatGPTReady() && u.connectionWorking() || !u.chatGPTReady() && u.kiloConnectionWorking() {
		return
	}
	continueSetup := func() {
		if u.page != "setup" {
			u.refreshState()
			return
		}
		u.setNotice(nativeToneNeutral, "")
		u.setSetupStep(setupModels)
		u.expanded["library.catalog"] = len(u.library.selection.Models) == 0
		u.models, u.catalogCached = nil, false
		// Fetch after the saved connection revision reaches the UI, so a
		// previous account's catalog cannot win a concurrent refresh.
		u.modelsPending = true
		u.refreshState()
	}
	if u.chatGPTReady() {
		if !u.setupConnectionNeeded() {
			continueSetup()
		}
	} else {
		u.saveConnection(continueSetup)
	}
}

func (u *nativeUI) setupModelsContinue() {
	u.persistLibraryEdits()
	if u.setupConnectionNeeded() {
		u.setSetupStep(setupConnect)
		return
	}
	if len(u.library.selection.Models) == 0 {
		u.setNotice(nativeToneWarning, u.tr("Choose at least one model to continue.", "Elige al menos un modelo para continuar."))
		return
	}
	if status, ready := u.libraryStatus(); !ready {
		u.setNotice(nativeToneWarning, status)
		return
	}
	u.setNotice(nativeToneNeutral, "")
	u.setSetupStep(setupReady)
	u.expanded["library.catalog"] = false
}

func (u *nativeUI) setProxyRunning(start bool, done func()) {
	if u.connectionWorking() {
		return
	}
	if start && u.setupConnectionNeeded() {
		u.beginSetup()
		return
	}
	path := "/api/stop"
	if start {
		path = "/api/start"
	}
	u.setNotice(nativeToneNeutral, "")
	u.call("POST", path, map[string]any{}, func(raw json.RawMessage) {
		u.acceptState(raw)
		if nativeBool(u.state, "running") != start {
			u.setNotice(nativeToneWarning, u.tr("The proxy status could not be confirmed. Try again.", "No se pudo confirmar el estado del proxy. Vuelve a intentarlo."))
			return
		}
		if done != nil {
			done()
		}
	})
}

func (u *nativeUI) finishSetup() {
	u.persistLibraryEdits()
	if u.setupNeeded() {
		u.beginSetup()
		return
	}
	if status, ready := u.libraryStatus(); !ready {
		u.setSetupStep(setupModels)
		u.setNotice(nativeToneWarning, status)
		return
	}
	u.setProxyRunning(true, func() {
		if u.page == "setup" && u.setupStep == setupReady {
			u.page = "agents"
		}
		u.setNotice(nativeToneSuccess, u.tr("Your proxy is running. Choose an agent to open your workspace.", "Tu proxy está activo. Elige un agente para abrir tu espacio de trabajo."))
	})
}

func (u *nativeUI) proxyButton() layout.Widget {
	running := nativeBool(u.state, "running")
	label := u.tr("Start proxy", "Arrancar proxy")
	if running {
		label = u.tr("Stop proxy", "Detener proxy")
	} else if !u.agentConnectionReady() {
		label = u.tr("Connect an account", "Conectar una cuenta")
	} else if u.setupConnectionNeeded() {
		label = u.tr("Save connection", "Guardar conexión")
	}
	if u.busy["POST/api/start"] {
		label = u.tr("Starting…", "Arrancando…")
	} else if u.busy["POST/api/stop"] {
		label = u.tr("Stopping…", "Deteniendo…")
	}
	id := "primary.agents.proxy"
	button := u.primaryButton
	if running {
		id = "agents.proxy.stop"
		button = u.button
	}
	return u.disabled(!u.connectionWorking(), button(id, label, func() { u.setProxyRunning(!running, nil) }))
}

func (u *nativeUI) setupStepReachable(step int) bool {
	if step == setupConnect {
		return true
	}
	if u.setupConnectionNeeded() {
		return false
	}
	if step == setupModels {
		return true
	}
	if step == setupReady {
		_, ready := u.libraryStatus()
		return ready && len(u.library.selection.Models) > 0
	}
	return false
}

func (u *nativeUI) setupStepper() layout.Widget {
	labels := [...]string{u.tr("Accounts", "Cuentas"), u.tr("Models", "Modelos"), u.tr("Start", "Arrancar")}
	steps := [3]layout.Widget{}
	for i, label := range labels {
		step, text := i, label
		steps[i] = u.disabled(u.setupStepReachable(step), func(gtx layout.Context) layout.Dimensions {
			id := "setup.step.account"
			if step == setupModels {
				id = "setup.step.models"
			} else if step == setupReady {
				id = "setup.step.ready"
			}
			b := u.clickable(id)
			for b.Clicked(gtx) {
				if gtx.Enabled() {
					u.setSetupStep(step)
					if u.invalidate != nil {
						u.invalidate()
					}
				}
			}
			return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				// Semantics live on a child area: Gio drops semantic content from a
				// clickable area without active gestures, which hid disabled steps.
				m := op.Record(gtx.Ops)
				// Center each row explicitly so circle, label and connector share one axis.
				centered := func(w layout.Widget) layout.FlexChild {
					return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.Center.Layout(gtx, w)
					})
				}
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				dims := layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					centered(u.setupStepCircle(step)),
					layout.Rigid(layout.Spacer{Height: 4}.Layout),
					centered(func(gtx layout.Context) layout.Dimensions {
						c, weight := nativeTextMuted, font.Normal
						if step == u.setupStep {
							c, weight = nativeInk, font.SemiBold
						}
						return u.textStyle(13, text, c, weight)(gtx)
					}),
				)
				call := m.Stop()
				defer clip.Rect(image.Rectangle{Max: dims.Size}).Push(gtx.Ops).Pop()
				semantic.ClassOp(semantic.Button).Add(gtx.Ops)
				semantic.LabelOp(text).Add(gtx.Ops)
				semantic.EnabledOp(gtx.Enabled()).Add(gtx.Ops)
				if step == u.setupStep {
					semantic.SelectedOp(true).Add(gtx.Ops)
				}
				call.Add(gtx.Ops)
				return dims
			})
		})
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				width, y := gtx.Constraints.Max.X, gtx.Dp(12)
				centers := [3]int{width / 6, width / 2, width * 5 / 6}
				for i := range 2 {
					paint.FillShape(gtx.Ops, nativeBorderStrong, clip.Rect{Min: image.Pt(centers[i]+gtx.Dp(12), y), Max: image.Pt(centers[i+1]-gtx.Dp(12), y+max(1, gtx.Dp(1)))}.Op())
				}
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Flexed(1, steps[0]),
					layout.Flexed(1, steps[1]),
					layout.Flexed(1, steps[2]),
				)
			}),
		)
	}
}

// setupStepCircle marks done steps with a check, the current step in brand colors and later steps as outlines.
func (u *nativeUI) setupStepCircle(step int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		size := gtx.Dp(24)
		square := func(inset int, c color.NRGBA) {
			paint.FillShape(gtx.Ops, c, clip.Rect(image.Rect(inset, inset, size-inset, size-inset)).Op())
		}
		var content layout.Widget
		switch {
		case step < u.setupStep:
			square(0, nativeSuccessFG)
			content = func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(16), gtx.Dp(16)))
				return nativeIconCheck.Layout(gtx, nativeSurface)
			}
		case step == u.setupStep:
			square(0, nativeInk)
			content = u.monoStyle(12, fmt.Sprint(step+1), nativeSurface, font.Bold)
		default:
			square(0, nativeInk)
			square(gtx.Dp(nativeLine), nativeBg)
			content = u.monoStyle(12, fmt.Sprint(step+1), nativeText, font.Bold)
		}
		gtx.Constraints = layout.Exact(image.Pt(size, size))
		return layout.Center.Layout(gtx, content)
	}
}

func (u *nativeUI) setupSummaryRowWidget(title, value string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(16), gtx.Dp(16)))
				return nativeIconCheck.Layout(gtx, nativeSuccessFG)
			}),
			layout.Rigid(layout.Spacer{Width: 10}.Layout),
			layout.Flexed(1, u.column(u.subheading(title), u.note(value))),
		)
	}
}

func (u *nativeUI) setupPanel() layout.Widget {
	if u.setupStep != setupConnect && u.setupConnectionNeeded() {
		u.setSetupStep(setupConnect)
	}
	var content layout.Widget
	switch u.setupStep {
	case setupModels:
		count := len(u.library.selection.Models)
		tone := nativeToneWarning
		if count > 0 {
			tone = nativeToneSuccess
		}
		badge := u.statusBadge(tone, fmt.Sprintf(u.tr("%d selected", "%d seleccionados"), count))
		introChildren := []layout.Widget{badge}
		if count > 0 {
			introChildren = append(introChildren, u.sharedDefaultPicker())
		}
		intro := u.section(u.tr("Choose your models", "Elige tus modelos"), u.tr("Your agents will share this curated model list.", "Tus agentes compartirán esta lista de modelos."), introChildren...)
		children := []layout.Widget{intro}
		if count == 0 {
			children = append(children, u.hint(u.tr("Choose at least one model to continue.", "Elige al menos un modelo para continuar.")))
		}
		content = u.column(append(children, u.modelsPanel())...)
	case setupReady:
		account := nativeString(u.state, "accountEmail")
		if account == "" {
			account = u.tr("Kilo account", "Cuenta de Kilo")
		}
		team := strings.TrimSpace(u.value("connection.org"))
		for _, raw := range nativeArray(u.state, "organizations") {
			org := nativeMap(raw)
			if nativeString(org, "id") == team && nativeString(org, "name") != "" {
				team = nativeString(org, "name")
				break
			}
		}
		if team != "" {
			account += " · " + team
		}
		if u.chatGPTReady() {
			subscription := nativeString(nativeMap(u.state["chatgpt"]), "email")
			if subscription == "" {
				subscription = u.tr("ChatGPT subscription", "Suscripción de ChatGPT")
			}
			if nativeBool(u.state, "kiloReady") {
				account += " · ChatGPT: " + subscription
			} else {
				account = subscription
			}
		}

		count := len(u.library.selection.Models)
		modelCount := fmt.Sprintf(u.tr("%d models selected", "%d modelos seleccionados"), count)
		if count == 1 {
			modelCount = u.tr("1 model selected", "1 modelo seleccionado")
		}
		defaultModel := u.library.selection.Initial
		if choice := u.library.selection.choice(defaultModel); choice != nil {
			defaultModel = nativeCodexDisplayName(*choice)
		}
		summary := u.section(u.tr("Your setup", "Tu configuración"), u.tr("Your connection and models are ready for the local proxy.", "Tu conexión y tus modelos están listos para el proxy local."),
			u.setupSummaryRowWidget(u.tr("Connected accounts", "Cuentas conectadas"), account),
			u.setupSummaryRowWidget(u.tr("Models", "Modelos"), modelCount),
			u.setupSummaryRowWidget(u.tr("Default model", "Modelo predeterminado"), defaultModel),
			u.pills(u.button("setup.connection-settings", u.tr("Connection settings", "Ajustes de conexión"), func() { u.page = "settings" })),
		)
		content = u.column(u.banner(nativeToneSuccess, u.tr("You're ready", "Todo está listo")), summary)
	default:
		content = u.column(u.setupConnectionPanel(), u.chatGPTAccountPanel())
	}
	return u.column(u.setupStepper(), content)
}

func (u *nativeUI) setupFooter() layout.Widget {
	var back, primary layout.Widget
	switch u.setupStep {
	case setupConnect:
		if u.chatGPTReady() {
			if nativeBool(nativeMap(u.state["chatgpt"]), "connected") {
				primary = u.disabled(!u.connectionWorking() && !u.setupConnectionNeeded(), u.primaryButton("primary.setup.connection-next", u.tr("Choose models", "Elegir modelos"), u.setupConnectionContinue))
			}
			break
		}
		hasKey := u.connectionHasKey()
		auth := nativeMap(u.state["auth"])
		pending := nativeString(auth, "status") == "pending" || nativeString(auth, "status") == "starting"
		if !hasKey && !pending && !u.expanded["setup.manual"] {
			primary = u.disabled(!u.kiloConnectionWorking() && !nativeBool(u.state, "running"), u.primaryButton("connection.login", u.tr("Sign in with Kilo / SSO", "Iniciar sesión con Kilo / SSO"), u.beginKiloLogin))
		} else if hasKey || u.expanded["setup.manual"] {
			canContinue := !u.kiloConnectionWorking() && !nativeBool(u.state, "running") && (hasKey || strings.TrimSpace(u.value("connection.key")) != "") && strings.TrimSpace(u.value("connection.org")) != ""
			primary = u.disabled(canContinue, u.primaryButton("primary.setup.connection-next", u.tr("Save & choose models", "Guardar y elegir modelos"), u.setupConnectionContinue))
		}
	case setupModels:
		back = u.button("setup.back-account", u.tr("Back", "Atrás"), func() { u.setSetupStep(setupConnect) })
		_, ready := u.libraryStatus()
		primary = u.disabled(ready && len(u.library.selection.Models) > 0, u.primaryButton("primary.setup.models-next", u.tr("Continue", "Continuar"), u.setupModelsContinue))
	case setupReady:
		back = u.button("setup.back-models", u.tr("Back", "Atrás"), func() { u.setSetupStep(setupModels) })
		label := u.tr("Start proxy and go to agents", "Arrancar proxy e ir a agentes")
		if nativeBool(u.state, "running") {
			label = u.tr("Go to agents", "Ir a agentes")
		} else if u.busy["POST/api/start"] {
			label = u.tr("Starting…", "Arrancando…")
		}
		primary = u.disabled(!u.connectionWorking(), u.primaryButton("primary.setup.finish", label, u.finishSetup))
	}
	children := []layout.FlexChild{}
	if back != nil {
		children = append(children, layout.Rigid(back))
	}
	children = append(children, layout.Flexed(1, layout.Spacer{}.Layout))
	if primary != nil {
		children = append(children, layout.Rigid(primary))
	}
	return u.column(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
	}, u.ghostButton("setup.later", u.tr("Explore the app — finish later", "Explorar la app — terminar después"), func() { u.page = "agents" }))
}

func (u *nativeUI) setupConnectionPanel() layout.Widget {
	auth := nativeMap(u.state["auth"])
	pending := nativeString(auth, "status") == "pending" || nativeString(auth, "status") == "starting"
	hasKey := u.connectionHasKey()
	editable := !u.kiloConnectionWorking() && !nativeBool(u.state, "running")
	widgets := []layout.Widget{}
	if !hasKey && u.expanded["setup.manual"] && !pending {
		widgets = append(widgets, u.disabled(editable, u.button("connection.login", u.tr("Sign in with Kilo / SSO", "Iniciar sesión con Kilo / SSO"), u.beginKiloLogin)))
	} else if hasKey && !pending {
		account := nativeString(u.state, "accountEmail")
		if account == "" {
			account = u.tr("Kilo credential connected", "Credencial de Kilo conectada")
		}
		widgets = append(widgets, u.actionRow(u.label(account), u.disabled(editable, u.button("setup.switch-account", u.tr("Use another account", "Usar otra cuenta"), u.beginKiloLogin))))
	}
	if pending {
		widgets = append(widgets, u.label(u.tr("Enter this code in Kilo: ", "Introduce este código en Kilo: ")+nativeString(auth, "code")), u.note(u.tr("Waiting for authorization. This screen will update when you finish signing in.", "Esperando autorización. Esta pantalla se actualizará al terminar el inicio de sesión.")), u.pills(u.button("connection.verify", u.tr("Open authorization page", "Abrir autorización"), func() { u.open(nativeString(auth, "verificationUrl")) }), u.button("connection.cancel", u.tr("Cancel login", "Cancelar inicio de sesión"), func() { u.call("POST", "/api/auth/cancel", map[string]any{}, u.acceptState) })))
	}
	if message := nativeString(auth, "message"); message != "" && nativeString(auth, "status") != "approved" {
		if nativeString(auth, "status") == "error" || nativeString(auth, "status") == "failed" {
			widgets = append(widgets, u.message(nativeToneError, nativeMessage(message, u.language)))
		} else {
			widgets = append(widgets, u.note(nativeMessage(message, u.language)))
		}
	}
	if hasKey && !pending {
		widgets = append(widgets, u.eyebrow(u.tr("Your team", "Tu equipo")))
		teamChoices := []nativeChoice{}
		for _, raw := range nativeArray(u.state, "organizations") {
			org := nativeMap(raw)
			id, name := nativeString(org, "id"), nativeString(org, "name")
			if name == "" {
				name = id
			}
			teamChoices = append(teamChoices, nativeChoice{Value: id, Label: name, Caption: id})
		}
		if len(teamChoices) > 0 {
			widgets = append(widgets, u.optionCards("team.", teamChoices, u.value("connection.org"), editable, func(id string) { u.setValue("connection.org", id) }))
		} else {
			widgets = append(widgets, u.note(u.tr("No teams loaded yet. Load your teams or enter the organization ID below.", "Aún no se han cargado equipos. Carga tus equipos o introduce el ID de organización abajo.")))
		}
		widgets = append(widgets, u.disabled(editable, u.button("connection.teams", u.tr("Load my teams", "Cargar mis equipos"), func() { u.call("POST", "/api/auth/organizations", map[string]any{}, u.acceptState) })))
	}
	widgets = append(widgets, u.disabled(editable, u.disclosure("setup.manual", u.tr("Use an API key or team ID instead", "Usar una API key o un ID de equipo en su lugar"))))
	if u.expanded["setup.manual"] {
		widgets = append(widgets, u.disabled(editable, u.field("connection.key", u.tr("Personal API key", "API key personal"), u.tr("Leave blank to keep the current key", "Deja vacío para mantener la clave"), true)), u.disabled(editable, u.field("connection.org", u.tr("Organization ID", "ID de organización"), "org_…", false)))
	} else if hasKey && u.value("connection.org") != "" {
		widgets = append(widgets, u.note(u.tr("Selected team ID: ", "ID de equipo seleccionado: ")+u.value("connection.org")))
	}
	widgets = append(widgets, u.disabled(editable, u.check("connection.remember", u.tr("Remember my login in the system credential store", "Recordar mi sesión en el almacén del sistema"), nil)), u.hint(u.tr("Remember saves your login in the system credential store, so you stay signed in after quitting.", "Recordar guarda tu inicio de sesión en el almacén del sistema para mantener la sesión al volver a abrir la aplicación.")))
	if hasKey || u.expanded["setup.manual"] {
		if !hasKey && strings.TrimSpace(u.value("connection.key")) == "" {
			widgets = append(widgets, u.hint(u.tr("Sign in or enter your personal API key to continue.", "Inicia sesión o introduce tu API key personal para continuar.")))
		} else if strings.TrimSpace(u.value("connection.org")) == "" {
			widgets = append(widgets, u.hint(u.tr("Choose a team to continue.", "Elige un equipo para continuar.")))
		}
	}
	if nativeBool(u.state, "running") {
		widgets = append(widgets, u.note(u.tr("Stop the proxy to change your connection, or continue with this account.", "Detén el proxy para cambiar la conexión o continúa con esta cuenta.")), u.pills(u.proxyButton(), u.button("setup.keep-account", u.tr("Continue with this account", "Continuar con esta cuenta"), func() { u.setSetupStep(setupModels) })))
	}
	return u.section(u.tr("Connect your Kilo account", "Conecta tu cuenta de Kilo"), u.tr("Choose the team that pays for your models.", "Elige el equipo que paga tus modelos."), widgets...)
}

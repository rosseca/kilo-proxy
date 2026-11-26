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
	if u.chatGPTReady() || u.liteLLMReady() {
		return false
	}
	return !u.owner.uiConnectionReady()
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
	return u.owner.readUIProjection().HasKey
}

func (u *nativeUI) connectionWorking() bool {
	return u.connectionWorkingExceptLaunch("")
}

func (u *nativeUI) connectionWorkingExceptLaunch(client string) bool {
	if u.chatGPTRequestBusy() || u.gatewayRequestBusy() {
		return true
	}
	for _, path := range []string{"/api/config", "/api/start", "/api/stop", "/api/forget", "/api/auth/start", "/api/auth/cancel", "/api/auth/organizations"} {
		if u.busy["POST"+path] {
			return true
		}
	}
	if u.clients != nil && u.clients.Launching != "" && u.clients.Launching != client {
		return true
	}
	auth := nativeString(nativeMap(u.state["auth"]), "status")
	if (auth == "pending" || auth == "starting") && !u.chatGPTReady() && !u.liteLLMReady() {
		return true
	}
	return nativeString(nativeMap(u.state["chatgpt"]), "status") == "pending" && !nativeBool(u.state, "kiloReady") && !u.liteLLMReady()
}

func (u *nativeUI) kiloConnectionWorking() bool {
	if u.gatewayRequestBusy() {
		return true
	}
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
	alternateReady := u.chatGPTReady() || u.liteLLMReady()
	if u.chatGPTRequestBusy() || alternateReady && u.connectionWorking() || !alternateReady && u.kiloConnectionWorking() {
		return
	}
	continueSetup := func() {
		if u.page != "setup" {
			u.refreshState()
			return
		}
		u.setNotice(nativeToneNeutral, "")
		u.setSetupStep(setupModels)
		u.expanded["library.catalog"] = false
		u.models, u.catalogCached = nil, false
		// Fetch after the saved connection revision reaches the UI, so a
		// previous account's catalog cannot win a concurrent refresh.
		u.modelsPending = true
		u.refreshState()
	}
	if alternateReady {
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
			u.setNotice(nativeToneWarning, u.tr("Could not confirm the proxy status. Try again.", "No se pudo confirmar el estado del proxy. Vuelve a intentarlo."))
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
		u.setNotice(nativeToneSuccess, u.tr("All set. Open an app to start working.", "Todo listo. Abre una app para empezar."))
	})
}

func (u *nativeUI) proxyButton() layout.Widget {
	running := nativeBool(u.state, "running")
	label := u.tr("Turn on", "Activar")
	if running {
		label = u.tr("Pause", "Pausar")
	} else if !u.agentConnectionReady() {
		label = u.tr("Connect an account", "Conectar una cuenta")
	} else if u.setupConnectionNeeded() {
		label = u.tr("Save connection", "Guardar conexión")
	}
	if u.busy["POST/api/start"] {
		label = u.tr("Turning on…", "Activando…")
	} else if u.busy["POST/api/stop"] {
		label = u.tr("Pausing…", "Pausando…")
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
	labels := [...]string{u.tr("Sign in", "Iniciar sesión"), u.tr("Models", "Modelos"), u.tr("Ready", "Listo")}
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
			square(gtx.Dp(nativeLine), nativeCitrus)
			content = u.monoStyle(12, fmt.Sprint(step+1), nativeInk, font.Bold)
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
		if u.setupContinueWhenSaved {
			if _, ready := u.libraryStatus(); ready && len(u.library.selection.Models) > 0 {
				u.setupContinueWhenSaved = false
				u.setupModelsContinue()
			}
		}
		content = u.setupModelsStep()
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
		if u.liteLLMReady() {
			identity := u.litellmSetupAccount()
			if nativeBool(u.state, "kiloReady") || u.chatGPTReady() {
				account += " · " + identity
			} else {
				account = identity
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
		summary := u.section(u.tr("Your setup", "Tu configuración"), u.tr("Press Get started and open your first app. You can change all of this later.", "Pulsa Empezar y abre tu primera app. Puedes cambiar todo esto después."),
			u.setupSummaryRowWidget(u.tr("Account", "Cuenta"), account),
			u.setupSummaryRowWidget(u.tr("Models", "Modelos"), modelCount),
			u.setupSummaryRowWidget(u.tr("Default model", "Modelo predeterminado"), defaultModel),
		)
		content = u.column(u.banner(nativeToneSuccess, u.tr("You're ready", "Todo está listo")), summary)
	default:
		content = u.column(u.setupConnectionPanel(), u.card(u.uxsLiteLLMRow()), u.setupChatGPTOption())
	}
	return u.column(u.setupStepper(), content)
}

func (u *nativeUI) setupFooter() layout.Widget {
	var back, primary layout.Widget
	switch u.setupStep {
	case setupConnect:
		if u.chatGPTReady() || u.liteLLMReady() {
			if u.liteLLMReady() || u.chatGPTConnected() {
				primary = u.disabled(!u.connectionWorking() && !u.setupConnectionNeeded(), u.primaryButton("primary.setup.connection-next", u.tr("Choose models", "Elegir modelos"), u.setupConnectionContinue))
			}
			break
		}
		hasKey := u.connectionHasKey()
		auth := nativeMap(u.state["auth"])
		pending := nativeString(auth, "status") == "pending" || nativeString(auth, "status") == "starting"
		if (hasKey || u.expanded["setup.manual"]) && !pending {
			canContinue := !u.kiloConnectionWorking() && !nativeBool(u.state, "running") && (hasKey || strings.TrimSpace(u.value("connection.key")) != "") && strings.TrimSpace(u.value("connection.org")) != ""
			primary = u.disabled(canContinue, u.primaryButton("primary.setup.connection-next", u.tr("Continue", "Continuar"), u.setupConnectionContinue))
		}
	case setupModels:
		back = u.button("setup.back-account", u.tr("Back", "Atrás"), func() { u.setSetupStep(setupConnect) })
		primary = u.uxmSetupPrimary()
	case setupReady:
		back = u.button("setup.back-models", u.tr("Back", "Atrás"), func() { u.setSetupStep(setupModels) })
		label := u.tr("Get started", "Empezar")
		if u.busy["POST/api/start"] {
			label = u.tr("Turning on…", "Activando…")
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
	if !hasKey && !pending {
		// The one action most people need: sign in through the browser.
		widgets = append(widgets, u.disabled(editable, u.primaryButton("connection.login", u.tr("Sign in with Kilo", "Iniciar sesión con Kilo"), u.beginKiloLogin)), u.note(u.tr("Your browser opens so you can confirm. Company single sign-on (SSO) works too.", "Se abrirá tu navegador para que lo confirmes. También funciona con el acceso de tu empresa (SSO).")))
	} else if hasKey && !pending {
		account := nativeString(u.state, "accountEmail")
		signedIn := u.tr("You're signed in to Kilo", "Has iniciado sesión en Kilo")
		if account != "" {
			signedIn = fmt.Sprintf(u.tr("Signed in as %s", "Has iniciado sesión como %s"), account)
		}
		widgets = append(widgets, u.actionRow(u.label(signedIn), u.disabled(editable, u.button("setup.switch-account", u.tr("Use another account", "Usar otra cuenta"), u.beginKiloLogin))))
	}
	if pending {
		widgets = append(widgets, u.setupAuthCode(auth))
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
			caption := ""
			if name == "" {
				name = id
			}
			teamChoices = append(teamChoices, nativeChoice{Value: id, Label: name, Caption: caption})
		}
		if len(teamChoices) > 0 {
			widgets = append(widgets, u.note(u.tr("Choose the team that pays for your models.", "Elige el equipo que paga tus modelos.")), u.optionCards("team.", teamChoices, u.value("connection.org"), editable, func(id string) { u.setValue("connection.org", id) }))
		} else {
			widgets = append(widgets, u.note(u.tr("Your teams haven't loaded yet.", "Tus equipos aún no se han cargado.")))
		}
		widgets = append(widgets, u.disabled(editable, u.button("connection.teams", u.tr("Load my teams", "Cargar mis equipos"), func() { u.call("POST", "/api/auth/organizations", map[string]any{}, u.acceptState) })))
	}
	if hasKey && !u.expanded["setup.manual"] && u.value("connection.org") != "" {
		widgets = append(widgets, u.note(u.tr("Selected team: ", "Equipo seleccionado: ")+u.teamName(u.value("connection.org"))))
	}
	if !pending {
		// Keys, organization IDs and keychain storage are for IT-provided setups.
		widgets = append(widgets, u.disabled(editable, u.disclosure("setup.manual", u.tr("Did your company give you a key? Use it here", "¿Tu empresa te dio una clave? Úsala aquí"))))
	}
	if u.expanded["setup.manual"] && !pending {
		widgets = append(widgets,
			u.disabled(editable, u.field("connection.key", u.tr("Personal API key", "API key personal"), u.tr("Leave blank to keep the current key", "Deja vacío para mantener la clave"), true)),
			u.disabled(editable, u.field("connection.org", u.tr("Organization ID", "ID de organización"), "org_…", false)),
			u.disabled(editable, u.check("connection.remember", u.tr("Keep me signed in on this computer", "Mantener la sesión iniciada en este equipo"), nil)),
			u.hint(u.tr("Saves your login in the system credential store, so you stay signed in after quitting.", "Guarda tu inicio de sesión en el almacén del sistema para mantener la sesión al volver a abrir la aplicación.")),
		)
	}
	if hasKey || u.expanded["setup.manual"] {
		if !hasKey && strings.TrimSpace(u.value("connection.key")) == "" {
			widgets = append(widgets, u.hint(u.tr("Sign in or enter your personal API key to continue.", "Inicia sesión o introduce tu API key personal para continuar.")))
		} else if strings.TrimSpace(u.value("connection.org")) == "" {
			widgets = append(widgets, u.hint(u.tr("Choose a team to continue.", "Elige un equipo para continuar.")))
		}
	}
	if nativeBool(u.state, "running") {
		widgets = append(widgets, u.note(u.tr("Pause the Kilo connection to change accounts, or continue with this account.", "Pausa la conexión con Kilo para cambiar de cuenta o continúa con esta cuenta.")), u.pills(u.proxyButton(), u.button("setup.keep-account", u.tr("Continue with this account", "Continuar con esta cuenta"), func() { u.setSetupStep(setupModels) })))
	}
	description := u.tr("Use the same account you use on kilo.ai.", "Usa la misma cuenta con la que entras en kilo.ai.")
	if hasKey {
		description = ""
	}
	return u.section(u.tr("Sign in to Kilo", "Inicia sesión en Kilo"), description, widgets...)
}

// setupAuthCode shows the device code large, with a copy button and the
// browser steps, because this is where most first-time users get lost.
func (u *nativeUI) setupAuthCode(auth map[string]any) layout.Widget {
	code := nativeString(auth, "code")
	steps := u.column(
		u.label(u.tr("1. Your browser opened the Kilo page.", "1. Se ha abierto la página de Kilo en tu navegador.")),
		u.label(u.tr("2. Check that it shows this code.", "2. Comprueba que muestra este código.")),
		u.label(u.tr("3. Press Authorize. This screen updates by itself.", "3. Pulsa Autorizar. Esta pantalla se actualiza sola.")),
	)
	codeRow := u.actionRow(u.column(u.eyebrow(u.tr("Your code", "Tu código")), u.monoStyle(32, code, nativeInk, font.Bold)), u.iconButton("connection.copy-code", u.tr("Copy", "Copiar"), nativeButtonSecondary, nativeIconCopy, func() { u.copy(code) }))
	return u.card(
		u.statusBadge(nativeToneInfo, u.tr("Waiting for you to authorize in the browser…", "Esperando a que autorices en el navegador…")),
		codeRow,
		steps,
		u.pills(
			u.button("connection.verify", u.tr("Open the browser again", "Volver a abrir el navegador"), func() { u.open(nativeString(auth, "verificationUrl")) }),
			u.ghostButton("connection.cancel", u.tr("Cancel login", "Cancelar inicio de sesión"), func() { u.call("POST", "/api/auth/cancel", map[string]any{}, u.acceptState) }),
		),
	)
}

// setupChatGPTOption keeps the experimental ChatGPT connection one click away
// so it does not compete with the Kilo sign-in. It stays open while in use.
func (u *nativeUI) setupChatGPTOption() layout.Widget {
	account := nativeMap(u.state["chatgpt"])
	status := nativeString(account, "status")
	if u.expanded["setup.chatgpt"] || u.chatGPTLoginRequested || nativeBool(account, "connected") || status == "pending" || status == "error" {
		return u.chatGPTAccountPanel()
	}
	return u.disclosure("setup.chatgpt", u.tr("Have ChatGPT Plus or Pro? Connect it too (optional)", "¿Tienes ChatGPT Plus o Pro? Conéctalo también (opcional)"))
}

//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"gioui.org/layout"
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
	u.owner.mu.Lock()
	defer u.owner.mu.Unlock()
	return strings.TrimSpace(u.owner.apiKey) == "" || strings.TrimSpace(u.owner.config.OrgID) == "" || u.owner.authPending() || u.owner.connectionNeedsSave
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
	u.notice = ""
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
	if u.connectionWorking() {
		return
	}
	u.saveConnection(func() {
		if u.page != "setup" {
			u.refreshState()
			return
		}
		u.notice = ""
		u.setSetupStep(setupModels)
		u.expanded["library.catalog"] = len(u.library.selection.Models) == 0
		u.models, u.catalogCached = nil, false
		// Fetch after the saved connection revision reaches the UI, so a
		// previous account's catalog cannot win a concurrent refresh.
		u.modelsPending = true
		u.refreshState()
	})
}

func (u *nativeUI) setupModelsContinue() {
	u.persistLibraryEdits()
	if u.setupConnectionNeeded() {
		u.setSetupStep(setupConnect)
		return
	}
	if len(u.library.selection.Models) == 0 {
		u.notice = u.tr("Choose at least one model to continue.", "Elige al menos un modelo para continuar.")
		return
	}
	if status, ready := u.libraryStatus(); !ready {
		u.notice = status
		return
	}
	u.notice = ""
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
	u.notice = ""
	u.call("POST", path, map[string]any{}, func(raw json.RawMessage) {
		u.acceptState(raw)
		if nativeBool(u.state, "running") != start {
			u.notice = u.tr("The proxy status could not be confirmed. Try again.", "No se pudo confirmar el estado del proxy. Vuelve a intentarlo.")
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
		u.notice = status
		return
	}
	u.setProxyRunning(true, func() {
		if u.page == "setup" && u.setupStep == setupReady {
			u.page = "agents"
		}
		u.notice = u.tr("Your proxy is running. Choose an agent to open your workspace.", "Tu proxy está activo. Elige un agente para abrir tu espacio de trabajo.")
	})
}

func (u *nativeUI) proxyButton() layout.Widget {
	running := nativeBool(u.state, "running")
	label := u.tr("Start proxy", "Arrancar proxy")
	if running {
		label = u.tr("Stop proxy", "Detener proxy")
	} else if !u.agentConnectionReady() {
		label = u.tr("Connect Kilo", "Conectar Kilo")
	} else if u.setupConnectionNeeded() {
		label = u.tr("Save connection", "Guardar conexión")
	}
	if u.busy["POST/api/start"] {
		label = u.tr("Starting…", "Arrancando…")
	} else if u.busy["POST/api/stop"] {
		label = u.tr("Stopping…", "Deteniendo…")
	}
	id := "primary.agents.proxy"
	if running {
		id = "agents.proxy.stop"
	}
	return u.disabled(!u.connectionWorking(), u.button(id, label, func() { u.setProxyRunning(!running, nil) }))
}

func (u *nativeUI) setupPanel() layout.Widget {
	if u.setupStep != setupConnect && u.setupConnectionNeeded() {
		u.setSetupStep(setupConnect)
	}
	steps := []layout.Widget{}
	for i, name := range []string{u.tr("Account & team", "Cuenta y equipo"), u.tr("Models", "Modelos"), u.tr("Start", "Arrancar")} {
		prefix := fmt.Sprintf("%d  ", i+1)
		if i < u.setupStep {
			prefix = "✓  "
		}
		steps = append(steps, u.modelCard(i == u.setupStep, u.eyebrow(prefix+name)))
	}
	content := u.setupConnectionPanel()
	switch u.setupStep {
	case setupModels:
		_, ready := u.libraryStatus()
		content = u.column(u.actionRow(u.note(u.tr("Choose at least one model. Changes save automatically.", "Elige al menos un modelo. Los cambios se guardan automáticamente.")), u.button("setup.back-account", u.tr("Back", "Atrás"), func() { u.setSetupStep(setupConnect) }), u.disabled(ready && len(u.library.selection.Models) > 0, u.button("primary.setup.models-next", u.tr("Continue", "Continuar"), u.setupModelsContinue))), u.modelsPanel())
	case setupReady:
		label := u.tr("Start proxy & go to agents", "Arrancar proxy e ir a agentes")
		if nativeBool(u.state, "running") {
			label = u.tr("Go to agents", "Ir a agentes")
		} else if u.busy["POST/api/start"] {
			label = u.tr("Starting…", "Arrancando…")
		}
		content = u.card(u.heading(u.tr("You're ready to connect your agents", "Ya puedes conectar tus agentes")), u.label(u.sharedModelSummary()), u.note(u.tr("Start the local proxy, then choose Codex or another agent. Opening an agent also starts the proxy automatically if it is stopped.", "Arranca el proxy local y elige Codex u otro agente. Abrir un agente también arranca el proxy automáticamente si está detenido.")), u.pills(u.disabled(!u.connectionWorking(), u.button("primary.setup.finish", label, u.finishSetup)), u.button("setup.back-models", u.tr("Review models", "Revisar modelos"), func() { u.setSetupStep(setupModels) }), u.button("setup.connection-settings", u.tr("Connection settings", "Ajustes de conexión"), func() { u.page = "settings" })))
	}
	return u.column(u.row(steps...), content, u.pills(u.button("setup.later", u.tr("Explore the app — finish setup later", "Explorar la app — terminar después"), func() { u.page = "agents" })))
}

func (u *nativeUI) setupConnectionPanel() layout.Widget {
	auth := nativeMap(u.state["auth"])
	pending := nativeString(auth, "status") == "pending" || nativeString(auth, "status") == "starting"
	hasKey := u.connectionHasKey()
	editable := !u.connectionWorking() && !nativeBool(u.state, "running")
	widgets := []layout.Widget{u.heading(u.tr("Connect your Kilo account", "Conecta tu cuenta de Kilo")), u.note(u.tr("Sign in in your browser, then choose the team that pays for your models.", "Inicia sesión en el navegador y elige el equipo que paga tus modelos."))}
	if !hasKey || pending {
		widgets = append(widgets, u.disabled(editable, u.button("connection.login", u.tr("Sign in with Kilo / SSO", "Iniciar sesión con Kilo / SSO"), u.beginKiloLogin)))
	} else {
		account := nativeString(u.state, "accountEmail")
		if account == "" {
			account = u.tr("Kilo credential connected", "Credencial de Kilo conectada")
		}
		widgets = append(widgets, u.actionRow(u.label(account), u.disabled(editable, u.button("setup.switch-account", u.tr("Use another account", "Usar otra cuenta"), u.beginKiloLogin))))
	}
	if pending {
		widgets = append(widgets, u.label(u.tr("Enter this code in Kilo: ", "Introduce este código en Kilo: ")+nativeString(auth, "code")), u.note(u.tr("Waiting for authorization. This screen will update when you finish signing in.", "Esperando autorización. Esta pantalla se actualizará al terminar el login.")), u.pills(u.button("connection.verify", u.tr("Open authorization page", "Abrir autorización"), func() { u.open(nativeString(auth, "verificationUrl")) }), u.button("connection.cancel", u.tr("Cancel login", "Cancelar login"), func() { u.call("POST", "/api/auth/cancel", map[string]any{}, u.acceptState) })))
	}
	if message := nativeString(auth, "message"); message != "" && nativeString(auth, "status") != "approved" {
		widgets = append(widgets, u.note(nativeMessage(message, u.language)))
	}
	if hasKey && !pending {
		widgets = append(widgets, u.eyebrow(u.tr("Your team", "Tu equipo")))
		teams := []layout.Widget{}
		for _, raw := range nativeArray(u.state, "organizations") {
			org := nativeMap(raw)
			id, name := nativeString(org, "id"), nativeString(org, "name")
			if name == "" {
				name = id
			}
			if u.value("connection.org") == id {
				name = "● " + name
			}
			teams = append(teams, u.disabled(editable, u.button("team."+id, name, func() { u.setValue("connection.org", id) })))
		}
		if len(teams) > 0 {
			widgets = append(widgets, u.pills(teams...))
		} else {
			widgets = append(widgets, u.note(u.tr("No teams loaded yet. Load your teams or enter the organization ID below.", "Aún no se han cargado equipos. Carga tus equipos o introduce el ID de organización abajo.")))
		}
		widgets = append(widgets, u.disabled(editable, u.button("connection.teams", u.tr("Load my teams", "Cargar mis equipos"), func() { u.call("POST", "/api/auth/organizations", map[string]any{}, u.acceptState) })))
	}
	widgets = append(widgets, u.pills(u.disabled(editable, u.button("setup.manual", u.tr("Use an API key or enter a team ID", "Usar una API key o un ID de equipo"), func() { u.expanded["setup.manual"] = !u.expanded["setup.manual"] }))))
	if u.expanded["setup.manual"] {
		widgets = append(widgets, u.disabled(editable, u.field("connection.key", u.tr("Personal API key", "API key personal"), u.tr("Leave blank to keep the current key", "Deja vacío para mantener la clave"), true)), u.disabled(editable, u.field("connection.org", u.tr("Organization ID", "ID de organización"), "org_…", false)))
	} else if hasKey && u.value("connection.org") != "" {
		widgets = append(widgets, u.note(u.tr("Selected team ID: ", "ID de equipo seleccionado: ")+u.value("connection.org")))
	}
	widgets = append(widgets, u.disabled(editable, u.check("connection.remember", u.tr("Remember my login in the system credential store", "Recordar mi sesión en el almacén del sistema"), nil)))
	if !u.checked("connection.remember") {
		widgets = append(widgets, u.note(u.tr("With this off, sign in again after quitting Kilo Proxy.", "Si lo desactivas, vuelve a iniciar sesión después de salir de Kilo Proxy.")))
	}
	canContinue := editable && (hasKey || strings.TrimSpace(u.value("connection.key")) != "") && strings.TrimSpace(u.value("connection.org")) != ""
	if hasKey || u.expanded["setup.manual"] {
		widgets = append(widgets, u.pills(u.disabled(canContinue, u.button("primary.setup.connection-next", u.tr("Save & choose models", "Guardar y elegir modelos"), u.setupConnectionContinue))))
	}
	if nativeBool(u.state, "running") {
		widgets = append(widgets, u.note(u.tr("Stop the proxy to change your connection, or continue with this account.", "Detén el proxy para cambiar la conexión o continúa con esta cuenta.")), u.pills(u.proxyButton(), u.button("setup.keep-account", u.tr("Continue with this account", "Continuar con esta cuenta"), func() { u.setSetupStep(setupModels) })))
	}
	return u.card(widgets...)
}

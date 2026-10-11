//go:build desktop

package main

import (
	"encoding/json"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/widget"
)

var uxsConnectionPaths = []string{"/api/config", "/api/start", "/api/stop", "/api/forget", "/api/auth/start", "/api/auth/cancel", "/api/auth/organizations"}

// uxsConnectionBusy reports any in-flight request that changes the connection.
func (u *nativeUI) uxsConnectionBusy() bool {
	for _, path := range uxsConnectionPaths {
		if u.busy["POST"+path] {
			return true
		}
	}
	return u.chatGPTRequestBusy()
}

func (u *nativeUI) uxsKiloPending() bool {
	status := nativeString(nativeMap(u.state["auth"]), "status")
	return status == "pending" || status == "starting"
}

// uxsKiloNeedsSave is a signed-in key with a team the backend has not saved yet
// (device login and auto-selected teams).
func (u *nativeUI) uxsKiloNeedsSave() bool {
	return nativeBool(u.state, "hasKey") && nativeString(u.state, "orgId") != "" && !nativeBool(u.state, "kiloReady") && !u.uxsKiloPending()
}

// uxsPauseProxy runs change with the proxy stopped. The local API only edits a
// stopped connection; uxsSettle starts the proxy again once things settle.
func (u *nativeUI) uxsPauseProxy(change func()) {
	if !nativeBool(u.state, "running") {
		change()
		return
	}
	s := u.uxs()
	s.resume = true
	u.call("POST", "/api/stop", map[string]any{}, func(raw json.RawMessage) {
		u.acceptState(raw)
		change()
	})
}

// uxsApplyConnection saves the team, key, port and remember choice through the
// same endpoint the old Save button used, restarting a running proxy.
func (u *nativeUI) uxsApplyConnection() {
	if u.uxsConnectionBusy() || u.uxsKiloPending() {
		return
	}
	port, err := strconv.Atoi(strings.TrimSpace(u.value("connection.port")))
	if err != nil || port < 1024 || port > 65535 {
		u.setNotice(nativeToneWarning, u.tr("Enter a port between 1024 and 65535", "Introduce un puerto entre 1024 y 65535"))
		return
	}
	u.uxsPauseProxy(func() {
		u.saveConnection(func() {
			u.setNotice(nativeToneSuccess, u.tr("Saved", "Guardado"))
			u.refreshState()
		})
	})
}

// uxsSettle finishes automatic work once connection requests are idle: save a
// fresh login, then restart a proxy that a change paused.
func (u *nativeUI) uxsSettle() {
	s := u.uxs()
	if u.uxsConnectionBusy() || u.uxsKiloPending() || nativeString(nativeMap(u.state["chatgpt"]), "status") == "pending" {
		return
	}
	if u.uxsKiloNeedsSave() {
		key := nativeString(u.state, "orgId") + "|" + nativeString(u.state, "accountEmail")
		if s.autoSaved != key {
			s.autoSaved = key
			u.uxsApplyConnection()
			return
		}
	}
	if s.resume {
		s.resume = false
		if !nativeBool(u.state, "running") && nativeBool(u.state, "connectionReady") {
			u.call("POST", "/api/start", map[string]any{}, u.acceptState)
		}
	}
}

// uxsAutoField is a text field that applies on Enter or when focus leaves it.
func (u *nativeUI) uxsAutoField(id, label, placeholder string, secret bool, apply func()) layout.Widget {
	field := u.field(id, label, placeholder, secret)
	return func(gtx layout.Context) layout.Dimensions {
		e := u.editor(id)
		e.Submit = true
		submitted := false
		for {
			ev, ok := e.Update(gtx)
			if !ok {
				break
			}
			if _, ok := ev.(widget.SubmitEvent); ok {
				submitted = true
			}
		}
		s := u.uxs()
		focused := gtx.Focused(e)
		blurred := s.focused[id] && !focused
		s.focused[id] = focused
		dims := field(gtx)
		if (submitted || blurred) && gtx.Enabled() && apply != nil {
			apply()
		}
		return dims
	}
}

func (u *nativeUI) uxsAccountsBlock() layout.Widget {
	return u.uxsBlock(u.tr("Accounts", "Cuentas"), u.tr("who pays for what your harnesses ask", "quién paga lo que piden tus harnesses"), u.uxsKiloRow(), u.uxsChatGPTRow())
}

func (u *nativeUI) uxsConnectionBlock() layout.Widget {
	base := nativeString(u.state, "baseURL")
	row := u.uxsRow(
		u.tr("Local address", "Dirección local"), u.tr("Any OpenAI-compatible client", "Cualquier cliente compatible con OpenAI"),
		u.uxsValue(u.uxsMono(base), u.tr("Harnesses you open from Kilo Proxy come already configured.", "Los harnesses que abres desde Kilo Proxy ya vienen configurados.")),
		u.uxsActions(
			u.button("connection.copy-url", u.tr("Copy URL", "Copiar URL"), func() { u.copy(nativeString(u.state, "baseURL")) }),
			u.button("connection.copy-key", u.tr("Copy local key", "Copiar clave local"), func() { u.copy(nativeString(u.state, "localKey")) }),
		),
		nil,
	)
	return u.uxsBlock(u.tr("Connection", "Conexión"), u.tr("for clients you set up by hand", "para clientes que configuras a mano"), row)
}

func (u *nativeUI) uxsTeamName(id string) string { return u.teamName(id) }

// uxsChooseTeam saves a team picked from the dropdown immediately.
func (u *nativeUI) uxsChooseTeam(id string) {
	u.expanded["uxs.team"] = false
	if id == nativeString(u.state, "orgId") && nativeBool(u.state, "kiloReady") {
		return
	}
	u.setValue("connection.org", id)
	u.uxsApplyConnection()
}

func (u *nativeUI) uxsLoadTeams() {
	u.uxsPauseProxy(func() {
		u.call("POST", "/api/auth/organizations", map[string]any{"apiKey": u.value("connection.key")}, func(raw json.RawMessage) {
			u.acceptState(raw)
			u.expanded["uxs.team"] = true
		})
	})
}

func (u *nativeUI) uxsKiloLogin() {
	u.uxsPauseProxy(u.beginKiloLogin)
}

func (u *nativeUI) uxsKiloSignOut() {
	u.uxsPauseProxy(func() {
		u.call("POST", "/api/forget", map[string]any{}, func(json.RawMessage) {
			u.setValue("connection.key", "")
			u.refreshState()
		})
	})
}

func (u *nativeUI) uxsCheckGateway() {
	revision := nativeNumber(u.state, "catalogRevision")
	u.call("POST", "/api/check", map[string]any{}, func(raw json.RawMessage) {
		if !u.applyModels(raw, revision, "catalog") {
			return
		}
		u.setNotice(nativeToneSuccess, u.tr("Gateway reached. Model listing does not verify organization credit or generation permissions.", "Gateway accesible. El catálogo no verifica saldo ni permisos de generación."))
	})
}

func (u *nativeUI) uxsKiloRow() layout.Widget {
	auth := nativeMap(u.state["auth"])
	pending := u.uxsKiloPending()
	busy := u.uxsConnectionBusy()
	hasKey := nativeBool(u.state, "hasKey")
	orgID := nativeString(u.state, "orgId")
	var status layout.Widget
	small := []string{}
	actions := []layout.Widget{}
	switch {
	case pending:
		status = u.uxsStatus(nativeToneInfo, u.tr("Waiting for authorization", "Esperando autorización"), nativeString(auth, "code"))
		small = append(small, u.tr("Authorize this device in Kilo:", "Autoriza este dispositivo en Kilo:")+" "+nativeString(auth, "code"))
		actions = append(actions,
			u.button("connection.verify", u.tr("Open authorization page", "Abrir autorización"), func() { u.open(nativeString(auth, "verificationUrl")) }),
			u.button("connection.cancel", u.tr("Cancel", "Cancelar"), func() { u.call("POST", "/api/auth/cancel", map[string]any{}, u.acceptState) }),
		)
	case hasKey && orgID != "":
		tone, label := nativeToneSuccess, u.tr("Connected", "Conectado")
		if !nativeBool(u.state, "kiloReady") {
			tone, label = nativeToneInfo, u.tr("Saving…", "Guardando…")
		}
		status = u.uxsStatus(tone, label, "")
		small = append(small, u.uxsKiloIdentity())
	case hasKey:
		status = u.uxsStatus(nativeToneWarning, u.tr("Choose a team", "Elige un equipo"), "")
		small = append(small, u.uxsKiloIdentity())
	default:
		status = u.uxsStatus(nativeToneNeutral, u.tr("Not connected", "Sin conectar"), "")
		small = append(small, u.tr("Sign in to use your Kilo credit and models.", "Inicia sesión para usar tus créditos y modelos de Kilo."))
		actions = append(actions, u.disabled(!busy, u.primaryButton("connection.login", u.tr("Sign in with Kilo", "Iniciar sesión con Kilo"), u.uxsKiloLogin)))
	}
	if hasKey && !pending {
		team := u.uxsTeamName(orgID)
		if team == "" {
			team = u.tr("choose", "elegir")
		}
		actions = append(actions, u.disabled(!busy, u.dropdownButton("uxs.team.toggle", u.tr("Team: ", "Equipo: ")+nativeEllipsis(team, 28), func() { u.expanded["uxs.team"] = !u.expanded["uxs.team"] })))
	}
	actions = append(actions, u.uxsToggleLink("uxs.kilo", "Manage", "Gestionar"))
	value := []layout.Widget{u.uxsValue(status, small...)}
	if msg := nativeString(auth, "message"); msg != "" {
		switch nativeString(auth, "status") {
		case "error", "failed", "denied", "expired":
			value = append(value, u.message(nativeToneError, nativeMessage(msg, u.language)))
		}
	}
	below := []layout.Widget{}
	if hasKey && (u.expanded["uxs.team"] || orgID == "") && !pending {
		below = append(below, u.uxsTeamMenu(busy))
	}
	if u.expanded["uxs.kilo"] {
		below = append(below, u.uxsKiloManage(busy, hasKey))
	}
	var area layout.Widget
	if len(below) > 0 {
		area = u.column(below...)
	}
	return u.uxsRow("Kilo", u.tr("Credit and most models", "Créditos y la mayoría de modelos"), u.column(value...), u.uxsActions(actions...), area)
}

func (u *nativeUI) uxsKiloIdentity() string {
	parts := []string{}
	if email := nativeString(u.state, "accountEmail"); email != "" {
		parts = append(parts, email)
	}
	var billing nativeBillingData
	nativeDecode(u.state["billing"], &billing)
	if balance := nativeBillingMoney(billing.BalanceUSD, billing.Status, billing.Stale); balance != "—" {
		parts = append(parts, u.tr("balance ", "saldo ")+balance)
	}
	return strings.Join(parts, " · ")
}

func (u *nativeUI) uxsTeamMenu(busy bool) layout.Widget {
	items := []layout.Widget{}
	current := nativeString(u.state, "orgId")
	for _, raw := range nativeArray(u.state, "organizations") {
		org := nativeMap(raw)
		id, name := nativeString(org, "id"), nativeString(org, "name")
		if name == "" {
			name = id
		}
		items = append(items, u.disabled(!busy, u.menuItem("team."+id, name, id == current, func() { u.uxsChooseTeam(id) })))
	}
	items = append(items, u.disabled(!busy, u.iconButton("connection.teams", u.tr("Load my teams", "Cargar mis equipos"), nativeButtonGhost, nativeIconRefresh, u.uxsLoadTeams)))
	children := []layout.Widget{u.eyebrow(u.tr("Team", "Equipo")), u.pills(items...)}
	if len(nativeArray(u.state, "organizations")) == 0 {
		children = append(children, u.note(u.tr("No teams loaded yet. Load them, or type the organization ID under Manage.", "Aún no hay equipos cargados. Cárgalos o escribe el ID de organización en Gestionar.")))
	}
	return u.column(children...)
}

func (u *nativeUI) uxsKiloManage(busy, hasKey bool) layout.Widget {
	editable := !busy && !u.uxsKiloPending()
	applyKey := func() {
		if strings.TrimSpace(u.value("connection.key")) == "" {
			return
		}
		if strings.TrimSpace(u.value("connection.org")) == "" {
			u.setNotice(nativeToneWarning, u.tr("Add the organization ID too; it saves on its own.", "Añade también el ID de organización; se guarda solo."))
			return
		}
		u.uxsApplyConnection()
	}
	applyOrg := func() {
		org := strings.TrimSpace(u.value("connection.org"))
		if org == "" || org == nativeString(u.state, "orgId") && nativeBool(u.state, "kiloReady") {
			return
		}
		if !hasKey && strings.TrimSpace(u.value("connection.key")) == "" {
			return
		}
		u.uxsApplyConnection()
	}
	applyPort := func() {
		if strings.TrimSpace(u.value("connection.port")) == strconv.Itoa(int(nativeNumber(u.state, "port"))) {
			return
		}
		u.uxsApplyConnection()
	}
	keyPlaceholder := "kilo_…"
	if hasKey {
		keyPlaceholder = "kilo_••••••••"
	}
	children := []layout.Widget{}
	if hasKey {
		children = append(children, u.pills(
			u.disabled(editable, u.dangerButton("connection.forget", u.tr("Sign out", "Cerrar sesión"), u.uxsKiloSignOut)),
			u.disabled(!busy, u.button("connection.check", u.tr("Check connection", "Comprobar conexión"), u.uxsCheckGateway)),
		))
	}
	children = append(children,
		u.eyebrow(u.tr("Advanced", "Avanzado")),
		u.disabled(editable, u.topRow(
			u.uxsAutoField("connection.key", u.tr("Use an API key instead of signing in", "Usar una API key en vez de iniciar sesión"), keyPlaceholder, true, applyKey),
			u.uxsAutoField("connection.org", u.tr("Organization ID (if it's not in the list)", "ID de organización (si no aparece en la lista)"), "org_…", false, applyOrg),
		)),
		u.disabled(editable, u.topRow(u.uxsAutoField("connection.port", u.tr("Local port", "Puerto local"), "8877", false, applyPort), func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{} })),
		u.disabled(editable, u.uxsToggle("connection.remember", u.tr("Remember the key in the system keychain", "Recordar la clave en el llavero del sistema"), u.tr("if you turn it off, we'll ask for it when the app opens", "si lo apagas, te la pediremos al abrir la app"), u.checked("connection.remember"), func(on bool) {
			u.setChecked("connection.remember", on)
			if hasKey && nativeString(u.state, "orgId") != "" {
				u.uxsApplyConnection()
			}
		})),
		u.note(u.tr("Fields save when you press Enter or leave them. If the proxy is running it restarts for a moment.", "Los campos se guardan al pulsar Intro o al salir de ellos. Si el proxy está en marcha, se reinicia un momento.")),
	)
	return u.column(children...)
}

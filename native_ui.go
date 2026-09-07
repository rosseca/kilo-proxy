//go:build desktop

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// All view state is confined to the window event goroutine. Network requests
// return through updates; polling never changes an editor the user is typing in.
type nativeUI struct {
	traceGeneration  uint64
	modelsPending    bool
	languageRevision uint64
	languageTarget   string

	owner                          *app
	invalidate                     func()
	theme                          *material.Theme
	updates                        chan func()
	editors                        map[string]*widget.Editor
	buttons                        map[string]*widget.Clickable
	checks                         map[string]*widget.Bool
	lists                          map[string]*widget.List
	expanded                       map[string]bool
	busy                           map[string]bool
	state                          map[string]any
	models                         []modelInfo
	clients                        *nativeClients
	client, language, page, notice string
	authenticated, laidOut         bool
	lastOrg                        string
	trace                          *requestTrace
	traceStage                     string
}

func newNativeUI(owner *app, invalidate func()) *nativeUI {
	t := material.NewTheme()
	t.Shaper = text.NewShaper(text.WithCollection(nativeFonts()))
	t.TextSize = 14
	t.Face = "Inter"
	t.Bg, t.Fg = nativeColor(0xf4f5ef), nativeColor(0x252b28)
	t.ContrastBg, t.ContrastFg = nativeColor(0x596b40), nativeColor(0x252b28)
	u := &nativeUI{owner: owner, invalidate: invalidate, theme: t, updates: make(chan func(), 128), editors: map[string]*widget.Editor{}, buttons: map[string]*widget.Clickable{}, checks: map[string]*widget.Bool{}, lists: map[string]*widget.List{}, expanded: map[string]bool{}, busy: map[string]bool{}, state: map[string]any{}, client: "codex", page: "connection", traceStage: "request"}
	owner.mu.Lock()
	u.language = owner.config.Language
	u.setValue("connection.org", owner.config.OrgID)
	u.setValue("connection.port", strconv.Itoa(owner.config.Port))
	u.setChecked("connection.remember", owner.config.Remember)
	owner.mu.Unlock()
	u.refreshState()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-owner.quit:
				return
			case <-ticker.C:
				u.enqueue(func() { u.refreshState() })
			}
		}
	}()
	return u
}
func nativeColor(rgb uint32) color.NRGBA {
	return color.NRGBA{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 255}
}
func (u *nativeUI) enqueue(f func()) {
	select {
	case u.updates <- f:
		if u.invalidate != nil {
			u.invalidate()
		}
	case <-u.owner.quit:
	}
}
func (u *nativeUI) drain() {
	for {
		select {
		case f := <-u.updates:
			f()
		default:
			return
		}
	}
}
func (u *nativeUI) tr(en, es string) string {
	if u.language == "es" {
		return es
	}
	return en
}
func (u *nativeUI) editor(id string) *widget.Editor {
	if u.editors[id] == nil {
		u.editors[id] = &widget.Editor{SingleLine: true, MaxLen: 16384}
	}
	return u.editors[id]
}
func (u *nativeUI) value(id string) string { return u.editor(id).Text() }
func (u *nativeUI) setValue(id, value string) {
	e := u.editor(id)
	if e.Text() != value {
		e.SetText(value)
	}
}
func (u *nativeUI) checked(id string) bool {
	if u.checks[id] == nil {
		u.checks[id] = &widget.Bool{}
	}
	return u.checks[id].Value
}
func (u *nativeUI) setChecked(id string, value bool) { u.checked(id); u.checks[id].Value = value }
func (u *nativeUI) clickable(id string) *widget.Clickable {
	if u.buttons[id] == nil {
		u.buttons[id] = &widget.Clickable{}
	}
	return u.buttons[id]
}
func (u *nativeUI) list(id string) *widget.List {
	if u.lists[id] == nil {
		u.lists[id] = &widget.List{List: layout.List{Axis: layout.Vertical}}
	}
	return u.lists[id]
}
func (u *nativeUI) label(s string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return material.Body1(u.theme, s).Layout(gtx) }
}
func (u *nativeUI) note(s string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		l := material.Caption(u.theme, s)
		l.Color = nativeColor(0x64705f)
		return l.Layout(gtx)
	}
}
func (u *nativeUI) heading(s string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return material.H6(u.theme, s).Layout(gtx) }
}
func (u *nativeUI) button(id, label string, action func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := u.clickable(id)
		for b.Clicked(gtx) {
			if gtx.Enabled() && action != nil {
				action()
				if u.invalidate != nil {
					u.invalidate()
				}
			}
		}
		style := material.Button(u.theme, b, label)
		style.CornerRadius = 7
		style.TextSize = 12
		style.Inset = layout.Inset{Top: 11, Bottom: 11, Left: 14, Right: 14}
		gtx.Constraints.Min.Y = max(gtx.Constraints.Min.Y, gtx.Dp(40))
		style.Background = nativeColor(0xeff2e8)
		if id == "connection.save-start" || id == "connection.login" || strings.Contains(id, "prepare") {
			style.Background = nativeColor(0xe8f36a)
		}
		if strings.HasPrefix(label, "● ") || strings.HasPrefix(label, "★ ") {
			style.Background = nativeColor(0x293b26)
			style.Color = nativeColor(0xf3f8df)
		}
		return style.Layout(gtx)
	}
}
func (u *nativeUI) disabled(enabled bool, child layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if !enabled {
			gtx = gtx.Disabled()
		}
		return child(gtx)
	}
}
func (u *nativeUI) field(id, label, placeholder string, secret bool) layout.Widget {
	return u.column(u.note(label), func(gtx layout.Context) layout.Dimensions {
		e := u.editor(id)
		e.SingleLine = true
		e.ReadOnly = false
		e.Mask = 0
		if secret {
			e.Mask = '•'
		}
		return widget.Border{Color: nativeColor(0xdfe3d8), Width: 1, CornerRadius: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			gtx.Constraints.Min.Y = max(0, gtx.Constraints.Min.Y-gtx.Dp(20))
			gtx.Constraints.Min.Y = max(gtx.Constraints.Min.Y, gtx.Dp(20))
			return layout.UniformInset(10).Layout(gtx, material.Editor(u.theme, e, placeholder).Layout)
		})
	})
}
func (u *nativeUI) check(id, label string, changed func(bool)) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		u.checked(id)
		w := u.checks[id]
		if w.Update(gtx) && changed != nil {
			changed(w.Value)
		}
		return material.CheckBox(u.theme, w, label).Layout(gtx)
	}
}
func (u *nativeUI) selectField(id, label string, options []string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		current := u.value(id)
		if !slices.Contains(options, current) && len(options) > 0 {
			current = options[0]
			u.setValue(id, current)
		}
		children := []layout.Widget{u.note(label), u.button(id+".toggle", current+"  ▾", func() { u.expanded[id] = !u.expanded[id] })}
		if u.expanded[id] {
			for _, option := range options {
				option := option
				children = append(children, u.button(id+".option."+option, option, func() { u.setValue(id, option); u.expanded[id] = false }))
			}
		}
		return u.column(children...)(gtx)
	}
}
func (u *nativeUI) column(children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		flex := make([]layout.FlexChild, 0, len(children)*2)
		for i, child := range children {
			if i > 0 {
				flex = append(flex, layout.Rigid(layout.Spacer{Height: 8}.Layout))
			}
			flex = append(flex, layout.Rigid(child))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, flex...)
	}
}
func (u *nativeUI) row(children ...layout.Widget) layout.Widget {
	return u.alignedRow(layout.End, children...)
}
func (u *nativeUI) topRow(children ...layout.Widget) layout.Widget {
	return u.alignedRow(layout.Start, children...)
}
func (u *nativeUI) alignedRow(alignment layout.Alignment, children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		max := 3
		if gtx.Constraints.Max.X < gtx.Dp(650) {
			max = 2
		}
		if gtx.Constraints.Max.X < gtx.Dp(380) {
			max = 1
		}
		rows := []layout.Widget{}
		for offset := 0; offset < len(children); offset += max {
			end := offset + max
			if end > len(children) {
				end = len(children)
			}
			subset := append([]layout.Widget(nil), children[offset:end]...)
			rows = append(rows, func(gtx layout.Context) layout.Dimensions {
				items := []layout.FlexChild{}
				for i, w := range subset {
					if i > 0 {
						items = append(items, layout.Rigid(layout.Spacer{Width: 10}.Layout))
					}
					items = append(items, layout.Flexed(1, w))
				}
				return layout.Flex{Axis: layout.Horizontal, Alignment: alignment}.Layout(gtx, items...)
			})
		}
		return u.column(rows...)(gtx)
	}
}
func (u *nativeUI) card(children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return nativeSurface(gtx, nativeColor(0xffffff), 12, func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: nativeColor(0xe0e5d8), Width: 1, CornerRadius: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(24).Layout(gtx, u.column(children...))
			})
		})
	}
}
func (u *nativeUI) scroll(id string, height unit.Dp, children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.Y = gtx.Dp(height)
		gtx.Constraints.Min.Y = 0
		return material.List(u.theme, u.list(id)).Layout(gtx, len(children), func(gtx layout.Context, i int) layout.Dimensions {
			return layout.Inset{Bottom: 10, Right: 14}.Layout(gtx, children[i])
		})
	}
}
func (u *nativeUI) code(id, value string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		e := u.editor(id)
		e.ReadOnly = true
		e.SingleLine = false
		e.MaxLen = 0
		e.Mask = 0
		if e.Text() != value {
			e.SetText(value)
		}
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		gtx.Constraints.Max.Y = gtx.Dp(300)
		return widget.Border{Color: nativeColor(0xdfe3d8), Width: 1, CornerRadius: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			style := material.Editor(u.theme, e, "")
			style.Font.Typeface = "Go Mono"
			style.TextSize = 12
			return layout.UniformInset(12).Layout(gtx, style.Layout)
		})
	}
}
func (u *nativeUI) copy(value string) {
	u.owner.mu.Lock()
	d := u.owner.desktop
	u.owner.mu.Unlock()
	if d == nil {
		u.notice = "Clipboard unavailable"
		return
	}
	if err := d.CopyText(value); err != nil {
		u.notice = nativeMessage(err.Error(), u.language)
	} else {
		u.notice = u.tr("Copied to clipboard", "Copiado al portapapeles")
	}
}
func (u *nativeUI) open(address string) {
	safe, err := externalDesktopURL(address)
	if err != nil {
		u.notice = nativeMessage(err.Error(), u.language)
		return
	}
	go func() {
		if err := openBrowser(safe); err != nil {
			u.enqueue(func() { u.notice = nativeMessage(err.Error(), u.language) })
		}
	}()
}

// The desktop uses exactly the same authenticated, bounded API as browser mode.
// It never duplicates configuration writes, keyring access or authorization rules.
func nativeRequest(owner *app, method, path string, payload any) (json.RawMessage, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, "http://"+owner.adminHost+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+owner.adminToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 45 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("Cannot reach the local service")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &failure)
		if failure.Error.Message == "" {
			failure.Error.Message = fmt.Sprintf("Local service returned HTTP %d", response.StatusCode)
		}
		return nil, errors.New(failure.Error.Message)
	}
	return raw, nil
}
func (u *nativeUI) call(method, path string, payload any, done func(json.RawMessage)) {
	key := method + path
	if u.busy[key] {
		return
	}
	u.busy[key] = true
	go func() {
		raw, err := nativeRequest(u.owner, method, path, payload)
		u.enqueue(func() {
			delete(u.busy, key)
			if err != nil {
				u.notice = nativeMessage(err.Error(), u.language)
				return
			}
			if done != nil {
				done(raw)
			}
		})
	}()
}
func (u *nativeUI) refreshState() {
	revision, saving := u.languageRevision, u.languageTarget != ""
	u.call("GET", "/api/state", nil, func(raw json.RawMessage) {
		u.acceptState(raw)
		// A response captured before a choice or during its save can contain
		// the previous language, even when it arrives after the save succeeds.
		if !saving && revision == u.languageRevision && u.languageTarget == "" {
			if lang := nativeString(u.state, "language"); lang != "" {
				u.language = lang
			}
		}
	})
}
func (u *nativeUI) acceptState(raw json.RawMessage) {
	var state map[string]any
	if json.Unmarshal(raw, &state) != nil {
		return
	}
	first := !u.authenticated
	oldRevision := nativeNumber(u.state, "catalogRevision")
	oldEpoch := nativeNumber(u.state, "activityEpoch")
	oldOrg := nativeString(u.state, "orgId")
	u.state = state
	u.authenticated = true
	if !first && oldEpoch != nativeNumber(state, "activityEpoch") {
		u.traceGeneration++
		u.trace = nil
	}
	if !first && oldRevision != nativeNumber(state, "catalogRevision") {
		u.models = nil
		if u.page == "clients" {
			u.modelsPending = true
		}
	}
	if u.modelsPending && !u.busy["POST/api/models"] {
		u.modelsPending = false
		u.refreshModels()
	}
	if first || oldOrg != nativeString(state, "orgId") {
		u.setValue("connection.org", nativeString(state, "orgId"))
	}
	if first {
		u.setValue("connection.port", fmt.Sprint(state["port"]))
		u.setChecked("connection.remember", nativeBool(state, "remember"))
		if warning := nativeString(state, "warning"); warning != "" {
			u.notice = nativeMessage(warning, u.language)
		}
	}
	if u.trace != nil {
		found := false
		for _, raw := range nativeArray(state, "events") {
			if nativeString(nativeMap(raw), "id") == u.trace.ID {
				found = true
				break
			}
		}
		if !found {
			u.trace = nil
		}
	}
}
func (u *nativeUI) applyModels(raw json.RawMessage, requestedRevision float64, field string) bool {
	var response struct {
		Models   []modelInfo `json:"models"`
		Catalog  []modelInfo `json:"catalog"`
		Revision float64     `json:"revision"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return false
	}
	if requestedRevision != nativeNumber(u.state, "catalogRevision") || response.Revision != nativeNumber(u.state, "catalogRevision") {
		u.modelsPending = true
		u.refreshState()
		return false
	}
	u.models = response.Models
	if field == "catalog" {
		u.models = response.Catalog
	}
	return true
}
func (u *nativeUI) refreshModels() {
	revision := nativeNumber(u.state, "catalogRevision")
	u.call("POST", "/api/models", map[string]any{}, func(raw json.RawMessage) {
		if u.applyModels(raw, revision, "models") {
			u.notice = fmt.Sprintf(u.tr("Loaded %d models", "%d modelos cargados"), len(u.models))
		}
	})
}
func nativeString(m map[string]any, key string) string  { v, _ := m[key].(string); return v }
func nativeBool(m map[string]any, key string) bool      { v, _ := m[key].(bool); return v }
func nativeNumber(m map[string]any, key string) float64 { v, _ := m[key].(float64); return v }
func nativeMap(v any) map[string]any                    { m, _ := v.(map[string]any); return m }
func nativeArray(m map[string]any, key string) []any    { v, _ := m[key].([]any); return v }

func (u *nativeUI) statusLabel() string {
	if nativeBool(u.state, "running") {
		return u.tr("Proxy running", "Proxy activo")
	}
	return u.tr("Proxy stopped", "Proxy detenido")
}
func (u *nativeUI) setLanguage(lang string) {
	if lang != "en" && lang != "es" {
		return
	}
	u.languageRevision++
	u.language, u.languageTarget = lang, lang
	u.saveLanguage()
}

func (u *nativeUI) saveLanguage() {
	const key = "POST/api/language"
	if u.languageTarget == "" || u.busy[key] {
		return
	}
	lang := u.languageTarget
	u.busy[key] = true
	go func() {
		_, err := nativeRequest(u.owner, "POST", "/api/language", map[string]string{"language": lang})
		u.enqueue(func() {
			delete(u.busy, key)
			// Preserve the latest choice while the previous request completes.
			// Serial writes also prevent an older save from winning on disk.
			if u.languageTarget != lang {
				u.saveLanguage()
				return
			}
			u.languageTarget = ""
			if err != nil {
				u.notice = nativeMessage(err.Error(), u.language)
			}
			u.refreshState()
		})
	}()
}
func (u *nativeUI) submitConnection(start bool) {
	port, err := strconv.Atoi(strings.TrimSpace(u.value("connection.port")))
	if err != nil || port < 1024 || port > 65535 {
		u.notice = u.tr("Enter a port between 1024 and 65535", "Introduce un puerto entre 1024 y 65535")
		return
	}
	submittedKey := u.value("connection.key")
	payload := map[string]any{"apiKey": submittedKey, "orgId": u.value("connection.org"), "port": port, "remember": u.checked("connection.remember")}
	u.call("POST", "/api/config", payload, func(json.RawMessage) {
		if u.value("connection.key") == submittedKey {
			u.setValue("connection.key", "")
		}
		u.notice = u.tr("Connection saved", "Conexión guardada")
		if start {
			u.call("POST", "/api/start", map[string]any{}, u.acceptState)
		} else {
			u.refreshState()
		}
	})
}
func (u *nativeUI) connectionPanel() layout.Widget {
	auth := nativeMap(u.state["auth"])
	pending := nativeString(auth, "status") == "pending" || nativeString(auth, "status") == "starting"
	connectionBusy := false
	for _, path := range []string{"/api/config", "/api/start", "/api/stop", "/api/forget", "/api/auth/start", "/api/auth/cancel", "/api/auth/organizations"} {
		if u.busy["POST"+path] {
			connectionBusy = true
		}
	}
	editable := !nativeBool(u.state, "running") && !pending && !connectionBusy
	login := []layout.Widget{u.heading(u.tr("Connect your Kilo team", "Conecta tu equipo de Kilo")), u.note(u.tr("Your tools send requests here. Kilo Proxy adds your organization header before forwarding them to Kilo.", "Tus herramientas envían aquí sus peticiones. Kilo Proxy añade la cabecera de tu organización antes de enviarlas a Kilo.")), u.disabled(editable, u.button("connection.login", u.tr("Sign in with Kilo / SSO", "Iniciar sesión con Kilo / SSO"), func() {
		u.call("POST", "/api/auth/start", map[string]any{}, func(raw json.RawMessage) {
			var auth map[string]any
			_ = json.Unmarshal(raw, &auth)
			if link := nativeString(auth, "verificationUrl"); link != "" {
				u.open(link)
			}
			u.refreshState()
		})
	}))}
	if email := nativeString(u.state, "accountEmail"); email != "" {
		login = append(login, u.label(email))
	}
	if pending {
		login = append(login, u.label(u.tr("Authorize this device in Kilo:", "Autoriza este dispositivo en Kilo:")+" "+nativeString(auth, "code")), u.pills(u.button("connection.verify", u.tr("Open authorization page", "Abrir autorización"), func() { u.open(nativeString(auth, "verificationUrl")) }), u.button("connection.cancel", u.tr("Cancel login", "Cancelar login"), func() { u.call("POST", "/api/auth/cancel", map[string]any{}, u.acceptState) })))
	}
	if msg := nativeString(auth, "message"); msg != "" {
		login = append(login, u.note(nativeMessage(msg, u.language)))
	}
	login = append(login, u.disabled(editable, u.field("connection.key", u.tr("Personal API key (or sign in above)", "API key personal (o inicia sesión arriba)"), u.tr("Leave blank to keep the current key", "Deja vacío para mantener la clave"), !u.checked("connection.reveal"))), u.check("connection.reveal", u.tr("Show API key", "Mostrar API key"), nil))
	for _, raw := range nativeArray(u.state, "organizations") {
		org := nativeMap(raw)
		id := nativeString(org, "id")
		name := nativeString(org, "name")
		if u.value("connection.org") == id {
			name = "✓ " + name
		}
		login = append(login, u.disabled(editable, u.button("team."+id, name, func() { u.setValue("connection.org", id) })))
	}
	login = append(login, u.disabled(editable, u.actionRow(u.row(u.field("connection.org", u.tr("Organization ID", "ID de organización"), "org_…", false), u.field("connection.port", u.tr("Local port", "Puerto local"), "8877", false)), u.button("connection.teams", u.tr("Load my teams", "Cargar mis equipos"), func() {
		u.call("POST", "/api/auth/organizations", map[string]any{"apiKey": u.value("connection.key")}, u.acceptState)
	}))), u.disabled(editable, u.check("connection.remember", u.tr("Remember upstream key in the system credential store", "Recordar la clave de Kilo en el almacén del sistema"), nil)), u.pills(u.disabled(editable, u.button("connection.save-start", u.tr("Save & start", "Guardar y arrancar"), func() { u.submitConnection(true) })), u.disabled(editable, u.button("connection.save", u.tr("Save connection", "Guardar conexión"), func() { u.submitConnection(false) })), u.disabled(nativeBool(u.state, "running"), u.button("connection.stop", u.tr("Stop proxy", "Detener proxy"), func() { u.call("POST", "/api/stop", map[string]any{}, u.acceptState) }))), u.pills(u.button("connection.check", u.tr("Check gateway", "Comprobar gateway"), func() {
		revision := nativeNumber(u.state, "catalogRevision")
		u.call("POST", "/api/check", map[string]any{}, func(raw json.RawMessage) {
			if !u.applyModels(raw, revision, "catalog") {
				return
			}
			u.notice = u.tr("Gateway reached. Model listing does not verify organization credit or generation permissions.", "Gateway accesible. El catálogo no verifica saldo ni permisos de generación.")
		})
	}), u.disabled(editable, u.button("connection.forget", u.tr("Forget upstream key", "Olvidar clave de Kilo"), func() {
		u.call("POST", "/api/forget", map[string]any{}, func(json.RawMessage) { u.setValue("connection.key", ""); u.refreshState() })
	}))))
	endpoint := u.card(u.heading(u.tr("Your local endpoint", "Tu endpoint local")), u.label(nativeString(u.state, "baseURL")), u.pills(u.button("connection.copy-url", u.tr("Copy base URL", "Copiar URL base"), func() { u.copy(nativeString(u.state, "baseURL")) }), u.button("connection.copy-key", u.tr("Copy local API key", "Copiar API key local"), func() { u.copy(nativeString(u.state, "localKey")) })), u.note(u.tr("The local key authenticates your tools. Your personal Kilo key stays on this device.", "La clave local autentica tus herramientas. Tu clave personal de Kilo permanece en este equipo.")), u.button("connection.clients", u.tr("Set up a client →", "Configurar un cliente →"), func() {
		u.page = "clients"
		if len(u.models) == 0 {
			u.refreshModels()
		}
	}))
	return u.column(u.card(login...), endpoint)
}

func (u *nativeUI) SmokeAction(action, value string) error {
	switch action {
	case "set-language":
		if value != "en" && value != "es" {
			return errors.New("invalid language")
		}
		u.setLanguage(value)
	case "copy-url":
		u.copy(nativeString(u.state, "baseURL"))
	case "configure-and-start":
		u.setValue("connection.key", "desktop-self-test-key")
		u.setValue("connection.org", "desktop-self-test-team")
		u.setValue("connection.port", value)
		u.setChecked("connection.remember", false)
		u.submitConnection(true)
	case "stop":
		u.call("POST", "/api/stop", map[string]any{}, u.acceptState)
	default:
		return errors.New("unknown native action")
	}
	return nil
}
func (u *nativeUI) SmokeSnapshot() map[string]string {
	ready := "false"
	if u.authenticated && u.laidOut {
		ready = "true"
	}
	status := "stopped"
	if nativeBool(u.state, "running") {
		status = "running"
	}
	return map[string]string{"version": version, "language": u.language, "language-saving": strconv.FormatBool(u.languageTarget != "" || u.busy["POST/api/language"]), "status": status, "content-ready": ready, "baseURL": nativeString(u.state, "baseURL"), "notice": u.notice}
}

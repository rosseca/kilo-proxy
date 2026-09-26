//go:build desktop

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// All view state is confined to the window event goroutine. Network requests
// return through updates; polling never changes an editor the user is typing in.
type nativeUI struct {
	frameMu             sync.Mutex
	closing             bool
	catalogCache        nativeCatalogCache
	catalogGeneration   uint64
	catalogCached       bool
	library             *nativeLibrary
	packs               *nativePackUI
	agents              *nativeAgents
	terminalCommands    *nativeTerminalCommands
	setupStep           int
	traceGeneration     uint64
	modelsPending       bool
	languageRevision    uint64
	languageTarget      string
	modelSortDismissTag int
	modelSortMenuTag    int
	modelMenuPointerTag int
	modelMenuPress      image.Point
	modelMenuViewport   image.Point
	modelMenuAnchors    map[string]image.Point
	activeModelMenu     *nativeModelMenuState

	imageDependencyDismissed bool
	imageSettingsFocus       bool
	updateRevision           uint64
	updateRequestFailed      bool
	proxyRevision            uint64

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
	noticeTone                     nativeTone
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
	t.Bg, t.Fg = nativeBg, nativeText
	t.ContrastBg, t.ContrastFg = nativeInk, nativeSurface
	u := &nativeUI{owner: owner, invalidate: invalidate, theme: t, updates: make(chan func(), 128), editors: map[string]*widget.Editor{}, buttons: map[string]*widget.Clickable{}, checks: map[string]*widget.Bool{}, lists: map[string]*widget.List{}, expanded: map[string]bool{}, busy: map[string]bool{}, state: map[string]any{}, client: "codex", page: "agents", traceStage: "request"}
	owner.mu.Lock()
	u.language = owner.config.Language
	u.setValue("connection.org", owner.config.OrgID)
	u.setValue("connection.port", strconv.Itoa(owner.config.Port))
	u.models = readNativeCatalogCache(owner.dir, owner.config.OrgID)
	u.catalogCached = len(u.models) > 0
	u.setChecked("connection.remember", owner.config.Remember)
	owner.mu.Unlock()
	u.initModelLibrary()
	if u.setupNeeded() {
		u.beginSetup()
	}
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
	return u.textStyle(14, s, nativeText, font.Normal)
}
func (u *nativeUI) note(s string) layout.Widget {
	return u.textStyle(12, s, nativeTextMuted, font.Normal)
}
func (u *nativeUI) heading(s string) layout.Widget {
	return u.monoStyle(17, s, nativeText, font.Bold)
}
func (u *nativeUI) subheading(s string) layout.Widget {
	return u.textStyle(14, s, nativeText, font.SemiBold)
}
func (u *nativeUI) button(id, label string, action func()) layout.Widget {
	return u.buttonKind(id, label, nativeButtonSecondary, nil, false, action)
}
func (u *nativeUI) primaryButton(id, label string, action func()) layout.Widget {
	return u.buttonKind(id, label, nativeButtonPrimary, nil, false, action)
}
func (u *nativeUI) ghostButton(id, label string, action func()) layout.Widget {
	return u.buttonKind(id, label, nativeButtonGhost, nil, false, action)
}
func (u *nativeUI) dangerButton(id, label string, action func()) layout.Widget {
	return u.buttonKind(id, label, nativeButtonDanger, nil, false, action)
}
func (u *nativeUI) iconButton(id, label string, kind nativeButtonKind, icon *widget.Icon, action func()) layout.Widget {
	return u.buttonWidget(id, label, kind, icon, false, false, action)
}
func (u *nativeUI) buttonKind(id, label string, kind nativeButtonKind, icon *widget.Icon, selected bool, action func()) layout.Widget {
	return u.buttonWidget(id, label, kind, icon, false, selected, action)
}
func (u *nativeUI) buttonWidget(id, label string, kind nativeButtonKind, icon *widget.Icon, iconAfter, selected bool, action func()) layout.Widget {
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
		background, foreground, border, borderWidth := nativeSurface, nativeInk, nativeBorderStrong, nativeLine
		switch kind {
		case nativeButtonPrimary:
			background, foreground, border = nativeAccent, nativeSurface, nativeAccent
		case nativeButtonGhost:
			background, border = color.NRGBA{}, color.NRGBA{}
		case nativeButtonDanger:
			foreground, border = nativeErrorFG, nativeErrorLine
		}
		if selected {
			background, foreground, border, borderWidth = nativeSelected, nativeInk, nativeInk, nativeLineHeavy
		}
		if !gtx.Enabled() {
			foreground = nativeTextDisabled
			if kind != nativeButtonGhost {
				background, border = nativeSurfaceAlt, color.NRGBA{}
			}
		} else if b.Hovered() {
			switch kind {
			case nativeButtonPrimary:
				background = nativeAccentHover
			case nativeButtonDanger:
				background = nativeErrorBG
			default:
				if selected {
					background = nativeSelected
				} else {
					background = nativeSurfaceAlt
				}
			}
		}
		if selected && icon == nil {
			icon = nativeIconCheck
		}
		weight := font.Medium
		if selected {
			weight = font.SemiBold
		}
		inset := layout.Inset{Top: 9, Bottom: 9, Left: 14, Right: 14}
		gtx.Constraints.Min.Y = max(gtx.Constraints.Min.Y, gtx.Dp(36))
		buttonBackground := material.ButtonLayout(u.theme, b)
		buttonBackground.Background, buttonBackground.CornerRadius = background, 0
		return widget.Border{Color: border, Width: borderWidth}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return buttonBackground.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				semantic.LabelOp(label).Add(gtx.Ops)
				if selected {
					semantic.SelectedOp(true).Add(gtx.Ops)
				}
				return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					iconWidget := func(gtx layout.Context) layout.Dimensions {
						if icon == nil {
							return layout.Dimensions{}
						}
						gtx.Constraints.Min = image.Pt(gtx.Dp(16), gtx.Dp(16))
						return icon.Layout(gtx, foreground)
					}
					iconGap := func(gtx layout.Context) layout.Dimensions {
						if icon == nil {
							return layout.Dimensions{}
						}
						return layout.Spacer{Width: 7}.Layout(gtx)
					}
					// Plain text: a material.Button here would lay out the same Clickable twice.
					textWidget := u.textStyle(13, label, foreground, weight)
					if iconAfter {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, layout.Rigid(textWidget), layout.Rigid(iconGap), layout.Rigid(iconWidget))
					}
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, layout.Rigid(iconWidget), layout.Rigid(iconGap), layout.Rigid(textWidget))
				})
			})
		})
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
	return u.column(u.label(label), func(gtx layout.Context) layout.Dimensions {
		e := u.editor(id)
		e.SingleLine = true
		e.ReadOnly = false
		e.Mask = 0
		if secret {
			e.Mask = '•'
		}
		border, width, background := nativeBorderStrong, nativeLine, nativeSurface
		if gtx.Focused(e) {
			border, width = nativeInk, nativeLineHeavy
		}
		if !gtx.Enabled() {
			background = nativeSurfaceAlt
		}
		return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: border, Width: width}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				gtx.Constraints.Min.Y = max(0, gtx.Constraints.Min.Y-gtx.Dp(20))
				gtx.Constraints.Min.Y = max(gtx.Constraints.Min.Y, gtx.Dp(20))
				return layout.UniformInset(10).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					style := material.Editor(u.theme, e, placeholder)
					style.Color, style.HintColor, style.TextSize = nativeText, nativeTextSubtle, 14
					return style.Layout(gtx)
				})
			})
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
func nativeChoices(values []string) []nativeChoice {
	choices := make([]nativeChoice, 0, len(values))
	for _, value := range values {
		choices = append(choices, nativeChoice{Value: value, Label: value})
	}
	return choices
}
func (u *nativeUI) selectField(id, label string, options []nativeChoice) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		current := u.value(id)
		var selected nativeChoice
		for _, option := range options {
			if option.Value == current {
				selected = option
				break
			}
		}
		if selected.Value == "" && len(options) > 0 {
			selected = options[0]
			u.setValue(id, selected.Value)
		}
		children := []layout.Widget{u.label(label), u.dropdownButton(id+".toggle", selected.Label, func() { u.expanded[id] = !u.expanded[id] })}
		if u.expanded[id] {
			for _, option := range options {
				option := option
				children = append(children, u.menuItem(id+".option."+option.Value, option.Label, option.Value == current, func() { u.setValue(id, option.Value); u.expanded[id] = false }))
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
		return nativeHardShadow(gtx, func(gtx layout.Context) layout.Dimensions {
			return nativeBox(gtx, nativeSurface, func(gtx layout.Context) layout.Dimensions {
				return widget.Border{Color: nativeBorder, Width: nativeLine}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.UniformInset(nativeSpace24).Layout(gtx, u.column(children...))
				})
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
		return widget.Border{Color: nativeBorderStrong, Width: 1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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
		u.setNotice(nativeToneWarning, u.tr("Clipboard unavailable", "Portapapeles no disponible"))
		return
	}
	if err := d.CopyText(value); err != nil {
		u.noticeError(err)
	} else {
		u.setNotice(nativeToneSuccess, u.tr("Copied to clipboard", "Copiado al portapapeles"))
	}
}
func (u *nativeUI) open(address string) {
	safe, err := externalDesktopURL(address)
	if err != nil {
		u.noticeError(err)
		return
	}
	u.owner.mu.Lock()
	bridge := u.owner.desktop
	u.owner.mu.Unlock()
	go func() {
		open := openBrowser
		if bridge != nil {
			open = bridge.OpenExternal
		}
		if err := open(safe); err != nil {
			u.enqueue(func() { u.noticeError(err) })
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
	changesProxy := method == "POST" && (path == "/api/start" || path == "/api/stop")
	if changesProxy {
		u.proxyRevision++
	}
	go func() {
		raw, err := nativeRequest(u.owner, method, path, payload)
		u.enqueue(func() {
			delete(u.busy, key)
			if changesProxy {
				u.proxyRevision++
			}
			if err != nil {
				u.noticeError(err)
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
	proxyRevision, proxySaving := u.proxyRevision, u.busy["POST/api/start"] || u.busy["POST/api/stop"]
	updateRevision, updateSaving := u.updateRevision, u.busy["POST/api/updates"]
	c := u.clientState()
	desktopRevision, desktopSaving := c.DesktopExperimentalRevision, c.DesktopExperimentalTarget != nil
	u.call("GET", "/api/state", nil, func(raw json.RawMessage) {
		// A snapshot that overlaps Start or Stop can predate the completed
		// operation. Discard it before applying any state-derived effects;
		// the next poll will fetch a coherent snapshot, including after errors.
		if proxySaving || proxyRevision != u.proxyRevision || u.busy["POST/api/start"] || u.busy["POST/api/stop"] {
			return
		}
		desktopExperimental := u.claudeDesktopExperimentalModels()
		update := u.state["update"]
		u.acceptState(raw)
		// A poll captured before a manual check must not restore its old status.
		if updateSaving || updateRevision != u.updateRevision || u.busy["POST/api/updates"] {
			u.state["update"] = update
		} else if u.updateRequestFailed {
			var previous releaseUpdateState
			nativeDecode(update, &previous)
			current := u.releaseUpdate()
			if current.Checking || current.CheckedAt != "" && current.CheckedAt != previous.CheckedAt {
				u.updateRequestFailed = false
			}
		}
		// Ignore a Desktop option snapshot captured before or during its save.
		if desktopSaving || desktopRevision != c.DesktopExperimentalRevision || c.DesktopExperimentalTarget != nil {
			u.state["claudeDesktopExperimentalModels"] = desktopExperimental
		}
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
	loginApproved := nativeString(nativeMap(state["auth"]), "status") == "approved" && nativeString(nativeMap(u.state["auth"]), "status") != "approved"
	u.acceptImageDependencyState(state)
	u.state = state
	u.authenticated = true
	if !first && oldEpoch != nativeNumber(state, "activityEpoch") {
		u.traceGeneration++
		u.trace = nil
	}
	if !first && oldRevision != nativeNumber(state, "catalogRevision") {
		u.models = nil
		if u.library != nil {
			for i := range u.library.selection.Models {
				choice := &u.library.selection.Models[i]
				m := &choice.Model
				*m = modelInfo{ID: m.ID, Name: m.ID, MaxOutputTokens: m.MaxOutputTokens}
				choice.MaximumOutputTokens = 0
			}
		}
		u.catalogCached = false
		if u.page == "clients" || u.page == "models" || u.page == "agents" || u.page == "setup" && u.setupStep == setupModels {
			u.modelsPending = true
		}
	}
	if u.modelsPending && !u.busy["POST/api/models"] {
		u.modelsPending = false
		u.refreshModels()
	}
	if first || loginApproved || oldOrg != nativeString(state, "orgId") {
		u.setValue("connection.org", nativeString(state, "orgId"))
	}
	if loginApproved {
		u.setValue("connection.key", "")
	}
	if first {
		u.setValue("connection.port", fmt.Sprint(state["port"]))
		u.setChecked("connection.remember", nativeBool(state, "remember"))
		if warning := nativeString(state, "warning"); warning != "" {
			u.setNotice(nativeToneWarning, nativeMessage(warning, u.language))
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
	u.owner.mu.Lock()
	currentRevision, org := u.owner.catalogRevision, u.owner.config.OrgID
	u.owner.mu.Unlock()
	if response.Revision != float64(currentRevision) {
		u.modelsPending = true
		u.refreshState()
		return false
	}
	u.models = response.Models
	if field == "catalog" {
		u.models = response.Catalog
	}
	u.catalogCached = false
	u.cacheModels(org)
	return true
}
func (u *nativeUI) refreshModels() {
	revision := nativeNumber(u.state, "catalogRevision")
	u.call("POST", "/api/models", map[string]any{}, func(raw json.RawMessage) {
		if u.applyModels(raw, revision, "models") {
			if u.page == "setup" {
				u.setNotice(nativeToneNeutral, "")
			} else {
				u.setNotice(nativeToneSuccess, fmt.Sprintf(u.tr("Loaded %d models", "%d modelos cargados"), len(u.models)))
			}
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
				u.noticeError(err)
			}
			u.refreshState()
		})
	}()
}
func (u *nativeUI) submitConnection(start bool) {
	u.saveConnection(func() {
		u.setNotice(nativeToneSuccess, u.tr("Connection saved", "Conexión guardada"))
		if start {
			u.call("POST", "/api/start", map[string]any{}, u.acceptState)
		} else {
			u.refreshState()
		}
	})
}

func (u *nativeUI) saveConnection(done func()) {
	port, err := strconv.Atoi(strings.TrimSpace(u.value("connection.port")))
	if err != nil || port < 1024 || port > 65535 {
		u.setNotice(nativeToneWarning, u.tr("Enter a port between 1024 and 65535", "Introduce un puerto entre 1024 y 65535"))
		return
	}
	submittedKey := u.value("connection.key")
	payload := map[string]any{"apiKey": submittedKey, "orgId": u.value("connection.org"), "port": port, "remember": u.checked("connection.remember")}
	u.call("POST", "/api/config", payload, func(json.RawMessage) {
		if u.value("connection.key") == submittedKey {
			u.setValue("connection.key", "")
		}
		if done != nil {
			done()
		}
	})
}
func (u *nativeUI) connectionPanel() layout.Widget {
	auth := nativeMap(u.state["auth"])
	pending := nativeString(auth, "status") == "pending" || nativeString(auth, "status") == "starting"
	running := nativeBool(u.state, "running")
	connectionBusy := false
	for _, path := range []string{"/api/config", "/api/start", "/api/stop", "/api/forget", "/api/auth/start", "/api/auth/cancel", "/api/auth/organizations"} {
		if u.busy["POST"+path] {
			connectionBusy = true
		}
	}
	editable := !running && !pending && !connectionBusy
	hasKey := nativeBool(u.state, "hasKey")
	account := []layout.Widget{}
	if hasKey {
		account = append(account, u.statusBadge(nativeToneSuccess, u.tr("Signed in", "Sesión iniciada")))
		email := nativeString(u.state, "accountEmail")
		if email == "" {
			email = u.tr("Kilo credential connected", "Credencial de Kilo conectada")
		}
		account = append(account, u.label(email))
		teamID := u.value("connection.org")
		teamName := teamID
		for _, raw := range nativeArray(u.state, "organizations") {
			org := nativeMap(raw)
			if nativeString(org, "id") == teamID && nativeString(org, "name") != "" {
				teamName = nativeString(org, "name")
				break
			}
		}
		if teamName != "" {
			account = append(account, u.note(u.tr("Team: ", "Equipo: ")+teamName))
		}
		account = append(account, u.pills(u.disabled(editable, u.dangerButton("connection.forget", u.tr("Sign out", "Cerrar sesión"), func() {
			u.call("POST", "/api/forget", map[string]any{}, func(json.RawMessage) { u.setValue("connection.key", ""); u.refreshState() })
		}))))
	} else {
		account = append(account, u.disabled(editable, u.primaryButton("connection.login", u.tr("Sign in with Kilo / SSO", "Iniciar sesión con Kilo / SSO"), u.beginKiloLogin)))
	}
	if pending {
		account = append(account,
			u.label(u.tr("Authorize this device in Kilo:", "Autoriza este dispositivo en Kilo:")+" "+nativeString(auth, "code")),
			u.pills(
				u.button("connection.verify", u.tr("Open authorization page", "Abrir autorización"), func() { u.open(nativeString(auth, "verificationUrl")) }),
				u.button("connection.cancel", u.tr("Cancel login", "Cancelar inicio de sesión"), func() { u.call("POST", "/api/auth/cancel", map[string]any{}, u.acceptState) }),
			),
		)
	}
	if msg := nativeString(auth, "message"); msg != "" {
		if nativeString(auth, "status") == "error" || nativeString(auth, "status") == "failed" {
			account = append(account, u.message(nativeToneError, nativeMessage(msg, u.language)))
		} else {
			account = append(account, u.note(nativeMessage(msg, u.language)))
		}
	}
	account = append(account, u.disabled(editable, u.disclosure("connection.reveal", u.tr("Use an API key instead", "Usar una API key"))))
	if u.expanded["connection.reveal"] {
		account = append(account, u.disabled(editable, u.field("connection.key", u.tr("Personal API key", "API key personal"), u.tr("Leave blank to keep the current key", "Deja vacío para mantener la clave"), true)))
	}

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
		account = append(account, u.optionCards("team.", teamChoices, u.value("connection.org"), editable, func(id string) { u.setValue("connection.org", id) }))
	}
	loadTeams := u.disabled(editable, u.button("connection.teams", u.tr("Load my teams", "Cargar mis equipos"), func() {
		u.call("POST", "/api/auth/organizations", map[string]any{"apiKey": u.value("connection.key")}, u.acceptState)
	}))
	if len(teamChoices) == 0 {
		account = append(account, u.disabled(editable, u.actionRow(u.field("connection.org", u.tr("Organization ID", "ID de organización"), "org_…", false), loadTeams)))
	} else {
		account = append(account, u.pills(loadTeams))
	}
	account = append(account,
		u.disabled(editable, u.disclosure("connection.advanced", u.tr("Advanced", "Avanzado"))),
	)
	if u.expanded["connection.advanced"] {
		account = append(account, u.disabled(editable, u.field("connection.port", u.tr("Local port", "Puerto local"), "8877", false)))
	}
	account = append(account,
		u.disabled(editable, u.check("connection.remember", u.tr("Remember upstream key in the system credential store", "Recordar la clave de Kilo en el almacén del sistema"), nil)),
		u.hint(u.tr("Turn this off if you want to enter your Kilo key again after quitting.", "Desactívalo si quieres volver a introducir tu clave de Kilo al salir y abrir la aplicación.")),
		u.pills(
			u.disabled(editable, u.primaryButton("connection.save-start", u.tr("Save & start", "Guardar y arrancar"), func() { u.submitConnection(true) })),
			u.disabled(editable, u.button("connection.save", u.tr("Save connection", "Guardar conexión"), func() { u.submitConnection(false) })),
			u.disabled(running, u.button("connection.stop", u.tr("Stop proxy", "Detener proxy"), func() { u.call("POST", "/api/stop", map[string]any{}, u.acceptState) })),
		),
		u.pills(u.button("connection.check", u.tr("Check gateway", "Comprobar gateway"), func() {
			revision := nativeNumber(u.state, "catalogRevision")
			u.call("POST", "/api/check", map[string]any{}, func(raw json.RawMessage) {
				if !u.applyModels(raw, revision, "catalog") {
					return
				}
				u.setNotice(nativeToneSuccess, u.tr("Gateway reached. Model listing does not verify organization credit or generation permissions.", "Gateway accesible. El catálogo no verifica saldo ni permisos de generación."))
			})
		})),
		u.subheading(u.tr("Your local endpoint", "Tu endpoint local")),
		u.label(nativeString(u.state, "baseURL")),
		u.pills(
			u.button("connection.copy-url", u.tr("Copy base URL", "Copiar URL base"), func() { u.copy(nativeString(u.state, "baseURL")) }),
			u.button("connection.copy-key", u.tr("Copy local API key", "Copiar API key local"), func() { u.copy(nativeString(u.state, "localKey")) }),
		),
		u.note(u.tr("The local key authenticates your tools. Your personal Kilo key stays on this device.", "La clave local autentica tus herramientas. Tu clave personal de Kilo permanece en este equipo.")),
		u.pills(u.buttonWidget("connection.clients", u.tr("Go to agents", "Ir a agentes"), nativeButtonGhost, nativeIconChevronRight, true, false, func() {
			u.page = "agents"
			if len(u.models) == 0 {
				u.refreshModels()
			}
		})),
	)
	if !editable {
		account = append(account, u.hint(u.tr("Stop the proxy or wait for the current connection action to finish before editing these settings.", "Detén el proxy o espera a que termine la acción de conexión para editar estos ajustes.")))
	}
	return nativeSettingsPanelWithGap(u.section(
		u.tr("Kilo account", "Cuenta de Kilo"),
		u.tr("Sign in and choose the team Kilo Proxy uses for upstream requests.", "Inicia sesión y elige el equipo que Kilo Proxy usará para las peticiones."),
		account...,
	))
}

func nativeSettingsPanelWithGap(panel layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: 8}.Layout(gtx, panel)
	}
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
		if handled, err := u.modelSmokeAction(action, value); handled {
			return err
		}
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
	snapshot := map[string]string{"version": version, "language": u.language, "language-saving": strconv.FormatBool(u.languageTarget != "" || u.busy["POST/api/language"]), "status": status, "content-ready": ready, "baseURL": nativeString(u.state, "baseURL"), "notice": u.notice}
	for key, value := range u.modelSmokeSnapshot() {
		snapshot[key] = value
	}
	return snapshot
}

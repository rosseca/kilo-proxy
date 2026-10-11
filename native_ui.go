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
	"unicode/utf8"

	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
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
	catalogFetchedAt    time.Time
	teamNameAsked       string                    // team whose name was already requested in the background
	appIcons            map[string]*nativeAppIcon // application icons by bundle path, loaded in the background
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
	contextRemovalIDs   []string
	contextRemovalTag   int

	imageDependencyDismissed    bool
	updateRevision              uint64
	updateRequestFailed         bool
	packageUpdateConfirmVersion string
	packageUpdateConfirmMethod  string
	packageUpdateStarted        bool
	packageUpdateFailed         bool
	proxyRevision               uint64
	chatGPTRevision             uint64
	chatGPTLoginRequested       bool

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
	sidebarVisible                 bool
	settingsTab                    string
	uxSettings                     *uxsSettingsState
	// setupContinueWhenSaved advances setup once accepted recommendations are saved.
	setupContinueWhenSaved bool
	lastOrg                string
	trace                  *requestTrace
	traceStage             string
}

// nativeRememberDefault keeps a saved login's choice. Without a saved login the
// box starts checked, so people who sign in stay signed in after quitting.
func nativeRememberDefault(remember, hasKey bool) bool { return remember || !hasKey }

func newNativeUI(owner *app, invalidate func()) *nativeUI {
	t := material.NewTheme()
	t.Shaper = text.NewShaper(text.WithCollection(nativeFonts()))
	t.TextSize = 14
	t.Face = nativeSansFace
	t.Bg, t.Fg = nativeBg, nativeText
	t.ContrastBg, t.ContrastFg = nativeInk, nativeSurface
	u := &nativeUI{owner: owner, invalidate: invalidate, theme: t, updates: make(chan func(), 128), editors: map[string]*widget.Editor{}, buttons: map[string]*widget.Clickable{}, checks: map[string]*widget.Bool{}, lists: map[string]*widget.List{}, expanded: map[string]bool{}, busy: map[string]bool{}, state: map[string]any{}, client: "codex", page: "agents", traceStage: "request"}
	owner.mu.Lock()
	u.language = owner.config.Language
	u.setValue("connection.org", owner.config.OrgID)
	u.setValue("connection.port", strconv.Itoa(owner.config.Port))
	catalog := readNativeCachedCatalog(owner.dir, owner.catalogScopeLocked())
	u.models, u.catalogFetchedAt = catalog.Models, catalog.FetchedAt
	u.catalogCached = len(u.models) > 0
	u.setChecked("connection.remember", nativeRememberDefault(owner.config.Remember, owner.apiKey != ""))
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
	return u.textStyle(13, s, nativeTextMuted, font.Normal)
}

// heading is the section title (Geist); Geist Mono is reserved for page titles,
// eyebrows, disclosures and status so each typeface has one job.
func (u *nativeUI) heading(s string) layout.Widget {
	return u.textStyle(18, s, nativeText, font.SemiBold)
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
		inline := kind == nativeButtonGhost || kind == nativeButtonLink || kind == nativeButtonDisclosure
		expanded := false
		if kind == nativeButtonDisclosure {
			// For a disclosure "selected" means expanded; it never takes the selection colour.
			expanded, selected = selected, false
		}
		background, foreground, border, borderWidth := nativeSurface, nativeInk, nativeBorderStrong, nativeLine
		switch kind {
		case nativeButtonPrimary:
			background, foreground, border = nativeAccent, nativeSurface, nativeAccent
		case nativeButtonDanger:
			foreground, border = nativeErrorFG, nativeErrorLine
		}
		if inline {
			background, border = color.NRGBA{}, color.NRGBA{}
		}
		if selected {
			background, foreground, border, borderWidth = nativeSelected, nativeInk, nativeInk, nativeLineHeavy
		}
		if !gtx.Enabled() {
			foreground = nativeTextDisabled
			if !inline {
				background, border = nativeDisabledBG, color.NRGBA{}
			}
		} else if b.Hovered() {
			switch {
			case kind == nativeButtonPrimary:
				background = nativeAccentHover
			case kind == nativeButtonDanger:
				background = nativeErrorBG
			case inline:
				background = nativeIvory
			case selected:
				background = nativeSelected
			default:
				background = nativeSurfaceAlt
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
		if inline {
			inset.Left, inset.Right = 4, 4
		}
		gtx.Constraints.Min.Y = max(gtx.Constraints.Min.Y, gtx.Dp(36))
		text := u.textStyle(13, label, foreground, weight)
		if kind == nativeButtonDisclosure {
			sign := "+ "
			if expanded {
				sign = "− "
			}
			// Short labels read as brutalist tags; long phrases stay legible in Geist.
			if utf8.RuneCountInString(label) <= 16 {
				text = u.monoStyle(13, sign+strings.ToUpper(label), foreground, font.Bold)
			} else {
				text = u.textStyle(13, sign+label, foreground, font.SemiBold)
			}
		}
		if inline {
			text = nativeUnderlined(text, foreground)
		}
		buttonBackground := material.ButtonLayout(u.theme, b)
		buttonBackground.Background, buttonBackground.CornerRadius = background, 0
		body := func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: border, Width: borderWidth}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return buttonBackground.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					semantic.LabelOp(label).Add(gtx.Ops)
					if selected || expanded {
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
						if iconAfter {
							return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, layout.Rigid(text), layout.Rigid(iconGap), layout.Rigid(iconWidget))
						}
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, layout.Rigid(iconWidget), layout.Rigid(iconGap), layout.Rigid(text))
					})
				})
			})
		}
		if kind == nativeButtonPrimary && gtx.Enabled() && !selected {
			// The hard shadow marks the one action that matters on a surface.
			return nativeHardShadowSized(gtx, nativePrimaryShadow, body)
		}
		return body(gtx)
	}
}

// nativeUnderlined draws a permanent 1dp underline so inline controls read as
// clickable before any hover.
func nativeUnderlined(content layout.Widget, c color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		dims := content(gtx)
		y := dims.Size.Y + gtx.Dp(2)
		paint.FillShape(gtx.Ops, c, clip.Rect{Min: image.Pt(0, y), Max: image.Pt(dims.Size.X, y+gtx.Dp(1))}.Op())
		dims.Size.Y += gtx.Dp(3)
		return dims
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
			background = nativeDisabledBG
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

// card groups the controls for one thing (an agent, a notice). It is flat:
// the hard shadow is reserved for primary actions.
func (u *nativeUI) card(children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return nativeBox(gtx, nativeSurface, func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: nativeBorder, Width: nativeLine}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(nativeSpace20).Layout(gtx, u.column(children...))
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
			style.Font.Typeface = nativeMonoFace
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
	changesChatGPT := method == "POST" && strings.HasPrefix(path, "/api/chatgpt/")
	if changesChatGPT {
		u.chatGPTRevision++
	}
	changesProxy := method == "POST" && (path == "/api/start" || path == "/api/stop")
	if changesProxy {
		u.proxyRevision++
	}
	go func() {
		raw, err := nativeRequest(u.owner, method, path, payload)
		u.enqueue(func() {
			delete(u.busy, key)
			if changesChatGPT {
				u.chatGPTRevision++
			}
			if changesProxy {
				u.proxyRevision++
			}
			if err != nil {
				if method == "POST" && path == "/api/chatgpt/login" {
					u.chatGPTLoginRequested = false
				}
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
	chatGPTRevision, chatGPTSaving := u.chatGPTRevision, u.chatGPTRequestBusy()
	proxyRevision, proxySaving := u.proxyRevision, u.busy["POST/api/start"] || u.busy["POST/api/stop"]
	updateRevision, updateSaving := u.updateRevision, u.busy["POST/api/updates"] || u.busy["POST/api/updates/install"]
	c := u.clientState()
	desktopRevision, desktopSaving := c.DesktopExperimentalRevision, c.DesktopExperimentalTarget != nil
	u.call("GET", "/api/state", nil, func(raw json.RawMessage) {
		// A snapshot that overlaps Start or Stop can predate the completed
		// operation. Discard it before applying any state-derived effects;
		// the next poll will fetch a coherent snapshot, including after errors.
		if proxySaving || proxyRevision != u.proxyRevision || u.busy["POST/api/start"] || u.busy["POST/api/stop"] {
			return
		}
		if chatGPTSaving || chatGPTRevision != u.chatGPTRevision || u.chatGPTRequestBusy() {
			return
		}
		desktopExperimental := u.claudeDesktopExperimentalModels()
		update := u.state["update"]
		u.acceptState(raw)
		// A poll captured before a manual check must not restore its old status.
		if updateSaving || updateRevision != u.updateRevision || u.busy["POST/api/updates"] || u.busy["POST/api/updates/install"] {
			u.state["update"] = update
		} else if u.updateRequestFailed {
			var previous releaseUpdateState
			nativeDecode(update, &previous)
			current := u.releaseUpdate()
			if current.Checking || current.CheckedAt != "" && current.CheckedAt != previous.CheckedAt {
				u.updateRequestFailed = false
			}
		}
		if !updateSaving && updateRevision == u.updateRevision && !u.busy["POST/api/updates"] && !u.busy["POST/api/updates/install"] {
			current := u.releaseUpdate()
			if u.packageUpdateStarted && !current.Installing && packageUpdateFailureCode(current.InstallMessage) {
				u.packageUpdateStarted, u.packageUpdateFailed = false, true
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
	if u.chatGPTLoginRequested {
		account := nativeMap(state["chatgpt"])
		if nativeString(account, "status") == "pending" && nativeString(account, "verificationUrl") == nativeChatGPTVerificationURL {
			u.chatGPTLoginRequested = false
			u.openChatGPTAuthorization()
		} else if nativeString(account, "status") == "error" || nativeBool(account, "connected") {
			u.chatGPTLoginRequested = false
		}
	}

	if !first && oldEpoch != nativeNumber(state, "activityEpoch") {
		u.traceGeneration++
		u.trace = nil
	}
	if !first && oldRevision != nativeNumber(state, "catalogRevision") {
		u.models = nil
		u.catalogFetchedAt = time.Time{}
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
		u.setChecked("connection.remember", nativeRememberDefault(nativeBool(state, "remember"), nativeBool(state, "hasKey")))
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
		Complete *bool       `json:"complete"`
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
	currentRevision, org := u.owner.catalogRevision, u.owner.catalogScopeLocked()
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
	// Missing flags preserve compatibility with older local API responses.
	// Partial success must also invalidate a previously fresh catalog's date.
	u.catalogFetchedAt = time.Time{}
	if response.Complete == nil || *response.Complete {
		u.catalogFetchedAt = time.Now().UTC()
	}
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

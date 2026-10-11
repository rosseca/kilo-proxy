//go:build desktop

package main

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// Settings is one scrollable page: a sticky section index on the left and a
// bordered block per topic. Every change saves itself; connection changes
// briefly stop and restart a running proxy because the local API only edits a
// stopped connection.

var (
	uxsRowTint   = nativeColor(0xfafaf6)
	uxsIndexRule = nativeColor(0xdddddd)
)

// uxsSettingsState keeps page-local view state out of the shared UI struct.
type uxsSettingsState struct {
	blockY     map[string]int
	jump       string
	jumpTarget string
	// verify is a jump applied from earlier block positions; layout re-checks
	// verifyY against the fresh positions.
	verify  string
	verifyY int
	focused map[string]bool
	// resume restarts the proxy once a connection change it paused has settled.
	resume bool
	// autoSaved remembers the connection already auto-saved after a login so a
	// failed save never loops.
	autoSaved string
}

type uxsSection struct{ id, en, es string }

var uxsSections = []uxsSection{
	{"accounts", "Accounts", "Cuentas"},
	{"connection", "Connection", "Conexión"},
	{"preferences", "Preferences", "Preferencias"},
	{"images", "Large images", "Imágenes grandes"},
	{"terminal", "Terminal commands", "Comandos de terminal"},
	{"updates", "Updates", "Actualizaciones"},
}

// uxsLegacyTabs maps the former tab names, still used by links elsewhere, to blocks.
var uxsLegacyTabs = map[string]string{"account": "accounts", "accounts": "accounts", "connection": "connection", "images": "images", "preferences": "preferences", "terminal": "terminal", "updates": "updates"}

func (u *nativeUI) uxs() *uxsSettingsState {
	if u.uxSettings == nil {
		u.uxSettings = &uxsSettingsState{blockY: map[string]int{}, focused: map[string]bool{}}
	}
	return u.uxSettings
}

// uxsPageLead replaces the page description on Settings.
func (u *nativeUI) uxsPageLead() ([2]string, bool) {
	if u.page != "settings" && u.page != "connection" {
		return [2]string{}, false
	}
	return [2]string{"Everything saves automatically. None of this is needed to get started.", "Todo se guarda solo. Nada de esto hace falta para empezar."}, true
}

// uxsBlock is a bordered box with an ivory header (title + muted subtitle) and rows.
func (u *nativeUI) uxsBlock(title, subtitle string, rows ...layout.Widget) layout.Widget {
	header := func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return nativeBox(gtx, nativeIvory, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Top: 14, Bottom: 14, Left: 18, Right: 18}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(u.textStyle(16, title, nativeText, font.SemiBold)),
					layout.Rigid(layout.Spacer{Width: 16}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						if subtitle == "" {
							return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
						}
						return layout.E.Layout(gtx, u.textStyle(13, subtitle, nativeTextSubtle, font.Normal))
					}),
				)
			})
		})
	}
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return nativeBox(gtx, nativeSurface, func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: nativeInk, Width: nativeLine}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				children := []layout.FlexChild{layout.Rigid(header), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return nativeRule(gtx, nativeLine) })}
				for i, row := range rows {
					if i > 0 {
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return nativeRule(gtx, 1) }))
					}
					children = append(children, layout.Rigid(row))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
			})
		})
	}
}

// uxsLabelWidth is the left label column; narrow blocks stack the row instead.
func uxsLabelWidth(gtx layout.Context) int {
	if gtx.Constraints.Max.X < gtx.Dp(760) {
		return gtx.Dp(170)
	}
	return gtx.Dp(200)
}

func uxsStacked(gtx layout.Context) bool { return gtx.Constraints.Max.X < gtx.Dp(600) }

// uxsRow lays out [label + sublabel | value | actions] and an optional tinted
// area below aligned with the value column.
func (u *nativeUI) uxsRow(label, sublabel string, value, actions, below layout.Widget) layout.Widget {
	labelWidget := func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{layout.Rigid(u.textStyle(14, label, nativeText, font.SemiBold))}
		if sublabel != "" {
			children = append(children, layout.Rigid(layout.Spacer{Height: 2}.Layout), layout.Rigid(u.textStyle(12, sublabel, nativeTextSubtle, font.Normal)))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}
	main := func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Top: 16, Bottom: 16, Left: 18, Right: 18}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			if uxsStacked(gtx) {
				parts := []layout.Widget{labelWidget}
				if value != nil {
					parts = append(parts, value)
				}
				if actions != nil {
					parts = append(parts, actions)
				}
				return u.column(parts...)(gtx)
			}
			width := uxsLabelWidth(gtx)
			children := []layout.FlexChild{
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X, gtx.Constraints.Max.X = width, width
					return layout.Inset{Right: 16}.Layout(gtx, labelWidget)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if value == nil {
						return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
					}
					return value(gtx)
				}),
			}
			if actions != nil {
				// Long action labels (a team name, say) must never squeeze the value
				// column into a one-letter-per-line strip.
				children = append(children, layout.Rigid(layout.Spacer{Width: 16}.Layout), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Max.X = max(0, gtx.Constraints.Max.X-gtx.Dp(200))
					gtx.Constraints.Min.X = min(gtx.Constraints.Min.X, gtx.Constraints.Max.X)
					return actions(gtx)
				}))
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
		})
	}
	if below == nil {
		return main
	}
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(main),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return nativeRule(gtx, 1) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return nativeBox(gtx, uxsRowTint, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					left := unit.Dp(18)
					if !uxsStacked(gtx) {
						left = unit.Dp(float32(uxsLabelWidth(gtx))/gtx.Metric.PxPerDp) + 18
					}
					return layout.Inset{Top: 16, Bottom: 18, Left: left, Right: 18}.Layout(gtx, below)
				})
			}),
		)
	}
}

// uxsValue stacks the current value over small explanatory lines.
func (u *nativeUI) uxsValue(main layout.Widget, small ...string) layout.Widget {
	children := []layout.FlexChild{layout.Rigid(main)}
	for _, line := range small {
		if line == "" {
			continue
		}
		children = append(children, layout.Rigid(layout.Spacer{Height: 3}.Layout), layout.Rigid(u.textStyle(12, line, nativeTextSubtle, font.Normal)))
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}
}

// uxsStatus renders "● Label · detail": the tone colours the mark and the
// label, the detail stays in ink. The mark always accompanies colour.
func (u *nativeUI) uxsStatus(tone nativeTone, text, detail string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		fg := nativeText
		switch tone {
		case nativeToneSuccess:
			fg = nativeSuccessFG
		case nativeToneWarning:
			fg = nativeWarningFG
		case nativeToneError:
			fg = nativeErrorFG
		case nativeToneInfo:
			fg = nativeInfoFG
		case nativeToneNeutral:
			fg = nativeTextMuted
		}
		mark := func(gtx layout.Context) layout.Dimensions {
			sz := gtx.Dp(10)
			switch tone {
			case nativeToneSuccess:
				paint.FillShape(gtx.Ops, fg, clip.Ellipse(image.Rectangle{Max: image.Pt(sz, sz)}).Op(gtx.Ops))
			case nativeToneWarning, nativeToneError:
				gtx.Constraints.Min = image.Pt(gtx.Dp(15), gtx.Dp(15))
				return nativeIconWarning.Layout(gtx, fg)
			case nativeToneInfo:
				gtx.Constraints.Min = image.Pt(gtx.Dp(15), gtx.Dp(15))
				return nativeIconInfo.Layout(gtx, fg)
			default:
				paint.FillShape(gtx.Ops, fg, clip.Stroke{Path: clip.Ellipse(image.Rectangle{Min: image.Pt(1, 1), Max: image.Pt(sz-1, sz-1)}).Path(gtx.Ops), Width: float32(gtx.Dp(1.5))}.Op())
			}
			return layout.Dimensions{Size: image.Pt(sz, sz)}
		}
		children := []layout.FlexChild{layout.Rigid(mark), layout.Rigid(layout.Spacer{Width: 7}.Layout)}
		if detail == "" {
			children = append(children, layout.Flexed(1, u.textStyle(15, text, fg, font.SemiBold)))
		} else {
			children = append(children, layout.Rigid(u.textStyle(15, text, fg, font.SemiBold)), layout.Flexed(1, u.textStyle(15, " · "+detail, nativeText, font.Medium)))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	}
}

// uxsPlain is a value line in ink, optionally followed by a coloured badge text.
func (u *nativeUI) uxsPlain(text string) layout.Widget {
	return u.textStyle(15, text, nativeText, font.Medium)
}

// uxsMono is a value set in Geist Mono (addresses, versions).
func (u *nativeUI) uxsMono(text string) layout.Widget {
	return u.monoStyle(14, text, nativeText, font.Bold)
}

// uxsLink is an underlined inline action (Manage / Close / Change / Manual).
func (u *nativeUI) uxsLink(id, label string, action func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = 0
		return u.buttonWidget(id, label, nativeButtonLink, nil, false, false, action)(gtx)
	}
}

// uxsToggleLink flips an expanded area and reads "Close" while open.
func (u *nativeUI) uxsToggleLink(id, closedEN, closedES string) layout.Widget {
	label := u.tr(closedEN, closedES)
	if u.expanded[id] {
		label = u.tr("Close", "Cerrar")
	}
	return u.uxsLink(id, label, func() { u.expanded[id] = !u.expanded[id] })
}

// uxsActions places row actions side by side at their natural width.
func (u *nativeUI) uxsActions(actions ...layout.Widget) layout.Widget {
	present := []layout.Widget{}
	for _, action := range actions {
		if action != nil {
			present = append(present, action)
		}
	}
	return func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{}
		for _, action := range present {
			if len(children) > 0 {
				children = append(children, layout.Rigid(layout.Spacer{Width: 12}.Layout))
			}
			children = append(children, layout.Rigid(action))
		}
		if uxsStacked(gtx) {
			return u.pills(present...)(gtx)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	}
}

// uxsToggle is a square switch: off is white with a black knob on the left,
// on is black with a citrus knob on the right (position is the second cue).
func (u *nativeUI) uxsToggle(id, label, caption string, on bool, change func(bool)) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := u.clickable(id)
		for b.Clicked(gtx) {
			if gtx.Enabled() && change != nil {
				change(!on)
				on = !on
			}
		}
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(semantic.Switch).Add(gtx.Ops)
			semantic.LabelOp(label).Add(gtx.Ops)
			semantic.SelectedOp(on).Add(gtx.Ops)
			semantic.EnabledOp(gtx.Enabled()).Add(gtx.Ops)
			track := func(gtx layout.Context) layout.Dimensions {
				size := image.Pt(gtx.Dp(44), gtx.Dp(24))
				bg, knob, border := nativeSurface, nativeInk, nativeInk
				if on {
					bg, knob = nativeInk, nativeCitrus
				}
				if !gtx.Enabled() {
					bg, border = nativeDisabledBG, nativeTextDisabled
					if on {
						knob = nativeTextDisabled
					}
				}
				paint.FillShape(gtx.Ops, bg, clip.Rect{Max: size}.Op())
				line := gtx.Dp(2)
				paint.FillShape(gtx.Ops, border, clip.Stroke{Path: clip.Rect{Min: image.Pt(line/2, line/2), Max: size.Sub(image.Pt(line/2, line/2))}.Path(), Width: float32(line)}.Op())
				k := gtx.Dp(16)
				x := gtx.Dp(4)
				if on {
					x = size.X - k - gtx.Dp(4)
				}
				paint.FillShape(gtx.Ops, knob, clip.Rect{Min: image.Pt(x, gtx.Dp(4)), Max: image.Pt(x+k, gtx.Dp(4)+k)}.Op())
				return layout.Dimensions{Size: size}
			}
			text := []layout.FlexChild{layout.Rigid(u.textStyle(14, label, nativeText, font.Medium))}
			if caption != "" {
				text = append(text, layout.Rigid(u.textStyle(12, caption, nativeTextSubtle, font.Normal)))
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(track),
				layout.Rigid(layout.Spacer{Width: 12}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, text...)
				}),
			)
		})
	}
}

// uxsRadioList is a bordered single-choice list; the selected row is citrus
// and carries a filled radio mark.
func (u *nativeUI) uxsRadioList(idPrefix string, choices []nativeChoice, selected string, enabled bool, choose func(string)) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return nativeBox(gtx, nativeSurface, func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: nativeInk, Width: nativeLine}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				children := []layout.FlexChild{}
				for i, choice := range choices {
					if i > 0 {
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return nativeRule(gtx, 1) }))
					}
					children = append(children, layout.Rigid(u.uxsRadio(idPrefix+choice.Value, choice, choice.Value == selected, enabled, choose)))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
			})
		})
	}
}

func (u *nativeUI) uxsRadio(id string, choice nativeChoice, selected, enabled bool, choose func(string)) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if !enabled {
			gtx = gtx.Disabled()
		}
		b := u.clickable(id)
		for b.Clicked(gtx) {
			if gtx.Enabled() && choose != nil {
				choose(choice.Value)
			}
		}
		background := nativeSurface
		if selected {
			background = nativeCitrus
		} else if b.Hovered() && gtx.Enabled() {
			background = nativeIvory
		}
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(semantic.RadioButton).Add(gtx.Ops)
			semantic.LabelOp(choice.Label).Add(gtx.Ops)
			semantic.SelectedOp(selected).Add(gtx.Ops)
			return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Inset{Top: 11, Bottom: 11, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					weight := font.Medium
					if selected {
						weight = font.SemiBold
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return uxsRadioMark(gtx, selected, enabled) }),
						layout.Rigid(layout.Spacer{Width: 12}.Layout),
						layout.Rigid(u.textStyle(14, choice.Label, nativeText, weight)),
						layout.Rigid(layout.Spacer{Width: 12}.Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							if choice.Caption == "" {
								return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
							}
							return layout.E.Layout(gtx, u.textStyle(12, choice.Caption, nativeTextSubtle, font.Normal))
						}),
					)
				})
			})
		})
	}
}

func uxsRadioMark(gtx layout.Context, selected, enabled bool) layout.Dimensions {
	size := gtx.Dp(18)
	ink := nativeInk
	if !enabled {
		ink = nativeTextDisabled
	}
	rect := image.Rectangle{Max: image.Pt(size, size)}
	paint.FillShape(gtx.Ops, nativeSurface, clip.Ellipse(rect).Op(gtx.Ops))
	line := gtx.Dp(2)
	paint.FillShape(gtx.Ops, ink, clip.Stroke{Path: clip.Ellipse(image.Rectangle{Min: image.Pt(line/2, line/2), Max: image.Pt(size-line/2, size-line/2)}).Path(gtx.Ops), Width: float32(line)}.Op())
	if selected {
		dot := gtx.Dp(8)
		at := (size - dot) / 2
		paint.FillShape(gtx.Ops, ink, clip.Ellipse(image.Rectangle{Min: image.Pt(at, at), Max: image.Pt(at+dot, at+dot)}).Op(gtx.Ops))
	}
	return layout.Dimensions{Size: image.Pt(size, size)}
}

// uxsSettingsPage lays out every block once, records where each starts and
// draws the index pinned to the top of the visible area.
func (u *nativeUI) uxsSettingsPage() layout.Widget {
	s := u.uxs()
	// Links elsewhere still name a former tab; treat it as a jump target.
	if target := uxsLegacyTabs[u.settingsTab]; target != "" {
		s.jump = target
	}
	u.settingsTab = ""
	// With block positions from an earlier layout, jump before the page list
	// lays out this frame; layout re-checks the position and corrects it.
	if y, ok := s.blockY[s.jump]; ok {
		l := u.list("page." + u.page)
		l.Position.First, l.Position.Offset = 0, y
		s.jumpTarget, s.verify, s.verifyY = s.jump, s.jump, y
		s.jump = ""
	}
	u.uxsSettle()
	type block struct {
		id     string
		widget layout.Widget
	}
	blocks := []block{
		{"accounts", u.uxsAccountsBlock()},
		{"connection", u.uxsConnectionBlock()},
		{"preferences", u.appearancePanel()},
		{"images", u.imageTransportPanel()},
		{"terminal", u.terminalCommandsPanel()},
		{"updates", u.updatesPanel()},
	}
	return func(gtx layout.Context) layout.Dimensions {
		listID := "page." + u.page
		width := gtx.Constraints.Max.X
		indexWidth, gap := 0, 0
		if width >= gtx.Dp(860) {
			indexWidth, gap = gtx.Dp(190), gtx.Dp(30)
		}
		blockWidth := width - indexWidth - gap
		bgtx := gtx
		bgtx.Constraints = layout.Constraints{Min: image.Pt(blockWidth, 0), Max: image.Pt(blockWidth, gtx.Constraints.Max.Y)}
		recording := op.Record(gtx.Ops)
		y := 0
		for i, b := range blocks {
			if i > 0 {
				y += gtx.Dp(20)
			}
			s.blockY[b.id] = y
			at := op.Offset(image.Pt(0, y)).Push(gtx.Ops)
			y += b.widget(bgtx).Size.Y
			at.Pop()
		}
		content := recording.Stop()
		list := u.list(listID)
		if s.verify != "" {
			if s.blockY[s.verify] != s.verifyY {
				s.jump = s.verify
			}
			s.verify = ""
		}
		if s.jump != "" {
			target := s.jump
			s.jump, s.jumpTarget = "", target
			offset := s.blockY[target]
			// Apply after the list finishes this layout, as it rewrites Position.
			u.enqueue(func() {
				if u.page == "settings" || u.page == "connection" {
					l := u.list(listID)
					l.Position.First, l.Position.Offset = 0, offset
				}
			})
		}
		if indexWidth > 0 {
			igtx := gtx
			igtx.Constraints = layout.Constraints{Min: image.Pt(indexWidth, 0), Max: image.Pt(indexWidth, gtx.Constraints.Max.Y)}
			indexRecording := op.Record(gtx.Ops)
			index := u.uxsIndex(u.uxsCurrentSection(gtx, list))(igtx)
			indexCall := indexRecording.Stop()
			sticky := 0
			if list.Position.First == 0 {
				sticky = max(0, min(list.Position.Offset, y-index.Size.Y))
			}
			at := op.Offset(image.Pt(0, sticky)).Push(gtx.Ops)
			indexCall.Add(gtx.Ops)
			at.Pop()
		}
		at := op.Offset(image.Pt(indexWidth+gap, 0)).Push(gtx.Ops)
		content.Add(gtx.Ops)
		at.Pop()
		return layout.Dimensions{Size: image.Pt(width, y)}
	}
}

// uxsCurrentSection is the block at the top of the viewport. At the end of
// the page a lower block can never reach the top, so a jump target wins there.
func (u *nativeUI) uxsCurrentSection(gtx layout.Context, list *widget.List) string {
	s := u.uxs()
	offset := list.Position.Offset
	if list.Position.First > 0 {
		offset = s.blockY["updates"]
	}
	current := uxsSections[0].id
	for _, section := range uxsSections {
		if y, ok := s.blockY[section.id]; ok && y <= offset+gtx.Dp(40) {
			current = section.id
		}
	}
	if s.jumpTarget != "" && !list.Position.BeforeEnd && s.blockY[s.jumpTarget] > s.blockY[current] {
		return s.jumpTarget
	}
	return current
}

func (u *nativeUI) uxsIndex(current string) layout.Widget {
	entries := make([]layout.FlexChild, 0, len(uxsSections))
	for _, section := range uxsSections {
		section := section
		entries = append(entries, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			b := u.clickable("settings.section." + section.id)
			for b.Clicked(gtx) {
				u.uxs().jump = section.id
				if u.invalidate != nil {
					u.invalidate()
				}
			}
			active := section.id == current
			label := u.tr(section.en, section.es)
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				semantic.ClassOp(semantic.Button).Add(gtx.Ops)
				semantic.LabelOp(label).Add(gtx.Ops)
				if active {
					semantic.SelectedOp(true).Add(gtx.Ops)
				}
				background, rule, fg, weight := color.NRGBA{}, uxsIndexRule, nativeTextMuted, font.Normal
				if active {
					background, rule, fg, weight = nativeIvory, nativeInk, nativeInk, font.SemiBold
				} else if b.Hovered() {
					background = nativeIvory
				}
				return layout.Stack{}.Layout(gtx,
					layout.Expanded(func(gtx layout.Context) layout.Dimensions {
						paint.FillShape(gtx.Ops, background, clip.Rect{Max: gtx.Constraints.Min}.Op())
						paint.FillShape(gtx.Ops, rule, clip.Rect{Max: image.Pt(gtx.Dp(3), gtx.Constraints.Min.Y)}.Op())
						return layout.Dimensions{Size: gtx.Constraints.Min}
					}),
					layout.Stacked(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.Inset{Top: 9, Bottom: 9, Left: 15, Right: 8}.Layout(gtx, u.textStyle(14, label, fg, weight))
					}),
				)
			})
		}))
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, entries...)
	}
}

// uxsAgo describes how long ago an RFC 3339 time was, e.g. "5 min ago" /
// "hace 5 min". It returns "" for an unparsable value.
func (u *nativeUI) uxsAgo(value string, now time.Time) string {
	when, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return ""
	}
	d := now.Sub(when)
	switch {
	case d < time.Minute:
		return u.tr("just now", "hace un momento")
	case d < time.Hour:
		n := int(d / time.Minute)
		return fmt.Sprintf(u.tr("%d min ago", "hace %d min"), n)
	case d < 48*time.Hour:
		n := int(d / time.Hour)
		return fmt.Sprintf(u.tr("%d h ago", "hace %d h"), n)
	}
	return fmt.Sprintf(u.tr("%d days ago", "hace %d días"), int(d/(24*time.Hour)))
}

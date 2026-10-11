//go:build desktop

package main

import (
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

var (
	uxaGridLine = nativeColor(0xc9ccc3)
	uxaWritten  = nativeColor(0x8d9288)
)

func uxaFill(gtx layout.Context, c color.NRGBA, r image.Rectangle) {
	paint.FillShape(gtx.Ops, c, clip.Rect(r).Op())
}

// uxaRecord lays out w without minimum constraints and returns it for replay.
func uxaRecord(gtx layout.Context, w layout.Widget) (op.CallOp, layout.Dimensions) {
	gtx.Constraints.Min = image.Point{}
	m := op.Record(gtx.Ops)
	dims := w(gtx)
	return m.Stop(), dims
}

func uxaPlace(gtx layout.Context, call op.CallOp, at image.Point) {
	t := op.Offset(at).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
}

// uxaText is a single-line label truncated with an ellipsis.
func (u *nativeUI) uxaText(size unit.Sp, s string, c color.NRGBA, weight font.Weight, mono bool, alignment text.Alignment) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		l := material.Label(u.theme, size, s)
		l.Color, l.Font.Weight, l.MaxLines, l.Alignment = c, weight, 1, alignment
		if mono {
			l.Font.Typeface = nativeMonoFace
		}
		return l.Layout(gtx)
	}
}

// uxaChoice is a black-when-selected button used by the period selector and tabs.
func (u *nativeUI) uxaChoice(id, label string, selected, bordered bool, choose func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := u.clickable(id)
		for b.Clicked(gtx) {
			if choose != nil {
				choose()
			}
		}
		background, foreground := nativeSurface, nativeInk
		if selected {
			background, foreground = nativeInk, nativeSurface
		} else if b.Hovered() {
			background = nativeSurfaceAlt
		}
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(semantic.Button).Add(gtx.Ops)
			semantic.LabelOp(label).Add(gtx.Ops)
			if selected {
				semantic.SelectedOp(true).Add(gtx.Ops)
			}
			weight := font.Medium
			if selected {
				weight = font.SemiBold
			}
			return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
				content := layout.Inset{Top: 9, Bottom: 9, Left: 14, Right: 14}
				if !bordered {
					return content.Layout(gtx, u.textStyle(14, label, foreground, weight))
				}
				return widget.Border{Color: nativeInk, Width: nativeLine}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return content.Layout(gtx, u.textStyle(14, label, foreground, weight))
				})
			})
		})
	}
}

// uxaPeriodSelector is one joined segmented control: Today / 7 / 14 / 30 days.
func (u *nativeUI) uxaPeriodSelector() layout.Widget {
	s := u.uxa()
	choices := []nativeChoice{{Value: "today", Label: u.tr("Today", "Hoy")}, {Value: "7", Label: u.tr("7 days", "7 días")}, {Value: "14", Label: u.tr("14 days", "14 días")}, {Value: "30", Label: u.tr("30 days", "30 días")}}
	return func(gtx layout.Context) layout.Dimensions {
		// One ink frame with 2dp separators: the buttons sit in the gaps of an ink fill.
		line := gtx.Dp(nativeLine)
		calls, widths, height := []op.CallOp{}, []int{}, 0
		for _, choice := range choices {
			choice := choice
			call, dims := uxaRecord(gtx, u.uxaChoice("activity.period."+choice.Value, choice.Label, s.period == choice.Value, false, func() { s.period, s.pickedDay = choice.Value, "" }))
			calls, widths = append(calls, call), append(widths, dims.Size.X)
			height = max(height, dims.Size.Y)
		}
		total := line
		for _, w := range widths {
			total += w + line
		}
		uxaFill(gtx, nativeInk, image.Rect(0, 0, total, height+2*line))
		x := line
		for i, call := range calls {
			uxaPlace(gtx, call, image.Pt(x, line))
			x += widths[i] + line
		}
		return layout.Dimensions{Size: image.Pt(total, height+2*line)}
	}
}

// uxaTabBar shows the Activity tabs over a full-width ink rule, with an optional trailing action.
func (u *nativeUI) uxaTabBar(trailing layout.Widget) layout.Widget {
	s := u.uxa()
	choices := []nativeChoice{{Value: "summary", Label: u.tr("Overview", "Resumen")}, {Value: "requests", Label: u.tr("Requests", "Peticiones")}, {Value: "conversations", Label: u.tr("Conversations", "Conversaciones")}}
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		dims := layout.Flex{Axis: layout.Horizontal, Alignment: layout.End}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				x, height := 0, 0
				for _, choice := range choices {
					choice := choice
					call, dims := uxaRecord(gtx, u.uxaChoice("activity.tab."+choice.Value, choice.Label, s.tab == choice.Value, false, func() { s.tab = choice.Value }))
					uxaPlace(gtx, call, image.Pt(x, 0))
					x += dims.Size.X
					height = max(height, dims.Size.Y)
				}
				return layout.Dimensions{Size: image.Pt(x, height)}
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if trailing == nil {
					return layout.Dimensions{}
				}
				return layout.Inset{Bottom: 4}.Layout(gtx, trailing)
			}),
		)
		uxaFill(gtx, nativeInk, image.Rect(0, dims.Size.Y, dims.Size.X, dims.Size.Y+gtx.Dp(nativeLine)))
		dims.Size.Y += gtx.Dp(nativeLine)
		return dims
	}
}

// uxaGrid lays cells in equal columns separated by 2dp ink rules, inside a 2dp ink frame.
func uxaGrid(columns int, cells ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		line := gtx.Dp(nativeLine)
		width := gtx.Constraints.Max.X
		cellWidth := max(1, (width-line*(columns+1))/columns)
		type placed struct {
			call op.CallOp
			at   image.Point
		}
		var items []placed
		var rects []image.Rectangle
		y := line
		for start := 0; start < len(cells); start += columns {
			end := min(start+columns, len(cells))
			calls := make([]op.CallOp, 0, end-start)
			height := 0
			for _, cell := range cells[start:end] {
				cgtx := gtx
				cgtx.Constraints = layout.Exact(image.Pt(cellWidth, 0))
				cgtx.Constraints.Max.Y = gtx.Constraints.Max.Y
				m := op.Record(gtx.Ops)
				dims := cell(cgtx)
				calls = append(calls, m.Stop())
				height = max(height, dims.Size.Y)
			}
			for i, call := range calls {
				x := line + i*(cellWidth+line)
				items = append(items, placed{call, image.Pt(x, y)})
				rects = append(rects, image.Rect(x, y, x+cellWidth, y+height))
			}
			y += height + line
		}
		total := image.Pt(line+columns*(cellWidth+line), y)
		uxaFill(gtx, nativeInk, image.Rectangle{Max: total})
		for _, r := range rects {
			uxaFill(gtx, nativeSurface, r)
		}
		for _, item := range items {
			uxaPlace(gtx, item.call, item.at)
		}
		return layout.Dimensions{Size: total}
	}
}

// uxaKPI is one tile of the summary row: mono eyebrow, big mono number, caption.
func (u *nativeUI) uxaKPI(label, value string, caption layout.Widget) layout.Widget {
	valueColor := nativeText
	if value == "—" {
		valueColor = nativeTextDisabled
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 18, Bottom: 18, Left: 18, Right: 18}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			children := []layout.Widget{u.eyebrow(label), u.monoStyle(30, value, valueColor, font.Bold)}
			if caption != nil {
				children = append(children, caption)
			}
			return u.column(children...)(gtx)
		})
	}
}

// uxaPanel is a bordered white surface with a title and a subtle subtitle.
func (u *nativeUI) uxaPanel(title, subtitle string, trailing layout.Widget, children ...layout.Widget) layout.Widget {
	head := func(gtx layout.Context) layout.Dimensions {
		titles := []layout.Widget{u.heading(title)}
		if subtitle != "" {
			titles = append(titles, u.textStyle(13, subtitle, nativeTextSubtle, font.Normal))
		}
		if trailing == nil {
			return u.column(titles...)(gtx)
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx, layout.Flexed(1, u.column(titles...)), layout.Rigid(trailing))
	}
	return u.card(append([]layout.Widget{head}, children...)...)
}

// uxaDashedLine paints a recessive horizontal gridline.
func uxaDashedLine(gtx layout.Context, x0, x1, y int) {
	dash, gap := gtx.Dp(4), gtx.Dp(4)
	for x := x0; x < x1; x += dash + gap {
		uxaFill(gtx, uxaGridLine, image.Rect(x, y, min(x+dash, x1), y+max(1, gtx.Dp(1))))
	}
}

// uxaBarChart draws daily spend bars on one axis. Today's bar is citrus with an
// ink outline and the "Today" label; hovering or clicking a bar shows its
// value in a tooltip, which defaults to the pinned day or today.
func (u *nativeUI) uxaBarChart(days []uxaDay, tooltip func(uxaDay) string) layout.Widget {
	s := u.uxa()
	return func(gtx layout.Context) layout.Dimensions {
		width := gtx.Constraints.Max.X
		tipHeight, plotHeight, labelHeight := gtx.Dp(44), gtx.Dp(170), gtx.Dp(26)
		margin := gtx.Dp(44) // right margin for the gridline labels
		n := max(1, len(days))
		gap := gtx.Dp(2)
		plotWidth := max(n, width-margin)
		barWidth := max(1, (plotWidth-gap*(n-1))/n)
		var peak int64
		for _, d := range days {
			if d.CostKnown {
				peak = max(peak, d.Cost)
			}
		}
		top := uxaNiceCeil(peak)
		base := tipHeight + plotHeight
		for _, level := range []int64{top / 2, top} {
			y := base - int(float64(level)/float64(top)*float64(plotHeight))
			uxaDashedLine(gtx, 0, width, y)
			call, dims := uxaRecord(gtx, u.textStyle(11, nativeMoney(dollars(level)), nativeTextSubtle, font.Normal))
			// Below the line, so the tooltip above the plot never hides the top label.
			uxaPlace(gtx, call, image.Pt(width-dims.Size.X, y+gtx.Dp(3)))
		}
		active := -1
		for i, d := range days {
			b := u.clickable("activity.bar." + d.Date)
			if b.Hovered() {
				active = i
			}
		}
		if active < 0 {
			for i, d := range days {
				if d.Date == s.pickedDay {
					active = i
				}
			}
		}
		if active < 0 {
			active = len(days) - 1
		}
		stride := max(1, (gtx.Dp(28)+barWidth+gap-1)/(barWidth+gap))
		for i, d := range days {
			x := i * (barWidth + gap)
			if d.CostKnown && d.Cost > 0 {
				height := max(gtx.Dp(2), int(float64(d.Cost)/float64(top)*float64(plotHeight)))
				r := image.Rect(x, base-height, x+barWidth, base)
				if d.Today {
					uxaFill(gtx, nativeInk, r)
					line := gtx.Dp(nativeLine)
					uxaFill(gtx, nativeCitrus, image.Rect(r.Min.X+line, r.Min.Y+line, r.Max.X-line, r.Max.Y))
				} else {
					uxaFill(gtx, nativeInk, r)
				}
			} else if !d.Known {
				uxaDashedLine(gtx, x, x+barWidth, base-gtx.Dp(3))
			}
			// Hover and click target: the whole column above the axis.
			b := u.clickable("activity.bar." + d.Date)
			for b.Clicked(gtx) {
				s.pickedDay = d.Date
			}
			label := tooltip(d)
			t := op.Offset(image.Pt(x, tipHeight)).Push(gtx.Ops)
			cgtx := gtx
			cgtx.Constraints = layout.Exact(image.Pt(barWidth, plotHeight))
			b.Layout(cgtx, func(gtx layout.Context) layout.Dimensions {
				semantic.ClassOp(semantic.Button).Add(gtx.Ops)
				semantic.DescriptionOp(label).Add(gtx.Ops)
				return layout.Dimensions{Size: gtx.Constraints.Min}
			})
			t.Pop()
			if d.Today || (len(days)-1-i)%stride == 0 {
				text := d.Date[len(d.Date)-2:]
				if text[0] == '0' {
					text = text[1:]
				}
				weight := font.Normal
				if d.Today {
					text, weight = u.tr("Today", "Hoy"), font.SemiBold
				}
				call, dims := uxaRecord(gtx, u.textStyle(12, text, nativeTextMuted, weight))
				uxaPlace(gtx, call, image.Pt(x+(barWidth-dims.Size.X)/2, base+gtx.Dp(6)))
			}
		}
		uxaFill(gtx, nativeInk, image.Rect(0, base, plotWidth, base+gtx.Dp(nativeLine)))
		if active >= 0 && active < len(days) {
			text := tooltip(days[active])
			call, dims := uxaRecord(gtx, func(gtx layout.Context) layout.Dimensions {
				return nativeBox(gtx, nativeInk, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: 7, Bottom: 7, Left: 12, Right: 12}.Layout(gtx, u.textStyle(13, text, nativeSurface, font.Medium))
				})
			})
			center := active*(barWidth+gap) + barWidth/2
			x := min(max(0, center-dims.Size.X/2), max(0, width-dims.Size.X))
			uxaPlace(gtx, call, image.Pt(x, max(0, tipHeight-dims.Size.Y-gtx.Dp(6))))
		}
		return layout.Dimensions{Size: image.Pt(width, base+labelHeight)}
	}
}

// uxaHorizontalBars draws "Name ▇▇▇▇ $12.30" rows scaled to the largest value.
func (u *nativeUI) uxaHorizontalBars(rows []uxaModelSpend) layout.Widget {
	var peak int64
	for _, row := range rows {
		peak = max(peak, row.Cost)
	}
	children := make([]layout.Widget, 0, len(rows))
	for _, row := range rows {
		row := row
		children = append(children, func(gtx layout.Context) layout.Dimensions {
			nameWidth := min(gtx.Dp(200), gtx.Constraints.Max.X/3)
			valueWidth := gtx.Dp(90)
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(image.Pt(nameWidth, gtx.Dp(22)))
					return u.uxaText(14, row.Name, nativeText, font.Normal, false, text.Start)(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					w := gtx.Constraints.Max.X
					h := gtx.Dp(14)
					if peak > 0 && row.Cost > 0 {
						uxaFill(gtx, nativeInk, image.Rect(0, 0, max(gtx.Dp(2), int(float64(row.Cost)/float64(peak)*float64(w))), h))
					}
					return layout.Dimensions{Size: image.Pt(w, h)}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(image.Pt(valueWidth, gtx.Dp(22)))
					return u.uxaText(14, nativeMoneyShort(dollars(row.Cost)), nativeText, font.Bold, true, text.End)(gtx)
				}),
			)
		})
	}
	return u.column(children...)
}

// uxaStackedBar shows cache reads, cache writes and uncached input in one framed bar.
func uxaStackedBar(read, written, uncached int64) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		width, height, line := gtx.Constraints.Max.X, gtx.Dp(22), gtx.Dp(nativeLine)
		uxaFill(gtx, nativeInk, image.Rect(0, 0, width, height))
		inner := image.Rect(line, line, width-line, height-line)
		uxaFill(gtx, nativeSurface, inner)
		total := read + written + uncached
		if total > 0 {
			x := inner.Min.X
			readWidth := int(float64(read) / float64(total) * float64(inner.Dx()))
			writeWidth := int(float64(written) / float64(total) * float64(inner.Dx()))
			uxaFill(gtx, nativeInk, image.Rect(x, inner.Min.Y, x+readWidth, inner.Max.Y))
			x += readWidth
			uxaFill(gtx, uxaWritten, image.Rect(x, inner.Min.Y, x+writeWidth, inner.Max.Y))
		}
		return layout.Dimensions{Size: image.Pt(width, height)}
	}
}

// uxaLegend is one "■ label" legend entry.
func (u *nativeUI) uxaLegend(swatch color.NRGBA, outlined bool, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				size := gtx.Dp(11)
				if outlined {
					uxaFill(gtx, nativeInk, image.Rect(0, 0, size, size))
					inset := gtx.Dp(1.5)
					uxaFill(gtx, nativeSurface, image.Rect(inset, inset, size-inset, size-inset))
				} else {
					uxaFill(gtx, swatch, image.Rect(0, 0, size, size))
				}
				return layout.Dimensions{Size: image.Pt(size, size)}
			}),
			layout.Rigid(layout.Spacer{Width: 6}.Layout),
			layout.Rigid(u.textStyle(13, label, nativeTextMuted, font.Normal)),
		)
	}
}

// uxaSwitch is the capture toggle. It drives the same widget.Bool as the old
// checkbox so the saving/rollback logic is unchanged. On means ink track and a
// citrus knob on the right (position is the second cue).
func (u *nativeUI) uxaSwitch(id, label string, changed func(bool)) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		u.checked(id)
		w := u.checks[id]
		if w.Update(gtx) && changed != nil {
			changed(w.Value)
		}
		return w.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(semantic.Switch).Add(gtx.Ops)
			semantic.LabelOp(label).Add(gtx.Ops)
			width, height, line := gtx.Dp(52), gtx.Dp(28), gtx.Dp(nativeLine)
			track, knob := nativeSurface, nativeInk
			if !gtx.Enabled() {
				track = nativeDisabledBG
			}
			if w.Value {
				track, knob = nativeInk, nativeCitrus
			}
			uxaFill(gtx, nativeInk, image.Rect(0, 0, width, height))
			uxaFill(gtx, track, image.Rect(line, line, width-line, height-line))
			size := height - 4*line
			x := 2 * line
			if w.Value {
				x = width - 2*line - size
			}
			uxaFill(gtx, knob, image.Rect(x, 2*line, x+size, 2*line+size))
			return layout.Dimensions{Size: image.Pt(width, height)}
		})
	}
}

// uxaInlineLink is a compact underlined text link that sits on a caption's
// baseline, unlike the 36dp link button.
func (u *nativeUI) uxaInlineLink(id, label string, size unit.Sp, open func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := u.clickable(id)
		for b.Clicked(gtx) {
			open()
		}
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(semantic.Button).Add(gtx.Ops)
			semantic.LabelOp(label).Add(gtx.Ops)
			call, dims := uxaRecord(gtx, u.textStyle(size, label, nativeInk, font.SemiBold))
			call.Add(gtx.Ops)
			thickness := max(1, gtx.Dp(1))
			if b.Hovered() {
				thickness = gtx.Dp(nativeLine)
			}
			uxaFill(gtx, nativeInk, image.Rect(0, dims.Size.Y-thickness, dims.Size.X, dims.Size.Y))
			return dims
		})
	}
}

func uxaRectBelow(dims layout.Dimensions, thickness int) image.Rectangle {
	return image.Rect(0, dims.Size.Y, dims.Size.X, dims.Size.Y+thickness)
}

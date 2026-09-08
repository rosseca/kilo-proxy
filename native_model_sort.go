//go:build desktop

package main

import (
	"image"
	"strconv"

	gioevent "gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"
)

func (u *nativeUI) modelSortLabel(order string) string {
	switch modelSortOrder(order) {
	case "codingIndex":
		return u.tr("Coding Index", "Índice de código")
	case "speed":
		return u.tr("Speed", "Velocidad")
	case "price":
		return u.tr("Price", "Precio")
	case "name":
		return u.tr("Name", "Nombre")
	default:
		return u.tr("Code Mode Rank", "Ranking de código")
	}
}

func (u *nativeUI) modelSortButton(gtx layout.Context) layout.Dimensions {
	const id = "models.sort"
	current := modelSortOrder(u.value(id))
	drawing := op.Record(gtx.Ops)
	dims := u.button(id+".toggle", u.modelSortLabel(current)+"  ▾", func() {
		u.expanded[id] = !u.expanded[id]
	})(gtx)
	if u.expanded[id] {
		// Draw the menu above the scrolling cards without moving the search
		// field or the user's current model when it opens.
		at := op.Offset(image.Pt(0, dims.Size.Y+gtx.Dp(6))).Push(gtx.Ops)
		menuContext := gtx
		menuContext.Constraints.Min = image.Pt(gtx.Dp(216), 0)
		menuContext.Constraints.Max = image.Pt(gtx.Dp(216), gtx.Dp(280))
		items := make([]layout.Widget, 0, len(modelSortOrders))
		for _, order := range modelSortOrders {
			label := u.modelSortLabel(order)
			if order == current {
				label = "● " + label
			}
			items = append(items, u.button(id+".option."+order, label, func() {
				u.setValue(id, order)
				u.expanded[id] = false
			}))
		}
		layout.Stack{}.Layout(menuContext,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				area := clip.Rect{Max: gtx.Constraints.Min}.Push(gtx.Ops)
				gioevent.Op(gtx.Ops, &u.modelSortMenuTag)
				area.Pop()
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return nativeSurface(gtx, nativeColor(0xffffff), 9, func(gtx layout.Context) layout.Dimensions {
					return widget.Border{Color: nativeColor(0xc6d0ba), Width: 1, CornerRadius: 9}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.UniformInset(6).Layout(gtx, u.column(items...))
					})
				})
			}),
		)
		at.Pop()
	}
	op.Defer(gtx.Ops, drawing.Stop())
	return dims
}

func (u *nativeUI) dismissModelSort(gtx layout.Context) {
	if !u.expanded["models.sort"] {
		return
	}
	for {
		input, ok := gtx.Event(pointer.Filter{Target: &u.modelSortDismissTag, Kinds: pointer.Press}, key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		switch input := input.(type) {
		case pointer.Event:
			u.expanded["models.sort"] = false
		case key.Event:
			if input.State == key.Press {
				u.expanded["models.sort"] = false
			}
		}
	}
	if !u.expanded["models.sort"] {
		return
	}
	// This transparent handler is behind the deferred button/menu and passes
	// outside clicks through to the search field or control the user chose.
	drawing := op.Record(gtx.Ops)
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	gioevent.Op(gtx.Ops, &u.modelSortDismissTag)
	pass.Pop()
	area.Pop()
	op.Defer(gtx.Ops, drawing.Stop())
}

func (u *nativeUI) modelSortHint(order string) string {
	switch modelSortOrder(order) {
	case "codeModeRank":
		return u.tr("Code mode usage · last 7 days · unranked models last", "Uso en modo código · últimos 7 días · sin ranking al final")
	case "codingIndex":
		return u.tr("Highest coding index first · missing scores last", "Mayor índice de código primero · sin puntuación al final")
	case "speed":
		return u.tr("Fastest output first · tokens/s · missing speeds last", "Salida más rápida primero · tokens/s · sin velocidad al final")
	case "price":
		return u.tr("Lowest input price first · USD per 1M tokens · unknown prices last", "Menor precio de entrada primero · USD por 1M tokens · sin precio al final")
	default:
		return u.tr("Name A–Z · uses your custom display names", "Nombre A–Z · usa tus nombres personalizados")
	}
}

func (u *nativeUI) modelSortMetric(m modelInfo, order string) string {
	order = modelSortOrder(order)
	if order == "price" || order == "name" {
		return ""
	}
	value := modelSortValue(m, order)
	text := "—"
	if value != nil {
		text = strconv.FormatFloat(*value, 'f', -1, 64)
		if order == "codeModeRank" {
			text = "#" + text
		} else if order == "speed" {
			text += " tokens/s"
		}
	}
	return u.modelSortLabel(order) + ": " + text
}

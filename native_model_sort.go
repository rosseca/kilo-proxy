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
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
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
	options := make([]modelLabOption, 0, len(modelSortOrders))
	for _, order := range modelSortOrders {
		options = append(options, modelLabOption{order, u.modelSortLabel(order)})
	}
	return u.modelMenu(gtx, "models.sort", modelSortOrder(u.value("models.sort")), options, false)
}

func (u *nativeUI) modelPickerToolbar(prefix string, selection *nativeClientSelection) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		search := u.field(prefix+"search", u.tr("Search models or saved names", "Buscar modelos o nombres guardados"), "provider/model", false)
		refresh := u.button(prefix+"refresh", u.tr("Refresh catalog", "Actualizar catálogo"), u.refreshModels)
		labs := func(gtx layout.Context) layout.Dimensions {
			models := append([]modelInfo(nil), u.models...)
			for _, choice := range selection.Models {
				models = append(models, choice.Model)
			}
			current := normalizeModelLab(u.value("models.lab"))
			// Keep a resettable filter even when a refresh or another client no
			// longer has models from the active publisher.
			if current != "" {
				models = append(models, modelInfo{Provider: current})
			}
			return u.modelMenu(gtx, "models.lab", current, modelLabOptions(models, u.language), true)
		}
		if gtx.Constraints.Max.X < gtx.Dp(900) {
			return u.column(u.actionRow(search, refresh), u.pills(labs, u.modelSortButton))(gtx)
		}
		return u.actionRow(search, labs, u.modelSortButton, refresh)(gtx)
	}
}

// Popups are rendered in window coordinates after the scrolling page. Their
// anchor is captured from the same press in root and button coordinates, so
// nested insets, wrapping and page scrolling need no duplicated layout math.
type nativeModelMenuState struct {
	id, current     string
	options         []modelLabOption
	anchor, trigger image.Point
	width           unit.Dp
}

func (u *nativeUI) beginModelMenus(gtx layout.Context) {
	if u.modelMenuViewport != gtx.Constraints.Max {
		u.expanded["models.sort"], u.expanded["models.lab"] = false, false
		u.modelMenuAnchors = make(map[string]image.Point)
	}
	u.modelMenuViewport = gtx.Constraints.Max
	u.activeModelMenu = nil
	for {
		input, ok := gtx.Event(pointer.Filter{Target: &u.modelMenuPointerTag, Kinds: pointer.Press})
		if !ok {
			break
		}
		u.modelMenuPress = input.(pointer.Event).Position.Round()
	}
}

func (u *nativeUI) trackModelMenuPointer(gtx layout.Context) {
	drawing := op.Record(gtx.Ops)
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	gioevent.Op(gtx.Ops, &u.modelMenuPointerTag)
	pass.Pop()
	area.Pop()
	op.Defer(gtx.Ops, drawing.Stop())
}

func (u *nativeUI) modelMenu(gtx layout.Context, id, current string, options []modelLabOption, scroll bool) layout.Dimensions {
	label := ""
	for _, option := range options {
		if option.Value == current {
			label = option.Label
			break
		}
	}
	drawing := op.Record(gtx.Ops)
	dims := u.button(id+".toggle", label+"  ▾", func() {
		open := !u.expanded[id]
		u.expanded["models.sort"], u.expanded["models.lab"] = false, false
		u.expanded[id] = open
		if open {
			if u.modelMenuAnchors == nil {
				u.modelMenuAnchors = make(map[string]image.Point)
			}
			history := u.clickable(id + ".toggle").History()
			if len(history) > 0 {
				u.modelMenuAnchors[id] = u.modelMenuPress.Sub(history[len(history)-1].Position)
			} else if _, ok := u.modelMenuAnchors[id]; !ok {
				// Keyboard/programmatic activation without a preceding pointer press
				// still gets a bounded popup; later pointer activation records the anchor.
				u.modelMenuAnchors[id] = image.Pt(u.modelMenuViewport.X/2, u.modelMenuViewport.Y/3)
			}
		}
	})(gtx)
	trigger := drawing.Stop()
	if u.expanded[id] {
		op.Defer(gtx.Ops, trigger)
	} else {
		trigger.Add(gtx.Ops)
	}
	if u.expanded[id] {
		width := unit.Dp(216)
		if scroll {
			width = 252
		}
		u.activeModelMenu = &nativeModelMenuState{id: id, current: current, options: options, anchor: u.modelMenuAnchors[id], trigger: dims.Size, width: width}
	}
	return dims
}

// Reserve the larger side if the requested menu cannot fit below its trigger.
// The returned rectangle is in window pixels and never extends off screen.
func nativeModelMenuRect(viewport, anchor, trigger, desired image.Point, margin, gap int) image.Rectangle {
	availableWidth := max(0, viewport.X-2*margin)
	width := min(desired.X, availableWidth)
	below := max(0, viewport.Y-margin-anchor.Y-trigger.Y-gap)
	above := max(0, anchor.Y-gap-margin)
	up := below < desired.Y && above > below
	room := below
	if up {
		room = above
	}
	height := min(desired.Y, room, max(0, viewport.Y-2*margin))
	x := max(margin, min(anchor.X, viewport.X-margin-width))
	y := anchor.Y + trigger.Y + gap
	if up {
		y = anchor.Y - gap - height
	}
	y = max(margin, min(y, viewport.Y-margin-height))
	return image.Rect(x, y, x+width, y+height)
}

func (u *nativeUI) layoutActiveModelMenu(gtx layout.Context) {
	menu := u.activeModelMenu
	if menu == nil || !u.expanded[menu.id] {
		return
	}
	margin, gap := gtx.Dp(8), gtx.Dp(6)
	desired := image.Pt(gtx.Dp(menu.width), gtx.Dp(280))
	bounds := nativeModelMenuRect(gtx.Constraints.Max, menu.anchor, menu.trigger, desired, margin, gap)
	menuContext := gtx
	menuContext.Constraints = layout.Constraints{Min: image.Pt(bounds.Dx(), 0), Max: bounds.Size()}
	drawing := op.Record(gtx.Ops)
	dims := layout.Stack{}.Layout(menuContext,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			area := clip.Rect{Max: gtx.Constraints.Min}.Push(gtx.Ops)
			gioevent.Op(gtx.Ops, &u.modelSortMenuTag)
			area.Pop()
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return nativeSurface(gtx, nativeColor(0xffffff), 9, func(gtx layout.Context) layout.Dimensions {
				return widget.Border{Color: nativeColor(0xc6d0ba), Width: 1, CornerRadius: 9}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(6).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return material.List(u.theme, u.list(menu.id+".options")).Layout(gtx, len(menu.options), func(gtx layout.Context, index int) layout.Dimensions {
							option := menu.options[index]
							label := option.Label
							if option.Value == menu.current {
								label = "● " + label
							}
							return layout.Inset{Bottom: 6}.Layout(gtx, u.button(menu.id+".option."+option.Value, label, func() {
								u.setValue(menu.id, option.Value)
								u.expanded[menu.id] = false
							}))
						})
					})
				})
			})
		}),
	)
	content := drawing.Stop()
	bounds = nativeModelMenuRect(gtx.Constraints.Max, menu.anchor, menu.trigger, dims.Size, margin, gap)
	drawing = op.Record(gtx.Ops)
	at := op.Offset(bounds.Min).Push(gtx.Ops)
	content.Add(gtx.Ops)
	at.Pop()
	op.Defer(gtx.Ops, drawing.Stop())
}

func (u *nativeUI) dismissModelSort(gtx layout.Context) {
	if !u.expanded["models.sort"] && !u.expanded["models.lab"] {
		return
	}
	for {
		input, ok := gtx.Event(pointer.Filter{Target: &u.modelSortDismissTag, Kinds: pointer.Press | pointer.Scroll, ScrollX: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}, ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}}, key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		switch input := input.(type) {
		case pointer.Event:
			u.expanded["models.sort"], u.expanded["models.lab"] = false, false
		case key.Event:
			if input.State == key.Press {
				u.expanded["models.sort"], u.expanded["models.lab"] = false, false
			}
		}
	}
	if !u.expanded["models.sort"] && !u.expanded["models.lab"] {
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

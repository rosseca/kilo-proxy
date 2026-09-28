//go:build desktop

package main

import (
	"image"
	"slices"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

type nativeModelGridState struct {
	columns int
	ids     []string
}

func nativeModelGridColumns(width, minimum, gap int) int {
	return max(1, (width+gap)/max(1, minimum+gap))
}

// A row measures each interactive card exactly once. Drawing operations retain
// their column transforms; the tallest card determines where the next row starts.
func nativeModelGridRow(columns, gap int, cards []layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		width := gtx.Constraints.Max.X
		space := max(0, width-gap*(columns-1))
		x, height := 0, 0
		for i, card := range cards {
			cellWidth := space / columns
			if i < space%columns {
				cellWidth++
			}
			cell := gtx
			cell.Constraints.Min = image.Pt(cellWidth, 0)
			cell.Constraints.Max.X = cellWidth
			at := op.Offset(image.Pt(x, 0)).Push(gtx.Ops)
			dims := card(cell)
			at.Pop()
			height = max(height, dims.Size.Y)
			x += cellWidth + gap
		}
		return layout.Dimensions{Size: gtx.Constraints.Constrain(image.Pt(width, height))}
	}
}

func (u *nativeUI) modelGrid(id string, ids []string, cards []layout.Widget) layout.Widget {
	return u.modelGridLayout(id, ids, cards, len(cards) > 6)
}

// modelGridLayout lays cards in responsive columns. A scrolling grid caps its
// height; a short curated list passes scroll=false so every card stays visible.
func (u *nativeUI) modelGridLayout(id string, ids []string, cards []layout.Widget, scroll bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gap := gtx.Dp(12)
		width := gtx.Constraints.Max.X
		if scroll {
			width -= gtx.Dp(14)
		}
		columns := nativeModelGridColumns(width, gtx.Dp(280), gap)
		c := u.clientState()
		if c.Grids == nil {
			c.Grids = map[string]*nativeModelGridState{}
		}
		if c.Grids[id] == nil {
			c.Grids[id] = &nativeModelGridState{}
		}
		state := c.Grids[id]
		list := u.list(id)
		if state.columns != columns || !slices.Equal(state.ids, ids) {
			first := 0
			if old := list.Position.First * state.columns; old >= 0 && old < len(state.ids) {
				if found := slices.Index(ids, state.ids[old]); found >= 0 {
					first = found / columns
				}
			}
			list.ScrollTo(first)
			state.columns = columns
			state.ids = append(state.ids[:0], ids...)
		}
		rows := (len(cards) + columns - 1) / columns
		row := func(gtx layout.Context, index int) layout.Dimensions {
			first := index * columns
			return nativeModelGridRow(columns, gap, cards[first:min(first+columns, len(cards))])(gtx)
		}
		if scroll {
			gtx.Constraints.Min.Y = 0
			gtx.Constraints.Max.Y = gtx.Dp(480)
			return material.List(u.theme, list).Layout(gtx, rows, func(gtx layout.Context, index int) layout.Dimensions {
				return layout.Inset{Right: 14, Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return row(gtx, index) })
			})
		}
		children := make([]layout.Widget, rows)
		for i := range children {
			children[i] = func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return row(gtx, i) })
			}
		}
		return u.column(children...)(gtx)
	}
}

func (u *nativeUI) modelPriceCells(m modelInfo) layout.Widget {
	if strings.HasPrefix(m.ID, "chatgpt/") {
		return u.textStyle(18, u.tr("Subscription quota", "Cuota de suscripción"), nativeInk, font.SemiBold)
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, u.textStyle(18, nativeTokenPrice(m.InputPrice)+" / "+nativeTokenPrice(m.OutputPrice), nativeInk, font.SemiBold)),
			layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
			layout.Rigid(u.note(u.tr("USD / 1M", "USD / 1M"))),
		)
	}
}

// Reserve a shared title track for ordinary names so every card's price cells
// align. Long names can still grow beyond it instead of being clipped.
func (u *nativeUI) modelCardHeading(children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.Y = max(gtx.Constraints.Min.Y, gtx.Dp(96))
		return u.column(children...)(gtx)
	}
}

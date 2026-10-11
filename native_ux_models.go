//go:build desktop

package main

import (
	"image"
	"image/color"
	"strconv"
	"strings"

	"gioui.org/font"
	gioevent "gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// Shared building blocks for the Models area (library, catalog, packs and the
// onboarding models step). Every name carries the uxm prefix so the redesign
// can merge with the other areas without collisions.

var (
	uxmIconEdit    = mustNativeIcon(icons.EditorModeEdit)
	uxmIconUp      = mustNativeIcon(icons.NavigationArrowUpward)
	uxmIconDown    = mustNativeIcon(icons.NavigationArrowDownward)
	uxmIconMore    = mustNativeIcon(icons.NavigationMoreHoriz)
	uxmIconContext = mustNativeIcon(icons.ImageCropLandscape)
	uxmIconOutput  = mustNativeIcon(icons.HardwareKeyboardTab)
	uxmIconRadioOn = mustNativeIcon(icons.ToggleRadioButtonChecked)
	uxmIconRadio   = mustNativeIcon(icons.ToggleRadioButtonUnchecked)
	uxmIconBoxOn   = mustNativeIcon(icons.ToggleCheckBox)
	uxmIconBox     = mustNativeIcon(icons.ToggleCheckBoxOutlineBlank)
	uxmIconSwap    = mustNativeIcon(icons.ActionSwapHoriz)
	uxmIconCopy    = mustNativeIcon(icons.ContentContentCopy)
	uxmHairline    = nativeColor(0xe3e3e3)
)

// uxmIcon draws an icon at a fixed square size.
func uxmIcon(icon *widget.Icon, size unit.Dp, c color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if icon == nil {
			return layout.Dimensions{}
		}
		s := gtx.Dp(size)
		gtx.Constraints.Min, gtx.Constraints.Max = image.Pt(s, s), image.Pt(s, s)
		return icon.Layout(gtx, c)
	}
}

// uxmFill paints a solid rectangle of the given size.
func uxmFill(gtx layout.Context, c color.NRGBA, size image.Point) layout.Dimensions {
	paint.FillShape(gtx.Ops, c, clip.Rect{Max: size}.Op())
	return layout.Dimensions{Size: size}
}

// uxmHRule paints a full-width rule.
func uxmHRule(thickness unit.Dp, c color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return uxmFill(gtx, c, image.Pt(gtx.Constraints.Max.X, gtx.Dp(thickness)))
	}
}

// uxmBordered draws a square border of the given width around padded content.
func uxmBordered(background color.NRGBA, width unit.Dp, inset layout.Inset, content layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: nativeInk, Width: width}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return inset.Layout(gtx, content)
			})
		})
	}
}

// uxmPanel is a full-width box with a 2px black border.
func uxmPanel(background color.NRGBA, content layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: nativeInk, Width: nativeLine}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(nativeLine).Layout(gtx, content)
			})
		})
	}
}

// uxmLeftBar overlays a vertical bar on the left edge of a row.
func uxmLeftBar(width unit.Dp, c color.NRGBA, content layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		dims := content(gtx)
		uxmFill(gtx, c, image.Pt(gtx.Dp(width), dims.Size.Y))
		return dims
	}
}

// uxmMono is a Geist Mono label (headings, eyebrows, counts and badges).
func (u *nativeUI) uxmMono(size unit.Sp, s string, c color.NRGBA) layout.Widget {
	return u.monoStyle(size, s, c, font.Bold)
}

// uxmSubtle is muted body text.
func (u *nativeUI) uxmSubtle(size unit.Sp, s string) layout.Widget {
	return u.textStyle(size, s, nativeTextSubtle, font.Normal)
}

// uxmTitle is the semibold body title of a row.
func (u *nativeUI) uxmTitle(size unit.Sp, s string) layout.Widget {
	return u.textStyle(size, s, nativeInk, font.SemiBold)
}

// uxmCountBox is the bordered number next to a heading or a tab.
func (u *nativeUI) uxmCountBox(n int, fg color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return widget.Border{Color: fg, Width: 1.5}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 1, Bottom: 1, Left: 6, Right: 6}.Layout(gtx, u.monoStyle(12, strconv.Itoa(n), fg, font.Bold))
		})
	}
}

// uxmBadge is a small citrus tag with a star, such as "★ PREDETERMINADO".
func (u *nativeUI) uxmBadge(text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return uxmBordered(nativeCitrus, 1, layout.Inset{Top: 2, Bottom: 2, Left: 6, Right: 6}, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(uxmIcon(nativeIconStar, 11, nativeInk)),
				layout.Rigid(layout.Spacer{Width: 4}.Layout),
				layout.Rigid(u.monoStyle(10, strings.ToUpper(text), nativeInk, font.Bold)),
			)
		})(gtx)
	}
}

// uxmDefaultBadge marks the default model.
func (u *nativeUI) uxmDefaultBadge() layout.Widget {
	return u.uxmBadge(u.tr("Default", "Predeterminado"))
}

// uxmTag is a plain bordered tag with optional emphasis colour.
func (u *nativeUI) uxmTag(text string, background color.NRGBA) layout.Widget {
	return uxmBordered(background, 1, layout.Inset{Top: 2, Bottom: 2, Left: 6, Right: 6}, u.monoStyle(10, strings.ToUpper(text), nativeInk, font.Bold))
}

// uxmChip is a bordered model chip. Citrus chips carry a star as second cue.
// A non-nil remove adds an inline ✕ button.
func (u *nativeUI) uxmChip(id, label string, star bool, remove func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		background := nativeSurface
		if star {
			background = nativeCitrus
		}
		return uxmBordered(background, 1.5, layout.Inset{Top: 4, Bottom: 4, Left: 8, Right: 8}, func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{}
			if star {
				children = append(children, layout.Rigid(uxmIcon(nativeIconStar, 12, nativeInk)), layout.Rigid(layout.Spacer{Width: 4}.Layout))
			}
			children = append(children, layout.Rigid(u.textStyle(12, label, nativeInk, font.Medium)))
			if remove != nil {
				children = append(children, layout.Rigid(layout.Spacer{Width: 6}.Layout), layout.Rigid(u.uxmIconButton(id, u.tr("Remove ", "Quitar ")+label, nativeIconClose, 12, false, remove)))
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
		})(gtx)
	}
}

// uxmIconButton is a bare clickable icon with an accessible label.
func (u *nativeUI) uxmIconButton(id, label string, icon *widget.Icon, size unit.Dp, filled bool, action func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := u.clickable(id)
		for b.Clicked(gtx) {
			if gtx.Enabled() && action != nil {
				action()
			}
		}
		fg := nativeInk
		if !gtx.Enabled() {
			fg = nativeTextDisabled
		}
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(semantic.Button).Add(gtx.Ops)
			semantic.LabelOp(label).Add(gtx.Ops)
			if filled {
				return nativeBox(gtx, nativeInk, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(4).Layout(gtx, uxmIcon(icon, size, nativeSurface))
				})
			}
			background := color.NRGBA{}
			if b.Hovered() && gtx.Enabled() {
				background = nativeIvory
			}
			return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(4).Layout(gtx, uxmIcon(icon, size, fg))
			})
		})
	}
}

// Popovers. At most one is open; its id lives in the "uxm.menu" editor so no
// new nativeUI field is needed. The anchor (trigger origin in window
// coordinates) is captured from the same press the existing model menus use.

func (u *nativeUI) uxmMenuOpen(id string) bool { return id != "" && u.value("uxm.menu") == id }
func (u *nativeUI) uxmCloseMenu()              { u.setValue("uxm.menu", "") }
func (u *nativeUI) uxmOpenMenu(id, clickID string) {
	u.setValue("uxm.menu", id)
	u.expanded["models.sort"], u.expanded["models.lab"] = false, false
	if u.modelMenuAnchors == nil {
		u.modelMenuAnchors = make(map[string]image.Point)
	}
	// The marker disappears with the anchors when the window is resized,
	// which closes the popover instead of leaving it at a stale place.
	u.modelMenuAnchors["uxm:open"] = image.Pt(1, 1)
	key := "uxm:" + id
	if history := u.clickable(clickID).History(); len(history) > 0 {
		u.modelMenuAnchors[key] = u.modelMenuPress.Sub(history[len(history)-1].Position)
	} else {
		delete(u.modelMenuAnchors, key)
	}
}
func (u *nativeUI) uxmToggleMenu(id, clickID string) {
	if u.uxmMenuOpen(id) {
		u.uxmCloseMenu()
		return
	}
	u.uxmOpenMenu(id, clickID)
}

// uxmPopover lays out trigger and, while id is open, a floating menu below it
// (above when there is more room). The menu is deferred above the page; an
// outside press, scroll or Escape closes it without reaching the page.
func (u *nativeUI) uxmPopover(id string, width unit.Dp, alignEnd bool, trigger layout.Widget, items func() []layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		dims := trigger(gtx)
		if !u.uxmMenuOpen(id) {
			return dims
		}
		catcher, body := u.clickable("uxm.menu.catcher"), u.clickable("uxm.menu.body")
		scroll := pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: catcher, Kinds: pointer.Press | pointer.Scroll, ScrollX: scroll, ScrollY: scroll}, key.Filter{Name: key.NameEscape})
			if !ok {
				break
			}
			switch e := ev.(type) {
			case pointer.Event:
				u.uxmCloseMenu()
			case key.Event:
				if e.State == key.Press {
					u.uxmCloseMenu()
				}
			}
		}
		for {
			if _, ok := gtx.Event(pointer.Filter{Target: body, Kinds: pointer.Press | pointer.Scroll, ScrollX: scroll, ScrollY: scroll}); !ok {
				break
			}
		}
		if _, open := u.modelMenuAnchors["uxm:open"]; !open {
			u.uxmCloseMenu()
		}
		if !u.uxmMenuOpen(id) {
			return dims
		}
		viewport := u.modelMenuViewport
		if viewport == (image.Point{}) {
			viewport = image.Pt(gtx.Dp(1180), gtx.Dp(820))
		}
		anchor, known := u.modelMenuAnchors["uxm:"+id]
		if !known {
			anchor = image.Pt(max(0, viewport.X/2-dims.Size.X), viewport.Y/4)
		}
		margin, gap := gtx.Dp(8), gtx.Dp(4)
		w := min(gtx.Dp(width), max(0, viewport.X-2*margin))
		below := max(0, viewport.Y-margin-anchor.Y-dims.Size.Y-gap)
		above := max(0, anchor.Y-gap-margin)
		content := u.uxmPopoverBody(id, items())
		measure := func(height int) (op.CallOp, image.Point) {
			mgtx := gtx
			mgtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, max(gtx.Dp(80), height))}
			rec := op.Record(gtx.Ops)
			size := content(mgtx).Size
			return rec.Stop(), size
		}
		call, size := measure(max(below, above))
		y := anchor.Y + dims.Size.Y + gap
		if size.Y > below && above > below {
			y = anchor.Y - gap - size.Y
		} else if size.Y > below {
			call, size = measure(below)
		}
		x := anchor.X
		if alignEnd {
			x = anchor.X + dims.Size.X - size.X
		}
		x = max(margin, min(x, viewport.X-margin-size.X))
		macro := op.Record(gtx.Ops)
		area := clip.Rect{Min: image.Pt(-1<<16, -1<<16), Max: image.Pt(1<<16, 1<<16)}.Push(gtx.Ops)
		gioevent.Op(gtx.Ops, catcher)
		area.Pop()
		at := op.Offset(image.Pt(x-anchor.X, y-anchor.Y)).Push(gtx.Ops)
		barea := clip.Rect{Max: size}.Push(gtx.Ops)
		gioevent.Op(gtx.Ops, body)
		barea.Pop()
		call.Add(gtx.Ops)
		at.Pop()
		op.Defer(gtx.Ops, macro.Stop())
		return dims
	}
}

func (u *nativeUI) uxmPopoverBody(id string, items []layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return nativeHardShadow(gtx, func(gtx layout.Context) layout.Dimensions {
			return nativeBox(gtx, nativeSurface, func(gtx layout.Context) layout.Dimensions {
				return widget.Border{Color: nativeInk, Width: nativeLine}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(nativeLine).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return material.List(u.theme, u.list("uxm.menu.list."+id)).Layout(gtx, len(items), func(gtx layout.Context, i int) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return items[i](gtx)
						})
					})
				})
			})
		})
	}
}

// uxmMenuItem is one popover row. Selected rows are citrus with a check;
// trailing text sits on the right; danger rows are red.
type uxmMenuItemSpec struct {
	icon                   *widget.Icon
	label, trailing        string
	selected, danger, keep bool
	disabled               bool
	bold                   string
}

func (u *nativeUI) uxmMenuItem(id string, spec uxmMenuItemSpec, action func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if spec.disabled {
			gtx = gtx.Disabled()
		}
		b := u.clickable(id)
		for b.Clicked(gtx) {
			if gtx.Enabled() {
				if !spec.keep {
					u.uxmCloseMenu()
				}
				if action != nil {
					action()
				}
				if u.invalidate != nil {
					u.invalidate()
				}
			}
		}
		fg := nativeInk
		if spec.danger {
			fg = nativeErrorFG
		}
		if !gtx.Enabled() {
			fg = nativeTextDisabled
		}
		background := nativeSurface
		if spec.selected {
			background = nativeCitrus
		} else if b.Hovered() && gtx.Enabled() {
			background = nativeIvory
		}
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(semantic.Button).Add(gtx.Ops)
			semantic.LabelOp(spec.label).Add(gtx.Ops)
			if spec.selected {
				semantic.SelectedOp(true).Add(gtx.Ops)
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return layout.Inset{Top: 10, Bottom: 10, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							weight := font.Normal
							if spec.selected {
								weight = font.SemiBold
							}
							children := []layout.FlexChild{}
							icon := spec.icon
							if spec.selected && icon == nil {
								icon = nativeIconCheck
							}
							if icon != nil {
								children = append(children, layout.Rigid(uxmIcon(icon, 15, fg)), layout.Rigid(layout.Spacer{Width: 10}.Layout))
							}
							label := spec.label
							children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								if spec.bold == "" {
									return u.textStyle(14, label, fg, weight)(gtx)
								}
								return layout.Flex{Alignment: layout.Baseline}.Layout(gtx, layout.Rigid(u.textStyle(14, label, fg, weight)), layout.Rigid(u.textStyle(14, spec.bold, fg, font.Bold)))
							}))
							if spec.trailing != "" {
								tc := nativeTextSubtle
								if spec.selected {
									tc = nativeInk
								}
								children = append(children, layout.Rigid(layout.Spacer{Width: 12}.Layout), layout.Rigid(u.textStyle(12, spec.trailing, tc, font.Medium)))
							}
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
						})
					})
				}),
				layout.Rigid(uxmHRule(1, uxmHairline)),
			)
		})
	}
}

// uxmMenuSeparator divides popover groups with a heavy rule.
func uxmMenuSeparator(gtx layout.Context) layout.Dimensions {
	return uxmHRule(nativeLine, nativeInk)(gtx)
}

// uxmMenuTitle is a mono eyebrow at the top of a popover.
func (u *nativeUI) uxmMenuTitle(text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 12, Bottom: 8, Left: 14, Right: 14}.Layout(gtx, u.eyebrow(text))
	}
}

// uxmMenuNote is a muted footer line inside a popover.
func (u *nativeUI) uxmMenuNote(text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return nativeBox(gtx, nativeIvory, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Top: 10, Bottom: 10, Left: 14, Right: 14}.Layout(gtx, u.uxmSubtle(12, text))
		})
	}
}

// uxmMoreButton is the ⋯ trigger; it turns black while its menu is open.
func (u *nativeUI) uxmMoreButton(menuID, label string) layout.Widget {
	clickID := menuID + ".toggle"
	return u.uxmIconButton(clickID, label, uxmIconMore, 18, u.uxmMenuOpen(menuID), func() { u.uxmToggleMenu(menuID, clickID) })
}

// uxmDropdown is a compact bordered "Label ▾" trigger for a popover.
func (u *nativeUI) uxmDropdown(menuID, label string, active bool) layout.Widget {
	clickID := menuID + ".toggle"
	return func(gtx layout.Context) layout.Dimensions {
		b := u.clickable(clickID)
		for b.Clicked(gtx) {
			if gtx.Enabled() {
				u.uxmToggleMenu(menuID, clickID)
			}
		}
		background, fg := nativeSurface, nativeInk
		if active {
			background, fg = nativeInk, nativeSurface
		} else if b.Hovered() && gtx.Enabled() {
			background = nativeIvory
		}
		if !gtx.Enabled() {
			fg = nativeTextDisabled
		}
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(semantic.Button).Add(gtx.Ops)
			semantic.LabelOp(label).Add(gtx.Ops)
			return uxmBordered(background, nativeLine, layout.Inset{Top: 9, Bottom: 9, Left: 12, Right: 8}, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(u.textStyle(13, label, fg, font.Medium)),
					layout.Rigid(layout.Spacer{Width: 4}.Layout),
					layout.Rigid(uxmIcon(nativeIconExpandMore, 16, fg)),
				)
			})(gtx)
		})
	}
}

// uxmLink is an underlined inline action.
func (u *nativeUI) uxmLink(id, label string, action func()) layout.Widget {
	return u.buttonKind(id, label, nativeButtonLink, nil, false, action)
}

// uxmTab is one Models tab with its count.
type uxmTab struct {
	value, label string
	count        int
}

// uxmTabs: the active tab is a black box with white text and a citrus count;
// a 3px black rule runs under the whole bar.
func (u *nativeUI) uxmTabs(prefix string, tabs []uxmTab, selected string, choose func(string)) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{}
		for _, tab := range tabs {
			tab := tab
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				b := u.clickable(prefix + tab.value)
				for b.Clicked(gtx) {
					u.uxmCloseMenu()
					if choose != nil {
						choose(tab.value)
					}
				}
				active := tab.value == selected
				background, fg, count := color.NRGBA{}, nativeInk, nativeInk
				if active {
					background, fg, count = nativeInk, nativeSurface, nativeCitrus
				} else if b.Hovered() {
					background = nativeIvory
				}
				return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					semantic.ClassOp(semantic.Button).Add(gtx.Ops)
					semantic.LabelOp(tab.label).Add(gtx.Ops)
					if active {
						semantic.SelectedOp(true).Add(gtx.Ops)
					}
					return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: 10, Bottom: 10, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(u.textStyle(15, tab.label, fg, font.SemiBold)),
								layout.Rigid(layout.Spacer{Width: 8}.Layout),
								layout.Rigid(u.uxmCountBox(tab.count, count)),
							)
						})
					})
				})
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.End}.Layout(gtx, children...)
			}),
			layout.Rigid(uxmHRule(nativeLineHeavy, nativeInk)),
		)
	}
}

// uxmColumn describes a table column: a fixed width, or flexible when weight > 0.
// Columns with hideBelow are dropped when the table is narrower than that width.
type uxmColumn struct {
	label     string
	width     unit.Dp
	weight    float32
	hideBelow unit.Dp
}

func uxmVisibleColumns(gtx layout.Context, columns []uxmColumn) []bool {
	visible := make([]bool, len(columns))
	for i, c := range columns {
		visible[i] = c.hideBelow == 0 || gtx.Constraints.Max.X >= gtx.Dp(c.hideBelow)
	}
	return visible
}

func uxmCells(gtx layout.Context, columns []uxmColumn, cells []layout.Widget, alignment layout.Alignment) layout.Dimensions {
	visible := uxmVisibleColumns(gtx, columns)
	children := []layout.FlexChild{}
	for i, c := range columns {
		if !visible[i] || i >= len(cells) {
			continue
		}
		cell := cells[i]
		if cell == nil {
			cell = func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{} }
		}
		padded := func(gtx layout.Context) layout.Dimensions {
			dims := layout.Inset{Left: 8, Right: 8}.Layout(gtx, cell)
			dims.Size.X = max(dims.Size.X, gtx.Constraints.Min.X)
			return dims
		}
		if c.weight > 0 {
			children = append(children, layout.Flexed(c.weight, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return padded(gtx)
			}))
		} else {
			w := c.width
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X, gtx.Constraints.Max.X = gtx.Dp(w), gtx.Dp(w)
				return padded(gtx)
			}))
		}
	}
	return layout.Flex{Alignment: alignment}.Layout(gtx, children...)
}

// uxmTableHeader is the ivory header of a bordered table.
func (u *nativeUI) uxmTableHeader(columns []uxmColumn) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		cells := make([]layout.Widget, len(columns))
		for i, c := range columns {
			cells[i] = u.monoStyle(11, strings.ToUpper(c.label), nativeTextSubtle, font.Bold)
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return nativeBox(gtx, nativeIvory, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Top: 9, Bottom: 9, Left: 6, Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return uxmCells(gtx, columns, cells, layout.Middle)
					})
				})
			}),
			layout.Rigid(uxmHRule(nativeLine, nativeInk)),
		)
	}
}

// uxmTableRow is one table row; marked rows get a 6px citrus bar on the left.
func (u *nativeUI) uxmTableRow(columns []uxmColumn, marked bool, background color.NRGBA, cells ...layout.Widget) layout.Widget {
	row := func(gtx layout.Context) layout.Dimensions {
		return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Top: 10, Bottom: 10, Left: 6, Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return uxmCells(gtx, columns, cells, layout.Middle)
			})
		})
	}
	if marked {
		row = uxmLeftBar(6, nativeCitrus, row)
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(row), layout.Rigid(uxmHRule(1, nativeInk)))
	}
}

// uxmStack stacks children without gaps.
func uxmStack(children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		flex := make([]layout.FlexChild, 0, len(children))
		for _, child := range children {
			if child != nil {
				flex = append(flex, layout.Rigid(child))
			}
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, flex...)
	}
}

// uxmGap is vertical space.
func uxmGap(height unit.Dp) layout.Widget { return layout.Spacer{Height: height}.Layout }

// uxmHStack lays children horizontally, centred, with a gap; a nil child is a
// flexible spacer that pushes the rest to the right.
func uxmHStack(gap unit.Dp, children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		flex := []layout.FlexChild{}
		for i, child := range children {
			if i > 0 && child != nil && children[i-1] != nil {
				flex = append(flex, layout.Rigid(layout.Spacer{Width: gap}.Layout))
			}
			if child == nil {
				flex = append(flex, layout.Flexed(1, layout.Spacer{}.Layout))
				continue
			}
			flex = append(flex, layout.Rigid(child))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, flex...)
	}
}

// uxmModelCell shows a model name (with optional badge) over "Provider · id".
func (u *nativeUI) uxmModelCell(name, provider, id string, badge layout.Widget) layout.Widget {
	title := u.uxmTitle(15, name)
	if badge != nil {
		title = u.pills(u.uxmTitle(15, name), badge)
	}
	meta := id
	if provider != "" {
		meta = provider + " · " + id
	}
	return uxmStack(title, uxmGap(2), u.uxmSubtle(12, meta))
}

// uxmClickRow is a full-width clickable row exposed with the given semantic class.
func (u *nativeUI) uxmClickRow(id, label string, class semantic.ClassOp, selected bool, action func(), content func(hovered bool) layout.Widget) layout.Widget {
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
		if !gtx.Enabled() {
			// Keep a disabled row in the semantic tree, marked as disabled.
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			m := op.Record(gtx.Ops)
			dims := content(false)(gtx)
			call := m.Stop()
			defer clip.Rect{Max: dims.Size}.Push(gtx.Ops).Pop()
			semantic.ClassOp(class).Add(gtx.Ops)
			semantic.LabelOp(label).Add(gtx.Ops)
			semantic.SelectedOp(selected).Add(gtx.Ops)
			semantic.EnabledOp(false).Add(gtx.Ops)
			call.Add(gtx.Ops)
			return dims
		}
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(class).Add(gtx.Ops)
			semantic.LabelOp(label).Add(gtx.Ops)
			semantic.SelectedOp(selected).Add(gtx.Ops)
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return content(b.Hovered() && gtx.Enabled())(gtx)
		})
	}
}

// uxmPrice is "$in / $out" per 1M tokens, or the subscription label.
func (u *nativeUI) uxmPrice(m modelInfo) string {
	if strings.HasPrefix(m.ID, "chatgpt/") {
		return u.tr("Subscription", "Suscripción")
	}
	if m.InputPrice == nil && m.OutputPrice == nil {
		return "—"
	}
	return nativeTokenPrice(m.InputPrice) + " / " + nativeTokenPrice(m.OutputPrice)
}

// uxmPlural picks the singular or plural sentence for n.
func uxmPlural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return strings.ReplaceAll(many, "%d", strconv.Itoa(n))
}

// uxmStrip is an ivory bordered strip: mono eyebrow, title, description, links.
func (u *nativeUI) uxmStrip(eyebrow string, title, description layout.Widget, actions ...layout.Widget) layout.Widget {
	body := []layout.Widget{u.eyebrow(eyebrow), uxmGap(4), title}
	if description != nil {
		body = append(body, uxmGap(2), description)
	}
	return uxmPanel(nativeIvory, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 12, Bottom: 12, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if gtx.Constraints.Max.X < gtx.Dp(560) || len(actions) == 0 {
				return u.column(uxmStack(body...), u.pills(actions...))(gtx)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, uxmStack(body...)),
				layout.Rigid(layout.Spacer{Width: 16}.Layout),
				layout.Rigid(u.pills(actions...)),
			)
		})
	})
}

// uxmOptionRow is a bordered "title / description … [action]" row.
func (u *nativeUI) uxmOptionRow(title, description string, action layout.Widget) layout.Widget {
	return uxmPanel(nativeSurface, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 14, Bottom: 14, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, uxmStack(u.uxmTitle(15, title), uxmGap(2), u.uxmSubtle(13, description))),
				layout.Rigid(layout.Spacer{Width: 16}.Layout),
				layout.Rigid(action),
			)
		})
	})
}

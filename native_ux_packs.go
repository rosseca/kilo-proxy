//go:build desktop

package main

import (
	"errors"
	"fmt"
	"image"
	"slices"
	"strings"

	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// Packs and My packs: a split layout with the pack list on the left and the
// selected pack on the right, a "Use in…" menu per harness and the shared
// catalog picker for personal packs.

// uxmPackAgents lists the harnesses a pack can be given to, in menu order.
var uxmPackAgents = []struct{ key, name string }{
	{"codex", "Codex"}, {"claude", "Claude Code"}, {"codex-cli", "Codex CLI"},
	{"t3-code", "T3 Code"},
	{"opencode", "OpenCode 1 & 2"}, {"omp", "Oh My Pi"}, {"zed", "Zed"}, {"claude-desktop", "Claude Desktop"},
}

// uxmPackUsers returns the harness names that use pack id.
func (u *nativeUI) uxmPackUsers(id string) []string {
	assignments := u.packsState().file.Assignments
	users := []string{}
	for _, agent := range uxmPackAgents {
		if assignments[agent.key] == id {
			users = append(users, agent.name)
		}
	}
	return users
}

// uxmHarnessChip is a citrus chip with a check for a harness using a pack.
func (u *nativeUI) uxmHarnessChip(name string) layout.Widget {
	return uxmBordered(nativeCitrus, 1.5, layout.Inset{Top: 3, Bottom: 3, Left: 8, Right: 8}, uxmHStack(4, uxmIcon(nativeIconCheck, 12, nativeInk), u.textStyle(13, name, nativeInk, font.Medium)))
}

// uxmPackListRow is one row of the pack list. The selected row is citrus with
// a 6px black bar.
func (u *nativeUI) uxmPackListRow(clickID, title, subtitle string, users []string, selected bool, action func()) layout.Widget {
	row := u.uxmClickRow(clickID, title, semantic.Button, selected, action, func(hovered bool) layout.Widget {
		background := nativeSurface
		if selected {
			background = nativeCitrus
		} else if hovered {
			background = nativeIvory
		}
		body := []layout.Widget{u.uxmTitle(16, title)}
		if subtitle != "" {
			body = append(body, uxmGap(3), u.textStyle(13, subtitle, nativeText, font.Normal))
		}
		if len(users) > 0 {
			body = append(body, uxmGap(6), u.uxmMono(11, "● "+strings.ToUpper(u.tr("Used by: ", "Lo usan: ")+strings.Join(users, " · ")), nativeInk))
		}
		return func(gtx layout.Context) layout.Dimensions {
			return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Inset{Top: 14, Bottom: 14, Left: 20, Right: 16}.Layout(gtx, uxmStack(body...))
			})
		}
	})
	if selected {
		row = uxmLeftBar(6, nativeInk, row)
	}
	return uxmStack(row, uxmHRule(1, nativeInk))
}

// uxmPacksSplit lays the list beside the detail, or above it when narrow.
func uxmPacksSplit(list, detail layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if gtx.Constraints.Max.X < gtx.Dp(760) {
			return uxmStack(list, uxmGap(16), detail)(gtx)
		}
		listWidth := gtx.Constraints.Max.X * 3 / 10
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X, gtx.Constraints.Max.X = listWidth, listWidth
				return list(gtx)
			}),
			layout.Rigid(layout.Spacer{Width: 24}.Layout),
			layout.Flexed(1, detail),
		)
	}
}

// uxmPackCanAssign reports whether pack id can be given to a harness now.
func (u *nativeUI) uxmPackCanAssign(id string) bool {
	p := u.packsState()
	library, ok := u.packLibrary(id)
	return ok && len(library.Models) > 0 && len(u.models) > 0 && len(u.missingPackModels(id)) == 0 && !p.store.recovery && p.saveError == nil
}

// uxmPackClaudeOK reports whether the pack holds a model Claude Desktop accepts.
func (u *nativeUI) uxmPackClaudeOK(id string) bool {
	library, ok := u.packLibrary(id)
	return ok && slices.ContainsFunc(library.Models, func(item modelLibraryItem) bool {
		return nativeClaudeDesktopModelAllowed(item.ID, u.claudeDesktopExperimentalModels())
	})
}

// uxmUseMenuItems is the "Use in…" menu: one row per harness with what it
// uses now. Choosing a harness that already uses this pack returns it to
// "Your models".
func (u *nativeUI) uxmUseMenuItems(id string) func() []layout.Widget {
	return func() []layout.Widget {
		p := u.packsState()
		name := u.packName(id)
		canAssign := u.uxmPackCanAssign(id)
		items := []layout.Widget{u.uxmMenuTitle(u.tr("Which app uses this pack?", "¿Qué app usa este pack?"))}
		for _, agent := range uxmPackAgents {
			agent := agent
			assigned := p.file.Assignments[agent.key]
			spec := uxmMenuItemSpec{label: agent.name, trailing: u.tr("Your models", "Tus modelos"), disabled: !canAssign}
			switch {
			case assigned == id:
				spec.trailing, spec.selected, spec.disabled = u.tr("✓ This pack", "✓ Este pack"), true, false
			case assigned != "":
				where := u.tr("Packs · ", "Packs · ")
				if p.file.personalPack(assigned) != nil {
					where = u.tr("My packs · ", "Mis packs · ")
				}
				other := u.packName(assigned)
				if other == "" {
					other = assigned
				}
				spec.trailing = where + other
			}
			if agent.key == "claude-desktop" && assigned != id && !u.uxmPackClaudeOK(id) {
				spec.trailing, spec.disabled = u.tr("Claude models only", "Solo modelos Claude"), true
			}
			items = append(items, u.uxmMenuItem("packs.use."+id+"."+agent.key, spec, func() {
				if p.file.Assignments[agent.key] == id {
					u.clearPackAssignment(agent.key)
					if p.file.Assignments[agent.key] == "" {
						u.setNotice(nativeToneSuccess, fmt.Sprintf(u.tr("✓ %s will use your models the next time you open it.", "✓ %s usará tus modelos la próxima vez que lo abras."), agent.name))
					}
					return
				}
				u.assignPack(id, agent.key)
				if p.file.Assignments[agent.key] == id {
					u.setNotice(nativeToneSuccess, fmt.Sprintf(u.tr("✓ %s will use “%s” the next time you open it.", "✓ %s usará “%s” la próxima vez que lo abras."), agent.name, name))
				}
			}))
		}
		items = append(items, u.uxmMenuNote(u.tr("Applies the next time you open the app.", "Se aplica la próxima vez que abras la app.")))
		return items
	}
}

// uxmUseMenu is the black "Use in… ▾" trigger with its menu.
func (u *nativeUI) uxmUseMenu(id string) layout.Widget {
	menuID := "packs.use." + id
	return u.uxmPopover(menuID, 400, true, u.uxmDropdown(menuID, u.tr("Use in…", "Usar en…"), true), u.uxmUseMenuItems(id))
}

// uxmPackFooter is the detail footer: harnesses using the pack and actions.
func (u *nativeUI) uxmPackFooter(id string, actions ...layout.Widget) layout.Widget {
	users := u.uxmPackUsers(id)
	chips := []layout.Widget{}
	for _, name := range users {
		chips = append(chips, u.uxmHarnessChip(name))
	}
	if len(chips) == 0 {
		chips = append(chips, u.uxmSubtle(13, u.tr("No app yet · they use your models", "Ninguna app todavía · usan tus modelos")))
	}
	left := uxmStack(u.eyebrow(u.tr("Used by", "Lo usan")), uxmGap(6), u.pills(chips...))
	right := uxmHStack(12, actions...)
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(uxmHRule(nativeLine, nativeInk)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 16, Bottom: 16, Left: 24, Right: 24}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					if gtx.Constraints.Max.X < gtx.Dp(520) {
						return uxmStack(left, uxmGap(12), right)(gtx)
					}
					return layout.Flex{Alignment: layout.End}.Layout(gtx,
						layout.Flexed(1, left),
						layout.Rigid(layout.Spacer{Width: 16}.Layout),
						layout.Rigid(right),
					)
				})
			}),
		)
	}
}

// uxmDashedLink is inline text with a dashed underline: a value edited on click.
func (u *nativeUI) uxmDashedLink(id, label, text string, action func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := u.clickable(id)
		for b.Clicked(gtx) {
			if gtx.Enabled() && action != nil {
				action()
			}
		}
		gtx.Constraints.Min.X = 0
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(semantic.Button).Add(gtx.Ops)
			semantic.LabelOp(label).Add(gtx.Ops)
			dims := u.textStyle(14, text, nativeInk, font.Normal)(gtx)
			y := dims.Size.Y + gtx.Dp(1)
			dash, gap := gtx.Dp(4), gtx.Dp(2)
			for x := 0; x < dims.Size.X; x += dash + gap {
				paint.FillShape(gtx.Ops, nativeInk, clip.Rect{Min: image.Pt(x, y), Max: image.Pt(min(x+dash, dims.Size.X), y+gtx.Dp(1))}.Op())
			}
			dims.Size.Y += gtx.Dp(3)
			return dims
		})
	}
}

// uxmPackModelCell shows a pack model, or its ID when the catalog lacks it.
func (u *nativeUI) uxmPackModelCell(id string, isDefault bool) layout.Widget {
	var badge layout.Widget
	if isDefault {
		badge = u.uxmDefaultBadge()
	}
	m, ok := u.packCatalogModel(id)
	if !ok {
		return uxmStack(u.uxmModelCell(id, "", id, badge), u.uxmSubtle(12, u.tr("Not in the current catalog", "No figura en el catálogo actual")))
	}
	return u.uxmModelCell(uxmSetupModelName(m), modelLabLabel(modelLab(m)), id, badge)
}

func (u *nativeUI) uxmPackContext(id string) string {
	if m, ok := u.packCatalogModel(id); ok && m.ContextWindow > 0 {
		return nativeContextTokens(m.ContextWindow)
	}
	return "—"
}

func (u *nativeUI) uxmPackPrice(id string) string {
	if m, ok := u.packCatalogModel(id); ok {
		return u.uxmPrice(m)
	}
	return "—"
}

// uxmPackHeader is the title block of a pack detail.
func (u *nativeUI) uxmPackHeader(title layout.Widget, lines ...layout.Widget) layout.Widget {
	body := append([]layout.Widget{title}, lines...)
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 22, Bottom: 22, Left: 24, Right: 24}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, uxmSpaced(8, body)...)
		})
	}
}

// uxmDuplicatePersonalPack copies a personal pack under a new name.
func (u *nativeUI) uxmDuplicatePersonalPack(id string) {
	p := u.packsState()
	source := p.file.personalPack(id)
	if source == nil {
		u.noticeError(errors.New(u.tr("Pack not found", "Pack no encontrado")))
		return
	}
	newID, err := newPersonalPackID()
	if err != nil {
		u.noticeError(err)
		return
	}
	name := fmt.Sprintf(u.tr("%s (copy)", "%s (copia)"), source.Name)
	if len([]rune(name)) > 80 {
		name = string([]rune(name)[:80])
	}
	clone := personalModelPack{ID: newID, Name: name, BasedOn: source.BasedOn, Library: cloneModelLibrary(source.Library), Roles: uxmCloneRoles(source.Roles)}
	if u.changePacks(func(file *modelPacksFile) error {
		file.Personal = append(file.Personal, clone)
		return nil
	}) {
		p.tab, p.selected, p.nameEditing = "mine", newID, ""
		u.setNotice(nativeToneSuccess, fmt.Sprintf(u.tr("✓ “%s” created.", "✓ “%s” creado."), name))
	}
}

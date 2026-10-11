//go:build desktop

package main

import (
	"fmt"

	"gioui.org/font"
	"gioui.org/layout"
)

// uxhDetail collects what a harness adds to the shared detail page layout.
type uxhDetail struct {
	// status sits under the header: profile state, hints and errors.
	status []layout.Widget
	// onOpen are extra "When opening" rows.
	onOpen []layout.Widget
	// blocks are harness-specific blocks between "When opening" and "Advanced".
	blocks []layout.Widget
	// advanced are extra "Advanced" rows, placed before the generic ones.
	advanced []layout.Widget
	// prepare overrides whether "Prepare without opening" is possible.
	canPrepare  *bool
	prepareNote string
	// noPrepare hides the prepare row (Open Design prepares only on launch).
	noPrepare bool
}

// uxhBlock is a titled box: an ivory header strip and rows split by 1px rules.
func (u *nativeUI) uxhBlock(title, aside string, rows ...layout.Widget) layout.Widget {
	header := func(gtx layout.Context) layout.Dimensions {
		return nativeBox(gtx, nativeIvory, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Top: 14, Bottom: 14, Left: 18, Right: 18}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, u.textStyle(16, title, nativeText, font.SemiBold)),
					layout.Rigid(u.textStyle(13, aside, nativeTextSubtle, font.Normal)),
				)
			})
		})
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: 18}.Layout(gtx, uxhFrame(func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{layout.Rigid(header), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return nativeRule(gtx, nativeLine) })}
			children = append(children, layout.Rigid(uxhStack(rows...)))
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		}))
	}
}

// uxhCell pads one block row.
func uxhCell(content layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Top: 14, Bottom: 14, Left: 18, Right: 18}.Layout(gtx, content)
	}
}

// uxhKV is a "label | value | action" row.
func (u *nativeUI) uxhKV(label string, value layout.Widget, actions ...layout.Widget) layout.Widget {
	return uxhCell(func(gtx layout.Context) layout.Dimensions {
		key := u.textStyle(14, label, nativeTextMuted, font.Normal)
		if gtx.Constraints.Max.X < gtx.Dp(560) {
			return u.column(key, value, u.pills(actions...))(gtx)
		}
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(180)
				gtx.Constraints.Max.X = gtx.Dp(180)
				return key(gtx)
			}),
			layout.Rigid(layout.Spacer{Width: 16}.Layout),
			layout.Flexed(1, value),
		}
		if len(actions) > 0 {
			children = append(children, layout.Rigid(layout.Spacer{Width: 16}.Layout), layout.Rigid(u.pills(actions...)))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	})
}

// uxhValue is the emphasised value text of a row.
func (u *nativeUI) uxhValue(text string) layout.Widget {
	return u.textStyle(15, text, nativeText, font.Medium)
}

// uxhAdvancedRow is a "title + description | action" row; extra widgets sit
// under the description.
func (u *nativeUI) uxhAdvancedRow(title, description string, extra []layout.Widget, actions ...layout.Widget) layout.Widget {
	text := []layout.Widget{u.textStyle(15, title, nativeText, font.Medium)}
	if description != "" {
		text = append(text, u.textStyle(12, description, nativeTextSubtle, font.Normal))
	}
	text = append(text, extra...)
	left := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, uxhRigid(text, 4)...)
	}
	return uxhCell(func(gtx layout.Context) layout.Dimensions {
		if len(actions) == 0 {
			return left(gtx)
		}
		if gtx.Constraints.Max.X < gtx.Dp(560) {
			return u.column(left, u.pills(actions...))(gtx)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, left),
			layout.Rigid(layout.Spacer{Width: 16}.Layout),
			layout.Rigid(u.pills(actions...)),
		)
	})
}

// uxhBackLink returns to the Harnesses list.
func (u *nativeUI) uxhBackLink() layout.Widget {
	link := u.buttonWidget("agents.back", "← "+u.tr("Harnesses", "Harnesses"), nativeButtonLink, nil, false, false, func() { u.page = "agents" })
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = 0
			return link(gtx)
		})
	}
}

// uxhDetailHeader is the card at the top of a harness page.
func (u *nativeUI) uxhDetailHeader(key string, l uxhLaunch, detail string) layout.Widget {
	installed := l.install != uxhMissing
	var action layout.Widget
	if installed {
		action = u.disabled(l.canOpen, u.primaryButton("client:"+key+":launch", l.label, func() { u.launchAgent(key) }))
	} else {
		action = u.buttonWidget("client:"+key+":install-header", u.tr("How to install", "Cómo instalarlo"), nativeButtonSecondary, nativeIconOpenInNew, true, false, func() { u.open(u.uxhInstallURL(key)) })
	}
	title := u.column(u.monoStyle(28, uxhName(key), nativeText, font.Bold), u.uxhStatus(l.install, detail))
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 14, Bottom: 22}.Layout(gtx, uxhFrame(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(18).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				head := []layout.FlexChild{
					layout.Rigid(u.uxhMonogram(key, 60, installed)),
					layout.Rigid(layout.Spacer{Width: 18}.Layout),
					layout.Flexed(1, title),
				}
				if gtx.Constraints.Max.X < gtx.Dp(560) {
					return u.column(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx, head...)
					}, action)(gtx)
				}
				head = append(head, layout.Rigid(layout.Spacer{Width: 18}.Layout), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Dp(200)
					return action(gtx)
				}))
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, head...)
			})
		}))
	}
}

// uxhFolderRow is the "Folder" row of "When opening": the general folder or
// this harness's own, with the action to switch between them.
func (u *nativeUI) uxhFolderRow(key string) layout.Widget {
	a := u.agentsState()
	override := u.uxhProjectOverride(key)
	folder := uxhShortPath(u.agentProject(key))
	if folder == "" {
		folder = u.tr("Your home folder", "Tu carpeta personal")
	}
	scope := u.tr(" · the general one", " · la general")
	if override != "" {
		scope = u.tr(" · only here", " · solo aquí")
	}
	value := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Baseline}.Layout(gtx, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx, layout.Rigid(u.uxhValue(folder)), layout.Rigid(u.textStyle(14, scope, nativeTextSubtle, font.Normal)))
		}))
	}
	typeID := "agent:" + key + ":folder-type"
	actions := []layout.Widget{u.disabled(a.FolderBusy == "", u.buttonWidget("agent:"+key+":folder", u.tr("Use another only here", "Usar otra solo aquí"), nativeButtonLink, nil, false, false, func() { u.chooseAgentProject(key) }))}
	if override != "" {
		actions = append(actions, u.buttonWidget("agent:"+key+":folder-reset", u.tr("Use the general one", "Usar la general"), nativeButtonLink, nil, false, false, func() { u.uxhSetProject(key, "") }))
	}
	actions = append(actions, u.disclosure(typeID, u.tr("Type", "Escribir")))
	rows := []layout.Widget{u.uxhKV(u.tr("Folder", "Carpeta"), value, actions...)}
	if u.expanded[typeID] {
		rows[0] = u.column(rows[0], uxhCell(u.field(agentProjectField(key), u.tr("Folder for this harness only (empty = the general one)", "Carpeta solo para este harness (vacía = la general)"), u.uxhGlobalFolder(), false)))
	}
	return u.column(rows...)
}

// uxhModelsRow is the "Models" row of "When opening".
func (u *nativeUI) uxhModelsRow(key string, summary string) layout.Widget {
	actions := []layout.Widget{u.buttonWidget("agents.models.edit", u.tr("Edit in Models", "Editar en Modelos"), nativeButtonLink, nil, false, false, func() { u.page = "models" })}
	if _, supported := packAgentNames[key]; supported {
		actions = append(actions, u.buttonWidget("agent:"+key+":pack", u.tr("Choose model pack", "Elegir pack de modelos"), nativeButtonLink, nil, false, false, func() {
			u.page = "models"
			u.packsState().tab = "packs"
			u.setValue("packs.agent", key)
		}))
	}
	value := []layout.Widget{u.uxhValue(summary)}
	if pack := u.assignedPackName(key); pack != "" {
		value = append(value, u.textStyle(12, fmt.Sprintf(u.tr("Model pack: %s · applied on next open", "Pack de modelos: %s · se aplicará al volver a abrir"), pack), nativeTextSubtle, font.Normal))
	}
	return u.uxhKV(u.tr("Models", "Modelos"), func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, uxhRigid(value, 4)...)
	}, actions...)
}

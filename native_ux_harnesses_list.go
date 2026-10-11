//go:build desktop

package main

import (
	"fmt"
	"image"
	"strings"
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

const uxhFolderMenuID = "agents:folder"

// uxhFrame is the 2px black frame shared by the top bar, lists and blocks.
func uxhFrame(content layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return nativeBox(gtx, nativeSurface, func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: nativeInk, Width: nativeLine}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(nativeLine).Layout(gtx, content)
			})
		})
	}
}

// uxhStack lays children out vertically, separated by 1px black rules.
func uxhStack(children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		flex := make([]layout.FlexChild, 0, len(children)*2)
		for i, child := range children {
			if i > 0 {
				flex = append(flex, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return nativeRule(gtx, 1) }))
			}
			flex = append(flex, layout.Rigid(child))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, flex...)
	}
}

// uxhTopBar shows what every harness opens with: the shared models and the
// general project folder, each with its one action.
func (u *nativeUI) uxhTopBar() layout.Widget {
	a := u.agentsState()
	folder := uxhShortPath(u.uxhGlobalFolder())
	if folder == "" {
		folder = u.tr("Your home folder", "Tu carpeta personal")
	}
	models := u.uxhTopCell(u.tr("Models they will use", "Modelos que usarán"), u.uxhModelsSummary(),
		u.buttonWidget("agents:edit-models", u.tr("Edit models", "Editar modelos"), nativeButtonLink, nil, false, false, func() { u.page = "models" }))
	change := u.buttonWidget(uxhFolderMenuID, u.tr("Change", "Cambiar"), nativeButtonLink, nativeIconExpandMore, true, u.expanded[uxhFolderMenuID], func() {
		u.expanded[uxhFolderMenuID] = !u.expanded[uxhFolderMenuID]
	})
	project := u.uxhTopCell(u.tr("Project folder", "Carpeta del proyecto"), folder, u.disabled(a.FolderBusy == "", change))
	return func(gtx layout.Context) layout.Dimensions {
		bar := uxhFrame(func(gtx layout.Context) layout.Dimensions {
			if gtx.Constraints.Max.X < gtx.Dp(600) {
				return uxhStack(models, project)(gtx)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, models),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					size := image.Pt(gtx.Dp(nativeLine), gtx.Dp(64))
					paint.FillShape(gtx.Ops, nativeInk, clip.Rect{Max: size}.Op())
					return layout.Dimensions{Size: size}
				}),
				layout.Flexed(1, project),
			)
		})
		dims := bar(gtx)
		if u.expanded[uxhFolderMenuID] {
			width := min(gtx.Dp(420), dims.Size.X)
			u.uxhOverlay(gtx, image.Pt(dims.Size.X-width, dims.Size.Y+gtx.Dp(4)), width, u.uxhFolderMenu())
		}
		return dims
	}
}

func (u *nativeUI) uxhTopCell(eyebrow, value string, action layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 14, Bottom: 14, Left: 18, Right: 18}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, u.column(u.eyebrow(eyebrow), u.textStyle(15, value, nativeText, font.SemiBold))),
				layout.Rigid(layout.Spacer{Width: 12}.Layout),
				layout.Rigid(action),
			)
		})
	}
}

// uxhOverlay draws a menu above the page at offset (relative to the current
// widget) and closes it on any click outside. Deferred ops paint after the
// rest of the frame, so the menu is never covered by later rows.
func (u *nativeUI) uxhOverlay(gtx layout.Context, offset image.Point, width int, menu layout.Widget) {
	macro := op.Record(gtx.Ops)
	scrim := u.clickable(uxhFolderMenuID + ":scrim")
	for scrim.Clicked(gtx) {
		u.expanded[uxhFolderMenuID] = false
	}
	far := gtx.Dp(4000)
	shift := op.Offset(image.Pt(-far, -far)).Push(gtx.Ops)
	sgtx := gtx
	sgtx.Constraints = layout.Exact(image.Pt(2*far, 2*far))
	scrim.Layout(sgtx, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} })
	shift.Pop()
	at := op.Offset(offset).Push(gtx.Ops)
	mgtx := gtx
	mgtx.Constraints = layout.Constraints{Min: image.Pt(width, 0), Max: image.Pt(width, gtx.Dp(2000))}
	nativeHardShadowSized(mgtx, 6, func(gtx layout.Context) layout.Dimensions {
		return uxhFrame(menu)(gtx)
	})
	at.Pop()
	op.Defer(gtx.Ops, macro.Stop())
}

// uxhFolderMenu lists recent folders, a system chooser and a typed path.
func (u *nativeUI) uxhFolderMenu() layout.Widget {
	a := u.agentsState()
	current := u.uxhGlobalFolder()
	recent := append([]string(nil), a.Preferences.Recent...)
	if len(recent) == 0 && current != "" {
		recent = []string{current}
	}
	items := []layout.Widget{func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 12, Left: 16, Right: 16, Bottom: 4}.Layout(gtx, u.eyebrow(u.tr("Recent", "Recientes")))
	}}
	for i, path := range recent {
		path := path
		when := ""
		if at, ok := a.Preferences.RecentUsed[path]; ok && at > 0 {
			when = u.uxhAgo(time.Unix(at, 0), a.now())
		}
		items = append(items, u.uxhMenuRow(fmt.Sprintf("%s:recent:%d", uxhFolderMenuID, i), uxhShortPath(path), when, path == current, func() {
			u.expanded[uxhFolderMenuID] = false
			if err := u.uxhUseGlobalFolder(path); err != nil {
				a.Error = nativeMessage(err.Error(), u.language)
			}
		}))
	}
	choose := u.iconButton(uxhFolderMenuID+":choose", u.tr("Choose another folder…", "Elegir otra carpeta…"), nativeButtonGhost, nativeIconFolder, func() {
		u.expanded[uxhFolderMenuID] = false
		u.uxhChooseGlobalFolder()
	})
	typed := u.actionRow(u.field(uxhFolderMenuID+":path", u.tr("Or type a path", "O escribe una ruta"), "~/Code/project", false),
		u.button(uxhFolderMenuID+":use", u.tr("Use", "Usar"), func() {
			typed := strings.TrimSpace(u.value(uxhFolderMenuID + ":path"))
			if typed == "" {
				return
			}
			if err := u.uxhUseGlobalFolder(typed); err != nil {
				a.Error = nativeMessage(err.Error(), u.language)
				return
			}
			u.setValue(uxhFolderMenuID+":path", "")
			u.expanded[uxhFolderMenuID] = false
		}))
	footer := u.column(choose, typed, u.note(u.tr("Used by coding harnesses. You can set another one in each harness's settings.", "Se usa en los harnesses de código. Puedes fijar otra en los ajustes de cada uno.")))
	return func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, len(items)+2)
		for _, item := range items {
			children = append(children, layout.Rigid(item))
		}
		children = append(children,
			layout.Rigid(layout.Spacer{Height: 8}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return nativeRule(gtx, nativeLine) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.UniformInset(12).Layout(gtx, footer) }),
		)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}
}

// uxhMenuRow is one choice in a dropdown. The current choice is citrus and
// carries a check mark, so selection never depends on colour alone.
func (u *nativeUI) uxhMenuRow(id, label, caption string, selected bool, action func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := u.clickable(id)
		for b.Clicked(gtx) {
			if action != nil {
				action()
			}
		}
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(semantic.Button).Add(gtx.Ops)
			semantic.LabelOp(label).Add(gtx.Ops)
			if selected {
				semantic.SelectedOp(true).Add(gtx.Ops)
			}
			background := nativeSurface
			if selected {
				background = nativeCitrus
			} else if b.Hovered() {
				background = nativeIvory
			}
			return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				weight := font.Normal
				if selected {
					weight = font.SemiBold
				}
				return layout.Inset{Top: 10, Bottom: 10, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							size := image.Pt(gtx.Dp(16), gtx.Dp(16))
							if selected {
								gtx.Constraints.Min = size
								return nativeIconCheck.Layout(gtx, nativeInk)
							}
							return layout.Dimensions{Size: size}
						}),
						layout.Rigid(layout.Spacer{Width: 8}.Layout),
						layout.Flexed(1, u.textStyle(14, label, nativeText, weight)),
						layout.Rigid(layout.Spacer{Width: 12}.Layout),
						layout.Rigid(u.textStyle(12, caption, nativeTextSubtle, font.Normal)),
					)
				})
			})
		})
	}
}

// uxhGroupHeader is a group's eyebrow with its caption on the right.
func (u *nativeUI) uxhGroupHeader(title, caption string, extra ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 10, Bottom: 0}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{layout.Rigid(u.eyebrow(title)), layout.Rigid(layout.Spacer{Width: 16}.Layout)}
			if gtx.Constraints.Max.X >= gtx.Dp(560) {
				children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.E.Layout(gtx, u.textStyle(13, caption, nativeTextSubtle, font.Normal))
				}))
			} else {
				children = append(children, layout.Flexed(1, layout.Spacer{}.Layout))
			}
			for _, w := range extra {
				children = append(children, layout.Rigid(layout.Spacer{Width: 12}.Layout), layout.Rigid(w))
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
		})
	}
}

// uxhRow is one harness in the list: monogram, name and purpose, last use,
// installation state, Open and a settings button.
func (u *nativeUI) uxhRow(key string) layout.Widget {
	l := u.uxhLaunchState(key)
	name := uxhName(key)
	installed := l.install != uxhMissing
	info := []layout.Widget{u.textStyle(15, name, nativeText, font.SemiBold)}
	if purpose := u.agentPurpose(key); purpose != "" {
		info = append(info, u.textStyle(12, purpose, nativeTextSubtle, font.Normal))
	}
	if l.local && installed && l.reason != "" {
		info = append(info, u.hint(l.reason))
	}
	if key == "claude" && u.agentsState().ClaudeError != "" {
		info = append(info, u.message(nativeToneError, u.agentsState().ClaudeError))
	}
	last := u.uxhLastUsed(key)
	status := u.uxhStatus(l.install, "")
	var action layout.Widget
	side := []layout.Widget{}
	if installed {
		action = u.disabled(l.canOpen, u.primaryButton("agent:"+key+":launch", l.label, func() { u.launchAgent(key) }))
		if l.install == uxhInstalled && !u.nativeLaunchAvailable(key) {
			side = append(side, u.disabled(!u.busy["GET"+nativeLaunchEndpoint], u.uxhSquareButton("agent:"+key+":detect-visible", u.tr("Check again", "Comprobar de nuevo"), nativeIconRefresh, func() { u.refreshAgentInstallation(key) })))
		}
		side = append(side, u.uxhSquareButton("agent:"+key+":settings", fmt.Sprintf(u.tr("%s settings", "Ajustes de %s"), name), nativeIconSettings, func() { u.agentSetup(key) }))
	} else {
		action = u.buttonWidget("agent:"+key+":install", u.tr("How to install", "Cómo instalarlo"), nativeButtonSecondary, nativeIconOpenInNew, true, false, func() { u.open(u.uxhInstallURL(key)) })
		if key == "codex" {
			side = append(side, u.disabled(u.agentsState().FolderBusy == "", u.uxhSquareButton("agent:codex:locate", u.tr("Locate Codex", "Localizar Codex"), nativeIconFolder, u.locateCodexApplication)))
		}
		side = append(side, u.disabled(!u.busy["GET"+nativeLaunchEndpoint], u.uxhSquareButton("agent:"+key+":detect-visible", u.tr("Check again", "Comprobar de nuevo"), nativeIconRefresh, func() { u.refreshAgentInstallation(key) })))
	}
	return func(gtx layout.Context) layout.Dimensions {
		background := nativeSurface
		if !installed {
			background = nativeIvory
		}
		return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Top: 14, Bottom: 14, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				width := gtx.Constraints.Max.X
				wide, narrow := width >= gtx.Dp(820), width < gtx.Dp(520)
				lines := append([]layout.Widget(nil), info...)
				if !wide {
					meta := status
					if last != "" && installed {
						meta = func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Rigid(status), layout.Rigid(u.textStyle(12, " · "+last, nativeTextSubtle, font.Normal)))
						}
					}
					lines = append(lines, meta)
				}
				text := layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, uxhRigid(lines, 3)...)
				})
				head := []layout.FlexChild{layout.Rigid(u.uxhMonogram(key, 44, installed)), layout.Rigid(layout.Spacer{Width: 16}.Layout), text}
				controls := []layout.FlexChild{}
				if wide {
					controls = append(controls,
						layout.Rigid(layout.Spacer{Width: 16}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Dp(120)
							return layout.E.Layout(gtx, u.textStyle(12, last, nativeTextSubtle, font.Normal))
						}),
						layout.Rigid(layout.Spacer{Width: 16}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Dp(140)
							return status(gtx)
						}),
					)
				}
				controls = append(controls, layout.Rigid(layout.Spacer{Width: 12}.Layout), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if wide {
						gtx.Constraints.Min.X = gtx.Dp(200)
					}
					return action(gtx)
				}))
				for _, w := range side {
					controls = append(controls, layout.Rigid(layout.Spacer{Width: 8}.Layout), layout.Rigid(w))
				}
				if narrow {
					return u.column(
						func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx, head...)
						},
						u.pills(append([]layout.Widget{action}, side...)...),
					)(gtx)
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, append(head, controls...)...)
			})
		})
	}
}

// uxhRigid wraps widgets as rigid flex children separated by gap dp.
func uxhRigid(children []layout.Widget, gap unit.Dp) []layout.FlexChild {
	flex := make([]layout.FlexChild, 0, len(children)*2)
	for i, child := range children {
		if i > 0 {
			flex = append(flex, layout.Rigid(layout.Spacer{Height: gap}.Layout))
		}
		flex = append(flex, layout.Rigid(child))
	}
	return flex
}

// uxhListPage is the Harnesses page: what they open with, then every
// harness grouped by kind, with missing ones collected at the end.
func (u *nativeUI) uxhListPage() layout.Widget {
	a, c := u.agentsState(), u.clientState()
	widgets := []layout.Widget{}
	if c.LaunchError != "" {
		widgets = append(widgets, u.message(nativeToneError, c.LaunchError))
	}
	if a.Error != "" {
		widgets = append(widgets, u.message(nativeToneError, a.Error))
	}
	if u.setupNeeded() {
		widgets = append(widgets, u.card(u.actionRow(u.column(u.heading(u.tr("Finish setting up your workspace", "Termina de configurar tu espacio")), u.note(u.tr("Connect Kilo or ChatGPT and choose at least one model. We'll guide you through it.", "Conecta Kilo o ChatGPT y elige al menos un modelo. Te guiamos paso a paso."))), u.primaryButton("primary.agents.setup", u.tr("Continue setup", "Continuar configuración"), u.beginSetup))))
	}
	widgets = append(widgets, u.uxhTopBar())
	if reason := u.uxhSharedReason(); reason != "" && !u.setupNeeded() {
		widgets = append(widgets, u.hint(reason))
	}
	missing := []layout.Widget{}
	for _, group := range u.uxhGroups() {
		rows := []layout.Widget{}
		for _, key := range group.keys {
			if u.uxhInstallState(key) == uxhMissing {
				missing = append(missing, u.uxhRow(key))
				continue
			}
			rows = append(rows, u.uxhRow(key))
		}
		if len(rows) == 0 {
			continue
		}
		widgets = append(widgets, u.uxhGroupHeader(u.tr(group.en, group.es), u.tr(group.captionEN, group.captionES)), uxhFrame(uxhStack(rows...)))
	}
	if len(missing) > 0 {
		widgets = append(widgets, u.uxhGroupHeader(fmt.Sprintf(u.tr("Not installed · %d", "No instalados · %d"), len(missing)), ""), uxhFrame(uxhStack(missing...)))
	}
	footer := []layout.Widget{
		u.note(u.tr("More integrations:", "Más integraciones:")),
		u.buttonWidget("agents:xcode", u.tr("Set up Xcode", "Configurar Xcode"), nativeButtonLink, nil, false, false, func() { u.agentSetup("xcode-chat") }),
		u.note("·"),
		u.buttonWidget("agents:other", u.tr("Other clients", "Otros clientes"), nativeButtonLink, nil, false, false, func() { u.agentSetup("other") }),
		u.note(u.tr("(OpenAI-compatible endpoint)", "(endpoint compatible con OpenAI)")),
	}
	widgets = append(widgets, layout.Spacer{Height: 8}.Layout, func(gtx layout.Context) layout.Dimensions {
		if gtx.Constraints.Max.X < gtx.Dp(720) {
			return u.pills(footer...)(gtx)
		}
		children := make([]layout.FlexChild, 0, len(footer)*2)
		for i, w := range footer {
			if i > 0 {
				children = append(children, layout.Rigid(layout.Spacer{Width: 10}.Layout))
			}
			children = append(children, layout.Rigid(w))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	})
	return u.column(widgets...)
}

//go:build desktop

package main

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

func nativeSurface(gtx layout.Context, background color.NRGBA, radius unit.Dp, content layout.Widget) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, background, clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Min}, gtx.Dp(radius)).Op(gtx.Ops))
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(content),
	)
}
func (u *nativeUI) textStyle(size unit.Sp, s string, c color.NRGBA, weight font.Weight) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		l := material.Label(u.theme, size, s)
		l.Color = c
		l.Font.Weight = weight
		return l.Layout(gtx)
	}
}
func (u *nativeUI) eyebrow(s string) layout.Widget {
	return u.textStyle(10, strings.ToUpper(s), nativeColor(0x657184), font.SemiBold)
}
func (u *nativeUI) metric(label, value, caption string) layout.Widget {
	return u.column(u.eyebrow(label), u.textStyle(28, value, nativeColor(0x273448), font.Medium), u.note(caption))
}
func (u *nativeUI) navButton(id, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := u.clickable("nav." + id)
		for b.Clicked(gtx) {
			u.page = id
			if (id == "clients" || id == "models") && len(u.models) == 0 {
				u.refreshModels()
			}
		}
		style := material.Button(u.theme, b, label)
		style.Background = nativeColor(0x18202b)
		style.Color = nativeColor(0xaeb8c8)
		style.CornerRadius = 7
		style.Font.Weight = font.Medium
		style.TextSize = 13
		style.Inset = layout.Inset{Top: 13, Bottom: 13, Left: 13, Right: 13}
		if u.page == id || (id == "settings" && u.page == "connection") || (id == "agents" && u.page == "clients") {
			style.Background = nativeColor(0x303c4f)
			style.Color = nativeColor(0xffffff)
		}
		return style.Layout(gtx)
	}
}
func (u *nativeUI) sidebar(gtx layout.Context) layout.Dimensions {
	connection := u.tr("Ready to connect", "Listo para conectar")
	if nativeBool(u.state, "running") {
		connection = u.tr("Your proxy is running", "Tu proxy está activo")
	} else if u.agentConnectionReady() {
		connection = u.tr("Proxy ready to start", "Proxy listo para arrancar")
	}
	gtx.Constraints.Min.X = gtx.Dp(196)
	gtx.Constraints.Max.X = gtx.Dp(196)
	gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
	return nativeSurface(gtx, nativeColor(0x18202b), 0, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 32, Bottom: 25, Left: 22, Right: 22}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(u.column(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(nativeLogoLayout(32)), layout.Rigid(layout.Spacer{Width: 9}.Layout),
						layout.Flexed(1, u.textStyle(20, "Kilo Proxy", nativeColor(0xffffff), font.SemiBold)),
					)
				}, u.textStyle(9, u.tr("YOUR TEAM. YOUR TOOLS.", "TU EQUIPO. TUS HERRAMIENTAS."), nativeColor(0x8794a8), font.Medium))),
				layout.Rigid(layout.Spacer{Height: 44}.Layout), layout.Rigid(u.eyebrow(u.tr("Workspace", "Espacio de trabajo"))), layout.Rigid(layout.Spacer{Height: 15}.Layout),
				layout.Rigid(u.column(u.navButton("agents", u.tr("Agents", "Agentes")), u.navButton("models", u.tr("Models", "Modelos")), u.navButton("activity", u.tr("Activity", "Actividad")), u.navButton("settings", u.tr("Settings", "Ajustes")))),
				layout.Flexed(1, layout.Spacer{}.Layout),
				layout.Rigid(u.column(u.textStyle(13, connection, nativeColor(0xffffff), font.Medium), u.textStyle(11, u.tr("Local → Kilo → Your team", "Local → Kilo → Tu equipo"), nativeColor(0x9da8ba), font.Normal))),
				layout.Rigid(layout.Spacer{Height: 32}.Layout), layout.Rigid(u.textStyle(10, "KILO PROXY   /   v"+version, nativeColor(0x8794a8), font.Normal)),
			)
		})
	})
}
func (u *nativeUI) statusChip(gtx layout.Context) layout.Dimensions {
	running := nativeBool(u.state, "running")
	background, foreground := nativeColor(0xe9ecf1), nativeColor(0x626b79)
	if running {
		background, foreground = nativeColor(0xe8f0e1), nativeColor(0x327653)
	}
	return nativeSurface(gtx, background, 20, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 8, Bottom: 8, Left: 13, Right: 13}.Layout(gtx, u.textStyle(11, "●  "+u.statusLabel(), foreground, font.Medium))
	})
}
func (u *nativeUI) pageTop(gtx layout.Context) layout.Dimensions {
	titles := map[string][2]string{"settings": {"Settings", "Ajustes"}, "connection": {"Settings", "Ajustes"}, "agents": {"Open your workspace", "Abre tu espacio de trabajo"}, "models": {"Models", "Modelos"}, "clients": {"Agent settings", "Ajustes del agente"}, "activity": {"Activity", "Actividad"}}
	descriptions := map[string][2]string{"settings": {"Your Kilo account, appearance and connection.", "Tu cuenta de Kilo, apariencia y conexión."}, "connection": {"Your Kilo account, appearance and connection.", "Tu cuenta de Kilo, apariencia y conexión."}, "agents": {"Your agents, ready with the same model library.", "Tus agentes, con la misma biblioteca de modelos."}, "models": {"Choose once. Use them across your agents.", "Elige una vez. Úsalos en todos tus agentes."}, "clients": {"Integration options and configuration exports.", "Opciones de integración y exportación de configuración."}, "activity": {"Requests, reported costs and cache reuse.", "Peticiones, costes informados y reutilización de caché."}}
	titles["setup"] = [2]string{"Welcome to Kilo Proxy", "Bienvenido a Kilo Proxy"}
	descriptions["setup"] = [2]string{"Connect your team. Choose models. Open your agents.", "Conecta tu equipo. Elige modelos. Abre tus agentes."}
	if u.page == "agents" && !nativeBool(u.state, "running") && u.agentConnectionReady() {
		descriptions["agents"] = [2]string{"Opening an agent starts the proxy automatically. You can also start it here.", "Abrir un agente arranca el proxy automáticamente. También puedes arrancarlo aquí."}
	}
	title, description := titles[u.page], descriptions[u.page]
	controls := u.statusChip
	if u.page == "agents" {
		controls = u.column(u.statusChip, u.proxyButton())
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, u.column(u.textStyle(26, u.tr(title[0], title[1]), nativeColor(0x252b28), font.SemiBold), u.note(u.tr(description[0], description[1])))),
		layout.Rigid(layout.Spacer{Width: 20}.Layout), layout.Rigid(controls),
	)
}
func (u *nativeUI) Layout(gtx layout.Context) layout.Dimensions {
	u.frameMu.Lock()
	defer u.frameMu.Unlock()
	if u.closing {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}

	u.beginModelMenus(gtx)
	defer u.trackModelMenuPointer(gtx)
	u.drain()
	defer u.persistLibraryEdits()
	u.dismissModelSort(gtx)
	u.laidOut = true
	paint.Fill(gtx.Ops, u.theme.Bg)
	wide := gtx.Constraints.Max.X >= gtx.Dp(940)
	children := []layout.FlexChild{}
	if wide {
		children = append(children, layout.Rigid(u.sidebar))
	}
	children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
		padding := unit.Dp(32)
		if !wide {
			padding = 20
		}
		return layout.Inset{Top: 22, Bottom: 18, Left: padding, Right: padding}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, layout.Flexed(1, u.note(u.tr("Workspace  /  ", "Espacio  /  ")+u.tr(map[string]string{"setup": "Setup", "connection": "Settings", "settings": "Settings", "agents": "Agents", "models": "Models", "clients": "Agent settings", "activity": "Activity"}[u.page], map[string]string{"setup": "Bienvenida", "connection": "Ajustes", "settings": "Ajustes", "agents": "Agentes", "models": "Modelos", "clients": "Ajustes del agente", "activity": "Actividad"}[u.page]))), layout.Rigid(u.button("language", u.tr("English ▾", "Español ▾"), func() {
						lang := "en"
						if u.language == "en" {
							lang = "es"
						}
						u.setLanguage(lang)
					})))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if wide {
						return layout.Dimensions{}
					}
					return layout.Inset{Top: 16}.Layout(gtx, u.pills(u.navButton("agents", u.tr("Agents", "Agentes")), u.navButton("models", u.tr("Models", "Modelos")), u.navButton("activity", u.tr("Activity", "Actividad")), u.navButton("settings", u.tr("Settings", "Ajustes"))))
				}),
				layout.Rigid(layout.Spacer{Height: 20}.Layout), layout.Rigid(u.pageTop), layout.Rigid(layout.Spacer{Height: 20}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if u.notice == "" {
						return layout.Dimensions{}
					}
					return layout.Inset{Bottom: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return nativeSurface(gtx, nativeColor(0xe9eef6), 8, func(gtx layout.Context) layout.Dimensions {
							return layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, layout.Flexed(1, u.note(u.notice)), layout.Rigid(layout.Spacer{Width: 10}.Layout), layout.Rigid(u.button("notice.dismiss", "×", func() { u.notice = "" })))
							})
						})
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					var panel layout.Widget
					switch u.page {
					case "setup":
						panel = u.setupPanel()
					case "agents":
						panel = u.agentsPanel()
					case "models":
						panel = u.modelsPanel()
					case "clients":
						panel = u.clientsPanel()
					case "activity":
						panel = u.activityPanel()
					default:
						panel = u.column(u.connectionPanel(), u.appearancePanel(), u.terminalCommandsPanel())
					}
					return material.List(u.theme, u.list("page."+u.page)).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
						return layout.Inset{Right: 14, Bottom: 20}.Layout(gtx, panel)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: 14}.Layout(gtx, u.note(u.tr("Closing the window keeps your proxy running. Quit from the system tray.", "Cerrar la ventana mantiene el proxy activo. Sal desde la barra del sistema.")))
				}),
			)
		})
	}))
	dims := layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
	u.layoutActiveModelMenu(gtx)
	return dims
}

// pills wraps compact controls at their natural width, avoiding a table of
// stretched buttons when choosing an editor or an Xcode integration.
func (u *nativeUI) pills(children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		x, y, lineHeight := 0, 0, 0
		gap := gtx.Dp(8)
		width := gtx.Constraints.Max.X
		for _, child := range children {
			cgtx := gtx
			cgtx.Constraints.Min = image.Point{}
			recording := op.Record(gtx.Ops)
			dims := child(cgtx)
			call := recording.Stop()
			if x > 0 && x+dims.Size.X > width {
				y += lineHeight + gap
				x = 0
				lineHeight = 0
			}
			at := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			at.Pop()
			x += dims.Size.X + gap
			if dims.Size.Y > lineHeight {
				lineHeight = dims.Size.Y
			}
		}
		return layout.Dimensions{Size: gtx.Constraints.Constrain(image.Pt(width, y+lineHeight))}
	}
}

// Keep form inputs flexible and utility actions at their natural width. Every
// control shares the same lower edge; a field's caption is outside that track.
func (u *nativeUI) actionRow(field layout.Widget, actions ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if gtx.Constraints.Max.X < gtx.Dp(440) {
			return u.column(field, u.pills(actions...))(gtx)
		}
		children := []layout.FlexChild{layout.Flexed(1, field)}
		for _, action := range actions {
			children = append(children, layout.Rigid(layout.Spacer{Width: 12}.Layout), layout.Rigid(action))
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.End}.Layout(gtx, children...)
	}
}
func (u *nativeUI) modelCard(selected bool, children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		background, border := nativeColor(0xffffff), nativeColor(0xdce1e8)
		if selected {
			background = nativeColor(0xffffff)
			border = nativeColor(0x8a9bb7)
		}
		return nativeSurface(gtx, background, 10, func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: border, Width: 1, CornerRadius: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(16).Layout(gtx, u.column(children...))
			})
		})
	}
}

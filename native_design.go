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
	return u.textStyle(10, strings.ToUpper(s), nativeColor(0x768566), font.SemiBold)
}
func (u *nativeUI) metric(label, value, caption string) layout.Widget {
	return u.column(u.eyebrow(label), u.textStyle(28, value, nativeColor(0x293b26), font.Medium), u.note(caption))
}
func (u *nativeUI) navButton(id, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := u.clickable("nav." + id)
		for b.Clicked(gtx) {
			u.page = id
			if id == "clients" && len(u.models) == 0 {
				u.refreshModels()
			}
		}
		style := material.Button(u.theme, b, label)
		style.Background = nativeColor(0x202720)
		style.Color = nativeColor(0xb7c2b1)
		style.CornerRadius = 7
		style.Font.Weight = font.Medium
		style.TextSize = 13
		style.Inset = layout.Inset{Top: 13, Bottom: 13, Left: 13, Right: 13}
		if u.page == id {
			style.Background = nativeColor(0x363e31)
			style.Color = nativeColor(0xe8f36a)
		}
		return style.Layout(gtx)
	}
}
func (u *nativeUI) sidebar(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Dp(210)
	gtx.Constraints.Max.X = gtx.Dp(210)
	gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
	return nativeSurface(gtx, nativeColor(0x202720), 0, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 32, Bottom: 25, Left: 22, Right: 22}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(u.column(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(nativeLogoLayout(32)), layout.Rigid(layout.Spacer{Width: 9}.Layout),
						layout.Flexed(1, u.textStyle(20, "Kilo Proxy", nativeColor(0xf0f4e8), font.SemiBold)),
					)
				}, u.textStyle(9, u.tr("YOUR TEAM. YOUR TOOLS.", "TU EQUIPO. TUS HERRAMIENTAS."), nativeColor(0x9ba58f), font.Medium))),
				layout.Rigid(layout.Spacer{Height: 44}.Layout), layout.Rigid(u.eyebrow(u.tr("Workspace", "Espacio de trabajo"))), layout.Rigid(layout.Spacer{Height: 15}.Layout),
				layout.Rigid(u.column(u.navButton("connection", u.tr("Connection", "Conexión")), u.navButton("clients", u.tr("Clients & models", "Clientes y modelos")), u.navButton("activity", u.tr("Activity & costs", "Actividad y costes")))),
				layout.Flexed(1, layout.Spacer{}.Layout),
				layout.Rigid(u.column(u.textStyle(24, u.tr("One gateway.\nYour way.", "Un gateway.\nA tu manera."), nativeColor(0xe8f36a), font.Medium), u.textStyle(11, u.tr("Local → Kilo → Your team", "Local → Kilo → Tu equipo"), nativeColor(0x9ca98f), font.Normal))),
				layout.Rigid(layout.Spacer{Height: 32}.Layout), layout.Rigid(u.textStyle(10, "KILO PROXY   /   v"+version, nativeColor(0x839178), font.Normal)),
			)
		})
	})
}
func (u *nativeUI) statusChip(gtx layout.Context) layout.Dimensions {
	running := nativeBool(u.state, "running")
	background, foreground := nativeColor(0xebeee5), nativeColor(0x66755b)
	if running {
		background, foreground = nativeColor(0xe8f0e1), nativeColor(0x327653)
	}
	return nativeSurface(gtx, background, 20, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 8, Bottom: 8, Left: 13, Right: 13}.Layout(gtx, u.textStyle(11, "●  "+u.statusLabel(), foreground, font.Medium))
	})
}
func (u *nativeUI) pageTop(gtx layout.Context) layout.Dimensions {
	titles := map[string][2]string{"connection": {"Connect your workspace", "Conecta tu espacio de trabajo"}, "clients": {"Clients & models", "Clientes y modelos"}, "activity": {"Your session, in detail", "Tu sesión, al detalle"}}
	descriptions := map[string][2]string{"connection": {"Your company’s Kilo account, in the tools you love.", "La cuenta de Kilo de tu empresa, en tus herramientas favoritas."}, "clients": {"Pick your models. We’ll prepare the configuration.", "Elige tus modelos. Preparamos la configuración por ti."}, "activity": {"Follow costs, cache reuse and the requests behind them.", "Sigue los costes, la caché y las peticiones de cada conversación."}}
	title, description := titles[u.page], descriptions[u.page]
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, u.column(u.textStyle(29, u.tr(title[0], title[1]), nativeColor(0x252b28), font.SemiBold), u.note(u.tr(description[0], description[1])))),
		layout.Rigid(layout.Spacer{Width: 20}.Layout), layout.Rigid(u.statusChip),
	)
}
func (u *nativeUI) Layout(gtx layout.Context) layout.Dimensions {
	u.drain()
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
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, layout.Flexed(1, u.note(u.tr("Workspace  /  ", "Espacio  /  ")+u.tr(map[string]string{"connection": "Connection", "clients": "Clients", "activity": "Activity"}[u.page], map[string]string{"connection": "Conexión", "clients": "Clientes", "activity": "Actividad"}[u.page]))), layout.Rigid(u.button("language", u.tr("English ▾", "Español ▾"), func() {
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
					return layout.Inset{Top: 16}.Layout(gtx, u.row(u.navButton("connection", u.tr("Connection", "Conexión")), u.navButton("clients", u.tr("Clients", "Clientes")), u.navButton("activity", u.tr("Activity", "Actividad"))))
				}),
				layout.Rigid(layout.Spacer{Height: 20}.Layout), layout.Rigid(u.pageTop), layout.Rigid(layout.Spacer{Height: 20}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if u.notice == "" {
						return layout.Dimensions{}
					}
					return layout.Inset{Bottom: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return nativeSurface(gtx, nativeColor(0xeaf0df), 8, func(gtx layout.Context) layout.Dimensions {
							return layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, layout.Flexed(1, u.note(u.notice)), layout.Rigid(layout.Spacer{Width: 10}.Layout), layout.Rigid(u.button("notice.dismiss", "×", func() { u.notice = "" })))
							})
						})
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					var panel layout.Widget
					switch u.page {
					case "clients":
						panel = u.clientsPanel()
					case "activity":
						panel = u.activityPanel()
					default:
						panel = u.connectionPanel()
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
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
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
		background, border := nativeColor(0xffffff), nativeColor(0xe0e5d8)
		if selected {
			background = nativeColor(0xfcfdf8)
			border = nativeColor(0xb8c99b)
		}
		return nativeSurface(gtx, background, 10, func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: border, Width: 1, CornerRadius: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(16).Layout(gtx, u.column(children...))
			})
		})
	}
}

//go:build desktop

package main

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/font"
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

var (
	// Brutalist palette: black on white, square corners, heavy rules and hard shadows.
	// Citrus (brand) is the single accent and only means "selected / active / running";
	// it always travels with a second cue (check, star, bar or label), never colour alone.
	nativeInk          = nativeColor(0x000000)
	nativeInkSoft      = nativeColor(0x2a2a2a)
	nativeForest       = nativeColor(0x202720)
	nativeCitrus       = nativeColor(0xe8f36a)
	nativeIvory        = nativeColor(0xf4f5ef)
	nativeAccent       = nativeColor(0x000000)
	nativeAccentHover  = nativeForest
	nativeSelected     = nativeCitrus
	nativeBg           = nativeColor(0xffffff)
	nativeSurface      = nativeColor(0xffffff)
	nativeSurfaceAlt   = nativeIvory
	nativeDisabledBG   = nativeColor(0xf0f0f0)
	nativeBorder       = nativeColor(0x000000)
	nativeBorderStrong = nativeColor(0x000000)
	nativeText         = nativeColor(0x000000)
	nativeTextMuted    = nativeColor(0x3d3d3d)
	nativeTextSubtle   = nativeColor(0x62685e)
	nativeTextDisabled = nativeColor(0x767676)
	nativeSidebarText  = nativeColor(0xbdbdbd)

	nativeInfoFG      = nativeColor(0x2b5277)
	nativeInfoBG      = nativeColor(0xeaf1f8)
	nativeInfoBorder  = nativeInfoFG
	nativeSuccessFG   = nativeColor(0x1e6b43)
	nativeSuccessBG   = nativeColor(0xe7f4ec)
	nativeSuccessLine = nativeSuccessFG
	nativeWarningFG   = nativeColor(0x8a5a00)
	nativeWarningBG   = nativeColor(0xfff5da)
	nativeWarningLine = nativeWarningFG
	nativeErrorFG     = nativeColor(0xb42318)
	nativeErrorBG     = nativeColor(0xfdeceb)
	nativeErrorLine   = nativeErrorFG

	nativeIconInfo         = mustNativeIcon(icons.ActionInfo)
	nativeIconSuccess      = mustNativeIcon(icons.ActionCheckCircle)
	nativeIconWarning      = mustNativeIcon(icons.AlertWarning)
	nativeIconError        = mustNativeIcon(icons.AlertError)
	nativeIconCheck        = mustNativeIcon(icons.NavigationCheck)
	nativeIconExpandMore   = mustNativeIcon(icons.NavigationExpandMore)
	nativeIconChevronRight = mustNativeIcon(icons.NavigationChevronRight)
	nativeIconClose        = mustNativeIcon(icons.NavigationClose)
	nativeIconOpenInNew    = mustNativeIcon(icons.ActionOpenInNew)
	nativeIconAgents       = mustNativeIcon(icons.ActionLaunch)
	nativeIconModels       = mustNativeIcon(icons.ActionViewModule)
	nativeIconActivity     = mustNativeIcon(icons.ActionTimeline)
	nativeIconSettings     = mustNativeIcon(icons.ActionSettings)
	nativeIconRefresh      = mustNativeIcon(icons.NavigationRefresh)
	nativeIconFolder       = mustNativeIcon(icons.FileFolderOpen)
	nativeIconBack         = mustNativeIcon(icons.NavigationArrowBack)
	nativeIconCopy         = mustNativeIcon(icons.ContentContentCopy)
	nativeIconAdd          = mustNativeIcon(icons.ContentAdd)
	nativeIconSearch       = mustNativeIcon(icons.ActionSearch)
	nativeIconDelete       = mustNativeIcon(icons.ActionDelete)
	nativeIconStar         = mustNativeIcon(icons.ToggleStar)
	nativeIconStarOutline  = mustNativeIcon(icons.ToggleStarBorder)
)

func mustNativeIcon(data []byte) *widget.Icon {
	icon, err := widget.NewIcon(data)
	if err != nil {
		panic(err)
	}
	return icon
}

type nativeTone uint8

const (
	nativeToneNeutral nativeTone = iota
	nativeToneInfo
	nativeToneSuccess
	nativeToneWarning
	nativeToneError
)

type nativeToneColors struct{ fg, bg, border color.NRGBA }

func nativeTonePalette(tone nativeTone) nativeToneColors {
	switch tone {
	case nativeToneInfo:
		return nativeToneColors{nativeInfoFG, nativeInfoBG, nativeInfoBorder}
	case nativeToneSuccess:
		return nativeToneColors{nativeSuccessFG, nativeSuccessBG, nativeSuccessLine}
	case nativeToneWarning:
		return nativeToneColors{nativeWarningFG, nativeWarningBG, nativeWarningLine}
	case nativeToneError:
		return nativeToneColors{nativeErrorFG, nativeErrorBG, nativeErrorLine}
	default:
		return nativeToneColors{nativeTextMuted, nativeSurfaceAlt, nativeBorder}
	}
}

func nativeToneIcon(tone nativeTone) *widget.Icon {
	switch tone {
	case nativeToneInfo:
		return nativeIconInfo
	case nativeToneSuccess:
		return nativeIconSuccess
	case nativeToneWarning:
		return nativeIconWarning
	case nativeToneError:
		return nativeIconError
	default:
		return nativeIconInfo
	}
}

// nativeBox paints a square background behind content.
func nativeBox(gtx layout.Context, background color.NRGBA, content layout.Widget) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, background, clip.Rect{Max: gtx.Constraints.Min}.Op())
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(content),
	)
}

// nativeHardShadow draws a solid offset shadow; content must paint an opaque background.
func nativeHardShadow(gtx layout.Context, content layout.Widget) layout.Dimensions {
	return nativeHardShadowSized(gtx, nativeShadow, content)
}

func nativeHardShadowSized(gtx layout.Context, size unit.Dp, content layout.Widget) layout.Dimensions {
	offset := gtx.Dp(size)
	gtx.Constraints.Max.X, gtx.Constraints.Max.Y = max(0, gtx.Constraints.Max.X-offset), max(0, gtx.Constraints.Max.Y-offset)
	gtx.Constraints.Min.X, gtx.Constraints.Min.Y = min(gtx.Constraints.Min.X, gtx.Constraints.Max.X), min(gtx.Constraints.Min.Y, gtx.Constraints.Max.Y)
	recording := op.Record(gtx.Ops)
	dims := content(gtx)
	call := recording.Stop()
	shift := image.Pt(offset, offset)
	paint.FillShape(gtx.Ops, nativeInk, clip.Rect{Min: shift, Max: dims.Size.Add(shift)}.Op())
	call.Add(gtx.Ops)
	return layout.Dimensions{Size: dims.Size.Add(shift), Baseline: dims.Baseline}
}

// monoStyle sets display text (titles, headings, labels) in Geist Mono, the brutalist typeface.
func (u *nativeUI) monoStyle(size unit.Sp, s string, c color.NRGBA, weight font.Weight) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		l := material.Label(u.theme, size, s)
		l.Color = c
		l.Font.Typeface = nativeMonoFace
		l.Font.Weight = weight
		return l.Layout(gtx)
	}
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
	return u.monoStyle(12, strings.ToUpper(s), nativeTextSubtle, font.Bold)
}
func (u *nativeUI) metric(label, value, caption string) layout.Widget {
	return u.column(u.eyebrow(label), u.monoStyle(28, value, nativeText, font.Bold), u.note(caption))
}

// section is an informational group: a heavy top rule, a heading and a one-line
// purpose. It has no box or shadow; those are reserved for actionable surfaces.
func (u *nativeUI) section(title, description string, children ...layout.Widget) layout.Widget {
	head := []layout.Widget{u.heading(title)}
	if description != "" {
		head = append(head, u.note(description))
	}
	body := u.column(append([]layout.Widget{u.column(head...)}, children...)...)
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Bottom: nativeSpace24}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return nativeRule(gtx, nativeLineHeavy) }),
				layout.Rigid(layout.Spacer{Height: nativeSpace16}.Layout),
				layout.Rigid(body),
			)
		})
	}
}

// nativeRule paints a full-width ink rule.
func nativeRule(gtx layout.Context, thickness unit.Dp) layout.Dimensions {
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(thickness))
	paint.FillShape(gtx.Ops, nativeInk, clip.Rect{Max: size}.Op())
	return layout.Dimensions{Size: size}
}
func (u *nativeUI) navButton(id, label string, icon *widget.Icon) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := u.clickable("nav." + id)
		for b.Clicked(gtx) {
			if gtx.Enabled() {
				u.page = id
				if (id == "clients" || id == "models") && len(u.models) == 0 {
					u.refreshModels()
				}
			}
		}
		active := u.page == id || (id == "settings" && u.page == "connection") || (id == "agents" && u.page == "clients")
		background, foreground := color.NRGBA{}, nativeSidebarText
		if active {
			background, foreground = nativeCitrus, nativeInk
		} else if b.Hovered() {
			background = nativeInkSoft
		}
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		gtx.Constraints.Min.Y = max(gtx.Constraints.Min.Y, gtx.Dp(44))
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(semantic.Button).Add(gtx.Ops)
			semantic.LabelOp(label).Add(gtx.Ops)
			if active {
				semantic.SelectedOp(true).Add(gtx.Ops)
			}
			return layout.Stack{}.Layout(gtx,
				layout.Expanded(func(gtx layout.Context) layout.Dimensions {
					// Paint directly: nativeBox's stacked child would reset the minimum size to zero.
					paint.FillShape(gtx.Ops, background, clip.Rect{Max: gtx.Constraints.Min}.Op())
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Expanded(func(gtx layout.Context) layout.Dimensions {
					if active {
						sz := image.Pt(gtx.Dp(4), gtx.Constraints.Min.Y)
						paint.FillShape(gtx.Ops, nativeInk, clip.Rect{Max: sz}.Op())
					} else if b.Hovered() {
						foreground = nativeIvory
					}
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Stacked(func(gtx layout.Context) layout.Dimensions {
					// Stacked children start at a zero minimum; keep the full-width 44dp row.
					gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, gtx.Dp(44))
					return layout.Inset{Left: 14, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						// Center the row vertically; Flex would pass the 44dp minimum to its children.
						return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, 0)
							return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Min = image.Pt(gtx.Dp(18), gtx.Dp(18))
									return icon.Layout(gtx, foreground)
								}),
								layout.Rigid(layout.Spacer{Width: 12}.Layout),
								layout.Flexed(1, u.textStyle(13, label, foreground, func() font.Weight {
									if active {
										return font.SemiBold
									}
									return font.Medium
								}())),
							)
						})
					})
				}),
			)
		})
	}
}
func (u *nativeUI) sidebar(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Min.X, gtx.Constraints.Max.X = gtx.Dp(224), gtx.Dp(224)
	gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
	return nativeBox(gtx, nativeInk, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 28, Bottom: 22, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(nativeLogoLayout(30)), layout.Rigid(layout.Spacer{Width: 9}.Layout),
						layout.Flexed(1, u.textStyle(18, "Kilo Proxy", nativeSurface, font.SemiBold)),
					)
				}),
				layout.Rigid(layout.Spacer{Height: 28}.Layout),
				layout.Rigid(u.column(u.navButton("agents", u.tr("Harnesses", "Harnesses"), nativeIconAgents), u.navButton("models", u.tr("Models", "Modelos"), nativeIconModels), u.navButton("activity", u.tr("Activity", "Actividad"), nativeIconActivity), u.navButton("settings", u.tr("Settings", "Ajustes"), nativeIconSettings))),
				layout.Flexed(1, layout.Spacer{}.Layout),
				// Navigation stays under the brand; the proxy status is pinned above the footer.
				layout.Rigid(u.proxyStatusBlock()),
				layout.Rigid(layout.Spacer{Height: 16}.Layout),
				layout.Rigid(u.monoStyle(10, "KILO PROXY / v"+version, nativeSidebarText, font.Normal)),
			)
		})
	})
}

// proxyState is the single source for the proxy's visible state everywhere.
func (u *nativeUI) proxyState() (nativeTone, string) {
	switch {
	case u.setupNeeded() || !u.agentConnectionReady():
		return nativeToneWarning, u.tr("Setup needed", "Configuración necesaria")
	case u.busy["POST/api/start"]:
		return nativeToneInfo, u.tr("Starting…", "Arrancando…")
	case u.busy["POST/api/stop"]:
		return nativeToneInfo, u.tr("Stopping…", "Deteniendo…")
	case nativeBool(u.state, "running"):
		return nativeToneSuccess, u.tr("Running", "En marcha")
	}
	return nativeToneNeutral, u.tr("Stopped", "Detenido")
}

// proxyTeam names the organization that pays for requests.
func (u *nativeUI) proxyTeam() string {
	return u.teamName(nativeString(u.state, "orgId"))
}

// teamName names a Kilo team from the loaded list or the name saved with the
// current team. An unknown name shows a shortened ID and asks Kilo for it once
// in the background, so a remembered login does not leave a raw ID on screen.
func (u *nativeUI) teamName(id string) string {
	if id == "" {
		return ""
	}
	for _, raw := range nativeArray(u.state, "organizations") {
		org := nativeMap(raw)
		if nativeString(org, "id") == id && nativeString(org, "name") != "" {
			return nativeString(org, "name")
		}
	}
	if id == nativeString(u.state, "orgId") {
		if name := nativeString(u.state, "orgName"); name != "" {
			return name
		}
	}
	r := []rune(id)
	if len(r) <= 12 {
		return id
	}
	if id == nativeString(u.state, "orgId") {
		u.requestTeamName(id)
	}
	return string(r[:8]) + "…"
}

// nativeEllipsis cuts s to at most n characters, ending in "…".
func nativeEllipsis(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

func (u *nativeUI) requestTeamName(id string) {
	if u.teamNameAsked == id || !nativeBool(u.state, "hasKey") || u.owner == nil {
		return
	}
	u.teamNameAsked = id
	go func() {
		raw, err := nativeRequest(u.owner, "POST", "/api/auth/team-name", map[string]any{})
		if err != nil {
			return // offline or signed out: keep the short ID, no notice
		}
		u.enqueue(func() { u.acceptState(raw) })
	}()
}

// proxyStatusBlock keeps the proxy state, billed team, local endpoint and the
// start/stop action permanently visible in the sidebar.
func (u *nativeUI) proxyStatusBlock() layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		tone, label := u.proxyState()
		running := tone == nativeToneSuccess
		pill := func(gtx layout.Context) layout.Dimensions {
			bg, fg, dot := nativeDisabledBG, nativeInk, nativeInk
			switch tone {
			case nativeToneSuccess:
				bg = nativeCitrus
			case nativeToneWarning:
				bg, fg, dot = nativeWarningBG, nativeWarningFG, nativeWarningFG
			case nativeToneInfo:
				bg, fg, dot = nativeInfoBG, nativeInfoFG, nativeInfoFG
			}
			return nativeBox(gtx, bg, func(gtx layout.Context) layout.Dimensions {
				return widget.Border{Color: nativeInk, Width: 1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: 5, Bottom: 5, Left: 8, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								sz := gtx.Dp(8)
								if running {
									paint.FillShape(gtx.Ops, dot, clip.Ellipse(image.Rectangle{Max: image.Pt(sz, sz)}).Op(gtx.Ops))
								} else {
									paint.FillShape(gtx.Ops, dot, clip.Stroke{Path: clip.Ellipse(image.Rectangle{Min: image.Pt(1, 1), Max: image.Pt(sz-1, sz-1)}).Path(gtx.Ops), Width: float32(gtx.Dp(1.5))}.Op())
								}
								return layout.Dimensions{Size: image.Pt(sz, sz)}
							}),
							layout.Rigid(layout.Spacer{Width: 7}.Layout),
							layout.Rigid(u.monoStyle(12, strings.ToUpper(label), fg, font.Bold)),
						)
					})
				})
			})
		}
		children := []layout.Widget{u.monoStyle(11, u.tr("PROXY", "PROXY"), nativeTextSubtle, font.Bold), pill}
		if team := u.proxyTeam(); team != "" {
			children = append(children, u.column(u.textStyle(11, u.tr("Billed to", "Se factura a"), nativeTextSubtle, font.Normal), u.textStyle(13, nativeEllipsis(team, 40), nativeInk, font.SemiBold)))
		}
		if base := nativeString(u.state, "baseURL"); base != "" {
			children = append(children, u.column(
				u.textStyle(11, u.tr("Local endpoint", "Endpoint local"), nativeTextSubtle, font.Normal),
				u.buttonWidget("sidebar.copy-url", strings.TrimPrefix(base, "http://"), nativeButtonLink, nativeIconCopy, true, false, func() { u.copy(base) }),
			))
		}
		children = append(children, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return u.proxyButton()(gtx)
		})
		return nativeBox(gtx, nativeIvory, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.UniformInset(12).Layout(gtx, u.column(children...))
		})
	}
}
func (u *nativeUI) proxyControl() layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		tone, label := u.proxyState()
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(u.statusBadge(tone, u.tr("Proxy: ", "Proxy: ")+label)),
			layout.Rigid(layout.Spacer{Width: 8}.Layout),
			layout.Rigid(u.proxyButton()),
		)
	}
}
func (u *nativeUI) pageTop(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Min.Y = 0
	gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, gtx.Dp(128))
	titles := map[string][2]string{"settings": {"Settings", "Ajustes"}, "connection": {"Settings", "Ajustes"}, "agents": {"Harnesses", "Harnesses"}, "models": {"Models", "Modelos"}, "clients": {"Harnesses", "Harnesses"}, "activity": {"Activity", "Actividad"}, "setup": {"Welcome to Kilo Proxy", "Bienvenido a Kilo Proxy"}}
	descriptions := map[string][2]string{"settings": {"Your Kilo and ChatGPT accounts, appearance and connection.", "Tus cuentas de Kilo y ChatGPT, apariencia y conexión."}, "connection": {"Your Kilo and ChatGPT accounts, appearance and connection.", "Tus cuentas de Kilo y ChatGPT, apariencia y conexión."}, "agents": {"Your harnesses, ready with the same model library.", "Tus harnesses, con la misma biblioteca de modelos."}, "models": {"Choose once. Use them across your harnesses.", "Elige una vez. Úsalos en todos tus harnesses."}, "clients": {"What it opens with, its models and advanced options.", "Con qué abre, sus modelos y opciones avanzadas."}, "activity": {"Requests, reported costs and cache reuse.", "Peticiones, costes informados y reutilización de caché."}, "setup": {"Connect Kilo or ChatGPT. Choose models. Open your harnesses.", "Conecta Kilo o ChatGPT. Elige modelos. Abre tus harnesses."}}
	if u.page == "agents" && !nativeBool(u.state, "running") && u.agentConnectionReady() {
		descriptions["agents"] = [2]string{"Opening a harness starts the proxy automatically. You can also start it here.", "Abrir un harness arranca el proxy automáticamente. También puedes arrancarlo aquí."}
	}
	title, description := titles[u.page], descriptions[u.page]
	if lead, ok := u.uxsPageLead(); ok {
		description = lead
	}
	controls := func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{} }
	if u.page == "setup" {
		choices := []nativeChoice{{Value: "en", Label: "English"}, {Value: "es", Label: "Español"}}
		controls = u.segmented("language.", choices, u.language, true, u.setLanguage)
	} else if !u.sidebarVisible {
		// Narrow windows hide the sidebar; the proxy state stays in the header.
		controls = u.proxyControl()
	}
	if u.page == "activity" {
		description, controls = u.uxaPageTop(description, controls)
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, u.column(u.monoStyle(28, u.tr(title[0], title[1]), nativeText, font.Bold), u.textStyle(14, u.tr(description[0], description[1]), nativeTextMuted, font.Normal))),
		layout.Rigid(layout.Spacer{Width: 16}.Layout), layout.Rigid(controls),
	)
}
func (u *nativeUI) pageFooter() layout.Widget {
	if u.page == "models" {
		return u.uxmPageFooter()
	}
	if u.page != "setup" {
		return nil
	}
	return u.setupFooter()
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
	paint.Fill(gtx.Ops, nativeBg)
	wide := u.page != "setup" && gtx.Constraints.Max.X >= gtx.Dp(940)
	u.sidebarVisible = wide
	children := []layout.FlexChild{}
	if wide {
		children = append(children, layout.Rigid(u.sidebar))
	}
	children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
		padding := unit.Dp(32)
		if !wide {
			padding = 20
		}
		if u.page == "setup" {
			padding = 24
		}
		main := func(gtx layout.Context) layout.Dimensions {
			maxWidth := gtx.Constraints.Max.X
			if u.page == "setup" {
				maxWidth = min(maxWidth, gtx.Dp(760))
			}
			gtx.Constraints.Max.X, gtx.Constraints.Min.X = maxWidth, maxWidth
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(u.pageTop),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if u.page == "setup" {
						return layout.Dimensions{}
					}
					if wide {
						return layout.Dimensions{}
					}
					choices := []nativeChoice{{Value: "agents", Label: u.tr("Harnesses", "Harnesses")}, {Value: "models", Label: u.tr("Models", "Modelos")}, {Value: "activity", Label: u.tr("Activity", "Actividad")}, {Value: "settings", Label: u.tr("Settings", "Ajustes")}}
					selected := u.page
					if selected == "clients" {
						selected = "agents"
					} else if selected == "connection" {
						selected = "settings"
					}
					return layout.Inset{Top: 14}.Layout(gtx, u.tabs("nav.", choices, selected, func(page string) {
						u.page = page
						if (page == "clients" || page == "models") && len(u.models) == 0 {
							u.refreshModels()
						}
					}))
				}),
				layout.Rigid(layout.Spacer{Height: 20}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if u.notice == "" {
						return layout.Dimensions{}
					}
					return layout.Inset{Bottom: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, u.banner(u.noticeTone, u.notice)),
							layout.Rigid(layout.Spacer{Width: 8}.Layout),
							layout.Rigid(u.iconButton("notice.dismiss", u.tr("Dismiss message", "Descartar mensaje"), nativeButtonGhost, nativeIconClose, func() { u.setNotice(nativeToneNeutral, "") })),
						)
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
						panel = u.settingsPanel()
					}
					if u.imageDependencyNoticeVisible() && u.page != "settings" && u.page != "connection" {
						panel = u.column(u.imageDependencyNotice(false), panel)
					}
					if u.updateAvailable() && u.page != "settings" && u.page != "connection" {
						panel = u.column(u.updateNotice(), panel)
					}
					return material.List(u.theme, u.list("page."+u.page)).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
						return layout.Inset{Right: 14, Bottom: 20}.Layout(gtx, panel)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					footer := u.pageFooter()
					if footer == nil {
						return layout.Dimensions{}
					}
					return layout.Inset{Top: 12}.Layout(gtx, footer)
				}),
			)
		}
		return layout.Inset{Top: 24, Bottom: 20, Left: padding, Right: padding}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if u.page != "setup" {
				return main(gtx)
			}
			return layout.Center.Layout(gtx, main)
		})
	}))
	dims := layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
	u.layoutActiveModelMenu(gtx)
	u.layoutContextRemovalDialog(gtx)
	return dims
}

// pills wraps compact controls at their natural width, avoiding a table of
// stretched buttons when choosing an editor or an Xcode integration.
func (u *nativeUI) pills(children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		x, y, lineHeight, used := 0, 0, 0, 0
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
			used = max(used, x+dims.Size.X)
			x += dims.Size.X + gap
			if dims.Size.Y > lineHeight {
				lineHeight = dims.Size.Y
			}
		}
		// Report the occupied width so a pill group can sit beside flexible content.
		return layout.Dimensions{Size: gtx.Constraints.Constrain(image.Pt(used, y+lineHeight))}
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
		width := nativeLine
		if selected {
			width = nativeLineHeavy
		}
		return nativeBox(gtx, nativeSurface, func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: nativeInk, Width: width}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !selected {
							return layout.Dimensions{}
						}
						size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(6))
						paint.FillShape(gtx.Ops, nativeCitrus, clip.Rect{Max: size}.Op())
						return layout.Dimensions{Size: size}
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.UniformInset(16).Layout(gtx, u.column(children...))
					}),
				)
			})
		})
	}
}

type nativeButtonKind uint8

const (
	nativeButtonSecondary nativeButtonKind = iota
	nativeButtonPrimary
	nativeButtonGhost
	nativeButtonDanger
	// nativeButtonLink is an always-underlined inline control (navigation or copy).
	nativeButtonLink
	// nativeButtonDisclosure expands content in place: mono caps, +/− and underline.
	nativeButtonDisclosure
)

type nativeChoice struct {
	Value, Label, Caption string
	Disabled              bool
}

const (
	nativeLine      unit.Dp = 2
	nativeLineHeavy unit.Dp = 3
	nativeShadow    unit.Dp = 4
	// nativePrimaryShadow is the hard shadow under primary buttons.
	nativePrimaryShadow unit.Dp = 3
	nativeSpace20       unit.Dp = 20
	nativeSpace4        unit.Dp = 4
	nativeSpace8        unit.Dp = 8
	nativeSpace12       unit.Dp = 12
	nativeSpace16       unit.Dp = 16
	nativeSpace24       unit.Dp = 24
	nativeSpace32       unit.Dp = 32
)

func (u *nativeUI) segmented(idPrefix string, choices []nativeChoice, selected string, enabled bool, choose func(value string)) layout.Widget {
	buttons := make([]layout.Widget, 0, len(choices))
	for _, choice := range choices {
		choice := choice
		buttons = append(buttons, u.disabled(enabled, u.buttonKind(idPrefix+choice.Value, choice.Label, nativeButtonSecondary, nil, choice.Value == selected, func() {
			if choose != nil {
				choose(choice.Value)
			}
		})))
	}
	return u.pills(buttons...)
}

func (u *nativeUI) optionCards(idPrefix string, choices []nativeChoice, selected string, enabled bool, choose func(value string)) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		columns := 1
		if gtx.Constraints.Max.X >= gtx.Dp(720) {
			columns = 3
		} else if gtx.Constraints.Max.X >= gtx.Dp(440) {
			columns = 2
		}
		rows := make([]layout.Widget, 0, (len(choices)+columns-1)/columns)
		for start := 0; start < len(choices); start += columns {
			end := min(start+columns, len(choices))
			children := make([]layout.FlexChild, 0, 2*(end-start)-1)
			for index := start; index < end; index++ {
				choice := choices[index]
				children = append(children, layout.Flexed(1, u.optionCard(idPrefix+choice.Value, choice, choice.Value == selected, enabled && !choice.Disabled, choose)))
				if index+1 < end {
					children = append(children, layout.Rigid(layout.Spacer{Width: 8}.Layout))
				}
			}
			rows = append(rows, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx, children...)
			})
		}
		return u.column(rows...)(gtx)
	}
}

func (u *nativeUI) optionCard(id string, choice nativeChoice, selected, enabled bool, choose func(string)) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if !enabled {
			gtx = gtx.Disabled()
		}
		b := u.clickable(id)
		for b.Clicked(gtx) {
			if gtx.Enabled() && choose != nil {
				choose(choice.Value)
			}
		}
		background, border, borderWidth := nativeSurface, nativeBorder, nativeLine
		if selected {
			background, border, borderWidth = nativeSelected, nativeInk, nativeLineHeavy
		}
		if b.Hovered() && !selected && gtx.Enabled() {
			background = nativeSurfaceAlt
		}
		if !enabled {
			background, border = nativeDisabledBG, color.NRGBA{}
		}
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(semantic.Button).Add(gtx.Ops)
			semantic.LabelOp(choice.Label).Add(gtx.Ops)
			if selected {
				semantic.SelectedOp(true).Add(gtx.Ops)
			}
			return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
				return widget.Border{Color: border, Width: borderWidth}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					title := u.subheading(choice.Label)
					if selected {
						title = func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Min = image.Pt(gtx.Dp(16), gtx.Dp(16))
									return nativeIconCheck.Layout(gtx, nativeInk)
								}),
								layout.Rigid(layout.Spacer{Width: 6}.Layout),
								layout.Flexed(1, u.subheading(choice.Label)),
							)
						}
					}
					content := []layout.Widget{title}
					if choice.Caption != "" {
						content = append(content, u.note(choice.Caption))
					}
					return layout.UniformInset(nativeSpace16).Layout(gtx, u.column(content...))
				})
			})
		})
	}
}

func (u *nativeUI) tabs(idPrefix string, choices []nativeChoice, selected string, choose func(value string)) layout.Widget {
	children := make([]layout.Widget, 0, len(choices))
	for _, choice := range choices {
		choice := choice
		children = append(children, func(gtx layout.Context) layout.Dimensions {
			b := u.clickable(idPrefix + choice.Value)
			for b.Clicked(gtx) {
				if choose != nil {
					choose(choice.Value)
				}
			}
			active := choice.Value == selected
			foreground, weight := nativeTextMuted, font.Medium
			if active {
				foreground, weight = nativeInk, font.SemiBold
			}
			gtx.Constraints.Min.Y, gtx.Constraints.Max.Y = gtx.Dp(36), gtx.Dp(36)
			return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				semantic.ClassOp(semantic.Button).Add(gtx.Ops)
				semantic.LabelOp(choice.Label).Add(gtx.Ops)
				if active {
					semantic.SelectedOp(true).Add(gtx.Ops)
				}
				dims := layout.Inset{Top: 8, Bottom: 8, Left: 8, Right: 8}.Layout(gtx, u.textStyle(13, choice.Label, foreground, weight))
				if active {
					at := op.Offset(image.Pt(0, gtx.Dp(33))).Push(gtx.Ops)
					paint.FillShape(gtx.Ops, nativeInk, clip.Rect{Max: image.Pt(dims.Size.X, gtx.Dp(3))}.Op())
					at.Pop()
				}
				dims.Size.Y = gtx.Dp(36)
				return dims
			})
		})
	}
	return u.pills(children...)
}
func (u *nativeUI) dropdownButton(id, label string, action func()) layout.Widget {
	return u.buttonWidget(id, label, nativeButtonSecondary, nativeIconExpandMore, true, false, action)
}

func (u *nativeUI) menuItem(id, label string, selected bool, action func()) layout.Widget {
	return u.buttonKind(id, label, nativeButtonSecondary, nil, selected, action)
}

func (u *nativeUI) disclosure(id, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		// A disclosure is an inline control; stretching it centers the label away from its content.
		gtx.Constraints.Min.X = 0
		return u.buttonWidget(id, label, nativeButtonDisclosure, nil, false, u.expanded[id], func() { u.expanded[id] = !u.expanded[id] })(gtx)
	}
}
func (u *nativeUI) banner(tone nativeTone, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		colors := nativeTonePalette(tone)
		return nativeBox(gtx, colors.bg, func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: colors.border, Width: 1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min = image.Pt(gtx.Dp(18), gtx.Dp(18))
							return nativeToneIcon(tone).Layout(gtx, colors.fg)
						}),
						layout.Rigid(layout.Spacer{Width: 10}.Layout),
						layout.Flexed(1, u.textStyle(13, text, colors.fg, font.Medium)),
					)
				})
			})
		})
	}
}

func (u *nativeUI) message(tone nativeTone, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		colors := nativeTonePalette(tone)
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Pt(gtx.Dp(14), gtx.Dp(14))
				return nativeToneIcon(tone).Layout(gtx, colors.fg)
			}),
			layout.Rigid(layout.Spacer{Width: 6}.Layout),
			layout.Flexed(1, u.textStyle(12, text, colors.fg, font.Normal)),
		)
	}
}

func (u *nativeUI) hint(text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Pt(gtx.Dp(14), gtx.Dp(14))
				return nativeIconInfo.Layout(gtx, nativeTextMuted)
			}),
			layout.Rigid(layout.Spacer{Width: 6}.Layout),
			layout.Flexed(1, u.note(text)),
		)
	}
}

func (u *nativeUI) statusBadge(tone nativeTone, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		colors := nativeTonePalette(tone)
		return nativeBox(gtx, colors.bg, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 6, Bottom: 6, Left: 10, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						sz := gtx.Dp(6)
						paint.FillShape(gtx.Ops, colors.fg, clip.Ellipse(image.Rectangle{Max: image.Pt(sz, sz)}).Op(gtx.Ops))
						return layout.Dimensions{Size: image.Pt(sz, sz)}
					}),
					layout.Rigid(layout.Spacer{Width: 6}.Layout),
					layout.Rigid(u.textStyle(11, text, colors.fg, font.Medium)),
				)
			})
		})
	}
}

func (u *nativeUI) learnMore(id, address string) layout.Widget {
	return u.buttonWidget(id, u.tr("Learn more", "Más información"), nativeButtonLink, nativeIconOpenInNew, true, false, func() { u.open(address) })
}

func (u *nativeUI) setNotice(tone nativeTone, text string) {
	u.notice, u.noticeTone = text, tone
	if text == "" {
		u.noticeTone = nativeToneNeutral
	}
}

func (u *nativeUI) noticeError(err error) {
	if err == nil {
		u.setNotice(nativeToneNeutral, "")
		return
	}
	u.setNotice(nativeToneError, nativeMessage(err.Error(), u.language))
}

// settingsPanel is one scrollable page with a section index (native_ux_settings.go).
func (u *nativeUI) settingsPanel() layout.Widget {
	return u.uxsSettingsPage()
}

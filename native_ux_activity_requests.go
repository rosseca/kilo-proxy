//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"image"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
)

// uxaCapturePanel is the capture switch with its privacy explanation.
func (u *nativeUI) uxaCapturePanel() layout.Widget {
	captureSaving := u.busy["POST/api/activity/config"]
	if !captureSaving {
		u.setChecked("activity.capture", nativeBool(u.state, "captureEnabled"))
	}
	on := u.checked("activity.capture")
	title := u.tr("Request details capture is off", "Captura de detalles desactivada")
	description := u.tr("No request or its content is stored. Turn it on only when you need to inspect requests for debugging; usage totals continue either way.", "No se guarda ninguna petición ni su contenido. Actívala solo cuando necesites inspeccionar peticiones para depurar; los totales de uso continúan igualmente.")
	if on {
		title = u.tr("Request details capture is on", "Captura de detalles activada")
		description = u.tr("Keeps the last 30 requests in memory with their content (credentials hidden). Turning it off erases them.", "Guarda en memoria las últimas 30 peticiones con su contenido (credenciales ocultas). Al desactivarla se borran.")
	}
	toggle := u.disabled(!captureSaving, u.uxaSwitch("activity.capture", u.tr("Save request content", "Guardar el contenido de las peticiones"), func(enabled bool) {
		u.call("POST", "/api/activity/config", map[string]bool{"enabled": enabled}, func(json.RawMessage) {
			u.state["captureEnabled"] = enabled
			if !enabled {
				u.traceGeneration++
				u.trace = nil
				u.uxa().selected = ""
				u.state["events"] = nil
			}
			u.refreshState()
		})
	}))
	clear := u.dangerButton("activity.clear", u.tr("Clear captures", "Borrar capturas"), func() {
		u.traceGeneration++
		u.uxa().selected = ""
		u.call("POST", "/api/activity/clear", map[string]any{}, func(json.RawMessage) { u.trace = nil; u.refreshState() })
	})
	body := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			text := u.column(u.subheading(title), u.textStyle(13, description, nativeTextSubtle, font.Normal))
			if gtx.Constraints.Max.X < gtx.Dp(560) {
				return u.column(layout.Widget(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Rigid(toggle), layout.Rigid(layout.Spacer{Width: 16}.Layout), layout.Flexed(1, text))
				}), clear)(gtx)
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(toggle), layout.Rigid(layout.Spacer{Width: 16}.Layout), layout.Flexed(1, text),
				layout.Rigid(layout.Spacer{Width: 16}.Layout), layout.Rigid(clear),
			)
		},
		u.disclosure("activity.about", u.tr("What's included", "Qué incluye")),
	}
	if u.expanded["activity.about"] {
		body = append(body, u.note(u.tr("Capture keeps up to 30 requests in memory, including message content and redacted headers. Your choice is saved. Turning capture off erases captured details immediately, while cost and cache totals continue.", "La captura guarda en memoria hasta 30 peticiones, incluido el contenido de los mensajes y las cabeceras con credenciales ocultas. Se guarda tu elección. Al desactivarla se borran de inmediato los detalles capturados, pero los totales de coste y caché continúan.")))
	}
	return u.card(body...)
}

func (u *nativeUI) uxaEvents() []event {
	var events []event
	nativeDecode(u.state["events"], &events)
	return events
}

func uxaFailed(e event) bool { return e.Status < 200 || e.Status >= 300 }

func (u *nativeUI) uxaRequestsTab() layout.Widget {
	s := u.uxa()
	events := u.uxaEvents()
	days := uxaPeriodDays(s.period)
	now := time.Now()
	var inPeriod []event
	failures := 0
	for _, e := range events {
		if !uxaInPeriod(e.At, days, now) {
			continue
		}
		inPeriod = append(inPeriod, e)
		if uxaFailed(e) {
			failures++
		}
	}
	shown := inPeriod
	if s.errorsOnly {
		shown = nil
		for _, e := range inPeriod {
			if uxaFailed(e) {
				shown = append(shown, e)
			}
		}
	}
	var selected *event
	for i := range events {
		if events[i].ID == s.selected {
			selected = &events[i]
		}
	}
	if selected == nil {
		s.selected = ""
	}
	filter := u.pills(
		u.uxaChoice("activity.filter.all", u.tr("All", "Todas"), !s.errorsOnly, true, func() { s.errorsOnly = false }),
		u.uxaChoice("activity.filter.errors", fmt.Sprintf(u.tr("Errors only (%d)", "Solo errores (%d)"), failures), s.errorsOnly, true, func() { s.errorsOnly = true }),
	)
	var list layout.Widget
	switch {
	case len(events) == 0 && !nativeBool(u.state, "captureEnabled"):
		list = u.uxaEmpty(u.tr("Capture is off, so no requests are listed and no content is stored. Turn it on above to see the next requests here.", "La captura está desactivada: no se listan peticiones ni se guarda su contenido. Actívala arriba para ver aquí las próximas peticiones."))
	case len(events) == 0:
		list = u.uxaEmpty(u.tr("Waiting for your first captured request…", "Esperando tu primera petición capturada…"))
	case len(shown) == 0 && s.errorsOnly:
		list = u.uxaEmpty(u.tr("No failed requests in this period.", "No hay peticiones con error en este periodo."))
	case len(shown) == 0:
		list = u.uxaEmpty(u.tr("No captured requests in this period. Choose a longer period to see earlier ones.", "No hay peticiones capturadas en este periodo. Elige un periodo más largo para ver las anteriores."))
	default:
		list = u.uxaRequestTable(shown)
	}
	main := uxaSpacedColumn(u.uxaCapturePanel(), filter, list)
	if selected == nil {
		return main
	}
	inspector := u.uxaInspector(*selected)
	return func(gtx layout.Context) layout.Dimensions {
		if gtx.Constraints.Max.X < gtx.Dp(900) {
			return uxaSpacedColumn(inspector, main)(gtx)
		}
		drawer := max(gtx.Dp(400), gtx.Constraints.Max.X*2/5)
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx,
			layout.Flexed(1, main),
			layout.Rigid(layout.Spacer{Width: 16}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X, gtx.Constraints.Max.X = drawer, drawer
				return inspector(gtx)
			}),
		)
	}
}

func (u *nativeUI) uxaEmpty(message string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return nativeBox(gtx, nativeSurfaceAlt, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.UniformInset(20).Layout(gtx, u.textStyle(14, message, nativeTextMuted, font.Normal))
		})
	}
}

type uxaColumn struct {
	title  string
	weight float32 // flex weight; 0 means fixed width
	width  int     // dp, for fixed columns
	end    bool
}

// uxaRequestRowCells formats one request for the table and the inspector.
func (u *nativeUI) uxaRequestCells(e event) (when, harness, model, tokens, cost string) {
	when, harness, model, tokens, cost = e.At, "—", "—", "—", "—"
	if at, err := time.Parse(time.RFC3339, e.At); err == nil {
		when = at.Local().Format("15:04:05")
	}
	if e.Usage == nil {
		return
	}
	harness = uxaHarness(u.language, e.Usage.Source)
	if e.Usage.Model != "" {
		model = u.uxaModelName(e.Usage.Model)
	}
	if e.Usage.Input != nil || e.Usage.Output != nil {
		in, out := "—", "—"
		if e.Usage.Input != nil {
			in = uxaCompact(u.language, *e.Usage.Input)
		}
		if e.Usage.Output != nil {
			out = uxaCompact(u.language, *e.Usage.Output)
		}
		tokens = in + " → " + out
	}
	if e.Usage.Billing == "subscription" {
		cost = u.tr("Subscription", "Suscripción")
	} else if e.Usage.CostUSD != nil {
		cost = uxaMoneyCell(*e.Usage.CostUSD)
	}
	return
}

func (u *nativeUI) uxaStatusText(e event) string {
	parts := []string{fmt.Sprint(e.Status)}
	if uxaFailed(e) {
		if reason := uxaStatusReason(u.language, e.Status); reason != "" {
			parts = append(parts, reason)
		}
		return strings.Join(parts, " · ")
	}
	if e.Usage != nil && e.Usage.Cached != nil {
		denominator := e.Usage.Input
		if e.Usage.Prompt != nil && *e.Usage.Prompt > 0 {
			denominator = e.Usage.Prompt
		}
		if *e.Usage.Cached == 0 {
			parts = append(parts, u.tr("no cache", "sin caché"))
		} else if denominator != nil && *denominator > 0 {
			parts = append(parts, u.tr("cache ", "caché ")+uxaPercent(*e.Usage.Cached, max(*denominator, *e.Usage.Cached)))
		}
	}
	return strings.Join(parts, " · ")
}

func (u *nativeUI) uxaRequestTable(events []event) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		narrow := gtx.Constraints.Max.X < gtx.Dp(640)
		columns := []uxaColumn{{title: u.tr("Time", "Hora"), width: 92}, {title: "Harness", weight: 1}, {title: u.tr("Model", "Modelo"), weight: 1.4}, {title: "Tokens", width: 120, end: true}, {title: u.tr("Cost", "Coste"), width: 86, end: true}, {title: u.tr("Status", "Estado"), weight: 1.3}}
		if narrow {
			columns = []uxaColumn{columns[0], columns[2], columns[4], columns[5]}
		}
		row := func(cells []layout.Widget) layout.Widget {
			return func(gtx layout.Context) layout.Dimensions {
				children := make([]layout.FlexChild, 0, 2*len(cells))
				for i, cell := range cells {
					cell, column := cell, columns[i]
					if i > 0 {
						children = append(children, layout.Rigid(layout.Spacer{Width: 12}.Layout))
					}
					if column.weight == 0 {
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							w := gtx.Dp(unit.Dp(column.width))
							gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w
							return cell(gtx)
						}))
					} else {
						children = append(children, layout.Flexed(column.weight, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return cell(gtx)
						}))
					}
				}
				return layout.Inset{Top: 12, Bottom: 12, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
				})
			}
		}
		align := func(column uxaColumn) text.Alignment {
			if column.end {
				return text.End
			}
			return text.Start
		}
		header := make([]layout.Widget, len(columns))
		for i, column := range columns {
			header[i] = u.uxaText(11, strings.ToUpper(column.title), nativeTextSubtle, font.Bold, true, align(column))
		}
		rows := []layout.Widget{func(gtx layout.Context) layout.Dimensions {
			return nativeBox(gtx, nativeSurfaceAlt, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return row(header)(gtx)
			})
		}}
		for _, e := range events {
			e := e
			when, harness, model, tokens, cost := u.uxaRequestCells(e)
			failed := uxaFailed(e)
			statusColor := nativeText
			if failed {
				statusColor = nativeErrorFG
			}
			status := func(gtx layout.Context) layout.Dimensions {
				icon := nativeIconCheck
				if failed {
					icon = nativeIconError
				}
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min = image.Pt(gtx.Dp(14), gtx.Dp(14))
						gtx.Constraints.Max = gtx.Constraints.Min
						return icon.Layout(gtx, statusColor)
					}),
					layout.Rigid(layout.Spacer{Width: 6}.Layout),
					layout.Flexed(1, u.uxaText(13, u.uxaStatusText(e), statusColor, font.Medium, false, text.Start)),
				)
			}
			all := map[string]layout.Widget{
				columns[0].title:         u.uxaText(13, when, nativeText, font.Normal, true, text.Start),
				"Harness":                u.uxaText(14, harness, nativeText, font.Normal, false, text.Start),
				u.tr("Model", "Modelo"):  u.uxaText(14, model, nativeText, font.Normal, false, text.Start),
				"Tokens":                 u.uxaText(14, tokens, nativeText, font.Normal, false, text.End),
				u.tr("Cost", "Coste"):    u.uxaText(14, cost, nativeText, font.Medium, true, text.End),
				u.tr("Status", "Estado"): status,
			}
			cells := make([]layout.Widget, len(columns))
			for i, column := range columns {
				cells[i] = all[column.title]
			}
			selected := u.uxa().selected == e.ID
			rows = append(rows, u.uxaClickableRow("activity.event."+e.ID, model+" · "+when, selected, func() { u.uxaOpenRequest(e) }, row(cells)))
		}
		return widget.Border{Color: nativeInk, Width: nativeLine}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			flex := make([]layout.FlexChild, 0, 2*len(rows))
			for i, r := range rows {
				if i > 0 {
					flex = append(flex, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))
						uxaFill(gtx, nativeInk, image.Rectangle{Max: size})
						return layout.Dimensions{Size: size}
					}))
				}
				flex = append(flex, layout.Rigid(r))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, flex...)
		})
	}
}

// uxaClickableRow highlights the open request in citrus; the open inspector is the second cue.
func (u *nativeUI) uxaClickableRow(id, label string, selected bool, open func(), content layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := u.clickable(id)
		for b.Clicked(gtx) {
			open()
		}
		background := nativeSurface
		if selected {
			background = nativeCitrus
		} else if b.Hovered() {
			background = nativeSurfaceAlt
		}
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.ClassOp(semantic.Button).Add(gtx.Ops)
			semantic.DescriptionOp(label).Add(gtx.Ops)
			if selected {
				semantic.SelectedOp(true).Add(gtx.Ops)
			}
			return nativeBox(gtx, background, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return content(gtx)
			})
		})
	}
}

func (u *nativeUI) uxaOpenRequest(e event) {
	s := u.uxa()
	s.selected = e.ID
	u.traceGeneration++
	u.trace = nil
	u.traceStage = "request"
	if !e.HasDetails {
		return
	}
	generation := u.traceGeneration
	u.call("GET", "/api/activity/"+e.ID, nil, func(raw json.RawMessage) { u.acceptTrace(generation, raw) })
}

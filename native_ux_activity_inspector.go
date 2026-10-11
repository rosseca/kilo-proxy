//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/widget"
)

// uxaInspector is the right-hand drawer for one request: summary tiles and the
// captured stages (client request, what was sent to Kilo, and the responses).
func (u *nativeUI) uxaInspector(e event) layout.Widget {
	s := u.uxa()
	when, harness, model, tokens, _ := u.uxaRequestCells(e)
	cost := "—"
	if e.Usage != nil && e.Usage.Billing == "subscription" {
		cost = u.tr("Subscription usage", "Uso de suscripción")
	} else if e.Usage != nil && e.Usage.CostUSD != nil {
		cost = nativeMoney(*e.Usage.CostUSD)
	}
	title := model
	if harness != "—" {
		title = harness + " → " + model
	}
	duration := fmt.Sprintf("%d ms", e.Duration)
	if e.Duration >= 1000 {
		duration = fmt.Sprintf("%.1f s", float64(e.Duration)/1000)
		if u.language == "es" {
			duration = strings.Replace(duration, ".", ",", 1)
		}
	}
	tile := func(label, value string) layout.Widget {
		return layout.Widget(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(12).Layout(gtx, u.column(u.textStyle(12, label, nativeTextSubtle, font.Normal), u.textStyle(15, value, nativeText, font.SemiBold)))
		})
	}
	children := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx,
				layout.Flexed(1, u.column(u.eyebrow(u.tr("Request", "Petición")+" · "+when), u.textStyle(20, title, nativeText, font.SemiBold), u.note(e.Method+" "+e.Path+" · "+u.uxaStatusText(e)))),
				layout.Rigid(u.iconButton("activity.inspector.close", u.tr("Close inspector", "Cerrar inspector"), nativeButtonGhost, nativeIconClose, func() {
					s.selected = ""
					u.traceGeneration++
					u.trace = nil
				})),
			)
		},
		uxaGrid(3, tile(u.tr("Cost", "Coste"), cost), tile("Tokens", tokens), tile(u.tr("Duration", "Duración"), duration)),
	}
	if e.Usage != nil && e.Usage.Billing != "subscription" {
		if source := u.costSourceLabel(e.Usage.CostSource); source != "" {
			children = append(children, u.note(source))
		}
	}
	switch {
	case !e.HasDetails && !nativeBool(u.state, "captureEnabled"):
		children = append(children, u.message(nativeToneInfo, u.tr("Capture is off, so the content of this request was not stored.", "La captura está desactivada, así que no se guardó el contenido de esta petición.")))
	case !e.HasDetails:
		children = append(children, u.message(nativeToneInfo, u.tr("The content of this request was not stored. Capture only keeps requests that start while it is on.", "No se guardó el contenido de esta petición. La captura solo guarda las peticiones que empiezan mientras está activada.")))
	case u.trace == nil || u.trace.ID != e.ID:
		children = append(children, u.note(u.tr("Loading the captured request…", "Cargando la petición capturada…")))
	default:
		children = append(children, u.uxaTraceStages()...)
	}
	return func(gtx layout.Context) layout.Dimensions {
		return nativeBox(gtx, nativeSurface, func(gtx layout.Context) layout.Dimensions {
			return widget.Border{Color: nativeInk, Width: nativeLineHeavy}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.UniformInset(20).Layout(gtx, u.column(children...))
			})
		})
	}
}

func (u *nativeUI) uxaTraceStages() []layout.Widget {
	s := u.uxa()
	trace := u.trace
	tab := u.traceStage
	if tab == "upstreamResponse" {
		tab = "response"
	}
	tabs := []nativeChoice{{Value: "request", Label: u.tr("Request", "Petición")}, {Value: "upstreamRequest", Label: u.tr("Sent to Kilo", "Enviado a Kilo")}, {Value: "response", Label: u.tr("Response", "Respuesta")}}
	buttons := make([]layout.Widget, 0, len(tabs))
	for _, choice := range tabs {
		choice := choice
		buttons = append(buttons, u.uxaChoice("activity.stage."+choice.Value, choice.Label, tab == choice.Value, false, func() {
			u.traceStage = choice.Value
			if choice.Value == "response" && s.responseSide == "upstreamResponse" {
				u.traceStage = "upstreamResponse"
			}
		}))
	}
	tabBar := func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		dims := u.pills(buttons...)(gtx)
		dims.Size.X = gtx.Constraints.Max.X
		uxaFill(gtx, nativeInk, uxaRectBelow(dims, gtx.Dp(nativeLine)))
		dims.Size.Y += gtx.Dp(nativeLine)
		return dims
	}
	children := []layout.Widget{tabBar}
	if tab == "response" {
		side := u.traceStage
		children = append(children, u.pills(
			u.uxaChoice("activity.side.upstreamResponse", u.tr("From Kilo", "De Kilo"), side == "upstreamResponse", true, func() { u.traceStage, s.responseSide = "upstreamResponse", "upstreamResponse" }),
			u.uxaChoice("activity.side.response", u.tr("To the client", "Al cliente"), side == "response", true, func() { u.traceStage, s.responseSide = "response", "response" }),
		))
	}
	part := trace.Request
	switch u.traceStage {
	case "upstreamRequest":
		part = trace.UpstreamRequest
	case "upstreamResponse":
		part = trace.UpstreamResponse
	case "response":
		part = trace.Response
	}
	if trace.Error != "" {
		children = append(children, u.message(nativeToneError, trace.Error))
	}
	if part.Truncated || part.HeadersTruncated {
		children = append(children, u.note(u.tr("Capture truncated to the size limit. Byte count refers to the original stream.", "Captura truncada al límite de tamaño. El contador de bytes corresponde al flujo original.")))
	}
	body := part.Body
	if u.checked("activity.format") {
		body = nativePretty(body)
	}
	headers, _ := json.MarshalIndent(part.Headers, "", "  ")
	sectionHead := func(label, copyID string, value string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, u.subheading(label)),
				layout.Rigid(u.buttonWidget(copyID, u.tr("Copy", "Copiar"), nativeButtonLink, nil, false, false, func() { u.copy(value) })),
			)
		}
	}
	children = append(children,
		u.check("activity.format", u.tr("Format JSON", "Formatear JSON"), nil),
		sectionHead(fmt.Sprintf(u.tr("Body · %s bytes", "Cuerpo · %s bytes"), uxaCount(u.language, part.Bytes)), "activity.copy-body", body),
		u.code("activity.body", body),
		sectionHead(u.tr("Headers", "Cabeceras"), "activity.copy-headers", string(headers)),
		u.code("activity.headers", string(headers)),
	)
	return children
}

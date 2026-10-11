//go:build desktop

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
)

func nativeDecode(value any, into any) {
	data, err := json.Marshal(value)
	if err == nil {
		_ = json.Unmarshal(data, into)
	}
}
func nativeMoney(value string) string {
	if value == "" {
		return "—"
	}
	clean := value
	if strings.Contains(value, ".") {
		clean = strings.TrimRight(strings.TrimRight(value, "0"), ".")
	}
	if clean == "" {
		clean = "0"
	}
	return "$" + clean
}

func nativeMoneyShort(value string) string {
	if !accountMoneyPattern.MatchString(value) {
		return "-"
	}
	negative := strings.HasPrefix(value, "-")
	if negative {
		value = value[1:]
	}
	whole, fraction, _ := strings.Cut(value, ".")
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	if whole == "0" && strings.Trim(fraction, "0") == "" {
		return "$0"
	}
	cents := fraction
	if len(cents) > 2 {
		cents = cents[:2]
	}
	if whole == "0" && strings.Trim(cents, "0") == "" {
		return "<$0.01"
	}
	cents += strings.Repeat("0", 2-len(cents))
	scaled := []byte(whole + cents)
	if len(fraction) > 2 && fraction[2] >= '5' {
		carry := true
		for i := len(scaled) - 1; i >= 0; i-- {
			if scaled[i] == '9' {
				scaled[i] = '0'
				continue
			}
			scaled[i]++
			carry = false
			break
		}
		if carry {
			scaled = append([]byte{'1'}, scaled...)
		}
	}
	if negative {
		scaled = append([]byte{'-'}, scaled...)
	}
	return "$" + string(scaled[:len(scaled)-2]) + "." + string(scaled[len(scaled)-2:])
}
func nativeCount(n int64) string {
	raw := fmt.Sprint(n)
	for i := len(raw) - 3; i > 0; i -= 3 {
		raw = raw[:i] + "," + raw[i:]
	}
	return raw
}
func nativeReportedCount(value, reported, requests int64) string {
	if requests > 0 && reported == 0 {
		return "—"
	}
	return nativeCount(value)
}

func nativeReportedMoneyShort(value string, reported int64) string {
	if reported == 0 {
		return "—"
	}
	short := nativeMoneyShort(value)
	if short == "-" {
		return "—"
	}
	return short
}

func (u *nativeUI) activityMetricTile(label, value, caption string) layout.Widget {
	valueColor := nativeText
	if value == "—" {
		valueColor = nativeTextDisabled
	}
	return func(gtx layout.Context) layout.Dimensions {
		width := gtx.Constraints.Min.X
		return nativeBox(gtx, nativeSurfaceAlt, func(gtx layout.Context) layout.Dimensions {
			// Stack children start with a zero minimum; fill the grid cell so tiles in a row share one width.
			gtx.Constraints.Min.X = width
			return layout.UniformInset(12).Layout(gtx, u.column(
				u.eyebrow(label), u.textStyle(28, value, valueColor, font.Medium), u.note(caption),
			))
		})
	}
}
func nativeCacheRatio(s usageSummary) string {
	if s.CacheRatioRequests == 0 || s.CacheRatioInput <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", float64(s.CacheRatioRead)*100/float64(s.CacheRatioInput))
}
func nativePretty(raw string) string {
	var out bytes.Buffer
	if json.Indent(&out, []byte(raw), "", "  ") == nil {
		return out.String()
	}
	return raw
}
func (u *nativeUI) coverage(summary usageSummary) string {
	billable := max(int64(0), summary.Requests-summary.SubscriptionRequests)
	missing := max(int64(0), billable-summary.Priced)
	text := fmt.Sprintf(u.tr("Cost reported: %d/%d requests\n%d requests without reported cost", "Coste informado: %d/%d peticiones\n%d peticiones sin coste informado"), summary.Priced, billable, missing)
	if summary.SubscriptionRequests > 0 {
		subscription := fmt.Sprintf(u.tr("%d subscription requests · quota shown separately", "%d peticiones de suscripción · cuota mostrada por separado"), summary.SubscriptionRequests)
		if billable == 0 {
			return subscription
		}
		text += "\n" + subscription
	}
	return text
}

func (u *nativeUI) responseStats(summary usageSummary) string {
	return fmt.Sprintf(u.tr("Response stats: %d interrupted or limited", "Estadísticas de respuesta: %d interrumpidas o limitadas"), summary.Incomplete)
}
func (u *nativeUI) reportedSpend(summary usageSummary) (label, amount string) {
	if summary.SubscriptionRequests > 0 && summary.Requests == summary.SubscriptionRequests {
		return u.tr("Subscription usage", "Uso de suscripción"), u.tr("Included in subscription", "Incluido en la suscripción")
	}
	label = u.tr("Reported inference cost", "Coste de inferencia informado")
	if summary.Priced > 0 && summary.Priced < summary.Requests-summary.SubscriptionRequests {
		label = u.tr("Reported subtotal", "Subtotal informado")
	}
	amount = "—"
	if summary.Priced > 0 {
		amount = nativeMoney(summary.CostUSD)
	}
	return label, amount
}
func (u *nativeUI) inferenceCostNote() string {
	return u.tr("Inference costs reported by the provider (including BYOK) or gateway. They may differ from your Kilo organization’s charges; these are not estimates.", "Costes de inferencia informados por el proveedor (incluido BYOK) o el gateway. Pueden diferir de los cargos de tu organización en Kilo; no son estimaciones.")
}
func (u *nativeUI) costSourceLabel(source string) string {
	switch source {
	case "usage.cost_details.upstream_inference_cost":
		return u.tr("Provider inference", "Inferencia del proveedor")
	case "provider_metadata.gateway.marketCost", "response.provider_metadata.gateway.marketCost":
		return u.tr("Gateway market cost", "Coste de mercado del gateway")
	case "usage.cost_microdollars", "usage.cost":
		return u.tr("Gateway-reported cost", "Coste informado por el gateway")
	}
	return ""
}
func (u *nativeUI) cacheCaption(s usageSummary, read bool) string {
	n := s.WithCacheWrite
	if read {
		n = s.WithCacheRead
	}
	return fmt.Sprintf(u.tr("Reported in %d of %d requests", "Informado en %d de %d peticiones"), n, s.Requests)
}
func (u *nativeUI) activityPanel() layout.Widget {
	var usage struct {
		Total    usageSummary   `json:"total"`
		Sessions []usageSummary `json:"sessions"`
	}
	nativeDecode(u.state["usage"], &usage)
	var events []event
	nativeDecode(u.state["events"], &events)
	total := usage.Total
	costLabel, _ := u.reportedSpend(total)
	costValue := nativeReportedMoneyShort(total.CostUSD, total.Priced)
	costCaption := u.coverage(total)
	if costValue != "—" {
		costCaption = nativeMoney(total.CostUSD) + " · " + costCaption
	}
	costNote := u.tr("Reported cost is not the same as Kilo charges.", "El coste informado no es lo mismo que los cargos de Kilo.")
	if total.SubscriptionRequests > 0 && total.Requests == total.SubscriptionRequests {
		costLabel, costValue = u.tr("Subscription usage", "Uso de suscripción"), fmt.Sprint(total.SubscriptionRequests)
		costCaption, costNote = u.tr("Requests using ChatGPT quota", "Peticiones que usan la cuota de ChatGPT"), u.tr("Subscription usage is separate from Kilo charges.", "El uso de suscripción es independiente de los cargos de Kilo.")
	}
	costMetrics := []layout.Widget{
		u.topRow(
			u.activityMetricTile(costLabel, costValue, costCaption),
			u.activityMetricTile(u.tr("Requests", "Peticiones"), fmt.Sprintf("%.0f", nativeNumber(u.state, "requests")), fmt.Sprintf(u.tr("%.0f active · %.0f errors", "%.0f activas · %.0f errores"), nativeNumber(u.state, "active"), nativeNumber(u.state, "failures"))),
			u.activityMetricTile(u.tr("Tokens", "Tokens"), nativeReportedCount(total.Input+total.Output, total.WithTokens, total.Requests), fmt.Sprintf(u.tr("%s input · %s output", "%s entrada · %s salida"), nativeCount(total.Input), nativeCount(total.Output))),
		),
		u.note(u.responseStats(total)),
		u.note(costNote),
		u.disclosure("activity.costs", u.tr("What's included", "Qué incluye")),
	}
	if u.expanded["activity.costs"] {
		costMetrics = append(costMetrics, u.note(u.inferenceCostNote()))
	}
	cacheMetrics := []layout.Widget{
		u.heading(u.tr("Cache reuse", "Reutilización de caché")),
		u.topRow(
			u.activityMetricTile(u.tr("Read from cache", "Leído de caché"), nativeReportedCount(total.Cached, total.WithCacheRead, total.Requests), u.cacheCaption(total, true)),
			u.activityMetricTile(u.tr("Written to cache", "Escrito en caché"), nativeReportedCount(total.CacheWrite, total.WithCacheWrite, total.Requests), u.cacheCaption(total, false)),
			u.activityMetricTile(u.tr("Prompt reused", "Prompt reutilizado"), nativeCacheRatio(total), fmt.Sprintf(u.tr("Ratio available for %d requests", "Ratio disponible en %d peticiones"), total.CacheRatioRequests)),
		),
		u.note(u.tr("Missing usage is not counted as zero.", "Los datos de uso ausentes no se cuentan como cero.")),
		u.disclosure("activity.usage.about", u.tr("What's included", "Qué incluye")),
	}
	if u.expanded["activity.usage.about"] {
		cacheMetrics = append(cacheMetrics, u.note(u.tr("Totals cover this Kilo Proxy process and use values returned by the gateway.", "Los totales cubren este proceso de Kilo Proxy y usan los valores devueltos por el gateway.")))
	}
	panels := []layout.Widget{u.accountUsagePanel(), u.card(costMetrics...), u.card(cacheMetrics...)}
	sessions := []layout.Widget{u.heading(u.tr("Conversations", "Conversaciones")), u.note(u.tr("Grouped by client session headers. Requests without an identifier appear as unassigned.", "Agrupadas por cabeceras de sesión del cliente. Las peticiones sin identificador aparecen sin asignar."))}
	if len(usage.Sessions) == 0 {
		sessions = append(sessions, u.note(u.tr("Your first conversation will appear here.", "Tu primera conversación aparecerá aquí.")))
	}
	for _, s := range usage.Sessions {
		s := s
		label, price := u.reportedSpend(s)
		if s.Priced > 0 && s.Priced < s.Requests {
			price = label + ": " + price
		}
		last := u.tr("Last request: cache not reported", "Última petición: caché no informada")
		if s.LastCache != nil {
			read, write := "—", "—"
			if s.LastCache.Read != nil {
				read = nativeCount(*s.LastCache.Read)
			}
			if s.LastCache.Write != nil {
				write = nativeCount(*s.LastCache.Write)
			}
			last = fmt.Sprintf(u.tr("Last request · cache read %s · cache write %s", "Última petición · caché leída %s · caché escrita %s"), read, write)
		}
		sessions = append(sessions, u.card(u.row(u.label(s.Label), u.label(price+" · "+nativeCount(s.Requests)+u.tr(" requests", " peticiones"))), u.note(u.tr("Organization: ", "Organización: ")+s.OrgID+" · "+s.Source), u.note(fmt.Sprintf(u.tr("Input %s · Output %s · Cached %s · Written %s · Reused %s", "Entrada %s · Salida %s · Caché %s · Escrita %s · Reutilizada %s"), nativeCount(s.Input), nativeCount(s.Output), nativeReportedCount(s.Cached, s.WithCacheRead, s.Requests), nativeReportedCount(s.CacheWrite, s.WithCacheWrite, s.Requests), nativeCacheRatio(s))), u.note(u.coverage(s)), u.note(u.responseStats(s)), u.note(last)))
	}
	panels = append(panels, u.card(sessions...))
	captureSaving := u.busy["POST/api/activity/config"]
	if !captureSaving {
		u.setChecked("activity.capture", nativeBool(u.state, "captureEnabled"))
	}
	activity := []layout.Widget{
		u.actionRow(u.heading(u.tr("Recent requests", "Peticiones recientes")), u.pills(
			u.disabled(!captureSaving, u.check("activity.capture", u.tr("Capture request details", "Capturar detalles"), func(enabled bool) {
				u.call("POST", "/api/activity/config", map[string]bool{"enabled": enabled}, func(json.RawMessage) {
					u.state["captureEnabled"] = enabled
					if !enabled {
						u.traceGeneration++
						u.trace = nil
						u.state["events"] = nil
					}
					u.refreshState()
				})
			})),
			u.dangerButton("activity.clear", u.tr("Clear captures", "Borrar capturas"), func() {
				u.traceGeneration++
				u.call("POST", "/api/activity/clear", map[string]any{}, func(json.RawMessage) { u.trace = nil; u.refreshState() })
			}),
		)),
		u.note(u.tr("Captures are off by default. Turning them off erases them; usage totals continue.", "Las capturas están desactivadas de forma predeterminada. Al desactivarlas se borran; los totales de uso continúan.")),
		u.disclosure("activity.about", u.tr("What's included", "Qué incluye")),
	}
	if u.expanded["activity.about"] {
		activity = append(activity, u.note(u.tr("Capture keeps up to 30 requests in memory, including message content and redacted headers. Your choice is saved. Turning capture off erases captured details immediately, while cost and cache totals continue.", "La captura guarda en memoria hasta 30 peticiones, incluido el contenido de los mensajes y las cabeceras con credenciales ocultas. Se guarda tu elección. Al desactivarla se borran de inmediato los detalles capturados, pero los totales de coste y caché continúan.")))
	}
	if len(events) == 0 {
		if nativeBool(u.state, "captureEnabled") {
			activity = append(activity, u.note(u.tr("Waiting for your first captured request…", "Esperando tu primera petición capturada…")))
		} else {
			activity = append(activity, u.note(u.tr("Request capture is off. Enable it only when you need to inspect requests for debugging.", "La captura está desactivada. Actívala solo cuando necesites inspeccionar peticiones para depurar.")))
		}
	}
	panels = append(panels, u.card(activity...))
	for _, request := range events {
		panels = append(panels, u.activityRequestRow(request))
	}
	if u.trace != nil {
		panels = append(panels, u.tracePanel())
	}
	return u.column(panels...)
}

func (u *nativeUI) activityRequestRow(request event) layout.Widget {
	tone := nativeToneError
	if request.Status >= 200 && request.Status < 300 {
		tone = nativeToneSuccess
	}
	model, tokens, cost := "—", "—", "—"
	costSource := ""
	if request.Usage != nil {
		if request.Usage.Model != "" {
			model = request.Usage.Model
		}
		input, output := "—", "—"
		if request.Usage.Input != nil {
			input = nativeCount(*request.Usage.Input)
		}
		if request.Usage.Output != nil {
			output = nativeCount(*request.Usage.Output)
		}
		tokens = fmt.Sprintf(u.tr("%s input · %s output", "%s entrada · %s salida"), input, output)
		if request.Usage.CostUSD != nil {
			cost = nativeMoney(*request.Usage.CostUSD)
			costSource = u.costSourceLabel(request.Usage.CostSource)
		}
	}
	costText := u.tr("Reported cost: ", "Coste informado: ") + cost
	if request.Usage != nil && request.Usage.Billing == "subscription" {
		costText = u.tr("Subscription usage", "Uso de suscripción")
		costSource = ""
	}
	children := []layout.Widget{
		u.topRow(
			u.statusBadge(tone, fmt.Sprint(request.Status)),
			u.subheading(model),
			u.note(fmt.Sprintf("%s · %s %s · %d ms", request.At, request.Method, request.Path, request.Duration)),
		),
		u.topRow(u.note(tokens), u.note(costText)),
	}
	if costSource != "" {
		children = append(children, u.note(costSource))
	}
	if request.HasDetails {
		children = append(children, u.ghostButton("activity.event."+request.ID, u.tr("Inspect request", "Inspeccionar petición"), func() {
			u.traceGeneration++
			generation := u.traceGeneration
			u.call("GET", "/api/activity/"+request.ID, nil, func(raw json.RawMessage) { u.acceptTrace(generation, raw) })
		}))
	} else {
		children = append(children, u.note(u.tr("No capture available", "No hay ninguna captura disponible")))
	}
	return u.card(children...)
}
func (u *nativeUI) tracePanel() layout.Widget {
	trace := u.trace
	stages := []struct{ id, en, es string }{{"request", "Client request", "Petición del cliente"}, {"upstreamRequest", "Gateway request", "Petición al gateway"}, {"upstreamResponse", "Gateway response", "Respuesta al gateway"}, {"response", "Client response", "Respuesta al cliente"}}
	stageChoices := make([]nativeChoice, 0, len(stages))
	for _, stage := range stages {
		stageChoices = append(stageChoices, nativeChoice{Value: stage.id, Label: u.tr(stage.en, stage.es)})
	}
	stageTabs := u.tabs("activity.stage.", stageChoices, u.traceStage, func(stage string) { u.traceStage = stage })
	part := trace.Request
	switch u.traceStage {
	case "upstreamRequest":
		part = trace.UpstreamRequest
	case "upstreamResponse":
		part = trace.UpstreamResponse
	case "response":
		part = trace.Response
	}
	headers, _ := json.MarshalIndent(part.Headers, "", "  ")
	children := []layout.Widget{u.heading(u.tr("Request inspector", "Inspector de peticiones") + " · " + trace.ID), stageTabs, u.check("activity.format", u.tr("Format JSON", "Formatear JSON"), nil)}
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
	children = append(children, u.eyebrow(u.tr("Headers", "Cabeceras")), u.button("activity.copy-headers", u.tr("Copy headers", "Copiar cabeceras"), func() { u.copy(string(headers)) }), u.code("activity.headers", string(headers)), u.eyebrow(fmt.Sprintf(u.tr("Body · %d bytes", "Cuerpo · %d bytes"), part.Bytes)), u.button("activity.copy-body", u.tr("Copy body", "Copiar cuerpo"), func() { u.copy(body) }), u.code("activity.body", body))
	return u.card(children...)
}

func (u *nativeUI) acceptTrace(generation uint64, raw json.RawMessage) {
	if generation != u.traceGeneration {
		return
	}
	var trace requestTrace
	if json.Unmarshal(raw, &trace) != nil {
		return
	}
	for _, entry := range nativeArray(u.state, "events") {
		if nativeString(nativeMap(entry), "id") == trace.ID {
			u.trace = &trace
			u.traceStage = "request"
			return
		}
	}
}

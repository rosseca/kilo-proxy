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

// uxaConversationsTab keeps the session-scoped data: totals for this Kilo
// Proxy process, the local daily history and the per-conversation groups.
func (u *nativeUI) uxaConversationsTab() layout.Widget {
	var usage struct {
		Total    usageSummary   `json:"total"`
		Sessions []usageSummary `json:"sessions"`
	}
	nativeDecode(u.state["usage"], &usage)
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
	session := []layout.Widget{
		u.heading(u.tr("This Kilo Proxy session", "Esta sesión de Kilo Proxy")),
		u.topRow(
			u.activityMetricTile(costLabel, costValue, costCaption),
			u.activityMetricTile(u.tr("Requests", "Peticiones"), fmt.Sprintf("%.0f", nativeNumber(u.state, "requests")), fmt.Sprintf(u.tr("%.0f active · %.0f errors", "%.0f activas · %.0f errores"), nativeNumber(u.state, "active"), nativeNumber(u.state, "failures"))),
			u.activityMetricTile(u.tr("Tokens", "Tokens"), nativeReportedCount(total.Input+total.Output, total.WithTokens, total.Requests), fmt.Sprintf(u.tr("%s input · %s output", "%s entrada · %s salida"), nativeCount(total.Input), nativeCount(total.Output))),
		),
		u.topRow(
			u.activityMetricTile(u.tr("Read from cache", "Leído de caché"), nativeReportedCount(total.Cached, total.WithCacheRead, total.Requests), u.cacheCaption(total, true)),
			u.activityMetricTile(u.tr("Written to cache", "Escrito en caché"), nativeReportedCount(total.CacheWrite, total.WithCacheWrite, total.Requests), u.cacheCaption(total, false)),
			u.activityMetricTile(u.tr("Prompt reused", "Prompt reutilizado"), nativeCacheRatio(total), fmt.Sprintf(u.tr("Ratio available for %d requests", "Ratio disponible en %d peticiones"), total.CacheRatioRequests)),
		),
		u.note(u.responseStats(total)),
		u.note(costNote + " " + u.tr("Missing usage is not counted as zero.", "Los datos de uso ausentes no se cuentan como cero.")),
		u.disclosure("activity.costs", u.tr("What's included", "Qué incluye")),
	}
	if u.expanded["activity.costs"] {
		session = append(session, u.note(u.inferenceCostNote()), u.note(u.tr("Totals cover this Kilo Proxy process and use values returned by the gateway.", "Los totales cubren este proceso de Kilo Proxy y usan los valores devueltos por el gateway.")))
	}
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
	return uxaSpacedColumn(u.card(sessions...), u.card(session...), u.uxaLocalHistoryCard())
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

//go:build desktop

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

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
	return fmt.Sprintf(u.tr("Cost reported for %d of %d requests · %d incomplete", "Coste informado en %d de %d peticiones · %d incompletas"), summary.Priced, summary.Requests, summary.Incomplete)
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
	cost := nativeMoney(total.CostUSD)
	if total.Requests > 0 && total.Priced == 0 {
		cost = "—"
	}
	panels := []layout.Widget{
		u.card(u.topRow(u.metric(u.tr("Session cost", "Coste de sesión"), cost, u.coverage(total)), u.metric(u.tr("Requests", "Peticiones"), fmt.Sprintf("%.0f", nativeNumber(u.state, "requests")), fmt.Sprintf(u.tr("%.0f active · %.0f errors", "%.0f activas · %.0f errores"), nativeNumber(u.state, "active"), nativeNumber(u.state, "failures"))), u.metric(u.tr("Tokens", "Tokens"), nativeReportedCount(total.Input+total.Output, total.WithTokens, total.Requests), fmt.Sprintf(u.tr("%s input · %s output", "%s entrada · %s salida"), nativeCount(total.Input), nativeCount(total.Output))))),
		u.card(u.heading(u.tr("Cache reuse", "Reutilización de caché")), u.topRow(u.metric(u.tr("Read from cache", "Leído de caché"), nativeReportedCount(total.Cached, total.WithCacheRead, total.Requests), u.cacheCaption(total, true)), u.metric(u.tr("Written to cache", "Escrito en caché"), nativeReportedCount(total.CacheWrite, total.WithCacheWrite, total.Requests), u.cacheCaption(total, false)), u.metric(u.tr("Prompt reused", "Prompt reutilizado"), nativeCacheRatio(total), fmt.Sprintf(u.tr("Ratio available for %d requests", "Ratio disponible en %d peticiones"), total.CacheRatioRequests))), u.note(u.tr("Missing usage is not counted as zero. Totals cover this Kilo Proxy process and use values returned by the gateway.", "Los datos ausentes no se cuentan como cero. Los totales cubren este proceso de Kilo Proxy y usan los valores devueltos por el gateway."))),
	}
	sessions := []layout.Widget{u.heading(u.tr("Conversations", "Conversaciones")), u.note(u.tr("Grouped by client session headers. Requests without an identifier appear as unassigned.", "Agrupadas por cabeceras de sesión del cliente. Las peticiones sin identificador aparecen sin asignar."))}
	if len(usage.Sessions) == 0 {
		sessions = append(sessions, u.note(u.tr("Your first conversation will appear here.", "Tu primera conversación aparecerá aquí.")))
	}
	for _, s := range usage.Sessions {
		s := s
		price := nativeMoney(s.CostUSD)
		if s.Priced == 0 {
			price = "—"
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
		sessions = append(sessions, u.card(u.row(u.label(s.Label), u.label(price+" · "+nativeCount(s.Requests)+u.tr(" requests", " peticiones"))), u.note(u.tr("Organization: ", "Organización: ")+s.OrgID+" · "+s.Source), u.note(fmt.Sprintf(u.tr("Input %s · Output %s · Cached %s · Written %s · Reused %s", "Entrada %s · Salida %s · Caché %s · Escrita %s · Reutilizada %s"), nativeCount(s.Input), nativeCount(s.Output), nativeReportedCount(s.Cached, s.WithCacheRead, s.Requests), nativeReportedCount(s.CacheWrite, s.WithCacheWrite, s.Requests), nativeCacheRatio(s))), u.note(u.coverage(s)), u.note(last)))
	}
	panels = append(panels, u.card(sessions...))
	u.setChecked("activity.capture", nativeBool(u.state, "captureEnabled"))
	activity := []layout.Widget{u.heading(u.tr("Recent requests", "Peticiones recientes")), u.row(u.check("activity.capture", u.tr("Capture request details", "Capturar detalles"), func(enabled bool) {
		u.call("POST", "/api/activity/config", map[string]bool{"enabled": enabled}, func(json.RawMessage) { u.state["captureEnabled"] = enabled })
	}), u.button("activity.clear", u.tr("Clear captures", "Borrar capturas"), func() {
		u.traceGeneration++
		u.call("POST", "/api/activity/clear", map[string]any{}, func(json.RawMessage) { u.trace = nil; u.refreshState() })
	})), u.note(u.tr("The last 30 captures stay in memory. Credentials are redacted; message content may still be sensitive. Clearing captures keeps cost and cache totals.", "Las últimas 30 capturas se guardan en memoria. Las credenciales se ocultan; el contenido de los mensajes puede ser sensible. Borrar capturas mantiene los totales de coste y caché."))}
	if len(events) == 0 {
		activity = append(activity, u.note(u.tr("Waiting for your first request…", "Esperando tu primera petición…")))
	}
	for _, e := range events {
		e := e
		label := fmt.Sprintf("%s   %s %s   %d · %d ms", e.At, e.Method, e.Path, e.Status, e.Duration)
		if e.Usage != nil && e.Usage.Model != "" {
			label += " · " + e.Usage.Model
		}
		if e.HasDetails {
			activity = append(activity, u.button("activity.event."+e.ID, label, func() {
				u.traceGeneration++
				generation := u.traceGeneration
				u.call("GET", "/api/activity/"+e.ID, nil, func(raw json.RawMessage) { u.acceptTrace(generation, raw) })
			}))
		} else {
			activity = append(activity, u.note(label+u.tr(" · No capture", " · Sin captura")))
		}
	}
	panels = append(panels, u.card(activity...))
	if u.trace != nil {
		panels = append(panels, u.tracePanel())
	}
	return u.column(panels...)
}
func (u *nativeUI) tracePanel() layout.Widget {
	trace := u.trace
	stages := []struct{ id, en, es string }{{"request", "Client request", "Petición del cliente"}, {"upstreamRequest", "Gateway request", "Petición al gateway"}, {"upstreamResponse", "Gateway response", "Respuesta del gateway"}, {"response", "Client response", "Respuesta al cliente"}}
	tabs := []layout.Widget{}
	for _, s := range stages {
		s := s
		label := u.tr(s.en, s.es)
		if u.traceStage == s.id {
			label = "● " + label
		}
		tabs = append(tabs, u.button("activity.stage."+s.id, label, func() { u.traceStage = s.id }))
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
	headers, _ := json.MarshalIndent(part.Headers, "", "  ")
	children := []layout.Widget{u.heading(u.tr("Request inspector", "Inspector de peticiones") + " · " + trace.ID), u.row(tabs...), u.check("activity.format", u.tr("Format JSON", "Formatear JSON"), nil)}
	if trace.Error != "" {
		children = append(children, u.note(trace.Error))
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

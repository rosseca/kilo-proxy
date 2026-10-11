//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/layout"
)

// uxaPageTop supplies the Activity header: a description per tab and, when the
// sidebar is visible, the period selector beside the title. Narrow windows keep
// the proxy control in the header and show the period inside the page.
func (u *nativeUI) uxaPageTop(description [2]string, controls layout.Widget) ([2]string, layout.Widget) {
	switch u.uxa().tab {
	case "requests":
		description = [2]string{"The latest requests that went through the proxy.", "Las últimas peticiones que han pasado por el proxy."}
	case "conversations":
		description = [2]string{"This Kilo Proxy session and this device, grouped by conversation.", "Esta sesión de Kilo Proxy y este dispositivo, agrupados por conversación."}
	default:
		description = [2]string{"What you spent on Kilo and how much the cache saved.", "Lo que has gastado en Kilo y cuánto ha ahorrado la caché."}
	}
	if u.sidebarVisible {
		controls = u.uxaPeriodSelector()
	}
	return description, controls
}

func (u *nativeUI) activityPanel() layout.Widget {
	s := u.uxa()
	var trailing layout.Widget
	if s.tab == "summary" {
		var billing nativeBillingData
		nativeDecode(u.state["billing"], &billing)
		refreshing := u.busy["POST/api/billing/refresh"] || billing.Status == "loading"
		trailing = u.disabled(!refreshing, u.iconButton("billing.refresh", u.tr("Refresh balance and usage", "Actualizar saldo y uso"), nativeButtonGhost, nativeIconRefresh, func() {
			u.call("POST", "/api/billing/refresh", map[string]any{}, func(json.RawMessage) { u.refreshState() })
		}))
	}
	head := []layout.Widget{}
	if !u.sidebarVisible {
		head = append(head, u.uxaPeriodSelector())
	}
	head = append(head, u.uxaTabBar(trailing))
	var body layout.Widget
	switch s.tab {
	case "requests":
		body = u.uxaRequestsTab()
	case "conversations":
		body = u.uxaConversationsTab()
	default:
		body = u.uxaSummaryTab()
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(u.column(head...)),
			layout.Rigid(layout.Spacer{Height: 22}.Layout),
			layout.Rigid(body),
		)
	}
}

// uxaSpacedColumn stacks panels with the page's 20dp rhythm.
func uxaSpacedColumn(children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		flex := make([]layout.FlexChild, 0, 2*len(children))
		for i, child := range children {
			if child == nil {
				continue
			}
			if i > 0 && len(flex) > 0 {
				flex = append(flex, layout.Rigid(layout.Spacer{Height: 20}.Layout))
			}
			flex = append(flex, layout.Rigid(child))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, flex...)
	}
}

func (u *nativeUI) uxaKiloReady(usage nativeAccountUsage) bool {
	return usage.Status == "ready" && !usage.Stale && len(usage.Days) > 0
}

func (u *nativeUI) uxaSummaryTab() layout.Widget {
	s := u.uxa()
	var billing nativeBillingData
	var history nativeUsageHistory
	nativeDecode(u.state["billing"], &billing)
	nativeDecode(u.state["usageHistory"], &history)
	days := uxaPeriodDays(s.period)
	now := time.Now()
	usage := billing.Usage
	series := uxaSeries{}
	if u.uxaKiloReady(usage) {
		series = uxaKiloSeries(usage, days)
	} else if history.StartedAt != "" || len(history.Days) > 0 {
		series = uxaLocalSeries(history, days, now)
	}
	periodLabel := u.tr("Today", "Hoy")
	if days > 1 {
		periodLabel = fmt.Sprintf(u.tr("%d days", "%d días"), days)
	}
	panels := []layout.Widget{}
	// Status first: why Kilo data is missing, stale or loading.
	if status := u.billingStatusWidget(billing.Status, billing.Stale, billing.Error); status != nil {
		panels = append(panels, status)
	}
	if !u.uxaKiloReady(usage) {
		message := u.billingStatus(usage.Status, usage.Stale, usage.Error)
		if message == "" {
			message = u.tr("Kilo has not reported daily usage yet.", "Kilo todavía no ha informado del uso diario.")
		}
		if series.Source == "local" {
			message += " " + u.tr("Showing what this device observed instead (local data, provider-reported costs).", "Mientras tanto se muestra lo observado en este dispositivo (datos locales, costes informados por el proveedor).")
		}
		tone := nativeToneWarning
		if usage.Status == "loading" {
			tone = nativeToneInfo
		}
		panels = append(panels, u.banner(tone, message))
	}

	// KPI row.
	cost, costKnown, requests, errors, input, cached, write := series.totals()
	scope := u.tr("Personal credit", "Crédito personal")
	if billing.Scope == "organization" {
		scope = u.tr("Team credit", "Crédito del equipo")
	}
	balanceCaption := scope
	if age := uxaRelativeAge(u.language, billing.FetchedAt, now); age != "" {
		balanceCaption += " · " + age
	}
	spent, spentCaption := "—", u.tr("Kilo charges", "Cargos de Kilo")
	requestValue := "—"
	var requestCaption layout.Widget = u.note(u.tr("No data", "Sin datos"))
	read, written, uncached, cacheTotal := uxaCacheSplit(input, cached, write)
	switch series.Source {
	case "kilo":
		if costKnown {
			spent = nativeMoneyShort(dollars(cost))
		}
		requestValue = uxaCount(u.language, requests)
		requestCaption = u.note(u.tr("No errors", "Sin errores"))
		if errors > 0 {
			requestCaption = func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Baseline}.Layout(gtx,
					layout.Rigid(u.note(fmt.Sprintf(u.tr("%s with errors · ", "%s con error · "), uxaCount(u.language, errors)))),
					layout.Rigid(u.uxaInlineLink("activity.kpi.errors", u.tr("view", "ver"), 13, func() { s.tab, s.errorsOnly = "requests", true })),
				)
			}
		}
	case "local":
		spentCaption = u.tr("Reported on this device", "Informado en este dispositivo")
		if costKnown {
			spent = nativeMoneyShort(dollars(cost))
		}
		requestValue = uxaCount(u.language, requests)
		requestCaption = u.note(u.tr("This device only", "Solo este dispositivo"))
	}
	kpis := []layout.Widget{
		u.uxaKPI(u.tr("Kilo balance", "Saldo en Kilo"), nativeBillingMoneyShort(billing.BalanceUSD, billing.Status, billing.Stale), u.note(balanceCaption)),
		u.uxaKPI(u.tr("Spent", "Gastado")+" · "+periodLabel, spent, u.note(spentCaption)),
		u.uxaKPI(u.tr("Requests", "Peticiones"), requestValue, requestCaption),
		u.uxaKPI(u.tr("Cache savings", "Ahorro por caché"), uxaPercent(read, cacheTotal), u.note(u.tr("of the prompt reused", "del prompt reutilizado"))),
	}
	panels = append(panels, func(gtx layout.Context) layout.Dimensions {
		columns := 4
		if gtx.Constraints.Max.X < gtx.Dp(760) {
			columns = 2
		}
		return uxaGrid(columns, kpis...)(gtx)
	})

	// Daily spend.
	if len(series.Days) > 0 {
		subtitle := u.tr("Kilo charges in dollars · UTC", "Cargos de Kilo en dólares · UTC")
		if series.Source == "local" {
			subtitle = u.tr("Local data · costs reported to this device · local time", "Datos locales · costes informados en este dispositivo · hora local")
		}
		tooltip := func(d uxaDay) string {
			if !d.Known {
				return uxaDayLabel(u.language, d.Date) + " · " + u.tr("no data", "sin datos")
			}
			money := "—"
			if d.CostKnown {
				money = nativeMoneyShort(dollars(d.Cost))
			}
			return fmt.Sprintf(u.tr("%s · %s · %s requests", "%s · %s · %s peticiones"), uxaDayLabel(u.language, d.Date), money, uxaCount(u.language, d.Requests))
		}
		if days == 1 {
			panels = append(panels, u.uxaPanel(u.tr("Spend today", "Gasto de hoy"), subtitle, nil,
				u.textStyle(16, tooltip(series.Days[len(series.Days)-1]), nativeText, font.Medium),
				u.note(u.tr("Choose 7, 14 or 30 days to see spend per day.", "Elige 7, 14 o 30 días para ver el gasto por día.")),
			))
		} else {
			panels = append(panels, u.uxaPanel(u.tr("Spend per day", "Gasto por día"), subtitle, nil, u.uxaBarChart(series.Days, tooltip)))
		}
	}

	// Spend per model, only when Kilo returned a usable breakdown for the period.
	if series.Source == "kilo" && len(usage.Models) > 0 {
		dates := map[string]bool{}
		for _, d := range series.Days {
			dates[d.Date] = true
		}
		rows := uxaModelBreakdown(usage.Models, dates, 5, u.tr("Others", "Otros"), u.uxaModelName)
		if len(rows) > 0 {
			panels = append(panels, u.uxaPanel(u.tr("By model", "Por modelo"), u.tr("Spend in the period · according to Kilo", "Gasto en el periodo · según Kilo"), nil, u.uxaHorizontalBars(rows)))
		}
	}

	// Cache.
	if series.Source != "" {
		cacheChildren := []layout.Widget{}
		if cacheTotal > 0 {
			cacheChildren = append(cacheChildren, uxaStackedBar(read, written, uncached), u.pills(
				u.uxaLegend(nativeInk, false, fmt.Sprintf(u.tr("Read from cache %s tokens", "Leído de caché %s tokens"), uxaCompact(u.language, read))),
				u.uxaLegend(uxaWritten, false, fmt.Sprintf(u.tr("Written to cache %s", "Escrito en caché %s"), uxaCompact(u.language, written))),
				u.uxaLegend(nativeSurface, true, fmt.Sprintf(u.tr("Uncached %s", "Sin caché %s"), uxaCompact(u.language, uncached))),
			))
		} else {
			cacheChildren = append(cacheChildren, u.note(u.tr("No input tokens reported in this period.", "No se han informado tokens de entrada en este periodo.")))
		}
		panels = append(panels, u.uxaPanel(u.tr("Cache", "Caché"), u.tr("The more the prompt is reused, the less you pay per request.", "Cuanto más se reutiliza el prompt, menos pagas por cada petición."), u.monoStyle(20, uxaPercent(read, cacheTotal), nativeText, font.Bold), cacheChildren...))
	}

	if u.chatGPTConnected() {
		panels = append(panels, u.chatGPTQuotaPanel())
	}
	panels = append(panels, u.uxaSourcesNote(billing, series, cost, costKnown))
	return uxaSpacedColumn(panels...)
}

// uxaModelName prefers the catalog's display name for a Kilo model ID.
func (u *nativeUI) uxaModelName(id string) string {
	trimmed := strings.TrimPrefix(id, "~")
	for _, model := range u.models {
		if (model.ID == id || model.ID == trimmed) && model.Name != "" {
			return model.Name
		}
	}
	return id
}

// uxaSourcesNote is the single footnote; it expands to the exact amounts,
// scope, timestamps and caveats that used to be spread over several panels.
func (u *nativeUI) uxaSourcesNote(billing nativeBillingData, series uxaSeries, cost int64, costKnown bool) layout.Widget {
	children := []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X, gtx.Constraints.Max.X = gtx.Dp(16), gtx.Dp(16)
					return nativeIconInfo.Layout(gtx, nativeTextSubtle)
				}),
				layout.Rigid(layout.Spacer{Width: 6}.Layout),
				layout.Rigid(u.buttonWidget("activity.sources", u.tr("Where this data comes from", "De dónde salen estos datos"), nativeButtonLink, nil, false, false, func() { u.expanded["activity.sources"] = !u.expanded["activity.sources"] })),
			)
		},
		u.textStyle(13, u.tr("All data comes from your Kilo account and may be a few minutes behind. We never store prompts or responses unless you turn on capture.", "Todos los datos vienen de tu cuenta de Kilo y pueden ir con unos minutos de retraso. Nunca guardamos prompts ni respuestas salvo que actives la captura."), nativeTextSubtle, font.Normal),
	}
	if !u.expanded["activity.sources"] {
		return u.column(children...)
	}
	if balance := nativeBillingMoney(billing.BalanceUSD, billing.Status, billing.Stale); balance != "—" {
		children = append(children, u.note(u.tr("Exact balance: ", "Saldo exacto: ")+balance))
	}
	if series.Source == "kilo" && costKnown {
		children = append(children, u.note(u.tr("Exact spend in the period: ", "Gasto exacto en el periodo: ")+nativeMoney(dollars(cost))))
	}
	scopeNote := u.tr("Available credit in Kilo. A separate BYOK provider's balance is not included.", "Crédito disponible en Kilo. No incluye el saldo de un proveedor BYOK externo.")
	if billing.Scope == "organization" {
		scopeNote = u.tr("This balance is shared by everyone in the selected team. It is not your personal spending limit and does not include a BYOK provider's balance.", "Este saldo se comparte entre los miembros del equipo seleccionado. No es tu límite personal de gasto ni incluye el saldo de un proveedor BYOK externo.")
	}
	children = append(children,
		u.note(scopeNote),
		u.note(u.tr("Spend, requests, errors and cache are your own billed usage in the selected account or team, including activity outside this proxy, grouped by UTC day. BYOK inference costs can differ from Kilo credit deductions.", "El gasto, las peticiones, los errores y la caché son tu uso facturado en la cuenta o equipo seleccionado, incluida la actividad fuera de este proxy, agrupado por día UTC. Los costes de inferencia BYOK pueden diferir de los cargos al crédito de Kilo.")),
		u.note(u.tr("Cache savings are cache reads as a share of all input tokens (reads, writes and uncached input).", "El ahorro por caché es la parte de los tokens de entrada leída de caché (lecturas, escrituras y entrada sin caché).")),
	)
	if series.Source == "local" {
		children = append(children, u.note(u.tr("Local data covers requests through this proxy on this device, by local day. Prompts, headers and responses are not stored.", "Los datos locales cubren las peticiones de este proxy en este dispositivo, por día local. No se guardan prompts, cabeceras ni respuestas.")))
	}
	if stamp := nativeUpdatedAt(billing.FetchedAt); stamp != "" {
		children = append(children, u.note(u.tr("Balance checked: ", "Saldo consultado: ")+stamp))
	}
	if stamp := nativeUpdatedAt(billing.Usage.FetchedAt); stamp != "" {
		children = append(children, u.note(u.tr("Usage checked: ", "Uso consultado: ")+stamp))
	}
	return u.column(children...)
}

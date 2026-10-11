//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"time"

	"gioui.org/layout"
)

type nativeAccountUsage struct {
	Status, Scope, PeriodStart, PeriodEnd, TimeZone, FetchedAt, Error string
	TodayUSD, YesterdayUSD, Last30DaysUSD                             *string
	Stale                                                             bool
	Days                                                              []struct {
		Date, CostUSD                                       string
		Requests, Input, Output, Cached, CacheWrite, Errors int64
	}
}

type nativeBillingData struct {
	Status, Scope, OrgID, FetchedAt, Error string
	BalanceUSD                             *string
	Stale                                  bool
	Usage                                  nativeAccountUsage
}

type nativeUsageHistory struct {
	Today, Yesterday, Last7Days usageSummary
	Timezone, StartedAt, Error  string
	Days                        []struct {
		Date string
		usageSummary
	}
}

func (u *nativeUI) billingStatus(status string, stale bool, errorText string) string {
	if stale {
		message := u.tr("Out of date. Refresh to check the latest balance and usage.", "Desactualizado. Actualiza para consultar el saldo y uso actuales.")
		if errorText != "" {
			message += " " + errorText
		}
		return message
	}
	switch status {
	case "ready":
		return ""
	case "loading":
		return u.tr("Checking Kilo…", "Consultando Kilo…")
	case "signed_out":
		return u.tr("Sign in to Kilo in Settings to see your balance and billed usage.", "Inicia sesión en Kilo desde Ajustes para ver el saldo y el uso facturado.")
	default:
		message := u.tr("Kilo data is unavailable. Check your connection or sign in again in Settings.", "Los datos de Kilo no están disponibles. Comprueba la conexión o vuelve a iniciar sesión en Ajustes.")
		if errorText != "" {
			message += " " + errorText
		}
		return message
	}
}

func nativeBillingMoney(value *string, status string, stale bool) string {
	if status != "ready" || stale || value == nil || !accountMoneyPattern.MatchString(*value) {
		return "—"
	}
	return nativeMoney(*value)
}

func nativeBillingMoneyShort(value *string, status string, stale bool) string {
	if nativeBillingMoney(value, status, stale) == "—" {
		return "—"
	}
	short := nativeMoneyShort(*value)
	if short == "-" {
		return "—"
	}
	return short
}

func (u *nativeUI) billingStatusWidget(status string, stale bool, errorText string) layout.Widget {
	if errorText != "" {
		return u.message(nativeToneError, u.billingStatus(status, stale, errorText))
	}
	if !stale && status == "loading" {
		return u.statusBadge(nativeToneInfo, u.tr("Checking Kilo…", "Consultando Kilo…"))
	}
	if message := u.billingStatus(status, stale, ""); message != "" {
		return u.message(nativeToneWarning, message)
	}
	return nil
}

func nativeUpdatedAt(value string) string {
	when, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return ""
	}
	return when.Local().Format("2006-01-02 15:04")
}

func (u *nativeUI) localHistoryMetric(title string, summary usageSummary) layout.Widget {
	label, exact := u.reportedSpend(summary)
	amount := "—"
	if summary.Requests > 0 {
		amount = nativeReportedMoneyShort(summary.CostUSD, summary.Priced)
	} else {
		exact = "—"
	}
	caption := fmt.Sprintf(u.tr("%d requests · cost in %d/%d", "%d peticiones · coste en %d/%d"), summary.Requests, summary.Priced, summary.Requests)
	if exact != "—" {
		caption = exact + " · " + caption
	}
	if summary.Priced > 0 && summary.Priced < summary.Requests {
		caption += "\n" + label
	}
	return u.activityMetricTile(title, amount, caption)
}

func (u *nativeUI) accountUsagePanel() layout.Widget {
	var billing nativeBillingData
	var history nativeUsageHistory
	nativeDecode(u.state["billing"], &billing)
	nativeDecode(u.state["usageHistory"], &history)
	refreshing := u.busy["POST/api/billing/refresh"] || billing.Status == "loading"
	scope := u.tr("Personal Kilo credit", "Crédito personal de Kilo")
	scopeNote := u.tr("Available credit in Kilo. A separate BYOK provider's balance is not included.", "Crédito disponible en Kilo. No incluye el saldo de un proveedor BYOK externo.")
	if billing.Scope == "organization" {
		scope = u.tr("Shared team credit", "Crédito compartido del equipo")
		scopeNote = u.tr("This balance is shared by everyone in the selected team. It is not your personal spending limit and does not include a BYOK provider's balance.", "Este saldo se comparte entre los miembros del equipo seleccionado. No es tu límite personal de gasto ni incluye el saldo de un proveedor BYOK externo.")
	}
	updated := u.tr("Not checked yet", "Todavía sin consultar")
	if stamp := nativeUpdatedAt(billing.FetchedAt); stamp != "" {
		updated = u.tr("Last checked: ", "Última consulta: ") + stamp
	}
	balance := nativeBillingMoney(billing.BalanceUSD, billing.Status, billing.Stale)
	balanceCaption := scope
	if balance != "—" {
		balanceCaption += " · " + u.tr("Exact: ", "Exacto: ") + balance
	}
	account := []layout.Widget{
		u.actionRow(u.heading(u.tr("Kilo account", "Cuenta de Kilo")), u.disabled(!refreshing, u.iconButton("billing.refresh", u.tr("Refresh balance and usage", "Actualizar saldo y uso"), nativeButtonSecondary, nativeIconRefresh, func() {
			u.call("POST", "/api/billing/refresh", map[string]any{}, func(json.RawMessage) { u.refreshState() })
		}))),
		u.activityMetricTile(u.tr("Remaining balance", "Saldo restante"), nativeBillingMoneyShort(billing.BalanceUSD, billing.Status, billing.Stale), balanceCaption),
		u.note(updated),
	}
	if status := u.billingStatusWidget(billing.Status, billing.Stale, billing.Error); status != nil {
		account = append(account, status)
	}
	usage := billing.Usage
	chargeTile := func(title string, value *string, caption string) layout.Widget {
		exact := nativeBillingMoney(value, usage.Status, usage.Stale)
		if exact != "—" {
			caption += " · " + u.tr("Exact: ", "Exacto: ") + exact
		}
		return u.activityMetricTile(title, nativeBillingMoneyShort(value, usage.Status, usage.Stale), caption)
	}
	account = append(account,
		u.heading(u.tr("Your Kilo charges", "Tus cargos de Kilo")),
		u.topRow(
			chargeTile(u.tr("Today · UTC", "Hoy · UTC"), usage.TodayUSD, u.tr("Billed by Kilo", "Facturado por Kilo")),
			chargeTile(u.tr("Yesterday · UTC", "Ayer · UTC"), usage.YesterdayUSD, u.tr("Billed by Kilo", "Facturado por Kilo")),
			chargeTile(u.tr("Last 30 days", "Últimos 30 días"), usage.Last30DaysUSD, u.tr("Including today · UTC", "Incluye hoy · UTC")),
		),
		u.note(u.tr("Kilo may report recent usage with a delay; provider costs can differ from these charges.", "Kilo puede informar del uso reciente con retraso; los costes del proveedor pueden diferir de estos cargos.")),
		u.disclosure("billing.about", u.tr("What's included", "Qué incluye")),
	)
	if status := u.billingStatusWidget(usage.Status, usage.Stale, usage.Error); status != nil {
		account = append(account, status)
	}
	if u.expanded["billing.about"] {
		account = append(account,
			u.note(scopeNote),
			u.note(u.tr("This is your own billed usage in the selected account or team, including activity outside this proxy. BYOK inference costs can differ from Kilo credit deductions.", "Este es tu uso facturado en la cuenta o equipo seleccionado, incluida la actividad fuera de este proxy. Los costes de inferencia BYOK pueden diferir de los cargos al crédito de Kilo.")),
		)
	}
	if stamp := nativeUpdatedAt(usage.FetchedAt); stamp != "" {
		account = append(account, u.note(u.tr("Usage checked: ", "Uso consultado: ")+stamp))
	}
	if usage.Status == "ready" && !usage.Stale && len(usage.Days) > 0 {
		account = append(account, u.disclosure("billing.daily", u.tr("Daily Kilo usage", "Uso diario en Kilo")))
		if u.expanded["billing.daily"] {
			rows := []layout.Widget{}
			for _, day := range usage.Days {
				rows = append(rows, u.note(fmt.Sprintf(u.tr("%s · %s · %s requests · %s tokens in / %s out · %s cached", "%s · %s · %s peticiones · %s tokens entrada / %s salida · %s en caché"), day.Date, nativeMoney(day.CostUSD), nativeCount(day.Requests), nativeCount(day.Input), nativeCount(day.Output), nativeCount(day.Cached))))
			}
			account = append(account, u.scroll("billing.days", 240, rows...))
		}
	}
	local := []layout.Widget{
		u.heading(u.tr("Observed on this device", "Observado en este dispositivo")),
		u.topRow(u.localHistoryMetric(u.tr("Today", "Hoy"), history.Today), u.localHistoryMetric(u.tr("Yesterday", "Ayer"), history.Yesterday), u.localHistoryMetric(u.tr("Last 7 days", "Últimos 7 días"), history.Last7Days)),
		u.note(u.tr("Local totals cover requests through this proxy; prompts, headers and responses are not stored.", "Los totales locales incluyen las peticiones de este proxy; no se guardan prompts, cabeceras ni respuestas.")),
		u.disclosure("history.about", u.tr("What's included", "Qué incluye")),
	}
	if u.expanded["history.about"] {
		local = append(local, u.note(u.tr("Saved daily totals apply to the current account and team. They include provider inference costs, which may differ from Kilo charges. Earlier activity cannot be recovered locally.", "Los totales diarios guardados corresponden a la cuenta y el equipo actuales. Incluyen los costes de inferencia del proveedor, que pueden diferir de los cargos de Kilo. La actividad anterior no se puede recuperar localmente.")))
	}
	if history.StartedAt != "" {
		timezone := history.Timezone
		if timezone == "Local time (at request completion)" {
			timezone = u.tr("Local time (at request completion)", "Hora local (al completarse la petición)")
		}
		local = append(local, u.note(u.tr("Recorded since: ", "Registrado desde: ")+nativeUpdatedAt(history.StartedAt)+" · "+timezone))
	} else {
		local = append(local, u.note(u.tr("History starts with your next request.", "El historial comienza con tu próxima petición.")))
	}
	if history.Error != "" {
		local = append(local, u.message(nativeToneError, u.tr("History could not be saved: ", "No se pudo guardar el historial: ")+history.Error))
	}
	if len(history.Days) > 0 {
		local = append(local, u.disclosure("history.daily", u.tr("Daily local history", "Historial local diario")))
		if u.expanded["history.daily"] {
			rows := []layout.Widget{}
			for _, day := range history.Days {
				label, money := u.reportedSpend(day.usageSummary)
				rows = append(rows, u.note(fmt.Sprintf(u.tr("%s · %s %s · %d requests · cost in %d/%d", "%s · %s %s · %d peticiones · coste en %d/%d"), day.Date, label, money, day.Requests, day.Priced, day.Requests)))
			}
			local = append(local, u.scroll("history.days", 240, rows...))
		}
	}
	panels := []layout.Widget{u.card(account...), u.card(local...)}
	if u.chatGPTConnected() {
		panels = append(panels, u.chatGPTQuotaPanel())
	}
	return u.column(append(panels, u.heading(u.tr("This Kilo Proxy session", "Esta sesión de Kilo Proxy")))...)
}

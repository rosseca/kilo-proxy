//go:build desktop

package main

import (
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
	Models []accountUsageModel
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

// uxaLocalHistoryCard shows the daily totals saved on this device. They
// contain no prompts, model names or conversation identifiers.
func (u *nativeUI) uxaLocalHistoryCard() layout.Widget {
	var history nativeUsageHistory
	nativeDecode(u.state["usageHistory"], &history)
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
	return u.card(local...)
}

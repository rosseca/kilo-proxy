//go:build desktop

package main

import (
	"image"
	"strings"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func TestNativeBillingMoneyDoesNotShowMissingOrStaleAsZero(t *testing.T) {
	zero, negative, small := "0.000000000", "-0.062515000", "0.000000001"
	for _, tc := range []struct {
		value  *string
		status string
		stale  bool
		want   string
	}{
		{&zero, "ready", false, "$0"}, {&small, "ready", false, "$0.000000001"}, {&negative, "ready", false, "$-0.062515"},
		{nil, "ready", false, "—"}, {&zero, "ready", true, "—"}, {&zero, "unavailable", false, "—"}, {&zero, "loading", false, "—"},
	} {
		if got := nativeBillingMoney(tc.value, tc.status, tc.stale); got != tc.want {
			t.Fatalf("%+v: %q", tc, got)
		}
	}
}

func TestNativeAccountUsageLayoutAndDailyDetails(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {780, 700}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"-"+lang, func(t *testing.T) {
				u := nativeTestUI(t)
				u.page = "activity"
				// Seed the source served by /api/state, not just the rendered
				// copy. Real one-second refreshes can run during a slow GPU
				// screenshot and must preserve the synthetic account and locale.
				now := time.Now()
				balance, todayCost, yesterdayCost, monthCost := "248.623145000", "1.203456000", "8.956780000", "10.160236000"
				todayDate, remoteDate := now.UTC().Format("2006-01-02"), now.UTC().AddDate(0, 0, -1).Format("2006-01-02")
				u.owner.mu.Lock()
				u.owner.config.Language = lang
				u.owner.syncBillingIdentityLocked()
				u.owner.billing.snapshot = billingSnapshot{
					Status: "ready", Scope: "organization", OrgID: u.owner.config.OrgID, BalanceUSD: &balance, FetchedAt: now.Format(time.RFC3339),
					Usage: accountUsageSnapshot{Status: "ready", Scope: "self", TimeZone: "UTC", TodayUSD: &todayCost, YesterdayUSD: &yesterdayCost, Last30DaysUSD: &monthCost, FetchedAt: now.Format(time.RFC3339), PeriodEnd: now.UTC().Format(time.RFC3339), Days: []accountUsageDay{
						{Date: todayDate, CostUSD: todayCost, Requests: 31, Input: 10000, Output: 200, Cached: 5000},
						{Date: remoteDate, CostUSD: yesterdayCost, Requests: 124, Input: 53012, Output: 600, Cached: 44000},
					}},
				}
				u.owner.usageHistory.mu.Lock()
				u.owner.usageHistory.disk.Scopes[usageAccountID(u.owner.apiKey, u.owner.config.OrgID)] = usageHistoryScope{
					StartedAt: now.AddDate(0, 0, -2).Format(time.RFC3339),
					Days: []usageHistoryDay{
						{Date: now.Format("2006-01-02"), usageSummary: usageSummary{Requests: 5, Priced: 3, CostUSD: "0.345678000", costNanos: 345678000}},
						{Date: now.AddDate(0, 0, -1).Format("2006-01-02"), usageSummary: usageSummary{Requests: 4, Priced: 4, CostUSD: "0.854321000", costNanos: 854321000}},
					},
				}
				u.owner.usageHistory.mu.Unlock()
				u.owner.mu.Unlock()
				u.refreshState()
				nativeTestWait(t, u, func() bool {
					return !u.busy["GET/api/state"] && nativeString(nativeMap(u.state["billing"]), "status") == "ready" && u.language == lang
				})
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				h.target(u.tr("Refresh balance and usage", "Actualizar saldo y uso"), semantic.Button)
				nativeGridCapture(t, h, "account-usage-"+fmtSize(size)+"-"+lang)
				// Exercise the same refresh that used to erase the fixture on
				// slower CI runners before checking the summary.
				u.refreshState()
				nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
				h.frame()
				hasLabel := func(want string, exact bool) bool {
					for _, node := range h.nodes() {
						if exact && node.Desc.Label == want || !exact && (strings.Contains(node.Desc.Label, want) || strings.Contains(node.Desc.Description, want)) {
							return true
						}
					}
					return false
				}
				// Short balance and period spend in the KPI row.
				for _, amount := range []string{"$248.62", "$10.16"} {
					if !hasLabel(amount, true) {
						t.Fatalf("short account metric %q absent from layout", amount)
					}
				}
				// Each Kilo day is reachable through its bar: date, short cost and requests.
				for _, day := range []string{uxaDayLabel(lang, remoteDate) + " · $8.96 · 124", uxaDayLabel(lang, todayDate) + " · $1.20 · 31"} {
					if !hasLabel(day, false) {
						t.Fatalf("daily Kilo usage %q missing from the chart", day)
					}
				}
				// The sources footnote expands to the exact amounts.
				nativeMenuWheel(h, image.Pt(size.X-100, size.Y-140), 3000)
				h.click(u.tr("Where this data comes from", "De dónde salen estos datos"), semantic.Button)
				if !u.expanded["activity.sources"] {
					t.Fatal("sources note did not open")
				}
				for _, amount := range []string{"$248.623145", "$10.160236"} {
					if !hasLabel(amount, false) {
						t.Fatalf("precise account metric %q absent from layout", amount)
					}
				}
				nativeGridCapture(t, h, "account-usage-daily-"+fmtSize(size)+"-"+lang)
				// Local device totals stay separate, in the Conversations tab.
				nativeMenuWheel(h, image.Pt(size.X-100, size.Y-140), -3000)
				h.click(u.tr("Conversations", "Conversaciones"), semantic.Button)
				if !hasLabel(u.tr("Observed on this device", "Observado en este dispositivo"), false) || hasLabel("$248.623145", false) {
					t.Fatal("local history is not shown separately from Kilo data")
				}
			})
		}
	}
}

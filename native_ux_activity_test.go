//go:build desktop

package main

import (
	"fmt"
	"image"
	"net/http"
	"strings"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

var image1280x1100 = image.Pt(1280, 1100)

// uxaSeedActivityForTest fills the app (the source behind /api/state) with 30
// days of Kilo usage, a per-model breakdown and captured requests.
func uxaSeedActivityForTest(t *testing.T, u *nativeUI, models bool) {
	t.Helper()
	now := time.Now()
	balance := "182.400000000"
	costs := []int64{3100, 4200, 2700, 400, 200, 5600, 6300, 4900, 7200, 3800, 600, 300, 6800, 4300}
	days := []accountUsageDay{}
	var total int64
	for i := 0; i < 30; i++ {
		cents := costs[(13-i%14+14)%14]
		total += cents * 1e7
		days = append(days, accountUsageDay{Date: now.UTC().AddDate(0, 0, -i).Format("2006-01-02"), CostUSD: dollars(cents * 1e7), Requests: cents / 17, Input: cents * 9000, Output: cents * 400, Cached: cents * 5600, CacheWrite: cents * 800, Errors: int64(i % 3)})
	}
	today, yesterday, month := days[0].CostUSD, days[1].CostUSD, dollars(total)
	usage := accountUsageSnapshot{Status: "ready", Scope: "self", TimeZone: "UTC", TodayUSD: &today, YesterdayUSD: &yesterday, Last30DaysUSD: &month, FetchedAt: now.Format(time.RFC3339), PeriodEnd: now.UTC().Format(time.RFC3339), Days: days}
	if models {
		for i, d := range days {
			for j, m := range []struct {
				id    string
				share int64
			}{{"vendor/one", 48}, {"anthropic/claude-sonnet-4.6", 30}, {"google/gemini-3.1-pro", 14}, {"z-ai/glm-5", 5}, {"mistral/devstral", 2}, {"qwen/qwen3-coder", 1}, {"deepseek/deepseek-v4", 1}} {
				cost, _ := uxaNanos(d.CostUSD)
				usage.Models = append(usage.Models, accountUsageModel{Date: d.Date, Model: m.id, CostUSD: dollars(cost * m.share / 100), Requests: int64(10*i + j)})
			}
		}
	}
	u.owner.mu.Lock()
	u.owner.syncBillingIdentityLocked()
	u.owner.billing.snapshot = billingSnapshot{Status: "ready", Scope: "organization", OrgID: u.owner.config.OrgID, BalanceUSD: &balance, FetchedAt: now.Add(-time.Minute).Format(time.RFC3339), Usage: usage}
	u.owner.captureEnabled = true
	u.owner.events = nil
	for i, r := range []struct {
		source, model string
		status        int
		in, out       int64
		cost          string
	}{
		{"claude-session", "anthropic/claude-sonnet-4.6", 200, 18200, 1100, "0.071234"},
		{"claude-session", "vendor/one", 200, 9800, 640, "0.009120"},
		{"codex-thread", "vendor/one", 200, 24100, 2300, "0.025400"},
		{"codex-thread", "google/gemini-3.1-pro", 429, 12000, 0, ""},
		{"unassigned", "z-ai/glm-5", 200, 2100, 380, "0.003100"},
	} {
		in, out, cached := r.in, r.out, r.in*8/10
		detail := &requestUsage{Source: r.source, Model: r.model, Input: &in, Output: &out, Cached: &cached}
		if r.cost != "" {
			cost := r.cost
			detail.CostUSD, detail.CostSource = &cost, "usage.cost"
		}
		id := fmt.Sprint(900 + i)
		u.owner.events = append(u.owner.events, event{Usage: detail, ID: id, HasDetails: i != 4, At: now.Add(-time.Duration(i) * 37 * time.Second).Format(time.RFC3339), Method: "POST", Path: "/v1/messages", Status: r.status, Duration: 4800 - int64(i)*300})
		body := `{"model":"` + r.model + `","max_tokens":8192,"stream":true,"messages":[{"role":"user","content":"synthetic prompt"}]}`
		part := tracePart{Headers: http.Header{"Authorization": {"Bearer ••••••••"}, "Anthropic-Version": {"2023-06-01"}}, Body: body, Bytes: int64(len(body))}
		u.owner.traces[id] = &requestTrace{ID: id, Request: part, UpstreamRequest: part, UpstreamResponse: part, Response: part, UpstreamStatus: r.status}
	}
	u.owner.mu.Unlock()
	u.refreshState()
	nativeTestWait(t, u, func() bool {
		return !u.busy["GET/api/state"] && nativeString(nativeMap(u.state["billing"]), "status") == "ready" && len(nativeArray(u.state, "events")) == 5
	})
}

func uxaHasLabel(h *nativePointerHarness, text string) bool {
	for _, node := range h.nodes() {
		if strings.Contains(node.Desc.Label, text) || strings.Contains(node.Desc.Description, text) {
			return true
		}
	}
	return false
}

func TestUXActivitySummaryKPIsChartModelsAndErrorsLink(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			h := newNativePointerHarness(t, image1280x1100)
			u := h.u
			u.language = lang
			u.owner.mu.Lock()
			u.owner.config.Language = lang
			u.owner.mu.Unlock()
			u.page = "activity"
			uxaSeedActivityForTest(t, u, true)
			h.frame()
			for _, want := range []string{"$182.40", u.tr("Team credit", "Crédito del equipo"), u.tr("Spend per day", "Gasto por día"), u.tr("By model", "Por modelo"), "Very Long First Model Name", u.tr("Others", "Otros"), u.tr("Where this data comes from", "De dónde salen estos datos")} {
				if !uxaHasLabel(h, want) {
					t.Fatalf("summary is missing %q", want)
				}
			}
			if uxaHasLabel(h, "harness") || uxaHasLabel(h, "Por harness") {
				t.Fatal("the removed per-harness panel came back")
			}
			// 14 days by default: one hover target per day, today labelled.
			bars := 0
			for _, node := range h.nodes() {
				if node.Desc.Class == semantic.Button && strings.Contains(node.Desc.Description, u.tr(" requests", " peticiones")) {
					bars++
				}
			}
			if bars != 14 {
				t.Fatalf("expected 14 day bars, got %d", bars)
			}
			h.click(u.tr("7 days", "7 días"), semantic.Button)
			if u.uxa().period != "7" || !h.selected(u.tr("7 days", "7 días"), semantic.Button) {
				t.Fatalf("period selector did not switch to 7 days: %q %v", u.uxa().period, h.target(u.tr("7 days", "7 días"), semantic.Button).Desc)
			}
			h.click(u.tr("view", "ver"), semantic.Button)
			if u.uxa().tab != "requests" || !u.uxa().errorsOnly {
				t.Fatal("errors link did not open the filtered request list")
			}
			h.frame()
			if !uxaHasLabel(h, uxaStatusReason(lang, 429)) {
				t.Fatal("errors filter does not show the failed request")
			}
			for _, node := range h.nodes() {
				if strings.Contains(node.Desc.Label, "18.2K") || strings.Contains(node.Desc.Label, "18,2K") {
					t.Fatal("errors filter kept successful requests")
				}
			}
		})
	}
}

func TestUXActivityModelBreakdownHiddenWithoutData(t *testing.T) {
	h := newNativePointerHarness(t, image1280x1100)
	u := h.u
	u.page = "activity"
	uxaSeedActivityForTest(t, u, false)
	h.frame()
	if uxaHasLabel(h, "By model") || uxaHasLabel(h, "Others") {
		t.Fatal("model panel shown without a model breakdown from Kilo")
	}
	if !uxaHasLabel(h, "Spend per day") {
		t.Fatal("daily chart missing")
	}
}

func TestUXActivityUnavailableKiloFallsBackToLocalHistory(t *testing.T) {
	h := newNativePointerHarness(t, image1280x1100)
	u := h.u
	u.page = "activity"
	now := time.Now()
	u.owner.mu.Lock()
	u.owner.syncBillingIdentityLocked()
	u.owner.billing.snapshot.Status, u.owner.billing.snapshot.Error = "unavailable", "Kilo rejected the saved credentials. Sign in again."
	u.owner.billing.snapshot.Usage.Status, u.owner.billing.snapshot.Usage.Error = "unavailable", "Kilo rejected the saved credentials. Sign in again."
	u.owner.usageHistory.mu.Lock()
	u.owner.usageHistory.disk.Scopes[usageAccountID(u.owner.apiKey, u.owner.config.OrgID)] = usageHistoryScope{
		StartedAt: now.AddDate(0, 0, -3).Format(time.RFC3339),
		Days:      []usageHistoryDay{{Date: now.Format("2006-01-02"), usageSummary: usageSummary{Requests: 5, Priced: 5, CostUSD: "0.345678000", costNanos: 345678000, Input: 1000, Cached: 600}}},
	}
	u.owner.usageHistory.mu.Unlock()
	u.owner.mu.Unlock()
	u.refreshState()
	nativeTestWait(t, u, func() bool {
		return !u.busy["GET/api/state"] && nativeString(nativeMap(u.state["billing"]), "status") == "unavailable" && len(nativeArray(nativeMap(u.state["usageHistory"]), "days")) == 1
	})
	h.frame()
	for _, want := range []string{"Kilo data is unavailable", "Showing what this device observed instead", "Local data", "$0.35", "This device only", "60%"} {
		if !uxaHasLabel(h, want) {
			t.Fatalf("local fallback is missing %q", want)
		}
	}
	// Days before local history started are unknown, not zero.
	if !uxaHasLabel(h, "no data") {
		t.Fatal("unknown local days were not labelled")
	}
}

func TestUXActivityRequestsInspectorStagesAndClose(t *testing.T) {
	h := newNativePointerHarness(t, image1280x1100)
	u := h.u
	u.page = "activity"
	uxaSeedActivityForTest(t, u, true)
	h.frame()
	h.click("Requests", semantic.Button)
	for _, want := range []string{"Claude Code", "Codex", "Claude Sonnet", "18.2K → 1.1K", "$0.071", "provider limit", "Request details capture is on", "Errors only (1)"} {
		if !uxaHasLabel(h, want) {
			t.Fatalf("request table missing %q", want)
		}
	}
	h.click("$0.071", semantic.Button)
	nativeTestWait(t, u, func() bool { return u.trace != nil && u.trace.ID == "900" })
	h.frame()
	if u.uxa().selected != "900" || !uxaHasLabel(h, "Claude Code → Claude Sonnet") || !uxaHasLabel(h, "$0.071234") || !uxaHasLabel(h, "4.8 s") {
		t.Fatal("inspector did not open with the request summary")
	}
	for _, stage := range []struct{ label, want string }{{"Sent to Kilo", "upstreamRequest"}, {"Response", "response"}} {
		h.click(stage.label, semantic.Button)
		if u.traceStage != stage.want || u.value("activity.body") == "" || u.value("activity.headers") == "" {
			t.Fatalf("stage %s not shown: %s", stage.label, u.traceStage)
		}
	}
	h.click("From Kilo", semantic.Button)
	if u.traceStage != "upstreamResponse" {
		t.Fatal("Kilo response not reachable")
	}
	h.click("Close inspector", semantic.Button)
	if u.uxa().selected != "" || u.trace != nil {
		t.Fatal("inspector did not close")
	}
	// A request without stored content explains why instead of loading forever.
	h.click("$0.003", semantic.Button)
	if !uxaHasLabel(h, "was not stored") {
		t.Fatal("missing explanation for a request without content")
	}
}

func TestUXActivityCaptureOffExplainsNothingIsStored(t *testing.T) {
	h := newNativePointerHarness(t, image1280x1100)
	u := h.u
	u.page = "activity"
	u.uxa().tab = "requests"
	h.frame()
	if !uxaHasLabel(h, "Capture is off, so no requests are listed and no content is stored") || !uxaHasLabel(h, "Request details capture is off") {
		t.Fatal("capture-off state not explained")
	}
	if !uxaHasLabel(h, "Save request content") {
		t.Fatal("capture switch missing")
	}
}

func TestUXActivityFormatting(t *testing.T) {
	for _, tc := range []struct{ lang, want string }{{"en", "3,214"}, {"es", "3.214"}} {
		if got := uxaCount(tc.lang, 3214); got != tc.want {
			t.Errorf("count %s: %s", tc.lang, got)
		}
	}
	if uxaCompact("es", 18200) != "18,2K" || uxaCompact("en", 41_200_000) != "41.2M" || uxaCompact("en", 640) != "640" {
		t.Error("compact token counts")
	}
	for value, want := range map[string]string{"0.071234": "$0.071", "0.0009": "<$0.001", "0": "$0", "2.5": "$2.50", "bad": "—"} {
		if got := uxaMoneyCell(value); got != want {
			t.Errorf("money cell %s: %s", value, got)
		}
	}
	if uxaDayLabel("es", "2026-10-06") != "Mar 6 oct" || uxaDayLabel("en", "2026-10-06") != "Tue 6 Oct" {
		t.Error("day labels: " + uxaDayLabel("es", "2026-10-06"))
	}
	if uxaNiceCeil(7_200_000_000) != 10_000_000_000 || uxaNiceCeil(1_500_000_000) != 2_000_000_000 || uxaNiceCeil(0) != 1e9 {
		t.Error("nice ceiling")
	}
	read, written, uncached, total := uxaCacheSplit(100, 60, 10)
	if read != 60 || written != 10 || uncached != 30 || total != 100 {
		t.Error("inclusive cache split")
	}
	if _, _, uncached, total = uxaCacheSplit(20, 60, 10); uncached != 20 || total != 90 {
		t.Error("exclusive cache split")
	}
	if uxaPercent(1, 0) != "—" {
		t.Error("unknown percentage shown as a number")
	}
}

func TestUXActivityModelBreakdownFoldsOthers(t *testing.T) {
	rows := []accountUsageModel{}
	for i, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		rows = append(rows, accountUsageModel{Date: "2026-10-10", Model: name, CostUSD: dollars(int64(10-i) * 1e9)}, accountUsageModel{Date: "2026-09-01", Model: name, CostUSD: dollars(1e12)})
	}
	got := uxaModelBreakdown(rows, map[string]bool{"2026-10-10": true}, 5, "Others", func(s string) string { return strings.ToUpper(s) })
	if len(got) != 6 || got[0].Name != "A" || got[0].Cost != 10e9 || got[5].Name != "Others" || got[5].Cost != (5+4)*1e9 {
		t.Fatalf("breakdown: %+v", got)
	}
}

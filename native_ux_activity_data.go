//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// uxaState is the Activity page's view state. It lives beside nativeUI rather
// than inside it so the redesign does not churn the shared UI struct.
type uxaState struct {
	period       string // today, 7, 14 or 30 days
	tab          string // summary, requests or conversations
	errorsOnly   bool
	selected     string // request ID open in the inspector
	pickedDay    string // day pinned by clicking a bar
	responseSide string // upstreamResponse or response
}

var uxaStates = struct {
	sync.Mutex
	m map[*nativeUI]*uxaState
}{m: map[*nativeUI]*uxaState{}}

func (u *nativeUI) uxa() *uxaState {
	uxaStates.Lock()
	defer uxaStates.Unlock()
	s := uxaStates.m[u]
	if s == nil {
		s = &uxaState{period: "14", tab: "summary", responseSide: "response"}
		uxaStates.m[u] = s
	}
	return s
}

func uxaPeriodDays(period string) int {
	switch period {
	case "today":
		return 1
	case "7":
		return 7
	case "30":
		return 30
	}
	return 14
}

// uxaDay is one bar of the daily chart. Known is false for days without data
// (before local history started); they are never drawn as zero spend.
type uxaDay struct {
	Date                                        string
	Cost                                        int64 // nanodollars
	CostKnown, Known, Today                     bool
	Requests, Errors, Input, Cached, CacheWrite int64
}

type uxaSeries struct {
	Source      string // kilo, local or empty
	Days        []uxaDay
	ErrorsKnown bool
}

func (s uxaSeries) totals() (cost int64, costKnown bool, requests, errors, input, cached, write int64) {
	for _, d := range s.Days {
		if d.CostKnown {
			cost += d.Cost
			costKnown = true
		}
		requests += d.Requests
		errors += d.Errors
		input += d.Input
		cached += d.Cached
		write += d.CacheWrite
	}
	return
}

func uxaNanos(value string) (int64, bool) {
	return billingMoney(json.RawMessage(strconv.Quote(value)), false)
}

// uxaKiloSeries returns the newest n UTC days, oldest first.
func uxaKiloSeries(usage nativeAccountUsage, n int) uxaSeries {
	series := uxaSeries{Source: "kilo", ErrorsKnown: true}
	count := min(n, len(usage.Days))
	for i := count - 1; i >= 0; i-- {
		d := usage.Days[i]
		cost, ok := uxaNanos(d.CostUSD)
		series.Days = append(series.Days, uxaDay{Date: d.Date, Cost: cost, CostKnown: ok, Known: true, Today: i == 0, Requests: d.Requests, Errors: d.Errors, Input: d.Input, Cached: d.Cached, CacheWrite: d.CacheWrite})
	}
	return series
}

// uxaLocalSeries rebuilds n local days from the device history. Days before
// the history started are unknown, not zero.
func uxaLocalSeries(history nativeUsageHistory, n int, now time.Time) uxaSeries {
	series := uxaSeries{Source: "local"}
	started := ""
	if at, err := time.Parse(time.RFC3339, history.StartedAt); err == nil {
		started = at.Local().Format("2006-01-02")
	}
	byDate := map[string]usageSummary{}
	for _, d := range history.Days {
		byDate[d.Date] = d.usageSummary
	}
	today := now.Local()
	for i := n - 1; i >= 0; i-- {
		date := today.AddDate(0, 0, -i).Format("2006-01-02")
		day := uxaDay{Date: date, Today: i == 0, Known: started != "" && date >= started}
		if s, ok := byDate[date]; ok {
			day.Known = true
			day.Requests, day.Input, day.Cached, day.CacheWrite = s.Requests, s.Input, s.Cached, s.CacheWrite
			if s.Priced > 0 {
				day.Cost, day.CostKnown = uxaNanos(s.CostUSD)
			}
		}
		series.Days = append(series.Days, day)
	}
	return series
}

// uxaCacheSplit separates input tokens into cache reads, cache writes and
// uncached input. Kilo and OpenAI-style usage count cached tokens inside the
// input total; when the cache parts exceed input, input clearly excludes them.
func uxaCacheSplit(input, cached, write int64) (read, written, uncached, total int64) {
	read, written = max(0, cached), max(0, write)
	if read+written > input {
		uncached = max(0, input)
	} else {
		uncached = input - read - written
	}
	return read, written, uncached, read + written + uncached
}

func uxaPercent(part, total int64) string {
	if total <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", math.Round(float64(part)*100/float64(total)))
}

type uxaModelSpend struct {
	Name     string
	Cost     int64
	Requests int64
}

// uxaModelBreakdown sums Kilo's per-model rows for the given dates, keeps the
// top entries and folds the rest into one "other" row.
func uxaModelBreakdown(rows []accountUsageModel, dates map[string]bool, top int, other string, name func(string) string) []uxaModelSpend {
	totals := map[string]*uxaModelSpend{}
	for _, row := range rows {
		if !dates[row.Date] {
			continue
		}
		cost, ok := uxaNanos(row.CostUSD)
		if !ok {
			continue
		}
		label := name(row.Model)
		entry := totals[label]
		if entry == nil {
			entry = &uxaModelSpend{Name: label}
			totals[label] = entry
		}
		entry.Cost += cost
		entry.Requests += row.Requests
	}
	list := make([]uxaModelSpend, 0, len(totals))
	for _, entry := range totals {
		list = append(list, *entry)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Cost != list[j].Cost {
			return list[i].Cost > list[j].Cost
		}
		return list[i].Name < list[j].Name
	})
	if len(list) > top+1 {
		rest := uxaModelSpend{Name: other}
		for _, entry := range list[top:] {
			rest.Cost += entry.Cost
			rest.Requests += entry.Requests
		}
		list = append(list[:top], rest)
	}
	return list
}

// uxaNiceCeil rounds a positive amount up to 1, 2 or 5 times a power of ten.
func uxaNiceCeil(nanos int64) int64 {
	if nanos <= 0 {
		return 1e9
	}
	value := float64(nanos) / 1e9
	step := math.Pow(10, math.Floor(math.Log10(value)))
	for _, m := range []float64{1, 2, 5, 10} {
		if m*step >= value-1e-12 {
			return max(int64(math.Round(m*step*1e9)), 1e7)
		}
	}
	return nanos
}

var (
	uxaWeekdaysEN = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	uxaWeekdaysES = []string{"Dom", "Lun", "Mar", "Mié", "Jue", "Vie", "Sáb"}
	uxaMonthsEN   = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	uxaMonthsES   = []string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}
)

// uxaDayLabel renders "Mon 6 Oct" / "Lun 6 oct" for a YYYY-MM-DD date.
func uxaDayLabel(language, date string) string {
	at, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	if language == "es" {
		return fmt.Sprintf("%s %d %s", uxaWeekdaysES[at.Weekday()], at.Day(), uxaMonthsES[at.Month()-1])
	}
	return fmt.Sprintf("%s %d %s", uxaWeekdaysEN[at.Weekday()], at.Day(), uxaMonthsEN[at.Month()-1])
}

// uxaCount groups thousands with "," in English and "." in Spanish.
func uxaCount(language string, n int64) string {
	raw := nativeCount(n)
	if language == "es" {
		return strings.ReplaceAll(raw, ",", ".")
	}
	return raw
}

// uxaCompact shortens token counts: 980, 18.2K, 41.2M (decimal comma in Spanish).
func uxaCompact(language string, n int64) string {
	var text string
	switch {
	case n < 1000:
		return uxaCount(language, n)
	case n < 1e6:
		text = fmt.Sprintf("%.1fK", float64(n)/1e3)
	default:
		text = fmt.Sprintf("%.1fM", float64(n)/1e6)
	}
	if language == "es" {
		text = strings.Replace(text, ".", ",", 1)
	}
	return text
}

// uxaMoneyCell shows a request cost with three decimals; exact values stay in the inspector.
func uxaMoneyCell(value string) string {
	nanos, ok := uxaNanos(value)
	if !ok || nanos < 0 {
		return "—"
	}
	switch {
	case nanos == 0:
		return "$0"
	case nanos < 1e6:
		return "<$0.001"
	case nanos < 1e9:
		return fmt.Sprintf("$0.%03d", min(int64(999), (nanos+5e5)/1e6))
	}
	return nativeMoneyShort(dollars(nanos))
}

// uxaRelativeAge renders how long ago an RFC 3339 timestamp was.
func uxaRelativeAge(language, stamp string, now time.Time) string {
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return ""
	}
	minutes := int(now.Sub(at).Minutes())
	switch {
	case minutes < 1:
		return helperText(language, "just now", "ahora mismo")
	case minutes < 60:
		return fmt.Sprintf(helperText(language, "%d min ago", "hace %d min"), minutes)
	}
	return at.Local().Format("2006-01-02 15:04")
}

// uxaHarness names the client from the session header the proxy recognised.
func uxaHarness(language, source string) string {
	switch source {
	case "codex-thread":
		return "Codex"
	case "claude-session":
		return "Claude Code"
	case "kilo-task":
		return "Kilo Code"
	case "client-session", "custom-session":
		return helperText(language, "Other client", "Otro cliente")
	}
	return "—"
}

func uxaStatusReason(language string, status int) string {
	reasons := map[int][2]string{
		400: {"bad request", "petición no válida"}, 401: {"credentials rejected", "credenciales rechazadas"},
		402: {"no credit", "sin saldo"}, 403: {"no access", "sin acceso"}, 404: {"not found", "no encontrado"},
		413: {"too large", "demasiado grande"}, 429: {"provider limit", "límite del proveedor"}, 499: {"cancelled", "cancelada"},
	}
	if reason, ok := reasons[status]; ok {
		return helperText(language, reason[0], reason[1])
	}
	if status >= 500 {
		return helperText(language, "provider error", "error del proveedor")
	}
	return ""
}

// uxaInPeriod reports whether an event timestamp falls within the selected local days.
func uxaInPeriod(stamp string, days int, now time.Time) bool {
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return true
	}
	local := now.Local()
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location()).AddDate(0, 0, -(days - 1))
	return !at.Before(start)
}

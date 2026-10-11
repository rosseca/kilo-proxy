package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const billingRefreshInterval = time.Minute

// Balances are the selected account's shared credit pool. Usage is always the
// authenticated user's billed spend in that account, never the team's total.
type billingSnapshot struct {
	Status     string               `json:"status"`
	Scope      string               `json:"scope"`
	OrgID      string               `json:"orgId,omitempty"`
	BalanceUSD *string              `json:"balanceUSD"`
	FetchedAt  string               `json:"fetchedAt,omitempty"`
	Error      string               `json:"error,omitempty"`
	Stale      bool                 `json:"stale"`
	Usage      accountUsageSnapshot `json:"usage"`
}

type accountUsageSnapshot struct {
	Status        string            `json:"status"`
	Scope         string            `json:"scope"`
	PeriodStart   string            `json:"periodStart,omitempty"`
	PeriodEnd     string            `json:"periodEnd,omitempty"`
	TimeZone      string            `json:"timeZone"`
	TodayUSD      *string           `json:"todayUSD"`
	YesterdayUSD  *string           `json:"yesterdayUSD"`
	Last30DaysUSD *string           `json:"last30DaysUSD"`
	Days          []accountUsageDay `json:"days"`
	// Models is Kilo's spend per model and UTC day. It is optional: the
	// grouped query is unverified against every account type, so a failed or
	// unusable response leaves it empty and the UI hides the breakdown.
	Models    []accountUsageModel `json:"models,omitempty"`
	FetchedAt string              `json:"fetchedAt,omitempty"`
	Error     string              `json:"error,omitempty"`
	Stale     bool                `json:"stale"`
}

type accountUsageDay struct {
	Date       string `json:"date"`
	CostUSD    string `json:"costUSD"`
	Requests   int64  `json:"requests"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	Cached     int64  `json:"cached"`
	CacheWrite int64  `json:"cacheWrite"`
	Errors     int64  `json:"errors"`
}

// Model names come from Kilo and stay in memory with the billing snapshot;
// they are never written to the local usage history.
type accountUsageModel struct {
	Date     string `json:"date"`
	Model    string `json:"model"`
	CostUSD  string `json:"costUSD"`
	Requests int64  `json:"requests"`
}

type billingCache struct {
	identity    [32]byte
	snapshot    billingSnapshot
	fetching    bool
	generation  uint64
	nextAttempt time.Time
}

func billingIdentity(key, org, endpoint string) [32]byte {
	return sha256.Sum256([]byte(key + "\x00" + org + "\x00" + endpoint))
}

// All cache access is under a.mu. The key itself is never kept in the snapshot.
func (a *app) syncBillingIdentityLocked() {
	identity := billingIdentity(a.apiKey, a.config.OrgID, a.accountURL)
	if identity == a.billing.identity {
		return
	}
	status := "loading"
	if a.apiKey == "" {
		status = "signed_out"
	}
	scope := "personal"
	if a.config.OrgID != "" {
		scope = "organization"
	}
	a.billing = billingCache{identity: identity, generation: a.billing.generation + 1, snapshot: billingSnapshot{
		Status: status, Scope: scope, OrgID: a.config.OrgID,
		Usage: accountUsageSnapshot{Status: status, Scope: "self", TimeZone: "UTC", Days: []accountUsageDay{}},
	}}
}

func (a *app) billingSnapshotLocked() billingSnapshot {
	a.syncBillingIdentityLocked()
	result := a.billing.snapshot
	// Even before a failed refresh completes, an old amount must look old.
	if at, err := time.Parse(time.RFC3339, result.FetchedAt); err == nil && time.Since(at) > 2*billingRefreshInterval {
		result.Stale = true
	}
	if at, err := time.Parse(time.RFC3339, result.Usage.FetchedAt); err == nil && time.Since(at) > 2*billingRefreshInterval {
		result.Usage.Stale = true
	}
	// A cached "today" from yesterday is not today's spend, even if a refresh
	// failed just after UTC midnight. Keep dated rows, but hide relative totals.
	if end, err := time.Parse(time.RFC3339, result.Usage.PeriodEnd); err == nil && end.UTC().Format("2006-01-02") != time.Now().UTC().Format("2006-01-02") {
		result.Usage.TodayUSD, result.Usage.YesterdayUSD, result.Usage.Last30DaysUSD = nil, nil, nil
		result.Usage.Status, result.Usage.Stale = "unavailable", true
		if result.Usage.Error == "" {
			result.Usage.Error = "Saved daily usage belongs to an earlier UTC day. Refresh to update it."
		}
	}
	return result
}

// Runtime enables background polling; explicit refresh remains available in
// tests and embedded callers. State and tray reads never wait for the network.
func (a *app) ensureBillingRefreshLocked(force bool) {
	a.syncBillingIdentityLocked()
	if a.apiKey == "" || a.billing.fetching || (!force && (!a.billingAutoRefresh || time.Now().Before(a.billing.nextAttempt))) {
		return
	}
	select {
	case <-a.quit:
		return
	default:
	}
	a.billing.fetching = true
	key, org, endpoint, transport := a.apiKey, a.config.OrgID, a.accountURL, a.transport
	identity, generation := a.billing.identity, a.billing.generation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		fresh := fetchBilling(ctx, endpoint, transport, key, org, time.Now().UTC())
		a.mu.Lock()
		defer a.mu.Unlock()
		if identity != billingIdentity(a.apiKey, a.config.OrgID, a.accountURL) || generation != a.billing.generation {
			return
		}
		previous := a.billing.snapshot
		if fresh.Status != "ready" && previous.BalanceUSD != nil {
			fresh.BalanceUSD, fresh.FetchedAt, fresh.Stale = previous.BalanceUSD, previous.FetchedAt, true
		}
		if fresh.Usage.Status != "ready" && previous.Usage.FetchedAt != "" {
			status, message := fresh.Usage.Status, fresh.Usage.Error
			fresh.Usage = previous.Usage
			fresh.Usage.Status, fresh.Usage.Error, fresh.Usage.Stale = status, message, true
		}
		a.billing.snapshot, a.billing.fetching = fresh, false
		a.billing.nextAttempt = time.Now().Add(billingRefreshInterval)
	}()
}

func (a *app) refreshBilling(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decodeBody(w, r, &input) {
		return
	}
	a.mu.Lock()
	a.ensureBillingRefreshLocked(true)
	a.mu.Unlock()
	a.state(w)
}

func billingGet(ctx context.Context, endpoint string, transport http.RoundTripper, key, org, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
	if err != nil {
		return errors.New("The Kilo account endpoint is invalid.")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Kilo-Proxy/"+version)
	if org != "" {
		req.Header.Set("X-KiloCode-OrganizationId", org)
	}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("Could not reach Kilo. Refresh to try again.")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case 401:
			return errors.New("Kilo rejected the saved credentials. Sign in again.")
		case 403:
			return errors.New("Kilo does not allow this account to view these billing data.")
		case 429:
			return errors.New("Kilo rate-limited the billing request. Try again later.")
		default:
			return fmt.Errorf("Kilo billing is unavailable (HTTP %d).", resp.StatusCode)
		}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return errors.New("Kilo returned an invalid billing response.")
	}
	if err := json.Unmarshal(data, result); err != nil {
		return errors.New("Kilo returned an invalid billing response.")
	}
	return nil
}

func fetchBilling(ctx context.Context, endpoint string, transport http.RoundTripper, key, org string, now time.Time) billingSnapshot {
	scope := "personal"
	if org != "" {
		scope = "organization"
	}
	result := billingSnapshot{Status: "unavailable", Scope: scope, OrgID: org}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		var response struct {
			Balance json.RawMessage `json:"balance"`
			Success *bool           `json:"success"`
			Error   json.RawMessage `json:"error"`
		}
		err := billingGet(ctx, endpoint, transport, key, org, "/api/profile/balance", &response)
		if err == nil && ((response.Success != nil && !*response.Success) || (len(response.Error) != 0 && string(response.Error) != "null")) {
			err = errors.New("Kilo returned an invalid balance response.")
		}
		if err == nil {
			amount, ok := billingMoney(response.Balance, false)
			if !ok {
				err = errors.New("Kilo did not return a valid balance.")
			} else {
				value := billingDollars(amount)
				result.Status, result.BalanceUSD, result.FetchedAt = "ready", &value, now.Format(time.RFC3339)
			}
		}
		if err != nil {
			result.Error = err.Error()
		}
	}()
	go func() {
		defer wg.Done()
		result.Usage = fetchAccountUsage(ctx, endpoint, transport, key, org, now)
	}()
	wg.Wait()
	return result
}

// Parse decimal JSON values using rational arithmetic. Negative balances are
// valid: Kilo may finish a request after its balance check consumed the pool.
func billingMoney(raw json.RawMessage, micro bool) (int64, bool) {
	if !json.Valid(raw) {
		return 0, false
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return 0, false
	}
	var text string
	switch v := value.(type) {
	case json.Number:
		text = string(v)
	case string:
		text = v
	default:
		return 0, false
	}
	if len(text) == 0 || len(text) > 64 || !json.Valid([]byte(text)) || (text[0] != '-' && (text[0] < '0' || text[0] > '9')) {
		return 0, false
	}
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		exponent, err := strconv.Atoi(text[i+1:])
		if err != nil || exponent < -18 || exponent > 18 {
			return 0, false
		}
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return 0, false
	}
	scale := int64(1e9)
	if micro {
		scale = 1000
	}
	r.Mul(r, big.NewRat(scale, 1))
	negative := r.Sign() < 0
	r.Abs(r)
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(r.Num(), r.Denom(), remainder)
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(r.Denom()) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	// Leave headroom for summing at most 30 daily rows.
	if !whole.IsInt64() || whole.Int64() > 1e17 {
		return 0, false
	}
	amount := whole.Int64()
	if negative {
		amount = -amount
	}
	return amount, true
}

func billingDollars(amount int64) string {
	if amount < 0 {
		return "-" + dollars(-amount)
	}
	return dollars(amount)
}

func fetchAccountUsage(ctx context.Context, endpoint string, transport http.RoundTripper, key, org string, now time.Time) accountUsageSnapshot {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	start := today.AddDate(0, 0, -29)
	result := accountUsageSnapshot{Status: "unavailable", Scope: "self", TimeZone: "UTC", PeriodStart: start.Format(time.RFC3339), PeriodEnd: now.Format(time.RFC3339), Days: []accountUsageDay{}}
	input := map[string]any{"startDate": result.PeriodStart, "endDate": result.PeriodEnd, "granularity": "day", "costSource": "cost", "personalScope": "personal-only", "viewAs": "self", "groupBy": []string{}, "limit": 1000}
	if org != "" {
		input["organizationId"] = org
	}
	encoded, _ := json.Marshal(input)
	var response struct {
		Result *struct {
			Data *struct {
				Rows *[]struct {
					Datetime         string                     `json:"datetime"`
					Dimensions       map[string]json.RawMessage `json:"dimensions"`
					CostMicrodollars json.RawMessage            `json:"costMicrodollars"`
					RequestCount     *int64                     `json:"requestCount"`
					InputTokens      *int64                     `json:"inputTokens"`
					OutputTokens     *int64                     `json:"outputTokens"`
					CacheWriteTokens *int64                     `json:"cacheWriteTokens"`
					CacheHitTokens   *int64                     `json:"cacheHitTokens"`
					ErrorCount       *int64                     `json:"errorCount"`
				} `json:"rows"`
				EffectiveGranularity string `json:"effectiveGranularity"`
			} `json:"data"`
		} `json:"result"`
	}
	err := billingGet(ctx, endpoint, transport, key, org, "/api/trpc/usageAnalytics.getTable?"+url.Values{"input": {string(encoded)}}.Encode(), &response)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	invalid := func() accountUsageSnapshot {
		result.Error = "Kilo did not return valid daily usage. No missing values are counted as zero."
		return result
	}
	if response.Result == nil || response.Result.Data == nil || response.Result.Data.Rows == nil || response.Result.Data.EffectiveGranularity != "day" || len(*response.Result.Data.Rows) > 30 {
		return invalid()
	}
	days := make(map[string]accountUsageDay)
	var total int64
	for _, row := range *response.Result.Data.Rows {
		var at time.Time
		for _, format := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02"} {
			parsed, parseErr := time.Parse(format, row.Datetime)
			if parseErr == nil {
				at = parsed.UTC()
				break
			}
		}
		date := at.Format("2006-01-02")
		_, duplicate := days[date]
		amount, ok := billingMoney(row.CostMicrodollars, true)
		if !ok || amount < 0 || duplicate || at.Before(start) || at.After(today) || at.Hour() != 0 || at.Minute() != 0 || at.Second() != 0 || len(row.Dimensions) != 0 {
			return invalid()
		}
		for _, count := range []*int64{row.RequestCount, row.InputTokens, row.OutputTokens, row.CacheWriteTokens, row.CacheHitTokens, row.ErrorCount} {
			if count == nil || *count < 0 {
				return invalid()
			}
		}
		total += amount
		days[date] = accountUsageDay{Date: date, CostUSD: dollars(amount), Requests: *row.RequestCount, Input: *row.InputTokens, Output: *row.OutputTokens, CacheWrite: *row.CacheWriteTokens, Cached: *row.CacheHitTokens, Errors: *row.ErrorCount}
	}
	for at := today; !at.Before(start); at = at.AddDate(0, 0, -1) {
		date := at.Format("2006-01-02")
		day, exists := days[date]
		if !exists {
			day = accountUsageDay{Date: date, CostUSD: dollars(0)}
		}
		result.Days = append(result.Days, day)
	}
	totalUSD := dollars(total)
	result.Status, result.FetchedAt = "ready", now.Format(time.RFC3339)
	result.TodayUSD, result.YesterdayUSD, result.Last30DaysUSD = &result.Days[0].CostUSD, &result.Days[1].CostUSD, &totalUSD
	result.Models = fetchAccountUsageModels(ctx, endpoint, transport, key, org, input, start, today)
	return result
}

// fetchAccountUsageModels asks the same usage table for one row per UTC day
// and model. Any failure returns nil: the breakdown is an optional extra and
// must never invent data or make the daily totals unavailable.
func fetchAccountUsageModels(ctx context.Context, endpoint string, transport http.RoundTripper, key, org string, daily map[string]any, start, today time.Time) []accountUsageModel {
	input := make(map[string]any, len(daily))
	for name, value := range daily {
		input[name] = value
	}
	input["groupBy"], input["limit"] = []string{"model"}, 5000
	encoded, _ := json.Marshal(input)
	var raw json.RawMessage
	if billingGet(ctx, endpoint, transport, key, org, "/api/trpc/usageAnalytics.getTable?"+url.Values{"input": {string(encoded)}}.Encode(), &raw) != nil {
		return nil
	}
	return parseAccountUsageModels(raw, start, today)
}

// parseAccountUsageModels tolerates rows it cannot use (missing model, bad
// date, invalid cost) by skipping them, unlike the strict daily parser. Rows
// for the same day and model are summed. Unusable responses return nil.
func parseAccountUsageModels(raw []byte, start, today time.Time) []accountUsageModel {
	var response struct {
		Result *struct {
			Data *struct {
				Rows []struct {
					Datetime         string                     `json:"datetime"`
					Dimensions       map[string]json.RawMessage `json:"dimensions"`
					CostMicrodollars json.RawMessage            `json:"costMicrodollars"`
					RequestCount     *int64                     `json:"requestCount"`
				} `json:"rows"`
				EffectiveGranularity string `json:"effectiveGranularity"`
			} `json:"data"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Result == nil || response.Result.Data == nil {
		return nil
	}
	data := response.Result.Data
	if data.EffectiveGranularity != "" && data.EffectiveGranularity != "day" {
		return nil
	}
	type key struct{ date, model string }
	costs, requests, order := map[key]int64{}, map[key]int64{}, []key{}
	for _, row := range data.Rows {
		var at time.Time
		parsed := false
		for _, format := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02"} {
			if value, err := time.Parse(format, row.Datetime); err == nil {
				at, parsed = value.UTC(), true
				break
			}
		}
		if !parsed {
			continue
		}
		day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
		if day.Before(start) || day.After(today) {
			continue
		}
		var model string
		if json.Unmarshal(row.Dimensions["model"], &model) != nil {
			continue
		}
		model = strings.TrimSpace(model)
		if model == "" || len(model) > 200 || strings.IndexFunc(model, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			continue
		}
		amount, ok := billingMoney(row.CostMicrodollars, true)
		if !ok || amount < 0 {
			continue
		}
		k := key{day.Format("2006-01-02"), model}
		if costs[k] > 1e17-amount {
			continue
		}
		if _, seen := costs[k]; !seen {
			order = append(order, k)
		}
		costs[k] += amount
		if row.RequestCount != nil && *row.RequestCount > 0 {
			requests[k] += *row.RequestCount
		}
	}
	if len(order) == 0 {
		return nil
	}
	result := make([]accountUsageModel, 0, len(order))
	for _, k := range order {
		result = append(result, accountUsageModel{Date: k.date, Model: k.model, CostUSD: dollars(costs[k]), Requests: requests[k]})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Date != result[j].Date {
			return result[i].Date > result[j].Date
		}
		return result[i].Model < result[j].Model
	})
	return result
}

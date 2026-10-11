package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var billingTestNow = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

func billingTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func billingTestRow(date, cost string) string {
	return `{"datetime":"` + date + `","dimensions":{},"costMicrodollars":` + cost + `,"requestCount":2,"inputTokens":100,"outputTokens":20,"cacheWriteTokens":10,"cacheHitTokens":60,"errorCount":0}`
}

func billingTestUsage(rows string) string {
	return `{"result":{"data":{"rows":[` + rows + `],"effectiveGranularity":"day"}}}`
}

func TestBillingOfficialContractScopeAndExactDecimals(t *testing.T) {
	for _, org := range []string{"", "team-id"} {
		t.Run(org, func(t *testing.T) {
			transport := usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("X-KiloCode-OrganizationId") != org || r.Header.Get("Cookie") != "" {
					t.Error("wrong billing request authentication or method")
				}
				if r.URL.Path == "/api/profile/balance" {
					return billingTestResponse(200, `{"balance":-0.062515}`), nil
				}
				if r.URL.Path != "/api/trpc/usageAnalytics.getTable" {
					t.Errorf("unexpected billing path: %s", r.URL.Path)
				}
				var input map[string]any
				if err := json.Unmarshal([]byte(r.URL.Query().Get("input")), &input); err != nil {
					t.Error(err)
				}
				if input["viewAs"] != "self" || input["costSource"] != "cost" || input["personalScope"] != "personal-only" || input["granularity"] != "day" || input["startDate"] != "2026-08-23T00:00:00Z" || input["endDate"] != "2026-09-21T12:00:00Z" {
					t.Errorf("wrong own billed usage query: %+v", input)
				}
				if org == "" && input["organizationId"] != nil || org != "" && input["organizationId"] != org {
					t.Error("usage crossed account scope")
				}
				return billingTestResponse(200, billingTestUsage(billingTestRow("2026-09-20T00:00:00Z", "1234567.123")+","+billingTestRow("2026-09-21T00:00:00Z", "1"))), nil
			})
			result := fetchBilling(context.Background(), "https://api.kilo.ai", transport, "secret", org, billingTestNow)
			if result.Status != "ready" || result.BalanceUSD == nil || *result.BalanceUSD != "-0.062515000" || result.OrgID != org || result.Usage.Status != "ready" {
				t.Fatalf("billing response: %+v", result)
			}
			if *result.Usage.TodayUSD != "0.000001000" || *result.Usage.YesterdayUSD != "1.234567123" || *result.Usage.Last30DaysUSD != "1.234568123" || len(result.Usage.Days) != 30 || result.Usage.Days[1].Cached != 60 {
				t.Fatalf("inexact or incomplete usage: %+v", result.Usage)
			}
			if result.Usage.Days[2].CostUSD != "0.000000000" || result.Usage.Scope != "self" || result.Usage.TimeZone != "UTC" {
				t.Fatal("invalid zero bucket or unclear scope")
			}
		})
	}
}

func TestBillingMoneyRejectsInvalidAndPreservesSmallOrNegative(t *testing.T) {
	for _, invalid := range []string{`null`, `{}`, `true`, `"NaN"`, `"Inf"`, `"1/2"`, `" 1"`, `"1 "`, `"+1"`, `"0x10"`, `1e99`, `1e-99`, `100000001`, `""`, `1 2`} {
		if _, ok := billingMoney(json.RawMessage(invalid), false); ok {
			t.Errorf("accepted invalid money: %s", invalid)
		}
	}
	for _, tc := range []struct{ input, want string }{{`0`, "0.000000000"}, {`"0.000000001"`, "0.000000001"}, {`-0.0000000006`, "-0.000000001"}, {`-12.34`, "-12.340000000"}, {`1e-6`, "0.000001000"}} {
		value, ok := billingMoney(json.RawMessage(tc.input), false)
		if !ok || billingDollars(value) != tc.want {
			t.Errorf("%s became %d (%t), want %s", tc.input, value, ok, tc.want)
		}
	}
}

func TestBillingUTCDateRolloverDoesNotMislabelRelativeTotals(t *testing.T) {
	a := testApp(t)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.apiKey = "key"
	a.syncBillingIdentityLocked()
	amount := "3.500000000"
	a.billing.snapshot.BalanceUSD = &amount
	a.billing.snapshot.Usage = accountUsageSnapshot{Status: "ready", PeriodEnd: time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339), TodayUSD: &amount, YesterdayUSD: &amount, Last30DaysUSD: &amount, Days: []accountUsageDay{{Date: "2026-09-20", CostUSD: amount}}}
	result := a.billingSnapshotLocked()
	if result.BalanceUSD == nil || result.Usage.TodayUSD != nil || result.Usage.YesterdayUSD != nil || result.Usage.Last30DaysUSD != nil || !result.Usage.Stale || result.Usage.Status != "unavailable" || len(result.Usage.Days) != 1 {
		t.Fatalf("yesterday's cached total was mislabeled as today: %+v", result)
	}
}

func TestBillingMissingMalformedAndDeniedNeverBecomeZero(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"balance":null}`, `{"balance":"NaN"}`, `{"balance":0,"success":false}`, `{"balance":0,"error":"private upstream text"}`, `{"balance":0} trailing`} {
		transport := usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/api/profile/balance" {
				return billingTestResponse(200, body), nil
			}
			return billingTestResponse(403, `{"error":"secret"}`), nil
		})
		result := fetchBilling(context.Background(), "https://api.kilo.ai", transport, "secret", "team", billingTestNow)
		if result.Status != "unavailable" || result.BalanceUSD != nil || result.Usage.Status != "unavailable" || result.Usage.TodayUSD != nil || strings.Contains(result.Error+result.Usage.Error, "private upstream") || strings.Contains(result.Error+result.Usage.Error, "secret") {
			t.Errorf("missing/denied money became valid or disclosed server body: %+v", result)
		}
	}
	transport := usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/profile/balance" {
			return billingTestResponse(200, `{"balance":12.3}`), nil
		}
		return billingTestResponse(401, `{"error":"private"}`), nil
	})
	result := fetchBilling(context.Background(), "https://api.kilo.ai", transport, "secret", "team", billingTestNow)
	if result.Status != "ready" || result.Usage.Status != "unavailable" || !strings.Contains(result.Usage.Error, "Sign in again") {
		t.Fatalf("independent endpoints not preserved: %+v", result)
	}
}

func TestBillingInvalidDailyDataNeverCreatesHistory(t *testing.T) {
	row := billingTestRow("2026-09-20T00:00:00Z", "1")
	for _, body := range []string{`{}`, `{"result":{"data":{"effectiveGranularity":"day"}}}`, strings.Replace(billingTestUsage(row), `"day"`, `"week"`, 1), billingTestUsage(row + "," + row), billingTestUsage(billingTestRow("2026-09-22T00:00:00Z", "1")), billingTestUsage(billingTestRow("2026-09-20T01:00:00Z", "1")), billingTestUsage(billingTestRow("2026-09-20T00:00:00Z", "-1")), strings.Replace(billingTestUsage(row), `"requestCount":2`, `"requestCount":null`, 1), strings.Replace(billingTestUsage(row), `"dimensions":{}`, `"dimensions":{"model":"unexpected"}`, 1)} {
		transport := usageMemoryTransport(func(*http.Request) (*http.Response, error) { return billingTestResponse(200, body), nil })
		result := fetchAccountUsage(context.Background(), "https://api.kilo.ai", transport, "key", "team", billingTestNow)
		if result.Status != "unavailable" || result.TodayUSD != nil || result.YesterdayUSD != nil || result.Last30DaysUSD != nil || len(result.Days) != 0 {
			t.Fatalf("malformed response became history: %s: %+v", body, result)
		}
	}
}

func waitBilling(t *testing.T, a *app) billingSnapshot {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		snapshot, fetching := a.billingSnapshotLocked(), a.billing.fetching
		a.mu.Unlock()
		if !fetching {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("billing did not settle")
	return billingSnapshot{}
}

func TestBillingCacheTTLStalenessAndAccountSwitch(t *testing.T) {
	a := testApp(t)
	a.apiKey, a.config.OrgID, a.billingAutoRefresh = "key-one", "team-one", true
	var calls, status atomic.Int32
	status.Store(200)
	a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if status.Load() != 200 {
			return billingTestResponse(int(status.Load()), `{}`), nil
		}
		if r.URL.Path == "/api/profile/balance" {
			return billingTestResponse(200, `{"balance":12.5}`), nil
		}
		return billingTestResponse(200, billingTestUsage("")), nil
	})
	a.mu.Lock()
	a.ensureBillingRefreshLocked(false)
	a.mu.Unlock()
	if result := waitBilling(t, a); result.Status != "ready" || result.Usage.Status != "ready" {
		t.Fatal(result)
	}
	a.mu.Lock()
	a.ensureBillingRefreshLocked(false)
	a.mu.Unlock()
	// Balance, daily usage and the per-model breakdown: three calls, no more.
	if calls.Load() != 3 {
		t.Fatal("state read bypassed refresh interval")
	}
	status.Store(401)
	a.mu.Lock()
	a.ensureBillingRefreshLocked(true)
	a.mu.Unlock()
	result := waitBilling(t, a)
	if result.Status != "unavailable" || !result.Stale || result.BalanceUSD == nil || *result.BalanceUSD != "12.500000000" || !result.Usage.Stale || result.Usage.TodayUSD == nil {
		t.Fatalf("failed refresh must retain explicitly stale same-account data: %+v", result)
	}
	a.mu.Lock()
	a.config.OrgID = "team-two"
	result = a.billingSnapshotLocked()
	if result.BalanceUSD != nil || result.Usage.TodayUSD != nil || result.Status != "loading" || result.OrgID != "team-two" {
		t.Error("team change leaked previous billing")
	}
	a.apiKey = ""
	result = a.billingSnapshotLocked()
	a.mu.Unlock()
	if result.Status != "signed_out" || result.BalanceUSD != nil || result.Usage.Status != "signed_out" {
		t.Fatal("signout retained billing")
	}
}

func TestBillingAsyncResultCannotCrossCredentialOrOrganizationChanges(t *testing.T) {
	for _, change := range []string{"key", "org"} {
		t.Run(change, func(t *testing.T) {
			a := testApp(t)
			a.apiKey, a.config.OrgID = "old-key", "old-org"
			started, release, finished := make(chan struct{}, 2), make(chan struct{}), make(chan struct{}, 2)
			a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
				started <- struct{}{}
				<-release
				defer func() { finished <- struct{}{} }()
				if r.URL.Path == "/api/profile/balance" {
					return billingTestResponse(200, `{"balance":999}`), nil
				}
				return billingTestResponse(200, billingTestUsage("")), nil
			})
			a.mu.Lock()
			a.ensureBillingRefreshLocked(true)
			a.mu.Unlock()
			<-started
			<-started
			a.mu.Lock()
			if change == "key" {
				a.apiKey = "new-key"
			} else {
				a.config.OrgID = "new-org"
			}
			generation := a.billing.generation
			_ = a.billingSnapshotLocked()
			if a.billing.generation == generation {
				t.Fatal("account change did not invalidate generation")
			}
			a.mu.Unlock()
			close(release)
			<-finished
			<-finished
			time.Sleep(10 * time.Millisecond)
			a.mu.Lock()
			result := a.billingSnapshotLocked()
			a.mu.Unlock()
			if result.BalanceUSD != nil || result.Usage.TodayUSD != nil || result.Status != "loading" {
				t.Fatalf("old async data crossed scope: %+v", result)
			}
		})
	}
}

func TestBillingNoRedirectsAndAdminOnlyRefresh(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls.Add(1) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer source.Close()
	result := fetchBilling(context.Background(), source.URL, http.DefaultTransport, "private", "team", billingTestNow)
	if destinationCalls.Load() != 0 || result.Status != "unavailable" || result.Usage.Status != "unavailable" {
		t.Fatal("billing followed redirect or returned a false success")
	}
	a := testApp(t)
	for _, mutate := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Authorization") }, func(r *http.Request) { r.Host = "evil.example" }, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }} {
		r := httptest.NewRequest("POST", "http://"+a.adminHost+"/api/billing/refresh", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+a.adminToken)
		mutate(r)
		w := httptest.NewRecorder()
		a.adminHandler().ServeHTTP(w, r)
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("unauthorized billing refresh: %d", w.Code)
		}
	}
}

func billingTestModelRow(date, model, cost string) string {
	return `{"datetime":"` + date + `","dimensions":{"model":` + model + `},"costMicrodollars":` + cost + `,"requestCount":3}`
}

func TestBillingModelBreakdownIsFetchedAndParsedTolerantly(t *testing.T) {
	var grouped atomic.Int32
	transport := usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/profile/balance" {
			return billingTestResponse(200, `{"balance":5}`), nil
		}
		var input map[string]any
		if err := json.Unmarshal([]byte(r.URL.Query().Get("input")), &input); err != nil {
			t.Error(err)
		}
		groupBy, _ := input["groupBy"].([]any)
		if len(groupBy) == 0 {
			return billingTestResponse(200, billingTestUsage(billingTestRow("2026-09-21T00:00:00Z", "2000000"))), nil
		}
		grouped.Add(1)
		if len(groupBy) != 1 || groupBy[0] != "model" || input["granularity"] != "day" || input["viewAs"] != "self" || input["organizationId"] != "team" {
			t.Errorf("wrong per-model query: %+v", input)
		}
		rows := strings.Join([]string{
			billingTestModelRow("2026-09-21T00:00:00Z", `"anthropic/claude-sonnet-4.6"`, "1500000"),
			billingTestModelRow("2026-09-21T00:00:00Z", `"anthropic/claude-sonnet-4.6"`, "250000"), // duplicate: summed
			billingTestModelRow("2026-09-20T00:00:00Z", `" z-ai/glm-5 "`, "500000"),
			billingTestModelRow("2026-09-21T00:00:00Z", `""`, "100"),                   // empty model
			billingTestModelRow("2026-09-21T00:00:00Z", `42`, "100"),                   // not a string
			billingTestModelRow("2026-09-21T00:00:00Z", `"bad\u0007model"`, "100"),     // control character
			billingTestModelRow("2026-07-01T00:00:00Z", `"too/old"`, "100"),            // outside the period
			billingTestModelRow("not a date", `"no/date"`, "100"),                      // bad date
			billingTestModelRow("2026-09-21T00:00:00Z", `"negative/cost"`, "-5"),       // negative cost
			billingTestModelRow("2026-09-21T00:00:00Z", `"invalid/cost"`, `"NaN"`),     // invalid cost
			`{"datetime":"2026-09-21T00:00:00Z","dimensions":{},"costMicrodollars":7}`, // no model dimension
		}, ",")
		return billingTestResponse(200, billingTestUsage(rows)), nil
	})
	result := fetchBilling(context.Background(), "https://api.kilo.ai", transport, "secret", "team", billingTestNow)
	if result.Usage.Status != "ready" || grouped.Load() != 1 {
		t.Fatalf("per-model breakdown not requested once after daily usage: %+v", result.Usage)
	}
	want := []accountUsageModel{
		{Date: "2026-09-21", Model: "anthropic/claude-sonnet-4.6", CostUSD: "1.750000000", Requests: 6},
		{Date: "2026-09-20", Model: "z-ai/glm-5", CostUSD: "0.500000000", Requests: 3},
	}
	if len(result.Usage.Models) != len(want) {
		t.Fatalf("models: %+v", result.Usage.Models)
	}
	for i := range want {
		if result.Usage.Models[i] != want[i] {
			t.Fatalf("model row %d: %+v, want %+v", i, result.Usage.Models[i], want[i])
		}
	}
}

func TestBillingModelBreakdownFailureKeepsDailyUsage(t *testing.T) {
	for name, grouped := range map[string]func() *http.Response{
		"http error": func() *http.Response { return billingTestResponse(400, `{"error":"invalid groupBy"}`) },
		"malformed":  func() *http.Response { return billingTestResponse(200, `{"result":`) },
		"no rows":    func() *http.Response { return billingTestResponse(200, billingTestUsage("")) },
		"wrong granularity": func() *http.Response {
			return billingTestResponse(200, `{"result":{"data":{"rows":[`+billingTestModelRow("2026-09-21T00:00:00Z", `"m"`, "1")+`],"effectiveGranularity":"week"}}}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			transport := usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/api/profile/balance" {
					return billingTestResponse(200, `{"balance":5}`), nil
				}
				if strings.Contains(r.URL.Query().Get("input"), `"groupBy":["model"]`) {
					return grouped(), nil
				}
				return billingTestResponse(200, billingTestUsage(billingTestRow("2026-09-21T00:00:00Z", "2000000"))), nil
			})
			result := fetchBilling(context.Background(), "https://api.kilo.ai", transport, "secret", "", billingTestNow)
			if result.Status != "ready" || result.Usage.Status != "ready" || result.Usage.Error != "" || *result.Usage.TodayUSD != "2.000000000" {
				t.Fatalf("optional breakdown broke daily usage: %+v", result.Usage)
			}
			if result.Usage.Models != nil {
				t.Fatalf("unusable breakdown produced rows: %+v", result.Usage.Models)
			}
		})
	}
}

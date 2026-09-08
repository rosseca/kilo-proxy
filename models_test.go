package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicModelMetricsPublishedFieldsAndFallbacks(t *testing.T) {
	metrics, err := parsePublicModelMetrics([]byte(`[
	 {"openrouterId":"vendor/one","codingIndex":"73.40","speedTokensPerSec":"125.5","chartData":{"modeRankings":{"code":2}},"priceInput":"999"},
	 {"openrouterId":"vendor/zero","codingIndex":0,"speedTokensPerSec":"0","chartData":{"modeRankings":{"code":0}},"benchmarks":{"artificial_analysis_coding_index":80}},
	 {"openrouterId":"vendor/fallback","codingIndex":null,"benchmarks":{"artificial_analysis_coding_index":"65.5","median_output_tokens_per_second":40}},
	 {"openrouterId":"vendor/nested","codingIndex":"NaN","speedTokensPerSec":"Inf","chartData":{"modeRankings":{"code":1.5}},"benchmarks":{"artificialAnalysis":{"codingIndex":55}}},
	 {"openrouterId":"vendor/unknown","codingIndex":-1,"speedTokensPerSec":null,"chartData":42},
	 {"openrouterId":"vendor/inactive","isActive":false,"codingIndex":99},
	 {"openrouterId":"vendor/one","codingIndex":1},false,{},null
	]`))
	if err != nil || len(metrics) != 5 {
		t.Fatalf("metrics=%v error=%v", metrics, err)
	}
	assertNumber := func(value *float64, want float64) {
		t.Helper()
		if value == nil || *value != want {
			t.Fatalf("metric=%v, want %v", value, want)
		}
	}
	assertNumber(metrics["vendor/one"].codeModeRank, 2)
	assertNumber(metrics["vendor/one"].codingIndex, 73.4)
	assertNumber(metrics["vendor/one"].speed, 125.5)
	assertNumber(metrics["vendor/zero"].codingIndex, 0)
	assertNumber(metrics["vendor/zero"].speed, 0)
	assertNumber(metrics["vendor/fallback"].codingIndex, 65.5)
	assertNumber(metrics["vendor/fallback"].speed, 40)
	assertNumber(metrics["vendor/nested"].codingIndex, 55)
	if metrics["vendor/zero"].codeModeRank != nil || metrics["vendor/nested"].codeModeRank != nil || metrics["vendor/nested"].speed != nil || metrics["vendor/unknown"].codingIndex != nil {
		t.Fatal("invalid or unknown metrics became sortable values")
	}
	for _, invalid := range []string{`null`, `{}`, `{"models":[]}`, `[] trailing`, `[`} {
		if _, err := parsePublicModelMetrics([]byte(invalid)); err == nil {
			t.Errorf("accepted invalid metadata %q", invalid)
		}
	}
	for _, invalid := range []string{`"NaN"`, `"-Inf"`, `"Infinity"`, `null`, `true`, `{}`, `[]`, `-1`, `""`} {
		if modelMetricNumber(json.RawMessage(invalid)) != nil {
			t.Errorf("accepted invalid numeric metric %q", invalid)
		}
	}
}

func TestPublicModelMetricsEnrichmentNeverChangesGatewayAccessOrPrices(t *testing.T) {
	a := testApp(t)
	a.apiKey, a.config.OrgID = "private-personal-key", "private-team"
	var metadataCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/gateway/models":
			if r.Header.Get("Authorization") != "Bearer private-personal-key" || r.Header.Get("X-KiloCode-OrganizationId") != "private-team" {
				t.Error("gateway credentials lost")
			}
			io.WriteString(w, `{"data":[{"id":"vendor/one","name":"My gateway name","pricing":{"prompt":"0.000002","completion":"0.000005"}},{"id":"vendor/one:free"}]}`)
		case "/api/models/stats":
			metadataCalls.Add(1)
			if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("X-KiloCode-OrganizationId") != "" || r.Header.Get("Cookie") != "" {
				t.Error("public metadata request included private credentials")
			}
			io.WriteString(w, `[{"openrouterId":"vendor/one","name":"Public name","priceInput":"999","priceOutput":"999","codingIndex":"70","speedTokensPerSec":100,"chartData":{"modeRankings":{"code":3}},"private":"must-not-forward"},{"openrouterId":"vendor/unavailable","codingIndex":99}]`)
		default:
			t.Errorf("unexpected route %q", r.URL.Path)
		}
	}))
	defer server.Close()
	setUpstream(a, server.URL)
	a.modelStatsURL = server.URL + "/api/models/stats"
	for range 2 {
		models, _, err := a.fetchModels(context.Background(), false)
		if err != nil || len(models) != 2 {
			t.Fatalf("models=%v error=%v", models, err)
		}
		var enriched, free modelInfo
		for _, model := range models {
			if model.ID == "vendor/one" {
				enriched = model
			} else {
				free = model
			}
		}
		if enriched.Name != "My gateway name" || *enriched.InputPrice != 2 || *enriched.OutputPrice != 5 || enriched.CodeModeRank == nil || *enriched.CodeModeRank != 3 || *enriched.CodingIndex != 70 || *enriched.Speed != 100 {
			t.Fatalf("incorrect enrichment: %+v", enriched)
		}
		if free.CodeModeRank != nil || free.CodingIndex != nil || free.Speed != nil {
			t.Fatal("metrics copied to a different model variant")
		}
		body, _ := json.Marshal(models)
		for _, private := range []string{"private-personal-key", "private-team", "must-not-forward", "vendor/unavailable"} {
			if strings.Contains(string(body), private) {
				t.Fatalf("unexpected metadata leaked: %s", private)
			}
		}
	}
	if metadataCalls.Load() != 1 {
		t.Fatal("public metadata was not cached")
	}
}

type modelMetricTransport func(*http.Request) (*http.Response, error)

func (f modelMetricTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPublicModelMetricsFailureRetainsGatewayCatalog(t *testing.T) {
	for _, mode := range []string{"offline", "invalid", "oversized", "server-error", "redirect", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			a := testApp(t)
			a.modelStatsURL = "https://metadata.invalid/stats"
			var calls atomic.Int32
			a.transport = modelMetricTransport(func(r *http.Request) (*http.Response, error) {
				status, body := http.StatusOK, `{"data":[{"id":"vendor/model"}]}`
				header := make(http.Header)
				if r.URL.Host == "metadata.invalid" {
					calls.Add(1)
					switch mode {
					case "offline":
						return nil, errors.New("private failure detail")
					case "invalid":
						body = `{"private":"never-forward"}`
					case "oversized":
						body = strings.Repeat(" ", (4<<20)+1)
					case "server-error":
						status, body = 503, "private failure detail"
					case "redirect":
						status = 302
						header.Set("Location", "https://must-not-follow.invalid/")
					case "cancelled":
						deadline, ok := r.Context().Deadline()
						if !ok || time.Until(deadline) > modelStatsTimeout {
							t.Error("metadata request is not bounded")
						}
						return nil, context.DeadlineExceeded
					}
				} else if r.URL.Host != "api.kilo.ai" {
					t.Errorf("unexpected network destination %s", r.URL.Host)
				}
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			for range 2 {
				models, _, err := a.fetchModels(context.Background(), false)
				if err != nil || len(models) != 1 || models[0].CodingIndex != nil {
					t.Fatalf("optional stats failure discarded catalog: %v %v", models, err)
				}
			}
			if calls.Load() != 1 {
				t.Fatal("failed metadata fetch was not backed off")
			}
		})
	}
}

func TestPublicModelMetricsConcurrentCacheAndLastGoodSnapshot(t *testing.T) {
	a := testApp(t)
	a.modelStatsURL = "https://metadata.invalid/stats"
	started, release := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	a.transport = modelMetricTransport(func(r *http.Request) (*http.Response, error) {
		if requests.Add(1) > 1 {
			return nil, errors.New("temporarily offline")
		}
		close(started)
		<-release
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"openrouterId":"vendor/one","codingIndex":70}]`)), Header: make(http.Header)}, nil
	})
	var wait sync.WaitGroup
	results := make(chan map[string]modelMetric, 8)
	for range 8 {
		wait.Add(1)
		go func() { defer wait.Done(); results <- a.publicModelMetrics(context.Background()) }()
	}
	<-started
	close(release)
	wait.Wait()
	close(results)
	for result := range results {
		if result["vendor/one"].codingIndex == nil || *result["vendor/one"].codingIndex != 70 {
			t.Fatal("inconsistent cached metrics")
		}
	}
	if requests.Load() != 1 {
		t.Fatal("concurrent requests were not combined")
	}
	a.mu.Lock()
	a.modelStatsCache.expiresAt = time.Time{}
	a.mu.Unlock()
	if result := a.publicModelMetrics(context.Background()); result["vendor/one"].codingIndex == nil {
		t.Fatal("last published snapshot lost during outage")
	}
	if requests.Load() != 2 {
		t.Fatal("expired cache was not refreshed")
	}
	a.mu.Lock()
	a.modelStatsURL = "https://different-metadata.invalid/stats"
	a.mu.Unlock()
	if result := a.publicModelMetrics(context.Background()); len(result) != 0 {
		t.Fatal("cached metrics crossed metadata endpoint boundaries")
	}
}

func TestPublicModelMetricsRejectsURLCredentialsAndHonorsCancellation(t *testing.T) {
	transport := modelMetricTransport(func(r *http.Request) (*http.Response, error) {
		t.Fatal("metadata URL credentials must fail before sending a request")
		return nil, nil
	})
	if _, err := fetchPublicModelMetrics(context.Background(), "https://private:secret@metadata.invalid/stats", transport); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("metadata URL credentials accepted or exposed")
	}
	a := testApp(t)
	a.modelStatsURL = "https://metadata.invalid/stats"
	started, release, complete := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a.transport = modelMetricTransport(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-release
		return nil, context.Canceled
	})
	go func() { a.publicModelMetrics(context.Background()); close(complete) }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if metrics := a.publicModelMetrics(ctx); metrics != nil {
		t.Fatal("cancelled waiter returned unexpected metadata")
	}
	close(release)
	<-complete
}

func TestCatalogNormalizesPricesAndCapabilities(t *testing.T) {
	models, err := parseModels([]byte(`{"data":[
 {"id":"provider/paid","name":"Paid","context_length":200000,"top_provider":{"max_completion_tokens":32000},"pricing":{"prompt":"0.000003","completion":0.000015},"supported_parameters":["tools","reasoning"],"architecture":{"input_modalities":["text","image","unknown"],"output_modalities":["text"]},"mayTrainOnYourPrompts":false,"secret":"never-forward"},
 {"id":"kilo-auto/test","name":"Auto","pricing":{"prompt":"-1","completion":"-1"}},
 {"id":"provider/free","name":"Free","pricing":{"prompt":"0","completion":0},"supported_parameters":[]},
 {"id":"provider/free","name":"Duplicate"},{"id":"bad id"}
 ]}`))
	if err != nil || len(models) != 3 {
		t.Fatalf("models=%v err=%v", models, err)
	}
	auto, free, paid := models[0], models[1], models[2]
	if auto.InputPrice != nil || auto.OutputPrice != nil || auto.Tools != nil {
		t.Fatal("unknown capabilities or variable prices misrepresented")
	}
	if free.InputPrice == nil || *free.InputPrice != 0 || free.Tools == nil || *free.Tools {
		t.Fatal("explicit zero/false lost")
	}
	if paid.InputPrice == nil || *paid.InputPrice != 3 || *paid.OutputPrice != 15 || !*paid.Tools || !*paid.Reasoning || paid.ContextWindow != 200000 || len(paid.InputModalities) != 2 {
		t.Fatalf("incorrect normalization: %+v", paid)
	}
	encoded, _ := json.Marshal(models)
	if strings.Contains(string(encoded), "never-forward") {
		t.Fatal("raw catalog fields leaked")
	}
	for _, body := range []string{`{}`, `{"data":null}`, `{"data":{}}`, `{"data":[{"id":"bad id"}]}`, `{"data":[]} trailing`} {
		if _, err := parseModels([]byte(body)); err == nil {
			t.Errorf("accepted invalid catalog %s", body)
		}
	}
	for _, price := range []string{`"NaN"`, `"Inf"`, `null`, `"-1"`, `{}`} {
		if tokenPrice(json.RawMessage(price)) != nil {
			t.Errorf("accepted invalid price %s", price)
		}
	}
}

func TestCatalogPublicAndOrganizationAuthentication(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "organization"}[configured], func(t *testing.T) {
			a := testApp(t)
			a.apiKey = "personal-secret"
			if configured {
				a.config.OrgID = "team"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/gateway/models" {
					t.Error("wrong catalog route")
				}
				wantAuth, wantOrg := "", ""
				if configured {
					wantAuth, wantOrg = "Bearer personal-secret", "team"
				}
				if r.Header.Get("Authorization") != wantAuth || r.Header.Get("X-KiloCode-OrganizationId") != wantOrg {
					t.Error("incorrect catalog authentication")
				}
				io.WriteString(w, `{"data":[{"id":"vendor/model"}]}`)
			}))
			defer upstream.Close()
			setUpstream(a, upstream.URL)
			w := adminRequest(a, "models", `{}`)
			if w.Code != 200 || strings.Contains(w.Body.String(), "personal-secret") {
				t.Fatalf("unsafe response: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCatalogRejectsStaleConnection(t *testing.T) {
	a := testApp(t)
	started, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		io.WriteString(w, `{"data":[{"id":"vendor/model"}]}`)
	}))
	defer upstream.Close()
	setUpstream(a, upstream.URL)
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- adminRequest(a, "models", `{}`) }()
	<-started
	a.mu.Lock()
	a.catalogRevision++
	a.mu.Unlock()
	close(release)
	if w := <-result; w.Code != 409 {
		t.Fatalf("stale catalog accepted: %d", w.Code)
	}
}

func TestCatalogLimitsAndRedactsUpstreamErrors(t *testing.T) {
	for _, mode := range []string{"oversize", "invalid", "unauthorized"} {
		t.Run(mode, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "oversize":
					io.WriteString(w, strings.Repeat(" ", (8<<20)+1))
				case "invalid":
					io.WriteString(w, `{"private":"upstream-secret"}`)
				case "unauthorized":
					w.WriteHeader(401)
					io.WriteString(w, "upstream-secret")
				}
			}))
			defer upstream.Close()
			a := testApp(t)
			setUpstream(a, upstream.URL)
			w := adminRequest(a, "models", `{}`)
			if w.Code != 502 || strings.Contains(w.Body.String(), "upstream-secret") {
				t.Fatalf("unsafe failure %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCatalogPreservesPublishedReasoningVariants(t *testing.T) {
	models, err := parseModels([]byte(`{"data":[
 {"id":"openai/gpt-5.6-sol-discounted","opencode":{"variants":{"off":{"reasoning":{"enabled":false,"effort":"none"}},"low":{"reasoning":{"enabled":true,"effort":"low"}},"medium":{"reasoning":{"effort":"medium"}},"high":{"reasoning":{"effort":"high"}},"xhigh":{"reasoning":{"effort":"xhigh"}},"max":{"reasoning":{"effort":"max"}}}}},
 {"id":"z-ai/glm-5.3","supported_parameters":["reasoning_effort"],"opencode":{"variants":{"low":{"reasoning":{"effort":"low"}},"high":{"reasoning":{"effort":"high"}},"max":{"reasoning":{"effort":"max"}},"duplicate":{"reasoning":{"effort":"max"}},"ultra":{"reasoning":{"enabled":false,"effort":"ultra"}},"invalid":{"reasoning":{"effort":"invalid"}},"budget":{"reasoning":{"max_tokens":1000}},"broken":42}}}
 ]}`))
	if err != nil || len(models) != 2 {
		t.Fatal(err)
	}
	if got := strings.Join(models[0].ReasoningEfforts, ","); got != "none,low,medium,high,xhigh,max" {
		t.Fatal(got)
	}
	if got := strings.Join(models[1].ReasoningEfforts, ","); got != "low,high,max" {
		t.Fatal(got)
	}
	if models[1].Reasoning == nil || !*models[1].Reasoning {
		t.Fatal("reasoning_effort not recognized")
	}
	for _, raw := range []string{`null`, `[]`, `{"variants":42}`, `{"variants":{"xhigh":{"reasoning":{"max_tokens":1000}}}}`} {
		if got := catalogReasoningEfforts(json.RawMessage(raw)); len(got) != 0 {
			t.Fatal(got)
		}
	}
}

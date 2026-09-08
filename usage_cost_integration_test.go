package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReportedInferenceCostSurvivesTraceTruncation(t *testing.T) {
	for _, tc := range []struct {
		name, terminal, cost, total, source string
	}{
		{
			name:     "upstream cost with zero fee",
			terminal: `{"type":"response.completed","response":{"model":"vendor/test","usage":{"input_tokens":3076,"output_tokens":6375,"cost":0,"is_byok":true,"cost_details":{"upstream_inference_cost":"0.21976"}}}}`,
			cost:     "0.219760000", total: "0.439520000", source: "usage.cost_details.upstream_inference_cost",
		},
		{
			name:     "gateway market cost below one microdollar",
			terminal: `{"type":"response.completed","response":{"model":"vendor/test","usage":{"input_tokens":1,"output_tokens":1}},"provider_metadata":{"gateway":{"marketCost":"0.000000125"}}}`,
			cost:     "0.000000125", total: "0.000000250", source: "provider_metadata.gateway.marketCost",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			a.captureEnabled = true
			// A retained trace can end before the terminal usage. Accounting must
			// still see that usage, and forwarding must preserve every stream byte.
			body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"" + strings.Repeat("x", traceBodyLimit+100) + "\"}\n\ndata: " + tc.terminal + "\n\n"
			a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1, Request: r}, nil
			})
			handler := a.inferenceHandler("test-upstream-key", "test-org", "test-local-key", "127.0.0.1:8877")
			for range 2 {
				r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/responses", strings.NewReader(`{"model":"vendor/test","stream":true}`))
				r.Header.Set("Authorization", "Bearer test-local-key")
				r.Header.Set("Thread-Id", "reported-cost-test")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != 200 || w.Body.String() != body {
					t.Fatal("cost observation changed the streamed response")
				}
				entry := a.events[0]
				if entry.Usage == nil || entry.Usage.CostUSD == nil || *entry.Usage.CostUSD != tc.cost || entry.Usage.CostSource != tc.source || !entry.Usage.Complete || entry.Usage.Limited {
					t.Fatalf("final reported cost lost: %+v", entry.Usage)
				}
				trace := a.traces[entry.ID]
				if trace == nil || strings.Contains(trace.UpstreamResponse.Body, "response.completed") {
					t.Fatal("fixture did not truncate the trace before terminal usage")
				}
			}
			if a.usageTotal.CostUSD != tc.total || a.usageTotal.Priced != 2 || a.usageTotal.Requests != 2 || a.usageTotal.Incomplete != 0 || len(a.usageSessions) != 1 {
				t.Fatalf("wrong session accounting: %+v", a.usageTotal)
			}
			for _, session := range a.usageSessions {
				if session.CostUSD != a.usageTotal.CostUSD || session.Priced != 2 {
					t.Fatal("conversation cost differs from process total")
				}
			}
		})
	}
}

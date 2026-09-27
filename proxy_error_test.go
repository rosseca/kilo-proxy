package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxy502IdentifiesFailureStageWithoutLeakingCause(t *testing.T) {
	for _, capture := range []bool{false, true} {
		for _, stage := range []string{"transport", "adaptation"} {
			name := stage + "/capture-off"
			if capture {
				name = stage + "/capture-on"
			}
			t.Run(name, func(t *testing.T) {
				a := testApp(t)
				if capture {
					if w := adminRequest(a, "activity/config", `{"enabled":true}`); w.Code != http.StatusOK {
						t.Fatal(w.Code, w.Body.String())
					}
				}
				a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
					if stage == "transport" {
						return nil, errors.New("private transport details upstream-secret")
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"private":"upstream-secret",`)),
						Request:    r,
					}, nil
				})
				r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8877/v1/responses", strings.NewReader(unionRequest))
				r.Header.Set("Authorization", "Bearer local-secret")
				w := httptest.NewRecorder()
				a.inferenceHandler("upstream-secret", "team", "local-secret", "127.0.0.1:8877").ServeHTTP(w, r)
				if w.Code != http.StatusBadGateway {
					t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
				}
				var body struct {
					Error struct{ Type, Code, Message string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				wantCode := "upstream_transport_error"
				if stage == "adaptation" {
					wantCode = "response_adaptation_error"
				}
				wantAdvice := "Comprueba la conexión"
				if stage == "adaptation" {
					wantAdvice = "Actualiza Kilo Proxy"
				}
				if body.Error.Type != "kilo_local_error" || body.Error.Code != wantCode || !strings.Contains(body.Error.Message, wantAdvice) || !strings.Contains(body.Error.Message, "Activity") {
					t.Fatalf("wrong failure classification or advice: %+v", body.Error)
				}
				for _, secret := range []string{"upstream-secret", "local-secret", "private transport details"} {
					if strings.Contains(w.Body.String(), secret) {
						t.Fatalf("error exposed private detail %q", secret)
					}
				}
				if !capture {
					if len(a.events) != 0 || len(a.traces) != 0 {
						t.Fatal("request capture enabled without consent")
					}
					return
				}
				if len(a.events) != 1 || !a.events[0].HasDetails {
					t.Fatal("missing opt-in failure trace")
				}
				trace := a.traces[a.events[0].ID]
				if trace == nil || trace.Error == "" || strings.Contains(trace.Error, "upstream-secret") {
					t.Fatalf("missing or unredacted failure detail: %+v", trace)
				}
				wantUpstreamStatus := 0
				if stage == "adaptation" {
					wantUpstreamStatus = http.StatusOK
				}
				if trace.UpstreamStatus != wantUpstreamStatus {
					t.Fatalf("upstream status = %d, want %d", trace.UpstreamStatus, wantUpstreamStatus)
				}
			})
		}
	}
}

func TestProxyForwardsUpstream502WithoutLocalClassification(t *testing.T) {
	a := testApp(t)
	a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"gateway unavailable"}`)),
			Request:    r,
		}, nil
	})
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8877/v1/responses", strings.NewReader(`{"model":"vendor/test","input":"hello"}`))
	r.Header.Set("Authorization", "Bearer local-secret")
	w := httptest.NewRecorder()
	a.inferenceHandler("upstream-secret", "team", "local-secret", "127.0.0.1:8877").ServeHTTP(w, r)
	if w.Code != http.StatusBadGateway || w.Body.String() != `{"error":"gateway unavailable"}` {
		t.Fatalf("upstream error changed: %d %s", w.Code, w.Body.String())
	}
}

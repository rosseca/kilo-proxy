package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestModelCatalogCompletenessReportsProviderFailureAndRecovery(t *testing.T) {
	for _, endpoint := range []string{"/api/models", "/api/check"} {
		for _, failedProvider := range []string{"kilo", "chatgpt"} {
			t.Run(endpoint+"/"+failedProvider, func(t *testing.T) {
				a := subscriptionTestApp(t)
				a.apiKey, a.config.OrgID = "synthetic-kilo-key", "synthetic-team"
				var failed atomic.Bool
				failed.Store(true)
				kilo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/api/gateway/models" {
						t.Errorf("unexpected Kilo path %s", r.URL.Path)
						http.NotFound(w, r)
						return
					}
					if failedProvider == "kilo" && failed.Load() {
						http.Error(w, "synthetic unavailable", http.StatusServiceUnavailable)
						return
					}
					jsonResponse(w, http.StatusOK, map[string]any{"data": []any{map[string]any{"id": "openai/gpt-test", "name": "Kilo model"}}})
				}))
				defer kilo.Close()
				subscription := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/codex/models" {
						t.Errorf("unexpected subscription path %s", r.URL.Path)
						http.NotFound(w, r)
						return
					}
					if failedProvider == "chatgpt" && failed.Load() {
						http.Error(w, "synthetic unavailable", http.StatusServiceUnavailable)
						return
					}
					jsonResponse(w, http.StatusOK, map[string]any{"models": []any{map[string]any{"slug": "gpt-test", "display_name": "Subscription model"}}})
				}))
				defer subscription.Close()
				// Custom gateways keep rankings/recommendations self-contained.
				setUpstream(a, kilo.URL)
				a.chatgpt.backendURL = subscription.URL
				for _, recovered := range []bool{false, true} {
					failed.Store(!recovered)
					r := httptest.NewRequest(http.MethodPost, "http://"+a.adminHost+endpoint, nil)
					r.Header.Set("Authorization", "Bearer "+a.adminToken)
					w := httptest.NewRecorder()
					a.adminHandler().ServeHTTP(w, r)
					var response struct {
						Complete *bool           `json:"complete"`
						Models   json.RawMessage `json:"models"`
						Catalog  []modelInfo     `json:"catalog"`
					}
					if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil {
						t.Fatalf("catalog failed instead of returning available models: status %d", w.Code)
					}
					if response.Complete == nil || *response.Complete != recovered {
						t.Fatalf("complete flag missing or wrong after recovery=%v: %v", recovered, response.Complete)
					}
					models := response.Catalog
					if endpoint == "/api/models" {
						if err := json.Unmarshal(response.Models, &models); err != nil {
							t.Fatal(err)
						}
					}
					want := 1
					if recovered {
						want = 2
					}
					if len(models) != want {
						t.Fatalf("got %d models, want %d after recovery=%v", len(models), want, recovered)
					}
					if !recovered && models[0].Connection == failedProvider {
						t.Fatal("partial result came from the unavailable provider")
					}
				}
			})
		}
	}
}

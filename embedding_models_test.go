package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const embeddingCatalogFixture = `{"defaultModel":"vendor/embed","models":[{"id":"vendor/embed","name":"Embedding","dimension":1536,"scoreThreshold":0.4,"dimensionMode":"fixed","note":"code"},{"id":"vendor/unknown"}],"aliases":{"embed":"vendor/embed"},"future":true}`

func TestEmbeddingCatalogFetchAndSchema(t *testing.T) {
	base, _ := url.Parse("https://gateway.test/api/gateway")
	calls := 0
	tr := usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/api/gateway/embedding-models" || r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("X-KiloCode-OrganizationId") != "org" {
			t.Error("incorrect scoped fetch")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(embeddingCatalogFixture)), Request: r}, nil
	})
	c, err := fetchEmbeddingCatalog(context.Background(), base, tr, "secret", "org")
	if err != nil || calls != 1 || len(c.Models) != 2 || c.Models[0].Dimension == nil || *c.Models[0].Dimension != 1536 || c.Models[1].Dimension != nil || c.Models[1].Name != "vendor/unknown" || c.Aliases["embed"] != "vendor/embed" {
		t.Fatalf("catalog %+v err %v", c, err)
	}
	if base.Path != "/api/gateway" {
		t.Fatal("base mutated")
	}
	for _, body := range []string{`{}`, `null`, `[]`, `{"models":null}`, `{"models":[]} {}`, `{"models":[{"id":"bad id"}]}`, `{"models":[{"id":"x"},{"id":"x"}]}`, `{"models":[{"id":"x","dimension":0}]}`, `{"models":[{"id":"x","dimension":1.5}]}`, `{"models":[],"defaultModel":"missing"}`, `{"models":[],"aliases":{"a":"missing"}}`, `{"models":[{"id":"x","scoreThreshold":2}]}`} {
		tr := usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})
		_, err := fetchEmbeddingCatalog(context.Background(), base, tr, "secret", "org")
		var e *embeddingCatalogError
		if !errors.As(err, &e) || e.Code != "invalid_embedding_catalog" {
			t.Fatalf("accepted %s: %v", body, err)
		}
	}
}

func TestEmbeddingCatalogFailures(t *testing.T) {
	base, _ := url.Parse("https://gateway.test/api/gateway")
	for _, status := range []int{301, 400, 401, 403, 409, 429, 500, 503} {
		calls := 0
		tr := usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: status, Header: http.Header{"Location": {"https://other.test"}, "Retry-After": {"12"}}, Body: io.NopCloser(strings.NewReader(`{"error":"secret private"}`)), Request: r}, nil
		})
		_, err := fetchEmbeddingCatalog(context.Background(), base, tr, "secret", "org")
		var e *embeddingCatalogError
		if !errors.As(err, &e) || strings.Contains(err.Error(), "secret") || calls != 1 {
			t.Fatalf("status %d err %v calls %d", status, err, calls)
		}
		want := status
		if status == 301 {
			want = 502
		}
		if e.Status != want {
			t.Fatalf("got status %d", e.Status)
		}
		if status >= 400 && e.RetryAfter != "12" {
			t.Fatal("Retry-After lost")
		}
	}
	tr := usageMemoryTransport(func(r *http.Request) (*http.Response, error) { return nil, errors.New("secret network details") })
	_, err := fetchEmbeddingCatalog(context.Background(), base, tr, "secret", "org")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("unsafe network error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = fetchEmbeddingCatalog(ctx, base, tr, "secret", "org")
	if err == nil {
		t.Fatal("ignored cancellation")
	}
	_, err = fetchEmbeddingCatalog(context.Background(), base, tr, "", "")
	var e *embeddingCatalogError
	if !errors.As(err, &e) || e.Status != 401 {
		t.Fatal("missing connection accepted")
	}
	for _, n := range []int{8 << 20, (8 << 20) + 1} {
		body := `{"models":[]}` + strings.Repeat(" ", n-len(`{"models":[]}`))
		tr := usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})
		_, err := fetchEmbeddingCatalog(context.Background(), base, tr, "secret", "org")
		if (err == nil) != (n == 8<<20) {
			t.Fatalf("size %d: %v", n, err)
		}
	}
}

func TestEmbeddingCatalogAdaptersAndScopeChange(t *testing.T) {
	a := subscriptionTestApp(t)
	a.apiKey = "secret"
	a.config.OrgID = "org"
	a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/gateway/embedding-models" {
			t.Error("wrong catalog")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(embeddingCatalogFixture)), Request: r}, nil
	})
	w := adminRequest(a, "embedding-models", "")
	if w.Code != 200 {
		t.Fatalf("private %d %s", w.Code, w.Body)
	}
	var private embeddingCatalog
	if json.Unmarshal(w.Body.Bytes(), &private) != nil || len(private.Models) != 2 {
		t.Fatal("private catalog invalid")
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:8877/v1/embedding-models", nil)
	r.Header.Set("Authorization", "Bearer local-secret")
	w = httptest.NewRecorder()
	a.inferenceHandler("secret", "org", "local-secret", r.Host).ServeHTTP(w, r)
	var public embeddingCatalog
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &public) != nil || len(public.Models) != 2 {
		t.Fatalf("public %d %s", w.Code, w.Body)
	}
	a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
		a.mu.Lock()
		a.config.OrgID = "changed"
		a.catalogRevision++
		a.mu.Unlock()
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(embeddingCatalogFixture)), Request: r}, nil
	})
	w = adminRequest(a, "embedding-models", "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "embedding_connection_changed") {
		t.Fatalf("stale result %d %s", w.Code, w.Body)
	}
	w = adminRequest(a, "embedding-models", `{}`)
	if w.Code != 405 || w.Header().Get("Allow") != "GET" {
		t.Fatal("wrong private method handling")
	}
}

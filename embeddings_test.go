package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func embeddingRequest(a *app, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/embeddings", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer local-secret")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.inferenceHandler("upstream-secret", "org-saved", "local-secret", r.Host).ServeHTTP(w, r)
	return w
}

func TestEmbeddingsProxyPreservesContract(t *testing.T) {
	for _, input := range []string{`"text"`, `["one","two"]`, `[1,2,3]`, `[[1,2],[3,4]]`, `null`} {
		t.Run(input, func(t *testing.T) {
			a := testApp(t)
			payload := `{"model":"google/gemini-embedding-2","input":` + input + `,"dimensions":256,"encoding_format":"base64","future":{"a":1}}`
			response := ` {"model":"gemini-embedding-2","data":[{"index":0,"embedding":"AAAA"}],"usage":{"prompt_tokens":8}} `
			calls := 0
			a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/api/gateway/embeddings" || r.Header.Get("Authorization") != "Bearer upstream-secret" || r.Header.Get("X-KiloCode-OrganizationId") != "org-saved" {
					t.Error("incorrect upstream path or credentials")
				}
				for _, h := range []string{"Cookie", "OpenAI-Organization", "X-Api-Key", "X-Forwarded-For"} {
					if r.Header.Get(h) != "" {
						t.Errorf("forwarded %s", h)
					}
				}
				b, _ := io.ReadAll(r.Body)
				if string(b) != payload {
					t.Error("request body changed")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "Set-Cookie": {"private"}, "Access-Control-Allow-Origin": {"*"}}, Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
			})
			r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/embeddings", strings.NewReader(payload))
			r.Header.Set("Authorization", "Bearer local-secret")
			r.Header.Set("X-KiloCode-OrganizationId", "attacker-org")
			r.Header.Set("Cookie", "private")
			r.Header.Set("OpenAI-Organization", "attacker")
			r.Header.Set("X-Api-Key", "private")
			w := httptest.NewRecorder()
			a.inferenceHandler("upstream-secret", "org-saved", "local-secret", r.Host).ServeHTTP(w, r)
			if w.Code != 200 || w.Body.String() != response || calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body)
			}
			if w.Header().Get("Set-Cookie") != "" || w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("unsafe response headers")
			}
		})
	}
}

func TestEmbeddingsProxyProviderAndValidation(t *testing.T) {
	for _, tc := range []struct {
		key, org, body, code string
		status               int
	}{
		{"", "", `{"model":"chatgpt/test"}`, "kilo_connection_required", 401},
		{"upstream-secret", "", `{}`, "kilo_connection_required", 401},
		{"upstream-secret", "org-saved", `{"model":"chatgpt/test"}`, "unsupported_embedding_provider", 400},
		{"upstream-secret", "org-saved", `null`, "", 400},
		{"upstream-secret", "org-saved", `[]`, "", 400},
		{"upstream-secret", "org-saved", `{} {}`, "", 400},
	} {
		a := testApp(t)
		a.transport = usageMemoryTransport(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected upstream call"); return nil, nil })
		r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/embeddings", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer local-secret")
		w := httptest.NewRecorder()
		a.inferenceHandler(tc.key, tc.org, "local-secret", r.Host).ServeHTTP(w, r)
		var d struct{ Error struct{ Code string } }
		_ = json.Unmarshal(w.Body.Bytes(), &d)
		if w.Code != tc.status || d.Error.Code != tc.code {
			t.Fatalf("want %d/%s got %d %s", tc.status, tc.code, w.Code, w.Body)
		}
	}
}

func TestEmbeddingsProxyUpstreamErrors(t *testing.T) {
	for _, status := range []int{400, 401, 402, 403, 413, 429, 500, 502, 503} {
		a := testApp(t)
		body := `{"error":"FUNCTION_PAYLOAD_TOO_LARGE"}`
		calls := 0
		a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}, "Retry-After": {"12"}, "X-Vercel-Error": {"FUNCTION_PAYLOAD_TOO_LARGE"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})
		w := embeddingRequest(a, `{"model":"vendor/embed","input":"test"}`)
		if w.Code != status || w.Body.String() != body || w.Header().Get("Retry-After") != "12" || calls != 1 {
			t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body)
		}
	}
}

func TestEmbeddingsProxySecurity(t *testing.T) {
	for _, tc := range []struct {
		method, path, auth, host, origin string
		status                           int
	}{
		{"POST", "/v1/embeddings", "", "127.0.0.1:8877", "", 401},
		{"POST", "/v1/embeddings", "Bearer wrong", "127.0.0.1:8877", "", 401},
		{"POST", "/v1/embeddings", "Bearer local-secret", "evil.test", "", 403},
		{"POST", "/v1/embeddings", "Bearer local-secret", "127.0.0.1:8877", "http://127.0.0.1:8877", 403},
		{"GET", "/v1/embeddings", "Bearer local-secret", "127.0.0.1:8877", "", 404},
		{"POST", "/v1/embeddings", "Bearer " + museProxyToken("local-secret", "terminal"), "127.0.0.1:8877", "", 404},
		{"GET", "/v1/embedding-models", "Bearer " + museProxyToken("local-secret", "terminal"), "127.0.0.1:8877", "", 404},
		{"POST", "/v1/embeddings", "Bearer " + museProxyToken("local-secret", "t3"), "127.0.0.1:8877", "", 404},
		{"GET", "/v1/embedding-models", "Bearer " + museProxyToken("local-secret", "t3"), "127.0.0.1:8877", "", 404},
		{"POST", "/v1/embeddings", "Bearer " + omnigentProxyToken("local-secret", "codex"), "127.0.0.1:8877", "", 404},
		{"GET", "/v1/embedding-models", "Bearer " + omnigentProxyToken("local-secret", "codex"), "127.0.0.1:8877", "", 404},
		{"POST", "/v1/embeddings", "Bearer " + omnigentProxyToken("local-secret", "claude"), "127.0.0.1:8877", "", 404},
		{"GET", "/v1/embedding-models", "Bearer " + omnigentProxyToken("local-secret", "claude"), "127.0.0.1:8877", "", 404},
		{"POST", "/v1/%65mbeddings", "Bearer local-secret", "127.0.0.1:8877", "", 404},
		{"POST", "/v1/embeddings?x=y", "Bearer local-secret", "127.0.0.1:8877", "", 404},
	} {
		a := testApp(t)
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:8877"+tc.path, strings.NewReader(`{}`))
		r.Host = tc.host
		r.Header.Set("Authorization", tc.auth)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		a.inferenceHandler("key", "org", "local-secret", "127.0.0.1:8877").ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s expected %d got %d", tc.path, tc.status, w.Code)
		}
	}
}

func TestEmbeddingsRequireReadyKiloSnapshot(t *testing.T) {
	for _, state := range []string{"unsaved", "pending", "changing"} {
		for _, path := range []string{"/v1/embeddings", "/v1/embedding-models"} {
			a := subscriptionTestApp(t)
			a.apiKey = "secret"
			a.config.OrgID = "org"
			switch state {
			case "unsaved":
				a.connectionNeedsSave = true
			case "pending":
				a.login = &loginSession{Status: "pending"}
			case "changing":
				a.providerChanging = true
			}
			a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
				t.Error("non-ready Kilo was contacted")
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(embeddingCatalogFixture)), Request: r}, nil
			})
			method := "POST"
			if strings.HasSuffix(path, "embedding-models") {
				method = "GET"
			}
			r := httptest.NewRequest(method, "http://127.0.0.1:8877"+path, strings.NewReader(`{"model":"vendor/embed","input":"test"}`))
			r.Header.Set("Authorization", "Bearer local-secret")
			w := httptest.NewRecorder()
			a.inferenceHandler("secret", "org", "local-secret", r.Host).ServeHTTP(w, r)
			if w.Code != 401 || !strings.Contains(w.Body.String(), "kilo_connection_required") {
				t.Fatalf("state %s path %s: %d %s", state, path, w.Code, w.Body)
			}
		}
	}
}

func TestEmbeddingsCanonicalRoutesOnly(t *testing.T) {
	a := testApp(t)
	a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
		t.Error("prefixed route contacted upstream")
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(embeddingCatalogFixture)), Request: r}, nil
	})
	for _, path := range []string{"/v1/embeddings", "/v1/embedding-models"} {
		method := "POST"
		if strings.HasSuffix(path, "embedding-models") {
			method = "GET"
		}
		r := httptest.NewRequest(method, "http://127.0.0.1:8877"+zedProxyPrefix("local-secret")+path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer local-secret")
		w := httptest.NewRecorder()
		a.inferenceHandler("secret", "org", "local-secret", r.Host).ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatalf("prefixed route status %d", w.Code)
		}
	}
}

func TestEmbeddingsProxyUploadBoundAndCancellation(t *testing.T) {
	a := testApp(t)
	calls := 0
	a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})
	r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/embeddings", strings.NewReader(`{"input":"`+strings.Repeat("x", 32<<20)+`"}`))
	r.ContentLength = -1
	r.Header.Set("Authorization", "Bearer local-secret")
	w := httptest.NewRecorder()
	a.inferenceHandler("key", "org", "local-secret", r.Host).ServeHTTP(w, r)
	if w.Code != 413 || calls != 0 {
		t.Fatalf("oversize status %d calls %d", w.Code, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
		if r.Context().Err() == nil {
			t.Error("cancellation lost")
		}
		return nil, r.Context().Err()
	})
	r = httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/embeddings", strings.NewReader(`{}`)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer local-secret")
	w = httptest.NewRecorder()
	a.inferenceHandler("key", "org", "local-secret", r.Host).ServeHTTP(w, r)
	if w.Code != 502 {
		t.Fatalf("canceled transport = %d", w.Code)
	}
}

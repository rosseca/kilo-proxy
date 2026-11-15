package main

import (
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddingUsageAccounting(t *testing.T) {
	good := `{"data":[0],"model":"short","usage":{"prompt_tokens":8,"is_byok":true,"cost":0,"cost_details":{"upstream_inference_cost":0.0000016}}}`
	for _, tc := range []struct {
		body              string
		status            int
		complete, limited bool
	}{
		{good, 200, true, false}, {good, 403, false, false}, {`{"data":[]}`, 200, false, false},
		{`{"usage":null}`, 200, false, false}, {`{"usage":{}}`, 200, true, false},
		{`{"usage":false}`, 200, false, true}, {`{"usage":{},"error":true}`, 200, false, false},
		{good[:len(good)-1], 200, false, true},
	} {
		r := httptest.NewRequest("POST", "http://localhost/v1/embeddings", nil)
		u := newUsageObserver(r, "org")
		u.setEmbeddingModel("openai/text-embedding-3-small")
		u.configure(&http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {"application/json"}}})
		for i := 0; i < len(tc.body); i += 7 {
			u.feed([]byte(tc.body[i:min(i+7, len(tc.body))]))
		}
		u.eof()
		v := u.snapshot().usage
		if v.Complete != tc.complete || v.Limited != tc.limited || v.Operation != "embedding" || v.Model != "openai/text-embedding-3-small" {
			t.Fatalf("%s -> %+v", tc.body, v)
		}
		if tc.body == good && tc.status == 200 {
			if v.Input == nil || *v.Input != 8 || v.Output != nil || v.BYOK == nil || !*v.BYOK || v.CostUSD == nil || *v.CostUSD != "0.000001600" || v.CostSource != "usage.cost_details.upstream_inference_cost" {
				t.Fatalf("usage %+v", v)
			}
		} else if v.CostUSD != nil || v.Input != nil {
			t.Fatal("invented partial usage")
		}
	}
}

type embeddingCancelEOFBody struct {
	io.Reader
	cancel context.CancelFunc
}

func (b embeddingCancelEOFBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		b.cancel()
	}
	return n, err
}
func (b embeddingCancelEOFBody) Close() error { return nil }

func TestEmbeddingUsageCaptureOffAndLateDisconnect(t *testing.T) {
	a := testApp(t)
	a.captureEnabled = false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	response := `{"data":"` + strings.Repeat("VECTOR_PRIVATE", 100000) + `","usage":{"prompt_tokens":8,"cost":0.1}}`
	a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: embeddingCancelEOFBody{strings.NewReader(response), cancel}, Request: r}, nil
	})
	r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/embeddings", strings.NewReader(`{"model":"openai/embed","input":"INPUT_PRIVATE"}`)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer local-secret")
	w := httptest.NewRecorder()
	a.inferenceHandler("upstream-secret", "org-saved", "local-secret", r.Host).ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != response || a.failures != 0 || a.usageTotal.Input != 8 || a.usageTotal.Incomplete != 0 {
		t.Fatalf("status=%d failures=%d usage=%+v", w.Code, a.failures, a.usageTotal)
	}
	a.drainUsageHistory()
	b, err := os.ReadFile(filepath.Join(a.dir, "usage-history.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"VECTOR_PRIVATE", "INPUT_PRIVATE", "upstream-secret"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("history leaked content")
		}
	}
	// A fully delivered response with unavailable accounting is still HTTP success.
	bctx, bcancel := context.WithCancel(context.Background())
	defer bcancel()
	a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: embeddingCancelEOFBody{strings.NewReader(`{"data":[]}`), bcancel}, Request: r}, nil
	})
	r = httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/embeddings", strings.NewReader(`{}`)).WithContext(bctx)
	r.Header.Set("Authorization", "Bearer local-secret")
	w = httptest.NewRecorder()
	a.inferenceHandler("upstream-secret", "org-saved", "local-secret", r.Host).ServeHTTP(w, r)
	if w.Code != 200 || a.failures != 0 {
		t.Fatal("late disconnect misclassified due to missing usage")
	}
}

func extractEmbedding(t *testing.T, body string, chunk int) ([]byte, error) {
	t.Helper()
	x := newEmbeddingUsageExtractor()
	for len(body) > 0 {
		n := min(chunk, len(body))
		x.feed([]byte(body[:n]))
		body = body[n:]
	}
	return x.finish()
}

func TestEmbeddingUsageExtractorLargeBatch(t *testing.T) {
	data := `"data":[` + strings.Repeat(`{"embedding":[0.5,-0.2,1e-10],"usage":"not metadata"},`, 30000) + `{}]`
	meta := `"model":"short-model","usage":{"prompt_tokens":8,"cost":0,"cost_details":{"upstream_inference_cost":0.0000016},"is_byok":true}`
	for _, body := range []string{`{` + data + `,` + meta + `}`, `{` + meta + `,` + data + `}`, `{"data":"` + strings.Repeat("AAAA", 350000) + `",` + meta + `}`} {
		for _, chunk := range []int{1, 7, 4096} {
			b, err := extractEmbedding(t, body, chunk)
			if err != nil {
				t.Fatal(err)
			}
			var d map[string]json.RawMessage
			if json.Unmarshal(b, &d) != nil || len(d) != 2 || string(d["usage"]) != `{"prompt_tokens":8,"cost":0,"cost_details":{"upstream_inference_cost":0.0000016},"is_byok":true}` {
				t.Fatalf("metadata=%s", b)
			}
		}
	}
}

func TestEmbeddingUsageExtractorSyntaxAndDuplicates(t *testing.T) {
	for _, body := range []string{
		`{"data":[{"usage":{},"text":"brace } quote \""}],"\u0075sage":{"prompt_tokens":8},"usage":{"prompt_tokens":9},"error":null}`,
		`{"unknown":"` + strings.Repeat("x", 10000) + `","usage":{"prompt_tokens":9},"error":{"secret":"` + strings.Repeat("y", 100000) + `"},"error":null}`,
		`{"` + strings.Repeat("z", 20000) + `":"value","usage":{"prompt_tokens":9}}`,
	} {
		b, err := extractEmbedding(t, body, 7)
		if err != nil {
			t.Fatal(err)
		}
		var d struct {
			Usage struct {
				Prompt int `json:"prompt_tokens"`
			}
			Error any
		}
		if json.Unmarshal(b, &d) != nil || d.Usage.Prompt != 9 || d.Error != nil {
			t.Fatalf("metadata=%s", b)
		}
	}
	for _, body := range []string{``, `[]`, `{"usage":{`, `{"usage":{}} garbage`, `{"usage":{},}`, `{"data":[}`, `{"usage":false}`, `{"usage":{"prompt_tokens":}}`, `{} {}`} {
		if b, err := extractEmbedding(t, body, 1); err == nil {
			t.Fatalf("accepted %q: %s", body, b)
		}
	}
}

func TestEmbeddingUsageRejectsMalformedEarlierDuplicate(t *testing.T) {
	for _, body := range []string{
		`{"usage":garbage,"usage":{"prompt_tokens":8,"cost":1}}`,
		`{"provider_metadata":{invalid},"provider_metadata":{},"usage":{}}`,
		`{"model":"bad\x","model":"valid","usage":{}}`,
	} {
		for _, chunk := range []int{1, 4096} {
			if _, err := extractEmbedding(t, body, chunk); err == nil {
				t.Fatalf("accepted malformed duplicate %s", body)
			}
		}
	}
}

func TestEmbeddingUsageExtractorBounds(t *testing.T) {
	// Selected value bytes (including JSON quotes) are the cumulative budget.
	for _, n := range []int{embeddingUsageMetadataLimit - 2, embeddingUsageMetadataLimit - 1} {
		x := newEmbeddingUsageExtractor()
		x.feed([]byte(`{"model":"` + strings.Repeat("x", n) + `"}`))
		_, err := x.finish()
		if (err == nil) != (n == embeddingUsageMetadataLimit-2) {
			t.Fatalf("n=%d err=%v", n, err)
		}
	}
	for _, depth := range []int{128, 129} {
		b := `{"data":` + strings.Repeat("[", depth-1) + `0` + strings.Repeat("]", depth-1) + `,"usage":{}}`
		_, err := extractEmbedding(t, b, 7)
		if (err == nil) != (depth == 128) {
			t.Fatalf("depth %d: %v", depth, err)
		}
	}
	_, err := extractEmbedding(t, `{"model":"`+strings.Repeat("x", 40000)+`","model":"`+strings.Repeat("y", 40000)+`"}`, 4096)
	if err == nil {
		t.Fatal("duplicate fields bypassed cumulative limit")
	}
}

func TestEmbeddingUsageExtractorPartitionsAndFinish(t *testing.T) {
	body := `{"data":[{"embedding":"\\\"{}"}],"usage":{"prompt_tokens":8},"provider_metadata":{"gateway":{"marketCost":"0.1"}},"error":null}`
	want, err := extractEmbedding(t, body, 4096)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(42))
	for range 50 {
		x := newEmbeddingUsageExtractor()
		s := body
		for len(s) > 0 {
			n := min(len(s), rng.Intn(17)+1)
			x.feed([]byte(s[:n]))
			s = s[n:]
		}
		got, err := x.finish()
		if err != nil || string(got) != string(want) {
			t.Fatalf("%s %v", got, err)
		}
		x.feed([]byte("ignored"))
		again, err := x.finish()
		if err != nil || string(again) != string(want) {
			t.Fatal("finish not idempotent")
		}
	}
}

func FuzzEmbeddingUsageExtractor(f *testing.F) {
	for _, seed := range []string{`{}`, `{"data":[1,2],"usage":{"prompt_tokens":8}}`, `{"error":null}`, `{"\u0075sage":{}}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 2<<20 {
			t.Skip()
		}
		a, ea := extractEmbedding(t, s, 1)
		b, eb := extractEmbedding(t, s, 4096)
		if (ea == nil) != (eb == nil) || string(a) != string(b) {
			t.Fatal("partition-dependent result")
		}
		if ea == nil && (!json.Valid(a) || len(a) > embeddingUsageMetadataLimit+1024) {
			t.Fatal("invalid or unbounded envelope")
		}
	})
}

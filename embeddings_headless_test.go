package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHeadlessEmbeddingsEndToEnd(t *testing.T) {
	setup := headlessTestClient(t)
	dir := setup.dir
	port, listenProxy := headlessTestProxyReservation(t)
	if err := setup.call(context.Background(), "POST", "/api/config", map[string]any{"apiKey": "synthetic-embedding-key", "orgId": "synthetic-org", "remember": true, "port": port}, nil); err != nil {
		t.Fatal(err)
	}
	setup.close()
	c, err := openHeadlessClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.app.listenProxy = listenProxy
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-embedding-key" || r.Header.Get("X-KiloCode-OrganizationId") != "synthetic-org" {
			t.Error("wrong saved organization connection")
		}
		if r.Method == "GET" && r.URL.Path == "/api/gateway/embedding-models" {
			io.WriteString(w, embeddingCatalogFixture)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/api/gateway/embeddings" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 404)
			return
		}
		var input struct {
			Model string `json:"model"`
			Input any    `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Model != "vendor/embed" {
			t.Error("model or input lost")
		}
		n := 1
		if batch, ok := input.Input.([]any); ok {
			n = len(batch)
		}
		data := make([]map[string]any, n)
		for i := range n {
			data[i] = map[string]any{"index": i, "object": "embedding", "embedding": []float64{float64(i), 0.25, -0.5}}
		}
		jsonResponse(w, 200, map[string]any{"object": "list", "model": "embed", "data": data, "usage": map[string]any{"prompt_tokens": 8, "is_byok": true, "cost": 0, "cost_details": map[string]any{"upstream_inference_cost": 0.0000016}}})
	}))
	defer upstream.Close()
	setUpstream(c.app, upstream.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { err := serveHeadless(ctx, c, false, io.Discard); c.close(); done <- err }()
	awaitHeadlessRuntime(t, dir, done)
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("headless did not stop")
		}
	}()
	client := &http.Client{Timeout: 5 * time.Second}
	base := "http://127.0.0.1:" + strconv.Itoa(port) + "/v1"
	for _, input := range []any{"one", []string{"one", "two"}} {
		payload, _ := json.Marshal(map[string]any{"model": "vendor/embed", "input": input, "encoding_format": "float"})
		r, _ := http.NewRequest("POST", base+"/embeddings", bytes.NewReader(payload))
		r.Header.Set("Authorization", "Bearer "+c.app.config.LocalKey)
		r.Header.Set("Content-Type", "application/json")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Data []struct {
				Index     int
				Embedding []float64
			}
			Usage struct {
				Prompt int `json:"prompt_tokens"`
			}
		}
		err = json.NewDecoder(response.Body).Decode(&body)
		response.Body.Close()
		want := 1
		if _, ok := input.([]string); ok {
			want = 2
		}
		if err != nil || response.StatusCode != 200 || len(body.Data) != want || body.Usage.Prompt != 8 {
			t.Fatalf("inference status=%d count=%d error=%v", response.StatusCode, len(body.Data), err)
		}
		for i, v := range body.Data {
			if v.Index != i || len(v.Embedding) != 3 || v.Embedding[0] != float64(i) {
				t.Fatal("vector order or values changed")
			}
		}
	}
	var output bytes.Buffer
	live, err := openHeadlessClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer live.close()
	if live.app != nil {
		t.Fatal("CLI did not connect to live controller")
	}
	if err := headlessEmbeddings(context.Background(), live, []string{"models", "--json"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "vendor/embed") {
		t.Fatal("missing discovery")
	}
	c.app.mu.Lock()
	failures, tokens := c.app.failures, c.app.usageTotal.Input
	c.app.mu.Unlock()
	if failures != 0 || tokens != 16 {
		t.Fatalf("accounting failures=%d tokens=%d", failures, tokens)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeadlessEmbeddingsOutputAndParsing(t *testing.T) {
	calls := 0
	client := headlessSetupTestClient{func(_ context.Context, method, path string, input, output any) error {
		calls++
		if method != "GET" || path != "/api/embedding-models" {
			t.Error("wrong controller request")
		}
		catalog, err := parseEmbeddingCatalog([]byte(embeddingCatalogFixture))
		if err != nil {
			return err
		}
		headlessSetupTestJSON(t, catalog, output)
		return nil
	}}
	for _, args := range [][]string{{"models"}, {"models", "--json"}} {
		var out bytes.Buffer
		if err := headlessEmbeddings(context.Background(), client, args, &out); err != nil {
			t.Fatal(err)
		}
		if len(args) == 1 {
			if out.String() != "vendor/embed\tEmbedding\tdimensions=1536\nvendor/unknown\tvendor/unknown\tdimensions=unknown\n" {
				t.Fatalf("output %q", out.String())
			}
		} else {
			var d embeddingCatalog
			if json.Unmarshal(out.Bytes(), &d) != nil || len(d.Models) != 2 {
				t.Fatal("invalid JSON")
			}
		}
	}
	if calls != 2 {
		t.Fatal("discovery not fresh")
	}
	command, dir, rest, handled, err := parseHeadlessCommand([]string{"--config-dir", "/private/tmp/embed-profile", "embeddings", "models", "--json"})
	if err != nil || !handled || command != "embeddings" || dir != "/private/tmp/embed-profile" || strings.Join(rest, " ") != "models --json" {
		t.Fatalf("parser: %s %s %v %v", command, dir, rest, err)
	}
	for _, args := range [][]string{nil, {"unknown"}, {"models", "extra"}, {"models", "--secret=must-not-echo"}} {
		var out, stderr bytes.Buffer
		before := calls
		if runHeadlessSetupCommand(context.Background(), client, "embeddings", args, nil, &out, &stderr) != 1 || out.Len() != 0 || strings.Contains(stderr.String(), "must-not-echo") || calls != before {
			t.Fatalf("args %v output=%q error=%q", args, out.String(), stderr.String())
		}
	}
}

func TestHeadlessEmbeddingsHelpAndFailures(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"models", "--help"}} {
		dir := filepath.Join(t.TempDir(), "not-created")
		var out, stderr bytes.Buffer
		if code := runHeadlessSetupCLI(dir, "embeddings", args, nil, &out, &stderr); code != 0 || !strings.Contains(out.String(), "embeddings models") {
			t.Fatalf("code=%d stderr=%s", code, stderr.String())
		}
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("help created a profile")
		}
	}
	client := headlessSetupTestClient{func(context.Context, string, string, any, any) error {
		return errors.New("Kilo rejected embedding model discovery (HTTP 403).")
	}}
	var out, stderr bytes.Buffer
	if runHeadlessSetupCommand(context.Background(), client, "embeddings", []string{"models"}, nil, &out, &stderr) != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), "403") {
		t.Fatal("failure not reported")
	}
}

func TestHeadlessEmbeddingsOfflineProfile(t *testing.T) {
	setup := headlessTestClient(t)
	dir := setup.dir
	if err := setup.call(context.Background(), "POST", "/api/config", map[string]any{"apiKey": "saved-secret", "orgId": "saved-org", "remember": true, "port": 8877}, nil); err != nil {
		t.Fatal(err)
	}
	setup.close()
	c, err := openHeadlessClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if c.app == nil {
		t.Fatal("expected exclusive offline controller")
	}
	c.app.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer saved-secret" || r.Header.Get("X-KiloCode-OrganizationId") != "saved-org" {
			t.Error("offline connection not preserved")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"models":[]}`)), Request: r}, nil
	})
	var out bytes.Buffer
	if err := headlessEmbeddings(context.Background(), c, []string{"models"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "No embedding models are listed for this organization.\n" || c.app.proxyServer != nil {
		t.Fatal("offline discovery started inference or returned bad output")
	}
}

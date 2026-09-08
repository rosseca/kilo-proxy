package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func imageTestPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{R: 123, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func imageTestDataURL(data []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}
func imageTestResponse(data []byte, usage any) map[string]any {
	return map[string]any{"model": "vendor/image-model", "choices": []any{map[string]any{"message": map[string]any{"images": []any{map[string]any{"type": "image_url", "image_url": map[string]string{"url": imageTestDataURL(data)}}}}}}, "usage": usage}
}
func imageTestApp(t *testing.T, generate http.HandlerFunc) *app {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer image-upstream-secret" || r.Header.Get("X-KiloCode-OrganizationId") != "image-org" {
			t.Error("image request lost its Kilo key or organization")
		}
		switch r.URL.Path {
		case "/api/gateway/models":
			jsonResponse(w, 200, map[string]any{"data": []any{map[string]any{"id": "vendor/image-model", "name": "Image model", "architecture": map[string]any{"input_modalities": []string{"text", "image"}, "output_modalities": []string{"image", "text"}}}, map[string]any{"id": "vendor/text-model", "architecture": map[string]any{"output_modalities": []string{"text"}}}, map[string]any{"id": "vendor/image-text-input", "architecture": map[string]any{"input_modalities": []string{"text"}, "output_modalities": []string{"image"}}}}})
		case "/chat/completions":
			generate(w, r)
		default:
			t.Errorf("unexpected image fixture route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	a := testApp(t)
	setUpstream(a, server.URL)
	a.apiKey = "image-upstream-secret"
	a.config.OrgID = "image-org"
	a.config.ImageGeneration = imageGenerationSettings{Enabled: true, Model: "vendor/image-model"}
	a.imageGenerationURL = server.URL + "/chat/completions"
	return a
}
func imageTestRequest() *http.Request {
	r := httptest.NewRequest("POST", "http://127.0.0.1:8877/mcp/images", nil)
	r.Header.Set("Authorization", "Bearer image-local-secret")
	r.Header.Set("Thread-Id", "image-test-thread")
	return r
}
func imageTestGenerate(a *app, args imageGenerationArguments) (*imageGenerationResult, error) {
	return a.generateImage(imageTestRequest(), args, "image-upstream-secret", "image-org", "image-local-secret")
}

func TestImageGenerationCreateEditAndRecordCost(t *testing.T) {
	pngData := imageTestPNG(t)
	calls := 0
	a := imageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			Model      string   `json:"model"`
			Stream     bool     `json:"stream"`
			Modalities []string `json:"modalities"`
			Messages   []struct {
				Content []struct {
					Type     string `json:"type"`
					Text     string `json:"text"`
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "vendor/image-model" || request.Stream || strings.Join(request.Modalities, ",") != "image,text" || len(request.Messages) != 1 {
			t.Errorf("incorrect image request: %+v", request)
		}
		if r.Header.Get("Thread-Id") != "" || r.Header.Get("Cookie") != "" {
			t.Error("local metadata leaked upstream")
		}
		parts := request.Messages[0].Content
		if parts[0].Text != "Draw a small green square" {
			t.Error("prompt changed")
		}
		if calls == 2 && (len(parts) != 2 || parts[1].ImageURL.URL != imageTestDataURL(pngData)) {
			t.Error("reference image was not sent as bounded embedded content")
		}
		response := imageTestResponse(pngData, map[string]any{"prompt_tokens": 25, "completion_tokens": 100, "cost": 0.04})
		// Image responses can exceed the general observer's 1 MiB buffer while their
		// final usage object is still small and must be counted exactly once.
		response["padding"] = strings.Repeat("x", usageBufferLimit+10)
		w.Header().Set("Set-Cookie", "image-upstream-secret")
		jsonResponse(w, 200, response)
	})
	result, err := imageTestGenerate(a, imageGenerationArguments{Prompt: "Draw a small green square"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Images) != 1 || result.Images[0].Width != 2 || result.Images[0].Height != 3 || result.CostUSD == nil || *result.CostUSD != "0.040000000" {
		t.Fatalf("image metadata/cost lost: %+v", result)
	}
	first := result.Images[0].Path
	info, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatal("generated image is not private")
	}
	data, err := os.ReadFile(first)
	if err != nil || !bytes.Equal(data, pngData) {
		t.Fatal("saved image differs from returned image")
	}
	result, err = imageTestGenerate(a, imageGenerationArguments{Prompt: "Draw a small green square", ReferenceImage: first})
	if err != nil {
		t.Fatal(err)
	}
	if result.Images[0].Path == first || calls != 2 {
		t.Fatal("image edit overwrote the original or repeated generation")
	}
	if a.usageTotal.CostUSD != "0.080000000" || a.usageTotal.Priced != 2 || a.requests != 2 || a.active != 0 || a.activeTraces != 0 {
		t.Fatalf("incorrect usage/activity: %+v", a.usageTotal)
	}
	if len(a.usageSessions) != 1 {
		t.Fatal("image usage lost the available Codex thread identity")
	}
	detail := a.traces[a.events[0].ID]
	raw, _ := json.Marshal(detail)
	for _, secret := range []string{"image-upstream-secret", "image-local-secret", a.adminToken} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("activity leaked a credential")
		}
	}
	if !strings.Contains(detail.UpstreamRequest.Body, "Draw a small green square") || !strings.Contains(detail.UpstreamRequest.Body, "image payload omitted") || !strings.Contains(detail.UpstreamResponse.Body, "cost") || strings.Contains(detail.UpstreamResponse.Body, imageTestDataURL(pngData)) {
		t.Fatalf("activity omitted useful request/usage or retained image payload: %+v", detail)
	}
}

func TestImageGenerationInferenceCostIsRecordedOnce(t *testing.T) {
	pngData := imageTestPNG(t)
	for _, tc := range []struct {
		name, cost, source string
		usage              any
		marketCost         json.RawMessage
	}{
		{
			name:       "metadata without usage",
			cost:       "0.125000001",
			source:     "provider_metadata.gateway.marketCost",
			marketCost: json.RawMessage(`0.125000001`),
		},
		{
			name:       "decimal string metadata without usage",
			cost:       "0.125000001",
			source:     "provider_metadata.gateway.marketCost",
			marketCost: json.RawMessage(`"0.125000001"`),
		},
		{
			name:   "BYOK upstream cost instead of zero fee",
			cost:   "0.219760000",
			source: "usage.cost_details.upstream_inference_cost",
			usage: map[string]any{
				"cost": 0, "is_byok": true,
				"cost_details": map[string]any{"upstream_inference_cost": json.Number("0.21976")},
			},
			// A second price is an alternative source, never an extra charge.
			marketCost: json.RawMessage(`0.3`),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			a := imageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				response := imageTestResponse(pngData, tc.usage)
				if tc.usage == nil {
					delete(response, "usage")
				}
				response["provider_metadata"] = map[string]any{
					"unrelated_private_metadata": "do-not-store-this-provider-value",
					"gateway": map[string]any{
						"marketCost":            tc.marketCost,
						"image_payload":         imageTestDataURL(pngData),
						"large_private_payload": strings.Repeat("x", usageBufferLimit+10),
					},
				}
				jsonResponse(w, 200, response)
			})
			result, err := imageTestGenerate(a, imageGenerationArguments{Prompt: "Draw a small green square"})
			if err != nil {
				t.Fatal(err)
			}
			if result.CostUSD == nil || *result.CostUSD != tc.cost || result.CostSource != tc.source || len(result.Images) != 1 {
				t.Fatalf("image result lost inference cost or source: %+v", result)
			}
			if calls.Load() != 1 || a.requests != 1 || a.active != 0 || a.activeTraces != 0 || a.failures != 0 {
				t.Fatalf("generation repeated or activity remained active: calls=%d requests=%d active=%d traces=%d failures=%d", calls.Load(), a.requests, a.active, a.activeTraces, a.failures)
			}
			if a.usageTotal.CostUSD != tc.cost || a.usageTotal.Requests != 1 || a.usageTotal.Priced != 1 || a.usageTotal.WithTokens != 0 {
				t.Fatalf("image inference cost was missing or counted twice: %+v", a.usageTotal)
			}
			if len(a.usageSessions) != 1 || len(a.events) != 1 {
				t.Fatal("expected one image conversation and activity event")
			}
			for _, session := range a.usageSessions {
				if session.CostUSD != tc.cost || session.Requests != 1 || session.Priced != 1 {
					t.Fatalf("conversation cost was missing or counted twice: %+v", session)
				}
			}
			eventUsage := a.events[0].Usage
			if eventUsage == nil || eventUsage.CostUSD == nil || *eventUsage.CostUSD != tc.cost || eventUsage.CostSource != tc.source {
				t.Fatalf("activity lost inference cost or source: %+v", eventUsage)
			}
			detail := a.traces[a.events[0].ID]
			if detail == nil {
				t.Fatal("image activity detail is missing")
			}
			var summary struct {
				ProviderMetadata map[string]json.RawMessage `json:"provider_metadata"`
			}
			if err := json.Unmarshal([]byte(detail.UpstreamResponse.Body), &summary); err != nil {
				t.Fatal(err)
			}
			var gateway map[string]json.RawMessage
			if err := json.Unmarshal(summary.ProviderMetadata["gateway"], &gateway); err != nil {
				t.Fatal(err)
			}
			if len(summary.ProviderMetadata) != 1 || len(gateway) != 1 || !bytes.Equal(gateway["marketCost"], tc.marketCost) {
				t.Fatalf("activity metadata should retain only the exact raw market cost: %s", detail.UpstreamResponse.Body)
			}
			rawTrace, _ := json.Marshal(detail)
			for _, omitted := range []string{"do-not-store-this-provider-value", "large_private_payload", imageTestDataURL(pngData)} {
				if bytes.Contains(rawTrace, []byte(omitted)) {
					t.Fatalf("activity retained unrelated provider metadata or image data: %q", omitted)
				}
			}
			var recordedResult imageGenerationResult
			if err := json.Unmarshal([]byte(detail.Response.Body), &recordedResult); err != nil || recordedResult.CostSource != tc.source {
				t.Fatalf("MCP activity response lost cost provenance: %+v, %v", recordedResult, err)
			}
		})
	}
}

func TestImageProviderCostMetadataOmitsNonMonetaryValues(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{"gateway":[]}`,
		`{"gateway":{"marketCost":{"image_payload":"private"}}}`,
		`{"gateway":{"marketCost":"private"}}`,
		`{"gateway":{"marketCost":-1}}`,
	} {
		if metadata := imageProviderCostMetadata(json.RawMessage(raw)); metadata != nil {
			t.Fatalf("retained nonmonetary provider metadata from %s: %+v", raw, metadata)
		}
	}
}

func TestImageGenerationRejectsInvalidModelsAndReferencesBeforePaidCall(t *testing.T) {
	var calls atomic.Int32
	a := imageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Error("invalid generation reached paid endpoint")
	})
	for _, settings := range []imageGenerationSettings{{}, {Enabled: true}, {Enabled: true, Model: "auto"}, {Enabled: true, Model: "vendor/text-model"}, {Enabled: true, Model: "vendor/missing"}} {
		a.config.ImageGeneration = settings
		if _, err := imageTestGenerate(a, imageGenerationArguments{Prompt: "Draw"}); err == nil {
			t.Fatalf("accepted invalid settings %+v", settings)
		}
	}
	a.config.ImageGeneration = imageGenerationSettings{Enabled: true, Model: "vendor/image-model"}
	for _, reference := range []string{"https://example.test/image.png", "/etc/passwd", "../image.png", filepath.Join(a.dir, "generated-images", "image_"+strings.Repeat("a", 64)+".png")} {
		if _, err := imageTestGenerate(a, imageGenerationArguments{Prompt: "Draw", ReferenceImage: reference}); err == nil {
			t.Fatalf("accepted unsafe reference %s", reference)
		}
	}
	for _, prompt := range []string{"", " \n", strings.Repeat("x", imagePromptLimit+1), "bad\x00prompt"} {
		if _, err := imageTestGenerate(a, imageGenerationArguments{Prompt: prompt}); err == nil {
			t.Fatal("accepted invalid prompt")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("preflight checks happened after generation")
	}
}

func TestImageGenerationRejectsSymlinkDirectoryAndReference(t *testing.T) {
	a := imageTestApp(t, func(http.ResponseWriter, *http.Request) { t.Error("unsafe storage reached upstream") })
	outside := t.TempDir()
	path := filepath.Join(a.dir, "generated-images")
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, err := a.generatedImagesRoot(); err == nil {
		t.Fatal("accepted symlinked generated-images directory")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	root, dir, err := a.generatedImagesRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	target := filepath.Join(outside, "reference.png")
	if err := os.WriteFile(target, imageTestPNG(t), 0600); err != nil {
		t.Fatal(err)
	}
	name := "image_" + strings.Repeat("a", 64) + ".png"
	if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readGeneratedReference(root, dir, filepath.Join(dir, name)); err == nil {
		t.Fatal("read reference through symlink")
	}
}

func TestImageGenerationRejectsMalformedResultsButRetainsCharges(t *testing.T) {
	good := imageTestPNG(t)
	oversized := append([]byte(nil), good...)
	// A valid PNG signature with impossible dimensions must fail before allocation.
	binary.BigEndian.PutUint32(oversized[16:20], 100000)
	for _, tc := range []struct{ name, url string }{{"remote", "https://example.test/image.png"}, {"invalid", "data:image/png;base64,aW52YWxpZA=="}, {"mime", "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(good)}, {"truncated", imageTestDataURL(good[:40])}, {"oversized dimensions", imageTestDataURL(oversized)}} {
		t.Run(tc.name, func(t *testing.T) {
			a := imageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
				response := imageTestResponse(good, map[string]any{"cost": 0.025})
				response["choices"] = []any{map[string]any{"message": map[string]any{"images": []any{map[string]any{"image_url": map[string]string{"url": tc.url}}}}}}
				jsonResponse(w, 200, response)
			})
			if _, err := imageTestGenerate(a, imageGenerationArguments{Prompt: "Draw"}); err == nil {
				t.Fatal("accepted malformed result")
			}
			if a.usageTotal.CostUSD != "0.025000000" || a.failures != 1 {
				t.Fatal("rejected image hid its actual upstream charge")
			}
			files, err := os.ReadDir(filepath.Join(a.dir, "generated-images"))
			if err != nil || len(files) != 0 {
				t.Fatal("invalid result left generated files")
			}
		})
	}
}

func TestImageGenerationHTTPFailureRedirectAndConcurrency(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500, 302} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			a := imageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://example.test/no-secret-leak")
				w.WriteHeader(status)
				io.WriteString(w, "image-upstream-secret raw provider failure")
			})
			_, err := imageTestGenerate(a, imageGenerationArguments{Prompt: "Draw"})
			if err == nil || strings.Contains(err.Error(), "image-upstream-secret") || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) {
				t.Fatalf("wrong sanitized error: %v", err)
			}
			if a.events[0].Status != status {
				t.Fatalf("lost provider HTTP status: %+v", a.events[0])
			}
		})
	}
	a := imageTestApp(t, func(http.ResponseWriter, *http.Request) { t.Error("concurrency guard allowed a third generation") })
	a.imageGenerationActive = imageGenerationConcurrency
	if _, err := imageTestGenerate(a, imageGenerationArguments{Prompt: "Draw"}); err == nil {
		t.Fatal("concurrency limit ignored")
	}
	if a.imageGenerationActive != imageGenerationConcurrency {
		t.Fatal("rejected request corrupted active slots")
	}
}

func TestImageGenerationCancellationReleasesSlot(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	a := imageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := imageTestRequest().WithContext(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := a.generateImage(request, imageGenerationArguments{Prompt: "Draw"}, "image-upstream-secret", "image-org", "image-local-secret")
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("generation did not reach test endpoint")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled image request did not return")
	}
	if a.imageGenerationActive != 0 || a.active != 0 {
		t.Fatal("cancelled request leaked a concurrency or activity slot")
	}
}

func TestImageGenerationRejectsEditingWithoutImageInput(t *testing.T) {
	var calls atomic.Int32
	a := imageTestApp(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	a.config.ImageGeneration.Model = "vendor/image-text-input"
	root, dir, err := a.generatedImagesRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	name := "image_" + strings.Repeat("b", 64) + ".png"
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(imageTestPNG(t))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("cannot write synthetic reference: %v, %v", writeErr, closeErr)
	}
	_, err = imageTestGenerate(a, imageGenerationArguments{Prompt: "Make it blue", ReferenceImage: filepath.Join(dir, name)})
	if err == nil || !strings.Contains(err.Error(), "image input") || calls.Load() != 0 {
		t.Fatalf("editing was not rejected before generation: error=%v calls=%d", err, calls.Load())
	}
}

func TestImageGenerationHasIndependentResponseHeaderTimeout(t *testing.T) {
	pngData := imageTestPNG(t)
	a := imageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
			jsonResponse(w, 200, imageTestResponse(pngData, nil))
		}
	})
	shared := a.transport.(*http.Transport)
	shared.ResponseHeaderTimeout = 200 * time.Millisecond
	t.Cleanup(shared.CloseIdleConnections)
	result, err := imageTestGenerate(a, imageGenerationArguments{Prompt: "Draw slowly"})
	if err != nil || len(result.Images) != 1 {
		t.Fatalf("image generation inherited the proxy header timeout: %v", err)
	}
	if a.transport != shared || shared.ResponseHeaderTimeout != 200*time.Millisecond {
		t.Fatal("image generation modified the shared proxy transport")
	}
	// The original proxy transport must still enforce its shorter budget for
	// ordinary requests, even after a successful slow image generation.
	request, err := http.NewRequest(http.MethodPost, a.imageGenerationURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer image-upstream-secret")
	request.Header.Set("X-KiloCode-OrganizationId", "image-org")
	response, err := (&http.Client{Transport: shared, Timeout: 3 * time.Second}).Do(request)
	if response != nil {
		response.Body.Close()
	}
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("ordinary proxy request lost its response header timeout: %v", err)
	}
}

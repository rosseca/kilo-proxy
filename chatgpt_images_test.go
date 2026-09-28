package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
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

func cgImageItem(data []byte) map[string]any {
	return map[string]any{"id": "ig_fixture", "type": "image_generation_call", "status": "completed", "result": base64.StdEncoding.EncodeToString(data)}
}

func cgImageUsage() map[string]any {
	// A monetary field must never turn a subscription request into Kilo spend.
	return map[string]any{"input_tokens": 23, "output_tokens": 41, "total_tokens": 64, "input_tokens_details": map[string]any{"cached_tokens": 7}, "cost": 123.45}
}

func cgImageDone(index int, item any) map[string]any {
	return map[string]any{"type": "response.output_item.done", "output_index": index, "item": item}
}

func cgImageTerminal(output ...any) map[string]any {
	if output == nil {
		output = []any{}
	}
	return map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_image_fixture", "status": "completed", "output": output, "usage": cgImageUsage()}}
}

func cgImageSSE(events ...map[string]any) string {
	var out strings.Builder
	for _, event := range events {
		data, _ := json.Marshal(event)
		fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", event["type"], data)
	}
	return out.String()
}

func TestChatGPTImagesOrchestratorUsesAvailableMainlineModel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		models []string
		want   string
	}{
		{"verified model preferred", []string{"chatgpt/gpt-5.3-codex", "chatgpt/gpt-future", "chatgpt/gpt-5.5"}, "chatgpt/gpt-5.5"},
		{"available older mainline", []string{"chatgpt/gpt-5.3-codex", "chatgpt/gpt-5.4"}, "chatgpt/gpt-5.4"},
		{"new mainline without hardcoded catalog", []string{"chatgpt/gpt-5.3-codex-spark", "chatgpt/gpt-future"}, "chatgpt/gpt-future"},
		{"no cross provider fallback", []string{"openai/gpt-5.5", "chatgpt/gpt-5.3-codex", "chatgpt/o3"}, ""},
		{"empty catalog", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			models := make([]modelInfo, 0, len(tc.models))
			for _, id := range tc.models {
				models = append(models, modelInfo{ID: id})
			}
			if got := chatGPTImageOrchestrator(models); got != tc.want {
				t.Fatalf("orchestrator = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestChatGPTImagesFinalEventsAndOrdering(t *testing.T) {
	first := imageTestPNG(t)
	var second bytes.Buffer
	if err := png.Encode(&second, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	message := map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "Done"}}}
	for _, tc := range []struct {
		name   string
		events []map[string]any
		want   [][]byte
	}{
		{"terminal output", []map[string]any{cgImageTerminal(message, cgImageItem(first))}, [][]byte{first}},
		{"empty terminal output", []map[string]any{cgImageDone(2, cgImageItem(first)), cgImageTerminal()}, [][]byte{first}},
		{"terminal wins without duplication", []map[string]any{cgImageDone(0, cgImageItem(first)), cgImageTerminal(cgImageItem(second.Bytes()))}, [][]byte{second.Bytes()}},
		{"out of order items", []map[string]any{cgImageDone(3, cgImageItem(second.Bytes())), cgImageDone(1, cgImageItem(first)), cgImageTerminal()}, [][]byte{first, second.Bytes()}},
		{"repeated item", []map[string]any{cgImageDone(0, cgImageItem(first)), cgImageDone(0, cgImageItem(first)), cgImageTerminal(cgImageItem(first))}, [][]byte{first}},
		{"four images with repeated final item", []map[string]any{cgImageDone(0, cgImageItem(first)), cgImageDone(1, cgImageItem(first)), cgImageDone(2, cgImageItem(first)), cgImageDone(3, cgImageItem(first)), cgImageDone(3, cgImageItem(first)), cgImageTerminal()}, [][]byte{first, first, first, first}},
		{"partial ignored", []map[string]any{{"type": "response.image_generation_call.partial_image", "partial_image_b64": "not-a-final-image", "partial_image_index": 0, "output_index": 0}, cgImageDone(0, cgImageItem(first)), cgImageTerminal()}, [][]byte{first}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			images, usage, err := readChatGPTImages(strings.NewReader(cgImageSSE(tc.events...)))
			if err != nil || len(images) != len(tc.want) {
				t.Fatalf("final images = %d; error = %v", len(images), err)
			}
			for i := range images {
				if !bytes.Equal(images[i].data, tc.want[i]) || images[i].MIME != "image/png" || images[i].Path != "" {
					t.Fatalf("image %d lost original data/order or was saved before validation", i)
				}
			}
			var counts map[string]any
			if json.Unmarshal(usage, &counts) != nil || counts["input_tokens"] != float64(23) {
				t.Fatal("terminal usage was lost")
			}
		})
	}
	t.Run("response done alias and EOF frame", func(t *testing.T) {
		terminal := cgImageTerminal(cgImageItem(first))
		terminal["type"] = "response.done"
		images, _, err := readChatGPTImages(strings.NewReader(strings.TrimRight(cgImageSSE(terminal), "\n")))
		if err != nil || len(images) != 1 {
			t.Fatalf("done alias: %v", err)
		}
	})
}

func TestChatGPTImagesRejectFailedMissingAndMalformedResults(t *testing.T) {
	good := cgImageItem(imageTestPNG(t))
	failed := cgImageItem(imageTestPNG(t))
	failed["status"] = "failed"
	invalid := cgImageItem([]byte("not an image"))
	badBase64 := cgImageItem(nil)
	badBase64["result"] = "not-base64!"
	partial := cgImageItem(imageTestPNG(t))
	partial["status"] = "generating"
	incomplete := cgImageTerminal(good)
	incomplete["type"] = "response.incomplete"
	incomplete["response"].(map[string]any)["status"] = "incomplete"
	for _, tc := range []struct {
		name string
		sse  string
	}{
		{"EOF before terminal", cgImageSSE(cgImageDone(0, good))},
		{"empty stream", ""},
		{"DONE without terminal", "data: [DONE]\n\n"},
		{"text only", cgImageSSE(cgImageTerminal(map[string]any{"type": "message"}))},
		{"partial only", cgImageSSE(map[string]any{"type": "response.image_generation_call.partial_image", "partial_image_b64": good["result"]}, cgImageTerminal())},
		{"failed item", cgImageSSE(cgImageTerminal(failed))},
		{"unfinished item", cgImageSSE(cgImageTerminal(partial))},
		{"invalid image", cgImageSSE(cgImageTerminal(invalid))},
		{"invalid base64", cgImageSSE(cgImageTerminal(badBase64))},
		{"incomplete response", cgImageSSE(incomplete)},
		{"too many images", cgImageSSE(cgImageTerminal(good, good, good, good, good))},
		{"negative output index", cgImageSSE(cgImageDone(-1, good), cgImageTerminal())},
		{"unbounded output index", cgImageSSE(cgImageDone(2000, good), cgImageTerminal())},
		{"invalid JSON", "data: {invalid\n\n"},
		{"upstream error", cgImageSSE(map[string]any{"type": "error", "error": map[string]any{"message": "private-upstream-secret"}})},
		{"failed response", cgImageSSE(map[string]any{"type": "response.failed", "response": map[string]any{"error": map[string]any{"message": "private-upstream-secret"}}})},
		{"cancelled response", cgImageSSE(map[string]any{"type": "response.cancelled"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			images, _, err := readChatGPTImages(strings.NewReader(tc.sse))
			if err == nil || len(images) != 0 {
				t.Fatal("invalid response returned images")
			}
			if strings.Contains(err.Error(), "private-upstream-secret") {
				t.Fatal("upstream error content leaked")
			}
		})
	}
	t.Run("invalid image retains completed usage", func(t *testing.T) {
		_, usage, err := readChatGPTImages(strings.NewReader(cgImageSSE(cgImageTerminal(invalid))))
		if err == nil || len(usage) == 0 {
			t.Fatal("consumed quota was discarded when image decoding failed")
		}
	})
}

func TestChatGPTImagesRejectOversizedPayloads(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
	}{
		{"decoded image bound", base64.StdEncoding.EncodedLen(imageFileLimit) + 4},
		{"event bound", imageResponseLimit + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := cgImageItem(nil)
			item["result"] = strings.Repeat("A", tc.size)
			images, _, err := readChatGPTImages(strings.NewReader(cgImageSSE(cgImageTerminal(item))))
			if err == nil || len(images) != 0 {
				t.Fatal("oversized response accepted")
			}
		})
	}
}

func cgImageTestApp(t *testing.T, generate http.HandlerFunc) *app {
	t.Helper()
	a := subscriptionTestApp(t)
	a.captureEnabled = true
	a.config.ImageGeneration = imageGenerationSettings{Enabled: true, Provider: "chatgpt", Model: "vendor/stale-kilo-model"}
	a.apiKey, a.config.OrgID = "unused-kilo-secret", "unused-kilo-org"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-chatgpt-access" || r.Header.Get("ChatGPT-Account-Id") != "synthetic-account" {
			t.Error("subscription credentials missing")
		}
		for _, name := range []string{"Cookie", "X-KiloCode-OrganizationId", "X-Api-Key", "Thread-Id"} {
			if r.Header.Get(name) != "" {
				t.Errorf("unexpected client/Kilo header %s", name)
			}
		}
		switch r.URL.Path {
		case "/codex/models":
			jsonResponse(w, 200, map[string]any{"models": []any{map[string]any{"slug": "gpt-other", "input_modalities": []string{"text", "image"}}, map[string]any{"slug": "gpt-5.5", "input_modalities": []string{"text", "image"}}}})
		case "/responses":
			generate(w, r)
		default:
			t.Errorf("request crossed to an unexpected route: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	a.chatgpt.backendURL = server.URL
	a.chatGPTResponsesURL = server.URL + "/responses"
	setUpstream(a, server.URL)
	a.imageGenerationURL = server.URL + "/must-not-use-kilo"
	return a
}

func cgImageGenerate(a *app, args imageGenerationArguments) (*imageGenerationResult, error) {
	r := imageTestRequest()
	r.Header.Set("Cookie", "private-client-cookie")
	r.Header.Set("X-Api-Key", "private-client-key")
	return a.generateImage(r, args, "unused-kilo-secret", "unused-kilo-org", "image-local-secret")
}

func TestChatGPTImagesCreateEditAccountingAndPrivacy(t *testing.T) {
	data := imageTestPNG(t)
	var calls atomic.Int32
	a := cgImageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body["model"] != "gpt-5.5" || body["stream"] != true || body["store"] != false || body["instructions"] == "" {
			t.Error("image request is not subscription Responses with the available preferred model")
		}
		tool := body["tools"].([]any)[0].(map[string]any)
		if tool["type"] != "image_generation" || tool["output_format"] != "png" || tool["model"] != nil || body["tool_choice"].(map[string]any)["type"] != "image_generation" {
			t.Error("image tool missing or an unverified image model was forced")
		}
		parts := body["input"].([]any)[0].(map[string]any)["content"].([]any)
		if parts[0].(map[string]any)["text"] != "Draw a green square" {
			t.Error("prompt changed")
		}
		if call == 1 && len(parts) != 1 {
			t.Error("generation acquired an unsolicited reference")
		}
		if call == 2 && (len(parts) != 2 || parts[1].(map[string]any)["type"] != "input_image" || parts[1].(map[string]any)["image_url"] != imageTestDataURL(data)) {
			t.Error("edit did not include the exact original as a bounded inline image")
		}
		// The subscription backend can omit Content-Type and return empty terminal output.
		w.Header()["Content-Type"] = nil
		w.Header().Set("Set-Cookie", "synthetic-chatgpt-access")
		io.WriteString(w, cgImageSSE(cgImageDone(0, cgImageItem(data)), cgImageTerminal()))
	})
	first, err := cgImageGenerate(a, imageGenerationArguments{Prompt: "Draw a green square"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Provider != "chatgpt" || first.Billing != "subscription" || first.Model != "chatgpt/gpt-5.5" || first.CostUSD != nil || first.CostSource != "" || len(first.Images) != 1 {
		t.Fatalf("incorrect provider/accounting result: %+v", first)
	}
	second, err := cgImageGenerate(a, imageGenerationArguments{Prompt: "Draw a green square", ReferenceImage: first.Images[0].Path})
	if err != nil {
		t.Fatal(err)
	}
	if first.Images[0].Path == second.Images[0].Path || calls.Load() != 2 {
		t.Fatal("edit replaced original or request retried")
	}
	for _, result := range []*imageGenerationResult{first, second} {
		got, err := os.ReadFile(result.Images[0].Path)
		if err != nil || !bytes.Equal(got, data) || result.Images[0].Width != 2 || result.Images[0].Height != 3 || filepath.Dir(result.Images[0].Path) != filepath.Join(a.dir, "generated-images") {
			t.Fatal("saved original or metadata mismatch")
		}
		info, err := os.Stat(result.Images[0].Path)
		if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
			t.Fatal("generated original must be private")
		}
	}
	if a.usageTotal.SubscriptionRequests != 2 || a.usageTotal.Priced != 0 || a.usageTotal.CostUSD != "0.000000000" || a.usageTotal.WithTokens != 2 || a.usageTotal.Cached != 14 || a.usageTotal.Incomplete != 0 || a.requests != 2 || a.failures != 0 || a.active != 0 || a.activeTraces != 0 {
		t.Fatalf("incorrect subscription accounting: %+v", a.usageTotal)
	}
	if len(a.traces) != 2 || len(a.usageSessions) != 1 {
		t.Fatal("image activity lost trace or originating conversation")
	}
	for _, trace := range a.traces {
		raw, _ := json.Marshal(trace)
		for _, secret := range []string{"synthetic-chatgpt-access", "synthetic-account", "unused-kilo-secret", "image-local-secret", "private-client-cookie", "private-client-key", base64.StdEncoding.EncodeToString(data)} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("activity retained credentials or image payload")
			}
		}
		if !strings.Contains(trace.UpstreamRequest.Body, "Draw a green square") || !strings.Contains(trace.UpstreamResponse.Body, "input_tokens") {
			t.Fatal("useful prompt/usage information missing")
		}
	}
}

func TestChatGPTImagesQuotaFailureNeverFallsBackOrLeaks(t *testing.T) {
	var calls atomic.Int32
	a := cgImageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		jsonResponse(w, 429, map[string]any{"error": map[string]any{"message": "private-error-payload synthetic-chatgpt-access", "type": "insufficient_quota"}})
	})
	result, err := cgImageGenerate(a, imageGenerationArguments{Prompt: "Draw a green square"})
	if err == nil || result != nil || !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "Kilo was not used") || calls.Load() != 1 {
		t.Fatal("quota failure was not reported without retries")
	}
	if a.usageTotal.SubscriptionRequests != 1 || a.usageTotal.Priced != 0 || a.requests != 1 || a.failures != 1 || a.active != 0 {
		t.Fatalf("failed subscription accounting: %+v", a.usageTotal)
	}
	all, _ := json.Marshal(a.traces)
	for _, secret := range []string{"private-error-payload", "synthetic-chatgpt-access"} {
		if strings.Contains(string(all), secret) || strings.Contains(err.Error(), secret) {
			t.Fatal("upstream quota response leaked private content")
		}
	}
	cgImagesAssertNoFiles(t, a)
}

func TestChatGPTImagesUntrustedReferencesAndRoot(t *testing.T) {
	for _, mode := range []string{"outside reference", "symlink reference", "symlink root"} {
		t.Run(mode, func(t *testing.T) {
			a := cgImageTestApp(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid local path reached inference") })
			outside := filepath.Join(t.TempDir(), "image_"+strings.Repeat("a", 64)+".png")
			if err := os.WriteFile(outside, imageTestPNG(t), 0600); err != nil {
				t.Fatal(err)
			}
			args := imageGenerationArguments{Prompt: "Draw a green square", ReferenceImage: outside}
			if mode == "symlink root" {
				if err := os.Symlink(filepath.Dir(outside), filepath.Join(a.dir, "generated-images")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				args.ReferenceImage = ""
			} else if mode == "symlink reference" {
				root, dir, err := a.generatedImagesRoot()
				if err != nil {
					t.Fatal(err)
				}
				root.Close()
				args.ReferenceImage = filepath.Join(dir, filepath.Base(outside))
				if err := os.Symlink(outside, args.ReferenceImage); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			if result, err := cgImageGenerate(a, args); err == nil || result != nil {
				t.Fatal("unsafe image path accepted")
			}
			data, _ := os.ReadFile(outside)
			if !bytes.Equal(data, imageTestPNG(t)) {
				t.Fatal("outside file changed")
			}
		})
	}
}

func cgImagesAssertNoFiles(t *testing.T, a *app) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(a.dir, "generated-images"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("failed generation saved files")
	}
}

func TestChatGPTImagesCancellationClosesStream(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	a := cgImageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.created\"}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(cancelled)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := a.generateImage(imageTestRequest().WithContext(ctx), imageGenerationArguments{Prompt: "Draw a green square"}, "unused-kilo-secret", "unused-kilo-org", "image-local-secret")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("image stream never started")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled image request succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not release caller")
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream remained open after cancellation")
	}
	cgImagesAssertNoFiles(t, a)
	if a.active != 0 || a.imageGenerationActive != 0 || a.failures != 1 {
		t.Fatal("cancelled image request retained an activity/concurrency slot")
	}
}

func TestChatGPTImagesMCPWithoutKiloConnection(t *testing.T) {
	a := cgImageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, cgImageSSE(cgImageTerminal(cgImageItem(imageTestPNG(t)))))
	})
	a.apiKey, a.config.OrgID = "", ""
	body := imageMCPTestObject(t, imageMCPTestCall(a, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"generate_image","arguments":{"prompt":"Draw a green square"}}}`))
	result := body["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatalf("subscription MCP failed: %+v", result)
	}
	structured := result["structuredContent"].(map[string]any)
	if structured["provider"] != "chatgpt" || structured["billing"] != "subscription" || structured["costUSD"] != nil || structured["model"] != "chatgpt/gpt-5.5" {
		t.Fatal("MCP did not expose subscription billing without a made-up price")
	}
	content := result["content"].([]any)
	if len(content) != 2 || content[1].(map[string]any)["type"] != "image" {
		t.Fatal("MCP image preview missing")
	}
}

func TestChatGPTImagesFailuresSaveNothingAndPreserveUsage(t *testing.T) {
	for _, mode := range []string{"truncated", "invalid completed image", "unsupported content type", "incomplete response"} {
		t.Run(mode, func(t *testing.T) {
			data := imageTestPNG(t)
			a := cgImageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				switch mode {
				case "truncated":
					io.WriteString(w, cgImageSSE(cgImageDone(0, cgImageItem(data))))
				case "invalid completed image":
					io.WriteString(w, cgImageSSE(cgImageTerminal(cgImageItem([]byte("broken image")))))
				case "unsupported content type":
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"error":"private-response-payload"}`)
				case "incomplete response":
					terminal := cgImageTerminal(cgImageItem(data))
					terminal["type"] = "response.incomplete"
					terminal["response"].(map[string]any)["status"] = "incomplete"
					io.WriteString(w, cgImageSSE(terminal))
				}
			})
			result, err := cgImageGenerate(a, imageGenerationArguments{Prompt: "Draw a green square"})
			if err == nil || result != nil || strings.Contains(err.Error(), "private-response-payload") {
				t.Fatal("invalid image response was accepted or leaked upstream content")
			}
			cgImagesAssertNoFiles(t, a)
			if a.failures != 1 || a.active != 0 || a.activeTraces != 0 || a.usageTotal.Priced != 0 || a.usageTotal.SubscriptionRequests != 1 {
				t.Fatal("failed image request left invalid accounting or activity")
			}
			if mode == "invalid completed image" && (a.usageTotal.WithTokens != 1 || a.usageTotal.Input != 23 || a.usageTotal.Output != 41) {
				t.Fatal("completed generation consumption was lost after image validation failed")
			}
		})
	}
}

func TestChatGPTImagesPreflightRequiresCurrentConnectionAndSettings(t *testing.T) {
	for _, mode := range []string{"disabled", "logged out", "catalog error", "no GPT model", "settings changed"} {
		t.Run(mode, func(t *testing.T) {
			a := cgImageTestApp(t, func(http.ResponseWriter, *http.Request) { t.Error("failed preflight reached inference") })
			switch mode {
			case "disabled":
				a.config.ImageGeneration.Enabled = false
			case "logged out":
				if err := a.chatgpt.logout(); err != nil {
					t.Fatal(err)
				}
			default:
				catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if mode == "catalog error" {
						jsonResponse(w, 503, map[string]any{"error": "private-model-error"})
						return
					}
					slug := "o3"
					if mode == "settings changed" {
						slug = "gpt-5.5"
						a.mu.Lock()
						a.config.ImageGeneration.Enabled = false
						a.mu.Unlock()
					}
					jsonResponse(w, 200, map[string]any{"models": []any{map[string]any{"slug": slug}}})
				}))
				defer catalog.Close()
				a.chatgpt.backendURL = catalog.URL
			}
			result, err := cgImageGenerate(a, imageGenerationArguments{Prompt: "Draw a green square"})
			if err == nil || result != nil || strings.Contains(err.Error(), "private-model-error") {
				t.Fatal("failed preflight accepted or exposed upstream content")
			}
			cgImagesAssertNoFiles(t, a)
			if a.requests != 0 || a.active != 0 || a.imageGenerationActive != 0 {
				t.Fatal("failed preflight was billed or retained a concurrency slot")
			}
		})
	}
}

func TestChatGPTImagesRedirectDoesNotForwardCredentials(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		t.Error("image request followed redirect to another origin")
	}))
	defer target.Close()
	a := cgImageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	result, err := cgImageGenerate(a, imageGenerationArguments{Prompt: "Draw a green square"})
	if err == nil || result != nil || redirected.Load() != 0 {
		t.Fatal("redirect was not rejected")
	}
	cgImagesAssertNoFiles(t, a)
}

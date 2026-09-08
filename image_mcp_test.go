package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func imageMCPTestCall(a *app, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://127.0.0.1:8877/mcp/images", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer image-local-secret")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("MCP-Protocol-Version", imageMCPProtocol)
	w := httptest.NewRecorder()
	a.inferenceHandler("image-upstream-secret", "image-org", "image-local-secret", "127.0.0.1:8877").ServeHTTP(w, r)
	return w
}
func imageMCPTestObject(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("MCP HTTP %d: %s", w.Code, w.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["jsonrpc"] != "2.0" {
		t.Fatal("missing JSON-RPC version")
	}
	return result
}

func TestImageMCPLifecycleAndToolResult(t *testing.T) {
	a := imageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, imageTestResponse(imageTestPNG(t), map[string]any{"cost": 0.04}))
	})
	initialized := imageMCPTestObject(t, imageMCPTestCall(a, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"synthetic-codex","version":"1"}}}`))
	if initialized["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatal("supported version was not negotiated")
	}
	notification := imageMCPTestCall(a, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if notification.Code != 202 || notification.Body.Len() != 0 {
		t.Fatal("initialized notification received an RPC response")
	}
	listing := imageMCPTestObject(t, imageMCPTestCall(a, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	tools := listing["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "generate_image" {
		t.Fatal("image tool is not discoverable")
	}
	schema := tools[0].(map[string]any)["inputSchema"].(map[string]any)
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatal("tool schema is not a bounded object")
	}
	response := imageMCPTestObject(t, imageMCPTestCall(a, `{"jsonrpc":"2.0","id":"image-3","method":"tools/call","params":{"name":"generate_image","arguments":{"prompt":"A green square"}}}`))
	if response["id"] != "image-3" {
		t.Fatal("response ID changed")
	}
	result := response["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatal(result)
	}
	content := result["content"].([]any)
	if len(content) != 2 || content[1].(map[string]any)["type"] != "image" || content[1].(map[string]any)["mimeType"] != "image/png" {
		t.Fatal("MCP image content missing")
	}
	data, err := base64.StdEncoding.DecodeString(content[1].(map[string]any)["data"].(string))
	if err != nil || string(data) != string(imageTestPNG(t)) {
		t.Fatal("MCP result image mismatch")
	}
	structured := result["structuredContent"].(map[string]any)
	path := structured["images"].([]any)[0].(map[string]any)["path"].(string)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("MCP result points to a missing image")
	}
	if structured["costUSD"] != "0.040000000" {
		t.Fatal("MCP result omitted actual cost")
	}
	var text map[string]any
	if json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &text) != nil || text["model"] != "vendor/image-model" {
		t.Fatal("structured result lacks text compatibility content")
	}
	if strings.Contains(stringMustJSON(t, response), "image-upstream-secret") {
		t.Fatal("MCP exposed upstream credentials")
	}
}

func TestImageMCPLargeTransparentOriginalReturnsBoundedPreview(t *testing.T) {
	original := imagePreviewTestPNG(t, 1536, 1024)
	if len(original) <= 4_500_000 {
		t.Fatal("regression fixture must exceed the observed 4.5 MB request limit")
	}
	a := imageTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, imageTestResponse(original, map[string]any{"cost": 0.21976}))
	})
	w := imageMCPTestCall(a, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"generate_image","arguments":{"prompt":"A transparent synthetic texture"}}}`)
	t.Logf("original PNG: %d bytes; complete MCP response: %d bytes", len(original), w.Body.Len())
	if w.Body.Len() > base64.StdEncoding.EncodedLen(imagePreviewByteLimit)+8192 {
		t.Fatalf("MCP response still contains an oversized image: %d bytes", w.Body.Len())
	}
	response := imageMCPTestObject(t, w)
	result := response["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatal("a successful generation became a tool error")
	}
	content := result["content"].([]any)
	if len(content) != 2 {
		t.Fatal("expected one bounded preview and its metadata")
	}
	inline := content[1].(map[string]any)
	data, err := base64.StdEncoding.DecodeString(inline["data"].(string))
	if err != nil || len(data) > imagePreviewByteLimit || inline["mimeType"] != "image/png" {
		t.Fatal("MCP did not return a bounded PNG preview")
	}
	preview, format, err := image.Decode(bytes.NewReader(data))
	if err != nil || format != "png" || preview.Bounds().Dx() > imagePreviewSideLimit || preview.Bounds().Dy() > imagePreviewSideLimit {
		t.Fatal("MCP preview dimensions or format are invalid")
	}
	_, _, _, clearAlpha := preview.At(0, preview.Bounds().Dy()/2).RGBA()
	_, _, _, partialAlpha := preview.At(preview.Bounds().Dx()/2, preview.Bounds().Dy()/2).RGBA()
	if clearAlpha != 0 || partialAlpha == 0 || partialAlpha == 0xffff {
		t.Fatalf("preview flattened transparency: clear=%d partial=%d", clearAlpha, partialAlpha)
	}
	structured := result["structuredContent"].(map[string]any)
	metadata := structured["images"].([]any)[0].(map[string]any)
	path := metadata["path"].(string)
	saved, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(saved, original) || metadata["width"] != float64(1536) || metadata["height"] != float64(1024) || metadata["mimeType"] != "image/png" {
		t.Fatal("MCP preview replaced the original file or its full-resolution metadata")
	}
	inlineMetadata := metadata["preview"].(map[string]any)
	if inlineMetadata["resized"] != true || inlineMetadata["bytes"] != float64(len(data)) || inlineMetadata["width"] != float64(preview.Bounds().Dx()) || inlineMetadata["height"] != float64(preview.Bounds().Dy()) || inlineMetadata["mimeType"] != "image/png" {
		t.Fatal("preview metadata does not describe the returned image content")
	}
	message, _ := structured["message"].(string)
	if !strings.Contains(message, "previews") || !strings.Contains(message, "original full-resolution") {
		t.Fatal("MCP did not disclose preview resizing and full-resolution files")
	}
	var text map[string]any
	if json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &text) != nil || text["message"] != message {
		t.Fatal("text result omitted the preview disclosure")
	}
	root, dir, err := a.generatedImagesRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	reference, mime, err := readGeneratedReference(root, dir, path)
	if err != nil || mime != "image/png" || !bytes.Equal(reference, original) {
		t.Fatal("editing would use the preview instead of the original")
	}
	if a.requests != 1 || a.usageTotal.Priced != 1 || a.usageTotal.CostUSD != "0.219760000" || structured["costSource"] != "usage.cost" {
		t.Fatal("preview processing repeated generation or lost its observed cost")
	}
}

func TestImageMCPBusyPreviewDoesNotGenerate(t *testing.T) {
	for range cap(imageMCPWorkSlots) {
		imageMCPWorkSlots <- struct{}{}
	}
	defer func() {
		for range cap(imageMCPWorkSlots) {
			<-imageMCPWorkSlots
		}
	}()
	a := imageTestApp(t, func(http.ResponseWriter, *http.Request) {
		t.Error("busy preview gate must reject before another upstream generation")
	})
	response := imageMCPTestObject(t, imageMCPTestCall(a, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"generate_image","arguments":{"prompt":"Draw"}}}`))
	result := response["result"].(map[string]any)
	if result["isError"] != true || !strings.Contains(stringMustJSON(t, result), "previewed") || a.requests != 0 {
		t.Fatal("occupied generation/preview slots did not produce a safe busy response")
	}
}
func stringMustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestImageMCPProtocolValidationDoesNotGenerate(t *testing.T) {
	a := imageTestApp(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid protocol request generated an image") })
	for _, tc := range []struct {
		name, body   string
		status, code int
	}{
		{"parse", `{`, 400, -32700},
		{"batch", `[]`, 400, -32600},
		{"version", `{"jsonrpc":"1.0","id":1,"method":"ping"}`, 400, -32600},
		{"null ID", `{"jsonrpc":"2.0","id":null,"method":"ping"}`, 400, -32600},
		{"invalid ID", `{"jsonrpc":"2.0","id":true,"method":"ping"}`, 400, -32600},
		{"unknown method", `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, 200, -32601},
		{"initialize params", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, 200, -32602},
		{"unknown tool", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"shell","arguments":{}}}`, 200, -32602},
		{"extra argument", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"generate_image","arguments":{"prompt":"Draw","url":"https://example.test"}}}`, 200, -32602},
		{"blank prompt", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"generate_image","arguments":{"prompt":" "}}}`, 200, -32602},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := imageMCPTestCall(a, tc.body)
			if w.Code != tc.status {
				t.Fatalf("HTTP %d != %d: %s", w.Code, tc.status, w.Body.String())
			}
			var response struct{ Error struct{ Code int } }
			if json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Error.Code != tc.code {
				t.Fatalf("wrong RPC error: %s", w.Body.String())
			}
		})
	}
	a.config.ImageGeneration.Enabled = false
	failed := imageMCPTestObject(t, imageMCPTestCall(a, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"generate_image","arguments":{"prompt":"Draw"}}}`))
	if failed["result"].(map[string]any)["isError"] != true {
		t.Fatal("disabled tool did not return a recoverable tool error")
	}
}

func TestImageMCPTransportAndAuth(t *testing.T) {
	a := imageTestApp(t, func(http.ResponseWriter, *http.Request) { t.Error("transport checks invoked generation") })
	for _, tc := range []struct {
		name, method, header, value string
		want                        int
	}{
		{"GET", "GET", "", "", 405},
		{"DELETE", "DELETE", "", "", 405},
		{"origin", "POST", "Origin", "https://example.test", 403},
		{"auth", "POST", "Authorization", "Bearer incorrect", 401},
		{"media", "POST", "Content-Type", "text/plain", 415},
		{"accept", "POST", "Accept", "text/event-stream", 406},
		{"protocol", "POST", "MCP-Protocol-Version", "2099-01-01", 400},
		{"host", "POST", "Host", "example.test", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://127.0.0.1:8877/mcp/images", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
			r.Header.Set("Authorization", "Bearer image-local-secret")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json, text/event-stream")
			if tc.header == "Host" {
				r.Host = tc.value
			} else if tc.header != "" {
				r.Header.Set(tc.header, tc.value)
			}
			w := httptest.NewRecorder()
			a.inferenceHandler("image-upstream-secret", "image-org", "image-local-secret", "127.0.0.1:8877").ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("HTTP %d != %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	if w := imageMCPTestCall(a, strings.Repeat("x", imageMCPRequestLimit+1)); w.Code != 413 {
		t.Fatalf("oversized MCP input was not rejected: %d", w.Code)
	}
	w := imageMCPTestCall(a, `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"generate_image","arguments":{"prompt":"must not execute a notification"}}}`)
	if w.Code != 202 || w.Body.Len() != 0 {
		t.Fatal("notification created an RPC response")
	}
}

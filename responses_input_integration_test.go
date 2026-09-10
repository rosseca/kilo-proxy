package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const delegatedInputOutput = "<codex_delegation>\n<source_thread_id>synthetic-source-task</source_thread_id>\n<input>Inspect the synthetic fixture and report the result.</input>\n</codex_delegation>"

func delegatedIntegrationRequest(t *testing.T, model, name, callIDForm string, stream bool) string {
	t.Helper()
	delegation := map[string]any{"type": "function_call_output", "id": "fco_delegated", "name": name, "namespace": "codex_app", "output": delegatedInputOutput}
	switch callIDForm {
	case "null":
		delegation["call_id"] = nil
	case "empty":
		delegation["call_id"] = ""
	}
	doc := map[string]any{
		"model": model, "stream": stream,
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "Synthetic initial request"},
			map[string]any{"type": "message", "role": "developer", "content": "Preserve the original roles."},
			delegation,
			map[string]any{"type": "message", "role": "user", "content": "Synthetic follow-up request"},
			map[string]any{"type": "function_call", "id": "fc_existing", "call_id": "existing-call", "name": "read_fixture", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "id": "fco_existing", "call_id": "existing-call", "output": "Existing result must remain unchanged."},
		},
		"reasoning": map[string]any{"effort": "high"},
		"metadata":  map[string]any{"exact": json.Number("9007199254740993")},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// This stub checks the provider boundary instead of calling the adapter directly:
// an orphan output with no preceding call must still fail if normalization is lost.
func validateDelegatedGatewayInput(doc map[string]any) error {
	input, ok := doc["input"].([]any)
	if !ok {
		return fmt.Errorf("input must be an array")
	}
	calls := map[string]bool{}
	for index, value := range input {
		item := object(value)
		switch stringValue(item["type"]) {
		case "function_call":
			id := stringValue(item["call_id"])
			if id == "" || calls[id] {
				return fmt.Errorf("input.%d.call_id: missing or duplicate call", index)
			}
			calls[id] = true
		case "function_call_output":
			id := stringValue(item["call_id"])
			if id == "" || !calls[id] {
				return fmt.Errorf("input.%d.type: Invalid input", index)
			}
		}
	}
	return nil
}

func TestResponsesDelegationEndToEnd(t *testing.T) {
	for index, model := range []string{"z-ai/glm-5", "openai/gpt-test", "anthropic/claude-test"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", model, stream), func(t *testing.T) {
				name := "create_thread"
				if stream {
					name = "send_message_to_thread"
					if index == 2 {
						name = "handoff_thread"
					}
				}
				body := delegatedIntegrationRequest(t, model, name, []string{"missing", "null", "empty"}[index], stream)
				original, _ := decodeObject([]byte(body))
				if validateDelegatedGatewayInput(original) == nil {
					t.Fatal("fixture must reproduce rejection before adaptation")
				}
				payload := `{"id":"resp_delegated","output":[],"debug":"upstream-secret"}`
				contentType := "application/json"
				if stream {
					contentType = "text/event-stream"
					payload = ": keepalive\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Synthetic response\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_delegated\",\"output\":[],\"debug\":\"upstream-secret\"}}\n\ndata: [DONE]\n\n"
				}
				a := testApp(t)
				var received map[string]any
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						http.Error(w, "read failure", 400)
						return
					}
					received, err = decodeObject(raw)
					if err == nil {
						err = validateDelegatedGatewayInput(received)
					}
					if err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
					if r.ContentLength != int64(len(raw)) {
						t.Errorf("upstream content length = %d, want %d", r.ContentLength, len(raw))
					}
					if r.URL.Path != "/api/gateway/responses" || r.Header.Get("Authorization") != "Bearer upstream-secret" || r.Header.Get("X-KiloCode-OrganizationId") != "team-id" {
						t.Error("gateway route or authentication changed")
					}
					if r.Header.Get("Cookie") != "" || r.Header.Get("X-Api-Key") != "" {
						t.Error("client credentials reached gateway")
					}
					w.Header().Set("Content-Type", contentType)
					w.Header().Set("Set-Cookie", "private-cookie")
					io.WriteString(w, payload)
				}))
				defer upstream.Close()
				setUpstream(a, upstream.URL)
				response := activityRequest(a, body)
				if response.Code != http.StatusOK || response.Body.String() != payload {
					t.Fatalf("response = %d %s", response.Code, response.Body.String())
				}
				before := original["input"].([]any)
				after := received["input"].([]any)
				if len(after) != len(before)+1 {
					t.Fatalf("input count = %d, want %d", len(after), len(before)+1)
				}
				call, output := object(after[2]), object(after[3])
				if call["type"] != "function_call" || call["namespace"] != "codex_app" || call["name"] != name || call["arguments"] != "{}" || call["call_id"] != output["call_id"] {
					t.Fatalf("delegation was not paired correctly: %#v", call)
				}
				wantOutput := map[string]any{"type": "function_call_output", "id": "fco_delegated", "output": delegatedInputOutput, "call_id": call["call_id"]}
				if !reflect.DeepEqual(output, wantOutput) {
					t.Fatalf("delegation content or provenance changed: %#v", output)
				}
				if !reflect.DeepEqual(before[:2], after[:2]) || !reflect.DeepEqual(before[3:], after[4:]) {
					t.Fatal("surrounding messages or an existing tool pair changed")
				}
				for _, key := range []string{"model", "stream", "reasoning", "metadata"} {
					if !reflect.DeepEqual(original[key], received[key]) {
						t.Errorf("%s changed", key)
					}
				}
				if len(a.events) != 1 || a.events[0].Status != 200 || !a.events[0].HasDetails {
					t.Fatal("missing successful activity entry")
				}
				trace := a.traces[a.events[0].ID]
				if trace.Request.Body != body {
					t.Fatal("original activity request must retain the unadapted delegation")
				}
				traced, err := decodeObject([]byte(trace.UpstreamRequest.Body))
				if err != nil || !reflect.DeepEqual(traced, received) {
					t.Fatal("upstream activity request must show the adapted tool pair")
				}
				wantCapturedResponse := strings.ReplaceAll(payload, "upstream-secret", "[REDACTED]")
				if trace.UpstreamResponse.Body != wantCapturedResponse || trace.Response.Body != wantCapturedResponse {
					t.Fatal("response activity missing, changed, or not redacted")
				}
				if trace.Request.Headers.Get("Authorization") != "[REDACTED]" || trace.UpstreamRequest.Headers.Get("Authorization") != "[REDACTED]" || trace.UpstreamResponse.Headers.Get("Set-Cookie") != "[REDACTED]" || trace.Response.Headers.Get("Set-Cookie") != "" {
					t.Fatal("activity credential redaction or response header filtering changed")
				}
				encoded, _ := json.Marshal(trace)
				for _, secret := range []string{"local-secret", "upstream-secret", "private-cookie", "another-secret"} {
					if strings.Contains(string(encoded), secret) {
						t.Errorf("trace leaked %s", secret)
					}
				}
			})
		}
	}
}

func TestResponsesDelegationComposesWithAnthropicSchemaBridge(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			doc, _ := decodeObject([]byte(unionRequest))
			delegated, _ := decodeObject([]byte(delegatedIntegrationRequest(t, "anthropic/claude-test", "create_thread", "missing", stream)))
			doc["input"] = append(delegated["input"].([]any), doc["input"].([]any)...)
			doc["stream"] = stream
			body, _ := json.Marshal(doc)
			payload := `{"id":"resp_union","output":[{"type":"function_call","id":"fc_choice","call_id":"call_choice","name":"choice","arguments":"{\"kilo_tool_input\":{\"mode\":\"read\"}}"}]}`
			contentType := "application/json"
			if stream {
				contentType = "text/event-stream"
				payload = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + payload + "}\n\ndata: [DONE]\n\n"
			}
			a := testApp(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				received, err := decodeObject(raw)
				if err == nil {
					err = validateDelegatedGatewayInput(received)
				}
				if err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				schema := object(object(received["tools"].([]any)[0])["parameters"])
				if needsEnvelope(schema) || object(schema["properties"])[toolEnvelope] == nil {
					http.Error(w, "top-level union was not adapted", 400)
					return
				}
				input := received["input"].([]any)
				args, err := unwrapArguments(object(input[len(input)-2])["arguments"])
				if err != nil || args != `{"mode":"read"}` || object(input[2])["arguments"] != "{}" {
					http.Error(w, "tool history was adapted incorrectly", 400)
					return
				}
				w.Header().Set("Content-Type", contentType)
				io.WriteString(w, payload)
			}))
			defer upstream.Close()
			setUpstream(a, upstream.URL)
			response := activityRequest(a, string(body))
			if response.Code != 200 || strings.Contains(response.Body.String(), toolEnvelope) || !strings.Contains(response.Body.String(), `\"mode\":\"read\"`) {
				t.Fatalf("schema bridge response = %d %s", response.Code, response.Body.String())
			}
			trace := a.traces[a.events[0].ID]
			if trace.Request.Body != string(body) || !strings.Contains(trace.UpstreamRequest.Body, toolEnvelope) || !strings.Contains(trace.UpstreamResponse.Body, toolEnvelope) || trace.Response.Body != response.Body.String() {
				t.Fatal("activity did not preserve both adaptation stages")
			}
		})
	}
}

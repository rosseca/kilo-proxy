package main

import (
	"fmt"
	"strings"
	"testing"
)

// Exercise the real HTTP handler and namespace/JSON/SSE composition. The
// upstream is a loopback contract fixture, not a paid Gemini inference call.
func TestGeminiSchemaHTTPToolsHistoryAndStreaming(t *testing.T) {
	for _, ns := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, model := range []string{"google/gemini-4-argon", "~google/gemini-4-argon"} {
				t.Run(fmt.Sprintf("namespace=%t/stream=%t/%s", ns, stream, model), func(t *testing.T) {
					schema := map[string]any{"type": "object", "properties": map[string]any{
						"body": map[string]any{"type": []any{"string", "array"}, "items": map[string]any{"type": "string", "minLength": 1}, "minItems": 1},
					}, "required": []any{"body"}, "additionalProperties": false}
					tool := map[string]any{"type": "function", "name": "submit", "description": "Synthetic HTTP body", "parameters": schema}
					tools := []any{tool}
					call := map[string]any{"type": "function_call", "call_id": "prior", "name": "submit", "arguments": "{\"body\":[\"first\",\"second\"]}"}
					if ns {
						tools = []any{map[string]any{"type": "namespace", "name": "fixture", "tools": tools}}
						call["namespace"] = "fixture"
					}
					resultItem := map[string]any{"type": "function_call_output", "call_id": "prior", "output": "unchanged tool result"}
					request := map[string]any{"model": model, "stream": stream, "tools": tools, "input": []any{call, resultItem}, "reasoning": map[string]any{"effort": "high"}, "metadata": map[string]any{"number": "9007199254740993"}}
					args := "{\"body\":[\"first\",\"second\"]}"
					result := ngProxy(t, "/v1/responses", ngJSON(t, request), func(wire map[string]any) ([]byte, string) {
						ngEqual(t, "selected model", wire["model"], model)
						ngEqual(t, "reasoning", wire["reasoning"], request["reasoning"])
						ngEqual(t, "metadata", wire["metadata"], request["metadata"])
						declarations := wire["tools"].([]any)
						if len(declarations) != 1 {
							t.Fatalf("tool cardinality=%d", len(declarations))
						}
						decl := object(declarations[0])
						if decl["type"] != "function" {
							t.Fatal("client function lost")
						}
						body := object(object(object(decl["parameters"])["properties"])["body"])
						if _, stillUnion := body["type"].([]any); stillUnion {
							t.Fatal("Google SDK's lossy type-union input reached upstream")
						}
						branches, ok := body["anyOf"].([]any)
						if !ok || len(branches) != 2 {
							t.Fatalf("explicit typed alternatives absent: %#v", body)
						}
						arrayCount := 0
						for _, v := range branches {
							branch := object(v)
							if branch["type"] == "array" {
								arrayCount++
								ngEqual(t, "array item restriction", branch["items"], map[string]any{"type": "string", "minLength": 1})
								ngEqual(t, "array size restriction", branch["minItems"], 1)
							}
						}
						if arrayCount != 1 {
							t.Fatal("array branch lost")
						}
						if _, wrongPlace := body["items"]; wrongPlace {
							t.Fatal("items left on untyped union")
						}
						history := wire["input"].([]any)
						ngEqual(t, "historical raw arguments", object(history[0])["arguments"], args)
						ngEqual(t, "historical tool result", history[1], resultItem)
						ngEqual(t, "historical tool alias", object(history[0])["name"], decl["name"])
						output := map[string]any{"type": "function_call", "id": "fc_body", "call_id": "next", "name": decl["name"], "arguments": args}
						response := map[string]any{"id": "resp_gemini_fixture", "status": "completed", "output": []any{output}}
						if stream {
							return ngSSE(t, response), "text/event-stream"
						}
						return ngJSON(t, response), "application/json"
					})
					if result.status != 200 || result.reached != 1 {
						t.Fatalf("status=%d reached=%d body=%s", result.status, result.reached, result.body)
					}
					var client map[string]any
					if stream {
						client, _, _, _ = ngReadSSE(t, result.body)
					} else {
						client = ngDoc(t, result.body)
					}
					out := object(client["output"].([]any)[0])
					ngEqual(t, "client tool name", out["name"], "submit")
					ngEqual(t, "client raw arguments", out["arguments"], args)
					if ns {
						ngEqual(t, "client namespace", out["namespace"], "fixture")
					} else if out["namespace"] != nil {
						t.Fatal("invented namespace")
					}
				})
			}
		}
	}
}

func TestGeminiSchemaHTTPUnchangedOtherProviders(t *testing.T) {
	for _, model := range []string{"openai/test-model", "anthropic/test-model", "google/gemma-test", "vendor/gemini-test"} {
		t.Run(model, func(t *testing.T) {
			request := map[string]any{"model": model, "input": "synthetic", "tools": []any{map[string]any{"type": "function", "name": "plain", "parameters": map[string]any{"type": "object", "properties": map[string]any{"body": map[string]any{"type": []any{"string", "array"}, "items": map[string]any{"type": "string"}}}}}}}
			response := []byte("{\"id\":\"preserved\",\"output\":[],\"sentinel\":\"ñ\"}")
			got := ngProxy(t, "/v1/responses", ngJSON(t, request), func(wire map[string]any) ([]byte, string) {
				ngEqual(t, "foreign provider request", wire, request)
				return response, "application/json"
			})
			if got.status != 200 || !strings.Contains(string(got.body), "preserved") {
				t.Fatalf("status=%d body=%s", got.status, got.body)
			}
		})
	}
}

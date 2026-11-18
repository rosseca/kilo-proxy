package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestNamespaceGatewayContractDynamicToolSchemas(t *testing.T) {
	const sourceArgs = `{"body":["example"]}`
	const wrappedArgs = `{"kilo_tool_input":{"body":["example"]}}`
	for _, tc := range []struct {
		model    string
		schema   string
		wireArgs string
	}{
		{
			model:    "google/gemini-4-argon",
			schema:   `{"type":"object","properties":{"body":{"type":["string","array"],"items":{"type":"string"}}},"required":["body"]}`,
			wireArgs: sourceArgs,
		},
		{
			model:    "anthropic/test-model",
			schema:   `{"oneOf":[{"type":"object","properties":{"body":{"type":"array","items":{"type":"string"}}},"required":["body"]},{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}]}`,
			wireArgs: wrappedArgs,
		},
	} {
		for _, declared := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/declared=%t/stream=%t", tc.model, declared, stream), func(t *testing.T) {
					tool := namespaceTestFunction("lookup")
					tool["parameters"] = ngDoc(t, []byte(tc.schema))
					group := namespaceTestGroup("mcp__fixture", tool)
					search := dynamicTestSearchTool()
					tools := []any{search}
					if declared {
						tools = append(tools, group)
					}
					req := map[string]any{"model": tc.model, "stream": stream, "tools": tools, "input": []any{
						map[string]any{"type": "tool_search_call", "call_id": "ts_1", "status": "completed", "execution": "client", "arguments": map[string]any{"query": "lookup"}},
						dynamicTestOutput("ts_1", group),
						map[string]any{"type": "function_call", "name": "lookup", "namespace": "mcp__fixture", "call_id": "previous_call", "arguments": sourceArgs},
					}}
					result := ngProxy(t, "/v1/responses", ngJSON(t, req), func(wire map[string]any) ([]byte, string) {
						wireTools, _ := wire["tools"].([]any)
						if len(wireTools) != 2 {
							t.Errorf("unexpected upstream tools: %s", ngJSON(t, wireTools))
							return nil, "application/json"
						}
						alias := stringValue(object(wireTools[1])["name"])
						response := map[string]any{"id": "resp_schema", "status": "completed", "tools": wireTools, "output": []any{
							map[string]any{"type": "function_call", "id": "fc_schema", "call_id": "call_schema", "name": alias, "arguments": tc.wireArgs},
						}}
						if stream {
							return ngSSE(t, response), "text/event-stream"
						}
						return ngJSON(t, response), "application/json"
					})
					if result.status != 200 || result.reached != 1 {
						t.Fatalf("status=%d reached=%d body=%s", result.status, result.reached, result.body)
					}
					wireTools := result.upstream["tools"].([]any)
					top := object(wireTools[1])
					alias := stringValue(top["name"])
					input := result.upstream["input"].([]any)
					discovered := object(object(input[1])["tools"].([]any)[0])
					if !strings.HasPrefix(alias, "kilo_ns_") || discovered["name"] != alias {
						t.Fatalf("tool identity diverged: top=%v discovered=%v", top["name"], discovered["name"])
					}
					params := object(top["parameters"])
					ngEqual(t, "both wire schemas", discovered["parameters"], params)
					if tc.model == "google/gemini-4-argon" {
						body := object(object(params["properties"])["body"])
						if body["items"] != nil || body["type"] != nil {
							t.Fatalf("discovered Gemini schema retained invalid union shape: %#v", body)
						}
						array := gsBranch(t, body, "array")
						ngEqual(t, "Gemini array items", array["items"], map[string]any{"type": "string"})
					} else {
						ngEqual(t, "Anthropic envelope", object(params["properties"])[toolEnvelope], tool["parameters"])
					}
					history := object(input[2])
					ngEqual(t, "history identity and arguments", history, map[string]any{"type": "function_call", "name": alias, "call_id": "previous_call", "arguments": tc.wireArgs})

					var client map[string]any
					if stream {
						var delta map[string]string
						client, _, delta, _ = ngReadSSE(t, result.body)
						ngEqual(t, "restored arguments in delta", delta["fc_schema"], sourceArgs)
					} else {
						client = ngDoc(t, result.body)
					}
					call := object(client["output"].([]any)[0])
					ngEqual(t, "client function identity", call["name"], "lookup")
					ngEqual(t, "client namespace", call["namespace"], "mcp__fixture")
					ngEqual(t, "restored arguments", call["arguments"], sourceArgs)
				})
			}
		}
	}
}

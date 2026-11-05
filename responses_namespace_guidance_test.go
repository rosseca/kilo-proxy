package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestResponsesNamespaceSharedGuidanceOverHTTP(t *testing.T) {
	const toolCount = 402
	const marker = "SHARED_GUIDANCE_FIXTURE."
	guidance := marker + " " + strings.Repeat("Use a precise path, return structured results, respect requested access, and keep related operations in the same workspace. ", 24)
	tools := make([]any, toolCount)
	for i := range tools {
		name := fmt.Sprintf("task_%03d", i)
		tools[i] = namespaceTestFunction(name)
		object(tools[i])["description"] = "Individual description for " + name + "."
	}
	group := namespaceTestGroup("fixture", tools...)
	group["description"] = guidance
	usage := map[string]any{
		"input_tokens": 281581, "input_tokens_details": map[string]any{"cached_tokens": 273744},
		"output_tokens": 37, "total_tokens": 281618,
	}

	for _, model := range []string{"openai/test-model", "anthropic/test-model"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", model, stream), func(t *testing.T) {
				req := namespaceTestDoc(group)
				req["model"] = model
				req["stream"] = stream
				req["input"] = "Short synthetic history after compaction."
				req["instructions"] = "Original instructions."
				raw := ngJSON(t, req)
				result := ngProxy(t, "/v1/responses", raw, func(wire map[string]any) ([]byte, string) {
					declared := wire["tools"].([]any)
					call := map[string]any{
						"type": "function_call", "id": "fc_fixture", "call_id": "call_fixture",
						"name": object(declared[0])["name"], "arguments": "{\"marker\":\"ok\"}",
					}
					response := map[string]any{
						"id": "resp_guidance_fixture", "status": "completed",
						"usage": usage, "output": []any{call},
					}
					if stream {
						return ngSSE(t, response), "text/event-stream"
					}
					return ngJSON(t, response), "application/json"
				})
				if result.status != 200 || result.reached != 1 {
					t.Fatalf("status=%d reached=%d", result.status, result.reached)
				}
				wire := result.upstream
				declared := wire["tools"].([]any)
				if len(declared) != toolCount {
					t.Fatalf("tools=%d, want %d", len(declared), toolCount)
				}
				wireJSON := ngJSON(t, wire)
				copies := strings.Count(string(wireJSON), marker)
				t.Logf("COMPACTION_FIXTURE client_json_bytes=%d upstream_compact_json_bytes=%d guidance_bytes=%d guidance_copies=%d tools=%d",
					len(raw), len(wireJSON), len(guidance), copies, toolCount)
				if copies != 1 {
					t.Errorf("shared guidance copies=%d, want 1", copies)
				}
				if len(wireJSON) >= 2*len(raw) {
					t.Errorf("tool declaration overhead exceeded input size: client=%d upstream=%d", len(raw), len(wireJSON))
				}
				ngEqual(t, "instructions", wire["instructions"], req["instructions"])
				ngEqual(t, "short history", wire["input"], req["input"])
				aliases := make(map[string]bool, toolCount)
				for i, item := range declared {
					wireTool, original := object(item), object(tools[i])
					description := stringValue(wireTool["description"])
					name := stringValue(original["name"])
					if !strings.Contains(description, "fixture."+name) || !strings.Contains(description, stringValue(original["description"])) {
						t.Fatalf("lost qualified name or individual description for %s", name)
					}
					alias := stringValue(wireTool["name"])
					if wireTool["type"] != "function" || alias == "" || alias == name || aliases[alias] {
						t.Fatalf("invalid flat tool at index %d", i)
					}
					aliases[alias] = true
					ngEqual(t, "tool schema", wireTool["parameters"], original["parameters"])
				}
				var client map[string]any
				if stream {
					client, _, _, _ = ngReadSSE(t, result.body)
				} else {
					client = ngDoc(t, result.body)
				}
				ngEqual(t, "upstream usage preserved", client["usage"], usage)
				call := object(client["output"].([]any)[0])
				ngEqual(t, "client tool name restored", call["name"], "task_000")
				ngEqual(t, "client namespace restored", call["namespace"], "fixture")
			})
		}
	}
}

func TestResponsesNamespaceSharedGuidanceFollowsExplicitChoice(t *testing.T) {
	const marker = "ONE_SHARED_RULE."
	selected := map[string]any{"type": "custom", "namespace": "fixture", "name": "paint"}
	for _, tc := range []struct {
		name    string
		choice  any
		carrier int
	}{
		{name: "auto", choice: "auto", carrier: 0},
		{name: "forced", choice: selected, carrier: 1},
		{name: "allowed", choice: map[string]any{"type": "allowed_tools", "mode": "required", "tools": []any{selected}}, carrier: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := namespaceTestGroup("fixture", namespaceTestFunction("read"), map[string]any{
				"type": "custom", "name": "paint", "description": "Individual paint instructions.", "format": map[string]any{"type": "text"},
			})
			group["description"] = marker
			req := namespaceTestDoc(group)
			req["tool_choice"] = tc.choice
			_, wire := namespaceTestPrepare(t, req)
			for i := range 2 {
				description := stringValue(namespaceTestTool(t, wire, i)["description"])
				if strings.Contains(description, marker) != (i == tc.carrier) {
					t.Errorf("shared guidance appears on the wrong tool at index %d", i)
				}
			}
		})
	}
}

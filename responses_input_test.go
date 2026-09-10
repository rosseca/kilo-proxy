package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const delegationOutput = "<codex_delegation>\n  <source_thread_id>source-test</source_thread_id>\n  <input>Inspect the sample &amp; report.\nDo not lose this line.</input>\n</codex_delegation>"

func delegationItem() map[string]any {
	return map[string]any{"type": "function_call_output", "id": "fco_test", "name": "create_thread", "namespace": "codex_app", "output": delegationOutput}
}

func adaptedInput(t *testing.T, doc map[string]any) ([]byte, map[string]any) {
	t.Helper()
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
	r.Header.Set("Content-Length", "stale")
	if err := prepareResponsesInput(r); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if r.ContentLength != int64(len(got)) {
		t.Fatal("incorrect content length")
	}
	result, err := decodeObject(got)
	if err != nil {
		t.Fatal(err)
	}
	return got, result
}

func TestResponsesInputDelegationPreservesHistory(t *testing.T) {
	for _, name := range []string{"create_thread", "send_message_to_thread", "handoff_thread"} {
		t.Run(name, func(t *testing.T) {
			for _, callID := range []any{nil, ""} {
				item := delegationItem()
				item["name"], item["call_id"] = name, callID
				before := map[string]any{"type": "message", "role": "developer", "content": "Policy"}
				after := map[string]any{"type": "message", "role": "user", "content": "Continue"}
				doc := map[string]any{"model": "z-ai/glm-5.3-flash", "input": []any{before, item, after}, "metadata": map[string]any{"exact": json.Number("9007199254740993")}, "reasoning": map[string]any{"effort": "high"}}
				raw, result := adaptedInput(t, doc)
				input := result["input"].([]any)
				if len(input) != 4 || !reflect.DeepEqual(input[0], before) || !reflect.DeepEqual(input[3], after) {
					t.Fatal("surrounding messages changed")
				}
				call, output := object(input[1]), object(input[2])
				if call["type"] != "function_call" || call["name"] != name || call["namespace"] != "codex_app" || call["arguments"] != "{}" || stringValue(call["call_id"]) == "" {
					t.Fatal("invalid compatibility call", call)
				}
				want := map[string]any{"type": "function_call_output", "id": "fco_test", "call_id": call["call_id"], "output": delegationOutput}
				if !reflect.DeepEqual(output, want) {
					t.Fatal("output or provenance changed", output)
				}
				for _, key := range []string{"model", "metadata", "reasoning"} {
					if !reflect.DeepEqual(doc[key], result[key]) {
						t.Fatal("request option changed", key)
					}
				}
				// Retrying an original request and reprocessing an adapted one are stable.
				again, _ := adaptedInput(t, doc)
				idempotent, _ := adaptedInput(t, result)
				if !bytes.Equal(raw, again) || !bytes.Equal(raw, idempotent) {
					t.Fatal("unstable adaptation")
				}
			}
		})
	}
}

func TestResponsesInputLeavesUnrelatedPayloadsByteExact(t *testing.T) {
	for _, change := range []struct {
		name, key string
		value     any
	}{
		{"paired", "call_id", "existing"}, {"invalid-call-id", "call_id", 123},
		{"another-namespace", "namespace", "other_app"}, {"other-tool", "name", "get_usage_limits"},
		{"other-type", "type", "custom_tool_call_output"}, {"no-envelope", "output", "ordinary tool output"},
		{"unfinished-envelope", "output", "<codex_delegation>unfinished"}, {"future-content", "output", []any{map[string]any{"type": "input_text", "text": delegationOutput}}},
	} {
		t.Run(change.name, func(t *testing.T) {
			item := delegationItem()
			item[change.key] = change.value
			raw, _ := json.Marshal(item)
			body := "{ \"input\" : [" + string(raw) + "], \"model\":\"openai/test\" }\n"
			r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
			if err := prepareResponsesInput(r); err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(r.Body)
			if string(got) != body {
				t.Fatal("unrelated request changed")
			}
		})
	}
	for _, body := range []string{`{"input":"hello"}`, `{"input":null}`, `{"input":[null,42,"text"]}`, `{"input":`, `null`, `{} {}`, `[]`} {
		r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
		if err := prepareResponsesInput(r); err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(r.Body)
		if string(got) != body {
			t.Fatal("opaque payload changed", body)
		}
	}
}

func TestResponsesInputStableUniqueCallIDs(t *testing.T) {
	for _, includeID := range []bool{true, false} {
		item := delegationItem()
		if !includeID {
			delete(item, "id")
		}
		_, single := adaptedInput(t, map[string]any{"input": []any{item}})
		base := object(single["input"].([]any)[0])["call_id"]
		prefix := map[string]any{"type": "message", "role": "user", "content": "Earlier message"}
		_, shifted := adaptedInput(t, map[string]any{"input": []any{prefix, item}})
		if object(shifted["input"].([]any)[1])["call_id"] != base {
			t.Fatal("ID depends on history position")
		}
		priorCall := map[string]any{"type": "function_call", "call_id": base, "name": "existing", "arguments": "{}"}
		priorOutput := map[string]any{"type": "function_call_output", "call_id": base, "output": "Already paired"}
		_, result := adaptedInput(t, map[string]any{"input": []any{priorCall, priorOutput, item, item}})
		input := result["input"].([]any)
		if len(input) != 6 || !reflect.DeepEqual(input[0], priorCall) || !reflect.DeepEqual(input[1], priorOutput) {
			t.Fatal("existing pair changed")
		}
		seen := map[string]bool{base.(string): true}
		for _, i := range []int{2, 4} {
			id := stringValue(object(input[i])["call_id"])
			if seen[id] || id == "" || object(input[i+1])["call_id"] != id {
				t.Fatal("call ID collision", id)
			}
			seen[id] = true
		}
	}
}

func TestResponsesInputSkipsOtherRoutesAndEncoding(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"input": []any{delegationItem()}})
	for _, tc := range []struct{ method, path, encoding string }{
		{"GET", "/v1/responses", ""}, {"POST", "/v1/messages", ""}, {"POST", "/v1/chat/completions", ""}, {"POST", "/v1/responses", "gzip"},
	} {
		r := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(body))
		r.Header.Set("Content-Encoding", tc.encoding)
		if err := prepareResponsesInput(r); err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(r.Body)
		if !bytes.Equal(got, body) {
			t.Fatal("unrelated route or encoding changed")
		}
	}
}

func TestResponsesInputUploadAndExpansionLimits(t *testing.T) {
	item := delegationItem()
	body, _ := json.Marshal(map[string]any{"input": []any{item}})
	r := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
	r.ContentLength = -1
	r.Body = http.MaxBytesReader(httptest.NewRecorder(), r.Body, 64)
	var oversized *http.MaxBytesError
	if err := prepareResponsesInput(r); !errors.As(err, &oversized) {
		t.Fatal("chunked upload limit lost", err)
	}
	// The original fits, but inserting a call would cross the existing 32 MiB cap.
	padding := strings.Repeat("x", bridgeLimit-len(body)-1)
	item["output"] = strings.Replace(delegationOutput, "Inspect", padding+"Inspect", 1)
	body, _ = json.Marshal(map[string]any{"input": []any{item}})
	if len(body) >= bridgeLimit {
		t.Fatal("invalid boundary fixture")
	}
	r = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
	if err := prepareResponsesInput(r); !errors.As(err, &oversized) {
		t.Fatal("adaptation overflow accepted", err)
	}
}

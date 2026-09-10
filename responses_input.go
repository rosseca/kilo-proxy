package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Codex Desktop sends cross-task notifications as named tool outputs without a
// call_id (TurnToolOutput), rather than results of calls in the receiving task.
// Kilo's Responses gateway requires a call/output pair. Add that pair only to
// upstream history: no tool is executed, and the notification retains tool
// authority and its original delegation envelope instead of becoming user text.
func prepareResponsesInput(r *http.Request) error {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || r.Header.Get("Content-Encoding") != "" {
		return nil
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(data))
	doc, err := decodeObject(data)
	if err != nil {
		return nil // Keep invalid JSON unchanged for the gateway's validation.
	}
	input, ok := doc["input"].([]any)
	if !ok {
		return nil
	}
	used := make(map[string]bool)
	for _, entry := range input {
		if id := stringValue(object(entry)["call_id"]); id != "" {
			used[id] = true
		}
	}
	var adapted []any
	for i, entry := range input {
		item := object(entry)
		if !isCodexDelegationOutput(item) {
			if adapted != nil {
				adapted = append(adapted, entry)
			}
			continue
		}
		if adapted == nil {
			adapted = append(make([]any, 0, len(input)+1), input[:i]...)
		}
		// Stable across retries and appended history, without exposing source IDs
		// in the synthetic ID. Also avoid collisions with calls already present.
		identity := stringValue(item["id"])
		if identity == "" {
			raw, _ := json.Marshal(item) // Already decoded from valid JSON.
			identity = string(raw)
		}
		digest := sha256.Sum256([]byte(toolKey(stringValue(item["namespace"]), stringValue(item["name"])) + "\x00" + identity))
		base := fmt.Sprintf("call_kilo_%x", digest[:12])
		callID := base
		for suffix := 1; used[callID]; suffix++ {
			callID = fmt.Sprintf("%s_%d", base, suffix)
		}
		used[callID] = true
		call := map[string]any{
			"type": "function_call", "call_id": callID,
			"name": item["name"], "namespace": item["namespace"], "arguments": "{}",
		}
		item["call_id"] = callID
		delete(item, "name")
		delete(item, "namespace")
		adapted = append(adapted, call, item)
	}
	if adapted == nil {
		return nil // Unrelated requests retain their exact bytes.
	}
	doc["input"] = adapted
	data, err = json.Marshal(doc)
	if err != nil {
		return err
	}
	if len(data) > bridgeLimit {
		return &http.MaxBytesError{Limit: bridgeLimit}
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	r.Header.Del("Content-Length")
	return nil
}

func isCodexDelegationOutput(item map[string]any) bool {
	if stringValue(item["type"]) != "function_call_output" || stringValue(item["namespace"]) != "codex_app" {
		return false
	}
	switch stringValue(item["name"]) {
	case "create_thread", "send_message_to_thread", "handoff_thread":
	default:
		return false
	}
	if id, exists := item["call_id"]; exists && id != nil && id != "" {
		return false
	}
	// Desktop's TurnToolOutput.output is a string. Leave other payload types
	// untouched rather than guessing at future protocol additions.
	output, ok := item["output"].(string)
	if !ok {
		return false
	}
	output = strings.TrimSpace(output)
	return strings.HasPrefix(output, "<codex_delegation>") && strings.HasSuffix(output, "</codex_delegation>")
}

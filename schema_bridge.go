package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Preserve union constraints inside an object envelope instead of flattening them.
// The envelope is private to this request and is removed from every tool call
// returned to the client. Only Anthropic Responses requests need this bridge.
const toolEnvelope = "kilo_tool_input"
const bridgeLimit = 32 << 20

type schemaBridge struct {
	tools    map[string]bool
	calls    map[string]bool
	sequence int64
}

func decodeObject(data []byte) (map[string]any, error) {
	var value map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errors.New("expected JSON object")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("unexpected trailing JSON")
	}
	return value, nil
}
func object(v any) map[string]any           { m, _ := v.(map[string]any); return m }
func stringValue(v any) string              { s, _ := v.(string); return s }
func toolKey(namespace, name string) string { return namespace + "\x00" + name }

func needsEnvelope(schema map[string]any) bool {
	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		if _, ok := schema[key]; ok {
			return true
		}
	}
	return false
}

// Relative JSON pointers are rooted in the original document. Preserve their
// meaning after nesting. Anchors/IDs and external refs need a full resolver;
// refuse those rather than silently changing validation semantics.
func relocateSchemaRefs(value any) error {
	node, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"$id", "$anchor", "$dynamicRef", "$dynamicAnchor", "$recursiveRef", "$recursiveAnchor"} {
		if _, exists := node[key]; exists {
			return errors.New("schema identifiers or dynamic references are unsupported")
		}
	}
	if value, exists := node["$ref"]; exists {
		ref, ok := value.(string)
		if !ok {
			return errors.New("invalid schema reference")
		}
		if ref == "#" {
			node["$ref"] = "#/properties/" + toolEnvelope
		} else if strings.HasPrefix(ref, "#/") {
			node["$ref"] = "#/properties/" + toolEnvelope + ref[1:]
		} else {
			return errors.New("external or anchored schema references are unsupported")
		}
	}
	// Walk schema positions only: defaults, examples, enums and consts are data.
	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies"} {
		for _, child := range object(node[key]) {
			if err := relocateSchemaRefs(child); err != nil {
				return err
			}
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		children, _ := node[key].([]any)
		for _, child := range children {
			if err := relocateSchemaRefs(child); err != nil {
				return err
			}
		}
	}
	for _, key := range []string{"items", "additionalItems", "additionalProperties", "unevaluatedProperties", "unevaluatedItems", "contains", "propertyNames", "not", "if", "then", "else", "contentSchema"} {
		if children, ok := node[key].([]any); ok {
			for _, child := range children {
				if err := relocateSchemaRefs(child); err != nil {
					return err
				}
			}
		} else if err := relocateSchemaRefs(node[key]); err != nil {
			return err
		}
	}
	return nil
}

func (b *schemaBridge) wrapTools(tools []any, namespace string) error {
	for _, entry := range tools {
		tool := object(entry)
		if tool == nil {
			continue
		}
		if stringValue(tool["type"]) == "namespace" {
			nested, _ := tool["tools"].([]any)
			if err := b.wrapTools(nested, stringValue(tool["name"])); err != nil {
				return err
			}
			continue
		}
		if stringValue(tool["type"]) != "function" {
			continue
		}
		schema := object(tool["parameters"])
		if !needsEnvelope(schema) {
			continue
		}
		name := stringValue(tool["name"])
		if name == "" {
			return errors.New("unnamed function with a union schema")
		}
		if err := relocateSchemaRefs(schema); err != nil {
			return fmt.Errorf("tool %s: %w", name, err)
		}
		tool["parameters"] = map[string]any{"type": "object", "properties": map[string]any{toolEnvelope: schema}, "required": []any{toolEnvelope}, "additionalProperties": false}
		description := stringValue(tool["description"])
		tool["description"] = description + "\nPass the original tool arguments in the required " + toolEnvelope + " property."
		b.tools[toolKey(namespace, name)] = true
	}
	return nil
}
func (b *schemaBridge) matches(item map[string]any) bool {
	return stringValue(item["type"]) == "function_call" && b.tools[toolKey(stringValue(item["namespace"]), stringValue(item["name"]))]
}
func wrapArguments(value any) (string, error) {
	args, ok := value.(string)
	if !ok {
		return "", errors.New("invalid tool arguments")
	}
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return "", errors.New("invalid tool arguments JSON")
	}
	data, err := json.Marshal(map[string]json.RawMessage{toolEnvelope: raw})
	return string(data), err
}
func unwrapArguments(value any) (string, error) {
	args, ok := value.(string)
	if !ok {
		return "", errors.New("invalid tool arguments")
	}
	var wrapped map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &wrapped); err != nil {
		return "", errors.New("invalid wrapped tool arguments JSON")
	}
	raw, ok := wrapped[toolEnvelope]
	if !ok || len(wrapped) != 1 {
		return "", errors.New("missing or invalid tool argument envelope")
	}
	// Codex function tools consume objects, never null or primitive arguments.
	if _, err := decodeObject(raw); err != nil {
		return "", errors.New("tool arguments must be an object")
	}
	return string(raw), nil
}

func prepareSchemaBridge(r *http.Request) (*schemaBridge, error) {
	if r.Method != "POST" || r.URL.Path != "/v1/responses" {
		return nil, nil
	}
	if r.Header.Get("Content-Encoding") != "" {
		return nil, nil
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(data))
	doc, err := decodeObject(data)
	if err != nil {
		return nil, nil
	} // Let the gateway report invalid JSON.
	model := strings.TrimPrefix(stringValue(doc["model"]), "~")
	if !strings.HasPrefix(model, "anthropic/") {
		return nil, nil
	}
	tools, _ := doc["tools"].([]any)
	b := &schemaBridge{tools: map[string]bool{}, calls: map[string]bool{}}
	if err := b.wrapTools(tools, ""); err != nil {
		return nil, err
	}
	if len(b.tools) == 0 {
		return nil, nil
	}
	// Tool-call history must use the same wire representation as new calls.
	input, _ := doc["input"].([]any)
	for _, v := range input {
		item := object(v)
		if b.matches(item) {
			args, err := wrapArguments(item["arguments"])
			if err != nil {
				return nil, err
			}
			item["arguments"] = args
		}
	}
	changed, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if len(changed) > bridgeLimit {
		return nil, errors.New("adapted request exceeds 32 MiB")
	}
	r.Body = io.NopCloser(bytes.NewReader(changed))
	r.ContentLength = int64(len(changed))
	r.Header.Del("Content-Length")
	return b, nil
}

func (b *schemaBridge) unwrapItem(item map[string]any, allowEmpty bool) error {
	if !b.matches(item) {
		return nil
	}
	if allowEmpty && stringValue(item["arguments"]) == "" {
		return nil
	}
	args, err := unwrapArguments(item["arguments"])
	if err != nil {
		return err
	}
	item["arguments"] = args
	return nil
}
func (b *schemaBridge) unwrapOutput(response map[string]any) error {
	output, _ := response["output"].([]any)
	for _, item := range output {
		if err := b.unwrapItem(object(item), false); err != nil {
			return err
		}
	}
	return nil
}
func (b *schemaBridge) transformEvent(data []byte) ([][]byte, error) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return [][]byte{data}, nil
	}
	event, err := decodeObject(data)
	if err != nil {
		return nil, errors.New("invalid Responses event")
	}
	kind := stringValue(event["type"])
	switch kind {
	case "response.output_item.added":
		item := object(event["item"])
		if b.matches(item) {
			b.calls[stringValue(item["id"])] = true
			if err := b.unwrapItem(item, true); err != nil {
				return nil, err
			}
		}
	case "response.function_call_arguments.delta":
		if b.calls[stringValue(event["item_id"])] {
			return nil, nil
		}
	case "response.function_call_arguments.done":
		if b.calls[stringValue(event["item_id"])] {
			args, err := unwrapArguments(event["arguments"])
			if err != nil {
				return nil, err
			}
			event["arguments"] = args
			// Emit one complete delta after validation. Never expose wrapper fragments
			// or an invalid call to the client, but keep the rest of the SSE stream live.
			delta := map[string]any{}
			for k, v := range event {
				if k != "arguments" {
					delta[k] = v
				}
			}
			delta["type"] = "response.function_call_arguments.delta"
			delta["delta"] = args
			delete(delta, "sequence_number")
			d, _ := json.Marshal(delta)
			done, _ := json.Marshal(event)
			return [][]byte{d, done}, nil
		}
	case "response.output_item.done":
		if err := b.unwrapItem(object(event["item"]), false); err != nil {
			return nil, err
		}
	case "response.completed", "response.incomplete":
		if err := b.unwrapOutput(object(event["response"])); err != nil {
			return nil, err
		}
	}
	changed, err := json.Marshal(event)
	return [][]byte{changed}, err
}

type bridgeReadCloser struct {
	io.Reader
	close func() error
}

func (r *bridgeReadCloser) Close() error { return r.close() }

func (b *schemaBridge) adaptResponse(r *http.Response) error {
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil
	}
	original := r.Body
	r.Header.Del("Content-Length")
	r.Header.Del("ETag")
	r.Header.Del("Content-MD5")
	r.Header.Del("Digest")
	r.ContentLength = -1
	if strings.Contains(r.Header.Get("Content-Type"), "text/event-stream") {
		reader, writer := io.Pipe()
		r.Body = &bridgeReadCloser{Reader: reader, close: func() error { _ = reader.Close(); return original.Close() }}
		go func() {
			defer original.Close()
			err := b.transformSSE(original, writer)
			_ = writer.CloseWithError(err)
		}()
		return nil
	}
	defer original.Close()
	raw, err := io.ReadAll(io.LimitReader(original, bridgeLimit+1))
	if err != nil {
		return err
	}
	if len(raw) > bridgeLimit {
		return errors.New("response exceeds schema bridge limit")
	}
	doc, err := decodeObject(raw)
	if err != nil {
		return err
	}
	if err := b.unwrapOutput(doc); err != nil {
		return err
	}
	raw, err = json.Marshal(doc)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))
	return nil
}

func (b *schemaBridge) transformSSE(source io.Reader, destination io.Writer) error {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 4096), bridgeLimit)
	var lines []string
	size := 0
	flush := func() error {
		if len(lines) == 0 {
			return nil
		}
		var payload []string
		var metadata []string
		for _, line := range lines {
			if strings.HasPrefix(line, "data:") {
				payload = append(payload, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			} else if !strings.HasPrefix(line, "event:") {
				metadata = append(metadata, line)
			}
		}
		if len(payload) == 0 {
			_, err := io.WriteString(destination, strings.Join(lines, "\n")+"\n\n")
			return err
		}
		events, err := b.transformEvent([]byte(strings.Join(payload, "\n")))
		if err != nil {
			return err
		}
		for _, event := range events {
			prefix := ""
			if len(metadata) > 0 {
				prefix = strings.Join(metadata, "\n") + "\n"
			}
			// Match the event name to synthesized deltas while retaining SSE IDs/retry.
			obj, _ := decodeObject(event)
			if name := stringValue(obj["type"]); name != "" {
				// Suppressed argument fragments and the synthesized delta change
				// event counts. Keep Responses sequence numbers contiguous.
				obj["sequence_number"] = b.sequence
				b.sequence++
				event, _ = json.Marshal(obj)
				prefix += "event: " + name + "\n"
			}
			if _, err := io.WriteString(destination, prefix+"data: "+string(event)+"\n\n"); err != nil {
				return err
			}
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		size += len(line)
		if size > bridgeLimit {
			return errors.New("SSE event exceeds schema bridge limit")
		}
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			lines = nil
			size = 0
		} else {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

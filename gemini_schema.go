package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Google's JSON-schema converter expands type arrays into bare anyOf branches,
// leaving items/properties and their constraints on the untyped parent. Expand
// only affected unions ourselves so each restriction stays with its own type.
// Tool names, arguments, history and responses do not need another wire format.
const geminiSchemaDepthLimit = 64

var geminiTypedKeywords = map[string]string{
	"items": "array", "prefixItems": "array", "additionalItems": "array",
	"contains": "array", "minContains": "array", "maxContains": "array",
	"minItems": "array", "maxItems": "array", "uniqueItems": "array", "unevaluatedItems": "array",
	"properties": "object", "patternProperties": "object", "additionalProperties": "object",
	"required": "object", "dependentRequired": "object", "dependentSchemas": "object",
	"dependencies": "object", "propertyNames": "object", "minProperties": "object",
	"maxProperties": "object", "unevaluatedProperties": "object",
	"minLength": "string", "maxLength": "string", "pattern": "string",
	"minimum": "number", "maximum": "number", "exclusiveMinimum": "number",
	"exclusiveMaximum": "number", "multipleOf": "number",
}

type geminiSchemaWalk struct {
	changed     bool
	refs        []string
	moved       []string
	identifiers bool
	dynamicRefs bool
}

func geminiPointerPart(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func geminiKeywordApplies(kind, target string) bool {
	return kind == target || target == "number" && kind == "integer"
}

func (w *geminiSchemaWalk) normalize(value any, path string, depth int) error {
	if depth > geminiSchemaDepthLimit {
		return errors.New("Gemini tool schema exceeds the supported nesting depth")
	}
	node := object(value)
	if node == nil { // Boolean schemas are not changed into permissive objects.
		return nil
	}
	if ref, ok := node["$ref"].(string); ok {
		w.refs = append(w.refs, ref)
	}
	for _, key := range []string{"$id", "$anchor", "$dynamicAnchor", "$recursiveAnchor"} {
		if _, ok := node[key]; ok {
			w.identifiers = true
		}
	}
	for _, key := range []string{"$dynamicRef", "$recursiveRef"} {
		if _, ok := node[key]; ok {
			w.dynamicRefs = true
		}
	}
	// Visit schema positions only. const/default/examples/enum contain data.
	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies"} {
		for name, child := range object(node[key]) {
			if err := w.normalize(child, path+"/"+key+"/"+geminiPointerPart(name), depth+1); err != nil {
				return err
			}
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		children, _ := node[key].([]any)
		for i, child := range children {
			if err := w.normalize(child, fmt.Sprintf("%s/%s/%d", path, key, i), depth+1); err != nil {
				return err
			}
		}
	}
	for _, key := range []string{"items", "additionalItems", "additionalProperties", "unevaluatedProperties", "unevaluatedItems", "contains", "propertyNames", "not", "if", "then", "else", "contentSchema"} {
		if children, ok := node[key].([]any); ok {
			for i, child := range children {
				if err := w.normalize(child, fmt.Sprintf("%s/%s/%d", path, key, i), depth+1); err != nil {
					return err
				}
			}
		} else if err := w.normalize(node[key], path+"/"+key, depth+1); err != nil {
			return err
		}
	}
	values, ok := node["type"].([]any)
	if !ok {
		return nil
	}
	kinds := make([]string, 0, len(values))
	seen := map[string]bool{}
	nonNull := 0
	for _, value := range values {
		kind, ok := value.(string)
		if !ok || kind != "null" && kind != "string" && kind != "array" && kind != "object" && kind != "boolean" && kind != "number" && kind != "integer" {
			return errors.New("Gemini tool schema has an invalid type union")
		}
		if seen[kind] {
			return errors.New("Gemini tool schema has a duplicate union type")
		}
		seen[kind] = true
		kinds = append(kinds, kind)
		if kind != "null" {
			nonNull++
		}
	}
	// A single non-null type is already converted correctly, including arrays
	// with nullable:true. Also leave pure type unions byte-for-byte untouched.
	if nonNull < 2 {
		return nil
	}
	affected := false
	for key := range geminiTypedKeywords {
		if _, ok := node[key]; ok {
			affected = true
			break
		}
	}
	if !affected {
		return nil
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if _, ok := node[key]; ok {
			return errors.New("Gemini tool type unions combined with schema combinators cannot be normalized safely")
		}
	}
	// These keywords consume evaluation annotations from sibling applicators.
	// Moving them into a branch can change which properties/items are allowed.
	for _, key := range []string{"unevaluatedProperties", "unevaluatedItems"} {
		if _, ok := node[key]; ok {
			return errors.New("Gemini tool unions with unevaluated constraints cannot be normalized safely")
		}
	}
	if seen["array"] {
		if _, exists := node["items"]; !exists || object(node["items"]) == nil {
			return errors.New("Gemini tool array unions require an explicit object items schema")
		}
	}
	branches := make([]any, 0, len(kinds))
	for _, kind := range kinds {
		branch := map[string]any{"type": kind}
		for key, target := range geminiTypedKeywords {
			if value, exists := node[key]; exists && geminiKeywordApplies(kind, target) {
				branch[key] = value
			}
		}
		branches = append(branches, branch)
	}
	for key, target := range geminiTypedKeywords {
		if _, exists := node[key]; !exists {
			continue
		}
		applicable := false
		for _, kind := range kinds {
			applicable = applicable || geminiKeywordApplies(kind, target)
		}
		if !applicable {
			return errors.New("Gemini tool union has a restriction for an absent type")
		}
		w.moved = append(w.moved, "#"+path+"/"+key)
		delete(node, key)
	}
	delete(node, "type")
	node["anyOf"] = branches
	w.changed = true
	return nil
}

func (w *geminiSchemaWalk) validateReferences() error {
	if !w.changed {
		return nil
	}
	if w.dynamicRefs || w.identifiers && len(w.refs) > 0 {
		return errors.New("Gemini tool union references with schema identifiers require a resolver")
	}
	for _, ref := range w.refs {
		if !strings.HasPrefix(ref, "#") {
			continue
		}
		pointer, err := url.PathUnescape(ref)
		if err != nil {
			return errors.New("Gemini tool schema has an invalid reference")
		}
		for _, moved := range w.moved {
			if pointer == moved || strings.HasPrefix(pointer, moved+"/") {
				return errors.New("Gemini tool reference targets a schema field moved by union normalization")
			}
		}
	}
	return nil
}

func normalizeGeminiTools(tools []any, depth int) (bool, error) {
	if depth > geminiSchemaDepthLimit {
		return false, errors.New("Gemini tool namespaces exceed the supported nesting depth")
	}
	changed := false
	for _, entry := range tools {
		tool := object(entry)
		if stringValue(tool["type"]) == "namespace" {
			nested, _ := tool["tools"].([]any)
			childChanged, err := normalizeGeminiTools(nested, depth+1)
			if err != nil {
				return false, err
			}
			changed = changed || childChanged
			continue
		}
		if stringValue(tool["type"]) != "function" {
			continue
		}
		walk := &geminiSchemaWalk{}
		if err := walk.normalize(tool["parameters"], "", 0); err != nil {
			return false, err
		}
		if err := walk.validateReferences(); err != nil {
			return false, err
		}
		changed = changed || walk.changed
	}
	return changed, nil
}

func prepareGeminiToolSchemas(r *http.Request, model string) error {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || r.Header.Get("Content-Encoding") != "" || !strings.HasPrefix(strings.TrimPrefix(model, "~"), "google/gemini-") {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, bridgeLimit+1))
	if err != nil {
		return err
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(data))
	if len(data) > bridgeLimit {
		return &http.MaxBytesError{Limit: bridgeLimit}
	}
	doc, err := decodeObject(data)
	if err != nil {
		return nil
	} // Keep the gateway's invalid-JSON behavior.
	tools, _ := doc["tools"].([]any)
	changed, err := normalizeGeminiTools(tools, 0)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
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

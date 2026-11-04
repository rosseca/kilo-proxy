package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func gsRequest(t *testing.T, schema string, model string) (map[string]any, []byte, error) {
	t.Helper()
	body := `{"model":"` + model + `","input":[{"type":"function_call","name":"send","call_id":"prior","arguments":"{\"body\":[\"raw\"]}"},{"type":"function_call_output","call_id":"prior","output":"unchanged"}],"tools":[{"type":"function","name":"send","parameters":` + schema + `}],"metadata":{"exact":9007199254740993}}`
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	r.Header.Set("Content-Length", "stale")
	err := prepareGeminiToolSchemas(r, model)
	raw, _ := io.ReadAll(r.Body)
	if err != nil {
		return nil, raw, err
	}
	if r.ContentLength != int64(len(raw)) {
		t.Fatal("wrong adapted content length")
	}
	doc, decodeErr := decodeObject(raw)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if object(doc["metadata"])["exact"].(json.Number).String() != "9007199254740993" {
		t.Fatal("large number precision lost")
	}
	before, _ := decodeObject([]byte(body))
	if !reflect.DeepEqual(doc["input"], before["input"]) {
		t.Fatal("tool arguments or history changed")
	}
	return object(object(doc["tools"].([]any)[0])["parameters"]), raw, nil
}

func gsBranch(t *testing.T, schema map[string]any, kind string) map[string]any {
	t.Helper()
	branches, ok := schema["anyOf"].([]any)
	if !ok {
		t.Fatalf("explicit alternatives missing: %#v", schema)
	}
	for _, v := range branches {
		if branch := object(v); branch["type"] == kind {
			return branch
		}
	}
	t.Fatalf("type %s missing", kind)
	return nil
}

func TestGeminiSchemaMovesConstraintsWithoutChangingArguments(t *testing.T) {
	schema := `{"type":"object","properties":{"body":{"type":["string","array"],"description":"string or rows","minLength":2,"pattern":"^[a-z]","items":{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false},"minItems":1,"maxItems":2,"uniqueItems":true}},"required":["body"],"additionalProperties":false}`
	got, _, err := gsRequest(t, schema, "google/gemini-4-argon")
	if err != nil {
		t.Fatal(err)
	}
	body := object(object(got["properties"])["body"])
	if _, exists := body["type"]; exists {
		t.Fatal("lossy SDK type array retained")
	}
	if body["description"] != "string or rows" || got["additionalProperties"] != false {
		t.Fatal("common or root restrictions lost")
	}
	array := gsBranch(t, body, "array")
	text := gsBranch(t, body, "string")
	if array["minItems"] != json.Number("1") || array["maxItems"] != json.Number("2") || array["uniqueItems"] != true {
		t.Fatal("array cardinality restrictions lost")
	}
	if text["minLength"] != json.Number("2") || text["pattern"] != "^[a-z]" {
		t.Fatal("string restrictions lost")
	}
	if array["pattern"] != nil || text["items"] != nil || body["items"] != nil || body["minLength"] != nil {
		t.Fatal("type-specific restriction at wrong level")
	}
	original, _ := decodeObject([]byte(schema))
	want := object(object(original["properties"])["body"])["items"]
	if !reflect.DeepEqual(array["items"], want) {
		t.Fatal("nested item restrictions changed")
	}
}

func TestGeminiSchemaObjectNumericAndNullableAlternatives(t *testing.T) {
	for _, schema := range []string{
		`{"type":["string","object"],"properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false,"minLength":2}`,
		`{"type":["integer","number","string"],"minimum":2,"maximum":9,"multipleOf":2,"minLength":1}`,
		`{"type":["string","array","null"],"items":{"type":"string"},"minItems":1,"description":"retained nullable union","enum":[null,"abc",["x"]]}`,
	} {
		t.Run(schema, func(t *testing.T) {
			got, _, err := gsRequest(t, schema, "~google/gemini-4-argon")
			if err != nil {
				t.Fatal(err)
			}
			original, _ := decodeObject([]byte(schema))
			if original["description"] != got["description"] || !reflect.DeepEqual(original["enum"], got["enum"]) {
				t.Fatal("common constraints changed")
			}
			for _, v := range original["type"].([]any) {
				kind := v.(string)
				branch := gsBranch(t, got, kind)
				switch kind {
				case "object":
					if branch["additionalProperties"] != false || !reflect.DeepEqual(branch["required"], original["required"]) {
						t.Fatal("object restriction lost")
					}
				case "integer", "number":
					if branch["minimum"] != json.Number("2") || branch["maximum"] != json.Number("9") || branch["multipleOf"] != json.Number("2") {
						t.Fatal("numeric restriction lost")
					}
				case "null":
					if len(branch) != 1 {
						t.Fatal("null acquired another type's restrictions")
					}
				}
			}
		})
	}
}

func TestGeminiSchemaSchemaPositionsAndDataRemainDistinct(t *testing.T) {
	schema := `{"type":"object","$defs":{"body":{"type":["string","array"],"items":{"type":"integer"}}},"properties":{"payload":{"$ref":"#/$defs/body"},"literal":{"type":"object","const":{"type":["string","array"],"items":false},"default":{"type":["string","array"],"items":false},"examples":[{"type":["string","array"],"items":false}],"enum":[{"type":["string","array"],"items":false}]}},"additionalProperties":{"type":["string","array"],"items":{"type":"string"}}}`
	got, _, err := gsRequest(t, schema, "google/gemini-4-argon")
	if err != nil {
		t.Fatal(err)
	}
	original, _ := decodeObject([]byte(schema))
	properties := object(got["properties"])
	if !reflect.DeepEqual(properties["literal"], object(original["properties"])["literal"]) {
		t.Fatal("schema-looking literal data mutated")
	}
	if object(properties["payload"])["$ref"] != "#/$defs/body" {
		t.Fatal("whole-definition reference changed")
	}
	gsBranch(t, object(object(got["$defs"])["body"]), "array")
	gsBranch(t, object(got["additionalProperties"]), "array")
}

func TestGeminiSchemaUnchangedRequestsKeepExactBytes(t *testing.T) {
	body := "  {\"model\":\"google/gemini-4-argon\",\"tools\":[{\"type\":\"function\",\"name\":\"safe\",\"parameters\":{\"type\":\"object\",\"properties\":{\"body\":{\"type\":[\"array\",\"null\"],\"items\":{\"type\":\"string\"}}}}}]}\n"
	for _, tc := range []struct{ method, path, model, encoding string }{
		{"POST", "/v1/responses", "google/gemini-4-argon", ""},
		{"POST", "/v1/responses", "openai/test", ""},
		{"POST", "/v1/responses", "anthropic/test", ""},
		{"POST", "/v1/responses", "google/gemma-test", ""},
		{"POST", "/v1/responses", "vendor/gemini-test", ""},
		{"GET", "/v1/responses", "google/gemini-4-argon", ""},
		{"POST", "/v1/chat/completions", "google/gemini-4-argon", ""},
		{"POST", "/v1/messages", "google/gemini-4-argon", ""},
		{"POST", "/v1/responses", "google/gemini-4-argon", "gzip"},
	} {
		t.Run(tc.method+tc.path+tc.model+tc.encoding, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(body))
			r.Header.Set("Content-Encoding", tc.encoding)
			length := r.ContentLength
			if err := prepareGeminiToolSchemas(r, tc.model); err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(r.Body)
			if !bytes.Equal(raw, []byte(body)) || r.ContentLength != length {
				t.Fatal("unaffected request bytes changed")
			}
		})
	}
}

func TestGeminiSchemaRejectsUnsafeNormalizationWithoutChangingBody(t *testing.T) {
	for _, schema := range []string{
		`{"type":["string","array"],"items":{"type":"string"},"anyOf":[{"minItems":1}]}`,
		`{"type":"object","$defs":{"body":{"type":["string","array"],"items":{"type":"string"}}},"properties":{"pointer":{"$ref":"#/$defs/body/items"}}}`,
		`{"type":"object","$defs":{"body":{"type":["string","array"],"items":{"type":"string"}}},"properties":{"pointer":{"$ref":"#%2F$defs%2Fbody%2Fitems"}}}`,
		`{"type":["string","array"],"items":[{"type":"string"}]}`,
		`{"type":["string","array"],"items":false}`,
		`{"type":["string","array"],"minItems":1}`,
		`{"type":["string","array"],"items":{"type":"string"},"$dynamicRef":"#target"}`,
		`{"type":["string","array"],"items":{"type":"string"},"unevaluatedItems":false}`,
		`{"type":["string","object"],"properties":{"a":{"type":"string"}},"if":{"required":["a"]},"then":{"properties":{"b":{"type":"string"}}},"unevaluatedProperties":false}`,
	} {
		t.Run(schema, func(t *testing.T) {
			body := `{"model":"google/gemini-4-argon","tools":[{"type":"function","name":"unsafe","parameters":` + schema + `}]}`
			r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
			if err := prepareGeminiToolSchemas(r, "google/gemini-4-argon"); err == nil {
				t.Fatal("unsafe conversion accepted")
			}
			raw, _ := io.ReadAll(r.Body)
			if string(raw) != body {
				t.Fatal("failed conversion changed outgoing body")
			}
		})
	}
}

func TestGeminiSchemaBoundsSchemaDepth(t *testing.T) {
	schema := `{"type":"string"}`
	for i := 0; i < geminiSchemaDepthLimit+2; i++ {
		schema = `{"type":"object","properties":{"next":` + schema + `}}`
	}
	if _, _, err := gsRequest(t, schema, "google/gemini-4-argon"); err == nil {
		t.Fatal("unbounded schema nesting accepted")
	}
}

package main

// These integration tests exercise the public proxy HTTP boundary against a
// synthetic upstream contract that accepts flat client tools. They do not copy
// Kilo Gateway's filtering algorithm or claim real provider/model execution.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

type ngID struct{ namespace, name, marker string }

var ngIDs = []ngID{{"multi_agent_v1", "spawn_agent", "spawn"}, {"multi_agent_v1", "send_input", "send"}, {"weather", "lookup", "weather"}, {"research", "lookup", "research"}, {"", "lookup", "flat"}}

func ngJSON(t *testing.T, x any) []byte {
	t.Helper()
	b, e := json.Marshal(x)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func ngDoc(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var x map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e := d.Decode(&x); e != nil {
		t.Fatalf("invalid fixture JSON: %v", e)
	}
	return x
}
func ngClone(t *testing.T, x any) any {
	t.Helper()
	var v any
	d := json.NewDecoder(bytes.NewReader(ngJSON(t, x)))
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		t.Fatal(e)
	}
	return v
}
func ngEqual(t *testing.T, label string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(ngClone(t, got), ngClone(t, want)) {
		t.Errorf("%s mismatch\ngot %s\nwant %s", label, ngJSON(t, got), ngJSON(t, want))
	}
}
func ngFunction(id ngID) map[string]any {
	return map[string]any{"type": "function", "name": id.name, "description": "Original client tool " + id.marker, "strict": false, "parameters": map[string]any{"type": "object", "properties": map[string]any{"marker": map[string]any{"const": id.marker}, "message": map[string]any{"type": "string"}}, "required": []any{"marker", "message"}, "additionalProperties": false}}
}
func ngTools() []any {
	var tools []any
	for _, ns := range []string{"multi_agent_v1", "weather", "research"} {
		var children []any
		for _, id := range ngIDs {
			if id.namespace == ns {
				children = append(children, ngFunction(id))
			}
		}
		tools = append(tools, map[string]any{"type": "namespace", "name": ns, "description": "Original client namespace " + ns, "tools": children})
	}
	return append(tools, ngFunction(ngIDs[len(ngIDs)-1]))
}
func ngArgs(t *testing.T, marker string) string {
	return string(ngJSON(t, map[string]any{"marker": marker, "message": "synthetic task ñ"}))
}
func ngCall(t *testing.T, id ngID) map[string]any {
	x := map[string]any{"type": "function_call", "id": "fc_" + id.marker, "call_id": "call_" + id.marker, "name": id.name, "arguments": ngArgs(t, id.marker)}
	if id.namespace != "" {
		x["namespace"] = id.namespace
	}
	return x
}
func ngRequest(t *testing.T, stream bool) map[string]any {
	input := []any{map[string]any{"role": "user", "content": "Synthetic namespace boundary check"}}
	for _, id := range ngIDs {
		call := ngCall(t, id)
		call["id"] = "prior_" + id.marker
		call["call_id"] = "prior_call_" + id.marker
		input = append(input, call, map[string]any{"type": "function_call_output", "call_id": call["call_id"], "output": "previous synthetic result"})
	}
	return map[string]any{"model": "openai/test-model", "stream": stream, "tools": ngTools(), "tool_choice": "auto", "input": input, "metadata": map[string]any{"large": json.Number("9007199254740993"), "unicode": "á ñ 😀"}, "parallel_tool_calls": true}
}
func ngMarker(tool map[string]any) string {
	p := object(tool["parameters"])
	if inner := object(object(p["properties"])["kilo_tool_input"]); inner != nil {
		p = inner
	}
	return stringValue(object(object(p["properties"])["marker"])["const"])
}

type ngLocalTransport struct {
	host string
	base http.RoundTripper
}

func (tr ngLocalTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "http" || r.URL.Host != tr.host {
		return nil, fmt.Errorf("synthetic upstream refuses external destination")
	}
	return tr.base.RoundTrip(r)
}

type ngResult struct {
	status   int
	body     []byte
	upstream map[string]any
	reached  int32
}
type ngResponder func(map[string]any) ([]byte, string)

func ngProxy(t *testing.T, path string, request []byte, respond ngResponder) ngResult {
	t.Helper()
	return ngProxyHeaders(t, path, request, nil, respond)
}
func ngProxyHeaders(t *testing.T, path string, request []byte, headers map[string]string, respond ngResponder) ngResult {
	t.Helper()
	captured := make(chan map[string]any, 1)
	var reached atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		b, e := io.ReadAll(r.Body)
		if e != nil {
			http.Error(w, "fixture read failed", 500)
			return
		}
		doc := ngDoc(t, b)
		captured <- doc
		body, ct := respond(doc)
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write(body)
	}))
	t.Cleanup(upstream.Close)
	u, _ := url.Parse(upstream.URL)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	t.Cleanup(tr.CloseIdleConnections)
	a := testApp(t)
	a.transport = ngLocalTransport{u.Host, tr}
	setUpstream(a, upstream.URL)
	front := httptest.NewUnstartedServer(nil)
	front.Config.Handler = a.inferenceHandler("synthetic-upstream", "synthetic-org", "synthetic-local", front.Listener.Addr().String())
	front.Start()
	t.Cleanup(front.Close)
	clientTr := http.DefaultTransport.(*http.Transport).Clone()
	clientTr.Proxy = nil
	t.Cleanup(clientTr.CloseIdleConnections)
	frontURL, _ := url.Parse(front.URL)
	client := &http.Client{Transport: ngLocalTransport{frontURL.Host, clientTr}}
	r, e := http.NewRequest(http.MethodPost, front.URL+path, bytes.NewReader(request))
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Authorization", "Bearer synthetic-local")
	r.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	resp, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	out, e := io.ReadAll(resp.Body)
	if e != nil {
		t.Fatal(e)
	}
	result := ngResult{status: resp.StatusCode, body: out, reached: reached.Load()}
	select {
	case result.upstream = <-captured:
	default:
	}
	return result
}
func ngResponse(t *testing.T, wire map[string]any) map[string]any {
	t.Helper()
	var calls []any
	tools, _ := wire["tools"].([]any)
	seen := map[string]bool{}
	safe := regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// The synthetic response obeys the declared choice, without claiming any
	// model selection behavior. Choice identities are checked separately below.
	selected := map[string]bool{}
	choice := object(wire["tool_choice"])
	switch choice["type"] {
	case "function", "custom":
		selected[stringValue(choice["name"])] = true
	case "allowed_tools":
		refs, _ := choice["tools"].([]any)
		for _, ref := range refs {
			selected[stringValue(object(ref)["name"])] = true
		}
	}
	for _, raw := range tools {
		tool := object(raw)
		kind := stringValue(tool["type"])
		name := stringValue(tool["name"])
		if kind == "namespace" {
			t.Errorf("upstream contract received an unadapted namespace")
			continue
		}
		if kind != "function" && kind != "custom" {
			continue
		}
		if !safe.MatchString(name) || seen[name] {
			t.Errorf("flat client tool names must be safe and unique: %q", name)
		}
		seen[name] = true
		if len(selected) > 0 && !selected[name] {
			continue
		}
		if kind == "custom" {
			calls = append(calls, map[string]any{"type": "custom_tool_call", "id": "fc_custom", "call_id": "call_custom", "name": name, "input": "opaque custom payload\nñ"})
			continue
		}
		marker := ngMarker(tool)
		if marker == "" {
			t.Errorf("missing synthetic schema marker")
			continue
		}
		args := ngArgs(t, marker)
		p := object(tool["parameters"])
		if object(object(p["properties"])["kilo_tool_input"]) != nil {
			args = string(ngJSON(t, map[string]any{"kilo_tool_input": ngDoc(t, []byte(args))}))
		}
		calls = append(calls, map[string]any{"type": "function_call", "id": "fc_" + marker, "call_id": "call_" + marker, "name": name, "arguments": args})
	}
	return map[string]any{"id": "resp_synthetic", "status": "completed", "output": calls, "tools": ngClone(t, wire["tools"]), "tool_choice": ngClone(t, wire["tool_choice"]), "metadata": map[string]any{"sentinel": "preserve"}}
}
func ngSSE(t *testing.T, response map[string]any) []byte {
	var out bytes.Buffer
	seq := 0
	emit := func(e map[string]any) {
		e["sequence_number"] = seq
		seq++
		fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", e["type"], ngJSON(t, e))
	}
	emit(map[string]any{"type": "response.created", "response": map[string]any{"id": response["id"], "status": "in_progress", "output": []any{}, "tools": response["tools"], "tool_choice": response["tool_choice"]}})
	for index, raw := range response["output"].([]any) {
		call := object(raw)
		key := "arguments"
		prefix := "response.function_call_arguments."
		if call["type"] == "custom_tool_call" {
			key = "input"
			prefix = "response.custom_tool_call_input."
		}
		added := object(ngClone(t, call))
		added[key] = ""
		emit(map[string]any{"type": "response.output_item.added", "output_index": index, "item": added})
		runes := []rune(stringValue(call[key]))
		cut := len(runes) / 2
		for _, delta := range []string{string(runes[:cut]), string(runes[cut:])} {
			emit(map[string]any{"type": prefix + "delta", "output_index": index, "item_id": call["id"], "delta": delta})
		}
		emit(map[string]any{"type": prefix + "done", "output_index": index, "item_id": call["id"], "name": call["name"], key: call[key]})
		emit(map[string]any{"type": "response.output_item.done", "output_index": index, "item": call})
	}
	emit(map[string]any{"type": "response.completed", "response": response})
	out.WriteString("data: [DONE]\n\n")
	return out.Bytes()
}
func ngReadSSE(t *testing.T, body []byte) (map[string]any, map[string]map[string]any, map[string]string, map[string]map[string]any) {
	t.Helper()
	var terminal map[string]any
	added := map[string]map[string]any{}
	delta := map[string]string{}
	done := map[string]map[string]any{}
	previous := int64(-1)
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		e := ngDoc(t, []byte(strings.TrimPrefix(line, "data: ")))
		n, ok := e["sequence_number"].(json.Number)
		if !ok {
			t.Errorf("missing SSE sequence number")
		} else {
			seq, err := n.Int64()
			if err != nil || seq <= previous {
				t.Errorf("nonmonotonic SSE sequence")
			}
			previous = seq
		}
		kind := stringValue(e["type"])
		switch {
		case kind == "response.output_item.added":
			item := object(e["item"])
			added[stringValue(item["id"])] = item
		case strings.HasSuffix(kind, ".delta"):
			delta[stringValue(e["item_id"])] += stringValue(e["delta"])
		case kind == "response.function_call_arguments.done" || kind == "response.custom_tool_call_input.done":
			done[stringValue(e["item_id"])] = e
		case kind == "response.completed":
			if terminal != nil {
				t.Errorf("duplicate terminal response")
			}
			terminal = object(e["response"])
		}
	}
	if terminal == nil || !strings.HasSuffix(string(body), "data: [DONE]\n\n") {
		t.Errorf("SSE terminal response/framing missing")
	}
	return terminal, added, delta, done
}
func ngAssertHistory(t *testing.T, request, wire map[string]any) {
	t.Helper()
	ngEqual(t, "metadata beyond names", wire["metadata"], request["metadata"])
	tools, _ := wire["tools"].([]any)
	aliases := map[string]string{}
	for _, raw := range tools {
		tool := object(raw)
		if tool["type"] == "function" {
			aliases[ngMarker(tool)] = stringValue(tool["name"])
		}
	}
	input, _ := wire["input"].([]any)
	original, _ := request["input"].([]any)
	if len(input) != len(original) {
		t.Errorf("history cardinality changed")
	}
	for _, id := range ngIDs {
		matches := 0
		for _, raw := range input {
			call := object(raw)
			if call["type"] == "function_call" && call["call_id"] == "prior_call_"+id.marker {
				matches++
				ngEqual(t, "history alias", call["name"], aliases[id.marker])
				if _, ok := call["namespace"]; ok {
					t.Errorf("history retains private namespace on wire")
				}
				ngEqual(t, "history argument bytes", call["arguments"], ngArgs(t, id.marker))
			}
		}
		if matches != 1 {
			t.Errorf("history match count %d for %s", matches, id.marker)
		}
	}
}
func ngChoiceIDs(choice string) []ngID {
	if choice == "forced-qualified" {
		return []ngID{ngIDs[2]}
	}
	if choice == "allowed-tools" {
		return []ngID{ngIDs[2], ngIDs[4]}
	}
	return ngIDs
}
func ngAssertCalls(t *testing.T, body map[string]any, ids []ngID) {
	t.Helper()
	var want []any
	for _, id := range ids {
		want = append(want, ngCall(t, id))
	}
	ngEqual(t, "restored namespaced calls", body["output"], want)
	ngEqual(t, "metadata in response", body["metadata"], map[string]any{"sentinel": "preserve"})
}
func ngAssertToolEcho(t *testing.T, body map[string]any) {
	t.Helper()
	tools, _ := body["tools"].([]any)
	want := ngTools()
	if len(tools) != len(want) {
		t.Errorf("namespace group/flat tool echo count got=%d want=%d", len(tools), len(want))
		return
	}
	for i, raw := range tools {
		tool := object(raw)
		original := object(want[i])
		ngEqual(t, "echo tool type", tool["type"], original["type"])
		ngEqual(t, "echo tool/group name", tool["name"], original["name"])
		if tool["type"] == "namespace" {
			children, _ := tool["tools"].([]any)
			originalChildren, _ := original["tools"].([]any)
			if len(children) != len(originalChildren) {
				t.Errorf("echo namespace child count")
				continue
			}
			for j, c := range children {
				child := object(c)
				expected := object(originalChildren[j])
				for _, key := range []string{"type", "name", "parameters", "strict"} {
					ngEqual(t, "echo child "+key, child[key], expected[key])
				}
			}
		}
	}
}
func TestNamespaceGatewayContractRoundTrip(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, choice := range []string{"auto", "forced-qualified", "allowed-tools"} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, choice), func(t *testing.T) {
				req := ngRequest(t, stream)
				switch choice {
				case "forced-qualified":
					req["tool_choice"] = map[string]any{"type": "function", "name": "weather.lookup"}
				case "allowed-tools":
					req["tool_choice"] = map[string]any{"type": "allowed_tools", "mode": "required", "tools": []any{map[string]any{"type": "function", "name": "weather.lookup"}, map[string]any{"type": "function", "name": "lookup"}}}
				}
				result := ngProxy(t, "/v1/responses", ngJSON(t, req), func(wire map[string]any) ([]byte, string) {
					ngAssertHistory(t, req, wire)
					tools, _ := wire["tools"].([]any)
					weatherAlias := ""
					for _, v := range tools {
						if tool := object(v); ngMarker(tool) == "weather" {
							weatherAlias = stringValue(tool["name"])
						}
					}
					if choice == "forced-qualified" {
						ngEqual(t, "forced qualified choice maps to exact alias", wire["tool_choice"], map[string]any{"type": "function", "name": weatherAlias})
					}
					if choice == "allowed-tools" {
						ngEqual(t, "allowed tool refs map without changing flat choice", wire["tool_choice"], map[string]any{"type": "allowed_tools", "mode": "required", "tools": []any{map[string]any{"type": "function", "name": weatherAlias}, map[string]any{"type": "function", "name": "lookup"}}})
					}
					response := ngResponse(t, wire)
					if stream {
						return ngSSE(t, response), "text/event-stream"
					}
					return ngJSON(t, response), "application/json"
				})
				if result.status != 200 {
					t.Fatalf("status=%d body=%s", result.status, result.body)
				}
				ids := ngChoiceIDs(choice)
				var client map[string]any
				if stream {
					var added, done map[string]map[string]any
					var delta map[string]string
					client, added, delta, done = ngReadSSE(t, result.body)
					if len(added) != len(ids) || len(done) != len(ids) {
						t.Errorf("SSE tool cardinality")
					}
					for _, id := range ids {
						call := ngCall(t, id)
						eventID := stringValue(call["id"])
						empty := object(ngClone(t, call))
						empty["arguments"] = ""
						ngEqual(t, "SSE added identity", added[eventID], empty)
						ngEqual(t, "SSE delta exact argument bytes", delta[eventID], call["arguments"])
						for _, key := range []string{"name", "namespace"} {
							ngEqual(t, "SSE done identity "+key, done[eventID][key], call[key])
						}
					}
				} else {
					client = ngDoc(t, result.body)
				}
				ngAssertCalls(t, client, ids)
				ngAssertToolEcho(t, client)
				ngEqual(t, "echo original tool choice", client["tool_choice"], req["tool_choice"])
			})
		}
	}
}
func TestNamespaceGatewayContractCustomTool(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			req := map[string]any{"model": "openai/test-model", "stream": stream, "input": "synthetic", "tools": []any{map[string]any{"type": "namespace", "name": "markup", "description": "Custom namespace", "tools": []any{map[string]any{"type": "custom", "name": "paint", "description": "Custom client tool", "format": map[string]any{"type": "text"}}}}}, "tool_choice": map[string]any{"type": "custom", "name": "markup.paint"}}
			result := ngProxy(t, "/v1/responses", ngJSON(t, req), func(wire map[string]any) ([]byte, string) {
				tools, _ := wire["tools"].([]any)
				if len(tools) != 1 || object(tools[0])["type"] != "custom" {
					t.Errorf("custom namespace was not flattened")
				}
				response := ngResponse(t, wire)
				if stream {
					return ngSSE(t, response), "text/event-stream"
				}
				return ngJSON(t, response), "application/json"
			})
			if result.status != 200 {
				t.Fatalf("status=%d body=%s", result.status, result.body)
			}
			var client map[string]any
			if stream {
				var added, done map[string]map[string]any
				var delta map[string]string
				client, added, delta, done = ngReadSSE(t, result.body)
				ngEqual(t, "custom added name", added["fc_custom"]["name"], "paint")
				ngEqual(t, "custom added namespace", added["fc_custom"]["namespace"], "markup")
				ngEqual(t, "custom raw input delta", delta["fc_custom"], "opaque custom payload\nñ")
				ngEqual(t, "custom input done namespace", done["fc_custom"]["namespace"], "markup")
			} else {
				client = ngDoc(t, result.body)
			}
			ngEqual(t, "custom call identity and input", client["output"], []any{map[string]any{"type": "custom_tool_call", "id": "fc_custom", "call_id": "call_custom", "name": "paint", "namespace": "markup", "input": "opaque custom payload\nñ"}})
			ngEqual(t, "custom tool choice restored", client["tool_choice"], req["tool_choice"])
		})
	}
}
func TestNamespaceGatewayContractAnthropicUnionComposition(t *testing.T) {
	for _, encoding := range []string{"", " IdEnTiTy "} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("encoding=%s/stream=%t", strings.TrimSpace(encoding), stream), func(t *testing.T) {
				req := ngRequest(t, stream)
				req["model"] = "anthropic/test-model"
				group := object(req["tools"].([]any)[0])
				spawn := object(group["tools"].([]any)[0])
				schema := object(spawn["parameters"])
				schema["anyOf"] = []any{map[string]any{"required": []any{"message"}}, map[string]any{"required": []any{"items"}}}
				object(schema["properties"])["items"] = map[string]any{"type": "array", "items": map[string]any{"type": "object"}}
				headers := map[string]string{}
				if encoding != "" {
					headers["Content-Encoding"] = encoding
				}
				result := ngProxyHeaders(t, "/v1/responses", ngJSON(t, req), headers, func(wire map[string]any) ([]byte, string) {
					tools, _ := wire["tools"].([]any)
					var declaration map[string]any
					for _, raw := range tools {
						tool := object(raw)
						if ngMarker(tool) == "spawn" {
							declaration = tool
						}
					}
					if declaration == nil {
						t.Errorf("union tool declaration lost")
					} else {
						wrapped := object(object(object(declaration["parameters"])["properties"])["kilo_tool_input"])
						if wrapped == nil {
							t.Errorf("existing Anthropic union envelope was not retained")
						}
						input, _ := wire["input"].([]any)
						calls, outputs := 0, 0
						for _, raw := range input {
							call := object(raw)
							if call["call_id"] != "prior_call_spawn" {
								continue
							}
							if call["type"] == "function_call" {
								calls++
								ngEqual(t, "union history alias matches declaration", call["name"], declaration["name"])
								args := ngDoc(t, []byte(stringValue(call["arguments"])))
								ngEqual(t, "union history keeps existing argument envelope", args["kilo_tool_input"], ngDoc(t, []byte(ngArgs(t, "spawn"))))
							}
							if call["type"] == "function_call_output" {
								outputs++
								ngEqual(t, "union paired tool output retained", call, map[string]any{"type": "function_call_output", "call_id": "prior_call_spawn", "output": "previous synthetic result"})
							}
						}
						if calls != 1 {
							t.Errorf("union prior function_call count=%d want=1", calls)
						}
						if outputs != 1 {
							t.Errorf("union paired output count=%d want=1", outputs)
						}
					}
					response := ngResponse(t, wire)
					if stream {
						return ngSSE(t, response), "text/event-stream"
					}
					return ngJSON(t, response), "application/json"
				})
				if result.status != 200 {
					t.Fatalf("status=%d body=%s", result.status, result.body)
				}
				var client map[string]any
				if stream {
					var added, done map[string]map[string]any
					var delta map[string]string
					client, added, delta, done = ngReadSSE(t, result.body)
					ngEqual(t, "union SSE namespace restored before schema adaptation", added["fc_spawn"]["namespace"], "multi_agent_v1")
					ngEqual(t, "union SSE argument envelope removed", delta["fc_spawn"], ngArgs(t, "spawn"))
					ngEqual(t, "union SSE done argument envelope removed", done["fc_spawn"]["arguments"], ngArgs(t, "spawn"))
				} else {
					client = ngDoc(t, result.body)
				}
				ngAssertCalls(t, client, ngIDs)
			})
		}
	}
}
func TestNamespaceGatewayContractAliasLookingFlatCollision(t *testing.T) {
	first := ngRequest(t, false)
	probe := ngProxy(t, "/v1/responses", ngJSON(t, first), func(wire map[string]any) ([]byte, string) { return ngJSON(t, ngResponse(t, wire)), "application/json" })
	if probe.status != 200 {
		t.Fatalf("first probe status=%d", probe.status)
	}
	tools, _ := probe.upstream["tools"].([]any)
	reserved := ""
	for _, v := range tools {
		tool := object(v)
		if ngMarker(tool) == "weather" {
			reserved = stringValue(tool["name"])
		}
	}
	if reserved == "" || reserved == "lookup" {
		t.Fatalf("probe failed to expose a namespaced wire alias")
	}
	intruder := ngID{"", reserved, "intruder"}
	req := ngRequest(t, false)
	req["tools"] = append(req["tools"].([]any), ngFunction(intruder))
	result := ngProxy(t, "/v1/responses", ngJSON(t, req), func(wire map[string]any) ([]byte, string) {
		var weather, flat string
		tools, _ := wire["tools"].([]any)
		for _, v := range tools {
			tool := object(v)
			switch ngMarker(tool) {
			case "weather":
				weather = stringValue(tool["name"])
			case "intruder":
				flat = stringValue(tool["name"])
			}
		}
		if weather == reserved || weather == "" {
			t.Errorf("namespaced wire alias collided with a genuine flat declaration")
		}
		ngEqual(t, "genuine alias-looking flat name retained", flat, reserved)
		return ngJSON(t, ngResponse(t, wire)), "application/json"
	})
	if result.status != 200 {
		t.Fatalf("collision status=%d body=%s", result.status, result.body)
	}
	client := ngDoc(t, result.body)
	var want []any
	for _, id := range append(append([]ngID{}, ngIDs...), intruder) {
		want = append(want, ngCall(t, id))
	}
	ngEqual(t, "only names owned by this request are restored", client["output"], want)
}
func TestNamespaceGatewayContractIdentityEncoding(t *testing.T) {
	for _, encoding := range []string{"identity", " IdEnTiTy "} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("encoding=%s/stream=%t", strings.TrimSpace(encoding), stream), func(t *testing.T) {
				req := ngRequest(t, stream)
				result := ngProxyHeaders(t, "/v1/responses", ngJSON(t, req), map[string]string{"Content-Encoding": encoding}, func(wire map[string]any) ([]byte, string) {
					ngAssertHistory(t, req, wire)
					response := ngResponse(t, wire)
					if stream {
						return ngSSE(t, response), "text/event-stream"
					}
					return ngJSON(t, response), "application/json"
				})
				if result.status != 200 {
					t.Fatalf("identity is uncompressed; status=%d body=%s", result.status, result.body)
				}
				var client map[string]any
				if stream {
					client, _, _, _ = ngReadSSE(t, result.body)
				} else {
					client = ngDoc(t, result.body)
				}
				ngAssertCalls(t, client, ngIDs)
			})
		}
	}
}
func TestNamespaceGatewayContractRejectsUnsupportedWithoutSending(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{{"previous-response", func(d map[string]any) { d["previous_response_id"] = "resp_previous" }}, {"dynamic-tools", func(d map[string]any) { d["tools"] = append(d["tools"].([]any), map[string]any{"type": "tool_search"}) }}, {"dynamic-results", func(d map[string]any) {
		d["input"] = append(d["input"].([]any), map[string]any{"type": "tool_search_output", "id": "search_output", "tools": []any{}})
	}}, {"hosted-child", func(d map[string]any) {
		object(d["tools"].([]any)[0])["tools"] = []any{map[string]any{"type": "web_search"}}
	}}, {"bare-ambiguous-choice", func(d map[string]any) { d["tool_choice"] = map[string]any{"type": "function", "name": "spawn_agent"} }}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := ngRequest(t, false)
			c.mutate(req)
			result := ngProxy(t, "/v1/responses", ngJSON(t, req), func(map[string]any) ([]byte, string) {
				t.Error("rejected namespace request reached upstream")
				return []byte(`{}`), "application/json"
			})
			if result.status != 400 || result.reached != 0 {
				t.Errorf("want local400/no-send, status=%d reached=%d body=%s", result.status, result.reached, result.body)
			}
		})
	}
}
func TestNamespaceGatewayContractFlatAndOtherProtocolsPassThrough(t *testing.T) {
	cases := []struct {
		name, path string
		request    map[string]any
	}{{"flat-responses", "/v1/responses", map[string]any{"model": "openai/test-model", "input": "synthetic", "tools": []any{ngFunction(ngIDs[len(ngIDs)-1])}, "tool_choice": map[string]any{"type": "function", "name": "lookup"}}}, {"chat-completions", "/v1/chat/completions", map[string]any{"model": "openai/test-model", "messages": []any{map[string]any{"role": "user", "content": "synthetic"}}, "tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}}}}, {"messages", "/v1/messages", map[string]any{"model": "anthropic/test-model", "max_tokens": 100, "messages": []any{map[string]any{"role": "user", "content": "synthetic"}}, "tools": []any{map[string]any{"name": "lookup", "input_schema": map[string]any{"type": "object"}}}}}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := ngProxy(t, c.path, ngJSON(t, c.request), func(wire map[string]any) ([]byte, string) {
				ngEqual(t, "protocol body remains unchanged", wire, c.request)
				return []byte(`{"id":"resp_passthrough","output":[],"sentinel":"preserve"}`), "application/json"
			})
			if result.status != 200 {
				t.Errorf("passthrough status=%d body=%s", result.status, result.body)
			}
			ngEqual(t, "passthrough response", ngDoc(t, result.body), map[string]any{"id": "resp_passthrough", "output": []any{}, "sentinel": "preserve"})
		})
	}
}

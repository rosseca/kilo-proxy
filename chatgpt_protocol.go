package main

// The subscription transport speaks Responses. These adapters keep execution of
// client tools in the original harness; no second agent executes those tools.
import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type chatGPTProtocol struct {
	path, model  string
	stream       bool
	includeUsage bool
}

func prepareChatGPTRequest(path string, body []byte) ([]byte, *chatGPTProtocol, error) {
	d, err := decodeObject(body)
	if err != nil {
		return nil, nil, err
	}
	p := &chatGPTProtocol{path: path, model: stringValue(d["model"])}
	p.stream, _ = d["stream"].(bool)
	p.includeUsage, _ = object(d["stream_options"])["include_usage"].(bool)
	if p.model == "" {
		return nil, nil, errors.New("model is required")
	}
	if path == "/v1/responses" {
		return body, p, nil
	}
	if path != "/v1/messages" && path != "/v1/chat/completions" {
		return nil, nil, errors.New("unsupported ChatGPT endpoint")
	}
	for _, key := range []string{"audio", "prediction", "logprobs", "top_logprobs", "functions", "function_call", "stop", "stop_sequences"} {
		if value, ok := d[key]; ok && value != nil && value != false {
			if list, ok := value.([]any); ok && len(list) == 0 {
				continue
			}
			return nil, nil, fmt.Errorf("ChatGPT adapter does not support %s", key)
		}
	}
	if management := object(d["context_management"]); management != nil {
		edits, _ := management["edits"].([]any)
		for _, raw := range edits {
			// Thinking cleanup is already satisfied when omitting Anthropic's
			// provider-specific signatures. Never discard actual conversation.
			if !strings.HasPrefix(stringValue(object(raw)["type"]), "clear_thinking_") {
				return nil, nil, errors.New("unsupported context_management edit")
			}
		}
	}
	if n, ok := d["n"]; ok && fmt.Sprint(n) != "1" {
		return nil, nil, errors.New("ChatGPT adapter supports only n=1")
	}
	if modalities, ok := d["modalities"].([]any); ok {
		for _, modality := range modalities {
			if modality != "text" {
				return nil, nil, errors.New("ChatGPT adapter supports only text output")
			}
		}
	}
	out := map[string]any{"model": p.model, "stream": true, "store": false, "instructions": ""}
	for _, key := range []string{"temperature", "top_p", "parallel_tool_calls", "service_tier"} {
		if v, ok := d[key]; ok {
			out[key] = v
		}
	}
	for _, key := range []string{"max_tokens", "max_completion_tokens"} {
		if v, ok := d[key]; ok {
			out["max_output_tokens"] = v
		}
	}
	if effort, ok := d["reasoning_effort"]; ok {
		out["reasoning"] = map[string]any{"effort": effort}
	}
	if thinking := object(d["thinking"]); thinking != nil {
		switch stringValue(thinking["type"]) {
		case "enabled", "adaptive":
			effort := "medium"
			if budget := cgNumber(thinking["budget_tokens"]); budget >= 10000 {
				effort = "high"
			} else if budget > 0 && budget < 4000 {
				effort = "low"
			}
			out["reasoning"] = map[string]any{"effort": effort}
		case "disabled":
			out["reasoning"] = map[string]any{"effort": "none"}
		default:
			return nil, nil, errors.New("unsupported thinking type")
		}
	}
	if config := object(d["output_config"]); config != nil {
		if effort, ok := config["effort"]; ok {
			out["reasoning"] = map[string]any{"effort": effort}
		}
		if f := object(config["format"]); f != nil {
			if f["type"] != "json_schema" {
				return nil, nil, errors.New("unsupported output_config format")
			}
			out["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "response", "schema": f["schema"], "strict": true}}
		}
	}
	if f := object(d["response_format"]); f != nil {
		switch stringValue(f["type"]) {
		case "text", "json_object":
			out["text"] = map[string]any{"format": f}
		case "json_schema":
			schema := object(f["json_schema"])
			if schema == nil {
				return nil, nil, errors.New("missing json_schema")
			}
			schema["type"] = "json_schema"
			out["text"] = map[string]any{"format": schema}
		default:
			return nil, nil, errors.New("unsupported response_format")
		}
	}
	var input []any
	if system, ok := d["system"]; ok {
		parts, err := cgContent(system, "developer", true)
		if err != nil {
			return nil, nil, fmt.Errorf("system: %w", err)
		}
		input = append(input, map[string]any{"role": "developer", "content": parts})
	}
	messages, ok := d["messages"].([]any)
	if !ok {
		return nil, nil, errors.New("messages must be an array")
	}
	for i, raw := range messages {
		m := object(raw)
		role := stringValue(m["role"])
		if role == "tool" {
			id := stringValue(m["tool_call_id"])
			if id == "" {
				return nil, nil, fmt.Errorf("messages.%d: tool_call_id is required", i)
			}
			result, err := cgToolResult(m["content"], false)
			if err != nil {
				return nil, nil, err
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": id, "output": result})
			continue
		}
		if role != "system" && role != "developer" && role != "user" && role != "assistant" {
			return nil, nil, fmt.Errorf("messages.%d: unsupported role", i)
		}
		if role == "system" {
			role = "developer"
		}
		var parts []any
		flush := func() {
			if len(parts) > 0 {
				input = append(input, map[string]any{"role": role, "content": parts})
				parts = nil
			}
		}
		if blocks, ok := m["content"].([]any); ok && path == "/v1/messages" {
			for _, rawBlock := range blocks {
				b := object(rawBlock)
				switch stringValue(b["type"]) {
				case "tool_use":
					flush()
					args, err := json.Marshal(b["input"])
					if err != nil || object(b["input"]) == nil || stringValue(b["id"]) == "" || stringValue(b["name"]) == "" {
						return nil, nil, errors.New("invalid tool_use block")
					}
					input = append(input, map[string]any{"type": "function_call", "call_id": b["id"], "name": b["name"], "arguments": string(args)})
				case "tool_result":
					flush()
					result, err := cgToolResult(b["content"], true)
					if err != nil || stringValue(b["tool_use_id"]) == "" {
						return nil, nil, errors.New("invalid tool_result block")
					}
					if failed, _ := b["is_error"].(bool); failed {
						result = cgErrorResult(result)
					}
					input = append(input, map[string]any{"type": "function_call_output", "call_id": b["tool_use_id"], "output": result})
				case "thinking", "redacted_thinking":
					// Anthropic signatures cannot be replayed to OpenAI. These are
					// reasoning annotations, never user content or tool output.
					continue
				default:
					converted, err := cgContent([]any{b}, role, true)
					if err != nil {
						return nil, nil, err
					}
					parts = append(parts, converted...)
				}
			}
		} else {
			var err error
			parts, err = cgContent(m["content"], role, false)
			if err != nil {
				return nil, nil, fmt.Errorf("messages.%d: %w", i, err)
			}
		}
		flush()
		if calls, ok := m["tool_calls"].([]any); ok {
			for _, rawCall := range calls {
				call := object(rawCall)
				fn := object(call["function"])
				args, valid := fn["arguments"].(string)
				if stringValue(call["type"]) != "function" || stringValue(call["id"]) == "" || stringValue(fn["name"]) == "" || !valid || !json.Valid([]byte(args)) {
					return nil, nil, errors.New("invalid function tool call")
				}
				input = append(input, map[string]any{"type": "function_call", "call_id": call["id"], "name": fn["name"], "arguments": args})
			}
		}
	}
	if input == nil {
		input = []any{}
	}
	out["input"] = input
	if tools, ok := d["tools"].([]any); ok {
		converted := make([]any, 0, len(tools))
		for _, raw := range tools {
			t := object(raw)
			if path == "/v1/chat/completions" {
				if stringValue(t["type"]) != "function" {
					return nil, nil, errors.New("only function tools are supported")
				}
				t = object(t["function"])
			} else if kind := stringValue(t["type"]); kind != "" && kind != "custom" {
				return nil, nil, errors.New("Anthropic server tools are not supported by ChatGPT")
			}
			if stringValue(t["name"]) == "" {
				return nil, nil, errors.New("tool name is required")
			}
			// Chat Completions and Anthropic tools are non-strict unless opted
			// in. Responses otherwise normalizes schemas into strict mode, which
			// can turn an optional client-tool argument into a required one.
			v := map[string]any{"type": "function", "name": t["name"], "strict": false}
			for _, key := range []string{"description", "strict", "parameters"} {
				if x, ok := t[key]; ok {
					v[key] = x
				}
			}
			if x, ok := t["input_schema"]; ok {
				v["parameters"] = x
			}
			converted = append(converted, v)
		}
		out["tools"] = converted
	}
	if choice, ok := d["tool_choice"]; ok {
		if c := object(choice); c != nil {
			kind := stringValue(c["type"])
			if path == "/v1/messages" {
				if v, ok := c["disable_parallel_tool_use"].(bool); ok {
					out["parallel_tool_calls"] = !v
				}
				switch kind {
				case "auto", "none":
					choice = kind
				case "any":
					choice = "required"
				case "tool":
					choice = map[string]any{"type": "function", "name": c["name"]}
				default:
					return nil, nil, errors.New("unsupported tool_choice")
				}
			} else if kind == "function" {
				choice = map[string]any{"type": "function", "name": object(c["function"])["name"]}
			} else {
				return nil, nil, errors.New("unsupported tool_choice")
			}
		}
		out["tool_choice"] = choice
	}
	b, err := json.Marshal(out)
	return b, p, err
}

func cgContent(value any, role string, anthropic bool) ([]any, error) {
	if value == nil {
		return nil, nil
	}
	textType := "input_text"
	if role == "assistant" {
		textType = "output_text"
	}
	if text, ok := value.(string); ok {
		return []any{map[string]any{"type": textType, "text": text}}, nil
	}
	blocks, ok := value.([]any)
	if !ok {
		return nil, errors.New("content must be a string or array")
	}
	parts := make([]any, 0, len(blocks))
	for _, raw := range blocks {
		b := object(raw)
		switch stringValue(b["type"]) {
		case "text":
			text, ok := b["text"].(string)
			if !ok {
				return nil, errors.New("text must be a string")
			}
			parts = append(parts, map[string]any{"type": textType, "text": text})
		case "image_url", "image":
			if role == "assistant" {
				return nil, errors.New("assistant image content is unsupported")
			}
			url, detail := "", ""
			if anthropic {
				s := object(b["source"])
				switch stringValue(s["type"]) {
				case "base64":
					url = "data:" + stringValue(s["media_type"]) + ";base64," + stringValue(s["data"])
				case "url":
					url = stringValue(s["url"])
				default:
					return nil, errors.New("unsupported image source")
				}
			} else {
				s := object(b["image_url"])
				url = stringValue(s["url"])
				detail = stringValue(s["detail"])
			}
			if url == "" {
				return nil, errors.New("image URL is required")
			}
			part := map[string]any{"type": "input_image", "image_url": url}
			if detail != "" {
				part["detail"] = detail
			}
			parts = append(parts, part)
		case "refusal":
			parts = append(parts, map[string]any{"type": "refusal", "refusal": b["refusal"]})
		default:
			return nil, fmt.Errorf("unsupported content type %q", stringValue(b["type"]))
		}
	}
	return parts, nil
}

func cgToolResult(v any, anthropic bool) (any, error) {
	if v == nil {
		return "", nil
	}
	if s, ok := v.(string); ok {
		return s, nil
	}
	return cgContent(v, "user", anthropic)
}
func cgErrorResult(v any) any {
	if s, ok := v.(string); ok {
		return "Tool error: " + s
	}
	return append([]any{map[string]any{"type": "input_text", "text": "Tool execution failed:"}}, v.([]any)...)
}
func cgNumber(v any) int64 {
	switch n := v.(type) {
	case json.Number:
		i, _ := n.Int64()
		return i
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

func cgUsage(response map[string]any, anthropic bool) map[string]any {
	u := object(response["usage"])
	input, output := cgNumber(u["input_tokens"]), cgNumber(u["output_tokens"])
	cached := cgNumber(object(u["input_tokens_details"])["cached_tokens"])
	if anthropic {
		return map[string]any{"input_tokens": max(int64(0), input-cached), "output_tokens": output, "cache_read_input_tokens": cached, "cache_creation_input_tokens": 0}
	}
	return map[string]any{"prompt_tokens": input, "completion_tokens": output, "total_tokens": input + output, "prompt_tokens_details": map[string]any{"cached_tokens": cached}, "completion_tokens_details": map[string]any{"reasoning_tokens": cgNumber(object(u["output_tokens_details"])["reasoning_tokens"])}}
}

func (p *chatGPTProtocol) result(response map[string]any) (map[string]any, error) {
	var text strings.Builder
	var calls, blocks []any
	outputs, _ := response["output"].([]any)
	for _, raw := range outputs {
		o := object(raw)
		switch stringValue(o["type"]) {
		case "message":
			content, _ := o["content"].([]any)
			for _, rawPart := range content {
				part := object(rawPart)
				if kind := stringValue(part["type"]); kind != "output_text" && kind != "refusal" {
					return nil, fmt.Errorf("unsupported upstream content type %q", kind)
				}
				value := stringValue(part["text"])
				if stringValue(part["type"]) == "refusal" {
					value = stringValue(part["refusal"])
				}
				text.WriteString(value)
				blocks = append(blocks, map[string]any{"type": "text", "text": value})
			}
		case "function_call":
			args := stringValue(o["arguments"])
			input, err := decodeObject([]byte(args))
			if err != nil {
				return nil, errors.New("upstream returned invalid tool arguments")
			}
			calls = append(calls, map[string]any{"id": o["call_id"], "type": "function", "function": map[string]any{"name": o["name"], "arguments": args}})
			blocks = append(blocks, map[string]any{"type": "tool_use", "id": o["call_id"], "name": o["name"], "input": input})
		case "reasoning": // Internal reasoning is not an Anthropic signed thinking block.
		default:
			return nil, fmt.Errorf("unsupported upstream output type %q", stringValue(o["type"]))
		}
	}
	stop := "stop"
	if len(calls) > 0 {
		stop = "tool_calls"
	}
	if stringValue(response["status"]) == "incomplete" {
		stop = "length"
	}
	if p.path == "/v1/messages" {
		if blocks == nil {
			blocks = []any{}
		}
		reason := "end_turn"
		if stop == "tool_calls" {
			reason = "tool_use"
		}
		if stop == "length" {
			reason = "max_tokens"
		}
		return map[string]any{"id": response["id"], "type": "message", "role": "assistant", "model": p.model, "content": blocks, "stop_reason": reason, "stop_sequence": nil, "usage": cgUsage(response, true)}, nil
	}
	message := map[string]any{"role": "assistant", "content": text.String()}
	if len(calls) > 0 {
		message["tool_calls"] = calls
		if text.Len() == 0 {
			message["content"] = nil
		}
	}
	return map[string]any{"id": response["id"], "object": "chat.completion", "created": cgCreated(response), "model": p.model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": stop}}, "usage": cgUsage(response, false)}, nil
}
func cgCreated(r map[string]any) int64 {
	if n := cgNumber(r["created_at"]); n != 0 {
		return n
	}
	return time.Now().Unix()
}

// Consume complete SSE frames, including multiline data and a final frame
// without a blank line. Bound each event instead of the whole conversation.
func cgReadSSE(reader io.Reader, visit func([]byte) error) error {
	s := bufio.NewScanner(reader)
	s.Buffer(make([]byte, 4096), bridgeLimit)
	var data []string
	size := 0
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		b := []byte(strings.Join(data, "\n"))
		data = nil
		size = 0
		if string(b) == "[DONE]" {
			return nil
		}
		return visit(b)
	}
	for s.Scan() {
		line := s.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		size += len(line)
		if size > bridgeLimit {
			return errors.New("ChatGPT SSE event too large")
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := s.Err(); err != nil {
		return err
	}
	return flush()
}

func (p *chatGPTProtocol) serve(w http.ResponseWriter, r *http.Request, response *http.Response) error {
	defer response.Body.Close()
	stop := context.AfterFunc(r.Context(), func() { _ = response.Body.Close() })
	defer stop()
	if route, ok := r.Context().Value(claudeDesktopAliasContextKey{}).(claudeDesktopAliasRoute); ok && route.Alias != "" {
		copy := *p
		copy.model = route.Alias
		p = &copy
	}
	if p.path == "/v1/responses" && p.stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Del("Content-Length")
		// Forward exact bytes (including custom tool events) while checking
		// that EOF really follows a terminal response. A dropped connection
		// must not count as a successful subscription request in Activity.
		complete := errors.New("ChatGPT terminal event consumed")
		err := cgReadSSE(io.TeeReader(response.Body, cgFlushingWriter{w}), func(raw []byte) error {
			if err := r.Context().Err(); err != nil {
				return err
			}
			event, err := decodeObject(raw)
			if err != nil {
				return err
			}
			terminal, err := cgTerminalEvent(event)
			if err != nil {
				return err
			}
			if terminal {
				return complete
			}
			return nil
		})
		if err == complete {
			return nil
		}
		if err != nil {
			return err
		}
		return errors.New("ChatGPT stream ended without a completed response")
	}
	s := &cgStream{p: p, w: w, blocks: map[string]*cgBlock{}, created: time.Now().Unix()}
	if p.stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Del("Content-Length")
	}
	var final map[string]any
	completedItems := map[int64]any{}
	completedSize := 0
	complete := errors.New("ChatGPT terminal event consumed")
	err := cgReadSSE(response.Body, func(raw []byte) error {
		if err := r.Context().Err(); err != nil {
			return err
		}
		event, err := decodeObject(raw)
		if err != nil {
			return err
		}
		if !p.stream && stringValue(event["type"]) == "response.output_item.done" {
			item := object(event["item"])
			if item == nil {
				return errors.New("missing completed output item")
			}
			index := int64(len(completedItems))
			if value, ok := event["output_index"]; ok {
				index = cgNumber(value)
				if index < 0 {
					return errors.New("invalid completed output index")
				}
			}
			completedSize += len(raw)
			if completedSize > bridgeLimit {
				return errors.New("ChatGPT buffered output exceeds 32 MiB")
			}
			completedItems[index] = item
		}
		terminal, err := cgTerminalEvent(event)
		if err != nil {
			return err
		}
		if terminal {
			final = object(event["response"])
		}
		if p.stream {
			if err := s.event(event); err != nil {
				return err
			}
		}
		if final != nil {
			return complete
		}
		return nil
	})
	if err == complete {
		err = nil
	}
	if err == nil && final == nil {
		err = errors.New("ChatGPT stream ended without a completed response")
	}
	if err != nil {
		if p.stream && r.Context().Err() == nil {
			_ = s.send("error", map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": err.Error()}})
		}
		return err
	}
	if p.stream {
		return nil
	}
	// The subscription backend can send the completed items separately and
	// leave terminal response.output empty. Rebuild only that missing output;
	// a populated terminal response is authoritative. Preserve opaque reasoning
	// and native custom tools for Responses clients, not just visible text.
	if output, _ := final["output"].([]any); len(output) == 0 && len(completedItems) > 0 {
		indexes := make([]int64, 0, len(completedItems))
		for index := range completedItems {
			indexes = append(indexes, index)
		}
		sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
		output = make([]any, 0, len(indexes))
		for _, index := range indexes {
			output = append(output, completedItems[index])
		}
		final["output"] = output
	}
	var result map[string]any
	if p.path == "/v1/responses" {
		result = final
	} else {
		result, err = p.result(final)
		if err != nil {
			return err
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Del("Content-Length")
	return json.NewEncoder(w).Encode(result)
}

type cgFlushingWriter struct{ w http.ResponseWriter }

func (f cgFlushingWriter) Write(data []byte) (int, error) {
	n, err := f.w.Write(data)
	if flusher, ok := f.w.(http.Flusher); ok {
		flusher.Flush()
	}
	return n, err
}

func cgTerminalEvent(event map[string]any) (bool, error) {
	kind := stringValue(event["type"])
	response := object(event["response"])
	status := stringValue(response["status"])
	terminal := kind == "response.completed" || kind == "response.incomplete" || kind == "response.done"
	if kind == "error" || kind == "response.failed" || kind == "response.cancelled" || (terminal && (status == "failed" || status == "cancelled")) {
		upstream := object(event["error"])
		if upstream == nil {
			upstream = object(response["error"])
		}
		message := stringValue(upstream["message"])
		if message == "" {
			message = stringValue(event["message"])
		}
		if message == "" {
			message = "ChatGPT response failed"
		}
		return false, errors.New(message)
	}
	if terminal && response == nil {
		return false, errors.New("missing terminal response")
	}
	return terminal, nil
}

type cgBlock struct {
	index          int
	kind, id, name string
	emitted        string
	closed         bool
}
type cgStream struct {
	p           *chatGPTProtocol
	w           http.ResponseWriter
	id          string
	created     int64
	started     bool
	next, tools int
	blocks      map[string]*cgBlock
}

func (s *cgStream) send(event string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	prefix := ""
	if s.p.path == "/v1/messages" {
		prefix = "event: " + event + "\n"
	}
	if _, err := fmt.Fprintf(s.w, "%sdata: %s\n\n", prefix, b); err != nil {
		return err
	}
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
func (s *cgStream) chat(delta any, finish any, usage any) error {
	v := map[string]any{"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.p.model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	if usage != nil {
		v["usage"] = usage
		v["choices"] = []any{}
	}
	return s.send("", v)
}
func (s *cgStream) start(response map[string]any) error {
	if s.started {
		return nil
	}
	s.started = true
	s.id = stringValue(response["id"])
	if s.id == "" {
		s.id = "chatcmpl-kilo"
	}
	if response != nil {
		s.created = cgCreated(response)
	}
	if s.p.path != "/v1/messages" {
		return s.chat(map[string]any{"role": "assistant", "content": ""}, nil, nil)
	}
	return s.send("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": s.id, "type": "message", "role": "assistant", "model": s.p.model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": cgUsage(response, true)}})
}
func (s *cgStream) block(key, kind string, item map[string]any) (*cgBlock, error) {
	if b := s.blocks[key]; b != nil {
		return b, nil
	}
	b := &cgBlock{index: s.next, kind: kind}
	s.next++
	if kind == "tool_use" {
		b.id = stringValue(item["call_id"])
		b.name = stringValue(item["name"])
		if s.p.path != "/v1/messages" {
			b.index = s.tools
		}
		s.tools++
	}
	s.blocks[key] = b
	if s.p.path != "/v1/messages" {
		if kind == "tool_use" {
			return b, s.chat(map[string]any{"tool_calls": []any{map[string]any{"index": b.index, "id": b.id, "type": "function", "function": map[string]any{"name": b.name, "arguments": ""}}}}, nil, nil)
		}
		return b, nil
	}
	content := map[string]any{"type": "text", "text": ""}
	if kind == "tool_use" {
		content = map[string]any{"type": "tool_use", "id": b.id, "name": b.name, "input": map[string]any{}}
	}
	return b, s.send("content_block_start", map[string]any{"type": "content_block_start", "index": b.index, "content_block": content})
}
func (s *cgStream) close(b *cgBlock) error {
	if b.closed {
		return nil
	}
	b.closed = true
	if s.p.path == "/v1/messages" {
		return s.send("content_block_stop", map[string]any{"type": "content_block_stop", "index": b.index})
	}
	return nil
}

func (s *cgStream) delta(b *cgBlock, value string) error {
	if value == "" {
		return nil
	}
	if b.closed {
		return errors.New("upstream emitted content after closing its block")
	}
	b.emitted += value
	if b.kind == "tool_use" {
		if s.p.path != "/v1/messages" {
			return s.chat(map[string]any{"tool_calls": []any{map[string]any{"index": b.index, "function": map[string]any{"arguments": value}}}}, nil, nil)
		}
		return s.send("content_block_delta", map[string]any{"type": "content_block_delta", "index": b.index, "delta": map[string]any{"type": "input_json_delta", "partial_json": value}})
	}
	if s.p.path != "/v1/messages" {
		return s.chat(map[string]any{"content": value}, nil, nil)
	}
	return s.send("content_block_delta", map[string]any{"type": "content_block_delta", "index": b.index, "delta": map[string]any{"type": "text_delta", "text": value}})
}

// Some compatible upstreams emit only the final output item. Recover content
// that has not already been streamed, without duplicating partial arguments.
func (s *cgStream) finishItem(item map[string]any) error {
	finish := func(key, kind, value string) error {
		b, err := s.block(key, kind, item)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(value, b.emitted) {
			return errors.New("upstream final content differs from its streamed content")
		}
		if err := s.delta(b, strings.TrimPrefix(value, b.emitted)); err != nil {
			return err
		}
		return s.close(b)
	}
	switch stringValue(item["type"]) {
	case "function_call":
		return finish(stringValue(item["id"]), "tool_use", stringValue(item["arguments"]))
	case "message":
		parts, _ := item["content"].([]any)
		for i, raw := range parts {
			part := object(raw)
			if kind := stringValue(part["type"]); kind != "output_text" && kind != "refusal" {
				return fmt.Errorf("unsupported upstream content type %q", kind)
			}
			value := stringValue(part["text"])
			if stringValue(part["type"]) == "refusal" {
				value = stringValue(part["refusal"])
			}
			if err := finish(stringValue(item["id"])+":"+fmt.Sprint(i), "text", value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *cgStream) event(e map[string]any) error {
	kind := stringValue(e["type"])
	if err := s.start(object(e["response"])); err != nil {
		return err
	}
	key := stringValue(e["item_id"])
	switch kind {
	case "response.output_item.added":
		i := object(e["item"])
		switch stringValue(i["type"]) {
		case "function_call":
			_, err := s.block(stringValue(i["id"]), "tool_use", i)
			return err
		case "message", "reasoning":
		default:
			return fmt.Errorf("unsupported upstream output type %q", stringValue(i["type"]))
		}
	case "response.output_text.delta", "response.refusal.delta":
		key += ":" + fmt.Sprint(e["content_index"])
		b, err := s.block(key, "text", nil)
		if err != nil {
			return err
		}
		return s.delta(b, stringValue(e["delta"]))
	case "response.function_call_arguments.delta":
		b := s.blocks[key]
		if b == nil {
			return errors.New("tool delta arrived before its tool call")
		}
		return s.delta(b, stringValue(e["delta"]))
	case "response.content_part.done":
		if b := s.blocks[key+":"+fmt.Sprint(e["content_index"])]; b != nil {
			return s.close(b)
		}
	case "response.output_item.done":
		i := object(e["item"])
		return s.finishItem(i)
	case "response.completed", "response.incomplete", "response.done":
		response := object(e["response"])
		if _, err := s.p.result(response); err != nil {
			return err
		}
		outputs, _ := response["output"].([]any)
		for _, output := range outputs {
			if err := s.finishItem(object(output)); err != nil {
				return err
			}
		}
		for index := 0; index < s.next; index++ {
			for _, b := range s.blocks {
				if b.index == index {
					if err := s.close(b); err != nil {
						return err
					}
				}
			}
		}
		stop := "stop"
		if s.tools > 0 {
			stop = "tool_calls"
		}
		if kind == "response.incomplete" || stringValue(response["status"]) == "incomplete" {
			stop = "length"
		}
		if s.p.path == "/v1/messages" {
			reason := "end_turn"
			if stop == "tool_calls" {
				reason = "tool_use"
			}
			if stop == "length" {
				reason = "max_tokens"
			}
			if err := s.send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": reason, "stop_sequence": nil}, "usage": cgUsage(response, true)}); err != nil {
				return err
			}
			return s.send("message_stop", map[string]any{"type": "message_stop"})
		}
		if err := s.chat(map[string]any{}, stop, nil); err != nil {
			return err
		}
		if s.p.includeUsage {
			if err := s.chat(nil, nil, cgUsage(response, false)); err != nil {
				return err
			}
		}
		_, err := io.WriteString(s.w, "data: [DONE]\n\n")
		if f, ok := s.w.(http.Flusher); ok {
			f.Flush()
		}
		return err
	}
	return nil
}

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
)

// This optional repair must not reject or buffer an unbounded gateway response.
const messagesCompatibilityLimit = 1 << 20

// Kilo sometimes returns end_turn alongside valid GLM client tool calls. Keep
// Messages requests and tools untouched; repair only a demonstrably complete
// response. Usage and upstream traces still observe the original gateway bytes.
func normalizeMessagesToolStop(r *http.Response) {
	if r.Request == nil || r.Request.Method != http.MethodPost || !strings.HasSuffix(r.Request.URL.Path, "/messages") || r.StatusCode < 200 || r.StatusCode >= 300 || r.Body == nil || r.Header.Get("Content-Encoding") != "" {
		return
	}
	kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if kind != "application/json" && kind != "text/event-stream" {
		return
	}
	original := r.Body
	reader, writer := io.Pipe()
	var once sync.Once
	var closeErr error
	closeOriginal := func() error {
		once.Do(func() { closeErr = original.Close() })
		return closeErr
	}
	ctx := r.Request.Context()
	stop := context.AfterFunc(ctx, func() {
		_ = writer.CloseWithError(ctx.Err())
		_ = closeOriginal()
	})
	r.Body = &bridgeReadCloser{Reader: reader, close: func() error {
		stop()
		_ = reader.Close()
		return closeOriginal()
	}}
	r.ContentLength = -1
	for _, name := range []string{"Content-Length", "ETag", "Content-MD5", "Digest"} {
		r.Header.Del(name)
	}
	go func() {
		defer stop()
		defer closeOriginal()
		var err error
		if kind == "text/event-stream" {
			err = repairMessagesSSE(ctx, original, writer)
		} else {
			err = repairMessagesJSON(ctx, original, writer)
		}
		_ = writer.CloseWithError(err)
	}()
}

func messagesInputObject(raw []byte) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && value != nil
}

// RawMessage preserves unknown fields, tool IDs, arguments and exact usage numbers.
func messagesStopJSON(raw []byte, nested bool) []byte {
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return raw
	}
	target := root
	if nested {
		target = nil
		if json.Unmarshal(root["delta"], &target) != nil || target == nil {
			return raw
		}
	}
	target["stop_reason"] = json.RawMessage(`"tool_use"`)
	if nested {
		root["delta"], _ = json.Marshal(target)
	}
	out, _ := json.Marshal(root)
	return out
}

func repairMessagesJSON(ctx context.Context, source io.Reader, destination io.Writer) error {
	raw, readErr := io.ReadAll(io.LimitReader(source, messagesCompatibilityLimit+1))
	var message struct {
		Kind    string `json:"type"`
		Stop    string `json:"stop_reason"`
		Content []struct {
			Type, ID, Name string
			Input          json.RawMessage
		}
	}
	if readErr == nil && len(raw) <= messagesCompatibilityLimit && ctx.Err() == nil && json.Unmarshal(raw, &message) == nil && message.Kind == "message" && message.Stop == "end_turn" {
		tools, valid := 0, true
		for _, block := range message.Content {
			if block.Type == "tool_use" {
				tools++
				valid = valid && block.ID != "" && block.Name != "" && messagesInputObject(block.Input)
			}
		}
		if tools > 0 && valid {
			raw = messagesStopJSON(raw, false)
		}
	}
	if _, err := destination.Write(raw); err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	_, err := io.Copy(destination, source)
	return err
}

type messagesToolInput struct {
	initial json.RawMessage
	partial []byte
	delta   bool
}

type messagesStreamState struct {
	started, invalid bool
	open             map[int]bool
	tools            map[int]*messagesToolInput
	argumentBytes    int
}

// Return a candidate only after every content block has closed and every client
// tool input is an object. One malformed or unfinished tool prevents the repair.
func (s *messagesStreamState) candidate(raw []byte) (kind string, candidate bool) {
	var event struct {
		Type  string
		Index *int
		Block struct {
			Type, ID, Name string
			Input          json.RawMessage
		} `json:"content_block"`
		Delta struct {
			Type    string
			Partial string `json:"partial_json"`
			Stop    string `json:"stop_reason"`
		}
	}
	if json.Unmarshal(raw, &event) != nil {
		s.invalid = true
		return "", false
	}
	kind = event.Type
	if s.invalid {
		return kind, false
	}
	switch kind {
	case "message_start":
		if s.started {
			s.invalid = true
		} else {
			s.started, s.open, s.tools = true, map[int]bool{}, map[int]*messagesToolInput{}
		}
	case "content_block_start", "content_block_delta", "content_block_stop":
		if !s.started || event.Index == nil || *event.Index < 0 {
			s.invalid = true
			break
		}
		index := *event.Index
		if kind == "content_block_start" {
			if s.open[index] || s.tools[index] != nil || len(s.open)+len(s.tools) >= 1024 {
				s.invalid = true
				break
			}
			s.open[index] = true
			if event.Block.Type == "tool_use" {
				if event.Block.ID == "" || event.Block.Name == "" || !messagesInputObject(event.Block.Input) {
					s.invalid = true
					break
				}
				s.argumentBytes += len(event.Block.Input)
				s.tools[index] = &messagesToolInput{initial: event.Block.Input}
			}
		} else if !s.open[index] {
			s.invalid = true
		} else if kind == "content_block_delta" {
			if tool := s.tools[index]; tool != nil {
				if event.Delta.Type != "input_json_delta" {
					s.invalid = true
					break
				}
				s.argumentBytes += len(event.Delta.Partial)
				if s.argumentBytes > messagesCompatibilityLimit {
					s.invalid = true
					break
				}
				tool.delta = true
				tool.partial = append(tool.partial, event.Delta.Partial...)
			}
		} else {
			delete(s.open, index)
			if tool := s.tools[index]; tool != nil {
				input := tool.initial
				if tool.delta {
					input = tool.partial
				}
				s.invalid = !messagesInputObject(input)
				tool.initial, tool.partial = nil, nil
			}
		}
	case "error":
		s.invalid = true
	case "message_delta":
		candidate = event.Delta.Stop == "end_turn" && s.started && len(s.open) == 0 && len(s.tools) > 0
	}
	if s.argumentBytes > messagesCompatibilityLimit {
		s.invalid = true
	}
	return kind, candidate && !s.invalid
}

func messagesSSEPayload(event []byte) []byte {
	var data []byte
	for _, line := range bytes.Split(event, []byte{'\n'}) {
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if bytes.HasPrefix(line, []byte("data:")) {
			if data != nil {
				data = append(data, '\n')
			}
			data = append(data, bytes.TrimPrefix(line[5:], []byte{' '})...)
		}
	}
	return data
}

func messagesSSEStop(event []byte) []byte {
	payload := messagesStopJSON(messagesSSEPayload(event), true)
	var out []byte
	written := false
	for _, line := range bytes.SplitAfter(event, []byte{'\n'}) {
		if !bytes.HasPrefix(line, []byte("data:")) {
			out = append(out, line...)
			continue
		}
		if !written {
			out = append(out, "data: "...)
			out = append(out, payload...)
			if bytes.HasSuffix(line, []byte("\r\n")) {
				out = append(out, '\r')
			}
			out = append(out, '\n')
			written = true
		}
	}
	return out
}

func repairMessagesSSE(ctx context.Context, source io.Reader, destination io.Writer) error {
	reader := bufio.NewReader(source)
	state := &messagesStreamState{}
	var event, held, following []byte
	write := func(data []byte) error { _, err := destination.Write(data); return err }
	flush := func(repair bool) error {
		if repair {
			held = messagesSSEStop(held)
		}
		if err := write(held); err != nil {
			return err
		}
		if err := write(following); err != nil {
			return err
		}
		held, following = nil, nil
		return nil
	}
	for {
		line, err := reader.ReadSlice('\n')
		event = append(event, line...)
		if len(event)+len(held)+len(following) > messagesCompatibilityLimit {
			if e := flush(false); e != nil {
				return e
			}
			if e := write(event); e != nil {
				return e
			}
			if err != nil && err != bufio.ErrBufferFull && err != io.EOF {
				return err
			}
			_, err = io.Copy(destination, reader)
			return err
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if e := flush(false); e != nil {
				return e
			}
			if e := write(event); e != nil {
				return e
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
		if !bytes.Equal(line, []byte{'\n'}) && !bytes.Equal(line, []byte{'\r', '\n'}) {
			continue
		}
		payload := messagesSSEPayload(event)
		kind, candidate := "", false
		if payload != nil {
			kind, candidate = state.candidate(payload)
		}
		if held != nil {
			if payload == nil || kind == "ping" {
				following = append(following, event...)
				event = nil
				continue
			}
			// Wait only for the terminal event. An error, cut stream or cancellation
			// after message_delta must retain the original stop reason.
			if err := flush(kind == "message_stop" && !state.invalid && ctx.Err() == nil); err != nil {
				return err
			}
			state.invalid = true
		} else if candidate {
			held, event = event, nil
			continue
		}
		if err := write(event); err != nil {
			return err
		}
		event = nil
	}
}

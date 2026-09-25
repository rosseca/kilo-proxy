package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
)

const claudeDesktopAliasBodyLimit = 32 << 20

type claudeDesktopAliasContextKey struct{}
type claudeDesktopAliasRoute struct{ Alias, Model string }

// Routes are derived from saved real IDs. An alias never changes destinations,
// and removed/disabled aliases fail locally instead of reaching another model.
func (a *app) resolveClaudeDesktopAlias(alias string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.config.ClaudeDesktopExperimentalModels {
		return "", errors.New("Experimental Claude Desktop models are disabled. Enable them in Claude Desktop integration settings and prepare its profile again.")
	}
	paths, err := a.claudeDesktopPaths()
	if err != nil {
		return "", err
	}
	selection, err := a.readClaudeDesktopSelection(paths)
	if err != nil {
		return "", errors.New("Prepare the experimental Claude Desktop profile before using its models.")
	}
	for _, model := range selection.Models {
		if !claudeDesktopModelSupported(model.ID) && claudeDesktopAlias(model.ID) == alias {
			return model.ID, nil
		}
	}
	return "", errors.New("This experimental Claude Desktop model is no longer selected. Restore it in your library and prepare Claude Desktop again.")
}

// This runs after the incoming trace reader, before image/schema adapters and
// upstream usage capture. Tool schemas, prompts and cached content remain exact.
func (a *app) prepareClaudeDesktopAlias(r *http.Request) (*http.Request, error) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" || r.Body == nil {
		return r, nil
	}
	raw, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		return r, err
	}
	restore := func(body []byte) {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.GetBody = nil
		r.ContentLength = int64(len(body))
		r.Header.Del("Content-Length")
	}
	restore(raw)
	var request map[string]json.RawMessage
	if json.Unmarshal(raw, &request) != nil || request == nil {
		return r, nil
	} // Preserve existing native-model error handling.
	var alias string
	if json.Unmarshal(request["model"], &alias) != nil || !strings.HasPrefix(alias, claudeDesktopAliasPrefix) {
		return r, nil
	}
	// Only modified requests require strict unique JSON keys. Ambiguous model or
	// tool fields must not acquire a different meaning during a compatibility edit.
	if _, err := decodeClaudeDesktopObject(raw); err != nil {
		return r, errors.New("Invalid experimental Claude Desktop request JSON.")
	}
	model, err := a.resolveClaudeDesktopAlias(alias)
	if err != nil {
		return r, err
	}
	request["model"], _ = json.Marshal(model)
	encoded, err := json.Marshal(request)
	if err != nil {
		return r, err
	}
	r = r.Clone(context.WithValue(r.Context(), claudeDesktopAliasContextKey{}, claudeDesktopAliasRoute{alias, model}))
	restore(encoded)
	if observer, _ := r.Context().Value(usageContextKey{}).(*usageObserver); observer != nil {
		observer.mu.Lock()
		observer.usage.Model = model
		observer.mu.Unlock()
	}
	return r, nil
}

func claudeDesktopAliasMessage(raw []byte, alias string, event bool) ([]byte, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || root == nil {
		return nil, errors.New("Invalid Messages response from the gateway.")
	}
	target := root
	if event {
		var kind string
		_ = json.Unmarshal(root["type"], &kind)
		if kind != "message_start" {
			return raw, nil
		}
		target = nil
		if json.Unmarshal(root["message"], &target) != nil || target == nil {
			return nil, errors.New("Invalid Messages start event from the gateway.")
		}
	}
	if _, exists := target["model"]; !exists {
		return raw, nil
	}
	target["model"], _ = json.Marshal(alias)
	if event {
		root["message"], _ = json.Marshal(target)
	}
	return json.Marshal(root)
}

// Upstream tracing/accounting is installed before ModifyResponse, so it retains
// the real provider ID and exact usage. Only the client-facing ID uses an alias.
func adaptClaudeDesktopAliasResponse(r *http.Response) error {
	if r.Request == nil {
		return nil
	}
	route, ok := r.Request.Context().Value(claudeDesktopAliasContextKey{}).(claudeDesktopAliasRoute)
	if !ok || r.StatusCode < 200 || r.StatusCode >= 300 || r.Body == nil {
		return nil
	}
	if r.Header.Get("Content-Encoding") != "" {
		return errors.New("Compressed experimental Messages responses are not supported.")
	}
	kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if kind != "application/json" && kind != "text/event-stream" {
		return errors.New("Unsupported experimental Messages response content type.")
	}
	original := r.Body
	reader, writer := io.Pipe()
	var once sync.Once
	var closeErr error
	closeOriginal := func() error { once.Do(func() { closeErr = original.Close() }); return closeErr }
	ctx := r.Request.Context()
	stop := context.AfterFunc(ctx, func() { _ = writer.CloseWithError(ctx.Err()); _ = closeOriginal() })
	r.Body = &bridgeReadCloser{Reader: reader, close: func() error { stop(); _ = reader.Close(); return closeOriginal() }}
	r.ContentLength = -1
	for _, header := range []string{"Content-Length", "ETag", "Content-MD5", "Digest"} {
		r.Header.Del(header)
	}
	go func() {
		defer stop()
		defer closeOriginal()
		var err error
		if kind == "text/event-stream" {
			err = rewriteClaudeDesktopAliasSSE(original, writer, route.Alias)
		} else {
			var raw []byte
			raw, err = io.ReadAll(io.LimitReader(original, claudeDesktopAliasBodyLimit+1))
			if err == nil && len(raw) > claudeDesktopAliasBodyLimit {
				err = errors.New("Experimental Messages response exceeds 32 MiB.")
			}
			if err == nil {
				raw, err = claudeDesktopAliasMessage(raw, route.Alias, false)
			}
			if err == nil {
				_, err = writer.Write(raw)
			}
		}
		_ = writer.CloseWithError(err)
	}()
	return nil
}

func rewriteClaudeDesktopAliasSSE(source io.Reader, destination io.Writer, alias string) error {
	reader := bufio.NewReader(source)
	var event []byte
	for {
		line, err := reader.ReadSlice('\n')
		event = append(event, line...)
		if len(event) > claudeDesktopAliasBodyLimit {
			return fmt.Errorf("Messages event exceeds %d bytes", claudeDesktopAliasBodyLimit)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if len(event) > 0 {
				if _, writeErr := destination.Write(event); writeErr != nil {
					return writeErr
				}
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
		var envelope struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(payload, &envelope) == nil && envelope.Type == "message_start" {
			replacement, err := claudeDesktopAliasMessage(payload, alias, true)
			if err != nil {
				return err
			}
			if !bytes.Equal(replacement, payload) {
				var out []byte
				written := false
				for _, part := range bytes.SplitAfter(event, []byte{'\n'}) {
					if !bytes.HasPrefix(part, []byte("data:")) {
						out = append(out, part...)
						continue
					}
					if !written {
						out = append(out, "data: "...)
						out = append(out, replacement...)
						if bytes.HasSuffix(part, []byte{'\r', '\n'}) {
							out = append(out, '\r')
						}
						out = append(out, '\n')
						written = true
					}
				}
				event = out
			}
		}
		if _, err := destination.Write(event); err != nil {
			return err
		}
		event = nil
	}
}

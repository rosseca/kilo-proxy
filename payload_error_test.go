package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPayloadErrorProxyPreservesLargeRequestAndOriginalTrace(t *testing.T) {
	for _, kind := range []string{"text", "image"} {
		t.Run(kind, func(t *testing.T) {
			part := map[string]any{"type": "input_text", "text": strings.Repeat("x", 5<<20)}
			if kind == "image" {
				part = map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + strings.Repeat("a", 5<<20)}
			}
			payload, err := json.Marshal(map[string]any{"model": "vendor/model", "input": []any{map[string]any{"role": "user", "content": []any{part}}}})
			if err != nil {
				t.Fatal(err)
			}
			const upstreamBody = "Request Entity Too Large\nFUNCTION_PAYLOAD_TOO_LARGE\ncdg1::synthetic-request-id"
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				got, readErr := io.ReadAll(r.Body)
				if readErr != nil || !bytes.Equal(got, payload) || r.ContentLength != int64(len(payload)) {
					t.Error("large request context was changed, truncated, or measured incorrectly")
				}
				if r.URL.Path != "/api/gateway/responses" || r.Header.Get("Authorization") != "Bearer synthetic-upstream" {
					t.Error("unexpected request route/authentication")
				}
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("Content-Length", strconv.Itoa(len(upstreamBody)))
				w.Header().Set("Digest", "sha-256=original-digest")
				w.Header().Set("X-Vercel-Id", "cdg1::synthetic-request-id")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				_, _ = io.WriteString(w, upstreamBody)
			}))
			defer upstream.Close()
			a := testApp(t)
			setUpstream(a, upstream.URL)
			r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/responses", bytes.NewReader(payload))
			r.Header.Set("Authorization", "Bearer synthetic-local")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			a.inferenceHandler("synthetic-upstream", "synthetic-org", "synthetic-local", "127.0.0.1:8877").ServeHTTP(w, r)
			var response struct {
				Error struct {
					Code, Type, Message string
					RequestBytes        int64 `json:"request_bytes"`
				}
			}
			if w.Code != 413 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Error.Code != "upstream_payload_too_large" || response.Error.Type != "invalid_request_error" || response.Error.RequestBytes != int64(len(payload)) {
				t.Fatalf("missing actionable413: %d %s", w.Code, w.Body.String())
			}
			for _, text := range []string{"4.5 MB", "base64", "Compact", "new conversation", "reduce attachments", "Retrying the identical request will not help", strconv.Itoa(len(payload))} {
				if !strings.Contains(response.Error.Message, text) {
					t.Errorf("missing guidance %q: %s", text, response.Error.Message)
				}
			}
			if strings.Contains(w.Body.String(), "context_length_exceeded") || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Digest") != "" || w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) {
				t.Fatal("replacement has misleading error type or stale entity headers")
			}
			if calls.Load() != 1 || a.requests != 1 || a.failures != 1 || a.active != 0 || a.activeTraces != 0 || len(a.events) != 1 || a.events[0].Status != 413 || a.usageTotal.Priced != 0 {
				t.Fatalf("request was replayed or counted incorrectly: calls=%d requests=%d failures=%d events=%+v usage=%+v", calls.Load(), a.requests, a.failures, a.events, a.usageTotal)
			}
			trace := a.traces[a.events[0].ID]
			if trace == nil || trace.UpstreamStatus != 413 || trace.UpstreamResponse.Body != upstreamBody || trace.UpstreamResponse.Bytes != int64(len(upstreamBody)) || trace.UpstreamResponse.Headers.Get("Digest") != "sha-256=original-digest" || trace.UpstreamResponse.Headers.Get("Content-Type") != "text/plain" {
				t.Fatalf("original upstream error was lost or observed twice: %+v", trace)
			}
			if trace.Response.Body != w.Body.String() || trace.Response.Headers.Get("Content-Type") != "application/json" || trace.Request.Bytes != int64(len(payload)) || trace.UpstreamRequest.Bytes != int64(len(payload)) {
				t.Fatal("original and client traces were mixed or request byte counts changed")
			}
		})
	}
}

type payloadCountBody struct {
	reader       io.Reader
	read, closes int
	closeErr     error
}

func (b *payloadCountBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}
func (b *payloadCountBody) Close() error { b.closes++; return b.closeErr }

func TestPayloadErrorBoundedPassthroughAndClose(t *testing.T) {
	for _, tc := range []struct {
		name, data, header string
		status             int
		length             int64
		maxPeek            int
	}{
		{"unrelated success", vercelPayloadError, "", 200, int64(len(vercelPayloadError)), 0},
		{"unknown413", "another provider's custom413", "", 413, -1, payloadErrorPeekLimit + 1},
		{"unknown error header", "another provider error", "SOMETHING_ELSE", 413, -1, payloadErrorPeekLimit + 1},
		{"large declared error", vercelPayloadError + strings.Repeat("x", payloadErrorPeekLimit*2), vercelPayloadError, 413, int64(payloadErrorPeekLimit*2 + len(vercelPayloadError)), 0},
		{"large unknown length error", vercelPayloadError + strings.Repeat("x", payloadErrorPeekLimit*2), vercelPayloadError, 413, -1, payloadErrorPeekLimit + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closeErr := errors.New("synthetic close error")
			original := &payloadCountBody{reader: strings.NewReader(tc.data), closeErr: closeErr}
			response := &http.Response{StatusCode: tc.status, Body: original, ContentLength: tc.length, Header: http.Header{"Content-Type": {"text/plain"}, "Digest": {"original"}, "X-Vercel-Error": {tc.header}}, Trailer: http.Header{"X-Original-Trailer": {"value"}}}
			headers, trailers := response.Header.Clone(), response.Trailer.Clone()
			normalizeUpstreamPayloadError(response)
			if original.read > tc.maxPeek || original.closes != 0 || response.ContentLength != tc.length || !reflect.DeepEqual(response.Header, headers) || !reflect.DeepEqual(response.Trailer, trailers) {
				t.Fatal("peek exceeded bound or changed passthrough response metadata/lifecycle")
			}
			data, err := io.ReadAll(response.Body)
			if err != nil || string(data) != tc.data || original.read != len(tc.data) {
				t.Fatal("passthrough lost, duplicated, or changed bytes")
			}
			if err = response.Body.Close(); err != closeErr || original.closes != 1 {
				t.Fatal("passthrough did not preserve original close/error")
			}
		})
	}
}

type payloadInterruptedReader struct {
	err  error
	sent bool
	tail *strings.Reader
}

func (r *payloadInterruptedReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, vercelPayloadError), r.err
	}
	return r.tail.Read(p)
}

func TestPayloadErrorPeekPreservesReadError(t *testing.T) {
	readErr := errors.New("synthetic upstream read interruption")
	original := &payloadCountBody{reader: &payloadInterruptedReader{err: readErr, tail: strings.NewReader("remaining bytes")}}
	response := &http.Response{StatusCode: 413, Body: original, ContentLength: -1, Header: http.Header{"Content-Type": {"text/plain"}}}
	normalizeUpstreamPayloadError(response)
	data, err := io.ReadAll(response.Body)
	if string(data) != vercelPayloadError || err != readErr || original.closes != 0 || response.ContentLength != -1 {
		t.Fatalf("peek lost the partial-body error: %q/%v", data, err)
	}
	data, err = io.ReadAll(response.Body)
	if err != nil || string(data) != "remaining bytes" {
		t.Fatal("passthrough lost bytes after the original read interruption")
	}
	_ = response.Body.Close()
	if original.closes != 1 {
		t.Fatal("original response was not closed exactly once")
	}
}

func TestPayloadErrorHeaderRecognitionAndEntityHeaders(t *testing.T) {
	original := &payloadCountBody{reader: strings.NewReader("encoded synthetic error")}
	response := &http.Response{StatusCode: 413, Body: original, ContentLength: -1, TransferEncoding: []string{"chunked"}, Uncompressed: true, Trailer: http.Header{"Digest": {"old"}}, Header: http.Header{
		"X-Vercel-Error": {vercelPayloadError}, "Content-Type": {"text/plain"}, "Content-Encoding": {"gzip"}, "Content-Length": {"999"}, "Transfer-Encoding": {"chunked"}, "Trailer": {"Digest"}, "Etag": {"old"}, "Content-Md5": {"old"}, "Digest": {"old"}, "Content-Digest": {"old"}, "Repr-Digest": {"old"}, "Content-Range": {"old"}, "X-Request-Id": {"preserved"},
	}}
	normalizeUpstreamPayloadError(response)
	data, err := io.ReadAll(response.Body)
	if err != nil || !json.Valid(data) || !bytes.Contains(data, []byte("upstream_payload_too_large")) || bytes.Contains(data, []byte("request_bytes")) || original.closes != 1 || response.ContentLength != int64(len(data)) || response.Uncompressed || len(response.TransferEncoding) != 0 || response.Trailer != nil {
		t.Fatal("known header was not normalized with an independent JSON body")
	}
	for _, name := range []string{"Content-Encoding", "Transfer-Encoding", "Trailer", "ETag", "Content-MD5", "Digest", "Content-Digest", "Repr-Digest", "Content-Range"} {
		if response.Header.Get(name) != "" {
			t.Errorf("stale entity header %s", name)
		}
	}
	if response.Header.Get("X-Request-Id") != "preserved" || response.Header.Get("Content-Type") != "application/json" || response.Header.Get("Content-Length") != strconv.Itoa(len(data)) {
		t.Fatal("unrelated headers lost or replacement length incorrect")
	}
	_ = response.Body.Close()
	if original.closes != 1 {
		t.Fatal("replacement close closed upstream twice")
	}
}

func TestPayloadErrorProxyUnrelatedResponsesPassThrough(t *testing.T) {
	for _, status := range []int{200, 400, 413, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			data := `{"error":{"message":"Other provider-specific error"}}`
			if status == 200 {
				data = `{"output":"FUNCTION_PAYLOAD_TOO_LARGE is a code example"}`
			}
			original := &payloadCountBody{reader: strings.NewReader(data)}
			a := testApp(t)
			a.transport = usageMemoryTransport(func(r *http.Request) (*http.Response, error) {
				_, _ = io.Copy(io.Discard, r.Body)
				return &http.Response{StatusCode: status, Request: r, Body: original, ContentLength: int64(len(data)), Header: http.Header{"Content-Type": {"application/json"}, "Content-Length": {strconv.Itoa(len(data))}, "Digest": {"original"}}}, nil
			})
			r := httptest.NewRequest("POST", "http://127.0.0.1:8877/v1/responses", strings.NewReader(`{"model":"vendor/model"}`))
			r.Header.Set("Authorization", "Bearer synthetic-local")
			w := httptest.NewRecorder()
			a.inferenceHandler("synthetic-upstream", "synthetic-org", "synthetic-local", "127.0.0.1:8877").ServeHTTP(w, r)
			if w.Code != status || w.Body.String() != data || w.Header().Get("Digest") != "original" || w.Header().Get("Content-Length") != strconv.Itoa(len(data)) || original.closes != 1 || original.read != len(data) {
				t.Fatalf("unrelated response changed: status=%d bytes=%d reads=%d closes=%d", w.Code, w.Body.Len(), original.read, original.closes)
			}
			trace := a.traces[a.events[0].ID]
			if trace.UpstreamResponse.Body != data || trace.Response.Body != data || trace.UpstreamResponse.Bytes != int64(len(data)) {
				t.Fatal("passthrough trace repeated the peek or changed content")
			}
		})
	}
}

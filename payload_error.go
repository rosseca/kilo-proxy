package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const payloadErrorPeekLimit = 16 << 10
const vercelPayloadError = "FUNCTION_PAYLOAD_TOO_LARGE"

// A failed peek must preserve its bytes, the read error, and the original
// closer. In particular, MultiReader alone would lose a non-EOF peek error.
type payloadReplayBody struct {
	prefix   *bytes.Reader
	err      error
	original io.ReadCloser
}

func (b *payloadReplayBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.prefix.Len() > 0 {
		return b.prefix.Read(p)
	}
	if b.err != nil {
		err := b.err
		b.err = nil
		return 0, err
	}
	return b.original.Read(p)
}

func (b *payloadReplayBody) Close() error { return b.original.Close() }

// traceTransport has already wrapped the upstream body and captured its
// original status/headers. Read through that wrapper so Activity retains the
// original error while the client receives separate, actionable JSON.
func normalizeUpstreamPayloadError(response *http.Response) {
	if response.StatusCode != http.StatusRequestEntityTooLarge || response.Body == nil || response.ContentLength > payloadErrorPeekLimit {
		return
	}
	original := response.Body
	body, err := io.ReadAll(io.LimitReader(original, payloadErrorPeekLimit+1))
	known := strings.TrimSpace(response.Header.Get("X-Vercel-Error")) == vercelPayloadError
	// Do not interpret compressed bytes as an error message. A verified error
	// header is sufficient even when the bounded original body is encoded.
	if encoding := response.Header.Get("Content-Encoding"); encoding == "" || strings.EqualFold(encoding, "identity") {
		known = known || bytes.Contains(body, []byte(vercelPayloadError))
	}
	if err != nil || len(body) > payloadErrorPeekLimit || !known {
		response.Body = &payloadReplayBody{prefix: bytes.NewReader(body), err: err, original: original}
		return
	}
	_ = original.Close()
	message := "Kilo's upstream gateway rejected this request because it exceeds its 4.5 MB request-body limit."
	problem := map[string]any{"code": "upstream_payload_too_large", "type": "invalid_request_error"}
	if response.Request != nil && response.Request.ContentLength > 0 {
		problem["request_bytes"] = response.Request.ContentLength
		message += fmt.Sprintf(" Outbound request size: %d bytes.", response.Request.ContentLength)
	}
	message += " Large images or base64 data retained in conversation history can cause this. Compact the conversation if your client supports it, or start a new conversation and reduce attachments. Retrying the identical request will not help."
	problem["message"] = message
	data, _ := json.Marshal(map[string]any{"error": problem})
	response.Body = io.NopCloser(bytes.NewReader(data))
	response.ContentLength = int64(len(data))
	response.TransferEncoding = nil
	response.Trailer = nil
	response.Uncompressed = false
	if response.Header == nil {
		response.Header = make(http.Header)
	}
	for _, name := range []string{"Content-Encoding", "Content-Length", "Transfer-Encoding", "Trailer", "ETag", "Content-MD5", "Digest", "Content-Digest", "Repr-Digest", "Content-Range"} {
		response.Header.Del(name)
	}
	response.Header.Set("Content-Type", "application/json")
	response.Header.Set("Content-Length", fmt.Sprint(len(data)))
}

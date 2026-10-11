package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const traceBodyLimit = 128 << 10
const traceCountLimit = 30
const activeTraceLimit = 16

type tracePart struct {
	HeadersTruncated bool        `json:"headersTruncated"`
	Headers          http.Header `json:"headers"`
	Body             string      `json:"body"`
	Bytes            int64       `json:"bytes"`
	Truncated        bool        `json:"truncated"`
}
type requestTrace struct {
	ImageTransport   *imageTransportTrace `json:"imageTransport,omitempty"`
	Error            string               `json:"error,omitempty"`
	ID               string               `json:"id"`
	Request          tracePart            `json:"request"`
	UpstreamRequest  tracePart            `json:"upstreamRequest"`
	UpstreamResponse tracePart            `json:"upstreamResponse"`
	Response         tracePart            `json:"response"`
	UpstreamStatus   int                  `json:"upstreamStatus"`
}
type traceBuffer struct {
	mu       sync.Mutex
	disabled bool
	omitted  bool
	data     []byte
	total    int64
	limit    int
}

func (b *traceBuffer) write(data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.disabled {
		return
	}
	b.total += int64(len(data))
	if b.omitted {
		return
	}
	if remaining := b.limit - len(b.data); remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		b.data = append(b.data, data...)
	}
}

func (b *traceBuffer) discard() {
	b.mu.Lock()
	defer b.mu.Unlock()
	clear(b.data)
	b.data = nil
	b.total = 0
	b.disabled = true
}

type traceReader struct {
	io.ReadCloser
	buffer *traceBuffer
}

func (r *traceReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.buffer.write(p[:n])
	return n, err
}

type traceCapture struct {
	imageTransport                                      *imageTransportTrace
	mu                                                  sync.Mutex
	disabled                                            bool
	traceError                                          string
	request, upRequest, upResponse, response            traceBuffer
	requestHeaders, upRequestHeaders, upResponseHeaders http.Header
	upstreamStatus                                      int
	secrets                                             []string
}

// Disabling capture releases both completed traces and the copies held by
// requests still in flight. Readers already installed in the pipeline become
// no-ops, including when capture is enabled again before the request finishes.
func (c *traceCapture) discard() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disabled = true
	c.requestHeaders, c.upRequestHeaders, c.upResponseHeaders = nil, nil, nil
	c.traceError = ""
	c.imageTransport = nil
	for _, b := range []*traceBuffer{&c.request, &c.upRequest, &c.upResponse, &c.response} {
		b.discard()
	}
}

func (c *traceCapture) setError(message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disabled {
		return
	}
	c.traceError = c.redact(message)
	if len(c.traceError) > 2048 {
		c.traceError = c.traceError[:2048]
	}
}

func (c *traceCapture) upstreamRequest(r *http.Request, captureBody bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disabled {
		return
	}
	c.upRequestHeaders = r.Header.Clone()
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	c.upRequestHeaders.Set("Host", host)
	if captureBody && r.Body != nil {
		r.Body = &traceReader{r.Body, &c.upRequest}
	}
}

func (c *traceCapture) upstreamResponse(response *http.Response, captureBody bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disabled {
		return
	}
	c.upstreamStatus = response.StatusCode
	c.upResponseHeaders = response.Header.Clone()
	if captureBody && response.Body != nil {
		response.Body = &traceReader{response.Body, &c.upResponse}
	}
}

type traceContextKey struct{}

func newTraceCapture(r *http.Request, secrets []string) *traceCapture {
	longest := 0
	for _, secret := range secrets {
		if len(secret) > longest {
			longest = len(secret)
		}
	}
	// Keep an overlap so a credential crossing the visible truncation boundary
	// can still be redacted in full before the visible prefix is extracted.
	limit := traceBodyLimit + longest
	c := &traceCapture{secrets: secrets, requestHeaders: r.Header.Clone()}
	c.requestHeaders.Set("Host", r.Host)
	c.request.limit = limit
	c.upRequest.limit = limit
	c.upResponse.limit = limit
	c.response.limit = limit
	return c
}
func (c *traceCapture) redact(value string) string {
	for _, secret := range c.secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}

// New credentials can be introduced by adapters after the incoming body was
// read. Expand the overlap before any upstream/response bytes are captured.
func (c *traceCapture) addSecrets(values ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disabled {
		return
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		escaped, _ := json.Marshal(value)
		for _, secret := range []string{value, string(escaped[1 : len(escaped)-1])} {
			c.secrets = append(c.secrets, secret)
			for _, buffer := range []*traceBuffer{&c.request, &c.upRequest, &c.upResponse, &c.response} {
				buffer.mu.Lock()
				if limit := traceBodyLimit + len(secret); buffer.limit < limit {
					buffer.limit = limit
				}
				buffer.mu.Unlock()
			}
		}
	}
}

// A model may echo a signed URL across separate streaming delta events. Whole
// string redaction cannot safely remove those fragments; omit response bodies
// for requests using uploaded images, retaining byte counts/status/headers.
func (c *traceCapture) omitImageUploadResponses() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, buffer := range []*traceBuffer{&c.upResponse, &c.response} {
		buffer.mu.Lock()
		clear(buffer.data)
		buffer.data = nil
		buffer.omitted = true
		buffer.mu.Unlock()
	}
}
func (c *traceCapture) part(headers http.Header, b *traceBuffer) tracePart {
	safe := make(http.Header)
	headerBudget := 32 << 10
	headersTruncated := false
	for name, values := range headers {
		lower := strings.ToLower(name)
		sensitive := strings.Contains(lower, "authorization") || strings.Contains(lower, "cookie") || strings.Contains(lower, "token") || strings.Contains(lower, "key") || strings.Contains(lower, "secret") || strings.Contains(lower, "password")
		for _, value := range values {
			if sensitive {
				value = "[REDACTED]"
			} else {
				value = c.redact(value)
			}
			if cost := len(name) + len(value); cost > headerBudget {
				headersTruncated = true
				if headerBudget <= len(name) {
					continue
				}
				value = strings.Clone(value[:headerBudget-len(name)])
			}
			headerBudget -= len(name) + len(value)
			safe.Add(name, value)
		}
	}
	b.mu.Lock()
	raw, total, omitted := string(b.data), b.total, b.omitted
	b.mu.Unlock()
	if omitted {
		return tracePart{HeadersTruncated: headersTruncated, Headers: safe, Body: "[Response body omitted: temporary image links may contain access credentials.]", Bytes: total}
	}
	value := strings.ToValidUTF8(c.redact(raw), "�")
	truncated := total > int64(traceBodyLimit)
	if len(value) > traceBodyLimit {
		value = value[:traceBodyLimit]
	}
	for !utf8.ValidString(value) && len(value) > 0 {
		value = value[:len(value)-1]
	}
	return tracePart{HeadersTruncated: headersTruncated, Headers: safe, Body: value, Bytes: total, Truncated: truncated}
}
func (c *traceCapture) finish(id string, headers http.Header) *requestTrace {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disabled {
		return nil
	}
	return &requestTrace{ImageTransport: c.imageTransportSnapshot(), Error: c.redact(c.traceError), ID: id, Request: c.part(c.requestHeaders, &c.request), UpstreamRequest: c.part(c.upRequestHeaders, &c.upRequest), UpstreamResponse: c.part(c.upResponseHeaders, &c.upResponse), Response: c.part(headers, &c.response), UpstreamStatus: c.upstreamStatus}
}

type traceTransport struct{ base http.RoundTripper }

func (t traceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c, _ := r.Context().Value(traceContextKey{}).(*traceCapture)
	if c != nil {
		c.upstreamRequest(r, true)
	}
	response, err := t.base.RoundTrip(r)
	if c != nil && response != nil {
		c.upstreamResponse(response, true)
	}
	if u, _ := r.Context().Value(usageContextKey{}).(*usageObserver); u != nil && response != nil && response.Body != nil {
		u.configure(response)
		response.Body = &usageReader{response.Body, u}
	}
	return response, err
}

func (a *app) activityDetail(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	id := strings.TrimPrefix(r.URL.Path, "/api/activity/")
	detail := a.traces[id]
	a.mu.Unlock()
	if detail == nil {
		jsonError(w, 404, "Details unavailable: capture is off or the entry expired.")
		return
	}
	jsonResponse(w, 200, detail)
}
func (a *app) activityConfig(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	if input.Enabled == nil {
		jsonError(w, http.StatusBadRequest, "Choose whether to enable request capture.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg := a.config
	cfg.CaptureActivity = *input.Enabled
	if err := writeSettings(a.dir, cfg); err != nil {
		jsonError(w, http.StatusInternalServerError, "Could not save request capture. Check the configuration folder permissions.")
		return
	}
	a.config = cfg
	if a.captureEnabled != *input.Enabled {
		a.activityEpoch++
	}
	a.captureEnabled = *input.Enabled
	if !a.captureEnabled {
		a.discardActivityLocked()
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

func (a *app) discardActivityLocked() {
	a.events = nil
	a.traces = make(map[string]*requestTrace)
	for capture := range a.activeCaptures {
		capture.discard()
	}
}

func (a *app) clearActivity(w http.ResponseWriter) {
	a.mu.Lock()
	a.discardActivityLocked()
	a.activityEpoch++
	a.mu.Unlock()
	jsonResponse(w, 200, map[string]bool{"ok": true})
}
func (a *app) beginActivity(r *http.Request, key, localKey string) (string, uint64, *traceCapture) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextEventID++
	id := strconv.FormatUint(a.nextEventID, 10)
	a.active++
	if !a.captureEnabled || a.activeTraces >= activeTraceLimit {
		return id, a.activityEpoch, nil
	}
	a.activeTraces++
	capture := newTraceCapture(r, []string{key, localKey, a.adminToken})
	if a.activeCaptures == nil {
		a.activeCaptures = make(map[*traceCapture]struct{})
	}
	a.activeCaptures[capture] = struct{}{}
	return id, a.activityEpoch, capture
}

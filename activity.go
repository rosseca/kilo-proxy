package main

import (
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
	Error            string    `json:"error,omitempty"`
	ID               string    `json:"id"`
	Request          tracePart `json:"request"`
	UpstreamRequest  tracePart `json:"upstreamRequest"`
	UpstreamResponse tracePart `json:"upstreamResponse"`
	Response         tracePart `json:"response"`
	UpstreamStatus   int       `json:"upstreamStatus"`
}
type traceBuffer struct {
	mu    sync.Mutex
	data  []byte
	total int64
	limit int
}

func (b *traceBuffer) write(data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += int64(len(data))
	if remaining := b.limit - len(b.data); remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		b.data = append(b.data, data...)
	}
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
	traceError                                          string
	request, upRequest, upResponse, response            traceBuffer
	requestHeaders, upRequestHeaders, upResponseHeaders http.Header
	upstreamStatus                                      int
	secrets                                             []string
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
	raw, total := string(b.data), b.total
	b.mu.Unlock()
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
	return &requestTrace{Error: c.redact(c.traceError), ID: id, Request: c.part(c.requestHeaders, &c.request), UpstreamRequest: c.part(c.upRequestHeaders, &c.upRequest), UpstreamResponse: c.part(c.upResponseHeaders, &c.upResponse), Response: c.part(headers, &c.response), UpstreamStatus: c.upstreamStatus}
}

type traceTransport struct{ base http.RoundTripper }

func (t traceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c, _ := r.Context().Value(traceContextKey{}).(*traceCapture)
	if c != nil {
		c.upRequestHeaders = r.Header.Clone()
		host := r.Host
		if host == "" {
			host = r.URL.Host
		}
		c.upRequestHeaders.Set("Host", host)
		if r.Body != nil {
			r.Body = &traceReader{r.Body, &c.upRequest}
		}
	}
	response, err := t.base.RoundTrip(r)
	if c != nil && response != nil {
		c.upstreamStatus = response.StatusCode
		c.upResponseHeaders = response.Header.Clone()
		if response.Body != nil {
			response.Body = &traceReader{response.Body, &c.upResponse}
		}
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
		jsonError(w, 404, "Details unavailable: capture was paused or the entry expired.")
		return
	}
	jsonResponse(w, 200, detail)
}
func (a *app) activityConfig(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	a.mu.Lock()
	a.captureEnabled = input.Enabled
	a.mu.Unlock()
	jsonResponse(w, 200, map[string]bool{"ok": true})
}
func (a *app) clearActivity(w http.ResponseWriter) {
	a.mu.Lock()
	a.events = nil
	a.traces = make(map[string]*requestTrace)
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
	return id, a.activityEpoch, newTraceCapture(r, []string{key, localKey, a.adminToken})
}

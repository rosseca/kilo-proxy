package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const usageBufferLimit = 1 << 20
const usageSessionLimit = 200

type requestUsage struct {
	Session    string  `json:"session"`
	Source     string  `json:"source"`
	Model      string  `json:"model,omitempty"`
	Input      *int64  `json:"input,omitempty"`
	Output     *int64  `json:"output,omitempty"`
	Cached     *int64  `json:"cached,omitempty"`
	CacheWrite *int64  `json:"cacheWrite,omitempty"`
	Reasoning  *int64  `json:"reasoning,omitempty"`
	CostUSD    *string `json:"costUSD,omitempty"`
	CostSource string  `json:"costSource,omitempty"`
	Complete   bool    `json:"complete"`
	Limited    bool    `json:"limited"`
	costNanos  int64
}

type usageSummary struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Source     string `json:"source"`
	OrgID      string `json:"orgId,omitempty"`
	Requests   int64  `json:"requests"`
	Priced     int64  `json:"priced"`
	WithTokens int64  `json:"withTokens"`
	Incomplete int64  `json:"incomplete"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	Cached     int64  `json:"cached"`
	CacheWrite int64  `json:"cacheWrite"`
	Reasoning  int64  `json:"reasoning"`
	CostUSD    string `json:"costUSD"`
	Updated    string `json:"updated"`
	costNanos  int64
}

type usageObserver struct {
	mu            sync.Mutex
	eventDropped  bool
	usage         requestUsage
	label, org    string
	sse           bool
	buffer, event []byte
	dropping      bool
	ended         bool
}
type usageContextKey struct{}

func newUsageObserver(r *http.Request, org string) *usageObserver {
	source, label, identity := "unassigned", "Unassigned requests", "unassigned"
	for _, candidate := range []struct{ header, source, label string }{
		{"Thread-Id", "codex-thread", "Codex task"},
		{"Session-Id", "client-session", "Client session"},
		{"Session_id", "client-session", "Client session"},
		{"X-KiloCode-TaskId", "kilo-task", "Kilo task"},
		{"X-Kilo-Local-Session", "custom-session", "Client session"},
	} {
		value := r.Header.Get(candidate.header)
		if value == "" || len(value) > 128 || strings.IndexFunc(value, func(c rune) bool {
			return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._:-", c))
		}) >= 0 {
			continue
		}
		source, identity = candidate.source, candidate.source+":"+value
		short := value
		if len(short) > 16 {
			short = short[:16] + "…"
		}
		label = candidate.label + " " + short
		break
	}
	digest := sha256.Sum256([]byte(org + "\x00" + identity))
	return &usageObserver{usage: requestUsage{Session: hex.EncodeToString(digest[:]), Source: source}, label: label, org: org}
}

func (u *usageObserver) configure(r *http.Response) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.sse = strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "text/event-stream")
}

// Only observe copies of bytes already read by the proxy. Never delay or rewrite traffic.
func (u *usageObserver) feed(data []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.sse {
		if len(u.buffer)+len(data) > usageBufferLimit {
			u.usage.Limited = true
			u.buffer = nil
			u.dropping = true
		}
		if !u.dropping {
			u.buffer = append(u.buffer, data...)
		}
		return
	}
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		part := data
		if i >= 0 {
			part = data[:i]
		}
		if len(u.buffer)+len(part) > usageBufferLimit {
			u.dropping = true
			u.buffer = nil
			u.usage.Limited = true
		}
		if !u.dropping {
			u.buffer = append(u.buffer, part...)
		}
		if i < 0 {
			return
		}
		if u.dropping {
			u.eventDropped = true
			u.event = nil
		} else {
			u.line(bytes.TrimSuffix(u.buffer, []byte{'\r'}))
		}
		u.buffer = nil
		u.dropping = false
		data = data[i+1:]
	}
}
func (u *usageObserver) line(line []byte) {
	if len(line) == 0 {
		if len(u.event) > 0 && !u.eventDropped {
			u.parse(u.event)
			u.event = nil
		}
		u.eventDropped = false
		u.event = nil
		return
	}
	if u.eventDropped {
		return
	}
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	part := bytes.TrimPrefix(line, []byte("data:"))
	part = bytes.TrimPrefix(part, []byte(" "))
	if len(u.event)+len(part)+1 > usageBufferLimit {
		u.usage.Limited = true
		u.event = nil
		u.eventDropped = true
		return
	}
	if len(u.event) > 0 {
		u.event = append(u.event, '\n')
	}
	u.event = append(u.event, part...)
}
func (u *usageObserver) eof() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.ended {
		return
	}
	u.ended = true
	if !u.sse && !u.dropping {
		u.parse(u.buffer)
	}
	// An SSE event without its terminating blank line is incomplete, not a final usage record.
	u.buffer = nil
	u.event = nil
}
func intValue(v any) *int64 {
	n, ok := v.(json.Number)
	if !ok {
		return nil
	}
	i, err := n.Int64()
	if err != nil || i < 0 || i > 1e15 {
		return nil
	}
	return &i
}
func usageObject(v any) map[string]any { m, _ := v.(map[string]any); return m }
func replaceInt(dst **int64, v any) {
	if n := intValue(v); n != nil {
		*dst = n
	}
}

// Accumulate money as integer nanodollars, never binary floating-point sums.
func money(v any, micro bool) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok || len(n) > 64 {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(n), 64)
	limit := float64(1e6)
	if micro {
		limit = 1e12
	}
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > limit {
		return 0, false
	}
	// Bound exponent work before asking big.Rat to parse an untrusted decimal.
	if i := strings.IndexAny(string(n), "eE"); i >= 0 {
		e, err := strconv.Atoi(string(n)[i+1:])
		if err != nil || e < -18 || e > 18 {
			return 0, false
		}
	}
	rat, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return 0, false
	}
	scale := int64(1e9)
	if micro {
		scale = 1000
	}
	rat.Mul(rat, big.NewRat(scale, 1))
	whole, rest := new(big.Int), new(big.Int)
	whole.QuoRem(rat.Num(), rat.Denom(), rest)
	if new(big.Int).Mul(rest, big.NewInt(2)).Cmp(rat.Denom()) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	if !whole.IsInt64() {
		return 0, false
	}
	return whole.Int64(), true
}
func dollars(n int64) string {
	return strconv.FormatInt(n/1e9, 10) + "." + leftPad(strconv.FormatInt(n%1e9, 10), 9)
}
func leftPad(s string, n int) string { return strings.Repeat("0", n-len(s)) + s }

func (u *usageObserver) parse(data []byte) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		u.usage.Complete = true
		return
	}
	var root map[string]any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if d.Decode(&root) != nil {
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return
	}
	kind, _ := root["type"].(string)
	payload := root
	if response := usageObject(root["response"]); response != nil {
		payload = response
	}
	if message := usageObject(root["message"]); message != nil && kind == "message_start" {
		payload = message
	}
	if model, ok := payload["model"].(string); ok && len(model) <= 256 {
		u.usage.Model = model
	}
	usage := usageObject(payload["usage"])
	if usage != nil {
		replaceInt(&u.usage.Input, usage["input_tokens"])
		replaceInt(&u.usage.Input, usage["prompt_tokens"])
		replaceInt(&u.usage.Output, usage["output_tokens"])
		replaceInt(&u.usage.Output, usage["completion_tokens"])
		replaceInt(&u.usage.Cached, usage["cache_read_input_tokens"])
		replaceInt(&u.usage.Cached, usage["cache_hit_tokens"])
		replaceInt(&u.usage.Cached, usageObject(usage["input_tokens_details"])["cached_tokens"])
		replaceInt(&u.usage.Cached, usageObject(usage["prompt_tokens_details"])["cached_tokens"])
		replaceInt(&u.usage.CacheWrite, usage["cache_creation_input_tokens"])
		replaceInt(&u.usage.CacheWrite, usage["cache_write_tokens"])
		replaceInt(&u.usage.Reasoning, usageObject(usage["output_tokens_details"])["reasoning_tokens"])
		replaceInt(&u.usage.Reasoning, usageObject(usage["completion_tokens_details"])["reasoning_tokens"])
		// Prefer Kilo's explicitly denominated field; never substitute BYOK provider charges.
		cost, source, ok := int64(0), "", false
		if v, present := usage["cost_microdollars"]; present {
			cost, ok = money(v, true)
			source = "usage.cost_microdollars"
		} else if v, present := usage["cost"]; present {
			cost, ok = money(v, false)
			source = "usage.cost"
		}
		if ok {
			u.usage.costNanos = cost
			value := dollars(cost)
			u.usage.CostUSD = &value
			u.usage.CostSource = source
		}
	}
	if (!u.sse && root != nil) || kind == "response.completed" || kind == "response.incomplete" || kind == "message_stop" {
		u.usage.Complete = true
	}
	if kind == "error" || root["error"] != nil {
		u.usage.Complete = false
	}
}

type usageReader struct {
	io.ReadCloser
	observer *usageObserver
}

func (r *usageReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.observer.feed(p[:n])
	if err == io.EOF {
		r.observer.eof()
	}
	return n, err
}

func (s *usageSummary) add(u *usageObserver) {
	s.Requests++
	if !u.usage.Complete || u.usage.Limited {
		s.Incomplete++
	}
	if u.usage.Input != nil || u.usage.Output != nil {
		s.WithTokens++
	}
	for _, pair := range []struct {
		dst *int64
		src *int64
	}{{&s.Input, u.usage.Input}, {&s.Output, u.usage.Output}, {&s.Cached, u.usage.Cached}, {&s.CacheWrite, u.usage.CacheWrite}, {&s.Reasoning, u.usage.Reasoning}} {
		if pair.src != nil && *pair.dst <= math.MaxInt64-*pair.src {
			*pair.dst += *pair.src
		}
	}
	if u.usage.CostUSD != nil && s.costNanos <= math.MaxInt64-u.usage.costNanos {
		s.Priced++
		s.costNanos += u.usage.costNanos
	}
	s.CostUSD = dollars(s.costNanos)
	s.Updated = time.Now().UTC().Format(time.RFC3339Nano)
}
func (a *app) recordUsage(u *usageObserver) {
	if u == nil {
		return
	}
	a.usageTotal.add(u)
	if a.usageSessions == nil {
		a.usageSessions = make(map[string]*usageSummary)
	}
	id := u.usage.Session
	if a.usageSessions[id] == nil && len(a.usageSessions) >= usageSessionLimit {
		id = "overflow"
	}
	s := a.usageSessions[id]
	if s == nil {
		s = &usageSummary{ID: id, Label: u.label, Source: u.usage.Source, OrgID: u.org}
		if id == "overflow" {
			s.Label = "Other sessions"
			s.Source = "overflow"
			s.OrgID = ""
		}
		a.usageSessions[id] = s
	}
	s.add(u)
}
func (a *app) usageSnapshot() map[string]any {
	sessions := make([]usageSummary, 0, len(a.usageSessions))
	for _, s := range a.usageSessions {
		sessions = append(sessions, *s)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Updated > sessions[j].Updated })
	total := a.usageTotal
	if total.CostUSD == "" {
		total.CostUSD = "0.000000000"
	}
	return map[string]any{"total": total, "sessions": sessions}
}

func (u *usageObserver) snapshot() *usageObserver {
	u.mu.Lock()
	defer u.mu.Unlock()
	return &usageObserver{usage: u.usage, label: u.label, org: u.org}
}

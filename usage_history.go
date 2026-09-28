package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const historyFileLimit = 16 << 20
const historyDayLimit = 366
const historyScopeLimit = 64

// Daily aggregates deliberately contain no prompts, responses, headers, model
// names, conversation identifiers or API keys. Scope hashes separate credentials
// and teams without retaining either credential or raw team ID on disk.
type usageHistoryDay struct {
	Date string `json:"date"`
	usageSummary
}
type usageHistoryScope struct {
	StartedAt string            `json:"startedAt"`
	Days      []usageHistoryDay `json:"days"`
}
type usageHistoryDisk struct {
	SchemaVersion int                          `json:"schemaVersion"`
	Scopes        map[string]usageHistoryScope `json:"scopes"`
}
type usageHistorySnapshot struct {
	Today     usageSummary      `json:"today"`
	Yesterday usageSummary      `json:"yesterday"`
	Last7Days usageSummary      `json:"last7Days"`
	Days      []usageHistoryDay `json:"days"`
	Timezone  string            `json:"timezone"`
	StartedAt string            `json:"startedAt,omitempty"`
	Error     string            `json:"error,omitempty"`
}
type usageHistoryStore struct {
	mu      sync.Mutex
	dir     string
	disk    usageHistoryDisk
	blocked bool
	warning string
	dirty   bool
	done    chan struct{}
	write   func(string, []byte) error
}

func usageAccountID(key, org string) string {
	if key == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(key + "\x00" + org))
	return hex.EncodeToString(digest[:])
}

func newUsageHistoryStore(dir string) *usageHistoryStore {
	s := &usageHistoryStore{dir: dir, disk: usageHistoryDisk{SchemaVersion: 1, Scopes: make(map[string]usageHistoryScope)}, write: atomicCatalogFile}
	path := filepath.Join(dir, "usage-history.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return s
	}
	if err == nil && info.Mode().IsRegular() && info.Size() <= historyFileLimit {
		var file *os.File
		file, err = os.Open(path)
		if err == nil {
			var data []byte
			data, err = io.ReadAll(io.LimitReader(file, historyFileLimit+1))
			file.Close()
			var disk usageHistoryDisk
			if err == nil && len(data) <= historyFileLimit && json.Unmarshal(data, &disk) == nil && validUsageHistory(&disk) {
				s.disk = disk
				return s
			}
		}
	}
	// Never overwrite unreadable history with a plausible-looking empty total.
	s.blocked = true
	s.warning = "Saved usage history could not be read. Existing files are unchanged; only new in-memory totals are available."
	return s
}

func historyCounters(s *usageSummary) []*int64 {
	return []*int64{&s.SubscriptionRequests, &s.Prompt, &s.WithPrompt, &s.WithCacheRead, &s.WithCacheWrite, &s.CacheRatioRequests, &s.CacheRatioInput, &s.CacheRatioRead, &s.Requests, &s.Priced, &s.WithTokens, &s.Incomplete, &s.Input, &s.Output, &s.Cached, &s.CacheWrite, &s.Reasoning}
}
func validUsageHistory(disk *usageHistoryDisk) bool {
	if disk.SchemaVersion != 1 || disk.Scopes == nil || len(disk.Scopes) > historyScopeLimit {
		return false
	}
	for id, scope := range disk.Scopes {
		hash, err := hex.DecodeString(id)
		if err != nil || len(hash) != sha256.Size || len(scope.Days) > historyDayLimit {
			return false
		}
		if _, err := time.Parse(time.RFC3339Nano, scope.StartedAt); err != nil {
			return false
		}
		seen := map[string]bool{}
		for i := range scope.Days {
			d := &scope.Days[i]
			if _, err := time.Parse("2006-01-02", d.Date); err != nil || seen[d.Date] {
				return false
			}
			seen[d.Date] = true
			for _, n := range historyCounters(&d.usageSummary) {
				if *n < 0 {
					return false
				}
			}
			n, err := strconv.ParseInt(strings.Replace(d.CostUSD, ".", "", 1), 10, 64)
			ok := err == nil && n >= 0 && dollars(n) == d.CostUSD
			if !ok || d.Priced > d.Requests || d.WithTokens > d.Requests || d.Incomplete > d.Requests || d.ID != "" || d.Label != "" || d.Source != "" || d.OrgID != "" || d.LastCache != nil {
				return false
			}
			d.costNanos = n
		}
		disk.Scopes[id] = scope
	}
	return true
}

func (s *usageHistoryStore) record(account string, u *usageObserver, now time.Time) {
	if s == nil || account == "" || u == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scope, exists := s.disk.Scopes[account]
	if !exists {
		if len(s.disk.Scopes) >= historyScopeLimit {
			s.warning = "Usage history has reached its account limit; new accounts are not saved."
			return
		}
		scope.StartedAt = now.UTC().Format(time.RFC3339Nano)
	}
	date := now.Format("2006-01-02")
	index := -1
	for i := range scope.Days {
		if scope.Days[i].Date == date {
			index = i
			break
		}
	}
	if index == -1 {
		scope.Days = append(scope.Days, usageHistoryDay{Date: date})
		index = len(scope.Days) - 1
	}
	scope.Days[index].add(u)
	scope.Days[index].LastCache = nil
	scope.Days[index].Updated = now.UTC().Format(time.RFC3339Nano)
	sort.Slice(scope.Days, func(i, j int) bool { return scope.Days[i].Date > scope.Days[j].Date })
	scope.Days = scope.Days[:min(len(scope.Days), historyDayLimit)]
	s.disk.Scopes[account] = scope
	if s.blocked {
		return
	}
	s.dirty = true
	if s.done == nil {
		s.done = make(chan struct{})
		go s.save()
	}
}

// Disk writes never hold the proxy mutex or delay response streaming. A single
// writer coalesces concurrent completions and atomically replaces the file.
func (s *usageHistoryStore) save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { close(s.done); s.done = nil }()
	for s.dirty {
		s.dirty = false
		data, err := json.MarshalIndent(s.disk, "", "  ")
		if len(data) > historyFileLimit {
			s.warning = "Usage history reached its file size limit. The last saved file is unchanged; current totals remain in memory."
			return
		}
		s.mu.Unlock()
		if err == nil {
			err = os.MkdirAll(s.dir, 0700)
		}
		if err == nil {
			err = s.write(filepath.Join(s.dir, "usage-history.json"), data)
		}
		s.mu.Lock()
		if err != nil {
			s.warning = "Usage history could not be saved. Current totals are in memory; check the configuration folder permissions."
			return
		}
		s.warning = ""
	}
}

func (s *usageHistoryStore) flush() {
	if s == nil {
		return
	}
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

func mergeHistorySummary(total *usageSummary, part usageSummary) {
	dst, src := historyCounters(total), historyCounters(&part)
	for i := range dst {
		if *dst[i] <= math.MaxInt64-*src[i] {
			*dst[i] += *src[i]
		}
	}
	if total.costNanos <= math.MaxInt64-part.costNanos {
		total.costNanos += part.costNanos
	}
	total.CostUSD = dollars(total.costNanos)
	if part.Updated > total.Updated {
		total.Updated = part.Updated
	}
}

func (s *usageHistoryStore) snapshot(account string, now time.Time) usageHistorySnapshot {
	empty := usageSummary{CostUSD: "0.000000000"}
	result := usageHistorySnapshot{Today: empty, Yesterday: empty, Last7Days: empty, Days: []usageHistoryDay{}, Timezone: "Local time (at request completion)"}
	if s == nil {
		return result
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result.Error = s.warning
	scope := s.disk.Scopes[account]
	result.StartedAt = scope.StartedAt
	today, yesterday := now.Format("2006-01-02"), now.AddDate(0, 0, -1).Format("2006-01-02")
	start := now.AddDate(0, 0, -6).Format("2006-01-02")
	for _, day := range scope.Days {
		result.Days = append(result.Days, day)
		if day.Date == today {
			result.Today = day.usageSummary
		}
		if day.Date == yesterday {
			result.Yesterday = day.usageSummary
		}
		if day.Date >= start && day.Date <= today {
			mergeHistorySummary(&result.Last7Days, day.usageSummary)
		}
	}
	return result
}

// Caller holds a.mu; the store has its own short-lived lock.
func (a *app) usageHistorySnapshotLocked() usageHistorySnapshot {
	return a.usageHistory.snapshot(usageAccountID(a.apiKey, a.config.OrgID), time.Now())
}

// Closing the listener cancels requests but does not join handler goroutines.
// Give their accounting defers time to run before flushing on stop/quit. Keep
// shutdown bounded even if a provider ignores cancellation.
func (a *app) drainUsageHistory() {
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		active := a.active
		a.mu.Unlock()
		if active == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.usageHistory.flush()
}

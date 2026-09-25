package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReleaseVersionComparison(t *testing.T) {
	for _, item := range []struct {
		a, b string
		want int
	}{
		{"0.9.0", "0.10.0", -1}, {"0.50.0", "0.49.9", 1},
		{"0.50.0-alpha.9", "0.50.0", -1}, {"0.50.0", "0.50.0-rc.1", 1},
		{"v0.50.0+build.1", "0.50.0+build.999", 0},
		{"0.50.0-alpha.9", "0.50.0-alpha.10", -1},
		{"0.50.0-alpha", "0.50.0-alpha.1", -1},
		{"0.50.0-1", "0.50.0-alpha", -1}, {"0.50.0-beta", "0.50.0-alpha", 1},
		{"999999999999999999999.0.0", "99999999999999999999.0.0", 1},
		{"1.0.999999999999999999999", "1.0.99999999999999999999", 1},
		{"1.0.0-999999999999999999999", "1.0.0-99999999999999999999", 1},
	} {
		t.Run(item.a+"_vs_"+item.b, func(t *testing.T) {
			a, validA := parseReleaseVersion(item.a)
			b, validB := parseReleaseVersion(item.b)
			if !validA || !validB || compareReleaseVersions(a, b) != item.want || compareReleaseVersions(b, a) != -item.want {
				t.Fatalf("comparison %s vs %s: valid=%v/%v, got %d, want %d", item.a, item.b, validA, validB, compareReleaseVersions(a, b), item.want)
			}
		})
	}
	for _, invalid := range []string{"", "dev", "unknown", "0.50", "0.50.00", "00.50.0", "0.50.0-01", "0.50.0-alpha..1", "0.50.0+", "v0.50.0/elsewhere", "v0.50.0?foo=bar", " 0.50.0", strings.Repeat("1", 129) + ".0.0"} {
		if _, valid := parseReleaseVersion(invalid); valid {
			t.Errorf("accepted invalid version %q", invalid)
		}
	}
}

func TestReleaseUpdateURLValidation(t *testing.T) {
	for _, address := range []string{
		releaseUpdatePagePrefix + "v0.50.0", releaseUpdatePagePrefix + "v1.20.300",
	} {
		if !validReleaseUpdateURL(address) {
			t.Errorf("rejected valid URL %q", address)
		}
	}
	for _, address := range []string{
		"http://github.com/rosseca/kilo-proxy/releases/tag/v0.50.0",
		"https://github.com.evil.test/rosseca/kilo-proxy/releases/tag/v0.50.0",
		"https://github.com/elsewhere/kilo-proxy/releases/tag/v0.50.0",
		"https://github.com@evil.test/rosseca/kilo-proxy/releases/tag/v0.50.0",
		releaseUpdatePagePrefix + "v0.50.0-alpha.1", releaseUpdatePagePrefix + "v0.50.0+build.1",
		releaseUpdatePagePrefix + "0.50.0", releaseUpdatePagePrefix + "v0.050.0",
		releaseUpdatePagePrefix + "v0.50.0?next=evil", releaseUpdatePagePrefix + "v0.50.0#elsewhere",
		releaseUpdatePagePrefix + "%76%30%2e50.0", releaseUpdatePagePrefix + "v0.50.0/../../elsewhere",
	} {
		if validReleaseUpdateURL(address) {
			t.Errorf("accepted unsafe release URL %q", address)
		}
	}
}

func releaseCheckerForTest(t *testing.T, current string, handler http.HandlerFunc) *releaseUpdateChecker {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := newReleaseUpdateChecker(current)
	c.endpoint = server.URL + "/repos/rosseca/kilo-proxy/releases/latest"
	t.Cleanup(c.close)
	return c
}

func waitReleaseCheck(t *testing.T, c *releaseUpdateChecker) releaseUpdateState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state := c.snapshot()
		if !state.Checking {
			if state.CheckedAt == "" {
				t.Fatal("check finished without a timestamp")
			}
			if _, err := time.Parse(time.RFC3339, state.CheckedAt); err != nil {
				t.Fatalf("invalid timestamp %q: %v", state.CheckedAt, err)
			}
			return state
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("release check did not finish")
	return releaseUpdateState{}
}

func TestReleaseUpdateAvailableAndReadOnlySnapshot(t *testing.T) {
	for _, item := range []struct {
		current string
		want    bool
	}{
		{"0.9.0", true}, {"0.49.9", true}, {"0.50.0-alpha.9", true},
		{"0.50.0", false}, {"0.50.0+local.1", false}, {"0.51.0-alpha.1", false}, {"1.0.0", false},
	} {
		t.Run(item.current, func(t *testing.T) {
			var calls atomic.Int32
			c := releaseCheckerForTest(t, item.current, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.URL.Path != "/repos/rosseca/kilo-proxy/releases/latest" || r.URL.RawQuery != "" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				for _, name := range []string{"Authorization", "Cookie", "X-KiloCode-OrganizationId", "X-Api-Key", "Proxy-Authorization"} {
					if r.Header.Get(name) != "" {
						t.Errorf("update request leaked %s", name)
					}
				}
				if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") != "Kilo-Proxy-Update-Check" || r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
					t.Error("missing public GitHub request headers")
				}
				io.WriteString(w, `{"tag_name":"v0.50.0","draft":false,"prerelease":false,"html_url":"https://evil.test/untrusted"}`)
			})
			initial := c.snapshot()
			if initial.Checking || initial.Available || initial.CheckedAt != "" || initial.Error != "" || initial.LatestVersion != "" || calls.Load() != 0 {
				t.Fatalf("construction or snapshot initiated a check: %+v", initial)
			}
			if state := c.check(); !state.Checking {
				t.Fatalf("check must return asynchronously: %+v", state)
			}
			state := waitReleaseCheck(t, c)
			if state.Error != "" || state.Available != item.want || state.LatestVersion != "0.50.0" || state.ReleaseURL != releaseUpdatePagePrefix+"v0.50.0" || calls.Load() != 1 {
				t.Fatalf("unexpected update state: %+v, calls=%d", state, calls.Load())
			}
		})
	}
}

func TestReleaseUpdateErrorsAreSafeAndNeverUpToDate(t *testing.T) {
	for _, item := range []struct {
		name   string
		status int
		body   string
	}{
		{"draft", 200, `{"tag_name":"v0.50.0","draft":true}`},
		{"prerelease", 200, `{"tag_name":"v0.50.0","prerelease":true}`},
		{"preview-tag", 200, `{"tag_name":"v0.50.0-alpha.1"}`},
		{"unprefixed-tag", 200, `{"tag_name":"0.50.0"}`},
		{"metadata-tag", 200, `{"tag_name":"v0.50.0+build.1"}`},
		{"malicious-tag", 200, `{"tag_name":"../../evil"}`},
		{"null", 200, `null`}, {"no-tag", 200, `{}`},
		{"invalid-json", 200, "sensitive-server-details"}, {"extra-json", 200, `{"tag_name":"v0.50.0"}{}`},
		{"oversized", 200, strings.Repeat(" ", releaseUpdateMaxBodyBytes+1)},
		{"rate-limited", 429, "sensitive-server-details"}, {"forbidden", 403, "sensitive-server-details"},
		{"no-releases", 404, "sensitive-server-details"}, {"server-error", 500, "sensitive-server-details"},
	} {
		t.Run(item.name, func(t *testing.T) {
			c := releaseCheckerForTest(t, "0.49.0", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(item.status)
				io.WriteString(w, item.body)
			})
			c.check()
			state := waitReleaseCheck(t, c)
			if state.Error == "" || state.Available || state.LatestVersion != "" || state.ReleaseURL != "" || strings.Contains(state.Error, "sensitive") || strings.Contains(state.Error, c.endpoint) {
				t.Fatalf("unsafe or misleading failed check: %+v", state)
			}
		})
	}
}

func TestReleaseUpdateUnknownVersionNeverRequests(t *testing.T) {
	for _, current := range []string{"", "dev", "unknown", "main-abcdef", "0.50", "vv0.50.0"} {
		var calls atomic.Int32
		c := releaseCheckerForTest(t, current, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
		state := c.check()
		if state.Checking || state.Error == "" || state.CheckedAt == "" || state.Available || state.LatestVersion != "" || calls.Load() != 0 {
			t.Errorf("unknown build %q falsely considered current: %+v", current, state)
		}
	}
}

func TestReleaseUpdateDoesNotFollowRedirects(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls.Add(1) }))
	defer destination.Close()
	c := releaseCheckerForTest(t, "0.49.0", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	})
	c.check()
	state := waitReleaseCheck(t, c)
	if destinationCalls.Load() != 0 || state.Error == "" || state.Available {
		t.Fatalf("redirect followed or treated as success: %+v", state)
	}
}

func TestReleaseUpdateConcurrentChecksAndRateLimit(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	c := releaseCheckerForTest(t, "0.49.0", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, `{"tag_name":"v0.50.0"}`)
	})
	if !c.check().Checking {
		t.Fatal("initial request did not start")
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach server")
	}
	var group sync.WaitGroup
	for range 40 {
		group.Go(func() {
			if !c.check().Checking {
				t.Error("concurrent caller did not share active check")
			}
		})
	}
	group.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent checks made %d requests", calls.Load())
	}
	releaseOnce.Do(func() { close(release) })
	before := waitReleaseCheck(t, c)
	if after := c.check(); after != before || calls.Load() != 1 {
		t.Fatalf("manual throttle did not retain completed state: %+v / %+v", before, after)
	}
	c.mu.Lock()
	c.lastAttempt = time.Now().Add(-releaseUpdateMinInterval - time.Second)
	c.mu.Unlock()
	c.check()
	waitReleaseCheck(t, c)
	if calls.Load() != 2 {
		t.Fatalf("eligible manual refresh failed: calls=%d", calls.Load())
	}
}

func TestReleaseUpdateTimeoutAndShutdownCancelRequest(t *testing.T) {
	for _, mode := range []string{"timeout", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			started, cancelled := make(chan struct{}), make(chan struct{})
			c := releaseCheckerForTest(t, "0.49.0", func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-r.Context().Done()
				close(cancelled)
			})
			if mode == "timeout" {
				c.timeout = 200 * time.Millisecond
			}
			quit := make(chan struct{})
			c.start(quit)
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("automatic check did not start")
			}
			if mode == "shutdown" {
				close(quit)
			}
			select {
			case <-cancelled:
			case <-time.After(3 * time.Second):
				t.Fatal("in-flight network request was not cancelled")
			}
			state := waitReleaseCheck(t, c)
			if state.Error == "" || state.Available || state.LatestVersion != "" || state.ReleaseURL != "" {
				t.Fatalf("cancelled request returned success: %+v", state)
			}
			if mode == "timeout" && !strings.Contains(state.Error, "timed out") {
				t.Fatalf("timeout was not reported: %+v", state)
			}
			c.close()
			if c.check().Checking {
				t.Fatal("check restarted after shutdown")
			}
		})
	}
}

func TestReleaseUpdateFailureClearsPreviousSuccessfulResult(t *testing.T) {
	var calls atomic.Int32
	c := releaseCheckerForTest(t, "0.49.0", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, "private infrastructure details")
			return
		}
		io.WriteString(w, `{"tag_name":"v0.50.0"}`)
	})
	c.check()
	if state := waitReleaseCheck(t, c); !state.Available || state.Error != "" {
		t.Fatalf("initial check failed: %+v", state)
	}
	c.mu.Lock()
	c.lastAttempt = time.Now().Add(-releaseUpdateMinInterval - time.Second)
	c.mu.Unlock()
	c.check()
	if state := waitReleaseCheck(t, c); state.Error == "" || state.Available || state.LatestVersion != "" || state.ReleaseURL != "" {
		t.Fatalf("failed refresh retained misleading success: %+v", state)
	}
}

func TestReleaseUpdatePeriodicChecksShareManualThrottle(t *testing.T) {
	var calls atomic.Int32
	c := releaseCheckerForTest(t, "0.49.0", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"tag_name":"v0.50.0"}`)
	})
	if c.interval != 6*time.Hour || c.timeout != 10*time.Second {
		t.Fatal("unexpected production update-check intervals")
	}
	c.interval = 20 * time.Millisecond
	quit := make(chan struct{})
	c.start(quit)
	deadline := time.Now().Add(3 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("startup check did not run")
	}
	waitReleaseCheck(t, c)
	time.Sleep(60 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("scheduled check bypassed shared throttle")
	}
	c.mu.Lock()
	c.lastAttempt = time.Now().Add(-releaseUpdateMinInterval - time.Second)
	c.mu.Unlock()
	deadline = time.Now().Add(3 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 2 {
		t.Fatal("scheduled refresh did not run when eligible")
	}
	waitReleaseCheck(t, c)
	close(quit)
}

func TestReleaseUpdateOfflineErrorDoesNotExposeEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()
	c := newReleaseUpdateChecker("0.49.0")
	t.Cleanup(c.close)
	c.endpoint = server.URL
	c.check()
	state := waitReleaseCheck(t, c)
	if state.Error == "" || state.Available || state.LatestVersion != "" || strings.Contains(state.Error, c.endpoint) || strings.Contains(state.Error, "127.0.0.1") {
		t.Fatalf("offline result exposed transport details or claimed success: %+v", state)
	}
}

func TestReleaseUpdateAdminAuthenticationAndState(t *testing.T) {
	a := testApp(t)
	var calls atomic.Int32
	c := releaseCheckerForTest(t, "0.49.0", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-KiloCode-OrganizationId") != "" || r.Header.Get("Cookie") != "" {
			t.Error("admin credentials reached GitHub")
		}
		io.WriteString(w, `{"tag_name":"v0.50.0"}`)
	})
	a.updates.close()
	a.updates = c
	a.apiKey, a.config.OrgID = "private-kilo-key", "private-team"
	for _, method := range []string{"GET", "POST"} {
		for _, item := range []struct {
			token, host, origin string
			status              int
		}{
			{"", a.adminHost, "", 401}, {"Bearer " + a.config.LocalKey, a.adminHost, "", 401},
			{"Bearer " + a.adminToken, "evil.test", "", 403},
			{"Bearer " + a.adminToken, a.adminHost, "https://evil.test", 403},
		} {
			r := httptest.NewRequest(method, "http://"+a.adminHost+"/api/updates", nil)
			r.Host = item.host
			r.Header.Set("Authorization", item.token)
			r.Header.Set("Origin", item.origin)
			w := httptest.NewRecorder()
			a.adminHandler().ServeHTTP(w, r)
			if w.Code != item.status {
				t.Fatalf("%s auth check: status=%d want=%d", method, w.Code, item.status)
			}
		}
	}
	for _, path := range []string{"updates", "state", "updates", "state"} {
		w := adminRequest(a, path, "")
		if w.Code != 200 {
			t.Fatalf("GET %s status=%d", path, w.Code)
		}
		if path == "state" {
			var state struct {
				Update releaseUpdateState `json:"update"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || state.Update != c.snapshot() {
				t.Fatalf("missing update snapshot in state: %v %+v", err, state.Update)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatal("read-only or unauthorized request caused network traffic")
	}
	w := adminRequest(a, "updates", `{}`)
	var state releaseUpdateState
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || w.Code != 200 || !state.Checking {
		t.Fatalf("POST did not return asynchronous snapshot: status=%d state=%+v error=%v", w.Code, state, err)
	}
	waitReleaseCheck(t, c)
	w = adminRequest(a, "updates", "")
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || state != c.snapshot() || calls.Load() != 1 {
		t.Fatalf("GET snapshot mismatch: %+v, calls=%d", state, calls.Load())
	}
	r := httptest.NewRequest("DELETE", "http://"+a.adminHost+"/api/updates", nil)
	r.Header.Set("Authorization", "Bearer "+a.adminToken)
	w = httptest.NewRecorder()
	a.adminHandler().ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, POST" {
		t.Fatalf("unsupported method accepted: %d", w.Code)
	}
	a.requestQuit()
	if c.ctx.Err() == nil {
		t.Fatal("app quit did not cancel update checks")
	}
}

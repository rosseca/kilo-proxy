package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	releaseUpdateAPIURL       = "https://api.github.com/repos/rosseca/kilo-proxy/releases/latest"
	releaseUpdatePagePrefix   = "https://github.com/rosseca/kilo-proxy/releases/tag/"
	releaseUpdateInterval     = 6 * time.Hour
	releaseUpdateMinInterval  = time.Minute
	releaseUpdateTimeout      = 10 * time.Second
	releaseUpdateMaxBodyBytes = 1 << 20
)

type releaseUpdateState struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	Available      bool   `json:"available"`
	Checking       bool   `json:"checking"`
	CheckedAt      string `json:"checkedAt"`
	Error          string `json:"error"`
	ReleaseURL     string `json:"releaseUrl"`
}

// This client is independent of the authenticated Kilo transport. Neither the
// admin token nor any account, organization or conversation data is sent here.
type releaseUpdateChecker struct {
	mu          sync.Mutex
	state       releaseUpdateState
	lastAttempt time.Time
	ctx         context.Context
	cancel      context.CancelFunc
	startOnce   sync.Once
	client      *http.Client
	endpoint    string
	interval    time.Duration
	timeout     time.Duration
}

func newReleaseUpdateChecker(current string) *releaseUpdateChecker {
	current = strings.TrimSpace(current)
	if _, valid := parseReleaseVersion(current); valid {
		current = strings.TrimPrefix(current, "v")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	ctx, cancel := context.WithCancel(context.Background())
	return &releaseUpdateChecker{
		state: releaseUpdateState{CurrentVersion: current},
		ctx:   ctx, cancel: cancel, endpoint: releaseUpdateAPIURL,
		interval: releaseUpdateInterval, timeout: releaseUpdateTimeout,
		client: &http.Client{
			Transport: transport, Timeout: releaseUpdateTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *releaseUpdateChecker) snapshot() releaseUpdateState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// start is called by normal application startup only, never by newApp, state
// reads, terminal runners or the isolated desktop self-test.
func (c *releaseUpdateChecker) start(quit <-chan struct{}) {
	c.startOnce.Do(func() {
		go func() {
			defer c.close()
			select {
			case <-quit:
				return
			case <-c.ctx.Done():
				return
			default:
			}
			c.check()
			ticker := time.NewTicker(c.interval)
			defer ticker.Stop()
			for {
				select {
				case <-quit:
					return
				case <-c.ctx.Done():
					return
				case <-ticker.C:
					c.check()
				}
			}
		}()
	})
}

func (c *releaseUpdateChecker) close() {
	c.cancel()
	c.client.CloseIdleConnections()
}

// All callers share one request. Even repeated manual clicks can make at most
// one GitHub request per minute; a throttled click returns the cached snapshot.
func (c *releaseUpdateChecker) check() releaseUpdateState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx.Err() != nil || c.state.Checking || (!c.lastAttempt.IsZero() && time.Since(c.lastAttempt) < releaseUpdateMinInterval) {
		return c.state
	}
	c.lastAttempt = time.Now()
	current, valid := parseReleaseVersion(c.state.CurrentVersion)
	if !valid {
		c.state.Error = "This build has an unknown version. Check the releases page manually."
		c.state.CheckedAt = time.Now().UTC().Format(time.RFC3339)
		return c.state
	}
	c.state.Checking = true
	c.state.Error = ""
	go c.fetch(current)
	return c.state
}

func (c *releaseUpdateChecker) fetch(current releaseVersion) {
	ctx, cancel := context.WithTimeout(c.ctx, c.timeout)
	defer cancel()
	tag, latest, message := c.fetchLatest(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Checking = false
	// A failed or cancelled refresh must not masquerade as a successful check of
	// the currently installed version, even if an earlier request succeeded.
	c.state.LatestVersion, c.state.ReleaseURL = "", ""
	c.state.Available = false
	c.state.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	c.state.Error = message
	if message == "" {
		c.state.LatestVersion = strings.TrimPrefix(tag, "v")
		c.state.ReleaseURL = releaseUpdatePagePrefix + url.PathEscape(tag)
		c.state.Available = compareReleaseVersions(latest, current) > 0
	}
}

func (c *releaseUpdateChecker) fetchLatest(ctx context.Context) (string, releaseVersion, string) {
	var empty releaseVersion
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return "", empty, "Could not check for updates. Try again later."
	}
	r.Header.Set("Accept", "application/vnd.github+json")
	r.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	r.Header.Set("User-Agent", "Kilo-Proxy-Update-Check")
	response, err := c.client.Do(r)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return "", empty, "The update check timed out. Try again later."
		}
		return "", empty, "Could not reach GitHub. Check your connection and try again later."
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case http.StatusForbidden, http.StatusTooManyRequests:
			return "", empty, "GitHub is limiting update checks. Try again later."
		case http.StatusNotFound:
			return "", empty, "No published release is available on GitHub."
		default:
			return "", empty, "GitHub could not provide the latest release. Try again later."
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, releaseUpdateMaxBodyBytes+1))
	if err != nil || len(body) > releaseUpdateMaxBodyBytes {
		return "", empty, "GitHub returned an invalid release response."
	}
	var release struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if json.Unmarshal(body, &release) != nil {
		return "", empty, "GitHub returned an invalid release response."
	}
	latest, valid := parseStableReleaseTag(release.TagName)
	if release.Draft || release.Prerelease || !valid {
		return "", empty, "GitHub did not return a valid stable release."
	}
	return release.TagName, latest, ""
}

func (a *app) updateSnapshot() releaseUpdateState {
	if a.updates == nil {
		return releaseUpdateState{CurrentVersion: strings.TrimPrefix(strings.TrimSpace(version), "v")}
	}
	return a.updates.snapshot()
}

func (a *app) updatesAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonResponse(w, http.StatusOK, a.updateSnapshot())
	case http.MethodPost:
		if a.updates == nil {
			jsonError(w, http.StatusServiceUnavailable, "The update checker is unavailable.")
			return
		}
		jsonResponse(w, http.StatusOK, a.updates.check())
	default:
		w.Header().Set("Allow", "GET, POST")
		jsonError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

// Compare numeric identifiers as decimal strings so unusually large version
// components cannot overflow. Build metadata never affects SemVer precedence.
type releaseVersion struct {
	major, minor, patch, pre string
}

var releaseVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func parseReleaseVersion(value string) (releaseVersion, bool) {
	if len(value) > 128 {
		return releaseVersion{}, false
	}
	match := releaseVersionPattern.FindStringSubmatch(value)
	if match == nil {
		return releaseVersion{}, false
	}
	for _, identifier := range strings.Split(match[4], ".") {
		if releaseNumericIdentifier(identifier) && len(identifier) > 1 && identifier[0] == '0' {
			return releaseVersion{}, false
		}
	}
	return releaseVersion{match[1], match[2], match[3], match[4]}, true
}

func releaseNumericIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func compareReleaseNumber(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

func compareReleaseVersions(a, b releaseVersion) int {
	for _, pair := range [][2]string{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if result := compareReleaseNumber(pair[0], pair[1]); result != 0 {
			return result
		}
	}
	if a.pre == b.pre {
		return 0
	}
	if a.pre == "" {
		return 1
	}
	if b.pre == "" {
		return -1
	}
	ap, bp := strings.Split(a.pre, "."), strings.Split(b.pre, ".")
	for i := 0; i < len(ap) && i < len(bp); i++ {
		an, bn := releaseNumericIdentifier(ap[i]), releaseNumericIdentifier(bp[i])
		if an != bn {
			if an {
				return -1
			}
			return 1
		}
		result := strings.Compare(ap[i], bp[i])
		if an {
			result = compareReleaseNumber(ap[i], bp[i])
		}
		if result != 0 {
			return result
		}
	}
	if len(ap) < len(bp) {
		return -1
	}
	return 1
}

func validReleaseUpdateURL(raw string) bool {
	if !strings.HasPrefix(raw, releaseUpdatePagePrefix) {
		return false
	}
	tag, err := url.PathUnescape(strings.TrimPrefix(raw, releaseUpdatePagePrefix))
	_, valid := parseStableReleaseTag(tag)
	return err == nil && valid && raw == releaseUpdatePagePrefix+url.PathEscape(tag)
}

func parseStableReleaseTag(tag string) (releaseVersion, bool) {
	parsed, valid := parseReleaseVersion(tag)
	return parsed, valid && strings.HasPrefix(tag, "v") && parsed.pre == "" && !strings.Contains(tag, "+")
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func imageDiagnosticContext() (context.Context, *traceCapture) {
	c := newTraceCapture(httptest.NewRequest("POST", "/v1/responses", nil), nil)
	c.beginImageTransport("cloudflare")
	return context.WithValue(context.Background(), traceContextKey{}, c), c
}

func imageDiagnosticSnapshot(c *traceCapture) *imageTransportTrace {
	return c.finish("request", nil).ImageTransport
}

func TestImageTransportDiagnosticsIsolationAndReadiness(t *testing.T) {
	m, starts := imageURLTestManager(t)
	firstCtx, firstCapture := imageDiagnosticContext()
	first, err := m.NewLease(firstCtx, "cloudflare", "")
	if err != nil {
		t.Fatal(err)
	}
	secondCtx, secondCapture := imageDiagnosticContext()
	second, err := m.NewLease(secondCtx, "cloudflare", "")
	if err != nil {
		t.Fatal(err)
	}
	raw := imageURLTestPNG(t)
	firstURL, err := first.Upload(firstCtx, raw, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	secondURL, err := second.Upload(secondCtx, raw, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(firstURL)
	tunnel := first.(*managedImageURLLease).tunnel
	serve := func(method, path string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		tunnel.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != want {
			t.Fatalf("status %d, want %d", w.Code, want)
		}
	}
	serve("GET", parsed.Path, 200)
	serve("HEAD", parsed.Path, 200)
	serve("GET", parsed.Path+"?secret=never-log-this", 404)
	serve("PRIVATE-METHOD", parsed.Path, 405)
	for range cap(tunnel.requests) {
		tunnel.requests <- struct{}{}
	}
	serve("GET", parsed.Path, 503)
	for range cap(tunnel.requests) {
		<-tunnel.requests
	}
	// The synthetic startup probe is not counted as an image access.
	d := imageDiagnosticSnapshot(firstCapture)
	if starts.Load() != 1 || d.TunnelReused || d.Probe.Attempts != 1 || d.Probe.LastHTTPStatus != 200 || d.Probe.LastFailed || d.Probe.VerifiedAt == 0 || d.TunnelReadyAt == 0 {
		t.Fatalf("startup: %+v", d)
	}
	image := d.Images[0]
	if image.ID != 1 || image.Requests != 5 || image.GETs != 3 || image.HEADs != 1 || image.HTTP200 != 2 || image.HTTP404 != 1 || image.HTTP405 != 1 || image.HTTP503 != 1 || image.WrittenBytes != int64(len(raw)) {
		t.Fatalf("accesses: %+v", image)
	}
	reused := imageDiagnosticSnapshot(secondCapture)
	if !reused.TunnelReused || reused.Probe.Attempts != 0 || reused.Probe.VerifiedAt != 0 || reused.TunnelReadyAt != 0 || reused.Images[0].Requests != 0 {
		t.Fatalf("reuse pretends to probe or crosses leases: %+v", reused)
	}
	if err := first.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, got, _ := imageURLTestGet(t, "GET", secondURL)
	if status != 200 || !bytes.Equal(got, raw) {
		t.Fatal("cleanup removed another lease")
	}
	removed := imageDiagnosticSnapshot(firstCapture).Images[0]
	if removed.RemovedAt == 0 || removed.RemovalReason != "lease_closed" {
		t.Fatalf("removal: %+v", removed)
	}
	// Completed snapshots remain immutable even if an already-started access finishes late.
	if d.Images[0].RemovedAt != 0 {
		t.Fatal("snapshot shares mutable state")
	}
	encoded, _ := json.Marshal(imageDiagnosticSnapshot(firstCapture))
	for _, secret := range []string{firstURL, parsed.Path, parsed.Host, "never-log-this", "PRIVATE-METHOD", string(raw)} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("diagnostic contains private data")
		}
	}
}

type imageDiagnosticFailingWriter struct {
	header http.Header
	short  bool
}

func (w *imageDiagnosticFailingWriter) Header() http.Header { return w.header }
func (w *imageDiagnosticFailingWriter) WriteHeader(int)     {}
func (w *imageDiagnosticFailingWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return 1, errors.New("private URL https://secret.invalid/bearer-token")
}

func TestImageTransportDiagnosticsWriteErrorsAndCaptureOff(t *testing.T) {
	ctx, c := imageDiagnosticContext()
	m, _ := imageURLTestManager(t)
	lease, err := m.NewLease(ctx, "cloudflare", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := lease.Upload(ctx, imageURLTestPNG(t), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	tunnel := lease.(*managedImageURLLease).tunnel
	for _, short := range []bool{false, true} {
		tunnel.ServeHTTP(&imageDiagnosticFailingWriter{header: make(http.Header), short: short}, httptest.NewRequest("GET", target, nil))
	}
	d := imageDiagnosticSnapshot(c)
	if d.Images[0].WriteFailures != 2 || d.Images[0].HTTP200 != 2 {
		t.Fatalf("failed writes not recorded: %+v", d)
	}
	encoded, _ := json.Marshal(d)
	if strings.Contains(string(encoded), "secret.invalid") || strings.Contains(string(encoded), "bearer-token") {
		t.Fatal("raw write error leaked")
	}
	c.discard()
	tunnel.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", target, nil))
	_ = lease.Close(context.Background())
	c.beginImageTransport("cloudflare")
	if c.finish("request", nil) != nil || c.imageTransport != nil {
		t.Fatal("capture resurrected after disable")
	}
	uncaptured, err := m.NewLease(context.Background(), "cloudflare", "")
	if err != nil {
		t.Fatal(err)
	}
	uncapturedURL, err := uncaptured.Upload(context.Background(), imageURLTestPNG(t), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(uncapturedURL)
	tunnel.mu.Lock()
	observation := tunnel.images[parsed.Path].observation
	tunnel.mu.Unlock()
	if observation != nil {
		t.Fatal("disabled capture retained diagnostic association")
	}
}

func TestImageTransportDiagnosticsFailedProbe(t *testing.T) {
	for _, status := range []int{0, 200, 404, 502} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ctx, c := imageDiagnosticContext()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			m, _ := imageURLTestManager(t)
			m.deps.client = &http.Client{Transport: imageURLTestTransport(func(r *http.Request) (*http.Response, error) {
				cancel()
				if status == 0 {
					return nil, errors.New("private URL and tunnel stderr")
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("wrong image or private challenge"))}, nil
			})}
			if _, err := m.NewLease(ctx, "cloudflare", ""); err == nil {
				t.Fatal("failed readiness accepted")
			}
			d := imageDiagnosticSnapshot(c)
			if d.Probe.Attempts != 1 || d.Probe.LastHTTPStatus != status || !d.Probe.LastFailed || d.Probe.VerifiedAt != 0 || d.TunnelReadyAt != 0 || len(d.Images) != 0 {
				t.Fatalf("probe result: %+v", d)
			}
			encoded, _ := json.Marshal(d)
			if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "challenge") {
				t.Fatal("raw probe output retained")
			}
		})
	}
}

func TestImageTransportDiagnosticsBoundedAndConcurrent(t *testing.T) {
	_, c := imageDiagnosticContext()
	var observations []*imageDeliveryObservation
	for range 64 {
		observations = append(observations, c.registerImage(12))
	}
	if c.registerImage(12) != nil {
		t.Fatal("unbounded image metadata")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				observations[0].served("GET", 200, 12, false, time.Now())
				_ = imageDiagnosticSnapshot(c)
			}
		})
	}
	wg.Wait()
	d := imageDiagnosticSnapshot(c)
	if len(d.Images) != 64 || d.Images[0].Requests != 800 {
		t.Fatal("counter/snapshot race")
	}
	c.discard()
	for _, o := range observations {
		o.removed("lease_closed")
	}
	if c.imageTransport != nil {
		t.Fatal("late removal restored metadata")
	}
}

func TestImageTransportDiagnosticsInferenceLifecycle(t *testing.T) {
	raw := responseUploadPNG(t, 1366, 1000, 1)
	body := responseUploadBody(t, [][]byte{raw}, false, 0)
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			a := testApp(t)
			if enabled {
				if w := adminRequest(a, "activity/config", `{"enabled":true}`); w.Code != 200 {
					t.Fatal(w.Code)
				}
			}
			a.config.ImageTransport = imageTransportSettings{Mode: "cloudflare", Profile: "high"}
			m, _ := imageURLTestManager(t)
			a.imageURLLeaseFactory = m.NewLease
			var target string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				doc, _ := decodeObject(data)
				for _, part := range responseImagePartsForTest(doc) {
					target = stringValue(part["image_url"])
				}
				if target == "" {
					t.Error("missing published image")
					return
				}
				status, got, _ := imageURLTestGet(t, "GET", target)
				if status != 200 || !bytes.Equal(got, raw) {
					t.Error("image unavailable during inference")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"output":[]}`)
			}))
			defer upstream.Close()
			setUpstream(a, upstream.URL)
			if w := activityRequest(a, string(body)); w.Code != 200 {
				t.Fatalf("inference %d: %s", w.Code, w.Body.String())
			}
			status, _, _ := imageURLTestGet(t, "GET", target)
			if status != 404 {
				t.Fatal("image survives inference cleanup")
			}
			if !enabled {
				if len(a.traces) != 0 || len(a.events) != 0 {
					t.Fatal("diagnostics enabled capture")
				}
				return
			}
			trace := a.traces[a.events[0].ID]
			d := trace.ImageTransport
			if d == nil || d.Backend != "cloudflare" || len(d.Images) != 1 || d.Images[0].Requests != 1 || d.Images[0].RemovedAt == 0 || d.CleanupReason != "request_finished" || d.CleanupFailed {
				t.Fatalf("lifecycle: %+v", d)
			}
			if d.GatewayStartedAt == 0 || d.GatewayFinishedAt < d.GatewayStartedAt || d.CleanupStartedAt < d.GatewayFinishedAt || d.CleanupFinishedAt < d.CleanupStartedAt {
				t.Fatalf("lifecycle timing: %+v", d)
			}
			encoded, _ := json.Marshal(trace)
			parsed, _ := url.Parse(target)
			if bytes.Contains(encoded, []byte(parsed.Path)) || !strings.Contains(trace.UpstreamResponse.Body, "omitted") {
				t.Fatal("temporary link privacy changed")
			}
			// Metadata is also exposed through the existing authenticated inspector API.
			response := adminRequest(a, "activity/"+a.events[0].ID, "")
			if response.Code != 200 || !strings.Contains(response.Body.String(), `"imageTransport"`) {
				t.Fatal("diagnostics missing from activity endpoint")
			}
		})
	}
}

// Unlike production's inline-only traversal, the test inspects the rewritten URL.
func responseImagePartsForTest(doc map[string]any) []map[string]any {
	var result []map[string]any
	input, _ := doc["input"].([]any)
	for _, item := range input {
		content, _ := object(item)["content"].([]any)
		for _, part := range content {
			p := object(part)
			if stringValue(p["type"]) == "input_image" {
				result = append(result, p)
			}
		}
	}
	return result
}

func TestImageTransportDiagnosticsCleanupReasons(t *testing.T) {
	body := responseUploadBody(t, [][]byte{responseUploadPNG(t, 1366, 1000, 1)}, false, 0)
	for _, reason := range []string{"request_canceled", "request_deadline", "preparation_failed"} {
		t.Run(reason, func(t *testing.T) {
			a := testApp(t)
			a.config.ImageTransport = imageTransportSettings{Mode: "cloudflare", Profile: "high"}
			m, _ := imageURLTestManager(t)
			a.imageURLLeaseFactory = m.NewLease
			ctx, c := imageDiagnosticContext()
			var cancel context.CancelFunc
			if reason == "request_deadline" {
				ctx, cancel = context.WithTimeout(ctx, time.Second)
			} else {
				ctx, cancel = context.WithCancel(ctx)
			}
			defer cancel()
			r := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
			_, cleanup, err := a.prepareResponseImageUploads(r, "key", "org")
			if err != nil || cleanup == nil {
				t.Fatal("preparation failed", err)
			}
			if reason == "request_deadline" {
				<-ctx.Done()
			} else if reason == "request_canceled" {
				cancel()
			}
			cleanup()
			completed := imageDiagnosticSnapshot(c)
			cleanup()
			if completed.CleanupReason != reason || completed.CleanupFailed || completed.CleanupFinishedAt == 0 || completed.Images[0].RemovedAt == 0 || a.imageUploadsActive != 0 {
				t.Fatalf("cleanup: %+v", completed)
			}
			if imageDiagnosticSnapshot(c).CleanupFinishedAt != completed.CleanupFinishedAt {
				t.Fatal("cleanup ran twice")
			}
		})
	}
}

func TestImageTransportDiagnosticsRemovalReasons(t *testing.T) {
	for _, reason := range []string{"lease_expired", "tunnel_closed"} {
		t.Run(reason, func(t *testing.T) {
			ctx, c := imageDiagnosticContext()
			m, _ := imageURLTestManager(t)
			lease, err := m.NewLease(ctx, "cloudflare", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := lease.Upload(ctx, imageURLTestPNG(t), "image/png"); err != nil {
				t.Fatal(err)
			}
			l := lease.(*managedImageURLLease)
			if reason == "lease_expired" {
				_ = l.closeWithReason(reason)
			} else {
				_ = l.tunnel.Close()
			}
			if got := imageDiagnosticSnapshot(c).Images[0].RemovalReason; got != reason {
				t.Fatalf("reason %s, want %s", got, reason)
			}
		})
	}
}

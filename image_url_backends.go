package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const imageURLLeaseLifetime = 15 * time.Minute // Exceeds the complete ten-minute inference deadline.
const imageURLStoreLimit = 256 << 20
const imageURLLeaseLimit = 16
const imageURLStartupTimeout = 90 * time.Second
const imageURLLitterboxEndpoint = "https://litterbox.catbox.moe/resources/internals/api.php"

type imageURLLease interface {
	Upload(context.Context, []byte, string) (string, error)
	ExpiresAt() time.Time
	Close(context.Context) error
}

// Dependencies are per-manager, immutable after its first use. Tests can replace
// external HTTP and process operations without global hooks or actual tunnels.
type imageURLBackendDeps struct {
	client *http.Client
	start  func(context.Context, string, string) (imageURLTunnelProcess, error)
}

type imageURLTunnelProcess interface {
	URL() string
	Done() <-chan struct{}
	Close() error
}

type imageURLBackendManager struct {
	mu        sync.Mutex
	deps      *imageURLBackendDeps
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
	closeDone chan struct{}
	closeErr  error
	starts    sync.WaitGroup
	tunnels   map[string]*imageURLTunnel
	pending   map[string]chan struct{}
	leases    map[*managedImageURLLease]struct{}
}

func imageURLHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}

func (m *imageURLBackendManager) initLocked() {
	if m.ctx != nil {
		return
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.closeDone = make(chan struct{})
	m.tunnels = make(map[string]*imageURLTunnel)
	m.pending = make(map[string]chan struct{})
	m.leases = make(map[*managedImageURLLease]struct{})
	if m.deps == nil {
		m.deps = &imageURLBackendDeps{}
	}
	if m.deps.client == nil {
		m.deps.client = imageURLHTTPClient()
	}
	if m.deps.start == nil {
		m.deps.start = startImageURLTunnelProcess
	}
}

func (m *imageURLBackendManager) NewLease(ctx context.Context, backend, ttl string) (imageURLLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch backend {
	case "cloudflare", "tailscale", "litterbox":
	default:
		return nil, errors.New("Choose Cloudflare, Litterbox, or Tailscale for image URLs.")
	}
	if ttl == "" {
		ttl = "1h"
	}
	if backend == "litterbox" {
		if _, ok := imageURLLitterboxTTL(ttl); !ok {
			return nil, errors.New("Litterbox expiry must be 1h, 12h, 24h, or 72h.")
		}
	}
	m.mu.Lock()
	m.initLocked()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("Image URL services are shutting down.")
	}
	m.mu.Unlock()
	var tunnel *imageURLTunnel
	var err error
	if backend != "litterbox" {
		tunnel, err = m.getTunnel(ctx, backend)
		if err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("Image URL services are shutting down.")
	}
	if len(m.leases) >= imageURLLeaseLimit {
		return nil, errors.New("Too many active image URL requests. Try again when earlier requests finish.")
	}
	leaseCtx, cancel := context.WithCancel(m.ctx)
	expiry := time.Now().Add(imageURLLeaseLifetime)
	if backend == "litterbox" {
		duration, _ := imageURLLitterboxTTL(ttl)
		expiry = time.Now().Add(duration)
	}
	l := &managedImageURLLease{manager: m, backend: backend, ttl: ttl, tunnel: tunnel, ctx: leaseCtx, cancel: cancel, expires: expiry}
	m.leases[l] = struct{}{}
	// A lost caller cannot retain local images indefinitely. The TTL is a safety
	// ceiling; normal inference completion/cancellation invokes Close immediately.
	l.timer = time.AfterFunc(imageURLLeaseLifetime, func() { _ = l.closeWithReason("lease_expired") })
	return l, nil
}

func (m *imageURLBackendManager) getTunnel(ctx context.Context, backend string) (*imageURLTunnel, error) {
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, errors.New("Image URL services are shutting down.")
		}
		if t := m.tunnels[backend]; t != nil && t.alive() {
			m.mu.Unlock()
			imageTraceCapture(ctx).updateImageTransport(func(d *imageTransportTrace) { d.TunnelReused = true })
			return t, nil
		}
		if wait := m.pending[backend]; wait != nil {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-m.ctx.Done():
				return nil, errors.New("Image URL services are shutting down.")
			case <-wait:
				continue
			}
		}
		old := m.tunnels[backend]
		delete(m.tunnels, backend)
		pending := make(chan struct{})
		m.pending[backend] = pending
		m.starts.Add(1)
		m.mu.Unlock()
		if old != nil {
			_ = old.Close()
		}
		startupCtx, cancel := context.WithTimeout(m.ctx, imageURLStartupTimeout)
		stop := context.AfterFunc(ctx, cancel)
		if capture := imageTraceCapture(ctx); capture != nil {
			startupCtx = context.WithValue(startupCtx, traceContextKey{}, capture)
		}
		imageTraceCapture(ctx).updateImageTransport(func(d *imageTransportTrace) { d.TunnelStartupStartedAt = time.Now().UnixMilli() })
		tunnel, err := newImageURLTunnel(startupCtx, backend, m.deps)
		if err == nil {
			imageTraceCapture(ctx).updateImageTransport(func(d *imageTransportTrace) { d.TunnelReadyAt = time.Now().UnixMilli() })
		}
		stop()
		cancel()
		m.mu.Lock()
		closed := m.closed
		if err == nil && !closed {
			m.tunnels[backend] = tunnel
		}
		delete(m.pending, backend)
		close(pending)
		m.mu.Unlock()
		if tunnel != nil && closed {
			_ = tunnel.Close()
		}
		m.starts.Done()
		if closed {
			return nil, errors.New("Image URL services are shutting down.")
		}
		if err != nil {
			return nil, err
		}
		return tunnel, nil
	}
}

func (m *imageURLBackendManager) Close() error {
	m.mu.Lock()
	m.initLocked()
	if m.closed {
		done := m.closeDone
		m.mu.Unlock()
		<-done
		return m.closeErr
	}
	m.closed = true
	m.cancel()
	leases := make([]*managedImageURLLease, 0, len(m.leases))
	for l := range m.leases {
		leases = append(leases, l)
	}
	tunnels := make([]*imageURLTunnel, 0, len(m.tunnels))
	for _, t := range m.tunnels {
		tunnels = append(tunnels, t)
	}
	m.mu.Unlock()
	for _, l := range leases {
		_ = l.Close(context.Background())
	}
	var result error
	for _, t := range tunnels {
		result = errors.Join(result, t.Close())
	}
	m.starts.Wait()
	m.mu.Lock()
	m.closeErr = result
	close(m.closeDone)
	m.mu.Unlock()
	return result
}

type managedImageURLLease struct {
	manager      *imageURLBackendManager
	backend, ttl string
	tunnel       *imageURLTunnel
	ctx          context.Context
	cancel       context.CancelFunc
	expires      time.Time
	uploadMu     sync.Mutex
	mu           sync.Mutex
	closed       bool
	count        int
	paths        []string
	timer        *time.Timer
}

func (l *managedImageURLLease) ExpiresAt() time.Time { return l.expires }

func (l *managedImageURLLease) Upload(ctx context.Context, data []byte, mime string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	l.uploadMu.Lock()
	defer l.uploadMu.Unlock()
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return "", errors.New("This image URL request has already finished.")
	}
	if l.count >= 64 {
		l.mu.Unlock()
		return "", errors.New("Image URL requests allow at most 64 images.")
	}
	l.mu.Unlock()
	if err := validateImageTransportData(data, mime); err != nil {
		return "", err
	}
	uploadCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(l.ctx, cancel)
	defer cancel()
	defer stop()
	if err := uploadCtx.Err(); err != nil {
		return "", err
	}
	if l.backend == "litterbox" {
		result, err := l.uploadLitterbox(uploadCtx, data, mime)
		if err != nil {
			return "", err
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.closed {
			return "", errors.New("This image URL request has already finished.")
		}
		l.count++
		return result, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return "", errors.New("This image URL request has already finished.")
	}
	if err := uploadCtx.Err(); err != nil {
		return "", err
	}
	if !l.tunnel.alive() {
		return "", errors.New("The image tunnel stopped. Retry the request to start a new tunnel.")
	}
	path, err := l.tunnel.putObserved(data, mime, imageTraceCapture(ctx))
	if err != nil {
		return "", err
	}
	l.paths = append(l.paths, path)
	l.count++
	return l.tunnel.process.URL() + path, nil
}

func (l *managedImageURLLease) Close(_ context.Context) error {
	return l.closeWithReason("lease_closed")
}

func (l *managedImageURLLease) closeWithReason(reason string) error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.cancel()
	if l.timer != nil {
		l.timer.Stop()
	}
	if l.tunnel != nil {
		for _, path := range l.paths {
			l.tunnel.removeWithReason(path, reason)
		}
	}
	l.paths = nil
	l.mu.Unlock()
	l.manager.mu.Lock()
	delete(l.manager.leases, l)
	l.manager.mu.Unlock()
	// Litterbox has no deletion API. Its independently chosen TTL remains in
	// effect; closing a lease only releases local state and cancels active I/O.
	return nil
}

func imageURLLitterboxTTL(ttl string) (time.Duration, bool) {
	hours, ok := map[string]int{"1h": 1, "12h": 12, "24h": 24, "72h": 72}[ttl]
	return time.Duration(hours) * time.Hour, ok
}

func (l *managedImageURLLease) uploadLitterbox(ctx context.Context, data []byte, mime string) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("reqtype", "fileupload")
	_ = writer.WriteField("time", l.ttl)
	ext := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp"}[mime]
	part, err := writer.CreateFormFile("fileToUpload", "image."+ext)
	if err != nil {
		return "", errors.New("Could not prepare the Litterbox image upload.")
	}
	if _, err = part.Write(data); err != nil {
		return "", errors.New("Could not prepare the Litterbox image upload.")
	}
	if err = writer.Close(); err != nil {
		return "", errors.New("Could not prepare the Litterbox image upload.")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, imageURLLitterboxEndpoint, &body)
	if err != nil {
		return "", errors.New("Could not prepare the Litterbox image upload.")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := l.manager.deps.client.Do(req)
	if err != nil {
		return "", errors.New("Litterbox upload failed. Check your connection or try another image transport method.")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, imageUploadURLBudget+1))
	if resp.StatusCode != http.StatusOK {
		// Status is safe to expose; response bodies and headers can contain a
		// service challenge, cookies, request identifiers or reflected input.
		return "", imageUploadProblem(http.StatusBadGateway, "Litterbox rejected the image upload (HTTP "+strconv.Itoa(resp.StatusCode)+"). The service may be unavailable or may restrict this connection. Try again later or choose another image transport method.")
	}
	if err != nil || len(raw) > imageUploadURLBudget {
		return "", errors.New("Litterbox returned an invalid upload response. Try again later or choose another image transport method.")
	}
	result := strings.TrimSpace(string(raw))
	parsed, err := url.Parse(result)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "litter.catbox.moe" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || len(parsed.Path) < 2 || strings.ContainsAny(parsed.Path[1:], "/\\") {
		return "", errors.New("Litterbox returned an invalid image URL.")
	}
	return result, nil
}

type imageURLStoredImage struct {
	observation *imageDeliveryObservation
	data        []byte
	mime        string
}
type imageURLTunnel struct {
	mu        sync.Mutex
	images    map[string]imageURLStoredImage
	bytes     int
	closed    bool
	server    *http.Server
	listener  net.Listener
	process   imageURLTunnelProcess
	requests  chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func (t *imageURLTunnel) alive() bool {
	if t == nil || t.process == nil {
		return false
	}
	select {
	case <-t.process.Done():
		return false
	default:
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.closed
}

func newImageURLTunnel(ctx context.Context, backend string, deps *imageURLBackendDeps) (*imageURLTunnel, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("Could not start the local image-only server.")
	}
	t := &imageURLTunnel{images: make(map[string]imageURLStoredImage), requests: make(chan struct{}, 16), listener: listener}
	t.server = &http.Server{Handler: t, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	// Bound live connections as well as handlers; slow clients cannot allocate an
	// unbounded number of HTTP goroutines on this dedicated origin.
	limited := &imageURLLimitedListener{Listener: listener, slots: make(chan struct{}, 32)}
	go func() { _ = t.server.Serve(limited) }()
	t.process, err = deps.start(ctx, backend, "http://"+listener.Addr().String())
	if err != nil {
		_ = t.Close()
		return nil, err
	}
	// A URL banner can precede edge registration. Publish only after an actual
	// public GET retrieves a randomly addressed synthetic PNG byte-for-byte.
	probe, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a3ioAAAAASUVORK5CYII=")
	path, err := t.put(probe, "image/png")
	if err == nil {
		err = t.waitReady(ctx, deps.client, path, probe)
		t.remove(path)
	}
	if err != nil {
		_ = t.Close()
		return nil, err
	}
	return t, nil
}

func (t *imageURLTunnel) waitReady(ctx context.Context, client *http.Client, path string, want []byte) error {
	for {
		if !t.alive() {
			return errors.New("The image tunnel exited before it became ready. Check the installed tunnel executable and connection.")
		}
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		req, _ := http.NewRequestWithContext(probeCtx, http.MethodGet, t.process.URL()+path, nil)
		resp, err := client.Do(req)
		ready := false
		status, failed := 0, err != nil
		if err == nil {
			status = resp.StatusCode
			raw, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(len(want)+1)))
			_ = resp.Body.Close()
			ready = resp.StatusCode == http.StatusOK && readErr == nil && bytes.Equal(raw, want)
			failed = readErr != nil
		}
		cancel()
		imageTraceCapture(ctx).imageProbe(status, failed || !ready, ready)
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("The image tunnel did not become publicly reachable before the startup deadline. Check your connection and tunnel settings.")
		case <-t.process.Done():
			return errors.New("The image tunnel stopped before becoming publicly reachable.")
		case <-time.After(time.Second):
		}
	}
}

func (t *imageURLTunnel) put(data []byte, mime string) (string, error) {
	return t.putObserved(data, mime, nil)
}

func (t *imageURLTunnel) putObserved(data []byte, mime string, capture *traceCapture) (string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", errors.New("Could not generate a private image link.")
	}
	path := "/" + hex.EncodeToString(token[:])
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return "", errors.New("The image tunnel is closed.")
	}
	if t.bytes+len(data) > imageURLStoreLimit || len(t.images) >= 129 {
		return "", errors.New("The temporary image server is full. Wait for earlier requests to finish.")
	}
	t.images[path] = imageURLStoredImage{data: bytes.Clone(data), mime: mime, observation: capture.registerImage(len(data))}
	t.bytes += len(data)
	return path, nil
}

func (t *imageURLTunnel) remove(path string) {
	t.removeWithReason(path, "lease_closed")
}

func (t *imageURLTunnel) removeWithReason(path, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if data, ok := t.images[path]; ok {
		data.observation.removed(reason)
		t.bytes -= len(data.data)
		delete(t.images, path)
	}
}

func (t *imageURLTunnel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	observation := t.images[r.URL.Path].observation
	t.mu.Unlock()
	status, written, writeFailed := http.StatusOK, 0, false
	started := time.Now()
	defer func() { observation.served(r.Method, status, written, writeFailed, started) }()
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		status = http.StatusMethodNotAllowed
		w.WriteHeader(status)
		return
	}
	select {
	case t.requests <- struct{}{}:
		defer func() { <-t.requests }()
	default:
		status = http.StatusServiceUnavailable
		w.WriteHeader(status)
		return
	}
	// No ServeMux, redirects, proxying, directory serving, health API, or lookup by
	// caller-supplied filename. Encoded variants and query strings are rejected.
	if r.URL.RawQuery != "" || r.URL.RawPath != "" || len(r.URL.Path) != 65 {
		status = http.StatusNotFound
		w.WriteHeader(status)
		return
	}
	t.mu.Lock()
	value, ok := t.images[r.URL.Path]
	closed := t.closed
	t.mu.Unlock()
	if !ok || closed {
		status = http.StatusNotFound
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", value.mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(value.data)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		var err error
		written, err = w.Write(value.data)
		writeFailed = err != nil || written != len(value.data)
	}
}

func (t *imageURLTunnel) Close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		for _, image := range t.images {
			image.observation.removed("tunnel_closed")
		}
		t.images = make(map[string]imageURLStoredImage)
		t.bytes = 0
		t.mu.Unlock()
		if t.server != nil {
			_ = t.server.Close()
		}
		if t.listener != nil {
			_ = t.listener.Close()
		}
		if t.process != nil {
			t.closeErr = t.process.Close()
		}
	})
	return t.closeErr
}

type imageURLLimitedListener struct {
	net.Listener
	slots chan struct{}
}
type imageURLLimitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (l *imageURLLimitedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &imageURLLimitedConn{Conn: conn, release: func() { <-l.slots }}, nil
		default:
			_ = conn.Close()
		}
	}
}
func (c *imageURLLimitedConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }

package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const gatewayURL = "https://api.kilo.ai/api/gateway"

type event struct {
	Usage      *requestUsage `json:"usage,omitempty"`
	ID         string        `json:"id"`
	HasDetails bool          `json:"hasDetails"`
	At         string        `json:"at"`
	Method     string        `json:"method"`
	Path       string        `json:"path"`
	Status     int           `json:"status"`
	Duration   int64         `json:"duration"`
}

type app struct {
	imageGenerationURL    string
	imageGenerationMu     sync.Mutex
	imageGenerationActive int
	launcher              *clientLaunchRuntime
	launchMu              sync.Mutex
	desktop               desktopBridge
	desktopProbes         chan desktopProbe
	editorTestRoot        string
	cursor                *cursorSession
	usageTotal            usageSummary
	usageSessions         map[string]*usageSummary
	captureEnabled        bool
	activeTraces          int
	nextEventID           uint64
	activityEpoch         uint64
	traces                map[string]*requestTrace
	codexProfileDir       string
	codexCLIProfileDir    string
	xcodeTestRoot         string
	claudeProfileDir      string
	catalogRevision       uint64
	modelStatsURL         string
	modelStatsCache       modelStatsCache
	accountURL            string
	authPollInterval      time.Duration
	login                 *loginSession
	organizations         []organization
	accountEmail          string
	keySaved              bool
	mu                    sync.Mutex
	dir                   string
	config                settings
	apiKey                string
	vault                 credentialVault
	vaultWarning          string
	adminToken            string
	adminHost             string
	upstream              *url.URL
	transport             http.RoundTripper
	proxyServer           *http.Server
	proxyListener         net.Listener
	started               time.Time
	requests              int
	failures              int
	active                int
	events                []event
	quit                  chan struct{}
	quitOnce              sync.Once
}

func newApp(dir string, vault credentialVault) (*app, error) {
	cfg, err := readSettings(dir)
	if err != nil {
		return nil, err
	}
	cfg.Language = initialLanguage(cfg.Language)
	u, _ := url.Parse(gatewayURL)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	// Credentials only travel directly to Kilo; ignore ambient HTTP(S)_PROXY settings.
	tr.Proxy = nil
	tr.ResponseHeaderTimeout = 120 * time.Second
	a := &app{dir: dir, config: cfg, vault: vault, adminToken: randomKey(""), upstream: u, transport: tr, quit: make(chan struct{})}
	a.captureEnabled = true
	a.traces = make(map[string]*requestTrace)
	a.accountURL = kiloAccountURL
	a.authPollInterval = 3 * time.Second
	if cfg.Remember {
		key, err := vault.Get(cfg.VaultID)
		if err != nil {
			a.vaultWarning = "No se pudo leer el almacén de credenciales. Introduce tu API key de nuevo."
		} else {
			a.apiKey = key
			a.keySaved = true
		}
	}
	return a, nil
}

func secureEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func localHostMatches(r *http.Request, host string) bool {
	return r.Host == host && (r.Header.Get("Origin") == "" || r.Header.Get("Origin") == "http://"+host)
}

func validRoute(method, path string) bool {
	if method == "GET" && path == "/v1/models" {
		return true
	}
	return method == "POST" && (path == "/v1/chat/completions" || path == "/v1/responses" || path == "/v1/messages")
}

type schemaBridgeContextKey struct{}

func (a *app) inferenceHandler(key, orgID, localKey, host string) http.Handler {
	proxy := &httputil.ReverseProxy{
		Transport:     traceTransport{a.transport},
		FlushInterval: -1,
		ErrorLog:      log.New(io.Discard, "", 0),
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(a.upstream)
			p.Out.URL.Path = strings.TrimRight(a.upstream.Path, "/") + strings.TrimPrefix(p.In.URL.Path, "/v1")
			p.Out.URL.RawPath = ""
			// Build a new header map so cookies, local credentials and caller-supplied
			// organization/provider overrides never reach the upstream.
			p.Out.Header = make(http.Header)
			for _, name := range []string{"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta"} {
				for _, value := range p.In.Header.Values(name) {
					p.Out.Header.Add(name, value)
				}
			}
			p.Out.Header.Set("Authorization", "Bearer "+key)
			p.Out.Header.Set("X-KiloCode-OrganizationId", orgID)
			p.Out.Header.Set("User-Agent", "Kilo-Local/"+version)
		},
		ModifyResponse: func(r *http.Response) error {
			if bridge, ok := r.Request.Context().Value(schemaBridgeContextKey{}).(*schemaBridge); ok {
				if err := bridge.adaptResponse(r); err != nil {
					return err
				}
			}
			r.Header.Del("Set-Cookie")
			for name := range r.Header {
				if strings.HasPrefix(strings.ToLower(name), "access-control-") {
					r.Header.Del(name)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if capture, _ := r.Context().Value(traceContextKey{}).(*traceCapture); capture != nil {
				capture.traceError = capture.redact(err.Error())
				if len(capture.traceError) > 2048 {
					capture.traceError = capture.traceError[:2048]
				}
			}
			jsonError(w, http.StatusBadGateway, "No se pudo conectar con Kilo. Comprueba la red e inténtalo de nuevo.")
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !localHostMatches(r, host) || r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
			jsonError(w, http.StatusForbidden, "Este endpoint solo admite clientes locales de API.")
			return
		}
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || !secureEqual(bearer, localKey) {
			jsonError(w, http.StatusUnauthorized, "API key local incorrecta. Cópiala desde Kilo Proxy.")
			return
		}
		if r.URL.Path == "/mcp/images" && r.URL.RawPath == "" && r.URL.RawQuery == "" {
			a.imageMCPHandler(w, r, key, orgID, localKey)
			return
		}
		if r.URL.RawPath == "" && r.URL.RawQuery == "" && r.URL.Path == "/xcode/v1/models" && r.Method == "GET" {
			a.xcodeChatModels(w)
			return
		}
		if r.URL.RawPath == "" && r.URL.Path == "/xcode/v1/chat/completions" && r.Method == "POST" {
			r = r.Clone(r.Context())
			r.URL.Path = "/v1/chat/completions"
		}
		if !validRoute(r.Method, r.URL.Path) || r.URL.RawPath != "" || !validQuery(r.URL) {
			jsonError(w, http.StatusNotFound, "Endpoint no compatible. Usa la base URL terminada en /v1.")
			return
		}
		start := time.Now()
		var usage *usageObserver
		if r.Method == http.MethodPost {
			usage = newUsageObserver(r, orgID)
			r = r.WithContext(context.WithValue(r.Context(), usageContextKey{}, usage))
		}
		id, epoch, capture := a.beginActivity(r, key, localKey)
		recorder := &statusWriter{ResponseWriter: w, status: http.StatusOK, capture: capture}
		w = recorder
		if capture != nil {
			r = r.WithContext(context.WithValue(r.Context(), traceContextKey{}, capture))
			if r.Body != nil {
				r.Body = &traceReader{r.Body, &capture.request}
			}
		}
		defer func() {
			var detail *requestTrace
			if capture != nil {
				detail = capture.finish(id, recorder.Header())
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			if capture != nil {
				a.activeTraces--
			}
			if r.Context().Err() != nil {
				recorder.status = 499
			}
			a.active--
			a.requests++
			if recorder.status >= 400 {
				a.failures++
			}
			if usage != nil {
				usage = usage.snapshot()
				if r.Context().Err() != nil {
					usage.usage.Complete = false
				}
			}
			a.recordUsage(usage)
			var usageDetail *requestUsage
			if usage != nil {
				usageDetail = &usage.usage
			}
			if epoch != a.activityEpoch {
				return
			}
			if detail != nil && a.captureEnabled {
				a.traces[id] = detail
			}
			a.events = append([]event{{Usage: usageDetail, ID: id, HasDetails: a.traces[id] != nil, At: time.Now().Format(time.RFC3339), Method: r.Method, Path: r.URL.Path, Status: recorder.status, Duration: time.Since(start).Milliseconds()}}, a.events...)
			if len(a.events) > traceCountLimit {
				for _, old := range a.events[traceCountLimit:] {
					delete(a.traces, old.ID)
				}
				a.events = a.events[:traceCountLimit]
			}
		}()
		// Bound request uploads, including requests using chunked transfer encoding.
		if r.ContentLength > 32<<20 {
			jsonError(w, http.StatusRequestEntityTooLarge, "La petición supera 32 MiB.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
		bridge, err := prepareSchemaBridge(r)
		if err != nil {
			var oversized *http.MaxBytesError
			if errors.As(err, &oversized) {
				jsonError(w, http.StatusRequestEntityTooLarge, "La petición supera 32 MiB.")
				return
			}
			jsonError(w, http.StatusBadRequest, "No se pudo adaptar el esquema de herramientas para Anthropic: "+err.Error())
			return
		}
		if bridge != nil {
			r = r.WithContext(context.WithValue(r.Context(), schemaBridgeContextKey{}, bridge))
		}

		proxy.ServeHTTP(recorder, r)
	})
}

type statusWriter struct {
	capture *traceCapture
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.wrote {
		w.status = status
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	if w.capture != nil {
		w.capture.response.write(b[:n])
	}
	return n, err
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (a *app) start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	select {
	case <-a.quit:
		return errors.New("application is closing")
	default:
	}
	if a.proxyServer != nil {
		return nil
	}
	if a.authPending() {
		return errors.New("login pending")
	}
	if a.apiKey == "" || a.config.OrgID == "" {
		return errMissingCredentials
	}
	host := net.JoinHostPort("127.0.0.1", strconv.Itoa(a.config.Port))
	ln, err := net.Listen("tcp4", host)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: a.inferenceHandler(a.apiKey, a.config.OrgID, a.config.LocalKey, host), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 << 10}
	a.proxyServer = srv
	a.proxyListener = ln
	a.started = time.Now()
	go func() { _ = srv.Serve(ln) }()
	return nil
}

func (a *app) stop() {
	a.mu.Lock()
	a.stopCursorLocked()
	srv := a.proxyServer
	// Serve runs asynchronously and may not have registered the listener yet.
	// Release our listener before exposing the stopped state to another start.
	if a.proxyListener != nil {
		_ = a.proxyListener.Close()
		a.proxyListener = nil
	}
	a.proxyServer = nil
	a.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
}

// Anthropic SDKs may append this fixed beta flag; no credential or arbitrary query forwarding.
func validQuery(u *url.URL) bool {
	return u.RawQuery == "" || (u.Path == "/v1/messages" && u.RawQuery == "beta=true")
}

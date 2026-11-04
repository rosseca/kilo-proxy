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
	"sync/atomic"
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
	catalogWarnings           []string
	chatGPTLastIdentity       string
	chatgpt                   *chatGPTConnection
	chatGPTResponsesURL       string // Test override; never accepted from clients or settings.
	chatGPTQuotaDue           time.Time
	providerChanging          bool
	updates                   *releaseUpdateChecker
	packageUpdates            *packageUpdateManager
	managedToolsMu            sync.Mutex
	managedTools              *managedToolsManager
	imageURLBackends          imageURLBackendManager
	imageDependencyLookup     func(string) string
	imageURLLeaseFactory      func(context.Context, string, string) (imageURLLease, error)
	attachmentClientFactory   func(string, string) *imageAttachmentClient
	imageUploadsActive        int
	imageUploadsDone          chan struct{}
	imageUploadWarning        string
	usageHistory              *usageHistoryStore
	billing                   billingCache
	usageQueryMu              sync.Mutex
	billingAutoRefresh        bool
	modelLibrary              *modelLibraryStore
	imageGenerationURL        string
	imageGenerationMu         sync.Mutex
	imageGenerationActive     int
	launcher                  *clientLaunchRuntime
	claudeDesktopCheckRunning func(string) (bool, error)
	openDesignCheckRunning    func(string) (bool, error)
	openDesignLaunchUntil     time.Time
	openMausBotCheckRunning   func(openMausBotPaths) (bool, error)
	openMausBotLaunchUntil    time.Time
	openCodeVersionProbe      func(string) (string, error) // Synthetic test fixtures only.
	openCodePathsMu           sync.RWMutex
	openCodeExecutables       [2]string
	cliPathsMu                sync.RWMutex
	cliExecutables            [2]string
	museExecutable            string
	museVersionProbe          func(string) (string, error) // Synthetic fixtures only.
	synaraExecutable          string
	omnigentExecutable        string
	omnigentLaunchUntil       time.Time
	omnigentSessionEfforts    map[string]omnigentSessionEffort
	t3CodeProbeClaude         func(string, clientLaunchRuntime) claudeCapabilities // Synthetic test fixtures only.
	t3CodeCheckRunning        func(string) (bool, error)
	t3CodeLaunchUntil         time.Time
	t3CodeAssets              *t3CodeRuntimeAssets
	synaraCheckRunning        func(string) (bool, error)
	synaraCheckEnvironment    func() error
	synaraCheckSystem         func(string) string    // Synthetic policy injection; never accepted from clients or settings.
	synaraAdapterSource       func() (string, error) // Test override; never accepted from clients or settings.
	synaraRuntimeDisabled     bool                   // Synthetic test fixtures only; never accepted from clients or settings.
	synaraLaunchUntil         time.Time
	terminalCommandsBinary    string
	terminalCommandsShell     string
	terminalCommandsProfiles  []string
	launchMu                  sync.Mutex
	desktop                   desktopBridge
	desktopProbes             chan desktopProbe
	editorTestRoot            string
	editorMu                  sync.Mutex
	zedCredentialStore        func(context.Context, string, string, string) error
	usageTotal                usageSummary
	usageSessions             map[string]*usageSummary
	captureEnabled            bool
	activeTraces              int
	activeCaptures            map[*traceCapture]struct{}
	nextEventID               uint64
	activityEpoch             uint64
	traces                    map[string]*requestTrace
	codexProfileDir           string
	codexCLIProfileDir        string
	xcodeTestRoot             string
	claudeProfileDir          string
	ompProfileDir             string
	openCodeProfileDir        string
	catalogRevision           uint64
	modelStatsURL             string
	modelStatsCache           modelStatsCache
	recommendedModelsURL      string
	recommendedModelsCache    recommendedModelsCache
	accountURL                string
	authPollInterval          time.Duration
	login                     *loginSession
	organizations             []organization
	accountEmail              string
	keySaved                  bool
	connectionNeedsSave       bool // Device login and auto-selected teams still need an explicit config save.
	uiProjection              atomic.Pointer[appUIProjection]
	mu                        sync.Mutex
	dir                       string
	config                    settings
	apiKey                    string
	vault                     credentialVault
	vaultWarning              string
	adminToken                string
	adminHost                 string
	upstream                  *url.URL
	transport                 http.RoundTripper
	proxyServer               *http.Server
	proxyListener             net.Listener
	listenProxy               func(string, string) (net.Listener, error)
	started                   time.Time
	requests                  int
	failures                  int
	active                    int
	events                    []event
	quit                      chan struct{}
	quitOnce                  sync.Once
	headless                  bool
}

func newApp(dir string, vault credentialVault) (*app, error) {
	cfg, err := readSettings(dir)
	if err != nil {
		return nil, err
	}
	cfg.Language = initialLanguage(cfg.Language)
	u, _ := url.Parse(gatewayURL)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	// Credentials travel directly to their service; ignore ambient HTTP(S)_PROXY settings.
	tr.Proxy = nil
	tr.ResponseHeaderTimeout = 120 * time.Second
	a := &app{dir: dir, config: cfg, vault: vault, adminToken: randomKey(""), upstream: u, transport: tr, quit: make(chan struct{})}
	a.openCodeExecutables = [2]string{cfg.OpenCode1Executable, cfg.OpenCode2Executable}
	a.cliExecutables = [2]string{cfg.CodexCLIExecutable, cfg.ClaudeCodeExecutable}
	a.museExecutable = cfg.MuseCodeExecutable
	a.synaraExecutable = cfg.SynaraExecutable
	a.omnigentExecutable = cfg.OmnigentExecutable
	a.updates = newReleaseUpdateChecker(version)
	a.chatgpt = newChatGPTConnection(vault, cfg.VaultID, tr, dir)
	a.modelLibrary = newModelLibraryStore(dir)
	a.usageHistory = newUsageHistoryStore(dir)
	a.zedCredentialStore = storeZedCredential
	a.captureEnabled = cfg.CaptureActivity
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
	a.publishUIProjectionLocked()
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

type responsesNamespaceContextKey struct{}

// responseAdaptationError identifies failures after Kilo returned response headers.
// Do not expose the wrapped error to API clients: it may contain private data.
type responseAdaptationError struct{ cause error }

func (e responseAdaptationError) Error() string { return "response adaptation: " + e.cause.Error() }
func (e responseAdaptationError) Unwrap() error { return e.cause }

func (a *app) inferenceHandler(key, orgID, localKey, host string) http.Handler {
	proxy := &httputil.ReverseProxy{
		Transport:     traceTransport{remoteImageFallbackTransport{base: a.transport}},
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
			// Kilo may explicitly mark an uncompressed SSE response as identity.
			// Treat it as unencoded so Messages compatibility adapters can run.
			if strings.EqualFold(strings.TrimSpace(r.Header.Get("Content-Encoding")), "identity") {
				r.Header.Del("Content-Encoding")
			}
			normalizeUpstreamPayloadError(r)
			normalizeMessagesToolStop(r)
			if err := adaptClaudeDesktopAliasResponse(r); err != nil {
				return responseAdaptationError{err}
			}
			// Restore client tool identity before decoding schema envelopes or
			// normalizing collaboration arguments.
			if bridge, ok := r.Request.Context().Value(responsesNamespaceContextKey{}).(*responsesNamespaceBridge); ok {
				if err := bridge.adaptResponse(r); err != nil {
					return responseAdaptationError{err}
				}
			}
			if bridge, ok := r.Request.Context().Value(schemaBridgeContextKey{}).(*schemaBridge); ok {
				if err := bridge.adaptResponse(r); err != nil {
					return responseAdaptationError{err}
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
				capture.setError(err.Error())
			}
			code := "upstream_transport_error"
			message := "Kilo Proxy no pudo completar la solicitud HTTP hacia Kilo. Comprueba la conexión y el estado del gateway; si se repite, activa la captura en Activity antes de reproducirlo."
			var adaptation responseAdaptationError
			if errors.As(err, &adaptation) {
				code = "response_adaptation_error"
				message = "Kilo Proxy recibió una respuesta de Kilo, pero no pudo adaptarla. Actualiza Kilo Proxy; si se repite, activa la captura en Activity antes de reproducirlo y comunica la traza."
			}
			jsonResponse(w, http.StatusBadGateway, map[string]any{"error": map[string]string{"type": "kilo_local_error", "code": code, "message": message}})
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !localHostMatches(r, host) || r.Header.Get("Origin") != "" || (r.Header.Get("Sec-Fetch-Site") != "" && !claudeDesktopMessagesRequest(r, host)) {
			jsonError(w, http.StatusForbidden, "Este endpoint solo admite clientes locales de API.")
			return
		}
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		museScope := museProxyScope(bearer, localKey)
		omnigentScope := omnigentProxyScope(bearer, localKey)
		if r.Header.Get("Authorization") == "" && strings.HasPrefix(r.URL.Path, "/omnigent/") {
			// The Anthropic SDK uses x-api-key. This exception accepts only a
			// scoped Omnigent credential, never the general local key.
			omnigentScope = omnigentProxyScope(r.Header.Get("X-Api-Key"), localKey)
		}
		if omnigentScope == "" && (!strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || museScope == "" && !secureEqual(bearer, localKey)) {
			jsonError(w, http.StatusUnauthorized, "API key local incorrecta. Cópiala desde Kilo Proxy.")
			return
		}
		if omnigentScope != "" {
			if omnigentScope == "claude" && r.Method == http.MethodPost && r.URL.Path == "/omnigent/claude/v1/session-effort" && r.URL.RawPath == "" && r.URL.RawQuery == "" && !r.URL.ForceQuery {
				a.omnigentSessionEffortAPI(w, r)
				return
			}
			path, ok := omnigentInferencePath(r, omnigentScope)
			if !ok {
				jsonError(w, http.StatusNotFound, "This credential is scoped to its Omnigent connection.")
				return
			}
			if r.Method == http.MethodGet {
				a.serveOmnigentModels(w, omnigentScope)
				return
			}
			r = r.Clone(context.WithValue(r.Context(), omnigentRequestScopeKey{}, omnigentScope))
			r.URL.Path = path
		}
		if museScope != "" {
			if museCatalogPath(r) {
				jsonError(w, http.StatusNotFound, "Use the private Muse Code model catalogue prepared by Kilo Proxy.")
				return
			}
			if !museInferencePath(r, museScope) {
				jsonError(w, http.StatusNotFound, "This credential is scoped to its Muse Code connection.")
				return
			}
			r = r.Clone(context.WithValue(r.Context(), museRequestScopeKey{}, museScope))
			r.URL.Path = "/v1/responses"
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
		if path, ok := openMausBotInferencePath(r); ok {
			r = r.Clone(context.WithValue(r.Context(), openMausBotRequestContextKey{}, true))
			r.URL.Path = path
		}
		if synaraInferencePath(r) {
			r = r.Clone(context.WithValue(r.Context(), synaraRequestContextKey{}, true))
			r.URL.Path = "/v1/messages"
		}
		if t3CodeInferencePath(r) {
			r = r.Clone(context.WithValue(r.Context(), t3CodeRequestContextKey{}, true))
			r.URL.Path = "/v1/messages"
		}
		if prefix := zedProxyPrefix(localKey); r.URL.RawPath == "" && strings.HasPrefix(r.URL.Path, prefix+"/v1/") {
			r = r.Clone(r.Context())
			r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		}
		if !validRoute(r.Method, r.URL.Path) || r.URL.RawPath != "" || !validQuery(r.URL) {
			jsonError(w, http.StatusNotFound, "Endpoint no compatible. Usa la base URL terminada en /v1.")
			return
		}
		start := time.Now()
		var usage *usageObserver
		if r.Method == http.MethodPost {
			usage = newUsageObserver(r, orgID)
			usage.historyAccount = usageAccountID(key, orgID)
			r = r.WithContext(context.WithValue(r.Context(), usageContextKey{}, usage))
		}
		id, epoch, capture := a.beginActivity(r, key, localKey)
		recorder := &statusWriter{ResponseWriter: w, status: http.StatusOK, capture: capture}
		w = recorder
		if capture != nil {
			if omnigentScope != "" {
				capture.addSecrets(omnigentProxyToken(localKey, omnigentScope))
			}
			if museScope != "" {
				capture.addSecrets(museProxyToken(localKey, museScope))
			}
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
				delete(a.activeCaptures, capture)
			}
			if usage != nil {
				usage = usage.snapshot()
			}
			// Clients may close immediately after receiving the terminal event.
			// That cannot invalidate final accounting already read upstream. A
			// cancellation before completion remains an incomplete 499 request.
			if r.Context().Err() != nil && (usage == nil || !usage.usage.Complete) {
				recorder.status = 499
			}
			a.active--
			a.requests++
			if recorder.status >= 400 {
				a.failures++
			}
			a.recordUsage(usage)
			var usageDetail *requestUsage
			if usage != nil {
				usageDetail = &usage.usage
			}
			if epoch != a.activityEpoch || !a.captureEnabled {
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
		if scope, _ := r.Context().Value(omnigentRequestScopeKey{}).(string); scope != "" {
			library, catalog, err := a.omnigentScopeLibrary(scope)
			if err != nil {
				jsonError(w, http.StatusConflict, err.Error())
				return
			}
			if scope == "claude" {
				var err error
				r, err = a.omnigentSessionEffortRequest(r)
				if err != nil {
					jsonError(w, http.StatusConflict, err.Error())
					return
				}
			}
			if err = prepareOmnigentModelRequest(r, scope, library, catalog); err != nil {
				status := http.StatusBadRequest
				var oversized *http.MaxBytesError
				if errors.As(err, &oversized) {
					status = http.StatusRequestEntityTooLarge
				}
				jsonError(w, status, err.Error())
				return
			}
		}
		if scope, _ := r.Context().Value(museRequestScopeKey{}).(string); scope != "" {
			library, catalog, err := a.museScopeLibrary(scope)
			if err != nil {
				jsonError(w, http.StatusConflict, err.Error())
				return
			}
			if err = prepareMuseModelRequest(r, library, catalog); err != nil {
				status := http.StatusBadRequest
				var oversized *http.MaxBytesError
				if errors.As(err, &oversized) {
					status = http.StatusRequestEntityTooLarge
				}
				jsonError(w, status, err.Error())
				return
			}
		}
		if err := a.prepareOpenMausBotReasoning(r); err != nil {
			status := http.StatusBadRequest
			var oversized *http.MaxBytesError
			if errors.As(err, &oversized) {
				status = http.StatusRequestEntityTooLarge
			}
			jsonError(w, status, err.Error())
			return
		}
		var aliasErr error
		r, aliasErr = a.prepareSynaraRequest(r)
		if aliasErr != nil {
			status := http.StatusBadRequest
			var oversized *http.MaxBytesError
			if errors.As(aliasErr, &oversized) {
				status = http.StatusRequestEntityTooLarge
			}
			jsonError(w, status, aliasErr.Error())
			return
		}
		r, aliasErr = a.prepareT3CodeRequest(r)
		if aliasErr != nil {
			status := http.StatusBadRequest
			var oversized *http.MaxBytesError
			if errors.As(aliasErr, &oversized) {
				status = http.StatusRequestEntityTooLarge
			}
			jsonError(w, status, aliasErr.Error())
			return
		}
		r, aliasErr = a.prepareClaudeDesktopAlias(r)
		if aliasErr != nil {
			var oversized *http.MaxBytesError
			if errors.As(aliasErr, &oversized) {
				jsonError(w, http.StatusRequestEntityTooLarge, "La petición supera 32 MiB.")
			} else {
				jsonError(w, http.StatusBadRequest, aliasErr.Error())
			}
			return
		}
		// identity carries plain JSON; normalize it before every Responses adapter.
		if r.URL.Path == "/v1/responses" && strings.EqualFold(strings.TrimSpace(r.Header.Get("Content-Encoding")), "identity") {
			r.Header.Del("Content-Encoding")
		}
		if err := prepareResponsesInput(r); err != nil {
			var oversized *http.MaxBytesError
			if errors.As(err, &oversized) {
				jsonError(recorder, http.StatusRequestEntityTooLarge, "La petición supera 32 MiB.")
				return
			}
			jsonError(recorder, http.StatusBadRequest, "Could not adapt Codex cross-task input: "+err.Error())
			return
		}
		if r.Method == http.MethodGet {
			if managed, _ := r.Context().Value(openMausBotRequestContextKey{}).(bool); managed {
				a.serveOpenMausBotModels(recorder)
				return
			}
			if a.chatgpt.snapshot().Connected {
				a.serveModelList(recorder, r)
			} else {
				proxy.ServeHTTP(recorder, r)
			}
			return
		}
		model, routeErr := requestModel(r)
		if routeErr != nil {
			status := http.StatusBadRequest
			var oversized *http.MaxBytesError
			if errors.As(routeErr, &oversized) {
				status = http.StatusRequestEntityTooLarge
			}
			jsonError(recorder, status, routeErr.Error())
			return
		}
		if strings.HasPrefix(model, "chatgpt/") {
			usage = newUsageObserver(r, "chatgpt:"+a.chatgpt.identity())
			usage.historyAccount = usageAccountID(a.chatgpt.identity(), "chatgpt")
			usage.org = "chatgpt"
			usage.usage.Billing = "subscription"
			usage.messages = false
			r = r.WithContext(context.WithValue(r.Context(), usageContextKey{}, usage))
			a.serveChatGPT(recorder, r)
			return
		}
		if key == "" || orgID == "" {
			jsonError(recorder, 401, "This model requires a Kilo connection. Connect Kilo or choose a ChatGPT subscription model.")
			return
		}
		if err := a.prepareResponsesMaxOutputTokens(r, model); err != nil {
			status := http.StatusBadRequest
			var oversized *http.MaxBytesError
			if errors.As(err, &oversized) {
				status = http.StatusRequestEntityTooLarge
			}
			jsonError(w, status, err.Error())
			return
		}
		if err := prepareGeminiToolSchemas(r, model); err != nil {
			var oversized *http.MaxBytesError
			if errors.As(err, &oversized) {
				jsonError(recorder, http.StatusRequestEntityTooLarge, "La petición supera 32 MiB.")
			} else {
				jsonError(recorder, http.StatusBadRequest, "Could not adapt Gemini tool schemas: "+err.Error())
			}
			return
		}
		bridge, err := prepareSchemaBridge(r)
		if err != nil {
			var oversized *http.MaxBytesError
			if errors.As(err, &oversized) {
				jsonError(w, http.StatusRequestEntityTooLarge, "La petición supera 32 MiB.")
				return
			}
			jsonError(w, http.StatusBadRequest, "Could not adapt Responses tools: "+err.Error())
			return
		}
		if bridge != nil {
			r = r.WithContext(context.WithValue(r.Context(), schemaBridgeContextKey{}, bridge))
		}

		namespaceBridge, err := prepareResponsesNamespaceBridge(r)
		if err != nil {
			var oversized *http.MaxBytesError
			if errors.As(err, &oversized) {
				jsonError(recorder, http.StatusRequestEntityTooLarge, "La petición supera 32 MiB.")
			} else {
				jsonError(recorder, http.StatusBadRequest, "Could not adapt Responses namespaces: "+err.Error())
			}
			return
		}
		if namespaceBridge != nil {
			r = r.WithContext(context.WithValue(r.Context(), responsesNamespaceContextKey{}, namespaceBridge))
		}

		imageRequest, cleanupImages, err := a.prepareResponseImageUploads(r, key, orgID)
		if cleanupImages != nil {
			defer func() {
				// Observe the inference deadline before cleanup cancels its
				// child context; completed usage remains authoritative.
				if requestErr := imageRequest.Context().Err(); requestErr != nil && (usage == nil || !usage.snapshot().usage.Complete) {
					recorder.status = 499
					if errors.Is(requestErr, context.DeadlineExceeded) {
						recorder.status = http.StatusGatewayTimeout
					}
				}
				cleanupImages()
			}()
		}
		if err != nil {
			status := http.StatusBadGateway
			var uploadError *imageUploadRequestError
			var oversized *http.MaxBytesError
			if errors.As(err, &uploadError) {
				status = uploadError.status
			} else if errors.As(err, &oversized) {
				status = http.StatusRequestEntityTooLarge
			}
			jsonError(recorder, status, err.Error())
			return
		}
		imageRequest, err = prepareRemoteImageFallback(imageRequest)
		if err != nil {
			var oversized *http.MaxBytesError
			if errors.As(err, &oversized) {
				jsonError(recorder, http.StatusRequestEntityTooLarge, "La petición supera 32 MiB.")
			} else {
				jsonError(recorder, http.StatusBadRequest, "Could not prepare the request for unavailable image recovery.")
			}
			return
		}
		capture.updateImageTransport(func(d *imageTransportTrace) { d.GatewayStartedAt = time.Now().UnixMilli() })
		proxy.ServeHTTP(recorder, imageRequest)
		capture.updateImageTransport(func(d *imageTransportTrace) { d.GatewayFinishedAt = time.Now().UnixMilli() })
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
	return a.startLocked()
}

// Caller holds a.mu, allowing profile preparation and startup to stay atomic
// with respect to another managed profile or connection update.
func (a *app) startLocked() error {
	select {
	case <-a.quit:
		return errProxyClosing
	default:
	}
	if a.proxyServer != nil {
		return nil
	}
	if a.providerChanging {
		return errProxyAuthenticationChanging
	}
	if a.authPending() && !a.chatGPTReadyLocked() {
		return errProxyLoginPending
	}
	if !a.connectionReadyLocked() {
		return errMissingCredentials
	}
	host := net.JoinHostPort("127.0.0.1", strconv.Itoa(a.config.Port))
	listen := a.listenProxy
	if listen == nil {
		listen = net.Listen
	}
	ln, err := listen("tcp4", host)
	if err != nil {
		return wrapProxyListenError(host, err)
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
	a.drainUsageHistory()
}

// Anthropic SDKs may append this fixed beta flag; no credential or arbitrary query forwarding.
func validQuery(u *url.URL) bool {
	return u.RawQuery == "" || (u.Path == "/v1/messages" && u.RawQuery == "beta=true")
}

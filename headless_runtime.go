package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

const headlessControlLimit = 16 << 20
const headlessShutdownTimeout = 15 * time.Second

type headlessStatus struct {
	Version         string `json:"version"`
	Running         bool   `json:"running"`
	ConnectionReady bool   `json:"connectionReady"`
	KiloReady       bool   `json:"kiloReady"`
	ChatGPTReady    bool   `json:"chatgptReady"`
	BaseURL         string `json:"baseURL"`
	Port            int    `json:"port"`
	Uptime          int64  `json:"uptime"`
	Requests        int    `json:"requests"`
	Active          int    `json:"active"`
	Failures        int    `json:"failures"`
}

type headlessClient struct {
	dir       string
	host      string
	token     string
	runtime   terminalRuntime
	http      *http.Client
	transport *http.Transport
	app       *app // Non-nil only for an exclusively locked, offline profile.
	release   func()
	closeOnce sync.Once
}

func (c *headlessClient) call(ctx context.Context, method, path string, input, output any) error {
	if !strings.HasPrefix(path, "/api/") || strings.ContainsAny(path, "\r\n?#") {
		return errors.New("Invalid local control request.")
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return errors.New("Cannot encode the local control request.")
		}
		body = bytes.NewReader(data)
	} else if method != http.MethodGet {
		body = strings.NewReader("{}")
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+c.host+path, body)
	if err != nil {
		return errors.New("Cannot create the local control request.")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return errors.New("Cannot reach Kilo Proxy's private local control. Check the running service and --config-dir.")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, headlessControlLimit+1))
	if err != nil || len(data) > headlessControlLimit {
		return errors.New("Kilo Proxy returned an invalid local control response.")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &failure) == nil && failure.Error.Message != "" && len(failure.Error.Message) <= 2048 && strings.IndexFunc(failure.Error.Message, unicode.IsControl) == -1 {
			return errors.New(failure.Error.Message)
		}
		return fmt.Errorf("Kilo Proxy rejected the local control request (HTTP %d).", response.StatusCode)
	}
	if output != nil && json.Unmarshal(data, output) != nil {
		return errors.New("Kilo Proxy returned an invalid local control response.")
	}
	return nil
}

func (c *headlessClient) close() {
	c.closeOnce.Do(func() {
		if c.transport != nil {
			c.transport.CloseIdleConnections()
		}
		if c.app != nil {
			c.app.requestQuit()
			c.app.cancelLogin()
			ctx, cancel := context.WithTimeout(context.Background(), headlessShutdownTimeout)
			c.app.shutdownHeadlessProxy(ctx)
			cancel()
			c.app.chatgpt.cancel()
			ctx, cancel = context.WithTimeout(context.Background(), imageUploadCleanupTimeout+2*time.Second)
			c.app.drainImageUploads(ctx)
			cancel()
			_ = c.app.imageURLBackends.Close()
			c.app.drainUsageHistory()
			if transport, ok := c.app.transport.(*http.Transport); ok {
				transport.CloseIdleConnections()
			}
		}
		if c.release != nil {
			c.release()
		}
	})
}

// A missing or stale runtime descriptor never authorizes bypassing the profile
// lock. Only its current authenticated daemon or one exclusive offline client
// may edit the profile.
func openHeadlessClient(dir string) (*headlessClient, error) {
	if err := secureHeadlessProfile(dir); err != nil {
		return nil, err
	}
	if info, err := readTerminalRuntime(dir); err == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		c := &headlessClient{dir: dir, host: info.Host, token: info.Token, runtime: info, transport: transport}
		c.http = &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var state headlessStatus
		err = c.call(ctx, http.MethodGet, "/api/state", nil, &state)
		cancel()
		if err == nil && validateHeadlessStatus(state) == nil {
			return c, nil
		}
		c.close()
	}
	release, err := acquireProfileLock(dir)
	if err != nil {
		return nil, err
	}
	vault, err := newHeadlessVault(dir)
	if err != nil {
		release()
		return nil, err
	}
	a, err := newApp(dir, vault)
	if err != nil {
		release()
		return nil, errors.New("Cannot load the headless Kilo Proxy profile. Check its saved settings and permissions.")
	}
	a.headless = true
	if err := a.setHeadlessProfilePaths(); err != nil {
		a.requestQuit()
		a.cancelLogin()
		a.drainUsageHistory()
		release()
		return nil, err
	}
	a.billingAutoRefresh = false
	a.adminHost = "127.0.0.1:1" // In-memory control: this address is never bound.
	c := &headlessClient{dir: dir, host: a.adminHost, token: a.adminToken, app: a, release: release}
	c.http = &http.Client{Transport: headlessLocalTransport{handler: a.adminHandler()}}
	return c, nil
}

type headlessLocalTransport struct{ handler http.Handler }

func (t headlessLocalTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	w := &headlessControlWriter{header: make(http.Header)}
	t.handler.ServeHTTP(w, req)
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return &http.Response{StatusCode: w.status, Header: w.header, Body: io.NopCloser(bytes.NewReader(w.body.Bytes())), Request: req}, nil
}

type headlessControlWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *headlessControlWriter) Header() http.Header { return w.header }
func (w *headlessControlWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *headlessControlWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(data)
}

// Shutdown waits for connection states, but Close only cancels their contexts:
// an admitted handler can still be returning from an upstream or committing a
// profile file. Keep the profile lock until those handlers have all returned.
type headlessControlHandlers struct {
	handler http.Handler
	mu      sync.Mutex
	closing bool
	active  sync.WaitGroup
}

func (h *headlessControlHandlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		jsonError(w, http.StatusServiceUnavailable, "Kilo Proxy is stopping.")
		return
	}
	h.active.Add(1)
	h.mu.Unlock()
	defer h.active.Done()
	h.handler.ServeHTTP(w, r)
}

func (h *headlessControlHandlers) stopAccepting() {
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
}

func (h *headlessControlHandlers) wait() { h.active.Wait() }

func validateHeadlessStatus(state headlessStatus) error {
	want := "http://127.0.0.1:" + strconv.Itoa(state.Port) + "/v1"
	if state.Version == "" || len(state.Version) > 128 || strings.IndexFunc(state.Version, unicode.IsControl) != -1 || state.Port < 1024 || state.Port > 65535 || state.BaseURL != want || state.Uptime < 0 || state.Requests < 0 || state.Active < 0 || state.Failures < 0 {
		return errors.New("Kilo Proxy returned an invalid service status.")
	}
	return nil
}

func (a *app) shutdownHeadlessProxy(ctx context.Context) {
	a.mu.Lock()
	srv, listener := a.proxyServer, a.proxyListener
	a.proxyServer, a.proxyListener = nil, nil
	a.mu.Unlock()
	if srv != nil {
		if err := srv.Shutdown(ctx); err != nil {
			_ = srv.Close()
		}
	}
	if listener != nil {
		_ = listener.Close()
	}
	a.drainUsageHistory()
}

func serveHeadless(ctx context.Context, c *headlessClient, setup bool, stdout io.Writer) error {
	if c.app == nil {
		return errors.New("Kilo Proxy is already running with this profile.")
	}
	a := c.app
	if err := a.setHeadlessProfilePaths(); err != nil {
		return err
	}
	if err := writeSettings(a.dir, a.config); err != nil {
		return errors.New("Cannot save the headless profile settings.")
	}
	if err := a.start(); err != nil && !(setup && errors.Is(err, errMissingCredentials)) {
		return errors.New(describeProxyStartFailure(err, "en").Message)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return errors.New("Cannot open Kilo Proxy's private local control listener.")
	}
	a.adminHost = listener.Addr().String()
	c.host = a.adminHost
	controlCtx, cancelControls := context.WithCancel(context.Background())
	defer cancelControls()
	controls := &headlessControlHandlers{handler: a.adminHandler()}
	admin := &http.Server{Handler: controls, BaseContext: func(net.Listener) context.Context { return controlCtx }, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	cleanupRuntime, err := a.publishTerminalRuntime()
	if err != nil {
		_ = listener.Close()
		return errors.New("Cannot publish the private Kilo Proxy terminal connection file.")
	}
	defer cleanupRuntime()
	defer func() {
		controls.stopAccepting()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := admin.Shutdown(shutdown); err != nil {
			cancelControls()
			_ = admin.Close()
		}
		_ = listener.Close()
		// Cancellation-aware upstream calls and closed request bodies can now
		// finish. Do not release the descriptor or profile while they can write.
		controls.wait()
	}()
	serveDone := make(chan error, 1)
	go func() { serveDone <- admin.Serve(listener) }()
	a.mu.Lock()
	running, port := a.proxyServer != nil, a.config.Port
	a.mu.Unlock()
	if running {
		fmt.Fprintf(stdout, "Kilo Proxy %s headless is running at http://127.0.0.1:%d/v1.\n", version, port)
	} else {
		fmt.Fprintf(stdout, "Kilo Proxy %s headless is ready for setup. Configure the account, models and terminal commands in another terminal.\n", version)
	}
	printHeadlessCommandSuggestion(ctx, c, c.dir, stdout)
	select {
	case <-ctx.Done():
		a.requestQuit()
	case <-a.quit:
	case err := <-serveDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.New("Kilo Proxy's private local control listener stopped unexpectedly.")
		}
	}
	return nil
}

func headlessHelp(w io.Writer) {
	fmt.Fprintln(w, `Kilo Proxy headless — macOS and Linux

Usage: kilo-proxy-headless [--config-dir DIR] COMMAND [OPTIONS]

  configure    Save Kilo credentials and the local port
  login        Sign in to Kilo or ChatGPT with a device code
  logout       Remove a saved connection
  models       Manage the shared models and reasoning preferences
  commands     Install or print the kilo-* terminal commands
  connection   Show the API endpoint (add --show-key to display its local key)
  serve        Run the proxy in the foreground (use --setup for first setup)
  status       Show the service status (use --json for scripts)
  proxy        Start or stop inference in the running service
  stop         Stop the service and wait for cleanup
  service      Install or inspect a systemd/launchd user service

The headless profile is separate from the desktop app by default.
The proxy and its private control API listen only on 127.0.0.1.`)
}

func parseHeadlessCommand(args []string) (command, dir string, rest []string, recognized bool, err error) {
	known := map[string]bool{"serve": true, "status": true, "stop": true, "proxy": true, "configure": true, "login": true, "logout": true, "models": true, "commands": true, "connection": true, "service": true, "help": true}
	leading := 0
	dirSpecified := false
	for leading < len(args) && strings.HasPrefix(args[leading], "--config-dir") {
		value := args[leading]
		if value == "--config-dir" {
			if dirSpecified {
				return "", "", nil, true, errors.New("Specify --config-dir only once.")
			}
			if leading+1 >= len(args) {
				return "", "", nil, headlessBinary, errors.New("--config-dir requires a directory.")
			}
			dir = args[leading+1]
			dirSpecified = true
			leading += 2
		} else if strings.HasPrefix(value, "--config-dir=") {
			if dirSpecified {
				return "", "", nil, true, errors.New("Specify --config-dir only once.")
			}
			dir = strings.TrimPrefix(value, "--config-dir=")
			dirSpecified = true
			leading++
		} else {
			break
		}
	}
	if leading == len(args) {
		return "help", dir, nil, headlessBinary, nil
	}
	command = args[leading]
	if command == "--help" || command == "-h" {
		return "help", dir, nil, headlessBinary, nil
	}
	if command == "--version" && headlessBinary {
		return "version", dir, nil, true, nil
	}
	if !known[command] {
		return command, dir, nil, headlessBinary, errors.New("Unknown headless command. Run kilo-proxy-headless --help.")
	}
	recognized = true
	trailing := args[leading+1:]
	for i := 0; i < len(trailing); i++ {
		value := trailing[i]
		if value == "--" {
			rest = append(rest, trailing[i:]...)
			break
		}
		if value == "--config-dir" {
			if i+1 >= len(trailing) {
				return command, dir, nil, true, errors.New("--config-dir requires a directory.")
			}
			if dirSpecified {
				return command, dir, nil, true, errors.New("Specify --config-dir only once.")
			}
			dir = trailing[i+1]
			dirSpecified = true
			i++
		} else if strings.HasPrefix(value, "--config-dir=") {
			if dirSpecified {
				return command, dir, nil, true, errors.New("Specify --config-dir only once.")
			}
			dir = strings.TrimPrefix(value, "--config-dir=")
			dirSpecified = true
		} else {
			rest = append(rest, value)
		}
	}
	if dirSpecified && dir == "" {
		return command, dir, nil, recognized, errors.New("--config-dir requires a directory.")
	}
	return command, dir, rest, recognized, nil
}

func runHeadlessCLI(args []string) (bool, int) {
	command, inputDir, rest, handled, err := parseHeadlessCommand(args)
	if !handled {
		return false, 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return true, 2
	}
	if command == "help" {
		headlessHelp(os.Stdout)
		return true, 0
	}
	if command == "version" {
		fmt.Fprintln(os.Stdout, version)
		return true, 0
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "The headless commands are available on macOS and Linux.")
		return true, 1
	}
	dir, err := headlessConfigDir(inputDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return true, 1
	}
	switch command {
	case "configure", "login", "logout", "models", "commands", "connection":
		return true, runHeadlessSetupCLI(dir, command, rest, os.Stdin, os.Stdout, os.Stderr)
	case "service":
		return true, runHeadlessServiceCLI(dir, rest, os.Stdout, os.Stderr)
	default:
		return true, runHeadlessRuntimeCLI(dir, command, rest, os.Stdout, os.Stderr)
	}
}

func runHeadlessRuntimeCLI(dir, command string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var setup, jsonOutput bool
	if command == "serve" {
		flags.BoolVar(&setup, "setup", false, "Keep the service running for first-time setup")
	}
	if command == "status" {
		flags.BoolVar(&jsonOutput, "json", false, "Print redacted service status as JSON")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(stdout, "Use %s with the options listed by kilo-proxy-headless --help.\n", command)
			return 0
		}
		fmt.Fprintln(stderr, "Invalid command options. Run kilo-proxy-headless --help.")
		return 2
	}
	if command == "proxy" {
		if len(flags.Args()) != 1 || flags.Args()[0] != "start" && flags.Args()[0] != "stop" {
			fmt.Fprintln(stderr, "Usage: proxy start|stop")
			return 2
		}
	} else if len(flags.Args()) != 0 {
		fmt.Fprintln(stderr, "Unexpected command arguments.")
		return 2
	}
	c, err := openHeadlessClient(dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer c.close()
	controlTimeout := 30 * time.Second
	if command == "stop" {
		controlTimeout = 75 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	switch command {
	case "serve":
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		err = serveHeadless(ctx, c, setup, stdout)
	case "status":
		var state headlessStatus
		err = c.call(ctx, http.MethodGet, "/api/state", nil, &state)
		if err == nil {
			err = validateHeadlessStatus(state)
		}
		if err == nil {
			if jsonOutput {
				err = json.NewEncoder(stdout).Encode(state)
			} else {
				mode := "stopped"
				if c.app == nil {
					mode = "service running; proxy stopped"
				}
				if state.Running {
					mode = "running"
				}
				fmt.Fprintf(stdout, "Kilo Proxy %s: %s\nProxy: %s\nConnection ready: %t\nRequests: %d; active: %d; failures: %d\n", state.Version, mode, state.BaseURL, state.ConnectionReady, state.Requests, state.Active, state.Failures)
			}
		}
	case "stop":
		if c.app != nil {
			fmt.Fprintln(stdout, "Kilo Proxy is already stopped.")
			return 0
		}
		err = c.call(ctx, http.MethodPost, "/api/quit", nil, nil)
		if err == nil {
			err = waitHeadlessStopped(ctx, dir, c.runtime)
		}
		if err == nil {
			fmt.Fprintln(stdout, "Kilo Proxy stopped.")
		}
	case "proxy":
		if c.app != nil {
			fmt.Fprintln(stderr, "Start the headless service with serve before controlling the proxy.")
			return 1
		}
		err = c.call(ctx, http.MethodPost, "/api/"+flags.Args()[0], nil, nil)
		if err == nil {
			fmt.Fprintf(stdout, "Proxy %s completed.\n", flags.Args()[0])
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func waitHeadlessStopped(ctx context.Context, dir string, previous terminalRuntime) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := readTerminalRuntime(dir)
		if err != nil || current != previous {
			if err == nil {
				return nil // A newer instance already owns the profile.
			}
			release, lockErr := acquireProfileLock(filepath.Clean(dir))
			if lockErr == nil {
				release()
				return nil
			}
			if !errors.Is(lockErr, errProfileLocked) {
				return lockErr
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("Kilo Proxy has not finished stopping. Check the service before restarting it.")
		case <-ticker.C:
		}
	}
}

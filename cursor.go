package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode"
)

// Cursor has a separate, explicitly started ingress. Never expose adminHandler
// or relax the Host/Origin restrictions of the ordinary loopback proxy.
type cursorSession struct {
	Status   string   `json:"status"`
	URL      string   `json:"baseURL"`
	Key      string   `json:"key"`
	Models   []string `json:"models"`
	Error    string   `json:"error,omitempty"`
	cancel   context.CancelFunc
	server   *http.Server
	listener net.Listener
}

func ngrokPath() string {
	if p, err := exec.LookPath("ngrok"); err == nil {
		return p
	}
	candidates := []string{"/opt/homebrew/bin/ngrok", "/usr/local/bin/ngrok"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", "ngrok"))
		if runtime.GOOS == "windows" {
			candidates = append(candidates, filepath.Join(home, "scoop", "shims", "ngrok.exe"))
		}
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && (runtime.GOOS == "windows" || st.Mode()&0111 != 0) {
			return p
		}
	}
	return ""
}

func cursorModelsValid(models []string) bool {
	if len(models) == 0 || len(models) > 50 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range models {
		if len(id) == 0 || len(id) > 256 || strings.ContainsAny(id, " \t\r\n\\\"<>") || seen[id] {
			return false
		}
		for _, ch := range id {
			if unicode.IsSpace(ch) || unicode.IsControl(ch) {
				return false
			}
		}
		seen[id] = true
	}
	return true
}

func (a *app) cursorAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		a.mu.Lock()
		defer a.mu.Unlock()
		jsonResponse(w, 200, map[string]any{"installed": ngrokPath() != "", "session": a.cursor})
		return
	}
	var input struct {
		Action string   `json:"action"`
		Models []string `json:"models"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	switch input.Action {
	case "check":
		a.checkCursor(w, r)
		return
	case "stop":
		a.mu.Lock()
		a.stopCursorLocked()
		a.mu.Unlock()
	case "start":
		if !cursorModelsValid(input.Models) {
			jsonError(w, 400, "Select between 1 and 50 unique model IDs.")
			return
		}
		if err := a.startCursor(input.Models); err != nil {
			jsonError(w, 409, err.Error())
			return
		}
	default:
		jsonError(w, 400, "Unknown Cursor action.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	jsonResponse(w, 200, map[string]any{"installed": ngrokPath() != "", "session": a.cursor})
}

func (a *app) stopCursorLocked() {
	if s := a.cursor; s != nil {
		if s.cancel != nil {
			s.cancel()
		}
		if s.listener != nil {
			_ = s.listener.Close()
		}
		if s.server != nil {
			_ = s.server.Close()
		}
	}
	a.cursor = nil
}

func (a *app) startCursor(models []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proxyServer == nil {
		return errors.New("Start the local proxy first.")
	}
	if a.cursor != nil && (a.cursor.Status == "starting" || a.cursor.Status == "running") {
		return errors.New("Stop the Cursor connection before changing models.")
	}
	binary := ngrokPath()
	if binary == "" {
		return errors.New("Install ngrok and configure its account authtoken first, then restart Kilo Local.")
	}
	a.stopCursorLocked()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return errors.New("Could not open the Cursor ingress.")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &cursorSession{Status: "starting", Key: randomKey("kl_cursor_"), Models: append([]string(nil), models...), cancel: cancel, listener: ln}
	delegate := a.inferenceHandler(a.apiKey, a.config.OrgID, s.Key, "cursor.internal")
	s.server = &http.Server{Handler: cursorIngress(s.Key, s.Models, delegate), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 << 10}
	cmd := exec.CommandContext(ctx, binary, "http", "http://"+ln.Addr().String(), "--inspect=false", "--log=stdout", "--log-format=json")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = ln.Close()
		return errors.New("Could not start ngrok.")
	}
	// Do not retain ngrok logs: they can contain account credentials and traffic.
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		cancel()
		_ = ln.Close()
		return errors.New("Could not start ngrok. Check its installation.")
	}
	a.cursor = s
	go func() { _ = s.server.Serve(ln) }()
	go func() {
		errorCode := ""
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			if code := cursorNgrokError.Find(scanner.Bytes()); code != nil {
				errorCode = string(code)
			}
			if endpoint := cursorTunnelURL(scanner.Bytes()); endpoint != "" {
				a.mu.Lock()
				if a.cursor == s && s.Status == "starting" {
					s.Status = "running"
					s.URL = endpoint + "/v1"
				}
				a.mu.Unlock()
			}
		}
		if scanner.Err() != nil {
			cancel()
		}
		_ = cmd.Wait()
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.cursor == s {
			cancel()
			_ = ln.Close()
			_ = s.server.Close()
			s.Status = "error"
			s.Key = ""
			s.URL = ""
			if s.Error == "" {
				s.Error = "ngrok stopped. Check ngrok config check, account limits, and network access, then reconnect."
			}
			if errorCode != "" {
				s.Error = errorCode + ": " + s.Error
			}
		}
	}()
	go func() {
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.cursor == s && s.Status == "starting" {
			cancel()
			_ = ln.Close()
			_ = s.server.Close()
			s.Status = "error"
			s.Key = ""
			s.Error = "ngrok did not connect within 30 seconds. Check your ngrok account and network."
		}
	}()
	return nil
}

var cursorNgrokError = regexp.MustCompile(`ERR_NGROK_[0-9]{1,12}\b`)

func cursorTunnelURL(line []byte) string {
	var entry struct {
		Msg string `json:"msg"`
		URL string `json:"url"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Msg != "started tunnel" {
		return ""
	}
	u, err := url.Parse(entry.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return ""
	}
	return strings.TrimRight(entry.URL, "/")
}

func cursorIngress(key string, models []string, delegate http.Handler) http.Handler {
	slots := make(chan struct{}, 8)
	allowed := map[string]bool{}
	for _, id := range models {
		allowed[id] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !secureEqual(r.Header.Get("Authorization"), "Bearer "+key) {
			jsonError(w, 401, "Invalid Cursor key.")
			return
		}
		if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
			jsonError(w, 403, "Browser requests are not supported.")
			return
		}
		if r.URL.RawQuery != "" || r.URL.RawPath != "" {
			jsonError(w, 404, "Unsupported Cursor endpoint.")
			return
		}
		if r.Method == "GET" && r.URL.Path == "/v1/models" {
			list := make([]map[string]string, 0, len(models))
			for _, id := range models {
				list = append(list, map[string]string{"id": id, "object": "model", "owned_by": "kilo"})
			}
			jsonResponse(w, 200, map[string]any{"object": "list", "data": list})
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" {
			jsonError(w, 404, "Use /v1/chat/completions.")
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			jsonError(w, 429, "Too many concurrent Cursor requests.")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
		if err != nil {
			jsonError(w, 413, "Cursor request exceeds 16 MiB.")
			return
		}
		var payload struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(body, &payload) != nil || !allowed[payload.Model] {
			jsonError(w, 400, "Select a model enabled in the Kilo Local Cursor helper.")
			return
		}
		clone := r.Clone(r.Context())
		clone.Host = "cursor.internal"
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.ContentLength = int64(len(body))
		// Internal authentication remains the dedicated Cursor key, so request traces
		// redact it through the existing capture pipeline.
		delegate.ServeHTTP(w, clone)
	})
}

// Reachability check uses only the locally generated model list; no prompt or
// organization credit is sent. Redirects must never receive the Cursor token.
func (a *app) checkCursor(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	s := a.cursor
	if s == nil || s.Status != "running" {
		a.mu.Unlock()
		jsonError(w, 409, "Connect Cursor first.")
		return
	}
	endpoint, key := s.URL, s.Key
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/models", nil)
	if err != nil {
		jsonError(w, 502, "Invalid tunnel URL.")
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("ngrok-skip-browser-warning", "1")
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		jsonError(w, 502, "Could not reach the public HTTPS endpoint. Check ngrok and your network.")
		return
	}
	defer resp.Body.Close()
	var result struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result) != nil || result.Object != "list" || len(result.Data) != len(s.Models) {
		jsonError(w, 502, "The public endpoint did not return the selected models. Check tunnel routing.")
		return
	}
	for i, m := range result.Data {
		if m.ID != s.Models[i] {
			jsonError(w, 502, "Unexpected model list from the public endpoint.")
			return
		}
	}
	a.mu.Lock()
	current := a.cursor == s && s.Status == "running"
	a.mu.Unlock()
	if !current {
		jsonError(w, 409, "Cursor connection changed during the check. Try again.")
		return
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

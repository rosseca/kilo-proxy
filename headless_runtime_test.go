package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func headlessTestClient(t *testing.T) *headlessClient {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("headless runtime is released for macOS and Linux")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	c, err := openHeadlessClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	return c
}

func headlessTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func awaitHeadlessRuntime(t *testing.T, dir string, done <-chan error) terminalRuntime {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, err := readTerminalRuntime(dir); err == nil {
			return info
		}
		select {
		case err := <-done:
			t.Fatal("service exited before becoming ready", err)
		case <-deadline.C:
			t.Fatal("service did not publish its runtime")
		case <-ticker.C:
		}
	}
}

func TestHeadlessCommandParsingPreservesDesktopAndForwardsSetup(t *testing.T) {
	for _, args := range [][]string{{"serve", "--config-dir", "/tmp/kilo", "--setup"}, {"--config-dir=/tmp/kilo", "serve", "--setup"}} {
		command, dir, rest, handled, err := parseHeadlessCommand(args)
		if err != nil || !handled || command != "serve" || dir != "/tmp/kilo" || len(rest) != 1 || rest[0] != "--setup" {
			t.Fatal("config-dir was not forwarded", command, dir, rest, handled, err)
		}
	}
	for _, args := range [][]string{{"serve", "--config-dir", ""}, {"--config-dir", "/tmp/a", "serve", "--config-dir", "/tmp/b"}, {"serve", "--config-dir"}} {
		if _, _, _, handled, err := parseHeadlessCommand(args); err == nil || !handled {
			t.Fatal("invalid common flags accepted", args)
		}
	}
	if !headlessBinary {
		for _, args := range [][]string{nil, {"--config-dir", "/tmp/kilo", "--no-tray"}, {"--version"}} {
			if _, _, _, handled, _ := parseHeadlessCommand(args); handled {
				t.Fatal("headless dispatcher stole the desktop flag invocation", args)
			}
		}
	}
}

func TestHeadlessOfflineControlUsesPrivateProfileAndStatusOmitsSecrets(t *testing.T) {
	c := headlessTestClient(t)
	c.app.apiKey = "upstream-secret-should-never-be-printed"
	c.app.config.OrgID = "private-org"
	c.app.accountEmail = "private@example.invalid"
	var state headlessStatus
	if err := c.call(context.Background(), "GET", "/api/state", nil, &state); err != nil {
		t.Fatal(err)
	}
	if err := validateHeadlessStatus(state); err != nil || !state.KiloReady || state.Running {
		t.Fatal("unexpected offline status", state, err)
	}
	encoded, _ := json.Marshal(state)
	for _, secret := range []string{c.app.apiKey, c.app.config.LocalKey, c.app.adminToken, c.app.config.OrgID, c.app.accountEmail, "localKey", "auth", "email"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatal("status included sensitive information", secret)
		}
	}
	if _, err := os.Stat(filepath.Join(c.dir, terminalRuntimeFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("offline setup published a runtime")
	}
	if _, err := openHeadlessClient(c.dir); !errors.Is(err, errProfileLocked) {
		t.Fatal("missing descriptor bypassed an existing lock", err)
	}
	stale := terminalRuntime{Version: 1, Host: "127.0.0.1:" + strconv.Itoa(headlessTestPort(t)), Token: randomKey(""), CodexProfile: filepath.Join(c.dir, "codex"), ClaudeProfile: filepath.Join(c.dir, "claude")}
	data, _ := json.Marshal(stale)
	if err := atomicCatalogFile(filepath.Join(c.dir, terminalRuntimeFile), data); err != nil {
		t.Fatal(err)
	}
	if _, err := openHeadlessClient(c.dir); !errors.Is(err, errProfileLocked) {
		t.Fatal("stale descriptor bypassed an existing lock", err)
	}
}

func TestHeadlessServeRequiresConnectionAndDoesNotPublishOnFailure(t *testing.T) {
	c := headlessTestClient(t)
	var output bytes.Buffer
	if err := serveHeadless(context.Background(), c, false, &output); err == nil {
		t.Fatal("service started without a connection")
	}
	if output.Len() != 0 || c.app.proxyServer != nil {
		t.Fatal("failed startup advertised or opened the proxy")
	}
	if _, err := readTerminalRuntime(c.dir); err == nil {
		t.Fatal("failed startup published a runtime")
	}
	c.app.apiKey, c.app.config.OrgID = "fixture-key", "fixture-org"
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	c.app.config.Port = listener.Addr().(*net.TCPAddr).Port
	if err := serveHeadless(context.Background(), c, true, &output); err == nil || !strings.Contains(err.Error(), strconv.Itoa(c.app.config.Port)) {
		t.Fatal("occupied port was not reported correctly", err)
	}
	if _, err := readTerminalRuntime(c.dir); err == nil {
		t.Fatal("listener failure published a runtime")
	}
}

func TestHeadlessSetupLiveConfigurationStatusAndStop(t *testing.T) {
	c := headlessTestClient(t)
	c.app.config.Port = headlessTestPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var serviceOutput bytes.Buffer
	done := make(chan error, 1)
	go func() {
		err := serveHeadless(ctx, c, true, &serviceOutput)
		c.close()
		done <- err
	}()
	info := awaitHeadlessRuntime(t, c.dir, done)
	for _, path := range []string{info.CodexProfile, info.ClaudeProfile, info.OMPProfile, info.OpenCodeConfig} {
		if !strings.HasPrefix(path, filepath.Join(c.dir, "profiles")+string(filepath.Separator)) {
			t.Fatal("headless terminal runtime pointed outside its private profiles", path)
		}
	}
	remote, err := openHeadlessClient(c.dir)
	if err != nil || remote.app != nil {
		t.Fatal("client did not connect to the authenticated daemon", err)
	}
	defer remote.close()
	if _, err := acquireProfileLock(c.dir); !errors.Is(err, errProfileLocked) {
		t.Fatal("service did not own its profile lock", err)
	}
	key := "headless-upstream-secret"
	if err := remote.call(ctx, "POST", "/api/config", map[string]any{"apiKey": key, "orgId": "fixture-org", "port": c.app.config.Port, "remember": true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := remote.call(ctx, "POST", "/api/start", nil, nil); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := runHeadlessRuntimeCLI(c.dir, "status", []string{"--json"}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	var state headlessStatus
	if err := json.Unmarshal(out.Bytes(), &state); err != nil || !state.Running || !state.ConnectionReady {
		t.Fatal("status did not report the running listener", err, out.String())
	}
	for _, secret := range []string{key, info.Token, c.app.config.LocalKey, "localKey", "orgId", "auth"} {
		if strings.Contains(out.String(), secret) {
			t.Fatal("status leaked credentials or account information", secret)
		}
	}
	out.Reset()
	if code := runHeadlessRuntimeCLI(c.dir, "proxy", []string{"stop"}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if err := remote.call(ctx, "GET", "/api/state", nil, &state); err != nil || state.Running {
		t.Fatal("proxy stop also lost control or kept listener running", err)
	}
	out.Reset()
	if code := runHeadlessRuntimeCLI(c.dir, "stop", nil, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service did not finish stopping")
	}
	if _, err := readTerminalRuntime(c.dir); err == nil {
		t.Fatal("stopped service retained its runtime")
	}
	if strings.Contains(serviceOutput.String(), key) || strings.Contains(serviceOutput.String(), info.Token) || strings.Contains(serviceOutput.String(), c.app.config.LocalKey) {
		t.Fatal("serve logs exposed credentials")
	}
}

func TestHeadlessShutdownDrainsInflightInference(t *testing.T) {
	c := headlessTestClient(t)
	c.app.apiKey, c.app.config.OrgID = "fixture-upstream", "fixture-org"
	c.app.config.Port = headlessTestPort(t)
	entered, releaseUpstream := make(chan struct{}), make(chan struct{})
	upstreamCancelled := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-releaseUpstream:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		case <-r.Context().Done():
			upstreamCancelled <- struct{}{}
		}
	}))
	defer upstream.Close()
	c.app.upstream, _ = url.Parse(upstream.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		err := serveHeadless(ctx, c, false, io.Discard)
		c.close()
		done <- err
	}()
	awaitHeadlessRuntime(t, c.dir, done)
	request, _ := http.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(c.app.config.Port)+"/v1/chat/completions", strings.NewReader(`{"model":"vendor/test","messages":[]}`))
	request.Header.Set("Authorization", "Bearer "+c.app.config.LocalKey)
	request.Header.Set("Content-Type", "application/json")
	responseDone := make(chan error, 1)
	go func() {
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode != 200 {
				err = errors.New("inference did not finish successfully")
			}
		}
		responseDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("inference did not reach the upstream")
	}
	cancel()
	select {
	case <-upstreamCancelled:
		close(releaseUpstream)
		t.Fatal("shutdown canceled an in-flight request before its drain deadline")
	case <-done:
		close(releaseUpstream)
		t.Fatal("service exited without draining its in-flight request")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseUpstream)
	if err := <-responseDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service did not finish its graceful shutdown")
	}
	if _, err := readTerminalRuntime(c.dir); err == nil {
		t.Fatal("signal shutdown retained its runtime")
	}
	release, err := acquireProfileLock(c.dir)
	if err != nil {
		t.Fatal("shutdown did not release the profile", err)
	}
	release()
}

func TestHeadlessShutdownDrainsChatGPTCredentialRefresh(t *testing.T) {
	c := headlessTestClient(t)
	c.app.config.Port = headlessTestPort(t)
	c.app.chatgpt.creds = chatGPTCredentials{Access: chatGPTTestJWT("headless-account"), Refresh: "headless-refresh", Account: "headless-account", Expires: time.Now().Add(-time.Hour).Unix()}
	refreshEntered, releaseRefresh := make(chan struct{}), make(chan struct{})
	refreshCancelled := make(chan struct{}, 1)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRefresh) }) }
	defer release()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_, _ = io.Copy(io.Discard, r.Body)
			close(refreshEntered)
			select {
			case <-releaseRefresh:
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": chatGPTTestJWT("headless-account"), "refresh_token": "rotated-headless-refresh", "expires_in": 3600})
			case <-r.Context().Done():
				refreshCancelled <- struct{}{}
			}
		case "/responses":
			subscriptionTextSSE(w)
		default:
			t.Error("unexpected upstream request", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer func() { release(); upstream.Close() }()
	c.app.chatgpt.authURL, c.app.chatGPTResponsesURL = upstream.URL, upstream.URL+"/responses"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		err := serveHeadless(ctx, c, false, io.Discard)
		c.close()
		done <- err
	}()
	awaitHeadlessRuntime(t, c.dir, done)
	request, _ := http.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(c.app.config.Port)+"/v1/responses", strings.NewReader(`{"model":"chatgpt/gpt-test","input":"Hello"}`))
	request.Header.Set("Authorization", "Bearer "+c.app.config.LocalKey)
	request.Header.Set("Content-Type", "application/json")
	responseDone := make(chan error, 1)
	go func() {
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err == nil {
			data, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			err = readErr
			if response.StatusCode != 200 || !bytes.Contains(data, []byte("Subscription works")) {
				err = errors.New("ChatGPT inference did not finish after its credential refresh")
			}
		}
		responseDone <- err
	}()
	select {
	case <-refreshEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("inference did not begin its ChatGPT credential refresh")
	}
	cancel()
	select {
	case <-refreshCancelled:
		t.Fatal("shutdown canceled an in-flight ChatGPT refresh before the drain deadline")
	case <-done:
		t.Fatal("shutdown finished before its in-flight ChatGPT refresh")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-responseDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service did not stop after completing the refreshed ChatGPT request")
	}
	if _, err := readTerminalRuntime(c.dir); err == nil {
		t.Fatal("stopped ChatGPT service retained its runtime")
	}
}

type headlessControlUnwindTransport struct {
	base      http.RoundTripper
	cancelled chan struct{}
	finish    <-chan struct{}
	once      sync.Once
}

func (t *headlessControlUnwindTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(r)
	if r.Context().Err() != nil {
		t.once.Do(func() { close(t.cancelled) })
		// Model a cancellation-aware upstream that is still unwinding cleanup
		// before returning to the profile-writing catalog handler.
		<-t.finish
	}
	return response, err
}

func TestHeadlessShutdownCancelsAndJoinsActiveControlRequests(t *testing.T) {
	c := headlessTestClient(t)
	c.app.apiKey, c.app.config.OrgID = "synthetic-key", "synthetic-org"
	c.app.config.Port = headlessTestPort(t)
	entered, allowUpstream, finishControl := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(allowUpstream); close(finishControl) })
	}
	defer release()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-allowUpstream:
			jsonResponse(w, 200, map[string]any{"data": []any{map[string]any{"id": "vendor/after-stop"}}})
		}
	}))
	defer func() { release(); upstream.Close() }()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	defer base.CloseIdleConnections()
	delayed := &headlessControlUnwindTransport{base: base, cancelled: make(chan struct{}), finish: finishControl}
	c.app.transport = delayed
	setUpstream(c.app, upstream.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { err := serveHeadless(ctx, c, false, io.Discard); c.close(); done <- err }()
	info := awaitHeadlessRuntime(t, c.dir, done)
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	request, _ := http.NewRequestWithContext(requestCtx, "POST", "http://"+info.Host+"/api/headless/catalog", strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer "+info.Token)
	request.Header.Set("Content-Type", "application/json")
	requestDone := make(chan error, 1)
	go func() {
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("control request did not reach its catalog upstream")
	}
	cancel()
	select {
	case <-delayed.cancelled:
	case <-done:
		t.Fatal("shutdown released the profile while its control request was still active")
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel the active control request after its grace period")
	}
	if _, err := acquireProfileLock(c.dir); !errors.Is(err, errProfileLocked) {
		t.Fatal("profile lock released before the control handler finished", err)
	}
	select {
	case <-done:
		t.Fatal("shutdown did not join the canceled control handler")
	default:
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish after its control handler returned")
	}
	select {
	case <-requestDone:
	case <-time.After(5 * time.Second):
		t.Fatal("closed control client did not return")
	}
	if _, err := readTerminalRuntime(c.dir); err == nil {
		t.Fatal("stopped control server retained its runtime")
	}
	if _, err := os.Stat(filepath.Join(c.dir, "model-catalog.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled catalog control persisted a result", err)
	}
	unlock, err := acquireProfileLock(c.dir)
	if err != nil {
		t.Fatal("finished control shutdown retained the profile lock", err)
	}
	unlock()
}

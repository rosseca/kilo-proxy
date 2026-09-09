package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const terminalRuntimeFile = "terminal-runtime.json"

type terminalRuntime struct {
	Version       int    `json:"version"`
	Host          string `json:"host"`
	Token         string `json:"token"`
	CodexProfile  string `json:"codexProfile"`
	ClaudeProfile string `json:"claudeProfile"`
}

var errTerminalAppUnavailable = errors.New("Open the updated Kilo Proxy app first. It can stay in the system tray while you use kilo-codex or kilo-claude.")

func validateTerminalRuntime(info terminalRuntime) error {
	host, port, err := net.SplitHostPort(info.Host)
	p, numberErr := strconv.Atoi(port)
	token, tokenErr := hex.DecodeString(info.Token)
	if err != nil || numberErr != nil || host != "127.0.0.1" || p < 1 || p > 65535 || info.Host != net.JoinHostPort(host, strconv.Itoa(p)) || info.Version != 1 || tokenErr != nil || len(token) != 32 {
		return errTerminalAppUnavailable
	}
	for _, path := range []string{info.CodexProfile, info.ClaudeProfile} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
			return errTerminalAppUnavailable
		}
	}
	return nil
}

func readTerminalRuntime(dir string) (terminalRuntime, error) {
	var empty terminalRuntime
	path := filepath.Join(dir, terminalRuntimeFile)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<10 || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return empty, errTerminalAppUnavailable
	}
	data, err := readCatalogFile(path)
	if err != nil || json.Unmarshal(data, &empty) != nil || validateTerminalRuntime(empty) != nil {
		return terminalRuntime{}, errTerminalAppUnavailable
	}
	return empty, nil
}

func (a *app) publishTerminalRuntime() (func(), error) {
	info := terminalRuntime{Version: 1, Host: a.adminHost, Token: a.adminToken}
	home := a.launchRuntime().home
	info.CodexProfile, info.ClaudeProfile = a.codexCLIProfileDir, a.claudeProfileDir
	if info.CodexProfile == "" {
		info.CodexProfile = filepath.Join(home, ".codex-kilo-cli")
	}
	if info.ClaudeProfile == "" {
		info.ClaudeProfile = filepath.Join(home, ".claude-kilo")
	}
	if err := validateTerminalRuntime(info); err != nil {
		return func() {}, err
	}
	if err := os.MkdirAll(a.dir, 0700); err != nil {
		return func() {}, err
	}
	st, err := os.Lstat(a.dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return func() {}, errTerminalAppUnavailable
	}
	data, _ := json.Marshal(info)
	path := filepath.Join(a.dir, terminalRuntimeFile)
	if err := atomicCatalogFile(path, data); err != nil {
		return func() {}, err
	}
	return func() {
		current, err := readTerminalRuntime(a.dir)
		if err == nil && current == info {
			_ = os.Remove(path)
		}
	}, nil
}

func requestTerminalPlan(ctx context.Context, dir string, input terminalPrepareRequest) (clientLaunchPlan, error) {
	var plan clientLaunchPlan
	info, err := readTerminalRuntime(dir)
	if err != nil {
		return plan, err
	}
	data, _ := json.Marshal(input)
	request, err := http.NewRequestWithContext(ctx, "POST", "http://"+info.Host+"/api/terminal/prepare", bytes.NewReader(data))
	if err != nil {
		return plan, errTerminalAppUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+info.Token)
	request.Header.Set("Content-Type", "application/json")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return plan, errTerminalAppUnavailable
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, clientLaunchTicketLimit+1))
	if err != nil || len(body) > clientLaunchTicketLimit {
		return plan, errors.New("Kilo Proxy returned an invalid terminal profile.")
	}
	if response.StatusCode != 200 {
		if response.StatusCode == 401 || response.StatusCode == 404 || response.StatusCode >= 300 && response.StatusCode < 400 {
			return plan, errTerminalAppUnavailable
		}
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &failure) == nil && len(failure.Error.Message) > 0 && len(failure.Error.Message) < 1024 && strings.IndexFunc(failure.Error.Message, unicode.IsControl) == -1 {
			return plan, errors.New(failure.Error.Message)
		}
		return plan, errors.New("Kilo Proxy could not prepare this agent. Check Models and Settings in the app.")
	}
	if json.Unmarshal(body, &plan) != nil || plan.Client != input.Client || plan.Kind != "terminal" || plan.Directory != filepath.Clean(input.Directory) {
		return clientLaunchPlan{}, errors.New("Kilo Proxy returned an invalid terminal profile.")
	}
	if err := validateTerminalProfile(plan, info); err != nil {
		return clientLaunchPlan{}, err
	}
	return plan, nil
}

// The runtime file is private, but a stale loopback port can belong to another
// service. Never accept executable paths, shell fragments, arbitrary flags or
// environment variables from a response. Profile locations come from disk.
func validateTerminalProfile(plan clientLaunchPlan, info terminalRuntime) error {
	invalid := errors.New("Kilo Proxy returned an invalid terminal profile.")
	name, _ := launchClientIdentity(plan.Client)
	if plan.Name != name || plan.Executable != "" {
		return invalid
	}
	switch plan.Client {
	case "codex-cli":
		key := plan.Env["KILO_LOCAL_API_KEY"]
		if len(plan.Args) != 0 || len(plan.Unset) != 0 || len(plan.Env) != 2 || plan.Env["CODEX_HOME"] != info.CodexProfile || len(key) < 1 || len(key) > 1024 || strings.ContainsAny(key, "\x00\r\n") {
			return invalid
		}
	case "claude":
		if len(plan.Env) != 1 || plan.Env["CLAUDE_CONFIG_DIR"] != info.ClaudeProfile || !reflect.DeepEqual(plan.Args, []string{"--settings", filepath.Join(info.ClaudeProfile, "settings.json")}) || !reflect.DeepEqual(plan.Unset, nativeClaudeResetEnv) {
			return invalid
		}
	default:
		return invalid
	}
	return nil
}

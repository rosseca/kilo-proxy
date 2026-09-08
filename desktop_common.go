package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// desktopBridge keeps the privileged OS operations out of the frontend. The
// ordinary authenticated loopback API remains usable in explicit browser mode.
type desktopBridge interface {
	CopyText(string) error
	OpenExternal(string) error
}

type desktopProbe struct {
	ID    string          `json:"id"`
	Value json.RawMessage `json:"value"`
	Error string          `json:"error"`
}

func externalDesktopURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.ContainsAny(raw, "\r\n\x00") {
		return "", errors.New("Only HTTPS links can be opened outside Kilo Proxy")
	}
	return u.String(), nil
}

func (a *app) desktopAPI(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	desktop, probes := a.desktop, a.desktopProbes
	a.mu.Unlock()
	if desktop == nil || r.Method != "POST" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/api/desktop/probe" {
		if probes == nil {
			http.NotFound(w, r)
			return
		}
		var result desktopProbe
		if !decodeDesktopJSON(w, r, 64<<10, &result) {
			return
		}
		select {
		case probes <- result:
		default:
		}
		jsonResponse(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.URL.Path != "/api/desktop/copy" && r.URL.Path != "/api/desktop/open" {
		http.NotFound(w, r)
		return
	}
	var input struct {
		Text string `json:"text"`
		URL  string `json:"url"`
	}
	if !decodeDesktopJSON(w, r, 1<<20, &input) {
		return
	}
	var err error
	switch r.URL.Path {
	case "/api/desktop/copy":
		err = desktop.CopyText(input.Text)
	case "/api/desktop/open":
		var address string
		address, err = externalDesktopURL(input.URL)
		if err == nil {
			err = desktop.OpenExternal(address)
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}

// Read the complete bounded body before invoking an OS operation. A decoder's
// first Decode alone can accept a small object followed by unlimited trailing
// data, so both the size limit and the single-object boundary matter here.
func decodeDesktopJSON(w http.ResponseWriter, r *http.Request, limit int64, into any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		jsonError(w, http.StatusUnsupportedMediaType, "JSON is required")
		return false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	raw = bytes.TrimSpace(raw)
	if err != nil || len(raw) == 0 || raw[0] != '{' {
		jsonError(w, http.StatusBadRequest, "Invalid desktop request")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		jsonError(w, http.StatusBadRequest, "Invalid desktop request")
		return false
	}
	return true
}

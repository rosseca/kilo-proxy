package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type recordingDesktopBridge struct {
	copies, opened   []string
	copyErr, openErr error
}

func (bridge *recordingDesktopBridge) CopyText(text string) error {
	bridge.copies = append(bridge.copies, text)
	return bridge.copyErr
}

func (bridge *recordingDesktopBridge) OpenExternal(address string) error {
	bridge.opened = append(bridge.opened, address)
	return bridge.openErr
}

func desktopRequest(a *app, method, path, body string, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://"+a.adminHost+"/api/desktop/"+path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+a.adminToken)
	request.Header.Set("Content-Type", "application/json")
	for _, change := range mutate {
		change(request)
	}
	response := httptest.NewRecorder()
	a.adminHandler().ServeHTTP(response, request)
	return response
}

func TestDesktopOperationsRequireBridgeAndPOST(t *testing.T) {
	a := testApp(t)
	for _, path := range []string{"copy", "open", "probe", "unknown"} {
		if response := desktopRequest(a, http.MethodPost, path, `{}`); response.Code != http.StatusNotFound {
			t.Fatalf("browser-mode %s = %d, want 404", path, response.Code)
		}
	}
	bridge := &recordingDesktopBridge{}
	a.desktop = bridge
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		for _, path := range []string{"copy", "open"} {
			if response := desktopRequest(a, method, path, `{}`); response.Code != http.StatusNotFound {
				t.Fatalf("%s %s = %d, want 404", method, path, response.Code)
			}
		}
	}
	if len(bridge.copies) != 0 || len(bridge.opened) != 0 {
		t.Fatal("unsupported operation reached the OS bridge")
	}
}

func TestDesktopOperationsRequireAdminAuthenticationAndOrigin(t *testing.T) {
	a := testApp(t)
	bridge := &recordingDesktopBridge{}
	a.desktop = bridge
	a.desktopProbes = make(chan desktopProbe, 1)
	for _, test := range []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		{"missing token", func(r *http.Request) { r.Header.Del("Authorization") }, http.StatusUnauthorized},
		{"wrong token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer bad") }, http.StatusUnauthorized},
		{"remote origin", func(r *http.Request) { r.Header.Set("Origin", "https://example.com") }, http.StatusForbidden},
		{"null origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, http.StatusForbidden},
		{"wrong local port", func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:1") }, http.StatusForbidden},
		{"wrong host", func(r *http.Request) { r.Host = "example.com" }, http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, path := range []string{"copy", "open", "probe"} {
				response := desktopRequest(a, http.MethodPost, path, `{"text":"private","url":"https://example.com","id":"probe"}`, test.mutate)
				if response.Code != test.status {
					t.Fatalf("%s = %d, want %d", path, response.Code, test.status)
				}
			}
		})
	}
	if len(bridge.copies) != 0 || len(bridge.opened) != 0 || len(a.desktopProbes) != 0 {
		t.Fatal("unauthorized request reached a desktop operation")
	}
}

func TestDesktopCopyPreservesUnicodeAndMultilineConfiguration(t *testing.T) {
	a := testApp(t)
	bridge := &recordingDesktopBridge{}
	a.desktop = bridge
	text := "# Configuración 日本語 🔑\nmodel = \"vendor/model\"\r\n\tname = \"équipe\"\n"
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		t.Fatal(err)
	}
	response := desktopRequest(a, http.MethodPost, "copy", string(body), func(r *http.Request) {
		r.Header.Set("Origin", "http://"+a.adminHost)
	})
	if response.Code != http.StatusOK || len(bridge.copies) != 1 || bridge.copies[0] != text {
		t.Fatalf("clipboard data changed: status %d, copied %q", response.Code, bridge.copies)
	}
	if len(bridge.opened) != 0 {
		t.Fatal("copy opened an external application")
	}
	bridge.copyErr = errors.New("clipboard unavailable")
	if response = desktopRequest(a, http.MethodPost, "copy", `{"text":"next"}`); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "clipboard unavailable") {
		t.Fatalf("clipboard failure was hidden: %d %s", response.Code, response.Body.String())
	}
}

func TestDesktopJSONRequestsAreBoundedSingleValues(t *testing.T) {
	a := testApp(t)
	bridge := &recordingDesktopBridge{}
	a.desktop = bridge
	for _, test := range []struct{ name, body string }{
		{"malformed", `{"text":`},
		{"wrong shape", `[]`},
		{"null body", `null`},
		{"unknown field", `{"text":"safe","unexpected":true}`},
		{"wrong value type", `{"text":123}`},
		{"oversized text", `{"text":"` + strings.Repeat("a", 1<<20) + `"}`},
		{"trailing object", `{"text":"safe"}{"text":"second"}`},
		{"trailing junk", `{"text":"safe"}unexpected`},
		{"oversized trailing whitespace", `{"text":"safe"}` + strings.Repeat(" ", 1<<20)},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := desktopRequest(a, http.MethodPost, "copy", test.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid copy body = %d, want 400", response.Code)
			}
		})
	}
	if len(bridge.copies) != 0 {
		t.Fatal("invalid body reached clipboard")
	}
}

func TestDesktopRejectsUnsafeExternalURLs(t *testing.T) {
	a := testApp(t)
	bridge := &recordingDesktopBridge{}
	a.desktop = bridge
	for _, address := range []string{
		"", "file:///tmp/secret", "javascript:alert(1)", "data:text/html,hello", "http://example.com", "ftp://example.com",
		"mailto:person@example.com", "//example.com", "https:", "https:example.com", "https:///example.com",
		"https://user:password@example.com", "https://user@example.com", "https://example.com:invalid",
		"https://example.com/\r\n--args", "https://example.com/\x00argument", "https://example.com/\n$(touch /tmp/nope)",
		"https://example.com\\@evil.example", "--args https://example.com", "https://example.com/%zz",
	} {
		t.Run(address, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"url": address})
			response := desktopRequest(a, http.MethodPost, "open", string(body))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("unsafe URL accepted with %d: %q", response.Code, address)
			}
		})
	}
	if len(bridge.opened) != 0 {
		t.Fatalf("unsafe URLs reached the OS: %q", bridge.opened)
	}
}

func TestDesktopOpensHTTPSLinksAndReportsFailure(t *testing.T) {
	a := testApp(t)
	bridge := &recordingDesktopBridge{}
	a.desktop = bridge
	address := "https://example.com/docs?q=kilo&lang=es#models"
	body, _ := json.Marshal(map[string]string{"url": address})
	response := desktopRequest(a, http.MethodPost, "open", string(body))
	if response.Code != http.StatusOK || len(bridge.opened) != 1 || bridge.opened[0] != address {
		t.Fatalf("HTTPS link was not delegated intact: status %d, opened %q", response.Code, bridge.opened)
	}
	if len(bridge.copies) != 0 {
		t.Fatal("opening a link changed the clipboard")
	}
	bridge.openErr = errors.New("browser unavailable")
	if response = desktopRequest(a, http.MethodPost, "open", string(body)); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "browser unavailable") {
		t.Fatalf("external application failure was hidden: %d %s", response.Code, response.Body.String())
	}
}

func TestDesktopProbeRequiresExplicitOptIn(t *testing.T) {
	a := testApp(t)
	a.desktop = &recordingDesktopBridge{}
	for _, body := range []string{`{"id":"first","value":true}`, `invalid`} {
		if response := desktopRequest(a, http.MethodPost, "probe", body); response.Code != http.StatusNotFound {
			t.Fatalf("probe exposed outside explicit self-test: %d", response.Code)
		}
	}
	a.desktopProbes = make(chan desktopProbe, 1)
	response := desktopRequest(a, http.MethodPost, "probe", `{"id":"language","value":{"language":"es"},"error":""}`)
	if response.Code != http.StatusOK || len(a.desktopProbes) != 1 {
		t.Fatalf("explicit probe failed: %d", response.Code)
	}
	probe := <-a.desktopProbes
	if probe.ID != "language" || string(probe.Value) != `{"language":"es"}` || probe.Error != "" {
		t.Fatalf("probe payload changed: %+v", probe)
	}
	// A full probe channel must never block the native frontend's request.
	a.desktopProbes <- probe
	if response = desktopRequest(a, http.MethodPost, "probe", `{"id":"overflow","value":true}`); response.Code != http.StatusOK || len(a.desktopProbes) != 1 {
		t.Fatalf("full probe channel failed: %d", response.Code)
	}
}

func TestDesktopProbeRejectsOversizedAndTrailingData(t *testing.T) {
	a := testApp(t)
	a.desktop = &recordingDesktopBridge{}
	a.desktopProbes = make(chan desktopProbe, 4)
	for _, body := range []string{
		`{"id":"large","value":"` + strings.Repeat("a", 64<<10) + `"}`,
		`{"id":"first"}{"id":"second"}`,
		`{"id":"first"}` + strings.Repeat(" ", 64<<10),
	} {
		if response := desktopRequest(a, http.MethodPost, "probe", body); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid probe body accepted with %d", response.Code)
		}
	}
	if len(a.desktopProbes) != 0 {
		t.Fatal("invalid body reached probe consumer")
	}
}

func TestDesktopRejectsNonJSONContentType(t *testing.T) {
	a := testApp(t)
	bridge := &recordingDesktopBridge{}
	a.desktop = bridge
	response := desktopRequest(a, http.MethodPost, "copy", `{"text":"safe"}`, func(r *http.Request) {
		r.Header.Set("Content-Type", "text/plain")
	})
	if response.Code != http.StatusUnsupportedMediaType || len(bridge.copies) != 0 {
		t.Fatalf("non-JSON request accepted: %d", response.Code)
	}
}

//go:build desktop

package main

import (
	"image"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

func TestNativeLiteLLMOnlyOnboarding(t *testing.T) {
	u := nativeFreshOnboarding(t)
	// Use the collapsed ChatGPT option rather than the fixture vault's error state.
	subscription := u.owner.chatgpt
	subscription.mu.Lock()
	subscription.state = chatGPTState{Status: "idle"}
	subscription.publishUIProjectionLocked()
	subscription.mu.Unlock()
	u.refreshState()
	nativeTestWait(t, u, func() bool {
		return nativeString(nativeMap(u.state["chatgpt"]), "status") == "idle"
	})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer synthetic-virtual-key" {
			http.Error(w, "unexpected gateway request", http.StatusUnauthorized)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"data": []any{map[string]string{
			"id": "company-model", "name": "Company Model",
		}}})
	}))
	t.Cleanup(gateway.Close)

	h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 820), now: time.Now()}
	h.frame()
	const add = "Does your company use LiteLLM? Add it here"
	const companyKey = "Did your company give you a key? Use it here"
	const chatGPT = "Have ChatGPT Plus or Pro? Connect it too (optional)"
	assertCollapsed := func() {
		t.Helper()
		h.reveal(add, semantic.Button)
		disclosure := h.target(add, semantic.Button).Desc
		if disclosure.Selected || !nativeUpdateHasText(h, "+ "+add) {
			t.Fatal("LiteLLM onboarding did not show the collapsed LiteLLM disclosure")
		}
		for _, pair := range [][2]string{{companyKey, add}, {add, chatGPT}} {
			above := h.target(pair[0], semantic.Button).Desc.Bounds
			below := h.target(pair[1], semantic.Button).Desc.Bounds
			gap := below.Min.Y - above.Max.Y
			if gap < 0 || gap > int(nativeSpace16) || below.Min.X != above.Min.X {
				t.Fatalf("%q %v did not sit directly below %q %v", pair[1], below, pair[0], above)
			}
		}
		for _, label := range []string{"Not configured", "API URL", "Test and save"} {
			if nativeUpdateHasText(h, label) {
				t.Fatalf("collapsed LiteLLM onboarding displayed %q", label)
			}
		}
	}
	assertCollapsed()
	h.click(add, semantic.Button)
	if !nativeUpdateHasText(h, "API URL") || !nativeUpdateHasText(h, "Virtual key (if required)") {
		t.Fatal("opening LiteLLM did not show the connection editor")
	}
	if u.value("providers.litellm.url") != "" || u.value("providers.litellm.key") != "" {
		t.Fatal("fresh LiteLLM settings prepopulate the gateway or the key")
	}
	u.setValue("providers.litellm.url", "http://127.0.0.1:4000")
	u.setValue("providers.litellm.key", "synthetic-discarded-key")
	h.frame()
	h.reveal("Cancel", semantic.Button)
	h.click("Cancel", semantic.Button)
	assertCollapsed()
	h.click(add, semantic.Button)
	if u.value("providers.litellm.url") != "" || u.value("providers.litellm.key") != "" {
		t.Fatal("reopening LiteLLM retained the cancelled draft")
	}
	u.setValue("providers.litellm.url", gateway.URL)
	u.setValue("providers.litellm.key", "synthetic-virtual-key")
	h.frame()
	h.reveal("Test and save", semantic.Button)
	h.click("Test and save", semantic.Button)
	nativeTestWait(t, u, func() bool { return u.liteLLMReady() && !u.busy["GET/api/state"] })
	if u.value("providers.litellm.key") != "" || nativeBool(u.state, "kiloReady") {
		t.Fatal("saving LiteLLM retained the virtual key in the editor or required Kilo")
	}
	h.frame()
	h.click("Choose models", semantic.Button)
	nativeTestWait(t, u, func() bool { return u.setupStep == setupModels && len(u.models) == 1 })
	if u.busy["POST/api/config"] || u.models[0].Connection != "litellm" {
		t.Fatal("LiteLLM onboarding used the Kilo settings or catalog")
	}
	h.frame()
	h.reveal("Company Model · LiteLLM", semantic.CheckBox)
	h.click("Company Model · LiteLLM", semantic.CheckBox)
	nativeTestWait(t, u, func() bool { _, ready := u.libraryStatus(); return ready })
	h.frame()
	h.click("Continue", semantic.Button)
	h.click("Get started", semantic.Button)
	nativeTestWait(t, u, func() bool { return u.page == "agents" && nativeBool(u.state, "running") })
}

func TestNativeLiteLLMLoginURL(t *testing.T) {
	for _, tc := range []struct {
		name, configured, authorization string
		allowed                         bool
	}{
		{"HTTPS normalizes the default port", "https://LITELLM.example.test:443/company", "https://litellm.example.test/company/kilo/login?session=sample", true},
		{"local gateway", "http://127.0.0.1:4000", "http://127.0.0.1:4000/kilo/login?session=sample", true},
		{"local IPv6", "http://[::1]:4000", "http://[::1]:4000/kilo/login", true},
		{"different origin", "https://litellm.example.test", "https://login.example.test/kilo/login", false},
		{"different port", "https://litellm.example.test:8443", "https://litellm.example.test/kilo/login", false},
		{"different scheme", "https://localhost:4000", "http://localhost:4000/kilo/login", false},
		{"remote plaintext", "http://litellm.example.test", "http://litellm.example.test/kilo/login", false},
		{"userinfo", "https://litellm.example.test", "https://user@litellm.example.test/kilo/login", false},
		{"missing URL", "https://litellm.example.test", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link, err := nativeLiteLLMLoginURL(tc.configured, tc.authorization)
			if tc.allowed && (err != nil || link != tc.authorization) || !tc.allowed && (err == nil || link != "") {
				t.Fatalf("wrong sign-in decision: %q, %v", link, err)
			}
		})
	}
}

func TestNativeLiteLLMBrowserSignInStates(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		t.Run(language, func(t *testing.T) {
			theme := material.NewTheme()
			theme.Shaper = text.NewShaper(text.WithCollection(nativeFonts()))
			theme.TextSize, theme.Face = 14, nativeSansFace
			bridge := &nativeRecordingBridge{}
			u := &nativeUI{
				theme: theme, language: language, authenticated: true, bridge: bridge,
				owner: &app{quit: make(chan struct{})}, updates: make(chan func(), 8),
				state: map[string]any{}, busy: map[string]bool{}, expanded: map[string]bool{},
				editors: map[string]*widget.Editor{}, buttons: map[string]*widget.Clickable{}, checks: map[string]*widget.Bool{},
			}
			h := &nativePointerHarness{t: t, u: u, size: image.Pt(560, 600)}
			draw := func() {
				h.ops.Reset()
				gtx := layout.Context{Ops: &h.ops, Source: h.router.Source(),
					Constraints: layout.Constraints{Max: h.size}, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
				u.uxsLiteLLMRow()(gtx)
				h.router.Frame(&h.ops)
			}
			has := func(en, es string) bool { return nativeUpdateHasText(h, u.tr(en, es)) }
			u.openLiteLLMEditor("", "key")
			draw()
			if u.value("providers.litellm.url") != "" || u.editor("providers.litellm.key").Mask != '•' {
				t.Fatal("manual key mode lost its blank address or password mask")
			}
			u.setValue("providers.litellm.key", "synthetic-virtual-key")
			u.setValue("providers.litellm.url", "http://127.0.0.1:4000")
			u.clickable("providers.litellm.authMode:browser").Click()
			draw()
			draw()
			if u.value("providers.litellm.key") != "" || has("Virtual key (if required)", "Clave virtual (si se requiere)") ||
				!has("Sign in in your browser", "Iniciar sesión en el navegador") {
				t.Fatal("browser mode retained a manual key or omitted its sign-in action")
			}
			const address = "http://127.0.0.1:4000"
			const link = address + "/kilo/login?session=synthetic"
			u.state["litellmLogin"] = map[string]any{"status": "pending"}
			u.liteLLMAuthorizationURL = link
			draw()
			if !has("Waiting for browser sign-in", "Esperando el inicio de sesión en el navegador") ||
				!has("Cancel sign-in", "Cancelar el inicio de sesión") {
				t.Fatal("pending browser login did not expose its status and cancel action")
			}
			u.clickable("providers.litellm.open-login").Click()
			draw()
			nativeTestWait(t, u, func() bool {
				bridge.mu.Lock()
				defer bridge.mu.Unlock()
				return bridge.URL == link
			})
			u.state["litellmLogin"] = map[string]any{"status": "idle"}
			u.acceptLiteLLMLogin()
			if u.liteLLMAuthorizationURL != "" || !u.expanded["providers.litellm"] ||
				u.value("providers.litellm.url") != address {
				t.Fatal("cancelling sign-in discarded its draft or retained the authorization link")
			}
			u.liteLLMAuthorizationURL = link
			u.state["litellmLogin"] = map[string]any{"status": "error"}
			u.acceptLiteLLMLogin()
			draw()
			if u.liteLLMAuthorizationURL != "" || !u.expanded["providers.litellm"] ||
				!has("LiteLLM sign-in failed. Try again.", "El inicio de sesión en LiteLLM falló. Vuelve a intentarlo.") {
				t.Fatal("failed browser login kept its expired link or lost its retry form")
			}
			u.liteLLMAuthorizationURL = link
			u.state["litellmLogin"] = map[string]any{"status": "success"}
			u.state["litellm"] = map[string]any{"url": address + "/v1", "authMode": "browser", "enabled": true, "ready": true, "keySaved": true}
			u.acceptLiteLLMLogin()
			draw()
			if u.expanded["providers.litellm"] || u.liteLLMAuthorizationURL != "" ||
				!has("Reconnect LiteLLM", "Volver a conectar LiteLLM") || !has("Disconnect LiteLLM", "Desconectar LiteLLM") {
				t.Fatal("connected browser mode did not replace the form with its connection actions")
			}
			u.openLiteLLMEditor(address, "key")
			draw()
			if !has("Connect without a key", "Conectar sin clave") || u.checked("providers.litellm.clear") {
				t.Fatal("switching from browser to key mode did not require an explicit keyless choice")
			}
			u.clickable("providers.litellm.save").Click()
			draw()
			if u.gatewayRequestBusy() || u.notice != u.tr("Enter a virtual key or choose to connect without one.", "Introduce una clave virtual o elige conectar sin clave.") {
				t.Fatal("an omitted key attempted to replace the browser connection")
			}
			u.closeLiteLLMEditor()
			u.state["litellmLogin"] = map[string]any{"status": "idle"}
			u.state["litellm"] = map[string]any{"url": address + "/v1", "authMode": "browser", "enabled": false, "ready": false}
			u.acceptLiteLLMLogin()
			draw()
			if !has("Signed out", "Sesión cerrada") || !has("Reconnect LiteLLM", "Volver a conectar LiteLLM") ||
				has("Disconnect LiteLLM", "Desconectar LiteLLM") || has("Use LiteLLM", "Usar LiteLLM") {
				t.Fatal("signed-out gateway did not offer a clean reconnect")
			}
		})
	}
}

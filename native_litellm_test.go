//go:build desktop

package main

import (
	"image"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func TestNativeLiteLLMOnlyOnboarding(t *testing.T) {
	u := nativeFreshOnboarding(t)
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
	h.reveal("Add LiteLLM", semantic.Button)
	h.click("Add LiteLLM", semantic.Button)
	if u.value("providers.litellm.url") != "" || u.value("providers.litellm.key") != "" {
		t.Fatal("fresh LiteLLM settings prepopulate the gateway or the key")
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

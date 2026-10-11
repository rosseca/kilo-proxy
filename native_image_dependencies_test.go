//go:build desktop

package main

import (
	"image"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func nativeImageDependencyFixture(t *testing.T, size image.Point, language string) (*nativePointerHarness, *atomic.Bool) {
	t.Helper()
	u := nativeTestUI(t)
	found := &atomic.Bool{}
	u.owner.mu.Lock()
	u.owner.imageDependencyLookup = func(tool string) string {
		if tool == "cloudflared" && found.Load() {
			return "/synthetic/cloudflared"
		}
		return ""
	}
	u.owner.mu.Unlock()
	u.setLanguage(language)
	nativeTestWait(t, u, func() bool { return u.languageTarget == "" && !u.busy["GET/api/state"] })
	u.refreshState()
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] && u.imageDependencyNoticeVisible() })
	u.page = "agents"
	h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
	h.frame()
	return h, found
}

func nativeImageDependencyText(h *nativePointerHarness, label string, visible bool) bool {
	for _, node := range h.nodes() {
		if node.Desc.Label == label && (!visible || !node.Desc.Bounds.Intersect(image.Rectangle{Max: h.size}).Empty()) {
			return true
		}
	}
	return false
}

// A frame or reveal can drain the real one-second background poll after the
// caller observed an idle API. Wait for the rendered control, rather than an
// earlier busy-map snapshot, before sending its real pointer events.
func nativeImageDependencyWaitForCheck(h *nativePointerHarness, visible bool) {
	h.t.Helper()
	label := h.u.tr("Check again", "Comprobar de nuevo")
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		h.frame()
		if h.u.busy["GET/api/state"] {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		nodes := h.nodes()
		for _, text := range nodes {
			if text.Desc.Label != label {
				continue
			}
			for _, node := range nodes {
				if node.Desc.Class == semantic.Button && !node.Desc.Disabled && text.Desc.Bounds.Min.In(node.Desc.Bounds) && (!visible || !node.Desc.Bounds.Intersect(image.Rectangle{Max: h.size}).Empty()) {
					return
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.target(label, semantic.Button)
	h.t.Fatalf("image dependency check did not become ready: state request busy=%v", h.u.busy["GET/api/state"])
}

func TestNativeImageDependencyNoticeActionsAndRecheck(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {780, 700}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"-"+lang, func(t *testing.T) {
				h, found := nativeImageDependencyFixture(t, size, lang)
				u := h.u
				title := u.tr("Install cloudflared for large images", "Instala cloudflared para imágenes grandes")
				if !nativeImageDependencyText(h, title, true) {
					t.Fatal("missing dependency was not visible when opening Agents")
				}
				nativeGridCapture(t, h, "image-dependency-agents-"+fmtSize(size)+"-"+lang)
				nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
				h.frame()
				nativeImageDependencyWaitForCheck(h, true)
				h.click(u.tr("Check again", "Comprobar de nuevo"), semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
				h.frame()
				if !u.imageDependencyNoticeVisible() || nativeBool(u.state, "running") {
					t.Fatal("rechecking a missing executable hid the notice or started the proxy")
				}
				bridge := u.owner.desktop.(*nativeRecordingBridge)
				h.click(u.tr("Install instructions", "Instrucciones de instalación"), semantic.Button)
				nativeTestWait(t, u, func() bool {
					bridge.mu.Lock()
					defer bridge.mu.Unlock()
					return bridge.URL == imageCloudflareInstallURL
				})
				copyLabel := u.tr("Copy install command", "Copiar comando de instalación")
				if command := u.imageDependency().InstallCommand; command != "" {
					h.click(copyLabel, semantic.Button)
					bridge.mu.Lock()
					copied := bridge.Text
					bridge.mu.Unlock()
					if copied != command {
						t.Fatalf("wrong install command copied: %q", copied)
					}
				} else if nativeImageDependencyText(h, copyLabel, false) {
					t.Fatal("copy action appeared without an install command")
				}
				u.setNotice(nativeToneNeutral, "")
				h.frame()
				h.click(u.tr("Not now", "Ahora no"), semantic.Button)
				u.refreshState()
				nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
				h.frame()
				if u.imageDependencyNoticeVisible() || nativeImageDependencyText(h, title, false) {
					t.Fatal("background refresh restored a dismissed notice")
				}
				h.click(u.tr("Start proxy", "Arrancar proxy"), semantic.Button)
				nativeTestWait(t, u, func() bool { return nativeBool(u.state, "running") && !u.busy["POST/api/start"] })
				h.frame()
				if !u.imageDependencyNoticeVisible() || !nativeImageDependencyText(h, title, true) {
					t.Fatal("proxy start did not restore the missing dependency notice")
				}
				h.click(u.tr("Image settings", "Ajustes de imágenes"), semantic.Button)
				h.frame()
				if u.page != "settings" || !nativeImageDependencyText(h, u.tr("Large images", "Imágenes grandes"), true) {
					t.Fatal("image settings action did not reveal Large images")
				}
				// Dismissing the global notice cannot suppress its settings guidance.
				u.imageDependencyDismissed = true
				h.frame()
				if !nativeImageDependencyText(h, title, true) {
					t.Fatal("settings hid the missing dependency guidance after dismissal")
				}
				nativeGridCapture(t, h, "image-dependency-settings-missing-"+fmtSize(size)+"-"+lang)
				nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
				h.frame()
				nativeImageDependencyWaitForCheck(h, false)
				h.reveal(u.tr("Check again", "Comprobar de nuevo"), semantic.Button)
				nativeImageDependencyWaitForCheck(h, true)
				found.Store(true)
				h.click(u.tr("Check again", "Comprobar de nuevo"), semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] && u.imageDependency().Installed })
				h.frame()
				if nativeImageDependencyText(h, title, false) || !nativeImageDependencyText(h, u.tr("cloudflared found", "cloudflared encontrado"), false) {
					t.Fatal("rechecking did not replace the warning with the executable-found badge")
				}
				u.showImageSettings()
				h.frame()
				h.frame()
				nativeGridCapture(t, h, "image-dependency-settings-found-"+fmtSize(size)+"-"+lang)
				saved, err := readSettings(u.owner.dir)
				if err != nil || saved.ImageTransport.Mode != "cloudflare" {
					t.Fatalf("notice actions changed the image choice: %+v, %v", saved.ImageTransport, err)
				}
			})
		}
	}
}

func TestNativeImageDependencyRecheckWaitsForPollDrainedDuringReveal(t *testing.T) {
	for _, beforeReveal := range []bool{false, true} {
		name := "during-reveal"
		if beforeReveal {
			name = "before-reveal"
		}
		t.Run(name, func(t *testing.T) {
			h, found := nativeImageDependencyFixture(t, image.Pt(780, 700), "en")
			u := h.u
			u.showImageSettings()
			h.frame()
			h.frame()
			nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
			nativeImageDependencyWaitForCheck(h, true)
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			closeRelease := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(closeRelease)
			var held atomic.Bool
			u.owner.mu.Lock()
			u.owner.imageDependencyLookup = func(tool string) string {
				installed := found.Load()
				if tool == "cloudflared" && held.CompareAndSwap(false, true) {
					close(entered)
					<-release
				}
				if tool == "cloudflared" && installed {
					return "/synthetic/cloudflared"
				}
				return ""
			}
			u.owner.mu.Unlock()
			// Precisely the CI sequence: the idle wait passed, then reveal's frame
			// drains a newly queued background poll and disables Check again.
			u.enqueue(u.refreshState)
			if beforeReveal {
				h.frame()
			} else {
				h.reveal("Check again", semantic.Button)
			}
			select {
			case <-entered:
			case <-time.After(8 * time.Second):
				t.Fatal("queued background poll did not reach the held fixture response")
			}
			if !u.busy["GET/api/state"] {
				t.Fatal("fixture did not retain the background poll during reveal")
			}
			nodes := h.nodes()
			labelPresent := false
			for _, text := range nodes {
				if text.Desc.Label != "Check again" {
					continue
				}
				labelPresent = true
				for _, node := range nodes {
					if node.Desc.Class == semantic.Button && !node.Desc.Disabled && text.Desc.Bounds.Min.In(node.Desc.Bounds) {
						t.Fatal("busy poll did not disable the rendered Check again control")
					}
				}
			}
			if !labelPresent {
				t.Fatal("held poll removed the exact Check again semantic label")
			}
			// The response is released by the next real frame, so the new wait must
			// consume both that frame and the API completion before trying the click.
			u.enqueue(closeRelease)
			if beforeReveal {
				nativeImageDependencyWaitForCheck(h, false)
				h.reveal("Check again", semantic.Button)
			}
			nativeImageDependencyWaitForCheck(h, true)
			found.Store(true)
			h.click("Check again", semantic.Button)
			nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] && u.imageDependency().Installed })
			h.frame()
			if nativeImageDependencyText(h, "Install cloudflared for large images", false) || !nativeImageDependencyText(h, "cloudflared found", false) {
				t.Fatal("enabled pointer recheck did not replace the missing dependency notice")
			}
			if nativeBool(u.state, "running") {
				t.Fatal("rechecking started the proxy")
			}
		})
	}
}

func TestNativeImageDependencyOnboardingAndExplicitChoice(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) {
			h, _ := nativeImageDependencyFixture(t, image.Pt(780, 700), lang)
			u := h.u
			u.beginSetup()
			h.frame()
			title := u.tr("Install cloudflared for large images", "Instala cloudflared para imágenes grandes")
			if !nativeImageDependencyText(h, title, true) {
				t.Fatal("onboarding did not show the missing dependency")
			}
			nativeGridCapture(t, h, "image-dependency-onboarding-"+lang)
			h.click(u.tr("Not now", "Ahora no"), semantic.Button)
			u.showImageSettings()
			h.frame()
			h.frame()
			h.click(u.tr("Off", "Desactivado"), semantic.Button)
			nativeTestWait(t, u, func() bool { return !u.busy["PUT/api/image-transport-settings"] && !u.busy["GET/api/state"] })
			u.refreshState()
			nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
			h.frame()
			if nativeImageDependencyText(h, title, false) {
				t.Fatal("Settings retained the cloudflared warning after selecting Off")
			}
			u.page = "agents"
			h.frame()
			if u.imageDependency().Required || nativeImageDependencyText(h, title, false) {
				t.Fatal("explicit Off still requested cloudflared")
			}
			stale := u.imageDependency()
			stale.Required = true
			u.state["imageTransportDependency"] = stale
			if u.imageDependencyNoticeVisible() {
				t.Fatal("an older dependency snapshot overrode the saved Off choice")
			}
			saved, err := readSettings(u.owner.dir)
			if err != nil || saved.ImageTransport.Mode != "off" {
				t.Fatalf("explicit Off was not preserved: %+v %v", saved.ImageTransport, err)
			}
			u.showImageSettings()
			h.frame()
			h.frame()
			h.click("Cloudflare", semantic.Button)
			nativeTestWait(t, u, func() bool { return !u.busy["PUT/api/image-transport-settings"] && !u.busy["GET/api/state"] })
			u.page = "agents"
			u.list("page.agents").ScrollTo(0)
			h.frame()
			if !u.imageDependencyNoticeVisible() || !nativeImageDependencyText(h, title, true) {
				t.Fatal("returning to Cloudflare did not restore the notice")
			}
			// A platform without a package-manager command still has instructions.
			dependency := u.imageDependency()
			dependency.InstallCommand = ""
			u.state["imageTransportDependency"] = dependency
			h.frame()
			if nativeImageDependencyText(h, u.tr("Copy install command", "Copiar comando de instalación"), false) {
				t.Fatal("copy action appeared without an install command")
			}
			h.target(u.tr("Install instructions", "Instrucciones de instalación"), semantic.Button)
		})
	}
}

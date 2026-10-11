//go:build desktop

package main

import (
	"image"
	"testing"
	"time"

	"gioui.org/io/semantic"
)

func nativeScrollImageSetting(h *nativePointerHarness, label string) {
	target := 260
	if h.size.X < 900 {
		target = 330
	}
	for attempt := 0; attempt < 10; attempt++ {
		y := 1000
		for _, text := range h.nodes() {
			if text.Desc.Label != label {
				continue
			}
			for _, node := range h.nodes() {
				if (node.Desc.Class == semantic.Button || node.Desc.Class == semantic.RadioButton) && text.Desc.Bounds.Min.In(node.Desc.Bounds) {
					y = node.Desc.Bounds.Min.Y
				}
			}
		}
		if y >= target-20 && y <= target+20 {
			return
		}
		nativeMenuWheel(h, image.Pt(h.size.X-100, h.size.Y-100), float32(y-target))
	}
}

func TestNativeImageTransportSettingsPointerAndPersistence(t *testing.T) {
	for _, size := range []image.Point{{1180, 820}, {780, 700}, {720, 700}} {
		for _, lang := range []string{"en", "es"} {
			t.Run(fmtSize(size)+"-"+lang, func(t *testing.T) {
				u := nativeTestUI(t)
				u.page = "settings"
				u.settingsTab = "images"
				// Persist the language through the real API. Assigning u.language
				// alone lets the periodic state refresh restore the fixture's English.
				u.setLanguage(lang)
				nativeTestWait(t, u, func() bool {
					return u.language == lang && u.languageTarget == "" && !u.busy["POST/api/language"] && !u.busy["GET/api/state"]
				})
				h := &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
				h.frame()
				// The method list opens from the row's Change link.
				change := u.tr("Change", "Cambiar")
				nativeScrollImageSetting(h, change)
				h.click(change, semantic.Button)
				if !u.expanded["images.change"] {
					t.Fatal("Change did not open the image methods")
				}
				capture := func(name string) {
					// Finish the pointer ripple before taking a review screenshot.
					h.now = h.now.Add(time.Second)
					h.frame()
					nativeGridCapture(t, h, name+"-"+fmtSize(size)+"-"+lang)
				}
				ttl := "1h"
				class := semantic.Button
				var choose func(label string, mode, profile string)
				method := func(label string, mode, profile string) {
					class = semantic.RadioButton
					defer func() { class = semantic.Button }()
					choose(label, mode, profile)
				}
				choose = func(label string, mode, profile string) {
					// Exercise the same refresh that the background poll performs,
					// without depending on the runner taking over one second.
					u.refreshState()
					nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
					h.frame()
					if u.language != lang {
						t.Fatalf("state refresh changed the test language to %q, want %q", u.language, lang)
					}
					nativeScrollImageSetting(h, label)
					h.click(label, class)
					nativeTestWait(t, u, func() bool { return !u.busy["PUT/api/image-transport-settings"] })
					saved, err := readSettings(u.owner.dir)
					if err != nil || saved.ImageTransport != (imageTransportSettings{mode, profile, ttl}) {
						t.Fatalf("image preference did not persist: %+v %v", saved.ImageTransport, err)
					}
					if saved.Language != lang {
						t.Fatalf("saving image preferences changed the persisted language to %q, want %q", saved.Language, lang)
					}
					h.frame()
					if !h.selected(label, class) {
						t.Fatalf("image option %q is not semantically selected", label)
					}
				}
				method(u.tr("Compress here", "Comprimir aquí"), "compress", "high")
				choose(u.tr("Balanced", "Equilibrado"), "compress", "balanced")
				choose(u.tr("Small size", "Tamaño pequeño"), "compress", "small")
				choose(u.tr("High quality", "Alta calidad"), "compress", "high")
				nativeScrollImageSetting(h, u.tr("Compress here", "Comprimir aquí"))
				capture("image-compression")
				method("Kilo · experimental", "upload", "high")
				capture("experimental-image-uploads")
				method("Cloudflare", "cloudflare", "high")
				nativeScrollImageSetting(h, "Cloudflare")
				capture("image-cloudflare")
				method("Tailscale Funnel", "tailscale", "high")
				capture("image-tailscale")
				method("Litterbox · experimental", "litterbox", "high")
				capture("image-litterbox-experimental")
				for _, expiry := range []struct{ value, en, es string }{
					{"12h", "12 hours", "12 horas"}, {"24h", "24 hours", "24 horas"}, {"72h", "72 hours", "72 horas"}, {"1h", "1 hour", "1 hora"},
				} {
					ttl = expiry.value
					choose(u.tr(expiry.en, expiry.es), "litterbox", "high")
				}
				nativeScrollImageSetting(h, u.tr("1 hour", "1 hora"))
				capture("image-litterbox")
				method(u.tr("Off", "Desactivado"), "off", "high")
				// Layout, scrolling and changing language must never opt in by themselves.
				nextLanguage := u.tr("es", "en")
				// Language lives in the Preferences block; jumping there keeps image settings intact.
				u.settingsTab = "preferences"
				h.frame()
				nativeScrollImageSetting(h, u.tr("Español", "English"))
				h.click(u.tr("Español", "English"), semantic.Button)
				nativeTestWait(t, u, func() bool {
					return u.language == nextLanguage && u.languageTarget == "" && !u.busy["POST/api/language"] && !u.busy["GET/api/state"]
				})
				h.frame()
				saved, err := readSettings(u.owner.dir)
				if err != nil || saved.Language != nextLanguage || saved.ImageTransport != (imageTransportSettings{"off", "high", ttl}) {
					t.Fatalf("changing language lost its preference or changed image settings: language=%q imageTransport=%+v error=%v", saved.Language, saved.ImageTransport, err)
				}
			})
		}
	}
}

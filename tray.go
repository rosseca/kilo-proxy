package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"unicode"
)

type trayState struct {
	labels                          trayLabels
	status, team, address, activity string
	running, pending, configured    bool
}

func (a *app) trayState() trayState {
	a.mu.Lock()
	defer a.mu.Unlock()
	labels := trayText(a.config.Language)
	s := trayState{
		labels:  labels,
		running: a.proxyServer != nil, pending: a.authPending(),
		configured: a.apiKey != "" && a.config.OrgID != "",
		address:    fmt.Sprintf("127.0.0.1:%d", a.config.Port),
		activity:   fmt.Sprintf(labels.activity, a.active, a.requests, a.failures),
		team:       labels.teamEmpty, status: labels.stopped,
	}
	if a.config.OrgID != "" {
		name := a.config.OrgID
		for _, org := range a.organizations {
			if org.ID == a.config.OrgID {
				name = org.Name
				break
			}
		}
		s.team = labels.teamPrefix + menuText(name)
	}
	if s.running {
		s.status = labels.running
	} else if s.pending {
		s.status = labels.pending
	} else if !s.configured {
		s.status = labels.unconfigured
	}
	return s
}

func menuText(s string) string {
	runes := []rune(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
	if len(runes) > 48 {
		return string(runes[:47]) + "…"
	}
	return string(runes)
}

// Rasterize our own K mark. macOS uses its alpha mask to adapt to light/dark
// menu bars; other desktops get a high-contrast badge on a transparent canvas.
func trayIcon(template bool) []byte {
	const size = 44
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+.5, float64(y)+.5
			k := (px >= 10 && px < 16 && py >= 7 && py < 37) ||
				(px >= 15 && px < 34 && py >= 7 && py < 37 &&
					((py <= 23 && px+py >= 32 && px+py < 41) ||
						(py >= 21 && py-px >= -1 && py-px < 8)))
			if k {
				img.SetNRGBA(x, y, color.NRGBA{R: 30, G: 38, B: 32, A: 255})
			} else if !template && (px-22)*(px-22)+(py-22)*(py-22) <= 22*22 {
				img.SetNRGBA(x, y, color.NRGBA{R: 232, G: 243, B: 106, A: 255})
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

package main

import (
	_ "embed"
	"fmt"
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

// Every surface is generated from internal/brand's selected geometric K.
// macOS uses the monochrome alpha template to adapt to its menu-bar appearance.
//
//go:embed ui/tray.png
var trayColorPNG []byte

//go:embed ui/tray-template.png
var trayTemplatePNG []byte

func trayIcon(template bool) []byte {
	if template {
		return trayTemplatePNG
	}
	return trayColorPNG
}

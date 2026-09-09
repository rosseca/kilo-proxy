package main

import (
	_ "embed"
	"fmt"
	"strings"
	"unicode"
)

type trayState struct {
	labels                                    trayLabels
	status, team, address, activity           string
	display, amount, spend, coverage, tooltip string
	running, pending, configured              bool
}

// Format the existing integer nanodollar accumulator without floating-point
// conversion. A positive amount below one cent must never look like free usage.
func traySpendAmount(summary usageSummary) string {
	if summary.Requests == 0 {
		return "$0.00"
	}
	if summary.Priced == 0 {
		return "—"
	}
	nanos := summary.costNanos
	amount := "$0.00"
	if nanos > 0 && nanos < 10000000 {
		amount = "<$0.01"
	} else {
		cents := nanos / 10000000
		if nanos%10000000 >= 5000000 {
			cents++
		}
		amount = fmt.Sprintf("$%d.%02d", cents/100, cents%100)
	}
	if summary.Priced < summary.Requests {
		amount += "*"
	}
	return amount
}

func (a *app) trayState() trayState {
	a.mu.Lock()
	defer a.mu.Unlock()
	labels := trayText(a.config.Language)
	s := trayState{
		labels:  labels,
		running: a.proxyServer != nil, pending: a.authPending(),
		display:    normalizeTrayDisplay(a.config.TrayDisplay),
		configured: a.apiKey != "" && a.config.OrgID != "",
		address:    fmt.Sprintf("127.0.0.1:%d", a.config.Port),
		activity:   fmt.Sprintf(labels.activity, a.active, a.requests, a.failures),
		team:       labels.teamEmpty, status: labels.stopped,
	}
	s.amount = traySpendAmount(a.usageTotal)
	spendLabel := labels.spend
	if a.usageTotal.Priced > 0 && a.usageTotal.Priced < a.usageTotal.Requests {
		spendLabel = labels.subtotal
	}
	s.spend = spendLabel + ": " + s.amount
	s.coverage = fmt.Sprintf(labels.coverage, a.usageTotal.Priced, a.usageTotal.Requests)
	s.tooltip = labels.tooltip
	if s.display == trayDisplaySpend {
		s.tooltip = "Kilo Proxy · " + s.spend + " · " + s.coverage
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

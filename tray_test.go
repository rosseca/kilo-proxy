package main

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestTraySpendFormatsReportedNanodollars(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		requests, priced, nanos int64
		want                    string
	}{
		{"new session", 0, 0, 0, "$0.00"},
		{"unknown", 3, 0, 0, "—"},
		{"known zero", 2, 2, 0, "$0.00"},
		{"partial zero", 3, 2, 0, "$0.00*"},
		{"one nanodollar", 1, 1, 1, "<$0.01"},
		{"partial tiny", 3, 2, 9999999, "<$0.01*"},
		{"one cent", 1, 1, 10000000, "$0.01"},
		{"below rounding boundary", 1, 1, 219999999, "$0.22"},
		{"half cent rounds up", 1, 1, 225000000, "$0.23"},
		{"decimal carry", 1, 1, 999999999, "$1.00"},
		{"large precise boundary", 1, 1, 9007199254740993, "$9007199.25"},
		{"max accumulator", 1, 1, math.MaxInt64, "$9223372036.85"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := usageSummary{Requests: tc.requests, Priced: tc.priced, costNanos: tc.nanos}
			if got := traySpendAmount(s); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTraySpendUsesProcessTotalAcrossConversationsAndImages(t *testing.T) {
	a := testApp(t)
	a.config.TrayDisplay = trayDisplaySpend
	a.config.Language = "en"
	for _, request := range []struct{ source, session, org, response string }{
		{"codex-thread", "codex-task", "org-a", `{"usage":{"cost":0.12}}`},
		{"claude-session", "claude-task", "org-a", `{"usage":{"cost":0.23}}`},
		{"unassigned", "image", "org-b", `{"usage":{"cost":0,"cost_details":{"upstream_inference_cost":0.21976}}}`},
		{"client-session", "unknown", "org-b", `{"usage":{"input_tokens":12}}`},
	} {
		usage := observeUsage(t, false, request.response)
		usage.usage.Source, usage.usage.Session, usage.org = request.source, request.session, request.org
		a.recordUsage(usage)
	}
	before := a.trayState()
	if before.amount != "$0.57*" || before.spend != "Reported subtotal this session: $0.57*" || before.coverage != "Cost reported: 3/4 requests" || !strings.Contains(before.tooltip, "$0.57*") {
		t.Fatalf("tray lost process subtotal or coverage: %+v", before)
	}
	a.clearActivity(httptest.NewRecorder())
	a.stop()
	if after := a.trayState(); after.amount != before.amount || after.coverage != before.coverage {
		t.Fatal("clearing captures or stopping reset tray spend")
	}
	a.config.TrayDisplay = trayDisplayIcon
	if icon := a.trayState(); icon.tooltip != icon.labels.tooltip || icon.amount != before.amount {
		t.Fatal("icon preference changed accounting or kept spend tooltip")
	}
}

func TestTrayFollowsPanelLifecycle(t *testing.T) {
	a := testApp(t)
	a.config.Language = "en"
	s := a.trayState()
	if s.running || s.configured || s.pending {
		t.Fatal("new app must require setup")
	}
	a.apiKey = "test-upstream-secret"
	a.config.OrgID = "team-123"
	a.config.Port = 0 // ephemeral port; no outbound requests in this test
	a.organizations = []organization{{ID: "team-123", Name: "Engineering"}}
	if w := adminRequest(a, "start", `{}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	s = a.trayState()
	if !s.running || !s.configured || s.team != "Team: Engineering" {
		t.Fatalf("tray did not follow panel start: %+v", s)
	}
	for _, secret := range []string{a.apiKey, a.adminToken, a.config.LocalKey} {
		if strings.Contains(fmt.Sprintf("%+v", s), secret) {
			t.Fatal("secret in tray state")
		}
	}
	if w := adminRequest(a, "stop", `{}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if a.trayState().running {
		t.Fatal("tray still shows running after panel stop")
	}
	a.login = &loginSession{Status: "pending"}
	if !a.trayState().pending {
		t.Fatal("pending login not reflected in tray")
	}
}

func TestQuitIsIdempotentAndPreventsRestart(t *testing.T) {
	a := testApp(t)
	a.apiKey, a.config.OrgID, a.config.Port = "test-key", "test-org", 0
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); a.requestQuit() }()
	}
	wg.Wait()
	select {
	case <-a.quit:
	default:
		t.Fatal("quit was not signalled")
	}
	if err := a.start(); err == nil {
		t.Fatal("proxy restarted while application was closing")
	}
	if w := adminRequest(a, "auth/start", `{}`); w.Code != 409 {
		t.Fatal("login accepted while application was closing")
	}
}

func TestTrayLabelsAreCompleteAndLocalized(t *testing.T) {
	english, spanish := trayText("en"), trayText("es")
	en, es := reflect.ValueOf(english), reflect.ValueOf(spanish)
	for i := range en.NumField() {
		name := en.Type().Field(i).Name
		if en.Field(i).String() == "" || es.Field(i).String() == "" {
			t.Errorf("label %s is missing a translation", name)
		}
		if name != "endpoint" && en.Field(i).String() == es.Field(i).String() {
			t.Errorf("label %s is not localized", name)
		}
	}
	if trayText("") != english || trayText("fr") != english {
		t.Fatal("unsupported tray language must fall back to English")
	}
	for _, labels := range []trayLabels{english, spanish} {
		if labels.message("") != labels.hint || labels.message("open-error") != labels.openError || labels.message("start-error") != labels.startError {
			t.Fatal("tray message lost its translated label")
		}
	}
}

func TestTrayStatusAndActivityInBothLanguages(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		t.Run(language, func(t *testing.T) {
			a := testApp(t)
			a.config.Language = language
			labels := trayText(language)
			a.active, a.requests, a.failures = 2, 3, 1
			s := a.trayState()
			if s.labels != labels || s.team != labels.teamEmpty || s.status != labels.unconfigured {
				t.Fatalf("unconfigured state: %+v", s)
			}
			if s.activity != fmt.Sprintf(labels.activity, 2, 3, 1) {
				t.Fatalf("activity: %s", s.activity)
			}
			a.apiKey, a.config.OrgID = "test-key", "org-id"
			a.organizations = []organization{{ID: "org-id", Name: "Engineering\nTeam"}}
			s = a.trayState()
			if s.status != labels.stopped || s.team != labels.teamPrefix+"Engineering Team" {
				t.Fatalf("stopped state: %+v", s)
			}
			a.login = &loginSession{Status: "pending"}
			if s = a.trayState(); s.status != labels.pending {
				t.Fatalf("pending state: %+v", s)
			}
			a.proxyServer = &http.Server{}
			if s = a.trayState(); s.status != labels.running {
				t.Fatalf("running state: %+v", s)
			}
			a.proxyServer = nil
		})
	}
}

func TestTrayFollowsLanguageSavedFromWindow(t *testing.T) {
	a := testApp(t)
	a.config.Language = "en"
	before := a.trayState()
	if w := adminRequest(a, "language", `{"language":"es"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	after := a.trayState()
	if after == before || after.labels != trayText("es") || after.status != after.labels.unconfigured {
		t.Fatalf("tray did not refresh with the saved language: %+v", after)
	}
	saved, err := readSettings(a.dir)
	if err != nil || saved.Language != "es" {
		t.Fatalf("language not persisted: %+v, %v", saved, err)
	}
	restarted, err := newApp(a.dir, &fakeVault{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.stop)
	if restarted.trayState().labels != trayText("es") {
		t.Fatal("restarting lost the explicit language")
	}
	if w := adminRequest(a, "language", `{"language":"fr"}`); w.Code != 400 {
		t.Fatal("unsupported language accepted")
	}
	if a.trayState() != after {
		t.Fatal("rejected language changed the tray")
	}
	if w := adminRequest(a, "language", `{"language":"en"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if a.trayState() != before {
		t.Fatal("switching back to English did not restore tray labels")
	}
}

func TestMenuTextSanitizesAndBoundsTeamNames(t *testing.T) {
	if got := menuText("Team\tOne\n"); got != "Team One " {
		t.Fatalf("unsafe menu text %q", got)
	}
	if got := menuText(strings.Repeat("é", 49)); len([]rune(got)) != 48 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long Unicode team name was not bounded: %q", got)
	}
}

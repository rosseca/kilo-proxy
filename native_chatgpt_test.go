//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"gioui.org/io/semantic"
	"image"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type nativeChatGPTFixture struct {
	h       *nativePointerHarness
	mu      sync.Mutex
	account map[string]any
	calls   map[string]int
	base    map[string]any
	gate    <-chan struct{}
	seen    chan struct{}
}

func newNativeChatGPTFixture(t *testing.T, language string, size image.Point) *nativeChatGPTFixture {
	u := nativeTestUI(t)
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
	f := &nativeChatGPTFixture{account: map[string]any{"connected": false, "status": "idle"}, calls: map[string]int{}, base: map[string]any{}}
	b, _ := json.Marshal(u.state)
	_ = json.Unmarshal(b, &f.base)
	f.base["language"] = language
	f.base["hasKey"], f.base["orgId"], f.base["auth"] = false, "", nil
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/models" {
			jsonResponse(w, 200, map[string]any{"models": []modelInfo{}, "revision": f.base["catalogRevision"]})
			return
		}
		f.mu.Lock()
		f.calls[r.Method+r.URL.Path]++
		if r.Method == "POST" {
			switch r.URL.Path {
			case "/api/chatgpt/login":
				f.account = map[string]any{"connected": false, "status": "pending", "code": "ABCD-1234", "verificationUrl": nativeChatGPTVerificationURL}
			case "/api/chatgpt/cancel", "/api/chatgpt/logout":
				f.account = map[string]any{"connected": false, "status": "idle"}
			case "/api/chatgpt/refresh":
				f.account["quota"] = map[string]any{"primary": map[string]any{"usedPercent": float64(35), "windowDurationMins": float64(300)}}
			default:
				t.Errorf("unexpected POST %s", r.URL.Path)
			}
		}
		snapshot := map[string]any{}
		for k, v := range f.base {
			snapshot[k] = v
		}
		snapshot["chatgpt"] = f.account
		snapshot["chatgptReady"] = nativeBool(f.account, "connected") && nativeString(f.account, "status") != "pending"
		snapshot["connectionReady"] = nativeBool(snapshot, "chatgptReady")
		raw, _ := json.Marshal(snapshot)
		gate, seen := f.gate, f.seen
		if r.URL.Path == "/api/state" {
			f.gate, f.seen = nil, nil
		} else {
			gate, seen = nil, nil
		}
		f.mu.Unlock()
		if seen != nil {
			close(seen)
		}
		if gate != nil {
			<-gate
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(server.Close)
	u.owner.adminHost = strings.TrimPrefix(server.URL, "http://")
	c := u.clientState()
	c.LaunchChecked, c.LaunchDetectStarted, c.OpenDesignChecked, c.OpenDesignDetectStarted = true, true, true, true
	u.terminalCommands = &nativeTerminalCommands{Started: true, Checked: true, ManualStarted: true, ManualChecked: true}
	u.owner.mu.Lock()
	u.owner.apiKey = ""
	u.owner.config.OrgID = ""
	u.owner.mu.Unlock()
	u.language = language
	u.page = "setup"
	u.setupStep = setupConnect
	f.h = &nativePointerHarness{t: t, u: u, size: size, now: time.Now()}
	f.refresh(t)
	return f
}
func (f *nativeChatGPTFixture) refresh(t *testing.T) {
	u := f.h.u
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
	u.refreshState()
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
	f.h.frame()
}
func (f *nativeChatGPTFixture) set(account map[string]any) {
	f.mu.Lock()
	f.account = account
	f.mu.Unlock()
}
func (f *nativeChatGPTFixture) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls["POST"+path]
}

func TestNativeChatGPTLoginOnboardingAndQuota(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		for _, size := range []image.Point{{1180, 820}, {780, 700}} {
			t.Run(language+"/"+fmtSize(size), func(t *testing.T) {
				f := newNativeChatGPTFixture(t, language, size)
				h, u := f.h, f.h.u
				login := u.tr("Sign in with ChatGPT", "Iniciar sesión con ChatGPT")
				if nativeUpdateHasText(h, "Organization ID") || nativeUpdateHasText(h, "ID de organización") {
					t.Fatal("ChatGPT onboarding requires Kilo organization")
				}
				h.reveal(login, semantic.Button)
				h.click(login, semantic.Button)
				nativeTestWait(t, u, func() bool {
					return !u.chatGPTRequestBusy() && nativeString(nativeMap(u.state["chatgpt"]), "status") == "pending"
				})
				h.frame()
				bridge := u.owner.desktop.(*nativeRecordingBridge)
				nativeTestWait(t, u, func() bool {
					bridge.mu.Lock()
					defer bridge.mu.Unlock()
					return bridge.URL == nativeChatGPTVerificationURL
				})
				if !nativeUpdateHasText(h, u.tr("Enter this code: ", "Introduce este código: ")+"ABCD-1234") {
					t.Fatal("device code missing")
				}
				nativeGridCapture(t, h, "chatgpt-pending-"+language+"-"+fmtSize(size))
				cancel := u.tr("Cancel ChatGPT login", "Cancelar login de ChatGPT")
				h.reveal(cancel, semantic.Button)
				h.click(cancel, semantic.Button)
				nativeTestWait(t, u, func() bool {
					return !u.chatGPTRequestBusy() && nativeString(nativeMap(u.state["chatgpt"]), "status") == "idle"
				})
				f.set(map[string]any{"connected": true, "status": "idle", "email": "person@example.test", "plan": "Plus", "quota": map[string]any{"primary": map[string]any{"usedPercent": float64(25), "windowDurationMins": float64(300)}, "secondary": map[string]any{"usedPercent": nil}}})
				f.refresh(t)
				if u.setupConnectionNeeded() || !u.agentConnectionReady() {
					t.Fatal("subscription connection still depends on Kilo credentials")
				}
				h.reveal(u.tr("Choose models", "Elegir modelos"), semantic.Button)
				h.click(u.tr("Choose models", "Elegir modelos"), semantic.Button)
				if u.setupStep != setupModels || f.count("/api/config") != 0 {
					t.Fatal("ChatGPT continuation saved a Kilo connection")
				}
				// The account view is shared by Connection/Settings; all quotas remain separate from money.
				u.page = "connection"
				h.frame()
				h.reveal(u.tr("Disconnect ChatGPT", "Desconectar ChatGPT"), semantic.Button)
				nativeGridCapture(t, h, "chatgpt-connected-"+language+"-"+fmtSize(size))
				refresh := u.tr("Refresh subscription usage", "Actualizar uso de la suscripción")
				h.reveal(refresh, semantic.Button)
				h.click(refresh, semantic.Button)
				nativeTestWait(t, u, func() bool { return !u.chatGPTRequestBusy() && f.count("/api/chatgpt/refresh") == 1 })
				h.frame()
				if nativeUpdateHasText(h, u.tr("Remaining balance", "Saldo restante")) {
					t.Fatal("subscription rendered Kilo money")
				}
				logout := u.tr("Disconnect ChatGPT", "Desconectar ChatGPT")
				h.reveal(logout, semantic.Button)
				h.click(logout, semantic.Button)
				nativeTestWait(t, u, func() bool {
					return !u.chatGPTRequestBusy() && !nativeBool(nativeMap(u.state["chatgpt"]), "connected")
				})
			})
		}
	}
}

func TestNativeChatGPTStalePollAndUnsafeLink(t *testing.T) {
	f := newNativeChatGPTFixture(t, "en", image.Pt(1180, 820))
	u := f.h.u
	gate, seen := make(chan struct{}), make(chan struct{})
	f.mu.Lock()
	f.gate, f.seen = gate, seen
	f.mu.Unlock()
	u.refreshState()
	<-seen
	u.chatGPTAction("login")
	nativeTestWait(t, u, func() bool {
		return !u.chatGPTRequestBusy() && nativeString(nativeMap(u.state["chatgpt"]), "status") == "pending"
	})
	close(gate)
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
	if nativeString(nativeMap(u.state["chatgpt"]), "status") != "pending" {
		t.Fatal("old poll reverted login")
	}
	bridge := u.owner.desktop.(*nativeRecordingBridge)
	nativeTestWait(t, u, func() bool {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		return bridge.URL == nativeChatGPTVerificationURL
	})
	bridge.mu.Lock()
	bridge.URL = ""
	bridge.mu.Unlock()
	for _, url := range []string{"https://evil.example/", nativeChatGPTVerificationURL + "?next=evil", "http://auth.openai.com/codex/device"} {
		u.state["chatgpt"] = map[string]any{"verificationUrl": url}
		u.openChatGPTAuthorization()
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.URL != "" {
		t.Fatal("opened untrusted auth link")
	}
}

func TestNativeChatGPTConcurrentAccountsPreserveSharedModels(t *testing.T) {
	f := newNativeChatGPTFixture(t, "en", image.Pt(1180, 1800))
	u := f.h.u
	nativeSeedSharedForTest(t, u, modelInfo{ID: "openai/gpt-5", Name: "GPT-5", ContextWindow: 128000}, modelInfo{ID: "chatgpt/gpt-5", Name: "GPT-5 · ChatGPT", ContextWindow: 128000})
	before := libraryFingerprint(u.modelLibraryValue())
	f.mu.Lock()
	f.base["hasKey"], f.base["orgId"], f.base["kiloReady"] = true, "kilo-team", true
	f.mu.Unlock()
	f.set(map[string]any{"connected": true, "status": "idle"})
	f.refresh(t)
	if !u.chatGPTConnected() || !nativeBool(u.state, "hasKey") {
		t.Fatal("both connections are not available together")
	}
	u.chatGPTAction("logout")
	nativeTestWait(t, u, func() bool { return !u.chatGPTRequestBusy() })
	if before != libraryFingerprint(u.modelLibraryValue()) || !nativeBool(u.state, "hasKey") {
		t.Fatal("subscription logout changed Kilo setup or shared models")
	}
}

func TestNativeChatGPTUnknownQuotaAndSubscriptionCosts(t *testing.T) {
	f := newNativeChatGPTFixture(t, "en", image.Pt(1180, 2200))
	u := f.h.u
	u.page = "connection"
	f.set(map[string]any{"connected": true, "status": "idle", "quota": map[string]any{"primary": map[string]any{"usedPercent": nil}}})
	f.refresh(t)
	if !nativeUpdateHasText(f.h, "Usage limits unavailable") {
		t.Fatal("unknown quota displayed as zero")
	}
	label, amount := u.reportedSpend(usageSummary{Requests: 2, SubscriptionRequests: 2})
	if strings.Contains(amount, "$") || label != "Subscription usage" {
		t.Fatal(fmt.Sprint(label, amount))
	}
}

func TestNativeChatGPTAsyncAuthorizationAndReconnect(t *testing.T) {
	f := newNativeChatGPTFixture(t, "en", image.Pt(1180, 2000))
	u := f.h.u
	u.chatGPTLoginRequested = true
	f.set(map[string]any{"connected": false, "status": "pending"})
	f.refresh(t)
	bridge := u.owner.desktop.(*nativeRecordingBridge)
	bridge.mu.Lock()
	opened := bridge.URL
	bridge.mu.Unlock()
	if opened != "" {
		t.Fatal("opened before device code was available")
	}
	f.set(map[string]any{"connected": false, "status": "pending", "code": "CODE", "verificationUrl": nativeChatGPTVerificationURL})
	f.refresh(t)
	nativeTestWait(t, u, func() bool {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		return bridge.URL == nativeChatGPTVerificationURL
	})
	bridge.mu.Lock()
	bridge.URL = ""
	bridge.mu.Unlock()
	f.refresh(t)
	bridge.mu.Lock()
	opened = bridge.URL
	bridge.mu.Unlock()
	if opened != "" {
		t.Fatal("polling repeatedly opened authorization")
	}
	f.set(map[string]any{"connected": true, "status": "error", "error": "refresh failed"})
	f.refresh(t)
	if !nativeUpdateHasText(f.h, "Reconnect ChatGPT") {
		t.Fatal("expired credentials have no reconnect action")
	}
	u.state["chatgptReady"] = false
	if u.chatGPTReady() {
		t.Fatal("explicit backend not-ready overridden")
	}
}

func TestNativeChatGPTMixedUnpricedCostsRemainUnknown(t *testing.T) {
	u := nativeTestUI(t)
	summary := usageSummary{Requests: 3, SubscriptionRequests: 2}
	label, amount := u.reportedSpend(summary)
	if label == "Subscription usage" || amount != "—" {
		t.Fatalf("unpriced Kilo requests hidden: %s %s", label, amount)
	}
	coverage := u.coverage(summary)
	if !strings.Contains(coverage, "0/1 requests") || !strings.Contains(coverage, "2 subscription requests") {
		t.Fatal(coverage)
	}
}

func TestNativeChatGPTReadyWhileKiloAuthorizationPending(t *testing.T) {
	f := newNativeChatGPTFixture(t, "en", image.Pt(1180, 820))
	u := f.h.u
	f.set(map[string]any{"connected": true, "status": "idle"})
	f.mu.Lock()
	f.base["auth"] = map[string]any{"status": "pending"}
	f.mu.Unlock()
	f.refresh(t)
	if u.connectionWorking() {
		t.Fatal("pending Kilo authorization blocks ready ChatGPT connection")
	}
	u.setupConnectionContinue()
	if u.setupStep != setupModels {
		t.Fatal("ChatGPT cannot continue independently")
	}
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] && !u.busy["POST/api/models"] })
}

func TestNativeDisconnectedIntegrationHintsOfferBothAccounts(t *testing.T) {
	for _, language := range []string{"en", "es"} {
		for _, client := range []string{"codex", "open-design"} {
			t.Run(language+"/"+client, func(t *testing.T) {
				u, recorder := nativeLaunchTestUI(t, client, false, false)
				nativeTestWait(t, u, func() bool { return !u.busy["GET/api/state"] })
				u.owner.mu.Lock()
				u.owner.apiKey = ""
				u.owner.config.OrgID = ""
				u.owner.mu.Unlock()
				u.state["chatgptReady"], u.state["chatgpt"] = false, map[string]any{"connected": false, "status": "idle"}
				u.language = language
				h := &nativePointerHarness{t: t, u: u, size: image.Pt(1180, 1800), now: time.Now()}
				h.frame()
				want := u.tr("Connect Kilo or ChatGPT before opening this app.", "Conecta Kilo o ChatGPT antes de abrir esta aplicación.")
				if client == "open-design" {
					want = u.tr("Connect Kilo or ChatGPT first.", "Conecta primero Kilo o ChatGPT.")
				}
				if !nativeUpdateHasText(h, want) {
					t.Fatalf("missing provider-neutral hint: %s", want)
				}
				if client == "open-design" {
					h.reveal(u.tr("Connect an account", "Conectar una cuenta"), semantic.Button)
					h.click(u.tr("Connect an account", "Conectar una cuenta"), semantic.Button)
					if u.page != "setup" {
						t.Fatal("account setup action failed")
					}
				}
				if recorder.count() != 0 || recorder.prepares.Load() != 0 {
					t.Fatal("disconnected hint prepared or launched a client")
				}
			})
		}
	}
}

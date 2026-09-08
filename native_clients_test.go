//go:build desktop

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func nativeClientModelsForTest() []modelInfo {
	tools := true
	one, two := 1.0, 2.0
	return []modelInfo{
		{ID: "vendor/one", Name: "Very Long First Model", ContextWindow: 64000, MaxOutputTokens: 4000, ReasoningEfforts: []string{"low", "high"}, Tools: &tools, OutputModalities: []string{"text"}, InputPrice: &one, OutputPrice: &two},
		{ID: "anthropic/claude-sonnet-4.6", Name: "Claude Sonnet", ContextWindow: 128000, Tools: &tools, OutputModalities: []string{"text"}},
	}
}

func TestNativeClientsSelectionsRemainIndependent(t *testing.T) {
	c := nativeClients{}
	for _, key := range []string{"codex", "codex-cli", "claude", "opencode", "zed", "cursor", "xcode-chat", "xcode-codex", "xcode-claude"} {
		s := c.selection(key)
		for _, m := range nativeClientModelsForTest() {
			if err := s.add(m, 50); err != nil {
				t.Fatal(err)
			}
		}
		s.choice("vendor/one").DisplayName = key
		s.Initial = "anthropic/claude-sonnet-4.6"
		s.Aliases["haiku"] = "vendor/one"
	}
	s := c.selection("codex")
	s.remove("anthropic/claude-sonnet-4.6")
	s.remove("vendor/one")
	if len(s.Models) != 0 || s.Initial != "" || len(s.Aliases) != 0 {
		t.Fatal("removed model remained as default or alias")
	}
	for key, s := range c.Selections {
		if key != "codex" && (len(s.Models) != 2 || s.Models[0].DisplayName != key) {
			t.Fatal("another client changed", key)
		}
	}
	if err := s.add(modelInfo{ID: "bad id"}, 50); err == nil {
		t.Fatal("invalid ID accepted")
	}
	if err := s.add(modelInfo{ID: "manual/model"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.add(modelInfo{ID: "manual/other"}, 1); err == nil {
		t.Fatal("model limit ignored")
	}
	if s.Models[0].Model.ContextWindow != 200000 {
		t.Fatal("unknown model has no safe editable default")
	}
}

func TestNativeClientsPayloadAndLoadRoundTrips(t *testing.T) {
	for _, key := range []string{"codex", "codex-cli", "claude", "opencode", "zed", "xcode-chat", "xcode-codex", "xcode-claude"} {
		t.Run(key, func(t *testing.T) {
			s := (&nativeClients{}).selection(key)
			for _, m := range nativeClientModelsForTest() {
				if err := s.add(m, 50); err != nil {
					t.Fatal(err)
				}
			}
			s.Initial = "anthropic/claude-sonnet-4.6"
			s.Models[0].DisplayName = "Short"
			s.Models[0].DefaultReasoning = "high"
			s.Mode = "modern"
			if key == "claude" || key == "xcode-claude" {
				s.Models[1].ClaudeEffort = "high"
				s.Aliases["haiku"] = "vendor/one"
			}
			payload, err := nativeClientPayload(key, s)
			if err != nil {
				t.Fatal(err)
			}
			var response any = payload
			if key == "opencode" || key == "zed" {
				response = map[string]any{"selection": payload, "configPath": "temporary-profile"}
			}
			if key == "xcode-codex" {
				response = payload.(map[string]any)["catalog"]
			}
			data, _ := json.Marshal(response)
			loaded, err := decodeNativeClientSelection(key, data, nativeClientModelsForTest())
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded.Models) != 2 || loaded.Initial != s.Initial || loaded.choice("vendor/one").DisplayName != "Short" {
				t.Fatalf("lost model identity/default/label: %+v", loaded)
			}
			if loaded.Saved != "" {
				t.Fatal("loaded profile incorrectly ready for current credentials")
			}
			if strings.Contains(key, "codex") {
				_, effort := nativeReasoningFor(*loaded.choice("vendor/one"))
				if effort != "high" {
					t.Fatal("reasoning default lost")
				}
			}
			if key == "opencode" || key == "zed" {
				m := loaded.choice("vendor/one")
				if m.Model.ContextWindow != 64000 || m.Model.MaxOutputTokens != 4000 {
					t.Fatal("limits lost")
				}
			}
			if nativeClientEndpoint(key) == "" {
				t.Fatal("client cannot reach its existing backend")
			}
		})
	}
	if _, err := decodeNativeClientSelection("zed", []byte(`{"selection":{"models":[]}}`), nil); err == nil {
		t.Fatal("invalid saved selection accepted")
	}
}

func TestNativeClientsFilteringAndDirtyState(t *testing.T) {
	catalog := nativeClientModelsForTest()
	catalog = append(catalog, modelInfo{ID: "image/one", Name: "Image"})
	s := (&nativeClients{}).selection("codex")
	_ = s.add(catalog[0], 50)
	s.Models[0].DisplayName = "Short"
	if got := nativeVisibleModels(catalog, s, "short", false, true, "codeModeRank"); len(got) != 1 || got[0].ID != "vendor/one" {
		t.Fatal("saved-name search failed")
	}
	if got := nativeVisibleModels(catalog, s, "", true, false, "codeModeRank"); len(got) != 1 {
		t.Fatal("selected-only filter failed")
	}
	if got := nativeVisibleModels(catalog, s, "", false, true, "codeModeRank"); len(got) != 2 {
		t.Fatal("tool/text filter failed")
	}
	if nativeModelPrice(catalog[0]) != "$1 / $2 USD / 1M" || nativeModelPrice(catalog[2]) != "— / — USD / 1M" {
		t.Fatal("unknown pricing misrepresented")
	}
	before := nativeSelectionFingerprint("codex", s, "local", "key", claudeCapabilities{})
	s.Models[0].DefaultReasoning = "high"
	if nativeSelectionFingerprint("codex", s, "local", "key", claudeCapabilities{}) == before {
		t.Fatal("reasoning did not mark selection dirty")
	}
	before = nativeSelectionFingerprint("codex", s, "local", "key", claudeCapabilities{})
	if nativeSelectionFingerprint("codex", s, "other-port", "key", claudeCapabilities{}) == before || nativeSelectionFingerprint("codex", s, "local", "new-key", claudeCapabilities{}) == before {
		t.Fatal("connection changes did not invalidate prepared profile")
	}
}

// These invoke Gio's real programmatic Clickable action, then the production
// asynchronous authenticated API. File effects happen only in the fixture root.
func TestNativeClientsWidgetActionsPrepareEveryEditor(t *testing.T) {
	for _, key := range []string{"codex", "codex-cli", "claude", "opencode", "zed", "xcode-chat", "xcode-codex", "xcode-claude"} {
		t.Run(key, func(t *testing.T) {
			u := nativeTestUI(t)
			u.page = "clients"
			u.models = nativeClientModelsForTest()
			u.client = key
			if strings.HasPrefix(key, "xcode-") {
				u.client = "xcode"
				u.clientState().Variant = strings.TrimPrefix(key, "xcode-")
				u.clients.Xcode = u.owner.xcodeInfo()
				u.clients.XcodeChecked = true
				u.clients.XcodeDetectStarted = true
			}
			if key == "claude" {
				u.setValue("clients-claude-mode", "modern")
				u.clientState().ClaudeDetectStarted = true
			}
			nativeTestFrame(t, u)
			u.clickable("client:" + key + ":select-all").Click()
			nativeTestFrame(t, u)
			s := u.clientState().selection(key)
			if len(s.Models) != 2 {
				t.Fatalf("native select-results did not select both models: %d", len(s.Models))
			}
			u.setValue(nativeClientField(key, "vendor/one", "name"), "Short Native")
			if strings.Contains(key, "codex") {
				field := nativeClientField(key, "vendor/one", "reasoning")
				u.clickable(field + ".toggle").Click()
				nativeTestFrame(t, u)
				u.clickable(field + ".option.high").Click()
				nativeTestFrame(t, u)
			}
			u.clickable("client:" + key + ":initial:vendor/one").Click()
			nativeTestFrame(t, u)
			u.clickable("client:" + key + ":prepare").Click()
			nativeTestFrame(t, u)
			nativeTestWait(t, u, func() bool { return s.Saved != "" })
			if s.Path == "" {
				t.Fatal("prepared profile has no path")
			}
			payload, err := nativeClientPayload(key, s)
			if err != nil {
				t.Fatal(err)
			}
			if key == "codex" || key == "codex-cli" || key == "xcode-codex" {
				data, err := os.ReadFile(filepath.Join(s.Path, "models.json"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(data), "Short Native") {
					t.Fatal("native label did not reach saved catalog")
				}
				config, err := os.ReadFile(filepath.Join(s.Path, "config.toml"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(config), "kilo-local") {
					t.Fatal("native prepare did not configure provider")
				}
			} else if key == "opencode" || key == "zed" {
				data, err := os.ReadFile(s.Path)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(data), "Short Native") {
					t.Fatal("native label did not reach editor settings")
				}
				if strings.Contains(string(data), "kl_local_") != (key == "opencode") {
					t.Fatal("incorrect editor credential storage")
				}
			} else if key == "xcode-chat" {
				if _, err := os.ReadFile(s.Path); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := os.ReadFile(filepath.Join(s.Path, "settings.json")); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := json.Marshal(payload)
			u.clickable("client:" + key + ":clear").Click()
			nativeTestFrame(t, u)
			u.clickable("client:" + key + ":load").Click()
			nativeTestFrame(t, u)
			nativeTestWait(t, u, func() bool { return len(u.clientState().selection(key).Models) == 2 })
			loaded := u.clientState().selection(key)
			if loaded.Saved != "" {
				t.Fatal("loading stale connection enabled launch")
			}
			afterPayload, err := nativeClientPayload(key, loaded)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(afterPayload)
			var left, right any
			_ = json.Unmarshal(before, &left)
			_ = json.Unmarshal(after, &right)
			if !reflect.DeepEqual(left, right) {
				t.Fatalf("save/load changed payload:\nbefore %s\nafter %s", before, after)
			}
		})
	}
}

func TestNativeClientsExportMasksPreviewsAndCopiesRealLocalKey(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "clients"
	u.client = "opencode"
	u.models = nativeClientModelsForTest()
	nativeTestFrame(t, u)
	u.clickable("client:opencode:select-all").Click()
	nativeTestFrame(t, u)
	s := u.clientState().selection("opencode")
	_, local, _ := u.clientBase()
	preview, err := u.clientExport("opencode", s, false)
	if err != nil {
		t.Fatal(err)
	}
	full, err := u.clientExport("opencode", s, true)
	if err != nil {
		t.Fatal(err)
	}
	if local == "" || strings.Contains(preview, local) || !strings.Contains(full, local) {
		t.Fatal("masked preview / full configuration boundary broken")
	}
	u.setChecked("client:opencode:show-config", true)
	nativeTestFrame(t, u)
	u.clickable("client:opencode:config-copy").Click()
	nativeTestFrame(t, u)
	bridge := u.owner.desktop.(*nativeRecordingBridge)
	bridge.mu.Lock()
	copied := bridge.Text
	bridge.mu.Unlock()
	if copied != full {
		t.Fatal("native Copy complete configuration did not use actual local credential")
	}
	for _, key := range []string{"codex", "codex-cli", "claude", "opencode", "zed"} {
		if nativeClientEndpoint(key) == "" {
			t.Fatal("missing route")
		}
	}
	// A remembered API key belongs to Kilo Proxy only, never an editor export.
	if strings.Contains(full, "synthetic-kilo-personal-key") {
		t.Fatal("upstream credential leaked into editor config")
	}
}

func TestNativeClientsMalformedProfileIsNotOverwritten(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "clients"
	u.client = "zed"
	u.models = nativeClientModelsForTest()
	dir := filepath.Join(u.owner.editorTestRoot, ".config", "zed")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	original := []byte("{ invalid team settings")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	nativeTestFrame(t, u)
	u.clickable("client:zed:select-all").Click()
	nativeTestFrame(t, u)
	u.clickable("client:zed:prepare").Click()
	nativeTestFrame(t, u)
	nativeTestWait(t, u, func() bool { return !u.busy["POST/api/editors/zed/profile"] })
	if !strings.Contains(u.notice, "valid JSON") {
		t.Fatalf("profile error was hidden: %s", u.notice)
	}
	if u.clientState().selection("zed").Saved != "" {
		t.Fatal("failed profile incorrectly marked ready")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatal("malformed user settings overwritten")
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatal("validation failure wrote a misleading backup")
	}
}

func TestNativeClientsGenericManualInputAndTabIsolation(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "clients"
	u.client = "generic"
	nativeTestFrame(t, u)
	u.setValue("client:generic:manual", "vendor/one")
	u.clickable("client:generic:add").Click()
	nativeTestFrame(t, u)
	s := u.clientState().selection("generic")
	if len(s.Models) != 1 || s.Initial != "vendor/one" {
		t.Fatal("manual model was not selected")
	}
	u.setValue("client:generic:manual", "invalid model id")
	u.clickable("client:generic:add").Click()
	nativeTestFrame(t, u)
	if len(s.Models) != 1 || s.Initial != "vendor/one" || u.value("client:generic:manual") != "invalid model id" {
		t.Fatal("invalid manual input destroyed the current selection or editable input")
	}
	u.setValue("client:generic:manual", "vendor/second")
	u.clickable("client:generic:add").Click()
	nativeTestFrame(t, u)
	if len(s.Models) != 1 || s.Initial != "vendor/second" {
		t.Fatal("generic model replacement retained both models")
	}
	u.clickable("client-tab:codex-cli").Click()
	nativeTestFrame(t, u)
	if u.client != "codex-cli" || len(u.clientState().selection("codex-cli").Models) != 0 {
		t.Fatal("client navigation copied a different editor selection")
	}
	u.clickable("client-tab:generic").Click()
	nativeTestFrame(t, u)
	if u.clientState().selection("generic").Initial != "vendor/second" {
		t.Fatal("navigation discarded the editor selection")
	}
}

func TestNativeClientsCatalogRefreshPreservesEditsAndUpdatesMetadata(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "clients"
	u.client = "codex"
	u.models = nativeClientModelsForTest()
	nativeTestFrame(t, u)
	u.setValue("client:codex:manual", "vendor/one")
	u.clickable("client:codex:add").Click()
	nativeTestFrame(t, u)
	u.setValue(nativeClientField("codex", "vendor/one", "name"), "My short name")
	u.setValue(nativeClientField("codex", "vendor/one", "reasoning"), "high")
	u.setChecked(nativeClientField("codex", "vendor/one", "custom"), true)
	u.setValue(nativeClientField("codex", "vendor/one", "levels"), "low,high")
	newPrice := 4.0
	u.models[0].InputPrice = &newPrice
	u.models[0].ReasoningEfforts = []string{"low"}
	u.models[0].ContextWindow = 99000
	nativeTestFrame(t, u)
	s := u.clientState().selection("codex")
	m := s.choice("vendor/one")
	if m.DisplayName != "My short name" || m.Model.ContextWindow != 64000 {
		t.Fatal("metadata refresh replaced profile edits")
	}
	if *m.Model.InputPrice != 4 || !reflect.DeepEqual(m.Model.ReasoningEfforts, []string{"low"}) {
		t.Fatal("selected model retained stale price/capabilities")
	}
	levels, initial := nativeReasoningFor(*m)
	if !m.ReasoningCustom || !reflect.DeepEqual(levels, []string{"low", "high"}) || initial != "high" {
		t.Fatal("catalog refresh discarded explicit reasoning customization")
	}
	visible := nativeVisibleModels(u.models, s, "My short name", true, false, "codeModeRank")
	if len(visible) != 1 || *visible[0].InputPrice != 4 {
		t.Fatal("model row did not use refreshed pricing")
	}
}

func TestNativeClientsLoadPreservesEditsMadeWhileRequestIsPending(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "clients"
	u.client = "zed"
	nativeTestFrame(t, u)
	u.clickable("client:zed:select-all").Click()
	nativeTestFrame(t, u)
	u.clickable("client:zed:prepare").Click()
	nativeTestFrame(t, u)
	s := u.clientState().selection("zed")
	nativeTestWait(t, u, func() bool { return s.Saved != "" })
	u.loadClient("zed")
	// The HTTP callback is queued onto the UI thread; this user edit happens
	// before that queue is drained, regardless of how fast the server responds.
	u.setValue(nativeClientField("zed", "vendor/one", "name"), "Keep my latest edit")
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/editors/zed/profile"] })
	if u.clientState().selection("zed") != s || s.choice("vendor/one").DisplayName != "Keep my latest edit" {
		t.Fatal("profile response erased a newer edit")
	}
	if !strings.Contains(u.notice, "changed while loading") {
		t.Fatalf("stale load was silently dropped: %s", u.notice)
	}
	// An intentional second load can replace those edits with the saved profile.
	u.loadClient("zed")
	nativeTestWait(t, u, func() bool { return !u.busy["GET/api/editors/zed/profile"] })
	if u.clientState().selection("zed") == s || u.clientState().selection("zed").choice("vendor/one").DisplayName == "Keep my latest edit" {
		t.Fatal("explicit retry could not load the saved selection")
	}
}

func TestNativeClientsCursorCheckCopyAndDisconnect(t *testing.T) {
	u := nativeTestUI(t)
	u.page = "clients"
	u.client = "cursor"
	// A synthetic ingress replaces the public network boundary. The native UI
	// still calls the production API/check/stop code; no ngrok is launched.
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer kl_cursor_synthetic" {
			http.Error(w, "bad ingress request", http.StatusUnauthorized)
			return
		}
		jsonResponse(w, 200, map[string]any{"object": "list", "data": []any{map[string]string{"id": "vendor/one"}}})
	}))
	t.Cleanup(public.Close)
	session := &cursorSession{Status: "running", URL: public.URL + "/v1", Key: "kl_cursor_synthetic", Models: []string{"vendor/one"}}
	u.owner.mu.Lock()
	u.owner.cursor = session
	u.owner.mu.Unlock()
	u.state["cursor"] = session
	s := u.clientState().selection("cursor")
	_ = s.add(modelInfo{ID: "unsaved/different-model"}, 50)
	nativeTestFrame(t, u)
	u.clickable("cursor-check").Click()
	nativeTestFrame(t, u)
	nativeTestWait(t, u, func() bool { return !u.busy["POST/api/cursor"] })
	if !strings.Contains(u.notice, "verified") || u.state["cursor"] == nil {
		t.Fatalf("successful check discarded connection: %s", u.notice)
	}
	for _, action := range []struct{ id, expected string }{
		{"cursor-copy-url", session.URL},
		{"cursor-copy-key", session.Key},
		{"cursor-copy-guide", cursorSetupGuide(session, s.ids(), "en", true)},
	} {
		u.clickable(action.id).Click()
		nativeTestFrame(t, u)
		bridge := u.owner.desktop.(*nativeRecordingBridge)
		bridge.mu.Lock()
		copied := bridge.Text
		bridge.mu.Unlock()
		if copied != action.expected {
			t.Fatal("incorrect native Cursor clipboard output", action.id)
		}
		if strings.Contains(copied, "unsaved/different-model") || strings.Contains(copied, "synthetic-kilo-personal-key") {
			t.Fatal("Cursor copy used unsaved models or upstream credentials")
		}
	}
	u.clickable("cursor-disconnect").Click()
	nativeTestFrame(t, u)
	nativeTestWait(t, u, func() bool { return !u.busy["POST/api/cursor"] })
	u.owner.mu.Lock()
	disconnected := u.owner.cursor == nil
	u.owner.mu.Unlock()
	if !disconnected || u.state["cursor"] != (*cursorSession)(nil) {
		t.Fatal("native disconnect did not revoke the synthetic session")
	}
}

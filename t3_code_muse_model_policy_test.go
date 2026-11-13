package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func t3MusePublisherPolicyFixture() modelLibrary {
	return modelLibrary{SchemaVersion: 1, DefaultModel: "qwen/unrelated", Models: []modelLibraryItem{
		{ID: "qwen/unrelated", DisplayName: "Meta Muse Spark"},
		{ID: "meta/muse-spark-1.3", ReasoningCustom: true, ReasoningLevels: []string{"low", "high"}, ReasoningEffort: "low"},
		{ID: "meta-llama/llama-4-maverick"},
		{ID: "meta/muse-wonder"},
		{ID: "meta-other/muse-spark"},
		{ID: "vendor/meta/muse-wonder"},
	}, Roles: map[string]string{"qwen/unrelated": "coding"}}
}

func TestT3MuseModelPolicyScopesOriginalPublishersAndKeepsOtherLibraries(t *testing.T) {
	library := t3MusePublisherPolicyFixture()
	before := cloneModelLibrary(library)
	prefs := t3CodeMuseOnlyPreferences("kilo")
	prefs.MuseModels = "official"
	prefs.Providers = []string{"muse", "opencode"}
	prefs.OpenCodeConnection = "kilo"
	catalog := []modelInfo{
		{ID: "qwen/unrelated", Provider: "meta", Name: "Meta Muse Wonder", ReasoningEfforts: []string{"max"}},
		{ID: "meta/muse-spark-1.3", Provider: "qwen", ReasoningEfforts: []string{"max"}},
		{ID: "meta-llama/llama-4-maverick"},
		{ID: "meta/muse-wonder", ReasoningEfforts: []string{"medium", "high", "max"}},
		{ID: "meta/muse-unselected"},
		{ID: "meta/unavailable", Connection: "chatgpt"},
	}
	policy, err := deriveT3CodeModelPolicy(prefs, library, catalog)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"meta/muse-spark-1.3", "meta/muse-unselected", "meta/muse-wonder", "meta-llama/llama-4-maverick"}
	var ids []string
	for _, m := range policy.MuseLibrary.Models {
		ids = append(ids, m.ID)
	}
	if !reflect.DeepEqual(ids, want) || policy.MuseLibrary.DefaultModel != want[0] {
		t.Fatal("wrong Meta subset/default", policy.MuseLibrary)
	}
	if !reflect.DeepEqual(policy.OpenCodeLibrary, before) || !reflect.DeepEqual(library, before) {
		t.Fatal("Muse changed another selection")
	}
	if !reflect.DeepEqual(policy.MuseReasoningLevels, map[string][]string{want[0]: {"low", "high"}, want[1]: {}, want[2]: {"medium", "high", "max"}, want[3]: {}}) {
		t.Fatal("reasoning escaped effective selection", policy.MuseReasoningLevels)
	}
	policy.MuseLibrary.Models[0].ReasoningLevels[0] = "max"
	policy.OpenCodeLibrary.Models[1].ReasoningLevels[0] = "max"
	if !reflect.DeepEqual(library, before) {
		t.Fatal("effective libraries share mutable reasoning")
	}
	library.DefaultModel = "meta/muse-wonder"
	policy, err = deriveT3CodeModelPolicy(prefs, library, catalog)
	if err != nil || policy.MuseLibrary.DefaultModel != "meta/muse-wonder" {
		t.Fatal("eligible default replaced", policy, err)
	}
	prefs.MuseModels = "shared"
	policy, err = deriveT3CodeModelPolicy(prefs, library, catalog)
	if err != nil || !reflect.DeepEqual(policy.MuseLibrary, library) {
		t.Fatal("explicit all-shared lost models", policy, err)
	}
}

func TestT3MuseModelPolicyEmptyDisabledNativeAndExistingLimit(t *testing.T) {
	prefs := t3CodeMuseOnlyPreferences("kilo")
	prefs.MuseModels = "official"
	unrelated := modelLibrary{SchemaVersion: 1, DefaultModel: "qwen/only", Models: []modelLibraryItem{{ID: "qwen/only"}}}
	if _, err := deriveT3CodeModelPolicy(prefs, unrelated, []modelInfo{{ID: "vendor/catalog-only"}}); err == nil || !strings.Contains(err.Error(), "No Meta models are available") {
		t.Fatal("empty Meta selection fell back", err)
	}
	prefs.MuseModels = "shared"
	if _, err := deriveT3CodeModelPolicy(prefs, unrelated, nil); err != nil {
		t.Fatal("all shared removed unrelated models", err)
	}
	for _, policy := range []string{"official", "shared"} {
		prefs.MuseModels = policy
		prefs.MuseConnection = "native"
		result, err := deriveT3CodeModelPolicy(prefs, emptyModelLibrary(), nil)
		if err != nil || len(result.MuseLibrary.Models) != 0 {
			t.Fatal("native Meta acquired Kilo model dependency", result, err)
		}
		prefs.MuseConnection = "kilo"
		prefs.Providers = []string{}
		if _, err := deriveT3CodeModelPolicy(prefs, emptyModelLibrary(), nil); err != nil {
			t.Fatal("disabled Muse acquired model dependency", err)
		}
		prefs.Providers = []string{"muse"}
	}
	catalog := []modelInfo{}
	for i := 0; i < 40; i++ {
		catalog = append(catalog, modelInfo{ID: fmt.Sprintf("meta/model-%d", i)})
	}
	prefs.MuseModels = "official"
	if policy, err := deriveT3CodeModelPolicy(prefs, emptyModelLibrary(), catalog); err != nil || len(policy.MuseLibrary.Models) != 40 {
		t.Fatal("new unrelated custom-model limit", err)
	}
}

func TestT3MuseModelPolicyMigrationAndStrictRequests(t *testing.T) {
	fresh := normalizeT3CodePreferences(t3CodePreferences{})
	if fresh.MuseModels != "official" {
		t.Fatal("new accounts do not default to Meta", fresh)
	}
	legacy := fresh
	legacy.MuseModels = ""
	legacy.MuseConnection = "kilo"
	if p := normalizeT3CodePreferences(legacy); p.MuseModels != "shared" {
		t.Fatal("legacy Kilo selection lost availability", p)
	}
	legacy.MuseConnection = "native"
	if p := normalizeT3CodePreferences(legacy); p.MuseModels != "official" {
		t.Fatal("new Muse selection lacks Meta default", p)
	}
	legacy.MuseModels = "shared"
	if p := normalizeT3CodePreferences(legacy); p.MuseModels != "shared" {
		t.Fatal("explicit shared choice reset", p)
	}
	data, _ := json.Marshal(fresh)
	var fields map[string]any
	json.Unmarshal(data, &fields)
	delete(fields, "museModels")
	data, _ = json.Marshal(fields)
	p, present, err := decodeT3CodePreferencesDocument(data)
	if err != nil || present.MuseModels || normalizeT3CodePreferences(p).MuseModels != "official" {
		t.Fatal("legacy request rejected", p, present, err)
	}
	for _, value := range []any{nil, "", "all", false, 2, []string{"shared"}} {
		fields["museModels"] = value
		data, _ = json.Marshal(fields)
		if _, _, err := decodeT3CodePreferencesDocument(data); err == nil {
			t.Fatal("invalid policy accepted", value)
		}
	}
	fields["museModels"] = "shared"
	data, _ = json.Marshal(fields)
	p, present, err = decodeT3CodePreferencesDocument(data)
	if err != nil || !present.MuseModels || p.MuseModels != "shared" {
		t.Fatal("explicit policy lost", err)
	}
	duplicate := strings.TrimSuffix(string(data), "}") + "," + `"museModels":"official"}`
	if _, _, err := decodeT3CodePreferencesDocument([]byte(duplicate)); err == nil {
		t.Fatal("duplicate policy accepted")
	}
}

func TestT3MuseModelPolicyLegacySaveKeepsExplicitChoice(t *testing.T) {
	a := t3CodeTestApp(t, "linux", t3CodeNightlyVersion)
	prefs := t3CodeMuseOnlyPreferences("kilo")
	prefs.MuseModels = "shared"
	a.config.T3Code = prefs
	raw, _ := json.Marshal(prefs)
	var document map[string]any
	json.Unmarshal(raw, &document)
	delete(document, "museModels")
	document["codex"] = true
	raw, _ = json.Marshal(document)
	response := adminRequest(a, "clients/t3-code/preferences", string(raw))
	if response.Code != 200 || a.config.T3Code.MuseModels != "shared" {
		t.Fatal("older UI reset saved shared choice", response.Code, response.Body.String())
	}
	document["museModels"] = "official"
	raw, _ = json.Marshal(document)
	response = adminRequest(a, "clients/t3-code/preferences", string(raw))
	if response.Code != 200 || a.config.T3Code.MuseModels != "official" {
		t.Fatal("explicit Meta policy did not save", response.Code, response.Body.String())
	}
	delete(document, "museModels")
	document["codex"] = false
	raw, _ = json.Marshal(document)
	response = adminRequest(a, "clients/t3-code/preferences", string(raw))
	if response.Code != 200 || a.config.T3Code.MuseModels != "official" {
		t.Fatal("legacy update reset explicit Meta", response.Code, response.Body.String())
	}
}

func TestT3MuseModelPolicyPreparedProfileScopesCatalogAuthAndRequests(t *testing.T) {
	a, binary, _ := t3CodeMuseKiloTestApp(t)
	a.config.T3Code.MuseModels = "official"
	library := t3MusePublisherPolicyFixture()
	state := a.modelLibrary.snapshot()
	if _, err := a.modelLibrary.save(library, state.Revision, false); err != nil {
		t.Fatal(err)
	}
	normal := filepath.Join(a.launcher.home, ".config", "muse")
	if err := os.MkdirAll(normal, 0700); err != nil {
		t.Fatal(err)
	}
	nativeSettings := []byte("synthetic native Meta configuration; preserve")
	if err := os.WriteFile(filepath.Join(normal, "settings.json"), nativeSettings, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("META_API_KEY", "synthetic-personal-meta-never-copy")
	metaCatalog := []modelInfo{
		{ID: "meta/muse-spark-1.3", Connection: "kilo"}, {ID: "meta-llama/llama-4-maverick", Connection: "kilo"},
		{ID: "meta/muse-wonder", Connection: "kilo"}, {ID: "meta/muse-wonder-new", Connection: "kilo"},
		{ID: "qwen/unrelated", Connection: "kilo"},
	}
	cache := filepath.Join(a.dir, "model-catalog.json")
	writeCatalog := func(models []modelInfo) {
		t.Helper()
		data, err := json.Marshal(nativeCachedCatalog{SchemaVersion: 1, OrgID: a.catalogScopeLocked(), KiloAccount: usageAccountID(a.apiKey, a.config.OrgID), KiloFetchedAt: time.Now().UTC(), Models: models, FetchedAt: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		if err := atomicCatalogFile(cache, data); err != nil {
			t.Fatal(err)
		}
	}
	writeCatalog(metaCatalog)
	prepareT3CodeFixture(t, a)
	saved := mustReadT3CodePrepared(t, a)
	if !a.t3CodeReady(saved, binary, a.launchRuntime()) || !reflect.DeepEqual(a.modelLibrary.snapshot().Library, library) {
		t.Fatal("filtered preparation changed global selection")
	}
	_, settingsPath, authPath := t3CodeMusePaths(t3CodePaths(a.dir).Root)
	settings, _ := os.ReadFile(settingsPath)
	auth, _ := os.ReadFile(authPath)
	var profile struct {
		Model   string                 `json:"model"`
		Catalog []museKiloCatalogModel `json:"model_catalog"`
	}
	if json.Unmarshal(settings, &profile) != nil || profile.Model != "meta/muse-spark-1.3" || len(profile.Catalog) != 4 {
		t.Fatal("wrong generated Meta catalog", string(settings))
	}
	for _, row := range profile.Catalog {
		if !strings.HasPrefix(row.ModelID, "meta/") && !strings.HasPrefix(row.ModelID, "meta-llama/") {
			t.Fatal("unrelated model passed shim", row)
		}
	}
	token := museProxyToken(a.config.LocalKey, "t3")
	if !bytes.Contains(auth, []byte(token)) || bytes.Contains(auth, []byte(a.config.LocalKey)) || bytes.Contains(auth, []byte("synthetic-personal-meta-never-copy")) {
		t.Fatal("credential boundary changed")
	}
	nativeAfter, _ := os.ReadFile(filepath.Join(normal, "settings.json"))
	if !bytes.Equal(nativeAfter, nativeSettings) {
		t.Fatal("normal Meta configuration modified")
	}
	effective, catalog, err := a.museScopeLibrary("t3")
	if err != nil || !reflect.DeepEqual(effective, saved.MuseLibrary) {
		t.Fatal("proxy did not use filtered selection", err)
	}
	for id, allowed := range map[string]bool{"meta/muse-wonder": true, "meta/muse-wonder-new": true, "qwen/unrelated": false} {
		req := httptest.NewRequest("POST", "http://127.0.0.1/t3-code/muse/v1/responses", strings.NewReader(fmt.Sprintf("{\"model\":%q}", id)))
		if err := prepareMuseModelRequest(req, effective, catalog); (err == nil) != allowed {
			t.Fatal("request escaped exact selection", id, err)
		}
	}
	expanded := append(append([]modelInfo{}, metaCatalog...), modelInfo{ID: "meta/muse-added", Connection: "kilo"})
	writeCatalog(expanded)
	a.t3CodeCheckRunning = func(string) (bool, error) { return true, nil }
	prepareT3CodeFixture(t, a)
	unchanged := mustReadT3CodePrepared(t, a)
	effective, catalog, err = a.museScopeLibrary("t3")
	if err != nil || !reflect.DeepEqual(unchanged.MuseLibrary, saved.MuseLibrary) || !reflect.DeepEqual(effective, saved.MuseLibrary) {
		t.Fatal("catalog addition interrupted or expanded a running Muse profile", err)
	}
	newModel := httptest.NewRequest("POST", "http://127.0.0.1/t3-code/muse/v1/responses", strings.NewReader(`{"model":"meta/muse-added"}`))
	if err := prepareMuseModelRequest(newModel, effective, catalog); err == nil {
		t.Fatal("unprepared new model reached the Muse route")
	}
	a.t3CodeCheckRunning = func(string) (bool, error) { return false, nil }
	prepareT3CodeFixture(t, a)
	saved = mustReadT3CodePrepared(t, a)
	if len(saved.MuseLibrary.Models) != 5 {
		t.Fatal("closed workspace did not pick up new Meta model", saved.MuseLibrary)
	}

	writeCatalog(metaCatalog[:2])
	if a.t3CodeReady(saved, binary, a.launchRuntime()) {
		t.Fatal("removed gateway models left the prepared catalog ready")
	}
	if _, _, err := a.museScopeLibrary("t3"); err == nil {
		t.Fatal("stale Muse profile accepted a request after the catalog changed")
	}
	writeCatalog(metaCatalog)
	a.config.T3Code.MuseModels = "shared"
	if a.t3CodeReady(saved, binary, a.launchRuntime()) {
		t.Fatal("policy change reused old preparation")
	}
	prepareT3CodeFixture(t, a)
	saved = mustReadT3CodePrepared(t, a)
	if !reflect.DeepEqual(saved.MuseLibrary, library) {
		t.Fatal("all-shared reprepare removed models")
	}

	// A legacy settings document must not narrow or invalidate this account's
	// existing shared preparation when its omitted field is recovered from disk.
	if err := writeSettings(a.dir, a.config); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(a.dir, "settings.json"))
	var document map[string]json.RawMessage
	json.Unmarshal(raw, &document)
	var fields map[string]json.RawMessage
	json.Unmarshal(document["t3Code"], &fields)
	delete(fields, "museModels")
	document["t3Code"], _ = json.Marshal(fields)
	raw, _ = json.Marshal(document)
	if err := os.WriteFile(filepath.Join(a.dir, "settings.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := readSettings(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	a.config = recovered
	if a.config.T3Code.MuseModels != "shared" || !a.t3CodeReady(saved, binary, a.launchRuntime()) || !reflect.DeepEqual(a.modelLibrary.snapshot().Library, library) {
		t.Fatal("legacy settings lost prepared shared availability")
	}
}

func TestT3MuseModelPolicyErrorsUseSavedLanguage(t *testing.T) {
	for _, message := range []string{"Choose Meta models or all shared models for Muse Code.", "No Meta models are available in the connected Kilo catalog. Refresh the catalog, choose all shared models, or disable Muse Code · Kilo.", "Invalid Muse model policy."} {
		spanish := nativeMessage(message, "es")
		if spanish == message || nativeMessage(spanish, "en") != message {
			t.Fatal("missing model policy translation", message)
		}
	}
}

func TestT3MuseModelPolicyReadSettingsLegacyDocument(t *testing.T) {
	for _, tc := range []struct {
		name, connection, policy, want string
		absent                         bool
	}{
		{"fresh settings", "", "", "official", true},
		{"legacy Kilo", "kilo", "", "shared", false},
		{"legacy native", "native", "", "official", false},
		{"explicit Meta", "kilo", "official", "official", false},
		{"explicit shared", "native", "shared", "shared", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			prefs := normalizeT3CodePreferences(t3CodePreferences{})
			prefs.Providers = []string{"muse"}
			prefs.MuseConnection = tc.connection
			raw, _ := json.Marshal(prefs)
			var fields map[string]any
			json.Unmarshal(raw, &fields)
			delete(fields, "museModels")
			if tc.policy != "" {
				fields["museModels"] = tc.policy
			}
			document := map[string]any{}
			if !tc.absent {
				document["t3Code"] = fields
			}
			raw, _ = json.Marshal(document)
			path := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			saved, err := readSettings(dir)
			if err != nil || saved.T3Code.MuseModels != tc.want {
				t.Fatalf("raw persisted %s: got %q want %q: %v", tc.name, saved.T3Code.MuseModels, tc.want, err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, raw) {
				t.Fatal("reading migration rewrote config")
			}
		})
	}
}

func TestT3MuseModelPolicyReadSettingsRejectsExplicitInvalidPolicy(t *testing.T) {
	for _, policy := range []any{nil, "", "all", false} {
		dir := t.TempDir()
		prefs := normalizeT3CodePreferences(t3CodePreferences{})
		raw, _ := json.Marshal(prefs)
		var fields map[string]any
		json.Unmarshal(raw, &fields)
		fields["museModels"] = policy
		raw, _ = json.Marshal(map[string]any{"t3Code": fields})
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readSettings(dir); err == nil {
			t.Fatal("explicit persisted invalid policy accepted", policy)
		}
	}
}

func TestT3MuseProviderCatalogColdPreparationAndAccountScope(t *testing.T) {
	a, binary, _ := t3CodeMuseKiloTestApp(t)
	a.config.T3Code.MuseModels = "official"
	if _, err := a.modelLibrary.save(emptyModelLibrary(), a.modelLibrary.snapshot().Revision, false); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/gateway/models" || r.Header.Get("X-KiloCode-OrganizationId") != "synthetic-org" {
			t.Errorf("wrong catalog request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		id := "meta/muse-first"
		switch r.Header.Get("Authorization") {
		case "Bearer synthetic-kilo":
		case "Bearer synthetic-rotated":
			id = "meta/muse-second"
		default:
			t.Error("catalog request lacked the scoped Kilo key")
			http.Error(w, "unexpected authorization", http.StatusUnauthorized)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"data": []modelInfo{{ID: id}, {ID: "vendor/muse-decoy"}}})
	}))
	defer upstream.Close()
	setUpstream(a, upstream.URL)

	prepareT3CodeFixture(t, a)
	first := mustReadT3CodePrepared(t, a)
	if first.MuseLibrary.DefaultModel != "meta/muse-first" || len(first.MuseLibrary.Models) != 1 || len(first.Library.Models) != 0 || !a.t3CodeReady(first, binary, a.launchRuntime()) {
		t.Fatal("cold catalog-only preparation failed", first.MuseLibrary)
	}
	a.mu.Lock()
	a.apiKey = "synthetic-rotated"
	a.catalogRevision++
	a.mu.Unlock()
	if a.t3CodeReady(first, binary, a.launchRuntime()) {
		t.Fatal("prior key's profile survived a same-organization key change")
	}
	if _, _, err := a.museScopeLibrary("t3"); err == nil {
		t.Fatal("prior key's prepared Muse catalog survived rotation")
	}
	prepareT3CodeFixture(t, a)
	second := mustReadT3CodePrepared(t, a)
	if second.MuseLibrary.DefaultModel != "meta/muse-second" || len(second.MuseLibrary.Models) != 1 || !a.t3CodeReady(second, binary, a.launchRuntime()) {
		t.Fatal("new key retained prior catalog", second.MuseLibrary)
	}
}

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const t3CodeRuntimeFixturePanel = `function rowsFor(S){let V=[];for(let t of ["codex","claudeAgent","grok"]){let n=t,r=S.providerInstances?.[n],i=r??{driver:t};V.push({instanceId:n,instance:i,driver:t,isDefault:!0,isDirty:r!==undefined})}return V}`

const t3CodeRuntimeFixtureProvidersSource = `function rowsFor(S){let V=[];for(let t of ["codex","claudeAgent","grok"]){let n=t,r=S.providerInstances?.[n],i=r??{driver:t};let a=r!==undefined;V.push({instanceId:n,instance:i,driver:t,isDefault:!0,isDirty:a})}return V}function modelCard(e){let {instanceId:n,instance:r,driverOption:i,liveProvider:s,mode:c}=e;let m=r.models;return displayModels({liveModels:s?.models,customModels:m})}`
const t3CodeRuntimeFixtureProjectionSource = `const driverNames={codex:"Codex",claudeAgent:"Claude",pi:"Pi"};function display(id){return id.split("_").map(word=>word[0]?.toUpperCase()+word.slice(1)).join(" ")}function defaultID(driver){return driver}function instanceName(e){let t=e.displayName?.trim(),n=driverNames[e.driver]??display(e.driver);if(t&&t!==n)return t;if(e.instanceId!==defaultID(e.driver)){let t=display(e.instanceId);if(t.length>0)return t}return t||n}function project(e){return e.map(e=>({instanceId:e.instanceId,driverKind:e.driver,displayName:instanceName(e),continuationGroupKey:e.continuation?.groupKey,isAvailable:e.availability!=="unavailable",snapshot:e,models:e.models}))};`

// The actual bundled panel computes isDirty first; keep the same unique call
// shape so a structural change in that contract causes an explicit failure.
func t3CodeRuntimeFixtureProviders() string {
	return strings.Replace(t3CodeRuntimeFixturePanel, "V.push({", "let a=r!==undefined;V.push({", 1)
}

func t3CodeRuntimeSynthetic(t *testing.T, platform string) (string, string, t3CodePrivateRuntime) {
	t.Helper()
	binary := t3CodeRuntimeSyntheticPackage(t, platform, []string{"assets/settings.providers-Fixture.js"})
	root := t.TempDir()
	record, err := prepareT3CodeRuntime(root, binary, platform, map[string][]string{t3CodeClaudeProxyID: {"claude-opus-5-5"}, t3CodeCodexProxyID: {"gpt-5.5"}})
	if err != nil {
		t.Fatal(err)
	}
	return binary, root, record
}

func t3CodeRuntimeSyntheticPackage(t *testing.T, platform string, providersAssets []string) string {
	t.Helper()
	binary := syntheticT3CodeExecutable(t, t.TempDir(), platform, "0.99.0-nightly.1")
	archive, err := t3CodeOpenPackage(binary, platform)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := archive.file.Name()
	files := map[string][]byte{}
	for _, name := range []string{"package.json", "apps/desktop/dist-electron/main.cjs", "apps/server/dist/bin.mjs"} {
		files[name], err = archive.read(name, 64<<10)
		if err != nil {
			t.Fatal(err)
		}
	}
	archive.file.Close()
	files["apps/desktop/dist-electron/main.cjs"] = append(files["apps/desktop/dist-electron/main.cjs"], []byte(" VITE_DEV_SERVER_URL T3CODE_PORT configuredBackendPort getDesktopScheme targetOrigin proxyRequest DesktopDevelopmentBackendPortRequiredError t3code-dev")...)
	files["apps/server/dist/client/index.html"] = []byte(`<html><script type="module" src="/` + providersAssets[0] + `"></script></html>`)
	for _, name := range providersAssets {
		files["apps/server/dist/client/"+name] = []byte(t3CodeRuntimeFixtureProvidersSource)
	}
	files["apps/server/dist/client/assets/utils-Fixture.js"] = []byte(t3CodeRuntimeFixtureProjectionSource)
	files["apps/server/dist/client/assets/public.css"] = []byte("body{color:red}")
	writeT3CodeFixtureASAR(t, archivePath, files)
	return binary
}

func TestT3CodeRuntimeProviderAssetNames(t *testing.T) {
	const legacy = "assets/settings.providers-Fixture.js"
	const chat = "assets/_chat.settings.providers-Fixture.js"
	for _, test := range []struct {
		name      string
		assets    []string
		wantError string
	}{
		{name: "legacy route", assets: []string{legacy}},
		{name: "chat route", assets: []string{chat}},
		{name: "ambiguous routes", assets: []string{legacy, chat}, wantError: "Ambiguous T3 Code Providers interface assets."},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := t3CodeRuntimeSyntheticPackage(t, "macos", test.assets)
			record, err := prepareT3CodeRuntime(t.TempDir(), binary, "macos", map[string][]string{t3CodeClaudeProxyID: {"claude-opus-5-5"}})
			if test.wantError != "" {
				if err == nil || err.Error() != test.wantError {
					t.Fatalf("expected %q, got %v", test.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if record.ProvidersAsset != test.assets[0] || !t3CodeRuntimeReady(record, binary, "macos") {
				t.Fatalf("provider asset %q did not produce a ready runtime", record.ProvidersAsset)
			}
			patched, err := os.ReadFile(filepath.Join(record.Root, filepath.FromSlash(record.ProvidersAsset)))
			if err != nil || !strings.Contains(string(patched), t3CodeClaudeNormalID) {
				t.Fatalf("provider account patch missing: %v", err)
			}
		})
	}
}

func TestT3CodeRuntimeModelsCardAsset(t *testing.T) {
	const route = "assets/_chat.settings.providers-Fixture.js"
	const dialog = "assets/AddProviderInstanceDialog-Fixture.js"
	const importDialog = `import{n as card}from"./AddProviderInstanceDialog-Fixture.js";`
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{"inline", t3CodeRuntimeFixtureProvidersSource, route},
		{"imported", importDialog + t3CodeRuntimeFixturePanel, dialog},
		{"unreferenced", t3CodeRuntimeFixturePanel, ""},
		{"missing", strings.ReplaceAll(importDialog, "-Fixture", "-Missing"), ""},
		{"ambiguous", importDialog + importDialog, ""},
		{"commented", "// " + importDialog, ""},
		{"quoted", "const sample = ' " + importDialog + " ';", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := map[string][]byte{route: []byte(test.source), dialog: []byte(t3CodeRuntimeFixtureProvidersSource)}
			got, err := t3CodeRuntimeModelsCardAsset(files, route)
			if got != test.want || (err != nil) != (test.want == "") {
				t.Fatalf("Models card asset = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestT3CodeRuntimePatchKeepsAccountsAndOtherProviders(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the JavaScript behavioral fixture")
	}
	source := strings.ReplaceAll(t3CodeRuntimeFixtureProviders(), "isDirty:r!==undefined", "isDirty:a")
	patched, err := patchT3CodeProvidersAsset([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	script := string(patched) + `
const settings={providerInstances:{codex:{driver:"codex",enabled:false},claudeAgent:{driver:"claudeAgent",enabled:false},grok:{driver:"grok",enabled:false},kilo_codex_normal:{driver:"codex",enabled:true},kilo_claude_normal:{driver:"claudeAgent",enabled:false}}};
const ids=()=>rowsFor(settings).map(x=>x.instanceId).join(",");
if(ids()!=="grok")throw Error("disabled duplicate shown or unrelated provider hidden");
settings.providerInstances.codex.enabled=true;
if(ids()!=="codex,grok")throw Error("enabled native account hidden");
delete settings.providerInstances.kilo_claude_normal;
if(ids()!=="codex,claudeAgent,grok")throw Error("unmanaged native account hidden");
settings.providerInstances.kilo_claude_normal={driver:"codex"};
if(ids()!=="codex,claudeAgent,grok")throw Error("wrong-driver account changed visibility");
`
	command := exec.Command(node, "--input-type=module", "--eval", script)
	command.Env = []string{"HOME=" + t.TempDir(), "PATH=" + filepath.Dir(node), "LANG=C"}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, output)
	}
	for _, invalid := range []string{strings.ReplaceAll(source, "isDefault:!0", "isDefault:!1"), source + source, strings.ReplaceAll(source, "S.providerInstances?.[n]", "S.instances?.[n]")} {
		if _, err := patchT3CodeProvidersAsset([]byte(invalid)); err == nil {
			t.Fatal("changed or ambiguous panel contract accepted")
		}
	}
}

const t3CodeRuntimeFixtureExplicitProviders = `function rowsFor(S){let V=[];for(let t of ["codex","claudeAgent","grok"]){let n=t,r=S.providerInstances?.[n],a=r!==undefined;(t==="codex"||t==="claudeAgent"||a||(r??{driver:t}).enabled!==false)&&V.push({instanceId:n,explicitInstance:r,driver:t,isDefault:!0,isDirty:a});for(let[n,r]of Object.entries(S.providerInstances??{})){if(n!==t&&r.driver===t)V.push({instanceId:n,explicitInstance:r,driver:r.driver,isDefault:!1})}}for(let[n,r]of Object.entries(S.providerInstances??{})){if(!["codex","claudeAgent","grok"].includes(r.driver))V.push({instanceId:n,explicitInstance:r,driver:r.driver,isDefault:!1})}return V}`

func TestT3CodeRuntimeExplicitAccountsPreserveUndefinedAndCustomRows(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the JavaScript behavioral fixture")
	}
	source := t3CodeRuntimeFixtureExplicitProviders
	patched, err := patchT3CodeProvidersAsset([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	script := "const original=(function(){" + source + ";return rowsFor;})();" + string(patched) + `
const cases=[
{name:"no configured defaults",settings:{},ids:"codex,claudeAgent,grok"},
{name:"disabled unmanaged defaults",settings:{providerInstances:{codex:{driver:"codex",enabled:false},claudeAgent:{driver:"claudeAgent",enabled:false},grok:{driver:"grok",enabled:false}}},ids:"codex,claudeAgent,grok"},
{name:"only disabled duplicates hidden",settings:{providerInstances:{codex:{driver:"codex",enabled:false},claudeAgent:{driver:"claudeAgent",enabled:false},grok:{driver:"grok",enabled:false},kilo_codex_normal:{driver:"codex",enabled:true},kilo_claude_normal:{driver:"claudeAgent",enabled:false},custom_codex:{driver:"codex",enabled:false,displayName:"My account"},kilo_codex_proxy:{driver:"codex",enabled:true},custom_other:{driver:"acpRegistry",enabled:false}}},ids:"kilo_codex_normal,custom_codex,kilo_codex_proxy,kilo_claude_normal,grok,custom_other"},
{name:"enabled native account kept",settings:{providerInstances:{codex:{driver:"codex",enabled:true},claudeAgent:{driver:"claudeAgent",enabled:false},kilo_codex_normal:{driver:"codex",enabled:true},kilo_claude_normal:{driver:"claudeAgent",enabled:true}}},ids:"codex,kilo_codex_normal,kilo_claude_normal,grok"},
{name:"wrong driver does not hide native",settings:{providerInstances:{codex:{driver:"codex",enabled:false},claudeAgent:{driver:"claudeAgent",enabled:false},kilo_codex_normal:{driver:"claudeAgent",enabled:true},kilo_claude_normal:{driver:"codex",enabled:true}}},ids:"codex,kilo_claude_normal,claudeAgent,kilo_codex_normal,grok"},
{name:"undefined native slots kept beside managed accounts",settings:{providerInstances:{kilo_codex_normal:{driver:"codex",enabled:true},kilo_claude_normal:{driver:"claudeAgent",enabled:false}}},ids:"codex,kilo_codex_normal,claudeAgent,kilo_claude_normal,grok"},
{name:"proxy account does not replace native slot",settings:{providerInstances:{codex:{driver:"codex",enabled:false},kilo_codex_proxy:{driver:"codex",enabled:true}}},ids:"codex,kilo_codex_proxy,claudeAgent,grok"}
];
for(const test of cases){
 const before=JSON.stringify(test.settings),expectedRows=original(test.settings),rows=rowsFor(test.settings);
 if(rows.map(row=>row.instanceId).join(",")!==test.ids)throw Error(test.name+": wrong visible account IDs");
 if(JSON.stringify(test.settings)!==before)throw Error(test.name+": settings changed");
 for(const row of rows){
  const expected=expectedRows.find(candidate=>candidate.instanceId===row.instanceId);
  if(!expected||row.explicitInstance!==expected.explicitInstance||JSON.stringify(row)!==JSON.stringify(expected))throw Error(test.name+": account identity or row changed");
 }
}
`
	command := exec.Command(node, "--input-type=module", "--eval", script)
	command.Env = []string{"HOME=" + t.TempDir(), "PATH=" + filepath.Dir(node), "LANG=C"}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, output)
	}
}

func TestT3CodeRuntimeExplicitAccountsRejectUnknownOrAmbiguousContracts(t *testing.T) {
	source := t3CodeRuntimeFixtureExplicitProviders
	legacy := strings.ReplaceAll(t3CodeRuntimeFixtureProviders(), "isDirty:r!==undefined", "isDirty:a")
	cases := map[string]string{
		"changed default flag":           strings.ReplaceAll(source, "isDefault:!0", "isDefault:!1"),
		"unknown instance field":         strings.ReplaceAll(source, "explicitInstance:r", "explicitConfig:r"),
		"uncorrelated optional instance": strings.ReplaceAll(source, "explicitInstance:r", "explicitInstance:other"),
		"wrong lookup ID":                strings.ReplaceAll(source, "S.providerInstances?.[n]", "S.providerInstances?.[other]"),
		"wrong settings field":           strings.ReplaceAll(source, "S.providerInstances?.[n]", "S.instances?.[n]"),
		"lookalike binding suffix":       strings.ReplaceAll(source, "r=S.providerInstances?.[n]", "other=S.providerInstances?.[n]"),
		"ambiguous lookup":               strings.ReplaceAll(source, "r=S.providerInstances?.[n]", "r=S.providerInstances?.[n],r=S.providerInstances?.[n]"),
		"inline dirty expression":        strings.ReplaceAll(source, "isDirty:a", "isDirty:r!==undefined"),
		"duplicate new shape":            source + source,
		"mixed old and new shapes":       legacy + source,
	}
	for name, invalid := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := patchT3CodeProvidersAsset([]byte(invalid)); err == nil {
				t.Fatal("changed or ambiguous explicit account-list contract accepted")
			}
		})
	}
}

func TestT3CodeRuntimeIntegrityAndReadOnlyOriginal(t *testing.T) {
	for _, platform := range []string{"macos", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			binary, _, record := t3CodeRuntimeSynthetic(t, platform)
			original, err := os.ReadFile(record.SourceArchive)
			if err != nil {
				t.Fatal(err)
			}
			if !t3CodeRuntimeReady(record, binary, platform) {
				t.Fatal("new runtime not ready")
			}
			current, err := os.ReadFile(record.SourceArchive)
			if err != nil || openDesignHash(current) != openDesignHash(original) {
				t.Fatal("original ASAR changed", err)
			}
			asset := filepath.Join(record.Root, filepath.FromSlash(record.ProvidersAsset))
			if err := os.WriteFile(asset, []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			if t3CodeRuntimeReady(record, binary, platform) {
				t.Fatal("tampered private asset accepted")
			}
			if _, err := startT3CodeRuntimeAssets(record); err == nil {
				t.Fatal("tampered private runtime served")
			}
		})
	}
}

func TestT3CodeRuntimeAssetServerIsBoundedAndCloses(t *testing.T) {
	_, _, record := t3CodeRuntimeSynthetic(t, runtime.GOOS)
	assets, err := startT3CodeRuntimeAssets(record)
	if err != nil {
		t.Fatal(err)
	}
	defer assets.Close()
	client := &http.Client{}
	for _, tc := range []struct {
		method, path, host, origin, accept string
		status                             int
	}{
		{"GET", "/", "", "", "", 200}, {"HEAD", "/assets/public.css", "", "", "", 200},
		{"GET", "/settings/providers", "", "", "text/html", 200},
		{"GET", "/settings/providers", "", "", "", 404}, {"GET", "/api/settings", "", "", "text/html", 404},
		{"GET", "/auth/token", "", "", "text/html", 404}, {"GET", "/missing.js", "", "", "text/html", 404},
		{"GET", "/%2e%2e/selection.json", "", "", "", 404}, {"GET", "/assets%5csecret", "", "", "", 404},
		{"POST", "/", "", "", "", 405}, {"GET", "/", "evil.invalid", "", "", 404}, {"GET", "/", "", "https://evil.invalid", "", 404},
	} {
		request, err := http.NewRequest(tc.method, assets.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.host != "" {
			request.Host = tc.host
		}
		request.Header.Set("Origin", tc.origin)
		request.Header.Set("Accept", tc.accept)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("%s %s: got %d want %d, %s", tc.method, tc.path, response.StatusCode, tc.status, body)
		}
		if tc.method == "HEAD" && len(body) != 0 {
			t.Fatal("HEAD returned body")
		}
		if response.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("private assets added browser CORS authorization")
		}
	}
	if err := os.WriteFile(filepath.Join(record.Root, "assets", "public.css"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(assets.URL + "/assets/public.css")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 503 {
		t.Fatal("tampered asset served", response.StatusCode)
	}
	assets.Close()
	if response, err := client.Get(assets.URL + "/"); err == nil {
		response.Body.Close()
		t.Fatal("closed asset listener still reachable")
	}
}

func TestT3CodeOfficialPrivateRuntimeMetadata(t *testing.T) {
	binary := os.Getenv("KILO_TEST_T3_RUNTIME_APP")
	if binary == "" {
		t.Skip("set KILO_TEST_T3_RUNTIME_APP to inspect an owned official package")
	}
	root := t.TempDir()
	if value := os.Getenv("KILO_TEST_T3_RUNTIME_OUTPUT"); value != "" {
		root = value
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	record, err := prepareT3CodeRuntime(root, binary, "macos", map[string][]string{t3CodeClaudeProxyID: {"claude-opus-5-5", "claude-sonnet-5-5", "claude-fable-5-1"}, t3CodeCodexProxyID: {"gpt-5.5"}})
	if err != nil || !t3CodeRuntimeReady(record, binary, "macos") {
		t.Fatal(record, err)
	}
	if value := os.Getenv("KILO_TEST_T3_RUNTIME_OUTPUT"); value != "" {
		data, _ := json.MarshalIndent(record, "", "  ")
		if err := os.WriteFile(filepath.Join(root, "runtime.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("private assets ready: files=%d providers=%s source=%s", len(record.Files), record.ProvidersAsset, record.SourceDigest)
}

func TestT3CodeRuntimeModelPolicyPreservesNativeCapabilitiesAndInstanceIsolation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the JavaScript behavioral fixture")
	}
	policy := map[string][]string{t3CodeClaudeProxyID: {"claude-opus-5-5", "vendor/shared"}, t3CodeCodexProxyID: {"gpt-5.5", "vendor/shared"}}
	projection, err := patchT3CodeModelsProjection([]byte(t3CodeRuntimeFixtureProjectionSource), policy)
	if err != nil {
		t.Fatal(err)
	}
	card, err := patchT3CodeModelsCard([]byte(t3CodeRuntimeFixtureProvidersSource), policy)
	if err != nil {
		t.Fatal(err)
	}
	script := string(projection) + "const modelCard=(function(){" + string(card) + ";return modelCard})();" + `
function displayModels(value){return value}
const capabilities={optionDescriptors:[{id:"effort",options:[{id:"high"},{id:"max"}]},{id:"thinking",options:[{id:"adaptive"}]}]};
const opus={slug:"claude-opus-5-5",capabilities},extra={slug:"claude-haiku-5-5",capabilities},gpt={slug:"gpt-5.5",capabilities},shared={slug:"vendor/shared",capabilities};
const models=[opus,extra,gpt,shared];
const records=[{instanceId:"kilo_claude_proxy",driver:"claudeAgent",models},{instanceId:"kilo_codex_proxy",driver:"codex",models},{instanceId:"kilo_claude_normal",driver:"claudeAgent",models},{instanceId:"kilo_codex_normal",driver:"codex",models},{instanceId:"pi",driver:"pi",models},{instanceId:"kilo_claude_proxy",driver:"codex",models}];
const projected=project(records);
if(projected[0].models.length!==2||projected[0].models[0]!==opus||projected[0].models[1]!==shared)throw Error("Claude allowlist or native object lost");
if(projected[1].models.length!==2||projected[1].models[0]!==gpt||projected[1].models[1]!==shared)throw Error("Codex allowlist lost");
if(projected[0].models[0].capabilities!==capabilities)throw Error("native capability options changed");
if(projected[0].snapshot.models[0]!==opus||projected[0].snapshot.models.length!==2)throw Error("picker snapshot leaked manifest model");
for(let i=2;i<records.length;i++){if(projected[i].models!==models||projected[i].snapshot.models!==models)throw Error("unrelated instance or driver filtered")}
if(models.length!==4||records[0].models!==models)throw Error("original server snapshot mutated");
const cardValue=modelCard({instanceId:"kilo_claude_proxy",instance:{driver:"claudeAgent",models},liveProvider:records[0]});
if(cardValue.liveModels.length!==2||cardValue.customModels.length!==2||cardValue.liveModels[0]!==opus)throw Error("Providers Models leaked unknown manifest or custom model "+JSON.stringify(cardValue));
const pending=modelCard({instanceId:"kilo_claude_proxy",instance:{driver:"claudeAgent",models}});
if(pending.liveModels!==undefined||pending.customModels.length!==2)throw Error("pending provider lost policy correlation");
const direct=modelCard({instanceId:"kilo_claude_normal",instance:{driver:"claudeAgent",models},liveProvider:records[2]});
if(direct.liveModels!==models||direct.customModels!==models)throw Error("direct account altered");
`
	command := exec.Command(node, "--input-type=module", "--eval", script)
	command.Env = []string{"HOME=" + t.TempDir(), "PATH=" + filepath.Dir(node), "LANG=C"}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, output)
	}
	for _, source := range []string{t3CodeRuntimeFixtureProjectionSource + t3CodeRuntimeFixtureProjectionSource, strings.ReplaceAll(t3CodeRuntimeFixtureProjectionSource, "snapshot:e,models:e.models", "snapshot:e,models:x.models"), strings.ReplaceAll(t3CodeRuntimeFixtureProjectionSource, "driverKind:", "other:")} {
		if _, err := patchT3CodeModelsProjection([]byte(source), policy); err == nil {
			t.Fatal("incompatible model projection accepted")
		}
	}
	for _, source := range []string{t3CodeRuntimeFixtureProvidersSource + t3CodeRuntimeFixtureProvidersSource, strings.ReplaceAll(t3CodeRuntimeFixtureProvidersSource, "liveProvider:s", "liveProvider:other"), strings.ReplaceAll(t3CodeRuntimeFixtureProvidersSource, "instanceId:n,instance:r", "instanceId:n,other:r")} {
		if _, err := patchT3CodeModelsCard([]byte(source), policy); err == nil {
			t.Fatal("incompatible model settings accepted")
		}
	}
}

func TestT3CodeRuntimePolicyIdentityAndRootAreBoundToPreparedWorkspace(t *testing.T) {
	binary, root, record := t3CodeRuntimeSynthetic(t, "macos")
	if !validT3CodeRuntimeRoot(record, root) {
		t.Fatal("prepared runtime root rejected")
	}
	if validT3CodeRuntimeRoot(record, t.TempDir()) {
		t.Fatal("outside workspace runtime accepted")
	}
	changed := record
	changed.ModelAllowlist = map[string][]string{t3CodeClaudeProxyID: {"claude-opus-5-5", "vendor/shared"}, t3CodeCodexProxyID: {"gpt-5.5"}}
	if validT3CodeRuntimeRoot(changed, root) || t3CodeRuntimeReady(changed, binary, "macos") {
		t.Fatal("changed descriptor policy accepted without prepared assets")
	}
	fresh, err := prepareT3CodeRuntime(root, binary, "macos", changed.ModelAllowlist)
	if err != nil || fresh.Root == record.Root || !validT3CodeRuntimeRoot(fresh, root) || !t3CodeRuntimeReady(fresh, binary, "macos") {
		t.Fatal("new policy did not create a distinct verified runtime", err)
	}
	for _, input := range []map[string][]string{{"claudeAgent": {"claude-opus-5-5"}}, {t3CodeClaudeProxyID: {"bad slug"}}, {t3CodeCodexProxyID: {"gpt-5.5", "gpt-5.5"}}} {
		if _, err := prepareT3CodeRuntime(root, binary, "macos", input); err == nil {
			t.Fatal("invalid model policy accepted")
		}
	}
}

func TestT3CodeRuntimeShutdownWatcherDisposesOnCloseOrQuit(t *testing.T) {
	for _, quitFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "closed_listener", true: "application_quit"}[quitFirst], func(t *testing.T) {
			_, _, record := t3CodeRuntimeSynthetic(t, runtime.GOOS)
			assets, err := startT3CodeRuntimeAssets(record)
			if err != nil {
				t.Fatal(err)
			}
			defer assets.Close()
			quit, finished := make(chan struct{}), make(chan struct{})
			go func() { assets.closeOnQuit(quit); close(finished) }()
			if quitFirst {
				close(quit)
			} else {
				if err := assets.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				t.Fatal("closed runtime retained its shutdown watcher")
			}
			select {
			case <-assets.done:
			default:
				t.Fatal("runtime disposal did not signal completion")
			}
			if err := assets.Close(); err != nil {
				t.Fatal("Close was not idempotent", err)
			}
			if response, err := http.Get(assets.URL + "/"); err == nil {
				response.Body.Close()
				t.Fatal("disposed listener remained reachable")
			}
		})
	}
}

func TestT3CodeRuntimePreparedDefaultOverridesOnlyKiloNativeFallback(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the JavaScript behavioral fixture")
	}
	policy := map[string][]string{t3CodeClaudeProxyID: {"claude-opus-5-5", "claude-fable-5-1"}, t3CodeCodexProxyID: {"gpt-6-astra", "gpt-6-luna"}}
	defaults := map[string]string{t3CodeClaudeProxyID: "claude-opus-5-5", t3CodeCodexProxyID: "gpt-6-astra"}
	source, err := patchT3CodeModelsProjection([]byte(t3CodeRuntimeFixtureProjectionSource), policy, defaults)
	if err != nil {
		t.Fatal(err)
	}
	script := string(source) + `
const capabilities={optionDescriptors:[{id:"effort",options:[{id:"high"},{id:"max"}]}]};
const opus={slug:"claude-opus-5-5",isDefault:false,capabilities},fable={slug:"claude-fable-5-1",isDefault:true,capabilities};
const models=[fable,opus],direct={instanceId:"kilo_claude_normal",driver:"claudeAgent",models};
const result=project([{instanceId:"kilo_claude_proxy",driver:"claudeAgent",models},direct,{instanceId:"kilo_claude_proxy",driver:"codex",models}]);
const fallback=result[0].models.find(model=>model.isDefault)?.slug??result[0].models[0]?.slug;
if(fallback!=="claude-opus-5-5")throw Error("native fallback ignored prepared default");
if(result[0].models.find(model=>model.slug==="claude-fable-5-1").isDefault!==false)throw Error("unselected native default remained active");
if(result[0].models.some(model=>model.capabilities!==capabilities))throw Error("native capabilities changed with default");
if(result[1].snapshot!==direct||result[1].models!==models||result[2].models!==models)throw Error("direct account or wrong driver altered");
if(opus.isDefault!==false||fable.isDefault!==true)throw Error("original server snapshot mutated");
const codex=project([{instanceId:"kilo_codex_proxy",driver:"codex",models:[{slug:"gpt-6-luna",isDefault:true,capabilities},{slug:"gpt-6-astra",capabilities}]}])[0];
if(codex.models.find(model=>model.isDefault)?.slug!=="gpt-6-astra")throw Error("Codex fallback ignored prepared default");
`
	command := exec.Command(node, "--input-type=module", "--eval", script)
	command.Env = []string{"HOME=" + t.TempDir(), "PATH=" + filepath.Dir(node), "LANG=C"}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, output)
	}
	binary, root, previous := t3CodeRuntimeSynthetic(t, "macos")
	selected := map[string]string{t3CodeClaudeProxyID: "claude-opus-5-5", t3CodeCodexProxyID: "gpt-5.5"}
	record, err := prepareT3CodeRuntimeWithModelDefaults(root, binary, "macos", previous.ModelAllowlist, selected)
	if err != nil || record.Root == previous.Root || !validT3CodeRuntimeRoot(record, root) || !t3CodeRuntimeReady(record, binary, "macos") {
		t.Fatal("default policy did not create a distinct verified runtime", err)
	}
	changed := record
	changed.ModelDefaults = map[string]string{t3CodeClaudeProxyID: "claude-fable-5-1"}
	if validT3CodeRuntimeRoot(changed, root) || t3CodeRuntimeReady(changed, binary, "macos") {
		t.Fatal("outside-selection default accepted")
	}
	for _, invalid := range []map[string]string{{"claudeAgent": "claude-opus-5-5"}, {t3CodeClaudeProxyID: "claude-fable-5-1"}, {t3CodeClaudeProxyID: ""}} {
		if _, err := prepareT3CodeRuntimeWithModelDefaults(root, binary, "macos", previous.ModelAllowlist, invalid); err == nil {
			t.Fatal("invalid account/default policy accepted")
		}
	}
}

func TestT3CodeRuntimeDirectNamesPreserveAccountIdentityAndCustomLabels(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the JavaScript behavioral fixture")
	}
	policy := map[string][]string{t3CodeClaudeProxyID: {"claude-opus-5-5"}, t3CodeCodexProxyID: {"gpt-6-astra"}}
	patched, err := patchT3CodeModelsProjection([]byte(t3CodeRuntimeFixtureProjectionSource), policy)
	if err != nil {
		t.Fatal(err)
	}
	script := `const original=(function(){` + t3CodeRuntimeFixtureProjectionSource + `;return {project,instanceName}})();` + string(patched) + `
const codex={instanceId:"kilo_codex_normal",driver:"codex",displayName:"Codex",models:[]};
const claude={instanceId:"kilo_claude_normal",driver:"claudeAgent",displayName:"Claude",models:[]};
if(original.instanceName(codex)!=="Kilo Codex Normal"||original.instanceName(claude)!=="Kilo Claude Normal")throw Error("fixture did not reproduce upstream direct-name fallback");
const records=[codex,claude,{...claude,displayName:" Claude "},{...claude,displayName:"Work Claude"},{...claude,displayName:""},{...claude,driver:"codex",displayName:"Codex"},{...codex,instanceId:"other_codex"},{...claude,instanceId:"claudeAgent"},{instanceId:"pi",driver:"pi",displayName:"Pi",models:[]}];
const projected=project(records);
if(projected[0].displayName!=="Codex"||projected[1].displayName!=="Claude"||projected[2].displayName!=="Claude")throw Error("managed direct labels ignored");
if(instanceName(codex)!=="Codex"||instanceName(claude)!=="Claude")throw Error("other consumers of the shared resolver retain old labels");
for(let i=3;i<records.length;i++){if(projected[i].displayName!==original.instanceName(records[i]))throw Error("unrelated or custom display name altered at "+i)}
for(let i=0;i<records.length;i++){if(projected[i].instanceId!==records[i].instanceId||projected[i].snapshot!==records[i]||projected[i].models!==records[i].models)throw Error("direct account identity, snapshot or model collection changed")}
if(codex.displayName!=="Codex"||claude.displayName!=="Claude")throw Error("original snapshot mutated");
`
	command := exec.Command(node, "--input-type=module", "--eval", script)
	command.Env = []string{"HOME=" + t.TempDir(), "PATH=" + filepath.Dir(node), "LANG=C"}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, output)
	}
	for _, source := range []string{
		strings.ReplaceAll(t3CodeRuntimeFixtureProjectionSource, "displayName:instanceName(e)", "displayName:instanceName(other)"),
		strings.ReplaceAll(t3CodeRuntimeFixtureProjectionSource, "if(t&&t!==n)return t;", "if(t)return t;"),
		strings.ReplaceAll(t3CodeRuntimeFixtureProjectionSource, "return t||n}", "return n||t}"),
		t3CodeRuntimeFixtureProjectionSource + `function instanceName(e){return e.displayName}`,
	} {
		if _, err := patchT3CodeModelsProjection([]byte(source), policy); err == nil {
			t.Fatal("changed or ambiguous direct display-name contract accepted")
		}
	}
}

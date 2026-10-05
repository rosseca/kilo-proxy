package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// This opt-in check calls the shipped authenticated MCP dispatcher, using its
// real account discovery and orchestration services. Only the Codex app-server
// is synthetic so its thread-bound bearer can stay inside the owned fixture;
// Claude uses its installed driver with a synthetic loopback Messages service.
// It performs no personal-account inference and opens no native window.
func TestSynaraInstalledMCPAccountDelegation(t *testing.T) {
	installed := os.Getenv("KILO_TEST_SYNARA_APP")
	if installed == "" || os.Getenv("KILO_TEST_SYNARA_DELEGATION") != "1" {
		t.Skip("set KILO_TEST_SYNARA_APP and KILO_TEST_SYNARA_DELEGATION=1 for the private MCP acceptance check")
	}
	if runtime.GOOS != "darwin" || !filepath.IsAbs(installed) {
		t.Fatal("installed MCP acceptance currently requires an absolute macOS Synara bundle")
	}
	if version, err := synaraVersion(installed, runtime.GOOS); err != nil || version != synaraSupportedVersion {
		t.Fatalf("official beta identity: %q %v", version, err)
	}
	// The installed provider owns and tears down native child process trees.
	// Fail before launching anything if the sandbox cannot query even our PID.
	ps, err := exec.LookPath("ps")
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(ps, "-p", strconv.Itoa(os.Getpid()), "-o", "pid=").CombinedOutput(); err != nil {
		t.Fatalf("native Synara acceptance requires process inspection for owned cleanup: %v %s", err, output)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	claude, err := resolveLaunchClient("claude", "")
	if err != nil {
		t.Fatal(err)
	}
	options := synaraProfileTestOptions(t)
	options.Library = modelLibrary{SchemaVersion: 1, DefaultModel: "chatgpt/gpt-6-astra", Models: []modelLibraryItem{
		{ID: "chatgpt/gpt-6-astra", DisplayName: "Synthetic Astra", ReasoningCustom: true, ReasoningEffort: "high", ReasoningLevels: []string{"low", "high"}},
		{ID: "anthropic/claude-opus-5", DisplayName: "Synthetic Opus", ReasoningEffort: "high"},
	}}
	options.Catalog = []modelInfo{{ID: "chatgpt/gpt-6-astra", ReasoningEfforts: []string{"low", "high"}}, {ID: "anthropic/claude-opus-5", ReasoningEfforts: []string{"low", "medium", "high"}}}
	options.NormalEnvironment = map[string]string{"CLAUDE_SECURESTORAGE_CONFIG_DIR": filepath.Join(options.NormalHome, ".claude")}
	options.ClaudeBinary = claude
	options.ClaudeCaps = claudeCaps("2.1.251")
	options.LocalKey = "synthetic-mcp-private-key"
	var upstreamMu sync.Mutex
	upstreamAccounts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account, credential := synaraClaudeNormalID, "synthetic-t3-normal-claude"
		if strings.HasPrefix(r.URL.Path, "/synara/") {
			account, credential = synaraClaudeProxyID, options.LocalKey
		}
		if r.Header.Get("Authorization") != "Bearer "+credential {
			t.Error("synthetic Claude account credential/routing changed")
			http.Error(w, "wrong synthetic account credential", http.StatusUnauthorized)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "invalid synthetic request", http.StatusBadRequest)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/count_tokens") {
			jsonResponse(w, 200, map[string]any{"input_tokens": 1})
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			http.Error(w, "unsupported synthetic route", http.StatusNotFound)
			return
		}
		if body["stream"] != true {
			jsonResponse(w, 200, map[string]any{"id": "synthetic_mcp_validation", "type": "message", "role": "assistant", "model": body["model"], "content": []any{map[string]any{"type": "text", "text": "Synthetic account delegation passed."}}, "stop_reason": "end_turn", "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}})
			return
		}
		upstreamMu.Lock()
		upstreamAccounts[account]++
		upstreamMu.Unlock()
		t3CodeFixtureMessagesSSE(w, stringValue(body["model"]), "Synthetic account delegation passed.", "synthetic_mcp_response")
	}))
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	options.Port, _ = strconv.Atoi(endpoint.Port())
	project := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("Owned synthetic MCP account fixture.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "--quiet"}, {"add", "README.md"}, {"-c", "user.name=Kilo Fixture", "-c", "user.email=kilo-fixture@invalid", "commit", "--quiet", "-m", "Synthetic MCP fixture"}} {
		command := exec.Command(git, args...)
		command.Dir, command.Env = project, cgClientEnv(project)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("temporary MCP project: %v %s", err, output)
		}
	}
	t3CodeFixtureNormalProfiles(t, options.NormalHome, options.Library, options.Catalog, server.URL, server.URL)
	privateRecords := filepath.Join(t.TempDir(), "owned-codex-records.jsonl")
	fakeSource := filepath.Join(t.TempDir(), "codex.mjs")
	encodedRecords, _ := json.Marshal(privateRecords)
	fake := strings.Replace(synaraDelegationCodexFixture, "__KILO_SYNARA_FIXTURE_RECORDS__", string(encodedRecords), 1)
	if err := os.WriteFile(fakeSource, []byte(fake), 0600); err != nil {
		t.Fatal(err)
	}
	options.CodexBinary = filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(options.CodexBinary, []byte("#!/bin/sh\nexec "+helperShellQuote(node)+" "+helperShellQuote(fakeSource)+" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	plan := saveSynaraProfilePlan(t, options)
	archive := filepath.Join(installed, "Contents", "Resources", "app.asar")
	source, _, err := synaraReadASARSource(archive, "apps/server/dist/index.mjs", synaraRuntimeSourceLimit)
	if err != nil {
		t.Fatal(err)
	}
	patched, err := patchSynaraDelegationServer(source, options)
	if err != nil {
		t.Fatal(err)
	}
	private := t.TempDir()
	uiHome := filepath.Join(private, "ui-home")
	if err := os.Mkdir(uiHome, 0700); err != nil {
		t.Fatal(err)
	}
	patchedPath := filepath.Join(private, "server.mjs")
	hookPath := filepath.Join(private, "backend-hook.mjs")
	if err := os.WriteFile(patchedPath, patched, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookPath, synaraRuntimeBackendHook(archive, patchedPath, openDesignHash(patched)), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, entry := range cgClientEnv(uiHome) {
		name, value, _ := strings.Cut(entry, "=")
		env[name] = value
	}
	env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	env["DISABLE_TELEMETRY"] = "1"
	env["DISABLE_ERROR_REPORTING"] = "1"
	fixture := map[string]any{"binary": t3CodeBundleExecutable(installed), "entry": filepath.Join(archive, "apps", "server", "dist", "index.mjs"), "hook": hookPath, "version": synaraSupportedVersion, "baseDir": options.DataDir, "project": project, "env": env, "records": privateRecords, "codexModel": "chatgpt/gpt-6-astra", "claudeModel": "anthropic/claude-opus-5"}
	data, _ := json.Marshal(fixture)
	manifest := filepath.Join(private, "fixture.json")
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(plan.CodexHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "scripts/verify_synara_mcp_delegation.mjs", manifest)
	command.Env = cgClientEnv(t.TempDir())
	command.WaitDelay = 3 * time.Second
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Installed Synara MCP account delegation passed") {
		t.Fatalf("private installed MCP account delegation failed: %v\n%s", err, output)
	}
	// The official gateway appends its MCP URL only to a session overlay. The
	// managed Codex provider configuration must not gain a bearer credential.
	after, err := os.ReadFile(filepath.Join(plan.CodexHome, "config.toml"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("the managed Codex profile changed during MCP delegation")
	}
	upstreamMu.Lock()
	for _, id := range []string{synaraClaudeNormalID, synaraClaudeProxyID} {
		if upstreamAccounts[id] < 2 {
			t.Errorf("MCP single/batch did not reach its isolated synthetic Claude account %s: %d", id, upstreamAccounts[id])
		}
	}
	upstreamMu.Unlock()
	t.Log(strings.TrimSpace(string(output)))
}

const synaraDelegationCodexFixture = `import { createInterface } from 'node:readline';
import { appendFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
const args=process.argv.slice(2);
if(args.includes('--version')){console.log('codex-cli 0.160.0');process.exit(0);}
if(args.includes('login')&&args.includes('status')){console.log(JSON.stringify({authenticated:true,authMethod:'apikey'}));process.exit(0);}
if(!args.includes('app-server'))process.exit(1);
const records=__KILO_SYNARA_FIXTURE_RECORDS__;
const record=value=>appendFileSync(records,JSON.stringify(value)+'\n',{mode:0o600});
const bearer=process.env.SYNARA_AGENT_GATEWAY_TOKEN;
if(bearer)record({kind:'lease',pid:process.pid,bearer});
const send=value=>process.stdout.write(JSON.stringify(value)+'\n');
let threadId=randomUUID(),turnId;
for await(const line of createInterface({input:process.stdin})){
 let request;try{request=JSON.parse(line);}catch{continue;}
 if(request.id===undefined)continue;
 let result={};
 switch(request.method){
 case 'initialize':result={userAgent:'Synthetic Codex fixture'};break;
 case 'account/read':result={account:{type:'apiKey'}};break;
 case 'model/list':result={data:[{id:'gpt-6-astra',name:'Native synthetic Astra',supportedReasoningEfforts:['low','high'],defaultReasoningEffort:'high'},{id:'chatgpt/gpt-6-astra',name:'Gateway synthetic Astra',supportedReasoningEfforts:['low','high'],defaultReasoningEffort:'high'}]};break;
 case 'thread/start':result={thread:{id:threadId}};record({kind:'thread',pid:process.pid,model:request.params?.model,home:process.env.CODEX_HOME});break;
 case 'turn/start':turnId=randomUUID();result={turn:{id:turnId,status:'inProgress'}};record({kind:'turn',pid:process.pid,model:request.params?.model,effort:request.params?.effort});break;
 case 'turn/interrupt':result={};break;
 }
 send({id:request.id,result});
 if(request.method==='turn/start')send({method:'turn/started',params:{threadId,turn:{id:turnId,status:'inProgress'}}});
 if(request.method==='turn/interrupt')send({method:'turn/completed',params:{threadId,turn:{id:turnId,status:'interrupted'}}});
}
`

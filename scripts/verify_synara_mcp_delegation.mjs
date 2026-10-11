// Opt-in installed-beta MCP acceptance. All homes, credentials, provider
// processes and messages belong to the disposable fixture supplied by Go.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { randomBytes, randomUUID } from 'node:crypto';
import { createServer } from 'node:net';

const fixture = JSON.parse(await readFile(process.argv[2], 'utf8'));
assert.equal(typeof fixture.version, 'string');
const listener = createServer();
await new Promise(resolve => listener.listen(0, '127.0.0.1', resolve));
const port = listener.address().port;
await new Promise(resolve => listener.close(resolve));
const credential = randomBytes(32).toString('hex');
const child = spawn(fixture.binary, ['--import', fixture.hook, fixture.entry,
  '--mode', 'desktop', '--port', String(port), '--host', '127.0.0.1',
  '--home-dir', fixture.baseDir, '--no-browser'], {
  cwd: fixture.project,
  env: { ...fixture.env, ELECTRON_RUN_AS_NODE: '1', NODE_USE_ENV_PROXY: '0',
    SYNARA_MODE: 'desktop', SYNARA_HOME: fixture.baseDir, SYNARA_AUTH_TOKEN: credential,
    SYNARA_NO_BROWSER: '1', SYNARA_AUTO_BOOTSTRAP_PROJECT_FROM_CWD: '0',
    SYNARA_LOG_PROVIDER_EVENTS: '0', SYNARA_DESKTOP_PARENT_STDIN: '1', OTEL_SDK_DISABLED: 'true' },
  stdio: ['pipe', 'pipe', 'pipe'],
});
let output = '', spawnError, ws, serial = 0;
child.on('error', error => { spawnError = error; });
child.stdout.on('data', data => { output = (output + data).slice(-18000); });
child.stderr.on('data', data => { output = (output + data).slice(-18000); });
const exited = () => child.exitCode !== null || child.signalCode !== null;
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const waitForExit = timeout => new Promise(resolve => {
  if (exited()) { resolve(true); return; }
  const onExit = () => { clearTimeout(timer); resolve(true); };
  const timer = setTimeout(() => { child.off('exit', onExit); resolve(false); }, timeout);
  child.once('exit', onExit);
});
const base = `http://127.0.0.1:${port}`;
const pending = new Map();
const rpc = (tag, payload) => new Promise((resolve, reject) => {
  const id = String(++serial);
  const timer = setTimeout(() => { pending.delete(id); reject(Error(`RPC timeout: ${tag}`)); }, 20000);
  pending.set(id, { resolve, reject, timer });
  ws.send(JSON.stringify({ _tag: 'Request', id, tag, payload, headers: [] }));
});
const dispatch = command => rpc('orchestration.dispatchCommand', { ...command,
  commandId: randomUUID(), createdAt: new Date().toISOString() });
const records = async () => {
  try { return (await readFile(fixture.records, 'utf8')).trim().split('\n').filter(Boolean).map(line => JSON.parse(line)); }
  catch (error) { if (error.code === 'ENOENT') return []; throw error; }
};
const snapshot = async threadId => (await rpc('orchestration.getThreadDetailSnapshot', { threadId })).thread;
const shell = () => rpc('orchestration.getShellSnapshot', {});
const projectId = randomUUID();
const ownedThreads = [];
// The token is obtained only from our synthetic app-server child, never from a
// real account or profile. It stays in memory and the private fixture file.
const caller = async () => {
  const prior = new Set((await records()).filter(value => value.kind === 'lease').map(value => value.pid));
  const threadId = randomUUID();
  const modelSelection = { provider: 'codex', instanceId: 'kilo_codex_proxy', model: fixture.codexModel, options: { reasoningEffort: 'low' } };
  await dispatch({ type: 'thread.create', threadId, projectId, title: 'Owned delegation caller',
    modelSelection, runtimeMode: 'approval-required', interactionMode: 'default', branch: null, worktreePath: null });
  ownedThreads.push(threadId);
  await dispatch({ type: 'thread.turn.start', threadId, modelSelection, runtimeMode: 'approval-required',
    interactionMode: 'default', message: { messageId: randomUUID(), role: 'user', attachments: [],
      text: 'Create the exact requested synthetic acceptance threads in this disposable project.' } });
  for (let i = 0; i < 150; i++) {
    const state = await snapshot(threadId);
    if (state?.session?.status === 'error' || state?.latestTurn?.state === 'error') throw Error('Synthetic caller failed to start');
    const lease = (await records()).find(value => value.kind === 'lease' && !prior.has(value.pid));
    if (lease && state?.latestTurn?.state === 'running' && state?.session?.activeTurnId) return { ...lease, threadId };
    await pause(100);
  }
  throw Error('Synthetic caller did not obtain an active gateway lease');
};
const mcp = async (lease, method, params) => {
  const response = await fetch(`${base}/mcp`, { method: 'POST',
    headers: { Authorization: `Bearer ${lease.bearer}`, Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({ jsonrpc: '2.0', id: randomUUID(), method, ...(params ? { params } : {}) }),
    signal: AbortSignal.timeout(30000) });
  assert.equal(response.status, 200, `Owned MCP ${method} HTTP status`);
  const data = await response.json();
  assert.equal(data.error, undefined, 'MCP dispatcher returned a protocol error');
  return data.result;
};
const call = (lease, name, args) => mcp(lease, 'tools/call', { name, arguments: args });
const content = result => JSON.parse(result.content.find(value => value.type === 'text').text);
const inspectCreated = async (result, selection) => {
  assert.notEqual(result.isError, true, JSON.stringify(result));
  const value = content(result);
  const id = value.threadId ?? value.id;
  assert.equal(typeof id, 'string', JSON.stringify(value));
  ownedThreads.push(id);
  for (let i = 0; i < 150; i++) {
    const state = await snapshot(id);
    assert.equal(state?.modelSelection.instanceId, selection.instanceId);
    assert.equal(state?.modelSelection.provider, selection.provider);
    assert.equal(state?.modelSelection.model, selection.model);
    assert.deepEqual(state?.modelSelection.options ?? {}, selection.options ?? {});
    if (state?.session?.status === 'error' || state?.latestTurn?.state === 'error') throw Error(`Delegated account ${selection.instanceId} failed`);
    if (state?.latestTurn?.state === 'completed' || selection.provider === 'codex' && state?.session?.activeTurnId) return id;
    await pause(100);
  }
  throw Error(`Delegated account ${selection.instanceId} did not start`);
};
const preflight = async (lease, name, args, code) => {
  const before = (await shell()).threads.map(value => value.id).sort();
  const result = await call(lease, name, args);
  assert.equal(result.isError, true, `${code} preflight succeeded`);
  assert.match(result.content.map(value => value.text ?? '').join('\n'), new RegExp(code));
  assert.deepEqual((await shell()).threads.map(value => value.id).sort(), before,
    'A rejected account selection created a partial or broken thread');
};

try {
  for (let i = 0; i < 150; i++) {
    try { if ((await fetch(`${base}/health`, { signal: AbortSignal.timeout(1000) })).status === 200) break; }
    catch { /* Await the owned backend only. */ }
    if (spawnError) throw spawnError;
    if (exited()) throw Error(`Owned backend exited: ${output}`);
    if (i === 149) throw Error('Owned backend health timeout');
    await pause(100);
  }
  const negotiate = new URL(`${base}/ws/negotiate`);
  for (const [key, value] of Object.entries({ 'x-synara-client-build': fixture.version,
    'x-synara-protocol-epoch': '1', 'x-synara-protocol-min-revision': '3', 'x-synara-protocol-max-revision': '3' })) negotiate.searchParams.set(key, value);
  const agreement = await (await fetch(negotiate)).json();
  assert.equal(agreement.negotiatedRevision, 3);
  const socket = new URL(`${base.replace('http:', 'ws:')}/ws`);
  for (const [key, value] of Object.entries({ token: credential, 'x-synara-client-build': fixture.version,
    'x-synara-protocol-epoch': '1', 'x-synara-protocol-revision': '3', 'x-synara-server-instance': agreement.serverInstanceId })) socket.searchParams.set(key, value);
  ws = new WebSocket(socket);
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(Error('WebSocket timeout')), 10000);
    ws.addEventListener('open', () => { clearTimeout(timer); resolve(); }, { once: true });
    ws.addEventListener('error', reject, { once: true });
  });
  ws.addEventListener('message', event => {
    const frame = JSON.parse(event.data);
    if (frame._tag === 'Chunk') { ws.send(JSON.stringify({ _tag: 'Ack', requestId: frame.requestId })); return; }
    if (frame._tag !== 'Exit') return;
    const waiting = pending.get(frame.requestId);
    if (!waiting) return;
    pending.delete(frame.requestId); clearTimeout(waiting.timer);
    if (frame.exit._tag === 'Success') waiting.resolve(frame.exit.value);
    else waiting.reject(Error(JSON.stringify(frame.exit)));
  });
  await rpc('server.refreshProviders', {});
  await dispatch({ type: 'project.create', projectId, title: 'Owned MCP acceptance', workspaceRoot: fixture.project });
  let lease = await caller();
  const tools = (await mcp(lease, 'tools/list')).tools;
  for (const name of ['synara_create_thread', 'synara_create_threads']) {
    const schema = tools.find(value => value.name === name).inputSchema;
    const target = name === 'synara_create_thread' ? schema.properties.target : schema.properties.threads.items.properties.target;
    assert.equal(target.properties.instanceId.type, 'string', 'Actual MCP tools omitted explicit account selection');
  }
  const capabilities = content(await call(lease, 'synara_capabilities', {}));
  const accounts = capabilities.providers.filter(value => ['codex', 'claudeAgent'].includes(value.provider));
  assert.deepEqual(accounts.map(value => value.instanceId).sort(),
    ['kilo_codex_normal', 'kilo_codex_proxy', 'kilo_claude_normal', 'kilo_claude_proxy'].sort());
  for (const account of accounts) {
    assert.equal(typeof account.displayName, 'string');
    assert.equal(account.enabled, true, `Fixture account disabled: ${account.instanceId}`);
    assert.equal(account.available, true, `Fixture account unavailable: ${JSON.stringify(account)}`);
    if (account.instanceId.endsWith('_proxy')) assert.ok(account.models.length > 0, `Managed model discovery empty: ${JSON.stringify(account)}`);
    // The native SDK may omit its catalog for an owned custom loopback URL.
    // Its unchanged, guarded default is the only safe native fallback; do not
    // invent a model or advertised capability for the normal account.
    else assert.ok(account.models.length > 0 || account.defaultModel, `Normal account has no discovered model or native default: ${JSON.stringify(account)}`);
  }
  assert.ok(accounts.find(value => value.instanceId === 'kilo_codex_proxy').models.some(value => value.slug === fixture.codexModel));
  assert.ok(!accounts.find(value => value.instanceId === 'kilo_codex_proxy').models.some(value => value.slug === 'gpt-6-astra'));
  const normalClaudeAccount = accounts.find(value => value.instanceId === 'kilo_claude_normal');
  const normalClaude = normalClaudeAccount.models.find(value => value.slug === 'opus') ?? normalClaudeAccount.models[0] ?? { slug: normalClaudeAccount.defaultModel };
  const selections = [
    { provider: 'codex', instanceId: 'kilo_codex_normal', model: 'gpt-6-astra', options: { reasoningEffort: 'low' } },
    { provider: 'codex', instanceId: 'kilo_codex_proxy', model: fixture.codexModel, options: { reasoningEffort: 'low' } },
    { provider: 'claudeAgent', instanceId: 'kilo_claude_normal', model: normalClaude.slug },
    { provider: 'claudeAgent', instanceId: 'kilo_claude_proxy', model: fixture.claudeModel, options: { effort: 'high' } },
  ];
  // Rejected plans keep the same active user turn and create no durable state.
  await preflight(lease, 'synara_create_thread', { requestId: randomUUID(), prompt: 'Synthetic exact account check', target: { provider: 'codex', model: fixture.codexModel } }, 'account_required');
  await preflight(lease, 'synara_create_threads', { requestId: randomUUID(), threads: [
    { prompt: 'Valid first target must not start before all preflight succeeds', target: selections[1] },
    { prompt: 'Unknown second target', target: { ...selections[1], instanceId: 'unknown_account' } },
  ] }, 'account_unavailable');
  await preflight(lease, 'synara_create_thread', { requestId: randomUUID(), prompt: 'Conflicting saved Claude effort', target: { ...selections[3], options: { effort: 'low' } } }, 'model_option_unavailable');
  for (const selection of selections) {
    const result = await call(lease, 'synara_create_thread', { requestId: randomUUID(), prompt: 'Reply with the synthetic account delegation response.', target: selection, environment: 'local', runtimeMode: 'approval-required' });
    await inspectCreated(result, selection);
    await dispatch({ type: 'thread.session.stop', threadId: lease.threadId });
    lease = await caller();
  }
  const settings = await rpc('server.getSettings', {});
  await rpc('server.updateSettings', { providerInstances: { ...settings.providerInstances,
    kilo_codex_normal: { ...settings.providerInstances.kilo_codex_normal, enabled: false } } });
  await preflight(lease, 'synara_create_threads', { requestId: randomUUID(), threads: [
    { prompt: 'Valid Kilo first target', target: selections[1] },
    { prompt: 'Disabled Normal second target', target: selections[0] },
  ] }, 'provider_unavailable');
  await rpc('server.updateSettings', { providerInstances: settings.providerInstances });
  const batch = await call(lease, 'synara_create_threads', { requestId: randomUUID(), threads: selections.map(target => ({ prompt: 'Reply with the synthetic account delegation response.', target, environment: 'local', runtimeMode: 'approval-required' })) });
  assert.notEqual(batch.isError, true, JSON.stringify(batch));
  const created = content(batch).threads;
  assert.equal(created.length, 4);
  for (let i = 0; i < created.length; i++) await inspectCreated({ content: [{ type: 'text', text: JSON.stringify(created[i]) }] }, selections[i]);
  const turns = (await records()).filter(value => value.kind === 'turn');
  assert.ok(turns.some(value => value.model === fixture.codexModel && value.effort === 'low'), 'Actual Codex turn lost its exact gateway model or Low effort');
  assert.ok(turns.some(value => value.model === 'gpt-6-astra' && value.effort === 'low'), 'Actual Normal Codex turn lost its native model or Low effort');
  console.log('Installed Synara MCP account delegation passed: four accounts, single/batch exact choice, model/effort, ambiguity/disabled preflight without partial threads');
} catch (error) {
  console.error(error.stack);
  // Never print provider lease records or the per-session bearer values.
  let safeOutput = output.replaceAll(/Bearer\s+[^\s"']+/g, 'Bearer [redacted]');
  for (const value of await records()) if (value.kind === 'lease' && value.bearer) safeOutput = safeOutput.replaceAll(value.bearer, '[owned bearer redacted]');
  console.error(safeOutput);
  process.exitCode = 1;
} finally {
  if (ws?.readyState === WebSocket.OPEN) {
    for (const threadId of ownedThreads) { try { await dispatch({ type: 'thread.session.stop', threadId }); } catch { /* Backend may already be draining. */ } }
  }
  ws?.close();
  for (const value of pending.values()) clearTimeout(value.timer);
  child.stdin?.end();
  if (!await waitForExit(20000)) {
    child.kill('SIGTERM');
    if (!await waitForExit(5000)) { child.kill('SIGKILL'); await waitForExit(5000); }
  }
  assert.ok(exited(), 'Owned Synara server did not terminate');
}

// Optional acceptance check against the version-checked private Synara server.
// The Go fixture supplies disposable homes and synthetic loopback upstreams.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { randomBytes, randomUUID } from 'node:crypto';
import { createServer } from 'node:net';
import { join } from 'node:path';

const fixturePath = process.argv[2] === '--manifest' ? process.argv[3] : process.argv[2];
const fixture = JSON.parse(await readFile(fixturePath, 'utf8'));
assert.equal(fixture.version, '1.0.0-beta.1', 'Unsupported Synara acceptance fixture');
const listener = createServer();
await new Promise(resolve => listener.listen(0, '127.0.0.1', resolve));
const port = listener.address().port;
await new Promise(resolve => listener.close(resolve));
const credential = randomBytes(32).toString('hex');
const child = spawn(fixture.binary, [...(fixture.backendHook ? ['--import', fixture.backendHook] : []), fixture.entry, '--mode', 'desktop', '--port', String(port),
  '--host', '127.0.0.1', '--home-dir', fixture.baseDir, '--no-browser'], {
  cwd: fixture.project,
  env: { ...fixture.env, ELECTRON_RUN_AS_NODE: '1', NODE_USE_ENV_PROXY: '0',
    SYNARA_MODE: 'desktop', SYNARA_HOME: fixture.baseDir, SYNARA_AUTH_TOKEN: credential,
    SYNARA_NO_BROWSER: '1', SYNARA_AUTO_BOOTSTRAP_PROJECT_FROM_CWD: '0', SYNARA_LOG_PROVIDER_EVENTS: '0', SYNARA_DESKTOP_PARENT_STDIN: '1', OTEL_SDK_DISABLED: 'true' },
  stdio: ['pipe', 'pipe', 'pipe'],
});
let output = '';
let spawnError;
child.on('error', error => { spawnError = error; });
const exited = () => child.exitCode !== null || child.signalCode !== null;
const waitForExit = timeout => new Promise(resolve => {
  if (exited()) { resolve(true); return; }
  const onExit = () => { clearTimeout(timer); resolve(true); };
  const timer = setTimeout(() => { child.off('exit', onExit); resolve(false); }, timeout);
  child.once('exit', onExit);
});
child.stdout.on('data', data => { output = (output + data).slice(-24000); });
child.stderr.on('data', data => { output = (output + data).slice(-24000); });
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const base = `http://127.0.0.1:${port}`;
let ws;
const token = credential;
let serial = 0;
const pending = new Map();
const streams = new Map();
const approved = new Set();
const rpc = (tag, payload) => new Promise((resolve, reject) => {
  const id = String(++serial);
  const timer = setTimeout(() => { pending.delete(id); reject(Error(`RPC timeout: ${tag}`)); }, 30000);
  pending.set(id, { resolve, reject, timer });
  ws.send(JSON.stringify({ _tag: 'Request', id, tag, payload, headers: [] }));
});
const dispatch = command => rpc('orchestration.dispatchCommand', {
  ...command, commandId: randomUUID(), createdAt: new Date().toISOString(),
});
const creation = {};
const snapshot = async threadId => {
  const data = await rpc('orchestration.getThreadDetailSnapshot', { threadId });
  assert.ok(data?.thread, `Missing Synara thread ${threadId}`);
  return data.thread;
};
const stopSession = threadId => dispatch({ type: 'thread.session.stop', threadId });
const awaitReply = async (threadId, previousCount) => {
  for (let i = 0; i < 240; i++) {
    const thread = await snapshot(threadId);
    for (const activity of thread.activities) {
      if (activity.kind !== 'approval.requested') continue;
      const request = activity.payload;
      if (!request) continue;
      if (approved.has(request.requestId)) continue;
      const path = join(fixture.project, 'README.md');
      const readCommand = `cat '${path.replaceAll("'", "'\\''")}'`;
      assert.ok(request.requestKind === 'command' && ['/bin/zsh', '/bin/bash', '/bin/sh'].some(shell =>
        request.detail === `${shell} -lc "${readCommand}"`), `Unexpected fixture permission request: ${JSON.stringify(request)}`);
      assert.equal(typeof request.lifecycleGeneration, 'string', 'Synara permission omitted its native lifecycle generation');
      approved.add(request.requestId);
      await dispatch({ type: 'thread.approval.respond',
        threadId, requestId: request.requestId, lifecycleGeneration: request.lifecycleGeneration, decision: 'accept' });
    }
    const replies = thread.messages.filter(message => message.role === 'assistant' && message.text.includes(fixture.expectedResponse));
    if (replies.length > previousCount && thread.latestTurn?.state === 'completed' &&
      thread.session?.activeTurnId === null && ['ready', 'stopped'].includes(thread.session?.status)) return thread;
    if (thread.session?.status === 'error' || thread.latestTurn?.state === 'error') {
      throw Error(`Agent failed: ${JSON.stringify({ session: thread.session, turn: thread.latestTurn, activities: thread.activities.slice(-12).map(({kind,summary,payload}) => ({kind,summary,requestKind:payload?.requestKind,requestId:payload?.requestId,detail:payload?.detail})) })}`);
    }
    await pause(250);
  }
  const thread = await snapshot(threadId);
  throw Error(`Turn timeout: ${JSON.stringify({ session: thread.session, turn: thread.latestTurn, messages: thread.messages, activities: thread.activities.slice(-12).map(({kind,summary,payload}) => ({kind,summary,requestKind:payload?.requestKind,requestId:payload?.requestId,detail:payload?.detail})) })}`);
};

try {
  for (let i = 0; i < 120; i++) {
    try {
      const health = await fetch(`${base}/health`, { signal: AbortSignal.timeout(2000) });
      if (health.status !== 200) throw Error(`Synara health ${health.status}`);
      break;
    } catch (error) {
      if (spawnError) throw spawnError;
      if (exited()) throw Error(`Backend exited: ${output}`);
      if (i === 119) throw error;
      await pause(250);
    }
  }
  const negotiate = new URL(`${base}/ws/negotiate`);
  for (const [key, value] of Object.entries({
    'x-synara-client-build': fixture.version, 'x-synara-protocol-epoch': '1',
    'x-synara-protocol-min-revision': '3', 'x-synara-protocol-max-revision': '3',
  })) negotiate.searchParams.set(key, value);
  const response = await fetch(negotiate, { signal: AbortSignal.timeout(5000) });
  const agreement = await response.json();
  assert.equal(response.status, 200, JSON.stringify(agreement));
  assert.equal(agreement.protocolEpoch, 1);
  assert.equal(agreement.negotiatedRevision, 3);
  const socket = new URL(`${base.replace('http:', 'ws:')}/ws`);
  for (const [key, value] of Object.entries({ token,
    'x-synara-client-build': fixture.version, 'x-synara-protocol-epoch': '1',
    'x-synara-protocol-revision': '3', 'x-synara-server-instance': agreement.serverInstanceId,
  })) socket.searchParams.set(key, value);
  ws = new WebSocket(socket);
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(Error('WebSocket timeout')), 10000);
    ws.addEventListener('open', () => { clearTimeout(timer); resolve(); }, { once: true });
    ws.addEventListener('error', reject, { once: true });
  });
  ws.addEventListener('message', event => {
    const frame = JSON.parse(event.data);
    if (frame._tag === 'Chunk') {
      streams.get(frame.requestId)?.push(...frame.values);
      ws.send(JSON.stringify({ _tag: 'Ack', requestId: frame.requestId }));
      return;
    }
    if (frame._tag !== 'Exit') return;
    const waiting = pending.get(frame.requestId);
    if (!waiting) return;
    pending.delete(frame.requestId);
    clearTimeout(waiting.timer);
    if (frame.exit._tag === 'Success') waiting.resolve(frame.exit.value);
    else waiting.reject(Error(JSON.stringify(frame.exit)));
  });
  const settings = await rpc('server.getSettings', {});
  for (const { id } of fixture.instances) {
    assert.ok(settings.providerInstances[id], `Missing prepared agent ${id}`);
    for (const variable of settings.providerInstances[id].environment) {
      if (variable.sensitive) assert.equal(variable.value, '', `Secret exposed by Synara for ${id}`);
    }
  }
  // Health is exposed before native driver probes settle; request and await
  // the shipped refresh operation instead of treating its initial warning as a failure.
  await rpc('server.refreshProviders', {});
  const config = await rpc('server.getConfig', {});
  for (const legacy of ['codex', 'claudeAgent']) {
    const provider = config.providers.find(value => value.instanceId === legacy);
    assert.ok(provider && !provider.enabled, `Legacy ${legacy} account was enabled: ${JSON.stringify(provider)}`);
  }
  assert.deepEqual(config.providers.filter(provider => provider.enabled).map(provider => provider.instanceId).sort(),
    ['kilo_codex_normal', 'kilo_codex_proxy', 'kilo_claude_normal', 'kilo_claude_proxy'].sort(),
    'Installed Synara did not expose exactly the four prepared accounts');
  for (const { id, model } of fixture.instances) {
    const provider = config.providers.find(value => value.instanceId === id);
    assert.ok(provider?.enabled && provider?.available, `Agent unavailable: ${id}: ${JSON.stringify(provider)}`);
    assert.equal(provider.status, 'ready', `Prepared Synara account is not ready: ${id}: ${JSON.stringify(provider)}`);
    assert.notEqual(provider.authStatus, 'unauthenticated', `Prepared Synara account is blocked by auth: ${id}`);
    console.log(`Checking installed Synara model discovery: ${id}`);
    const catalog = await rpc('provider.listModels', {
      provider: id.includes('claude') ? 'claudeAgent' : 'codex', instanceId: id,
    });
    provider.models = catalog.models;
    provider.customModels = (settings.providerInstances[id].config.customModels ?? []).map(slug => ({ slug, name: slug }));
    if (id.endsWith('_proxy')) assert.ok([...provider.models, ...provider.customModels].some(value =>
      value.slug === model || value.id === model), `Missing exact model ${model}: ${JSON.stringify(provider)}`);
  }
  // Force a real settings round trip to catch normalization that could make the
  // Kilo readiness check reject a workspace after an ordinary Synara setting change.
  await rpc('server.updateSettings', { enableAssistantStreaming: true });
  const projectId = randomUUID();
  const project = { type: 'project.create', projectId, title: 'Synthetic Synara acceptance', workspaceRoot: fixture.project };
  await dispatch(project);
  for (const { id, model, options, alternateModel } of fixture.instances) {
    const threadId = randomUUID();
    const selection = { provider: id.includes('claude') ? 'claudeAgent' : 'codex', instanceId: id, model, ...(options ? { options } : {}) };
    await dispatch({ type: 'thread.create', ...creation, threadId, projectId, title: id, modelSelection: selection,
      runtimeMode: 'approval-required', interactionMode: 'default', branch: null, worktreePath: null });
    const streamId = String(++serial);
    const frames = [];
    streams.set(streamId, frames);
    ws.send(JSON.stringify({ _tag: 'Request', id: streamId, tag: 'orchestration.subscribeThread',
      payload: { threadId }, headers: [] }));
    const send = () => dispatch({ type: 'thread.turn.start', threadId,
      message: { messageId: randomUUID(), role: 'user', text: 'Reply with the synthetic fixture response.', attachments: [] },
      modelSelection: selection, runtimeMode: 'approval-required', interactionMode: 'default' });
    await send();
    await awaitReply(threadId, 0);
    if (id === 'kilo_claude_proxy') selection.model = alternateModel;
    await send();
    const thread = await awaitReply(threadId, 1);
    if (id === 'kilo_claude_proxy') assert.equal(thread.modelSelection.model, alternateModel);
    assert.equal(thread.modelSelection.instanceId, id);
    assert.ok(frames.length > 0, `Thread subscription produced no events for ${id}`);
    await stopSession(threadId);
    // A new native process must resume this chat with the same instance/home.
    let closed = false;
    for (let i = 0; i < 200; i++) {
      const stopped = await snapshot(threadId);
      if (!stopped.session || stopped.session.status === 'stopped') { closed = true; break; }
      await pause(100);
    }
    assert.ok(closed, `Native session did not stop for ${id}: ${JSON.stringify((await snapshot(threadId)).session)}`);
    if (id === 'kilo_claude_proxy') selection.model = model;
    await send();
    const resumed = await awaitReply(threadId, 2);
    assert.equal(resumed.modelSelection.instanceId, id);
    assert.equal(resumed.modelSelection.model, model);
    await stopSession(threadId);
    ws.send(JSON.stringify({ _tag: 'Interrupt', requestId: streamId }));
    streams.delete(streamId);
    console.log(`Passed installed Synara: ${id}, exact model, streaming, second turn, stop and resume`);
    if (id === 'kilo_claude_proxy') console.log(`Passed installed Synara: ${model} High → ${alternateModel} Low → ${model} High after resume`);
  }
  assert.ok(approved.size > 0, 'No real provider permission request was approved');
  if (fixture.verifyModelPicker) {
    const { verifySynaraPicker } = await import('./verify_synara_picker.mjs');
    await verifySynaraPicker({ fixture, base, token, credential, config, projectId,
      rpc, dispatch, creation, snapshot, awaitReply, stopSession });
  }
  console.log('Installed Synara four-agent acceptance passed');
} catch (error) {
  console.error(error.stack);
  console.error(output);
  process.exitCode = 1;
} finally {
  ws?.close();
  for (const value of pending.values()) clearTimeout(value.timer);
  child.stdin?.end();
  if (!await waitForExit(20000)) {
    child.kill('SIGTERM');
    if (!await waitForExit(5000)) {
      child.kill('SIGKILL');
      await waitForExit(5000);
    }
  }
  assert.ok(exited(), 'Owned Synara server did not terminate');
}

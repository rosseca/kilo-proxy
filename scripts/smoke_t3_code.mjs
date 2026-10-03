// Optional acceptance check against an installed, unmodified T3 desktop server.
// The Go fixture supplies disposable homes and synthetic loopback upstreams.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { randomBytes, randomUUID } from 'node:crypto';
import { createServer } from 'node:net';
import { join } from 'node:path';

const fixturePath = process.argv[2] === '--manifest' ? process.argv[3] : process.argv[2];
const fixture = JSON.parse(await readFile(fixturePath, 'utf8'));
const listener = createServer();
await new Promise(resolve => listener.listen(0, '127.0.0.1', resolve));
const port = listener.address().port;
await new Promise(resolve => listener.close(resolve));
const credential = randomBytes(32).toString('hex');
const child = spawn(fixture.binary, [fixture.entry, '--mode', 'desktop', '--port', String(port),
  '--host', '127.0.0.1', '--base-dir', fixture.baseDir, '--no-browser', '--bootstrap-fd', '3', fixture.project], {
  env: { ...fixture.env, ELECTRON_RUN_AS_NODE: '1', NODE_USE_ENV_PROXY: '0',
    T3CODE_OTEL_SDK_DISABLED: 'true', OTEL_SDK_DISABLED: 'true' },
  stdio: ['ignore', 'pipe', 'pipe', 'pipe'],
});
let output = '';
child.stdout.on('data', data => { output = (output + data).slice(-12000); });
child.stderr.on('data', data => { output = (output + data).slice(-12000); });
child.stdio[3].end(JSON.stringify({ mode: 'desktop', noBrowser: true, port, host: '127.0.0.1',
  desktopBootstrapToken: credential, tailscaleServeEnabled: false, tailscaleServePort: 443 }) + '\n');
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const base = `http://127.0.0.1:${port}`;
let ws;
let token;
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
const snapshot = async threadId => {
  const response = await fetch(`${base}/api/orchestration/threads/${threadId}`, {
    signal: AbortSignal.timeout(5000), headers: { Authorization: `Bearer ${token}` },
  });
  const data = await response.json();
  assert.equal(response.status, 200, JSON.stringify(data));
  return data.thread;
};
const awaitReply = async (threadId, previousCount) => {
  for (let i = 0; i < 240; i++) {
    const thread = await snapshot(threadId);
    for (const activity of thread.activities) {
      if (activity.kind !== 'approval.requested') continue;
      const request = activity.payload;
      if (approved.has(request.requestId)) continue;
      const path = join(fixture.project, 'README.md');
      const readCommand = `cat '${path.replaceAll("'", "'\\''")}'`;
      assert.ok(request.requestKind === 'command' && ['/bin/zsh', '/bin/bash', '/bin/sh'].some(shell =>
        request.detail === `${shell} -lc "${readCommand}"`), `Unexpected fixture permission request: ${JSON.stringify(request)}`);
      approved.add(request.requestId);
      await dispatch({ type: 'thread.approval.respond', threadId, requestId: request.requestId, decision: 'accept' });
    }
    const replies = thread.messages.filter(message => message.role === 'assistant' && message.text.includes(fixture.expectedResponse));
    if (replies.length > previousCount && thread.latestTurn?.state === 'completed') return thread;
    if (thread.session?.status === 'error' || thread.latestTurn?.state === 'error') {
      throw Error(`Agent failed: ${JSON.stringify({ session: thread.session, turn: thread.latestTurn, activities: thread.activities.slice(-5) })}`);
    }
    await pause(250);
  }
  const thread = await snapshot(threadId);
  throw Error(`Turn timeout: ${JSON.stringify({ session: thread.session, turn: thread.latestTurn, messages: thread.messages, activities: thread.activities.slice(-5) })}`);
};

try {
  for (let i = 0; i < 120; i++) {
    try {
      const response = await fetch(`${base}/oauth/token`, { method: 'POST', signal: AbortSignal.timeout(2000),
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams({ grant_type: 'urn:ietf:params:oauth:grant-type:token-exchange', subject_token: credential,
          subject_token_type: 'urn:t3:params:oauth:token-type:environment-bootstrap', requested_token_type: 'urn:ietf:params:oauth:token-type:access_token' }),
      });
      const data = await response.json();
      assert.equal(response.status, 200);
      token = data.access_token;
      break;
    } catch (error) {
      if (child.exitCode !== null) throw Error(`Backend exited: ${output}`);
      if (i === 119) throw error;
      await pause(250);
    }
  }
  const ticketResponse = await fetch(`${base}/api/auth/websocket-ticket`, {
    method: 'POST', signal: AbortSignal.timeout(5000), headers: { Authorization: `Bearer ${token}` },
  });
  const ticket = await ticketResponse.json();
  assert.equal(ticketResponse.status, 200);
  ws = new WebSocket(`${base.replace('http:', 'ws:')}/ws?wsTicket=${encodeURIComponent(ticket.ticket)}`);
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
      if (variable.sensitive) assert.equal(variable.value, '', `Secret exposed by T3 for ${id}`);
    }
  }
  const config = await rpc('server.getConfig', {});
  for (const { id, model } of fixture.instances) {
    const provider = config.providers.find(value => value.instanceId === id);
    assert.ok(provider?.enabled && provider?.installed, `Agent unavailable: ${id}: ${JSON.stringify(provider)}`);
    if (id.endsWith('_proxy')) assert.ok(provider.models.some(value => value.slug === model || value.id === model), `Missing exact model ${model}: ${JSON.stringify(provider.models)}`);
  }
  // Force a real settings round trip to catch normalization that could make the
  // Kilo readiness check reject a workspace after an ordinary T3 setting change.
  await rpc('server.updateSettings', { patch: { confirmThreadDelete: false } });
  const projectId = randomUUID();
  await dispatch({ type: 'project.create', projectId, title: 'Synthetic T3 acceptance', workspaceRoot: fixture.project });
  for (const { id, model, options } of fixture.instances) {
    const threadId = randomUUID();
    const selection = { instanceId: id, model, ...(options ? { options } : {}) };
    await dispatch({ type: 'thread.create', threadId, projectId, title: id, modelSelection: selection,
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
    await send();
    const thread = await awaitReply(threadId, 1);
    assert.equal(thread.modelSelection.instanceId, id);
    assert.ok(frames.length > 0, `Thread subscription produced no events for ${id}`);
    await dispatch({ type: 'thread.session.stop', threadId });
    // A new native process must resume this chat with the same instance/home.
    let closed = false;
    for (let i = 0; i < 80; i++) {
      const stopped = await snapshot(threadId);
      if (!stopped.session || stopped.session.status === 'stopped') { closed = true; break; }
      await pause(100);
    }
    assert.ok(closed, `Native session did not stop for ${id}`);
    await send();
    const resumed = await awaitReply(threadId, 2);
    assert.equal(resumed.modelSelection.instanceId, id);
    await dispatch({ type: 'thread.session.stop', threadId });
    ws.send(JSON.stringify({ _tag: 'Interrupt', requestId: streamId }));
    streams.delete(streamId);
    console.log(`Passed installed T3: ${id}, exact model, streaming, second turn, stop and resume`);
  }
  console.log('Installed T3 four-agent acceptance passed');
} catch (error) {
  console.error(error.stack);
  console.error(output);
  process.exitCode = 1;
} finally {
  ws?.close();
  for (const value of pending.values()) clearTimeout(value.timer);
  child.kill('SIGTERM');
  await Promise.race([new Promise(resolve => child.once('exit', resolve)), pause(5000)]);
  if (child.exitCode === null) child.kill('SIGKILL');
}

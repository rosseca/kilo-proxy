#!/usr/bin/env node
// Opt-in compatibility smoke against an installed OpenMausBot distribution.
// No desktop is opened: export the bundled driver from a temporary copy after
// removing its CLI entrypoint. All requests go to a synthetic loopback gateway.
// Usage: node scripts/smoke_openmausbot.mjs [--resources /path/to/resources]
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, mkdir, readFile, writeFile, rm } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const args = process.argv.slice(2);
if (args.length && (args.length !== 2 || args[0] !== '--resources')) {
  throw new Error('Usage: node scripts/smoke_openmausbot.mjs [--resources PATH]');
}
const resources = resolve(args[1] || '/Applications/OpenMausBot.app/Contents/Resources');
const source = await readFile(join(resources, 'server', 'openmausbot.js'), 'utf8');
const marker = '\n// server/openmausbot.ts\n';
const entrypoint = source.lastIndexOf(marker);
// Fail closed if the distribution's entrypoint changes. Never execute its CLI.
assert(entrypoint > 0 && /^\s*main\(\)\.then\(exitAfterFlush,/.test(source.slice(entrypoint + marker.length)),
  'OpenMausBot CLI entrypoint changed; review the smoke adapter before running');
for (const symbol of ['init_openai_compat', 'OpenAICompatDriver', 'init_config', 'parseStoredConfig']) {
  assert(source.includes(symbol), `Installed bundle no longer exposes ${symbol}`);
}
const scratch = await mkdtemp(join(tmpdir(), 'kilo-openmaus-smoke-'));
const data = join(scratch, 'data');
await mkdir(data, { mode: 0o700 }); // Prevent legacy ~/.opengrokbot migration.
const originalEnv = { ...process.env };
const originalFetch = globalThis.fetch;
let server;
const runtimes = [];
const requests = [];
let serverFailure;
try {
  process.env.OMB_DATA_DIR = data;
  process.env.OMB_DISABLE_MODEL_CATALOG_FETCH = '1';
  // Synthetic ambient credentials deliberately differ from the dedicated key.
  // The smoke proves the dedicated connection never falls back to these.
  for (const name of Object.keys(process.env)) {
    if (/KEY|TOKEN|SECRET|PASSWORD|AUTH/i.test(name)) delete process.env[name];
  }
  process.env.OPENAI_COMPAT_API_KEY = 'synthetic-ambient-key';
  process.env.OPENAI_COMPAT_URL = 'https://must-not-contact.invalid/v1';
  process.env.OPENAI_COMPAT_PROVIDER = 'must-not-inherit';
  delete process.env.KILO_LOCAL_API_KEY;
  globalThis.fetch = (target, init) => {
    const url = new URL(typeof target === 'string' || target instanceof URL ? target : target.url);
    assert.equal(url.hostname, '127.0.0.1', 'Smoke attempted a non-loopback request');
    assert.equal(url.protocol, 'http:');
    assert(server && url.port === String(server.address().port), 'Unexpected local server');
    return originalFetch(target, { ...init, redirect: 'error' });
  };
  const imported = join(scratch, 'installed-driver.mjs');
  await writeFile(imported, source.slice(0, entrypoint) +
    '\ninit_openai_compat(); init_config(); export { OpenAICompatDriver, parseStoredConfig };\n', { mode: 0o600 });
  const { OpenAICompatDriver: driver, parseStoredConfig } = await import(pathToFileURL(imported));
  const models = ['openai/gpt-6.1-sol', 'chatgpt/gpt-6.1-sol'];
  const localKey = 'synthetic-local-proxy-key';
  const sentinel = 'KILO_OPENMAUS_SENTINEL';
  const executed = join(scratch, 'mcp-executed');
  const mcp = join(scratch, 'sentinel-mcp.mjs');
  await writeFile(mcp, `
import { createInterface } from 'node:readline';
import { writeFileSync } from 'node:fs';
const lines = createInterface({ input: process.stdin });
lines.on('line', line => {
  const req = JSON.parse(line);
  if (req.id === undefined) return;
  let result;
  if (req.method === 'initialize') result = { protocolVersion:'2024-11-05',capabilities:{tools:{}},serverInfo:{name:'synthetic-sentinel',version:'1'} };
  else if (req.method === 'tools/list') result = { tools:[{name:'read_sentinel',description:'Read only the synthetic fixture',inputSchema:{type:'object',properties:{},additionalProperties:false}}] };
  else if (req.method === 'tools/call') {
    if (req.params.name !== 'read_sentinel') throw new Error('Unexpected tool');
    writeFileSync(${JSON.stringify(executed)}, 'executed');
    result = {content:[{type:'text',text:${JSON.stringify(sentinel)}}]};
  } else result = {};
  process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:req.id,result})+'\\n');
});
`, { mode: 0o600 });
  server = createServer(async (req, res) => {
    try {
      assert.equal(req.headers.authorization, `Bearer ${localKey}`);
      assert.equal(req.headers['x-kilocode-organizationid'], undefined);
      if (req.url === '/v1/models') {
        requests.push({ route: 'models' });
        res.writeHead(200, { 'content-type': 'application/json' });
        res.end(JSON.stringify({ data: models.map((id, i) => ({ id, name: `Friendly ${i}`, context_length: 272000 })) }));
        return;
      }
      assert.equal(req.url, '/v1/chat/completions');
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      const body = JSON.parse(Buffer.concat(chunks).toString());
      requests.push(body);
      assert(models.includes(body.model));
      assert.equal(body.provider, undefined, 'Inherited an unrelated upstream provider pin');
      assert.equal(body.reasoning_effort, undefined, 'Upstream added reasoning support; update compatibility claims');
      if (!body.stream) {
        res.writeHead(200, { 'content-type': 'application/json' });
        res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'synthetic helper' }, finish_reason: 'stop' }],
          usage: { prompt_tokens: 20, completion_tokens: 4, prompt_tokens_details: { cached_tokens: 10 }, cost: 0.00012 } }));
        return;
      }
      assert.deepEqual(body.stream_options, { include_usage: true });
      const result = body.messages.find(message => message.role === 'tool');
      const tool = body.tools.find(tool => tool.function.name.endsWith('_read_sentinel'));
      assert(tool, 'Actual driver did not mount the synthetic MCP tool');
      res.writeHead(200, { 'content-type': 'text/event-stream' });
      const frame = payload => res.write(`data: ${JSON.stringify(payload)}\n\n`);
      if (result) {
        assert.equal(result.tool_call_id, 'synthetic-call');
        const assistant = body.messages.find(message => message.tool_calls?.length);
        assert.equal(assistant.tool_calls[0].id, result.tool_call_id);
        assert(result.content.includes(sentinel) || /denied/i.test(result.content));
        frame({ choices: [{ index: 0, delta: { content: 'tool roundtrip complete' }, finish_reason: null }] });
        frame({ choices: [{ index: 0, delta: {}, finish_reason: 'stop' }], usage: { prompt_tokens: 30, completion_tokens: 8 } });
      } else {
        // Fragmented tool args and a separate finish frame exercise the actual
        // streaming parser, rather than feeding it a preassembled completion.
        frame({ choices: [{ index: 0, delta: { tool_calls: [{ index: 0, id: 'synthetic-call', type: 'function', function: { name: tool.function.name, arguments: '{' } }] }, finish_reason: null }] });
        frame({ choices: [{ index: 0, delta: { tool_calls: [{ index: 0, function: { arguments: '}' } }] }, finish_reason: null }] });
        frame({ choices: [{ index: 0, delta: {}, finish_reason: 'tool_calls' }], usage: { prompt_tokens: 20, completion_tokens: 4 } });
      }
      res.end('data: [DONE]\n\n');
    } catch (error) {
      serverFailure = error;
      res.writeHead(500, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ error: { message: 'Synthetic gateway assertion failed' } }));
    }
  });
  await new Promise((resolveListen, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolveListen);
  });
  const raw = { url: `http://127.0.0.1:${server.address().port}/v1`, apiKeyEnv: 'KILO_LOCAL_API_KEY',
    model: models[0], managedModels: models, provider: '', tools: true };
  parseStoredConfig({ instances: { kiloProxy: { driver: 'openai-compat', enabled: true, config: raw } },
    defaultModelSelection: { instanceId: 'kiloProxy', model: models[0] }, context: { autoCompact: true, compactAt: 272000 } });
  const create = async (config, key = localKey) => {
    const runtime = await driver.create({ instanceId: 'kiloProxy', displayName: 'Kilo Proxy', enabled: true,
      config: driver.decodeConfig(config), environment: key ? { KILO_LOCAL_API_KEY: key } : {} });
    runtimes.push(runtime);
    return runtime;
  };
  const runtime = await create(raw);
  assert.equal((await runtime.snapshot()).authenticated, true);
  assert.deepEqual(runtime.models.options.map(model => model.id), models);
  assert.equal(runtime.models.default, models[0]);
  assert(runtime.models.options.every(model => model.label === model.id && model.contextWindow === undefined));
  assert.equal(runtime.adapter.capabilities.effortLevels, undefined, 'Driver now supports effort; update helper');
  assert.equal(runtime.adapter.capabilities.sessionModelSwitch, 'in-session');
  const beforeCatalog = requests.length;
  await runtime.refreshModels();
  assert.equal(requests.length, beforeCatalog, 'Managed models must not expand to the entire gateway catalog');
  const absent = await create(raw, '');
  assert.equal((await absent.snapshot()).state, 'unavailable', 'Dedicated key silently fell back to an ambient key');
  await assert.rejects(absent.adapter.sendTurn({ threadId: 'synthetic-missing-key', text: 'no credentials' }), /no API key/i);
  const usage = [];
  assert.equal(await runtime.generateText('synthetic helper', { onUsage: row => usage.push(row) }), 'synthetic helper');
  assert.equal(usage[0].cachedInput, 10);
  assert.equal(usage[0].costUsd, 0.00012);

  const turn = async (model, behavior) => {
    const events = [];
    const threadId = `synthetic-${behavior}`;
    let completion;
    const ended = new Promise((resolveEnd, reject) => {
      const timer = setTimeout(() => reject(new Error('OpenMausBot synthetic turn timed out')), 15000);
      runtime.adapter.onEvent(event => {
        if (event.threadId !== threadId) return;
        events.push(event);
        if (event.type === 'request.opened') {
          assert.equal(event.requestType, 'permission');
          void runtime.adapter.respondToRequest(threadId, event.requestId, { behavior });
        }
        if (event.type === 'turn.completed') {
          clearTimeout(timer);
          completion = event;
          resolveEnd();
        }
      });
    });
    await runtime.adapter.sendTurn({ threadId, model, text: 'Use the synthetic sentinel tool.', approvalMode: 'ask',
      integrations: { custom: { fixture: { command: process.execPath, args: [mcp] } } } });
    await ended;
    if (serverFailure) throw serverFailure;
    assert(events.some(event => event.type === 'request.opened'), 'Tool bypassed permission prompt');
    assert(events.some(event => event.type === 'request.resolved' && event.behavior === behavior));
    assert.equal(completion.ok, behavior === 'allow', JSON.stringify(events.filter(event => event.type === 'runtime.error')));
    assert.equal(completion.cost, null, 'Upstream now reports chat cost; revisit documentation');
    assert.equal(completion.usage.input, 50);
    assert.equal(completion.usage.output, 12);
  };
  await turn(models[0], 'deny');
  assert.equal(existsSync(executed), false, 'Denied MCP tool was executed');
  await turn(models[1], 'allow');
  assert.equal(await readFile(executed, 'utf8'), 'executed');
  assert.equal(requests.filter(request => request.stream).length, 4);
  assert.equal(requests.filter(request => request.route === 'models').length, 0);
  console.log('PASS: installed OpenMausBot managed model catalog, two namespaces, streamed tool/results, permission deny/allow, dedicated key isolation, nonstream usage');
  console.log('Verified limits: no effort selector; managed labels are model IDs; context metadata is not imported; streamed costs stay in Kilo Proxy Activity.');
} finally {
  await Promise.allSettled(runtimes.map(runtime => runtime.adapter.stopAll()));
  if (server) {
    server.closeAllConnections();
    await new Promise(resolveClose => server.close(resolveClose));
  }
  globalThis.fetch = originalFetch;
  for (const name of Object.keys(process.env)) if (!(name in originalEnv)) delete process.env[name];
  Object.assign(process.env, originalEnv);
  await rm(scratch, { recursive: true, force: true });
}

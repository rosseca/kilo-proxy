import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const fixture = JSON.parse(await readFile(process.argv[2], 'utf8'));
// Execute the official Hub submit closure and real private account/model
// resolver with synthetic Effect/discovery/persistence. No provider inference,
// actual workspace, account credentials or application process is involved.
const succeed = value => ({ value, *[Symbol.iterator]() { return yield this; }, pipe(...ops) { return ops.reduce((result, op) => op(result), this); } });
const fail = error => ({ error, *[Symbol.iterator]() { return yield this; }, pipe(...ops) { return ops.reduce((result, op) => op(result), this); } });
const run = effect => { if (effect.error) throw effect.error; return effect.value; };
const Effect = {
  succeed, fail,
  gen(fn) { try { const generator = fn(); let step = generator.next(); while (!step.done) step = generator.next(run(step.value)); return succeed(step.value); } catch (error) { return fail(error); } },
  map: fn => effect => { try { return effect.error ? effect : succeed(fn(effect.value)); } catch (error) { return fail(error); } },
  mapError: fn => effect => effect.error ? fail(fn(effect.error)) : effect,
  flatMap: fn => effect => effect.error ? effect : fn(effect.value),
  catch: fn => effect => effect.error ? fn(effect.error) : effect,
  forEach: (values, fn) => { try { return succeed(values.map((value, index) => run(fn(value, index)))); } catch (error) { return fail(error); } },
};
const Option = { isNone: value => value.none, match: ({ onNone, onSome }) => value => value.none ? onNone() : onSome(value.value) };
const ids = ['kilo_codex_normal', 'kilo_codex_proxy', 'kilo_claude_normal', 'kilo_claude_proxy'];
const settings = { providers: { codex: { enabled: true }, claudeAgent: { enabled: true } }, providerInstances: {} };
for (const id of [...ids, 'codex', 'claudeAgent']) {
  const driver = id.includes('claude') ? 'claudeAgent' : 'codex';
  settings.providerInstances[id] = { driver, enabled: ids.includes(id), displayName: id, config: { customModels: id.endsWith('_proxy') ? ['chatgpt/gpt-6-astra', 'anthropic/claude-opus-5'] : [] } };
}
const statuses = Object.entries(settings.providerInstances).map(([instanceId, value]) => ({ instanceId, driver: value.driver, available: true, authStatus: 'authenticated' }));
const calls = { queue: [], validations: [], projects: [], discovery: [], git: [], authority: 0 };
const reset = () => { for (const key of ['queue', 'validations', 'projects', 'discovery', 'git']) calls[key].length = 0; calls.authority = 0; };
let principalKind = 'coordinator', groupsEnabled = true;
const snapshotQuery = {
  getThreadShellById: () => succeed({ value: { runtimeMode: 'auto' } }),
  getProjectShellById: projectId => { calls.projects.push(projectId); return succeed({ value: { workspaceRoot: `/synthetic/${projectId}`, kind: 'project' } }); },
};
const providerDiscovery = { listModels(input) { calls.discovery.push(input); return succeed({ models: [{ slug: `${input.provider}-native-model`, name: 'Native model', supportedReasoningEfforts: [{ value: 'low' }, { value: 'high' }] }], source: 'synthetic-native' }); } };
const context = vm.createContext({
  Effect, Option, snapshotQuery, providerDiscovery,
  deriveProviderInstances: settings => Object.entries(settings.providerInstances).map(([instanceId, value]) => ({ ...value, instanceId, isDefault: instanceId === value.driver })),
  providerDefaultModel: provider => `${provider}-native-default`,
  getClaudeContextWindowSuffix: () => null,
  stripClaudeContextWindowSuffix: value => value,
  validateOptionsWithoutCatalog: () => {},
  validateAdvertisedOption: (target, descriptor) => {
    const effort = target.options?.reasoningEffort;
    if (effort && descriptor.supportedReasoningEfforts?.length && !descriptor.supportedReasoningEfforts.some(value => value.value === effort)) throw new context.AgentGatewayTargetError('model_option_unavailable', 'Unsupported native reasoning effort');
  },
  isServerGroupsEnabled: () => groupsEnabled,
  principalFor: () => succeed({ kind: principalKind, projectId: 'hub-project' }),
  ThreadId: { makeUnsafe: value => value }, TurnId: { makeUnsafe: value => value },
  resolveHubWorkSource: () => succeed([{ messageId: 'source-message' }]),
  errorText$1: error => error.message,
  ToolInputError: class extends Error {},
  mcpToolResultJson$1: value => ({ isError: false, value }),
  mcpToolResultError: message => ({ isError: true, message }),
  hubWorkItem: value => value,
  service: { submit(value) { calls.queue.push(value); return succeed({ replayed: false, items: value.tasks }); } },
});
vm.runInContext(`class AgentGatewayTargetError extends Error { constructor(code, message) { super(message); this.code = code; } }
globalThis.AgentGatewayTargetError = AgentGatewayTargetError;
${fixture.helpers.replace('__KILO_SYNARA_GATEWAY_MODELS__', JSON.stringify(fixture.models))}
${fixture.catalog}
${fixture.resolve}
globalThis.availabilities = kiloSynaraAccountAvailabilities(globalThis.testSettings, globalThis.testStatuses);`, Object.assign(context, { testSettings: settings, testStatuses: statuses }));
context.loadProviderAvailabilities = succeed(context.availabilities);
vm.runInContext(`${fixture.resolveTarget}
globalThis.dependencies = {
  validateTarget(input) { globalThis.testCalls.validations.push(input); return resolveAutomationTarget(input); },
  git: { readBranchContext(root) { globalThis.testCalls.git.push(root); return Effect.succeed({ isRepo: false }); } }
};
${fixture.hubSubmit}
globalThis.submit = submit;`, Object.assign(context, { testCalls: calls }));
const submit = (threads, assertCallerTurnActive = () => { calls.authority++; return succeed(); }) => {
  const input = { requestId: 'exact-plan', threads };
  const before = JSON.stringify(input);
  const result = run(context.submit(input, { callerThreadId: 'coordinator', callerTurnId: 'turn', assertCallerTurnActive }));
  assert.equal(JSON.stringify(input), before, 'Validation must not rewrite the durable request/fingerprint');
  return result;
};
const targetFor = id => {
  const provider = id.includes('claude') ? 'claudeAgent' : 'codex';
  return { provider, instanceId: id, model: id.endsWith('_normal') ? `${provider}-native-model` : provider === 'claudeAgent' ? 'anthropic/claude-opus-5' : 'chatgpt/gpt-6-astra', ...(id === 'kilo_claude_proxy' ? { options: { effort: 'high' } } : {}) };
};
const specs = ids.map(id => ({ prompt: 'Synthetic delegation contract', target: targetFor(id) }));
reset();
const accepted = submit(specs);
assert.equal(accepted.isError, false, accepted.message);
assert.equal(calls.queue.length, 1);
assert.deepEqual(calls.validations.map(value => value.target.instanceId), ids);
assert.deepEqual(Array.from(calls.queue[0].tasks, value => value.spec.target.instanceId), ids, 'Hub must retain all four explicitly chosen accounts');
assert.ok(calls.validations.every(value => value.projectId === 'hub-project'));
assert.equal(calls.authority, 1);
reset();
assert.equal(submit([{ ...specs[1], projectId: 'chosen-project' }]).isError, false);
assert.equal(calls.validations[0].projectId, 'chosen-project');
assert.equal(calls.discovery[0].cwd, '/synthetic/chosen-project');

for (const test of [
  { name: 'missing account', target: { ...targetFor(ids[1]), instanceId: 'missing_account' } },
  { name: 'implicit legacy account', target: { provider: 'codex', model: 'chatgpt/gpt-6-astra' } },
  { name: 'wrong provider for account', target: { ...targetFor(ids[1]), instanceId: ids[3] } },
  { name: 'unprepared Kilo model', target: { ...targetFor(ids[1]), model: 'vendor/not-prepared' } },
  { name: 'contradictory saved Claude effort', target: { ...targetFor(ids[3]), options: { effort: 'low' } } },
  { name: 'unsupported native Codex effort', target: { ...targetFor(ids[1]), options: { reasoningEffort: 'ultra' } } },
  ...ids.map(instanceId => ({ name: `disabled ${instanceId}`, target: targetFor(instanceId), change: { enabled: false } })),
  { name: 'unauthenticated Claude Normal', target: targetFor(ids[2]), change: { authStatus: 'unauthenticated', message: 'Sign in to selected account' } },
  { name: 'unavailable Codex Normal', target: targetFor(ids[0]), change: { available: false } },
]) {
  reset();
  const prior = context.availabilities.get(test.target.instanceId);
  if (test.change) context.availabilities.set(test.target.instanceId, { ...prior, ...test.change });
  const validFirst = specs.find(value => value.target.instanceId !== test.target.instanceId);
  const result = submit([validFirst, { prompt: 'Invalid second task', target: test.target }]);
  assert.equal(result.isError, true, test.name);
  assert.equal(calls.queue.length, 0, `${test.name}: no durable Hub records or workers may be created`);
  assert.equal(calls.git.length, 0, `${test.name}: preflight precedes worktree inspection for the whole batch`);
  assert.equal(calls.validations.length, 2, `${test.name}: valid first task then invalid second must both reach the real resolver`);
  if (test.change) context.availabilities.set(test.target.instanceId, prior);
}
reset();
assert.equal(submit(specs, () => fail(new Error('Caller turn inactive'))).isError, true);
assert.equal(calls.validations.length, 0, 'Authority must be checked before account discovery');
assert.equal(calls.queue.length, 0);
reset();
principalKind = 'worker';
assert.equal(submit(specs), null);
assert.equal(calls.validations.length, 0, 'Standalone route remains handled by its original preflight');
assert.equal(calls.queue.length, 0);
principalKind = 'coordinator';
groupsEnabled = false;
assert.equal(submit(specs), null);
assert.equal(calls.validations.length, 0);
groupsEnabled = true;

// Prove this fixture reproduces the original gap: the same invalid second
// target is accepted by the exact unpatched official submit closure.
vm.runInContext(`${fixture.originalHubSubmit.replace('const submit =', 'const originalSubmit =')}
globalThis.submit = originalSubmit;`, context);
reset();
assert.equal(submit([specs[1], { prompt: 'Invalid second task', target: { ...targetFor(ids[3]), options: { effort: 'low' } } }]).isError, false);
assert.equal(calls.queue.length, 1);
assert.equal(calls.queue[0].tasks.length, 2);
assert.equal(calls.validations.length, 0);
console.log('Synara Hub: four explicit accounts and full-batch account/auth/model/effort preflight before queue; original bypass reproduced');

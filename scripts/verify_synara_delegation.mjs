import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const fixture = JSON.parse(await readFile(process.argv[2], 'utf8'));
// Execute the actual private compatibility functions, with synchronous Effect
// values and synthetic native discovery. No app, credential or network access.
const succeed = value => ({ value, *[Symbol.iterator]() { return yield this; }, pipe(...ops) { return ops.reduce((result, op) => op(result), this); } });
const fail = error => ({ error, *[Symbol.iterator]() { return yield this; }, pipe(...ops) { return ops.reduce((result, op) => op(result), this); } });
const run = effect => { if (effect.error) throw effect.error; return effect.value; };
const Effect = {
  succeed, fail,
  gen(fn) { try { const generator = fn(); let step = generator.next(); while (!step.done) step = generator.next(run(step.value)); return succeed(step.value); } catch (error) { return fail(error); } },
  map: fn => effect => { try { return effect.error ? effect : succeed(fn(effect.value)); } catch (error) { return fail(error); } },
  catch: fn => effect => effect.error ? fn(effect.error) : effect,
  forEach: (values, fn) => { try { return succeed(values.map(value => run(fn(value)))); } catch (error) { return fail(error); } },
};
const context = vm.createContext({ Effect, console,
  deriveProviderInstances: settings => Object.entries(settings.providerInstances).map(([instanceId, value]) => ({ ...value, instanceId, isDefault: instanceId === value.driver })),
  providerDefaultModel: provider => `${provider}-native-default`,
  AGENT_GATEWAY_TARGET_OPTIONS_DESCRIPTION: 'synthetic contract',
  PROVIDER_KINDS$1: ['codex', 'claudeAgent'],
  readRecordArg: (args, name) => args[name],
  readStringArg: (args, name) => args[name],
  parseProviderKind: value => value,
  getClaudeContextWindowSuffix: () => null,
  stripClaudeContextWindowSuffix: value => value,
  providerTargetOptionRules: provider => [{ key: provider === 'claudeAgent' ? 'effort' : 'reasoningEffort', valueType: 'string', allowedValues: ['low', 'medium', 'high'], allowedValuesSource: 'provider' }],
  providerPrimaryOptionKey: provider => provider === 'claudeAgent' ? 'effort' : 'reasoningEffort',
  providerOptionRuleSpec: () => ({ advertised: true }),
  convertDiscoveredOptionValue: value => value,
  validateOptionsWithoutCatalog: () => {},
  validateAdvertisedOption: (target, descriptor) => { if (target.options?.reasoningEffort && descriptor.supportedReasoningEfforts?.length && !descriptor.supportedReasoningEfforts.some(value => value.value === target.options.reasoningEffort)) throw Error('unsupported effort'); },
});
vm.runInContext(`class AgentGatewayTargetError extends Error { constructor(code, message) { super(message); this.code = code; } }
${fixture.helpers.replace('__KILO_SYNARA_GATEWAY_MODELS__', JSON.stringify(fixture.models))}
${fixture.catalog}
${fixture.resolve}
${fixture.schema}
${fixture.decode}
${fixture.modelRules}
globalThis.gateway = { kiloSynaraAccountAvailabilities, kiloSynaraGatewayCatalogs, resolveAgentGatewayTarget, readModelSelectionArg, modelTargetOptionRules, schema: MODEL_SELECTION_INPUT_SCHEMA };`, context);
const { gateway } = context;
const ids = ['kilo_codex_normal', 'kilo_codex_proxy', 'kilo_claude_normal', 'kilo_claude_proxy'];
const settings = { providers: { codex: { enabled: true }, claudeAgent: { enabled: true } }, providerInstances: {} };
for (const id of [...ids, 'codex', 'claudeAgent']) {
  const driver = id.includes('claude') ? 'claudeAgent' : 'codex';
  settings.providerInstances[id] = { driver, enabled: ids.includes(id), displayName: id, config: { customModels: id.endsWith('_proxy') ? ['chatgpt/gpt-6-astra', 'anthropic/claude-opus-5', 'vendor/not-prepared'] : [] } };
}
const statuses = Object.entries(settings.providerInstances).map(([instanceId, value]) => ({ instanceId, driver: value.driver, available: true, authStatus: 'authenticated' }));
const discoveryCalls = [];
const discovery = { listModels(input) { discoveryCalls.push(input); return succeed({ models: [{ slug: `${input.provider}-native-model`, name: 'Native model', supportedReasoningEfforts: [{ value: 'low' }, { value: 'high' }] }], source: 'synthetic-native' }); } };
const availabilities = gateway.kiloSynaraAccountAvailabilities(settings, statuses);
const catalogs = run(gateway.kiloSynaraGatewayCatalogs(availabilities, discovery, '/synthetic/project'));
assert.deepEqual(Array.from(catalogs, value => value.instanceId).sort(), ids.sort());
assert.equal(discoveryCalls.length, 4);
assert.ok(discoveryCalls.every(value => ids.includes(value.instanceId)));
for (const catalog of catalogs) {
  if (catalog.instanceId.endsWith('_normal')) {
    assert.deepEqual(Array.from(catalog.models, value => value.slug), [`${catalog.provider}-native-model`]);
    assert.equal(catalog.defaultModel, `${catalog.provider}-native-default`);
  } else {
    assert.deepEqual(Array.from(catalog.models, value => value.slug), ['chatgpt/gpt-6-astra', 'anthropic/claude-opus-5']);
    assert.equal(catalog.defaultModel, fixture.models.defaultModel);
    assert.ok(catalog.models.some(value => value.slug === catalog.defaultModel), 'Managed default must be an exact prepared gateway ID');
  }
}
assert.ok(gateway.schema.properties.instanceId);
const target = { provider: 'codex', instanceId: 'kilo_codex_proxy', model: 'chatgpt/gpt-6-astra', options: { reasoningEffort: 'low' } };
const decoded = gateway.readModelSelectionArg({ target }, 'target');
assert.equal(decoded.instanceId, target.instanceId);
const resolve = value => run(gateway.resolveAgentGatewayTarget({ target: value, availability: availabilities.get(value.instanceId ?? value.provider), discovery, cwd: '/synthetic/project' }));
assert.deepEqual(resolve(target), target, 'Astra Low must retain exact Kilo account/model/options');
for (const id of ids) {
  const provider = id.includes('claude') ? 'claudeAgent' : 'codex';
  const model = id.endsWith('_normal') ? `${provider}-native-model` : 'chatgpt/gpt-6-astra';
  const selected = resolve({ provider, instanceId: id, model });
  assert.equal(selected.instanceId, id, 'An explicit Normal/Kilo account must not be substituted');
}
assert.throws(() => resolve({ provider: 'codex', model: target.model }), error => error.code === 'account_required');
assert.throws(() => resolve({ provider: 'codex', instanceId: 'codex', model: target.model }), error => error.code === 'account_required', 'The shipped ModelSelection decoder fills missing account with its legacy provider ID');
assert.throws(() => resolve({ ...target, instanceId: 'missing_account' }), error => error.code === 'account_unavailable');
assert.throws(() => resolve({ ...target, instanceId: 'kilo_claude_proxy' }), error => error.code === 'account_provider_mismatch');
assert.throws(() => resolve({ ...target, model: 'vendor/not-prepared' }), error => error.code === 'model_unavailable');
assert.throws(() => resolve({ ...target, options: { reasoningEffort: 'ultra' } }), /unsupported effort/);
const claude = { provider: 'claudeAgent', instanceId: 'kilo_claude_proxy', model: 'anthropic/claude-opus-5', options: { effort: 'high' } };
assert.equal(resolve(claude).instanceId, claude.instanceId);
assert.throws(() => resolve({ ...claude, options: { effort: 'low' } }), error => error.code === 'model_option_unavailable');
assert.throws(() => resolve({ ...claude, model: 'chatgpt/gpt-6-astra' }), error => error.code === 'model_option_unavailable');
const claudeCatalog = catalogs.find(value => value.instanceId === 'kilo_claude_proxy');
for (const descriptor of fixture.models.claudeAgent) {
  const rules = gateway.modelTargetOptionRules('claudeAgent', descriptor);
  assert.deepEqual(Array.from(rules.find(value => value.key === 'effort').allowedValues), descriptor.kiloSavedEffort ? [descriptor.kiloSavedEffort] : [], 'Actual pinned option guidance must advertise only the prepared Claude override');
}
const automatic = claudeCatalog.models.find(value => value.slug === 'chatgpt/gpt-6-astra');
assert.equal(automatic.kiloSavedEffort, undefined);
assert.equal(resolve({ ...claude, model: automatic.slug, options: {} }).model, automatic.slug, 'Automatic Claude must keep its exact managed gateway model');
for (const id of ids) {
  const previous = availabilities.get(id);
  availabilities.set(id, { ...previous, enabled: false });
  const provider = id.includes('claude') ? 'claudeAgent' : 'codex';
  const model = id.endsWith('_normal') ? `${provider}-native-model` : 'chatgpt/gpt-6-astra';
  assert.throws(() => resolve({ provider, instanceId: id, model }), error => error.code === 'provider_unavailable' && error.message.includes('disabled'));
  availabilities.set(id, previous);
}
availabilities.set('kilo_codex_normal', { ...availabilities.get('kilo_codex_normal'), authStatus: 'unauthenticated', message: 'Sign in to the selected Normal account' });
assert.throws(() => resolve({ provider: 'codex', instanceId: 'kilo_codex_normal', model: 'codex-native-model' }), error => error.code === 'provider_unavailable' && error.message.includes('Sign in'));
assert.equal(resolve(target).instanceId, 'kilo_codex_proxy', 'Broken Normal auth must not block Kilo delegation');
console.log('Synara delegation: four explicit accounts, exact Astra Low, saved Claude defaults, disabled/auth/model guards passed');

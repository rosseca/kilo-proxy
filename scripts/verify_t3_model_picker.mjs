// Opt-in acceptance of the unmodified frontend shipped inside T3 Desktop.
// Chromium is headless and disposable. Desktop persistence/bootstrap IPC is
// injected; the packaged frontend, server and native provider drivers are real.
import assert from 'node:assert/strict';
import { mkdir, readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { isAbsolute, join } from 'node:path';
import { chromium } from 'playwright';

export async function verifyT3ModelPicker(input) {
  const { fixture, base, token, credential, config, projectId, dispatch, creation,
    snapshot, awaitReply, stopSession } = input;
  const document = JSON.parse(await readFile(fixture.clientSettingsPath, 'utf8'));
  const prepared = document.settings ?? document;
  const baseline = structuredClone(prepared);
  delete baseline.providerModelPreferences.kilo_claude_proxy;
  const proxy = config.providers.find(provider => provider.instanceId === 'kilo_claude_proxy');
  const normal = config.providers.find(provider => provider.instanceId === 'kilo_claude_normal');
  const codexProxy = config.providers.find(provider => provider.instanceId === 'kilo_codex_proxy');
  const codexNormal = config.providers.find(provider => provider.instanceId === 'kilo_codex_normal');
  assert.ok(proxy && normal && codexProxy && codexNormal);
  for (const [provider, expectedName] of [[proxy, 'Kilo Proxy · Claude'], [codexProxy, 'Kilo Proxy · Codex']]) {
    assert.equal(provider.displayName, expectedName, 'Kilo provider name does not produce its native KP badge');
    assert.equal(provider.accentColor, '#327653', 'Kilo provider lost its brand accent');
  }
  assert.equal(normal.displayName, 'Claude · Normal');
  assert.equal(codexNormal.displayName, 'Codex · Normal');
  for (const provider of [normal, codexNormal]) assert.ok(!provider.accentColor, 'Normal acquired a Kilo brand accent');
  const builtin = 'claude-opus-5-5';
  assert.ok(proxy.models.some(model => model.slug === builtin), 'Baseline catalog did not contain the old built-in');
  const v2 = fixture.protocolV2 === true;
  const threadId = randomUUID();
  const initialSelection = { instanceId: proxy.instanceId, model: fixture.defaultModel };
  await dispatch({ type: 'thread.create', ...creation, threadId, projectId,
    title: 'Synthetic stale builtin picker acceptance', modelSelection: initialSelection,
    runtimeMode: 'approval-required', interactionMode: 'default', branch: null, worktreePath: null });
  if (v2) await dispatch({ type: 'message.dispatch', ...creation, threadId, messageId: randomUUID(),
    text: 'Reply with the synthetic fixture response before restarting this old chat.', attachments: [],
    modelSelection: initialSelection, dispatchMode: { type: 'start_immediately' } });
  else await dispatch({ type: 'thread.turn.start', threadId,
    message: { messageId: randomUUID(), role: 'user', text: 'Reply with the synthetic fixture response before restarting this old chat.', attachments: [] },
    modelSelection: initialSelection, runtimeMode: 'approval-required', interactionMode: 'default' });
  await awaitReply(threadId, 0);
  await stopSession(threadId);
  await dispatch({ type: v2 ? 'thread.model-selection.set' : 'thread.meta.update', threadId,
    modelSelection: { instanceId: proxy.instanceId, model: builtin } });
  assert.equal((await snapshot(threadId)).modelSelection.model, builtin,
    'Fixture did not persist its old built-in selection');
  const browser = await chromium.launch({ headless: true });
  let page;
  const browserErrors = [];
  const capture = async (name, element) => {
    if (!fixture.artifactsDirectory) return;
    assert.ok(isAbsolute(fixture.artifactsDirectory), 'T3 screenshot directory must be absolute');
    const directory = join(fixture.artifactsDirectory, fixture.version);
    await mkdir(directory, { recursive: true, mode: 0o700 });
    const path = join(directory, `${name}.png`);
    if (element) await element.screenshot({ path, animations: 'disabled' });
    else await page.screenshot({ path, fullPage: true, animations: 'disabled' });
    console.log(`Packaged T3 frontend screenshot: ${path}`);
  };
  const assertKiloBadge = async (scope, location) => {
    const badge = scope.getByText('KP', { exact: true });
    assert.equal(await badge.count(), 1, `${location} did not show a single native KP badge`);
    assert.ok(await badge.isVisible(), `${location} KP badge was hidden`);
    assert.ok(await scope.locator('[data-provider-accent-color="#327653"]').count() > 0,
      `${location} did not render the Kilo accent on its icon`);
    const style = await badge.evaluate(element => ({ background: getComputedStyle(element).backgroundColor,
      color: getComputedStyle(element).color }));
    assert.equal(style.background, 'rgb(50, 118, 83)', `${location} KP badge did not use Kilo green`);
    assert.equal(style.color, 'rgb(255, 255, 255)', `${location} KP badge did not use white text`);
  };
  const assertProviderRail = async content => {
    for (const provider of [proxy, codexProxy]) {
      const button = content.getByRole('button', { name: provider.displayName, exact: true });
      assert.equal(await button.count(), 1, `Missing distinct proxy rail option ${provider.displayName}`);
      assert.equal(await button.getAttribute('data-provider-accent-color'), '#327653');
      await assertKiloBadge(button, `${provider.displayName} picker rail`);
    }
    for (const provider of [normal, codexNormal]) {
      const button = content.getByRole('button', { name: provider.displayName, exact: true });
      assert.equal(await button.count(), 1, `Missing normal rail option ${provider.displayName}`);
      assert.equal(await button.getByText('KP', { exact: true }).count(), 0, `${provider.displayName} acquired a KP badge`);
      assert.equal(await button.getAttribute('data-provider-accent-color'), null,
        `${provider.displayName} acquired the Kilo accent`);
    }
  };
  const contextFor = async (settings, storageState) => {
    const context = await browser.newContext({ viewport: { width: 1500, height: 1000 }, deviceScaleFactor: 2,
      ...(storageState ? { storageState } : {}) });
    await context.addInitScript(({ settings, base, token, credential }) => {
      let currentSettings = structuredClone(settings);
      const noSubscription = () => () => {};
      window.desktopBridge = new Proxy({
        getAppBranding: () => null, getClientPlatform: () => 'darwin', getSystemLocale: () => 'en-US',
        getLocalEnvironmentBootstraps: () => [{ id: 'primary', label: 'Local environment',
          httpBaseUrl: base, wsBaseUrl: base.replace('http:', 'ws:'), bootstrapToken: credential }],
        getLocalEnvironmentBearerToken: async () => token,
        getLocalEnvironmentEnabled: () => true,
        getClientSettings: async () => structuredClone(currentSettings),
        setClientSettings: async value => { currentSettings = structuredClone(value); },
        getConnectionCatalog: async () => null,
        getWindowState: async () => ({ isMaximized: false, isFullScreen: false }),
        setTheme: async () => {},
      }, { get: (object, key) => key in object ? object[key] :
        typeof key === 'string' && key.startsWith('on') ? noSubscription : undefined });
    }, { settings, base, token, credential });
    page = await context.newPage();
    page.on('pageerror', error => browserErrors.push(error.message));
    await page.goto(`${base}/#/`, { waitUntil: 'domcontentloaded' });
    // The backend descriptor supplies the actual environment ID. Let T3's
    // official sidebar produce its route rather than inventing that ID.
    await page.locator('[data-thread-item] [role="button"]').filter({ hasText: 'Synthetic stale builtin picker acceptance' }).first().click();
    await page.locator('[data-chat-provider-model-picker]').first().waitFor({ timeout: 30000 });
    return context;
  };
  const openPicker = async instance => {
    await page.locator('[data-chat-provider-model-picker]').first().click();
    const content = page.locator('[data-model-picker-content]');
    await content.waitFor();
    await content.getByRole('button', { name: instance.displayName, exact: true }).click();
    return content;
  };
  const expandLegacy = async content => {
    const legacy = content.getByRole('option').filter({ hasText: /^Legacy models/ });
    if (await legacy.count() && await legacy.getAttribute('aria-expanded') !== 'true') await legacy.click();
  };
  const labelFor = (provider, slug) => {
    const model = provider.models.find(model => model.slug === slug || model.id === slug);
    assert.ok(model, `No frontend catalog model for ${slug}`);
    return model.shortName ?? model.name ?? slug;
  };
  try {
    const before = await contextFor(baseline);
    let content = await openPicker(proxy);
    await expandLegacy(content);
    const beforeRows = await content.getByRole('option').allTextContents();
    console.log(`Picker baseline ${fixture.version}: ${JSON.stringify(beforeRows)}`);
    assert.ok(beforeRows.some(text => /^Claude Opus 5\.5/.test(text)), 'Baseline did not reproduce the built-in 5.5 picker row');
    await page.keyboard.press('Escape');
    const beforeSelection = await page.locator('[data-chat-provider-model-picker-label]').first().innerText();
    assert.ok(/^Claude Opus 5\.5/.test(beforeSelection), `Baseline changed stale built-in unexpectedly: ${beforeSelection}`);
    // Let the official UI write a draft carrying an explicit stale selection.
    // Reuse that exact persisted payload after reopening with prepared settings.
    await page.getByRole('button', { name: 'New thread', exact: true }).click();
    await page.locator('[data-chat-provider-model-picker]').first().waitFor();
    content = await openPicker(proxy);
    await expandLegacy(content);
    await content.getByRole('option').filter({ hasText: /^Claude Opus 5\.5/ }).click();
    await page.locator('[contenteditable="true"]').first().fill('Synthetic stale builtin draft');
    const draftURL = page.url();
    assert.ok(new URL(draftURL).hash.startsWith('#/draft/'), `UI did not create a draft: ${draftURL}`);
    await page.waitForFunction(({ builtin, draftId }) => {
      const value = localStorage.getItem('t3code:composer-drafts:v1');
      return value?.includes(builtin) && value.includes(draftId) && value.includes('Synthetic stale builtin draft');
    }, { builtin, draftId: new URL(draftURL).hash.split('/').at(-1) }, { timeout: 5000 });
    const saved = await before.storageState();
    assert.ok(saved.origins.some(origin => origin.localStorage.some(item =>
      item.name === 't3code:composer-drafts:v1' && item.value.includes(builtin))),
    'Official UI did not persist the stale built-in draft');
    await before.close();

    const after = await contextFor(prepared, saved);
    content = await openPicker(proxy);
    const proxyRows = await content.getByRole('option').allTextContents();
    console.log(`Picker prepared ${fixture.version}: ${JSON.stringify(proxyRows)}`);
    assert.equal(proxyRows.length, fixture.libraryModels.length, 'Kilo picker contained extra or missing model rows');
    const modelLabels = fixture.libraryModels.map(model => labelFor(proxy, model));
    for (const name of modelLabels) assert.ok(proxyRows.some(text => text.includes(name)), `Missing prepared model in picker: ${name}`);
    assert.ok(proxyRows.every(text => modelLabels.some(name => text.includes(name))), 'Kilo picker exposed a built-in model outside Models');
    assert.ok(proxyRows[0].includes(labelFor(proxy, fixture.defaultModel)), 'Kilo picker did not put the configured shared default first');
    await page.keyboard.press('Escape');
    const afterSelection = await page.locator('[data-chat-provider-model-picker-label]').first().innerText();
    assert.ok(afterSelection.includes(labelFor(proxy, fixture.defaultModel)), `Old built-in did not resolve to prepared default: ${afterSelection}`);
    await assertKiloBadge(page.locator('[data-chat-provider-model-picker]').first(), 'Claude composer');
    await capture('claude-composer');
    await capture('claude-composer-badge', page.locator('[data-chat-provider-model-picker]').first());
    // Stable correctly locks a chat to its existing provider. Check Normal's
    // independent model list in the owned draft, where switching is supported.
    await page.goto(draftURL, { waitUntil: 'domcontentloaded' });
    await page.locator('[data-chat-provider-model-picker-label]').first().waitFor();
    const resolvedDraft = await page.locator('[data-chat-provider-model-picker-label]').first().innerText();
    assert.ok(resolvedDraft.includes(labelFor(proxy, fixture.defaultModel)),
      `Persisted draft did not resolve the hidden built-in to prepared default: ${resolvedDraft}`);
    content = await openPicker(normal);
    await expandLegacy(content);
    const normalRows = await content.getByRole('option').allTextContents();
    console.log(`Picker normal ${fixture.version}: ${JSON.stringify(normalRows)}`);
    assert.ok(normalRows.some(text => /^Claude Opus 5\.5/.test(text)), 'Normal Claude lost its built-in 5.5 model');
    const normalModels = normal.models.filter(model => !model.isCustom);
    assert.equal(normalModels.length, 12, 'Unexpected built-in contract in pinned T3 version');
    assert.equal(normalRows.filter(text => !text.startsWith('Legacy models')).length, 12, 'Normal Claude lost built-in picker rows');
    await assertProviderRail(content);
    await capture('four-provider-picker');
    await capture('four-provider-picker-detail', content);
    await page.keyboard.press('Escape');
    await page.locator('[data-thread-item] [role="button"]').filter({ hasText: 'Synthetic stale builtin picker acceptance' }).first().click();
    await page.locator('[data-chat-provider-model-picker-label]').first().waitFor();

    for (let turn = 0; turn < 3; turn++) {
      const editor = page.locator('[contenteditable="true"]').first();
      await editor.fill(`Reply with the synthetic fixture response. UI turn ${turn + 1}.`);
      await page.getByRole('button', { name: /^(Submit|Send) message$/ }).click();
      const replied = await awaitReply(threadId, turn + 1);
      assert.equal(replied.modelSelection.instanceId, proxy.instanceId);
      assert.equal(replied.modelSelection.model, fixture.defaultModel,
        'Frontend sent the stale built-in instead of the exact configured gateway ID');
      if (turn === 1) await stopSession(threadId);
    }
    await stopSession(threadId);
    await page.goto(draftURL, { waitUntil: 'domcontentloaded' });
    await page.locator('[data-chat-provider-model-picker-label]').first().waitFor();
    const reopenedDraft = await page.locator('[data-chat-provider-model-picker-label]').first().innerText();
    assert.ok(reopenedDraft.includes(labelFor(proxy, fixture.defaultModel)),
      `Persisted draft changed after continuing the old chat: ${reopenedDraft}`);
    await page.getByRole('button', { name: /^(Submit|Send) message$/ }).click();
    await page.waitForFunction(() => location.hash.includes('/') && !location.hash.startsWith('#/draft/'));
    const promotedThread = new URL(page.url()).hash.split('/').at(-1);
    const drafted = await awaitReply(promotedThread, 0);
    assert.equal(drafted.modelSelection.instanceId, proxy.instanceId);
    assert.equal(drafted.modelSelection.model, fixture.defaultModel,
      'Frontend sent the old draft built-in instead of the exact configured gateway ID');
    await stopSession(promotedThread);
    // Select Codex in a separate unsent draft, preserving the five real Opus
    // requests already checked by the Go fixture while exercising its badge.
    await page.getByRole('button', { name: 'New thread', exact: true }).click();
    await page.locator('[data-chat-provider-model-picker]').first().waitFor();
    content = await openPicker(codexProxy);
    const codexModel = fixture.instances.find(instance => instance.id === codexProxy.instanceId).model;
    await content.getByRole('option').filter({ hasText: labelFor(codexProxy, codexModel) }).click();
    await assertKiloBadge(page.locator('[data-chat-provider-model-picker]').first(), 'Codex composer');
    await capture('codex-composer-badge', page.locator('[data-chat-provider-model-picker]').first());
    content = await openPicker(codexProxy);
    await assertProviderRail(content);
    await capture('codex-composer-picker');
    await capture('codex-picker-detail', content);
    await page.keyboard.press('Escape');
    await page.goto(`${base}/#/settings/providers`, { waitUntil: 'domcontentloaded' });
    await page.getByText('Kilo Proxy · Claude', { exact: true }).first().waitFor();
    await capture('four-provider-settings');
    await after.close();
    console.log(`Passed packaged T3 branding ${fixture.version}: Claude/Codex native KP badges with computed Kilo green in composer and four-provider picker; both Normal instances remain distinct and unbranded (injected Desktop IPC).`);
    console.log(`Passed packaged T3 frontend ${fixture.version}: baseline built-in reproduced; Kilo Models only, default first; all 12 Normal built-ins preserved; old chat continued for three UI turns with stop/resume and old draft promoted using exact gateway ID (injected Desktop IPC).`);
  } catch (error) {
    if (page && !page.isClosed()) {
      console.error('Frontend acceptance URL:', page.url());
      console.error('Frontend acceptance DOM:', (await page.locator('body').innerText()).slice(-16000));
      console.error('Frontend acceptance buttons:', await page.locator('button').evaluateAll(elements => elements.map(element => ({ name: element.getAttribute('aria-label'), text: element.innerText }))));
    }
    console.error('Frontend acceptance errors:', browserErrors);
    throw error;
  } finally {
    await browser.close();
  }
}

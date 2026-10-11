// Opt-in UI acceptance of the frontend shipped in the installed Synara bundle.
// A disposable Chromium context supplies the actual desktop WebSocket bridge;
// inference, persistence and native provider drivers run in the real backend.
import assert from 'node:assert/strict';
import { mkdir, readFile } from 'node:fs/promises';
import { isAbsolute, join } from 'node:path';
import { chromium } from 'playwright';

export async function verifySynaraPicker(input) {
  const { fixture, base, token, config, rpc, awaitReply, stopSession } = input;
  const proxy = config.providers.find(provider => provider.instanceId === 'kilo_claude_proxy');
  const codex = config.providers.find(provider => provider.instanceId === 'kilo_codex_proxy');
  const normal = config.providers.find(provider => provider.instanceId === 'kilo_claude_normal');
  const codexNormal = config.providers.find(provider => provider.instanceId === 'kilo_codex_normal');
  assert.ok(proxy && codex && normal && codexNormal);
  for (const [provider, name] of [[proxy, 'Kilo Proxy · Claude'], [codex, 'Kilo Proxy · Codex']]) {
    assert.equal(provider.displayName, name);
  }
  const existing = new Set((await rpc('orchestration.getShellSnapshot', {})).threads.map(thread => thread.id));
  // Supply the exact snapshot that Prepare wrote, through the same synchronous
  // bridge method as the native preload. This checks the shipped frontend's
  // import and selection logic; native Electron IPC is a separate smoke check.
  assert.ok(isAbsolute(fixture.composerSeedPath));
  const composerSeed = JSON.parse(await readFile(fixture.composerSeedPath, 'utf8'));
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1500, height: 1000 }, deviceScaleFactor: 2 });
  await context.addInitScript(({ base, token, composerSeed }) => {
    const noSubscription = () => () => {};
    window.desktopBridge = new Proxy({
      getWsUrl: () => `${base.replace('http:', 'ws:')}/?token=${encodeURIComponent(token)}`,
      setTheme: async () => {},
      setWindowMaterial: async () => {},
      getAppIcon: async () => 'beta',
      getZoomFactor: () => 1,
      setMenuShortcuts: async () => {},
      windowControls: { getState: async () => ({ isMaximized: false, isFullScreen: false }),
        onState: noSubscription },
      customTitleBar: { getState: async () => ({ enabled: false, platform: 'darwin' }) },
      storageMigration: { readSnapshot: () => composerSeed, acknowledgeSnapshot: async () => {
        window.synaraFixtureSeedAcknowledged = true;
      } },
      browser: { getState: async ({threadId}) => ({threadId,open:false,tabs:[],activeTabId:null}), onState: noSubscription, onBrowserUseOpenPanelRequest: noSubscription,
        onBrowserCopyLink: noSubscription, annotations: { onEvent: noSubscription } },
      computerPreview: { onFrame: noSubscription },
      appSnap: { getState: async () => ({enabled:false,supported:false,permissions:[]}), onState: noSubscription, onCaptured: noSubscription, onError: noSubscription,
        onPermissionGuideState: noSubscription, listPendingCaptures: async () => [] },
      audioLevel: { onLevel: noSubscription },
    }, { get: (object, key) => key in object ? object[key] :
      typeof key === 'string' && key.startsWith('on') ? noSubscription : undefined });
  }, { base, token, composerSeed });
  const page = await context.newPage();
  const errors = [];
  const consoleErrors = [];
  page.on('console', message => { if(message.type()==='error') consoleErrors.push(message.text()); });
  page.on('pageerror', error => errors.push(error.message));
  const capture = async name => {
    if (!fixture.artifactsDirectory) return;
    assert.ok(isAbsolute(fixture.artifactsDirectory));
    const directory = join(fixture.artifactsDirectory, fixture.version);
    await mkdir(directory, { recursive: true, mode: 0o700 });
    const path = join(directory, `${name}.png`);
    await page.screenshot({ path, fullPage: true, animations: 'disabled' });
    console.log(`Packaged Synara frontend screenshot: ${path}`);
  };
  try {
    await page.goto(`${base}/#/`, { waitUntil: 'domcontentloaded' });
    const deferImport = page.getByRole('button', {name:'Not now',exact:true});
    if (await deferImport.isVisible()) await deferImport.click();
    else await deferImport.waitFor({state:'visible',timeout:3000}).then(() => deferImport.click()).catch(() => {});
    const skipTour = page.getByRole('button', { name: 'Skip tour', exact: true });
    if (await skipTour.isVisible()) await skipTour.click();
    else await skipTour.waitFor({ state: 'visible', timeout: 3000 }).then(() => skipTour.click()).catch(() => {});
    assert.equal(await page.evaluate(() => window.synaraFixtureSeedAcknowledged), true,
      'Shipped frontend did not acknowledge the prepared composer snapshot');
    await page.getByRole('button', { name: 'New thread', exact: true }).click();
    const draftPicker = page.getByRole('button').filter({ hasText: codex.displayName }).first();
    await draftPicker.waitFor({ timeout: 30000 });
    if (await skipTour.isVisible()) await skipTour.click();
    assert.ok((await draftPicker.innerText()).includes(fixture.defaultModel) || (await draftPicker.innerText()).includes(fixture.defaultDisplayName),
      'Shipped frontend did not consume the prepared shared-default composer seed');
    await draftPicker.click();
    const menu = page.getByRole('menu').first();
    await menu.waitFor();
    const sources = menu.getByRole('tablist', { name: 'Model sources', exact: true });
    assert.equal(await sources.getByRole('tab').count(), 5,
      'Picker must offer exactly four accounts and Starred');
    for (const provider of [proxy, codex, normal, codexNormal]) {
      const account = sources.getByRole('tab', { name: provider.displayName, exact: true });
      assert.equal(await account.count(), 1,
        `Picker did not distinguish ${provider.displayName}`);
      assert.notEqual(await account.getAttribute('aria-disabled'), 'true',
        `${provider.displayName} is disabled in the real picker`);
      await account.click();
      assert.equal(await account.getAttribute('aria-selected'), 'true',
        `Picker did not select ${provider.displayName}`);
    }
    assert.equal(await sources.getByRole('tab', { name: 'Codex', exact: true }).count(), 0,
      'Legacy Codex account remains selectable');
    assert.equal(await sources.getByRole('tab', { name: 'Claude', exact: true }).count(), 0,
      'Legacy Claude account remains selectable');
    // Synara renders its supported accent as a coloured dot; its native UI
    // does not implement T3's initial-letter KP badge.
    for (const provider of [proxy, codex]) {
      const accents = await sources.getByRole('tab', {name: provider.displayName, exact:true})
        .locator('[style]').evaluateAll(elements => elements.map(element =>
          getComputedStyle(element).backgroundColor));
      assert.ok(accents.includes('rgb(50, 118, 83)'), `${provider.displayName} green indicator was not rendered`);
    }
    await sources.getByRole('tab', { name: proxy.displayName, exact:true }).click();
    await capture('four-agent-picker');
    const names = [fixture.defaultModel, fixture.defaultDisplayName]
      .map(name => name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
    const exactModel = menu.getByRole('menuitem').filter({hasText: new RegExp(names.join('|'))});
    assert.equal(await exactModel.count(), 1, 'Claude picker did not offer the prepared shared default');
    await exactModel.click();
    const editor = page.locator('[contenteditable="true"]').first();
    await editor.fill('Reply with the synthetic fixture response from the exact shared default.');
    const submit = page.getByRole('button', { name: /^(Submit|Send) message$/ }).first();
    await submit.click();
    let threadId;
    for (let attempt = 0; attempt < 100; attempt++) {
      const shell = await rpc('orchestration.getShellSnapshot', {});
      const created = shell.threads.filter(thread => !existing.has(thread.id));
      if (created.length > 0) {
        assert.equal(created.length, 1, 'UI created more than one acceptance conversation');
        threadId = created[0].id;
        break;
      }
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    assert.ok(threadId, 'Synara UI did not persist its new conversation');
    const replied = await awaitReply(threadId, 0);
    assert.equal(replied.modelSelection.instanceId, proxy.instanceId);
    assert.equal(replied.modelSelection.model, fixture.defaultModel);
    await stopSession(threadId);
    await capture('claude-shared-default');
    assert.deepEqual(errors, [], 'Installed Synara frontend raised browser errors');
    console.log('Installed Synara model picker acceptance passed: four named agents, Kilo green indicator, exact shared default UI turn');
  } catch (error) {
    await capture('picker-failure');
    console.error(`Synara visible controls: ${JSON.stringify(await page.getByRole('button').allTextContents())}`);
    console.error(`Synara frontend errors: ${JSON.stringify(errors)}, console: ${JSON.stringify(consoleErrors).slice(0,4000)}`);
    console.error(`Synara visible fixture text: ${(await page.locator('body').innerText()).slice(-5000)}`);
    throw error;
  } finally {
    await context.close();
    await browser.close();
  }
}

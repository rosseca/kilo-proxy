import {test, expect, startProxy} from './fixture.mjs';
import {readFile, writeFile, stat} from 'node:fs/promises';
import {createServer} from 'node:net';
import path from 'node:path';

const first = 'vendor/one', second = 'anthropic/claude-sonnet-4.6';
const choose = (page, id) => page.locator('[data-open-design-id="'+id+'"]');
const headers = gateway => ({Authorization:'Bearer '+gateway.token});
async function freePort() {
 const server = createServer();
 await new Promise((resolve, reject) => {server.once('error', reject); server.listen(0, '127.0.0.1', resolve);});
 const port = server.address().port;
 await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
 return port;
}
async function readAPI(request, gateway, endpoint) {
 const response = await request.get(new URL('/api/'+endpoint, gateway.url).href, {headers:headers(gateway)});
 expect(response.ok()).toBe(true); return response.json();
}
async function records(gateway) {
 try {return JSON.parse(await readFile(gateway.launchRecords, 'utf8'));}
 catch (error) {if (error.code === 'ENOENT') return []; throw error;}
}
async function seedLibrary(request, gateway) {
 const current = await readAPI(request, gateway, 'model-library');
 const library = {schemaVersion:1, defaultModel:first, models:[{id:first, displayName:'My saved model', contextWindow:64000, maxOutputTokens:4000, reasoningEffort:'high', reasoningLevels:['low', 'high'], reasoningCustom:true}]};
 const response = await request.put(new URL('/api/model-library', gateway.url).href, {headers:headers(gateway), data:{library, revision:current.revision}});
 expect(response.ok()).toBe(true); return library;
}

for (const engine of ['codex-cli', 'claude', 'opencode']) test(`Open Design prepares ${engine} and launches with models without changing the shared library`, async ({page, gateway, request}, testInfo) => {
 await startProxy(page, gateway);
 await page.locator('#start-stop').click();
 await expect(page.locator('#status-label')).toHaveText('Proxy stopped');
 const shared = await seedLibrary(request, gateway);
 await page.locator('#tab-open-design').click();
 await expect(choose(page, first)).toBeChecked();
 await page.locator('#open-design-engine').selectOption(engine);
 await choose(page, second).check();
 await page.locator('[data-open-design-initial="'+second+'"]').click();
 await expect(page.locator('#client-launch-directory-field')).toBeHidden();
 await expect(page.locator('#open-design-engine-status')).toContainText('without opening a terminal');
 await expect(page.locator('#open-design-next')).toContainText('Models & providers → Local CLI');
 await expect(page.locator('#open-design-key')).toHaveCount(0);
 const mutations = [];
 page.on('request', req => {if (req.method() === 'POST') mutations.push({endpoint:new URL(req.url()).pathname, body:req.postDataJSON()});});
 await expect(page.locator('#client-launch')).toBeEnabled();
 await page.locator('#client-launch').click();
 await expect.poll(async () => (await records(gateway)).length).toBe(1);
 await expect(page.locator('#status-label')).toHaveText('Proxy running');
 expect(mutations.map(value => value.endpoint)).toEqual(['/api/open-design/profile', '/api/clients/launch']);
 expect(mutations[0].body).toMatchObject({engine, library:{schemaVersion:1, defaultModel:second}});
 expect(mutations[0].body.library.models[0]).toEqual(shared.models[0]);
 expect(mutations[1].body).toEqual({client:'open-design', engine, directory:''});
 expect((await records(gateway))[0]).toMatchObject({client:'open-design', kind:'desktop'});
 expect((await readAPI(request, gateway, 'model-library')).library).toEqual(shared);
 const profile = await readAPI(request, gateway, 'open-design/profile');
 expect(profile.prepared).toBe(true); expect(profile.engine).toBe(engine);
 const prefs = JSON.parse(await readFile(profile.configPath, 'utf8'));
 const runtime = engine === 'codex-cli' ? 'codex' : engine;
 expect(prefs.agentId).toBe(runtime);
 expect(prefs.agentModels[runtime].model).toBe('default');
 if (engine === 'opencode') {
  const shim = prefs.agentCliEnv.opencode.OPENCODE_BIN;
  expect(shim.startsWith(profile.profileDir + path.sep)).toBe(true);
  const info = await stat(shim);
  expect(info.isFile()).toBe(true); expect(info.size).toBeGreaterThan(0);
 }
 const selection = JSON.parse(await readFile(path.join(profile.profileDir, 'selection.json'), 'utf8'));
 expect(selection.library.defaultModel).toBe(second);
 await page.locator('#client-launch').click();
 await expect.poll(async () => (await records(gateway)).length).toBe(2);
 expect(mutations.filter(value => value.endpoint === '/api/open-design/profile')).toHaveLength(2);
 await page.reload();
 await page.locator('#tab-open-design').click();
 await expect(page.locator('#open-design-engine')).toHaveValue(engine);
 await expect(choose(page, first)).toBeChecked();
 await expect(choose(page, second)).toBeChecked();
 if (engine === 'codex-cli') {
  await page.locator('#language').selectOption('es');
  await expect(page.locator('#open-design-engine-label')).toHaveText('Motor de programación');
  await page.setViewportSize({width:390, height:844});
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.locator('#open-design-helper').screenshot({path:testInfo.outputPath('open-design-local-cli-es-mobile.png')});
 }
});

test('Open Design gates missing credentials and engines, and prepares a changed saved port on launch', async ({page, gateway, request}) => {
 await page.locator('#tab-open-design').click();
 await choose(page, first).check();
 await expect(page.locator('#client-launch')).toBeDisabled();
 await expect(page.locator('#client-launch-status')).toContainText('Connect your Kilo account and team first');
 await startProxy(page, gateway);
 await page.locator('#start-stop').click();
 await expect(page.locator('#status-label')).toHaveText('Proxy stopped');
 await writeFile(gateway.launchControl, JSON.stringify({unavailable:'codex-cli'}));
 await page.locator('#open-design-detect').click();
 await expect(page.locator('#client-launch')).toBeDisabled();
 await expect(page.locator('#open-design-engine-status')).toContainText('Synthetic application unavailable');
 await page.locator('#open-design-engine').selectOption('claude');
 await expect(page.locator('#client-launch')).toBeEnabled();
 const port = await freePort();
 const saved = await request.post(new URL('/api/config', gateway.url).href, {headers:headers(gateway), data:{apiKey:'synthetic-replacement-personal-key', orgId:'synthetic-replacement-team', port, remember:true}});
 expect(saved.ok()).toBe(true);
 await expect(page.locator('#base-url')).toHaveText('http://127.0.0.1:'+port+'/v1');
 await page.locator('#open-design-detect').click();
 await page.locator('#client-launch').click();
 await expect.poll(async () => (await records(gateway)).length).toBe(1);
 const profile = await readAPI(request, gateway, 'open-design/profile');
 const prefs = JSON.parse(await readFile(profile.configPath, 'utf8'));
 expect(prefs.agentCliEnv.claude.ANTHROPIC_BASE_URL).toBe('http://127.0.0.1:'+port);
});

test('Open Design refuses changed settings while its managed instance runs and allows a fresh retry', async ({page, gateway}) => {
 await startProxy(page, gateway);
 await page.locator('#tab-open-design').click();
 await choose(page, first).check();
 await page.locator('#client-launch').click();
 await expect.poll(async () => (await records(gateway)).length).toBe(1);
 await writeFile(gateway.launchControl, JSON.stringify({openDesignRunning:true}));
 await choose(page, second).check();
 await page.locator('#client-launch').click();
 await expect(page.locator('#client-launch-status')).toContainText('Quit the Open Design Kilo instance');
 expect((await records(gateway)).length).toBe(1);
 await expect(page.locator('#client-launch')).toBeEnabled();
 await writeFile(gateway.launchControl, JSON.stringify({openDesignIdle:true}));
 await page.locator('#client-launch').click();
 await expect.poll(async () => (await records(gateway)).length).toBe(2);
 await expect(page.locator('#client-launch-status')).not.toHaveClass(/error/);
});

import {test, expect, startProxy, copied, state} from './fixture.mjs';
import {readFile, writeFile} from 'node:fs/promises';

const choose = (page, id) => page.locator('[data-open-design-id="'+id+'"]');
async function records(gateway) {
 try {return JSON.parse(await readFile(gateway.launchRecords, 'utf8'));}
 catch (error) {if (error.code === 'ENOENT') return []; throw error;}
}

test('Open Design copies individual BYOK fields and exact selected models without exposing credentials', async ({page, gateway, request}, testInfo) => {
 await startProxy(page, gateway);
 await page.locator('#tab-open-design').click();
 await expect(page.locator('#client-launch')).toBeDisabled();
 await choose(page, 'vendor/one').check();
 await page.locator('#open-design-manual-title').click();
 await page.locator('#open-design-id').fill('vendor/two');
 await page.locator('#open-design-add').click();
 await page.locator('[data-open-design-initial="vendor/two"]').click();
 const saved = await state(request, gateway);
 expect(await copied(page, '#open-design-copy-url')).toBe(saved.baseURL);
 expect(await copied(page, '#open-design-copy-key')).toBe(saved.localKey);
 expect(await copied(page, '#open-design-copy-model')).toBe('vendor/two');
 expect(await copied(page, '#open-design-copy-models')).toBe('vendor/one\nvendor/two');
 await expect(page.locator('#open-design-key')).toHaveValue('kl_local_••••••••••••••••');
 expect(await page.locator('#open-design-helper').evaluate(node => node.textContent + Array.from(node.querySelectorAll('input')).map(input => input.value).join('\n'))).not.toContain(saved.localKey);
 await expect(page.locator('#client-launch-directory-field')).toBeHidden();
 await expect(page.locator('#open-design-step-provider')).toContainText('OpenAI. Then select Provider preset → Custom provider');
 await expect(page.locator('#open-design-step-model')).toContainText('Custom model id');
 await expect(page.locator('#open-design-capabilities')).toContainText('cannot read, write or edit project files');
 await expect(page.locator('#client-launch-help')).toContainText('does not save its provider settings');
 await expect(page.locator('#open-design-install')).toHaveAttribute('href', 'https://github.com/nexu-io/open-design/releases/latest');
 await page.locator('#language').selectOption('es');
 await expect(page.locator('#open-design-copy-key')).toHaveText('Copiar clave local');
 await page.setViewportSize({width:390, height:844});
 expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
 await expect(page.locator('#toast')).toBeHidden();
 await page.locator('#open-design-helper').screenshot({path:testInfo.outputPath('open-design-es-mobile.png')});
});

test('Open Design launch starts a stopped proxy without preparing a managed profile', async ({page, gateway}) => {
 await startProxy(page, gateway);
 await page.locator('#start-stop').click();
 await expect(page.locator('#status-label')).toHaveText('Proxy stopped');
 await page.locator('#tab-open-design').click();
 await choose(page, 'vendor/one').check();
 const mutations = [];
 page.on('request', request => {if (request.method() === 'POST') mutations.push(new URL(request.url()).pathname);});
 await expect(page.locator('#client-launch')).toBeEnabled();
 await page.locator('#client-launch').click();
 await expect.poll(async () => (await records(gateway)).length).toBe(1);
 expect((await records(gateway))[0]).toMatchObject({client:'open-design', executable:'/synthetic/open-design', kind:'desktop'});
 await expect(page.locator('#status-label')).toHaveText('Proxy running');
 expect(mutations).toEqual(['/api/clients/launch']);
});

test('Open Design keeps launch disabled until signed in and updates copied URL after a saved port change', async ({page, gateway, request}) => {
 await page.locator('#tab-open-design').click();
 await choose(page, 'vendor/one').check();
 await expect(page.locator('#client-launch')).toBeDisabled();
 await expect(page.locator('#client-launch-status')).toContainText('Connect your Kilo account and team first');
 expect(await records(gateway)).toEqual([]);
 await startProxy(page, gateway);
 await page.locator('#start-stop').click();
 await expect(page.locator('#status-label')).toHaveText('Proxy stopped');
 const port = gateway.proxyPort === 9988 ? 9989 : 9988;
 const saved = await request.post(new URL('/api/config', gateway.url).href, {headers:{Authorization:'Bearer '+gateway.token}, data:{apiKey:'synthetic-replacement-personal-key', orgId:'synthetic-replacement-team', port, remember:true}});
 expect(saved.ok()).toBe(true);
 await expect(page.locator('#open-design-url')).toHaveValue('http://127.0.0.1:'+port+'/v1');
 expect(await copied(page, '#open-design-copy-url')).toBe('http://127.0.0.1:'+port+'/v1');
 await writeFile(gateway.launchControl, JSON.stringify({unavailable:'open-design'}));
 await page.locator('#client-launch-refresh').click();
 await expect(page.locator('#client-launch')).toBeDisabled();
 await expect(page.locator('#client-launch-status')).toContainText('Synthetic application unavailable');
});

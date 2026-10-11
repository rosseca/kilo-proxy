import {test, expect, startProxy} from './fixture.mjs';
import {readFile, writeFile} from 'node:fs/promises';

const library=()=>({schemaVersion:1,defaultModel:'anthropic/claude-sonnet-4.6',models:[
 {id:'vendor/one',displayName:'My coding model',contextPreset:'custom',contextWindow:128000,reasoningEffort:'high',reasoningCustom:true,reasoningLevels:['low','high']},
 {id:'anthropic/claude-sonnet-4.6',displayName:'My default model',contextPreset:'custom',contextWindow:128000,reasoningCustom:true,reasoningLevels:[]},
]});
const headers=gateway=>({Authorization:'Bearer '+gateway.token});
async function getAPI(request,gateway,path) {
 const response=await request.get(new URL('/api/'+path,gateway.url).href,{headers:headers(gateway)});
 expect(response.ok(),await response.text()).toBe(true);return response.json();
}
async function saveLibrary(request,gateway,value) {
 const current=await getAPI(request,gateway,'model-library');
 const response=await request.put(new URL('/api/model-library',gateway.url).href,{headers:headers(gateway),data:{revision:current.revision,library:value}});
 expect(response.ok(),await response.text()).toBe(true);
}
async function records(gateway) {try{return JSON.parse(await readFile(gateway.launchRecords,'utf8'));}catch(error){if(error.code==='ENOENT')return [];throw error;}}

test('Synara prepares four agents before desktop launch and guards a second opening',async({page,gateway,request},testInfo)=>{
 await saveLibrary(request,gateway,library());
 await startProxy(page,gateway);
 await page.locator('#tab-synara').click();
 await expect(page.locator('#synara-summary')).toContainText(library().defaultModel);
 await expect(page.locator('#synara-models li')).toHaveText(['My default model · '+library().defaultModel,'My coding model · vendor/one']);
 await expect(page.locator('#synara-agents li')).toHaveCount(4);
 await expect(page.locator('#synara-agents li')).toHaveText([
  'Codex · Normal · Existing Codex CLI login',
  'Kilo Proxy · Codex · Shared models through the proxy',
  'Claude · Normal · Existing Claude Code login',
  'Kilo Proxy · Claude · Compatible shared models through the proxy',
 ]);
 await expect(page.locator('#synara-intro')).toContainText('Kilo Proxy names and green account indicators');
 await expect(page.locator('#synara-agents')).toContainText('Existing Codex CLI login');
 await expect(page.locator('#synara-agents')).toContainText('Existing Claude Code login');
 await expect(page.locator('#client-launch-directory-field')).toBeHidden();
 await expect(page.locator('#client-launch-custom')).toBeHidden();
 expect(await page.locator('#synara-helper input,#synara-helper select').count()).toBe(0);
 let prepares=0;
 page.on('request',request=>{if(request.method()==='POST'&&new URL(request.url()).pathname==='/api/clients/synara')prepares++;});
 await expect(page.locator('#client-launch')).toBeEnabled();
 await page.locator('#client-launch').click();
 await expect.poll(async()=>(await records(gateway)).length).toBe(1);
 expect(prepares).toBe(1);
 await expect(page.locator('#client-launch')).toBeEnabled();
 await page.locator('#client-launch').click();
 await expect(page.locator('#client-launch-status')).toContainText('before opening it again');
 expect((await records(gateway)).length).toBe(1);
 expect(prepares).toBe(2);
 expect((await records(gateway)).every(record=>record.client==='synara'&&record.kind==='desktop')).toBe(true);
 const profile=await getAPI(request,gateway,'clients/synara');
 expect(profile.prepared).toBe(true);
 expect(profile.version).toBe('1.0.0-beta.1');
 expect(profile.library).toEqual((await getAPI(request,gateway,'model-library')).library);
 await page.locator('#synara-options').click();
 await expect(page.locator('#synara-prepared')).toContainText('Four agents prepared');
 await expect(page.locator('#synara-compatibility')).toContainText('new chat to switch between normal and Kilo');
 await expect(page.locator('#synara-compatibility')).toContainText('exact gateway model ID');
 await expect(page.locator('#synara-compatibility')).toContainText('family and version match one prepared model');
 await expect(page.locator('#synara-compatibility')).toContainText('reasoning defaults come from Kilo Models');
 await expect(page.locator('#synara-compatibility')).not.toContainText('hides');
 await expect(page.locator('#synara-compatibility')).toContainText('check the selected model before continuing');
 await expect(page.locator('#synara-privacy')).toContainText('regular Synara workspace and chats stay separate');
 await expect(page.locator('#synara-requirements')).toContainText('Supports Synara Beta. Requires native Codex CLI and Claude Code.');
 await expect(page.locator('#synara-requirements')).toContainText('Detected: '+profile.version);
 await expect(page.locator('#synara-requirements')).toContainText('not Desktop app logins');
 await expect(page.locator('#synara-install')).toHaveAttribute('href','https://github.com/Emanuele-web04/synara/releases');
 await expect(page.locator('#synara-helper')).not.toContainText('synthetic-kilo-personal-key');
 await expect(page.locator('#synara-helper')).not.toContainText(profile.profileDir);
 await expect(page.locator('#toast')).toBeHidden();
 await page.locator('#synara-helper').screenshot({path:testInfo.outputPath('synara-en-wide.png')});
 await page.locator('#language').selectOption('es');
 await page.setViewportSize({width:390,height:844});
 await expect(page.locator('#synara-agents')).toContainText('Sesión existente de Codex CLI');
 await expect(page.locator('#synara-agents li').nth(1)).toContainText('Kilo Proxy · Codex');
 await expect(page.locator('#synara-agents li').nth(3)).toContainText('Kilo Proxy · Claude');
 await expect(page.locator('#synara-intro')).toContainText('indicadores verdes de cuenta');
 await expect(page.locator('#synara-requirements')).toContainText('Admite Synara Beta. Requiere Codex CLI nativo y Claude Code.');
 await expect(page.locator('#synara-requirements')).toContainText('Detectado: '+profile.version);
 await expect(page.locator('#synara-compatibility')).toContainText('inicia uno nuevo para cambiar entre normal y Kilo');
 await expect(page.locator('#synara-compatibility')).toContainText('revisa el modelo elegido antes de continuar');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
 await page.locator('#synara-helper').screenshot({path:testInfo.outputPath('synara-es-mobile.png')});
 expect(await page.evaluate(()=>window.__copied)).toEqual([]);
});

test('Synara explains missing models, connection and installation without launching',async({page,gateway,request})=>{
 await page.locator('#tab-synara').click();
 await expect(page.locator('#client-launch')).toBeDisabled();
 await expect(page.locator('#client-launch-status')).toContainText('Connect your selected provider first.');
 await startProxy(page,gateway);
 await expect(page.locator('#client-launch-status')).toContainText('Add and save models');
 await saveLibrary(request,gateway,library());
 await writeFile(gateway.launchControl,JSON.stringify({unavailable:'synara'}));
 await page.locator('#client-launch-refresh').click();
 await expect(page.locator('#client-launch')).toBeDisabled();
 await expect(page.locator('#client-launch-status')).toContainText('Synthetic application unavailable');
 await page.locator('#synara-options').click();
 await expect(page.locator('#synara-install')).toBeVisible();
 expect(await records(gateway)).toEqual([]);
 await writeFile(gateway.launchControl,'{}');
 await page.locator('#client-launch-refresh').click();
 await expect(page.locator('#client-launch')).toBeEnabled();
});

test('Synara stops stale preparation and refreshes an externally changed library',async({page,gateway,request})=>{
 await saveLibrary(request,gateway,library());await startProxy(page,gateway);
 await page.locator('#tab-synara').click();await expect(page.locator('#client-launch')).toBeEnabled();
 let release,entered=false;const gate=new Promise(resolve=>{release=resolve;});
 await page.route('**/api/clients/synara',async route=>{if(route.request().method()==='POST'){entered=true;await gate;}await route.continue();});
 try {
  await page.locator('#client-launch').click();await expect.poll(()=>entered).toBe(true);
  const changed=library();changed.defaultModel='vendor/one';await saveLibrary(request,gateway,changed);release();
  await expect(page.locator('#client-launch-status')).toContainText('shared models changed while preparing');
  expect(await records(gateway)).toEqual([]);
  await expect(page.locator('#synara-summary')).toContainText('vendor/one');
  await expect(page.locator('#client-launch')).toBeEnabled();await page.locator('#client-launch').click();
  await expect.poll(async()=>(await records(gateway)).length).toBe(1);
 }finally{release();}
});

test('Synara refuses changing a running private workspace and recovers after quit',async({page,gateway,request})=>{
 await saveLibrary(request,gateway,library());await startProxy(page,gateway);
 await page.locator('#tab-synara').click();await page.locator('#synara-options').click();
 await expect(page.locator('#synara-prepare')).toBeEnabled();
 await writeFile(gateway.launchControl,JSON.stringify({synaraRunning:true}));
 await page.locator('#synara-prepare').click();
 await expect(page.locator('#synara-status')).toContainText(/(Quit|Close).*Synara/i);
 expect(await records(gateway)).toEqual([]);
 await writeFile(gateway.launchControl,'{}');
 await page.locator('#synara-prepare').click();
 await expect(page.locator('#synara-prepared')).toContainText('Four agents prepared');
 expect(await records(gateway)).toEqual([]);
});

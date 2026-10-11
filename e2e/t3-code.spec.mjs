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

test('T3 Code prepares four agents before desktop launch and guards a second opening',async({page,gateway,request},testInfo)=>{
 await saveLibrary(request,gateway,library());
 await startProxy(page,gateway);
 await page.locator('#tab-t3-code').click();
 await expect(page.locator('#t3-code-summary')).toContainText(library().defaultModel);
 await expect(page.locator('#t3-code-models li')).toHaveText(['My default model · '+library().defaultModel,'My coding model · vendor/one']);
 await expect(page.locator('#t3-code-agents li')).toHaveCount(4);
 await expect(page.locator('#t3-code-agents li')).toHaveText([
  'Codex · Normal · Existing Codex CLI login',
  'Kilo Proxy · Codex · Shared models through the proxy',
  'Claude · Normal · Existing Claude Code login',
  'Kilo Proxy · Claude · Compatible shared models through the proxy',
 ]);
 await expect(page.locator('#t3-code-intro')).toContainText('Green KP badges on T3’s provider rail and composer');
 await expect(page.locator('#t3-code-agents')).toContainText('Existing Codex CLI login');
 await expect(page.locator('#t3-code-agents')).toContainText('Existing Claude Code login');
 await expect(page.locator('#client-launch-directory-field')).toBeHidden();
 await expect(page.locator('#client-launch-custom')).toBeHidden();
 expect(await page.locator('#t3-code-helper input,#t3-code-helper select').count()).toBe(0);
 let prepares=0;
 page.on('request',request=>{if(request.method()==='POST'&&new URL(request.url()).pathname==='/api/clients/t3-code')prepares++;});
 await expect(page.locator('#client-launch')).toBeEnabled();
 await page.locator('#client-launch').click();
 await expect.poll(async()=>(await records(gateway)).length).toBe(1);
 expect(prepares).toBe(1);
 await expect(page.locator('#client-launch')).toBeEnabled();
 await page.locator('#client-launch').click();
 await expect(page.locator('#client-launch-status')).toContainText('before opening it again');
 expect((await records(gateway)).length).toBe(1);
 expect(prepares).toBe(2);
 expect((await records(gateway)).every(record=>record.client==='t3-code'&&record.kind==='desktop')).toBe(true);
 const profile=await getAPI(request,gateway,'clients/t3-code');
 expect(profile.prepared).toBe(true);
 expect(profile.version).toBe('0.0.45');
 expect(profile.library).toEqual((await getAPI(request,gateway,'model-library')).library);
 await page.locator('#t3-code-options').click();
 await expect(page.locator('#t3-code-prepared')).toContainText('Four agents prepared');
 await expect(page.locator('#t3-code-compatibility')).toContainText('new chat to switch between normal and Kilo');
 await expect(page.locator('#t3-code-compatibility')).toContainText('Preparation hides the built-in Claude entries');
 await expect(page.locator('#t3-code-compatibility')).toContainText('check the selected model before continuing');
 await expect(page.locator('#t3-code-privacy')).toContainText('regular T3 Code workspace and chats stay separate');
 await expect(page.locator('#t3-code-requirements')).toContainText('not Desktop app logins');
 await expect(page.locator('#t3-code-install')).toHaveAttribute('href','https://github.com/pingdotgg/t3code/releases');
 await expect(page.locator('#t3-code-helper')).not.toContainText('synthetic-kilo-personal-key');
 await expect(page.locator('#t3-code-helper')).not.toContainText(profile.profileDir);
 await page.locator('#t3-code-helper').screenshot({path:testInfo.outputPath('t3-code-en-wide.png')});
 await page.locator('#language').selectOption('es');
 await page.setViewportSize({width:390,height:844});
 await expect(page.locator('#t3-code-agents')).toContainText('Sesión existente de Codex CLI');
 await expect(page.locator('#t3-code-agents li').nth(1)).toContainText('Kilo Proxy · Codex');
 await expect(page.locator('#t3-code-agents li').nth(3)).toContainText('Kilo Proxy · Claude');
 await expect(page.locator('#t3-code-intro')).toContainText('marcas KP verdes');
 await expect(page.locator('#t3-code-compatibility')).toContainText('inicia uno nuevo para cambiar entre normal y Kilo');
 await expect(page.locator('#t3-code-compatibility')).toContainText('revisa el modelo elegido antes de continuar');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
 await page.locator('#t3-code-helper').screenshot({path:testInfo.outputPath('t3-code-es-mobile.png')});
 expect(await page.evaluate(()=>window.__copied)).toEqual([]);
});

test('T3 Code explains missing models, connection and installation without launching',async({page,gateway,request})=>{
 await page.locator('#tab-t3-code').click();
 await expect(page.locator('#client-launch')).toBeDisabled();
 await expect(page.locator('#client-launch-status')).toContainText('Connect your selected provider first.');
 await startProxy(page,gateway);
 await expect(page.locator('#client-launch-status')).toContainText('Add and save models');
 await saveLibrary(request,gateway,library());
 await writeFile(gateway.launchControl,JSON.stringify({unavailable:'t3-code'}));
 await page.locator('#client-launch-refresh').click();
 await expect(page.locator('#client-launch')).toBeDisabled();
 await expect(page.locator('#client-launch-status')).toContainText('Synthetic application unavailable');
 await page.locator('#t3-code-options').click();
 await expect(page.locator('#t3-code-install')).toBeVisible();
 expect(await records(gateway)).toEqual([]);
 await writeFile(gateway.launchControl,'{}');
 await page.locator('#client-launch-refresh').click();
 await expect(page.locator('#client-launch')).toBeEnabled();
});

test('T3 Code stops stale preparation and refreshes an externally changed library',async({page,gateway,request})=>{
 await saveLibrary(request,gateway,library());await startProxy(page,gateway);
 await page.locator('#tab-t3-code').click();await expect(page.locator('#client-launch')).toBeEnabled();
 let release,entered=false;const gate=new Promise(resolve=>{release=resolve;});
 await page.route('**/api/clients/t3-code',async route=>{if(route.request().method()==='POST'){entered=true;await gate;}await route.continue();});
 try {
  await page.locator('#client-launch').click();await expect.poll(()=>entered).toBe(true);
  const changed=library();changed.defaultModel='vendor/one';await saveLibrary(request,gateway,changed);release();
  await expect(page.locator('#client-launch-status')).toContainText('shared models changed while preparing');
  expect(await records(gateway)).toEqual([]);
  await expect(page.locator('#t3-code-summary')).toContainText('vendor/one');
  await expect(page.locator('#client-launch')).toBeEnabled();await page.locator('#client-launch').click();
  await expect.poll(async()=>(await records(gateway)).length).toBe(1);
 }finally{release();}
});

test('T3 Code refuses changing a running private workspace and recovers after quit',async({page,gateway,request})=>{
 await saveLibrary(request,gateway,library());await startProxy(page,gateway);
 await page.locator('#tab-t3-code').click();await page.locator('#t3-code-options').click();
 await expect(page.locator('#t3-code-prepare')).toBeEnabled();
 await writeFile(gateway.launchControl,JSON.stringify({t3CodeRunning:true}));
 await page.locator('#t3-code-prepare').click();
 await expect(page.locator('#t3-code-status')).toContainText(/(Quit|Close).*T3 Code/i);
 expect(await records(gateway)).toEqual([]);
 await writeFile(gateway.launchControl,'{}');
 await page.locator('#t3-code-prepare').click();
 await expect(page.locator('#t3-code-prepared')).toContainText('Four agents prepared');
 expect(await records(gateway)).toEqual([]);
});

test('T3 nightly prepares its actual version and explains V2 handoff in EN/ES',async({page,gateway,request},testInfo)=>{
 const version='0.0.46-nightly.20261003.2610';
 await writeFile(gateway.launchControl,JSON.stringify({t3CodeVersion:version}));
 await saveLibrary(request,gateway,library());await startProxy(page,gateway);
 await page.locator('#tab-t3-code').click();await page.locator('#t3-code-options').click();
 await expect(page.locator('#t3-code-requirements')).toContainText('Detected: '+version);
 await expect(page.locator('#t3-code-compatibility')).toContainText('change agents between turns');
 await expect(page.locator('#t3-code-compatibility')).toContainText('attachments are not transferred');
 await expect(page.locator('#t3-code-compatibility')).toContainText('built-in Claude entries are hidden during preparation');
 await page.locator('#t3-code-prepare').click();
 await expect(page.locator('#t3-code-prepared')).toContainText('Four agents prepared');
 const profile=await getAPI(request,gateway,'clients/t3-code');
 expect(profile.prepared).toBe(true);expect(profile.version).toBe(version);
 await page.locator('#client-launch').click();
 await expect.poll(async()=>(await records(gateway)).length).toBe(1);
 await page.locator('#t3-code-helper').screenshot({path:testInfo.outputPath('t3-nightly-en.png')});
 await page.locator('#language').selectOption('es');await page.setViewportSize({width:390,height:844});
 await expect(page.locator('#t3-code-requirements')).toContainText('Detectado: '+version);
 await expect(page.locator('#t3-code-compatibility')).toContainText('cambiar de agente entre turnos');
 await expect(page.locator('#t3-code-compatibility')).toContainText('Revisa el modelo elegido antes de continuar un chat antiguo');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
 await page.locator('#t3-code-helper').screenshot({path:testInfo.outputPath('t3-nightly-es-mobile.png')});
});

import {test, expect, startProxy} from './fixture.mjs';
import {readFile, writeFile} from 'node:fs/promises';

const library=()=>({schemaVersion:1,defaultModel:'openai/gpt-fixture',models:[
 {id:'openai/gpt-fixture',displayName:'My GPT model',contextPreset:'custom',contextWindow:128000},
 {id:'vendor/one',displayName:'My coding model',contextPreset:'custom',contextWindow:128000,reasoningEffort:'high',reasoningCustom:true,reasoningLevels:['low','high']},
 {id:'anthropic/claude-sonnet-4.6',displayName:'My default model',contextPreset:'custom',contextWindow:128000,reasoningCustom:true,reasoningLevels:[]},
]});
const claudeOfficialIds=[
 'anthropic/claude-opus-5.5',
 'anthropic/claude-sonnet-5.5',
 'anthropic/claude-fable-5.1',
 'anthropic/claude-sonnet-4.6',
 'anthropic/claude-haiku-4.5',
];
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
async function setControl(gateway,changes={}) {let prior={};try{prior=JSON.parse(await readFile(gateway.launchControl,'utf8'));}catch(error){if(error.code!=='ENOENT')throw error;}await writeFile(gateway.launchControl,JSON.stringify({...prior,t3Models:true,...changes}));}
async function setupT3(request,gateway) {await setControl(gateway);await saveLibrary(request,gateway,library());}

test('Muse-only T3 uses the selected CLI and opens without gateway credentials or shared models',async({page,gateway,request})=>{
 await setControl(gateway,{t3Capabilities:true});
 const preferences={version:1,codex:false,claude:false,kiloCodex:false,kiloClaude:false,codexModels:'official',claudeModels:'official',providers:['muse'],openCode2:false};
 for(const [endpoint,data] of [['clients/cli/preferences',{version:1,museExecutable:gateway.codexExecutable}],['clients/t3-code/preferences',preferences]]) {
  const result=await request.post(new URL('/api/'+endpoint,gateway.url).href,{headers:headers(gateway),data});expect(result.ok(),await result.text()).toBe(true);
 }
 expect((await getAPI(request,gateway,'model-library')).library.models).toEqual([]);
 await page.reload();
 await page.locator('#tab-t3-code').click();await expect(page.locator('#t3-code-provider-muse')).toBeChecked();
 await expect(page.locator('#client-launch')).toBeEnabled();await page.locator('#client-launch').click();
 await expect.poll(async()=>(await records(gateway)).length).toBe(1);
 expect((await records(gateway))[0].client).toBe('t3-code');
 await expect(page.locator('#status-label')).toHaveText('Proxy stopped');
 expect((await getAPI(request,gateway,'model-library')).library.models).toEqual([]);
 const profile=await getAPI(request,gateway,'clients/t3-code');expect(profile.prepared).toBe(true);expect(profile.effectiveCodexModels).toEqual([]);expect(profile.effectiveClaudeModels).toEqual([]);
 await page.locator('#t3-code-settings-title').click();
 await page.locator('#t3-code-account-kiloCodex').check();
 await expect(page.locator('#t3-code-account-kiloCodex')).toBeEnabled();
 await expect(page.locator('#client-launch')).toBeDisabled();await expect(page.locator('#client-launch-status')).toContainText('Connect your selected provider first.');
 expect((await records(gateway)).length).toBe(1);
});

test('T3 Code prepares chosen accounts before desktop launch and guards a second opening',async({page,gateway,request},testInfo)=>{
 await setupT3(request,gateway);
 await startProxy(page,gateway);
 await page.locator('#tab-t3-code').click();
 await expect(page.locator('#t3-code-summary')).toHaveText('Kilo Proxy · Codex: 1 · Kilo Proxy · Claude: 5');
 await expect(page.locator('#t3-code-models li')).toHaveText([
  'Kilo Proxy · Codex · My GPT model · openai/gpt-fixture',
  'Kilo Proxy · Claude · Claude Opus 5.5 · anthropic/claude-opus-5.5',
  'Kilo Proxy · Claude · Claude Sonnet 5.5 · anthropic/claude-sonnet-5.5',
  'Kilo Proxy · Claude · Claude Fable 5.1 · anthropic/claude-fable-5.1',
  'Kilo Proxy · Claude · My default model · anthropic/claude-sonnet-4.6',
  'Kilo Proxy · Claude · Claude Haiku 4.5 · anthropic/claude-haiku-4.5',
 ]);
 await expect(page.locator('#t3-code-agents li')).toHaveCount(4);
 await expect(page.locator('#t3-code-agents li')).toHaveText([
  'Codex · Existing Codex CLI login',
  'Kilo Proxy · Codex · Models through the proxy',
  'Claude · Existing Claude Code login',
  'Kilo Proxy · Claude · Models through the proxy',
 ]);
 await expect(page.locator('#t3-code-intro')).toContainText('Green KP badges on T3’s provider rail and composer');
 await expect(page.locator('#t3-code-agents')).toContainText('Existing Codex CLI login');
 await expect(page.locator('#t3-code-agents')).toContainText('Existing Claude Code login');
 await expect(page.locator('#client-launch-directory-field')).toBeHidden();
 await expect(page.locator('#client-launch-custom')).toBeHidden();
 await page.locator('#t3-code-settings-title').click();
 await expect(page.locator('#t3-code-account-codex')).toBeChecked();
 await expect(page.locator('#t3-code-account-claude')).toBeChecked();
 await expect(page.locator('#t3-code-codex-models')).toHaveValue('official');
 await expect(page.locator('#t3-code-claude-models')).toHaveValue('official');
 await expect(page.locator('#t3-code-opencode-version-field')).toBeHidden();
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
 await expect(page.locator('#t3-code-prepared')).toContainText('Your saved T3 accounts and models are prepared.');
 await expect(page.locator('#t3-code-compatibility')).toContainText('new chat to switch accounts');
 await expect(page.locator('#t3-code-compatibility')).toContainText('native reasoning selector');
 await expect(page.locator('#t3-code-compatibility')).toContainText('Check the selected account and model before continuing');
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
 await expect(page.locator('#t3-code-compatibility')).toContainText('Inicia un chat nuevo para cambiar de cuenta');
 await expect(page.locator('#t3-code-compatibility')).toContainText('Revisa la cuenta y el modelo elegidos antes de continuar');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
 await page.locator('#t3-code-helper').screenshot({path:testInfo.outputPath('t3-code-es-mobile.png')});
 expect(await page.evaluate(()=>window.__copied)).toEqual([]);
});

test('T3 loads the Claude and Meta catalogs without shared models and still requires them for Shared',async({page,gateway,request})=>{
 await setControl(gateway,{t3Capabilities:true,t3ColdCatalog:true});
 const preferences={version:1,codex:false,claude:false,kiloCodex:false,kiloClaude:true,codexModels:'official',claudeModels:'official',providers:['muse'],openCode2:false,openCodeConnection:'native',museConnection:'kilo',museModels:'official'};
 for(const [endpoint,data] of [['clients/cli/preferences',{version:1,museExecutable:gateway.codexExecutable}],['clients/t3-code/preferences',preferences]]) {
  const result=await request.post(new URL('/api/'+endpoint,gateway.url).href,{headers:headers(gateway),data});
  expect(result.ok(),await result.text()).toBe(true);
 }
 const empty=(await getAPI(request,gateway,'model-library')).library;
 expect(empty.models).toEqual([]);
 await page.reload();
 await startProxy(page,gateway);
 await page.locator('#tab-t3-code').click();
 await expect(page.locator('#t3-code-summary')).toHaveText('Kilo Proxy · Codex: 0 · Kilo Proxy · Claude: 0 · Kilo Proxy · Muse Code: 0');
 await page.locator('#t3-code-settings-title').click();
 await expect(page.locator('#t3-code-claude-official')).toHaveText('Claude models');
 await expect(page.locator('#t3-code-muse-official')).toHaveText('Meta models');
 await expect(page.locator('#client-launch')).toBeEnabled();
 const prepare=page.waitForRequest(r=>r.method()==='POST'&&new URL(r.url()).pathname==='/api/clients/t3-code');
 await page.locator('#client-launch').click();
 expect((await prepare).postDataJSON()).toEqual({preferences});
 await expect.poll(async()=>(await records(gateway)).map(record=>record.client)).toEqual(['t3-code']);
 await expect(page.locator('#t3-code-summary')).toHaveText('Kilo Proxy · Codex: 0 · Kilo Proxy · Claude: 5 · Kilo Proxy · Muse Code: 2');
 const profile=await getAPI(request,gateway,'clients/t3-code');
 expect(profile.prepared).toBe(true);
 expect(profile.library).toEqual(empty);
 expect(profile.effectiveClaudeModels.map(model=>model.id)).toEqual(claudeOfficialIds);
 expect(profile.effectiveMuseModels.map(model=>model.id)).toEqual(['meta/muse-spark-1.3','meta-llama/llama-4-maverick']);
 await page.locator('#t3-code-muse-models').selectOption('shared');
 await expect(page.locator('#t3-code-muse-models')).toBeEnabled();
 await expect(page.locator('#client-launch')).toBeDisabled();
 await expect(page.locator('#client-launch-status')).toContainText('Add and save models');
 expect((await getAPI(request,gateway,'model-library')).library).toEqual(empty);
 expect((await records(gateway)).length).toBe(1);
});

test('T3 Code explains missing models, connection and installation without launching',async({page,gateway,request})=>{
 await setControl(gateway);
 await page.locator('#tab-t3-code').click();
 await expect(page.locator('#client-launch')).toBeDisabled();
 await expect(page.locator('#client-launch-status')).toContainText('Connect Kilo to use the Claude or Meta catalog in T3 Code.');
 await startProxy(page,gateway);
 await expect(page.locator('#client-launch-status')).toContainText('Add and save models');
 await setupT3(request,gateway);
 await setControl(gateway,{unavailable:'t3-code'});
 await page.locator('#client-launch-refresh').click();
 await expect(page.locator('#client-launch')).toBeDisabled();
 await expect(page.locator('#client-launch-status')).toContainText('Synthetic application unavailable');
 await page.locator('#t3-code-options').click();
 await expect(page.locator('#t3-code-install')).toBeVisible();
 expect(await records(gateway)).toEqual([]);
 await setControl(gateway,{unavailable:'',t3CodeRunning:false});
 await page.locator('#client-launch-refresh').click();
 await expect(page.locator('#client-launch')).toBeEnabled();
});

test('T3 blocks unsaved or failed OpenCode paths without blocking Claude and recovers after explicit save',async({page,gateway,request})=>{
 await setupT3(request,gateway);await startProxy(page,gateway);await page.locator('#tab-t3-code').click();await expect(page.locator('#client-launch')).toBeEnabled();
 let prepares=0,fail=true;page.on('request',request=>{if(request.method()==='POST'&&new URL(request.url()).pathname==='/api/clients/t3-code')prepares++;});
 await page.locator('#tab-opencode').click();await expect(page.locator('#opencode-executable')).toBeEnabled();await page.locator('#opencode-executable').fill(gateway.openCodeExecutable);
 await page.locator('#tab-t3-code').click();await expect(page.locator('#client-launch')).toBeDisabled();await expect(page.locator('#client-launch-status')).toContainText('OpenCode executable paths');
 await page.locator('#t3-code-options').click();await page.locator('#t3-code-prepare').click();await expect(page.locator('#t3-code-status')).toContainText('CLI executable settings changed');expect(prepares).toBe(0);expect(await records(gateway)).toEqual([]);
 await page.locator('#tab-claude').click();await page.locator('#model-picker input[name="model-choice"][value="vendor/one"]').check();await expect(page.locator('#client-launch')).toBeEnabled();
 await page.route('**/api/clients/opencode/preferences',async route=>{if(route.request().method()==='POST'&&fail)return route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{message:'Synthetic OpenCode persistence unavailable'}})});await route.continue();});
 await page.locator('#tab-opencode').click();await page.locator('#opencode-executable-save').click();await expect(page.locator('#opencode-version-status')).toContainText('Synthetic OpenCode persistence unavailable');
 await page.locator('#tab-t3-code').click();await expect(page.locator('#client-launch')).toBeDisabled();expect(prepares).toBe(0);expect(await records(gateway)).toEqual([]);
 fail=false;await page.locator('#tab-opencode').click();await page.locator('#opencode-version-retry').click();await expect(page.locator('#opencode-executable')).toBeEnabled();expect((await getAPI(request,gateway,'clients/opencode/preferences')).executable1).toBe(gateway.openCodeExecutable);
 await page.locator('#tab-t3-code').click();await expect(page.locator('#client-launch')).toBeEnabled();await page.locator('#client-launch').click();await expect.poll(async()=>(await records(gateway)).length).toBe(1);expect(prepares).toBe(1);
});

test('T3 Code stops stale preparation and refreshes an externally changed library',async({page,gateway,request})=>{
 await setupT3(request,gateway);await startProxy(page,gateway);
 await page.locator('#tab-t3-code').click();await expect(page.locator('#client-launch')).toBeEnabled();
 let release,entered=false;const gate=new Promise(resolve=>{release=resolve;});
 await page.route('**/api/clients/t3-code',async route=>{if(route.request().method()==='POST'){entered=true;await gate;}await route.continue();});
 try {
  await page.locator('#client-launch').click();await expect.poll(()=>entered).toBe(true);
  const changed=library();changed.defaultModel='vendor/one';await saveLibrary(request,gateway,changed);release();
  await expect(page.locator('#client-launch-status')).toContainText('shared models changed while preparing');
  expect(await records(gateway)).toEqual([]);
  expect((await getAPI(request,gateway,'model-library')).library.defaultModel).toBe('vendor/one');
  await expect(page.locator('#t3-code-summary')).toContainText('Kilo Proxy · Claude: 5');
  await expect(page.locator('#client-launch')).toBeEnabled();await page.locator('#client-launch').click();
  await expect.poll(async()=>(await records(gateway)).length).toBe(1);
 }finally{release();}
});

test('T3 Code refuses changing a running private workspace and recovers after quit',async({page,gateway,request})=>{
 await setupT3(request,gateway);await startProxy(page,gateway);
 await page.locator('#tab-t3-code').click();await page.locator('#t3-code-options').click();
 await expect(page.locator('#t3-code-prepare')).toBeEnabled();
 await setControl(gateway,{t3CodeRunning:true});
 await page.locator('#t3-code-prepare').click();
 await expect(page.locator('#t3-code-status')).toContainText(/(Quit|Close).*T3 Code/i);
 expect(await records(gateway)).toEqual([]);
 await setControl(gateway,{unavailable:'',t3CodeRunning:false});
 await page.locator('#t3-code-prepare').click();
 await expect(page.locator('#t3-code-prepared')).toContainText('Your saved T3 accounts and models are prepared.');
 expect(await records(gateway)).toEqual([]);
});

test('T3 nightly prepares its actual version and explains V2 handoff in EN/ES',async({page,gateway,request},testInfo)=>{
 const version='0.0.46-nightly.20261003.2610';
 await setControl(gateway,{t3CodeVersion:version});
 await setupT3(request,gateway);await startProxy(page,gateway);
 await page.locator('#tab-t3-code').click();await page.locator('#t3-code-options').click();
 await expect(page.locator('#t3-code-requirements')).toContainText('Detected: '+version);
 await expect(page.locator('#t3-code-compatibility')).toContainText('change agents between turns');
 await expect(page.locator('#t3-code-compatibility')).toContainText('attachments are not transferred');
 await expect(page.locator('#t3-code-compatibility')).toContainText('native reasoning selector');
 await page.locator('#t3-code-prepare').click();
 await expect(page.locator('#t3-code-prepared')).toContainText('Your saved T3 accounts and models are prepared.');
 const profile=await getAPI(request,gateway,'clients/t3-code');
 expect(profile.prepared).toBe(true);expect(profile.version).toBe(version);
 await page.locator('#client-launch').click();
 await expect.poll(async()=>(await records(gateway)).length).toBe(1);
 await page.locator('#t3-code-helper').screenshot({path:testInfo.outputPath('t3-nightly-en.png')});
 await page.locator('#language').selectOption('es');await page.setViewportSize({width:390,height:844});
 await expect(page.locator('#t3-code-requirements')).toContainText('Detectado: '+version);
 await expect(page.locator('#t3-code-compatibility')).toContainText('cambiar de agente entre turnos');
 await expect(page.locator('#t3-code-compatibility')).toContainText('Revisa la cuenta y el modelo elegidos antes de continuar un chat antiguo');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
 await page.locator('#t3-code-helper').screenshot({path:testInfo.outputPath('t3-nightly-es-mobile.png')});
});

test('T3 settings save account and model choices, recover a failed write, and require preparation',async({page,gateway,request},testInfo)=>{
 await setupT3(request,gateway);await startProxy(page,gateway);
 await page.locator('#tab-t3-code').click();await page.locator('#t3-code-settings-title').click();
 await expect(page.locator('#t3-code-account-codex')).toBeEnabled();
 let writes=0,release;const gate=new Promise(resolve=>{release=resolve;});
 await page.route('**/api/clients/t3-code/preferences',async route=>{
  if(route.request().method()==='POST'&&++writes===1){await gate;await route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{message:'Synthetic settings unavailable',type:'kilo_local_error'}})});return;}
  await route.continue();
 });
 try {
  await page.locator('#t3-code-account-codex').uncheck();
  await expect.poll(()=>writes).toBe(1);
  await expect(page.locator('#t3-code-account-claude')).toBeDisabled();
  await expect(page.locator('#client-launch')).toBeDisabled();
  release();
  await expect(page.locator('#t3-code-settings-status')).toContainText('Synthetic settings unavailable');
  await expect(page.locator('#t3-code-account-codex')).toBeChecked();
  await expect(page.locator('#t3-code-settings-retry')).toBeVisible();
  await page.locator('#t3-code-settings-retry').click();
  await expect(page.locator('#t3-code-account-codex')).not.toBeChecked();
  await expect(page.locator('#t3-code-account-claude')).toBeEnabled();
  expect(writes).toBe(2);
  await expect(page.locator('#t3-code-agents li')).toHaveCount(3);
  await expect(page.locator('#t3-code-settings-status')).toContainText('Close T3 Code · Kilo and prepare again');
  await page.locator('#t3-code-codex-models').selectOption('shared');
  await expect(page.locator('#t3-code-summary')).toContainText('Kilo Proxy · Codex: 3');
  await expect(page.locator('#t3-code-claude-models')).toBeEnabled();
  const saved=await getAPI(request,gateway,'clients/t3-code/preferences');
  expect(saved.preferences.codex).toBe(false);expect(saved.preferences.claude).toBe(true);
  expect(saved.preferences.codexModels).toBe('shared');expect(saved.preferences.claudeModels).toBe('official');
  await page.locator('#t3-code-options').click();await page.locator('#t3-code-prepare').click();
  await expect(page.locator('#t3-code-prepared')).toHaveText('Your saved T3 accounts and models are prepared.');
  const profile=await getAPI(request,gateway,'clients/t3-code');expect(profile.preferences).toEqual(saved.preferences);
  expect(profile.effectiveClaudeModels.map(model=>model.id)).toEqual(claudeOfficialIds);
  expect(await records(gateway)).toEqual([]);
  await expect(page.locator('#toast')).toBeHidden();
  await page.locator('#t3-code-helper').screenshot({path:testInfo.outputPath('t3-settings-en.png')});
  await page.locator('#language').selectOption('es');await page.setViewportSize({width:390,height:844});
  await expect(page.locator('#t3-code-settings-title')).toHaveText('Ajustes de T3 Code');
  await expect(page.locator('#t3-code-codex-models-label')).toHaveText('Modelos de Codex Kilo');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
  await page.locator('#t3-code-helper').screenshot({path:testInfo.outputPath('t3-settings-es-mobile.png')});
 }finally{release();}
});

test('T3 refuses externally changed account settings before writing a profile',async({page,gateway,request})=>{
 await setupT3(request,gateway);await startProxy(page,gateway);
 await page.locator('#tab-t3-code').click();await page.locator('#t3-code-options').click();
 await expect(page.locator('#t3-code-prepare')).toBeEnabled();
 const saved=await getAPI(request,gateway,'clients/t3-code/preferences');
 const update=await request.post(new URL('/api/clients/t3-code/preferences',gateway.url).href,{headers:headers(gateway),data:{...saved.preferences,claude:false}});
 expect(update.ok(),await update.text()).toBe(true);
 let prepares=0;page.on('request',request=>{if(request.method()==='POST'&&new URL(request.url()).pathname==='/api/clients/t3-code')prepares++;});
 await page.locator('#t3-code-prepare').click();
 await expect(page.locator('#t3-code-status')).toContainText('T3 Code settings changed. Review them and open again.');
 expect(prepares).toBe(0);expect(await records(gateway)).toEqual([]);
 await expect(page.locator('#t3-code-agents')).not.toContainText('Existing Claude Code login');
 await page.locator('#t3-code-prepare').click();await expect(page.locator('#t3-code-prepared')).toHaveText('Your saved T3 accounts and models are prepared.');
 expect(prepares).toBe(1);
});

test('T3 exposes only native supported extras and one OpenCode version per account',async({page,gateway,request},testInfo)=>{
 await setControl(gateway,{t3Capabilities:true});await setupT3(request,gateway);await startProxy(page,gateway);
 await page.locator('#tab-t3-code').click();await page.locator('#t3-code-settings-title').click();
 await expect(page.locator('#t3-code-provider-options label')).toHaveText(['Cursor','Grok','OpenCode','Antigravity','Muse Code','Pi']);
 await expect(page.locator('#t3-code-opencode-version-field')).toBeVisible();
 await expect(page.locator('#t3-code-opencode-version')).toBeDisabled();
 await page.locator('#t3-code-provider-cursor').check();await expect(page.locator('#t3-code-provider-opencode')).toBeEnabled();
 await page.locator('#t3-code-provider-opencode').check();await expect(page.locator('#t3-code-opencode-version')).toBeEnabled();
 await page.locator('#t3-code-opencode-version').selectOption('2');await expect(page.locator('#t3-code-provider-opencode')).toBeEnabled();
 const second=await getAPI(request,gateway,'clients/t3-code/preferences');
 expect(second.preferences.providers).toEqual(['cursor','opencode']);expect(second.preferences.openCode2).toBe(true);
 await expect(page.locator('#t3-code-agents')).toContainText('OpenCode 2');
 await page.locator('#t3-code-opencode-version').selectOption('1');await expect(page.locator('#t3-code-provider-opencode')).toBeEnabled();
 expect((await getAPI(request,gateway,'clients/t3-code/preferences')).preferences.openCode2).toBe(false);
 await page.locator('#t3-code-provider-opencode').uncheck();await expect(page.locator('#t3-code-provider-cursor')).toBeEnabled();
 const disabled=await getAPI(request,gateway,'clients/t3-code/preferences');expect(disabled.preferences.providers).toEqual(['cursor']);expect(disabled.preferences.openCode2).toBe(false);
 await expect(page.locator('#t3-code-opencode-version')).toBeDisabled();
 expect(await records(gateway)).toEqual([]);
 await page.locator('#t3-code-helper').screenshot({path:testInfo.outputPath('t3-settings-extra-providers.png')});
});

test('T3 keeps saved unavailable choices visible so they can be disabled after changing builds',async({page,gateway,request},testInfo)=>{
 await setControl(gateway,{t3Capabilities:true});await setupT3(request,gateway);
 const original=await getAPI(request,gateway,'clients/t3-code/preferences');
 const selected=await request.post(new URL('/api/clients/t3-code/preferences',gateway.url).href,{headers:headers(gateway),data:{...original.preferences,providers:['cursor','opencode'],openCode2:true}});expect(selected.ok(),await selected.text()).toBe(true);
 await setControl(gateway,{t3Capabilities:false,t3Models:true});await startProxy(page,gateway);
 await page.locator('#tab-t3-code').click();await page.locator('#t3-code-settings-title').click();
 await expect(page.locator('#t3-code-provider-options label')).toHaveText(['Cursor · unavailable in this build','OpenCode · unavailable in this build']);
 await expect(page.locator('#t3-code-provider-cursor')).toBeChecked();await expect(page.locator('#t3-code-provider-cursor')).toBeEnabled();
 await expect(page.locator('#t3-code-opencode-version')).toHaveValue('2');await expect(page.locator('#t3-code-opencode-version-2')).toHaveJSProperty('disabled',true);
 await expect(page.locator('#toast')).toBeHidden();await page.locator('#t3-code-helper').screenshot({path:testInfo.outputPath('t3-settings-unavailable-en.png')});
 await page.locator('#t3-code-provider-cursor').click();await expect(page.locator('#t3-code-provider-cursor')).toHaveCount(0);
 await page.locator('#t3-code-opencode-version').selectOption('1');await expect(page.locator('#t3-code-opencode-version-field')).toBeHidden();
 await page.locator('#t3-code-provider-opencode').click();await expect(page.locator('#t3-code-provider-options label')).toHaveCount(0);
 const cleared=await getAPI(request,gateway,'clients/t3-code/preferences');expect(cleared.preferences.providers).toEqual([]);expect(cleared.preferences.openCode2).toBe(false);expect(await records(gateway)).toEqual([]);
});

test('T3 provider controls belong only to the T3 harness panel',async({page,gateway,request})=>{
 await setControl(gateway,{t3Capabilities:true});
 await page.reload();await page.locator('#tab-t3-code').click();await page.locator('#t3-code-settings-title').click();
 await expect(page.locator('#t3-code-helper #t3-code-settings')).toBeVisible();
 await expect(page.locator('#t3-code-helper #t3-code-account-codex')).toBeVisible();
 await expect(page.locator('#t3-code-helper #t3-code-provider-opencode')).toBeVisible();
 await expect(page.locator('#t3-code-helper #t3-code-provider-muse')).toBeVisible();
 expect(await page.locator('#t3-code-settings').count()).toBe(1);expect(await page.locator('#cli-preferences #t3-code-settings').count()).toBe(0);
 await expect(page.locator('#client-launch-help')).toContainText('chosen in this T3 Code panel');
 await page.locator('#language').selectOption('es');await expect(page.locator('#client-launch-help')).toContainText('elegidos en este panel de T3 Code');
 expect(await records(gateway)).toEqual([]);
});


test('T3 gear separates the OpenCode connection from its version and confirms shared models before preparing',async({page,gateway,request},testInfo)=>{
 await setControl(gateway,{t3Capabilities:true});await setupT3(request,gateway);await startProxy(page,gateway);
 await page.locator('#tab-t3-code').click();await page.locator('#t3-code-settings-title').click();
 await expect(page.locator('#t3-code-opencode-connection')).toHaveValue('native');
 await expect(page.locator('#t3-code-opencode-connection')).toBeDisabled();
 await page.locator('#t3-code-provider-opencode').check();await expect(page.locator('#t3-code-opencode-connection')).toBeEnabled();
 await page.locator('#t3-code-opencode-connection').selectOption('kilo');await expect(page.locator('#t3-code-provider-opencode')).toBeEnabled();
 await page.locator('#t3-code-opencode-version').selectOption('2');await expect(page.locator('#t3-code-provider-opencode')).toBeEnabled();
 const response=await getAPI(request,gateway,'clients/t3-code/preferences');
 expect(response.preferences.openCodeConnection).toBe('kilo');expect(response.preferences.openCode2).toBe(true);
 expect(response.openCodeKiloSupported).toBe(true);
 await expect(page.locator('#t3-code-agents')).toContainText('Kilo Proxy · OpenCode 2');
 await expect(page.locator('#t3-code-opencode-connection-help')).toContainText('models saved in Models');
 await expect(page.locator('#t3-code-opencode-help')).not.toContainText('own login');
 const before=await getAPI(request,gateway,'model-library');
 const profile=await getAPI(request,gateway,'clients/t3-code');
 expect(profile.effectiveOpenCodeModels.map(model=>model.id)).toEqual(before.library.models.map(model=>model.id));
 for(const model of profile.effectiveOpenCodeModels)await expect(page.locator('#t3-code-models')).toContainText('Kilo Proxy · OpenCode · '+(model.name?model.name+' · ':'')+model.id);
 await expect(page.locator('#t3-code-settings-status')).toContainText('prepare again');
 await page.locator('#t3-code-helper').screenshot({path:testInfo.outputPath('t3-opencode-kilo-en.png')});
 await page.locator('#language').selectOption('es');await page.setViewportSize({width:390,height:844});
 await expect(page.locator('#t3-code-opencode-connection-label')).toHaveText('Conexión de OpenCode');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
 await page.locator('#t3-code-opencode-connection').selectOption('native');await expect(page.locator('#t3-code-provider-opencode')).toBeEnabled();
 await expect(page.locator('#t3-code-opencode-version')).toHaveValue('2');
 await expect(page.locator('#t3-code-opencode-help')).toContainText('perfil separado que puede requerir su propio inicio de sesión');
 await expect(page.locator('#t3-code-models')).not.toContainText('Kilo Proxy · OpenCode');
 await page.locator('#t3-code-settings-reload').click();await expect(page.locator('#t3-code-opencode-connection')).toHaveValue('native');
 await expect(page.locator('#t3-code-opencode-version')).toHaveValue('2');
 await page.locator('#t3-code-helper').screenshot({path:testInfo.outputPath('t3-opencode-native-v2-es.png')});
 expect(await records(gateway)).toEqual([]);
});

test('Muse Kilo-only T3 requires shared connection and keeps its Meta account choice distinct',async({page,gateway,request},testInfo)=>{
 await setControl(gateway,{t3Capabilities:true});await saveLibrary(request,gateway,library());
 const selected=await request.post(new URL('/api/clients/cli/preferences',gateway.url).href,{headers:headers(gateway),data:{version:1,museExecutable:gateway.codexExecutable}});expect(selected.ok(),await selected.text()).toBe(true);
 let preferences={version:1,codex:false,claude:false,kiloCodex:false,kiloClaude:false,codexModels:'official',claudeModels:'official',providers:['muse'],openCode2:false,openCodeConnection:'native',museConnection:'kilo'};
 const shared=(await getAPI(request,gateway,'model-library')).library,writes=[];
 await page.route('**/api/clients/t3-code**',async route=>{
  const path=new URL(route.request().url()).pathname,body=route.request().method()==='POST'?route.request().postDataJSON():null;
  if(path.endsWith('/preferences')){if(body){writes.push(body);preferences=body;}return route.fulfill({json:{preferences,supportedProviders:[{id:'muse',label:'Muse Code'}],museKiloSupported:true,requiresPreparation:true}});}
  return route.fulfill({json:{preferences,library:shared,prepared:false,effectiveCodexModels:[],effectiveClaudeModels:[],effectiveMuseModels:preferences.museConnection==='kilo'?shared.models.map(model=>({id:model.id,name:model.displayName||model.id})):[]}});
 });
 await page.reload();await page.locator('#tab-t3-code').click();await expect(page.locator('#client-launch')).toBeDisabled();await expect(page.locator('#client-launch-status')).toContainText('Connect your selected provider first');
 await startProxy(page,gateway);await expect(page.locator('#client-launch')).toBeEnabled();await expect(page.locator('#t3-code-agents')).toContainText('Kilo Proxy · Muse Code');await expect(page.locator('#t3-code-summary')).toContainText('Muse Code: 3');
 await page.locator('#t3-code-settings-title').click();await expect(page.locator('#t3-code-muse-connection')).toHaveValue('kilo');await page.locator('#t3-code-muse-connection').selectOption('native');await expect(page.locator('#t3-code-muse-connection')).toBeEnabled();expect(writes.at(-1).museConnection).toBe('native');await expect(page.locator('#t3-code-agents')).toContainText('Meta login and catalog');await expect(page.locator('#t3-code-summary')).not.toContainText('Muse Code:');
 await page.locator('#t3-code-muse-connection').selectOption('kilo');await expect(page.locator('#t3-code-muse-connection')).toBeEnabled();expect(writes.at(-1).museConnection).toBe('kilo');
 await expect(page.locator('#toast')).toBeHidden();
 for(const [language,width,height]of [['en',1180,1000],['es',780,1000]]){await page.locator('#language').selectOption(language);await page.setViewportSize({width,height});await expect(page.locator('#t3-code-muse-connection-help')).toContainText(language==='en'?'separate Muse profile':'perfil Muse separado');expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.locator('#t3-code-settings').screenshot({path:testInfo.outputPath(`t3-muse-kilo-${language}-${width}.png`)});}
});

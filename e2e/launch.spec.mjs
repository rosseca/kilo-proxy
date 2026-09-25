import {test,expect,startProxy} from './fixture.mjs';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import path from 'node:path';

const modelID='vendor/one';
const selected=page=>page.locator('#model-picker input[name="model-choice"][value="'+modelID+'"]');
async function records(gateway){try{return JSON.parse(await readFile(gateway.launchRecords,'utf8'));}catch(error){if(error.code==='ENOENT')return [];throw error;}}
const control=(gateway,value)=>writeFile(gateway.launchControl,JSON.stringify(value));
async function choose(page,id){
 const client=id.startsWith('xcode-')?'xcode':id;
 await page.locator('#tab-'+client).click();
 if(client==='xcode')await page.locator('[data-xcode-variant="'+id.slice(6)+'"]').click();
 if(['opencode','zed','omp'].includes(client))await page.locator('[data-editor-id="'+modelID+'"]').check();
 else if(client==='xcode')await page.locator('[data-xcode-focus="choose:'+modelID+'"]').check();
 else await selected(page).check();
 return ['opencode','zed','omp'].includes(client)?page.locator('[data-editor-name="'+modelID+'"]'):client==='xcode'?page.locator('[data-xcode-focus="name:'+modelID+'"]'):page.locator('[data-focus="'+(client==='claude'?'claude-name':'name')+':'+modelID+'"]');
}
const endpoint=id=>['codex','codex-cli'].includes(id)?id+'/catalog':id==='claude'?'claude/profile':['opencode','zed'].includes(id)?'editors/'+id+'/profile':'xcode/'+id.slice(6);

const cliInstallationURLs={
 'codex-cli':'https://developers.openai.com/codex/cli/',
 claude:'https://code.claude.com/docs/en/setup#install-claude-code',
 opencode:'https://opencode.ai/docs/#install',
 omp:'https://github.com/can1357/oh-my-pi#install',
};

for(const [id,url] of Object.entries(cliInstallationURLs)){
 test(`launcher links to official ${id} installation and checks again without launching`,async({page,gateway},testInfo)=>{
  await control(gateway,{unavailable:id,...(id==='claude'?{claudeVersion:''}:{})});
  let capabilityChecks=0;
  page.on('request',request=>{if(new URL(request.url()).pathname==='/api/claude/info')capabilityChecks++;});
  await startProxy(page,gateway);
  await choose(page,id);
  const status=page.locator('#client-installation-status'),link=page.locator('#client-installation-link');
  await expect(status).toHaveText('CLI not found');
  await expect(link).toBeVisible();
  await expect(link).toHaveText('Installation guide ↗');
  await expect(link).toHaveAttribute('href',url);
  await expect(link).toHaveAttribute('target','_blank');
  await expect(link).toHaveAttribute('rel','noopener noreferrer');
  await expect(page.locator('#client-launch')).toBeDisabled();
  await expect(page.locator('#client-launch-status')).toContainText('Synthetic application unavailable');
  if(id==='claude'){
   await expect(page.locator('#claude-version-status')).toHaveText('Claude Code was not detected. Basic compatibility is used.');
   await page.locator('#model-picker input[name="model-choice"][value="anthropic/claude-sonnet-4.6"]').check();
   await expect(page.locator('[data-focus="claude-effort:anthropic/claude-sonnet-4.6"]')).toBeDisabled();
   expect(capabilityChecks).toBe(1);
  }
  const writes=[];
  page.on('request',request=>{if(request.method()==='POST')writes.push(new URL(request.url()).pathname);});
  await page.locator('#client-launch-refresh').click();
  await expect(status).toHaveText('CLI not found');
  await expect(page.locator('#client-launch-refresh')).toBeEnabled();
  expect(await records(gateway)).toEqual([]);
  if(id==='claude'){
   expect(capabilityChecks).toBe(2);
   await page.locator('#language').selectOption('es');
   await expect(status).toHaveText('CLI no encontrado');
   await expect(link).toHaveText('Guía de instalación ↗');
   await expect(page.locator('#client-launch-refresh')).toHaveText('Volver a comprobar');
   await page.setViewportSize({width:390,height:844});
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
   await page.locator('#client-launch-bar').screenshot({path:testInfo.outputPath('cli-installation-es-mobile.png')});
   await page.locator('#language').selectOption('en');
   writes.length=0; // Language changes persist the UI preference, not CLI setup.
  }
  await control(gateway,id==='claude'?{claudeVersion:'2.1.251'}:{});
  await page.locator('#client-launch-refresh').click();
  await expect(status).toHaveText('Installed');
  await expect(link).toBeHidden();
  await expect(link).not.toHaveAttribute('href');
  await expect(page.locator('#client-launch')).toBeEnabled();
  if(id==='claude'){
   await expect(page.locator('#claude-version-status')).toHaveText('Claude Code 2.1.251 · compatible with Kilo configuration');
   await expect(page.locator('#claude-capabilities-note')).toContainText('per-model reasoning preferences');
   await expect(page.locator('[data-focus="claude-effort:anthropic/claude-sonnet-4.6"]')).toBeEnabled();
   await expect(page.locator('#snippet-code')).toContainText('"modelPicker"');
   expect(capabilityChecks).toBe(3);
  }
  expect(writes).toEqual([]);
  expect(await records(gateway)).toEqual([]);
 });
}

test('Claude installation checks reuse an in-flight capability check',async({page,gateway})=>{
 await control(gateway,{claudeVersion:'2.1.251'});
 await startProxy(page,gateway);
 let release,checks=0;
 const gate=new Promise(resolve=>{release=resolve;});
 await page.route('**/api/claude/info',async route=>{checks++;await gate;await route.continue();});
 try{
  await page.locator('#tab-claude').click();
  await expect.poll(()=>checks).toBe(1);
  await page.locator('#tab-codex').click();
  await page.locator('#tab-claude').click();
  await page.locator('#tab-claude').click();
  await expect(page.locator('#client-launch-refresh')).toHaveText('Checking…');
  await expect(page.locator('#client-launch-refresh')).toBeDisabled();
  await expect(page.locator('#detect-claude')).toBeDisabled();
  expect(checks).toBe(1);
  release();
  await expect(page.locator('#claude-version-status')).toHaveText('Claude Code 2.1.251 · compatible with Kilo configuration');
  await expect(page.locator('#client-launch-refresh')).toHaveText('Check again');
  await expect(page.locator('#client-launch-refresh')).toBeEnabled();
  expect(checks).toBe(1);
 }finally{release();}
});

test('installed CLIs with a missing terminal stay installed without installation guidance',async({page,gateway},testInfo)=>{
 await control(gateway,{terminalMissing:true});
 await startProxy(page,gateway);
 for(const id of Object.keys(cliInstallationURLs)){
  await choose(page,id);
  await expect(page.locator('#client-installation-status')).toHaveText('Installed');
  await expect(page.locator('#client-installation-link')).toBeHidden();
  await expect(page.locator('#client-launch')).toBeDisabled();
  await expect(page.locator('#client-launch-status')).toHaveText('Synthetic terminal unavailable.');
 }
 await page.locator('#client-launch-bar').screenshot({path:testInfo.outputPath('cli-terminal-missing.png')});
 expect(await records(gateway)).toEqual([]);
 await control(gateway,{});
 await page.locator('#client-launch-refresh').click();
 await expect(page.locator('#client-installation-status')).toHaveText('Installed');
 await expect(page.locator('#client-launch')).toBeEnabled();
 await expect(page.locator('#client-launch-status')).toBeEmpty();
 expect(await records(gateway)).toEqual([]);
});

for(const id of ['codex','codex-cli','claude','opencode','zed','xcode-chat','xcode-codex','xcode-claude']) {
 test(`launcher prepares, opens and reuses the current ${id} profile`,async({page,gateway,request},testInfo)=>{
  await startProxy(page,gateway);
  const discovery=await request.get(new URL('/api/clients/launch',gateway.url).href,{headers:{Authorization:'Bearer '+gateway.token}});
  expect(discovery.ok()).toBe(true);
  const detected=(await discovery.json()).clients[id];
  expect(detected.available).toBe(true);expect(detected.path).toBeTruthy();
  const name=await choose(page,id),folder=path.join(gateway.root,'project with spaces');
  await mkdir(folder);
  if(id==='codex')await expect(page.locator('#client-launch-directory-field')).toBeHidden();
  else await page.locator('#client-launch-directory').fill(folder);
  let prepares=0;
  page.on('request',request=>{if(request.method()==='POST'&&new URL(request.url()).pathname==='/api/'+endpoint(id))prepares++;});
  await expect(page.locator('#client-launch')).toBeEnabled();
  await page.locator('#client-launch').click();
  await expect(page.locator('#client-launch-status')).toContainText('opened.');
  expect(await records(gateway)).toEqual([{client:id,directory:id==='codex'?gateway.root:folder,executable:detected.path,kind:['codex-cli','claude','opencode'].includes(id)?'terminal':'desktop'}]);
  expect(prepares).toBe(1);
  await page.locator('#client-launch').click();
  await expect.poll(async()=>(await records(gateway)).length).toBe(2);
  expect(prepares).toBe(1);
  await expect(page.locator('#client-launch')).toBeEnabled();
  await name.fill('Latest saved name');
  await page.locator('#client-launch').click();
  await expect.poll(async()=>(await records(gateway)).length).toBe(3);
  expect(prepares).toBe(2);
  await expect(page.locator('#client-launch-status')).not.toHaveClass(/error/);
  if(id==='zed')await expect(page.locator('#editor-next')).toContainText('Preparation saves the local key in the system credential store');
  if(id==='xcode-chat')await expect(page.locator('#xcode-guide')).toContainText('Add a Chat Provider');
  if(id==='codex'){
   await expect(page.locator('#client-launch')).toHaveText('Launch Codex Desktop');
   await page.locator('#client-launch-bar').screenshot({path:testInfo.outputPath('launcher-en-wide.png')});
   await page.locator('#language').selectOption('es');
   await page.setViewportSize({width:390,height:844});
   await expect(page.locator('#client-launch')).toHaveText('Abrir Codex Desktop');
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
   await page.locator('#client-launch-bar').screenshot({path:testInfo.outputPath('launcher-es-mobile.png')});
  }
 });
}

test('launcher uses a custom local Codex app independently of command export settings',async({page,gateway})=>{
 await control(gateway,{unavailable:'codex'});
 await startProxy(page,gateway);
 await choose(page,'codex');
 await expect(page.locator('#client-launch-status')).toContainText('Synthetic application unavailable');
 await expect(page.locator('#client-launch')).toBeDisabled();
 await page.locator('#desktop-platform').selectOption('windows');
 await page.locator('#desktop-app-path').fill('C:\\export-only\\Codex.exe');
 await page.locator('#client-launch-custom > summary').click();
 await page.locator('#client-launch-app').fill('/synthetic/custom-codex');
 await page.locator('#client-launch').click();
 await expect.poll(async()=>(await records(gateway)).length).toBe(1);
 expect((await records(gateway))[0]).toMatchObject({client:'codex',executable:'/synthetic/custom-codex',directory:gateway.root});
 await page.locator('#tab-generic').click();
 await expect(page.locator('#client-launch-bar')).toBeHidden();
 await page.locator('#tab-codex-cli').click();
 await expect(page.locator('#client-launch-directory')).toHaveValue(gateway.root);
});

test('launcher refuses stale selection after a delayed preparation and allows a fresh retry',async({page,gateway})=>{
 await startProxy(page,gateway);
 const name=await choose(page,'codex');
 await control(gateway,{holdPrepare:true});
 let prepares=0,launches=0;
 page.on('request',request=>{if(request.method()!=='POST')return;const url=new URL(request.url()).pathname;if(url==='/api/codex/catalog')prepares++;if(url==='/api/clients/launch')launches++;});
 await expect(page.locator('#client-launch')).toBeEnabled();
 await page.locator('#client-launch').click();
 await expect.poll(async()=>{try{return await readFile(gateway.prepareWaiting,'utf8');}catch{return '';}}).toBe('waiting');
 await expect(page.locator('#client-launch')).toBeDisabled();
 await name.fill('Changed during preparation');
 await control(gateway,{});
 await expect(page.locator('#client-launch-status')).toContainText('selection changed');
 expect(prepares).toBe(1);expect(launches).toBe(0);expect(await records(gateway)).toEqual([]);
 await page.locator('#client-launch').click();
 await expect.poll(async()=>(await records(gateway)).length).toBe(1);
 expect(prepares).toBe(2);expect(launches).toBe(1);
});

test('launcher stops after preparation errors and clears launch errors after retry',async({page,gateway})=>{
 await startProxy(page,gateway);
 await choose(page,'codex');
 await page.locator('#codex-manual-entry > summary').click();
 await page.locator('#codex-manual-id').fill('__invalid/catalog');
 await page.locator('#add-codex-model').click();
 await page.locator('#client-launch').click();
 await expect(page.locator('#client-launch-status')).toContainText('Invalid catalog');
 expect(await records(gateway)).toEqual([]);
 await page.locator('#model-picker input[value="__invalid/catalog"]').click();
 await control(gateway,{fail:true});
 await page.locator('#client-launch').click();
 await expect(page.locator('#client-launch-status')).toContainText('Could not open');
 expect(await records(gateway)).toEqual([]);
 await control(gateway,{});
 await page.locator('#client-launch').click();
 await expect.poll(async()=>(await records(gateway)).length).toBe(1);
 await expect(page.locator('#client-launch-status')).not.toHaveClass(/error/);
});

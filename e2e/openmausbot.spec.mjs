import {test, expect, startProxy} from './fixture.mjs';
import {readFile, writeFile} from 'node:fs/promises';

const library = () => ({schemaVersion:1,defaultModel:'anthropic/claude-sonnet-4.6',models:[
 {id:'vendor/one',displayName:'My coding model',contextPreset:'low',reasoningEffort:'high'},
 {id:'anthropic/claude-sonnet-4.6',displayName:'My default model',contextPreset:'recommended'},
]});
const headers = gateway => ({Authorization:'Bearer '+gateway.token});
async function getAPI(request,gateway,path) {
 const response=await request.get(new URL('/api/'+path,gateway.url).href,{headers:headers(gateway)});
 expect(response.ok()).toBe(true);return response.json();
}
async function saveLibrary(request,gateway,value) {
 const current=await getAPI(request,gateway,'model-library');
 const response=await request.put(new URL('/api/model-library',gateway.url).href,{headers:headers(gateway),data:{revision:current.revision,library:value}});
 expect(response.ok(),await response.text()).toBe(true);
}
async function records(gateway) {try{return JSON.parse(await readFile(gateway.launchRecords,'utf8'));}catch(error){if(error.code==='ENOENT')return [];throw error;}}

test('OpenMausBot uses shared models and prepares every desktop launch without a project or manual key',async({page,gateway,request},testInfo)=>{
 await saveLibrary(request,gateway,library());
 await startProxy(page,gateway);
 await page.locator('#tab-openmausbot').click();
 await expect(page.locator('#openmausbot-summary')).toContainText(library().defaultModel);
 await expect(page.locator('#openmausbot-models li')).toHaveText([library().defaultModel,'vendor/one']);
 await expect(page.locator('#client-launch-directory-field')).toBeHidden();
 await expect(page.locator('#client-launch-custom')).toBeHidden();
 expect(await page.locator('#openmausbot-helper input,#openmausbot-helper select').count()).toBe(0);
 let prepares=0;
 page.on('request',request=>{if(request.method()==='POST'&&new URL(request.url()).pathname==='/api/clients/openmausbot')prepares++;});
 for(let count=1;count<=2;count++){
  await expect(page.locator('#client-launch')).toBeEnabled();
  await page.locator('#client-launch').click();
  await expect.poll(async()=>(await records(gateway)).length).toBe(count);
  expect(prepares).toBe(count);
 }
 const launches=await records(gateway);
 expect(launches.every(record=>record.client==='openmausbot'&&record.kind==='desktop'&&record.directory===gateway.root)).toBe(true);
 const profile=await getAPI(request,gateway,'clients/openmausbot');
 expect(profile.configPath.startsWith(gateway.root)).toBe(true);
 expect(profile.modelCount).toBe(2);expect(profile.initialModel).toBe(library().defaultModel);
 const config=await readFile(profile.configPath,'utf8');
 expect(config).toContain('vendor/one');expect(config).not.toContain('synthetic-kilo-personal-key');
 await page.locator('#openmausbot-options').click();
 await expect(page.locator('#openmausbot-compatibility')).toContainText('does not apply custom names or reasoning levels');
 await expect(page.locator('#openmausbot-install')).toHaveAttribute('href','https://github.com/milind-soni/OpenMausBot/releases/latest');
 await page.locator('#openmausbot-helper').screenshot({path:testInfo.outputPath('openmausbot-en-wide.png')});
 await page.locator('#language').selectOption('es');
 await page.setViewportSize({width:390,height:844});
 await expect(page.locator('#openmausbot-compatibility')).toContainText('no aplica nombres personalizados ni niveles de razonamiento');
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
 await page.locator('#openmausbot-helper').screenshot({path:testInfo.outputPath('openmausbot-es-mobile.png')});
 expect(await page.evaluate(()=>window.__copied)).toEqual([]);
});

test('OpenMausBot explains missing models, credentials and installation without launching',async({page,gateway,request})=>{
 await page.locator('#tab-openmausbot').click();
 await expect(page.locator('#client-launch')).toBeDisabled();
 await expect(page.locator('#client-launch-status')).toContainText('Connect your selected provider first.');
 await startProxy(page,gateway);
 await expect(page.locator('#client-launch-status')).toContainText('Add and save models');
 await saveLibrary(request,gateway,library());
 await writeFile(gateway.launchControl,JSON.stringify({unavailable:'openmausbot'}));
 await page.locator('#client-launch-refresh').click();
 await expect(page.locator('#client-launch')).toBeDisabled();
 await expect(page.locator('#client-launch-status')).toContainText('Synthetic application unavailable');
 await page.locator('#openmausbot-options').click();
 await expect(page.locator('#openmausbot-install')).toBeVisible();
 expect(await records(gateway)).toEqual([]);
 await writeFile(gateway.launchControl,'{}');
 await page.locator('#client-launch-refresh').click();
 await expect(page.locator('#client-launch')).toBeEnabled();
});

test('OpenMausBot stops stale preparation and refreshes an externally changed shared library',async({page,gateway,request})=>{
 await saveLibrary(request,gateway,library());
 await startProxy(page,gateway);
 await page.locator('#tab-openmausbot').click();
 await expect(page.locator('#client-launch')).toBeEnabled();
 let release,entered=false;const gate=new Promise(resolve=>{release=resolve;});
 await page.route('**/api/clients/openmausbot',async route=>{
  if(route.request().method()==='POST'){entered=true;await gate;}
  await route.continue();
 });
 try {
  await page.locator('#client-launch').click();
  await expect.poll(()=>entered).toBe(true);
  const changed=library();changed.defaultModel='vendor/one';
  await saveLibrary(request,gateway,changed);
  release();
  await expect(page.locator('#client-launch-status')).toContainText('shared models changed while preparing');
  expect(await records(gateway)).toEqual([]);
  await expect(page.locator('#openmausbot-summary')).toContainText('vendor/one');
  await expect(page.locator('#client-launch')).toBeEnabled();
  await page.locator('#client-launch').click();
  await expect.poll(async()=>(await records(gateway)).length).toBe(1);
 }finally{release();}
});

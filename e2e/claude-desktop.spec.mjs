import {test,expect,startProxy,state} from './fixture.mjs';
import {readFile} from 'node:fs/promises';

const first='vendor/one',second='anthropic/claude-sonnet-4.6',third='anthropic/claude-opus-4.6';
const choose=(page,id)=>page.locator(`[data-editor-id="${id}"]`);
const name=(page,id)=>page.locator(`[data-editor-name="${id}"]`);
const headers=gateway=>({Authorization:'Bearer '+gateway.token});
async function records(gateway){try{return JSON.parse(await readFile(gateway.launchRecords,'utf8'));}catch(error){if(error.code==='ENOENT')return [];throw error;}}
async function saved(request,gateway){const response=await request.get(new URL('/api/claude-desktop/profile',gateway.url).href,{headers:headers(gateway)});expect(response.ok()).toBe(true);return response.json();}
async function seedLibrary(request,gateway){
 const url=new URL('/api/model-library',gateway.url).href;
 const current=await (await request.get(url,{headers:headers(gateway)})).json();
 const response=await request.put(url,{headers:headers(gateway),data:{revision:current.revision,library:{schemaVersion:1,defaultModel:first,models:[{id:first,displayName:'Shared One',contextPreset:'recommended'},{id:second,displayName:'Shared Claude',contextPreset:'recommended'},{id:third,displayName:'Shared Opus',contextPreset:'recommended'}]}}});
 expect(response.ok()).toBe(true);
}

test('Claude Desktop starts from shared models and prepares a named configuration separately from Claude Code',async({page,gateway,request},testInfo)=>{
 await seedLibrary(request,gateway);
 await page.locator('#tab-claude-desktop').click();
 await expect(page.locator('#editor-title')).toHaveText('Claude Desktop · select and prepare');
 await expect(choose(page,first)).toHaveCount(0);await expect(choose(page,second)).toBeChecked();await expect(choose(page,third)).toBeChecked();
 await expect(name(page,second)).toHaveValue('Shared Claude');
 await expect(page.locator(`[data-editor-initial="${second}"]`)).toHaveAttribute('aria-pressed','true');
 await expect(page.locator('#client-launch-directory-field')).toBeHidden();
 for(const id of ['editor-copy','editor-export','editor-preview','editor-shell','claude-models'])await expect(page.locator('#'+id)).toBeHidden();
 await expect(page.locator('#editor-limit-note')).toContainText('uses Claude models by default');
 await expect(page.locator('#editor-limit-note')).toContainText('1 shared model omitted');
 await expect(page.locator('#editor-limit-note')).toContainText('first Claude model');
 await expect(page.locator('#editor-picker .context-policy')).toHaveCount(0);
 await expect(page.locator('#editor-picker input[type=number]')).toHaveCount(0);
 await expect(page.locator('[data-editor-effort]')).toHaveCount(0);
 await name(page,second).fill('Desktop Claude');
 await page.locator(`[data-editor-initial="${third}"]`).click();
 await page.locator('#editor-save').click();
 await expect(page.locator('#editor-status')).toContainText('Configuration saved:');
 const result=await saved(request,gateway);
 expect(result.selection.initial).toBe(third);
 expect(result.selection.models).toHaveLength(2);
 expect(result.selection.models[0].name).toBe('Desktop Claude');
 expect(result.selection.models.every(model=>!model.contextWindow&&!model.maxOutputTokens)).toBe(true);
 expect(result.configPath.startsWith(gateway.root)).toBe(true);
 const raw=await readFile(result.configPath,'utf8');
 expect(raw).not.toContain(first);expect(raw).toContain(second);expect(raw).toContain(third);
 const shared=await (await request.get(new URL('/api/model-library',gateway.url).href,{headers:headers(gateway)})).json();
 expect(shared.library.defaultModel).toBe(first);expect(shared.library.models).toHaveLength(3);
 expect(shared.library.models.find(model=>model.id===second).displayName).toBe('Shared Claude');
 await page.locator('#tab-claude').click();await expect(page.locator('#claude-models')).toBeVisible();
 await expect(page.locator('#client-launch-directory-field')).toBeVisible();
 await page.locator('#tab-claude-desktop').click();await expect(name(page,second)).toHaveValue('Desktop Claude');
 await page.locator('#language').selectOption('es');
 await expect(page.locator('#editor-title')).toHaveText('Claude Desktop · selecciona y prepara');
 await expect(page.locator('#editor-limit-note')).toContainText('usa modelos Claude por defecto');
 await page.setViewportSize({width:390,height:844});
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
 await page.locator('#editor-helper').screenshot({path:testInfo.outputPath('claude-desktop-es-mobile.png')});
});

test('Claude Desktop prepares on launch, ignores project folders and starts the proxy',async({page,gateway,request})=>{
 await seedLibrary(request,gateway);
 await startProxy(page,gateway);await page.locator('#start-stop').click();
 await expect(page.locator('#status-label')).toHaveText('Proxy stopped');
 await page.locator('#tab-opencode').click();await page.locator('#client-launch-directory').fill('/missing/irrelevant-project');
 await page.locator('#tab-claude-desktop').click();
 await expect(page.locator('#client-launch')).toHaveText('Launch Claude Desktop');
 await expect(page.locator('#client-launch')).toBeEnabled();
 let prepares=0,launchBody;
 page.on('request',req=>{if(req.method()!=='POST')return;const pathname=new URL(req.url()).pathname;if(pathname==='/api/claude-desktop/profile')prepares++;if(pathname==='/api/clients/launch')launchBody=req.postDataJSON();});
 await page.locator('#client-launch').click();
 await expect.poll(async()=>(await records(gateway)).length).toBe(1);
 expect(launchBody).toEqual({client:'claude-desktop'});
 expect((await records(gateway))[0].client).toBe('claude-desktop');
 expect(prepares).toBe(1);expect((await state(request,gateway)).running).toBe(true);
 await page.locator('#client-launch').click();await expect.poll(async()=>(await records(gateway)).length).toBe(2);
 expect(prepares).toBe(2);
 await name(page,second).fill('Updated Desktop');await page.locator('#client-launch').click();
 await expect.poll(async()=>(await records(gateway)).length).toBe(3);
 expect((await saved(request,gateway)).selection.models[0].name).toBe('Updated Desktop');
 await expect(page.locator('#client-launch-status')).not.toHaveClass(/error/);
});

test('Claude Desktop experimental option persists and derives real provider models without changing the shared library',async({page,gateway,request},testInfo)=>{
 await seedLibrary(request,gateway);
 const libraryURL=new URL('/api/model-library',gateway.url).href,optionsURL=new URL('/api/claude-desktop/options',gateway.url).href;
 const sharedBefore=await (await request.get(libraryURL,{headers:headers(gateway)})).json();
 let prepares=0,launches=0,inferences=0,optionWrites=[];
 page.on('request',req=>{
  if(req.method()!=='POST')return;
  const path=new URL(req.url()).pathname;
  if(path==='/api/claude-desktop/profile')prepares++;
  if(path==='/api/clients/launch')launches++;
  if(['/v1/messages','/v1/responses','/v1/chat/completions'].includes(path))inferences++;
  if(path==='/api/claude-desktop/options')optionWrites.push(req.postDataJSON());
 });
 await page.locator('#tab-claude-desktop').click();
 const toggle=page.getByRole('checkbox',{name:'Experimental: use models from other providers',exact:true});
 await expect(toggle).toBeEnabled();await expect(toggle).not.toBeChecked();
 await expect(choose(page,first)).toHaveCount(0);
 await name(page,second).fill('My Desktop Claude');
 await toggle.check();
 await expect(toggle).toBeEnabled();await expect(toggle).toBeChecked();
 await expect(choose(page,first)).toBeChecked();
 await expect(name(page,first)).toHaveValue('Shared One');
 await expect(name(page,second)).toHaveValue('My Desktop Claude');
 await expect(page.locator(`[data-editor-initial="${first}"]`)).toHaveAttribute('aria-pressed','true');
 await expect(page.locator('#editor-limit-note')).toContainText('Experimental models:');
 await expect(page.locator('#editor-limit-note')).toContainText('Preparation does not verify inference');
 await expect(page.locator('#editor-picker')).not.toContainText('claude-kilo-v1-');
 expect((await (await request.get(optionsURL,{headers:headers(gateway)})).json()).experimentalModels).toBe(true);
 expect((await state(request,gateway)).claudeDesktopExperimentalModels).toBe(true);
 expect(optionWrites).toEqual([{experimentalModels:true}]);
 expect({prepares,launches,inferences}).toEqual({prepares:0,launches:0,inferences:0});
 expect((await state(request,gateway)).running).toBe(false);
 await page.locator('#editor-manual-title').click();
 for(const id of ['claude-kilo-v1-123','anthropic/claude-kilo-v1-123']){
  await page.locator('#editor-id').fill(id);await expect(page.locator('#editor-add')).toBeDisabled();
 }
 await page.locator('#editor-id').fill('');
 await page.locator('#editor-save').click();
 await expect(page.locator('#editor-status')).toContainText('Configuration saved:');
 const configured=await saved(request,gateway);
 expect(configured.selection.initial).toBe(first);
 expect(configured.selection.models.map(model=>model.id)).toEqual([first,second,third]);
 expect(configured.selection.models.find(model=>model.id===second).name).toBe('My Desktop Claude');
 expect(configured.selection.models.every(model=>!model.contextWindow&&!model.maxOutputTokens)).toBe(true);
 expect((await state(request,gateway)).running).toBe(false);
 await toggle.uncheck();await expect(toggle).toBeEnabled();await expect(choose(page,first)).toHaveCount(0);
 await expect(name(page,second)).toHaveValue('My Desktop Claude');
 await expect(page.locator(`[data-editor-initial="${second}"]`)).toHaveAttribute('aria-pressed','true');
 await expect(page.locator('#editor-status')).not.toContainText('Configuration saved:');
 expect({prepares,launches,inferences}).toEqual({prepares:1,launches:0,inferences:0});
 const sharedAfter=await (await request.get(libraryURL,{headers:headers(gateway)})).json();
 expect(sharedAfter).toEqual(sharedBefore);
 await toggle.check();await expect(toggle).toBeEnabled();await expect(choose(page,first)).toBeChecked();
 await expect(name(page,second)).toHaveValue('My Desktop Claude');
 await page.reload();await page.locator('#tab-claude-desktop').click();
 await expect(toggle).toBeEnabled();await expect(toggle).toBeChecked();await expect(choose(page,first)).toBeChecked();
 await expect(page.locator(`[data-editor-initial="${first}"]`)).toHaveAttribute('aria-pressed','true');
 await page.locator('#language').selectOption('es');
 await expect(page.getByRole('checkbox',{name:'Experimental: usar modelos de otros proveedores',exact:true})).toBeChecked();
 await expect(page.locator('#editor-limit-note')).toContainText('Modelos experimentales:');
 await expect(page.locator('#editor-limit-note')).toContainText('Preparar no verifica la inferencia');
 await page.setViewportSize({width:390,height:844});
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
 await page.locator('#editor-helper').screenshot({path:testInfo.outputPath('claude-desktop-experimental-es-mobile.png')});
 expect(optionWrites).toEqual([{experimentalModels:true},{experimentalModels:false},{experimentalModels:true}]);
 expect({prepares,launches,inferences}).toEqual({prepares:1,launches:0,inferences:0});
});

test('Claude Desktop ignores a delayed pre-toggle state and follows later external option changes',async({page,gateway,request})=>{
 await seedLibrary(request,gateway);
 await page.locator('#tab-claude-desktop').click();
 const toggle=page.locator('#claude-desktop-experimental');
 await expect(toggle).toBeEnabled();await expect(toggle).not.toBeChecked();
 let releaseState,staleCaptured=false,staleDelivered=false,optionReads=0;
 const release=new Promise(resolve=>releaseState=resolve);
 page.on('requestfinished',req=>{if(req.method()==='GET'&&new URL(req.url()).pathname==='/api/claude-desktop/options')optionReads++;});
 await page.route('**/api/state',async route=>{
  if(staleCaptured){await route.continue();return;}
  const response=await route.fetch(),body=await response.json();
  staleCaptured=true;
  await release;
  await route.fulfill({response,json:body});staleDelivered=true;
 });
 try{
  await expect.poll(()=>staleCaptured).toBe(true);
  await toggle.check();await expect(toggle).toBeEnabled();await expect(choose(page,first)).toBeChecked();
  await page.locator(`[data-editor-initial="${third}"]`).click();
  releaseState();
  await expect.poll(()=>staleDelivered).toBe(true);
  await expect.poll(()=>optionReads).toBeGreaterThan(0);
  await expect(toggle).toBeChecked();await expect(choose(page,first)).toBeChecked();
  await expect(page.locator(`[data-editor-initial="${third}"]`)).toHaveAttribute('aria-pressed','true');
  expect((await state(request,gateway)).claudeDesktopExperimentalModels).toBe(true);
  const optionsURL=new URL('/api/claude-desktop/options',gateway.url).href;
  const changed=await request.post(optionsURL,{headers:headers(gateway),data:{experimentalModels:false}});
  expect(changed.ok()).toBe(true);
  await expect(toggle).not.toBeChecked();await expect(choose(page,first)).toHaveCount(0);
  await expect(page.locator(`[data-editor-initial="${second}"]`)).toHaveAttribute('aria-pressed','true');
  expect((await records(gateway))).toHaveLength(0);
  expect((await state(request,gateway)).running).toBe(false);
 }finally{releaseState();await page.unroute('**/api/state');}
});

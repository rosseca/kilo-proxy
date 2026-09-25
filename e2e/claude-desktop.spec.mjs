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
 await expect(page.locator('#editor-limit-note')).toContainText('currently accepts Claude models only');
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
 await expect(page.locator('#editor-limit-note')).toContainText('actualmente solo acepta modelos Claude');
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

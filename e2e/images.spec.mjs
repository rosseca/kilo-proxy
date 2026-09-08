import {test,expect,startProxy,state} from './fixture.mjs';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import path from 'node:path';

const coding='vendor/one',imageModel='image-lab/painter';
// The preserving TOML editor may use table headers or equivalent dotted keys.
const imageTable=/\[mcp_servers\.kilo_images\]|mcp_servers\.kilo_images\./;
async function imageCatalog(page,gateway){
  await writeFile(gateway.launchControl,JSON.stringify({imageModels:true}));
  await page.locator('#load-models').click();
  await expect(page.locator('#codex-image-model option[value="'+imageModel+'"]')).toHaveCount(1);
}
async function chooseImages(page,gateway,client='codex'){
  await page.locator('#tab-'+client).click();
  await imageCatalog(page,gateway);
  await page.locator(`[data-focus="model:${coding}"]`).check();
  await page.locator('#codex-image-enabled').check();
  await page.locator('#codex-image-model').selectOption(imageModel);
}
for(const client of ['codex','codex-cli'])test(`optional image MCP prepares and reloads independently of coding models in ${client}`,async({page,gateway,request},testInfo)=>{
  const dir=gateway.profiles[client];
  await mkdir(dir,{recursive:true});
  await writeFile(path.join(dir,'config.toml'),'approval_policy = "on-request"\n[mcp_servers.keep_me]\nurl = "http://127.0.0.1:1/unrelated"\n');
  await startProxy(page,gateway);
  await page.locator('#tab-'+client).click();
  await imageCatalog(page,gateway);
  await page.locator(`[data-focus="model:${coding}"]`).check();
  await expect(page.locator(`[data-focus="model:${imageModel}"]`)).toHaveCount(0);
  await expect(page.locator('#codex-image-model option')).toHaveCount(2);
  await page.locator('#codex-image-enabled').check();
  await expect(page.locator('#save-codex-catalog')).toBeDisabled();
  await expect(page.locator('#client-launch')).toBeDisabled();
  await expect(page.locator('#codex-image-status')).toContainText('Choose an image model');
  await page.locator('#codex-image-model').selectOption(imageModel);
  await page.locator(client==='codex-cli'?'#client-launch':'#save-codex-catalog').click();
  await expect(page.locator('#codex-setup-status')).toContainText('Profile ready');
  const config=await readFile(path.join(dir,'config.toml'),'utf8');
  expect(config.replaceAll('"','')).toMatch(imageTable);
  expect(config).toContain('/mcp/images');
  expect(config).toMatch(/bearer_token_env_var"?\s*=\s*['"]KILO_LOCAL_API_KEY['"]/);
  expect(config).toMatch(/tool_timeout_sec"?\s*=\s*360/);
  expect(config).toContain('[mcp_servers.keep_me]');
  expect(config).toContain('approval_policy = "on-request"');
  const catalog=JSON.parse(await readFile(path.join(dir,'models.json'),'utf8'));
  expect(catalog.models.map(model=>model.slug)).toEqual([coding]);
  expect((await state(request,gateway)).imageGeneration).toEqual({enabled:true,model:imageModel});
  if(client==='codex')await page.locator('#codex-image-generation').screenshot({path:testInfo.outputPath('codex-images-en.png')});
  await page.reload();
  await page.locator('#tab-'+client).click();
  await expect(page.locator('#codex-image-enabled')).toBeChecked();
  await expect(page.locator('#codex-image-model')).toHaveValue(imageModel);
  if(client==='codex'){
    await page.locator('#language').selectOption('es');
    await expect(page.locator('#codex-image-model')).toHaveAccessibleName('Modelo de imágenes');
    await expect(page.locator('#codex-image-enabled')).toHaveAccessibleName('Activar generación de imágenes');
    await expect(page.locator('#codex-image-model')).toHaveValue(imageModel);
    await page.locator('#codex-image-generation').screenshot({path:testInfo.outputPath('codex-images-es.png')});
    await page.locator('#language').selectOption('en');
  }
  await page.locator('#load-codex-catalog').click();
  await expect(page.locator(`[data-focus="model:${coding}"]`)).toBeChecked();
  await page.locator('#codex-image-enabled').uncheck();
  await page.locator('#save-codex-catalog').click();
  await expect(page.locator('#codex-setup-status')).toContainText('Profile ready');
  const disabled=await readFile(path.join(dir,'config.toml'),'utf8');
  expect(disabled.replaceAll('"','')).not.toMatch(imageTable);
  expect(disabled).toContain('[mcp_servers.keep_me]');
  expect((await state(request,gateway)).imageGeneration.enabled).toBe(false);
});

test('image helper explains an empty catalog and retains an unavailable saved model for replacement',async({page,gateway,request})=>{
  await page.locator('#tab-codex').click();
  await page.locator(`[data-focus="model:${coding}"]`).check();
  await page.locator('#codex-image-enabled').check();
  await expect(page.locator('#save-codex-catalog')).toBeDisabled();
  await expect(page.locator('#codex-image-model option')).toHaveCount(1);
  await expect(page.locator('#codex-image-status')).not.toBeEmpty();
  await imageCatalog(page,gateway);
  await page.locator('#codex-image-model').selectOption(imageModel);
  await page.locator('#save-codex-catalog').click();
  await expect(page.locator('#codex-setup-status')).toContainText('Profile ready');
  await writeFile(gateway.launchControl,'{}');
  await page.locator('#load-models').click();
  await expect(page.locator('#codex-image-model')).toHaveValue(imageModel);
  await expect(page.locator('#codex-image-status')).toContainText('unavailable');
  await expect(page.locator('#save-codex-catalog')).toBeDisabled();
  expect((await state(request,gateway)).imageGeneration).toEqual({enabled:true,model:imageModel});
  await page.locator('#codex-image-enabled').uncheck();
  await expect(page.locator('#save-codex-catalog')).toBeEnabled();
});

test('image MCP generates and edits a synthetic PNG through the organization gateway with usage',async({page,gateway,request})=>{
  await startProxy(page,gateway);
  await chooseImages(page,gateway);
  await page.locator('#save-codex-catalog').click();
  await expect(page.locator('#codex-setup-status')).toContainText('Profile ready');
  const local=await state(request,gateway),endpoint=gateway.baseURL.replace(/\/v1$/,'')+'/mcp/images';
  const rpc=async(id,method,params={})=>{
    const response=await request.post(endpoint,{headers:{Authorization:'Bearer '+local.localKey,Accept:'application/json, text/event-stream'},data:{jsonrpc:'2.0',id,method,params}});
    expect(response.status()).toBe(200);const body=await response.json();expect(body.error).toBeUndefined();return body.result;
  };
  expect((await request.post(endpoint,{data:{jsonrpc:'2.0',id:0,method:'tools/list'}})).status()).toBe(401);
  const initialized=await rpc(1,'initialize',{protocolVersion:'2025-11-25',capabilities:{},clientInfo:{name:'synthetic-image-e2e',version:'1'}});
  expect(initialized.protocolVersion).toBe('2025-11-25');
  const tools=await rpc(2,'tools/list');
  expect(tools.tools.map(tool=>tool.name)).toContain('generate_image');
  const generated=await rpc(3,'tools/call',{name:'generate_image',arguments:{prompt:'SYNTHETIC_IMAGE_PROMPT'}});
  expect(generated.isError).not.toBe(true);
  const info=generated.structuredContent;
  expect(info.model).toBe(imageModel);expect(Number(info.costUSD)).toBe(0.0042);
  expect(info.images).toHaveLength(1);
  expect(info.images[0]).toMatchObject({mimeType:'image/png',width:1,height:1});
  const file=info.images[0].path;
  expect(path.dirname(file)).toBe(path.join(gateway.root,'app','generated-images'));
  expect((await readFile(file)).toString('base64')).toBe(gateway.imageBase64);
  expect(generated.content.find(item=>item.type==='image')).toMatchObject({mimeType:'image/png',data:gateway.imageBase64});
  const edited=await rpc(4,'tools/call',{name:'generate_image',arguments:{prompt:'SYNTHETIC_IMAGE_EDIT',reference_image:file}});
  expect(edited.isError).not.toBe(true);
  expect(edited.structuredContent.images[0].path).not.toBe(file);
  const requests=JSON.parse(await readFile(gateway.imageRecords,'utf8'));
  expect(requests).toHaveLength(2);
  expect(requests[0]).toMatchObject({model:imageModel,modalities:['image','text'],stream:false});
  expect(requests[0].messages[0].content).toContainEqual({type:'text',text:'SYNTHETIC_IMAGE_PROMPT'});
  expect(requests[1].messages[0].content).toContainEqual({type:'image_url',image_url:{url:'data:image/png;base64,'+gateway.imageBase64}});
  const rejected=await rpc(5,'tools/call',{name:'generate_image',arguments:{prompt:'REJECT_EXTERNAL_FILE',reference_image:path.join(gateway.profiles.codex,'config.toml')}});
  expect(rejected.isError).toBe(true);
  expect(JSON.parse(await readFile(gateway.imageRecords,'utf8'))).toHaveLength(2);
  await expect(page.locator('#spend-total')).toHaveText('$0.008400');
});

import {test,expect,startProxy,state as readState} from './fixture.mjs';

async function subscriptionFixture(page,gateway){
 const base=await readState(page.request,gateway);
 const fixture={account:{connected:false,status:'idle'},posts:[],opened:0};
 const snapshot=real=>({...real,chatgpt:fixture.account,chatgptReady:fixture.account.connected===true&&fixture.account.status!=='pending',connectionReady:real.connectionReady||fixture.account.connected===true&&fixture.account.status!=='pending',catalogRevision:(real.catalogRevision||0)+(fixture.account.connected?100:0)});
 await page.context().route('https://auth.openai.com/**',route=>{fixture.opened++;return route.fulfill({contentType:'text/html',body:'<p>Local authorization test fixture</p>'});});
 await page.route('**/api/state',async route=>{const response=await route.fetch();await route.fulfill({json:snapshot(await response.json())});});
 await page.route('**/api/chatgpt/*',async route=>{
  const action=new URL(route.request().url()).pathname.split('/').at(-1);fixture.posts.push(action);
  if(action==='login')fixture.account={connected:false,status:'pending'};
  if(action==='cancel'||action==='logout')fixture.account={connected:false,status:'idle'};
  if(action==='refresh')fixture.account={...fixture.account,quota:{primary:{usedPercent:35,windowDurationMins:300}}};
  await route.fulfill({json:snapshot(base)});
 });
 await page.route('**/api/models',async route=>{const response=await route.fetch(),data=await response.json();await route.fulfill({json:{...data,revision:snapshot(base).catalogRevision,models:[...(data.models||[]),...(fixture.account.connected?[{id:'chatgpt/gpt-5',name:'GPT-5 · ChatGPT',contextWindow:128000,maxOutputTokens:32000,tools:true,inputModalities:['text','image']}]:[])]}});});
 await expect(page.locator('#chatgpt-account')).toBeVisible();return fixture;
}

for(const language of ['en','es'])test(`ChatGPT device login coexists with Kilo and quota is independent (${language})`,async({page,gateway},testInfo)=>{
 await startProxy(page,gateway);await page.locator('#start-stop').click();await expect(page.locator('#status-label')).toHaveText('Proxy stopped');
 if(language==='es')await page.locator('#language').selectOption('es');
 const fixture=await subscriptionFixture(page,gateway);
 await page.locator('#chatgpt-login').click();
 await expect(page.locator('#chatgpt-pending')).toBeVisible();await expect(page.locator('#chatgpt-verify')).toBeHidden();
 expect(fixture.opened).toBe(0);
 fixture.account={...fixture.account,code:'ABCD-1234',verificationUrl:'https://auth.openai.com/codex/device'};
 await expect(page.locator('#chatgpt-code')).toHaveText('ABCD-1234');
 await expect.poll(()=>fixture.opened).toBe(1);
 await expect(page.locator('#sso-login')).toBeVisible();await expect(page.locator('#org-id')).toHaveValue('e2e-team');
 fixture.account={connected:true,status:'idle',email:'person@example.test',plan:'Plus',quota:{primary:{usedPercent:25,windowDurationMins:300},secondary:{usedPercent:null}}};
 await expect(page.locator('#chatgpt-identity')).toContainText('person@example.test');
 await expect(page.locator('#subscription-usage-title')).toBeVisible();
 await expect(page.locator('#account-balance')).toBeVisible();
 await expect(page.locator('#subscription-usage')).toContainText(language==='en'?'25% used':'25% usado');
 await expect(page.locator('#subscription-usage')).not.toContainText('$');
 await expect(page.locator('#provider-select')).toHaveCount(0);
 await page.locator('#chatgpt-account').screenshot({path:testInfo.outputPath('chatgpt-account-wide.png')});
 await page.locator('#subscription-usage').screenshot({path:testInfo.outputPath('chatgpt-quota-wide.png')});
 await page.setViewportSize({width:780,height:700});
 await page.locator('#chatgpt-account').screenshot({path:testInfo.outputPath('chatgpt-account-narrow.png')});
 await page.locator('#subscription-usage').screenshot({path:testInfo.outputPath('chatgpt-quota-narrow.png')});
 await page.locator('#refresh-chatgpt').click();await expect(page.locator('#subscription-usage')).toContainText(language==='en'?'35% used':'35% usado');
 await page.locator('#chatgpt-logout').click();await expect(page.locator('#chatgpt-login')).toBeVisible();
 await expect(page.locator('#org-id')).toHaveValue('e2e-team');await expect(page.locator('#account-balance')).toBeVisible();await expect(page.locator('#subscription-usage')).toHaveCount(0);
 expect(fixture.posts).toEqual(['login','refresh','logout']);
});

test('subscription-only onboarding needs no Kilo organization and rejects foreign authorization links',async({page,gateway})=>{
 const fixture=await subscriptionFixture(page,gateway);
 await page.locator('#chatgpt-login').click();fixture.account={connected:false,status:'pending',code:'BAD',verificationUrl:'https://evil.invalid/codex/device'};
 await expect(page.locator('#chatgpt-code')).toHaveText('BAD');await expect(page.locator('#chatgpt-verify')).toBeHidden();expect(fixture.opened).toBe(0);
 await page.locator('#chatgpt-cancel').click();await expect(page.locator('#chatgpt-login')).toBeVisible();
 fixture.account={connected:true,status:'idle',quota:{primary:{usedPercent:null},secondary:{usedPercent:null}}};
 await expect(page.locator('#chatgpt-identity')).toContainText('ChatGPT connected');
 await expect(page.locator('#org-id')).toHaveValue('');await expect(page.locator('#org-id')).not.toHaveAttribute('required');
 await expect(page.locator('#subscription-usage')).toContainText('Usage limits unavailable');await expect(page.locator('#subscription-usage')).not.toContainText('0%');
 await expect(page.locator('#start-stop')).toBeEnabled();
 await page.locator('#tab-opencode').click();await expect(page.locator('#editor-picker')).toContainText('GPT-5 · ChatGPT');
 await expect(page.locator('#editor-picker input[data-editor-id="chatgpt/gpt-5"]')).toBeVisible();
 const model=page.locator('#editor-picker .codex-model-entry').filter({has:page.locator('input[data-editor-id="chatgpt/gpt-5"]')});
 await expect(model.locator('.model-option-prices')).toHaveText('Subscription quota');
 await expect(model).not.toContainText('USD / 1M');
 await page.locator('#language').selectOption('es');
 await expect(model.locator('.model-option-prices')).toHaveText('Cuota de suscripción');
 expect(fixture.posts).toEqual(['login','cancel']);
});

import {test,expect} from './fixture.mjs';

const first='vendor/one',second='anthropic/claude-sonnet-4.6';
const longID='future-lab/an-experimental-coding-model-with-a-very-long-unbroken-identifier-and-context-window-2026';
const longName='An exceptionally long model name that still stays readable inside its own card';

async function geometry(page,picker,columns){
  const result=await picker.evaluate(grid=>{
    const cards=[...grid.children].filter(el=>el.classList.contains('codex-model-entry'));
    const bounds=cards.map(card=>{const r=card.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height};});
    const content=[...grid.querySelectorAll('.model-option-title,.model-option-prices,.codex-row-controls')];
    return {columns:getComputedStyle(grid).gridTemplateColumns.split(' ').length,bounds,overflow:content.filter(el=>el.scrollWidth>el.clientWidth+1).length};
  });
  expect(result.columns).toBe(columns);
  expect(result.bounds).toHaveLength(3);
  expect(result.overflow).toBe(0);
  const [a,b,c]=result.bounds;
  expect(a.width).toBeGreaterThan(240);
  expect(a.width).toBeLessThan(400);
  if(columns===3){expect(Math.abs(a.y-b.y)).toBeLessThan(1);expect(Math.abs(b.y-c.y)).toBeLessThan(1);expect(b.x).toBeGreaterThan(a.x+a.width);expect(c.x).toBeGreaterThan(b.x+b.width);}
  if(columns===2){expect(Math.abs(a.y-b.y)).toBeLessThan(1);expect(c.y).toBeGreaterThan(a.y+a.height);expect(Math.abs(c.x-a.x)).toBeLessThan(1);}
  if(columns===1){expect(b.y).toBeGreaterThan(a.y+a.height);expect(c.y).toBeGreaterThan(b.y+b.height);expect(Math.abs(c.x-a.x)).toBeLessThan(1);}
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
}

for(const language of ['en','es'])for(const client of ['codex','opencode','xcode']){
  test(`model card grid ${client} ${language} preserves long names, defaults and filters across 3, 2 and 1 columns`,async({page},testInfo)=>{
    await page.setViewportSize({width:1180,height:1000});
    await page.locator('#language').selectOption(language);
    await page.locator('#tab-'+client).click();
    if(client==='xcode')await page.locator('[data-xcode-variant="codex"]').click();
    const prefix=client==='opencode'?'editor':client==='xcode'?'xcode':'model';
    const picker=page.locator('#'+prefix+'-picker');
    const choice=id=>page.locator(client==='opencode'?`[data-editor-id="${id}"]`:client==='xcode'?`[data-xcode-focus="choose:${id}"]`:`[data-focus="model:${id}"]`);
    const name=id=>page.locator(client==='opencode'?`[data-editor-name="${id}"]`:client==='xcode'?`[data-xcode-focus="name:${id}"]`:`[data-focus="name:${id}"]`);
    const initial=id=>page.locator(client==='opencode'?`[data-editor-initial="${id}"]`:client==='xcode'?`[data-xcode-focus="initial:${id}"]`:`[data-focus="default:${id}"]`);
    await choice(first).check();await choice(second).check();
    if(client==='codex')await page.locator('#codex-manual-entry summary').click();
    if(client==='opencode')await page.locator('#editor-manual-title').click();
    await page.locator(client==='codex'?'#codex-manual-id':'#'+prefix+'-id').fill(longID);
    await page.locator(client==='codex'?'#add-codex-model':'#'+prefix+'-add').click();
    await name(longID).fill(longName);
    await initial(longID).click();
    await page.locator('#'+prefix+'-sort').selectOption('price');
    await expect(picker.locator('.model-price-label')).toHaveText(Array(3).fill(language==='en'?['Input','Output']:['Entrada','Salida']).flat());
    await expect(picker.locator('.model-price-unit')).toHaveText(Array(3).fill('USD / 1M tokens'));
    await expect(picker.locator('.model-option-title strong').filter({hasText:longName})).toHaveText(longName);
    await expect(picker.locator('.model-option-title small').filter({hasText:longID})).toHaveText(longID);
    await geometry(page,picker,3);
    await picker.scrollIntoViewIfNeeded();
    await page.screenshot({path:testInfo.outputPath(`grid-${client}-${language}-wide.png`)});
    await page.locator('#'+prefix+'-lab').selectOption('vendor');
    await expect(choice(longID)).toHaveCount(0);
    await page.locator('#'+prefix+'-lab').selectOption('');
    await expect(choice(longID)).toBeChecked();
    await expect(name(longID)).toHaveValue(longName);
    await expect(initial(longID)).toHaveAttribute('aria-pressed','true');
    for(const [width,columns]of [[960,2],[390,1]]){
      await page.setViewportSize({width,height:1000});
      await geometry(page,picker,columns);
      await expect(choice(longID)).toBeChecked();
      await expect(name(longID)).toHaveValue(longName);
      await expect(initial(longID)).toHaveAttribute('aria-pressed','true');
    }
    await picker.scrollIntoViewIfNeeded();
    await page.screenshot({path:testInfo.outputPath(`grid-${client}-${language}-mobile.png`)});
  });
}

test('mixed model cards keep column hit targets and lower-row edits through scrolling and resize',async({page,gateway,request})=>{
  // Seed a larger saved profile through the actual backend. The gateway's two
  // catalog entries stay unselected, so selected and unselected cards coexist.
  const {codexCatalog}=await import('../ui/codex-catalog.mjs');
  const saved=Array.from({length:7},(_,i)=>({id:`zz-lab/model-${i}`,displayName:`ZZ Saved Model ${i}`,reasoningLevels:['low','high'],defaultReasoning:'low'}));
  const response=await request.post(new URL('/api/codex/catalog',gateway.url).href,{headers:{Authorization:'Bearer '+gateway.token},data:{catalog:codexCatalog(saved,saved[0].id)}});
  expect(response.ok()).toBe(true);
  await page.setViewportSize({width:1180,height:1000});
  await page.locator('#tab-codex').click();
  await page.locator('#load-codex-catalog').click();
  await expect(page.locator('#model-picker .is-selected')).toHaveCount(7);
  await page.locator('#codex-selected-only').uncheck();
  await page.locator('#model-sort').selectOption('name');
  const picker=page.locator('#model-picker'),cards=picker.locator(':scope>.codex-model-entry');
  await expect(cards).toHaveCount(9);
  await expect(cards.nth(1).locator('input[name="model-choice"]')).toHaveValue(first);
  const a=await cards.first().boundingBox(),b=await cards.nth(1).boundingBox();
  expect(Math.abs(a.y-b.y)).toBeLessThan(1);expect(b.x).toBeGreaterThan(a.x+a.width);
  await cards.nth(1).locator('input[name="model-choice"]').check();
  await expect(picker.locator('.is-selected')).toHaveCount(8);
  await expect(page.locator(`[data-focus="model:${second}"]`)).not.toBeChecked();
  const last=saved.at(-1).id,name=page.locator(`[data-focus="name:${last}"]`),effort=page.locator(`[data-focus="effort:${last}"]`);
  await name.fill('ZZ Last card edited');
  await effort.selectOption('high');
  await page.locator(`[data-focus="default:${last}"]`).click();
  await expect.poll(()=>picker.evaluate(el=>el.scrollTop)).toBeGreaterThan(0);
  await page.setViewportSize({width:390,height:1000});
  await name.scrollIntoViewIfNeeded();
  await expect(name).toHaveValue('ZZ Last card edited');
  await expect(effort).toHaveValue('high');
  await expect(page.locator(`[data-focus="default:${last}"]`)).toHaveAttribute('aria-pressed','true');
  await page.setViewportSize({width:1180,height:1000});
  await page.locator('#save-codex-catalog').click();
  await expect(page.locator('#codex-setup-status')).toContainText('Profile ready');
  await page.locator('#clear-codex-models').click();
  await page.locator('#load-codex-catalog').click();
  await expect(picker.locator('.is-selected')).toHaveCount(8);
  await expect(name).toHaveValue('ZZ Last card edited');
  await expect(effort).toHaveValue('high');
  await expect(page.locator(`[data-focus="default:${last}"]`)).toHaveAttribute('aria-pressed','true');
  await expect(page.locator(`[data-focus="model:${first}"]`)).toBeChecked();
});

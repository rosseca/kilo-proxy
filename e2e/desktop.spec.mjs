import {test,expect,startProxy,state,copied} from './fixture.mjs';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import path from 'node:path';

const first='vendor/one', second='anthropic/claude-sonnet-4.6';
const model=(page,id)=>page.locator('input[name="model-choice"]').filter({visible:true}).and(page.locator(`[value="${id}"]`));
const readJSON=async file=>JSON.parse(await readFile(file,'utf8'));
async function seed(dir,name,text) { await mkdir(dir,{recursive:true});await writeFile(path.join(dir,name),text); }

test('connection, team selection, language persistence, all clients and mobile navigation',async({page,gateway,request})=>{
  await expect(page).toHaveTitle('Kilo Proxy · Your gateway, within reach');
  await expect(page.locator('.brand')).toHaveAccessibleName('Kilo Proxy, home');
  await expect(page.locator('.brand-light')).toHaveText('Proxy');
  await startProxy(page,gateway);
  await expect(page.locator('#api-key')).toBeDisabled();
  await expect(page.locator('#key-state')).toHaveText('Saved in the system store');
  const local=await state(request,gateway);
  expect(await copied(page,'[data-copy="url"]')).toBe(gateway.baseURL);
  expect(await copied(page,'[data-copy="key"]')).toBe(local.localKey);
  expect(local.hasKey).toBe(true);expect(local.keySaved).toBe(true);
  expect(JSON.stringify(local)).not.toContain('synthetic-kilo-personal-key');
  await page.locator('#check').click();
  await expect(page.locator('#notice')).toContainText('2 models');
  await page.locator('#start-stop').click();
  await expect(page.locator('#status-label')).toHaveText('Proxy stopped');
  await page.locator('#load-teams').click();
  await expect(page.locator('#team-select option')).toHaveCount(3);
  await page.locator('#team-select').selectOption('other-team');
  await expect(page.locator('#org-id')).toHaveValue('other-team');
  await page.locator('#team-select').selectOption('e2e-team');

  for(const client of ['generic','codex','codex-cli','claude','opencode','zed','cursor','xcode']) {
    await page.locator('#tab-'+client).click();
    await expect(page.locator('#tab-'+client)).toHaveAttribute('aria-selected','true');
    await expect(page.locator('#client-panel')).toHaveAttribute('aria-labelledby','tab-'+client);
  }
  await page.locator('#tab-generic').focus();
  await page.keyboard.press('ArrowRight');
  await expect(page.locator('#tab-zed')).toBeFocused();
  await expect(page.locator('#editor-helper')).toBeVisible();
  await page.locator('#language').selectOption('es');
  await expect(page).toHaveTitle('Kilo Proxy · Tu gateway, a mano');
  await expect(page.locator('.brand')).toHaveAccessibleName('Kilo Proxy, inicio');
  await expect(page.locator('#status-label')).toHaveText('Proxy detenido');
  await expect.poll(async()=>(await state(request,gateway)).language).toBe('es');
  await page.reload();
  await expect(page.locator('#language')).toHaveValue('es');
  await expect(page.locator('#status-label')).toHaveText('Proxy detenido');
  await page.setViewportSize({width:390,height:844});
  await page.locator('#tab-opencode').click();
  await expect(page.locator('#editor-title')).toContainText('OpenCode');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
  await page.locator('#language').selectOption('en');
  await expect(page.locator('#editor-intro')).toContainText('configuration');
  await page.locator('#forget').click();
  await expect(page.locator('#key-state')).toHaveText('Not signed in');
  expect((await state(request,gateway)).hasKey).toBe(false);
});

test('Codex Desktop and CLI prepare independent profiles with names and reasoning',async({page,gateway})=>{
  for(const client of ['codex','codex-cli']) {
    const dir=gateway.profiles[client], original='# Keep my preferences\napproval_policy = "on-request"\n';
    await seed(dir,'config.toml',original);
    await page.locator('#tab-'+client).click();
    await expect(model(page,first)).toBeVisible();
    await model(page,first).check();await model(page,second).check();
    await page.locator(`[data-focus="name:${first}"]`).fill(client==='codex'?'Short GUI':'Short CLI');
    await page.locator(`[data-focus="effort:${first}"]`).selectOption('high');
    await page.locator(`[data-focus="default:${first}"]`).click();
    await page.locator('#save-codex-catalog').click();
    await expect(page.locator('#codex-setup-status')).toContainText('Profile ready');
    const config=await readFile(path.join(dir,'config.toml'),'utf8');
    expect(config).toContain('approval_policy = "on-request"');
    expect(config).toMatch(/env_key"?\s*=\s*['"]KILO_LOCAL_API_KEY['"]/);
    expect(config).toContain(gateway.baseURL);
    expect(await readFile(path.join(dir,'config.toml.bak'),'utf8')).toBe(original);
    const catalog=await readJSON(path.join(dir,'models.json'));
    expect(catalog.models).toHaveLength(2);
    const selected=catalog.models.find(m=>m.slug===first);
    expect(selected.display_name).toBe(client==='codex'?'Short GUI':'Short CLI');
    expect(selected.default_reasoning_level).toBe('high');
    expect(selected.supported_reasoning_levels.map(level=>level.effort)).toEqual(['low','high']);
    const launch=await copied(page,'#copy-launch');
    expect(launch).toContain(client==='codex' ? '.codex-kilo-desktop':'.codex-kilo-cli');
    expect(launch).toMatch(/KILO_LOCAL_API_KEY=['"]?kl_local_/);
    for(const order of ['name','speed','price','codingIndex','codeModeRank']) {
      await page.locator('#model-sort').selectOption(order);
      await expect(page.locator('#codex-setup-status')).toContainText('Profile ready');
      await expect(page.locator(`[data-focus="default:${first}"]`)).toHaveAttribute('aria-pressed','true');
    }
    await page.locator('#model-lab').selectOption('anthropic');
    await expect(model(page,first)).toHaveCount(0);
    await expect(page.locator('#codex-setup-status')).toContainText('Profile ready');
    await page.locator('#model-lab').selectOption('');
    await expect(page.locator(`[data-focus="default:${first}"]`)).toHaveAttribute('aria-pressed','true');
    expect(await readJSON(path.join(dir,'models.json'))).toEqual(catalog);
    await page.locator('#clear-codex-models').click();
    await page.locator('#load-codex-catalog').click();
    await expect(page.locator(`[data-focus="name:${first}"]`)).toHaveValue(client==='codex'?'Short GUI':'Short CLI');
  }
});

test('lab filters combine with sorting and search across every helper while retaining hidden and manual selections',async({page,gateway},testInfo)=>{
  let previous='';
  for(const client of ['generic','codex','codex-cli','claude','opencode','zed','cursor','xcode']) {
    await page.locator('#tab-'+client).click();
    const prefix=['opencode','zed'].includes(client)?'editor':client==='xcode'?'xcode':'model';
    const lab=page.locator('#'+prefix+'-lab'),sort=page.locator('#'+prefix+'-sort'),search=page.locator('#'+prefix+'-search');
    await expect(lab).toBeVisible();
    await expect(lab).toHaveAccessibleName('Lab');
    await expect(lab).toHaveValue(previous);
    await expect(lab.locator('option')).toHaveText(['All labs','Anthropic','Vendor']);
    const variants=client==='xcode'?['chat','codex','claude']:[null];
    for(const variant of variants) {
      if(variant) await page.locator(`[data-xcode-variant="${variant}"]`).click();
      await lab.selectOption('');
      const choices=page.locator(prefix==='editor'?'#editor-picker [data-editor-id]':prefix==='xcode'?'#xcode-picker [data-xcode-focus^="choose:"]':'#model-picker input[name="model-choice"]');
      const firstChoice=prefix==='editor'?page.locator(`[data-editor-id="${first}"]`):prefix==='xcode'?page.locator(`[data-xcode-focus="choose:${first}"]`):model(page,first);
      await expect(choices).toHaveCount(2);
      await firstChoice.check();
      const nameInput=prefix==='editor'?page.locator(`[data-editor-name="${first}"]`):prefix==='xcode'?page.locator(`[data-xcode-focus="name:${first}"]`):['codex','codex-cli','claude'].includes(client)?page.locator(`[data-focus="${client==='claude'?'claude-name':'name'}:${first}"]`):null;
      if(nameInput) await nameInput.fill('Preserved name');
      await lab.selectOption('anthropic');
      await expect(choices).toHaveCount(1);
      await expect(firstChoice).toHaveCount(0);
      await sort.selectOption('price');
      await search.fill('vendor');
      await expect(choices).toHaveCount(0);
      await expect(lab).toHaveValue('anthropic');
      await expect(lab.locator('option')).toHaveText(['All labs','Anthropic','Vendor']);
      await search.fill('');
      await expect(choices).toHaveCount(1);
      const only=prefix==='editor'?page.locator('#editor-selected'):prefix==='xcode'?page.locator('#xcode-selected'):['codex','codex-cli','claude'].includes(client)?page.locator('#codex-selected-only'):null;
      if(only) {
        await only.check();
        await expect(choices).toHaveCount(0);
        await lab.selectOption('vendor');
        await expect(choices).toHaveCount(1);
        await expect(firstChoice).toBeChecked();
        await only.uncheck();
      }
      await lab.selectOption('');
      await expect(choices).toHaveCount(2);
      await expect(firstChoice).toBeChecked();
      if(nameInput) await expect(nameInput).toHaveValue('Preserved name');
      await expect(sort).toHaveValue('price');
      await lab.selectOption('anthropic');
      previous='anthropic';
    }
  }
  await page.locator('#tab-codex').click();
  await page.locator('#codex-manual-entry > summary').click();
  for(const id of ['~anthropic/manual','future-lab/manual','constructor/manual','__proto__/manual']) {
    await page.locator('#codex-manual-id').fill(id);
    await page.locator('#add-codex-model').click();
  }
  await expect(page.locator('#model-lab option[value="anthropic"]')).toHaveCount(1);
  await expect(page.locator('#model-lab option[value="future-lab"]')).toHaveText('Future Lab');
  await expect(page.locator('#model-lab option[value="constructor"]')).toHaveText('Constructor');
  await expect(page.locator('#model-lab option[value="__proto__"]')).toHaveText('Proto');
  // Rendering unknown labs must be safe even when an ID is unsupported by Codex's catalog format.
  await page.locator('#model-lab').selectOption('__proto__');
  await model(page,'__proto__/manual').click();
  await expect(model(page,'__proto__/manual')).toHaveCount(0);
  await page.locator('#codex-selected-only').uncheck();
  await page.locator('#model-lab').selectOption('anthropic');
  await expect(model(page,'~anthropic/manual')).toBeChecked();
  await expect(model(page,second)).not.toBeChecked();
  await page.locator('#select-codex-results').click();
  await expect(model(page,second)).toBeChecked();
  await page.locator('#model-lab').selectOption('future-lab');
  await expect(model(page,'future-lab/manual')).toBeChecked();
  await page.locator('#tab-opencode').click();
  await expect(page.locator('#editor-lab')).toHaveValue('future-lab');
  await expect(page.locator('#editor-lab option[value="future-lab"]')).toHaveText('Future Lab');
  await expect(page.locator('#editor-picker [data-editor-id]')).toHaveCount(0);
  await page.locator('#editor-lab').selectOption('');
  await expect(page.locator('#editor-picker [data-editor-id]')).toHaveCount(2);
  await expect(page.locator(`[data-editor-id="${first}"]`)).toBeChecked();
  await page.locator('#tab-codex').click();
  await expect(model(page,first)).toBeChecked();
  await expect(page.locator(`[data-focus="name:${first}"]`)).toHaveValue('Preserved name');
  await expect(page.locator(`[data-focus="default:${first}"]`)).toHaveAttribute('aria-pressed','true');
  await page.locator('#save-codex-catalog').click();
  await expect(page.locator('#codex-setup-status')).toContainText('Profile ready');
  const saved=await readJSON(path.join(gateway.profiles.codex,'models.json'));
  expect(saved.models[0].slug).toBe(first);
  expect(saved.models.map(m=>m.slug)).toContain('~anthropic/manual');
  expect(saved.models.map(m=>m.slug)).toContain('future-lab/manual');
  for(const language of ['en','es']) {
    await page.locator('#language').selectOption(language);
    for(const client of ['codex','opencode','xcode']) {
      await page.locator('#tab-'+client).click();
      const prefix=client==='opencode'?'editor':client==='xcode'?'xcode':'model';
      await expect(page.locator('#'+prefix+'-lab')).toHaveAccessibleName(language==='en'?'Lab':'Laboratorio');
      await expect(page.locator('#'+prefix+'-lab option').first()).toHaveText(language==='en'?'All labs':'Todos los laboratorios');
      for(const [size,width,height] of [['wide',1180,820],['mobile',390,844]]) {
        await page.setViewportSize({width,height});
        await page.locator('#'+prefix+'-lab').scrollIntoViewIfNeeded();
        expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
        await page.screenshot({path:testInfo.outputPath('lab-'+prefix+'-'+language+'-'+size+'.png')});
      }
    }
  }
});

test('all model helpers share ranking, coding, speed, price and name sorting without promoting selections',async({page},testInfo)=>{
  const expected={codeModeRank:[second,first],codingIndex:[first,second],speed:[second,first],price:[first,second],name:[second,first]};
  let previous='codeModeRank';
  for(const client of ['generic','codex','codex-cli','claude','opencode','zed','cursor','xcode']) {
    await page.locator('#tab-'+client).click();
    const prefix=['opencode','zed'].includes(client)?'editor':client==='xcode'?'xcode':'model';
    const sort=page.locator('#'+prefix+'-sort');
    await expect(sort).toBeVisible();
    await expect(sort).toHaveAccessibleName('Sort by');
    await expect(sort).toHaveValue(previous);
    await expect(sort.locator('option')).toHaveText(['Code Mode Rank','Coding Index','Speed','Price','Name']);
    const variants=client==='xcode'?['chat','codex','claude']:[null];
    for(const variant of variants) {
      if(variant) await page.locator(`[data-xcode-variant="${variant}"]`).click();
      const choices=page.locator(prefix==='editor'?'#editor-picker [data-editor-id]':prefix==='xcode'?'#xcode-picker [data-xcode-focus^="choose:"]':'#model-picker input[name="model-choice"]');
      await expect(choices).toHaveCount(2);
      const firstChoice=prefix==='editor'?page.locator(`[data-editor-id="${first}"]`):prefix==='xcode'?page.locator(`[data-xcode-focus="choose:${first}"]`):model(page,first);
      await firstChoice.check();
      for(const [order,ids] of Object.entries(expected)) {
        await sort.selectOption(order);
        await expect.poll(()=>choices.evaluateAll((elements,prefix)=>elements.map(element=>prefix==='editor'?element.dataset.editorId:prefix==='xcode'?element.dataset.xcodeFocus.slice(7):element.value),prefix)).toEqual(ids);
        await expect(firstChoice).toBeChecked();
        previous=order;
      }
      const nameInput=prefix==='editor'?page.locator(`[data-editor-name="${first}"]`):prefix==='xcode'?page.locator(`[data-xcode-focus="name:${first}"]`):['codex','codex-cli','claude'].includes(client)?page.locator(`[data-focus="${client==='claude'?'claude-name':'name'}:${first}"]`):null;
      if(nameInput) {
        await nameInput.fill('A custom name');
        await sort.selectOption('speed');
        await sort.selectOption('name');
        await expect.poll(()=>choices.evaluateAll((elements,prefix)=>elements.map(element=>prefix==='editor'?element.dataset.editorId:prefix==='xcode'?element.dataset.xcodeFocus.slice(7):element.value),prefix)).toEqual([first,second]);
        await expect(nameInput).toHaveValue('A custom name');
      }
      if(variant) {
        await page.locator('#xcode-selected').check();
        await expect(choices).toHaveCount(1);
        await expect(firstChoice).toBeChecked();
        await page.locator('#xcode-selected').uncheck();
        await expect(choices).toHaveCount(2);
      }
    }
  }
  await page.locator('#language').selectOption('es');
  await expect(page.locator('#xcode-sort')).toHaveAccessibleName('Ordenar por');
  await expect(page.locator('#xcode-sort option')).toHaveText(['Ranking de Code Mode','Índice de programación','Velocidad','Precio','Nombre']);
  await page.screenshot({path:testInfo.outputPath('sort-xcode-desktop.png')});
  for(const client of ['codex','opencode','xcode']) {
    await page.locator('#tab-'+client).click();
    const prefix=client==='opencode'?'editor':client==='xcode'?'xcode':'model';
    await expect(page.locator('#'+prefix+'-sort')).toHaveValue('name');
    await expect(page.locator('#'+prefix+'-sort')).toHaveAccessibleName('Ordenar por');
    await page.setViewportSize({width:390,height:844});
    expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
    await expect(page.locator('#'+prefix+'-sort')).toBeVisible();
    await page.locator('#'+prefix+'-sort').scrollIntoViewIfNeeded();
    await page.screenshot({path:testInfo.outputPath('sort-'+prefix+'-mobile.png')});
  }
});

test('Claude prepares and reloads model picker, alias and reasoning without overwriting preferences',async({page,gateway})=>{
  const dir=gateway.profiles.claude,original=JSON.stringify({permissions:{defaultMode:'default'},env:{MY_TEAM_SETTING:'keep'}});
  await seed(dir,'settings.json',original);
  await page.locator('#tab-claude').click();
  await page.locator('#claude-mode').selectOption('modern');
  await model(page,first).check();await model(page,second).check();
  await page.locator(`[data-focus="claude-name:${second}"]`).fill('Sonnet');
  await page.locator(`[data-focus="claude-effort:${second}"]`).selectOption('high');
  await page.locator(`[data-focus="claude-default:${second}"]`).click();
  await page.locator('#claude-model-actions details > summary').click();
  await page.locator('#claude-alias-haiku').selectOption(first);
  await page.locator('#save-claude-profile').click();
  await expect(page.locator('#claude-setup-status')).toContainText('Configuration saved');
  const settings=await readJSON(path.join(dir,'settings.json'));
  expect(settings.permissions.defaultMode).toBe('default');
  expect(settings.env.MY_TEAM_SETTING).toBe('keep');
  expect(settings.env.ANTHROPIC_AUTH_TOKEN).toMatch(/^kl_local_/);
  expect(settings.env.ANTHROPIC_BASE_URL).toBe(gateway.baseURL.replace('/v1',''));
  expect(settings.env.ANTHROPIC_DEFAULT_HAIKU_MODEL).toBe(first);
  expect(settings.modelPicker.options).toContainEqual({model:'claude-sonnet-4-6',label:'Sonnet'});
  expect(settings.modelOverrides['claude-sonnet-4-6']).toBe(second);
  expect(settings.modelSettings['claude-sonnet-4-6'].effortLevel).toBe('high');
  expect(await readFile(path.join(dir,'settings.json.bak'),'utf8')).toBe(original);
  expect(await copied(page,'#copy-launch')).toContain('.claude-kilo');
  await page.locator('#clear-codex-models').click();
  await page.locator('#load-claude-profile').click();
  await expect(page.locator(`[data-focus="claude-name:${second}"]`)).toHaveValue('Sonnet');
});

test('OpenCode and Zed save multiple models, limits, backups and independent selections',async({page,gateway})=>{
  for(const client of ['opencode','zed']) {
    const dir=gateway.profiles[client],name=client==='zed'?'settings.json':'opencode.json';
    const original='// Keep team comments\n{"theme":"dark", "permission":{"bash":"ask"},}\n';
    await seed(dir,name,original);
    await page.locator('#tab-'+client).click();
    await expect(page.locator('[data-editor-id]')).toHaveCount(2);
    await expect(page.locator('#editor-picker')).toContainText('USD / 1M');
    await expect(page.locator('#editor-save')).toBeDisabled();
    await page.locator(`[data-editor-id="${first}"]`).check();
    await page.locator(`[data-editor-id="${second}"]`).check();
    await page.locator(`[data-editor-name="${first}"]`).fill('Short One');
    await page.locator(`[data-editor-initial="${second}"]`).click();
    const firstRow=page.locator('.codex-model-entry').filter({has:page.locator(`[data-editor-id="${first}"]`)});
    await firstRow.locator('details > summary').click();
    await firstRow.getByRole('spinbutton',{name:'Context tokens: '+first,exact:true}).fill('80000');
    await firstRow.getByRole('spinbutton',{name:'Max output tokens (0 = unspecified): '+first,exact:true}).fill('5000');
    await page.locator('#editor-save').click();
    await expect(page.locator('#editor-status')).toContainText('Configuration saved:');
    const settings=await readFile(path.join(dir,name),'utf8');
    expect(settings).toContain('// Keep team comments');
    expect(settings).toContain('"bash":"ask"');
    expect(settings).toContain('Short One');
    expect(settings).toContain('80000');
    expect(settings).toContain('5000');
    expect(await readFile(path.join(dir,name+'.bak'),'utf8')).toBe(original);
    expect(settings.includes('kl_local_')).toBe(client==='opencode');
    const output=await copied(page,'#editor-copy');
    if(client==='opencode') {
      expect(output).toContain('OPENCODE_CONFIG=');
      expect(output).toContain('kilo-local/'+second);
      expect(output).not.toContain('kl_local_');
    } else expect(output).toMatch(/^kl_local_/);
    const exported=await copied(page,'#editor-export');
    expect(exported).toContain(first);expect(exported).toContain(second);
    await page.locator(`[data-editor-name="${first}"]`).fill('Unsaved');
    await expect(page.locator('#editor-copy')).toBeDisabled();
    await page.locator('#editor-load').click();
    await expect(page.locator(`[data-editor-name="${first}"]`)).toHaveValue('Short One');
    await expect(page.locator('#editor-copy')).toBeDisabled();
    await page.locator('#editor-save').click();
    await expect(page.locator('#editor-copy')).toBeEnabled();
    expect(await readFile(path.join(dir,name+'.bak'),'utf8')).toBe(original);
  }
  await page.locator('#tab-opencode').click();
  await expect(page.locator('[data-editor-id]:checked')).toHaveCount(2);
  await page.locator('#language').selectOption('es');
  await expect(page.locator('#editor-save')).toHaveText('1. Preparar OpenCode');
});

test('real JSON and SSE proxy traffic reaches cost, cache and redacted activity inspectors',async({page,gateway,request})=>{
  await startProxy(page,gateway);
  const initial=await state(request,gateway);
  const unauthorized=await request.post(gateway.baseURL+'/responses',{data:{model:first}});
  expect(unauthorized.status()).toBe(401);
  for(const stream of [false,true]) {
    const response=await request.post(gateway.baseURL+'/responses',{
      headers:{Authorization:'Bearer '+initial.localKey,'Thread-Id':'e2e-conversation'},
      data:{model:first,input:'SYNTHETIC_USER_MESSAGE',stream},
    });
    expect(response.status()).toBe(200);expect(await response.text()).toContain('SYNTHETIC_GATEWAY_REPLY');
  }
  await expect(page.locator('#spend-total')).toHaveText('$0.024600');
  await expect(page.locator('#cache-read-total')).toHaveText('160');
  await expect(page.locator('#cache-write-total')).toHaveText('20');
  await expect(page.locator('#cache-ratio-total')).toHaveText('80%');
  await expect(page.locator('#usage-sessions')).toContainText('e2e-conversation');
  await expect(page.locator('#event-rows button')).toHaveCount(2);
  await page.locator('#event-rows button').first().click();
  await expect(page.locator('#trace-body')).toContainText('SYNTHETIC_USER_MESSAGE');
  await expect(page.locator('#trace-headers')).toContainText('[REDACTED]');
  await page.locator('#trace-tab-upstreamRequest').click();
  await expect(page.locator('#trace-headers')).toContainText('e2e-team');
  await expect(page.locator('#trace-headers')).not.toContainText('synthetic-kilo-personal-key');
  await page.locator('#trace-tab-response').click();
  await expect(page.locator('#trace-body')).toContainText('SYNTHETIC_GATEWAY_REPLY');
  await page.locator('#capture-activity').click();
  await expect.poll(async()=>(await state(request,gateway)).captureEnabled).toBe(false);
  await expect(page.locator('#capture-activity')).not.toBeChecked();
  await page.locator('#clear-activity').click();
  await expect(page.locator('#activity-table')).toBeHidden();
  await expect(page.locator('#spend-total')).toHaveText('$0.024600');
  await expect(page.locator('#cache-read-total')).toHaveText('160');
  await page.locator('#start-stop').click();
  await expect(page.locator('#status-label')).toHaveText('Proxy stopped');
  await page.locator('#start-stop').click();
  await expect(page.locator('#status-label')).toHaveText('Proxy running');
  await expect(page.locator('#spend-total')).toHaveText('$0.024600');
});

test('admin access requires the app token and rejects cross-origin mutations',async({page,gateway,request})=>{
  const endpoint=new URL('/api/start',gateway.url).href;
  expect((await request.post(endpoint,{data:{}})).status()).toBe(401);
  expect((await request.post(endpoint,{headers:{Authorization:'Bearer '+gateway.token,Origin:'https://unexpected.example'},data:{}})).status()).toBe(403);
  const clean=await page.context().newPage();
  await clean.goto(new URL('/',gateway.url).href);
  await expect(clean.locator('#locked')).toBeVisible();
  await clean.close();
});

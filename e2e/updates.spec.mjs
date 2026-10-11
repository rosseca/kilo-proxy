import {test,expect} from './fixture.mjs';

test('update guidance handles unknown, checking, available, current and offline states', async ({page},testInfo) => {
  let update={currentVersion:'0.50.0',available:false,checking:false,checkedAt:''},checks=0,rejectCheck=false;
  await page.route('**/api/state',async route=>{
    const response=await route.fetch(),body=await response.json();
    await route.fulfill({response,json:{...body,update}});
  });
  await page.route('**/api/updates',async route=>{
    expect(route.request().method()).toBe('POST');
    expect(route.request().headers().authorization).toMatch(/^Bearer /);
    checks++;
    if(rejectCheck){await route.fulfill({status:503,json:{error:{message:'Unavailable'}}});return;}
    update={...update,checking:true,error:''};
    await route.fulfill({json:update});
  });
  await page.reload();
  await expect(page.locator('#updates-status')).toHaveText('Not checked yet.');
  await expect(page.locator('#update-notice')).toBeHidden();
  await page.locator('#updates-check').click();
  expect(checks).toBe(1);
  await expect(page.locator('#updates-status')).toHaveText('Checking GitHub…');
  await expect(page.locator('#updates-check')).toBeDisabled();
  update={...update,checking:false,available:true,latestVersion:'0.51.0',checkedAt:'2026-09-25T19:00:00Z',releaseUrl:'https://github.com/rosseca/kilo-proxy/releases/tag/v0.51.0'};
  await expect(page.locator('#updates-status')).toHaveText('Kilo Proxy v0.51.0 is available.');
  await expect(page.locator('#updates-download')).toHaveAttribute('href',update.releaseUrl);
  await expect(page.locator('#update-notice')).toBeVisible();
  await expect(page.locator('#update-notice-download')).toHaveAttribute('href',update.releaseUrl);
  await expect(page.locator('#update-notice-download')).toHaveAttribute('rel','noopener noreferrer');
  await testInfo.attach('app-updates.png',{body:await page.locator('#app-updates').screenshot(),contentType:'image/png'});
  await page.locator('#language').selectOption('es');
  await expect(page.locator('#updates-title')).toHaveText('Actualizaciones de la app');
  await expect(page.locator('#updates-status')).toHaveText('Kilo Proxy v0.51.0 está disponible.');
  await expect(page.locator('#updates-download')).toHaveText('Descargar actualización ↗');
  await page.setViewportSize({width:390,height:844});
  await page.locator('#update-notice').scrollIntoViewIfNeeded();
  await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await testInfo.attach('app-updates-mobile.png',{body:await page.locator('#update-notice').screenshot(),contentType:'image/png'});
  update={...update,available:false,latestVersion:'0.50.0'};
  await expect(page.locator('#updates-status')).toHaveText('Tienes la versión más reciente.');
  await expect(page.locator('#update-notice')).toBeHidden();
  await expect(page.locator('#updates-download')).not.toHaveAttribute('href');
  update={...update,error:'GitHub rate limited'};
  await expect(page.locator('#updates-status')).toHaveText('No se pudieron comprobar las actualizaciones. Inténtalo más tarde.');
  await expect(page.locator('#updates-check')).toBeEnabled();
  update={...update,error:''};
  await expect(page.locator('#updates-status')).toHaveText('Tienes la versión más reciente.');
  rejectCheck=true;
  await page.locator('#updates-check').click();
  await expect.poll(()=>checks).toBe(2);
  await expect(page.locator('#updates-status')).toHaveText('No se pudieron comprobar las actualizaciones. Inténtalo más tarde.');
  // A later automatic result clears a local manual-request failure.
  update={...update,checkedAt:'2026-09-25T20:00:00Z'};
  await expect(page.locator('#updates-status')).toHaveText('Tienes la versión más reciente.');
  // A malformed or foreign link must never become a download action.
  update={...update,available:true,error:'',releaseUrl:'https://example.invalid/malicious'};
  await expect(page.locator('#updates-download')).toBeHidden();
  await expect(page.locator('#update-notice')).toBeHidden();
});

test('managed update requires current consent and handles terminal failure, progress and retry', async ({page},testInfo) => {
  let update={currentVersion:'0.50.0',available:true,checking:false,latestVersion:'0.51.0',checkedAt:'2026-09-25T19:00:00Z',releaseUrl:'https://github.com/rosseca/kilo-proxy/releases/tag/v0.51.0',installMethod:'brew-formula',canInstall:true};
  let installs=0,fail=true,release,disconnect=false;
  const gate=new Promise(resolve=>{release=resolve;});
  await page.route('**/api/state',async route=>{
    if(disconnect){await route.abort('connectionrefused');return;}
    const response=await route.fetch(),body=await response.json();
    await route.fulfill({response,json:{...body,update}});
  });
  await page.route('**/api/updates/install',async route=>{
    expect(route.request().method()).toBe('POST');
    expect(route.request().headers().authorization).toMatch(/^Bearer /);
    expect(route.request().postDataJSON()).toEqual({version:update.latestVersion,confirm:true});
    installs++;
    if(fail){await route.fulfill({status:409,json:{error:{message:'Synthetic terminal failure'}}});return;}
    await gate;
    // The terminal can start while the POST response is lost. The following
    // Installing=true snapshot still establishes an intentional handoff.
    if(installs===3){await route.abort('connectionreset');return;}
    await route.fulfill({json:{started:true,message:'Synthetic terminal opened'}});
  });
  try {
    await page.reload();
    await expect(page.locator('#updates-method')).toContainText('Homebrew');
    expect(installs).toBe(0);
    await page.locator('#update-notice-install').click();
    await expect(page.locator('#updates-confirmation')).toBeVisible();
    await expect(page.locator('#updates-confirmation-text')).toContainText('interrupts active requests');
    await page.locator('#updates-cancel').click();
    await expect(page.locator('#updates-confirmation')).toBeHidden();
    expect(installs).toBe(0);
    await page.locator('#updates-install').click();
    update={...update,installMethod:'apt'};
    await expect(page.locator('#updates-method')).toContainText('APT');
    await expect(page.locator('#updates-confirmation')).toBeHidden();
    // Even a synthetic stale click must not reuse Homebrew consent for APT.
    await page.locator('#updates-confirm').dispatchEvent('click');
    expect(installs).toBe(0);
    await page.locator('#language').selectOption('es');
    await page.setViewportSize({width:390,height:844});
    await page.locator('#updates-install').click();
    await expect(page.locator('#updates-confirmation-text')).toContainText('0.51.0 con APT');
    await expect(page.locator('#updates-confirmation-text')).toContainText('contraseña de administrador');
    await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
    await testInfo.attach('package-update-confirmation-mobile-es.png',{body:await page.locator('#updates-confirmation').screenshot(),contentType:'image/png'});
    await page.locator('#updates-confirm').click();
    await expect(page.locator('#updates-status')).toHaveText('No se pudo iniciar la actualización. Inténtalo de nuevo o descárgala manualmente.');
    expect(installs).toBe(1);
    await expect(page.locator('#updates-download')).toHaveAttribute('href',update.releaseUrl);
    fail=false;
    await page.locator('#updates-install').click();
    await page.locator('#updates-confirm').click();
    await expect(page.locator('#updates-status')).toHaveText('Abriendo la terminal de actualización…');
    await expect(page.locator('#updates-install')).toBeDisabled();
    await expect(page.locator('#updates-check')).toBeDisabled();
    await page.locator('#updates-confirm').dispatchEvent('click');
    await page.locator('#updates-install').dispatchEvent('click');
    await expect.poll(()=>installs).toBe(2);
    update={...update,installing:true,installMessage:'update_starting'};
    release();
    await expect(page.locator('#updates-status')).toHaveText('Esperando a la terminal de actualización…');
    expect(installs).toBe(2);
    update={...update,installing:false,installMessage:'update_start_failed'};
    await expect(page.locator('#updates-status')).toHaveText('No se pudo iniciar la actualización. Inténtalo de nuevo o descárgala manualmente.');
    await expect(page.locator('#updates-install')).toBeEnabled();
    await expect(page.locator('#updates-message')).toContainText('No se pudo abrir la terminal');
    await page.locator('#updates-install').click();
    update={...update,installing:true,installMessage:'update_starting'};
    await page.locator('#updates-confirm').click();
    await expect.poll(()=>installs).toBe(3);
    await expect(page.locator('#updates-status')).toHaveText('Esperando a la terminal de actualización…');
    await expect(page.locator('#updates-status')).not.toHaveClass(/update-error/);
    disconnect=true;
    await expect(page.locator('#updates-status')).toHaveText('Sigue la terminal de actualización. Puedes cerrar este panel cuando Kilo Proxy se haya cerrado.');
    await expect(page.locator('#notice')).not.toContainText('Se ha perdido la conexión');
  } finally { release(); }
});

test('accepted update recovers a quick helper failure without accepting an older failure poll', async ({page}) => {
  let update={currentVersion:'0.50.0',available:true,checking:false,latestVersion:'0.51.0',releaseUrl:'https://github.com/rosseca/kilo-proxy/releases/tag/v0.51.0',installMethod:'apt',canInstall:true,installMessage:'package_update_failed'};
  let installs=0,holdNext=false,failNextState=false,release,seen;
  const held=new Promise(resolve=>{seen=resolve;});
  const gate=new Promise(resolve=>{release=resolve;});
  await page.route('**/api/state',async route=>{
    if(failNextState){failNextState=false;await route.abort('connectionreset');return;}
    const snapshot={...update},hold=holdNext;
    holdNext=false;
    const response=await route.fetch(),body=await response.json();
    if(hold){seen();await gate;}
    await route.fulfill({response,json:{...body,update:snapshot}});
  });
  await page.route('**/api/updates/install',async route=>{
    expect(route.request().postDataJSON()).toEqual({version:'0.51.0',confirm:true});
    installs++;
    // Failure can happen before any GET has seen Installing=true.
    update={...update,installing:false,installMessage:'update_start_failed'};
    await route.fulfill({json:{started:true,message:'Synthetic terminal process started'}});
  });
  try {
    await page.reload();
    await expect(page.locator('#updates-install')).toBeEnabled();
    holdNext=true;
    await held;
    await page.locator('#updates-install').click();
    await page.locator('#updates-confirm').click();
    await expect(page.locator('#updates-status')).toHaveText('Waiting for the update terminal…');
    release();
    failNextState=true;
    // The held pre-POST failure cannot turn a newly accepted attempt into failure.
    await expect(page.locator('#updates-status')).toHaveText('Waiting for the update terminal…');
    await expect(page.locator('#updates-status')).toHaveText('Follow the update terminal. You can close this panel once Kilo Proxy has closed.');
    // A transient lost GET must not stop polling and hide the actual failure.
    await expect(page.locator('#updates-status')).toHaveText('Could not start the update. Try again or download it manually.');
    await expect(page.locator('#updates-install')).toBeEnabled();
    await page.locator('#updates-install').click();
    await page.locator('#updates-confirm').click();
    await expect.poll(()=>installs).toBe(2);
    await expect(page.locator('#updates-status')).toHaveText('Waiting for the update terminal…');
  } finally { release(); }
});

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

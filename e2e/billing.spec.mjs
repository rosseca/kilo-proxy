import {test,expect,startProxy,state} from './fixture.mjs';
import {writeFile} from 'node:fs/promises';

test('billing distinguishes shared credit, own Kilo charges and persistent local observations', async ({page,request,gateway},testInfo) => {
  await expect(page.locator('#account-balance')).toHaveText('—');
  await expect(page.locator('#account-balance-status')).toContainText('Sign in');
  await startProxy(page,gateway);
  await expect(page.locator('#account-balance')).toHaveText('$248.623145');
  await expect(page.locator('#account-spend-today')).toHaveText('$1.203456');
  await expect(page.locator('#account-spend-yesterday')).toHaveText('$8.95678');
  await expect(page.locator('#account-spend-month')).toHaveText('$10.160236');
  await expect(page.locator('#account-usage')).toContainText('shared by everyone');
  await expect(page.locator('#local-spend-today')).toHaveText('Not reported');
  await page.locator('#account-daily summary').click();
  await expect(page.locator('#account-daily tbody tr')).toHaveCount(30);
  await expect(page.locator('#account-daily tbody tr').nth(1)).toContainText('$8.95678');
  await page.locator('#account-tray-display').selectOption('balance');
  await expect.poll(async () => (await state(request,gateway)).trayDisplay).toBe('balance');
  await page.reload();
  await expect(page.locator('#account-tray-display')).toHaveValue('balance');
  const s = await state(request,gateway);
  const response = await request.post(s.baseURL+'/responses',{headers:{Authorization:'Bearer '+s.localKey},data:{model:'vendor/one',input:'synthetic daily usage check'}});
  expect(response.ok()).toBe(true);
  await expect(page.locator('#local-spend-today')).not.toHaveText('Not reported');
  await expect(page.locator('#account-spend-today')).toHaveText('$1.203456');
  await page.locator('#account-usage').scrollIntoViewIfNeeded();
  await testInfo.attach('account-usage.png',{body:await page.locator('#account-usage').screenshot(),contentType:'image/png'});
  await page.locator('#language').selectOption('es');
  await expect(page.locator('#account-usage-title')).toHaveText('Cuenta de Kilo');
  await expect(page.locator('#account-tray-display option:checked')).toHaveText('Saldo de la cuenta de Kilo');
});

test('failed refresh hides stale money and missing balance never becomes zero', async ({page,request,gateway}) => {
  await writeFile(gateway.launchControl,JSON.stringify({billingMissingBalance:true}));
  await startProxy(page,gateway);
  await expect(page.locator('#account-balance-status')).toContainText('did not return a valid balance');
  await expect(page.locator('#account-balance')).toHaveText('—');
  await expect(page.locator('#account-spend-today')).toHaveText('$1.203456');
  await writeFile(gateway.launchControl,'{}');
  await page.locator('#refresh-billing').click();
  await expect(page.locator('#account-balance')).toHaveText('$248.623145');
  await writeFile(gateway.launchControl,JSON.stringify({billingFailure:true}));
  await page.locator('#refresh-billing').click();
  await expect(page.locator('#account-balance')).toHaveText('—');
  await expect(page.locator('#account-spend-yesterday')).toHaveText('—');
  await expect(page.locator('#account-balance-status')).toContainText('Out of date');
  await expect.poll(async () => (await state(request,gateway)).billing.error).toContain('Sign in again');
});

test('switching teams replaces the shared balance and your billed usage', async ({page,request,gateway}) => {
  await startProxy(page,gateway);
  await expect(page.locator('#account-balance')).toHaveText('$248.623145');
  await page.locator('#start-stop').click();
  await expect(page.locator('#status-label')).toHaveText('Proxy stopped');
  await page.locator('#org-id').fill('other-team');
  await page.locator('#start-stop').click();
  await expect(page.locator('#account-balance')).toHaveText('$100.001');
  await expect(page.locator('#account-spend-today')).toHaveText('$0');
  await expect.poll(async () => (await state(request,gateway)).billing.orgId).toBe('other-team');
  await expect(page.locator('#local-spend-today')).toHaveText('Not reported');
});

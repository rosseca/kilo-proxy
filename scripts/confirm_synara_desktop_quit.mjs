// Optional native smoke helper. The endpoint and paths belong to the disposable
// Electron process started by the Go fixture, never to the user's Synara app.
import assert from 'node:assert/strict';
import { readFile, writeFile } from 'node:fs/promises';
import { chromium } from '@playwright/test';

const fixture = JSON.parse(await readFile(process.argv[2], 'utf8'));
assert.match(fixture.endpoint, /^ws:\/\/127\.0\.0\.1:\d+\/devtools\/browser\/[\w-]+$/);
const browser = await chromium.connectOverCDP(fixture.endpoint, { timeout: 15000 });
let disconnected = false;
const closed = new Promise(resolve => browser.once('disconnected', () => {
  disconnected = true;
  resolve();
}));
let page;
const deadline = Date.now() + 15000;
while (!page && Date.now() < deadline) {
  for (const candidate of browser.contexts().flatMap(context => context.pages())) {
    if (candidate.url() === 'about:blank' || candidate.url().startsWith('devtools:')) continue;
    const ready = await candidate.evaluate(() => document.documentElement.dataset.runtime === 'electron'
      && document.querySelector('#root')?.childElementCount > 0
      && typeof window.desktopBridge?.onQuitConfirmationRequest === 'function'
      && typeof window.desktopBridge?.replyQuitConfirmation === 'function').catch(() => false);
    if (ready) { page = candidate; break; }
  }
  if (!page) await new Promise(resolve => setTimeout(resolve, 100));
}
assert.ok(page, 'The owned Electron process must have a ready renderer page');
await page.evaluate(() => new Promise(resolve => setTimeout(resolve, 0)));
await writeFile(fixture.readyFile, 'ready\n', { mode: 0o600 });

// Older Betas can quit directly; newer Betas ask even with zero running chats.
// Exercise the real confirmation button without patching the quit guard.
const confirmed = (async () => {
  const dialog = page.getByRole('alertdialog').filter({ hasText: 'Are you sure you want to quit?' });
  await dialog.waitFor({ state: 'visible', timeout: 18000 });
  if (fixture.screenshot) await page.screenshot({ path: fixture.screenshot, animations: 'disabled' });
  await dialog.getByRole('button', { name: 'Quit', exact: true }).click();
  process.stdout.write('Owned Synara quit confirmation clicked\n');
})();
try {
  await Promise.race([confirmed, closed]);
} catch (error) {
  if (!disconnected) throw error;
}
// Disconnect only this test client. Electron's normal shutdown is verified by
// Go through exit status, runtime removal, shutdown logs and backend lifetime.
process.exit(0);

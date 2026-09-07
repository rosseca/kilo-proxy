import {defineConfig, devices} from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/global-setup.mjs',
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  timeout: 60000,
  expect: {timeout: 10000},
  reporter: [['list'], ['html', {open: 'never'}]],
  use: {viewport: {width: 1440, height: 1080}, trace: 'retain-on-failure', screenshot: 'only-on-failure'},
  projects: [
    {name: 'chromium', use: {...devices['Desktop Chrome'], viewport: {width:1440,height:1080}}},
    {name: 'webkit', use: {...devices['Desktop Safari'], viewport: {width:1440,height:1080}}},
  ],
});

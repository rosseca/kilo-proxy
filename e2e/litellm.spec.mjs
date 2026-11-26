import {createServer} from 'node:http';
import {once} from 'node:events';
import {test, expect, startProxy, state} from './fixture.mjs';

test('LiteLLM setup preserves a working account and switches catalogs independently', async ({page, gateway, request}) => {
  const key = 'synthetic-litellm-virtual-key';
  const forwarded = [];
  const liteLLM = createServer(async (req, res) => {
    if (req.headers.authorization !== 'Bearer ' + key) {
      res.writeHead(401).end();
      return;
    }
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET' && req.url === '/team/v1/models') {
      res.end(JSON.stringify({data: [{id: 'vendor/one', name: 'LiteLLM Model One'}]}));
      return;
    }
    if (req.method === 'POST' && req.url === '/team/v1/chat/completions') {
      let body = '';
      for await (const chunk of req) body += chunk;
      forwarded.push(JSON.parse(body).model);
      res.end(JSON.stringify({model: 'vendor/one', choices: [], usage: {prompt_tokens: 2, completion_tokens: 1, total_tokens: 3}}));
      return;
    }
    res.writeHead(404).end();
  });
  try {
    liteLLM.listen(0, '127.0.0.1');
    await once(liteLLM, 'listening');
    const url = 'http://127.0.0.1:' + liteLLM.address().port + '/team';
    const account = page.locator('#litellm-account');
    const input = account.getByLabel('API URL');
    const password = account.getByLabel('LiteLLM virtual key');
    await account.getByRole('button', {name: 'Add LiteLLM'}).click();
    await expect(input).toHaveValue('');
    expect(await input.getAttribute('placeholder')).toBeNull();
    await expect(password).toHaveAttribute('type', 'password');
    await input.fill(url);
    await password.fill(key);
    await account.getByRole('button', {name: 'Test and save'}).click();
    await expect(account.locator('#litellm-status')).toHaveText('Connected · 1 model available · ' + url + '/v1');
    await expect(page.locator('#diagram-provider')).toHaveText('LiteLLM');

    await account.getByRole('button', {name: 'Edit LiteLLM'}).click();
    await expect(input).toHaveValue(url + '/v1');
    await expect(password).toHaveValue('');
    await password.fill('wrong-key');
    await account.getByRole('button', {name: 'Test and save'}).click();
    await expect(account.getByRole('alert')).toContainText('rejected the virtual key');
    await expect(account.locator('#litellm-status')).toContainText('Connected');
    await account.getByRole('button', {name: 'Cancel'}).click();

    await page.locator('#start-stop').click();
    await expect(page.locator('#status-label')).toHaveText('Proxy running');
    await page.locator('#tab-codex').click();
    const choices = page.locator('#model-picker');
    const liteLLMChoice = choices.locator('input[value^="litellm/"][value$="/vendor/one"]');
    const kiloChoice = choices.locator('input[value="vendor/one"]');
    await expect(liteLLMChoice).toHaveCount(1);
    await expect(choices.locator('.model-connection-badge')).toHaveText('LiteLLM');
    await page.locator('#capture-activity').check();
    await expect.poll(async () => (await state(request, gateway)).captureEnabled).toBe(true);
    const local = await state(request, gateway);
    const response = await request.post(local.baseURL + '/chat/completions', {
      headers: {Authorization: 'Bearer ' + local.localKey},
      data: {model: await liteLLMChoice.inputValue(), messages: [{role: 'user', content: 'synthetic test'}]},
    });
    expect(response.ok()).toBe(true);
    expect(forwarded).toEqual(['vendor/one']);
    await expect(page.locator('#event-rows')).toContainText('Usage managed in LiteLLM');

    await page.locator('#start-stop').click();
    await expect(page.locator('#status-label')).toHaveText('Proxy stopped');
    await startProxy(page, gateway);
    await expect(page.locator('#diagram-provider')).toHaveText('Kilo + LiteLLM');
    await expect(kiloChoice).toHaveCount(1);
    await expect(liteLLMChoice).toHaveCount(1);
    await account.getByLabel('Use LiteLLM').click();
    await expect(page.locator('#diagram-provider')).toHaveText('Kilo Gateway');
    await expect(liteLLMChoice).toHaveCount(0);
    await expect(kiloChoice).toHaveCount(1);
    await account.getByLabel('Use LiteLLM').click();
    await expect(account.getByLabel('Use LiteLLM')).toBeChecked();
    await page.getByLabel('Use Kilo', {exact: true}).click();
    await expect(page.locator('#diagram-provider')).toHaveText('LiteLLM');
    await expect(liteLLMChoice).toHaveCount(1);
    await expect(kiloChoice).toHaveCount(0);
    expect((await state(request, gateway)).hasKey).toBe(true);
  } finally {
    await new Promise(resolve => liteLLM.close(resolve));
  }
});

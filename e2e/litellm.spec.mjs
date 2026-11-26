import {createServer} from 'node:http';
import {once} from 'node:events';
import {createHash} from 'node:crypto';
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
      if (forwarded.length === 2) res.setHeader('x-litellm-response-cost', '0.012345');
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
    const options = {
      headers: {Authorization: 'Bearer ' + local.localKey},
      data: {model: await liteLLMChoice.inputValue(), messages: [{role: 'user', content: 'synthetic test'}]},
    };
    const endpoint = local.baseURL + '/chat/completions';
    expect((await request.post(endpoint, options)).ok()).toBe(true);
    const rows = page.locator('#event-rows');
    const unpricedRow = rows.locator('tr').filter({hasText: 'litellm/'}).filter({hasText: 'Not reported'});
    await expect(unpricedRow).toHaveCount(1);
    expect((await request.post(endpoint, options)).ok()).toBe(true);
    expect(forwarded).toEqual(['vendor/one', 'vendor/one']);
    const pricedRow = rows.locator('tr').filter({hasText: 'LiteLLM-reported cost'});
    await expect(pricedRow).toContainText('$0.012345');
    await expect(unpricedRow).toHaveCount(1);
    await expect(page.locator('#spend-title')).toHaveText('Reported subtotal');
    await expect(page.locator('#spend-total')).toHaveText('$0.012345');

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

async function body(req) {
  let contents = '';
  for await (const chunk of req) contents += chunk;
  return contents;
}

test('LiteLLM browser sign-in supports cancel, separate authorization and disconnect', async ({page, gateway, request}) => {
  const registrations = new Map();
  const revocations = [];
  const accessToken = 'synthetic-litellm-access';
  const refreshToken = 'synthetic-litellm-refresh';
  let address, foreignIssuer = true, exchanges = 0;
  const liteLLM = createServer(async (req, res) => {
    const incoming = new URL(req.url, address);
    res.setHeader('Content-Type', 'application/json');
    switch (req.method + ' ' + incoming.pathname) {
      case 'GET /team/.well-known/litellm-cli-auth':
        res.end(JSON.stringify({
          contract_version: 1,
          issuer: foreignIssuer ? 'https://wrong-issuer.example' : address,
          resource: address,
          authorization_endpoint: address + '/authorize',
          registration_endpoint: address + '/register',
          token_endpoint: address + '/token',
          revocation_endpoint: address + '/revoke',
          response_types_supported: ['code'],
          grant_types_supported: ['authorization_code', 'refresh_token'],
          code_challenge_methods_supported: ['S256'],
          token_endpoint_auth_methods_supported: ['none'],
          revocation_endpoint_auth_methods_supported: ['none'],
        }));
        return;
      case 'POST /team/register': {
        const registration = JSON.parse(await body(req));
        const client = 'synthetic-client-' + (registrations.size + 1);
        registrations.set(client, {redirect: registration.redirect_uris[0]});
        res.writeHead(201).end(JSON.stringify({
          client_id: client,
          token_endpoint_auth_method: 'none',
          redirect_uris: registration.redirect_uris,
        }));
        return;
      }
      case 'GET /team/authorize': {
        const query = incoming.searchParams;
        const registration = registrations.get(query.get('client_id'));
        if (!registration || query.get('redirect_uri') !== registration.redirect || query.get('code_challenge_method') !== 'S256') {
          res.writeHead(400).end();
          return;
        }
        registration.challenge = query.get('code_challenge');
        const callback = new URL(registration.redirect);
        callback.searchParams.set('code', 'synthetic-browser-code');
        callback.searchParams.set('state', query.get('state'));
        res.writeHead(200, {'Content-Type': 'text/html'});
        res.end('<a href="' + callback.href.replaceAll('&', '&amp;').replaceAll('"', '&quot;') + '">Approve synthetic sign-in</a>');
        return;
      }
      case 'POST /team/token': {
        const tokenRequest = new URLSearchParams(await body(req));
        const registration = registrations.get(tokenRequest.get('client_id'));
        if (!registration || registration.redirect !== tokenRequest.get('redirect_uri') ||
            tokenRequest.get('grant_type') !== 'authorization_code' || tokenRequest.get('code') !== 'synthetic-browser-code' ||
            createHash('sha256').update(tokenRequest.get('code_verifier') || '').digest('base64url') !== registration.challenge) {
          res.writeHead(400).end();
          return;
        }
        exchanges++;
        res.end(JSON.stringify({access_token: accessToken, refresh_token: refreshToken, token_type: 'Bearer', expires_in: 1800}));
        return;
      }
      case 'POST /team/revoke': {
        const revocation = new URLSearchParams(await body(req));
        revocations.push(revocation.get('token'));
        res.writeHead(200).end();
        return;
      }
      case 'GET /team/v1/models':
        if (req.headers.authorization === 'Bearer ' + accessToken) {
          res.end(JSON.stringify({data: [{id: 'company/model', name: 'Company model'}]}));
          return;
        }
        res.writeHead(401).end();
        return;
      default:
        res.writeHead(404).end();
    }
  });
  try {
    liteLLM.listen(0, '127.0.0.1');
    await once(liteLLM, 'listening');
    address = 'http://127.0.0.1:' + liteLLM.address().port + '/team';
    await startProxy(page, gateway);
    const account = page.locator('#litellm-account');
    const signIn = account.getByRole('button', {name: 'Sign in in your browser'});
    const launch = account.getByRole('link', {name: 'Open LiteLLM sign-in'});
    await account.getByRole('button', {name: 'Add LiteLLM'}).click();
    await expect(account.getByRole('radio', {name: 'Virtual key', exact: true})).toBeChecked();
    await account.getByLabel('API URL').fill(address);
    await account.getByRole('radio', {name: 'Browser sign-in'}).check();
    await expect(account.getByLabel('LiteLLM virtual key')).toBeHidden();
    await signIn.click();
    await expect(account.getByRole('alert')).toContainText('LiteLLM advertised a different address');
    await expect(launch).toBeHidden();
    expect(registrations.size).toBe(0);
    foreignIssuer = false;

    await signIn.click();
    await expect(launch).toBeVisible();
    await account.getByRole('button', {name: 'Cancel LiteLLM sign-in'}).click();
    await expect(launch).toBeHidden();
    await expect(account.getByLabel('API URL')).toHaveValue(address);
    expect(exchanges).toBe(0);

    await signIn.click();
    await expect(launch).toBeVisible();
    await expect(launch).toHaveAttribute('rel', 'noopener noreferrer');
    const [oauthPage] = await Promise.all([page.context().waitForEvent('page'), launch.click()]);
    expect(await oauthPage.evaluate(() => window.opener === null)).toBe(true);
    await oauthPage.getByRole('link', {name: 'Approve synthetic sign-in'}).click();
    await expect(account.locator('#litellm-status')).toContainText('Connected · 1 model available');
    await expect(launch).toBeHidden();
    await expect(page.locator('#diagram-provider')).toHaveText('Kilo + LiteLLM');
    const connected = await state(request, gateway);
    expect(connected.litellm.authMode).toBe('browser');
    expect(JSON.stringify(connected)).not.toContain(accessToken);
    expect(JSON.stringify(connected)).not.toContain(refreshToken);
    expect(exchanges).toBe(1);

    await account.getByRole('button', {name: 'Disconnect LiteLLM'}).click();
    await expect(account.locator('#litellm-status')).toContainText('Signed out');
    await expect(page.locator('#diagram-provider')).toHaveText('Kilo Gateway');
    expect(revocations).toEqual([refreshToken]);
    expect((await state(request, gateway)).hasKey).toBe(true);
    await page.locator('#language').selectOption('es');
    await account.getByRole('button', {name: 'Volver a conectar LiteLLM'}).click();
    await expect(account.getByRole('radio', {name: 'Inicio de sesión en el navegador'})).toBeChecked();
    await expect(account.getByLabel('URL de la API')).toHaveValue(address + '/v1');
  } finally {
    await new Promise(resolve => liteLLM.close(resolve));
  }
});

import {test as base, expect} from '@playwright/test';
import {mkdtemp, readFile, writeFile, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {spawn} from 'node:child_process';

export const test = base.extend({
  gateway: async ({}, use, testInfo) => {
    const dir = await mkdtemp(path.join(tmpdir(), 'kilo-e2e-fixture-'));
    const manifest = path.join(dir, 'manifest.json'), stop = path.join(dir,'stop');
    const proc = spawn(process.env.KILO_E2E_BINARY, ['-test.run=^TestE2EServer$', '-test.timeout=4m', '-test.v'], {
      env: {...process.env, KILO_E2E_MANIFEST:manifest,KILO_E2E_STOP:stop},
      stdio:['ignore','pipe','pipe'],
    });
    let output='';
    proc.stdout.on('data', data=>output+=data);proc.stderr.on('data', data=>output+=data);
    const exited = new Promise(resolve=>proc.once('exit',(code,signal)=>resolve({code,signal})));
    proc.on('error', error=>output+=String(error));
    let fixture;
    try {
      await expect.poll(async()=>{
        if(proc.exitCode!==null)throw new Error(output);
        try {fixture=JSON.parse(await readFile(manifest,'utf8'));return true;}catch{return false;}
      }, {timeout:20000,message:'Go E2E fixture started'}).toBe(true);
      await use(fixture);
    } finally {
      await writeFile(stop,'stop');
      let timer;
      const result = await Promise.race([exited,new Promise(resolve=>{timer=setTimeout(()=>resolve(null),10000);})]);
      clearTimeout(timer);
      if(!result)proc.kill('SIGKILL');
      await testInfo.attach('go-fixture.log',{body:output,contentType:'text/plain'});
      await rm(dir,{recursive:true,force:true});
      expect(result, 'Go fixture shuts down cleanly').not.toBeNull();
      expect(result?.code, output).toBe(0);
    }
  },
  page: async ({page,gateway},use) => {
    const errors=[];
    page.on('pageerror',error=>errors.push(error.message));
    // Clipboard is a platform boundary: capture the exact payload at the real
    // browser API, without replacing any application API or backend response.
    await page.addInitScript(()=>{
      window.__copied=[];
      Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async text=>{window.__copied.push(text);}}});
    });
    await page.goto(gateway.url);
    await expect(page.locator('#locked')).toBeHidden();
    await expect(page.locator('#status-label')).toHaveText('Proxy stopped');
    await use(page);
    expect(errors,'No unhandled browser errors').toEqual([]);
  },
});
export {expect};

export async function startProxy(page,gateway) {
  await page.locator('#api-key').fill('synthetic-kilo-personal-key');
  await page.locator('#org-id').fill('e2e-team');
  await page.locator('#port').fill(String(gateway.proxyPort));
  await page.locator('#remember').check();
  await page.locator('#start-stop').click();
  await expect(page.locator('#status-label')).toHaveText('Proxy running');
}
export async function state(request,gateway) {
  const response=await request.get(new URL('/api/state',gateway.url).href,{headers:{Authorization:'Bearer '+gateway.token}});
  expect(response.ok()).toBe(true);return response.json();
}
export async function copied(page,button) {
  const count=await page.evaluate(()=>window.__copied.length);
  await page.locator(button).click();
  await expect.poll(()=>page.evaluate(()=>window.__copied.length)).toBe(count+1);
  return page.evaluate(()=>window.__copied.at(-1));
}

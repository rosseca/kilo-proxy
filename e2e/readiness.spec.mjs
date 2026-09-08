import {test,expect,startProxy} from './fixture.mjs';
import {readFile,writeFile} from 'node:fs/promises';

test('connection waits for the first real state before accepting credentials',async({page,gateway})=>{
 await writeFile(gateway.launchControl,JSON.stringify({holdState:true}));
 await page.reload();
 await expect.poll(async()=>{try{return await readFile(gateway.stateWaiting,'utf8');}catch{return '';}}).toBe('waiting');
 for(const id of ['api-key','org-id','port','remember','start-stop','check','sso-login'])await expect(page.locator('#'+id)).toBeDisabled();
 await writeFile(gateway.launchControl,'{}');
 await expect(page.locator('#org-id')).toBeEnabled();
 await startProxy(page,gateway);
 await expect(page.locator('#org-id')).toHaveValue('e2e-team');
 await expect(page.locator('#port')).toHaveValue(String(gateway.proxyPort));
});

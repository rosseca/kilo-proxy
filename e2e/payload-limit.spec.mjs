import {test,expect,startProxy,state} from './fixture.mjs';
import {writeFile} from 'node:fs/promises';

test('with image handling off, oversized image history explains upstream 413 and preserves the original rejection in Activity',async({page,gateway,request})=>{
  // Exercise the gateway's body-limit rejection without local image validation,
  // compression or uploads intercepting this deliberately opaque size fixture.
  await page.locator('#image-transport-mode').selectOption('off');
  await expect.poll(async()=>(await state(request,gateway)).imageTransport.mode).toBe('off');
  await startProxy(page,gateway);
  await page.locator('#capture-activity').check();
  await expect.poll(async()=>(await state(request,gateway)).captureEnabled).toBe(true);
  await writeFile(gateway.launchControl,JSON.stringify({payloadTooLarge:true}));
  const local=await state(request,gateway);
  const response=await request.post(gateway.baseURL+'/responses',{
    headers:{Authorization:'Bearer '+local.localKey,'Thread-Id':'oversized-image-history'},
    data:{model:'vendor/one',input:[{role:'user',content:[
      {type:'input_text',text:'SYNTHETIC_IMAGE_HISTORY'},
      {type:'input_image',image_url:'data:image/png;base64,'+'A'.repeat(4_600_000)},
    ]}]},
  });
  expect(response.status(),await response.text()).toBe(413);
  const failure=await response.json();
  expect(failure.error.code).toBe('upstream_payload_too_large');
  expect(failure.error.message).toContain('4.5 MB');
  expect(failure.error.message).toMatch(/compact/i);
  expect(failure.error.message).toMatch(/new conversation/i);
  await expect(page.locator('#event-rows button')).toHaveCount(1);
  await expect(page.locator('#event-rows')).toContainText('413');
  await page.locator('#event-rows button').click();
  await expect(page.locator('#trace-body')).toContainText('SYNTHETIC_IMAGE_HISTORY');
  await page.locator('#trace-tab-upstreamResponse').click();
  await expect(page.locator('#trace-body')).toContainText('FUNCTION_PAYLOAD_TOO_LARGE');
  await expect(page.locator('#trace-body')).toContainText('synthetic::payload-limit');
  await expect(page.locator('#trace-body')).not.toContainText('upstream_payload_too_large');
  await page.locator('#trace-tab-response').click();
  await expect(page.locator('#trace-body')).toContainText('upstream_payload_too_large');
  await expect(page.locator('#trace-body')).toContainText('4.5 MB');
});

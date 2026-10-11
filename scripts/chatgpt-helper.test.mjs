import test from 'node:test';
import assert from 'node:assert/strict';
import {connectionReady,chatGPTReady,quotaWindows,validChatGPTVerificationURL} from '../ui/chatgpt-helper.mjs';
import {reportedSpend} from '../ui/usage-helper.mjs';
import {openDesignCanLaunch} from '../ui/open-design-helper.mjs';
test('connection readiness supports concurrent accounts or a subscription without Kilo key or organization',()=>{
 assert.equal(connectionReady({chatgpt:{connected:true},connectionReady:true}),true);
 assert.equal(connectionReady({connectionReady:false,hasKey:true,orgId:'old-kilo'}),false);
 assert.equal(connectionReady({hasKey:true,orgId:'team'}),true);
 assert.equal(chatGPTReady({chatgptReady:false,chatgpt:{connected:true,status:'idle'}}),false);
 assert.equal(openDesignCanLaunch({chatgpt:{connected:true},connectionReady:true},'codex-cli',{'codex-cli':{available:true}},{models:[{id:'gpt-5'}]}),true);
});
test('device authorization is restricted to its exact official endpoint',()=>{
 assert.equal(validChatGPTVerificationURL('https://auth.openai.com/codex/device'),true);
 for(const value of ['http://auth.openai.com/codex/device','https://auth.openai.com/codex/device?next=bad','https://auth.openai.com.evil.test/codex/device','https://evil.test/',undefined])assert.equal(validChatGPTVerificationURL(value),false);
});
test('unknown quota windows stay unknown and observed percentages are bounded',()=>{
 assert.deepEqual(quotaWindows({primary:{usedPercent:null},secondary:{}}),[]);
 assert.deepEqual(quotaWindows({primary:{usedPercent:0},secondary:{usedPercent:120}}).map(w=>w.usedPercent),[0,100]);
});
test('subscription usage is separate from monetary prices in both languages',()=>{
 for(const language of ['en','es']){
  const spend=reportedSpend({requests:2,subscriptionRequests:2,priced:0,costUSD:'0.000000'},language);
  assert.equal(spend.label,language==='en'?'Subscription usage':'Uso de suscripción');
  assert.ok(!spend.amount.includes('$'));
  assert.ok(!spend.coverage.includes('without reported cost'));
 }
});

test('mixed unpriced Kilo requests retain unknown cost beside subscription usage',()=>{
 for(const language of ['en','es']){
  const spend=reportedSpend({requests:3,subscriptionRequests:2,priced:0,costUSD:'0.000000'},language);
  assert.equal(spend.amount,language==='en'?'Not reported':'Coste desconocido');
  assert.match(spend.coverage,language==='en'?/0 of 1 requests/:/0 de 1 peticiones/);
  assert.match(spend.coverage,language==='en'?/2 subscription requests/:/2 peticiones de suscripción/);
 }
});

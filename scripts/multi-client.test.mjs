import test from 'node:test';
import assert from 'node:assert/strict';
import {clientConfig} from '../ui/client-config.mjs';
const base={baseURL:'http://127.0.0.1:8877/v1',key:'local-fixture',model:'anthropic/model-b',selectedModels:[{id:'anthropic/model-a',name:'Model A',contextWindow:200000,maxOutputTokens:32000},{id:'anthropic/model-b',name:'Model B'}]};
test('OpenCode registers all exact IDs, selects the default and includes only known limits',()=>{
 const config=JSON.parse(clientConfig({...base,client:'opencode'}));
 assert.equal(config.model,'kilo-local/anthropic/model-b');
 const provider=config.provider['kilo-local'];
 assert.equal(provider.npm,'@ai-sdk/openai-compatible');
 assert.deepEqual(Object.keys(provider.models),['anthropic/model-a','anthropic/model-b']);
 assert.deepEqual(provider.models['anthropic/model-a'].limit,{context:200000,output:32000});
 assert.equal(provider.models['anthropic/model-b'].limit,undefined);
 assert.equal(provider.options.apiKey,'local-fixture');
 assert.equal(provider.options.apiKey,base.key); // The export now includes the local proxy credential.
});
test('Claude independently maps aliases and keeps startup model separate',()=>{
 const config=JSON.parse(clientConfig({...base,client:'claude',aliases:{sonnet:'anthropic/model-a',opus:'anthropic/model-b',haiku:'anthropic/model-a'}}));
 assert.equal(config.env.ANTHROPIC_MODEL,'anthropic/model-b');
 assert.equal(config.env.ANTHROPIC_DEFAULT_SONNET_MODEL,'anthropic/model-a');
 assert.equal(config.env.ANTHROPIC_DEFAULT_OPUS_MODEL,'anthropic/model-b');
 assert.equal(config.env.ANTHROPIC_DEFAULT_HAIKU_MODEL,'anthropic/model-a');
 assert.equal(config.env.ANTHROPIC_AUTH_TOKEN,base.key);
 assert.equal(config.env.ANTHROPIC_BASE_URL,'http://127.0.0.1:8877');
 assert.equal(config.modelPicker,undefined);assert.equal(config.availableModels,undefined);
});
test('Removed defaults and alias targets fall back to an existing selected model',()=>{
 const config=JSON.parse(clientConfig({...base,client:'claude',model:'deleted',aliases:{sonnet:'deleted',opus:'anthropic/model-b'}}));
 assert.equal(config.env.ANTHROPIC_MODEL,'anthropic/model-a');
 assert.equal(config.env.ANTHROPIC_DEFAULT_SONNET_MODEL,'anthropic/model-a');
 assert.equal(config.env.ANTHROPIC_DEFAULT_HAIKU_MODEL,'anthropic/model-a');
 assert.equal(config.env.ANTHROPIC_DEFAULT_OPUS_MODEL,'anthropic/model-b');
});
test('Selection rejects invalid IDs and deduplicates without losing single-model fallback',()=>{
 const config=JSON.parse(clientConfig({...base,client:'opencode',selectedModels:[null,{id:'bad\nvalue'},base.selectedModels[0],base.selectedModels[0]]}));
 assert.deepEqual(Object.keys(config.provider['kilo-local'].models),['anthropic/model-a']);
 const single=JSON.parse(clientConfig({...base,client:'opencode',selectedModels:[]}));
 assert.equal(single.model,'kilo-local/anthropic/model-b');
 assert.deepEqual(Object.keys(single.provider['kilo-local'].models),['anthropic/model-b']);
});

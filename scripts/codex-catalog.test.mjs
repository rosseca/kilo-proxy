import test from 'node:test';
import assert from 'node:assert/strict';
import {codexCatalog} from '../ui/codex-catalog.mjs';
import {clientConfig,launchCommand} from '../ui/client-config.mjs';
test('Codex catalog preserves multiple IDs and contexts without inventing reasoning levels',()=>{
 const catalog=codexCatalog([{id:'vendor/one',name:'One',contextWindow:123000,inputModalities:['text','image']},{id:'vendor/two',name:'Two',reasoning:true},{id:'vendor/one'},{id:'bad id'}]);
 assert.deepEqual(catalog.models.map(m=>m.slug),['vendor/one','vendor/two']);
 assert.equal(catalog.models[0].context_window,123000);
 assert.deepEqual(catalog.models[0].input_modalities,['text','image']);
 assert.equal(catalog.models[1].context_window,undefined);
 for(const m of catalog.models){assert.equal(m.visibility,'list');assert.equal(m.supported_in_api,true);assert.deepEqual(m.supported_reasoning_levels,[]);assert.equal(m.prefer_websockets,false);assert.equal(m.use_responses_lite,false);}
});
test('GUI catalog reference is scoped to Desktop and launch requires both files',()=>{
 const args={baseURL:'http://127.0.0.1:8877/v1',key:'local',model:'vendor/two',catalogPath:'models.json'};
 assert.match(clientConfig({...args,client:'codex'}),/model = "vendor\/two"\nmodel_catalog_json = "models.json"/);
 assert.doesNotMatch(clientConfig({...args,client:'codex-cli'}),/model_catalog_json/);
 assert.doesNotMatch(clientConfig({...args,client:'codex',catalogPath:''}),/model_catalog_json/);
 for(const platform of ['macos','windows','linux']) {
  const launch=launchCommand({client:'codex',key:'local',appPath:'/path/Codex',platform,catalog:true});
  assert.match(launch,/models.json/);
 }
 assert.doesNotMatch(launchCommand({client:'codex-cli',key:'local',catalog:true}),/models.json/);
});

test('chosen GUI default is first in the catalog because model/list uses priority',()=>{
 const catalog=codexCatalog([{id:'v/one'},{id:'v/two'}],'v/two');
 assert.deepEqual(catalog.models.map(m=>m.slug),['v/two','v/one']);
 assert.equal(catalog.models[0].priority,0);
});

test('native reasoning picker has exact presets and no duplicate model variants',()=>{
 const {models}=codexCatalog([{id:'anthropic/claude-fable-5.1'},{id:'openai/gpt-5.6-luna'}]);
 assert.equal(models.length,2);
 for(const model of models) assert.deepEqual(model.supported_reasoning_levels.map(l=>l.effort),['low','medium','high','xhigh','max']);
 assert.equal(models[0].default_reasoning_level,'high');
 assert.equal(models[1].default_reasoning_level,'medium');
});
test('manual levels are ordered, filtered and keep a valid initial level',()=>{
 const make=m=>codexCatalog([{id:'anthropic/claude-fable-5.1',...m}]).models[0];
 assert.equal(make({reasoningLevels:['high','low','oops','low'],defaultReasoning:'high'}).default_reasoning_level,'high');
 assert.deepEqual(make({reasoningLevels:['high','low','oops','low']}).supported_reasoning_levels.map(l=>l.effort),['low','high']);
 assert.equal(make({reasoningLevels:['low'],defaultReasoning:'max'}).default_reasoning_level,'low');
 assert.deepEqual(make({reasoningLevels:[]}).supported_reasoning_levels,[]);
 assert.equal(make({reasoningLevels:[]}).default_reasoning_level,null);
});

test('Sol discounted and GLM 5.3 expose their exact gateway levels and defaults',()=>{
 const models=codexCatalog([{id:'openai/gpt-5.6-sol-discounted'},{id:'z-ai/glm-5.3'},{id:'~z-ai/glm-5.3-flash'}]).models;
 assert.deepEqual(models[0].supported_reasoning_levels.map(l=>l.effort),['none','low','medium','high','xhigh','max']);
 assert.equal(models[0].default_reasoning_level,'low');
 for(const model of models.slice(1)){
  assert.deepEqual(model.supported_reasoning_levels.map(l=>l.effort),['low','high','max']);
  assert.equal(model.default_reasoning_level,'max');
 }
});
test('gateway effort metadata precedes fallback presets while manual choices remain intact',()=>{
 const make=m=>codexCatalog([{id:'openai/gpt-5.6-sol',...m}]).models[0];
 assert.deepEqual(make({reasoningEfforts:['none','low','max']}).supported_reasoning_levels.map(l=>l.effort),['none','low','max']);
 assert.equal(make({reasoningEfforts:['none','low','max']}).default_reasoning_level,'low');
 assert.deepEqual(make({reasoningEfforts:['low','max'],reasoningLevels:['high'],defaultReasoning:'high'}).supported_reasoning_levels.map(l=>l.effort),['high']);
 assert.deepEqual(make({reasoningEfforts:['low','max'],reasoningLevels:[]}).supported_reasoning_levels,[]);
 const unknown=codexCatalog([{id:'vendor/new-route',reasoningEfforts:['high','low','low','invalid']}]).models[0];
 assert.deepEqual(unknown.supported_reasoning_levels.map(l=>l.effort),['low','high']);
});

test('custom Codex labels change display names while IDs, reasoning and defaults stay intact',()=>{
 const entry={id:'openai/gpt-5.6-sol-discounted',name:'OpenAI: GPT-5.6 Sol (50% off)',displayName:'  Sol barato  '};
 const original=codexCatalog([{...entry,displayName:undefined}],entry.id).models[0];
 const renamed=codexCatalog([entry],entry.id).models[0];
 assert.equal(renamed.display_name,'Sol barato');
 assert.deepEqual({...renamed,display_name:original.display_name},original);
 assert.equal(codexCatalog([{...entry,displayName:'   '}]).models[0].display_name,entry.name);
 assert.equal(codexCatalog([{...entry,displayName:'A\nB'}]).models[0].display_name,'A B');
 assert.equal(codexCatalog([{...entry,displayName:'x'.repeat(100)}]).models[0].display_name.length,80);
 // Labels need not be unique: upstream identity remains the exact model slug.
 assert.equal(codexCatalog([{...entry,displayName:'My model'},{id:'z-ai/glm-5.3',displayName:'My model'}]).models.length,2);
});

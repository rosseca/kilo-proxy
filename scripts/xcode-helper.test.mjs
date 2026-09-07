import test from 'node:test';
import assert from 'node:assert/strict';
import {xcodeChatGuide,xcodeSelectionPayload} from '../ui/xcode-helper.mjs';
test('Xcode Chat uses a scoped base URL without repeating v1 and preserves authentication',()=>{
 for(const language of ['en','es']){
  const guide=xcodeChatGuide('http://127.0.0.1:8877/v1','local-test',language);
  assert.match(guide,/URL: http:\/\/127.0.0.1:8877\/xcode\n/);
  assert.match(guide,/API Key Header: Authorization\nAPI Key: Bearer local-test/);
  assert.doesNotMatch(guide,/v1\/v1/);
 }
});
test('Xcode variants preserve exact models and map legacy Claude choices to all three aliases',()=>{
 const models=[{id:'anthropic/claude-sonnet-4.6',displayName:'Sonnet'},{id:'anthropic/claude-opus-4.6',displayName:'Opus'},{id:'vendor/third',displayName:'Third'}];
 const chat=xcodeSelectionPayload('chat',models,models[1].id);
 assert.equal(chat.models.length,3);assert.equal(chat.initial,models[1].id);
 const claude=xcodeSelectionPayload('claude',models,models[1].id);
 assert.deepEqual(claude.aliases,{sonnet:models[1].id,opus:models[0].id,haiku:models[2].id});
 const codex=xcodeSelectionPayload('codex',models,models[1].id);
 assert.equal(codex.catalog.models[0].slug,models[1].id);
 assert.equal(codex.catalog.models[0].display_name,'Opus');
 assert.deepEqual(codex.catalog.models[2].supported_reasoning_levels,[]);
});

test('Xcode Codex strips unsupported max/ultra and keeps valid defaults',()=>{
 const {catalog}=xcodeSelectionPayload('codex',[{id:'z-ai/glm-5.3'},{id:'openai/gpt-5.6-sol',reasoningLevels:['max','ultra']}],'z-ai/glm-5.3');
 assert.deepEqual(catalog.models[0].supported_reasoning_levels.map(r=>r.effort),['low','high']);
 assert.equal(catalog.models[0].default_reasoning_level,'high');
 assert.deepEqual(catalog.models[1].supported_reasoning_levels,[]);
 assert.equal(catalog.models[1].default_reasoning_level,null);
});

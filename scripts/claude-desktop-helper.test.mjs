import test from 'node:test';
import assert from 'node:assert/strict';
import {claudeDesktopModelAllowed,claudeDesktopModelSupported,claudeDesktopSelection} from '../ui/claude-desktop-helper.mjs';

test('Desktop filters mixed libraries without changing their models or shared default',()=>{
 const source=[{id:'openai/gpt-5',displayName:'GPT',contextWindow:272000},{id:'anthropic/claude-sonnet-4.6',displayName:'Daily Claude',contextWindow:128000,maxOutputTokens:32000},{id:'anthropic/claude-opus-4.6',name:'Deep Claude'}];
 const before=JSON.stringify(source),result=claudeDesktopSelection(source,'openai/gpt-5');
 assert.equal(result.omitted,1);assert.equal(result.fallback,true);
 assert.deepEqual(result.selection,{models:[{id:'anthropic/claude-sonnet-4.6',name:'Daily Claude'},{id:'anthropic/claude-opus-4.6',name:'Deep Claude'}],initial:'anthropic/claude-sonnet-4.6'});
 assert.equal(JSON.stringify(source),before);
 assert.doesNotMatch(JSON.stringify(result.selection),/contextWindow|maxOutputTokens|reasoning/);
 const compatible=claudeDesktopSelection(source,'anthropic/claude-opus-4.6');
 assert.equal(compatible.selection.initial,'anthropic/claude-opus-4.6');assert.equal(compatible.fallback,false);
});

test('Experimental Desktop accepts real provider IDs and keeps the shared default and names',()=>{
 const source=[{id:'openai/gpt-5',displayName:'Shared GPT',contextWindow:272000},{id:'moonshot/kimi-k2.5',name:'Kimi',maxOutputTokens:32000},{id:'anthropic/claude-sonnet-4.6',displayName:'Daily Claude',reasoning:{enabled:true}}];
 const before=JSON.stringify(source),enabled=claudeDesktopSelection(source,'openai/gpt-5',true);
 assert.deepEqual(enabled,{selection:{models:[{id:'openai/gpt-5',name:'Shared GPT'},{id:'moonshot/kimi-k2.5',name:'Kimi'},{id:'anthropic/claude-sonnet-4.6',name:'Daily Claude'}],initial:'openai/gpt-5'},omitted:0,fallback:false});
 const disabled=claudeDesktopSelection(source,'openai/gpt-5',false);
 assert.equal(disabled.selection.initial,'anthropic/claude-sonnet-4.6');assert.equal(disabled.omitted,2);
 assert.equal(JSON.stringify(source),before);
 assert.deepEqual(claudeDesktopSelection(source,'openai/gpt-5',true),enabled);
 for(const id of ['openai/gpt-5','moonshot/kimi-k2.5','z-ai/glm','vendor/claude-sonnet-4.6','~local-model','model:tag','x'.repeat(200)])assert.equal(claudeDesktopModelAllowed(id,true),true,id);
});

test('Reserved aliases and invalid real IDs are rejected in both Desktop modes',()=>{
 for(const id of ['claude-kilo-v1-abc','anthropic/claude-kilo-v1-abc','claude-kilo-v1-','anthropic/claude-kilo-v1-','bad id','bad\nID','model\n','model\r','model\u2028','claude-sonnet\n','-bad','x'.repeat(201),'',null,undefined]){
  assert.equal(claudeDesktopModelSupported(id),false,String(id));
  assert.equal(claudeDesktopModelAllowed(id,false),false,String(id));
  assert.equal(claudeDesktopModelAllowed(id,true),false,String(id));
 }
 const alias='claude-kilo-v1-abc';
 assert.deepEqual(claudeDesktopSelection([{id:alias},{id:'openai/gpt-5'}],alias,true),{selection:{models:[{id:'openai/gpt-5',name:'openai/gpt-5'}],initial:'openai/gpt-5'},omitted:1,fallback:true});
});

test('Desktop does not let other model providers masquerade as Claude',()=>{
 for(const id of ['anthropic/claude-sonnet-4.6','claude-opus-4-6'])assert.equal(claudeDesktopModelSupported(id),true,id);
 for(const id of ['openai/gpt-5','moonshot/kimi','z-ai/glm','vendor/claude-sonnet-4.6','anthropic/not-claude','bad id','anthropic/claude-GPT-5','claude-phi4','claude-K2.5','claude-gemini','claude-a/other','claude-'+ 'x'.repeat(194)])assert.equal(claudeDesktopModelSupported(id),false,id);
 assert.equal(claudeDesktopModelSupported('claude-'+'x'.repeat(193)),true);
 const result=claudeDesktopSelection([{id:'openai/gpt-5'}],'openai/gpt-5');
 assert.deepEqual(result.selection,{models:[],initial:''});assert.equal(result.omitted,1);
});

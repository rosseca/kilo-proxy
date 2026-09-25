import test from 'node:test';
import assert from 'node:assert/strict';
import {claudeDesktopModelSupported,claudeDesktopSelection} from '../ui/claude-desktop-helper.mjs';

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

test('Desktop does not let other model providers masquerade as Claude',()=>{
 for(const id of ['anthropic/claude-sonnet-4.6','claude-opus-4-6'])assert.equal(claudeDesktopModelSupported(id),true,id);
 for(const id of ['openai/gpt-5','moonshot/kimi','z-ai/glm','vendor/claude-sonnet-4.6','anthropic/not-claude','bad id','anthropic/claude-GPT-5','claude-phi4','claude-K2.5','claude-gemini','claude-a/other','claude-'+ 'x'.repeat(194)])assert.equal(claudeDesktopModelSupported(id),false,id);
 assert.equal(claudeDesktopModelSupported('claude-'+'x'.repeat(193)),true);
 const result=claudeDesktopSelection([{id:'openai/gpt-5'}],'openai/gpt-5');
 assert.deepEqual(result.selection,{models:[],initial:''});assert.equal(result.omitted,1);
});

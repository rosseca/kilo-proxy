import test from 'node:test';
import assert from 'node:assert/strict';
import {contextModel, contextLimits, contextLibraryFields, resolveContextPolicy, syncContextModels, contextError} from '../ui/context-policy.mjs';
import {codexCatalog} from '../ui/codex-catalog.mjs';
import {editorPayload} from '../ui/editor-helper.mjs';
import {ompPayload, ompConfig} from '../ui/omp-helper.mjs';
import {claudeSelection, claudeSettings, claudeContextTag} from '../ui/claude-helper.mjs';
import {openDesignLibrary} from '../ui/open-design-helper.mjs';
import {clientConfig} from '../ui/client-config.mjs';

test('presets bound the chosen working window without changing the catalog maximum', () => {
 const catalog={id:'vendor/large',contextWindow:1050000,maxOutputTokens:128000};
 const selected=contextModel(catalog);
 assert.equal(selected.contextPreset,'recommended');
 assert.deepEqual(contextLimits(selected),{contextWindow:272000,maxOutputTokens:68000,autoCompactTokenLimit:195808});
 assert.equal(contextLimits({...selected,contextPreset:'low'}).contextWindow,128000);
 assert.equal(contextLimits({...selected,contextPreset:'maximum'}).contextWindow,1050000);
 assert.equal(contextLimits({...selected,contextPreset:'custom',contextTokens:400000}).contextWindow,400000);
 assert.equal(catalog.contextWindow,1050000);
 for(const preset of ['recommended','low','maximum','custom'])assert.equal(resolveContextPolicy(preset,500000,64000,128000).contextWindow,64000);
});

test('unknown maxima cannot silently turn Maximum into another preset', () => {
 assert.equal(contextLimits({id:'manual/new'}).contextWindow,272000);
 assert.throws(()=>contextLimits({id:'manual/new',contextPreset:'maximum'}),/maximum context/);
 for(const value of [0,1000,1024.5,100000001,NaN])assert.throws(()=>resolveContextPolicy('custom',value),/whole number/);
 assert.throws(()=>resolveContextPolicy('agent-default'),/valid context preset/);
 const minimum=resolveContextPolicy('custom',1024,0,128000);
 assert.equal(minimum.maxOutputTokens,256);
 assert.ok(minimum.autoCompactTokenLimit<minimum.contextWindow-minimum.maxOutputTokens);
});

test('saved library policy survives refresh while legacy limits remain explicit custom choices', () => {
 const catalog={id:'vendor/large',contextWindow:1050000};
 const legacy=contextModel({id:catalog.id,contextWindow:400000},catalog,true);
 assert.equal(legacy.contextPreset,'custom');assert.equal(contextLimits(legacy).contextWindow,400000);
 assert.deepEqual(contextLibraryFields(legacy),{contextPreset:'custom',contextWindow:400000});
 const recommended=contextModel({id:catalog.id,contextPreset:'recommended'},catalog,true);
 assert.equal(contextLimits(recommended).contextWindow,272000);
 assert.deepEqual(contextLibraryFields(recommended),{contextPreset:'recommended'});
 const refreshed=contextModel(recommended,{...catalog,contextWindow:64000});
 assert.equal(contextLimits(refreshed).contextWindow,64000);
 assert.equal(contextLimits(contextModel(refreshed,catalog)).contextWindow,272000);
 const library=openDesignLibrary([recommended,legacy]);
 assert.doesNotMatch(JSON.stringify(library),/contextMaximum|contextTokens|1050000/);
});

test('Codex and editor/OMP exports preserve Maximum, custom limits and output headroom', () => {
 const models=[{id:'vendor/large',contextWindow:1050000,maxOutputTokens:128000,contextPreset:'maximum'}, {id:'vendor/small',contextWindow:64000,contextPreset:'recommended'}, {id:'vendor/unknown',contextPreset:'low'}];
 const codex=codexCatalog(models).models;
 assert.deepEqual(codex.map(m=>m.context_window),[1050000,64000,128000]);
 assert.deepEqual(codex.map(m=>m.auto_compact_token_limit),models.map(m=>contextLimits(m).autoCompactTokenLimit));
 for(const payload of [editorPayload(models,''),ompPayload(models,'')]) {
  assert.deepEqual(payload.models.map(m=>m.contextWindow),[1050000,64000,128000]);
  for(const m of payload.models)assert.ok(m.maxOutputTokens<=m.contextWindow/4);
 }
 const exportOMP=JSON.parse(ompConfig({baseURL:'http://127.0.0.1:8877/v1',key:'local',selectedModels:models}));
 assert.equal(exportOMP.providers['kilo-local'].models[0].contextWindow,1050000);
 const openCode=JSON.parse(clientConfig({client:'opencode',selectedModels:editorPayload(models,'').models}));
 assert.equal(openCode.provider['kilo-local'].models['vendor/large'].limit.context,1050000);
 assert.deepEqual(openCode.provider['kilo-local'].models['vendor/unknown'].limit,{context:128000,output:8192});
 assert.equal(openCode.compaction.auto,true);
});

test('Claude exports a conservative session budget and rejects unsupported small windows', () => {
 const selection=claudeSelection([{id:'vendor/large',contextWindow:1050000},{id:'vendor/medium',contextWindow:200000,contextPreset:'low'}],'vendor/large');
 const settings=claudeSettings(selection,{},'http://127.0.0.1:8877/v1','local');
 assert.equal(settings.autoCompactWindow,128000);assert.equal(settings.autoCompactEnabled,true);
 assert.equal(settings.env.CLAUDE_CODE_AUTO_COMPACT_WINDOW,'128000');
 assert.equal(settings.env.CLAUDE_CODE_MAX_OUTPUT_TOKENS,'8192');
 assert.throws(()=>claudeSettings(claudeSelection([{id:'vendor/tiny',contextWindow:64000}]),{},'http://127.0.0.1:8877/v1','local'),/at least 100,000/);
});

test('Claude tags only the models whose catalog maximum reaches the 1M ceiling', () => {
 for(const [maximum,tagged] of [[0,false],[999999,false],[1000000,true],[1048576,true]])assert.equal(claudeContextTag('vendor/model',maximum)==='vendor/model',!tagged);
 // A recommended working preset below 1M must not hide the catalog maximum: without
 // the tag Claude Code assumes ~200K and the model's real ceiling is unreachable.
 const selection=claudeSelection([{id:'vendor/large',contextWindow:1050000},{id:'vendor/medium',contextWindow:200000}],'vendor/large');
 assert.equal(selection.models[0].contextMaximum,1050000);assert.equal(selection.models[1].contextMaximum,200000);
 const settings=claudeSettings(selection,{},'http://127.0.0.1:8877/v1','local');
 assert.equal(settings.model,'vendor/large[1m]');
 assert.equal(settings.env.ANTHROPIC_MODEL,'vendor/large[1m]');
 assert.deepEqual(settings.modelPicker,undefined);
 // The tag lifts Claude Code's assumed ceiling; the preset still sets the budget.
 assert.equal(settings.autoCompactWindow,200000);
 const maximum=claudeSettings(claudeSelection([{id:'vendor/large',contextWindow:1050000,contextPreset:'maximum'}],'vendor/large'),{},'http://127.0.0.1:8877/v1','local');
 assert.equal(maximum.model,'vendor/large[1m]');assert.equal(maximum.autoCompactWindow,1000000);
 const unlisted=claudeSelection([{id:'vendor/large'}],'vendor/large');
 assert.equal(claudeSettings(unlisted,{},'http://127.0.0.1:8877/v1','local').model,'vendor/large');
});


test('output defaults and overrides never exceed the separately retained provider cap', () => {
 const model=contextModel({id:'vendor/tiny-output',contextWindow:128000,maxOutputTokens:4000});
 assert.equal(contextLimits({...model,maxOutputTokens:0}).maxOutputTokens,4000);
 assert.equal(contextLimits({...model,maxOutputTokens:128000}).maxOutputTokens,4000);
 assert.equal(contextLimits({...model,maxOutputTokens:2000}).maxOutputTokens,2000);
});


test('fresh unknown or missing metadata clears stale maxima for hidden selections', () => {
 const previous=contextModel({id:'vendor/large',contextPreset:'maximum',contextWindow:1050000,maxOutputTokens:128000});
 assert.equal(contextLimits(contextModel(previous)).contextWindow,1050000);
 const selected=new Map([[previous.id,previous]]);
 syncContextModels(selected,[{id:previous.id,contextWindow:64000,maxOutputTokens:4000}]);
 assert.equal(contextLimits(previous).contextWindow,64000);
 assert.equal(contextLimits(previous).maxOutputTokens,4000);
 syncContextModels(selected,[{id:previous.id,contextWindow:0,maxOutputTokens:0}]);
 assert.equal(previous.contextMaximum,0);assert.equal(previous.maximumOutputTokens,0);
 assert.match(contextError(selected),/maximum context/);
 syncContextModels(selected,[{id:previous.id,contextWindow:1050000,maxOutputTokens:128000}]);
 assert.equal(contextError(selected),'');
 syncContextModels(selected,[]);
 assert.match(contextError(selected),/maximum context/);
});

import test from 'node:test';
import assert from 'node:assert/strict';
import {openDesignLibrary, openDesignCanLaunch, openDesignEngines} from '../ui/open-design-helper.mjs';

const state = {baseURL:'http://127.0.0.1:8877/v1', localKey:'kl_local_synthetic', hasKey:true, orgId:'synthetic-team'};
const engines = {'codex-cli':{available:true}, claude:{available:true}, opencode:{available:true}};
const models = [{id:'vendor/first', name:'Friendly First'}, {id:'vendor/second', name:'Friendly Second'}];

test('Open Design profile payload retains exact IDs and initial choice without credentials or catalog prices', () => {
 const library = openDesignLibrary([{...models[0], apiKey:'secret', pricing:{input:5}, contextWindow:64000, maxOutputTokens:4096}, models[1]], 'vendor/second');
 assert.deepEqual(library, {schemaVersion:1, defaultModel:'vendor/second', models:[{id:'vendor/first', displayName:'Friendly First', contextWindow:64000, maxOutputTokens:4096}, {id:'vendor/second', displayName:'Friendly Second'}]});
 assert.doesNotMatch(JSON.stringify(library), /secret|pricing|apiKey/);
});

test('saved custom reasoning and names survive profile preparation without fabricating unknown limits', () => {
 const levels = ['low', 'high'];
 const library = openDesignLibrary([{...models[0], displayName:'My model', reasoningCustom:true, reasoningLevels:levels, reasoningEffort:'high'}, {id:'manual/new'}]);
 assert.equal(library.models[0].displayName, 'My model');
 assert.equal(library.models[0].reasoningEffort, 'high');
 assert.deepEqual(library.models[0].reasoningLevels, levels);
 library.models[0].reasoningLevels.push('max');
 assert.deepEqual(levels, ['low', 'high']);
 assert.equal(library.models[1].contextWindow, undefined);
 assert.equal(library.models[1].maxOutputTokens, undefined);
});

test('launch requires a supported installed engine, a selected model and completed account connection', () => {
 const library = openDesignLibrary(models);
 assert.deepEqual(openDesignEngines, ['codex-cli', 'claude', 'opencode']);
 for (const engine of openDesignEngines) assert.equal(openDesignCanLaunch(state, engine, engines, library), true);
 for (const missing of [{hasKey:false}, {orgId:''}, {orgId:'   '}, {auth:{status:'pending'}}, {auth:{status:'starting'}}]) assert.equal(openDesignCanLaunch({...state, ...missing}, 'codex-cli', engines, library), false);
 assert.equal(openDesignCanLaunch(state, 'codex-cli', {'codex-cli':{available:false}}, library), false);
 assert.equal(openDesignCanLaunch(state, 'cursor', engines, library), false);
 assert.equal(openDesignCanLaunch(state, 'codex-cli', engines, openDesignLibrary()), false);
 assert.equal(openDesignCanLaunch(undefined, 'codex-cli', engines, library), false);
 assert.equal(openDesignCanLaunch({...state, running:false}, 'codex-cli', engines, library), true);
});

test('selection drops malformed and duplicate IDs and recovers a removed initial model', () => {
 const library = openDesignLibrary([null, {id:44}, {id:'bad\nmodel'}, ...models, models[0]], 'removed/model');
 assert.deepEqual(library.models.map(model => model.id), ['vendor/first', 'vendor/second']);
 assert.equal(library.defaultModel, 'vendor/first');
 assert.equal(openDesignLibrary(Array.from({length:60}, (_, i) => ({id:'vendor/'+i}))).models.length, 50);
});

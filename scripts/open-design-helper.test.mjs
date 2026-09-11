import test from 'node:test';
import assert from 'node:assert/strict';
import {openDesignConnection, openDesignMaskedKey} from '../ui/open-design-helper.mjs';

const state = {baseURL:'http://127.0.0.1:8877/v1', localKey:'kl_local_synthetic_credential', hasKey:true, orgId:'synthetic-team'};
const models = [{id:'vendor/first', name:'Friendly First'}, {id:'vendor/second', name:'Friendly Second'}];

test('Open Design connection uses exact IDs and the real key independently of its masked preview', () => {
 const values = openDesignConnection(state, models, 'vendor/second');
 assert.equal(values.model, 'vendor/second');
 assert.deepEqual(values.ids, ['vendor/first', 'vendor/second']);
 assert.equal(values.key, state.localKey);
 assert.notEqual(values.key, openDesignMaskedKey);
 assert.equal(values.canLaunch, true);
 assert.doesNotMatch(JSON.stringify(values), /Friendly/);
});

test('current proxy credentials and port replace earlier copy fields', () => {
 const old = openDesignConnection(state, models, 'vendor/first');
 const changed = openDesignConnection({...state, baseURL:'http://127.0.0.1:9900/v1', localKey:'kl_local_rotated_synthetic'}, models, 'vendor/second');
 assert.equal(changed.baseURL, 'http://127.0.0.1:9900/v1');
 assert.equal(changed.key, 'kl_local_rotated_synthetic');
 assert.equal(changed.model, 'vendor/second');
 assert.notEqual(changed.key, old.key);
 assert.notEqual(changed.baseURL, old.baseURL);
});

test('launch requires a selected model and a completed Kilo account and team', () => {
 for (const missing of [{hasKey:false}, {orgId:''}, {orgId:'   '}, {localKey:''}, {baseURL:''}, {auth:{status:'pending'}}, {auth:{status:'starting'}}]) {
  assert.equal(openDesignConnection({...state, ...missing}, models).canLaunch, false, JSON.stringify(missing));
 }
 assert.equal(openDesignConnection(state, []).canLaunch, false);
 assert.equal(openDesignConnection(undefined, models).canLaunch, false);
 assert.equal(openDesignConnection({...state, running:false}, models).canLaunch, true, 'The backend may start a configured but stopped proxy');
});

test('selection drops malformed and duplicate IDs and recovers when initial model is removed', () => {
 const values = openDesignConnection(state, [null, {id:44}, {id:'bad\nmodel'}, ...models, models[0]], 'removed/model');
 assert.deepEqual(values.ids, ['vendor/first', 'vendor/second']);
 assert.equal(values.model, 'vendor/first');
 assert.equal(openDesignConnection(state, Array.from({length:60}, (_, i) => ({id:'vendor/'+i}))).ids.length, 50);
});

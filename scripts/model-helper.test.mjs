import test from 'node:test';
import assert from 'node:assert/strict';
import {filterModels,formatPrice,validModelID} from '../ui/model-helper.mjs';
import {clientConfig} from '../ui/client-config.mjs';
import {translate} from '../ui/i18n.mjs';
const models = [
 {id:'vendor/coder',name:'Code Model',tools:true,outputModalities:['text'],contextWindow:128000},
 {id:'vendor/image',name:'Image Model',tools:true,outputModalities:['image']},
 {id:'vendor/unknown',name:'Unknown Model',tools:null,outputModalities:['text']}
];
test('coding filter requires explicit tools and text, with all-model fallback and search',()=>{
 assert.deepEqual(filterModels(models).map(m=>m.id),['vendor/coder']);
 assert.equal(filterModels(models,'VENDOR CODE').length,1);
 assert.equal(filterModels(models,'missing').length,0);
 assert.equal(filterModels(models,'',false).length,3);
});
test('published prices distinguish zero from missing or variable rates in both languages',()=>{
 assert.equal(formatPrice(3,'en'),'$3.00');
 assert.equal(formatPrice(0,'en'),'$0.00');
 assert.equal(formatPrice(0.000001,'en'),'$0.000001');
 for(const value of [null,undefined,-1,NaN,Infinity,'3']) assert.equal(formatPrice(value),null);
 assert.match(formatPrice(3,'es'),/3,00/);
 assert.equal(translate('Variable / sin dato','en'),'Variable / unavailable');
});
test('catalog model ID and context produce client config without hardcoded model aliases',()=>{
 const m=models[0];
 const config=JSON.parse(clientConfig({client:'zed',baseURL:'http://127.0.0.1:8877/v1',key:'local',model:m.id,contextWindow:m.contextWindow}));
 assert.deepEqual(config.language_models.openai_compatible['kilo-local'].available_models,[{name:m.id,display_name:m.id,max_tokens:128000}]);
 assert.equal(validModelID(m.id),true);
 for(const id of ['', 'a b','a\nb','x'.repeat(257)]) assert.equal(validModelID(id),false);
});

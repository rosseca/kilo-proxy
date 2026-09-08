import test from 'node:test';
import assert from 'node:assert/strict';
import {filterModels,formatPrice,validModelID,sortModels,modelSortOptions,mergeModelSelection,modelLab,modelLabOptions,filterModelLab} from '../ui/model-helper.mjs';
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
test('all five model sorts use published directions and deterministic names and IDs',()=>{
 const catalog=[
  {id:'vendor/b',name:'Beta',codeModeRank:1,codingIndex:20,speed:0,inputPrice:0},
  {id:'vendor/z',name:'Zulu',displayName:'Alpha',codeModeRank:2,codingIndex:60,speed:200,inputPrice:3},
  {id:'vendor/a',name:'ALPHA',codeModeRank:2,codingIndex:60,speed:200,inputPrice:1},
  {id:'vendor/missing',name:'A missing metric'}
 ];
 const ids=order=>sortModels(catalog,order).map(m=>m.id);
 assert.deepEqual(ids('codeModeRank'),['vendor/b','vendor/a','vendor/z','vendor/missing']);
 assert.deepEqual(ids('codingIndex'),['vendor/a','vendor/z','vendor/b','vendor/missing']);
 assert.deepEqual(ids('speed'),['vendor/a','vendor/z','vendor/b','vendor/missing']);
 assert.deepEqual(ids('price'),['vendor/b','vendor/a','vendor/z','vendor/missing']);
 assert.deepEqual(ids('name'),['vendor/missing','vendor/a','vendor/z','vendor/b']);
 assert.deepEqual(sortModels(catalog).map(m=>m.id),ids('codeModeRank'));
 assert.deepEqual(sortModels(catalog,'unknown').map(m=>m.id),ids('codeModeRank'));
});
test('unavailable numeric metadata sorts last while explicit free price and zero measurements remain valid',()=>{
 for(const [order,field] of [['codingIndex','codingIndex'],['speed','speed'],['price','inputPrice'],['codeModeRank','codeModeRank']]) {
  const invalid=[undefined,null,'20',NaN,Infinity,-1,...(order==='codeModeRank'?[0,1.5]:[])];
  const rows=invalid.map((value,i)=>({id:'missing/'+i,name:'A missing '+i,[field]:value}));
  const known={id:'known',name:'Z known',[field]:order==='codeModeRank'?1:0};
  assert.equal(sortModels([...rows,known],order)[0].id,'known',order);
 }
});
test('sorting is a view operation that preserves selection order, initial model and custom names',()=>{
 const first=Object.freeze({id:'one',name:'Catalog A',displayName:'Zebra',speed:50});
 const second=Object.freeze({id:'two',name:'Catalog Z',displayName:'Apple',speed:100});
 const selection=new Map([[first.id,first],[second.id,second]]),initial='one';
 const rows=Object.freeze([...selection.values()]);
 for(const {value} of modelSortOptions()) {
  const sorted=sortModels(rows,value);
  assert.notEqual(sorted,rows);
  assert.deepEqual([...selection.keys()],['one','two']);
  assert.equal(initial,'one');
  assert.equal(selection.get('one').displayName,'Zebra');
 }
 assert.deepEqual(sortModels(rows,'name').map(m=>m.id),['two','one']);
});
test('sort choices have stable keys and English and Spanish labels',()=>{
 assert.deepEqual(modelSortOptions().map(o=>o.label),['Code Mode Rank','Coding Index','Speed','Price','Name']);
 assert.deepEqual(modelSortOptions('es').map(o=>o.label),['Ranking de Code Mode','Índice de programación','Velocidad','Precio','Nombre']);
 assert.deepEqual(modelSortOptions().map(o=>o.value),modelSortOptions('es').map(o=>o.value));
 assert.equal(translate('Ordenar por','en'),'Sort by');
});
test('catalog refresh replaces observed metrics without replacing saved model choices',()=>{
 const saved=Object.freeze({id:'vendor/model',name:'Saved name',displayName:'Short',speed:200,codingIndex:90,codeModeRank:1,inputPrice:8,contextWindow:50000,effort:'high'});
 const current=Object.freeze({id:saved.id,name:'Gateway name',speed:100,inputPrice:0});
 const merged=mergeModelSelection(current,saved);
 assert.equal(merged.speed,100);assert.equal(merged.inputPrice,0);
 assert.equal(merged.codeModeRank,undefined);assert.equal(merged.codingIndex,undefined);
 assert.equal(merged.displayName,'Short');assert.equal(merged.name,'Saved name');assert.equal(merged.contextWindow,50000);assert.equal(merged.effort,'high');
 assert.equal(saved.speed,200);
 assert.deepEqual(mergeModelSelection(undefined,saved),saved);
});
test('lab identity uses provider metadata or the ID prefix without rewriting exact model IDs',()=>{
 const entries=[
  {id:'vendor/model',provider:' OpenAI '},
  {id:'~anthropic/claude'},
  {id:'anthropic/claude',provider:'~ANTHROPIC'},
  {id:'future-lab/manual',provider:' '},
  {id:'single-model'},
  {id:''}
 ];
 const before=JSON.stringify(entries);
 assert.deepEqual(entries.map(modelLab),['openai','anthropic','anthropic','future-lab','single-model','']);
 assert.equal(JSON.stringify(entries),before);
 assert.deepEqual(filterModelLab(entries,'~ANTHROPIC').map(m=>m.id),['~anthropic/claude','anthropic/claude']);
});
test('lab options are discovered from models, deduplicate prefixes and keep friendly known names',()=>{
 const catalog=['openai','anthropic','~anthropic','google','deepseek','meta-llama','mistralai','qwen','x-ai','z-ai','minimax','moonshotai','future-lab'].map(provider=>({id:provider+'/model'}));
 const options=modelLabOptions(catalog);
 assert.deepEqual(options,[
  {value:'',label:'All labs'},
  {value:'anthropic',label:'Anthropic'},
  {value:'deepseek',label:'DeepSeek'},
  {value:'future-lab',label:'Future Lab'},
  {value:'google',label:'Google'},
  {value:'meta-llama',label:'Meta'},
  {value:'minimax',label:'MiniMax'},
  {value:'mistralai',label:'Mistral AI'},
  {value:'moonshotai',label:'Moonshot AI'},
  {value:'openai',label:'OpenAI'},
  {value:'qwen',label:'Qwen'},
  {value:'x-ai',label:'xAI'},
  {value:'z-ai',label:'Z.ai'}
 ]);
 assert.deepEqual(modelLabOptions([]),[{value:'',label:'All labs'}]);
 assert.deepEqual(modelLabOptions([{id:'new_org/manual'}],'es'),[{value:'',label:'Todos los laboratorios'},{value:'new_org',label:'New Org'}]);
 assert.deepEqual(modelLabOptions([{id:'constructor/manual'},{id:'__proto__/manual'}]),[{value:'',label:'All labs'},{value:'constructor',label:'Constructor'},{value:'__proto__',label:'Proto'}]);
 assert.deepEqual(modelLabOptions([{id:'___/manual'},{id:'---/manual'}]),[{value:'',label:'All labs'},{value:'---',label:'---'},{value:'___',label:'___'}]);
 assert.equal(translate('Laboratorio','en'),'Lab');
});
test('lab filters combine with search and coding filters while preserving hidden selection order and defaults',()=>{
 const selected=new Map([
  ['openai/code',{id:'openai/code',name:'Code One',tools:true,outputModalities:['text'],inputPrice:3}],
  ['anthropic/code',{id:'anthropic/code',displayName:'My name',name:'Code Two',tools:true,outputModalities:['text'],inputPrice:1}],
  ['anthropic/image',{id:'anthropic/image',name:'Code Image',tools:true,outputModalities:['image'],inputPrice:0}]
 ]);
 const snapshot=JSON.stringify([...selected]),initial='openai/code';
 const visible=sortModels(filterModels(filterModelLab([...selected.values()],'anthropic'),'Code'),'price');
 assert.deepEqual(visible.map(m=>m.id),['anthropic/code']);
 assert.deepEqual(filterModelLab([...selected.values()],'missing'),[]);
 assert.deepEqual(filterModelLab([...selected.values()]).map(m=>m.id),[...selected.keys()]);
 assert.equal(JSON.stringify([...selected]),snapshot);
 assert.equal(initial,'openai/code');
});

import test from 'node:test';
import assert from 'node:assert/strict';
import {createT3CodeHelper, t3CodePrepared} from '../ui/t3-code-helper.mjs';

const saved = () => ({revision:2,library:{schemaVersion:1,defaultModel:'vendor/second',models:[{id:'vendor/first',displayName:'Friendly',reasoningEffort:'high'},{id:'vendor/second'}]}});
const tick = () => new Promise(resolve => setImmediate(resolve));
function fixture(t, api) {
 const previous=globalThis.document,nodes=new Map();
 const element=()=>({children:[],textContent:'',classList:{toggle(){}},replaceChildren(){this.children=[];},append(item){this.children.push(item);},addEventListener(){}});
 globalThis.document={getElementById(id){if(!nodes.has(id))nodes.set(id,element());return nodes.get(id);},createElement:element};
 t.after(()=>{globalThis.document=previous;});
 const helper=createT3CodeHelper({api,onChange(){}});
 helper.render({state:{connectionReady:true,baseURL:'http://127.0.0.1:8877/v1',localKey:'private-local-secret'},language:'en'});
 return {helper,nodes};
}

test('prepared status requires the current complete shared library',()=>{
 const source=saved(),profile={prepared:true,library:structuredClone(source.library)};
 assert.equal(t3CodePrepared(source,profile),true);
 profile.library.models[0].reasoningEffort='low';
 assert.equal(t3CodePrepared(source,profile),false);
 assert.equal(t3CodePrepared({...source,recoveryRequired:true},profile),false);
});

test('T3 refuses a library exceeding its 32 custom-model limit before writing', async t => {
 const source = saved(), writes = [];
 source.library.models = Array.from({length:33}, (_, i) => ({id:'vendor/model-'+i}));
 source.library.defaultModel = 'vendor/model-32';
 const {helper} = fixture(t, async (path, body) => {
  if (body) writes.push(body);
  return path === 'model-library' ? source : {};
 });
 await tick();
 assert.equal(helper.launchState().valid, false);
 assert.match(helper.launchState().reason, /32 shared models/);
 await assert.rejects(helper.launchState().prepare(), /32 shared models/);
 assert.equal(writes.length, 0);
 helper.render({state:{connectionReady:true},language:'es'});
 assert.match(helper.launchState().reason, /32 modelos compartidos/);
});

test('four agents use an immutable common-library snapshot without exposing credentials or profile paths',async t=>{
 const source=saved(),calls=[];
 const {helper,nodes}=fixture(t,async(path,body)=>{calls.push({path,body});return path==='model-library'?source:{prepared:true,library:structuredClone(source.library),profileDir:'/private/profile/secret',version:'0.0.45'};});
 await tick();
 assert.equal(helper.launchState().valid,true);
 assert.equal(helper.launchState().ready,false);
 await helper.launchState().prepare();
 const sent=calls.find(call=>call.body);
 assert.equal(sent.path,'clients/t3-code');
 assert.deepEqual(sent.body,{library:source.library});
 source.library.models[0].displayName='Changed later';
 assert.equal(sent.body.library.models[0].displayName,'Friendly');
 assert.equal(calls.filter(call=>call.path==='model-library'&&call.body).length,0);
 assert.deepEqual(nodes.get('t3-code-models').children.map(node=>node.textContent),['vendor/second','Friendly · vendor/first']);
 assert.equal(nodes.get('t3-code-agents').children.length,4);
 assert.match(nodes.get('t3-code-compatibility').textContent,/new chat to switch between normal and Kilo/);
 assert.match(nodes.get('t3-code-requirements').textContent,/not Desktop app logins/);
 assert.match(nodes.get('t3-code-prepared').textContent,/Four agents prepared/);
 assert.doesNotMatch([...nodes.values()].map(node=>node.textContent).join(' '),/private-local-secret|\/private\/profile\/secret/);
});

test('concurrent library changes block prepare or launch and a failed refresh clears stale data',async t=>{
 let source=saved(),fail=false;const writes=[];
 const {helper}=fixture(t,async(path,body)=>{if(fail)throw new Error('unreadable');if(body)writes.push(body);return path==='model-library'?structuredClone(source):{};});
 await tick();
 source.library.defaultModel='vendor/first';
 await assert.rejects(helper.launchState().prepare(),/shared models changed/);
 assert.equal(writes.length,0);
 await helper.launchState().prepare();
 assert.equal(writes[0].library.defaultModel,'vendor/first');
 fail=true;await helper.reload();
 assert.equal(helper.launchState().valid,false);
 assert.equal(helper.launchState().count,0);
 assert.match(helper.launchState().reason,/unreadable/);
});

test('connection edits invalidate prepared status and disconnect blocks preparation',async t=>{
 const source=saved();const writes=[];
 const {helper,nodes}=fixture(t,async(path,body)=>{if(body)writes.push(body);return path==='model-library'?source:{prepared:true,library:source.library};});
 await tick();
 assert.match(nodes.get('t3-code-prepared').textContent,/Four agents prepared/);
 helper.render({state:{connectionReady:true,baseURL:'http://127.0.0.1:9999/v1',localKey:'different'},language:'es'});
 assert.match(nodes.get('t3-code-prepared').textContent,/Prepara o abre/);
 assert.equal(nodes.get('t3-code-agents').children.length,4);
 helper.render({state:{connectionReady:false},language:'es'});
 await assert.rejects(helper.launchState().prepare(),/Conecta primero/);
 assert.equal(writes.length,0);
});

test('nightly guidance shows the detected version and explains handoff in both languages',async t=>{
 const source=saved(),version='0.0.46-nightly.20261003.2610';
 const {helper,nodes}=fixture(t,async path=>path==='model-library'?source:{prepared:true,library:source.library,version});
 await tick();
 assert.match(nodes.get('t3-code-requirements').textContent,/Detected: 0\.0\.46-nightly\.20261003\.2610/);
 assert.match(nodes.get('t3-code-compatibility').textContent,/change agents between turns/);
 assert.match(nodes.get('t3-code-compatibility').textContent,/attachments are not transferred/);
 assert.doesNotMatch(nodes.get('t3-code-compatibility').textContent,/new chat to switch/);
 helper.render({state:{connectionReady:true},language:'es'});
 assert.match(nodes.get('t3-code-requirements').textContent,/Detectado: 0\.0\.46-nightly\.20261003\.2610/);
 assert.match(nodes.get('t3-code-compatibility').textContent,/cambiar de agente entre turnos/);
 assert.match(nodes.get('t3-code-compatibility').textContent,/no transfiere el razonamiento/);
});

test('preparation and installation guidance translates to Spanish',async t=>{
 const source=saved();let fail=false;
 const {helper,nodes}=fixture(t,async(path,body)=>{if(body&&fail)throw new Error('Quit the T3 Code Kilo window before changing its models or connection. Your regular T3 Code window can stay open.');return path==='model-library'?source:{message:'Install Claude Code CLI before preparing T3 Code. Claude Desktop is a separate application.'};});
 await tick();
 helper.render({state:{connectionReady:true},language:'es'});
 assert.match(nodes.get('t3-code-status').textContent,/Instala Claude Code CLI/);
 fail=true;
 await assert.rejects(helper.launchState().prepare(),/Quit the T3 Code/);
 assert.match(nodes.get('t3-code-status').textContent,/Cierra la ventana Kilo de T3 Code/);
});

import test from 'node:test';
import assert from 'node:assert/strict';
import {createSynaraHelper, synaraPrepared} from '../ui/synara-helper.mjs';

const saved = () => ({revision:2,library:{schemaVersion:1,defaultModel:'vendor/second',models:[{id:'vendor/first',displayName:'Friendly',reasoningEffort:'high'},{id:'vendor/second'}]}});
const tick = () => new Promise(resolve => setImmediate(resolve));
function fixture(t, api) {
 const previous=globalThis.document,nodes=new Map();
 const element=()=>({children:[],textContent:'',classList:{toggle(){}},replaceChildren(){this.children=[];},append(item){this.children.push(item);},addEventListener(){}});
 globalThis.document={getElementById(id){if(!nodes.has(id))nodes.set(id,element());return nodes.get(id);},createElement:element};
 t.after(()=>{globalThis.document=previous;});
 const helper=createSynaraHelper({api,onChange(){}});
 helper.render({state:{connectionReady:true,baseURL:'http://127.0.0.1:8877/v1',localKey:'private-local-secret'},language:'en'});
 return {helper,nodes};
}

test('prepared status requires the current complete shared library',()=>{
 const source=saved(),profile={prepared:true,library:structuredClone(source.library)};
 assert.equal(synaraPrepared(source,profile),true);
 profile.library.models[0].reasoningEffort='low';
 assert.equal(synaraPrepared(source,profile),false);
 assert.equal(synaraPrepared({...source,recoveryRequired:true},profile),false);
});

test('Synara refuses a library exceeding its 32 custom-model limit before writing', async t => {
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
 const {helper,nodes}=fixture(t,async(path,body)=>{calls.push({path,body});return path==='model-library'?source:{prepared:true,library:structuredClone(source.library),profileDir:'/private/profile/secret',version:'9.4.2-beta.8'};});
 await tick();
 assert.equal(helper.launchState().valid,true);
 assert.equal(helper.launchState().ready,false);
 await helper.launchState().prepare();
 const sent=calls.find(call=>call.body);
 assert.equal(sent.path,'clients/synara');
 assert.deepEqual(sent.body,{library:source.library});
 source.library.models[0].displayName='Changed later';
 assert.equal(sent.body.library.models[0].displayName,'Friendly');
 assert.equal(calls.filter(call=>call.path==='model-library'&&call.body).length,0);
 assert.deepEqual(nodes.get('synara-models').children.map(node=>node.textContent),['vendor/second','Friendly · vendor/first']);
 assert.deepEqual(nodes.get('synara-agents').children.map(node=>node.textContent.split(' · ').slice(0,2).join(' · ')),['Codex · Normal','Kilo Proxy · Codex','Claude · Normal','Kilo Proxy · Claude']);
 assert.match(nodes.get('synara-intro').textContent,/Kilo Proxy names and green account indicators/);
 assert.match(nodes.get('synara-privacy').textContent,/Normal agents reference your existing CLI sessions; Kilo Proxy does not copy login credentials/);
 assert.doesNotMatch(nodes.get('synara-privacy').textContent,/without copying login data/);
 assert.match(nodes.get('synara-compatibility').textContent,/new chat to switch between normal and Kilo/);
 assert.match(nodes.get('synara-compatibility').textContent,/exact gateway model ID/);
 assert.match(nodes.get('synara-compatibility').textContent,/family and version match one prepared model/);
 assert.match(nodes.get('synara-compatibility').textContent,/reasoning defaults come from Kilo Models/);
 assert.doesNotMatch(nodes.get('synara-compatibility').textContent,/hides|hidden|summary handoff/);
 assert.match(nodes.get('synara-requirements').textContent,/file-based CLI authentication/);
 assert.match(nodes.get('synara-requirements').textContent,/^Supports Synara Beta\. Requires native Codex CLI and Claude Code\./);
 assert.match(nodes.get('synara-requirements').textContent,/Detected: 9\.4\.2-beta\.8/);
 assert.match(nodes.get('synara-requirements').textContent,/not Desktop app logins/);
 assert.match(nodes.get('synara-prepared').textContent,/Four agents prepared/);
 helper.render({state:{connectionReady:true,baseURL:'http://127.0.0.1:8877/v1',localKey:'private-local-secret'},language:'es'});
 assert.match(nodes.get('synara-requirements').textContent,/^Admite Synara Beta\. Requiere Codex CLI nativo y Claude Code\./);
 assert.match(nodes.get('synara-requirements').textContent,/Detectado: 9\.4\.2-beta\.8/);
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
 const {helper,nodes}=fixture(t,async(path,body)=>{if(body)writes.push(body);return path==='model-library'?source:{prepared:true,library:source.library,message:'Synara prepared with Codex and Claude agents using normal and Kilo Proxy connections.'};});
 await tick();
 assert.match(nodes.get('synara-prepared').textContent,/Four agents prepared/);
 assert.match(nodes.get('synara-status').textContent,/Synara prepared/);
 helper.render({state:{connectionReady:true,baseURL:'http://127.0.0.1:9999/v1',localKey:'different'},language:'es'});
 assert.match(nodes.get('synara-prepared').textContent,/Prepara o abre/);
 assert.equal(nodes.get('synara-status').textContent,'');
 assert.equal(nodes.get('synara-agents').children.length,4);
 assert.match(nodes.get('synara-intro').textContent,/nombres Kilo Proxy y los indicadores verdes/);
 assert.match(nodes.get('synara-privacy').textContent,/Los agentes normales referencian tus sesiones CLI existentes; Kilo Proxy no copia credenciales de inicio de sesión/);
 assert.doesNotMatch(nodes.get('synara-privacy').textContent,/sin copiar datos de inicio de sesión/);
 assert.match(nodes.get('synara-agents').children[1].textContent,/^Kilo Proxy · Codex · /);
 assert.match(nodes.get('synara-agents').children[3].textContent,/^Kilo Proxy · Claude · /);
 helper.render({state:{connectionReady:false},language:'es'});
 await assert.rejects(helper.launchState().prepare(),/Conecta primero/);
 assert.equal(writes.length,0);
});

test('preparation and installation guidance translates to Spanish',async t=>{
 const source=saved();let fail=false;
 const {helper,nodes}=fixture(t,async(path,body)=>{if(body&&fail)throw new Error('Quit the Synara Kilo window before changing its models or connection. Your regular Synara window can stay open.');return path==='model-library'?source:{message:'Install Claude Code CLI before preparing Synara. Claude Desktop is a separate application.'};});
 await tick();
 helper.render({state:{connectionReady:true},language:'es'});
 assert.match(nodes.get('synara-status').textContent,/Instala Claude Code CLI/);
 fail=true;
 await assert.rejects(helper.launchState().prepare(),/Quit the Synara/);
 assert.match(nodes.get('synara-status').textContent,/Cierra la ventana Kilo de Synara/);
});

import test from 'node:test';
import assert from 'node:assert/strict';
import {createOpenMausBotHelper, openMausBotLibraryReady} from '../ui/openmausbot-helper.mjs';

const saved = () => ({revision:2, library:{schemaVersion:1, defaultModel:'vendor/second', models:[{id:'vendor/first',displayName:'Friendly',contextPreset:'low',reasoningEffort:'high'},{id:'vendor/second'}]}});
const tick = () => new Promise(resolve => setImmediate(resolve));
function fixture(t, api) {
 const previous = globalThis.document, nodes = new Map();
 const element = () => ({children:[],textContent:'',classList:{toggle(){}},replaceChildren(){this.children=[];},append(item){this.children.push(item);},addEventListener(){}});
 globalThis.document = {getElementById(id){if(!nodes.has(id))nodes.set(id,element());return nodes.get(id);},createElement:element};
 t.after(()=>{globalThis.document=previous;});
 const helper = createOpenMausBotHelper({api,onChange(){}});
 helper.render({state:{connectionReady:true,baseURL:'http://127.0.0.1:8877/v1',localKey:'synthetic'},language:'en'});
 return {helper,nodes};
}

test('requires a saved nonempty library with its selected default and no recovery warning', () => {
 assert.equal(openMausBotLibraryReady(saved()),true);
 for(const value of [null,{library:{}},{...saved(),recoveryRequired:true},{library:{...saved().library,defaultModel:'missing'}},{library:{...saved().library,models:[]}}])assert.equal(openMausBotLibraryReady(value),false);
});

test('always prepares an immutable shared snapshot without writing a second selection or exposing credentials', async t => {
 const source=saved(),calls=[];
 const {helper,nodes}=fixture(t,async(path,body)=>{calls.push({path,body});return path==='model-library'?source:{prepared:true,configPath:'/isolated/OpenMausBot/config.json'};});
 await tick();
 assert.equal(helper.launchState().valid,true);
 assert.equal(helper.launchState().ready,false);
 await helper.launchState().prepare();
 const sent=calls.find(call=>call.body);
 assert.equal(sent.path,'clients/openmausbot');
 assert.deepEqual(sent.body,{library:source.library});
 source.library.models[0].displayName='Changed later';
 assert.equal(sent.body.library.models[0].displayName,'Friendly');
 assert.equal(calls.filter(call=>call.path==='model-library'&&call.body).length,0);
 assert.match(nodes.get('openmausbot-compatibility').textContent,/does not apply custom names or reasoning levels/);
 assert.deepEqual(nodes.get('openmausbot-models').children.map(node=>node.textContent),['vendor/second','vendor/first']);
 assert.equal(helper.launchState().ready,false);
 assert.doesNotMatch([...nodes.values()].map(node=>node.textContent).join(' '),/synthetic/);
});

test('saved library changes require a fresh launch and a load failure never reuses stale models', async t => {
 let source=saved(),fail=false;const writes=[];
 const {helper}=fixture(t,async(path,body)=>{if(fail)throw new Error('disk unreadable');if(body)writes.push(body);return path==='model-library'?structuredClone(source):{};});
 await tick();
 source.library.defaultModel='vendor/first';
 await assert.rejects(helper.launchState().prepare(),/shared models changed/);
 assert.equal(writes.length,0);
 assert.equal(helper.launchState().count,2);
 await helper.launchState().prepare();
 assert.equal(writes[0].library.defaultModel,'vendor/first');
 fail=true;await helper.reload();
 assert.equal(helper.launchState().valid,false);
 assert.equal(helper.launchState().count,0);
 assert.match(helper.launchState().reason,/disk unreadable/);
});

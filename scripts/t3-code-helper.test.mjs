import test from 'node:test';
import assert from 'node:assert/strict';
import {createT3CodeHelper, t3CodeNativeOnly, t3CodePrepared, t3CodePreferencesValid} from '../ui/t3-code-helper.mjs';

const saved = () => ({revision:2,library:{schemaVersion:1,defaultModel:'vendor/second',models:[{id:'vendor/first',displayName:'Friendly',reasoningEffort:'high'},{id:'vendor/second'}]}});
const emptyLibrary = () => ({schemaVersion:1,defaultModel:'',models:[]});
const preferences = () => ({version:1,codex:true,claude:true,kiloCodex:true,kiloClaude:true,codexModels:'shared',claudeModels:'shared',providers:[],openCode2:false,openCodeConnection:'native',museConnection:'native',museModels:'official'});
const preferencesResponse = () => ({preferences:preferences(),supportedProviders:[],openCode2Supported:false,requiresPreparation:false});
const tick = () => new Promise(resolve => setImmediate(resolve));
function fixture(t, api, options = {}) {
 const previous=globalThis.document,nodes=new Map();
 const element=()=>({children:[],textContent:'',value:'',checked:false,disabled:false,hidden:false,listeners:{},classList:{toggle(){}},replaceChildren(){this.children=[];},append(item){this.children.push(item);},addEventListener(name,callback){this.listeners[name]=callback;},dispatch(name){this.listeners[name]?.({target:this});}});
 globalThis.document={getElementById(id){if(!nodes.has(id))nodes.set(id,element());return nodes.get(id);},createElement:element};
 t.after(()=>{globalThis.document=previous;});
 const helper=createT3CodeHelper({api:async(path,body)=>path==='clients/t3-code/preferences'?(options.preferencesAPI ? options.preferencesAPI(body) : preferencesResponse()):api(path,body),onChange(){}});
 helper.render({state:{connectionReady:true,kiloReady:true,baseURL:'http://127.0.0.1:8877/v1',localKey:'private-local-secret'},language:'en'});
 return {helper,nodes};
}

test('prepared status requires the current complete shared library',()=>{
 const source=saved(),profile={prepared:true,library:structuredClone(source.library)};
 assert.equal(t3CodePrepared(source,profile),true);
 profile.library.models[0].reasoningEffort='low';
 assert.equal(t3CodePrepared(source,profile),false);
 assert.equal(t3CodePrepared({...source,recoveryRequired:true},profile),false);
});

test('native-only readiness requires explicit valid preferences and keeps native model ownership',()=>{
 const native={...preferences(),codex:false,claude:false,kiloCodex:false,kiloClaude:false,providers:['muse']};
 const profile={prepared:true,preferences:structuredClone(native),library:{schemaVersion:1,defaultModel:'',models:[]}};
 assert.equal(t3CodeNativeOnly(native),true);
 assert.equal(t3CodeNativeOnly({...native,version:0}),false);
 assert.equal(t3CodeNativeOnly({...native,kiloClaude:true}),false);
 assert.equal(t3CodePrepared({recoveryRequired:true},profile,native),true);
 assert.equal(t3CodePrepared(null,profile,{...native,kiloCodex:true}),false);
});

test('native Muse prepares without shared Models or gateway and ignores unrelated connection edits',async t=>{
 const native={...preferences(),codex:false,claude:false,kiloCodex:false,kiloClaude:false,providers:['muse']};
 const writes=[]; let libraryReads=0;
 const {helper,nodes}=fixture(t,async(path,body)=>{
  if(path==='model-library'){libraryReads++;throw new Error('unrelated library recovery');}
  if(body)writes.push(structuredClone(body));
  return {prepared:true,preferences:structuredClone(native),library:{schemaVersion:1,defaultModel:'',models:[]},effectiveCodexModels:[],effectiveClaudeModels:[]};
 },{preferencesAPI:async()=>({preferences:structuredClone(native),supportedProviders:[{id:'muse',label:'Muse Code'}]})});
 await tick();
 helper.render({state:{connectionReady:false},language:'en'});
 const state=helper.launchState();
 assert.equal(state.nativeOnly,true);
 assert.equal(state.count,0);
 assert.equal(state.valid,true);
 assert.equal(state.reason,'');
 assert.equal(nodes.get('t3-code-prepare').disabled,false);
 helper.render({state:{connectionReady:false,baseURL:'http://unrelated.invalid',localKey:'unrelated',orgId:'unrelated'},language:'en'});
 assert.equal(helper.launchState().fingerprint,state.fingerprint);
 await helper.launchState().prepare();
 assert.deepEqual(writes,[{preferences:native}]);
 assert.equal(libraryReads,1);
 assert.match(nodes.get('t3-code-prepared').textContent,/saved T3 accounts and models are prepared/);
});

test('native Muse detects external account changes before preparing or after a concurrent change',async t=>{
 let current={...preferences(),codex:false,claude:false,kiloCodex:false,kiloClaude:false,providers:['muse']};
 const native=structuredClone(current),writes=[];
 let changeDuringPrepare=false;
 const {helper}=fixture(t,async(path,body)=>{
  if(path==='model-library')return {recoveryRequired:true,library:{schemaVersion:1,defaultModel:'',models:[]}};
  if(body){writes.push(structuredClone(body));if(changeDuringPrepare)current={...current,kiloCodex:true};}
  return {prepared:true,preferences:structuredClone(native),library:{schemaVersion:1,defaultModel:'',models:[]}};
 },{preferencesAPI:async()=>({preferences:structuredClone(current),supportedProviders:[{id:'muse',label:'Muse Code'}]})});
 await tick();
 current={...current,kiloClaude:true};
 await assert.rejects(helper.launchState().prepare(),/settings changed/);
 assert.equal(writes.length,0);
 assert.equal(helper.launchState().nativeOnly,false);
 assert.equal(helper.launchState().valid,false);
 current=structuredClone(native);await helper.reload();changeDuringPrepare=true;
 await assert.rejects(helper.launchState().prepare(),/settings or connection changed/);
 assert.deepEqual(writes,[{preferences:native}]);
 assert.equal(helper.launchState().nativeOnly,false);
 assert.equal(helper.launchState().valid,false);
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

test('enabled accounts use an immutable common-library snapshot without exposing credentials or profile paths',async t=>{
 const source=saved(),calls=[];
 const {helper,nodes}=fixture(t,async(path,body)=>{calls.push({path,body});return path==='model-library'?source:{prepared:true,preferences:preferences(),library:structuredClone(source.library),profileDir:'/private/profile/secret',version:'0.0.45'};});
 await tick();
 assert.equal(helper.launchState().valid,true);
 assert.equal(helper.launchState().ready,false);
 await helper.launchState().prepare();
 const sent=calls.find(call=>call.body);
 assert.equal(sent.path,'clients/t3-code');
 assert.deepEqual(sent.body,{library:source.library,preferences:preferences()});
 source.library.models[0].displayName='Changed later';
 assert.equal(sent.body.library.models[0].displayName,'Friendly');
 assert.equal(calls.filter(call=>call.path==='model-library'&&call.body).length,0);
 assert.deepEqual(nodes.get('t3-code-models').children.map(node=>node.textContent),[]);
 assert.deepEqual(nodes.get('t3-code-agents').children.map(node=>node.textContent.split(' · ').slice(0,2).join(' · ')),['Codex · Existing Codex CLI login','Kilo Proxy · Codex','Claude · Existing Claude Code login','Kilo Proxy · Claude']);
 assert.match(nodes.get('t3-code-intro').textContent,/Green KP badges.*provider rail and composer/);
 assert.match(nodes.get('t3-code-compatibility').textContent,/new chat to switch accounts/);
 assert.match(nodes.get('t3-code-requirements').textContent,/not Desktop app logins/);
 assert.match(nodes.get('t3-code-prepared').textContent,/saved T3 accounts and models are prepared/);
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
 const {helper,nodes}=fixture(t,async(path,body)=>{if(body)writes.push(body);return path==='model-library'?source:{prepared:true,preferences:preferences(),library:source.library};});
 await tick();
 assert.match(nodes.get('t3-code-prepared').textContent,/saved T3 accounts and models are prepared/);
 helper.render({state:{connectionReady:true,baseURL:'http://127.0.0.1:9999/v1',localKey:'different'},language:'es'});
 assert.match(nodes.get('t3-code-prepared').textContent,/Prepara o abre/);
 assert.equal(nodes.get('t3-code-agents').children.length,4);
 assert.match(nodes.get('t3-code-intro').textContent,/marcas KP verdes.*selector y el compositor/);
 assert.match(nodes.get('t3-code-agents').children[1].textContent,/^Kilo Proxy · Codex · /);
 assert.match(nodes.get('t3-code-agents').children[3].textContent,/^Kilo Proxy · Claude · /);
 helper.render({state:{connectionReady:false},language:'es'});
 await assert.rejects(helper.launchState().prepare(),/Conecta primero/);
 assert.equal(writes.length,0);
});

test('nightly guidance shows the detected version and explains handoff in both languages',async t=>{
 const source=saved(),version='9.0.0-beta.2';
 const {helper,nodes}=fixture(t,async path=>path==='model-library'?source:{prepared:true,preferences:preferences(),library:source.library,version,protocolV2:true});
 await tick();
 assert.match(nodes.get('t3-code-requirements').textContent,/Detected: 9\.0\.0-beta\.2/);
 assert.match(nodes.get('t3-code-compatibility').textContent,/change agents between turns/);
 assert.match(nodes.get('t3-code-compatibility').textContent,/attachments are not transferred/);
 assert.doesNotMatch(nodes.get('t3-code-compatibility').textContent,/new chat to switch/);
 helper.render({state:{connectionReady:true},language:'es'});
 assert.match(nodes.get('t3-code-requirements').textContent,/Detectado: 9\.0\.0-beta\.2/);
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

test('settings save a concrete account policy once, disable editing while pending, and expose only supported providers', async t => {
 let current=preferences(), release, posted=[];
 const support=[{id:'cursor',label:'Cursor'},{id:'opencode',label:'OpenCode'}];
 const {helper,nodes}=fixture(t, async path => path==='model-library'?saved():{preferences:structuredClone(current),prepared:true,library:saved().library,effectiveCodexModels:[{id:'openai/gpt-fixture',name:'Fixture GPT'}],effectiveClaudeModels:[{id:'anthropic/claude-opus-5.5',name:'Claude Opus 5.5'}]}, {preferencesAPI:async body=>{
  if(body){posted.push(structuredClone(body));await new Promise(resolve=>{release=resolve;});current=structuredClone(body);}
  return {preferences:structuredClone(current),requiresPreparation:!!body,supportedProviders:support,openCode2Supported:true};
 }});
 await tick();
 assert.deepEqual(nodes.get('t3-code-provider-options').children.map(label=>label.children[1].textContent),['Cursor','OpenCode']);
 assert.equal(nodes.get('t3-code-opencode-version-field').hidden,false);
 assert.equal(nodes.get('t3-code-opencode-version').disabled,true);
 assert.deepEqual(nodes.get('t3-code-models').children.map(row=>row.textContent),['Kilo Proxy · Codex · Fixture GPT · openai/gpt-fixture','Kilo Proxy · Claude · Claude Opus 5.5 · anthropic/claude-opus-5.5']);
 const codex=nodes.get('t3-code-account-codex');codex.checked=false;codex.dispatch('change');
 assert.equal(posted.length,1);assert.equal(posted[0].codex,false);assert.equal(posted[0].claude,true);
 assert.equal(codex.checked,false);assert.equal(helper.preferences().codex,true);
 assert.equal(nodes.get('t3-code-account-claude').disabled,true);
 assert.equal(helper.launchState().valid,false);
 await helper.savePreferences({...current,claude:false});assert.equal(posted.length,1);
 release();await tick();await tick();
 assert.equal(helper.preferences().codex,false);
 assert.equal(nodes.get('t3-code-account-codex').checked,false);
 assert.equal(nodes.get('t3-code-account-claude').disabled,false);
 assert.doesNotMatch(nodes.get('t3-code-agents').children.map(row=>row.textContent).join(' '),/Normal/);
 assert.equal(nodes.get('t3-code-agents').children.length,3);
});

test('a failed settings save retains the confirmed policy and retries exactly the requested policy', async t=>{
 let current=preferences(),fail=true,writes=[];
 const {helper,nodes}=fixture(t,async path=>path==='model-library'?saved():{preferences:current,library:saved().library,prepared:true},{preferencesAPI:async body=>{
  if(body){writes.push(structuredClone(body));if(fail)throw new Error('synthetic disk unavailable');current=structuredClone(body);}
  return {preferences:current,supportedProviders:[],openCode2Supported:false,requiresPreparation:true};
 }});
 await tick();
 await helper.savePreferences({...current,claude:false});
 assert.equal(helper.preferences().claude,true);
 assert.equal(nodes.get('t3-code-settings-retry').hidden,false);
 assert.match(nodes.get('t3-code-settings-status').textContent,/disk unavailable/);
 assert.equal(helper.launchState().valid,false);
 fail=false;nodes.get('t3-code-settings-retry').dispatch('click');await tick();await tick();
 assert.equal(helper.preferences().claude,false);
 assert.deepEqual(writes[1],writes[0]);
 assert.equal(nodes.get('t3-code-settings-retry').hidden,true);
});

test('settings edits elsewhere invalidate a preparation snapshot before writing', async t=>{
 let current=preferences(),writes=[];
 const {helper}=fixture(t,async(path,body)=>{if(body)writes.push(body);return path==='model-library'?saved():{prepared:true,preferences:current,library:saved().library};},{preferencesAPI:async()=>({preferences:structuredClone(current),supportedProviders:[],requiresPreparation:false})});
 await tick();current.claude=false;
 await assert.rejects(helper.launchState().prepare(),/settings changed/);
 assert.equal(writes.length,0);
 assert.equal(helper.preferences().claude,false);
});

test('OpenCode selects one version and disabling its provider clears OpenCode 2 without changing other providers',async t=>{
 let current={...preferences(),providers:['cursor','opencode'],openCode2:true},writes=[];
 const {helper,nodes}=fixture(t,async path=>path==='model-library'?saved():{prepared:false,preferences:current,library:saved().library},{preferencesAPI:async body=>{if(body){writes.push(structuredClone(body));current=structuredClone(body);}return {preferences:current,supportedProviders:[{id:'opencode',label:'OpenCode'}],openCode2Supported:true};}});
 await tick();assert.equal(nodes.get('t3-code-opencode-version').value,'2');assert.equal(nodes.get('t3-code-opencode-version').disabled,false);
 const checkbox=nodes.get('t3-code-provider-options').children[0].children[0];checkbox.checked=false;checkbox.dispatch('change');await tick();await tick();
 assert.deepEqual(writes[0].providers,['cursor']);assert.equal(writes[0].openCode2,false);
 assert.equal(helper.preferences().openCode2,false);
 assert.equal(nodes.get('t3-code-opencode-version').disabled,true);
 helper.render({state:{connectionReady:true},language:'es'});
 assert.match(nodes.get('t3-code-settings-help').textContent,/se conservan tus chats/);
 assert.match(nodes.get('t3-code-codex-official').textContent,/OpenAI y ChatGPT/);
 assert.match(nodes.get('t3-code-settings-status').textContent,/Ajustes guardados/);
});

test('official mode does not reject an unrelated shared library over the per-account custom limit',async t=>{
 const source=saved();source.library.models=Array.from({length:40},(_,i)=>({id:'vendor/model-'+i}));source.library.defaultModel='vendor/model-0';
 const prefs={...preferences(),codexModels:'official',claudeModels:'official'};
 const {helper}=fixture(t,async path=>path==='model-library'?source:{prepared:false,preferences:prefs,library:source.library},{preferencesAPI:async()=>({preferences:prefs,supportedProviders:[],requiresPreparation:true})});
 await tick();assert.equal(helper.launchState().valid,true);
});

test('saved choices absent from a newer build stay visible until disabled, without enabling unknown providers or OpenCode 2',async t=>{
 let current={...preferences(),providers:['cursor','opencode'],openCode2:true},writes=[];
 const {helper,nodes}=fixture(t,async path=>path==='model-library'?saved():{prepared:false,preferences:current,library:saved().library},{preferencesAPI:async body=>{if(body){writes.push(structuredClone(body));current=structuredClone(body);}return {preferences:current,supportedProviders:[{id:'opencode',label:'OpenCode'},{id:'unknown',label:'Unknown provider'}],openCode2Supported:false};}});
 await tick();
 assert.deepEqual(nodes.get('t3-code-provider-options').children.map(label=>label.children[1].textContent),['OpenCode','Cursor · unavailable in this build']);
 assert.equal(nodes.get('t3-code-opencode-version').value,'2');assert.equal(nodes.get('t3-code-opencode-version-2').disabled,true);
 assert.match(nodes.get('t3-code-opencode-help').textContent,/cannot use the saved OpenCode 2 choice/);
 const cursor=nodes.get('t3-code-provider-options').children[1].children[0];assert.equal(cursor.checked,true);assert.equal(cursor.disabled,false);cursor.checked=false;cursor.dispatch('change');await tick();await tick();
 assert.deepEqual(writes[0].providers,['opencode']);assert.equal(writes[0].openCode2,true);
 nodes.get('t3-code-opencode-version').value='1';nodes.get('t3-code-opencode-version').dispatch('change');await tick();await tick();
 assert.equal(writes[1].openCode2,false);assert.equal(nodes.get('t3-code-opencode-version-field').hidden,true);
 nodes.get('t3-code-opencode-version').value='2';nodes.get('t3-code-opencode-version').dispatch('change');await tick();assert.equal(writes.length,2);
 assert.deepEqual(nodes.get('t3-code-provider-options').children.map(label=>label.children[1].textContent),['OpenCode']);
 helper.render({state:{connectionReady:true},language:'es'});assert.doesNotMatch(nodes.get('t3-code-agents').children.map(node=>node.textContent).join(' '),/Cursor|Unknown/);
});


test('OpenCode connection is optional in old responses and only enabled Kilo OpenCode requires the gateway',()=>{
 const own={...preferences(),kiloCodex:false,kiloClaude:false,providers:['opencode']};
 delete own.openCodeConnection;
 assert.equal(t3CodePreferencesValid(own),true);
 assert.equal(t3CodeNativeOnly(own),true);
 assert.equal(t3CodeNativeOnly({...own,openCodeConnection:'kilo'}),false);
 assert.equal(t3CodeNativeOnly({...own,openCodeConnection:'kilo',providers:[]}),true);
 assert.equal(t3CodePreferencesValid({...own,openCodeConnection:''}),true);
 for(const connection of ['other',null,false,12]) assert.equal(t3CodePreferencesValid({...own,openCodeConnection:connection}),false);
 assert.equal(t3CodePrepared(null,{prepared:true,preferences:own},{...own,openCodeConnection:'native'}),true);
});

test('OpenCode connection saves an explicit immutable choice and displays only its matching Kilo summary',async t=>{
 let current={...preferences(),providers:['opencode'],openCode2:true}, release;
 const posts=[];
 const summary=[{id:'vendor/first',name:'First shared'},{id:'vendor/second',name:'Second shared'}];
 const {helper,nodes}=fixture(t,async path=>path==='model-library'?saved():{preferences:structuredClone(current),prepared:false,library:saved().library,effectiveOpenCodeModels:summary},{preferencesAPI:async body=>{
  if(body){posts.push(structuredClone(body));await new Promise(resolve=>{release=resolve;});current=structuredClone(body);}
  return {preferences:structuredClone(current),supportedProviders:[{id:'opencode',label:'OpenCode'}],openCode2Supported:true,openCodeKiloSupported:true};
 }});
 await tick();
 assert.equal(nodes.get('t3-code-opencode-connection').value,'native');
 assert.match(nodes.get('t3-code-opencode-connection-help').textContent,/login and catalog/);
 assert.match(nodes.get('t3-code-opencode-help').textContent,/separate profile.*own login/);
 const before=helper.launchState().fingerprint;
 nodes.get('t3-code-opencode-connection').value='kilo';nodes.get('t3-code-opencode-connection').dispatch('change');
 assert.equal(posts.length,1);assert.equal(posts[0].openCodeConnection,'kilo');assert.equal(posts[0].openCode2,true);
 assert.equal(helper.preferences().openCodeConnection,'native');assert.equal(nodes.get('t3-code-opencode-connection').disabled,true);
 assert.equal(helper.launchState().valid,false);
 release();await tick();await tick();
 assert.equal(helper.preferences().openCodeConnection,'kilo');assert.notEqual(helper.launchState().fingerprint,before);
 assert.deepEqual(nodes.get('t3-code-models').children.map(row=>row.textContent),['Kilo Proxy · OpenCode · First shared · vendor/first','Kilo Proxy · OpenCode · Second shared · vendor/second']);
 assert.match(nodes.get('t3-code-agents').children.at(-1).textContent,/Kilo Proxy · OpenCode 2 · Shared models through the proxy/);
 assert.match(nodes.get('t3-code-summary').textContent,/OpenCode: 2/);
 assert.equal(helper.launchState().count,2);
 helper.render({state:{connectionReady:true},language:'es'});
 assert.equal(nodes.get('t3-code-opencode-connection-label').textContent,'Conexión de OpenCode');
 assert.match(nodes.get('t3-code-opencode-connection-help').textContent,/modelos guardados en Modelos/);
 assert.doesNotMatch(nodes.get('t3-code-opencode-help').textContent,/inicio de sesión/);
 nodes.get('t3-code-opencode-connection').value='native';nodes.get('t3-code-opencode-connection').dispatch('change');release();await tick();await tick();
 assert.equal(helper.preferences().openCodeConnection,'native');assert.deepEqual(nodes.get('t3-code-models').children,[]);
 assert.match(nodes.get('t3-code-opencode-help').textContent,/perfil separado.*inicio de sesión/);
 assert.doesNotMatch(nodes.get('t3-code-agents').children.at(-1).textContent,/Kilo Proxy/);
});

test('OpenCode Kilo fails closed without the multiple-instance capability and permits explicit recovery to an own account',async t=>{
 let current={...preferences(),kiloCodex:false,kiloClaude:false,providers:['opencode'],openCodeConnection:'kilo'};const writes=[];
 const {helper,nodes}=fixture(t,async path=>path==='model-library'?saved():{preferences:current,prepared:false,library:saved().library},{preferencesAPI:async body=>{if(body){writes.push(body);current=structuredClone(body);}return {preferences:current,supportedProviders:[{id:'opencode',label:'OpenCode'}]};}});
 await tick();assert.equal(helper.launchState().valid,false);assert.match(helper.launchState().reason,/cannot use the Kilo OpenCode connection/);
 assert.equal(nodes.get('t3-code-opencode-connection-kilo').disabled,true);assert.equal(nodes.get('t3-code-opencode-connection').value,'kilo');
 nodes.get('t3-code-opencode-connection').value='native';nodes.get('t3-code-opencode-connection').dispatch('change');await tick();await tick();
 assert.equal(writes.length,1);assert.equal(helper.launchState().nativeOnly,true);
 nodes.get('t3-code-opencode-connection').value='kilo';nodes.get('t3-code-opencode-connection').dispatch('change');await tick();assert.equal(writes.length,1);
 assert.equal(nodes.get('t3-code-opencode-connection').value,'native');
});

test('OpenCode Kilo alone needs a saved library and connection without imposing the custom Codex or Claude model limit',async t=>{
 const source=saved();source.library.models=Array.from({length:40},(_,i)=>({id:'vendor/model-'+i}));source.library.defaultModel='vendor/model-0';
 const current={...preferences(),kiloCodex:false,kiloClaude:false,providers:['opencode'],openCodeConnection:'kilo'};
 const {helper}=fixture(t,async path=>path==='model-library'?source:{preferences:current,prepared:false,library:source.library},{preferencesAPI:async()=>({preferences:current,supportedProviders:[{id:'opencode',label:'OpenCode'}],openCodeKiloSupported:true})});
 await tick();assert.equal(helper.launchState().nativeOnly,false);assert.equal(helper.launchState().valid,true);
 helper.render({state:{connectionReady:false},language:'en'});assert.equal(helper.launchState().valid,false);await assert.rejects(helper.launchState().prepare(),/Connect your selected provider/);
});

test('OpenCode Kilo rejects an old response that does not confirm the requested connection, then retries the exact choice',async t=>{
 let current={...preferences(),providers:['opencode']},omit=true;const writes=[];
 const {helper,nodes}=fixture(t,async path=>path==='model-library'?saved():{preferences:current,prepared:false,library:saved().library},{preferencesAPI:async body=>{
  if(body){writes.push(structuredClone(body));current=structuredClone(body);}
  const returned=structuredClone(current);if(omit)delete returned.openCodeConnection;
  return {preferences:returned,supportedProviders:[{id:'opencode',label:'OpenCode'}],openCodeKiloSupported:true};
 }});
 await tick();assert.equal(helper.preferences().openCodeConnection,'native');
 await helper.savePreferences({...current,openCodeConnection:'kilo'});
 assert.equal(helper.preferences().openCodeConnection,'native');assert.equal(helper.launchState().valid,false);
 assert.match(nodes.get('t3-code-settings-status').textContent,/did not confirm/);
 omit=false;nodes.get('t3-code-settings-retry').dispatch('click');await tick();await tick();
 assert.deepEqual(writes[1],writes[0]);assert.equal(helper.preferences().openCodeConnection,'kilo');
});

test('an externally changed OpenCode connection invalidates the profile snapshot before preparation',async t=>{
 let current={...preferences(),providers:['opencode']};const writes=[];
 const {helper}=fixture(t,async(path,body)=>{if(body)writes.push(body);return path==='model-library'?saved():{preferences:current,prepared:false,library:saved().library};},{preferencesAPI:async()=>({preferences:structuredClone(current),supportedProviders:[{id:'opencode',label:'OpenCode'}],openCodeKiloSupported:true})});
 await tick();current={...current,openCodeConnection:'kilo'};
 await assert.rejects(helper.launchState().prepare(),/settings changed/);assert.deepEqual(writes,[]);
 assert.equal(helper.preferences().openCodeConnection,'kilo');
});


test('legacy Muse remains native but explicit Kilo Muse alone requires Models and gateway',async t=>{
 const old={...preferences(),kiloCodex:false,kiloClaude:false,providers:['muse']};delete old.museConnection;
 delete old.museModels;
 assert.equal(t3CodeNativeOnly(old),true);
 assert.equal(t3CodeNativeOnly({...old,museConnection:'kilo'}),false);
 for(const invalid of [null,'other'])assert.equal(t3CodePreferencesValid({...old,museConnection:invalid}),false);
 let source=saved();const kilo={...old,museConnection:'kilo'};
 const {helper,nodes}=fixture(t,async path=>path==='model-library'?source:{preferences:kilo,prepared:false,effectiveMuseModels:[{id:'vendor/exact',name:'Exact Muse model'}]}, {preferencesAPI:async()=>({preferences:kilo,supportedProviders:[{id:'muse',label:'Muse Code'}],museKiloSupported:true})});
 await tick();assert.equal(helper.launchState().nativeOnly,false);assert.equal(helper.launchState().valid,true);assert.match(nodes.get('t3-code-summary').textContent,/Muse Code: 1/);
 helper.render({state:{connectionReady:false},language:'es'});assert.equal(helper.launchState().valid,false);assert.match(helper.launchState().reason,/Conecta primero/);
 source={library:{schemaVersion:1,defaultModel:'',models:[]}};helper.render({state:{connectionReady:true},language:'en'});await helper.reload();assert.equal(helper.launchState().valid,false);
});

test('newly enabling Muse selects Kilo explicitly and missing capability permits recovery to Meta',async t=>{
 let prefs={...preferences(),kiloCodex:false,kiloClaude:false};const writes=[];
 const {helper,nodes}=fixture(t,async path=>path==='model-library'?saved():{preferences:prefs,prepared:false}, {preferencesAPI:async body=>{if(body){writes.push(structuredClone(body));prefs=structuredClone(body);}return {preferences:prefs,supportedProviders:[{id:'muse',label:'Muse Code'}]};}});
 await tick();const muse=nodes.get('t3-code-provider-options').children.find(row=>row.children[0]?.id==='t3-code-provider-muse').children[0];muse.checked=true;muse.dispatch('change');await tick();
 assert.equal(writes[0].museConnection,'kilo');assert.equal(writes[0].museModels,'official');assert.equal(helper.launchState().valid,false);assert.match(helper.launchState().reason,/cannot use the Kilo Muse connection/);assert.equal(nodes.get('t3-code-muse-connection-kilo').disabled,true);
 nodes.get('t3-code-muse-connection').value='native';nodes.get('t3-code-muse-connection').dispatch('change');await tick();assert.equal(helper.preferences().museConnection,'native');assert.equal(helper.launchState().nativeOnly,true);assert.ok(nodes.get('t3-code-agents').children.some(row=>/Meta login/.test(row.textContent)));
});

test('Muse Kilo saves require exact confirmation and stale mode changes block preparation',async t=>{
 let prefs={...preferences(),kiloCodex:false,kiloClaude:false,providers:['muse']},omit=true;const writes=[];
 const {helper,nodes}=fixture(t,async(path,body)=>{if(body)writes.push(body);return path==='model-library'?saved():{preferences:prefs,prepared:true,library:saved().library,effectiveMuseModels:[]};},{preferencesAPI:async body=>{if(body)prefs=structuredClone(body);const response=structuredClone(prefs);if(body&&omit)delete response.museConnection;return {preferences:response,supportedProviders:[{id:'muse',label:'Muse Code'}],museKiloSupported:true};}});
 await tick();await helper.savePreferences({...prefs,museConnection:'kilo'});assert.equal(nodes.get('t3-code-settings-retry').hidden,false);assert.equal(helper.launchState().valid,false);assert.equal(writes.length,0);
 omit=false;nodes.get('t3-code-settings-retry').dispatch('click');await tick();assert.equal(helper.preferences().museConnection,'kilo');prefs={...prefs,museConnection:'native'};await assert.rejects(helper.launchState().prepare(),/settings changed/);assert.equal(writes.length,0);
});


test('Muse policy normalizes legacy availability and confirms explicit Meta/shared saves', async t=>{
 let current={...preferences(),codex:false,claude:false,kiloCodex:false,kiloClaude:false,providers:['muse'],museConnection:'kilo'};
 delete current.museModels;
 const writes=[];
 const {helper,nodes}=fixture(t,async path=>path==='model-library'?saved():{prepared:false,preferences:structuredClone(current),library:saved().library,effectiveMuseModels:[]},{preferencesAPI:async body=>{
  if(body){writes.push(structuredClone(body));current=structuredClone(body);}
  return {preferences:structuredClone(current),supportedProviders:[{id:'muse',label:'Muse Code'}],museKiloSupported:true};
 }});
 await tick();
 assert.equal(helper.preferences().museModels,'shared');
 assert.equal(nodes.get('t3-code-muse-models').value,'shared');
 assert.equal(nodes.get('t3-code-muse-models-field').hidden,false);
 nodes.get('t3-code-muse-models').value='official';nodes.get('t3-code-muse-models').dispatch('change');
 await tick();
 assert.equal(writes.at(-1).museModels,'official');
 assert.equal(writes.at(-1).museConnection,'kilo');
 assert.equal(helper.preferences().museModels,'official');
 assert.match(nodes.get('t3-code-agents').children.at(-1).textContent,/Meta models through the proxy/);
 helper.render({state:{connectionReady:true},language:'es'});
 assert.equal(nodes.get('t3-code-muse-official').textContent,'Modelos de Meta');
 assert.equal(nodes.get('t3-code-muse-shared').textContent,'Todos los modelos compartidos');
 nodes.get('t3-code-muse-models').value='shared';nodes.get('t3-code-muse-models').dispatch('change');
 await tick();assert.equal(helper.preferences().museModels,'shared');
 nodes.get('t3-code-muse-connection').value='native';nodes.get('t3-code-muse-connection').dispatch('change');
 await tick();
 assert.equal(helper.preferences().museModels,'shared');
 assert.equal(nodes.get('t3-code-muse-models-field').hidden,true);
});

test('Muse policy changes invalidate preparation and omitted response cannot confirm Meta', async t=>{
 let current={...preferences(),providers:['muse'],museConnection:'kilo',museModels:'shared'};
 let omit=false;
 const {helper,nodes}=fixture(t,async path=>path==='model-library'?saved():{prepared:true,preferences:structuredClone(current),library:saved().library},{preferencesAPI:async body=>{
  if(body)current=structuredClone(body);
  const response=structuredClone(current);if(omit)delete response.museModels;
  return {preferences:response,supportedProviders:[{id:'muse',label:'Muse Code'}],museKiloSupported:true};
 }});
 await tick();omit=true;
 await helper.savePreferences({...helper.preferences(),museModels:'official'});
 assert.equal(helper.preferences().museModels,'shared');
 assert.match(nodes.get('t3-code-settings-status').textContent,/did not confirm/);
 assert.equal(helper.launchState().valid,false);
 assert.equal(t3CodePreferencesValid({...current,museModels:null}),false);
 assert.equal(t3CodePreferencesValid({...current,museModels:''}),false);
 assert.equal(t3CodePreferencesValid({...current,museModels:'all'}),false);
 omit=false;nodes.get('t3-code-settings-retry').dispatch('click');await tick();
 assert.equal(helper.preferences().museModels,'official');
});

test('Claude and Meta catalogs can prepare from a cold cache and empty library', async t => {
 const current = {...preferences(),kiloCodex:false,claudeModels:'official',providers:['muse'],museConnection:'kilo',museModels:'official'};
 const effectiveClaudeModels = [{id:'anthropic/claude-haiku-5.5',name:'Claude Haiku 5.5'}];
 const effectiveMuseModels = [{id:'meta/muse-fixture',name:'Muse Fixture'}];
 const writes = [];
 const {helper,nodes} = fixture(t,async (path,body) => {
  if (path === 'model-library') return {library:emptyLibrary()};
  if (!body) return {prepared:false,preferences:current,library:emptyLibrary()};
  writes.push(structuredClone(body));
  return {prepared:true,preferences:current,library:emptyLibrary(),effectiveClaudeModels,effectiveMuseModels};
 },{preferencesAPI:async () => ({preferences:structuredClone(current),supportedProviders:[{id:'muse',label:'Muse Code'}],museKiloSupported:true})});
 await tick();
 assert.equal(helper.launchState().count,0);
 assert.equal(helper.launchState().canPrepareWithoutModelSummary,true);
 assert.equal(helper.launchState().valid,true);
 assert.equal(nodes.get('t3-code-prepare').disabled,false);
 assert.equal(nodes.get('t3-code-claude-official').textContent,'Claude models');
 assert.match(nodes.get('t3-code-muse-models-help').textContent,/from your Kilo catalog/);
 const fingerprint = helper.launchState().fingerprint;
 await helper.launchState().prepare();
 assert.deepEqual(writes,[{preferences:current}]);
 assert.equal(helper.launchState().fingerprint,fingerprint);
 assert.equal(helper.launchState().count,2);
 assert.match(nodes.get('t3-code-prepared').textContent,/saved T3 accounts and models are prepared/);
 helper.render({state:{connectionReady:true,kiloReady:false},language:'es'});
 assert.equal(nodes.get('t3-code-claude-official').textContent,'Modelos de Claude');
 assert.equal(nodes.get('t3-code-muse-official').textContent,'Modelos de Meta');
 assert.equal(helper.launchState().count,0);
 assert.equal(helper.launchState().valid,false);
 assert.match(helper.launchState().reason,/Conecta Kilo/);
 await assert.rejects(helper.launchState().prepare(),/Conecta Kilo/);
 assert.equal(writes.length,1);
});

test('an optional library is rechecked but not sent for Claude models', async t => {
 const current = {...preferences(),kiloCodex:false,claudeModels:'official'};
 let source = saved();
 let reads = 0;
 const writes = [];
 const {helper,nodes} = fixture(t,async (path,body) => {
  if (path === 'model-library') {
   reads++;
   if (source === null) throw new Error('unreadable library');
   return structuredClone(source);
  }
  if (body) writes.push(structuredClone(body));
  const library = source && !source.recoveryRequired ? source.library : emptyLibrary();
  return {prepared:!!body,preferences:current,library:structuredClone(library)};
 },{preferencesAPI:async () => ({preferences:structuredClone(current)})});
 await tick();
 source.library.models[0].reasoningEffort = 'low';
 await assert.rejects(helper.launchState().prepare(),/shared models changed/);
 assert.equal(writes.length,0);
 await helper.launchState().prepare();
 assert.deepEqual(writes,[{preferences:current}]);
 assert.match(nodes.get('t3-code-prepared').textContent,/saved T3 accounts and models are prepared/);
 source = {...source,recoveryRequired:true,warning:'last good backup'};
 await helper.reload();
 assert.equal(helper.launchState().valid,true);
 await helper.launchState().prepare();
 source = null;
 await helper.reload();
 assert.equal(helper.launchState().valid,true);
 await helper.launchState().prepare();
 assert.equal(reads,10);
 assert.deepEqual(writes,[{preferences:current},{preferences:current},{preferences:current}]);
 assert.match(nodes.get('t3-code-prepared').textContent,/saved T3 accounts and models are prepared/);
});

test('an official Claude catalog still requires Models when another Kilo account uses Shared', async t => {
 const current = {...preferences(),kiloCodex:false,claudeModels:'official',providers:['muse'],museConnection:'kilo',museModels:'shared'};
 const writes = [];
 const {helper} = fixture(t,async (path,body) => {
  if (path === 'model-library') return {library:emptyLibrary()};
  if (body) writes.push(body);
  return {prepared:false,preferences:current,library:emptyLibrary()};
 },{preferencesAPI:async () => ({preferences:current,supportedProviders:[{id:'muse',label:'Muse Code'}],museKiloSupported:true})});
 await tick();
 assert.equal(helper.launchState().valid,false);
 assert.match(helper.launchState().reason,/Add and save models/);
 await assert.rejects(helper.launchState().prepare(),/Add and save models/);
 assert.deepEqual(writes,[]);
});

test('official catalogs invalidate prepared summaries when the Kilo account changes', async t => {
 const current = {...preferences(),kiloCodex:false,claudeModels:'official'};
 const initialState = {connectionReady:true,kiloReady:true,usageAccountScope:'account-a',catalogRevision:7};
 const nextState = {...initialState,usageAccountScope:'account-b',catalogRevision:8};
 let switchBeforeWrite = false;
 let switchAfterWrite = false;
 const writes = [];
 const {helper,nodes} = fixture(t,async (path,body) => {
  if (path === 'model-library') {
   if (switchBeforeWrite) helper.render({state:nextState,language:'en'});
   return {library:emptyLibrary()};
  }
  if (body) {
   writes.push(body);
   if (switchAfterWrite) helper.render({state:nextState,language:'en'});
  }
  return {prepared:true,preferences:current,library:emptyLibrary(),effectiveClaudeModels:[{id:'anthropic/claude-opus-5.5'}]};
 },{preferencesAPI:async () => ({preferences:current})});
 await tick();
 helper.render({state:initialState,language:'en'});
 await helper.reload();
 assert.equal(helper.launchState().count,1);
 const original = helper.launchState().fingerprint;
 switchBeforeWrite = true;
 await assert.rejects(helper.launchState().prepare(),/connection changed/);
 assert.equal(writes.length,0);
 assert.notEqual(helper.launchState().fingerprint,original);
 assert.equal(helper.launchState().count,0);
 assert.match(nodes.get('t3-code-prepared').textContent,/Prepare or open/);
 switchBeforeWrite = false;
 switchAfterWrite = true;
 helper.render({state:initialState,language:'en'});
 await assert.rejects(helper.launchState().prepare(),/settings or connection changed while preparing/);
 assert.equal(writes.length,1);
 assert.equal(helper.launchState().count,0);
});

test('an official preparation verifies the canonical library returned by the server', async t => {
 const current = {...preferences(),kiloCodex:false,claudeModels:'official'};
 const source = {library:{...emptyLibrary(),startingPack:'recommended'}};
 let confirm = false;
 const {helper,nodes} = fixture(t,async (path,body) => {
  if (path === 'model-library') return structuredClone(source);
  return {prepared:!!body,preferences:current,library:confirm ? structuredClone(source.library) : emptyLibrary()};
 },{preferencesAPI:async () => ({preferences:current})});
 await tick();
 await assert.rejects(helper.launchState().prepare(),/did not confirm the prepared catalog/);
 assert.match(nodes.get('t3-code-prepared').textContent,/Prepare or open/);
 confirm = true;
 await helper.launchState().prepare();
 assert.match(nodes.get('t3-code-prepared').textContent,/saved T3 accounts and models are prepared/);
});

import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,writeFileSync,readFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawnSync} from 'node:child_process';
import {editorPayload,editorLaunch} from '../ui/editor-helper.mjs';
import {clientConfig} from '../ui/client-config.mjs';
test('OpenCode and Zed export multiple exact models, names and limits without giving Zed a key',()=>{
 const s=editorPayload([{id:'vendor/a',name:'Short A',contextWindow:64000,maxOutputTokens:4096},{id:'vendor/b',name:'Short B'}],'vendor/b');
 for(const client of ['opencode','zed']){
  const data=JSON.parse(clientConfig({client,model:s.initial,selectedModels:s.models,baseURL:'http://127.0.0.1:8877/v1',key:'local-only'}));
  if(client==='zed'){assert.equal(data.language_models.openai_compatible['kilo-local'].available_models.length,2);assert.equal(data.agent.default_model.model,'vendor/b');assert.doesNotMatch(JSON.stringify(data),/local-only/)}
  else{assert.equal(Object.keys(data.provider['kilo-local'].models).length,2);assert.equal(data.provider['kilo-local'].options.apiKey,'local-only');assert.equal(data.model,'kilo-local/vendor/b')}
 }
 assert.equal(s.models[1].contextWindow,200000);assert.equal(editorPayload([{id:'bad',contextWindow:0}],'bad').models[0].contextWindow,0);
});
test('OpenCode launcher quotes paths, clears inline override and scopes environment',{skip:process.platform==='win32'},()=>{
 const dir=mkdtempSync(join(tmpdir(),'kilo-editor-'));
 try{
  const config=join(dir,"user's config.json"),output=join(dir,'result');writeFileSync(config,'{}');
  writeFileSync(join(dir,'opencode'),'#!/bin/sh\nprintf "%s\\n%s\\n%s\\n%s\\n" "$OPENCODE_CONFIG" "${OPENCODE_CONFIG_CONTENT-unset}" "$1" "$2" > "$KILO_TEST_OUTPUT"\n',{mode:0o755});
  const run=spawnSync('sh',['-c',editorLaunch(config,'vendor/model')],{env:{...process.env,PATH:dir+':'+process.env.PATH,OPENCODE_CONFIG:'original',OPENCODE_CONFIG_CONTENT:'conflict',KILO_TEST_OUTPUT:output},encoding:'utf8'});
  assert.equal(run.status,0,run.stderr);assert.equal(readFileSync(output,'utf8'),config+'\nunset\n--model\nkilo-local/vendor/model\n');
  assert.equal(spawnSync('sh',['-c',editorLaunch(join(dir,'missing'),'vendor/model')]).status,1);
  const ps=editorLaunch(config,'vendor/model','powershell');assert.match(ps,/finally/);assert.match(ps,/\$env:OPENCODE_CONFIG = \$kiloPrevious/);assert.doesNotMatch(ps,/apiKey|kl_local_/);
 }finally{rmSync(dir,{recursive:true,force:true})}
});

test('OpenCode PowerShell launch restores prior configuration and escapes paths',()=>{const command=editorLaunch("C:\\Users\\Team's profile\\opencode.json",'vendor/model','powershell');assert.match(command,/Team''s profile/);assert.match(command,/finally/);assert.match(command,/\$env:OPENCODE_CONFIG_CONTENT = \$kiloInline/);assert.doesNotMatch(command,/kl_local_/)});

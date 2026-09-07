import test from 'node:test';import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,writeFileSync,rmSync} from 'node:fs';import {tmpdir} from 'node:os';import {join} from 'node:path';import {spawnSync} from 'node:child_process';
import {claudeCapabilities,claudeSelection,claudeSettings,claudeEfforts,claudeEffortKey,claudeLaunch} from '../ui/claude-helper.mjs';
const models=[{id:'anthropic/claude-fable-5.1',displayName:'Fable',effort:'low'},{id:'anthropic/claude-opus-4.6',displayName:'Opus',effort:'medium'}];
test('Claude picker and per-model settings use versioned native capabilities',()=>{
 const selection=claudeSelection(models,models[0].id,{haiku:models[1].id},'modern');
 const config=claudeSettings(selection,claudeCapabilities('2.1.263'),'http://127.0.0.1:8877/v1','fixture');
 assert.deepEqual(config.modelPicker,{options:models.map(m=>({model:claudeEffortKey(m.id),label:m.displayName})),replaceBuiltInOptions:true});
 assert.equal(config.modelSettings['claude-fable-5-1'].effortLevel,'low');
 assert.equal(config.modelOverrides['claude-fable-5-1'],models[0].id);
 assert.equal(config.env.ANTHROPIC_DEFAULT_HAIKU_MODEL,claudeEffortKey(models[1].id));
 assert.equal(config.env.ANTHROPIC_DEFAULT_FABLE_MODEL,claudeEffortKey(models[0].id));
 assert.equal(config.env.ANTHROPIC_BASE_URL,'http://127.0.0.1:8877');
 const old=claudeSettings(selection,claudeCapabilities('2.1.39'),'http://127.0.0.1:8877/v1','fixture');
 assert.equal(old.modelPicker,undefined);assert.equal(old.modelSettings,undefined);assert.equal(old.effortLevel,'low');
 assert.deepEqual(claudeEfforts('z-ai/glm-5.3',claudeCapabilities('2.1.263')),[]);
 assert.deepEqual(claudeEfforts(models[0].id,claudeCapabilities('2.1.263')),['low','medium','high','xhigh']);
 assert.equal(claudeCapabilities('2.1.242').perModelEffort,false);
});
test('Claude launcher requires setup, isolates settings, clears conflicting auth and leaves the parent unchanged',{skip:process.platform==='win32'},()=>{
 const dir=mkdtempSync(join(tmpdir(),'claude-launch-'));
 try{
  const bin=join(dir,'bin');mkdirSync(bin);
  writeFileSync(join(bin,'claude'),'#!/usr/bin/env node\nconsole.log(JSON.stringify({args:process.argv.slice(2),dir:process.env.CLAUDE_CONFIG_DIR,stale:process.env.ANTHROPIC_API_KEY,provider:process.env.CLAUDE_CODE_USE_BEDROCK}))',{mode:0o755});
  const env={...process.env,PATH:bin+':'+process.env.PATH,KILO_TEST_HOME:dir,ANTHROPIC_API_KEY:'stale',CLAUDE_CODE_USE_BEDROCK:'1'};
  const command=claudeLaunch().replaceAll('$HOME','$KILO_TEST_HOME');
  assert.notEqual(spawnSync('/bin/sh',['-c',command],{env}).status,0);
  mkdirSync(join(dir,'.claude-kilo'));writeFileSync(join(dir,'.claude-kilo/settings.json'),'{}');
  const result=spawnSync('/bin/sh',['-c',command+"\nprintf '%s' \"$ANTHROPIC_API_KEY\""],{env,encoding:'utf8'});
  assert.equal(result.status,0,result.stderr);
  const [child,parent]=result.stdout.split('\n');
  assert.deepEqual(JSON.parse(child),{args:['--settings',join(dir,'.claude-kilo/settings.json')],dir:join(dir,'.claude-kilo')});assert.equal(parent,'stale');
 }finally{rmSync(dir,{recursive:true,force:true})}
});
test('Claude PowerShell launch scopes and restores environment without embedding credentials',()=>{
 const command=claudeLaunch('powershell');
 assert.match(command,/finally/);assert.match(command,/claude --settings \$kiloSettings/);
 assert.match(command,/\$kiloPrevious\[\$kiloName\]/);assert.match(command,/Test-Path -PathType Leaf/);
});

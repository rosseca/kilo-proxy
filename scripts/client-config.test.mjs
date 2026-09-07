import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,writeFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawnSync} from 'node:child_process';
import {clientConfig, launchCommand} from '../ui/client-config.mjs';
const base = {baseURL:'http://127.0.0.1:8877/v1',key:'local-test',model:'openai/gpt-5.2'};
test('Codex uses HTTP Responses and a distinct environment variable',()=>{
 const text=clientConfig({...base,client:'codex'});
 assert.match(text,/wire_api = "responses"/);assert.match(text,/supports_websockets = false/);
 assert.match(text,/env_key = "KILO_LOCAL_API_KEY"/);assert.ok(!text.includes(base.key));
 assert.match(text,/requires_openai_auth = false/);
});
test('Claude SDK base URL does not double v1 and bearer uses local key',()=>{
 const result=JSON.parse(clientConfig({...base,client:'claude',model:'anthropic/claude-sonnet-4.5'}));
 assert.equal(new URL('/v1/messages', result.env.ANTHROPIC_BASE_URL).href,'http://127.0.0.1:8877/v1/messages');
 assert.equal(result.env.ANTHROPIC_BASE_URL,'http://127.0.0.1:8877');
 assert.equal(result.env.ANTHROPIC_AUTH_TOKEN,'local-test');assert.equal(result.env.ANTHROPIC_API_KEY,undefined);
 assert.equal(result.env.ANTHROPIC_DEFAULT_HAIKU_MODEL,'anthropic/claude-sonnet-4.5');
});
test('desktop launcher isolates GUI data and quotes app paths and keys', {skip:process.platform==='win32'},()=>{
 const dir=mkdtempSync(join(tmpdir(),'kilo-launch-'));
 try {
  const profile=join(dir,'.codex-kilo-desktop');mkdirSync(profile);
  writeFileSync(join(profile,'config.toml'),'# fixture');
  const bin=join(dir,'bin');mkdirSync(bin);
  writeFileSync(join(bin,'open'),'#!/usr/bin/env node\nprocess.stdout.write(JSON.stringify(process.argv.slice(2)))',{mode:0o755});
  const key="a'b$()\\key",appPath="/Applications/Team's Codex.app";
  const command=launchCommand({client:'codex',key,appPath,platform:'macos',language:'en'});
  const result=spawnSync('/bin/sh',['-c',command.replaceAll('$HOME','$KILO_TEST_HOME')],{encoding:'utf8',env:{...process.env,PATH:bin+':'+process.env.PATH,KILO_TEST_HOME:dir}});
  assert.equal(result.status,0,result.stderr);
  const args=JSON.parse(result.stdout);
  assert.ok(args.includes('-n'));assert.ok(args.includes(appPath));
  assert.ok(args.includes('CODEX_HOME='+profile));
  assert.ok(args.includes('CODEX_ELECTRON_USER_DATA_PATH='+join(dir,'Library/Application Support/Codex Kilo')));
  assert.ok(args.includes('KILO_LOCAL_API_KEY='+key));
  assert.ok(args.includes('--user-data-dir='+join(dir,'Library/Application Support/Codex Kilo')));
 } finally {rmSync(dir,{recursive:true,force:true});}
});
test('CLI launcher scopes its environment and refuses an unconfigured profile', {skip:process.platform==='win32'},()=>{
 const dir=mkdtempSync(join(tmpdir(),'kilo-cli-'));
 try {
  const bin=join(dir,'bin');mkdirSync(bin);
  writeFileSync(join(bin,'codex'),'#!/usr/bin/env node\nconsole.log(JSON.stringify({home:process.env.CODEX_HOME,key:process.env.KILO_LOCAL_API_KEY}))',{mode:0o755});
  const env={...process.env,PATH:bin+':'+process.env.PATH,KILO_TEST_HOME:dir};
  delete env.CODEX_HOME;delete env.KILO_LOCAL_API_KEY;
  const command=launchCommand({client:'codex-cli',key:"local'key",shell:'unix',catalog:true}).replaceAll('$HOME','$KILO_TEST_HOME');
  assert.notEqual(spawnSync('/bin/sh',['-c',command],{env}).status,0);
  mkdirSync(join(dir,'.codex-kilo-cli'));writeFileSync(join(dir,'.codex-kilo-cli/config.toml'),'# fixture');
  const missing=spawnSync('/bin/sh',['-c',command],{env,encoding:'utf8'});
  assert.notEqual(missing.status,0);assert.match(missing.stderr,/models.json/);
  writeFileSync(join(dir,'.codex-kilo-cli/models.json'),'{}');
  const result=spawnSync('/bin/sh',['-c',command+"\nprintf '%s' \"${CODEX_HOME-unset}|${KILO_LOCAL_API_KEY-unset}\""],{env,encoding:'utf8'});
  assert.equal(result.status,0,result.stderr);
  const [child,parent]=result.stdout.split('\n');
  assert.deepEqual(JSON.parse(child),{home:join(dir,'.codex-kilo-cli'),key:"local'key"});
  assert.equal(parent,'unset|unset');
 } finally {rmSync(dir,{recursive:true,force:true});}
});
test('Windows launcher restores process environment after launching desktop',()=>{
 const command=launchCommand({client:'codex',key:"a'b",platform:'windows',appPath:"C:\\Codex\\Codex.exe"});
 assert.ok(command.includes("'a''b'"));assert.ok(command.includes('Start-Process'));
 assert.ok(command.includes('CODEX_ELECTRON_USER_DATA_PATH'));assert.ok(command.includes('finally'));
 assert.match(command,/if \(\$null -eq \$kiloPrevious\[\$kiloName\]\)/);
 assert.match(command,/Remove-Item -LiteralPath "Env:\$kiloName"/);
 assert.match(command,/Set-Item -LiteralPath "Env:\$kiloName" -Value \$kiloPrevious\[\$kiloName\]/);
 assert.doesNotMatch(command,/SetEnvironmentVariable/);
});

test('desktop catalog launcher refuses missing models.json before opening the app', {skip:process.platform==='win32'},()=>{
 const dir=mkdtempSync(join(tmpdir(),'kilo-catalog-launch-'));
 try {
  const profile=join(dir,'.codex-kilo-desktop');mkdirSync(profile);
  writeFileSync(join(profile,'config.toml'),'# fixture');
  const bin=join(dir,'bin');mkdirSync(bin);
  writeFileSync(join(bin,'open'),'#!/bin/sh\nprintf opened',{mode:0o755});
  const command=launchCommand({client:'codex',key:'test',appPath:'/Applications/Codex.app',platform:'macos',language:'en',catalog:true}).replaceAll('$HOME','$KILO_TEST_HOME');
  const options={encoding:'utf8',env:{...process.env,PATH:bin+':'+process.env.PATH,KILO_TEST_HOME:dir}};
  const missing=spawnSync('/bin/sh',['-c',command],options);
  assert.notEqual(missing.status,0);assert.match(missing.stderr,/models.json/);assert.equal(missing.stdout,'');
  writeFileSync(join(profile,'models.json'),'{}');
  const present=spawnSync('/bin/sh',['-c',command],options);
  assert.equal(present.status,0,present.stderr);assert.equal(present.stdout,'opened');
 } finally {rmSync(dir,{recursive:true,force:true});}
});

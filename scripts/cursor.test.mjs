import test from 'node:test';
import assert from 'node:assert/strict';
import {clientConfig,cursorGuide,launchCommand} from '../ui/client-config.mjs';
test('Cursor guide handles multiple exact IDs without exposing loopback credentials',()=>{
 const text=clientConfig({client:'cursor',language:'en',models:['openai/one','vendor/two','openai/one'],baseURL:'http://127.0.0.1:8877/v1',key:'local-sensitive-test'});
 assert.match(text,/external HTTPS endpoint required/);
 assert.match(text,/openai\/one\nvendor\/two$/);
 assert.doesNotMatch(text,/127\.0\.0\.1|local-sensitive-test|model_catalog_json/);
 assert.equal(launchCommand({client:'cursor'}),'');
});
test('Cursor guide preserves Spanish and English limitations and rejects invalid IDs',()=>{
 for(const language of ['en','es']) {
  const text=cursorGuide(['bad\nID','good/model','x'.repeat(257)],language);
  assert.ok(text.endsWith('good/model'));assert.doesNotMatch(text,/bad\nID/);
  assert.match(text,/HTTPS/);assert.match(text,/Cursor Tab/);
 }
});

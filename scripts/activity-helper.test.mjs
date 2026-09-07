import test from 'node:test';
import assert from 'node:assert/strict';
import {formatTraceJSON} from '../ui/activity-helper.mjs';
test('debug formatting preserves exact number lexemes, duplicate keys and escaped content',()=>{
 const input=String.raw`{"exact":9007199254740993,"exact":1e400,"message":"quote: \" and slash: \\ and newline: \n","empty":[],"nested":{"a":1}}`;
 const pretty=formatTraceJSON(input);
 assert.match(pretty,/9007199254740993/);assert.match(pretty,/1e400/);
 assert.equal((pretty.match(/"exact"/g)||[]).length,2);
 assert.deepEqual(JSON.parse(pretty),JSON.parse(input));
 assert.match(pretty,/\n  "exact"/);
});
test('SSE and incomplete captured JSON stay unchanged',()=>{
 for(const text of ['data: {"delta":"hello"}\n\ndata: [DONE]\n','{"truncated":','plain text'])assert.equal(formatTraceJSON(text),text);
});

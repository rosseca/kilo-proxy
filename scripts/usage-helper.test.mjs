import test from 'node:test';
import assert from 'node:assert/strict';
import {reportedCost, usageCoverage} from '../ui/usage-helper.mjs';
test('spend distinguishes missing, explicitly free, and sub-microdollar costs',()=>{
 assert.equal(reportedCost(undefined),null);
 assert.equal(reportedCost('0.000000000',0),null);
 assert.equal(reportedCost('0.000000000',1),'$0.000000');
 assert.equal(reportedCost('0.000000001',1),'< $0.000001');
 assert.equal(reportedCost('0.012345000',1),'$0.012345');
 assert.equal(reportedCost('-1.0'),null);
 assert.deepEqual(usageCoverage({requests:5,priced:2,incomplete:1}),{requests:5,priced:2,missing:3,incomplete:1});
});

test('cache percentages use matched input samples and distinguish unknown from zero',async()=>{
 const {cacheStats,lastCacheStats}=await import('../ui/usage-helper.mjs');
 assert.deepEqual(cacheStats({requests:3,cached:160,cacheWrite:20,withCacheRead:2,withCacheWrite:2,cacheRatioRequests:2,cacheRatioInput:200,cacheRatioRead:160}),{read:160,write:20,ratio:.8,covered:2,requests:3});
 assert.equal(cacheStats({cached:0}).read,null);
 assert.equal(cacheStats({cached:0,withCacheRead:1,cacheRatioInput:100,cacheRatioRead:0}).ratio,0);
 assert.equal(cacheStats({cacheRatioInput:0}).ratio,null);
 assert.equal(lastCacheStats({read:80,prompt:100,complete:true}).ratio,.8);
 assert.equal(lastCacheStats({read:80,prompt:100,complete:false}).ratio,null);
 assert.equal(lastCacheStats({read:null,prompt:100,complete:true}).read,null);
});

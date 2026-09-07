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

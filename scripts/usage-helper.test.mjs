import test from 'node:test';
import assert from 'node:assert/strict';
import {reportedCost, usageCoverage, reportedSpend, inferenceCostNote, costSourceLabel} from '../ui/usage-helper.mjs';
test('spend distinguishes missing, explicitly free, and sub-microdollar costs',()=>{
 assert.equal(reportedCost(undefined),null);
 assert.equal(reportedCost('0.000000000',0),null);
 assert.equal(reportedCost('0.000000000',1),'$0.000000');
 assert.equal(reportedCost('0.000000001',1),'< $0.000001');
 assert.equal(reportedCost('0.012345000',1),'$0.012345');
 assert.equal(reportedCost('-1.0'),null);
 assert.deepEqual(usageCoverage({requests:5,priced:2,incomplete:1}),{requests:5,priced:2,missing:3,incomplete:1});
});

test('a reported zero is explicitly a subtotal when other requests have no reported price',()=>{
 const summary={requests:38,priced:2,incomplete:19,costUSD:'0.000000000'};
 for(const language of ['en','es']){
  const result=reportedSpend(summary,language);
  assert.equal(result.partial,true);
  assert.equal(result.amount,'$0.000000');
  assert.equal(result.label,language==='es'?'Subtotal informado':'Reported subtotal');
  assert.equal(result.coverage,language==='es'?'Coste informado en 2 de 38 peticiones · 36 peticiones sin coste informado':'Cost reported for 2 of 38 requests · 36 requests without reported cost');
  assert.equal(result.responseStats,language==='es'?'Estadísticas de respuesta: 19 interrumpidas o limitadas':'Response stats: 19 interrupted or limited');
 }
 assert.equal(reportedSpend({...summary,costUSD:'0.125000000'}).amount,'$0.125000');
 assert.equal(reportedSpend({...summary,costUSD:'0.125000000'}).partial,true);
});

test('fully reported zero and unknown totals remain distinct regardless of response completeness',()=>{
 for(const language of ['en','es']){
  const complete=reportedSpend({requests:38,priced:38,incomplete:19,costUSD:'0.000000000'},language);
  assert.equal(complete.partial,false);
  assert.equal(complete.label,language==='es'?'Coste de inferencia informado':'Reported inference cost');
  assert.equal(complete.amount,'$0.000000');
  assert.ok(complete.coverage.includes(language==='es'?'0 peticiones sin coste informado':'0 requests without reported cost'));
  for(const requests of [0,38]){
   const unknown=reportedSpend({requests,priced:0,incomplete:0,costUSD:'0.000000000'},language);
   assert.equal(unknown.partial,false);
   assert.equal(unknown.amount,language==='es'?'Coste desconocido':'Not reported');
  }
 }
});

test('inference cost sources are identified without claiming a BYOK charge belongs to the Kilo invoice',()=>{
 for(const language of ['en','es']){
  assert.equal(costSourceLabel('usage.cost_details.upstream_inference_cost',language),language==='es'?'Inferencia del proveedor':'Provider inference');
  for(const source of ['provider_metadata.gateway.marketCost','response.provider_metadata.gateway.marketCost'])assert.equal(costSourceLabel(source,language),language==='es'?'Coste de mercado del gateway':'Gateway market cost');
  for(const source of ['usage.cost','usage.cost_microdollars'])assert.equal(costSourceLabel(source,language),language==='es'?'Coste informado por el gateway':'Gateway-reported cost');
  for(const source of ['',undefined,'unknown.field'])assert.equal(costSourceLabel(source,language),'');
  const note=inferenceCostNote(language);
  assert.ok(note.includes('BYOK'));
  assert.ok(note.includes(language==='es'?'Pueden diferir de los cargos':'may differ from your Kilo organization’s charges'));
  assert.ok(note.includes(language==='es'?'no son estimaciones':'not estimates'));
 }
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

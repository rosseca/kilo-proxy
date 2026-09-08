export function reportedCost(value, priced = 1) {
  if (!priced || typeof value !== 'string' || !/^\d+\.\d+$/.test(value)) return null;
  const amount = Number(value);
  if (!Number.isFinite(amount)) return null;
  if (amount > 0 && amount < 0.000001) return '< $0.000001';
  return '$' + amount.toFixed(6);
}
export function usageCoverage(summary) {
  const requests = Number(summary?.requests) || 0;
  const priced = Number(summary?.priced) || 0;
  return {requests, priced, missing: Math.max(0, requests - priced), incomplete: Number(summary?.incomplete) || 0};
}

// A known zero is still only a subtotal when other requests have no cost.
// Response completeness is independent: a limited response can report a price.
export function reportedSpend(summary, language = 'en') {
  const {requests,priced,missing,incomplete}=usageCoverage(summary);
  const es=language==='es',partial=priced>0&&missing>0;
  return {
    partial,
    amount:reportedCost(summary?.costUSD,priced) ?? (es?'Coste desconocido':'Not reported'),
    label:partial?(es?'Subtotal informado':'Reported subtotal'):(es?'Coste de inferencia informado':'Reported inference cost'),
    coverage:es?`Coste informado en ${priced} de ${requests} peticiones · ${missing} peticiones sin coste informado`:`Cost reported for ${priced} of ${requests} requests · ${missing} requests without reported cost`,
    responseStats:es?`Estadísticas de respuesta: ${incomplete} interrumpidas o limitadas`:`Response stats: ${incomplete} interrupted or limited`
  };
}

export function inferenceCostNote(language = 'en') {
  return language==='es'?'Costes de inferencia informados por el proveedor (incluido BYOK) o el gateway. Pueden diferir de los cargos de tu organización en Kilo; no son estimaciones.':'Inference costs reported by the provider (including BYOK) or gateway. They may differ from your Kilo organization’s charges; these are not estimates.';
}
export function costSourceLabel(source, language = 'en') {
  const es=language==='es';
  if(source==='usage.cost_details.upstream_inference_cost')return es?'Inferencia del proveedor':'Provider inference';
  if(['provider_metadata.gateway.marketCost','response.provider_metadata.gateway.marketCost'].includes(source))return es?'Coste de mercado del gateway':'Gateway market cost';
  if(['usage.cost_microdollars','usage.cost'].includes(source))return es?'Coste informado por el gateway':'Gateway-reported cost';
  return '';
}

export function cacheStats(summary) {
 const requests=Number(summary?.requests)||0;
 const covered=Number(summary?.cacheRatioRequests)||0;
 const input=Number(summary?.cacheRatioInput)||0,read=Number(summary?.cacheRatioRead)||0;
 const ratio=input>0 && read>=0 && read<=input ? read/input : null;
 return {read:summary?.withCacheRead>0 ? summary.cached : null,write:summary?.withCacheWrite>0 ? summary.cacheWrite : null,ratio,covered,requests};
}
export function lastCacheStats(sample) {
 const known=value=>typeof value==='number' && Number.isFinite(value) && value>=0;
 return {read:known(sample?.read)?sample.read:null,prompt:known(sample?.prompt)?sample.prompt:null,
 ratio:sample?.complete && known(sample.read) && known(sample.prompt) && sample.prompt>0 && sample.read<=sample.prompt ? sample.read/sample.prompt : null};
}

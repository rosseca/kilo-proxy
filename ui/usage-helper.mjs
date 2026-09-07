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

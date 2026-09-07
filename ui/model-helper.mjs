export function filterModels(models, query = '', coding = true) {
  const terms = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  return models.filter(m => (!coding || (m.tools === true && m.outputModalities?.includes('text'))) && terms.every(term => `${m.name} ${m.id}`.toLowerCase().includes(term)));
}
export function formatPrice(value, language = 'en') {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
    ? new Intl.NumberFormat(language, {style:'currency',currency:'USD',maximumFractionDigits:6}).format(value)
    : null;
}
export function validModelID(id) { return !!id && id.length <= 256 && !/[\s\u0000-\u001f\u007f]/u.test(id); }

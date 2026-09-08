export function filterModels(models, query = '', coding = true) {
  const terms = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  return models.filter(m => (!coding || (m.tools === true && m.outputModalities?.includes('text'))) && terms.every(term => `${m.name} ${m.id}`.toLowerCase().includes(term)));
}
const modelSortChoices = [
  ['codeModeRank', 'Code Mode Rank', 'Ranking de Code Mode'],
  ['codingIndex', 'Coding Index', 'Índice de programación'],
  ['speed', 'Speed', 'Velocidad'],
  ['price', 'Price', 'Precio'],
  ['name', 'Name', 'Nombre']
];
let currentModelSort = 'codeModeRank';
export function setModelSort(order) {
  currentModelSort = modelSortChoices.some(([key]) => key === order) ? order : 'codeModeRank';
}
export function modelSortOptions(language = 'en') {
  return modelSortChoices.map(([value,en,es]) => ({value,label:language === 'es' ? es : en}));
}
export function configureModelSort(select, language = 'en') {
  if (select.dataset.sortLanguage !== language) {
    select.replaceChildren();
    for (const choice of modelSortOptions(language)) {
      const option = select.ownerDocument.createElement('option');
      option.value = choice.value; option.textContent = choice.label; select.append(option);
    }
    select.dataset.sortLanguage = language;
  }
  select.value = currentModelSort;
}
export function sortModels(models, order = 'codeModeRank') {
  if (!modelSortChoices.some(([key]) => key === order)) order = 'codeModeRank';
  const numeric = value => typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
  const text = value => typeof value === 'string' ? value.trim() : '';
  const name = model => (text(model.displayName) || text(model.name) || text(model.id)).toLowerCase();
  const compare = (a,b) => a < b ? -1 : a > b ? 1 : 0;
  const field = {codeModeRank:'codeModeRank',codingIndex:'codingIndex',speed:'speed',price:'inputPrice'}[order];
  const direction = ['codingIndex','speed'].includes(order) ? -1 : 1;
  return [...models].sort((a,b) => {
    if (field) {
      const value = model => field === 'codeModeRank' && (!Number.isInteger(model[field]) || model[field] < 1) ? null : numeric(model[field]);
      const av=value(a), bv=value(b);
      if (av === null && bv !== null) return 1;
      if (av !== null && bv === null) return -1;
      if (av !== null && bv !== null && av !== bv) return compare(av,bv)*direction;
    }
    return compare(name(a),name(b)) || compare(a.id,b.id);
  });
}
export function mergeModelSelection(catalogModel, selection) {
  const merged = {...catalogModel,...selection};
  if (catalogModel) {
    // Refresh catalog observations without replacing a user's saved choices.
    for (const field of ['codeModeRank','codingIndex','speed','inputPrice','outputPrice']) merged[field] = catalogModel[field];
  }
  return merged;
}
export function formatPrice(value, language = 'en') {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
    ? new Intl.NumberFormat(language, {style:'currency',currency:'USD',maximumFractionDigits:6}).format(value)
    : null;
}
export function validModelID(id) { return !!id && id.length <= 256 && !/[\s\u0000-\u001f\u007f]/u.test(id); }

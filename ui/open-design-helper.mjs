import {validModelID, modelPriceDetails, filterModels, sortModels, configureModelSort, setModelSort, filterModelLab, configureModelLab, setModelLab} from './model-helper.mjs';

export const openDesignMaskedKey = 'kl_local_••••••••••••••••';

// These are individual provider fields, not an import format supported by Open Design.
export function openDesignConnection(state, models = [], initial = '') {
 const ids = [...new Set(models.map(model => model?.id).filter(id => typeof id === 'string' && validModelID(id)))].slice(0, 50);
 const model = ids.includes(initial) ? initial : ids[0] || '';
 const baseURL = state?.baseURL || '', key = state?.localKey || '';
 const pending = ['starting', 'pending'].includes(state?.auth?.status);
 return {baseURL, key, model, ids, canLaunch:!!(state?.hasKey && state?.orgId?.trim() && baseURL && key && model && !pending)};
}

export function createOpenDesignHelper({copy, refreshCatalog, onChange = () => {}}) {
 const $ = id => document.getElementById(id), selected = new Map();
 let ctx = {language:'en', catalog:[]}, initial = '', signature = '';
 const L = (en, es) => ctx.language === 'es' ? es : en;
 const connection = () => openDesignConnection(ctx.state, [...selected.values()], initial);
 function controls() {
  const current = connection();
  const labels = {
   'open-design-title':L('Open Design · connect your Kilo models', 'Open Design · conecta tus modelos de Kilo'),
   'open-design-intro':L('Open the installed desktop app above, then complete the provider setup below once. The proxy starts before the app opens.', 'Abre la aplicación instalada desde el botón superior y configura el proveedor una vez siguiendo los pasos de abajo. El proxy arranca antes de abrir la aplicación.'),
   'open-design-refresh':L('Refresh catalog', 'Actualizar catálogo'),
   'open-design-selected-label':L('Selected only', 'Solo seleccionados'),
   'open-design-sort-label':L('Sort by', 'Ordenar por'),
   'open-design-lab-label':L('Lab', 'Laboratorio'),
   'open-design-manual-title':L('Add by exact ID', 'Añadir por ID exacto'),
   'open-design-add':L('Add model', 'Añadir modelo'),
   'open-design-setup-title':L('One-time provider setup', 'Configuración inicial del proveedor'),
   'open-design-step-provider':L('In Open Design, open Models & providers → API providers → OpenAI. Then select Provider preset → Custom provider.', 'En Open Design, abre Models & providers → API providers → OpenAI. Después elige Provider preset → Custom provider.'),
   'open-design-step-model':L('Paste the API key and Base URL below. Under Model, select Custom (type below)… and paste the initial model ID into Custom model id. Use Test to check the connection; edits save automatically (All changes saved).', 'Pega la API key y la Base URL de abajo. En Model, elige Custom (type below)… y pega el ID del modelo inicial en Custom model id. Usa Test para comprobar la conexión; los cambios se guardan automáticamente (All changes saved).'),
   'open-design-model-note':L('Choose models that support Chat Completions. Use the exact IDs below to switch models in Open Design. Its model discovery may list the full Kilo catalog; this helper does not restrict that list. Repeat provider setup after changing the proxy port or local key.', 'Elige modelos compatibles con Chat Completions. Usa los IDs exactos de abajo para cambiar de modelo en Open Design. Su catálogo puede mostrar todos los modelos de Kilo; este helper no restringe esa lista. Actualiza el proveedor si cambias el puerto del proxy o la clave local.'),
   'open-design-capabilities':L('API provider (BYOK) supports chat, but cannot read, write or edit project files. For code changes, use Open Design’s Local CLI mode with a configured CLI. This setup does not configure an image provider.', 'API provider (BYOK) permite chatear, pero no leer, escribir ni editar archivos del proyecto. Para cambios de código, usa el modo Local CLI de Open Design con un CLI configurado. Estos ajustes no configuran un proveedor de imágenes.'),
   'open-design-url-label':'Base URL', 'open-design-key-label':'API key',
   'open-design-model-label':L('Initial model · exact ID', 'Modelo inicial · ID exacto'),
   'open-design-models-label':L('Selected model IDs', 'IDs de modelos seleccionados'),
   'open-design-copy-url':L('Copy URL', 'Copiar URL'), 'open-design-copy-key':L('Copy local key', 'Copiar clave local'),
   'open-design-copy-model':L('Copy initial model', 'Copiar modelo inicial'), 'open-design-copy-models':L('Copy model IDs', 'Copiar IDs de modelos'),
   'open-design-install':L('Download Open Design for macOS or Windows ↗', 'Descargar Open Design para macOS o Windows ↗'),
   'open-design-linux':L('Linux: build from source using the project instructions ↗', 'Linux: compila desde el código siguiendo las instrucciones del proyecto ↗'),
  };
  for (const [id, text] of Object.entries(labels)) $(id).textContent = text;
  $('open-design-search').placeholder = L('Search model or provider', 'Buscar modelo o proveedor');
  $('open-design-search').setAttribute('aria-label', $('open-design-search').placeholder);
  $('open-design-id').setAttribute('aria-label', L('Exact model ID', 'ID exacto del modelo'));
  $('open-design-url').value = current.baseURL;
  $('open-design-key').value = current.key ? openDesignMaskedKey : '';
  $('open-design-model').value = current.model;
  $('open-design-model-ids').textContent = current.ids.join('\n');
  $('open-design-copy-url').disabled = !current.baseURL;
  $('open-design-copy-key').disabled = !current.key;
  $('open-design-copy-model').disabled = !current.model;
  $('open-design-copy-models').disabled = !current.ids.length;
  $('open-design-add').disabled = selected.size >= 50 || !validModelID($('open-design-id').value.trim());
  $('open-design-status').textContent = L('Select your models, then copy the connection fields into Open Design. Provider settings are saved inside Open Design.', 'Selecciona tus modelos y copia los campos de conexión en Open Design. Los ajustes del proveedor se guardan dentro de Open Design.');
  onChange();
 }
 function render(context) {
  ctx = context; controls();
  const all = new Map(ctx.catalog.map(model => [model.id, model]));
  for (const [id, model] of selected) if (!all.has(id)) all.set(id, model);
  configureModelSort($('open-design-sort'), ctx.language);
  configureModelLab($('open-design-lab'), [...all.values()], ctx.language);
  const matches = sortModels(filterModels(filterModelLab([...all.values()], $('open-design-lab').value), $('open-design-search').value, false).filter(model => !$('open-design-selected').checked || selected.has(model.id)), $('open-design-sort').value);
  const next = JSON.stringify([matches, [...selected.keys()], initial, ctx.language]);
  if (signature === next) return;
  signature = next;
  const scroll = $('open-design-picker').scrollTop;
  $('open-design-picker').replaceChildren();
  for (const model of matches.slice(0, 200)) {
   const chosen = selected.has(model.id), card = document.createElement('div'), label = document.createElement('label');
   card.className = 'codex-model-entry' + (chosen ? ' is-selected' : ''); label.className = 'model-option';
   const check = document.createElement('input'); check.type = 'checkbox'; check.checked = chosen; check.disabled = !chosen && selected.size >= 50;
   check.dataset.openDesignId = model.id; check.setAttribute('aria-label', model.name || model.id);
   check.addEventListener('change', () => {
    if (check.checked) {selected.set(model.id, model); if (!initial) initial = model.id;}
    else {selected.delete(model.id); if (initial === model.id) initial = selected.keys().next().value || '';}
    render(ctx);
   });
   const text = document.createElement('span'), title = document.createElement('strong'), id = document.createElement('small');
   text.className = 'model-option-title'; title.textContent = model.name || model.id; id.textContent = model.id; text.append(title, id);
   label.append(check, text, modelPriceDetails(model, ctx.language)); card.append(label);
   if (chosen) {
    const row = document.createElement('div'), button = document.createElement('button'); row.className = 'codex-row-controls'; button.type = 'button'; button.className = 'codex-default-button';
    button.textContent = initial === model.id ? L('★ Initial model', '★ Modelo inicial') : L('Use as initial model', 'Usar como modelo inicial');
    button.setAttribute('aria-pressed', String(initial === model.id)); button.dataset.openDesignInitial = model.id;
    button.addEventListener('click', () => {initial = model.id; render(ctx);}); row.append(button); card.append(row);
   }
   $('open-design-picker').append(card);
  }
  if (!matches.length) {const empty = document.createElement('p'); empty.textContent = L('No matches. Refresh the catalog or add a model by ID.', 'Sin resultados. Actualiza el catálogo o añade un modelo por ID.'); $('open-design-picker').append(empty);}
  $('open-design-picker').scrollTop = scroll;
 }
 for (const id of ['open-design-search', 'open-design-selected']) $(id).addEventListener('input', () => render(ctx));
 $('open-design-sort').addEventListener('change', () => {setModelSort($('open-design-sort').value); render(ctx);});
 $('open-design-lab').addEventListener('change', () => {setModelLab($('open-design-lab').value); render(ctx);});
 $('open-design-refresh').addEventListener('click', () => refreshCatalog());
 $('open-design-id').addEventListener('input', controls);
 $('open-design-add').addEventListener('click', () => {
  const id = $('open-design-id').value.trim(); if (!validModelID(id) || selected.size >= 50) return;
  selected.set(id, ctx.catalog.find(model => model.id === id) || {id, name:id}); if (!initial) initial = id;
  $('open-design-id').value = ''; $('open-design-search').value = ''; $('open-design-selected').checked = true; render(ctx);
 });
 for (const [button, field] of [['open-design-copy-url', 'baseURL'], ['open-design-copy-key', 'key'], ['open-design-copy-model', 'model']]) $(button).addEventListener('click', () => {const value = connection()[field]; if (value) copy(value);});
 $('open-design-copy-models').addEventListener('click', () => {const ids = connection().ids; if (ids.length) copy(ids.join('\n'));});
 return {render, launchState:() => {
  const current = connection();
  return {id:'open-design', count:current.ids.length, ready:true, valid:current.canLaunch, working:false,
   reason:!ctx.state?.hasKey || !ctx.state?.orgId?.trim() ? L('Connect your Kilo account and team first.', 'Conecta primero tu cuenta y equipo de Kilo.') : ['starting', 'pending'].includes(ctx.state?.auth?.status) ? L('Finish signing in before opening Open Design.', 'Completa el inicio de sesión antes de abrir Open Design.') : '',
   fingerprint:JSON.stringify([current.baseURL, current.key, current.ids, current.model, current.canLaunch])};
 }};
}

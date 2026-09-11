import {validModelID, modelPriceDetails, filterModels, sortModels, configureModelSort, setModelSort, filterModelLab, configureModelLab, setModelLab} from './model-helper.mjs';

export const openDesignEngines = ['codex-cli', 'claude', 'opencode'];
export function openDesignLibrary(models = [], initial = '') {
 const selected = new Map();
 for (const model of models) {
  if (typeof model?.id !== 'string' || !validModelID(model.id) || selected.has(model.id) || selected.size >= 50) continue;
  const entry = {id:model.id, displayName:Array.from(model.displayName || model.name || model.id).slice(0, 80).join('')};
  for (const key of ['contextWindow', 'maxOutputTokens']) if (Number.isSafeInteger(model[key]) && model[key] > 0) entry[key] = model[key];
  for (const key of ['reasoningEffort', 'reasoningCustom']) if (model[key] !== undefined) entry[key] = model[key];
  if (Array.isArray(model.reasoningLevels)) entry.reasoningLevels = [...model.reasoningLevels];
  selected.set(model.id, entry);
 }
 return {schemaVersion:1, defaultModel:selected.has(initial) ? initial : selected.keys().next().value || '', models:[...selected.values()]};
}
export function openDesignCanLaunch(state, engine, engines, library) {
 return !!(openDesignEngines.includes(engine) && engines?.[engine]?.available && state?.hasKey && state?.orgId?.trim() && library.models.length && !['starting', 'pending'].includes(state?.auth?.status));
}

export function createOpenDesignHelper({api, refreshCatalog, onChange = () => {}}) {
 const $ = id => document.getElementById(id), selected = new Map();
 let ctx = {language:'en', catalog:[]}, initial = '', signature = '', engine = 'codex-cli';
 let profile = null, loaded = false, loading = false, working = false, edited = false, engineEdited = false, error = '';
 const L = (en, es) => ctx.language === 'es' ? es : en;
 const library = () => openDesignLibrary([...selected.values()], initial);
 const fingerprint = () => JSON.stringify([engine, library(), ctx.state?.baseURL, ctx.state?.localKey]);
 function controls() {
  const labels = {
   'open-design-title':L('Open Design · choose your coding agent', 'Open Design · elige tu agente de programación'),
   'open-design-intro':L('Choose a local CLI and launch Open Design. Kilo Proxy prepares its models and credentials automatically, then starts the proxy before opening the app.', 'Elige un CLI local y abre Open Design. Kilo Proxy prepara sus modelos y credenciales automáticamente y arranca el proxy antes de abrir la aplicación.'),
   'open-design-engine-label':L('Coding engine', 'Motor de programación'),
   'open-design-detect':loading ? L('Checking…', 'Comprobando…') : L('Check engines', 'Comprobar motores'),
   'open-design-refresh':L('Refresh catalog', 'Actualizar catálogo'),
   'open-design-selected-label':L('Selected only', 'Solo seleccionados'),
   'open-design-sort-label':L('Sort by', 'Ordenar por'),
   'open-design-lab-label':L('Lab', 'Laboratorio'),
   'open-design-manual-title':L('Add by exact ID', 'Añadir por ID exacto'),
   'open-design-add':L('Add model', 'Añadir modelo'),
   'open-design-model-note':L('Starts from your saved model library. Changes here apply to the Open Design profile and do not replace your shared library. The initial model is saved in the selected CLI profile.', 'Parte de tu biblioteca de modelos guardada. Los cambios aquí se aplican al perfil de Open Design y no sustituyen tu biblioteca compartida. El modelo inicial se guarda en el perfil del CLI elegido.'),
   'open-design-capabilities':L('Open Design uses Local CLI for project files, tools and code changes. It opens with a separate Kilo profile. If it is already open, close that instance before applying new settings and launching again.', 'Open Design usa Local CLI para trabajar con archivos, herramientas y cambios de código. Se abre con un perfil de Kilo independiente. Si ya está abierto, cierra esa instancia antes de aplicar nuevos ajustes y volver a abrir.'),
   'open-design-next':L('Complete Open Design’s welcome flow if shown. Use Models & providers → Local CLI and the selected agent. Keep the CLI default to follow the initial model above; its model and reasoning controls depend on that CLI.', 'Completa la bienvenida de Open Design si aparece. Usa Models & providers → Local CLI y el agente elegido. Mantén el modelo predeterminado del CLI para seguir el modelo inicial de arriba; sus controles de modelos y razonamiento dependen de ese CLI.'),
   'open-design-maintenance':L('Image and media providers are configured separately in Open Design. Update the installed app normally; this Kilo workspace does not run its own updater.', 'Los proveedores de imágenes y multimedia se configuran aparte en Open Design. Actualiza la aplicación instalada de forma habitual; este espacio Kilo no ejecuta su propio actualizador.'),
   'open-design-install':L('Download Open Design for macOS or Windows ↗', 'Descargar Open Design para macOS o Windows ↗'),
   'open-design-linux':L('Linux: build from source using the project instructions ↗', 'Linux: compila desde el código siguiendo las instrucciones del proyecto ↗'),
  };
  for (const [id, text] of Object.entries(labels)) $(id).textContent = text;
  $('open-design-search').placeholder = L('Search model or provider', 'Buscar modelo o proveedor');
  $('open-design-search').setAttribute('aria-label', $('open-design-search').placeholder);
  $('open-design-id').setAttribute('aria-label', L('Exact model ID', 'ID exacto del modelo'));
  $('open-design-engine').value = engine;
  $('open-design-engine').disabled = loading || working;
  $('open-design-detect').disabled = loading || working;
  $('open-design-add').disabled = working || selected.size >= 50 || !validModelID($('open-design-id').value.trim());
  $('open-design-engine-status').textContent = loading ? L('Checking installed CLI engines…', 'Comprobando los motores CLI instalados…') : profile?.engines?.[engine]?.available ? L('Installed · runs inside Open Design, without opening a terminal.', 'Instalado · se ejecuta dentro de Open Design, sin abrir una terminal.') : profile?.engines?.[engine]?.reason || L('This CLI is not installed. Install it and check again.', 'Este CLI no está instalado. Instálalo y vuelve a comprobar.');
  $('open-design-status').textContent = error || (working ? L('Preparing the CLI and Open Design profile…', 'Preparando el CLI y el perfil de Open Design…') : L('Select an engine and models, then use Launch above. Settings are prepared on every launch.', 'Elige un motor y modelos y usa Abrir arriba. Los ajustes se preparan en cada arranque.'));
  $('open-design-status').classList.toggle('error', !!error);
  onChange();
 }
 async function load() {
  if (loading || working) return;
  loading = true; error = ''; controls();
  try {
   const result = await api('open-design/profile'); profile = result;
   if (!engineEdited && openDesignEngines.includes(result.engine)) engine = result.engine;
   if (!edited && !selected.size) {
    for (const model of result.library?.models || []) selected.set(model.id, {...model});
    initial = result.library?.defaultModel || selected.keys().next().value || '';
   }
  } catch (failure) {profile = null; error = failure.message;}
  finally {loaded = true; loading = false; render(ctx);}
 }
 async function prepare() {
  if (working || loading) throw new Error(L('Wait for engine preparation to finish.', 'Espera a que termine la preparación del motor.'));
  const body = {engine, library:library()}; working = true; error = ''; render(ctx);
  try {await api('open-design/profile', body);}
  catch (failure) {error = failure.message; throw failure;}
  finally {working = false; render(ctx);}
 }
 function render(context) {
  ctx = context;
  if (!loaded && !loading) void load();
  controls();
  const all = new Map(ctx.catalog.map(model => [model.id, model]));
  for (const [id, model] of selected) if (!all.has(id)) all.set(id, model);
  configureModelSort($('open-design-sort'), ctx.language);
  configureModelLab($('open-design-lab'), [...all.values()], ctx.language);
  const matches = sortModels(filterModels(filterModelLab([...all.values()], $('open-design-lab').value), $('open-design-search').value, false).filter(model => !$('open-design-selected').checked || selected.has(model.id)), $('open-design-sort').value);
  const next = JSON.stringify([matches, [...selected.keys()], initial, ctx.language, working]);
  if (signature === next) return;
  signature = next;
  const scroll = $('open-design-picker').scrollTop;
  $('open-design-picker').replaceChildren();
  for (const model of matches.slice(0, 200)) {
   const chosen = selected.has(model.id), card = document.createElement('div'), label = document.createElement('label');
   card.className = 'codex-model-entry' + (chosen ? ' is-selected' : ''); label.className = 'model-option';
   const check = document.createElement('input'); check.type = 'checkbox'; check.checked = chosen; check.disabled = working || (!chosen && selected.size >= 50);
   check.dataset.openDesignId = model.id; check.setAttribute('aria-label', model.name || model.id);
   check.addEventListener('change', () => {
    edited = true;
    if (check.checked) {selected.set(model.id, model); if (!initial) initial = model.id;}
    else {selected.delete(model.id); if (initial === model.id) initial = selected.keys().next().value || '';}
    render(ctx);
   });
   const text = document.createElement('span'), title = document.createElement('strong'), id = document.createElement('small');
   text.className = 'model-option-title'; title.textContent = selected.get(model.id)?.displayName || model.name || model.id; id.textContent = model.id; text.append(title, id);
   label.append(check, text, modelPriceDetails(model, ctx.language)); card.append(label);
   if (chosen) {
    const row = document.createElement('div'), button = document.createElement('button'); row.className = 'codex-row-controls'; button.type = 'button'; button.className = 'codex-default-button';
    button.textContent = initial === model.id ? L('★ Initial model', '★ Modelo inicial') : L('Use as initial model', 'Usar como modelo inicial');
    button.setAttribute('aria-pressed', String(initial === model.id)); button.dataset.openDesignInitial = model.id;
    button.disabled = working; button.addEventListener('click', () => {edited = true; initial = model.id; render(ctx);}); row.append(button); card.append(row);
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
 $('open-design-detect').addEventListener('click', () => load());
 $('open-design-engine').addEventListener('change', () => {engine = $('open-design-engine').value; engineEdited = true; error = ''; render(ctx);});
 $('open-design-id').addEventListener('input', controls);
 $('open-design-add').addEventListener('click', () => {
  const id = $('open-design-id').value.trim(); if (!validModelID(id) || selected.size >= 50 || working) return;
  edited = true; selected.set(id, ctx.catalog.find(model => model.id === id) || {id, name:id}); if (!initial) initial = id;
  $('open-design-id').value = ''; $('open-design-search').value = ''; $('open-design-selected').checked = true; render(ctx);
 });
 return {render, reload:load, launchState:() => ({id:'open-design', engine, count:selected.size, ready:false, working:working || loading, prepare,
  valid:loaded && openDesignCanLaunch(ctx.state, engine, profile?.engines, library()),
  reason:!ctx.state?.hasKey || !ctx.state?.orgId?.trim() ? L('Connect your Kilo account and team first.', 'Conecta primero tu cuenta y equipo de Kilo.') : error || (!loading && !profile?.engines?.[engine]?.available ? profile?.engines?.[engine]?.reason || L('Install the selected CLI and check again.', 'Instala el CLI elegido y vuelve a comprobar.') : ''),
  fingerprint:fingerprint()})};
}

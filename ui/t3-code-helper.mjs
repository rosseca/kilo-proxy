import {connectionReady} from './chatgpt-helper.mjs';
import {translate} from './i18n.mjs';
import {openMausBotLibraryReady} from './openmausbot-helper.mjs';

const librarySignature = library => JSON.stringify(library);
export function t3CodePrepared(saved, profile) {
 return Boolean(openMausBotLibraryReady(saved) && profile?.prepared === true && librarySignature(saved.library) === librarySignature(profile.library));
}

// Use the common library. Never write a separate model selection or global CLI
// settings, and prepare an immutable snapshot before every launch.
export function createT3CodeHelper({api, onChange}) {
 const $ = id => document.getElementById(id);
 let ctx = {state:null, language:'en'}, saved = null, profile = null;
 let loaded = false, loading = false, working = false, error = '', profileConnection = '';
 const L = (en, es) => ctx.language === 'en' ? en : es;
 const connectionSignature = () => JSON.stringify([ctx.state?.baseURL, ctx.state?.localKey, ctx.state?.orgId, ctx.state?.connectionReady, ctx.state?.chatgpt?.connected]);
 const prepared = () => profileConnection === connectionSignature() && t3CodePrepared(saved, profile);
 const ready = () => loaded && openMausBotLibraryReady(saved);
 const signature = () => JSON.stringify([connectionSignature(), saved?.library]);
 const reason = () => !connectionReady(ctx.state) ? L('Connect your selected provider first.', 'Conecta primero el proveedor seleccionado.') : error || (loading ? L('Loading shared models…', 'Cargando modelos compartidos…') : !ready() ? L('Add and save models in the native app’s Models page, then refresh here.', 'Añade y guarda modelos en la página Modelos de la app nativa y actualiza aquí.') : '');
 function render(next = ctx) {
  ctx = next;
  $('t3-code-intro').textContent = L('Separate T3 Code workspace with four agents: Codex, Codex · Kilo, Claude and Claude · Kilo.', 'Espacio T3 Code separado con cuatro agentes: Codex, Codex · Kilo, Claude y Claude · Kilo.');
  $('t3-code-summary').textContent = saved?.library?.models?.length ? L('Default Kilo model: ', 'Modelo Kilo inicial: ') + saved.library.defaultModel : L('No shared models saved.', 'No hay modelos compartidos guardados.');
  const list = $('t3-code-models');
  list.replaceChildren();
  const models = saved?.library?.models || [];
  const displayed = [...models.filter(model => model.id === saved?.library?.defaultModel), ...models.filter(model => model.id !== saved?.library?.defaultModel)];
  for (const model of displayed) {
   const item = document.createElement('li');
   item.textContent = model.displayName ? model.displayName + ' · ' + model.id : model.id;
   list.append(item);
  }
  $('t3-code-library-help').textContent = L('Kilo agents use the saved shared library. Manage models in Models in the native Kilo Proxy app, then refresh here. Normal agents keep their own model catalog and CLI login.', 'Los agentes Kilo usan la biblioteca compartida guardada. Gestiona los modelos en Modelos en la app nativa Kilo Proxy y actualiza aquí. Los agentes normales conservan su catálogo de modelos y su sesión CLI.');
  $('t3-code-agents-title').textContent = L('Choose an agent for each new chat', 'Elige un agente para cada chat nuevo');
  $('t3-code-agents').replaceChildren();
  for (const text of [L('Codex · Existing Codex CLI login', 'Codex · Sesión existente de Codex CLI'), L('Codex · Kilo · Shared models through the proxy', 'Codex · Kilo · Modelos compartidos a través del proxy'), L('Claude · Existing Claude Code login', 'Claude · Sesión existente de Claude Code'), L('Claude · Kilo · Compatible shared models through the proxy', 'Claude · Kilo · Modelos compartidos compatibles a través del proxy')]) {
   const item = document.createElement('li'); item.textContent = text; $('t3-code-agents').append(item);
  }
  $('t3-code-options').textContent = L('Options', 'Opciones');
  $('t3-code-requirements').textContent = L('Requires T3 Code 0.0.45, Codex CLI and Claude Code. The normal options use CLI sessions, not Desktop app logins.', 'Requiere T3 Code 0.0.45, Codex CLI y Claude Code. Las opciones normales usan sesiones CLI, no los inicios de sesión de las apps Desktop.');
  $('t3-code-compatibility').textContent = L('Choose the agent when creating a new chat. Existing chats keep their agent; start a new chat to switch between normal and Kilo. Codex offers the model’s supported reasoning levels. Claude offers only levels supported by its CLI and model. Generation and tools also depend on the provider. In Claude · Kilo, choose a prepared model by its exact gateway ID. T3 also lists built-in Claude models, which may not be available through your provider.', 'Elige el agente al crear un chat nuevo. Los chats existentes conservan su agente; inicia uno nuevo para cambiar entre normal y Kilo. Codex ofrece los niveles de razonamiento compatibles con el modelo. Claude ofrece solo los compatibles con su CLI y modelo. La generación y las herramientas también dependen del proveedor. En Claude · Kilo, elige un modelo preparado por su ID exacto del gateway. T3 también muestra modelos de Claude incluidos en la app, que pueden no estar disponibles a través de tu proveedor.');
  $('t3-code-privacy').textContent = L('Your regular T3 Code workspace and chats stay separate and can remain open. Normal agents use their existing CLI sessions without copying login data. Quit only T3 Code · Kilo before changing models or the connection, then reopen it.', 'Tu espacio T3 Code habitual y sus chats siguen separados y pueden permanecer abiertos. Los agentes normales usan sus sesiones CLI existentes sin copiar datos de inicio de sesión. Cierra solo T3 Code · Kilo antes de cambiar los modelos o la conexión y vuelve a abrirlo.');
  $('t3-code-reload').textContent = loading ? L('Loading…', 'Cargando…') : L('Refresh shared models', 'Actualizar modelos compartidos');
  $('t3-code-reload').disabled = loading || working;
  $('t3-code-prepare').textContent = working ? L('Preparing…', 'Preparando…') : L('Prepare without opening', 'Preparar sin abrir');
  $('t3-code-prepare').disabled = working || loading || !ready() || !connectionReady(ctx.state);
  $('t3-code-install').textContent = L('Get T3 Code ↗', 'Obtener T3 Code ↗');
  $('t3-code-prepared').textContent = prepared() ? L('Four agents prepared for the current shared models.', 'Cuatro agentes preparados con los modelos compartidos actuales.') : L('Prepare or open T3 Code · Kilo to apply the current shared models.', 'Prepara o abre T3 Code · Kilo para aplicar los modelos compartidos actuales.');
  $('t3-code-status').textContent = translate(error || (working ? L('Preparing four agents…', 'Preparando cuatro agentes…') : profile?.message || ''), ctx.language);
  $('t3-code-status').classList.toggle('error', !!error);
  onChange();
  if (!loaded && !loading) void reload();
 }
 async function reload() {
  if (loading || working) return;
  loading = true; error = ''; const connection = connectionSignature(); render();
  try { [saved, profile] = await Promise.all([api('model-library'), api('clients/t3-code')]); profileConnection = connection; }
  catch (failure) { saved = null; profile = null; error = failure.message; }
  finally { loaded = true; loading = false; render(); }
 }
 async function prepare() {
  if (working || loading || !ready() || !connectionReady(ctx.state)) throw new Error(reason());
  working = true; error = ''; const snapshot = structuredClone(saved.library), connection = connectionSignature(); render();
  try {
   const latest = await api('model-library');
   if (!openMausBotLibraryReady(latest) || librarySignature(latest.library) !== librarySignature(snapshot)) {
    saved = latest;
    throw new Error(L('The shared models changed. Review the refreshed list and open again.', 'Los modelos compartidos han cambiado. Revisa la lista actualizada y vuelve a abrir.'));
   }
   profile = await api('clients/t3-code', {library:snapshot}); profileConnection = connection;
   const current = await api('model-library');
   if (!openMausBotLibraryReady(current) || librarySignature(current.library) !== librarySignature(snapshot)) {
    saved = current;
    throw new Error(L('The shared models changed while preparing. Review the refreshed list and open again.', 'Los modelos compartidos cambiaron durante la preparación. Revisa la lista actualizada y vuelve a abrir.'));
   }
   return profile;
  } catch (failure) { error = failure.message; throw failure; }
  finally { working = false; render(); }
 }
 $('t3-code-reload').addEventListener('click', () => void reload());
 $('t3-code-prepare').addEventListener('click', () => { void prepare().catch(() => {}); });
 return {render, reload, launchState:() => ({id:'t3-code', count:saved?.library?.models?.length || 0, ready:false, valid:ready() && connectionReady(ctx.state), reason:reason(), working:working || loading, fingerprint:signature(), prepare})};
}

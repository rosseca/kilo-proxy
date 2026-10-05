import {connectionReady} from './chatgpt-helper.mjs';
import {translate} from './i18n.mjs';
import {openMausBotLibraryReady} from './openmausbot-helper.mjs';

const librarySignature = library => JSON.stringify(library);
export function synaraPrepared(saved, profile) {
 return Boolean(openMausBotLibraryReady(saved) && profile?.prepared === true && librarySignature(saved.library) === librarySignature(profile.library));
}

// Use the common library. Never write a separate model selection or global CLI
// settings, and prepare an immutable snapshot before every launch.
export function createSynaraHelper({api, onChange}) {
 const $ = id => document.getElementById(id);
 let ctx = {state:null, language:'en'}, saved = null, profile = null;
 let loaded = false, loading = false, working = false, error = '', profileConnection = '';
 const L = (en, es) => ctx.language === 'en' ? en : es;
 const connectionSignature = () => JSON.stringify([ctx.state?.baseURL, ctx.state?.localKey, ctx.state?.orgId, ctx.state?.connectionReady, ctx.state?.chatgpt?.connected]);
 const prepared = () => profileConnection === connectionSignature() && synaraPrepared(saved, profile);
 const tooManyModels = () => (saved?.library?.models?.length || 0) > 32;
 const ready = () => loaded && openMausBotLibraryReady(saved) && !tooManyModels();
 const signature = () => JSON.stringify([connectionSignature(), saved?.library]);
 const reason = () => !connectionReady(ctx.state) ? L('Connect your selected provider first.', 'Conecta primero el proveedor seleccionado.') : error || (loading ? L('Loading shared models…', 'Cargando modelos compartidos…') : tooManyModels() ? L('Synara supports up to 32 shared models. Reduce your selection in Models and prepare again.', 'Synara admite hasta 32 modelos compartidos. Reduce la selección en Modelos y prepara de nuevo.') : !ready() ? L('Add and save models in the native app’s Models page, then refresh here.', 'Añade y guarda modelos en la página Modelos de la app nativa y actualiza aquí.') : '');
 function render(next = ctx) {
  ctx = next;
  $('synara-intro').textContent = L('Separate Synara workspace with four agents: Codex · Normal, Kilo Proxy · Codex, Claude · Normal and Kilo Proxy · Claude. Kilo Proxy names and green account indicators identify Kilo agents.', 'Espacio Synara separado con cuatro agentes: Codex · Normal, Kilo Proxy · Codex, Claude · Normal y Kilo Proxy · Claude. Los nombres Kilo Proxy y los indicadores verdes de cuenta identifican los agentes Kilo.');
  $('synara-summary').textContent = saved?.library?.models?.length ? L('Default Kilo model: ', 'Modelo Kilo inicial: ') + saved.library.defaultModel : L('No shared models saved.', 'No hay modelos compartidos guardados.');
  const list = $('synara-models');
  list.replaceChildren();
  const models = saved?.library?.models || [];
  const displayed = [...models.filter(model => model.id === saved?.library?.defaultModel), ...models.filter(model => model.id !== saved?.library?.defaultModel)];
  for (const model of displayed) {
   const item = document.createElement('li');
   item.textContent = model.displayName ? model.displayName + ' · ' + model.id : model.id;
   list.append(item);
  }
  $('synara-library-help').textContent = L('Kilo agents use the saved shared library. Manage models in Models in the native Kilo Proxy app, then refresh here. Normal agents keep their own model catalog and CLI login.', 'Los agentes Kilo usan la biblioteca compartida guardada. Gestiona los modelos en Modelos en la app nativa Kilo Proxy y actualiza aquí. Los agentes normales conservan su catálogo de modelos y su sesión CLI.');
  $('synara-agents-title').textContent = L('Choose an agent for each new chat', 'Elige un agente para cada chat nuevo');
  $('synara-agents').replaceChildren();
  for (const text of [L('Codex · Normal · Existing Codex CLI login', 'Codex · Normal · Sesión existente de Codex CLI'), L('Kilo Proxy · Codex · Shared models through the proxy', 'Kilo Proxy · Codex · Modelos compartidos a través del proxy'), L('Claude · Normal · Existing Claude Code login', 'Claude · Normal · Sesión existente de Claude Code'), L('Kilo Proxy · Claude · Compatible shared models through the proxy', 'Kilo Proxy · Claude · Modelos compartidos compatibles a través del proxy')]) {
   const item = document.createElement('li'); item.textContent = text; $('synara-agents').append(item);
  }
  $('synara-options').textContent = L('Options', 'Opciones');
  $('synara-requirements').textContent = L('Supports Synara Beta 1.0.0-beta.1. Requires native Codex CLI and Claude Code. Normal agents use CLI sessions, not Desktop app logins. Codex · Normal requires file-based CLI authentication; keyring/auto is not supported by Synara.', 'Admite Synara Beta 1.0.0-beta.1. Requiere Codex CLI nativo y Claude Code. Los agentes normales usan sesiones CLI, no los inicios de sesión de las apps Desktop. Codex · Normal requiere autenticación CLI en archivo; Synara no admite keyring/auto.') + (profile?.version ? L(' Detected: ', ' Detectado: ') + profile.version + '.' : '');
  $('synara-compatibility').textContent = L('Choose an agent for each new chat. Existing chats keep their account; start a new chat to switch between normal and Kilo. Select the exact gateway model ID shown in the prepared list and check the selected model before continuing; A built-in Claude choice maps to your exact gateway ID when its family and version match one prepared model; other built-ins may be unavailable. For custom Claude gateway IDs, compatible reasoning defaults come from Kilo Models. Close and reopen Synara · Kilo after changing them; saved levels require Claude Code 2.1.251 or newer. Generation and tools depend on the model and provider.', 'Elige un agente para cada chat nuevo. Los chats existentes conservan su cuenta; inicia uno nuevo para cambiar entre normal y Kilo. Selecciona el ID exacto del gateway mostrado en la lista preparada y revisa el modelo elegido antes de continuar; Una opción Claude incluida se asigna a tu ID exacto del gateway cuando su familia y versión coinciden con un modelo preparado; las demás pueden no estar disponibles. Para IDs Claude personalizados del gateway, el razonamiento compatible inicial viene de Modelos de Kilo. Cierra y reabre Synara · Kilo tras cambiarlos; los niveles guardados requieren Claude Code 2.1.251 o posterior. La generación y las herramientas dependen del modelo y del proveedor.');
  $('synara-privacy').textContent = L('Your regular Synara workspace and chats stay separate and can remain open. Normal agents reference your existing CLI sessions; Kilo Proxy does not copy login credentials. Quit only Synara · Kilo before changing models or the connection, then reopen it.', 'Tu espacio Synara habitual y sus chats siguen separados y pueden permanecer abiertos. Los agentes normales referencian tus sesiones CLI existentes; Kilo Proxy no copia credenciales de inicio de sesión. Cierra solo Synara · Kilo antes de cambiar los modelos o la conexión y vuelve a abrirlo.');
  $('synara-reload').textContent = loading ? L('Loading…', 'Cargando…') : L('Refresh shared models', 'Actualizar modelos compartidos');
  $('synara-reload').disabled = loading || working;
  $('synara-prepare').textContent = working ? L('Preparing…', 'Preparando…') : L('Prepare without opening', 'Preparar sin abrir');
  $('synara-prepare').disabled = working || loading || !ready() || !connectionReady(ctx.state);
  $('synara-install').textContent = L('Get Synara ↗', 'Obtener Synara ↗');
  $('synara-prepared').textContent = prepared() ? L('Four agents prepared for the current shared models.', 'Cuatro agentes preparados con los modelos compartidos actuales.') : L('Prepare or open Synara · Kilo to apply the current shared models.', 'Prepara o abre Synara · Kilo para aplicar los modelos compartidos actuales.');
  const message = !profile?.prepared || prepared() ? profile?.message || '' : '';
  $('synara-status').textContent = translate(error || (working ? L('Preparing four agents…', 'Preparando cuatro agentes…') : message), ctx.language);
  $('synara-status').classList.toggle('error', !!error);
  onChange();
  if (!loaded && !loading) void reload();
 }
 async function reload() {
  if (loading || working) return;
  loading = true; error = ''; const connection = connectionSignature(); render();
  try { [saved, profile] = await Promise.all([api('model-library'), api('clients/synara')]); profileConnection = connection; }
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
   profile = await api('clients/synara', {library:snapshot}); profileConnection = connection;
   const current = await api('model-library');
   if (!openMausBotLibraryReady(current) || librarySignature(current.library) !== librarySignature(snapshot)) {
    saved = current;
    throw new Error(L('The shared models changed while preparing. Review the refreshed list and open again.', 'Los modelos compartidos cambiaron durante la preparación. Revisa la lista actualizada y vuelve a abrir.'));
   }
   return profile;
  } catch (failure) { error = failure.message; throw failure; }
  finally { working = false; render(); }
 }
 $('synara-reload').addEventListener('click', () => void reload());
 $('synara-prepare').addEventListener('click', () => { void prepare().catch(() => {}); });
 return {render, reload, launchState:() => ({id:'synara', count:saved?.library?.models?.length || 0, ready:false, valid:ready() && connectionReady(ctx.state), reason:reason(), working:working || loading, fingerprint:signature(), prepare})};
}

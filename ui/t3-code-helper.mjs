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
 const tooManyModels = () => (saved?.library?.models?.length || 0) > 32;
 const ready = () => loaded && openMausBotLibraryReady(saved) && !tooManyModels();
 const signature = () => JSON.stringify([connectionSignature(), saved?.library]);
 const reason = () => !connectionReady(ctx.state) ? L('Connect your selected provider first.', 'Conecta primero el proveedor seleccionado.') : error || (loading ? L('Loading shared models…', 'Cargando modelos compartidos…') : tooManyModels() ? L('T3 Code supports up to 32 shared models. Reduce your selection in Models and prepare again.', 'T3 Code admite hasta 32 modelos compartidos. Reduce la selección en Modelos y prepara de nuevo.') : !ready() ? L('Add and save models in the native app’s Models page, then refresh here.', 'Añade y guarda modelos en la página Modelos de la app nativa y actualiza aquí.') : '');
 function render(next = ctx) {
  ctx = next;
  $('t3-code-intro').textContent = L('Separate T3 Code workspace with four agents: Codex · Normal, Kilo Proxy · Codex, Claude · Normal and Kilo Proxy · Claude. Green KP badges on T3’s provider rail and composer identify Kilo agents; model rows use the Kilo Proxy name.', 'Espacio T3 Code separado con cuatro agentes: Codex · Normal, Kilo Proxy · Codex, Claude · Normal y Kilo Proxy · Claude. Las marcas KP verdes en los iconos del selector y el compositor de T3 identifican los agentes Kilo; las filas de modelos usan el nombre Kilo Proxy.');
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
  for (const text of [L('Codex · Normal · Existing Codex CLI login', 'Codex · Normal · Sesión existente de Codex CLI'), L('Kilo Proxy · Codex · Shared models through the proxy', 'Kilo Proxy · Codex · Modelos compartidos a través del proxy'), L('Claude · Normal · Existing Claude Code login', 'Claude · Normal · Sesión existente de Claude Code'), L('Kilo Proxy · Claude · Compatible shared models through the proxy', 'Kilo Proxy · Claude · Modelos compartidos compatibles a través del proxy')]) {
   const item = document.createElement('li'); item.textContent = text; $('t3-code-agents').append(item);
  }
  $('t3-code-options').textContent = L('Options', 'Opciones');
  $('t3-code-requirements').textContent = L('Supports T3 Code 0.0.45 and nightly 0.0.46-nightly.20261003.2610. Requires Codex CLI and Claude Code. The normal options use CLI sessions, not Desktop app logins.', 'Admite T3 Code 0.0.45 y nightly 0.0.46-nightly.20261003.2610. Requiere Codex CLI y Claude Code. Las opciones normales usan sesiones CLI, no los inicios de sesión de las apps Desktop.') + (profile?.version ? L(' Detected: ', ' Detectado: ') + profile.version + '.' : '');
  $('t3-code-compatibility').textContent = L('Choose the agent when creating a new chat. Existing chats keep their agent; start a new chat to switch between normal and Kilo. Codex offers the model’s supported reasoning levels. Claude offers only levels supported by its CLI and model. Generation and tools also depend on the provider. Kilo Proxy · Claude starts with your prepared shared models. Preparation hides the built-in Claude entries. An older chat using a hidden model falls back to your shared default; check the selected model before continuing.', 'Elige el agente al crear un chat nuevo. Los chats existentes conservan su agente; inicia uno nuevo para cambiar entre normal y Kilo. Codex ofrece los niveles de razonamiento compatibles con el modelo. Claude ofrece solo los compatibles con su CLI y modelo. La generación y las herramientas también dependen del proveedor. Kilo Proxy · Claude empieza con tus modelos compartidos preparados. La preparación oculta las entradas de Claude incluidas en T3. Un chat antiguo que usaba un modelo oculto pasa a tu modelo compartido inicial; revisa el modelo elegido antes de continuar.');
  if (profile?.version === '0.0.46-nightly.20261003.2610') $('t3-code-compatibility').textContent = L('This nightly can change agents between turns in the same chat. T3 transfers a summary when switching providers; previous reasoning, tool results and attachments are not transferred. Each agent keeps its own CLI profile. Codex offers the model’s supported reasoning levels. This nightly ignores Claude effort options for custom IDs, so Kilo Proxy · Claude uses compatible levels saved in Kilo Models. Close and reopen the Kilo workspace after changing them; Claude Code 2.1.251 or newer is required for these levels (2.1.267 or newer for Opus/Sonnet 5.5). Kilo Proxy · Claude starts with your prepared shared models; built-in Claude entries are hidden during preparation. Check the selected model before continuing an older chat.', 'Este nightly permite cambiar de agente entre turnos en el mismo chat. T3 transfiere un resumen al cambiar de proveedor; no transfiere el razonamiento anterior, los resultados de herramientas ni los adjuntos. Cada agente conserva su propio perfil CLI. Codex ofrece los niveles de razonamiento compatibles con el modelo. Este nightly ignora las opciones de esfuerzo de Claude para IDs personalizados, por lo que Kilo Proxy · Claude usa los niveles compatibles guardados en Modelos de Kilo. Cierra y reabre el espacio Kilo tras cambiarlos; requieren Claude Code 2.1.251 o posterior (2.1.267 o posterior para Opus/Sonnet 5.5). Kilo Proxy · Claude empieza con tus modelos compartidos preparados; la preparación oculta las entradas de Claude incluidas en T3. Revisa el modelo elegido antes de continuar un chat antiguo.');
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

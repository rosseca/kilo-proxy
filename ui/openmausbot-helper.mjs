import {connectionReady} from './chatgpt-helper.mjs';

export function openMausBotLibraryReady(saved) {
 const library = saved?.library;
 return !saved?.recoveryRequired && library?.schemaVersion === 1 && Array.isArray(library.models) && library.models.length > 0 && library.models.some(model => model.id === library.defaultModel);
}

const librarySignature = library => JSON.stringify([library?.schemaVersion, library?.defaultModel, (library?.models || []).map(model => [model.id, model.displayName || '', model.reasoningEffort || '', model.reasoningLevels || [], model.reasoningCustom === true, model.contextPreset || '', model.contextWindow || 0, model.maxOutputTokens || 0])]);
export function openMausBotReasoningReady(saved, profile) {
 return Boolean(openMausBotLibraryReady(saved) && profile?.prepared === true && profile.library && profile.reasoningEfforts && typeof profile.reasoningEfforts === 'object' && !Array.isArray(profile.reasoningEfforts) && librarySignature(saved.library) === librarySignature(profile.library));
}
export function openMausBotReasoningLabel(effort, language = 'en') {
 const labels = {none:['None','Sin razonamiento'],minimal:['Minimal','Mínimo'],low:['Low','Bajo'],medium:['Medium','Medio'],high:['High','Alto'],xhigh:['Extra high','Muy alto'],max:['Max','Máximo'],ultra:['Ultra','Ultra']};
 return (labels[effort] || ['Automatic','Automático'])[language === 'es' ? 1 : 0];
}

// This helper reads the common library; it never creates a second selection or
// writes /api/model-library. Preparation always sends an immutable snapshot.
export function createOpenMausBotHelper({api, onChange}) {
 const $ = id => document.getElementById(id);
 let ctx = {state:null, language:'en'}, saved = null, profile = null;
 let loaded = false, loading = false, working = false, error = '', profileConnection = '';
 const connectionSignature = () => JSON.stringify([ctx.state?.baseURL, ctx.state?.localKey, ctx.state?.orgId, ctx.state?.connectionReady, ctx.state?.chatgpt?.connected]);
 const reasoningReady = () => profileConnection === connectionSignature() && openMausBotReasoningReady(saved, profile);
 const L = (en, es) => ctx.language === 'en' ? en : es;
 const signature = () => JSON.stringify([ctx.state?.baseURL, ctx.state?.localKey, ctx.state?.orgId, ctx.state?.connectionReady, ctx.state?.chatgpt?.connected, saved?.library]);
 const ready = () => loaded && openMausBotLibraryReady(saved);
 const reason = () => !connectionReady(ctx.state) ? L('Connect your selected provider first.', 'Conecta primero el proveedor seleccionado.') : error || (loading ? L('Loading shared models…', 'Cargando modelos compartidos…') : !ready() ? L('Add and save models in the native app’s Models page, then refresh here.', 'Añade y guarda modelos en la página Modelos de la app nativa y actualiza aquí.') : '');
 function render(next = ctx) {
  ctx = next;
  $('openmausbot-intro').textContent = L('Desktop assistant using the saved shared model library.', 'Asistente de escritorio que usa la biblioteca de modelos compartida guardada.');
  $('openmausbot-summary').textContent = saved?.library?.models?.length ? L('Default: ', 'Inicial: ') + saved.library.defaultModel : L('No shared models saved.', 'No hay modelos compartidos guardados.');
  const list = $('openmausbot-models');
  list.replaceChildren();
  const models = saved?.library?.models || [];
  const displayed = [...models.filter(model => model.id === saved?.library?.defaultModel), ...models.filter(model => model.id !== saved?.library?.defaultModel)];
  for (const model of displayed) {
   const item = document.createElement('li');
   item.textContent = model.id + (reasoningReady() ? L(' · Reasoning: ', ' · Razonamiento: ') + openMausBotReasoningLabel(profile.reasoningEfforts[model.id], ctx.language) : '');
   list.append(item);
  }
  $('openmausbot-library-help').textContent = L('Manage this library in Models in the native Kilo Proxy app. Refresh here after saving changes. Reasoning shown here is the prepared proxy setting.', 'Gestiona esta biblioteca en Modelos en la app nativa Kilo Proxy. Actualiza aquí tras guardar cambios. El razonamiento mostrado es el ajuste preparado del proxy.') + (!reasoningReady() ? L(' Prepare or open OpenMausBot to confirm reasoning for these models.', ' Prepara o abre OpenMausBot para confirmar el razonamiento de estos modelos.') : '');
  $('openmausbot-options').textContent = L('Options', 'Opciones');
  $('openmausbot-compatibility').textContent = L('Applies your shared models with the default listed first. OpenMausBot shows exact model IDs instead of custom names. Kilo Proxy applies each model’s supported reasoning level saved in Models. Automatic sends no fixed level; OpenMausBot has no separate reasoning picker. Generation and tools depend on the model and provider.', 'Aplica tus modelos compartidos, mostrando primero el predeterminado. OpenMausBot muestra los ID exactos en lugar de nombres personalizados. Kilo Proxy aplica el nivel de razonamiento compatible de cada modelo guardado en Modelos. Automático no envía un nivel fijo; OpenMausBot no tiene otro selector de razonamiento. La generación y las herramientas dependen del modelo y del proveedor.');
  $('openmausbot-privacy').textContent = L('Uses a separate Kilo profile with only the local proxy key. Quit that instance before changing models or the connection, then open it again.', 'Usa un perfil Kilo independiente con solo la clave local del proxy. Cierra esa instancia antes de cambiar los modelos o la conexión y vuelve a abrirla.');
  $('openmausbot-reload').textContent = loading ? L('Loading…', 'Cargando…') : L('Refresh shared models', 'Actualizar modelos compartidos');
  $('openmausbot-reload').disabled = loading || working;
  $('openmausbot-prepare').textContent = working ? L('Preparing…', 'Preparando…') : L('Prepare without opening', 'Preparar sin abrir');
  $('openmausbot-prepare').disabled = working || loading || !ready() || !connectionReady(ctx.state);
  $('openmausbot-install').textContent = L('Get OpenMausBot ↗', 'Obtener OpenMausBot ↗');
  $('openmausbot-config').textContent = profile?.configPath || '';
  $('openmausbot-status').textContent = error || (working ? L('Preparing local configuration…', 'Preparando la configuración local…') : '');
  $('openmausbot-status').classList.toggle('error', !!error);
  onChange();
  if (!loaded && !loading) void reload();
 }
 async function reload() {
  if (loading || working) return;
  loading = true; error = ''; const connection = connectionSignature(); render();
  try { [saved, profile] = await Promise.all([api('model-library'), api('clients/openmausbot')]); profileConnection = connection; }
  catch (failure) { saved = null; error = failure.message; }
  finally { loaded = true; loading = false; render(); }
 }
 async function prepare() {
  if (working || loading || !ready()) throw new Error(reason());
  working = true; error = ''; const snapshot = structuredClone(saved.library), connection = connectionSignature(); render();
  try {
   const latest = await api('model-library');
   if (!openMausBotLibraryReady(latest) || JSON.stringify(latest.library) !== JSON.stringify(snapshot)) {
    saved = latest;
    throw new Error(L('The shared models changed. Review the refreshed list and open again.', 'Los modelos compartidos han cambiado. Revisa la lista actualizada y vuelve a abrir.'));
   }
   profile = await api('clients/openmausbot', {library:snapshot});
   profileConnection = connection;
   const current = await api('model-library');
   if (!openMausBotLibraryReady(current) || JSON.stringify(current.library) !== JSON.stringify(snapshot)) {
    saved = current;
    throw new Error(L('The shared models changed while preparing. Review the refreshed list and open again.', 'Los modelos compartidos cambiaron durante la preparación. Revisa la lista actualizada y vuelve a abrir.'));
   }
   return profile;
  } catch (failure) { error = failure.message; throw failure; }
  finally { working = false; render(); }
 }
 $('openmausbot-reload').addEventListener('click', () => void reload());
 $('openmausbot-prepare').addEventListener('click', () => { void prepare().catch(() => {}); });
 return {render, reload, launchState:() => ({id:'openmausbot', count:saved?.library?.models?.length || 0, ready:false, valid:ready() && connectionReady(ctx.state), reason:reason(), working:working || loading, fingerprint:signature(), prepare})};
}

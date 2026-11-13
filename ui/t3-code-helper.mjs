import {connectionReady} from './chatgpt-helper.mjs';
import {translate} from './i18n.mjs';
import {openMausBotLibraryReady} from './openmausbot-helper.mjs';

const librarySignature = library => JSON.stringify(library);
const emptyLibrary = {schemaVersion:1,defaultModel:'',models:[]};
const availableLibrary = saved => !saved?.warning && openMausBotLibraryReady(saved) ? saved.library : null;
function catalogLibrary(saved) {
 const library = saved?.library;
 if (!saved?.warning && !saved?.recoveryRequired && library?.schemaVersion === 1 && Array.isArray(library.models) && library.models.length === 0 && library.defaultModel === '') return library;
 return availableLibrary(saved) ?? emptyLibrary;
}
const openCodeConnection = value => value?.openCodeConnection || 'native';
const museConnection = value => value?.museConnection || 'native';
const museModelPolicy = value => value?.museModels ?? (museConnection(value) === 'kilo' ? 'shared' : 'official');
const museUsesKilo = value => value?.providers?.includes('muse') && museConnection(value) === 'kilo';
const openCodeUsesKilo = value => value?.providers?.includes('opencode') && openCodeConnection(value) === 'kilo';
const normalizePreferences = value => ({...structuredClone(value),openCodeConnection:openCodeConnection(value),museConnection:museConnection(value),museModels:museModelPolicy(value)});
const preferencesSignature = value => JSON.stringify(value && [value.version,value.codex,value.claude,value.kiloCodex,value.kiloClaude,value.codexModels,value.claudeModels,[...(value.providers ?? [])].sort(),value.openCode2,openCodeConnection(value),museConnection(value),museModelPolicy(value)]);
const knownProviders = {cursor:'Cursor',grok:'Grok',opencode:'OpenCode',antigravity:'Antigravity',muse:'Muse Code',pi:'Pi'};
export function t3CodePreferencesValid(value) {
 return value?.version === 1 && ['official','shared'].includes(value.codexModels) && ['official','shared'].includes(value.claudeModels) && ['codex','claude','kiloCodex','kiloClaude','openCode2'].every(key => typeof value[key] === 'boolean') && Array.isArray(value.providers) && value.providers.every(id => typeof id === 'string') && (!value.openCode2 || value.providers.includes('opencode')) && [undefined,'','native','kilo'].includes(value.openCodeConnection) && [undefined,'','native','kilo'].includes(value.museConnection) && [undefined,'official','shared'].includes(value.museModels);
}
export function t3CodeNativeOnly(value) {
 return t3CodePreferencesValid(value) && !value.kiloCodex && !value.kiloClaude && !openCodeUsesKilo(value) && !museUsesKilo(value);
}
const usesGatewayCatalog = value => Boolean(value?.kiloClaude && value.claudeModels === 'official' || museUsesKilo(value) && museModelPolicy(value) === 'official');
const requiresSharedLibrary = value => !value || Boolean(value.kiloCodex || openCodeUsesKilo(value) || value.kiloClaude && value.claudeModels === 'shared' || museUsesKilo(value) && museModelPolicy(value) === 'shared');
export function t3CodePrepared(saved, profile, preferences) {
 if (profile?.prepared !== true || preferences && preferencesSignature(preferences) !== preferencesSignature(profile.preferences)) return false;
 if (t3CodeNativeOnly(preferences)) return true;
 const library = requiresSharedLibrary(preferences) ? availableLibrary(saved) : catalogLibrary(saved);
 return !!library && librarySignature(library) === librarySignature(profile.library);
}

// Settings and launch snapshots belong to the separate Kilo workspace.
export function createT3CodeHelper({api, onChange}) {
 const $ = id => document.getElementById(id);
 let ctx = {state:null, language:'en'}, saved = null, profile = null, preferences = null, capabilities = [];
 let loaded = false, loading = false, working = false, saving = false, error = '', preferencesError = '', pendingPreferences = null, profileConnection = '', requiresPreparation = false, openCode2Supported = false, openCodeKiloSupported = false, museKiloSupported = false, generation = 0;
 const L = (en, es) => ctx.language === 'en' ? en : es;
 const connectionSignature = () => JSON.stringify([ctx.state?.baseURL, ctx.state?.localKey, ctx.state?.orgId, ctx.state?.usageAccountScope, ctx.state?.catalogRevision, ctx.state?.kiloReady, ctx.state?.connectionReady, ctx.state?.chatgpt?.connected]);
 const nativeOnly = () => t3CodeNativeOnly(preferences);
 const catalogMode = () => usesGatewayCatalog(preferences);
 const selectedLibrary = source => requiresSharedLibrary(preferences) ? availableLibrary(source) : catalogLibrary(source);
 const connected = () => nativeOnly() || (catalogMode() ? ctx.state?.kiloReady === true : connectionReady(ctx.state));
 const prepared = () => connected() && !saving && !pendingPreferences && (nativeOnly() || profileConnection === connectionSignature()) && t3CodePrepared(saved, profile, preferences);
 const tooManyModels = () => (saved?.library?.models?.length || 0) > 32 && (preferences?.kiloCodex && preferences.codexModels === 'shared' || preferences?.kiloClaude && preferences.claudeModels === 'shared');
 const openCodeKiloUnavailable = () => openCodeUsesKilo(preferences) && !openCodeKiloSupported;
 const museKiloUnavailable = () => museUsesKilo(preferences) && !museKiloSupported;
 const ready = () => !museKiloUnavailable() && !openCodeKiloUnavailable() && loaded && t3CodePreferencesValid(preferences) && (!requiresSharedLibrary(preferences) || !!availableLibrary(saved)) && !tooManyModels() && !pendingPreferences && !saving;
 const signature = () => JSON.stringify([nativeOnly() ? null : connectionSignature(), nativeOnly() ? null : selectedLibrary(saved), preferencesSignature(preferences)]);
 const reason = () => museKiloUnavailable() ? L('This T3 build cannot use the Kilo Muse connection. Choose your Meta account, disable Muse or install a compatible T3 build.','Esta compilación de T3 no admite la conexión Kilo de Muse. Elige tu cuenta de Meta, desactiva Muse o instala una compilación T3 compatible.') : openCodeKiloUnavailable() ? L('This T3 build cannot use the Kilo OpenCode connection. Choose your own OpenCode account, disable OpenCode or install a compatible T3 build.', 'Esta compilación de T3 no admite la conexión Kilo de OpenCode. Elige tu cuenta propia de OpenCode, desactiva OpenCode o instala una compilación T3 compatible.') : !connected() ? catalogMode() ? L('Connect Kilo to use the Claude or Meta catalog in T3 Code.', 'Conecta Kilo para usar el catálogo de Claude o Meta en T3 Code.') : L('Connect your selected provider first.', 'Conecta primero el proveedor seleccionado.') : preferencesError || error || (loading ? L('Loading T3 Code settings…', 'Cargando ajustes de T3 Code…') : tooManyModels() ? L('T3 Code supports up to 32 shared models per account. Reduce your selection in Models or choose official models.', 'T3 Code admite hasta 32 modelos compartidos por cuenta. Reduce la selección en Modelos o elige modelos oficiales.') : saving ? L('Wait for T3 Code settings to finish saving.', 'Espera a que termine el guardado de los ajustes de T3 Code.') : !ready() ? L('Add and save models in the native app’s Models page, then refresh here.', 'Añade y guarda modelos en la página Modelos de la app nativa y actualiza aquí.') : '');
 const currentProfile = () => (!catalogMode() || profileConnection === connectionSignature()) && preferencesSignature(preferences) === preferencesSignature(profile?.preferences) ? profile : null;
 const visibleProviders = value => [...capabilities.map(row=>({...row,supported:true})),...(value?.providers||[]).filter(id=>knownProviders[id]&&!capabilities.some(row=>row.id===id)).map(id=>({id,label:knownProviders[id],supported:false}))];
 const acceptPreferences = response => {
  if (!t3CodePreferencesValid(response?.preferences)) throw new Error(L('Could not read T3 Code preferences. Refresh to try again.', 'No se pudieron leer las preferencias de T3 Code. Actualiza para reintentarlo.'));
  preferences = normalizePreferences(response.preferences);
  if (Array.isArray(response.supportedProviders)) capabilities = response.supportedProviders.filter(row => knownProviders[row?.id] && typeof row?.label === 'string');
  openCode2Supported = response.openCode2Supported === true;
  openCodeKiloSupported = response.openCodeKiloSupported === true;
  museKiloSupported = response.museKiloSupported === true;
  requiresPreparation = response.requiresPreparation === true;
 };
 function renderPreferences() {
  const displayed = saving && pendingPreferences ? pendingPreferences : preferences;
  $('t3-code-settings-title').textContent = L('T3 Code settings', 'Ajustes de T3 Code');
  $('t3-code-settings-help').textContent = L('Changes save automatically and apply only to the separate Kilo workspace. Close T3 Code · Kilo and prepare it again; your chats and existing CLI logins are kept.', 'Los cambios se guardan solos y solo afectan al espacio Kilo separado. Cierra T3 Code · Kilo y prepara de nuevo; se conservan tus chats y sesiones CLI existentes.');
  $('t3-code-accounts-label').textContent = L('Accounts', 'Cuentas');
  const editable = loaded && !!preferences && !loading && !working && !saving && !pendingPreferences;
  for (const key of ['codex','claude','kiloCodex','kiloClaude']) {
   const node = $('t3-code-account-'+key); node.checked = displayed?.[key] === true; node.disabled = !editable;
  }
  $('t3-code-codex-models-label').textContent = L('Codex Kilo models', 'Modelos de Codex Kilo');
  $('t3-code-claude-models-label').textContent = L('Claude Kilo models', 'Modelos de Claude Kilo');
  $('t3-code-codex-official').textContent = L('OpenAI and ChatGPT', 'OpenAI y ChatGPT');
  $('t3-code-claude-official').textContent = L('Claude models', 'Modelos de Claude');
  for (const kind of ['codex','claude']) {
   $('t3-code-'+kind+'-shared').textContent = L('All shared models', 'Todos los modelos compartidos');
   const select = $('t3-code-'+kind+'-models'); select.value = displayed?.[kind+'Models'] ?? 'official'; select.disabled = !editable || !displayed?.[kind === 'codex' ? 'kiloCodex' : 'kiloClaude'];
  }
  $('t3-code-reasoning-help').textContent = L('The Claude catalog includes models available through Kilo that work with your installed versions. Shared models come from your saved list in Models. Reasoning options vary by model and installed versions.', 'El catálogo de Claude incluye los modelos disponibles en Kilo compatibles con tus versiones instaladas. Los modelos compartidos salen de tu lista guardada en Modelos. Las opciones de razonamiento dependen del modelo y de las versiones instaladas.');
  $('t3-code-extra-providers-label').textContent = L('Other providers', 'Otros proveedores');
  $('t3-code-provider-options').replaceChildren();
  const visible = visibleProviders(displayed);
  for (const provider of visible) {
   const label = document.createElement('label'); label.className = 'checkbox-label';
   const input = document.createElement('input'); input.type = 'checkbox'; input.id = 't3-code-provider-'+provider.id; input.checked = displayed?.providers.includes(provider.id) === true; input.disabled = !editable;
   input.addEventListener('change', () => { if (input.checked && !provider.supported) {input.checked=false;return;} const next = structuredClone(displayed); next.providers = next.providers.filter(id => id !== provider.id); if (input.checked) {next.providers.push(provider.id);if(provider.id==='muse')next.museConnection='kilo';} if (provider.id === 'opencode' && !input.checked) next.openCode2 = false; void savePreferences(next); });
   const text = document.createElement('span'); text.textContent = provider.label+(provider.supported?'':L(' · unavailable in this build',' · no disponible en esta compilación')); label.append(input); label.append(text); $('t3-code-provider-options').append(label);
  }
  $('t3-code-providers-help').textContent = visible.some(row=>!row.supported) ? L('Turn off unavailable saved choices or install a compatible T3 build before preparing.','Desactiva las selecciones guardadas no disponibles o instala una compilación T3 compatible antes de preparar.') : capabilities.length ? capabilities.some(row=>['opencode','muse'].includes(row.id)) ? L('Choose OpenCode and Muse connections below. Other providers use their own logins.', 'Elige debajo las conexiones de OpenCode y Muse. Los demás proveedores usan sus sesiones propias.') : L('These providers use their own installations and logins.', 'Estos proveedores usan sus instalaciones y sesiones propias.') : L('This T3 build does not expose additional compatible providers.', 'Esta compilación de T3 no ofrece proveedores compatibles adicionales.');
  const hasMuse=visible.some(row=>row.id==='muse');
  $('t3-code-muse-connection-field').hidden=!hasMuse; $('t3-code-muse-connection-help').hidden=!hasMuse;
  $('t3-code-muse-connection-label').textContent=L('Muse connection','Conexión de Muse');
  $('t3-code-muse-connection-kilo').textContent=L('Kilo Proxy','Kilo Proxy');
  $('t3-code-muse-connection-native').textContent=L('Own Meta account','Cuenta propia de Meta');
  $('t3-code-muse-connection').value=museConnection(displayed);
  $('t3-code-muse-connection').disabled=!editable||!displayed?.providers.includes('muse')||!capabilities.some(row=>row.id==='muse');
  $('t3-code-muse-connection-kilo').disabled=!museKiloSupported;
  $('t3-code-muse-connection-help').textContent=!museKiloSupported ? L('This T3 build cannot use the Kilo Muse connection. Choose your Meta account, disable Muse or install a compatible T3 build.','Esta compilación de T3 no admite la conexión Kilo de Muse. Elige tu cuenta de Meta, desactiva Muse o instala una compilación T3 compatible.') : L('Kilo uses the chosen model policy in a separate Muse profile. Your Meta account keeps its own login and catalog. Both use the Muse executable selected in Settings. Close T3 Code · Kilo and prepare again after changing the connection.','Kilo usa la política de modelos elegida en un perfil Muse separado. Tu cuenta de Meta conserva su sesión y catálogo propios. Ambas usan el ejecutable Muse elegido en Ajustes. Cierra T3 Code · Kilo y prepara de nuevo tras cambiar la conexión.');
  $('t3-code-muse-models-field').hidden=!hasMuse||!museUsesKilo(displayed);
  $('t3-code-muse-models-help').hidden=!hasMuse||!museUsesKilo(displayed);
  $('t3-code-muse-models-label').textContent=L('Muse Kilo models','Modelos de Muse Kilo');
  $('t3-code-muse-official').textContent=L('Meta models','Modelos de Meta');
  $('t3-code-muse-shared').textContent=L('All shared models','Todos los modelos compartidos');
  $('t3-code-muse-models').value=museModelPolicy(displayed);
  $('t3-code-muse-models').disabled=!editable||!museUsesKilo(displayed)||!capabilities.some(row=>row.id==='muse');
  $('t3-code-muse-models-help').textContent=L('Meta models come from your Kilo catalog. Shared models come from your saved list in Models. Both options use Kilo.','Los modelos de Meta salen de tu catálogo de Kilo. Los modelos compartidos salen de tu lista guardada en Modelos. Ambas opciones usan Kilo.');
  const hasOpenCode = visible.some(row=>row.id === 'opencode');
  $('t3-code-opencode-connection-field').hidden = !hasOpenCode;
  $('t3-code-opencode-connection-help').hidden = !hasOpenCode;
  $('t3-code-opencode-connection-label').textContent = L('OpenCode connection', 'Conexión de OpenCode');
  $('t3-code-opencode-connection-native').textContent = L('Own OpenCode account', 'Cuenta propia de OpenCode');
  $('t3-code-opencode-connection-kilo').textContent = L('Kilo Proxy · shared models', 'Kilo Proxy · modelos compartidos');
  $('t3-code-opencode-connection').value = openCodeConnection(displayed);
  $('t3-code-opencode-connection').disabled = !editable || !displayed?.providers.includes('opencode') || !capabilities.some(row=>row.id === 'opencode');
  $('t3-code-opencode-connection-kilo').disabled = !openCodeKiloSupported;
  $('t3-code-opencode-connection-help').textContent = !openCodeKiloSupported ? L('This T3 build cannot use the Kilo OpenCode connection. Choose your own OpenCode account, disable OpenCode or install a compatible T3 build.', 'Esta compilación de T3 no admite la conexión Kilo de OpenCode. Elige tu cuenta propia de OpenCode, desactiva OpenCode o instala una compilación T3 compatible.') : L('Your own account uses OpenCode’s login and catalog. Kilo uses the models saved in Models through the proxy, with a separate profile. Close T3 Code · Kilo and prepare again after changing this connection.', 'Tu cuenta propia usa la sesión y el catálogo de OpenCode. Kilo usa los modelos guardados en Modelos a través del proxy, con un perfil separado. Cierra T3 Code · Kilo y prepara de nuevo tras cambiar esta conexión.');
  const hasOpenCode2 = openCode2Supported && capabilities.some(row => row.id === 'opencode') || displayed?.openCode2;
  $('t3-code-opencode-version-field').hidden = !hasOpenCode2;
  $('t3-code-opencode-help').hidden = !hasOpenCode2;
  $('t3-code-opencode-version-label').textContent = L('OpenCode version', 'Versión de OpenCode');
  $('t3-code-opencode-version').value = displayed?.openCode2 ? '2' : '1';
  $('t3-code-opencode-version').disabled = !editable || !displayed?.providers.includes('opencode');
  $('t3-code-opencode-version-2').disabled = !openCode2Supported;
  $('t3-code-opencode-help').textContent = !openCode2Supported && displayed?.openCode2 ? L('This T3 build cannot use the saved OpenCode 2 choice. Choose OpenCode 1, disable OpenCode or install a compatible T3 build.','Esta compilación de T3 no admite la selección guardada de OpenCode 2. Elige OpenCode 1, desactiva OpenCode o instala una compilación T3 compatible.') : L('Choose one version for this account. OpenCode 2 requires 2.0.18 or newer; both versions are never started together with the same profile.', 'Elige una versión para esta cuenta. OpenCode 2 requiere 2.0.18 o posterior; nunca se abren ambas versiones a la vez con el mismo perfil.');
  if (openCodeConnection(displayed) === 'native' && (openCode2Supported || !displayed?.openCode2)) $('t3-code-opencode-help').textContent += L(' OpenCode 2 has a separate profile that may need its own login.', ' OpenCode 2 tiene un perfil separado que puede requerir su propio inicio de sesión.');
  $('t3-code-settings-status').textContent = translate(preferencesError || (saving ? L('Saving T3 Code settings…', 'Guardando ajustes de T3 Code…') : loading ? L('Loading T3 Code settings…', 'Cargando ajustes de T3 Code…') : requiresPreparation && !prepared() ? L('Settings saved. Close T3 Code · Kilo and prepare again. Your chats are kept.', 'Ajustes guardados. Cierra T3 Code · Kilo y prepara de nuevo. Tus chats se conservan.') : ''), ctx.language);
  $('t3-code-settings-status').classList.toggle('error', !!preferencesError);
  $('t3-code-settings-retry').hidden = !pendingPreferences || !preferencesError;
  $('t3-code-settings-retry').textContent = L('Retry saving', 'Reintentar guardado'); $('t3-code-settings-retry').disabled = saving || loading || working;
  $('t3-code-settings-reload').textContent = L('Reload settings', 'Cargar ajustes'); $('t3-code-settings-reload').disabled = saving || loading || working;
 }
 function render(next = ctx) {
  ctx = next;
  $('t3-code-intro').textContent = L('A separate T3 Code workspace with the accounts and models you choose. Green KP badges on T3’s provider rail and composer identify Kilo accounts.', 'Un espacio T3 Code separado con las cuentas y modelos que elijas. Las marcas KP verdes en los iconos del selector y el compositor de T3 identifican las cuentas Kilo.');
  const current = currentProfile();
  const codexModels = current?.effectiveCodexModels ?? [], claudeModels = current?.effectiveClaudeModels ?? [];
  const museModels = museUsesKilo(preferences) ? current?.effectiveMuseModels ?? [] : [];
  const openCodeModels = openCodeUsesKilo(preferences) ? current?.effectiveOpenCodeModels ?? [] : [];
  $('t3-code-summary').textContent = `Kilo Proxy · Codex: ${codexModels.length} · Kilo Proxy · Claude: ${claudeModels.length}`+(openCodeUsesKilo(preferences) ? ` · Kilo Proxy · OpenCode: ${openCodeModels.length}` : '')+(museUsesKilo(preferences)?` · Kilo Proxy · Muse Code: ${museModels.length}`:'');
  $('t3-code-models').replaceChildren();
  for (const [name, models] of [['Kilo Proxy · Codex', codexModels], ['Kilo Proxy · Claude', claudeModels], ['Kilo Proxy · OpenCode', openCodeModels],['Kilo Proxy · Muse Code',museModels]]) {
   for (const model of models) { const item = document.createElement('li'); const label = model.name ?? model.displayName; item.textContent = name+' · '+(label ? label+' · ' : '')+model.id; $('t3-code-models').append(item); }
  }
  $('t3-code-library-help').textContent = L('Choose official models or your saved shared library in T3 Code settings below. Existing Codex and Claude accounts keep their CLI login.', 'Elige modelos oficiales o tu biblioteca compartida guardada en los ajustes de T3 Code. Las cuentas existentes de Codex y Claude conservan su sesión CLI.');
  $('t3-code-agents-title').textContent = L('Enabled accounts', 'Cuentas activadas');
  $('t3-code-agents').replaceChildren();
  const accounts = [];
  if (preferences?.codex) accounts.push(L('Codex · Existing Codex CLI login', 'Codex · Sesión existente de Codex CLI'));
  if (preferences?.kiloCodex) accounts.push(L('Kilo Proxy · Codex · Models through the proxy', 'Kilo Proxy · Codex · Modelos a través del proxy'));
  if (preferences?.claude) accounts.push(L('Claude · Existing Claude Code login', 'Claude · Sesión existente de Claude Code'));
  if (preferences?.kiloClaude) accounts.push(L('Kilo Proxy · Claude · Models through the proxy', 'Kilo Proxy · Claude · Modelos a través del proxy'));
  for (const provider of visibleProviders(preferences)) if (preferences?.providers.includes(provider.id)) {
   let label = provider.label;
   if (provider.id === 'opencode') label = (openCodeUsesKilo(preferences) ? 'Kilo Proxy · ' : '')+(preferences.openCode2 ? 'OpenCode 2' : 'OpenCode')+(openCodeUsesKilo(preferences) ? L(' · Shared models through the proxy', ' · Modelos compartidos a través del proxy') : L(' · Own login and catalog', ' · Sesión y catálogo propios'));
   if(provider.id==='muse')label=museUsesKilo(preferences)?(museModelPolicy(preferences)==='official'?L('Kilo Proxy · Muse Code · Meta models through the proxy','Kilo Proxy · Muse Code · Modelos de Meta a través del proxy'):L('Kilo Proxy · Muse Code · Shared models through the proxy','Kilo Proxy · Muse Code · Modelos compartidos a través del proxy')):L('Muse Code · Meta login and catalog','Muse Code · Sesión y catálogo de Meta');
   accounts.push(label+(provider.supported?'':L(' · unavailable in this build',' · no disponible en esta compilación')));
  }
  for (const text of accounts) { const item = document.createElement('li'); item.textContent = text; $('t3-code-agents').append(item); }
  $('t3-code-options').textContent = L('Advanced', 'Avanzado');
  $('t3-code-requirements').textContent = L('Detects installed T3 Code release channels, including alpha, beta and nightly. Preparation checks the installed contract and CLIs required by your enabled accounts. Existing accounts use CLI sessions, not Desktop app logins.', 'Detecta canales instalados de T3 Code, incluidos alpha, beta y nightly. La preparación comprueba el contrato instalado y los CLI que requieren las cuentas activadas. Las cuentas existentes usan sesiones CLI, no los inicios de sesión de las apps Desktop.') + (profile?.version ? L(' Detected: ', ' Detectado: ') + profile.version + '.' : '');
  const chat = profile?.protocolV2 ? L('This T3 build can change agents between turns in the same chat. T3 transfers a summary when switching providers; previous reasoning, tool results and attachments are not transferred.', 'Esta compilación de T3 permite cambiar de agente entre turnos en el mismo chat. T3 transfiere un resumen al cambiar de proveedor; no transfiere el razonamiento anterior, los resultados de herramientas ni los adjuntos.') : L('Start a new chat to switch accounts when this T3 build keeps an existing chat’s agent.', 'Inicia un chat nuevo para cambiar de cuenta si esta compilación de T3 conserva el agente de un chat existente.');
  $('t3-code-compatibility').textContent = chat+' '+L('Check the selected account and model before continuing an older chat.', 'Revisa la cuenta y el modelo elegidos antes de continuar un chat antiguo.');
  $('t3-code-privacy').textContent = L('Your regular T3 Code workspace and chats stay separate and can remain open. Existing accounts use their CLI sessions without copying login data. Close only T3 Code · Kilo, prepare again and reopen it after changing accounts, models or the connection. Your chats are kept.', 'Tu espacio T3 Code habitual y sus chats siguen separados y pueden permanecer abiertos. Las cuentas existentes usan sus sesiones CLI sin copiar datos de inicio de sesión. Cierra solo T3 Code · Kilo, prepara de nuevo y vuelve a abrir tras cambiar cuentas, modelos o la conexión. Tus chats se conservan.');
  $('t3-code-reload').textContent = loading ? L('Loading…', 'Cargando…') : L('Refresh models', 'Actualizar modelos'); $('t3-code-reload').disabled = loading || working || saving;
  $('t3-code-prepare').textContent = working ? L('Preparing…', 'Preparando…') : L('Prepare without opening', 'Preparar sin abrir'); $('t3-code-prepare').disabled = working || loading || !ready() || !connected();
  $('t3-code-install').textContent = L('Get T3 Code ↗', 'Obtener T3 Code ↗');
  $('t3-code-prepared').textContent = prepared() ? L('Your saved T3 accounts and models are prepared.', 'Las cuentas y modelos guardados de T3 están preparados.') : L('Prepare or open T3 Code · Kilo to apply your saved accounts and models.', 'Prepara o abre T3 Code · Kilo para aplicar las cuentas y modelos guardados.');
  $('t3-code-status').textContent = translate(error || (working ? L('Preparing T3 Code accounts…', 'Preparando cuentas de T3 Code…') : profile?.message || ''), ctx.language); $('t3-code-status').classList.toggle('error', !!error);
  renderPreferences(); onChange();
  if (!loaded && !loading) void reload();
 }
 async function reload() {
  if (loading || working || saving) return;
  loading = true; error = ''; preferencesError = ''; const currentGeneration = ++generation, connection = connectionSignature(); render();
  try {
   const [libraryResult, profileResult, preferencesResult] = await Promise.allSettled([api('model-library'), api('clients/t3-code'), api('clients/t3-code/preferences')]);
   if (generation !== currentGeneration) return;
   if (preferencesResult.status !== 'fulfilled') throw preferencesResult.reason;
   if (profileResult.status !== 'fulfilled') throw profileResult.reason;
   acceptPreferences(preferencesResult.value);
   if (libraryResult.status !== 'fulfilled' && requiresSharedLibrary(preferences)) throw libraryResult.reason;
   saved = libraryResult.status === 'fulfilled' ? libraryResult.value : null; profile = profileResult.value; profileConnection = connection; pendingPreferences = null;
  } catch (failure) { if (generation === currentGeneration) { saved = null; profile = null; preferences = null; error = failure.message; preferencesError = failure.message; } }
  finally { if (generation === currentGeneration) { loaded = true; loading = false; render(); } }
 }
 async function savePreferences(next) {
  if (saving || working || loading || !t3CodePreferencesValid(next)) return;
  const snapshot = normalizePreferences(next); saving = true; preferencesError = ''; pendingPreferences = snapshot; const currentGeneration = ++generation;
  if (profile) profile = {...profile, prepared:false}; render();
  try {
   const response = await api('clients/t3-code/preferences', snapshot);
   if (generation !== currentGeneration) return;
   if (preferencesSignature(response?.preferences) !== preferencesSignature(snapshot)) throw new Error(L('T3 Code did not confirm the saved preferences. Reload settings before preparing.', 'T3 Code no confirmó las preferencias guardadas. Carga los ajustes antes de preparar.'));
   acceptPreferences(response); requiresPreparation = true; pendingPreferences = null;
   const connection = connectionSignature();
   const nextProfile = await api('clients/t3-code');
   if (generation === currentGeneration) { profile = nextProfile; profileConnection = connection; }
  } catch (failure) { if (generation === currentGeneration) preferencesError = failure.message; }
  finally { if (generation === currentGeneration) { saving = false; render(); } }
 }
 async function prepare() {
  if (working || loading || !ready() || !connected()) throw new Error(reason());
  const native = nativeOnly();
  const shared = requiresSharedLibrary(preferences);
  const snapshot = native ? null : structuredClone(selectedLibrary(saved));
  const preferencesSnapshot = structuredClone(preferences), connection = connectionSignature();
  const readLibrary = () => native ? Promise.resolve(null) : shared ? api('model-library') : api('model-library').catch(() => null);
  working = true; error = ''; render();
  try {
   const [latest, settings] = await Promise.all([readLibrary(), api('clients/t3-code/preferences')]);
   if (!native && librarySignature(selectedLibrary(latest)) !== librarySignature(snapshot)) { saved = latest; throw new Error(L('The shared models changed. Review the refreshed list and open again.', 'Los modelos compartidos han cambiado. Revisa la lista actualizada y vuelve a abrir.')); }
   if (preferencesSignature(settings.preferences) !== preferencesSignature(preferencesSnapshot)) { acceptPreferences(settings); throw new Error(L('T3 Code settings changed. Review them and open again.', 'Los ajustes de T3 Code han cambiado. Revísalos y vuelve a abrir.')); }
   if (!native && connectionSignature() !== connection) throw new Error(L('The T3 Code connection changed. Review it and open again.', 'La conexión de T3 Code ha cambiado. Revísala y vuelve a abrir.'));
   const nextProfile = await api('clients/t3-code', {preferences:preferencesSnapshot, ...(shared ? {library:snapshot} : {})});
   const [current, currentSettings] = await Promise.all([readLibrary(), api('clients/t3-code/preferences')]);
   if (!native && librarySignature(selectedLibrary(current)) !== librarySignature(snapshot)) { saved = current; throw new Error(L('The shared models changed while preparing. Review the refreshed list and open again.', 'Los modelos compartidos cambiaron durante la preparación. Revisa la lista actualizada y vuelve a abrir.')); }
   if (preferencesSignature(currentSettings.preferences) !== preferencesSignature(preferencesSnapshot) || !native && connectionSignature() !== connection) { acceptPreferences(currentSettings); throw new Error(L('T3 Code settings or connection changed while preparing. Review them and open again.', 'Los ajustes o la conexión de T3 Code cambiaron durante la preparación. Revísalos y vuelve a abrir.')); }
   if (!native && !shared && !t3CodePrepared(current, nextProfile, preferencesSnapshot)) throw new Error(L('T3 Code did not confirm the prepared catalog. Refresh and try again.', 'T3 Code no confirmó el catálogo preparado. Actualiza y vuelve a intentarlo.'));
   profile = nextProfile; profileConnection = connection;
   requiresPreparation = false; return profile;
  } catch (failure) { error = failure.message; throw failure; }
  finally { working = false; render(); }
 }
 for (const key of ['codex','claude','kiloCodex','kiloClaude']) $('t3-code-account-'+key).addEventListener('change', () => { if (preferences) void savePreferences({...structuredClone(preferences), [key]:$('t3-code-account-'+key).checked}); });
 for (const kind of ['codex','claude','muse']) $('t3-code-'+kind+'-models').addEventListener('change', () => { if (preferences) void savePreferences({...structuredClone(preferences), [kind+'Models']:$('t3-code-'+kind+'-models').value}); });
 $('t3-code-muse-connection').addEventListener('change',()=>{if(!preferences)return;const value=$('t3-code-muse-connection').value;if(!['native','kilo'].includes(value)||value==='kilo'&&!museKiloSupported){$('t3-code-muse-connection').value=museConnection(preferences);return;}void savePreferences({...structuredClone(preferences),museConnection:value});});
 $('t3-code-opencode-connection').addEventListener('change', () => { if (!preferences) return; const value = $('t3-code-opencode-connection').value; if (!['native','kilo'].includes(value) || value === 'kilo' && !openCodeKiloSupported) {$('t3-code-opencode-connection').value=openCodeConnection(preferences);return;} void savePreferences({...structuredClone(preferences),openCodeConnection:value}); });
 $('t3-code-opencode-version').addEventListener('change', () => { if (!preferences) return; if ($('t3-code-opencode-version').value === '2' && !openCode2Supported) {$('t3-code-opencode-version').value=preferences.openCode2?'2':'1';return;} void savePreferences({...structuredClone(preferences), openCode2:$('t3-code-opencode-version').value === '2'}); });
 $('t3-code-settings-retry').addEventListener('click', () => { if (pendingPreferences) void savePreferences(pendingPreferences); });
 $('t3-code-settings-reload').addEventListener('click', () => { void reload(); });
 $('t3-code-reload').addEventListener('click', () => { void reload(); });
 $('t3-code-prepare').addEventListener('click', () => { void prepare().catch(() => {}); });
 return {render, reload, savePreferences, preferences:() => preferences && structuredClone(preferences), launchState:() => {
  const current = currentProfile();
  const count = (current?.effectiveCodexModels?.length ?? 0) + (current?.effectiveClaudeModels?.length ?? 0) + (openCodeUsesKilo(preferences) ? current?.effectiveOpenCodeModels?.length ?? 0 : 0) + (museUsesKilo(preferences) ? current?.effectiveMuseModels?.length ?? 0 : 0);
  return {id:'t3-code', nativeOnly:nativeOnly(), count, canPrepareWithoutModelSummary:catalogMode(), ready:false, valid:ready() && connected(), reason:reason(), working:working || loading || saving, fingerprint:signature(), prepare};
 }};
}

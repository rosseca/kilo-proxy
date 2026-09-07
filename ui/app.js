import {createEditorHelper} from './editor-helper.mjs';
import {createXcodeHelper} from './xcode-helper.mjs';
import {claudeCapabilities,claudeEfforts,claudeSelection,claudeSettings} from './claude-helper.mjs';
'use strict';
import {reportedCost, usageCoverage, cacheStats, lastCacheStats} from './usage-helper.mjs';
import {formatTraceJSON} from './activity-helper.mjs';
import {codexCatalog,codexDisplayName,reasoningFor,reasoningLevels} from './codex-catalog.mjs';
import {filterModels, formatPrice, validModelID} from './model-helper.mjs';
import {clientConfig, launchCommand} from './client-config.mjs';
import {chooseLanguage, translate, bindDocument} from './i18n.mjs';
const $ = (id) => document.getElementById(id);
let language = chooseLanguage('', navigator.languages || [navigator.language]);
let activeTrace=null, tracePart='request', traceRequest=0;
let languageChosen = false, noticeContent, toastContent, lockContent;
const t = (source, params) => translate(source, language, params);
const translateDocument = bindDocument(document);
function updateMessages() {
  if (noticeContent) $('notice').textContent = noticeContent();
  if (toastContent) $('toast').textContent = t(toastContent);
  if (lockContent) { $('locked-title').textContent = t(lockContent[0]); $('locked-message').textContent = t(lockContent[1]); }
}
function applyLanguage(next) {
  language = next;
  translateDocument(language);
  $('language').value = language;
  teamSignature = ''; // Recreate the translated placeholder without changing the selected team.
  $('toggle-key').textContent = t($('api-key').type === 'password' ? 'Ver' : 'Ocultar');
  $('toggle-key').setAttribute('aria-label', t($('api-key').type === 'password' ? 'Mostrar API key' : 'Ocultar API key'));
  updateMessages();
  renderModels();
  if (state) render(state); else renderSnippet();
}
let token = location.hash.slice(1);
if (token && /^[a-f0-9]{64}$/.test(token)) {
  sessionStorage.setItem('kilo-local-control', token);
  history.replaceState(null, '', '/');
} else token = sessionStorage.getItem('kilo-local-control') || '';
let state, client = 'generic', busy = false, stopped = false, initialized = false, toastTimer;
let lastAuthStatus, teamSignature = '';
const clientModels = {};
const codexClients = Object.fromEntries(['codex','codex-cli'].map(id=>[id,{models:new Map(),initial:'',setup:null}]));
const isCodexClient = () => ['codex','codex-cli'].includes(client);
const codexSelection = () => codexClients[client] || codexClients.codex;
const cursorModels = new Map();
const multiClients = Object.fromEntries(['opencode','claude'].map(id=>[id,{models:new Map(),initial:'',aliases:{},signature:''}]));
let claudeInstalled=claudeCapabilities(), claudeChecked=false, claudeSetup=null, claudeRendering='';
let cursorSignature = '';
let desktopSignature = '';
function codexSetupSignature(selection=codexSelection()) { return JSON.stringify([state?.baseURL,codexCatalog([...selection.models.values()],selection.initial)]); }
function renderCodexSetup() {
  const cli=client==='codex-cli', setup=codexSelection().setup;
  $('save-codex-catalog').textContent=t($('save-codex-catalog').disabled ? 'Preparando perfil…' : cli ? '1. Preparar Codex CLI' : '1. Preparar Codex GUI');
  const ready=setup?.signature===codexSetupSignature();
  $('codex-setup-status').textContent=ready ? t('Perfil listo en {path}. Copia el arranque para abrir Codex Kilo. Si ya está abierto, ciérralo primero.',{path:setup.path}) : t(setup ? 'Hay cambios sin guardar. Prepara este perfil antes de abrir Codex Kilo.' : 'Prepara este perfil para guardar los modelos y la configuración.');
  $('codex-profile-help').textContent=t('Crea {path} y guarda config.toml y models.json en este ordenador. Actualiza los parámetros de Kilo, conserva los demás ajustes y guarda una copia .bak de cada archivo que cambia.',{path:cli ? '~/.codex-kilo-cli' : '~/.codex-kilo-desktop'});
  $('codex-cli-switch-help').hidden=!cli;
}
function effectiveModel() { if(multiClients[client]?.models.size)return multiClients[client].initial; return isCodexClient() ? codexSelection().initial : $('model').value.trim(); }
let catalog = [], catalogRevision, catalogLoading = false, catalogError = '', catalogFetchedAt = '', catalogRequest = 0;
const descriptions = {
  cursor: ['Cursor: varios modelos, con un requisito de red.', 'Selecciona modelos y conecta el túnel HTTPS desde el helper de Cursor.', 'Guía de conexión de Cursor'],
  generic: ['Dos valores. Ninguna cabecera extra.', 'En tu herramienta, elige un proveedor compatible con OpenAI. Pega la URL y la clave local. El modelo mantiene su ID de Kilo.', 'Conexión compatible con OpenAI'],
  zed: ['Tu agente de Zed, con saldo de empresa.', 'En Agent Settings → LLM Providers, añade un proveedor compatible con OpenAI. Combina este bloque con tus ajustes y guarda la clave local en la interfaz del proveedor.', 'settings.json · combinar con tus ajustes'],
  opencode: ['OpenCode, conectado directamente.', 'Configuración para OpenCode v1. En /connect → Other usa el ID kilo-local y pega la clave local. Combina este bloque con tu configuración.', 'opencode.json · v1'],
  codex: ['Codex Desktop: dos instancias independientes.', 'Selecciona tus modelos y pulsa «Preparar Codex GUI». El helper crea el perfil aislado y guarda la configuración en este ordenador. Después copia el arranque para abrir una segunda instancia gráfica.', 'config.toml · plantilla opcional para otro ordenador'],
  'codex-cli': ['Codex CLI en otra terminal.', 'Selecciona modelos y pulsa «Preparar Codex CLI». El helper crea y actualiza su perfil independiente. Copia el arranque y usa /model en Codex para cambiar de modelo y razonamiento.', 'config.toml · plantilla opcional para otro ordenador'],
  claude: ['Claude Code: un perfil propio para Kilo.', 'Selecciona modelos, pulsa «Preparar Claude Code» y copia el arranque. El helper crea y actualiza ~/.claude-kilo/settings.json sin editar tu perfil habitual.', 'settings.json · plantilla opcional para otro ordenador'],
  xcode: ['Kilo dentro de Xcode.', 'En los ajustes de inteligencia, añade un proveedor de modelos compatible con OpenAI. Introduce estos valores; si pide una cabecera, usa Authorization con el valor Bearer seguido de la clave local.', 'Añadir proveedor de modelos']
};
function notify(message, error = false) {
  noticeContent = typeof message === 'function' ? message : () => t(message);
  $('notice').textContent = noticeContent(); $('notice').classList.toggle('error', error); $('notice').hidden = false;
}
function toast(message) {
  toastContent = message; clearTimeout(toastTimer); $('toast').textContent = t(message); $('toast').hidden = false;
  toastTimer = setTimeout(() => $('toast').hidden = true, 2600);
}
function lock(title, message) {
  lockContent = [title, message]; $('locked-title').textContent = t(title); $('locked-message').textContent = t(message); $('locked').hidden = false;
}
async function api(path, body) {
  const response = await fetch('/api/' + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: {Authorization: 'Bearer ' + token, ...(body === undefined ? {} : {'Content-Type': 'application/json'})},
    ...(body === undefined ? {} : {body: JSON.stringify(body)})
  });
  if (response.status === 401) { stopped = true; lock('Este enlace ha caducado', 'Vuelve a abrir el panel desde el enlace que muestra la aplicación.'); }
  const data = await response.json();
  if (!response.ok) throw new Error(data.error?.message || t('No se pudo completar la operación.'));
  return data;
}
async function copy(text) {
  try { await navigator.clipboard.writeText(text); toast('Copiado al portapapeles'); }
  catch { notify('El navegador ha bloqueado el portapapeles. Selecciona y copia el texto manualmente.', true); }
}
function snippet(reveal = false) {
  if (client === 'claude') return JSON.stringify(claudeSettings(currentClaudeSelection(),currentClaudeCaps(),state?.baseURL || '',reveal ? state?.localKey || '' : 'kl_local_••••••••••••••••'),null,2);
  if (client === 'cursor') return cursorConnectionGuide(reveal);
  if (!state || !validModelID(effectiveModel())) return t('Selecciona un modelo para generar la configuración.');
  const model = effectiveModel();
  const key = reveal ? state.localKey : 'kl_local_••••••••••••••••';
  return clientConfig({client,language,selectedModels:[...(multiClients[client]?.models.values() || [])],aliases:multiClients[client]?.aliases,catalogPath:isCodexClient() && codexSelection().models.size ? 'models.json' : '',baseURL:state.baseURL,key,model,contextWindow:Math.max(1024, Number($('context-window').value) || 200000)});
}
function launch(key) {
  return launchCommand({client,key,shell:$('launch-shell').value,platform:$('desktop-platform').value,appPath:$('desktop-app-path').value.trim(),language,catalog:isCodexClient() && codexSelection().models.size > 0});
}
const editorHelper=createEditorHelper({api,notify,copy,refreshCatalog:loadModels});
const xcodeHelper=createXcodeHelper({api,notify,refreshCatalog:loadModels});
function renderSnippet() {
 const xcodeActive=client==='xcode',editorActive=['opencode','zed'].includes(client);
 $('editor-helper').hidden=!editorActive;
 if(editorActive)editorHelper.render({client,state,catalog,language});
 $('xcode-helper').hidden=!xcodeActive;
 document.querySelector('#client-panel > .client-instructions').hidden=xcodeActive||editorActive;
 document.querySelector('#client-panel > .snippet-stack').hidden=xcodeActive||editorActive;
 if(xcodeActive)xcodeHelper.render({state,catalog,language});
  const isCodex = ['codex','codex-cli'].includes(client);
  $('codex-copy-help').hidden = !isCodex;
  $('codex-copy-title').textContent=t(client==='codex' ? 'Configurar y abrir Codex GUI' : 'Configurar y abrir Codex CLI');
  $('codex-copy-first').textContent=t(client==='codex' ? '1. Selecciona los modelos y pulsa «Preparar Codex GUI». Se crean la carpeta, config.toml y models.json; si ya existen, se actualizan.' : '1. Selecciona los modelos y pulsa «Preparar Codex CLI». Se crean la carpeta, config.toml y models.json; si ya existen, se actualizan.');
  $('copy-launch').textContent = t(isCodex || client==='claude' ? '2. Copiar arranque ↗' : 'Copiar comando ↗');
  renderDesktopModels();
  renderCursorModels();
  renderMultiClients();
  const info = descriptions[client].map(value => t(value));
  $('context-setting').hidden = client !== 'zed';
  $('protocol-note').hidden = !['codex','codex-cli','claude'].includes(client);
  $('protocol-note').textContent = ['codex','codex-cli'].includes(client) ? t('Usa un modelo de Kilo compatible con Responses. El proxy transmite HTTP/SSE; no traduce Chat Completions a Responses.') : t('La URL de Claude no lleva /v1: el SDK lo añade. Cada modelo debe admitir Anthropic Messages. Los alias también se usan en tareas internas; elige modelos compatibles para los tres.');
  $('launch-panel').hidden = !['codex','codex-cli','claude'].includes(client);
  $('launch-shell').hidden = !['codex-cli','claude'].includes(client);
  $('desktop-settings').hidden = client !== 'codex';
  $('launch-preview-help').hidden = !isCodex;
  const revealLaunch = isCodex && $('reveal-launch-key').checked && state;
  $('launch-preview-note').textContent = t(revealLaunch ? 'Comando completo: puedes seleccionar y copiar este texto. Contiene tu clave local.' : 'Vista previa: la clave está oculta. Usa «Copiar arranque» para copiar el comando completo con la clave real, o muéstrala aquí antes de seleccionar el texto.');
  $('launch-code').textContent = launch(revealLaunch ? state.localKey : 'kl_local_••••••••••••••••');
  $('copy-config').disabled = client !== 'cursor' && !validModelID(effectiveModel());
  $('copy-config').textContent = t(client === 'cursor' ? 'Copiar guía ↗' : client==='claude' ? 'Copiar JSON (opcional) ↗' : isCodex ? 'Copiar TOML (opcional) ↗' : 'Copiar configuración ↗');
  $('copy-launch').disabled = $('copy-config').disabled || (client === 'codex' && !$('desktop-app-path').value.trim());
  if (client === 'codex') $('protocol-note').textContent = t('El perfil de Kilo tiene su propio config.toml y sus propios datos de interfaz. No copies auth.json ni cookies del perfil principal. El mecanismo de aislamiento se ha verificado en el código de la app instalada; puede variar entre versiones. Usa un modelo compatible con Responses.');
  $('client-heading').textContent = info[0]; $('client-description').textContent = info[1]; $('snippet-name').textContent = info[2];
  $('snippet-code').textContent = snippet();
}
function render(s) {
  state = s;
  if (!initialized) {
    if (!languageChosen) { language = chooseLanguage(s.language, navigator.languages || [navigator.language]); translateDocument(language); $('language').value = language; }
    $('org-id').value = s.orgId; $('port').value = s.port; $('remember').checked = s.remember; initialized = true;
    if (s.warning) notify(s.warning, true);
  }
  $('toggle-key').textContent = t($('api-key').type === 'password' ? 'Ver' : 'Ocultar');
  $('toggle-key').setAttribute('aria-label', t($('api-key').type === 'password' ? 'Mostrar API key' : 'Ocultar API key'));
  $('version').textContent = 'v' + s.version;
  const pending = ['starting','pending'].includes(s.auth?.status);
  $('load-models').disabled = catalogLoading || pending;
  $('credentials-fields').disabled = s.running || busy || pending;
  $('sso-login').disabled = s.running || busy || pending;
  $('load-teams').disabled = s.running || busy || pending || !s.hasKey;
  $('login-progress').hidden = !s.auth;
  $('login-verification').hidden = s.auth?.status !== 'pending';
  $('cancel-login').hidden = !pending;
  $('login-message').textContent = t(s.auth?.message || '') || (pending ? t('Autoriza este código en la página de Kilo. Esperando a que completes el login…') : t('Login cancelado.'));
  $('login-code').textContent = s.auth?.code || '';
  if (s.auth?.verificationUrl) $('login-link').href = s.auth.verificationUrl; else $('login-link').removeAttribute('href');
  $('account-email').hidden = !s.accountEmail;
  $('account-email').textContent = s.accountEmail ? t('Conectado como {email}', {email:s.accountEmail}) : '';
  const signature = JSON.stringify(s.organizations || []);
  if (signature !== teamSignature) {
    teamSignature = signature; $('team-select').replaceChildren();
    const placeholder = document.createElement('option'); placeholder.value = ''; placeholder.textContent = s.organizations?.length ? t('Selecciona tu equipo') : t('Conecta con Kilo o introduce el ID manualmente'); $('team-select').append(placeholder);
    for (const org of s.organizations || []) { const o=document.createElement('option');o.value=org.id;o.textContent=org.name || org.id;$('team-select').append(o); }
  }
  if (s.auth?.status === 'approved' && lastAuthStatus !== 'approved') $('org-id').value = s.orgId;
  $('team-select').value = $('org-id').value;
  lastAuthStatus = s.auth?.status;
  $('key-state').textContent = s.hasKey ? (s.keySaved ? t('Guardada en el sistema') : t('En esta sesión')) : t('Sin login');
  $('api-key').placeholder = s.hasKey ? t('Clave configurada · pega otra para cambiarla') : t('Pega tu API key de Kilo');
  $('status-pill').classList.toggle('running', s.running);
  $('status-label').textContent = s.running ? t('Proxy activo') : t('Proxy detenido');
  $('start-label').textContent = s.running ? t('Detener proxy') : t('Guardar y arrancar');
  $('start-icon').textContent = s.running ? '■' : '▶';
  $('start-stop').classList.toggle('stop', s.running);
  $('start-stop').disabled = busy || pending; $('check').disabled = busy || pending; $('forget').disabled = busy || s.running;
  $('forget').hidden = !s.hasKey;
  $('base-url').textContent = s.baseURL;
  $('endpoint-status').classList.toggle('live', s.running);
  $('endpoint-status').replaceChildren();
  const dot = document.createElement('span'); dot.className = 'device-dot'; $('endpoint-status').append(dot, s.running ? t('Escuchando · listo para recibir peticiones de tu editor.') : t('Arranca el proxy para aceptar peticiones.'));
  $('connection-foot').textContent = s.running ? t('Detener interrumpe las peticiones que estén en curso.') : t('La clave de Kilo nunca se copia a tu editor.');
  $('request-count').textContent = s.requests; $('active-count').textContent = s.active; $('error-count').textContent = s.failures;
  $('capture-activity').checked=s.captureEnabled;
  renderUsage(s.usage);
  if(activeTrace && !(s.events || []).some(event=>event.id===activeTrace.id)){activeTrace=null;traceRequest++;}
  renderTrace();
  $('empty-activity').hidden = !!s.events?.length; $('activity-table').hidden = !s.events?.length;
  $('event-rows').replaceChildren();
  for (const e of s.events || []) {
    const row = document.createElement('tr');
    const values = [new Date(e.at).toLocaleTimeString(language, {hour: '2-digit', minute: '2-digit', second: '2-digit'}), e.method + ' ' + e.path, e.status, e.usage?.model || '—', reportedCost(e.usage?.costUSD) ?? t('Coste desconocido'), e.duration < 1000 ? e.duration + ' ms' : (e.duration / 1000).toFixed(1) + ' s'];
    values.forEach((value, i) => {
      const cell = document.createElement('td');
      if (i === 2) { const badge = document.createElement('span'); badge.className = 'status-code' + (e.status >= 400 ? ' error' : ''); badge.textContent = value; cell.append(badge); }
      else cell.textContent = value;
      row.append(cell);
    });
    const details=document.createElement('td');
    if(e.hasDetails){const button=document.createElement('button');button.type='button';button.className='text-button';button.textContent=t('Inspeccionar');button.setAttribute('aria-label',t('Inspeccionar petición {id}',{id:e.id}));button.addEventListener('click',()=>void showTrace(e));details.append(button);}
    else details.textContent=t('Sin captura');
    row.append(details);$('event-rows').append(row);
  }
  if (catalogRevision !== s.catalogRevision && !pending) {
    catalogRevision = s.catalogRevision; catalog = []; catalogFetchedAt = ''; void loadModels();
  }
  renderSnippet();
}

function cacheNumber(value){return value===null || value===undefined ? t('Sin dato de caché') : Number(value).toLocaleString(language);}
function cachePercent(value){return value===null ? t('Sin dato de caché') : (value*100).toLocaleString(language,{maximumFractionDigits:1})+'%';}
function renderUsage(usage) {
 const total=usage?.total || {};
 const coverage=usageCoverage(total);
 $('spend-total').textContent=reportedCost(total.costUSD,coverage.priced) ?? t('Coste desconocido');
 $('spend-coverage').textContent=t('Coste recibido en {priced} de {requests} peticiones',coverage);
 $('spend-tokens').textContent=t('{input} entrada total · {output} salida',{input:total.withPrompt>0 ? cacheNumber(total.prompt) : t('Coste desconocido'),output:total.withTokens>0 ? cacheNumber(total.output) : t('Coste desconocido')});
 const cache=cacheStats(total);
 $('cache-read-total').textContent=cacheNumber(cache.read);
 $('cache-write-total').textContent=cacheNumber(cache.write);
 $('cache-ratio-total').textContent=cachePercent(cache.ratio);
 $('cache-coverage').textContent=t('Porcentaje calculado sobre {covered} de {requests} peticiones con datos completos de entrada y caché.',cache);
 $('cache-token-coverage').textContent=t('Caché leída reportada en {read} peticiones; escritura en {write}.',{read:total.withCacheRead||0,write:total.withCacheWrite||0});
 $('spend-partial').textContent=t('{count} peticiones incompletas o con lectura limitada',{count:coverage.incomplete});
 $('usage-sessions').replaceChildren();
 for(const session of usage?.sessions || []){
  const row=document.createElement('tr');
  let label=session.label;
  if(session.source==='unassigned') label=t('Peticiones sin sesión identificada');
  else if(session.source==='overflow') label=t('Otras sesiones');
  else label=label.replace('Codex task',t('Tarea de Codex')).replace('Claude session',t('Sesión de Claude')).replace('Client session',t('Sesión del cliente')).replace('Kilo task',t('Tarea de Kilo'));
  const c=usageCoverage(session),cache=cacheStats(session),last=lastCacheStats(session.lastCache);
  const lastText=last.read===null ? t('Sin dato de caché') : cacheNumber(last.read)+(last.prompt!==null ? ' / '+cacheNumber(last.prompt) : '')+' · '+cachePercent(last.ratio);
  for(const value of [label,session.orgId||'—',session.requests,reportedCost(session.costUSD,c.priced) ?? t('Coste desconocido'),`${c.priced}/${c.requests}`,cacheNumber(cache.read),cacheNumber(cache.write),cachePercent(cache.ratio)+' · '+cache.covered+'/'+cache.requests,lastText]){
   const cell=document.createElement('td');cell.textContent=value;row.append(cell);
  }
  $('usage-sessions').append(row);
 }
 $('usage-session-table').hidden=!usage?.sessions?.length;
}

async function showTrace(event){
 const request=++traceRequest;
 try{const data=await api('activity/'+event.id);if(request!==traceRequest)return;activeTrace={...data,event};tracePart='request';renderTrace();$('activity-inspector').scrollIntoView({block:'nearest',behavior:'smooth'});}
 catch(error){notify(error.message,true);}
}
let traceSignature='';
function renderTrace(){
 $('activity-inspector').hidden=!activeTrace;
 if(!activeTrace){traceSignature='';return;}
 const signature=JSON.stringify([activeTrace.id,tracePart,language,$('trace-format').checked]);if(signature===traceSignature)return;traceSignature=signature;
 const part=activeTrace[tracePart],event=activeTrace.event;
 $('trace-title').textContent='#'+activeTrace.id+' · '+event.method+' '+event.path+' · HTTP '+event.status;
 for(const button of $('trace-tabs').querySelectorAll('[data-part]')){const selected=button.dataset.part===tracePart;button.setAttribute('aria-selected',String(selected));button.tabIndex=selected?0:-1;}
 $('trace-panel').setAttribute('aria-labelledby','trace-tab-'+tracePart);
 $('trace-summary').textContent=t('{bytes} bytes observados',{bytes:part.bytes.toLocaleString(language)})+(part.truncated ? ' · '+t('Cuerpo recortado a 128 KiB') : '')+(tracePart==='upstreamResponse' && activeTrace.upstreamStatus ? ' · HTTP '+activeTrace.upstreamStatus : '');
 $('trace-headers').textContent=Object.entries(part.headers || {}).map(([name,values])=>name+': '+values.join(', ')).join('\n') || t('Sin cabeceras capturadas');
 if(part.headersTruncated)$('trace-summary').textContent+=' · '+t('Cabeceras recortadas a 32 KiB');
 if(activeTrace.error)$('trace-summary').textContent+=' · '+t('Error del proxy')+': '+activeTrace.error;
 let body=part.body;
 if($('trace-format').checked)body=formatTraceJSON(body);
 $('trace-body').textContent=body || t('Sin cuerpo capturado');
}
$('capture-activity').addEventListener('change',event=>{const enabled=event.target.checked;action(()=>api('activity/config',{enabled}));});
$('clear-activity').addEventListener('click',()=>action(async()=>{await api('activity/clear',{});activeTrace=null;traceRequest++;renderTrace();}));
$('close-trace').addEventListener('click',()=>{activeTrace=null;traceRequest++;renderTrace();});
$('trace-format').addEventListener('change',renderTrace);
for(const button of $('trace-tabs').querySelectorAll('[data-part]')){
 button.addEventListener('click',()=>{tracePart=button.dataset.part;renderTrace();});
 button.addEventListener('keydown',event=>{const tabs=[...$('trace-tabs').querySelectorAll('[data-part]')],index=tabs.indexOf(button);const next=event.key==='ArrowRight' ? (index+1)%tabs.length : event.key==='ArrowLeft' ? (index+tabs.length-1)%tabs.length : event.key==='Home' ? 0 : event.key==='End' ? tabs.length-1 : -1;if(next>=0){event.preventDefault();tabs[next].click();tabs[next].focus();}});
}

function renderMultiClients() {
  renderClaudeSetup();
  for (const [id,selection] of Object.entries(multiClients)) {
    $(id+'-models').hidden = client !== id;
    if(id==='claude')continue;
    const candidate=$('model').value.trim();
    $('add-'+id+'-model').disabled = !validModelID(candidate) || selection.models.has(candidate) || selection.models.size>=50;
    const signature=JSON.stringify({models:[...selection.models],initial:selection.initial,aliases:selection.aliases,language});
    if(signature===selection.signature)continue;
    selection.signature=signature;
    $(id+'-selected-models').replaceChildren();
    for(const [modelID,model] of selection.models) {
      const row=document.createElement('li'),label=document.createElement('span'),remove=document.createElement('button');
      label.textContent=(model.name===modelID ? modelID : model.name+' · '+modelID);
      remove.type='button';remove.className='text-button';remove.textContent=t('Quitar');remove.setAttribute('aria-label',t('Quitar {model}',{model:modelID}));
      remove.addEventListener('click',()=>{
        selection.models.delete(modelID);
        if(selection.initial===modelID)selection.initial=selection.models.keys().next().value || '';
        for(const alias of Object.keys(selection.aliases))if(selection.aliases[alias]===modelID)delete selection.aliases[alias];
        renderSnippet();
      });
      row.append(label,remove);$(id+'-selected-models').append(row);
    }
    $(id+'-model-actions').hidden=!selection.models.size;
    const fill=(element,value,placeholder=false)=>{
      element.replaceChildren();
      if(placeholder){const option=document.createElement('option');option.value='';option.textContent=t('Usar modelo inicial');element.append(option);}
      for(const [modelID,model] of selection.models){const option=document.createElement('option');option.value=modelID;option.textContent=model.name || modelID;element.append(option);}
      element.value=value;
    };
    fill($(id+'-default-model'),selection.initial);
    if(id==='claude') {
      for(const alias of ['sonnet','opus','haiku'])fill($('claude-alias-'+alias),selection.aliases[alias] || '',true);
      $('claude-model-commands').textContent=[...selection.models.keys()].map(modelID=>'/model '+modelID).join('\n');
    }
  }
}
for(const [id,selection] of Object.entries(multiClients)) {
  if(id==='claude')continue;
  $('add-'+id+'-model').addEventListener('click',()=>{
    const candidate=$('model').value.trim();if(!validModelID(candidate)||selection.models.size>=50)return;
    selection.models.set(candidate,catalog.find(m=>m.id===candidate) || {id:candidate,name:candidate});
    if(!selection.initial)selection.initial=candidate;
    renderSnippet();
  });
  $(id+'-default-model').addEventListener('change',()=>{selection.initial=$(id+'-default-model').value;renderSnippet();});
}
for(const alias of ['sonnet','opus','haiku'])$('claude-alias-'+alias).addEventListener('change',()=>{
  multiClients.claude.aliases[alias]=$('claude-alias-'+alias).value;renderSnippet();
});

function renderCursorModels() {
  renderCursorConnection();
  $('cursor-guide').hidden = client !== 'cursor';
  const id=$('model').value.trim();
  $('add-cursor-model').disabled = !validModelID(id) || cursorModels.has(id) || cursorModels.size>=50 || ['starting','running'].includes(state?.cursor?.status);
  $('copy-cursor-models').disabled = !cursorModels.size;
  const signature=JSON.stringify({models:[...cursorModels],language,locked:['starting','running'].includes(state?.cursor?.status)});
  if(signature===cursorSignature)return;
  cursorSignature=signature;$('cursor-selected-models').replaceChildren();
  for (const [id,name] of cursorModels) {
    const row=document.createElement('li'),label=document.createElement('span'),remove=document.createElement('button');
    label.textContent=id;remove.type='button';remove.className='text-button';remove.textContent=t('Quitar');remove.setAttribute('aria-label',t('Quitar de Cursor: {model}',{model:id}));
    remove.disabled=['starting','running'].includes(state?.cursor?.status);remove.addEventListener('click',()=>{cursorModels.delete(id);renderSnippet();});row.append(label,remove);$('cursor-selected-models').append(row);
  }
}
$('add-cursor-model').addEventListener('click',()=>{
  const id=$('model').value.trim();if(!validModelID(id)||cursorModels.size>=50||['starting','running'].includes(state?.cursor?.status))return;
  cursorModels.set(id,id);renderSnippet();
});
$('copy-cursor-models').addEventListener('click',()=>cursorModels.size && copy([...cursorModels.keys()].join('\n')));
function renderDesktopModels() {
  renderCodexSetup();
  const active=['codex','codex-cli','claude'].includes(client);
  $('codex-models').hidden=!isCodexClient();
  $('codex-bulk-controls').hidden=!active;
  $('manual-model-field').hidden=active;
  $('model-picker').setAttribute('role',active ? 'group' : 'radiogroup');
  $('model-picker').classList.toggle('codex-picker',active);
  const id=$('codex-manual-id').value.trim();
  $('add-codex-model').disabled=!validModelID(id) || codexSelection().models.has(id) || codexSelection().models.size>=50;
  const signature=JSON.stringify({client,models:[...codexSelection().models],default:codexSelection().initial,language});
  if(signature===desktopSignature)return;
  desktopSignature=signature;
  $('codex-selection-count').textContent=t(codexSelection().models.size===1 ? '1 modelo seleccionado' : '{count} modelos seleccionados',{count:codexSelection().models.size});
  $('codex-catalog-actions').hidden=!codexSelection().models.size;
  $('codex-catalog-preview').textContent=JSON.stringify(codexCatalog([...codexSelection().models.values()],codexSelection().initial),null,2);
  if(isCodexClient() && !document.activeElement?.classList.contains('codex-name-input'))renderModels();
}
function codexRowControls(model,expanded) {
  const id=model.id, current=reasoningFor(model), controls=document.createElement('div');
  controls.className='codex-row-controls';
  const nameLabel=document.createElement('label'), nameInput=document.createElement('input');
  nameLabel.className='codex-name-label';nameLabel.append(document.createTextNode(t('Nombre en Codex')));
  nameInput.type='text';nameInput.className='codex-name-input';nameInput.maxLength=80;nameInput.autocomplete='off';nameInput.spellcheck=false;
  nameInput.value=model.displayName || '';nameInput.placeholder=model.name || id;nameInput.dataset.focus='name:'+id;
  nameInput.setAttribute('aria-label',t('Nombre en Codex: {model}',{model:id}));
  nameInput.addEventListener('input',()=>{
    model.displayName=nameInput.value;
    const entry=nameInput.closest('.codex-model-entry');
    entry.querySelector('.model-option strong').textContent=codexDisplayName(model);
    entry.querySelector('input[name="model-choice"]').setAttribute('aria-label',codexDisplayName(model)+' · '+id);
    // Update the exported catalog without replacing the input/caret while typing.
    renderSnippet();
  });
  nameLabel.append(nameInput);controls.append(nameLabel);
  const initialLabel=document.createElement('label'), initial=document.createElement('select');
  initialLabel.textContent=t('Razonamiento');
  initial.setAttribute('aria-label',t('Razonamiento inicial: {model}',{model:model.name || id}));
  initial.dataset.focus='effort:'+id;
  for(const effort of current.levels){const option=document.createElement('option');option.value=effort;option.textContent=effort;initial.append(option);}
  if(!current.levels.length){const option=document.createElement('option');option.textContent=t('Sin niveles configurados');initial.append(option);}
  else initial.value=current.initial;
  initial.disabled=!current.levels.length;
  initial.addEventListener('change',()=>{model.reasoningLevels=current.levels;model.defaultReasoning=initial.value;renderSnippet();});
  initialLabel.append(initial);controls.append(initialLabel);
  const defaultButton=document.createElement('button');defaultButton.type='button';defaultButton.className='codex-default-button';
  defaultButton.textContent=t(codexSelection().initial===id ? '★ Modelo inicial' : 'Usar al iniciar');
  defaultButton.setAttribute('aria-pressed',String(codexSelection().initial===id));
  defaultButton.setAttribute('aria-label',t('Usar al iniciar: {model}',{model:model.name || id}));defaultButton.dataset.focus='default:'+id;
  defaultButton.addEventListener('click',()=>{codexSelection().initial=id;renderSnippet();});controls.append(defaultButton);
  const levels=document.createElement('details'), summary=document.createElement('summary');
  levels.dataset.model=id;levels.open=expanded.has(id);summary.textContent=t('Personalizar niveles');levels.append(summary);
  const choices=document.createElement('div');choices.className='reasoning-choices';
  for(const effort of reasoningLevels){
    const choice=document.createElement('label'),check=document.createElement('input');check.type='checkbox';check.checked=current.levels.includes(effort);
    check.setAttribute('aria-label',(model.name || id)+' · '+effort);check.dataset.focus='level:'+id+':'+effort;
    check.addEventListener('change',()=>{model.reasoningLevels=reasoningLevels.filter(level=>level===effort ? check.checked : reasoningFor(model).levels.includes(level));model.defaultReasoning=reasoningFor(model).initial;renderSnippet();});
    choice.append(check,document.createTextNode(effort));choices.append(choice);
  }
  levels.append(choices);controls.append(levels);return controls;
}
function codexVisibleModels() {
  if(client==='claude')return claudeVisibleModels();
  // Saved/manual selections stay reachable even when absent from Kilo's catalog.
  const available=new Map(catalog.map(model=>[model.id,model]));
  for(const [id,model] of codexSelection().models)available.set(id,available.has(id) ? {...available.get(id),displayName:model.displayName} : model);
  const selectedOnly=$('codex-selected-only').checked;
  const candidates=[...available.values()].filter(model=>!selectedOnly || codexSelection().models.has(model.id));
  return candidates.filter(model=>filterModels([{...model,name:(model.name || '')+' '+codexDisplayName(model)}],$('model-search').value,false).length).filter(model=>selectedOnly || codexSelection().models.has(model.id) || !$('coding-models').checked || (model.tools===true && model.outputModalities?.includes('text')));
}
$('add-codex-model').addEventListener('click',()=>{
  const id=$('codex-manual-id').value.trim();if(!validModelID(id) || codexSelection().models.has(id) || codexSelection().models.size>=50)return;
  codexSelection().models.set(id,{...(catalog.find(m=>m.id===id) || {id,name:id})});
  if(!codexSelection().initial) codexSelection().initial=id;
  $('model-search').value='';$('codex-selected-only').checked=true;$('codex-manual-id').value='';
  renderModels();renderSnippet();
});
$('select-codex-results').addEventListener('click',()=>{
  if(client==='claude'){const selection=multiClients.claude;for(const model of claudeVisibleModels()){if(selection.models.size>=50)break;selection.models.set(model.id,selection.models.get(model.id) || {...model});if(!selection.initial)selection.initial=model.id;}renderModels();renderSnippet();return;}
  for(const model of codexVisibleModels()){
    if(codexSelection().models.has(model.id))continue;if(codexSelection().models.size>=50)break;codexSelection().models.set(model.id,{...model});if(!codexSelection().initial)codexSelection().initial=model.id;
  }
  renderModels();renderSnippet();
});
$('clear-codex-models').addEventListener('click',()=>{if(client==='claude'){multiClients.claude.models.clear();multiClients.claude.initial='';renderModels();renderSnippet();return;}codexSelection().models.clear();codexSelection().initial='';$('codex-selected-only').checked=false;renderModels();renderSnippet();});
$('load-codex-catalog').addEventListener('click',async()=>{
  const target=client, selection=codexSelection();
  try {
    const data=await api(target+'/catalog');selection.models.clear();
    for(const model of data.catalog.models){selection.models.set(model.slug,{...catalog.find(entry=>entry.id===model.slug),id:model.slug,name:catalog.find(entry=>entry.id===model.slug)?.name || model.slug,displayName:model.display_name || '',contextWindow:model.context_window,inputModalities:model.input_modalities,reasoningLevels:(model.supported_reasoning_levels || []).map(r=>r.effort),defaultReasoning:model.default_reasoning_level});}
    selection.initial=selection.models.has(data.defaultModel) ? data.defaultModel : selection.models.keys().next().value || '';
    if(client===target){$('codex-selected-only').checked=true;$('model-search').value='';}
    renderModels();renderSnippet();toast('Catálogo de Codex cargado');
  }catch(error){notify(error.message,true);}
});
$('suggest-codex-reasoning').addEventListener('click',()=>{for(const model of codexSelection().models.values()){model.reasoningEfforts=catalog.find(entry=>entry.id===model.id)?.reasoningEfforts;delete model.reasoningLevels;delete model.defaultReasoning;}renderSnippet();});
$('save-codex-catalog').addEventListener('click',async()=>{
  const target=client, selection=codexSelection();
  if(!selection.models.size)return;
  const button=$('save-codex-catalog');button.disabled=true;
  button.textContent=t('Preparando perfil…');
  const signature=codexSetupSignature(selection);
  try {
    const result=await api(target+'/catalog',{catalog:codexCatalog([...selection.models.values()],selection.initial)});
    selection.setup={signature,path:result.profileDir};renderCodexSetup();toast(target==='codex-cli' ? 'Perfil de Codex CLI preparado' : 'Perfil de Codex GUI preparado');
  }catch(error){selection.setup=null;renderCodexSetup();notify(error.message,true);}
  finally{button.disabled=false;renderCodexSetup();}
});
$('codex-manual-id').addEventListener('input',renderDesktopModels);
$('codex-selected-only').addEventListener('change',renderModels);
$('download-codex-catalog').addEventListener('click',()=>{
  if(!codexSelection().models.size)return;
  const blob=new Blob([JSON.stringify(codexCatalog([...codexSelection().models.values()],codexSelection().initial),null,2)+'\n'],{type:'application/json'});
  const url=URL.createObjectURL(blob),link=document.createElement('a');link.href=url;link.download='models.json';link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
});
function applyModelContext() {
  const selected = catalog.find(m => m.id === $('model').value.trim());
  if (selected?.contextWindow) $('context-window').value = selected.contextWindow;
}
function renderModels() {
  const matches = ['codex','codex-cli','claude'].includes(client) ? codexVisibleModels() : filterModels(catalog, $('model-search').value, $('coding-models').checked);
  const selectedID = $('model').value.trim();
  const selected = catalog.find(m => m.id === selectedID);
  $('load-models').disabled = catalogLoading || ['starting','pending'].includes(state?.auth?.status);
  $('catalog-status').textContent = catalogLoading ? t('Cargando modelos de Kilo…') : catalogError ? t(catalogError) : t('{shown} de {total} modelos · actualizado {time}', {shown:matches.length,total:catalog.length,time:catalogFetchedAt ? new Date(catalogFetchedAt).toLocaleTimeString(language, {hour:'2-digit',minute:'2-digit'}) : '—'});
  const picker = $('model-picker'), restoreFocus = picker.contains(document.activeElement), scroll = picker.scrollTop;
  const focusKey=document.activeElement?.dataset.focus;
  const caret=document.activeElement?.classList.contains('codex-name-input') ? [document.activeElement.selectionStart,document.activeElement.selectionEnd] : null;
  const expanded=new Set([...picker.querySelectorAll('details[open]')].map(el=>el.dataset.model));
  picker.replaceChildren();
  for (const m of matches) {
    const row = document.createElement('label'), radio = document.createElement('input'), name = document.createElement('span'), prices = document.createElement('span');
    row.className = 'model-option'; radio.type = ['codex','codex-cli','claude'].includes(client) ? 'checkbox' : 'radio'; radio.name = 'model-choice'; radio.value = m.id;radio.dataset.focus='model:'+m.id; radio.checked = isCodexClient() ? codexSelection().models.has(m.id) : client==='claude' ? multiClients.claude.models.has(m.id) : m.id === selectedID; radio.disabled = catalogLoading;
    const shownName=isCodexClient() ? codexDisplayName(m) : client==='claude' ? (m.displayName || m.name || m.id) : m.name;
    radio.setAttribute('aria-label',shownName + ' · ' + m.id);
    const title = document.createElement('strong'), id = document.createElement('small'); title.textContent = shownName; id.textContent = m.id; name.append(title,id);
    prices.className = 'model-option-prices';
    for (const [label,price] of [['Entrada',m.inputPrice],['Salida',m.outputPrice]]) {
      const line = document.createElement('span'); line.textContent = `${t(label)} ${formatPrice(price,language) ?? t('Variable / sin dato')}`; prices.append(line);
    }
    row.append(radio,name,prices);
    if(isCodexClient()){
      const entry=document.createElement('div');entry.className='codex-model-entry';entry.classList.toggle('is-selected',codexSelection().models.has(m.id));entry.append(row);
      if(codexSelection().models.has(m.id))entry.append(codexRowControls(codexSelection().models.get(m.id),expanded));
      picker.append(entry);
    }else if(client==='claude'){const entry=document.createElement('div');entry.className='codex-model-entry';entry.classList.toggle('is-selected',multiClients.claude.models.has(m.id));entry.append(row);if(multiClients.claude.models.has(m.id))entry.append(claudeRowControls(multiClients.claude.models.get(m.id)));picker.append(entry);}
    else picker.append(row);
  }
  if (!matches.length && !catalogLoading) { const empty=document.createElement('p');empty.textContent=t('Sin resultados. Cambia la búsqueda o desactiva el filtro.');picker.append(empty); }
  if(restoreFocus){const control=[...picker.querySelectorAll('[data-focus]')].find(el=>el.dataset.focus===focusKey);control?.focus({preventScroll:true});if(caret && control?.classList.contains('codex-name-input'))control.setSelectionRange(...caret);}
  picker.scrollTop = scroll;
  $('model-hint').textContent = client === 'cursor' ? (language==='en'?'Add models to the Cursor list, then connect below.':'Añade modelos a la lista de Cursor y conecta abajo.') : ['codex','codex-cli'].includes(client)
    ? t('Codex requiere Responses. Busca OpenAI como punto de partida; el catálogo no certifica esa compatibilidad.')
    : client === 'claude' ? t('Claude Code requiere Messages. Busca Anthropic como punto de partida; el catálogo no certifica esa compatibilidad.')
    : t('Selecciona un modelo y el helper completará su ID y la ventana de contexto de Zed.');
  const details = $('model-details'); details.replaceChildren(); details.hidden = ['codex','codex-cli','claude'].includes(client) || !selectedID;
  if(['codex','codex-cli','claude'].includes(client)){$('model-hint').textContent=t('Marca modelos, ajusta el razonamiento en su fila y guarda. La estrella indica el modelo inicial.');return;}
  if (!selected) {
    if (selectedID) details.textContent = t('ID manual o no encontrado en el catálogo actual. Revisa el modelo y su ventana de contexto.');
    return;
  }
  const title = document.createElement('strong'); title.textContent = selected.name; details.append(title);
  const grid = document.createElement('dl');
  const count = n => n ? n.toLocaleString(language) : t('Coste desconocido');
  const flag = value => value === true ? t('Sí') : value === false ? t('No') : t('Coste desconocido');
  for (const [label,value] of [
    ['Entrada · USD / 1M tokens',formatPrice(selected.inputPrice,language) ?? t('Variable / sin dato')],
    ['Salida · USD / 1M tokens',formatPrice(selected.outputPrice,language) ?? t('Variable / sin dato')],
    ['Contexto · tokens',count(selected.contextWindow)],['Salida máxima · tokens',count(selected.maxOutputTokens)],
    ['Herramientas',flag(selected.tools)],['Razonamiento',flag(selected.reasoning)]
  ]) { const dt=document.createElement('dt'),dd=document.createElement('dd');dt.textContent=t(label);dd.textContent=value;grid.append(dt,dd); }
  details.append(grid);
  if (selected.mayTrain) { const note=document.createElement('p');note.textContent=t('Kilo indica que este modelo puede usar tus prompts para entrenamiento.');details.append(note); }
  if (selected.expirationDate) { const note=document.createElement('p');note.textContent=t('Fecha de retirada publicada: {date}',{date:selected.expirationDate});details.append(note); }
}
async function loadModels() {
  const request = ++catalogRequest, revision = catalogRevision;
  catalogLoading = true; catalogError = ''; renderModels();
  try {
    const result = await api('models', {});
    if (request !== catalogRequest || revision !== catalogRevision || result.revision !== catalogRevision) return;
    catalog = result.models; catalogFetchedAt = result.fetchedAt;
  } catch (error) { if (request === catalogRequest) catalogError = error.message; }
  finally { if (request === catalogRequest) { catalogLoading = false; renderModels(); renderSnippet(); } }
}
$('load-models').addEventListener('click', () => void loadModels());
$('model-search').addEventListener('input', renderModels);
$('coding-models').addEventListener('change', renderModels);
$('model-picker').addEventListener('change', event => {
  if(!event.target.matches('input[name="model-choice"]'))return;
  if(client==='claude'){const selection=multiClients.claude,id=event.target.value;if(event.target.checked){if(selection.models.size>=50){event.target.checked=false;return;}selection.models.set(id,{...(catalog.find(m=>m.id===id) || {id,name:id})});if(!selection.initial)selection.initial=id;}else{selection.models.delete(id);if(selection.initial===id)selection.initial=selection.models.keys().next().value || '';for(const alias of Object.keys(selection.aliases))if(selection.aliases[alias]===id)delete selection.aliases[alias];}renderModels();renderSnippet();return;}
  if(isCodexClient()){
    const id=event.target.value;
    if(event.target.checked){if(codexSelection().models.size>=50){event.target.checked=false;toast('Máximo 50 modelos');return;}codexSelection().models.set(id,{...(catalog.find(m=>m.id===id) || {id,name:id})});if(!codexSelection().initial)codexSelection().initial=id;}
    else {codexSelection().models.delete(id);if(codexSelection().initial===id)codexSelection().initial=codexSelection().models.keys().next().value || '';}
  }
  if (!event.target.matches(isCodexClient() ? 'input[type=checkbox]' : 'input[type=radio]')) return;
  $('model').value = event.target.value; applyModelContext(); renderModels(); renderSnippet();
});
async function refresh() { if (!stopped) render(await api('state')); }
async function action(fn) {
  if (busy) return;
  busy = true; if (state) render(state);
  $('notice').hidden = true;
  try { await fn(); await refresh(); }
  catch (error) { notify(error.message || t('No se pudo conectar con la aplicación.'), true); }
  finally { busy = false; if (state) render(state); }
}
async function save() {
  if (!$('connection-form').reportValidity()) throw new Error(t('Completa los campos de conexión.'));
  await api('config', {apiKey: $('api-key').value, orgId: $('org-id').value, port: Number($('port').value), remember: $('remember').checked});
  $('api-key').value = '';
  $('api-key').type = 'password'; $('toggle-key').textContent = t('Ver'); $('toggle-key').setAttribute('aria-pressed', 'false');
}
$('connection-form').addEventListener('submit', e => {
  e.preventDefault(); action(async () => {
    if (state.running) { await api('stop', {}); toast('Proxy detenido'); }
    else { await save(); await api('start', {}); toast('Proxy activo. Ya puedes conectar tu editor.'); }
  });
});
$('check').addEventListener('click', () => action(async () => {
  if (!state.running) await save();
  const result = await api('check', {});
  notify(() => t('{count} modelos en el catálogo.', {count:result.models}) + ' ' + t(result.message));
}));
$('forget').addEventListener('click', () => action(async () => {
  await api('forget', {}); $('api-key').value = ''; $('remember').checked = false; toast('API key olvidada');
}));
$('toggle-key').addEventListener('click', () => {
  const reveal = $('api-key').type === 'password'; $('api-key').type = reveal ? 'text' : 'password';
  $('toggle-key').textContent = reveal ? t('Ocultar') : t('Ver'); $('toggle-key').setAttribute('aria-pressed', String(reveal));
  $('toggle-key').setAttribute('aria-label', reveal ? t('Ocultar API key') : t('Mostrar API key'));
});
document.querySelectorAll('[data-copy]').forEach(button => button.addEventListener('click', () => state && copy(button.dataset.copy === 'url' ? state.baseURL : state.localKey)));
function selectClient(button) {
  clientModels[client] = $('model').value;
  $('model').value = clientModels[button.dataset.client] || '';
  client = button.dataset.client;
  document.querySelectorAll('[data-client]').forEach(b => { b.setAttribute('aria-selected', String(b === button)); b.tabIndex = b === button ? 0 : -1; });
  $('client-panel').setAttribute('aria-labelledby', button.id); applyModelContext(); renderModels(); renderSnippet();
}
document.querySelectorAll('[data-client]').forEach((button, i) => {
  button.addEventListener('click', () => selectClient(button));
  button.addEventListener('keydown', e => {
    const tabs = [...document.querySelectorAll('[data-client]')];
    const next = e.key === 'ArrowRight' ? (i+1)%tabs.length : e.key === 'ArrowLeft' ? (i+tabs.length-1)%tabs.length : e.key === 'Home' ? 0 : e.key === 'End' ? tabs.length-1 : -1;
    if (next >= 0) { e.preventDefault(); selectClient(tabs[next]); tabs[next].focus(); }
  });
});
$('sso-login').addEventListener('click', () => action(async () => {
  const login = await api('auth/start', {}); lastAuthStatus = 'pending';
  window.open(login.verificationUrl, '_blank', 'noopener,noreferrer');
}));
$('cancel-login').addEventListener('click', () => action(async () => { await api('auth/cancel', {}); }));
$('load-teams').addEventListener('click', () => action(async () => {
  const result = await api('auth/organizations', {}); $('org-id').value = result.orgId;
  render(result); if (!result.organizations?.length) notify('Kilo no devuelve organizaciones para esta cuenta.');
}));
$('team-select').addEventListener('change', () => { $('org-id').value = $('team-select').value; });
$('copy-launch').addEventListener('click', () => state && copy(launch(state.localKey)));
$('reveal-launch-key').addEventListener('change',renderSnippet);
$('launch-shell').addEventListener('change', renderSnippet);
$('desktop-platform').addEventListener('change', () => {
  $('desktop-app-path').value = $('desktop-platform').value === 'macos' ? '/Applications/ChatGPT.app' : '';
  $('desktop-app-path').placeholder = $('desktop-platform').value === 'windows' ? 'C:\\…\\Codex.exe' : $('desktop-platform').value === 'macos' ? '/Applications/Codex.app' : '/opt/codex/codex';
  renderSnippet();
});
$('desktop-app-path').addEventListener('input', renderSnippet);
$('copy-config').addEventListener('click', () => state && copy(snippet(true)));
$('model').addEventListener('input', () => { applyModelContext(); renderModels(); renderSnippet(); });
$('context-window').addEventListener('input', renderSnippet);
$('quit').addEventListener('click', () => action(async () => {
  await api('quit', {}); stopped = true;
  lock('Kilo Local está cerrado', 'El proxy se ha detenido. Puedes cerrar esta pestaña. Para volver a usarlo, abre la aplicación.');
}));
document.querySelectorAll('.nav-link').forEach(link => link.addEventListener('click', () => {
  document.querySelectorAll('.nav-link').forEach(l => l.classList.toggle('selected', l === link));
}));
async function poll() {
  if (stopped) return;
  if (!busy) { try { await refresh(); } catch { if (!stopped) notify('Se ha perdido la conexión con la aplicación. Comprueba que Kilo Local siga abierto.', true); } }
  setTimeout(poll, 2500);
}
$('language').addEventListener('change', async () => {
  languageChosen = true;
  applyLanguage($('language').value);
  $('language').disabled = true;
  try { if (!token || stopped) throw new Error('offline'); await api('language', {language}); }
  catch { notify('No se pudo guardar el idioma. La selección solo se mantendrá en esta pestaña.', true); }
  finally { $('language').disabled = false; }
});
if (!token) { stopped = true; lock('Abre Kilo Local', 'Este panel necesita el enlace de acceso de la aplicación. Usa «Abrir panel» en el icono de Kilo Local de la barra de menús o bandeja del sistema.'); }
else poll();

function currentClaudeCaps(){return $('claude-mode').value==='modern' ? claudeCapabilities('2.1.251') : claudeInstalled;}
function currentClaudeSelection(){const s=multiClients.claude;return claudeSelection([...s.models.values()],s.initial,s.aliases,$('claude-mode').value);}
function claudeSetupSignature(){return JSON.stringify([state?.baseURL,state?.localKey,currentClaudeCaps(),currentClaudeSelection()]);}
function renderClaudeSetup(){
 $('claude-mode').options[0].textContent=t('Versión instalada (automático)');
 $('claude-mode').options[1].textContent=t('Claude Code 2.1.251 o posterior');
 const s=multiClients.claude,caps=currentClaudeCaps();
 $('claude-models').hidden=client!=='claude';
 $('claude-model-actions').hidden=!s.models.size;
 $('save-claude-profile').textContent=t($('save-claude-profile').disabled ? 'Preparando perfil…' : '1. Preparar Claude Code');
 $('claude-version-status').textContent=t(claudeInstalled.version ? 'Claude Code {version} · compatible con la configuración de Kilo' : claudeChecked ? 'No se pudo detectar Claude Code. Se usa compatibilidad básica.' : 'Versión pendiente de comprobar.',{version:claudeInstalled.version});
 $('claude-capabilities-note').textContent=t(caps.perModelEffort ? 'Lista y nombres personalizados, con preferencias de razonamiento por modelo compatibles con Claude Code 2.1.251+.' : caps.picker ? 'Lista y nombres personalizados disponibles. El razonamiento inicial es global en esta versión.' : 'Esta versión usa alias y comandos /model. Los nombres cortos se guardan en el helper; el selector personalizado requiere 2.1.242+.');
 $('claude-setup-status').textContent=claudeSetup?.signature===claudeSetupSignature() ? t('Configuración guardada en {path}. Claude Code está listo para arrancar con Kilo.',{path:claudeSetup.path}) : t(claudeSetup ? 'Hay cambios sin guardar. Vuelve a preparar Claude Code.' : 'Pulsa «Preparar Claude Code» para guardar este perfil.');
 if(client==='claude')$('codex-selection-count').textContent=t(s.models.size===1 ? '1 modelo seleccionado' : '{count} modelos seleccionados',{count:s.models.size});
 const id=$('claude-manual-id').value.trim();$('add-claude-model').disabled=!validModelID(id) || s.models.has(id) || s.models.size>=50;
 const signature=JSON.stringify([currentClaudeSelection(),caps,language]);
 if(signature===claudeRendering)return;claudeRendering=signature;
 for(const alias of ['sonnet','opus','haiku']){
  const select=$('claude-alias-'+alias);select.replaceChildren();const option=document.createElement('option');option.value='';option.textContent=t('Usar modelo inicial');select.append(option);
  for(const m of s.models.values()){const o=document.createElement('option');o.value=m.id;o.textContent=m.displayName || m.name || m.id;select.append(o);}select.value=s.aliases[alias] || '';
 }
 $('claude-model-commands').textContent=[...s.models.keys()].map(id=>'/model '+id).join('\n');
 if(client==='claude' && !document.activeElement?.classList.contains('codex-name-input'))renderModels();
}
function claudeVisibleModels(){
 const selection=multiClients.claude.models,available=new Map(catalog.map(m=>[m.id,m]));
 for(const [id,m] of selection)available.set(id,{...available.get(id),...m});
 const query=$('model-search').value.trim().toLowerCase();
 return [...available.values()].filter(m=>(!$('codex-selected-only').checked || selection.has(m.id)) && (m.id+' '+(m.name || '')+' '+(m.displayName || '')).toLowerCase().includes(query) && (selection.has(m.id) || !$('coding-models').checked || m.tools===true && m.outputModalities?.includes('text')));
}
function claudeRowControls(model){
 const s=multiClients.claude,caps=currentClaudeCaps(),controls=document.createElement('div');controls.className='codex-row-controls';
 const nameLabel=document.createElement('label'),name=document.createElement('input');nameLabel.className='codex-name-label';nameLabel.textContent=t('Nombre en Claude');name.type='text';name.className='codex-name-input';name.maxLength=80;name.value=model.displayName || '';name.placeholder=model.name || model.id;name.dataset.focus='claude-name:'+model.id;
 name.addEventListener('input',()=>{model.displayName=name.value;name.closest('.codex-model-entry').querySelector('.model-option strong').textContent=name.value || model.name || model.id;renderSnippet();});nameLabel.append(name);controls.append(nameLabel);
 const label=document.createElement('label'),effort=document.createElement('select');label.textContent=t('Razonamiento');effort.dataset.focus='claude-effort:'+model.id;
 const auto=document.createElement('option');auto.value='';auto.textContent=t('Automático');effort.append(auto);
 for(const value of claudeEfforts(model.id,caps)){const option=document.createElement('option');option.value=value;option.textContent=value;effort.append(option);}
 effort.value=model.effort || '';effort.disabled=!claudeEfforts(model.id,caps).length || !caps.perModelEffort && model.id!==s.initial;
 effort.title=t('Solo modelos reconocidos por Claude. max se elige dentro de la sesión.');
 effort.addEventListener('change',()=>{model.effort=effort.value;renderSnippet();});label.append(effort);controls.append(label);
 const initial=document.createElement('button');initial.type='button';initial.className='codex-default-button';initial.dataset.focus='claude-default:'+model.id;initial.textContent=t(s.initial===model.id ? '★ Modelo inicial' : 'Usar al iniciar');initial.setAttribute('aria-pressed',String(s.initial===model.id));initial.addEventListener('click',()=>{s.initial=model.id;renderSnippet();});controls.append(initial);
 return controls;
}
async function detectClaude(){
 $('detect-claude').disabled=true;
 try{claudeInstalled=await api('claude/info');claudeChecked=true;renderSnippet();}catch(error){notify(error.message,true);}finally{$('detect-claude').disabled=false;}
}
$('detect-claude').addEventListener('click',detectClaude);
$('tab-claude').addEventListener('click',()=>{if(!claudeChecked)void detectClaude();});
$('claude-mode').addEventListener('change',()=>{for(const m of multiClients.claude.models.values())if(m.effort && !claudeEfforts(m.id,currentClaudeCaps()).includes(m.effort))delete m.effort;renderSnippet();});
$('claude-manual-id').addEventListener('input',renderClaudeSetup);
$('add-claude-model').addEventListener('click',()=>{
 const id=$('claude-manual-id').value.trim(),s=multiClients.claude;if(!validModelID(id)||s.models.size>=50)return;
 s.models.set(id,{...(catalog.find(m=>m.id===id) || {id,name:id})});if(!s.initial)s.initial=id;
 $('model-search').value='';$('codex-selected-only').checked=true;$('claude-manual-id').value='';renderModels();renderSnippet();
});
$('save-claude-profile').addEventListener('click',async()=>{
 if(!multiClients.claude.models.size)return;
 const signature=claudeSetupSignature(),button=$('save-claude-profile');button.disabled=true;renderClaudeSetup();
 try{const result=await api('claude/profile',currentClaudeSelection());claudeSetup={signature,path:result.profileDir};toast('Perfil de Claude preparado');}
 catch(error){claudeSetup=null;notify(error.message,true);}finally{button.disabled=false;renderClaudeSetup();}
});
$('load-claude-profile').addEventListener('click',async()=>{
 try{const saved=await api('claude/profile'),s=multiClients.claude;s.models.clear();for(const m of saved.models)s.models.set(m.id,{...catalog.find(entry=>entry.id===m.id),...m,name:m.displayName || m.id});s.initial=saved.initial;s.aliases=saved.aliases || {};$('claude-mode').value=saved.mode;$('codex-selected-only').checked=true;$('model-search').value='';renderModels();renderSnippet();toast('Perfil de Claude cargado');}catch(error){notify(error.message,true);}
});

applyLanguage(language);

function cursorConnectionGuide(reveal=false) {
 const session=state?.cursor;
 if(session?.status!=='running')return clientConfig({client:'cursor',language,models:[...cursorModels.keys()]});
 return `Cursor → Settings → Models

Override OpenAI Base URL: ${session.baseURL}
OpenAI API Key: ${reveal ? session.key : '••••••••••••••••'}

Add Custom Model:
${session.models.join('\n')}

${language==='en' ? 'Enable the OpenAI key and URL override. Add each model ID, then select it in chat. Disable the override to return to Cursor built-in models. Tab and Composer are not provided by Kilo.' : 'Activa la clave OpenAI y la URL alternativa. Añade cada ID y selecciónalo en el chat. Desactiva la URL alternativa para volver a los modelos propios de Cursor. Kilo no proporciona Tab ni Composer.'}`;
}
function renderCursorConnection() {
 const en=language==='en',s=state?.cursor,active=['starting','running'].includes(s?.status);
 if(active){cursorModels.clear();for(const id of s.models)cursorModels.set(id,id)}
 const texts={
 'cursor-heading':en?'Cursor · HTTPS connection':'Cursor · conexión HTTPS',
 'cursor-intro':en?'Select models below, then connect. The app starts a dedicated ngrok tunnel for Cursor’s servers.':'Selecciona modelos y conecta. La app inicia un túnel ngrok propio para los servidores de Cursor.',
 'cursor-setup-title':en?'First time? Set up ngrok once':'¿Primera vez? Configura ngrok una vez',
 'cursor-setup-help':en?'Install ngrok 3 for your OS, create an account, and run the command below with your ngrok authtoken. This is a separate credential from Kilo. Restart Kilo Local after installation.':'Instala ngrok 3 para tu sistema, crea una cuenta y ejecuta el comando con tu authtoken de ngrok. Es una credencial distinta a la de Kilo. Reinicia Kilo Local después de instalarlo.',
 'cursor-privacy':en?'Connecting publishes an authenticated inference endpoint. Prompts and responses travel through Cursor, ngrok and Kilo. Local ngrok inspection is disabled; cloud logging follows your ngrok account settings. The admin panel stays private.':'Conectar publica un endpoint de IA autenticado. Los mensajes y respuestas pasan por Cursor, ngrok y Kilo. La inspección local de ngrok está desactivada; los registros en la nube dependen de tu cuenta ngrok. El panel de administración sigue siendo privado.',
 'cursor-connect':en?'Connect Cursor':'Conectar Cursor','cursor-disconnect':en?'Disconnect / revoke key':'Desconectar / revocar clave',
 'cursor-check':en?'Test public connection (no model charge)':'Probar conexión pública (sin gasto de modelo)',
 'cursor-copy-url':en?'Copy URL':'Copiar URL','cursor-copy-key':en?'Copy Cursor key':'Copiar clave de Cursor',
 'cursor-steps':en?'Paste these values into Cursor → Settings → Models. Enable the OpenAI key and base URL override. Add the model IDs below, then select one in chat. Turn the override off to use Cursor built-in models.':'Pega estos valores en Cursor → Settings → Models. Activa la clave OpenAI y la URL alternativa. Añade los IDs y selecciona uno en el chat. Desactiva la URL alternativa para usar los modelos propios de Cursor.'};
 for(const [id,text] of Object.entries(texts))$(id).textContent=text;
 $('cursor-connect').disabled=active||!state?.running||!cursorModels.size||busy;
 $('cursor-disconnect').disabled=!s||busy;
 $('cursor-check').disabled=s?.status!=='running'||busy;
 $('cursor-status').textContent=s?.error || (s?.status==='running'?(en?'HTTPS tunnel connected. Paste the values below into Cursor.':'Túnel HTTPS conectado. Pega estos valores en Cursor.'):s?.status==='starting'?(en?'Connecting ngrok…':'Conectando ngrok…'):(en?'Disconnected. Start the proxy and select at least one model.':'Desconectado. Inicia el proxy y selecciona al menos un modelo.'));
 $('cursor-connection').hidden=s?.status!=='running';$('cursor-url').value=s?.baseURL||'';$('cursor-key').value=s?.key||'';
}
for(const action of ['connect','disconnect'])$('cursor-'+action).addEventListener('click',async()=>{
 busy=true;renderCursorConnection();
 try {await api('cursor',{action:action==='connect'?'start':'stop',models:[...cursorModels.keys()]});render(await api('state'));}
 catch(error){notify(error.message,true)}finally{busy=false;renderSnippet()}
});
$('cursor-copy-url').addEventListener('click',()=>copy(state?.cursor?.baseURL||''));
$('cursor-copy-key').addEventListener('click',()=>copy(state?.cursor?.key||''));

$('cursor-check').addEventListener('click',async()=>{busy=true;renderCursorConnection();try{await api('cursor',{action:'check'});notify(()=>language==='en'?'Public HTTPS and authentication verified. Now test a chat in Cursor.':'HTTPS público y autenticación verificados. Prueba ahora un chat en Cursor.')}catch(error){notify(error.message,true)}finally{busy=false;renderCursorConnection()}});

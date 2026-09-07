'use strict';
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
const desktopModels = new Map();
const cursorModels = new Map();
const multiClients = Object.fromEntries(['opencode','claude'].map(id=>[id,{models:new Map(),initial:'',aliases:{},signature:''}]));
let cursorSignature = '';
let desktopDefault = '', desktopSignature = '';
function effectiveModel() { if(multiClients[client]?.models.size)return multiClients[client].initial; return client === 'codex' ? desktopDefault : $('model').value.trim(); }
let catalog = [], catalogRevision, catalogLoading = false, catalogError = '', catalogFetchedAt = '', catalogRequest = 0;
const descriptions = {
  cursor: ['Cursor: varios modelos, con un requisito de red.', 'Prepara aquí los IDs que quieres añadir a Cursor. La conexión necesita un gateway HTTPS accesible desde sus servidores; Kilo Local no ofrece ese acceso en esta versión.', 'Guía de Cursor · conexión pendiente'],
  generic: ['Dos valores. Ninguna cabecera extra.', 'En tu herramienta, elige un proveedor compatible con OpenAI. Pega la URL y la clave local. El modelo mantiene su ID de Kilo.', 'Conexión compatible con OpenAI'],
  zed: ['Tu agente de Zed, con saldo de empresa.', 'En Agent Settings → LLM Providers, añade un proveedor compatible con OpenAI. Combina este bloque con tus ajustes y guarda la clave local en la interfaz del proveedor.', 'settings.json · combinar con tus ajustes'],
  opencode: ['OpenCode, conectado directamente.', 'Configuración para OpenCode v1. En /connect → Other usa el ID kilo-local y pega la clave local. Combina este bloque con tu configuración.', 'opencode.json · v1'],
  codex: ['Codex Desktop: dos instancias independientes.', 'Crea la carpeta ~/.codex-kilo-desktop y guarda este bloque en su config.toml (en Windows: %USERPROFILE%\\.codex-kilo-desktop\\config.toml). Selecciona el sistema y la ruta de la app. El comando abre otra instancia gráfica con datos propios; conserva tu Codex habitual abierto.', 'config.toml · perfil de Codex Desktop para Kilo'],
  'codex-cli': ['Codex CLI en otra terminal.', 'Crea ~/.codex-kilo-cli y guarda este bloque en su config.toml (en Windows: %USERPROFILE%\\.codex-kilo-cli\\config.toml). Ejecuta el comando para abrir el CLI con Kilo; usa codex normalmente en otra terminal para tu proveedor habitual.', 'config.toml · perfil de Codex CLI para Kilo'],
  claude: ['Claude Code con tu organización.', 'Combina esta configuración en ~/.claude/settings.json (en Windows, %USERPROFILE%\\.claude\\settings.json). Guarda ahí solo la clave local. Al arrancar Claude, /status debe mostrar la URL del proxy.', 'settings.json · configuración de usuario'],
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
  if (client === 'cursor') return clientConfig({client,language,models:[...cursorModels.keys()]});
  if (!state || !validModelID(effectiveModel())) return t('Selecciona un modelo para generar la configuración.');
  const model = effectiveModel();
  const key = reveal ? state.localKey : 'kl_local_••••••••••••••••';
  return clientConfig({client,language,selectedModels:[...(multiClients[client]?.models.values() || [])],aliases:multiClients[client]?.aliases,catalogPath:client === 'codex' && desktopModels.size ? 'models.json' : '',baseURL:state.baseURL,key,model,contextWindow:Math.max(1024, Number($('context-window').value) || 200000)});
}
function launch(key) {
  return launchCommand({client,key,shell:$('launch-shell').value,platform:$('desktop-platform').value,appPath:$('desktop-app-path').value.trim(),language,catalog:client === 'codex' && desktopModels.size > 0});
}
function renderSnippet() {
  const isCodex = ['codex','codex-cli'].includes(client);
  $('codex-copy-help').hidden = !isCodex;
  $('copy-launch').textContent = t(isCodex ? '2. Copiar arranque ↗' : 'Copiar comando ↗');
  renderDesktopModels();
  renderCursorModels();
  renderMultiClients();
  const info = descriptions[client].map(value => t(value));
  $('context-setting').hidden = client !== 'zed';
  $('protocol-note').hidden = !['codex','codex-cli','claude'].includes(client);
  $('protocol-note').textContent = ['codex','codex-cli'].includes(client) ? t('Usa un modelo de Kilo compatible con Responses. El proxy transmite HTTP/SSE; no traduce Chat Completions a Responses.') : t('La URL de Claude no lleva /v1: el SDK lo añade. Cada modelo debe admitir Anthropic Messages. Los alias también se usan en tareas internas; elige modelos compatibles para los tres.');
  $('launch-panel').hidden = !['codex','codex-cli','claude'].includes(client);
  $('launch-shell').hidden = client !== 'codex-cli';
  $('desktop-settings').hidden = client !== 'codex';
  $('launch-preview-help').hidden = !isCodex;
  const revealLaunch = isCodex && $('reveal-launch-key').checked && state;
  $('launch-preview-note').textContent = t(revealLaunch ? 'Comando completo: puedes seleccionar y copiar este texto. Contiene tu clave local.' : 'Vista previa: la clave está oculta. Usa «Copiar arranque» para copiar el comando completo con la clave real, o muéstrala aquí antes de seleccionar el texto.');
  $('launch-code').textContent = launch(revealLaunch ? state.localKey : 'kl_local_••••••••••••••••');
  $('copy-config').disabled = client !== 'cursor' && !validModelID(effectiveModel());
  $('copy-config').textContent = t(client === 'cursor' ? 'Copiar guía ↗' : isCodex ? '1. Copiar config.toml ↗' : 'Copiar configuración ↗');
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
  if(activeTrace && !(s.events || []).some(event=>event.id===activeTrace.id)){activeTrace=null;traceRequest++;}
  renderTrace();
  $('empty-activity').hidden = !!s.events?.length; $('activity-table').hidden = !s.events?.length;
  $('event-rows').replaceChildren();
  for (const e of s.events || []) {
    const row = document.createElement('tr');
    const values = [new Date(e.at).toLocaleTimeString(language, {hour: '2-digit', minute: '2-digit', second: '2-digit'}), e.method + ' ' + e.path, e.status, e.duration < 1000 ? e.duration + ' ms' : (e.duration / 1000).toFixed(1) + ' s'];
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
  for (const [id,selection] of Object.entries(multiClients)) {
    $(id+'-models').hidden = client !== id;
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
  $('cursor-guide').hidden = client !== 'cursor';
  const id=$('model').value.trim();
  $('add-cursor-model').disabled = !validModelID(id) || cursorModels.has(id) || cursorModels.size>=50;
  $('copy-cursor-models').disabled = !cursorModels.size;
  const signature=JSON.stringify({models:[...cursorModels],language});
  if(signature===cursorSignature)return;
  cursorSignature=signature;$('cursor-selected-models').replaceChildren();
  for (const [id,name] of cursorModels) {
    const row=document.createElement('li'),label=document.createElement('span'),remove=document.createElement('button');
    label.textContent=id;remove.type='button';remove.className='text-button';remove.textContent=t('Quitar');remove.setAttribute('aria-label',t('Quitar de Cursor: {model}',{model:id}));
    remove.addEventListener('click',()=>{cursorModels.delete(id);renderSnippet();});row.append(label,remove);$('cursor-selected-models').append(row);
  }
}
$('add-cursor-model').addEventListener('click',()=>{
  const id=$('model').value.trim();if(!validModelID(id)||cursorModels.size>=50)return;
  cursorModels.set(id,id);renderSnippet();
});
$('copy-cursor-models').addEventListener('click',()=>cursorModels.size && copy([...cursorModels.keys()].join('\n')));
function renderDesktopModels() {
  const active=client==='codex';
  $('codex-models').hidden=!active;
  $('codex-bulk-controls').hidden=!active;
  $('manual-model-field').hidden=active;
  $('model-picker').setAttribute('role',active ? 'group' : 'radiogroup');
  $('model-picker').classList.toggle('codex-picker',active);
  const id=$('codex-manual-id').value.trim();
  $('add-codex-model').disabled=!validModelID(id) || desktopModels.has(id) || desktopModels.size>=50;
  const signature=JSON.stringify({models:[...desktopModels],default:desktopDefault,language});
  if(signature===desktopSignature)return;
  desktopSignature=signature;
  $('codex-selection-count').textContent=t(desktopModels.size===1 ? '1 modelo seleccionado' : '{count} modelos seleccionados',{count:desktopModels.size});
  $('codex-catalog-actions').hidden=!desktopModels.size;
  $('codex-catalog-preview').textContent=JSON.stringify(codexCatalog([...desktopModels.values()],desktopDefault),null,2);
  if(active && !document.activeElement?.classList.contains('codex-name-input'))renderModels();
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
  defaultButton.textContent=t(desktopDefault===id ? '★ Modelo inicial' : 'Usar al iniciar');
  defaultButton.setAttribute('aria-pressed',String(desktopDefault===id));
  defaultButton.setAttribute('aria-label',t('Usar al iniciar: {model}',{model:model.name || id}));defaultButton.dataset.focus='default:'+id;
  defaultButton.addEventListener('click',()=>{desktopDefault=id;renderSnippet();});controls.append(defaultButton);
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
  // Saved/manual selections stay reachable even when absent from Kilo's catalog.
  const available=new Map(catalog.map(model=>[model.id,model]));
  for(const [id,model] of desktopModels)available.set(id,available.has(id) ? {...available.get(id),displayName:model.displayName} : model);
  const selectedOnly=$('codex-selected-only').checked;
  const candidates=[...available.values()].filter(model=>!selectedOnly || desktopModels.has(model.id));
  return candidates.filter(model=>filterModels([{...model,name:(model.name || '')+' '+codexDisplayName(model)}],$('model-search').value,false).length).filter(model=>selectedOnly || desktopModels.has(model.id) || !$('coding-models').checked || (model.tools===true && model.outputModalities?.includes('text')));
}
$('add-codex-model').addEventListener('click',()=>{
  const id=$('codex-manual-id').value.trim();if(!validModelID(id) || desktopModels.has(id) || desktopModels.size>=50)return;
  desktopModels.set(id,{...(catalog.find(m=>m.id===id) || {id,name:id})});
  if(!desktopDefault) desktopDefault=id;
  $('model-search').value='';$('codex-selected-only').checked=true;$('codex-manual-id').value='';
  renderModels();renderSnippet();
});
$('select-codex-results').addEventListener('click',()=>{
  for(const model of codexVisibleModels()){
    if(desktopModels.has(model.id))continue;if(desktopModels.size>=50)break;desktopModels.set(model.id,{...model});if(!desktopDefault)desktopDefault=model.id;
  }
  renderModels();renderSnippet();
});
$('clear-codex-models').addEventListener('click',()=>{desktopModels.clear();desktopDefault='';$('codex-selected-only').checked=false;renderModels();renderSnippet();});
$('load-codex-catalog').addEventListener('click',async()=>{
  try {
    const data=await api('codex/catalog');desktopModels.clear();
    for(const model of data.catalog.models){desktopModels.set(model.slug,{...catalog.find(entry=>entry.id===model.slug),id:model.slug,name:catalog.find(entry=>entry.id===model.slug)?.name || model.slug,displayName:model.display_name || '',contextWindow:model.context_window,inputModalities:model.input_modalities,reasoningLevels:(model.supported_reasoning_levels || []).map(r=>r.effort),defaultReasoning:model.default_reasoning_level});}
    desktopDefault=desktopModels.has(data.defaultModel) ? data.defaultModel : desktopModels.keys().next().value || '';
    $('codex-selected-only').checked=true;$('model-search').value='';
    renderModels();renderSnippet();toast('Catálogo de Codex cargado');
  }catch(error){notify(error.message,true);}
});
$('suggest-codex-reasoning').addEventListener('click',()=>{for(const model of desktopModels.values()){model.reasoningEfforts=catalog.find(entry=>entry.id===model.id)?.reasoningEfforts;delete model.reasoningLevels;delete model.defaultReasoning;}renderSnippet();});
$('save-codex-catalog').addEventListener('click',async()=>{
  if(!desktopModels.size)return;
  const button=$('save-codex-catalog');button.disabled=true;
  try {await api('codex/catalog',{catalog:codexCatalog([...desktopModels.values()],desktopDefault)});toast('Catálogo guardado. Reinicia Codex Kilo.');}
  catch(error){notify(error.message,true);}finally{button.disabled=false;}
});
$('codex-manual-id').addEventListener('input',renderDesktopModels);
$('codex-selected-only').addEventListener('change',renderModels);
$('download-codex-catalog').addEventListener('click',()=>{
  if(!desktopModels.size)return;
  const blob=new Blob([JSON.stringify(codexCatalog([...desktopModels.values()],desktopDefault),null,2)+'\n'],{type:'application/json'});
  const url=URL.createObjectURL(blob),link=document.createElement('a');link.href=url;link.download='models.json';link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
});
function applyModelContext() {
  const selected = catalog.find(m => m.id === $('model').value.trim());
  if (selected?.contextWindow) $('context-window').value = selected.contextWindow;
}
function renderModels() {
  const matches = client==='codex' ? codexVisibleModels() : filterModels(catalog, $('model-search').value, $('coding-models').checked);
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
    row.className = 'model-option'; radio.type = client === 'codex' ? 'checkbox' : 'radio'; radio.name = 'model-choice'; radio.value = m.id;radio.dataset.focus='model:'+m.id; radio.checked = client === 'codex' ? desktopModels.has(m.id) : m.id === selectedID; radio.disabled = catalogLoading;
    const shownName=client==='codex' ? codexDisplayName(m) : m.name;
    radio.setAttribute('aria-label',shownName + ' · ' + m.id);
    const title = document.createElement('strong'), id = document.createElement('small'); title.textContent = shownName; id.textContent = m.id; name.append(title,id);
    prices.className = 'model-option-prices';
    for (const [label,price] of [['Entrada',m.inputPrice],['Salida',m.outputPrice]]) {
      const line = document.createElement('span'); line.textContent = `${t(label)} ${formatPrice(price,language) ?? t('Variable / sin dato')}`; prices.append(line);
    }
    row.append(radio,name,prices);
    if(client==='codex'){
      const entry=document.createElement('div');entry.className='codex-model-entry';entry.classList.toggle('is-selected',desktopModels.has(m.id));entry.append(row);
      if(desktopModels.has(m.id))entry.append(codexRowControls(desktopModels.get(m.id),expanded));
      picker.append(entry);
    }else picker.append(row);
  }
  if (!matches.length && !catalogLoading) { const empty=document.createElement('p');empty.textContent=t('Sin resultados. Cambia la búsqueda o desactiva el filtro.');picker.append(empty); }
  if(restoreFocus){const control=[...picker.querySelectorAll('[data-focus]')].find(el=>el.dataset.focus===focusKey);control?.focus({preventScroll:true});if(caret && control?.classList.contains('codex-name-input'))control.setSelectionRange(...caret);}
  picker.scrollTop = scroll;
  $('model-hint').textContent = client === 'cursor' ? t('Añade varios modelos a tu lista de Cursor. La lista no establece una conexión con el proxy.') : ['codex','codex-cli'].includes(client)
    ? t('Codex requiere Responses. Busca OpenAI como punto de partida; el catálogo no certifica esa compatibilidad.')
    : client === 'claude' ? t('Claude Code requiere Messages. Busca Anthropic como punto de partida; el catálogo no certifica esa compatibilidad.')
    : t('Selecciona un modelo y el helper completará su ID y la ventana de contexto de Zed.');
  const details = $('model-details'); details.replaceChildren(); details.hidden = client==='codex' || !selectedID;
  if(client==='codex'){$('model-hint').textContent=t('Marca modelos, ajusta el razonamiento en su fila y guarda. La estrella indica el modelo inicial.');return;}
  if (!selected) {
    if (selectedID) details.textContent = t('ID manual o no encontrado en el catálogo actual. Revisa el modelo y su ventana de contexto.');
    return;
  }
  const title = document.createElement('strong'); title.textContent = selected.name; details.append(title);
  const grid = document.createElement('dl');
  const count = n => n ? n.toLocaleString(language) : t('Sin dato');
  const flag = value => value === true ? t('Sí') : value === false ? t('No') : t('Sin dato');
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
  if(client==='codex'){
    const id=event.target.value;
    if(event.target.checked){if(desktopModels.size>=50){event.target.checked=false;toast('Máximo 50 modelos');return;}desktopModels.set(id,{...(catalog.find(m=>m.id===id) || {id,name:id})});if(!desktopDefault)desktopDefault=id;}
    else {desktopModels.delete(id);if(desktopDefault===id)desktopDefault=desktopModels.keys().next().value || '';}
  }
  if (!event.target.matches(client==='codex' ? 'input[type=checkbox]' : 'input[type=radio]')) return;
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
applyLanguage(language);
if (!token) { stopped = true; lock('Abre Kilo Local', 'Este panel necesita el enlace de acceso de la aplicación. Usa «Abrir panel» en el icono de Kilo Local de la barra de menús o bandeja del sistema.'); }
else poll();

import {contextControls,contextModel,syncContextModels,contextError,contextPreview} from './context-policy.mjs';
import {renderAccountUsage} from './account-usage.mjs';
import {renderUpdates} from './update-helper.mjs';
import {createOpenDesignHelper} from './open-design-helper.mjs';
import {createEditorHelper} from './editor-helper.mjs';
import {configureDesktop, writeClipboard, openExternal, bindDesktopLinks} from './desktop-helper.mjs';
import {createXcodeHelper} from './xcode-helper.mjs';
import {claudeCapabilities,claudeEfforts,claudeSelection,claudeSettings} from './claude-helper.mjs';
'use strict';
import {reportedCost, reportedSpend, inferenceCostNote, costSourceLabel, cacheStats, lastCacheStats} from './usage-helper.mjs';
import {formatTraceJSON} from './activity-helper.mjs';
import {codexCatalog,codexDisplayName,reasoningFor,reasoningLevels} from './codex-catalog.mjs';
import {filterModels, formatPrice, modelPriceDetails, validModelID, sortModels, configureModelSort, setModelSort, mergeModelSelection, filterModelLab, configureModelLab, setModelLab} from './model-helper.mjs';
import {imageGenerationModels,imageGenerationSelection,imageGenerationValid,codexImageMCPConfig} from './model-helper.mjs';
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
let state, client = 'generic', busy = false, stopped = false, initialized = false, toastTimer, imageTransportPending = null;
let imageDependencyPrompt={};
let updateCheckPending = false, updateRequestError = false;
let updateCheckRevision = 0;
let updateFailedCheck = '';
try { const saved=JSON.parse(sessionStorage.getItem('kilo-cloudflare-prompt')||'{}');if(saved&&typeof saved==='object')imageDependencyPrompt={dismissed:saved.dismissed===true,mode:saved.mode,running:saved.running===true}; } catch {}
let lastAuthStatus, teamSignature = '';
const clientModels = {};
const codexClients = Object.fromEntries(['codex','codex-cli'].map(id=>[id,{models:new Map(),initial:'',setup:null,preparing:false,imageGeneration:null,imageGenerationBaseline:null,queueMode:'queue'}]));
const isCodexClient = () => ['codex','codex-cli'].includes(client);
const codexSelection = () => codexClients[client] || codexClients.codex;
const multiClients = Object.fromEntries(['opencode','claude'].map(id=>[id,{models:new Map(),initial:'',aliases:{},signature:''}]));
let claudeInstalled=claudeCapabilities(), claudeChecked=false, claudeSetup=null, claudeRendering='',claudePreparing=false,claudeDetecting=false;
let launchInfo=null,launchDetecting=false,launchDetected=false,launchBusy=false,launchMessage='',launchError=false,launchDirectoryEdited=false;
let desktopSignature = '';
function codexSetupSignature(selection=codexSelection()) { return JSON.stringify([state?.baseURL,contextPreview(()=>codexCatalog([...selection.models.values()],selection.initial)),selection.imageGeneration,selection===codexClients.codex?selection.queueMode:'']); }
function renderCodexSetup() {
  const cli=client==='codex-cli', setup=codexSelection().setup;
  $('save-codex-catalog').disabled=codexSelection().preparing||!!contextError(codexSelection().models)||!imageGenerationValid(codexSelection().imageGeneration,catalog);
  $('save-codex-catalog').textContent=t(codexSelection().preparing ? 'Preparando perfil…' : cli ? '1. Preparar Codex CLI' : '1. Preparar Codex GUI');
  const ready=setup?.signature===codexSetupSignature();
  $('codex-setup-status').textContent=contextError(codexSelection().models)|| (ready ? t('Perfil listo en {path}. Ábrelo con el botón superior. El comando de arranque es opcional.',{path:setup.path}) : t(setup ? 'Hay cambios sin guardar. Se guardarán antes de abrir.' : 'Selecciona modelos y abre el cliente. Su perfil se prepara automáticamente.'));
  $('codex-profile-help').textContent=t('Crea {path} y guarda config.toml y models.json en este ordenador. Actualiza los parámetros de Kilo, conserva los demás ajustes y guarda una copia .bak de cada archivo que cambia.',{path:cli ? '~/.codex-kilo-cli' : '~/.codex-kilo-desktop'});
  $('codex-cli-switch-help').hidden=!cli;
}
function renderCodexImages() {
  const L=(en,es)=>language==='en'?en:es,selection=codexSelection(),images=selection.imageGeneration;
  const models=imageGenerationModels(catalog),enabled=images?.enabled===true,chosen=images?.model||'';
  $('codex-image-generation').hidden=!isCodexClient();
  $('codex-image-eyebrow').textContent=L('OPTIONAL TOOL','HERRAMIENTA OPCIONAL');
  $('codex-image-title').textContent=L('Image generation','Generación de imágenes');
  $('codex-image-enable-label').textContent=L('Enable','Activar');
  $('codex-image-enabled').setAttribute('aria-label',L('Enable image generation','Activar generación de imágenes'));
  $('codex-image-enabled').checked=enabled;$('codex-image-enabled').disabled=images===null;
  $('codex-image-intro').textContent=L('Give Codex an image tool with its own model. Your coding models stay independent.','Añade una herramienta de imágenes a Codex con su propio modelo. Los modelos de programación son independientes.');
  $('codex-image-options').hidden=!enabled;
  $('codex-image-model-label').textContent=L('Image model','Modelo de imágenes');
  const picker=$('codex-image-model'),signature=JSON.stringify([language,chosen,models.map(model=>[model.id,model.name])]);
  if(picker.dataset.imageOptions!==signature){
    picker.replaceChildren();
    const add=(value,label,disabled=false)=>{const option=document.createElement('option');option.value=value;option.textContent=label;option.disabled=disabled;picker.append(option);};
    add('',L('Choose an image model','Elige un modelo de imágenes'));
    for(const model of models)add(model.id,model.name||model.id);
    if(chosen&&!models.some(model=>model.id===chosen))add(chosen,chosen+L(' · not in current catalog',' · fuera del catálogo actual'),true);
    picker.value=chosen;picker.dataset.imageOptions=signature;
  }
  picker.disabled=!enabled;
  $('codex-image-refresh').textContent=catalogLoading?L('Refreshing…','Actualizando…'):L('Refresh image models','Actualizar modelos de imágenes');
  $('codex-image-refresh').disabled=catalogLoading;
  $('codex-image-status').textContent=!enabled?L('Optional. Enable it when you want Codex to create images.','Opcional. Actívala cuando quieras que Codex cree imágenes.'):!chosen?L('Choose an image model before preparing or launching Codex.','Elige un modelo de imágenes antes de preparar o abrir Codex.'):!models.some(model=>model.id===chosen)?L('The saved image model is unavailable in the current catalog. Refresh, choose another model, or disable this tool.','El modelo guardado no está disponible en el catálogo actual. Actualiza, elige otro modelo o desactiva la herramienta.'):chosen;
  $('codex-image-status').classList.toggle('image-generation-warning',enabled&&!models.some(model=>model.id===chosen));
  $('codex-image-billing').textContent=L("Uses your configured Kilo organization. Provider or gateway charges depend on its billing setup. Editing currently supports images created with this tool.",'Usa tu organización de Kilo configurada. Los cargos del proveedor o gateway dependen de su facturación. La edición admite por ahora imágenes creadas con esta herramienta.');
  $('codex-image-save-help').textContent=L('Saved with Prepare or Launch. This setting is shared by Codex GUI and CLI. Restart Codex after preparing to load the tool.','Se guarda al Preparar o Abrir. El ajuste se comparte entre Codex GUI y CLI. Reinicia Codex después de preparar para cargar la herramienta.');
}
function renderCodexQueueMode() {
  const L=(en,es)=>language==='en'?en:es,desktop=client==='codex',selection=codexSelection();
  $('codex-queue-mode-settings').hidden=!desktop;
  if(!desktop)return;
  const select=$('codex-queue-mode');
  select.querySelector('option[value="queue"]').textContent=L('Queue · wait for the next turn','Queue · esperar al siguiente turno');
  select.querySelector('option[value="steer"]').textContent=L('Steer · add them to the current turn','Steer · incorporarlos al turno actual');
  select.value=selection.queueMode==='steer'?'steer':'queue';
}
function acceptCodexImageSettings(saved) {
  if(!saved)return;
  for(const selection of Object.values(codexClients)){
    if(selection.imageGeneration===null||JSON.stringify(selection.imageGeneration)===JSON.stringify(selection.imageGenerationBaseline))selection.imageGeneration={...saved};
    selection.imageGenerationBaseline={...saved};
  }
}
$('codex-image-enabled').addEventListener('change',()=>{
  const selection=codexSelection();selection.imageGeneration=imageGenerationSelection({...(selection.imageGeneration||{model:''}),enabled:$('codex-image-enabled').checked});renderSnippet();
});
$('codex-image-model').addEventListener('change',()=>{
  const selection=codexSelection();selection.imageGeneration={...(selection.imageGeneration||{enabled:false}),model:$('codex-image-model').value};renderSnippet();
});
$('codex-image-refresh').addEventListener('click',()=>{void loadModels();});
$('codex-queue-mode').addEventListener('change',()=>{codexSelection().queueMode=$('codex-queue-mode').value==='steer'?'steer':'queue';renderSnippet();});
function effectiveModel() { if(multiClients[client]?.models.size)return multiClients[client].initial; return isCodexClient() ? codexSelection().initial : $('model').value.trim(); }
let catalog = [], catalogRevision, catalogLoading = false, catalogError = '', catalogFetchedAt = '', catalogRequest = 0;
const descriptions = {
  generic: ['Dos valores. Ninguna cabecera extra.', 'En tu herramienta, elige un proveedor compatible con OpenAI. Pega la URL y la clave local. El modelo mantiene su ID de Kilo.', 'Conexión compatible con OpenAI'],
  zed: ['Tu agente de Zed, con saldo de empresa.', 'En Agent Settings → LLM Providers, añade un proveedor compatible con OpenAI. Combina este bloque con tus ajustes y guarda la clave local en la interfaz del proveedor.', 'settings.json · combinar con tus ajustes'],
  'open-design': ['Open Design', '', ''],
  omp: ['Oh My Pi', '', ''],
  'claude-desktop': ['Claude Desktop', '', ''],
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
async function api(path, body, method = body === undefined ? 'GET' : 'POST') {
  const response = await fetch('/api/' + path, {
    method,
    headers: {Authorization: 'Bearer ' + token, ...(body === undefined ? {} : {'Content-Type': 'application/json'})},
    ...(body === undefined ? {} : {body: JSON.stringify(body)})
  });
  if (response.status === 401) { stopped = true; lock('Este enlace ha caducado', 'Vuelve a abrir el panel desde el enlace que muestra la aplicación.'); }
  const data = await response.json();
  if (!response.ok) throw new Error(data.error?.message || t('No se pudo completar la operación.'));
  return data;
}
async function copy(text) {
  try { await writeClipboard(text); toast('Copiado al portapapeles'); }
  catch { notify('El navegador ha bloqueado el portapapeles. Selecciona y copia el texto manualmente.', true); }
}
function snippet(reveal = false) {
  if (client === 'claude') {try{return JSON.stringify(claudeSettings(currentClaudeSelection(),currentClaudeCaps(),state?.baseURL || '',reveal ? state?.localKey || '' : 'kl_local_••••••••••••••••'),null,2);}catch(error){return error.message;}}
  if (!state || !validModelID(effectiveModel())) return t('Selecciona un modelo para generar la configuración.');
  const model = effectiveModel();
  const key = reveal ? state.localKey : 'kl_local_••••••••••••••••';
  const config=clientConfig({client,language,selectedModels:[...(multiClients[client]?.models.values() || [])],aliases:multiClients[client]?.aliases,catalogPath:isCodexClient() && codexSelection().models.size ? 'models.json' : '',baseURL:state.baseURL,zedBaseURL:state.zedBaseURL,key,model,queueMode:codexSelection().queueMode,contextWindow:Math.max(1024, Number($('context-window').value) || 200000)});
  return isCodexClient()?codexImageMCPConfig(config,codexSelection().imageGeneration,state.baseURL):config;
}
function launch(key) {
  return launchCommand({client,key,shell:$('launch-shell').value,platform:$('desktop-platform').value,appPath:$('desktop-app-path').value.trim(),language,catalog:isCodexClient() && codexSelection().models.size > 0});
}
const openDesignHelper=createOpenDesignHelper({api,refreshCatalog:loadModels,onChange:renderClientLaunch});
const editorHelper=createEditorHelper({api,notify,copy,refreshCatalog:loadModels,onChange:renderClientLaunch});
const xcodeHelper=createXcodeHelper({api,notify,refreshCatalog:loadModels,onChange:renderClientLaunch});
function clientLaunchSelection(){
 if(isCodexClient()){
  const selection=codexSelection(),fingerprint=codexSetupSignature(selection);
  return {id:client,count:selection.models.size,ready:selection.setup?.signature===fingerprint,fingerprint,working:selection.preparing,valid:!contextError(selection.models)&&imageGenerationValid(selection.imageGeneration,catalog),reason:contextError(selection.models),prepare:prepareCodex};
 }
 if(client==='claude')return {id:client,count:multiClients.claude.models.size,valid:!contextError(multiClients.claude.models),reason:contextError(multiClients.claude.models),ready:claudeSetup?.signature===claudeSetupSignature(),fingerprint:claudeSetupSignature(),working:claudePreparing||claudeDetecting,prepare:prepareClaude};
 if(client==='open-design')return openDesignHelper.launchState();
 if(['opencode','zed','omp','claude-desktop'].includes(client))return editorHelper.launchState();
 if(client==='xcode')return xcodeHelper.launchState();
 return null;
}
function renderClientLaunch(){
 const L=(en,es)=>language==='en'?en:es,selection=clientLaunchSelection();
 $('client-launch-bar').hidden=!selection;
 if(!selection)return;
 if(!launchDetected&&!launchDetecting)void detectLaunchClients();
 const detected=launchInfo?.clients?.[selection.id],custom=selection.id==='codex'&&$('client-launch-app').value.trim();
 const cli=['codex-cli','claude','opencode','omp'].includes(selection.id),available=!!detected?.available||!!custom,terminal=detected?.kind==='terminal'||cli;
 const name=detected?.name||({'codex':'Codex Desktop','codex-cli':'Codex CLI',claude:'Claude Code','claude-desktop':'Claude Desktop',opencode:'OpenCode',omp:'Oh My Pi','open-design':'Open Design',zed:'Zed'}[selection.id]||'Xcode');
 $('client-launch-title').textContent=L('Open on this computer','Abrir en este ordenador');
 const checking=launchDetecting||client==='claude'&&claudeDetecting;
 $('client-launch-refresh').textContent=checking?L('Checking…','Comprobando…'):L('Check again','Volver a comprobar');
 $('client-launch-refresh').disabled=checking||launchBusy;
 $('client-installation').hidden=!cli;
 const installed=detected?.installed,installStatus=$('client-installation-status'),installLink=$('client-installation-link');
 installStatus.textContent=launchDetecting?L('Checking installation…','Comprobando instalación…'):installed===true?L('Installed','Instalado'):installed===false?L('CLI not found','CLI no encontrado'):L('Installation status unavailable','Estado de instalación no disponible');
 installStatus.dataset.state=launchDetecting?'checking':installed===true?'installed':installed===false?'missing':'unknown';
 let installURL='';
 try{const url=new URL(detected?.installURL);if(url.protocol==='https:'&&!url.username&&!url.password)installURL=url.href;}catch{}
 installLink.hidden=!cli||launchDetecting||installed!==false||!installURL;
 if(!installLink.hidden)installLink.href=installURL;else installLink.removeAttribute('href');
 installLink.textContent=L('Installation guide ↗','Guía de instalación ↗');
 installLink.setAttribute('aria-label',L(name+' installation guide', 'Guía de instalación de '+name));
 installLink.title=L('Open official installation instructions','Abrir las instrucciones oficiales de instalación');
 $('client-launch-directory-label').textContent=L('Project folder (optional)','Carpeta del proyecto (opcional)');
 $('client-launch-directory-field').hidden=['codex','open-design','claude-desktop'].includes(selection.id);
 $('client-launch-directory').placeholder=launchInfo?.directory||'';
 $('client-launch-custom').hidden=selection.id!=='codex';
 $('client-launch-custom-label').textContent=L('Custom Codex application','Aplicación de Codex personalizada');
 $('client-launch-app-label').textContent=L('Application path on this computer','Ruta de la aplicación en este ordenador');
 $('client-launch-app').placeholder=launchInfo?.clients?.codex?.path||L('Absolute application path','Ruta absoluta de la aplicación');
 $('client-launch').textContent=launchBusy?L('Launching…','Abriendo…'):L('Launch ',terminal?'Iniciar ':'Abrir ')+name;
 $('client-launch').disabled=launchBusy||launchDetecting||selection.working||selection.valid===false||!state||!selection.count||!available;
 $('client-launch-help').textContent=selection.id==='claude-desktop'?L('Prepares Kilo with the selected models and starts the proxy before opening Claude Desktop. Close Claude first to apply configuration changes.','Prepara Kilo con los modelos seleccionados y arranca el proxy antes de abrir Claude Desktop. Cierra Claude primero para aplicar los cambios de configuración.'):selection.id==='open-design'?L('Prepares the selected CLI with your Kilo models, starts the proxy and opens Open Design in its separate Kilo profile.','Prepara el CLI elegido con tus modelos de Kilo, arranca el proxy y abre Open Design en su perfil de Kilo independiente.'):L('Opens with the current models and Kilo configuration. Changes are saved first. Command export settings below apply only to copied commands.','Abre con los modelos y la configuración de Kilo actuales. Los cambios se guardan antes. Los ajustes de exportación inferiores solo afectan a los comandos copiados.')+(selection.id==='zed'?L(' Paste the local key into Zed once using the instructions below.',' Pega la clave local en Zed una vez siguiendo las instrucciones inferiores.'):selection.id==='xcode-chat'?L(' Add the chat provider in Xcode once using the connection details below.',' Añade el proveedor de chat en Xcode una vez con la conexión indicada abajo.'):'');
 const reason=selection.reason||(!available&&!launchDetecting?(detected?.reason||(installed===true?L('The application is installed but cannot be launched on this computer.','La aplicación está instalada pero no se puede abrir en este ordenador.'):L('Application not detected. Install it, then check again.','Aplicación no detectada. Instálala y vuelve a comprobar.'))):!selection.count?L('Select at least one model.','Selecciona al menos un modelo.'):'');
 $('client-launch-status').textContent=launchMessage||reason;
 $('client-launch-status').classList.toggle('error',launchError);
}
async function detectLaunchClients(){
 if(launchDetecting)return;
 launchDetecting=true;launchDetected=true;renderClientLaunch();
 try{
  launchInfo=await api('clients/launch');
  if(!launchDirectoryEdited)$('client-launch-directory').value=launchInfo.directory||'';
 }catch(error){launchMessage=error.message;launchError=true;}
 finally{launchDetecting=false;renderClientLaunch();}
}
function launchFingerprint(){const selection=clientLaunchSelection();return JSON.stringify([selection?.id,selection?.fingerprint,['codex','open-design','claude-desktop'].includes(selection?.id)?'':$('client-launch-directory').value.trim(),selection?.id==='codex'?$('client-launch-app').value.trim():'']);}
async function openClient(){
 if(launchBusy||$('client-launch').disabled)return;
 const selection=clientLaunchSelection(),fingerprint=launchFingerprint();
 const body={client:selection.id,...(selection.id==='open-design'?{engine:selection.engine}:{}),...(['codex','open-design','claude-desktop'].includes(selection.id)?{}:{directory:$('client-launch-directory').value.trim()}),...(selection.id==='codex'&&$('client-launch-app').value.trim()?{appPath:$('client-launch-app').value.trim()}:{})};
 launchBusy=true;launchMessage='';launchError=false;renderClientLaunch();
 try{
  if(!selection.ready||selection.id==='claude-desktop')await selection.prepare();
  if(fingerprint!==launchFingerprint())throw new Error(language==='en'?'Your selection changed while preparing. Review it and open again.':'La selección cambió durante la preparación. Revísala y vuelve a abrir.');
  const result=await api('clients/launch',body);launchMessage=result.message;await refresh();
 }catch(error){launchMessage=error.message;launchError=true;}
 finally{launchBusy=false;renderClientLaunch();}
}
$('client-launch').addEventListener('click',openClient);
$('client-launch-refresh').addEventListener('click',()=>{launchMessage='';launchError=false;void detectLaunchClients();if(client==='claude')void detectClaude();if(client==='open-design')void openDesignHelper.reload();});
$('client-launch-directory').addEventListener('input',()=>{launchDirectoryEdited=true;launchMessage='';launchError=false;renderClientLaunch();});
$('client-launch-app').addEventListener('input',()=>{launchMessage='';launchError=false;renderClientLaunch();});
function renderSnippet() {
 const openDesignActive=client==='open-design';
 $('open-design-helper').hidden=!openDesignActive;
 if(openDesignActive)openDesignHelper.render({state,catalog,language});
 const xcodeActive=client==='xcode',editorActive=['opencode','zed','omp','claude-desktop'].includes(client);
 $('editor-helper').hidden=!editorActive;
 if(editorActive)editorHelper.render({client,state,catalog,language});
 $('xcode-helper').hidden=!xcodeActive;
 document.querySelector('#client-panel > .client-instructions').hidden=xcodeActive||editorActive||openDesignActive;
 document.querySelector('#client-panel > .snippet-stack').hidden=xcodeActive||editorActive||openDesignActive;
 if(xcodeActive)xcodeHelper.render({state,catalog,language});
  const isCodex = ['codex','codex-cli'].includes(client);
  $('codex-copy-help').hidden = !isCodex;
  $('codex-copy-title').textContent=t(client==='codex' ? 'Configurar y abrir Codex GUI' : 'Configurar y abrir Codex CLI');
  $('codex-copy-first').textContent=t('Selecciona modelos y abre el cliente con el botón superior. La carpeta, config.toml y models.json se crean o actualizan automáticamente.');
  $('copy-launch').textContent = t('Copiar arranque (opcional) ↗');
  renderDesktopModels();
  renderMultiClients();
  const info = descriptions[client].map(value => t(value));
  if(isCodex||client==='claude')info[1]=t('Selecciona modelos y abre el cliente desde el botón superior. Se prepara su perfil aislado con la configuración actual. Los comandos y archivos de abajo son exportaciones opcionales.');
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
  $('copy-config').disabled = !validModelID(effectiveModel());
  $('copy-config').textContent = t(client==='claude' ? 'Copiar JSON (opcional) ↗' : isCodex ? 'Copiar TOML (opcional) ↗' : 'Copiar configuración ↗');
  $('copy-launch').disabled = $('copy-config').disabled || (client === 'codex' && !$('desktop-app-path').value.trim());
  if (client === 'codex') $('protocol-note').textContent = t('El perfil de Kilo tiene su propio config.toml y sus propios datos de interfaz. No copies auth.json ni cookies del perfil principal. El mecanismo de aislamiento se ha verificado en el código de la app instalada; puede variar entre versiones. Usa un modelo compatible con Responses.');
  $('client-heading').textContent = info[0]; $('client-description').textContent = info[1]; $('snippet-name').textContent = info[2];
  $('snippet-code').textContent = snippet();
  renderClientLaunch();
}
function renderImageDependency(s,current) {
  const L=(en,es)=>language==='es'?es:en,dependency=s.imageTransportDependency;
  const mode=s.imageTransport?.mode||'cloudflare',missing=dependency?.tool==='cloudflared'&&dependency.required===true&&dependency.installed===false;
  if(imageDependencyPrompt.mode!==mode||(!imageDependencyPrompt.running&&s.running)||dependency?.installed===true)imageDependencyPrompt.dismissed=false;
  imageDependencyPrompt.mode=mode;imageDependencyPrompt.running=!!s.running;
  try { sessionStorage.setItem('kilo-cloudflare-prompt',JSON.stringify(imageDependencyPrompt)); } catch {}
  $('image-dependency-notice').hidden=!missing||current.mode!=='cloudflare'||imageDependencyPrompt.dismissed;
  $('image-dependency-title').textContent=L('Set up large images','Prepara las imágenes grandes');
  $('image-dependency-description').textContent=L('Cloudflare is selected for large images, but cloudflared was not found. Install it to send original images through temporary links. Text and smaller requests can still run.','Cloudflare está seleccionado para las imágenes grandes, pero no se ha encontrado cloudflared. Instálalo para enviar las imágenes originales mediante enlaces temporales. El texto y las peticiones pequeñas pueden seguir funcionando.');
  $('image-dependency-dismiss').textContent=L('Not now','Ahora no');
  $('image-cloudflare-dependency').hidden=current.mode!=='cloudflare';
  $('image-cloudflare-dependency-status').textContent=dependency?.installed===true?L('cloudflared found','cloudflared encontrado'):dependency?.installed===false?L('cloudflared not found','No se ha encontrado cloudflared'):L('cloudflared status unavailable','Estado de cloudflared no disponible');
  $('image-cloudflare-dependency-help').textContent=dependency?.installed===true?L('The executable is available. The tunnel starts only when a large request needs it; connectivity has not been tested.','El ejecutable está disponible. El túnel se inicia solo cuando lo necesita una petición grande; no se ha probado la conexión.'):L('Install cloudflared, then check again. Kilo Proxy does not install software or test a tunnel automatically. You can also choose local compression.','Instala cloudflared y vuelve a comprobarlo. Kilo Proxy no instala programas ni prueba un túnel automáticamente. También puedes elegir la compresión local.');
  let installURL='https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/downloads/';
  try { const candidate=new URL(dependency?.installURL);if(candidate.protocol==='https:'&&candidate.hostname==='developers.cloudflare.com')installURL=candidate.href; } catch {}
  for(const prefix of ['image-dependency','image-cloudflare']) {
    $(prefix+'-install').textContent=L('Installation instructions ↗','Instrucciones de instalación ↗');$(prefix+'-install').href=installURL;
    $(prefix+'-copy').textContent=L('Copy install command','Copiar comando de instalación');$(prefix+'-copy').hidden=!dependency?.installCommand||dependency.installed===true;$(prefix+'-copy').disabled=busy;
    $(prefix+'-command').textContent=dependency?.installCommand||'';$(prefix+'-command').hidden=!dependency?.installCommand||dependency.installed===true;
    $(prefix+'-check').textContent=L('Check again','Comprobar de nuevo');$(prefix+'-check').disabled=busy;
  }
}
function renderImageTransport(s) {
  const L=(en,es)=>language==='es'?es:en;
  const current=imageTransportPending??s.imageTransport??{mode:'cloudflare',profile:'high',litterboxTTL:'1h'};
  $('image-transport-title').textContent=L('Large images','Imágenes grandes');
  $('image-transport-description').textContent=L("Choose one method for inline images when a request exceeds 4.4 MB. Works with Responses, Chat Completions, and Anthropic Messages. Smaller requests and your original files stay unchanged.", 'Elige un método para las imágenes cuando una petición supera 4,4 MB. Funciona con Responses, Chat Completions y Anthropic Messages. Las peticiones pequeñas y tus archivos originales no cambian.');
  $('image-transport-mode-label').textContent=L('Large image handling','Tratamiento de imágenes grandes');
  for(const [mode,en,es] of [['off','Off','Desactivado'],['compress','Compress locally','Comprimir en local'],['cloudflare','Cloudflare quick tunnel','Túnel rápido de Cloudflare'],['tailscale','Tailscale Funnel','Tailscale Funnel'],['litterbox','Litterbox · Experimental','Litterbox · Experimental'],['upload','Upload to Kilo · Experimental','Subir a Kilo · Experimental']])$('image-transport-mode').querySelector(`option[value="${mode}"]`).textContent=L(en,es);
  $('image-transport-mode').value=current.mode||'cloudflare';
  $('image-transport-mode').disabled=busy;
  $('image-compression-options').hidden=current.mode!=='compress';
  $('image-compression-profile-label').textContent=L('Compression profile','Perfil de compresión');
  for(const [profile,en,es] of [['high','High quality','Alta calidad'],['balanced','Balanced','Equilibrado'],['small','Small size','Tamaño pequeño']])$('image-compression-profile').querySelector(`option[value="${profile}"]`).textContent=L(en,es);
  $('image-compression-profile').value=current.profile||'high';
  $('image-compression-profile').disabled=busy;
  $('image-compression-levels').textContent=L('High quality: up to 3072 px / quality 92. Balanced: 2048 px / quality 85. Small size: 1280 px / quality 75. Aspect ratio is preserved.', 'Alta calidad: hasta 3072 px / calidad 92. Equilibrado: 2048 px / calidad 85. Tamaño pequeño: 1280 px / calidad 75. Se conserva la proporción.');
  $('image-compression-note').textContent=L('Tries lossless optimization first, then your selected profile if needed. Only outbound copies change. If the request still does not fit, it stops with an explanation; it never lowers quality further or uploads images automatically.', 'Primero intenta optimizar sin pérdidas y después aplica el perfil elegido si hace falta. Solo cambian las copias enviadas. Si la petición sigue sin caber, se detiene y lo explica; nunca reduce más la calidad ni sube imágenes automáticamente.');
  $('image-upload-options').hidden=current.mode!=='upload';
  $('image-upload-description').textContent=L('Uploads original image bytes to Kilo and sends temporary links. No resizing, tunnel, or storage setup is needed. Up to 5 unique images uploaded per request, 20 MiB each.', 'Sube las imágenes originales a Kilo y envía enlaces temporales. No cambia su tamaño ni requiere configurar un túnel o almacenamiento. Hasta 5 imágenes únicas subidas por petición y 20 MiB por imagen.');
  $('image-upload-limit').textContent=L("Experimental: uses Kilo's Cloud Agent attachment storage with your account. This is not a documented Gateway integration and may stop working.", 'Experimental: usa el almacenamiento de adjuntos de Cloud Agent de Kilo con tu cuenta. No es una integración documentada del Gateway y puede dejar de funcionar.');
  $('image-upload-cleanup').textContent=L('Deletion is requested after completion or cancellation. Network failures or an app crash can leave remote copies behind; an expired link does not mean the image was deleted. Any unconfirmed deletion is shown here.', 'Se solicita el borrado al terminar o cancelar. Un fallo de red o el cierre inesperado de la app puede dejar copias remotas; que un enlace caduque no significa que la imagen se haya borrado. Los borrados sin confirmar se muestran aquí.');
  $('image-transport-off').hidden=current.mode!=='off';
  $('image-transport-off').textContent=L("Images pass through unchanged. Large requests can still exceed Kilo's limit and need conversation compaction or fewer attachments.", 'Las imágenes se envían sin cambios. Las peticiones grandes pueden superar el límite de Kilo y requerir compactar la conversación o reducir los adjuntos.');
  const publicDetails={
    cloudflare:{
      title:L('Cloudflare quick tunnel','Túnel rápido de Cloudflare'),
      description:L('Serves original image bytes from this computer through public, unguessable links. Links are removed after the request finishes or is cancelled. Keep Kilo Proxy running while images are in use.','Sirve las imágenes originales desde este equipo mediante enlaces públicos difíciles de adivinar. Los enlaces se retiran al terminar o cancelar la petición. Mantén Kilo Proxy abierto mientras se usan las imágenes.'),
      requirements:L('Requires cloudflared installed on this computer and available on PATH. No Cloudflare account or S3 bucket is needed. The tunnel starts when a large request needs it.','Requiere cloudflared instalado en este equipo y disponible en PATH. No necesita cuenta de Cloudflare ni un bucket S3. El túnel se inicia cuando lo necesita una petición grande.')
    },
    tailscale:{
      title:'Tailscale Funnel',
      description:L('Funnel makes image links publicly reachable, including outside your tailnet. Images stay on this computer and links are removed after the request finishes or is cancelled.','Funnel permite acceder a los enlaces de imágenes desde Internet, incluso fuera de tu tailnet. Las imágenes permanecen en este equipo y los enlaces se retiran al terminar o cancelar la petición.'),
      requirements:L('Requires the tailscale command, a signed-in account, and Funnel enabled for this device. Uses a dedicated HTTPS port 8443; leave it free for Kilo Proxy.','Requiere el comando tailscale, una cuenta con sesión iniciada y Funnel habilitado para este dispositivo. Usa el puerto HTTPS 8443; déjalo libre para Kilo Proxy.')
    },
    litterbox:{
      title:L('Temporary public hosting','Alojamiento público temporal'),
      description:L('Uploads original images to Litterbox, a third-party service. Anyone with the link can access them until expiry. Choose this only for images you can share with that service.','Sube las imágenes originales a Litterbox, un servicio externo. Cualquiera con el enlace puede acceder hasta que caduque. Elígelo solo para imágenes que puedas compartir con ese servicio.'),
      requirements:L('No account or extra executable is required.','No requiere cuenta ni ejecutables adicionales.')
    }
  };
  const publicDetail=publicDetails[current.mode];
  $('image-public-options').hidden=!publicDetail;
  $('image-public-title').textContent=publicDetail?.title||'';
  $('image-public-description').textContent=publicDetail?.description||'';
  $('image-public-requirements').textContent=publicDetail?.requirements||'';
  $('image-litterbox-options').hidden=current.mode!=='litterbox';
  $('image-litterbox-experimental').hidden=current.mode!=='litterbox';
  $('image-litterbox-experimental').textContent=L('Experimental: live availability could not be confirmed from this network. If the service rejects uploads, choose Cloudflare or local compression.','Experimental: no se ha podido confirmar la disponibilidad real desde esta red. Si el servicio rechaza las subidas, elige Cloudflare o la compresión local.');
  $('image-litterbox-terms').textContent=L('Litterbox requires prior approval for commercial service use.','Litterbox requiere autorización previa para uso en servicios comerciales.');
  $('image-litterbox-faq').textContent=L('Read the Litterbox FAQ','Consulta las preguntas frecuentes de Litterbox');
  $('image-litterbox-ttl-label').textContent=L('Link expiry','Caducidad del enlace');
  for(const [ttl,en,es] of [['1h','1 hour','1 hora'],['12h','12 hours','12 horas'],['24h','24 hours','24 horas'],['72h','72 hours','72 horas']])$('image-litterbox-ttl').querySelector(`option[value="${ttl}"]`).textContent=L(en,es);
  $('image-litterbox-ttl').value=current.litterboxTTL||'1h';
  $('image-litterbox-ttl').disabled=busy;
  $('image-litterbox-cleanup').textContent=L('Litterbox handles expiry. Kilo Proxy cannot delete these uploads early, even after you switch modes or close the app.','Litterbox gestiona la caducidad. Kilo Proxy no puede borrar estas subidas antes, aunque cambies de modo o cierres la app.');
  $('image-transport-saving').textContent=L('Cloudflare is the default for new settings. Your saved choice is preserved. Changes save automatically for new requests, without restarting. Only the selected method is used; failures never switch to another backend or upload service. Earlier uploads still receive their scheduled cleanup.', 'Cloudflare es la opción inicial para ajustes nuevos. Se conserva tu elección guardada. Los cambios se guardan automáticamente para nuevas peticiones, sin reiniciar. Solo se usa el método elegido; los fallos nunca cambian a otro backend o servicio de subida. Las subidas anteriores conservan su limpieza programada.');
  $('image-upload-warning').hidden=!s.imageUploadWarning;
  $('image-upload-warning').textContent=s.imageUploadWarning?L('Image cleanup needs attention: ','Revisa la limpieza de imágenes: ')+t(s.imageUploadWarning):'';
  renderImageDependency(s,current);
}
function render(s) {
  state = s;
  for(const selection of Object.values(codexClients))if(selection.imageGeneration===null){selection.imageGeneration=imageGenerationSelection(s.imageGeneration);selection.imageGenerationBaseline=imageGenerationSelection(s.imageGeneration);}
  configureDesktop(api, !!s.desktop);
  if (!initialized) {
    if (!languageChosen) { language = chooseLanguage(s.language, navigator.languages || [navigator.language]); translateDocument(language); $('language').value = language; }
    $('org-id').value = s.orgId; $('port').value = s.port; $('remember').checked = s.remember; initialized = true;
    if (s.warning) notify(s.warning, true);
  }
  renderImageTransport(s);
  if (updateRequestError && (s.update?.checking || (s.update?.checkedAt || '') !== updateFailedCheck)) updateRequestError = false;
  renderUpdates(document, s, language, updateCheckPending, updateRequestError);
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
  // action() renders the last server snapshot before saving. Keep the user's
  // pending choice visible until that save finishes, and prevent a second edit.
  if (!busy) $('capture-activity').checked=s.captureEnabled;
  $('capture-activity').disabled=busy;
  $('empty-activity-title').textContent=s.captureEnabled?t('Esperando tu primera petición capturada…'):t('Captura desactivada');
  $('empty-activity-help').textContent=s.captureEnabled?t('Verás el endpoint, las cabeceras y los cuerpos capturados para depurar.'):t('Activa la captura cuando necesites inspeccionar peticiones para depurar.');
  renderUsage(s.usage);
  renderAccountUsage($('account-usage'), s, language, busy);
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
      if(i===4 && reportedCost(e.usage?.costUSD)!==null){
        const source=costSourceLabel(e.usage?.costSource,language);
        if(source){const hint=document.createElement('small');hint.className='cost-source';hint.textContent=source;cell.append(hint);}
      }
      row.append(cell);
    });
    const details=document.createElement('td');
    if(e.hasDetails){const button=document.createElement('button');button.type='button';button.className='text-button';button.textContent=t('Inspeccionar');button.setAttribute('aria-label',t('Inspeccionar petición {id}',{id:e.id}));button.addEventListener('click',()=>void showTrace(e));details.append(button);}
    else details.textContent=t('Sin captura');
    row.append(details);$('event-rows').append(row);
  }
  if (catalogRevision !== s.catalogRevision && !pending) {
    catalogRevision = s.catalogRevision; catalog = []; syncSelectedContext(); catalogFetchedAt = ''; void loadModels();
  }
  renderSnippet();
}

function cacheNumber(value){return value===null || value===undefined ? t('Sin dato de caché') : Number(value).toLocaleString(language);}
function cachePercent(value){return value===null ? t('Sin dato de caché') : (value*100).toLocaleString(language,{maximumFractionDigits:1})+'%';}
function renderUsage(usage) {
 const total=usage?.total || {};
 const spend=reportedSpend(total,language);
 $('spend-title').textContent=spend.label;
 $('spend-total').textContent=spend.amount;
 $('spend-coverage').textContent=spend.coverage;
 $('spend-semantics').textContent=inferenceCostNote(language);
 $('spend-tokens').textContent=t('{input} entrada total · {output} salida',{input:total.withPrompt>0 ? cacheNumber(total.prompt) : t('Coste desconocido'),output:total.withTokens>0 ? cacheNumber(total.output) : t('Coste desconocido')});
 const cache=cacheStats(total);
 $('cache-read-total').textContent=cacheNumber(cache.read);
 $('cache-write-total').textContent=cacheNumber(cache.write);
 $('cache-ratio-total').textContent=cachePercent(cache.ratio);
 $('cache-coverage').textContent=t('Porcentaje calculado sobre {covered} de {requests} peticiones con datos completos de entrada y caché.',cache);
 $('cache-token-coverage').textContent=t('Caché leída reportada en {read} peticiones; escritura en {write}.',{read:total.withCacheRead||0,write:total.withCacheWrite||0});
 $('spend-partial').textContent=spend.responseStats;
 $('usage-sessions').replaceChildren();
 for(const session of usage?.sessions || []){
  const row=document.createElement('tr');
  let label=session.label;
  if(session.source==='unassigned') label=t('Peticiones sin sesión identificada');
  else if(session.source==='overflow') label=t('Otras sesiones');
  else label=label.replace('Codex task',t('Tarea de Codex')).replace('Claude session',t('Sesión de Claude')).replace('Client session',t('Sesión del cliente')).replace('Kilo task',t('Tarea de Kilo'));
  const spend=reportedSpend(session,language),cache=cacheStats(session),last=lastCacheStats(session.lastCache);
  const lastText=last.read===null ? t('Sin dato de caché') : cacheNumber(last.read)+(last.prompt!==null ? ' / '+cacheNumber(last.prompt) : '')+' · '+cachePercent(last.ratio);
  const price=spend.partial?spend.label+': '+spend.amount:spend.amount;
  const coverage=spend.coverage+'\n'+spend.responseStats;
  for(const value of [label,session.orgId||'—',session.requests,price,coverage,cacheNumber(cache.read),cacheNumber(cache.write),cachePercent(cache.ratio)+' · '+cache.covered+'/'+cache.requests,lastText]){
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
$('capture-activity').addEventListener('change',event=>{const enabled=event.target.checked;action(async()=>{await api('activity/config',{enabled});if(!enabled){activeTrace=null;traceRequest++;renderTrace();}});});
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

function renderDesktopModels() {
  renderCodexSetup();
  renderCodexImages();
  renderCodexQueueMode();
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
  $('codex-catalog-preview').textContent=JSON.stringify(contextPreview(()=>codexCatalog([...codexSelection().models.values()],codexSelection().initial)),null,2);
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
  levels.append(choices);controls.append(levels);controls.append(contextControls(model,{language,catalogModel:catalog.find(entry=>entry.id===id),onChange:renderSnippet}));return controls;
}
function codexVisibleModels() {
  if(client==='claude')return claudeVisibleModels();
  // Saved/manual selections stay reachable even when absent from Kilo's catalog.
  const available=new Map(catalog.map(model=>[model.id,model]));
  for(const [id,model] of codexSelection().models)available.set(id,available.has(id) ? {...available.get(id),displayName:model.displayName} : model);
  const selectedOnly=$('codex-selected-only').checked;
  const candidates=filterModelLab([...available.values()],$('model-lab').value).filter(model=>!selectedOnly || codexSelection().models.has(model.id));
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
  const before=codexSetupSignature(selection);
  try {
    const data=await api(target+'/catalog');
    if(codexSetupSignature(selection)!==before){notify(language==='en'?'Your selection changed while loading. Load again to replace those edits.':'Tu selección cambió durante la carga. Carga de nuevo para sustituir esos cambios.');return;}
    selection.models.clear();
    selection.imageGeneration=imageGenerationSelection(data.imageGeneration)??imageGenerationSelection(state?.imageGeneration);
    selection.imageGenerationBaseline=imageGenerationSelection(selection.imageGeneration);
    acceptCodexImageSettings(selection.imageGeneration);
    if(target==='codex')selection.queueMode=data.followUpQueueMode==='steer'?'steer':'queue';
    for(const model of data.catalog.models){selection.models.set(model.slug,{...catalog.find(entry=>entry.id===model.slug),id:model.slug,name:catalog.find(entry=>entry.id===model.slug)?.name || model.slug,displayName:model.display_name || '',contextPreset:'custom',contextTokens:model.context_window,contextMaximum:catalog.find(entry=>entry.id===model.slug)?.contextWindow||0,contextWindow:model.context_window,inputModalities:model.input_modalities,reasoningLevels:(model.supported_reasoning_levels || []).map(r=>r.effort),defaultReasoning:model.default_reasoning_level});}
    selection.initial=selection.models.has(data.defaultModel) ? data.defaultModel : selection.models.keys().next().value || '';
    if(client===target){$('codex-selected-only').checked=true;$('model-search').value='';}
    renderModels();renderSnippet();toast('Catálogo de Codex cargado');
  }catch(error){notify(error.message,true);}
});
$('suggest-codex-reasoning').addEventListener('click',()=>{for(const model of codexSelection().models.values()){model.reasoningEfforts=catalog.find(entry=>entry.id===model.id)?.reasoningEfforts;delete model.reasoningLevels;delete model.defaultReasoning;}renderSnippet();});
async function prepareCodex(){
  const target=client, selection=codexSelection();
  if(!selection.models.size)return;
  if(!imageGenerationValid(selection.imageGeneration,catalog))throw new Error(language==='en'?'Choose an available image model before preparing Codex.':'Elige un modelo de imágenes disponible antes de preparar Codex.');
  if(selection.preparing)throw new Error(language==='en'?'This profile is already being prepared.':'Este perfil ya se está preparando.');
  selection.preparing=true;renderCodexSetup();renderClientLaunch();
  const signature=codexSetupSignature(selection);
  const imagesSent=imageGenerationSelection(selection.imageGeneration);
  try {
    const result=await api(target+'/catalog',{catalog:codexCatalog([...selection.models.values()],selection.initial),...(imagesSent?{imageGeneration:imagesSent}:{}),...(target==='codex'?{followUpQueueMode:selection.queueMode==='steer'?'steer':'queue'}:{})});
    acceptCodexImageSettings(imagesSent);
    selection.setup={signature,path:result.profileDir};renderCodexSetup();toast(target==='codex-cli' ? 'Perfil de Codex CLI preparado' : 'Perfil de Codex GUI preparado');
  }catch(error){selection.setup=null;throw error;}
  finally{selection.preparing=false;renderCodexSetup();renderClientLaunch();}
}
$('save-codex-catalog').addEventListener('click',()=>prepareCodex().catch(error=>notify(error.message,true)));
$('codex-manual-id').addEventListener('input',renderDesktopModels);
$('codex-selected-only').addEventListener('change',renderModels);
$('download-codex-catalog').addEventListener('click',()=>{
  if(!codexSelection().models.size)return;
  const blob=new Blob([JSON.stringify(contextPreview(()=>codexCatalog([...codexSelection().models.values()],codexSelection().initial)),null,2)+'\n'],{type:'application/json'});
  const url=URL.createObjectURL(blob),link=document.createElement('a');link.href=url;link.download='models.json';link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
});
function applyModelContext() {
  const selected = catalog.find(m => m.id === $('model').value.trim());
  if (selected?.contextWindow) $('context-window').value = selected.contextWindow;
}
function renderModels() {
  configureModelSort($('model-sort'),language);
  const manualModels=isCodexClient() ? [...codexSelection().models.values()] : client==='claude' ? [...multiClients.claude.models.values()] : validModelID($('model').value.trim()) ? [{id:$('model').value.trim()}] : [];
  configureModelLab($('model-lab'),[...catalog,...manualModels],language);
  const availableCount=new Set([...catalog,...manualModels].map(model=>model.id)).size;
  const matches = sortModels(['codex','codex-cli','claude'].includes(client) ? codexVisibleModels() : filterModels(filterModelLab(catalog,$('model-lab').value), $('model-search').value, $('coding-models').checked),$('model-sort').value);
  const selectedID = $('model').value.trim();
  const selected = catalog.find(m => m.id === selectedID);
  $('load-models').disabled = catalogLoading || ['starting','pending'].includes(state?.auth?.status);
  $('catalog-status').textContent = catalogLoading ? t('Cargando modelos de Kilo…') : catalogError ? t(catalogError) : t('{shown} de {total} modelos · actualizado {time}', {shown:matches.length,total:availableCount,time:catalogFetchedAt ? new Date(catalogFetchedAt).toLocaleTimeString(language, {hour:'2-digit',minute:'2-digit'}) : '—'});
  const picker = $('model-picker'), restoreFocus = picker.contains(document.activeElement), scroll = picker.scrollTop;
  const focusKey=document.activeElement?.dataset.focus;
  const caret=document.activeElement?.classList.contains('codex-name-input') ? [document.activeElement.selectionStart,document.activeElement.selectionEnd] : null;
  const expanded=new Set([...picker.querySelectorAll('details[open]')].map(el=>el.dataset.model));
  picker.replaceChildren();
  for (const m of matches) {
    const row = document.createElement('label'), radio = document.createElement('input'), name = document.createElement('span');
    row.className = 'model-option'; radio.type = ['codex','codex-cli','claude'].includes(client) ? 'checkbox' : 'radio'; radio.name = 'model-choice'; radio.value = m.id;radio.dataset.focus='model:'+m.id; radio.checked = isCodexClient() ? codexSelection().models.has(m.id) : client==='claude' ? multiClients.claude.models.has(m.id) : m.id === selectedID; radio.disabled = catalogLoading;
    const shownName=isCodexClient() ? codexDisplayName(m) : client==='claude' ? (m.displayName || m.name || m.id) : m.name;
    radio.setAttribute('aria-label',shownName + ' · ' + m.id);
    const title = document.createElement('strong'), id = document.createElement('small'); title.textContent = shownName; id.textContent = m.id; name.append(title,id);
    name.className='model-option-title';
    row.append(radio,name,modelPriceDetails(m,language));
    if(isCodexClient()){
      const entry=document.createElement('div');entry.className='codex-model-entry';entry.classList.toggle('is-selected',codexSelection().models.has(m.id));entry.append(row);
      if(codexSelection().models.has(m.id))entry.append(codexRowControls(codexSelection().models.get(m.id),expanded));
      picker.append(entry);
    }else if(client==='claude'){const entry=document.createElement('div');entry.className='codex-model-entry';entry.classList.toggle('is-selected',multiClients.claude.models.has(m.id));entry.append(row);if(multiClients.claude.models.has(m.id))entry.append(claudeRowControls(multiClients.claude.models.get(m.id)));picker.append(entry);}
    else {row.classList.add('model-card');picker.append(row);}
  }
  if (!matches.length && !catalogLoading) { const empty=document.createElement('p');empty.textContent=t('Sin resultados. Cambia la búsqueda o desactiva el filtro.');picker.append(empty); }
  if(restoreFocus){const control=[...picker.querySelectorAll('[data-focus]')].find(el=>el.dataset.focus===focusKey);control?.focus({preventScroll:true});if(caret && control?.classList.contains('codex-name-input'))control.setSelectionRange(...caret);}
  picker.scrollTop = scroll;
  $('model-hint').textContent = ['codex','codex-cli'].includes(client)
    ? t('Codex requiere Responses. Busca OpenAI como punto de partida; el catálogo no certifica esa compatibilidad.')
    : client === 'claude' ? t('Claude Code requiere Messages. Busca Anthropic como punto de partida; el catálogo no certifica esa compatibilidad.')
    : t('Selecciona un modelo y el helper completará su ID y la ventana de contexto de Zed.');
  const details = $('model-details'); details.replaceChildren(); details.hidden = ['codex','codex-cli','claude'].includes(client) || !selectedID;
  if(['codex','codex-cli','claude'].includes(client)){$('model-hint').textContent=t('Marca modelos, ajusta el razonamiento en su tarjeta y guarda. La estrella indica el modelo inicial.');return;}
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
function syncSelectedContext() {
 for(const selection of [...Object.values(codexClients),...Object.values(multiClients)])syncContextModels(selection.models,catalog);
 for(const helper of [editorHelper,xcodeHelper,openDesignHelper])helper.setCatalog(catalog);
}
async function loadModels() {
  const request = ++catalogRequest, revision = catalogRevision;
  catalogLoading = true; catalogError = ''; renderModels();
  try {
    const result = await api('models', {});
    if (request !== catalogRequest || revision !== catalogRevision || result.revision !== catalogRevision) return;
    catalog = result.models; syncSelectedContext(); catalogFetchedAt = result.fetchedAt;
  } catch (error) { if (request === catalogRequest) catalogError = error.message; }
  finally { if (request === catalogRequest) { catalogLoading = false; renderModels(); renderSnippet(); } }
}
$('load-models').addEventListener('click', () => void loadModels());
$('model-search').addEventListener('input', renderModels);
$('model-sort').addEventListener('change', () => { setModelSort($('model-sort').value); $('model-picker').scrollTop=0; renderModels(); });
$('model-lab').addEventListener('change', () => { setModelLab($('model-lab').value); $('model-picker').scrollTop=0; renderModels(); });
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
async function refresh() {
  if (stopped) return;
  const revision = updateCheckRevision, next = await api('state');
  // A state poll already in flight must not undo a newer manual check.
  if (revision !== updateCheckRevision && state) next.update = state.update;
  render(next);
}
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
  if (client !== button.dataset.client) { $('codex-selected-only').checked = false; $('model-search').value = ''; }
  clientModels[client] = $('model').value;
  $('model').value = clientModels[button.dataset.client] || '';
  client = button.dataset.client;
  launchMessage='';launchError=false;
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
  await openExternal(login.verificationUrl);
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
  lock('Kilo Proxy está cerrado', 'El proxy se ha detenido. Puedes cerrar esta pestaña. Para volver a usarlo, abre la aplicación.');
}));
document.querySelectorAll('.nav-link').forEach(link => link.addEventListener('click', () => {
  document.querySelectorAll('.nav-link').forEach(l => l.classList.toggle('selected', l === link));
}));
bindDesktopLinks(document, error => notify(error.message, true));
async function poll() {
  if (stopped) return;
  if (!busy) { try { await refresh(); } catch { if (!stopped) notify('Se ha perdido la conexión con la aplicación. Comprueba que Kilo Proxy siga abierto.', true); } }
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
if (!token) { stopped = true; lock('Abre Kilo Proxy', 'Este panel necesita el enlace de acceso de la aplicación. Usa «Abrir panel» en el icono de Kilo Proxy de la barra de menús o bandeja del sistema.'); }
else poll();

function currentClaudeCaps(){return $('claude-mode').value==='modern' ? claudeCapabilities('2.1.251') : claudeInstalled;}
function currentClaudeSelection(){const s=multiClients.claude;return claudeSelection([...s.models.values()],s.initial,s.aliases,$('claude-mode').value);}
function claudeSetupSignature(){return JSON.stringify([state?.baseURL,state?.localKey,currentClaudeCaps(),contextPreview(currentClaudeSelection)]);}
function renderClaudeSetup(){
 $('claude-mode').options[0].textContent=t('Versión instalada (automático)');
 $('claude-mode').options[1].textContent=t('Claude Code 2.1.251 o posterior');
 const s=multiClients.claude,caps=currentClaudeCaps();
 $('claude-models').hidden=client!=='claude';
 $('claude-model-actions').hidden=!s.models.size;
 $('save-claude-profile').disabled=claudePreparing||!!contextError(multiClients.claude.models);
 $('save-claude-profile').textContent=t(claudePreparing ? 'Preparando perfil…' : '1. Preparar Claude Code');
 $('claude-version-status').textContent=t(claudeInstalled.version ? 'Claude Code {version} · compatible con la configuración de Kilo' : claudeChecked ? 'No se pudo detectar Claude Code. Se usa compatibilidad básica.' : 'Versión pendiente de comprobar.',{version:claudeInstalled.version});
 $('claude-capabilities-note').textContent=t(caps.perModelEffort ? 'Lista y nombres personalizados, con preferencias de razonamiento por modelo compatibles con Claude Code 2.1.251+.' : caps.picker ? 'Lista y nombres personalizados disponibles. El razonamiento inicial es global en esta versión.' : 'Esta versión usa alias y comandos /model. Los nombres cortos se guardan en el helper; el selector personalizado requiere 2.1.242+.');
 $('claude-capabilities-note').textContent+=(language==='en'?' Context applies to the whole session: the smallest selected window is used (100K–1M). Restart Claude after changing it.':' El contexto se aplica a toda la sesión: se usa la menor ventana seleccionada (100K–1M). Reinicia Claude después de cambiarlo.');
 $('claude-setup-status').textContent=claudeSetup?.signature===claudeSetupSignature() ? t('Configuración guardada en {path}. Claude Code está listo para arrancar con Kilo.',{path:claudeSetup.path}) : t(claudeSetup ? 'Hay cambios sin guardar. Se guardarán antes de abrir.' : 'Selecciona modelos y abre el cliente. Su perfil se prepara automáticamente.');
 if(client==='claude')$('codex-selection-count').textContent=t(s.models.size===1 ? '1 modelo seleccionado' : '{count} modelos seleccionados',{count:s.models.size});
 const id=$('claude-manual-id').value.trim();$('add-claude-model').disabled=!validModelID(id) || s.models.has(id) || s.models.size>=50;
 const signature=JSON.stringify([contextPreview(currentClaudeSelection),caps,language]);
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
 for(const [id,m] of selection)available.set(id,mergeModelSelection(available.get(id),m));
 const query=$('model-search').value.trim().toLowerCase();
 return filterModelLab([...available.values()],$('model-lab').value).filter(m=>(!$('codex-selected-only').checked || selection.has(m.id)) && (m.id+' '+(m.name || '')+' '+(m.displayName || '')).toLowerCase().includes(query) && (selection.has(m.id) || !$('coding-models').checked || m.tools===true && m.outputModalities?.includes('text')));
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
 controls.append(contextControls(model,{language,catalogModel:catalog.find(entry=>entry.id===model.id),onChange:renderSnippet}));
 return controls;
}
async function detectClaude(){
 if(claudeDetecting)return;
 claudeDetecting=true;$('detect-claude').disabled=true;renderClientLaunch();
 try{claudeInstalled=await api('claude/info');claudeChecked=true;renderSnippet();}catch(error){notify(error.message,true);}finally{claudeDetecting=false;$('detect-claude').disabled=false;renderClientLaunch();}
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
async function prepareClaude(){
 if(!multiClients.claude.models.size)return;
 if(claudePreparing)throw new Error(language==='en'?'This profile is already being prepared.':'Este perfil ya se está preparando.');
 const signature=claudeSetupSignature();claudePreparing=true;renderClaudeSetup();renderClientLaunch();
 try{const result=await api('claude/profile',currentClaudeSelection());claudeSetup={signature,path:result.profileDir};toast('Perfil de Claude preparado');}
 catch(error){claudeSetup=null;throw error;}finally{claudePreparing=false;renderClaudeSetup();renderClientLaunch();}
}
$('save-claude-profile').addEventListener('click',()=>prepareClaude().catch(error=>notify(error.message,true)));
$('load-claude-profile').addEventListener('click',async()=>{
 try{const saved=await api('claude/profile'),s=multiClients.claude;s.models.clear();for(const m of saved.models)s.models.set(m.id,contextModel({...m,name:m.displayName||m.id},catalog.find(entry=>entry.id===m.id),true));s.initial=saved.initial;s.aliases=saved.aliases || {};$('claude-mode').value=saved.mode;$('codex-selected-only').checked=true;$('model-search').value='';renderModels();renderSnippet();toast('Perfil de Claude cargado');}catch(error){notify(error.message,true);}
});

applyLanguage(language);

$('account-usage').addEventListener('click', event => {
  if (event.target.closest('#refresh-billing')) action(() => api('billing/refresh', {}));
});
$('account-usage').addEventListener('change', event => {
  if (event.target.id === 'account-tray-display') {
    const display = event.target.value;
    action(async () => { await api('tray-settings', {display}, 'PUT'); });
  }
});
for(const id of ['image-transport-mode','image-compression-profile','image-litterbox-ttl'])$(id).addEventListener('change', async event => {
  const current={mode:state?.imageTransport?.mode||'cloudflare',profile:state?.imageTransport?.profile||'high',litterboxTTL:state?.imageTransport?.litterboxTTL||'1h'};
  if(id==='image-transport-mode')current.mode=event.target.value;else if(id==='image-litterbox-ttl')current.litterboxTTL=event.target.value;else current.profile=event.target.value;
  imageTransportPending=current;
  try { await action(() => api('image-transport-settings', current, 'PUT')); }
  finally { imageTransportPending=null; if(state)renderImageTransport(state); }
});
for(const prefix of ['image-dependency','image-cloudflare']) {
  $(prefix+'-copy').addEventListener('click',()=>{const command=state?.imageTransportDependency?.installCommand;if(command)void copy(command);});
  $(prefix+'-check').addEventListener('click',()=>void action(async()=>{}));
}
$('image-dependency-dismiss').addEventListener('click',()=>{imageDependencyPrompt.dismissed=true;if(state)renderImageTransport(state);});
$('updates-check').addEventListener('click', async () => {
  if (updateCheckPending || state?.update?.checking) return;
  updateCheckPending = true;
  updateCheckRevision++;
  updateRequestError = false;
  renderUpdates(document, state, language, true);
  try {
    const update = await api('updates', {});
    if (state) state.update = update;
  } catch {
    updateRequestError = true;
    updateFailedCheck = state?.update?.checkedAt || '';
  } finally {
    updateCheckRevision++;
    updateCheckPending = false;
    renderUpdates(document, state, language, false, updateRequestError);
  }
});

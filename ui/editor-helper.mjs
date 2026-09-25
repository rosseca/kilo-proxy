import {claudeDesktopModelAllowed,claudeDesktopSelection} from './claude-desktop-helper.mjs';
import {contextLimits,contextControls,contextModel,syncContextModels,contextError,contextPreview} from './context-policy.mjs';
import {validModelID,modelPriceDetails,filterModels,sortModels,configureModelSort,setModelSort,mergeModelSelection,filterModelLab,configureModelLab,setModelLab} from './model-helper.mjs';
import {ompPayload,ompLaunch,ompConfig,ompEfforts} from './omp-helper.mjs';
import {clientConfig} from './client-config.mjs';
export function editorPayload(models,initial) {
 return {models:models.map(m=>({id:m.id,name:(m.displayName||m.name||m.id).slice(0,80),contextWindow:contextLimits(m).contextWindow,maxOutputTokens:contextLimits(m).maxOutputTokens})),initial};
}
const quote=s=>"'"+s.replaceAll("'","'\\''")+"'";
const ps=s=>"'"+s.replaceAll("'","''")+"'";
export function editorLaunch(path,model,shell='unix'){
 if(shell==='powershell')return `& {
  $kiloConfig = ${ps(path)}
  if (!(Test-Path -LiteralPath $kiloConfig -PathType Leaf)) { throw 'Prepare OpenCode first.' }
  $kiloNames = @('OPENCODE_CONFIG', 'OPENCODE_CONFIG_CONTENT')
  $kiloPrevious = @{}
  foreach ($kiloName in $kiloNames) { $kiloPrevious[$kiloName] = [Environment]::GetEnvironmentVariable($kiloName, 'Process') }
  try {
    $env:OPENCODE_CONFIG = $kiloConfig
    Remove-Item -LiteralPath Env:OPENCODE_CONFIG_CONTENT -ErrorAction SilentlyContinue
    opencode --model ${ps('kilo-local/'+model)}
  } finally {
    foreach ($kiloName in $kiloNames) {
      if ($null -eq $kiloPrevious[$kiloName]) {
        Remove-Item -LiteralPath "Env:$kiloName" -ErrorAction SilentlyContinue
      } else {
        Set-Item -LiteralPath "Env:$kiloName" -Value $kiloPrevious[$kiloName]
      }
    }
  }
}`;
 return `(\n  kilo_config=${quote(path)}\n  [ -f "$kilo_config" ] || { printf '%s\\n' 'Prepare OpenCode first.' >&2; exit 1; }\n  unset OPENCODE_CONFIG_CONTENT\n  OPENCODE_CONFIG="$kilo_config" opencode --model ${quote('kilo-local/'+model)}\n)`;
}
export function createEditorHelper({api,notify,copy,refreshCatalog,onChange=()=>{}}){
 const $=id=>document.getElementById(id),selections=Object.fromEntries(['opencode','zed','omp','claude-desktop'].map(id=>[id,{models:new Map(),initial:'',saved:null,path:'',profileDir:''}]));
 let ctx={client:'opencode',language:'en',catalog:[]},working=false,signature='',renderedClient='',desktopLibraryLoaded=false,desktopLibraryLoading=false,desktopOmitted=0,desktopFallback=false,desktopExperimental=false,desktopPending=null,desktopLibrary={models:[],defaultModel:''},desktopOptionsSyncing=false,desktopOptionsRevision=0,desktopOptionsCheckedState=null;
 const desktopNames=new Map();
 const L=(en,es)=>ctx.language==='en'?en:es,s=()=>selections[ctx.client];
 const payload=()=>ctx.client==='claude-desktop'?claudeDesktopSelection([...s().models.values()],s().initial,desktopExperimental).selection:ctx.client==='omp'?ompPayload([...s().models.values()],s().initial):editorPayload([...s().models.values()],s().initial);
 const endpoint=client=>client==='omp'?'omp/profile':client==='claude-desktop'?'claude-desktop/profile':'editors/'+client+'/profile';
 const launch=()=>ctx.client==='omp'?ompLaunch(s().profileDir,s().initial,$('editor-shell').value):editorLaunch(s().path,s().initial,$('editor-shell').value);
 const fingerprint=()=>JSON.stringify([ctx.client,contextPreview(payload),ctx.state?.baseURL,ctx.state?.zedBaseURL,ctx.state?.localKey,ctx.client==='omp'?ctx.state?.imageGeneration:null,ctx.client==='claude-desktop'?desktopExperimental:null]);
 const prepared=()=>s().saved===fingerprint();
 const selectionError=()=>ctx.client==='claude-desktop'?'':contextError(s().models);
 function controls(){
  const invalid=selectionError();
  const zed=ctx.client==='zed',omp=ctx.client==='omp',desktop=ctx.client==='claude-desktop',name=zed?'Zed':omp?'Oh My Pi':desktop?'Claude Desktop':'OpenCode';
  const labels={
   'editor-title':`${name} · `+L('select and prepare','selecciona y prepara'),
   'editor-intro':desktop?L('Prepare the named Kilo third-party configuration for Chat, Cowork and Code. Starts from your shared model library; edits here apply to this configuration.','Prepara la configuración de terceros Kilo para Chat, Cowork y Code. Parte de tu biblioteca compartida; los cambios aquí se aplican a esta configuración.'):omp?L('Choose your Kilo models and open Oh My Pi in a dedicated profile. Friendly names, limits and reasoning preferences are saved together.','Elige tus modelos de Kilo y abre Oh My Pi en un perfil propio. Los nombres cortos, límites y preferencias de razonamiento se guardan juntos.'):zed?L('Save models and short names to Zed and configure its local key in the system credential store. Other providers and settings are retained.','Guarda modelos y nombres cortos en Zed y configura su clave local en el almacén de credenciales del sistema. Se conservan otros proveedores y ajustes.'):L('Prepare a dedicated OpenCode configuration with your models and local authentication.','Prepara una configuración propia de OpenCode con tus modelos y autenticación local.'),
   'editor-refresh':L('Refresh catalog','Actualizar catálogo'),'editor-load':L('Load saved selection','Cargar selección guardada'),'editor-save':working?L('Working…','Preparando…'):L('1. Prepare ','1. Preparar ')+name,
   'editor-add':L('Add model','Añadir modelo'),'editor-manual-title':L('Add by exact ID','Añadir por ID exacto'),'editor-selected-label':L('Selected only','Solo seleccionados'),
   'editor-copy':zed?L('Copy key for Zed (recovery)','Copiar clave para Zed (recuperación)'):L('Copy launch command (optional)','Copiar arranque (opcional)'),
   'editor-export':L('Copy configuration (optional)','Copiar configuración (opcional)'),
   'editor-next':desktop?L('Close Claude before opening to apply changes. Claude Desktop uses one applied third-party configuration at a time. Features depend on your installed app and operating system.','Cierra Claude antes de abrir para aplicar cambios. Claude Desktop usa una configuración de terceros activa cada vez. Las funciones dependen de la app y del sistema operativo.'):omp?L('Launch above to prepare ~/.omp-kilo and open a project terminal. No /login is needed. Use /model to switch models and thinking levels. Your regular Oh My Pi profile stays separate. The configured Kilo image tool is added when enabled.','Inicia desde el botón superior para preparar ~/.omp-kilo y abrir una terminal del proyecto. No hace falta /login. Usa /model para cambiar modelo y nivel de razonamiento. Tu perfil habitual de Oh My Pi queda separado. Se añade la herramienta de imágenes de Kilo cuando está activada.'):zed?L('Preparation saves the local key in the system credential store. Select a model in Zed’s Agent panel; existing projects stay open. Copy key is only for recovery in “agent: open settings” → kilo-local. Copying configuration alone does not save credentials. This configures Zed Agent, not edit prediction or external agents.','La preparación guarda la clave local en el almacén de credenciales del sistema. Elige modelo en el panel Agent de Zed; tus proyectos siguen abiertos. Copiar clave sirve solo para recuperarla en «agent: open settings» → kilo-local. Copiar solo la configuración no guarda las credenciales. Configura Zed Agent, no la predicción de código ni agentes externos.'):L('Open above to start a project terminal, or copy the optional command. Use /models to switch models. No /connect is needed: the protected profile stores only the local proxy key. Global and project settings still merge; project settings can override this profile.','Abre desde el botón superior para iniciar una terminal del proyecto, o copia el comando opcional. Cambia con /models. No hace falta /connect: el perfil protegido guarda solo la clave local del proxy. Se combinan los ajustes globales y del proyecto; los del proyecto pueden prevalecer.'),
   'editor-limit-note':desktop?(desktopExperimental?L('Experimental models: provider compatibility, tools, context and reasoning support may be limited. Preparation does not verify inference. Context and output limits are managed by the app and model; shared context presets are not applied.','Modelos experimentales: la compatibilidad del proveedor y el soporte de herramientas, contexto y razonamiento pueden ser limitados. Preparar no verifica la inferencia. La app y el modelo gestionan los límites de contexto y salida; los preajustes compartidos no se aplican.'):L('Claude Desktop uses Claude models by default. Enable the experimental option to use models from other providers. Context and output limits are managed by the app and model; shared context presets are not applied.','Claude Desktop usa modelos Claude por defecto. Activa la opción experimental para usar modelos de otros proveedores. La app y el modelo gestionan los límites de contexto y salida; los preajustes compartidos no se aplican.')):omp?L('Uses Responses over HTTP/SSE. Reasoning controls require declared model capabilities. Recommended uses up to 272,000 context tokens. Output is capped at one quarter of the working window. Modified files receive .bak backups.','Usa Responses por HTTP/SSE. Los controles de razonamiento requieren capacidades declaradas del modelo. Recomendado usa hasta 272.000 tokens de contexto. La salida se limita a un cuarto de la ventana de trabajo. Los archivos modificados reciben copia .bak.'):L('Uses Chat Completions. Model lists and saved settings do not verify generation or tool support. Recommended uses up to 272,000 context tokens. Presets never exceed the known model maximum. Changed files receive .bak backups.','Usa Chat Completions. La lista y los ajustes guardados no verifican generación ni herramientas. Recomendado usa hasta 272.000 tokens de contexto. Los presets no superan el máximo conocido del modelo. Los archivos modificados reciben copia .bak.')};
  if(desktop&&desktopOmitted)labels['editor-limit-note']+=' '+(desktopOmitted===1?L('1 shared model omitted; your library is unchanged.','1 modelo compartido omitido; tu biblioteca se conserva.'):L(`${desktopOmitted} shared models omitted; your library is unchanged.`,`${desktopOmitted} modelos compartidos omitidos; tu biblioteca se conserva.`))+(desktopFallback&&s().initial?(desktopExperimental?L(' The first available model is used because your shared default is unsupported.',' Se usa el primer modelo disponible porque el predeterminado compartido no es compatible.'):L(' The first Claude model is used because your shared default is unsupported.',' Se usa el primer modelo Claude porque el predeterminado compartido no es compatible.')):'');
  for(const [id,text]of Object.entries(labels))$(id).textContent=text;
  $('editor-search').placeholder=L('Search model, provider or saved name','Buscar modelo, proveedor o nombre guardado');
  $('editor-sort-label').textContent=L('Sort by','Ordenar por');
  $('editor-lab-label').textContent=L('Lab','Laboratorio');
  $('claude-desktop-experimental-field').hidden=!desktop;
  $('claude-desktop-experimental-label').textContent=L('Experimental: use models from other providers','Experimental: usar modelos de otros proveedores');
  $('claude-desktop-experimental').checked=desktopPending??desktopExperimental;
  $('claude-desktop-experimental').disabled=working||!desktopLibraryLoaded;
  configureModelSort($('editor-sort'),ctx.language);
  $('editor-save').disabled=working||!s().models.size||!ctx.state||!!invalid;
  $('editor-load').disabled=working;$('editor-refresh').disabled=working;$('editor-add').disabled=working||desktop&&!claudeDesktopModelAllowed($('editor-id').value.trim(),desktopExperimental);
  $('editor-id').placeholder=desktop&&!desktopExperimental?'anthropic/claude-sonnet-4.6':'provider/model';
  $('editor-copy').disabled=working||!prepared();$('editor-export').disabled=!s().models.size||!!invalid;
  $('editor-shell').hidden=zed||desktop;
  for(const id of ['editor-copy','editor-export','editor-preview'])$(id).hidden=desktop;
  $('editor-status').textContent=invalid|| (prepared()?L('Configuration saved: ','Configuración guardada: ')+s().path:s().saved?L('Unsaved changes. Prepare this editor again.','Cambios sin guardar. Prepara este editor de nuevo.'):L('Select models and prepare the configuration.','Selecciona modelos y prepara la configuración.'));
  $('editor-preview').textContent=!desktop&&prepared()?(zed?L('Provider: kilo-local\nLocal key saved in the system credential store.','Proveedor: kilo-local\nClave local guardada en el almacén de credenciales del sistema.'):launch()):'';
  onChange();
 }
 function render(context,force=false){
  if(ctx.client!==context.client){$('editor-search').value='';$('editor-selected').checked=false;}
  ctx=context;
  if(desktopLibraryLoaded&&!working&&!desktopOptionsSyncing&&ctx.state!==desktopOptionsCheckedState&&typeof ctx.state?.claudeDesktopExperimentalModels==='boolean'&&desktopExperimental!==ctx.state.claudeDesktopExperimentalModels)void syncDesktopOptions();
  if(ctx.client==='claude-desktop'&&!desktopLibraryLoaded&&!desktopLibraryLoading)void loadDesktopLibrary();
  controls();
  const selected=s(),q=$('editor-search').value,only=$('editor-selected').checked;
  const all=new Map(ctx.catalog.filter(m=>ctx.client!=='claude-desktop'||claudeDesktopModelAllowed(m.id,desktopExperimental)).map(m=>[m.id,m]));for(const [id,m]of selected.models)all.set(id,mergeModelSelection(all.get(id),m));
  configureModelLab($('editor-lab'),[...all.values()],ctx.language);
  const matches=sortModels(filterModels(filterModelLab([...all.values()],$('editor-lab').value),q,false).filter(m=>!only||selected.models.has(m.id)),$('editor-sort').value);
  const next=JSON.stringify([ctx.client,ctx.language,matches,contextPreview(payload),working,ctx.client==='claude-desktop'?desktopExperimental:null]);if(next===signature)return;
  if(!force&&renderedClient===ctx.client&&!working&&$('editor-picker').contains(document.activeElement)&&document.activeElement.matches('input[type=text],input[type=number]'))return;
  // A background state refresh can render a changed name/reasoning preference
  // before the next input gains focus. Keep the user's limit panels expanded.
  const expandedLimits=renderedClient===ctx.client?new Set([...$('editor-picker').querySelectorAll('details[open]')].map(details=>details.dataset.editorLimits)):new Set();
  signature=next;renderedClient=ctx.client;
  const scroll=$('editor-picker').scrollTop;$('editor-picker').replaceChildren();
  for(const m of matches.slice(0,200)){
   const chosen=selected.models.has(m.id),entry=document.createElement('div');entry.className='codex-model-entry'+(chosen?' is-selected':'');
   const label=document.createElement('label');label.className='model-option';
   const check=document.createElement('input');check.type='checkbox';check.checked=chosen;check.disabled=working||(!chosen&&selected.models.size>=50);check.dataset.editorId=m.id;check.setAttribute('aria-label',m.name||m.id);
   check.addEventListener('change',()=>{if(check.checked){selected.models.set(m.id,{...m,name:(m.name||m.id).slice(0,80)});if(!selected.initial)selected.initial=m.id}else{selected.models.delete(m.id);if(selected.initial===m.id)selected.initial=selected.models.keys().next().value||''}render(ctx)});
   const text=document.createElement('span'),title=document.createElement('strong'),id=document.createElement('small');title.textContent=m.name||m.id;id.textContent=m.id;text.append(title,id);
   text.className='model-option-title';label.append(check,text,modelPriceDetails(m,ctx.language));entry.append(label);
   if(chosen){
    const value=selected.models.get(m.id),row=document.createElement('div');row.className='codex-row-controls';
    const name=document.createElement('input');name.type='text';name.value=value.name||value.id;name.maxLength=80;name.setAttribute('aria-label',L('Display name: ','Nombre visible: ')+m.id);name.dataset.editorName=m.id;name.disabled=working;
    name.addEventListener('input',()=>{value.name=name.value;title.textContent=name.value||m.id;controls()});row.append(name);
    const initial=document.createElement('button');initial.type='button';initial.className='codex-default-button';initial.textContent=selected.initial===m.id?L('★ Initial model','★ Modelo inicial'):L('Use on startup','Usar al iniciar');initial.setAttribute('aria-pressed',String(selected.initial===m.id));initial.dataset.editorInitial=m.id;initial.disabled=working;initial.addEventListener('click',()=>{selected.initial=m.id;if(ctx.client==='claude-desktop')desktopFallback=false;render(ctx)});row.append(initial);
    if(ctx.client==='omp'){
     const label=document.createElement('label'),effort=document.createElement('select');label.textContent=L('Reasoning','Razonamiento');effort.dataset.editorEffort=m.id;effort.setAttribute('aria-label',L('Reasoning: ','Razonamiento: ')+m.id);
     const levels=ompEfforts(value),auto=document.createElement('option');auto.value='';auto.textContent=L('Automatic','Automático');effort.append(auto);
     for(const level of levels){const option=document.createElement('option');option.value=level;option.textContent=level;effort.append(option)}
     effort.value=levels.includes(value.effort)?value.effort:'';effort.disabled=working||!levels.length;effort.addEventListener('change',()=>{value.effort=effort.value;controls()});label.append(effort);row.append(label);
    }
    if(ctx.client!=='claude-desktop'){
    row.append(contextControls(value,{language:ctx.language,catalogModel:ctx.catalog.find(model=>model.id===m.id),disabled:working,onChange:controls}));
    const limits=document.createElement('details'),summary=document.createElement('summary');limits.dataset.editorLimits=m.id;limits.open=expandedLimits.has(m.id);summary.textContent=L('Output limit','Límite de salida');limits.append(summary);
    for(const [field,title,fallback]of [['maxOutputTokens',L('Max output tokens (0 = automatic)','Salida máxima (0 = automática)'),0]]){
     const l=document.createElement('label'),input=document.createElement('input');l.textContent=title;input.type='number';input.min=field==='contextWindow'?'1024':'0';input.max='100000000';input.value=value[field]||fallback;input.setAttribute('aria-label',title+': '+m.id);input.disabled=working;input.addEventListener('input',()=>{value[field]=Number(input.value);const policy=row.querySelector('.context-policy');policy.replaceWith(contextControls(value,{language:ctx.language,catalogModel:ctx.catalog.find(model=>model.id===m.id),disabled:working,onChange:controls}));controls()});l.append(input);limits.append(l)
    }row.append(limits);
    }
    entry.append(row)
   }$('editor-picker').append(entry)
  }
  if(!matches.length){const empty=document.createElement('p');empty.textContent=L('No matches. Refresh the catalog or add a model by ID.','Sin resultados. Actualiza el catálogo o añade un modelo por ID.');$('editor-picker').append(empty)}
  $('editor-picker').scrollTop=scroll;
 }
 for(const id of ['editor-search','editor-selected'])$(id).addEventListener('input',()=>render(ctx));
 $('editor-sort').addEventListener('change',()=>{setModelSort($('editor-sort').value);$('editor-picker').scrollTop=0;render(ctx,true)});
 $('editor-lab').addEventListener('change',()=>{setModelLab($('editor-lab').value);$('editor-picker').scrollTop=0;render(ctx,true)});
 $('editor-shell').addEventListener('change',controls);
 $('editor-id').addEventListener('input',controls);
 $('editor-refresh').addEventListener('click',()=>refreshCatalog());
 $('editor-add').addEventListener('click',()=>{const id=$('editor-id').value.trim();if(!validModelID(id)||s().models.size>=50||ctx.client==='claude-desktop'&&!claudeDesktopModelAllowed(id,desktopExperimental))return;s().models.set(id,{...(ctx.catalog.find(m=>m.id===id)||{id,name:id})});if(!s().initial)s().initial=id;$('editor-search').value='';$('editor-selected').checked=true;$('editor-id').value='';render(ctx)});
 function applyDesktopLibrary(){
  const selection=selections['claude-desktop'];
  for(const model of selection.models.values())desktopNames.set(model.id,model.name);
  const filtered=claudeDesktopSelection(desktopLibrary.models||[],desktopLibrary.defaultModel||'',desktopExperimental);
  desktopOmitted=filtered.omitted;desktopFallback=filtered.fallback;
  selection.models=new Map(filtered.selection.models.map(model=>[model.id,{...model,name:desktopNames.get(model.id)??model.name}]));
  selection.initial=filtered.selection.initial;selection.saved=null;
 }
 async function syncDesktopOptions(){
  // A polled state may have been captured before the last options write.
  // Confirm it against options, and discard reads overtaken by a local toggle.
  const revision=desktopOptionsRevision;desktopOptionsSyncing=true;desktopOptionsCheckedState=ctx.state;
  try{
   const options=await api('claude-desktop/options');
   if(revision!==desktopOptionsRevision||working)return;
   const experimental=options.experimentalModels===true;
   if(desktopExperimental!==experimental){
    const source=await api('model-library');
    if(revision!==desktopOptionsRevision||working)return;
    desktopLibrary=source.library||{models:[],defaultModel:''};desktopExperimental=experimental;applyDesktopLibrary();
   }
   if(ctx.state)ctx.state.claudeDesktopExperimentalModels=experimental;
   desktopOptionsCheckedState=ctx.state;render(ctx,true);
  }catch(error){notify(error.message,true)}finally{desktopOptionsSyncing=false}
 }
 async function loadDesktopLibrary(){
  desktopLibraryLoading=true;working=true;
  try{
   const [result,options]=await Promise.all([api('model-library'),api('claude-desktop/options')]);
   desktopLibrary=result.library||{models:[],defaultModel:''};desktopExperimental=options.experimentalModels===true;
   if(ctx.state)ctx.state.claudeDesktopExperimentalModels=desktopExperimental;
   applyDesktopLibrary();
  }catch(error){notify(error.message,true)}finally{desktopLibraryLoaded=true;desktopLibraryLoading=false;working=false;render(ctx)}
 }
 $('claude-desktop-experimental').addEventListener('change',async()=>{
  if(working)return;
  const experimentalModels=$('claude-desktop-experimental').checked;desktopOptionsRevision++;desktopPending=experimentalModels;working=true;render(ctx);
  try{
   const source=await api('model-library');
   const options=await api('claude-desktop/options',{experimentalModels});
   desktopLibrary=source.library||{models:[],defaultModel:''};desktopExperimental=options.experimentalModels===true;
   if(ctx.state)ctx.state.claudeDesktopExperimentalModels=desktopExperimental;
   applyDesktopLibrary();$('editor-search').value='';$('editor-selected').checked=false;
  }catch(error){notify(error.message,true)}finally{desktopPending=null;working=false;render(ctx,true)}
 });
 async function prepare(){
  if(working)throw new Error(L('This editor is already being prepared.','Este editor ya se está preparando.'));
  const client=ctx.client,selection=s(),fp=fingerprint(),body=payload();working=true;render(ctx);
  try{const result=await api(endpoint(client),body);selection.path=result.configPath;selection.profileDir=result.profileDir;selection.saved=fp;notify(()=>L('Editor configuration saved.','Configuración del editor guardada.'))}finally{working=false;render(ctx)}
 }
 $('editor-save').addEventListener('click',()=>prepare().catch(e=>notify(e.message,true)));
 $('editor-load').addEventListener('click',async()=>{
  const client=ctx.client,selection=s();working=true;render(ctx);
  try{const result=await api(endpoint(client)),loaded=client==='claude-desktop'?claudeDesktopSelection(result.selection.models,result.selection.initial,desktopExperimental).selection:result.selection;selection.models=new Map(loaded.models.map(m=>[m.id,client==='claude-desktop'?m:contextModel(m,ctx.catalog.find(entry=>entry.id===m.id),true)]));selection.initial=loaded.initial;selection.path=result.configPath;selection.profileDir=result.profileDir;selection.saved=null;$('editor-selected').checked=true;$('editor-search').value='';notify(()=>L('Selection loaded. Prepare again to apply current connection settings.','Selección cargada. Prepara de nuevo para aplicar la conexión actual.'))}catch(e){notify(e.message,true)}finally{working=false;render(ctx)}
 });
 $('editor-copy').addEventListener('click',()=>prepared()&&copy(ctx.client==='zed'?ctx.state.localKey:launch()));
 $('editor-export').addEventListener('click',()=>copy((ctx.client==='omp'?ompConfig:clientConfig)({client:ctx.client,language:ctx.language,baseURL:ctx.state?.baseURL,zedBaseURL:ctx.state?.zedBaseURL,key:ctx.state?.localKey,model:s().initial,selectedModels:ctx.client==='omp'?[...s().models.values()]:payload().models})));
 return {render,setCatalog:catalog=>{for(const selection of Object.values(selections))syncContextModels(selection.models,catalog);},launchState:()=>({id:ctx.client,count:s().models.size,valid:!selectionError(),reason:selectionError(),ready:prepared(),fingerprint:fingerprint(),working,prepare})};
}

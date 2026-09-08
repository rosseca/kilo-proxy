import {validModelID,formatPrice,filterModels,sortModels,configureModelSort,setModelSort,mergeModelSelection,filterModelLab,configureModelLab,setModelLab} from './model-helper.mjs';
import {clientConfig} from './client-config.mjs';
export function editorPayload(models,initial) {
 return {models:models.map(m=>({id:m.id,name:(m.displayName||m.name||m.id).slice(0,80),contextWindow:m.contextWindow==null?200000:Number(m.contextWindow),maxOutputTokens:m.maxOutputTokens==null?0:Number(m.maxOutputTokens)})),initial};
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
export function createEditorHelper({api,notify,copy,refreshCatalog}){
 const $=id=>document.getElementById(id),selections=Object.fromEntries(['opencode','zed'].map(id=>[id,{models:new Map(),initial:'',saved:null,path:''}]));
 let ctx={client:'opencode',language:'en',catalog:[]},working=false,signature='',renderedClient='';
 const L=(en,es)=>ctx.language==='en'?en:es,s=()=>selections[ctx.client];
 const payload=()=>editorPayload([...s().models.values()],s().initial);
 const fingerprint=()=>JSON.stringify([ctx.client,payload(),ctx.state?.baseURL,ctx.state?.localKey]);
 const prepared=()=>s().saved===fingerprint();
 function controls(){
  const zed=ctx.client==='zed',name=zed?'Zed':'OpenCode';
  const labels={
   'editor-title':`${name} · `+L('select and prepare','selecciona y prepara'),
   'editor-intro':zed?L('Save multiple models and short names directly to Zed settings. Other providers and settings are retained.','Guarda varios modelos y nombres cortos directamente en los ajustes de Zed. Se conservan otros proveedores y ajustes.'):L('Prepare a dedicated OpenCode configuration with your models and local authentication.','Prepara una configuración propia de OpenCode con tus modelos y autenticación local.'),
   'editor-refresh':L('Refresh catalog','Actualizar catálogo'),'editor-load':L('Load saved selection','Cargar selección guardada'),'editor-save':working?L('Working…','Preparando…'):L('1. Prepare ','1. Preparar ')+name,
   'editor-add':L('Add model','Añadir modelo'),'editor-manual-title':L('Add by exact ID','Añadir por ID exacto'),'editor-selected-label':L('Selected only','Solo seleccionados'),
   'editor-copy':zed?L('2. Copy key for Zed','2. Copiar clave para Zed'):L('2. Copy launch command','2. Copiar arranque'),
   'editor-export':L('Copy configuration (optional)','Copiar configuración (opcional)'),
   'editor-next':zed?L('After preparing: in Zed, open “agent: open settings”, find kilo-local and paste the copied local key once. Zed stores it in its keychain. Select a model in the Agent panel. This configures Zed Agent, not edit prediction or external agents.','Después de preparar: abre «agent: open settings» en Zed, busca kilo-local y pega la clave local una vez. Zed la guarda en su llavero. Elige modelo en el panel Agent. Configura Zed Agent, no la predicción de código ni agentes externos.'):L('Run the command in a project terminal. Use /models to switch models. No /connect is needed: the protected profile stores only the local proxy key. Global and project settings still merge; project settings can override this profile.','Ejecuta el comando en una terminal del proyecto. Cambia con /models. No hace falta /connect: el perfil protegido guarda solo la clave local del proxy. Se combinan los ajustes globales y del proyecto; los del proyecto pueden prevalecer.'),
   'editor-limit-note':L('Uses Chat Completions. Model lists and saved settings do not verify generation or tool support. Context defaults to 200,000 for unknown models: review limits below. Changed files receive .bak backups.','Usa Chat Completions. La lista y los ajustes guardados no verifican generación ni herramientas. El contexto de modelos desconocidos se inicia en 200.000: revisa sus límites abajo. Los archivos modificados reciben copia .bak.')};
  for(const [id,text]of Object.entries(labels))$(id).textContent=text;
  $('editor-search').placeholder=L('Search model, provider or saved name','Buscar modelo, proveedor o nombre guardado');
  $('editor-sort-label').textContent=L('Sort by','Ordenar por');
  $('editor-lab-label').textContent=L('Lab','Laboratorio');
  configureModelSort($('editor-sort'),ctx.language);
  $('editor-save').disabled=working||!s().models.size||!ctx.state;
  $('editor-load').disabled=working;$('editor-refresh').disabled=working;$('editor-add').disabled=working;
  $('editor-copy').disabled=working||!prepared();$('editor-export').disabled=!s().models.size;
  $('editor-shell').hidden=zed;
  $('editor-status').textContent=prepared()?L('Configuration saved: ','Configuración guardada: ')+s().path:s().saved?L('Unsaved changes. Prepare this editor again.','Cambios sin guardar. Prepara este editor de nuevo.'):L('Select models and prepare the configuration.','Selecciona modelos y prepara la configuración.');
  $('editor-preview').textContent=prepared()?(zed?L('Provider: kilo-local\nAPI key: ••••••••••••••••','Proveedor: kilo-local\nAPI key: ••••••••••••••••'):editorLaunch(s().path,s().initial,$('editor-shell').value)):'';
 }
 function render(context,force=false){
  if(ctx.client!==context.client){$('editor-search').value='';$('editor-selected').checked=false;}
  ctx=context;controls();
  const selected=s(),q=$('editor-search').value,only=$('editor-selected').checked;
  const all=new Map(ctx.catalog.map(m=>[m.id,m]));for(const [id,m]of selected.models)all.set(id,mergeModelSelection(all.get(id),m));
  configureModelLab($('editor-lab'),[...all.values()],ctx.language);
  const matches=sortModels(filterModels(filterModelLab([...all.values()],$('editor-lab').value),q,false).filter(m=>!only||selected.models.has(m.id)),$('editor-sort').value);
  const next=JSON.stringify([ctx.client,ctx.language,matches,payload(),working]);if(next===signature)return;
  if(!force&&renderedClient===ctx.client&&!working&&$('editor-picker').contains(document.activeElement)&&document.activeElement.matches('input[type=text],input[type=number]'))return;
  signature=next;renderedClient=ctx.client;
  const scroll=$('editor-picker').scrollTop;$('editor-picker').replaceChildren();
  for(const m of matches.slice(0,200)){
   const chosen=selected.models.has(m.id),entry=document.createElement('div');entry.className='codex-model-entry';
   const label=document.createElement('label');label.className='model-option';
   const check=document.createElement('input');check.type='checkbox';check.checked=chosen;check.disabled=working||(!chosen&&selected.models.size>=50);check.dataset.editorId=m.id;check.setAttribute('aria-label',m.name||m.id);
   check.addEventListener('change',()=>{if(check.checked){selected.models.set(m.id,{...m,name:(m.name||m.id).slice(0,80)});if(!selected.initial)selected.initial=m.id}else{selected.models.delete(m.id);if(selected.initial===m.id)selected.initial=selected.models.keys().next().value||''}render(ctx)});
   const text=document.createElement('span'),title=document.createElement('strong'),id=document.createElement('small');title.textContent=m.name||m.id;id.textContent=m.id;text.append(title,id);
   const price=document.createElement('small');price.textContent=(formatPrice(m.inputPrice,ctx.language)||'—')+' / '+(formatPrice(m.outputPrice,ctx.language)||'—')+' USD / 1M';label.append(check,text,price);entry.append(label);
   if(chosen){
    const value=selected.models.get(m.id),row=document.createElement('div');row.className='codex-row-controls';
    const name=document.createElement('input');name.type='text';name.value=value.name||value.id;name.maxLength=80;name.setAttribute('aria-label',L('Display name: ','Nombre visible: ')+m.id);name.dataset.editorName=m.id;name.disabled=working;
    name.addEventListener('input',()=>{value.name=name.value;title.textContent=name.value||m.id;controls()});row.append(name);
    const initial=document.createElement('button');initial.type='button';initial.className='codex-default-button';initial.textContent=selected.initial===m.id?L('★ Initial model','★ Modelo inicial'):L('Use on startup','Usar al iniciar');initial.dataset.editorInitial=m.id;initial.disabled=working;initial.addEventListener('click',()=>{selected.initial=m.id;render(ctx)});row.append(initial);
    const limits=document.createElement('details'),summary=document.createElement('summary');summary.textContent=L('Context and output limits','Límites de contexto y salida');limits.append(summary);
    for(const [field,title,fallback]of [['contextWindow',L('Context tokens','Tokens de contexto'),200000],['maxOutputTokens',L('Max output tokens (0 = unspecified)','Salida máxima (0 = sin especificar)'),0]]){
     const l=document.createElement('label'),input=document.createElement('input');l.textContent=title;input.type='number';input.min=field==='contextWindow'?'1024':'0';input.max='100000000';input.value=value[field]||fallback;input.setAttribute('aria-label',title+': '+m.id);input.disabled=working;input.addEventListener('input',()=>{value[field]=Number(input.value);controls()});l.append(input);limits.append(l)
    }row.append(limits);entry.append(row)
   }$('editor-picker').append(entry)
  }
  if(!matches.length){const empty=document.createElement('p');empty.textContent=L('No matches. Refresh the catalog or add a model by ID.','Sin resultados. Actualiza el catálogo o añade un modelo por ID.');$('editor-picker').append(empty)}
  $('editor-picker').scrollTop=scroll;
 }
 for(const id of ['editor-search','editor-selected'])$(id).addEventListener('input',()=>render(ctx));
 $('editor-sort').addEventListener('change',()=>{setModelSort($('editor-sort').value);$('editor-picker').scrollTop=0;render(ctx,true)});
 $('editor-lab').addEventListener('change',()=>{setModelLab($('editor-lab').value);$('editor-picker').scrollTop=0;render(ctx,true)});
 $('editor-shell').addEventListener('change',controls);
 $('editor-refresh').addEventListener('click',()=>refreshCatalog());
 $('editor-add').addEventListener('click',()=>{const id=$('editor-id').value.trim();if(!validModelID(id)||s().models.size>=50)return;s().models.set(id,ctx.catalog.find(m=>m.id===id)||{id,name:id});if(!s().initial)s().initial=id;$('editor-search').value='';$('editor-selected').checked=true;$('editor-id').value='';render(ctx)});
 $('editor-save').addEventListener('click',async()=>{
  const client=ctx.client,selection=s(),fp=fingerprint(),body=payload();working=true;render(ctx);
  try{const result=await api('editors/'+client+'/profile',body);selection.path=result.configPath;selection.saved=fp;notify(()=>L('Editor configuration saved.','Configuración del editor guardada.'))}catch(e){notify(e.message,true)}finally{working=false;render(ctx)}
 });
 $('editor-load').addEventListener('click',async()=>{
  const client=ctx.client,selection=s();working=true;render(ctx);
  try{const result=await api('editors/'+client+'/profile');selection.models=new Map(result.selection.models.map(m=>[m.id,m]));selection.initial=result.selection.initial;selection.path=result.configPath;selection.saved=null;$('editor-selected').checked=true;$('editor-search').value='';notify(()=>L('Selection loaded. Prepare again to apply current connection settings.','Selección cargada. Prepara de nuevo para aplicar la conexión actual.'))}catch(e){notify(e.message,true)}finally{working=false;render(ctx)}
 });
 $('editor-copy').addEventListener('click',()=>prepared()&&copy(ctx.client==='zed'?ctx.state.localKey:editorLaunch(s().path,s().initial,$('editor-shell').value)));
 $('editor-export').addEventListener('click',()=>copy(clientConfig({client:ctx.client,language:ctx.language,baseURL:ctx.state?.baseURL,key:ctx.state?.localKey,model:s().initial,selectedModels:payload().models})));
 return {render};
}

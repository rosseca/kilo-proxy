import {codexCatalog,reasoningFor,codexDisplayName} from './codex-catalog.mjs';
import {writeClipboard} from './desktop-helper.mjs';
import {claudeCapabilities,claudeEfforts,claudeSelection} from './claude-helper.mjs';
import {validModelID,formatPrice} from './model-helper.mjs';

export function xcodeChatGuide(baseURL,key,language='en') {
 const url=baseURL.replace(/\/v1\/?$/,'')+'/xcode';
 return language==='en' ? `Xcode → Settings → Intelligence → Add a Chat Provider (or Add a Model Provider)\n\nChoose Internet Hosted to supply authentication for this local URL.\nURL: ${url}\nAPI Key Header: Authorization\nAPI Key: Bearer ${key}\n\nDo not append /v1: Xcode adds it. Keep Kilo Proxy running.\nXcode fetches the saved selection from ${url}/v1/models.\nSelect a model in Xcode. Re-add or refresh the provider if its list is stale.\nModels must support Chat Completions; listing does not verify generation access.` : `Xcode → Settings → Intelligence → Add a Chat Provider (o Add a Model Provider)\n\nElige Internet Hosted para introducir autenticación con esta URL local.\nURL: ${url}\nAPI Key Header: Authorization\nAPI Key: Bearer ${key}\n\nNo añadas /v1: lo añade Xcode. Mantén Kilo Proxy abierto.\nXcode obtiene la selección guardada de ${url}/v1/models.\nElige un modelo en Xcode. Actualiza o vuelve a añadir el proveedor si la lista no cambia.\nLos modelos deben admitir Chat Completions; listar no verifica el acceso al generar.`;
}
// Portable enum verified in Xcode's advertised Codex 0.106.0 source.
export function xcodeReasoning(model) {
 const r=reasoningFor(model),levels=r.levels.filter(x=>['none','minimal','low','medium','high','xhigh'].includes(x));
 return {levels,initial:levels.includes(r.initial)?r.initial:levels.includes('high')?'high':levels[0]||null};
}
export function xcodeSelectionPayload(variant,models,initial,aliases={}) {
 if(variant==='codex')return {catalog:codexCatalog(models.map(m=>({...m,reasoningLevels:xcodeReasoning(m).levels,defaultReasoning:xcodeReasoning(m).initial})),initial)};
 if(variant==='claude'){const ids=[initial,...models.map(m=>m.id).filter(id=>id!==initial)];aliases=Object.fromEntries(['sonnet','opus','haiku'].map((name,i)=>[name,aliases[name] || ids[i] || initial]));}
 return claudeSelection(models,initial,aliases,'installed');
}
export function createXcodeHelper({api,notify,refreshCatalog}) {
 const $=id=>document.getElementById(id), states=Object.fromEntries(['chat','codex','claude'].map(id=>[id,{models:new Map(),initial:'',aliases:{},saved:null}]));
 let variant='chat',context={catalog:[],language:'en'},installation=null,loading=false,saving=false,signature='',detected=false;
 const L=(en,es)=>context.language==='en'?en:es;
 const selection=()=>states[variant];
 const payload=(v=variant,s=selection())=>xcodeSelectionPayload(v,[...s.models.values()],s.initial,s.aliases);
 const fingerprint=(v=variant,s=selection())=>JSON.stringify([context.state?.baseURL,context.state?.localKey,v,payload(v,s),v==='claude'?installation?.claude:null]);
 const caps=()=>installation?.claude || claudeCapabilities();
 function status(){
  const s=selection(),ready=s.saved?.signature===fingerprint();
  $('xcode-save').disabled=saving||!s.models.size||(variant!=='chat'&&!installation?.available);
  $('xcode-save').textContent=saving?L('Preparing…','Preparando…'):L('1. Prepare ','1. Preparar ')+{chat:'Xcode Chat',codex:L('Codex in Xcode','Codex en Xcode'),claude:L('Claude in Xcode','Claude en Xcode')}[variant];
  $('xcode-status').textContent=ready?L('Saved: ','Guardado: ')+s.saved.path: s.saved?L('Unsaved changes. Prepare this variant again.','Hay cambios sin guardar. Prepara esta variante de nuevo.'):L('Choose models and prepare this variant.','Elige modelos y prepara esta variante.');
  $('xcode-guide').textContent=variant==='chat'?xcodeChatGuide(context.state?.baseURL||'http://127.0.0.1:8877/v1','kl_local_••••••••••••••••',context.language):L('Close Xcode before preparing its agent profile. Then reopen Xcode, enable/install the agent in Settings → Intelligence and start a new conversation. Keep Kilo Proxy running.\n\nThe profile contains only the local proxy key; no terminal environment is required. These settings affect the agent inside Xcode. Existing unrelated settings are kept, with .bak backups.\n\nXcode may override the active model or restrict its picker. A saved profile is not proof of a successful gateway request.','Cierra Xcode antes de preparar el perfil del agente. Después abre Xcode, activa/instala el agente en Settings → Intelligence e inicia una conversación nueva. Mantén Kilo Proxy abierto.\n\nEl perfil contiene solo la clave local del proxy; no requiere variables de terminal. Estos ajustes afectan al agente dentro de Xcode. Conservamos los demás ajustes con copias .bak.\n\nXcode puede imponer el modelo activo o limitar su selector. Un perfil guardado no verifica una petición al gateway.')+'\n\n'+(variant==='codex'?'~/Library/Developer/Xcode/CodingAssistant/codex/config.toml\n~/Library/Developer/Xcode/CodingAssistant/codex/models.json':'~/Library/Developer/Xcode/CodingAssistant/ClaudeAgentConfig/settings.json');
  $('xcode-copy').hidden=variant!=='chat';$('xcode-copy').disabled=!s.models.size;
 }
 function render(next=context){
  context=next;
  if(!detected){detected=true;queueMicrotask(detect);}
  status();
  const sig=JSON.stringify([context.catalog,context.language,variant,payload(),installation,loading,$('xcode-search').value]);
  if(sig===signature)return;signature=sig;
  $('xcode-title').textContent=L('Xcode: chat and coding agents','Xcode: chat y agentes de programación');
  $('xcode-intro').textContent=L('Chat Completions is the message API used by Xcode Chat. Codex uses Responses, and Claude uses Messages. Each variant has its own model selection.','Chat Completions es la API de mensajes que usa Xcode Chat. Codex usa Responses y Claude usa Messages. Cada variante tiene su propia selección de modelos.');
  $('xcode-refresh').textContent=L('Refresh catalog','Actualizar catálogo');
  $('xcode-agent-warning').hidden=variant==='chat';$('xcode-agent-warning').textContent=L('Close Xcode before preparing an agent profile. Reopen it and start a new conversation after saving.','Cierra Xcode antes de preparar el perfil de un agente. Vuelve a abrirlo e inicia una conversación nueva después de guardar.');
  $('xcode-search').placeholder=L('Search model or provider','Buscar modelo o proveedor');
  $('xcode-manual-label').textContent=L('Add an exact model ID','Añadir un ID de modelo exacto');
  $('xcode-add').textContent=L('Add','Añadir');$('xcode-load').textContent=L('Load saved selection','Cargar selección guardada');
  $('xcode-detect').textContent=loading?L('Checking…','Comprobando…'):L('Detect Xcode','Detectar Xcode');
  $('xcode-detect').disabled=loading;
  $('xcode-copy').textContent=L('2. Copy connection details','2. Copiar conexión');
  $('xcode-compatibility').textContent=!installation?L('Detect Xcode before preparing agent profiles. Chat works through the local proxy.','Detecta Xcode antes de preparar los perfiles de agentes. Chat funciona a través del proxy local.'):!installation.available?L('Xcode was not found on this computer. Agent preparation requires macOS and Xcode.','No se encontró Xcode en este ordenador. Preparar agentes requiere macOS y Xcode.'):`Xcode ${installation.version} · Codex ${installation.codexVersion||'?'} · Claude ${caps().version||'?'} — `+L('versions advertised by Xcode; downloaded agents may differ.','versiones anunciadas por Xcode; los agentes descargados pueden variar.');
  $('xcode-limit-note').textContent=variant==='claude'&&!caps().picker?L('This Claude version uses up to three aliases (Sonnet, Opus, Haiku). Map them below. Xcode may show its built-in labels. Only the initial model can set global reasoning.','Esta versión de Claude usa hasta tres alias (Sonnet, Opus, Haiku). Asígnalos abajo. Xcode puede mostrar sus nombres propios. Solo el modelo inicial puede fijar el razonamiento global.'):variant==='chat'?L('Up to 50 models. Exact gateway IDs are preserved. Short names are hints; Xcode may display IDs. Provider registration in Xcode is manual.','Hasta 50 modelos. Se conservan los IDs exactos del gateway. Los nombres cortos son orientativos; Xcode puede mostrar los IDs. El alta del proveedor en Xcode es manual.'):L('Up to 50 models. The profile includes names and supported reasoning. Xcode controls which choices appear in its own interface. Its verified Codex catalog supports levels up to xhigh; max and ultra are omitted.','Hasta 50 modelos. El perfil incluye nombres y razonamiento compatible. Xcode controla qué opciones aparecen en su interfaz. Su catálogo de Codex verificado admite niveles hasta xhigh; se omiten max y ultra.');
  document.querySelectorAll('[data-xcode-variant]').forEach(b=>{b.setAttribute('aria-selected',String(b.dataset.xcodeVariant===variant));b.textContent=b.dataset.xcodeVariant==='chat'?'Chat':b.dataset.xcodeVariant==='codex'?L('Codex in Xcode','Codex en Xcode'):L('Claude in Xcode','Claude en Xcode')});
  const s=selection(),available=new Map(context.catalog.map(m=>[m.id,m]));for(const [id,m]of s.models)available.set(id,{...available.get(id),...m});
  const list=$('xcode-picker'),scroll=list.scrollTop,focus=document.activeElement?.dataset.xcodeFocus;
  list.replaceChildren();const query=$('xcode-search').value.toLowerCase();
  const matches=[...available.values()].filter(m=>(m.id+' '+(m.name||'')+' '+(m.displayName||'')).toLowerCase().includes(query)).sort((a,b)=>Number(s.models.has(b.id))-Number(s.models.has(a.id)));
  for(const model of matches){
   const chosen=s.models.has(model.id),entry=document.createElement('div');entry.className='codex-model-entry'+(chosen?' is-selected':'');
   const row=document.createElement('label');row.className='model-option';const check=document.createElement('input');check.type='checkbox';check.checked=chosen;check.dataset.xcodeFocus='choose:'+model.id;
   const title=document.createElement('span'),strong=document.createElement('strong'),small=document.createElement('small');strong.textContent=codexDisplayName(model);small.textContent=model.id;title.append(strong,small);
   const price=document.createElement('span');price.className='model-option-prices';price.textContent=(formatPrice(model.inputPrice,context.language)||'—')+' / '+(formatPrice(model.outputPrice,context.language)||'—')+' USD / 1M';row.append(check,title,price);entry.append(row);
   check.addEventListener('change',()=>{if(check.checked){const max=variant==='claude'&&!caps().picker?3:50;if(s.models.size>=max){check.checked=false;notify(L('Maximum ','Máximo ')+max+L(' models',' modelos'),true);return}s.models.set(model.id,{...model});if(!s.initial)s.initial=model.id}else{s.models.delete(model.id);if(s.initial===model.id)s.initial=s.models.keys().next().value||'';for(const a of Object.keys(s.aliases))if(s.aliases[a]===model.id)s.aliases[a]=''}render()});
   if(chosen){
    const m=s.models.get(model.id),controls=document.createElement('div');controls.className='codex-row-controls';
    const label=document.createElement('label');label.className='codex-name-label';label.textContent=L('Short name','Nombre corto');const input=document.createElement('input');input.type='text';input.value=m.displayName||'';input.maxLength=80;input.dataset.xcodeFocus='name:'+m.id;input.className='codex-name-input';label.append(input);controls.append(label);
    input.addEventListener('input',()=>{m.displayName=input.value;strong.textContent=codexDisplayName(m);signature=JSON.stringify([context.catalog,context.language,variant,payload(),installation,loading,$('xcode-search').value]);status()});
    const initial=document.createElement('button');initial.type='button';initial.className='codex-default-button';initial.setAttribute('aria-pressed',String(s.initial===m.id));initial.textContent=s.initial===m.id?L('★ Initial model','★ Modelo inicial'):L('Use first','Usar al iniciar');initial.dataset.xcodeFocus='initial:'+m.id;initial.addEventListener('click',()=>{s.initial=m.id;if(variant==='claude'&&!caps().perModelEffort)for(const other of s.models.values())if(other.id!==m.id)delete other.effort;render()});controls.append(initial);
    if(variant!=='chat'){
     const label=document.createElement('label');label.textContent=L('Reasoning','Razonamiento');const select=document.createElement('select');select.dataset.xcodeFocus='effort:'+m.id;
     const values=variant==='codex'?xcodeReasoning(m).levels:claudeEfforts(m.id,caps());const initialValue=variant==='codex'?xcodeReasoning(m).initial:m.effort||'';
     for(const value of ['',...values]){const o=document.createElement('option');o.value=value;o.textContent=value||L('Automatic','Automático');select.append(o)}select.value=initialValue||'';select.disabled=!values.length||(variant==='claude'&&!caps().perModelEffort&&s.initial!==m.id);
     select.addEventListener('change',()=>{if(variant==='codex'){m.reasoningLevels=values;m.defaultReasoning=select.value||xcodeReasoning(m).initial}else{m.effort=select.value}status()});label.append(select);controls.append(label);
    }
    entry.append(controls);
   }
   list.append(entry);
  }
  if(!matches.length){const p=document.createElement('p');p.textContent=L('No models. Refresh the catalog or add an exact ID below.','Sin modelos. Actualiza el catálogo o añade un ID exacto abajo.');list.append(p)}
  list.scrollTop=scroll;if(focus)[...list.querySelectorAll('[data-xcode-focus]')].find(e=>e.dataset.xcodeFocus===focus)?.focus({preventScroll:true});
  const aliases=$('xcode-aliases');aliases.hidden=variant!=='claude';aliases.replaceChildren();
  if(variant==='claude')for(const alias of ['sonnet','opus','haiku']){const label=document.createElement('label');label.textContent=alias;const select=document.createElement('select');select.dataset.alias=alias;for(const m of s.models.values()){const option=document.createElement('option');option.value=m.id;option.textContent=m.displayName||m.id;select.append(option)}select.value=payload().aliases[alias]||s.initial;select.addEventListener('change',()=>{s.aliases[alias]=select.value;status()});label.append(select);aliases.append(label)}
 }
 document.querySelectorAll('[data-xcode-variant]').forEach(b=>b.addEventListener('click',()=>{variant=b.dataset.xcodeVariant;$('xcode-search').value='';render()}));
 $('xcode-search').addEventListener('input',()=>render());
 $('xcode-refresh').addEventListener('click',async()=>{$('xcode-refresh').disabled=true;try{await refreshCatalog()}finally{$('xcode-refresh').disabled=false;render()}});
 $('xcode-add').addEventListener('click',()=>{const id=$('xcode-id').value.trim(),s=selection();if(!validModelID(id)){notify(L('Enter a valid model ID','Introduce un ID de modelo válido'),true);return}const max=variant==='claude'&&!caps().picker?3:50;if(!s.models.has(id)&&s.models.size>=max){notify(L('Model limit reached','Límite de modelos alcanzado'),true);return}s.models.set(id,s.models.get(id)||{...context.catalog.find(m=>m.id===id),id});if(!s.initial)s.initial=id;$('xcode-id').value='';$('xcode-search').value='';render()});
 async function detect(){loading=true;render();try{installation=await api('xcode/info')}catch(e){notify(e.message,true)}finally{loading=false;render()}}
 $('xcode-detect').addEventListener('click',detect);
 $('xcode-save').addEventListener('click',async()=>{const target=variant,s=selection(),sent=fingerprint();saving=true;render();try{const result=await api('xcode/'+target,payload(target,s));s.saved={signature:sent,path:result.profileDir}}catch(e){notify(e.message,true)}finally{saving=false;render()}});
 $('xcode-load').addEventListener('click',async()=>{const target=variant,s=selection();try{const data=await api('xcode/'+target);s.models.clear();if(target==='codex'){for(const m of data.models)s.models.set(m.slug,{id:m.slug,displayName:m.display_name,contextWindow:m.context_window,inputModalities:m.input_modalities,reasoningLevels:(m.supported_reasoning_levels||[]).map(r=>r.effort),defaultReasoning:m.default_reasoning_level});s.initial=data.models[0]?.slug||''}else{for(const m of data.models)s.models.set(m.id,m);s.initial=data.initial;s.aliases=data.aliases||{}}s.saved=null;render()}catch(e){notify(e.message,true)}});
 $('xcode-copy').addEventListener('click',async()=>{try{await writeClipboard(xcodeChatGuide(context.state?.baseURL||'',context.state?.localKey||'',context.language));notify(L('Connection copied with the local key','Conexión copiada con la clave local'))}catch(e){notify(L('Clipboard unavailable','Portapapeles no disponible'),true)}});
 return {render};
}

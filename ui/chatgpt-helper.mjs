export const chatGPTVerificationURL = 'https://auth.openai.com/codex/device';
export const chatGPTConnected = state => state?.chatgpt?.connected === true;
export const chatGPTReady = state => typeof state?.chatgptReady === 'boolean' ? state.chatgptReady : chatGPTConnected(state) && state?.chatgpt?.status !== 'pending';
export const connectionReady = state => typeof state?.connectionReady === 'boolean' ? state.connectionReady : !!(state?.hasKey && state?.orgId?.trim() && !['starting','pending'].includes(state?.auth?.status));
export const validChatGPTVerificationURL = value => value === chatGPTVerificationURL;

export function quotaWindows(quota) {
  return ['primary','secondary'].flatMap(key => {
    const value=quota?.[key];
    if (typeof value?.usedPercent !== 'number' || !Number.isFinite(value.usedPercent)) return [];
    return [{key,usedPercent:Math.max(0,Math.min(100,value.usedPercent)),windowDurationMins:value.windowDurationMins,resetsAt:value.resetsAt}];
  });
}

export function renderChatGPT(document,state,language,busy) {
  const $=id=>document.getElementById(id),L=(en,es)=>language==='es'?es:en;
  const account=state?.chatgpt||{}, pending=account.status==='pending';
  $('connection-title').textContent=L('Your connection','Tu conexión');
  $('chatgpt-account').hidden=false;
  $('chatgpt-title').textContent=L('ChatGPT subscription','Suscripción de ChatGPT');
  $('chatgpt-intro').textContent=L('Experimental direct ChatGPT subscription connection alongside Kilo. Both accounts share the same model library and agents. Models labelled ChatGPT use subscription quota; Kilo models use Kilo credit.','Conexión directa experimental con la suscripción de ChatGPT junto a Kilo. Ambas cuentas comparten biblioteca y agentes. Los modelos etiquetados ChatGPT usan la cuota de suscripción; los modelos de Kilo usan su crédito.');
  $('chatgpt-identity').textContent=account.connected?[L('ChatGPT connected','ChatGPT conectado'),account.email,account.plan].filter(Boolean).join(' · '):L('ChatGPT is not connected','ChatGPT no está conectado');
  $('chatgpt-login').textContent=account.connected?L('Reconnect ChatGPT','Volver a conectar ChatGPT'):L('Sign in with ChatGPT','Iniciar sesión con ChatGPT');
  $('chatgpt-login').hidden=(account.connected&&account.status!=='error')||pending;$('chatgpt-login').disabled=busy||state.running;
  $('chatgpt-logout').textContent=L('Disconnect ChatGPT','Desconectar ChatGPT');
  $('chatgpt-logout').hidden=!account.connected;$('chatgpt-logout').disabled=busy||state.running;
  $('chatgpt-pending').hidden=!pending;
  $('chatgpt-pending-label').textContent=L('Enter this code on the authorization page. This screen updates when you finish.','Introduce este código en la página de autorización. Esta pantalla se actualiza al terminar.');
  $('chatgpt-code').textContent=account.code||'';
  const link=$('chatgpt-verify');link.textContent=L('Open ChatGPT authorization ↗','Abrir autorización de ChatGPT ↗');link.hidden=!validChatGPTVerificationURL(account.verificationUrl);
  if(!link.hidden)link.href=chatGPTVerificationURL;else link.removeAttribute('href');
  $('chatgpt-cancel').textContent=L('Cancel ChatGPT login','Cancelar login de ChatGPT');$('chatgpt-cancel').disabled=busy;
  $('chatgpt-error').hidden=account.status!=='error'&&!account.error;
  $('chatgpt-error').textContent=L('Could not connect to ChatGPT. Try signing in again.','No se ha podido conectar con ChatGPT. Vuelve a iniciar sesión.');
  $('chatgpt-privacy').textContent=L('Subscription credentials stay on this computer. Agents use only the local proxy key.','Las credenciales de suscripción se quedan en este equipo. Los agentes solo usan la clave local del proxy.');

}

export function renderSubscriptionUsage(root,state,language,busy) {
  const L=(en,es)=>language==='es'?es:en, doc=root.ownerDocument, account=state?.chatgpt||{},quota=account.quota||{};
  const make=(tag,text,cls)=>{const el=doc.createElement(tag);el.textContent=text;if(cls)el.className=cls;return el;};
  const heading=make('div','', 'account-heading'), title=make('h2',L('Subscription usage','Uso de suscripción'));title.id='subscription-usage-title';
  const refresh=make('button',L('Refresh subscription usage','Actualizar uso de la suscripción'),'secondary-button');refresh.type='button';refresh.id='refresh-chatgpt';refresh.disabled=busy||!account.connected;heading.append(title,refresh);
  const note=make('p',L('ChatGPT quota is separate from Kilo credit. Token counts describe usage, not a monetary charge.','La cuota de ChatGPT es independiente del crédito de Kilo. Los tokens describen el uso, no un cargo monetario.'),'field-help');
  const grid=make('div','','account-metrics');
  for(const window of quotaWindows(quota)){
    const card=make('div','','account-metric'),label=window.key==='primary'?L('Current window','Ventana actual'):L('Longer window','Ventana ampliada');
    card.append(make('span',label+(window.windowDurationMins>0?' · '+window.windowDurationMins+' min':'')),make('strong',window.usedPercent.toLocaleString(language,{maximumFractionDigits:1})+L('% used','% usado')));
    if(window.resetsAt>0)card.append(make('p',L('Resets: ','Se restablece: ')+new Date(window.resetsAt*1000).toLocaleString(language),'field-help'));
    grid.append(card);
  }
  if(!grid.childElementCount)grid.append(make('p',L('Usage limits unavailable','Límites de uso no disponibles'),'field-help'));
  const status=make('p',quota.stale||quota.available===false&&quotaWindows(quota).length?L('Last reported usage is out of date. Refresh to check your current quota.','El último uso informado está desactualizado. Actualiza para consultar tu cuota actual.'):quota.error?L('Could not refresh subscription usage.','No se ha podido actualizar el uso de la suscripción.'):'','account-status');status.hidden=!status.textContent;status.setAttribute('role','status');
  root.replaceChildren(heading,note,grid,status);
  if(state.chatgptUsageHistory){
    const history=state.chatgptUsageHistory,local=make('div','','account-local-history');local.append(make('h3',L('Subscription requests on this device','Peticiones de suscripción en este equipo')));
    for(const [key,title] of [['today',L('Today','Hoy')],['yesterday',L('Yesterday','Ayer')],['last7Days',L('Last 7 days','Últimos 7 días')]]){
      const value=history[key]||{},tokens=value.withTokens>0?Number((value.input||0)+(value.output||0)).toLocaleString(language):'—';
      local.append(make('p',title+': '+(value.requests||0)+L(' requests · ',' peticiones · ')+tokens+' tokens','field-help'));
    }
    local.append(make('p',L('Observed through this proxy only; quota also includes usage elsewhere.','Solo lo observado a través de este proxy; la cuota también incluye uso en otros lugares.'),'field-help'));
    if(history.error)local.append(make('p',L('Subscription history could not be saved.','No se ha podido guardar el historial de suscripción.'),'account-status'));
    root.append(local);
  }
}

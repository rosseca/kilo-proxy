import {reportedSpend} from './usage-helper.mjs';

// Preserve Kilo's decimal strings. Zero is valid only in a fresh successful
// response; missing, failed and stale data must not masquerade as free usage.
export function billingMoney(value, status = 'ready', stale = false) {
  if (status !== 'ready' || stale || typeof value !== 'string' || !/^-?\d+(?:\.\d+)?$/.test(value)) return '—';
  const clean = value.includes('.') ? value.replace(/0+$/, '').replace(/\.$/, '') : value;
  return '$' + (clean === '-0' ? '0' : clean);
}

export function billingStatus(status, stale, language = 'en', error = '') {
  const L = (en, es) => language === 'es' ? es : en;
  if (stale) return L('Out of date. Refresh to check the latest balance and usage.', 'Desactualizado. Actualiza para consultar el saldo y uso actuales.') + (error ? ' ' + error : '');
  if (status === 'ready') return '';
  if (status === 'loading') return L('Checking Kilo…', 'Consultando Kilo…');
  if (status === 'signed_out') return L('Sign in to Kilo in Connection to see your balance and billed usage.', 'Inicia sesión en Kilo desde Conexión para ver el saldo y el uso facturado.');
  return L('Kilo data is unavailable. Check your connection or sign in again.', 'Los datos de Kilo no están disponibles. Comprueba la conexión o vuelve a iniciar sesión.') + (error ? ' ' + error : '');
}

export function renderAccountUsage(root, state, language = 'en', busy = false) {
  const L = (en, es) => language === 'es' ? es : en;
  const billing = state?.billing || {}, usage = billing.usage || {}, history = state?.usageHistory || {};
  const doc = root.ownerDocument;
  const opened = new Set([...root.querySelectorAll('details[open]')].map(node => node.id));
  const make = (tag, text, className) => { const node = doc.createElement(tag); if (text !== undefined) node.textContent = text; if (className) node.className = className; return node; };
  const note = text => make('p', text, 'field-help');
  const metric = (label, amount, caption, id) => { const node = make('div', undefined, 'account-metric'); const value = make('strong', amount); value.id = id; node.append(make('span', label), value, note(caption)); return node; };
  const grid = (...children) => { const node = make('div', undefined, 'account-metrics'); node.append(...children); return node; };
  const updated = value => { const date = new Date(value); return value && Number.isFinite(date.getTime()) ? date.toLocaleString(language, {dateStyle:'medium',timeStyle:'short'}) : L('Not checked yet', 'Todavía sin consultar'); };
  const addStatus = (parent, value, id) => { const status = make('p', billingStatus(value.status, value.stale, language, value.error), 'account-status'); status.id = id; status.setAttribute('role', 'status'); status.hidden = !status.textContent; parent.append(status); };
  const section = make('div');
  const heading = make('div', undefined, 'account-heading');
  const title = make('h2', L('Kilo account', 'Cuenta de Kilo')); title.id = 'account-usage-title';
  const refresh = make('button', L('Refresh balance and usage', 'Actualizar saldo y uso'), 'secondary-button'); refresh.type = 'button'; refresh.id = 'refresh-billing'; refresh.disabled = busy || billing.status === 'loading';
  heading.append(title, refresh); section.append(heading);
  const team = billing.scope === 'organization';
  section.append(metric(L('Remaining balance', 'Saldo restante'), billingMoney(billing.balanceUSD, billing.status, billing.stale), team ? L('Shared team credit', 'Crédito compartido del equipo') : L('Personal Kilo credit', 'Crédito personal de Kilo'), 'account-balance'));
  section.append(note(team ? L("This balance is shared by everyone in the selected team. It is not your personal spending limit and does not include a BYOK provider's balance.", 'Este saldo se comparte entre los miembros del equipo seleccionado. No es tu límite personal de gasto ni incluye el saldo de un proveedor BYOK externo.') : L("Available credit in Kilo. A separate BYOK provider's balance is not included.", 'Crédito disponible en Kilo. No incluye el saldo de un proveedor BYOK externo.')));
  section.append(note(L('Last checked: ', 'Última consulta: ') + updated(billing.fetchedAt))); addStatus(section, billing, 'account-balance-status');
  section.append(make('h3', L('Your Kilo charges', 'Tus cargos de Kilo')), grid(
    metric(L('Today · UTC', 'Hoy · UTC'), billingMoney(usage.todayUSD, usage.status, usage.stale), L('Billed by Kilo', 'Facturado por Kilo'), 'account-spend-today'),
    metric(L('Yesterday · UTC', 'Ayer · UTC'), billingMoney(usage.yesterdayUSD, usage.status, usage.stale), L('Billed by Kilo', 'Facturado por Kilo'), 'account-spend-yesterday'),
    metric(L('Last 30 days', 'Últimos 30 días'), billingMoney(usage.last30DaysUSD, usage.status, usage.stale), L('Including today · UTC', 'Incluye hoy · UTC'), 'account-spend-month'),
  ));
  section.append(note(L('Your own billed usage in the selected account or team, including activity outside this proxy. Kilo may report recent usage with a delay. BYOK inference costs can differ from Kilo credit deductions.', 'Tu propio uso facturado en la cuenta o equipo seleccionado, incluida la actividad fuera de este proxy. Kilo puede informar del uso reciente con retraso. Los costes de inferencia BYOK pueden diferir de los cargos al crédito de Kilo.')));
  addStatus(section, usage, 'account-usage-status');
  if (usage.fetchedAt) section.append(note(L('Usage checked: ', 'Uso consultado: ') + updated(usage.fetchedAt)));
  const dailyTable = (id, title, headers, rows) => {
    const details = make('details'); details.id = id; details.open = opened.has(id); details.append(make('summary', title));
    const wrap = make('div', undefined, 'table-wrap'); const table = make('table'); const head = make('thead'); const tr = make('tr'); headers.forEach(text => tr.append(make('th', text))); head.append(tr); const body = make('tbody');
    rows.forEach(values => { const row = make('tr'); values.forEach(text => row.append(make('td', String(text)))); body.append(row); });
    table.append(head, body); wrap.append(table); details.append(wrap); return details;
  };
  if (usage.status === 'ready' && !usage.stale && usage.days?.length) section.append(dailyTable('account-daily', L('Daily Kilo usage', 'Uso diario en Kilo'), [L('Date · UTC', 'Fecha · UTC'), L('Kilo charges', 'Cargos de Kilo'), L('Requests', 'Peticiones'), L('Input / output', 'Entrada / salida'), L('Cached tokens', 'Tokens en caché')], usage.days.map(day => [day.date, billingMoney(day.costUSD), day.requests, `${day.input} / ${day.output}`, day.cached])));

  const appearance = make('label', L('Menu bar / tray display', 'Mostrar en la barra de menús / bandeja'), 'account-tray');
  const select = make('select'); select.id = 'account-tray-display'; select.disabled = busy;
  for (const [value,label] of [['icon',L('K icon','Icono K')],['spend',L('Session cost','Coste de esta sesión')],['balance',L('Kilo account balance','Saldo de la cuenta de Kilo')],['chatgpt-quota',L('Remaining ChatGPT quota','Cuota de ChatGPT restante')]]) { const option = make('option', label); option.value = value; select.append(option); }
  select.value = ['icon','spend','balance','chatgpt-quota'].includes(state?.trayDisplay) ? state.trayDisplay : 'icon'; appearance.append(select); section.append(appearance, note(L('Keeps the K icon. macOS shows the amount beside it; Windows shows it in the tooltip and menu. Linux support depends on your desktop. Unavailable or stale balances show a dash.', 'Mantiene el icono K. macOS muestra el importe a su lado; Windows lo muestra al pasar el cursor y en el menú. En Linux depende del escritorio. El saldo no disponible o desactualizado se muestra como un guion.')));

  const local = make('div', undefined, 'account-local-history'); local.append(make('h3', L('Observed on this device', 'Observado en este dispositivo')));
  const localMetric = (label, summary, id) => { const spend = reportedSpend(summary, language); return metric(label, spend.amount, (spend.partial ? spend.label + " · " : "") + spend.coverage, id); };
  local.append(grid(localMetric(L('Today','Hoy'),history.today,'local-spend-today'),localMetric(L('Yesterday','Ayer'),history.yesterday,'local-spend-yesterday'),localMetric(L('Last 7 days','Últimos 7 días'),history.last7Days,'local-spend-week')));
  local.append(note(L('Saved daily totals for requests through this proxy, for the current account and team. Includes provider inference costs, which may differ from Kilo charges. No prompts, headers or responses are stored in this history.', 'Totales diarios guardados de peticiones a través de este proxy, para la cuenta y equipo actuales. Incluye costes de inferencia del proveedor, que pueden diferir de los cargos de Kilo. Este historial no guarda prompts, cabeceras ni respuestas.')));
  local.append(note(history.startedAt ? L('Recorded since: ','Registrado desde: ') + updated(history.startedAt) + ' · ' + (history.timezone || '') : L('History starts with your next request. Earlier activity cannot be recovered locally.', 'El historial comienza con tu próxima petición. La actividad anterior no se puede recuperar localmente.')));
  if (history.error) local.append(note(L('History could not be saved: ', 'No se pudo guardar el historial: ') + history.error));
  if (history.days?.length) local.append(dailyTable('local-daily', L('Daily local history','Historial local diario'), [L('Local date','Fecha local'),L('Reported inference cost','Coste de inferencia informado'),L('Requests','Peticiones'),L('Cost coverage','Cobertura de coste')],history.days.map(day => { const spend=reportedSpend(day,language); return [day.date, (spend.partial ? spend.label + ': ' : '') + spend.amount, day.requests, `${day.priced}/${day.requests}`]; })));
  root.replaceChildren(section, local);
}

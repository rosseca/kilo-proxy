const releasePrefix = 'https://github.com/rosseca/kilo-proxy/releases/tag/';

export function updateReleaseURL(value) {
  return typeof value === 'string' && /^https:\/\/github\.com\/rosseca\/kilo-proxy\/releases\/tag\/v(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)$/.test(value) ? value : '';
}

export function updateView(update = {}, language = 'en', currentVersion = '') {
  const L = (en, es) => language === 'es' ? es : en;
  const releaseURL = updateReleaseURL(update.releaseUrl);
  const available = update.available === true && !!releaseURL;
  const latest = releaseURL.slice(releasePrefix.length);
  const checked = typeof update.checkedAt === 'string' && Number.isFinite(Date.parse(update.checkedAt));
  let status = L('Not checked yet.', 'Todavía sin comprobar.');
  if (update.checking) status = L('Checking GitHub…', 'Comprobando GitHub…');
  else if (update.error || (update.available === true && !releaseURL)) status = L('Could not check for updates. Try again later.', 'No se pudieron comprobar las actualizaciones. Inténtalo más tarde.');
  else if (available) status = L(`Kilo Proxy ${latest} is available.`, `Kilo Proxy ${latest} está disponible.`);
  else if (checked && update.latestVersion && update.available === false) status = L('You’re up to date.', 'Tienes la versión más reciente.');
  return {
    available, releaseURL, status,
    current: L('Installed version: ', 'Versión instalada: ') + (update.currentVersion || currentVersion || '—'),
    checked: checked ? L('Last checked: ', 'Última comprobación: ') + new Date(update.checkedAt).toLocaleString(language === 'es' ? 'es-ES' : 'en-US') : '',
    title: L('App updates', 'Actualizaciones de la app'),
    description: L('Checks GitHub on startup and every 6 hours for a newer stable release. Download and install updates when you are ready.', 'Comprueba GitHub al arrancar y cada 6 horas para buscar una versión estable más reciente. Descarga e instala las actualizaciones cuando quieras.'),
    banner: L(`Kilo Proxy ${latest} is available`, `Kilo Proxy ${latest} está disponible`),
    download: L('Download update ↗', 'Descargar actualización ↗'),
    check: update.checking ? L('Checking…', 'Comprobando…') : L('Check for updates', 'Buscar actualizaciones'),
  };
}

export function renderUpdates(document, state, language, pending = false, requestError = false) {
  const $ = id => document.getElementById(id);
  const update = {...state?.update};
  if (requestError) update.error = 'request_failed';
  const view = updateView(update, language, state?.version);
  $('update-notice').hidden = !view.available;
  $('update-notice-title').textContent = view.banner;
  $('updates-title').textContent = view.title;
  $('updates-description').textContent = view.description;
  $('updates-current').textContent = view.current;
  $('updates-status').textContent = view.status;
  $('updates-status').classList.toggle('update-error', !!update.error);
  $('updates-checked').textContent = view.checked;
  $('updates-checked').hidden = !view.checked;
  $('updates-check').textContent = view.check;
  $('updates-check').disabled = pending || !!update.checking;
  for (const id of ['update-notice-download', 'updates-download']) {
    $(id).hidden = !view.available;
    $(id).textContent = view.download;
    if (view.available) $(id).href = view.releaseURL;
    else $(id).removeAttribute('href');
  }
}

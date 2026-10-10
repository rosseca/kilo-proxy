const releasePrefix = 'https://github.com/rosseca/kilo-proxy/releases/tag/';

export function updateReleaseURL(value) {
  return typeof value === 'string' && /^https:\/\/github\.com\/rosseca\/kilo-proxy\/releases\/tag\/v(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)$/.test(value) ? value : '';
}

export function packageUpdateFailureCode(code) {
  return ['update_start_failed', 'package_update_failed', 'installation_changed', 'package_manager_unavailable'].includes(code);
}

export function updateView(update = {}, language = 'en', currentVersion = '', install = {}) {
  const L = (en, es) => language === 'es' ? es : en;
  const releaseURL = updateReleaseURL(update.releaseUrl);
  const available = update.available === true && !!releaseURL;
  const latest = releaseURL.slice(releasePrefix.length);
  const manager = ['brew-cask', 'brew-formula'].includes(update.installMethod) ? 'Homebrew' : update.installMethod === 'apt' ? 'APT' : '';
  const canInstall = available && update.canInstall === true && !!manager && latest === `v${update.latestVersion}` && !update.checking && !update.error;
  const working = install.pending === true || install.started === true || update.installing === true;
  const confirming = canInstall && !working && install.version === update.latestVersion && install.method === update.installMethod;
  const messages = {
    detecting_installation: L('Checking how Kilo Proxy was installed…', 'Comprobando cómo se instaló Kilo Proxy…'),
    package_update_failed: L('The last package update failed. Review its terminal output, then try again or update manually.', 'La última actualización del paquete falló. Revisa su terminal e inténtalo de nuevo o actualiza manualmente.'),
    update_start_failed: L('Could not open the update terminal. Try again or update manually.', 'No se pudo abrir la terminal de actualización. Inténtalo de nuevo o actualiza manualmente.'),
    run_as_user: L('Open Kilo Proxy as your regular user to update it.', 'Abre Kilo Proxy con tu usuario habitual para actualizarlo.'),
    installation_changed: L('The installation changed. Reopen Kilo Proxy before updating.', 'La instalación ha cambiado. Vuelve a abrir Kilo Proxy antes de actualizar.'),
    package_manager_unavailable: L('The package manager is unavailable. Update manually or download the release.', 'El gestor de paquetes no está disponible. Actualiza manualmente o descarga la versión.'),
  };
  const checked = typeof update.checkedAt === 'string' && Number.isFinite(Date.parse(update.checkedAt));
  let status = L('Not checked yet.', 'Todavía sin comprobar.');
  if (install.pending) status = L('Opening the update terminal…', 'Abriendo la terminal de actualización…');
  else if (install.handedOff) status = L('Follow the update terminal. You can close this panel once Kilo Proxy has closed.', 'Sigue la terminal de actualización. Puedes cerrar este panel cuando Kilo Proxy se haya cerrado.');
  else if (working) status = L('Waiting for the update terminal…', 'Esperando a la terminal de actualización…');
  else if (install.failed) status = L('Could not start the update. Try again or download it manually.', 'No se pudo iniciar la actualización. Inténtalo de nuevo o descárgala manualmente.');
  else if (update.checking) status = L('Checking GitHub…', 'Comprobando GitHub…');
  else if (update.error || (update.available === true && !releaseURL)) status = L('Could not check for updates. Try again later.', 'No se pudieron comprobar las actualizaciones. Inténtalo más tarde.');
  else if (available) status = L(`Kilo Proxy ${latest} is available.`, `Kilo Proxy ${latest} está disponible.`);
  else if (checked && update.latestVersion && update.available === false) status = L('You’re up to date.', 'Tienes la versión más reciente.');
  return {
    available, releaseURL, status, canInstall, working, confirming,
    current: L('Installed version: ', 'Versión instalada: ') + (update.currentVersion || currentVersion || '—'),
    checked: checked ? L('Last checked: ', 'Última comprobación: ') + new Date(update.checkedAt).toLocaleString(language === 'es' ? 'es-ES' : 'en-US') : '',
    title: L('App updates', 'Actualizaciones de la app'),
    description: L('Checks GitHub on startup and every 6 hours. Package updates run only after your confirmation; direct downloads remain available.', 'Comprueba GitHub al arrancar y cada 6 horas. Las actualizaciones del paquete sólo se ejecutan tras tu confirmación; también puedes descargarlo manualmente.'),
    method: manager ? L(`Installed with ${manager}. Updates run in a visible terminal.`, `Instalado con ${manager}. Las actualizaciones se ejecutan en una terminal visible.`) : '',
    message: !working && Object.hasOwn(messages, update.installMessage) ? messages[update.installMessage] : '',
    unavailable: manager && !update.canInstall && !working ? L('Package update is unavailable for this installation. Use your package manager manually or download the release.', 'La actualización del paquete no está disponible para esta instalación. Usa el gestor manualmente o descarga la versión.') : '',
    install: L('Update and restart', 'Actualizar y reiniciar'),
    confirmation: L(`Update to ${update.latestVersion} with ${manager}? This closes Kilo Proxy and interrupts active requests. A terminal opens; APT may ask for your administrator password there. Kilo Proxy restarts after a successful update.`, `¿Actualizar a ${update.latestVersion} con ${manager}? Se cerrará Kilo Proxy y se interrumpirán las peticiones activas. Se abrirá una terminal; APT puede pedirte allí la contraseña de administrador. Kilo Proxy se reiniciará si la actualización termina correctamente.`),
    confirm: L('Confirm update and restart', 'Confirmar actualización y reinicio'),
    cancel: L('Cancel', 'Cancelar'),
    banner: L(`Kilo Proxy ${latest} is available`, `Kilo Proxy ${latest} está disponible`),
    download: L('Download update ↗', 'Descargar actualización ↗'),
    check: update.checking ? L('Checking…', 'Comprobando…') : L('Check for updates', 'Buscar actualizaciones'),
  };
}

export function renderUpdates(document, state, language, pending = false, requestError = false, install = {}) {
  const $ = id => document.getElementById(id);
  const update = {...state?.update};
  if (requestError) update.error = 'request_failed';
  const view = updateView(update, language, state?.version, install);
  $('update-notice').hidden = !view.available;
  $('update-notice-title').textContent = view.banner;
  $('updates-title').textContent = view.title;
  $('updates-description').textContent = view.description;
  $('updates-current').textContent = view.current;
  $('updates-status').textContent = view.status;
  $('updates-status').classList.toggle('update-error', !!update.error || (!!install.failed && !view.working));
  $('updates-checked').textContent = view.checked;
  $('updates-checked').hidden = !view.checked;
  $('updates-check').textContent = view.check;
  $('updates-check').disabled = pending || !!update.checking || view.working;
  $('updates-method').textContent = view.method;
  $('updates-method').hidden = !view.method;
  $('updates-unavailable').textContent = view.unavailable;
  $('updates-unavailable').hidden = !view.unavailable;
  $('updates-message').textContent = view.message;
  $('updates-message').hidden = !view.message;
  for (const id of ['update-notice-install', 'updates-install']) {
    $(id).hidden = !view.canInstall;
    $(id).textContent = view.install;
    $(id).disabled = view.working;
  }
  $('updates-confirmation').hidden = !view.confirming;
  $('updates-confirmation-text').textContent = view.confirmation;
  $('updates-confirm').textContent = view.confirm;
  $('updates-confirm').disabled = view.working;
  $('updates-cancel').textContent = view.cancel;
  for (const id of ['update-notice-download', 'updates-download']) {
    $(id).hidden = !view.available;
    $(id).textContent = view.download;
    if (view.available) $(id).href = view.releaseURL;
    else $(id).removeAttribute('href');
  }
}

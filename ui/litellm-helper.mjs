import {translate} from './i18n.mjs';

export const liteLLMReady = state => state?.litellm?.ready === true;

function browserAuthorizationURL(value, base) {
  if (typeof value !== 'string') return '';
  try {
    const authorization = new URL(value);
    const gateway = new URL(base);
    const loopback = ['localhost','[::1]'].includes(authorization.hostname) || /^127\.(?:\d+\.){2}\d+$/.test(authorization.hostname);
    if (authorization.origin === gateway.origin && !authorization.username && !authorization.password && !authorization.hash &&
      (authorization.protocol === 'https:' || authorization.protocol === 'http:' && loopback)) return authorization.href;
  } catch {}
  return '';
}

export function createLiteLLMHelper(document, update) {
  const $ = id => document.getElementById(id);
  let state, language = 'en', busy = false, editing = false, saving = false, failure = '';
  let authorizationURL = '';
  const L = (en, es) => language === 'es' ? es : en;
  const form = $('litellm-form'), url = $('litellm-url'), key = $('litellm-key');
  const withoutKey = $('litellm-without-key'), edit = $('litellm-edit');
  const authMode = $('litellm-auth-mode'), browser = $('litellm-auth-browser');
  const logout = $('litellm-logout'), pendingBox = $('litellm-login-pending'), loginLink = $('litellm-login-link');

  function draw() {
    const connection = state?.litellm;
    const configured = Boolean(connection?.url);
    const browserAccount = connection?.authMode === 'browser';
    const login = state?.litellmLogin;
    const pending = login?.status === 'pending';
    $('kilo-enabled-row').hidden = typeof state?.kiloEnabled !== 'boolean';
    $('kilo-enabled').checked = state?.kiloEnabled !== false;
    $('kilo-enabled').disabled = busy;
    $('litellm-account').hidden = !connection;
    $('litellm-enabled-row').hidden = !configured || browserAccount && !connection.keySaved;
    $('litellm-enabled').checked = connection?.enabled === true;
    $('litellm-enabled').disabled = busy || pending || !configured;
    let status = L('Not configured', 'Sin configurar');
    if (configured) {
      const count = connection.modelsCount;
      const available = Number.isInteger(count)
        ? ' · ' + count.toLocaleString(language) + (count === 1 ? L(' model available', ' modelo disponible') : L(' models available', ' modelos disponibles'))
        : '';
      const headline = browserAccount && !connection.keySaved ? L('Signed out', 'Sesión cerrada')
        : !connection.enabled ? L('Paused', 'En pausa') : connection.ready
        ? L('Connected', 'Conectada') + available
        : L('Connection needs attention', 'Revisa la conexión');
      status = headline + ' · ' + connection.url;
    }
    $('litellm-status').textContent = status;
    edit.textContent = browserAccount && !connection.hasKey ? L('Reconnect LiteLLM', 'Volver a conectar LiteLLM')
      : configured ? L('Edit LiteLLM', 'Editar LiteLLM') : L('Add LiteLLM', 'Añadir LiteLLM');
    edit.hidden = editing || pending;
    edit.disabled = busy;
    edit.setAttribute('aria-expanded', String(editing && !pending));
    logout.hidden = !browserAccount || !connection.keySaved || editing || pending;
    logout.disabled = busy;
    form.hidden = !editing || pending;
    url.disabled = busy;
    authMode.disabled = busy;
    $('litellm-key-fields').hidden = browser.checked;
    $('litellm-browser-help').hidden = !browser.checked;
    withoutKey.disabled = busy || browser.checked;
    $('litellm-without-key-row').hidden = !configured;
    key.disabled = busy || browser.checked || withoutKey.checked;
    $('litellm-key-help').textContent = browserAccount
      ? L('Enter a virtual key or choose to connect without one.', 'Introduce una clave virtual o elige conectar sin clave.')
      : connection?.hasKey && configured
      ? L('Leave blank to keep the key for this address. Enter the new server’s key when changing the URL.', 'Déjala vacía para conservar la clave de esta dirección. Si cambias la URL, introduce la clave del nuevo servidor.')
      : connection?.keySaved
        ? L('Enter your virtual key to reconnect.', 'Introduce tu clave virtual para volver a conectar.')
        : L('Enter a key if your gateway requires one.', 'Introduce una clave si tu gateway la requiere.');
    $('litellm-save').textContent = browser.checked
      ? saving ? L('Preparing sign-in…', 'Preparando el acceso…') : L('Sign in in your browser', 'Iniciar sesión en el navegador')
      : saving ? L('Checking connection…', 'Comprobando conexión…') : L('Test and save', 'Probar y guardar');
    $('litellm-save').disabled = busy;
    $('litellm-cancel').disabled = busy;
    pendingBox.hidden = !pending;
    loginLink.hidden = !pending || !authorizationURL;
    if (loginLink.hidden) loginLink.removeAttribute('href');
    else loginLink.href = authorizationURL;
    $('litellm-login-cancel').disabled = busy;
    const error = failure || (login?.status === 'error' && (login.error || L('LiteLLM sign-in failed.', 'No se pudo iniciar sesión en LiteLLM.')));
    $('litellm-error').textContent = error ? translate(error, language) : '';
    $('litellm-error').hidden = !error;
  }

  async function send(path, draft) {
    if (busy) return null;
    failure = '';
    try {
      return await update(path, draft);
    } catch (error) {
      failure = error.message || L('Could not complete the operation.', 'No se pudo completar la operación.');
      draw();
      return null;
    }
  }

  function closeForm(restoreFocus = true) {
    editing = false;
    key.value = '';
    withoutKey.checked = false;
    failure = '';
    draw();
    if (restoreFocus) edit.focus();
  }
  edit.addEventListener('click', () => {
    url.value = state?.litellm?.url || '';
    browser.checked = state?.litellm?.authMode === 'browser';
    $('litellm-auth-key').checked = !browser.checked;
    key.value = '';
    withoutKey.checked = false;
    editing = true;
    failure = '';
    draw();
    url.focus();
  });
  $('litellm-cancel').addEventListener('click', () => closeForm());
  authMode.addEventListener('change', () => {
    if (browser.checked) {
      key.value = '';
      withoutKey.checked = false;
    }
    draw();
  });
  withoutKey.addEventListener('change', () => {
    if (withoutKey.checked) key.value = '';
    draw();
  });
  form.addEventListener('submit', async event => {
    event.preventDefault();
    if (busy) return;
    const draft = {url: url.value.trim()};
    saving = true;
    if (browser.checked) {
      const result = await send('providers/litellm/login', draft);
      saving = false;
      if (result && state?.litellmLogin?.status === 'pending') {
        authorizationURL = browserAuthorizationURL(result.authorizationURL, draft.url);
        if (!authorizationURL) failure = L('The LiteLLM sign-in link is invalid. Cancel and try again.', 'El enlace de acceso de LiteLLM no es válido. Cancela y vuelve a intentarlo.');
      }
      draw();
      if (!loginLink.hidden) loginLink.focus();
      return;
    }
    if (withoutKey.checked || key.value.trim()) draft.apiKey = key.value.trim();
    const confirmed = await send('providers/litellm', draft);
    saving = false;
    if (confirmed) closeForm();
    else draw();
  });
  $('litellm-login-cancel').addEventListener('click', async () => {
    if (await send('providers/litellm/login/cancel', {})) {
      if (editing) url.focus();
      else edit.focus();
    }
  });
  logout.addEventListener('click', async () => {
    if (await send('providers/litellm/logout', {})) edit.focus();
  });
  for (const [id, provider] of [['kilo-enabled', 'kilo'], ['litellm-enabled', 'litellm']]) {
    $(id).addEventListener('change', async event => {
      await send('providers/' + provider + '/active', {enabled: event.target.checked});
      draw();
    });
  }
  return (nextState, nextLanguage, working) => {
    const settled = nextState?.litellmLogin?.status !== 'pending';
    const completed = authorizationURL && settled && nextState?.litellmLogin?.status === 'success';
    const restoreFocus = pendingBox.contains(document.activeElement);
    if (settled) authorizationURL = '';
    state = nextState;
    language = nextLanguage;
    busy = working;
    if (completed && editing) closeForm(restoreFocus);
    else {
      draw();
      if (settled && restoreFocus) (editing ? url : edit).focus();
    }
  };
}

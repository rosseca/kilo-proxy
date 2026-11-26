import {translate} from './i18n.mjs';

export const liteLLMReady = state => state?.litellm?.ready === true;

export function createLiteLLMHelper(document, update) {
  const $ = id => document.getElementById(id);
  let state, language = 'en', busy = false, editing = false, saving = false, failure = '';
  const L = (en, es) => language === 'es' ? es : en;
  const form = $('litellm-form'), url = $('litellm-url'), key = $('litellm-key');
  const withoutKey = $('litellm-without-key'), edit = $('litellm-edit');

  function draw() {
    const connection = state?.litellm;
    const configured = Boolean(connection?.url);
    $('kilo-enabled-row').hidden = typeof state?.kiloEnabled !== 'boolean';
    $('kilo-enabled').checked = state?.kiloEnabled !== false;
    $('kilo-enabled').disabled = busy;
    $('litellm-account').hidden = !connection;
    $('litellm-enabled-row').hidden = !configured;
    $('litellm-enabled').checked = connection?.enabled === true;
    $('litellm-enabled').disabled = busy || !configured;
    let status = L('Not configured', 'Sin configurar');
    if (configured) {
      const count = connection.modelsCount;
      const available = Number.isInteger(count)
        ? ' · ' + count.toLocaleString(language) + (count === 1 ? L(' model available', ' modelo disponible') : L(' models available', ' modelos disponibles'))
        : '';
      const headline = !connection.enabled ? L('Paused', 'En pausa') : connection.ready
        ? L('Connected', 'Conectada') + available
        : L('Connection needs attention', 'Revisa la conexión');
      status = headline + ' · ' + connection.url;
    }
    $('litellm-status').textContent = status;
    edit.textContent = configured ? L('Edit LiteLLM', 'Editar LiteLLM') : L('Add LiteLLM', 'Añadir LiteLLM');
    edit.hidden = editing;
    edit.disabled = busy;
    edit.setAttribute('aria-expanded', String(editing));
    form.hidden = !editing;
    url.disabled = busy;
    withoutKey.disabled = busy;
    $('litellm-without-key-row').hidden = !configured;
    key.disabled = busy || withoutKey.checked;
    $('litellm-key-help').textContent = connection?.hasKey && configured
      ? L('Leave blank to keep the key for this address. Enter the new server’s key when changing the URL.', 'Déjala vacía para conservar la clave de esta dirección. Si cambias la URL, introduce la clave del nuevo servidor.')
      : connection?.keySaved
        ? L('Enter your virtual key to reconnect.', 'Introduce tu clave virtual para volver a conectar.')
        : L('Enter a key if your gateway requires one.', 'Introduce una clave si tu gateway la requiere.');
    $('litellm-save').textContent = saving ? L('Checking connection…', 'Comprobando conexión…') : L('Test and save', 'Probar y guardar');
    $('litellm-save').disabled = busy;
    $('litellm-cancel').disabled = busy;
    $('litellm-error').textContent = translate(failure, language);
    $('litellm-error').hidden = !failure;
  }

  async function send(path, draft) {
    if (busy) return false;
    failure = '';
    try {
      await update(path, draft);
      return true;
    } catch (error) {
      failure = error.message || L('Could not complete the operation.', 'No se pudo completar la operación.');
      draw();
      return false;
    }
  }

  function closeForm() {
    editing = false;
    key.value = '';
    withoutKey.checked = false;
    failure = '';
    draw();
    edit.focus();
  }
  edit.addEventListener('click', () => {
    url.value = state?.litellm?.url || '';
    key.value = '';
    withoutKey.checked = false;
    editing = true;
    failure = '';
    draw();
    url.focus();
  });
  $('litellm-cancel').addEventListener('click', closeForm);
  withoutKey.addEventListener('change', () => {
    if (withoutKey.checked) key.value = '';
    draw();
  });
  form.addEventListener('submit', async event => {
    event.preventDefault();
    if (busy) return;
    const draft = {url: url.value.trim()};
    if (withoutKey.checked || key.value.trim()) draft.apiKey = key.value.trim();
    saving = true;
    const confirmed = await send('providers/litellm', draft);
    saving = false;
    if (confirmed) closeForm();
    else draw();
  });
  for (const [id, provider] of [['kilo-enabled', 'kilo'], ['litellm-enabled', 'litellm']]) {
    $(id).addEventListener('change', async event => {
      await send('providers/' + provider + '/active', {enabled: event.target.checked});
      draw();
    });
  }
  return (nextState, nextLanguage, working) => {
    state = nextState;
    language = nextLanguage;
    busy = working;
    draw();
  };
}

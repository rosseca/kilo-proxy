let desktopAPI;
export function configureDesktop(api, enabled) {
  desktopAPI = enabled ? api : undefined;
}

export async function writeClipboard(text) {
  if (desktopAPI) await desktopAPI('desktop/copy', {text});
  else await navigator.clipboard.writeText(text);
}

export async function openExternal(url) {
  if (desktopAPI) await desktopAPI('desktop/open', {url});
  else window.open(url, '_blank', 'noopener,noreferrer');
}

export function bindDesktopLinks(document, onError) {
  document.addEventListener('click', event => {
    const link = event.target.closest?.('a[href]');
    if (!desktopAPI || !link || !/^https?:/.test(link.href)) return;
    event.preventDefault();
    openExternal(link.href).catch(onError);
  });
}

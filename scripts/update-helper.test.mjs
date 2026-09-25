import {test} from 'node:test';
import assert from 'node:assert/strict';
import {updateReleaseURL, updateView} from '../ui/update-helper.mjs';

const current = {currentVersion:'0.50.0',latestVersion:'0.51.0',checkedAt:'2026-09-25T19:00:00Z',releaseUrl:'https://github.com/rosseca/kilo-proxy/releases/tag/v0.51.0',available:true};

test('update links stay on a stable release of this repository', () => {
  assert.equal(updateReleaseURL(current.releaseUrl), current.releaseUrl);
  for (const value of [undefined, 'javascript:alert(1)', current.releaseUrl + '?redirect=other', current.releaseUrl + '#other', current.releaseUrl.replace('github.com','github.com.evil.test'), current.releaseUrl.replace('/rosseca/','/other/'), current.releaseUrl.replace('v0.51.0','v0.51.0-rc.1'), current.releaseUrl.replace('v0.51.0','v00.51.0')]) {
    assert.equal(updateReleaseURL(value), '');
    assert.equal(updateView({...current,releaseUrl:value}).available, false);
  }
});

test('unknown, checking and failed checks never claim the app is current', () => {
  assert.equal(updateView().status,'Not checked yet.');
  assert.equal(updateView({...current,checking:true}).status,'Checking GitHub…');
  assert.equal(updateView({...current,error:'GitHub unavailable',available:false}).status,'Could not check for updates. Try again later.');
  assert.equal(updateView({available:false,checkedAt:'invalid',latestVersion:'0.50.0'}).status,'Not checked yet.');
  assert.equal(updateView({available:false,checkedAt:current.checkedAt}).status,'Not checked yet.');
});

test('available updates and successful current checks are localized', () => {
  const english = updateView(current), spanish = updateView(current,'es');
  assert.equal(english.status,'Kilo Proxy v0.51.0 is available.');
  assert.equal(english.current,'Installed version: 0.50.0');
  assert.equal(spanish.status,'Kilo Proxy v0.51.0 está disponible.');
  assert.equal(spanish.download,'Descargar actualización ↗');
  assert.match(spanish.checked,/Última comprobación:/);
  assert.equal(updateView({...current,available:false}).status,'You’re up to date.');
  assert.equal(updateView({...current,available:false},'es').status,'Tienes la versión más reciente.');
});

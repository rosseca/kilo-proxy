import {test} from 'node:test';
import assert from 'node:assert/strict';
import {updateReleaseURL, updateView, packageUpdateFailureCode} from '../ui/update-helper.mjs';

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

test('package update requires a trusted matching release and a supported manager', () => {
  const managed = {...current, installMethod: 'brew-formula', canInstall: true};
  assert.equal(updateView(managed).canInstall, true);
  assert.equal(updateView({...managed, installMethod: 'brew-cask'}).canInstall, true);
  assert.equal(updateView({...managed, installMethod: 'apt'}).canInstall, true);
  for (const change of [{canInstall:false}, {installMethod:'manual'}, {installMethod:'other'}, {latestVersion:'0.52.0'}, {releaseUrl:'https://example.invalid/v0.51.0'}, {available:false}, {checking:true}, {error:'offline'}]) {
    assert.equal(updateView({...managed,...change}).canInstall, false);
  }
  assert.equal(updateView(current).canInstall, false, 'older API state stays manual');
});

test('package confirmation binds version and manager and explains shutdown in both languages', () => {
  const managed = {...current, installMethod:'apt', canInstall:true};
  const confirmation = {version:'0.51.0', method:'apt'};
  for (const language of ['en','es']) {
    const view = updateView(managed,language,'',confirmation);
    assert.equal(view.confirming,true);
    assert.match(view.confirmation,/0\.51\.0.*APT/);
    assert.match(view.confirmation,language==='en' ? /interrupts active requests/ : /interrumpirán las peticiones activas/);
    assert.equal(view.install,language==='en' ? 'Update and restart' : 'Actualizar y reiniciar');
    assert.equal(updateView({...managed,installMethod:'brew-formula'},language,'',confirmation).confirming,false);
    assert.equal(updateView({...managed,latestVersion:'0.52.0',releaseUrl:current.releaseUrl.replace('0.51.0','0.52.0')},language,'',confirmation).confirming,false);
  }
});

test('handoff progress disables consent and reports failure without claiming installation', () => {
  const managed = {...current, installMethod:'apt', canInstall:true};
  const pending = updateView(managed,'en','',{pending:true});
  assert.equal(pending.working,true);
  assert.equal(pending.confirming,false);
  assert.equal(pending.status,'Opening the update terminal…');
  assert.equal(updateView(managed,'es','',{started:true}).status,'Esperando a la terminal de actualización…');
  assert.match(updateView(managed,'es','',{started:true,handedOff:true}).status,/Puedes cerrar este panel cuando Kilo Proxy se haya cerrado/);
  assert.match(updateView(managed,'en','',{failed:true}).status,/Could not start the update/);
  assert.equal(updateView(managed,'en','',{failed:true}).working,false);
  assert.equal(updateView({...managed,installing:true}).working,true);
});

test('package result messages are localized codes and never display arbitrary backend content', () => {
  const managed = {...current, installMethod:'apt', canInstall:true, installMessage:'package_update_failed'};
  assert.match(updateView(managed).message,/last package update failed/);
  assert.match(updateView(managed,'es').message,/última actualización del paquete falló/);
  assert.equal(updateView({...managed,installMessage:'<img src=x onerror=alert(1)>'}).message,'');
  assert.equal(updateView({...managed,installMessage:'constructor'}).message,'');
  assert.equal(updateView(managed,'en','',{pending:true}).message,'');
  for (const code of ['update_start_failed','package_update_failed','installation_changed','package_manager_unavailable']) assert.equal(packageUpdateFailureCode(code),true);
  for (const code of ['update_starting','managed_installation','constructor',undefined]) assert.equal(packageUpdateFailureCode(code),false);
});

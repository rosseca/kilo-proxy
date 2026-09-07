#!/usr/bin/env python3
"""Open the native release ZIP through LaunchServices and check app startup/quit.

Requires a macOS graphical login session. Uses a fresh temporary profile, no
saved keychain credentials, no browser, and no upstream inference requests.
"""
import argparse
import json
import os
from pathlib import Path
import platform
import re
import signal
import subprocess
import tempfile
import time
import urllib.request

from package import ROOT


def running_application(bundle):
    # NSRunningApplication.finishedLaunching confirms native startup completed;
    # an HTTP listener alone would miss a stalled AppKit/LaunchServices launch.
    script = '''ObjC.import("AppKit");
var apps=$.NSWorkspace.sharedWorkspace.runningApplications;
var result=null;
for(var i=0;i<apps.count;i++){
    var a=apps.objectAtIndex(i);
    if(ObjC.unwrap(a.bundleIdentifier)==="ai.kilo.local-proxy" && ObjC.unwrap(a.bundleURL.path)===BUNDLE){
        result={pid:Number(a.processIdentifier),finishedLaunching:Boolean(a.finishedLaunching)};
        break;
    }
}
JSON.stringify(result);'''.replace('BUNDLE', json.dumps(str(bundle)))
    result = subprocess.run(['/usr/bin/osascript', '-l', 'JavaScript', '-e', script],
                            check=True, capture_output=True, text=True, timeout=10)
    return json.loads(result.stdout)


def wait_for(check, description, seconds=30):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = check()
        if value:
            return value
        time.sleep(0.25)
    raise RuntimeError('Timed out waiting for ' + description)


def smoke(directory, version):
    if platform.system() != 'Darwin':
        raise RuntimeError('This launch test requires macOS with a graphical login session')
    arch = {'arm64': 'arm64', 'x86_64': 'amd64'}[platform.machine()]
    name = f'kilo-local-{version}-darwin-{arch}'
    archive = directory / (name + '.zip')
    with tempfile.TemporaryDirectory(prefix='kilo-launch-') as temporary:
        root = Path(temporary).resolve()
        subprocess.run(['/usr/bin/ditto', '-x', '-k', str(archive), str(root)], check=True)
        bundle = root / name / 'Kilo Local.app'
        subprocess.run(['/usr/bin/codesign', '--verify', '--deep', '--strict',
                        '--verbose=2', str(bundle)], check=True)
        stdout, stderr = root / 'stdout', root / 'stderr'
        for path in (stdout, stderr):
            path.touch(mode=0o600)
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        base, token = None, None

        def request(path, method='GET'):
            req = urllib.request.Request(base + path, method=method,
                                         headers={'Authorization': 'Bearer ' + token})
            with opener.open(req, timeout=5) as response:
                return json.load(response)

        try:
            subprocess.run(['/usr/bin/open', '-n', '--stdout', str(stdout), '--stderr', str(stderr),
                            str(bundle), '--args', '--no-browser', '--config-dir', str(root / 'profile')],
                           check=True, capture_output=True, text=True, timeout=35)
            match = wait_for(lambda: re.search(r'Control panel: (http://127\.0\.0\.1:\d+)/#([a-f0-9]{64})',
                                              stdout.read_text()), 'the control panel')
            base, token = match.groups()
            state = request('/api/state')
            if state['version'] != version:
                raise RuntimeError('The opened app has an unexpected version')

            def finished():
                app = running_application(bundle)
                return app and app['finishedLaunching']

            wait_for(finished, 'native application launch completion')
            if 'System tray error' in stderr.read_text():
                raise RuntimeError('The native tray failed to initialize')
            request('/api/quit', 'POST')
            wait_for(lambda: running_application(bundle) is None, 'native application shutdown')
            print(f'Passed: extracted {arch} app signature, LaunchServices startup, native event loop, '
                  f'authenticated panel ({version}), and graceful quit.')
        finally:
            # Only terminate the unique temporary bundle launched by this test.
            app = running_application(bundle)
            if app:
                if base:
                    try:
                        request('/api/quit', 'POST')
                    except (OSError, ValueError):
                        pass
                try:
                    os.kill(app['pid'], signal.SIGTERM)
                except ProcessLookupError:
                    pass
                try:
                    wait_for(lambda: running_application(bundle) is None, 'test cleanup', seconds=5)
                except RuntimeError:
                    current = running_application(bundle)
                    if current and current['pid'] == app['pid']:
                        os.kill(app['pid'], signal.SIGKILL)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=Path, default=ROOT / 'dist')
    parser.add_argument('--version', default=(ROOT / 'VERSION').read_text().strip())
    args = parser.parse_args()
    smoke(args.directory.resolve(), args.version)


if __name__ == '__main__':
    main()

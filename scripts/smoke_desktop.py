#!/usr/bin/env python3
"""Exercise the actual native window, clipboard, proxy and tray action lifecycle."""
import argparse
import json
import os
from pathlib import Path
import platform
import subprocess
import stat
import tarfile
import tempfile
import zipfile

REQUIRED = {
    'rendered-ui-and-authenticated-backend', 'window-and-tray-language-en',
    'window-and-tray-language-es', 'native-clipboard-via-ui', 'proxy-start-via-ui',
    'close-keeps-proxy', 'tray-reopen-preserves-session', 'tray-stop',
}


def windows_desktop_context():
    """Read the smoke process's shell context without starting or altering it."""
    import ctypes
    from ctypes import wintypes

    user32 = ctypes.WinDLL('user32', use_last_error=True)
    kernel32 = ctypes.WinDLL('kernel32', use_last_error=True)
    user32.FindWindowW.argtypes = [wintypes.LPCWSTR, wintypes.LPCWSTR]
    user32.FindWindowW.restype = wintypes.HWND
    kernel32.ProcessIdToSessionId.argtypes = [wintypes.DWORD, ctypes.POINTER(wintypes.DWORD)]
    kernel32.ProcessIdToSessionId.restype = wintypes.BOOL
    session = wintypes.DWORD()
    identified = kernel32.ProcessIdToSessionId(os.getpid(), ctypes.byref(session))
    return {'taskbar_present': bool(user32.FindWindowW('Shell_TrayWnd', None)),
            'session_id': session.value if identified else None,
            'process_arch': platform.machine().lower()}


def extract(archive, directory):
    if archive.name.endswith('.zip'):
        with zipfile.ZipFile(archive) as zipped:
            for member in zipped.infolist():
                target = (directory / member.filename).resolve()
                if (directory not in target.parents or member.filename.startswith('/') or
                        stat.S_IFMT(member.external_attr >> 16) not in (0, stat.S_IFREG, stat.S_IFDIR)):
                    raise ValueError('Unsafe ZIP entry')
            # Apple's extractor preserves Mach-O executable permissions and seals.
            if platform.system() == 'Darwin':
                subprocess.run(['/usr/bin/ditto', '-x', '-k', str(archive), str(directory)], check=True)
            else:
                zipped.extractall(directory)
    else:
        with tarfile.open(archive) as tar:
            for member in tar.getmembers():
                target = (directory / member.name).resolve()
                if (directory not in target.parents or member.name.startswith('/') or
                        not (member.isfile() or member.isdir())):
                    raise ValueError('Unsafe TAR entry')
            tar.extractall(directory)
    name = 'Kilo Proxy.exe' if platform.system() == 'Windows' else 'kilo-proxy'
    candidates = [path for path in directory.rglob(name) if path.is_file()]
    if len(candidates) != 1:
        raise ValueError('Archive must contain exactly one desktop executable')
    return candidates[0]


def smoke(binary, root, expected_version=None):
    report = root / 'desktop-report.json'
    command = [str(binary.resolve()), '--desktop-self-test', str(report)]
    if platform.system() == 'Windows':
        print('Windows graphical session: ' + json.dumps(windows_desktop_context()), flush=True)
    try:
        result = subprocess.run(command, capture_output=True, text=True, timeout=150)
    except subprocess.TimeoutExpired as error:
        stderr = error.stderr or b''
        if isinstance(stderr, bytes):
            stderr = stderr.decode('utf-8', errors='replace')
        raise RuntimeError('Native desktop did not quit within 150 seconds. ' + stderr[-2500:]) from error
    if not report.exists():
        # Startup logs may contain the synthetic admin URL; do not publish it.
        raise RuntimeError(f'Desktop did not produce a report (exit {result.returncode}). '
                           + result.stderr[-2500:])
    data = json.loads(report.read_text())
    if result.returncode != 0 or not data.get('passed') or not REQUIRED.issubset(data.get('checks', [])):
        raise RuntimeError('Native desktop check failed: ' + json.dumps(data) + '\n' + result.stderr[-2500:])
    expected_arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'amd64': 'amd64', 'x86_64': 'amd64'}[platform.machine().lower()]
    expected_os = {'Darwin': 'darwin', 'Windows': 'windows', 'Linux': 'linux'}[platform.system()]
    if data.get('arch') != expected_arch or data.get('os') != expected_os:
        raise RuntimeError('Native smoke ran the wrong architecture')
    if expected_version is None:
        expected_version = (Path(__file__).resolve().parents[1] / 'VERSION').read_text().strip()
    if data.get('version') != expected_version:
        raise RuntimeError('Native smoke ran a stale executable version')
    print(f'Passed native {data["os"]}/{data["arch"]} desktop {data["version"]}: '
          + ', '.join(data['checks']) + ', process-exited-cleanly')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument('--binary', type=Path)
    group.add_argument('--archive', type=Path)
    parser.add_argument('--version', default=(Path(__file__).resolve().parents[1] / 'VERSION').read_text().strip(),
                        help='Expected executable version (defaults to VERSION).')
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='kilo-native-smoke-') as temporary:
        root = Path(temporary).resolve()
        binary = extract(args.archive.resolve(), root) if args.archive else args.binary
        smoke(binary, root, args.version)


if __name__ == '__main__':
    main()

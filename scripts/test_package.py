"""Guard macOS bundle signing and release archive verification without native tools."""
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
import json
import os
import plistlib
import subprocess
import struct
import sys
import tempfile
import tarfile
import time
import unittest
from unittest.mock import patch
import zipfile

import package
import smoke_desktop


class PackageTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.output = self.root / 'dist'
        self.version = '0.20.1'
        for filename in ['VERSION', 'README.md', 'docs/headless.md', 'THIRD-PARTY-NOTICES.txt',
                         'third_party/systray/LICENSE', 'third_party/systray/PATCHES.md',
                         'ui/icon.svg', 'ui/icon.png']:
            path = self.root / filename
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(self.version if filename == 'VERSION' else 'fixture')
        self.calls = []

    def build(self, *arguments, platform='darwin', runner=None):
        with patch.object(package, 'ROOT', self.root), \
             patch.object(package.sys, 'platform', platform), \
             patch.object(package.sys, 'argv', ['package.py', *arguments]), \
             patch.object(package, 'native_target', return_value=({'darwin':'darwin','linux':'linux','win32':'windows'}[platform], 'arm64' if platform == 'darwin' else 'amd64')), \
             patch.object(package, 'icon_png', return_value=b'icon fixture'), \
             patch.object(package.subprocess, 'run', side_effect=runner or self.native_command):
            package.main()

    def native_command(self, command, **kwargs):
        self.calls.append(command)
        if command[:2] == ['go', 'run']:
            self.assertEqual(command[2], 'github.com/tc-hib/go-winres@v0.3.3')
            arch = command[command.index('--arch') + 1]
            resource = Path(command[command.index('--out') + 1] + f'_windows_{arch}.syso')
            resource.write_bytes(b'resource fixture')
        elif command[0] == 'go':
            env = kwargs['env']
            tags = command[command.index('-tags') + 1]
            self.assertIn(tags, ('desktop', 'headless'))
            self.assertEqual(env['CGO_ENABLED'], '0' if tags == 'headless' or env['GOOS'] == 'windows' else '1')
            Path(command[command.index('-o') + 1]).write_bytes(self.binary_fixture(env['GOOS'], env['GOARCH']))
        elif command[0] == '/usr/bin/codesign':
            bundle = Path(command[-1])
            seal = bundle / 'Contents/_CodeSignature/CodeResources'
            if '--sign' in command:
                # Signing must occur after the executable, icon and metadata exist.
                self.assertTrue((bundle / 'Contents/MacOS/kilo-proxy').is_file())
                self.assertTrue((bundle / 'Contents/Resources/AppIcon.icns').is_file())
                with (bundle / 'Contents/Info.plist').open('rb') as stream:
                    metadata = plistlib.load(stream)
                    self.assertEqual(metadata['CFBundleVersion'], self.version)
                    self.assertEqual(metadata['CFBundleDisplayName'], 'Kilo Proxy')
                    self.assertEqual(metadata['CFBundleIdentifier'], 'ai.kilo.local-proxy')
                    self.assertEqual(metadata['CFBundleExecutable'], 'kilo-proxy')
                seal.parent.mkdir()
                seal.write_bytes(b'bundle resource seal fixture')
            else:
                self.assertIn('--strict', command)
                self.assertTrue(seal.is_file(), 'The app bundle resource seal was lost')
        elif command[0] == '/usr/bin/ditto':
            with zipfile.ZipFile(command[-2]) as archive:
                archive.extractall(command[-1])
        else:
            self.fail(f'Unexpected command: {command}')
        return subprocess.CompletedProcess(command, 0)

    @staticmethod
    def binary_fixture(system, arch):
        header = bytearray(768)
        if system == 'darwin':
            header[:4] = b'\xcf\xfa\xed\xfe'
            struct.pack_into('<I', header, 4, 0x0100000c if arch == 'arm64' else 0x01000007)
        elif system == 'linux':
            header[:6] = b'\x7fELF\x02\x01'
            struct.pack_into('<H', header, 18, 183 if arch == 'arm64' else 62)
        else:
            header[:2] = b'MZ'
            struct.pack_into('<I', header, 60, 64)
            header[64:68] = b'PE\x00\x00'
            struct.pack_into('<H', header, 68, 0xaa64 if arch == 'arm64' else 0x8664)
            struct.pack_into('<H', header, 70, 1)  # one section
            struct.pack_into('<H', header, 84, 240)  # PE32+ optional header
            struct.pack_into('<H', header, 88, 0x20b)
            struct.pack_into('<II', header, 208, 0x1000, 40)
            struct.pack_into('<IIII', header, 336, 256, 0x1000, 256, 512)
            struct.pack_into('<IIIII', header, 512, 0, 0, 0, 0x1040, 0)
            header[576:589] = b'kernel32.dll\0'
        return bytes(header)

    def test_bundle_seal_and_executable_permissions_survive_zip(self):
        self.build('--target', 'darwin/arm64')
        self.assertEqual([call[0] for call in self.calls],
                         ['go', '/usr/bin/codesign', '/usr/bin/codesign'])
        self.assertIn('--sign', self.calls[1])
        self.assertIn('--verify', self.calls[2])
        archive = self.output / f'kilo-proxy-{self.version}-darwin-arm64.zip'
        with zipfile.ZipFile(archive) as zipped:
            prefix = f'kilo-proxy-{self.version}-darwin-arm64/Kilo Proxy.app/Contents/'
            self.assertEqual(zipped.read(prefix + '_CodeSignature/CodeResources'),
                             b'bundle resource seal fixture')
            mode = zipped.getinfo(prefix + 'MacOS/kilo-proxy').external_attr >> 16
            if os.name != 'nt':
                self.assertEqual(mode & 0o777, 0o755)

    def test_invalid_signature_prevents_archive_and_manifest(self):
        def reject_verification(command, **kwargs):
            if '--verify' in command:
                raise subprocess.CalledProcessError(1, command)
            return self.native_command(command, **kwargs)
        with self.assertRaises(subprocess.CalledProcessError):
            self.build('--target', 'darwin/arm64', runner=reject_verification)
        self.assertEqual(list(self.output.glob('*.zip')), [])
        self.assertFalse((self.output / 'SHA256SUMS.txt').exists())

    def test_non_macos_host_rejects_darwin_before_building(self):
        for arguments in [['--target', 'darwin/arm64'], ['--target', 'darwin/amd64']]:
            with self.subTest(arguments=arguments), self.assertRaises(SystemExit):
                self.build(*arguments, platform='linux')
        self.assertEqual(self.calls, [])
        self.assertFalse(self.output.exists())

    def test_non_macos_host_can_build_repeated_non_darwin_targets(self):
        self.build('--target', 'windows/amd64', '--target', 'linux/amd64',
                   '--target', 'windows/amd64', platform='linux')
        self.assertEqual([call[:2] for call in self.calls], [['go', 'run'], ['go', 'build'], ['go', 'build']])
        self.assertEqual(list(self.root.glob('*.syso')), [])
        lines = (self.output / 'SHA256SUMS.txt').read_text().splitlines()
        self.assertEqual(len(lines), 2)
        self.assertTrue(lines[0].endswith('windows-amd64.zip'))
        self.assertTrue(lines[1].endswith('linux-amd64.tar.gz'))

    def test_post_extraction_verification_checks_both_macos_archives(self):
        self.build('--target', 'darwin/arm64', '--target', 'darwin/amd64')
        self.calls = []
        self.build('--verify-macos-archives')
        self.assertEqual([call[0] for call in self.calls],
                         ['/usr/bin/ditto', '/usr/bin/codesign',
                          '/usr/bin/ditto', '/usr/bin/codesign'])
        self.assertTrue(all('--verify' in call for call in self.calls[1::2]))

    def test_post_extraction_verification_failure_is_fatal(self):
        self.build('--target', 'darwin/arm64', '--target', 'darwin/amd64')
        def damaged_bundle(command, **kwargs):
            if command[0] == '/usr/bin/codesign':
                raise subprocess.CalledProcessError(1, command)
            return self.native_command(command, **kwargs)
        with self.assertRaises(subprocess.CalledProcessError):
            self.build('--verify-macos-archives', runner=damaged_bundle)

    def test_raw_build_does_not_create_bundle_or_manifest(self):
        self.build('--build-only', '--target', 'darwin/arm64')
        self.assertEqual([call[0] for call in self.calls], ['go'])
        raw = self.output / 'kilo-proxy-darwin-arm64'
        self.assertTrue(raw.is_file())
        self.assertFalse((self.output / 'staging').exists())
        self.assertFalse((self.output / 'SHA256SUMS.txt').exists())

    def test_prebuilt_package_preserves_tested_executable_without_go_build(self):
        prebuilt = self.root / 'binaries'
        prebuilt.mkdir()
        data = self.binary_fixture('windows', 'arm64') + b'tested desktop contents'
        (prebuilt / 'kilo-proxy-windows-arm64.exe').write_bytes(data)
        self.build('--binaries-directory', str(prebuilt), '--target', 'windows/arm64', platform='linux')
        self.assertEqual(self.calls, [])
        with zipfile.ZipFile(self.output / f'kilo-proxy-{self.version}-windows-arm64.zip') as zipped:
            self.assertEqual(zipped.read(f'kilo-proxy-{self.version}-windows-arm64/Kilo Proxy.exe'), data)

    def test_prebuilt_wrong_architecture_fails_before_packaging(self):
        prebuilt = self.root / 'binaries'
        prebuilt.mkdir()
        (prebuilt / 'kilo-proxy-windows-arm64.exe').write_bytes(self.binary_fixture('windows', 'amd64'))
        with self.assertRaises(SystemExit):
            self.build('--binaries-directory', str(prebuilt), '--target', 'windows/arm64', platform='linux')
        self.assertFalse(self.output.exists())
        self.assertEqual(self.calls, [])

    def test_native_graphics_build_rejects_foreign_linux_architecture(self):
        with self.assertRaises(SystemExit):
            self.build('--build-only', '--target', 'linux/arm64', platform='linux')
        self.assertEqual(self.calls, [])

    def test_headless_cross_build_has_no_cgo_desktop_or_native_signing(self):
        self.build('--headless', '--build-only', '--target', 'darwin/arm64',
                   '--target', 'linux/arm64', platform='linux')
        self.assertEqual([call[:2] for call in self.calls], [['go', 'build'], ['go', 'build']])
        self.assertTrue((self.output / 'kilo-proxy-headless-darwin-arm64').is_file())
        self.assertTrue((self.output / 'kilo-proxy-headless-linux-arm64').is_file())
        self.assertFalse((self.output / 'SHA256SUMS.txt').exists())

    def test_headless_prebuilt_archive_has_console_binary_and_no_desktop_installer(self):
        prebuilt = self.root / 'binaries'
        prebuilt.mkdir()
        data = self.binary_fixture('darwin', 'arm64') + b'tested headless contents'
        (prebuilt / 'kilo-proxy-headless-darwin-arm64').write_bytes(data)
        self.build('--headless', '--binaries-directory', str(prebuilt),
                   '--target', 'darwin/arm64', platform='linux')
        self.assertEqual(self.calls, [])
        archive = self.output / f'kilo-proxy-headless-{self.version}-darwin-arm64.tar.gz'
        with tarfile.open(archive) as tar:
            name = f'kilo-proxy-headless-{self.version}-darwin-arm64/kilo-proxy-headless'
            self.assertEqual(tar.extractfile(name).read(), data)
            self.assertEqual(tar.getmember(name).mode, 0o755)
            self.assertEqual(tar.extractfile(f'kilo-proxy-headless-{self.version}-darwin-arm64/docs/headless.md').read(), b'fixture')
            self.assertFalse(any('Kilo Proxy.app' in member.name or member.name.endswith('install-user.sh')
                                 for member in tar.getmembers()))

    def test_headless_rejects_windows_and_app_bundle_verification(self):
        for arguments in [('--target', 'windows/amd64'), ('--verify-macos-archives',)]:
            with self.subTest(arguments=arguments), self.assertRaises(SystemExit):
                self.build('--headless', *arguments, platform='linux')
        self.assertEqual(self.calls, [])

    def test_headless_archive_encodes_unix_execute_bits_from_windows_file_modes(self):
        prebuilt = self.root / 'binaries'
        prebuilt.mkdir()
        data = self.binary_fixture('linux', 'arm64') + b'tested console contents'
        (prebuilt / 'kilo-proxy-headless-linux-arm64').write_bytes(data)
        gettarinfo = tarfile.TarFile.gettarinfo
        chmod = Path.chmod
        observed = []

        def ineffective_chmod(path, mode, **kwargs):
            chmod(path, 0o666 if path.name == 'kilo-proxy-headless' else mode, **kwargs)

        def windows_file_mode(tar, *args, **kwargs):
            member = gettarinfo(tar, *args, **kwargs)
            if member.isfile():
                member.mode = 0o666
                observed.append(member.name)
            return member

        with patch.object(Path, 'chmod', new=ineffective_chmod), \
             patch.object(tarfile.TarFile, 'gettarinfo', new=windows_file_mode):
            self.build('--headless', '--binaries-directory', str(prebuilt),
                       '--target', 'linux/arm64', platform='win32')
        name = f'kilo-proxy-headless-{self.version}-linux-arm64/kilo-proxy-headless'
        self.assertIn(name, observed)
        with tarfile.open(self.output / f'kilo-proxy-headless-{self.version}-linux-arm64.tar.gz') as tar:
            self.assertEqual(tar.getmember(name).mode, 0o755)
            self.assertEqual(tar.extractfile(name).read(), data)

    def test_current_release_checksums_require_all_ten_archives_atomically(self):
        self.version = '0.55.0'
        self.output.mkdir()
        manifest = self.output / 'SHA256SUMS.txt'
        manifest.write_text('previous manifest')
        names = package.archive_names(self.version, package.TARGETS, package.HEADLESS_TARGETS)
        for name in names[:-1]:
            (self.output / name).write_bytes(b'archive')
        with self.assertRaises(SystemExit):
            self.build('--checksums-only', '--version', self.version, platform='linux')
        self.assertEqual(manifest.read_text(), 'previous manifest')
        (self.output / names[-1]).write_bytes(b'archive')
        self.build('--checksums-only', '--version', self.version, platform='linux')
        self.assertEqual(len(manifest.read_text().splitlines()), 10)

    def test_checksums_require_all_six_archives_before_replacing_manifest(self):
        self.output.mkdir()
        manifest = self.output / 'SHA256SUMS.txt'
        manifest.write_text('previous manifest')
        for system, arch in package.TARGETS[:-1]:
            ext = '.tar.gz' if system == 'linux' else '.zip'
            (self.output / f'kilo-proxy-{self.version}-{system}-{arch}{ext}').write_bytes(b'archive')
        with self.assertRaises(SystemExit):
            self.build('--checksums-only', platform='linux')
        self.assertEqual(manifest.read_text(), 'previous manifest')
        (self.output / f'kilo-proxy-{self.version}-windows-arm64.zip').write_bytes(b'archive')
        self.build('--checksums-only', platform='linux')
        self.assertEqual(len(manifest.read_text().splitlines()), 6)

    def test_windows_package_rejects_external_runtime_before_output(self):
        prebuilt = self.root / 'binaries'
        prebuilt.mkdir()
        data = bytearray(self.binary_fixture('windows', 'arm64'))
        data[576:600] = b'WebView2Loader.dll\0'.ljust(24, b'\0')
        (prebuilt / 'kilo-proxy-windows-arm64.exe').write_bytes(data)
        with self.assertRaises(SystemExit):
            self.build('--binaries-directory', str(prebuilt), '--target', 'windows/arm64', platform='linux')
        self.assertFalse(self.output.exists())

    def test_windows_import_table_rejects_invalid_pointer(self):
        binary = self.root / 'invalid.exe'
        data = bytearray(self.binary_fixture('windows', 'amd64'))
        struct.pack_into('<I', data, 524, 0xffffffff)
        binary.write_bytes(data)
        with self.assertRaisesRegex(ValueError, 'import address'):
            package.verify_windows_runtime(binary)

    def test_native_smoke_rejects_zip_symlink_before_extraction(self):
        archive = self.root / 'unsafe.zip'
        directory = self.root / 'extract'
        directory.mkdir()
        with zipfile.ZipFile(archive, 'w') as zipped:
            link = zipfile.ZipInfo('escape')
            link.create_system = 3
            link.external_attr = 0o120777 << 16
            zipped.writestr(link, '../outside')
            zipped.writestr('escape/file', 'must not escape')
        with self.assertRaisesRegex(ValueError, 'Unsafe ZIP'):
            smoke_desktop.extract(archive, directory)
        self.assertEqual(list(directory.iterdir()), [])

    def test_native_smoke_version_argument_defaults_and_overrides(self):
        default = (Path(smoke_desktop.__file__).resolve().parents[1] / 'VERSION').read_text().strip()
        for arguments, version in [([], default), (['--version', '0.23.0-alpha.1'], '0.23.0-alpha.1')]:
            with self.subTest(version=version), \
                 patch('sys.argv', ['smoke_desktop.py', '--binary', str(self.root / 'preview'), *arguments]), \
                 patch.object(smoke_desktop, 'smoke') as run:
                smoke_desktop.main()
                self.assertEqual(run.call_args.args[2], version)

    def test_native_smoke_preview_version_preserves_strict_report_checks(self):
        report = {'passed': True, 'checks': sorted(smoke_desktop.REQUIRED),
                  'os': 'linux', 'arch': 'amd64', 'version': '0.23.0-alpha.1'}

        def execute(command, **kwargs):
            Path(command[-1]).write_text(json.dumps(report))
            return subprocess.CompletedProcess(command, 0, '', '')

        with patch.object(smoke_desktop.platform, 'system', return_value='Linux'), \
             patch.object(smoke_desktop.platform, 'machine', return_value='x86_64'), \
             patch.object(smoke_desktop.subprocess, 'run', side_effect=execute):
            smoke_desktop.smoke(self.root / 'preview', self.root, '0.23.0-alpha.1')
            with self.assertRaisesRegex(RuntimeError, 'stale executable version'):
                smoke_desktop.smoke(self.root / 'preview', self.root, '0.23.0-alpha.2')
            report['arch'] = 'arm64'
            with self.assertRaisesRegex(RuntimeError, 'wrong architecture'):
                smoke_desktop.smoke(self.root / 'preview', self.root, '0.23.0-alpha.1')
            report['arch'] = 'amd64'
            report['checks'] = []
            with self.assertRaisesRegex(RuntimeError, 'Native desktop check failed'):
                smoke_desktop.smoke(self.root / 'preview', self.root, '0.23.0-alpha.1')

    def test_native_smoke_nonzero_keeps_fatal_header_and_redacts_private_diagnostics(self):
        token = 'synthetic-admin-token-that-must-not-leak'
        address = 'http://127.0.0.1:12345/#' + token
        report = {'passed': True, 'checks': sorted(smoke_desktop.REQUIRED),
                  'os': 'linux', 'arch': 'amd64', 'version': '0.23.0-alpha.1',
                  'error': address + ' repeated token ' + token,
                  'admin_token': 'another-private-admin-token'}

        def execute(command, **kwargs):
            Path(command[-1]).write_text(json.dumps(report))
            kwargs['stdout'].write(('Control panel: ' + address + '\n').encode())
            kwargs['stderr'].write(('Native check passed: proxy-start-via-ui\n'
                                    'Exception 0xc000000d\nPC=0x1234\n'
                                    'goroutine 1 [syscall]:\ncausal-native-frame\n'
                                    + 'irrelevant frame\n' * 2000 + 'final-native-frame\n'
                                    + 'upstream: wss://private.invalid/socket\n').encode())
            return subprocess.CompletedProcess(command, 2)

        with patch.object(smoke_desktop.platform, 'system', return_value='Linux'), \
             patch.object(smoke_desktop.subprocess, 'run', side_effect=execute), \
             self.assertRaises(RuntimeError) as failure:
            smoke_desktop.smoke(self.root / 'preview', self.root, '0.23.0-alpha.1')
        message = str(failure.exception)
        for required in ['exit 2', 'Exception 0xc000000d', 'PC=0x1234',
                         'causal-native-frame', 'final-native-frame', 'proxy-start-via-ui',
                         'report:', 'tray-stop', 'diagnostic truncated']:
            self.assertIn(required, message)
        for private in [address, token, '127.0.0.1', 'private.invalid', 'another-private-admin-token']:
            self.assertNotIn(private, message)
        self.assertLess(len(message), 22000)

    def test_native_smoke_timeout_preserves_progress_and_missing_report_failure(self):
        def execute(command, **kwargs):
            kwargs['stdout'].write(b'Control panel: http://127.0.0.1:43210/#private-token\n')
            kwargs['stderr'].write(b'Native check passed: rendered-ui-and-authenticated-backend\n'
                                    b'Exception 0xc000000d\ngoroutine 1 [syscall]:\n')
            raise subprocess.TimeoutExpired(command, kwargs['timeout'])

        with patch.object(smoke_desktop.platform, 'system', return_value='Linux'), \
             patch.object(smoke_desktop.subprocess, 'run', side_effect=execute), \
             self.assertRaises(RuntimeError) as failure:
            smoke_desktop.smoke(self.root / 'preview', self.root)
        message = str(failure.exception)
        self.assertIn('process did not exit within 150 seconds', message)
        self.assertIn('rendered-ui-and-authenticated-backend', message)
        self.assertIn('Exception 0xc000000d', message)
        self.assertNotIn('127.0.0.1', message)
        self.assertNotIn('private-token', message)

    def test_native_smoke_waits_for_desktop_without_descendant_capture_pipes(self):
        report = {'passed': True, 'checks': sorted(smoke_desktop.REQUIRED),
                  'os': 'linux', 'arch': 'amd64', 'version': '0.23.0-alpha.1'}
        gate, ready, done = (self.root / name for name in ('release-child', 'child-ready', 'child-done'))
        child = '''from pathlib import Path
import sys, time
gate, ready, done = map(Path, sys.argv[1:])
ready.write_text('holding inherited output handles')
deadline = time.monotonic() + 10
while not gate.exists() and time.monotonic() < deadline:
    time.sleep(.02)
sys.stdout.close()
sys.stderr.close()
done.write_text('closed inherited output handles')
'''
        parent = '''from pathlib import Path
import subprocess, sys, time
subprocess.Popen([sys.executable, '-c', sys.argv[1], *sys.argv[2:5]],
                 stdout=sys.stdout, stderr=sys.stderr)
deadline = time.monotonic() + 5
while not Path(sys.argv[3]).exists():
    if time.monotonic() >= deadline:
        raise RuntimeError('owned descendant did not start')
    time.sleep(.02)
Path(sys.argv[5]).write_text(sys.argv[6])
'''
        real_run = subprocess.run

        def execute(command, **kwargs):
            return real_run([sys.executable, '-c', parent, child, str(gate), str(ready),
                             str(done), command[-1], json.dumps(report)], **kwargs)

        with ThreadPoolExecutor(max_workers=1) as executor, \
             patch.object(smoke_desktop.platform, 'system', return_value='Linux'), \
             patch.object(smoke_desktop.platform, 'machine', return_value='x86_64'), \
             patch.object(smoke_desktop.subprocess, 'run', side_effect=execute):
            future = executor.submit(smoke_desktop.smoke, self.root / 'preview', self.root, '0.23.0-alpha.1')
            try:
                # The real parent exits while its own descendant still holds
                # stdout/stderr. Capturing pipes would wait until gate is set.
                future.result(timeout=5)
                self.assertTrue(ready.exists())
                self.assertFalse(done.exists())
            finally:
                gate.write_text('release only our temporary descendant')
                deadline = time.monotonic() + 5
                while not done.exists() and time.monotonic() < deadline:
                    time.sleep(.02)
                self.assertTrue(done.exists(), 'owned descendant did not close its handles')

    def test_post_extraction_can_verify_one_matrix_target(self):
        self.build('--target', 'darwin/arm64')
        self.calls.clear()
        self.build('--verify-macos-archives', '--target', 'darwin/arm64')
        self.assertEqual([call[0] for call in self.calls], ['/usr/bin/ditto', '/usr/bin/codesign'])


if __name__ == '__main__':
    unittest.main()

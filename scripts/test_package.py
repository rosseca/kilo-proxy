"""Guard macOS bundle signing and release archive verification without native tools."""
from pathlib import Path
import json
import os
import plistlib
import subprocess
import struct
import tempfile
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
        for filename in ['VERSION', 'README.md', 'THIRD-PARTY-NOTICES.txt',
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
            self.assertEqual(command[command.index('-tags') + 1], 'desktop')
            self.assertEqual(env['CGO_ENABLED'], '0' if env['GOOS'] == 'windows' else '1')
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

    def test_post_extraction_can_verify_one_matrix_target(self):
        self.build('--target', 'darwin/arm64')
        self.calls.clear()
        self.build('--verify-macos-archives', '--target', 'darwin/arm64')
        self.assertEqual([call[0] for call in self.calls], ['/usr/bin/ditto', '/usr/bin/codesign'])


if __name__ == '__main__':
    unittest.main()

"""Guard macOS bundle signing and release archive verification without native tools."""
from pathlib import Path
import os
import plistlib
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import zipfile

import package


class PackageTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.output = self.root / 'dist'
        self.version = '0.20.1'
        for filename in ['VERSION', 'README.md', 'THIRD-PARTY-NOTICES.txt',
                         'third_party/systray/LICENSE', 'third_party/systray/PATCHES.md',
                         'ui/icon.svg']:
            path = self.root / filename
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(self.version if filename == 'VERSION' else 'fixture')
        self.calls = []

    def build(self, *arguments, platform='darwin', runner=None):
        with patch.object(package, 'ROOT', self.root), \
             patch.object(package.sys, 'platform', platform), \
             patch.object(package.sys, 'argv', ['package.py', *arguments]), \
             patch.object(package, 'icon_png', return_value=b'icon fixture'), \
             patch.object(package.subprocess, 'run', side_effect=runner or self.native_command):
            package.main()

    def native_command(self, command, **kwargs):
        self.calls.append(command)
        if command[0] == 'go':
            Path(command[command.index('-o') + 1]).write_bytes(b'executable fixture')
        elif command[0] == '/usr/bin/codesign':
            bundle = Path(command[-1])
            seal = bundle / 'Contents/_CodeSignature/CodeResources'
            if '--sign' in command:
                # Signing must occur after the executable, icon and metadata exist.
                self.assertTrue((bundle / 'Contents/MacOS/kilo-local').is_file())
                self.assertTrue((bundle / 'Contents/Resources/AppIcon.icns').is_file())
                with (bundle / 'Contents/Info.plist').open('rb') as stream:
                    self.assertEqual(plistlib.load(stream)['CFBundleVersion'], self.version)
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

    def test_bundle_seal_and_executable_permissions_survive_zip(self):
        self.build('--target', 'darwin/arm64')
        self.assertEqual([call[0] for call in self.calls],
                         ['go', '/usr/bin/codesign', '/usr/bin/codesign'])
        self.assertIn('--sign', self.calls[1])
        self.assertIn('--verify', self.calls[2])
        archive = self.output / f'kilo-local-{self.version}-darwin-arm64.zip'
        with zipfile.ZipFile(archive) as zipped:
            prefix = f'kilo-local-{self.version}-darwin-arm64/Kilo Local.app/Contents/'
            self.assertEqual(zipped.read(prefix + '_CodeSignature/CodeResources'),
                             b'bundle resource seal fixture')
            mode = zipped.getinfo(prefix + 'MacOS/kilo-local').external_attr >> 16
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
        for arguments in [[], ['--target', 'darwin/arm64']]:
            with self.subTest(arguments=arguments), self.assertRaises(SystemExit):
                self.build(*arguments, platform='linux')
        self.assertEqual(self.calls, [])
        self.assertFalse(self.output.exists())

    def test_non_macos_host_can_build_repeated_non_darwin_targets(self):
        self.build('--target', 'windows/amd64', '--target', 'linux/arm64',
                   '--target', 'windows/amd64', platform='linux')
        self.assertEqual([call[0] for call in self.calls], ['go', 'go'])
        lines = (self.output / 'SHA256SUMS.txt').read_text().splitlines()
        self.assertEqual(len(lines), 2)
        self.assertTrue(lines[0].endswith('windows-amd64.zip'))
        self.assertTrue(lines[1].endswith('linux-arm64.tar.gz'))

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


if __name__ == '__main__':
    unittest.main()

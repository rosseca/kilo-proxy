"""Guard Debian input identity, runtime dependencies and inert package contents."""
from contextlib import ExitStack
import hashlib
import os
from pathlib import Path
import platform
import shutil
import stat
import struct
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import package_debian as package


def elf(arch='amd64', dynamic=False):
    data = bytearray(512)
    data[:8] = b'\x7fELF\x02\x01\x01\x00'
    struct.pack_into('<HHI', data, 16, 2, {'amd64': 62, 'arm64': 183}[arch], 1)
    struct.pack_into('<Q', data, 32, 64)
    types = [1, 2, 3] if dynamic else [1]
    struct.pack_into('<HHH', data, 52, 64, 56, len(types))
    for index, value in enumerate(types):
        struct.pack_into('<I', data, 64 + index * 56, value)
    return bytes(data)


class DebianTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='kilo-debian-tests-')
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.desktop = self.root / "desktop binaries with spaces ' literal"
        self.headless = self.root / 'headless binaries'
        self.output = self.root / 'output with spaces'
        for path in (self.desktop, self.headless):
            path.mkdir()
        for name, content in {'VERSION': '0.56.3', 'README.md': 'readme',
                              'THIRD-PARTY-NOTICES.txt': 'notices',
                              'docs/headless.md': 'console guide', 'ui/icon.svg': '<svg/>',
                              'third_party/gio/LICENSE': 'gio license',
                              'third_party/gio/kilo-local.patch': 'upstream patch'}.items():
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
        self.desktop_binary = self.desktop / 'kilo-proxy-linux-amd64'
        self.headless_binary = self.headless / 'kilo-proxy-headless-linux-amd64'
        self.desktop_binary.write_bytes(elf(dynamic=True))
        self.headless_binary.write_bytes(elf())
        self.desktop_binary.chmod(0o755)
        self.headless_binary.chmod(0o755)
        self.stages = []
        self.calls = []

    def fake_build(self, command, **kwargs):
        self.calls.append(command)
        self.assertEqual(command[:5], ['dpkg-deb', '--root-owner-group', '--uniform-compression', '-Zxz', '-z9'])
        self.assertEqual(kwargs['env']['SOURCE_DATE_EPOCH'], '0')
        stage, destination = Path(command[-2]), Path(command[-1])
        contents = {path.relative_to(stage).as_posix(): (path.read_bytes(), stat.S_IMODE(path.stat().st_mode), path.stat().st_mtime)
                    for path in stage.rglob('*') if path.is_file()}
        self.stages.append(contents)
        destination.write_bytes(b'fixture .deb')
        return subprocess.CompletedProcess(command, 0)

    def build(self, version='0.56.3', runner=None, **kwargs):
        with ExitStack() as stack:
            stack.enter_context(patch.object(package, 'require_native_host'))
            stack.enter_context(patch.object(package, 'verify_version'))
            stack.enter_context(patch.object(package, 'desktop_dependencies', return_value='libc6 (>= 2.38), libegl1, libgles2'))
            stack.enter_context(patch.object(package.subprocess, 'run', side_effect=runner or self.fake_build))
            stack.enter_context(patch.dict(os.environ, {'SOURCE_DATE_EPOCH': '0'}))
            return package.build_packages(self.desktop, self.headless, 'amd64', version,
                                          self.output, root=self.root, **kwargs)

    def test_prerelease_mapping_and_version_injection_rejected(self):
        for version in ('0.56.3', '1.0.0-alpha.1', '1.0.0-beta.12', '1.0.0-rc.3'):
            self.assertEqual(package.debian_version(version), version.replace('-', '~'))
        for version in ('01.2.3', '1.2', 'v1.2.3', '1.2.3\nDepends: evil', '../1.2.3',
                        '1.2.3-beta.0', '1.2.3+metadata', '1.2.3;touch /tmp/example'):
            with self.subTest(version=version), self.assertRaises(ValueError):
                package.debian_version(version)

    def test_elf_architecture_os_and_table_validation(self):
        for arch in ('amd64', 'arm64'):
            self.headless_binary.write_bytes(elf(arch))
            package.validate_elf(self.headless_binary, arch, headless=True)
            with self.assertRaises(ValueError):
                package.validate_elf(self.headless_binary, 'arm64' if arch == 'amd64' else 'amd64', headless=True)
        for mutation in ('windows', 'big-endian', 'freebsd', 'bad-table'):
            data = bytearray(elf())
            if mutation == 'windows':
                data[:2] = b'MZ'
            elif mutation == 'big-endian':
                data[5] = 2
            elif mutation == 'freebsd':
                data[7] = 9
            else:
                struct.pack_into('<Q', data, 32, len(data) - 1)
            self.headless_binary.write_bytes(data)
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                package.validate_elf(self.headless_binary, 'amd64', headless=True)

    def test_headless_requires_static_and_desktop_dynamic_elf(self):
        with self.assertRaisesRegex(ValueError, 'static'):
            package.validate_elf(self.desktop_binary, 'amd64', headless=True)
        with self.assertRaisesRegex(ValueError, 'dynamic table'):
            package.validate_elf(self.headless_binary, 'amd64')
        # Reject PT_DYNAMIC even without an interpreter (not a fully static executable).
        data = bytearray(elf())
        struct.pack_into('<I', data, 64, 2)
        self.headless_binary.write_bytes(data)
        with self.assertRaisesRegex(ValueError, 'static'):
            package.validate_elf(self.headless_binary, 'amd64', headless=True)

    def test_binary_links_special_files_and_nonexecutables_rejected(self):
        if os.name == 'posix':
            self.headless_binary.chmod(0o644)
            with self.assertRaises(ValueError):
                self.build()
        self.headless_binary.chmod(0o755)
        self.headless_binary.unlink()
        try:
            self.headless_binary.symlink_to(self.desktop_binary)
        except OSError:
            self.skipTest('Host does not permit symlink fixtures')
        with self.assertRaises(ValueError):
            self.build()
        self.headless_binary.unlink()
        if hasattr(os, 'mkfifo'):
            os.mkfifo(self.headless_binary)
            with self.assertRaises(ValueError):
                package.read_regular(self.headless_binary)
        self.assertFalse(self.output.exists())

    def test_source_directory_symlink_rejected(self):
        alias = self.root / 'alias'
        try:
            alias.symlink_to(self.desktop, target_is_directory=True)
        except OSError:
            self.skipTest('Host does not permit symlink fixtures')
        with patch.object(package, 'require_native_host'), self.assertRaises(ValueError):
            package.build_packages(alias, self.headless, 'amd64', '0.56.3', self.output, root=self.root)

    def test_runtime_dependencies_preserve_shlib_versions_and_include_dlopen(self):
        response = subprocess.CompletedProcess([], 0, stdout='shlibs:Depends=libc6 (>= 2.38), libegl1 (>= 1.4.0), libwayland-client0 (>= 1.20)\n')
        with patch.object(package.subprocess, 'run', return_value=response) as run:
            depends = package.desktop_dependencies(self.desktop_binary, self.root)
        self.assertIn('libc6 (>= 2.38)', depends)
        self.assertEqual(depends.count('libegl1'), 1)
        for value in package.DESKTOP_RUNTIME_DEPENDS:
            self.assertIn(value.split(' (')[0], depends)
        self.assertIn('ca-certificates', depends)
        command = run.call_args.args[0]
        self.assertEqual(command, ['dpkg-shlibdeps', '-O', '-e' + str(self.desktop_binary)])
        self.assertNotIn('--ignore-missing-info', command)

    def test_dependency_parser_fails_closed(self):
        for output in ('', 'shlibs:Depends=', 'shlibs:Depends=libc6\nPackage: evil',
                       'shlibs:Depends=libegl1-dev', 'shlibs:Depends=libc6 (>= 2.38); evil',
                       'unrelated=libc6', 'shlibs:Depends=${unresolved:Depends}'):
            with self.subTest(output=output), self.assertRaises(ValueError):
                package.parse_shlibdeps(output)

    def test_payload_coexists_and_installation_is_inert(self):
        paths = self.build()
        self.assertEqual([path.name for path in paths], ['kilo-proxy-desktop_0.56.3_amd64.deb', 'kilo-proxy-headless_0.56.3_amd64.deb'])
        desktop, headless = self.stages
        self.assertEqual(desktop['usr/bin/kilo-proxy'][0], self.desktop_binary.read_bytes())
        self.assertEqual(headless['usr/bin/kilo-proxy-headless'][0], self.headless_binary.read_bytes())
        if os.name == 'posix':
            self.assertEqual(desktop['usr/bin/kilo-proxy'][1], 0o755)
        self.assertEqual(desktop['usr/bin/kilo-proxy'][2], 0)
        self.assertEqual(desktop['usr/share/applications/kilo-proxy.desktop'][0].decode(), package.DESKTOP_ENTRY)
        self.assertIn('usr/share/icons/hicolor/scalable/apps/kilo-proxy.svg', desktop)
        self.assertFalse({path for path in set(desktop) & set(headless) if not path.startswith('DEBIAN/')},
                         'The two packages must share no installed files')
        for name, contents in zip(package.PACKAGES, self.stages):
            self.assertEqual({path for path in contents if path.startswith('DEBIAN/')}, {'DEBIAN/control', 'DEBIAN/md5sums'})
            self.assertTrue(all(path.startswith(('usr/', 'DEBIAN/')) for path in contents))
            self.assertTrue(all('systemd' not in path and '/home/' not in path and '/etc/' not in path for path in contents))
            if os.name == 'posix':
                self.assertEqual(contents['DEBIAN/control'][1], 0o644)
            control = contents['DEBIAN/control'][0].decode()
            for expected in (f'Package: {name}\n', 'Version: 0.56.3\n', 'Architecture: amd64\n'):
                self.assertIn(expected, control)
            notices = 'usr/share/doc/' + name + '/THIRD-PARTY-NOTICES.txt'
            self.assertEqual(contents[notices][0], b'notices')
            self.assertIn('usr/share/doc/' + name + '/third_party/gio/LICENSE', contents)
            for line in contents['DEBIAN/md5sums'][0].decode().splitlines():
                digest, path = line.split('  ', 1)
                self.assertEqual(digest, hashlib.md5(contents[path][0], usedforsecurity=False).hexdigest())
        self.assertIn('Depends: ca-certificates\n', headless['DEBIAN/control'][0].decode())
        self.assertNotIn('Conflicts:', desktop['DEBIAN/control'][0].decode())

    def test_version_and_preflight_failures_do_not_touch_output(self):
        with patch.object(package, 'require_native_host'), \
             patch.object(package, 'verify_version', side_effect=ValueError('stale binary')), \
             self.assertRaisesRegex(ValueError, 'stale'):
            package.build_packages(self.desktop, self.headless, 'amd64', '0.56.3', self.output, root=self.root)
        self.assertFalse(self.output.exists())
        self.output.mkdir()
        existing = self.output / 'kilo-proxy-headless_0.56.3_amd64.deb'
        existing.write_bytes(b'existing release')
        with self.assertRaisesRegex(ValueError, 'overwrite'):
            self.build()
        self.assertEqual(existing.read_bytes(), b'existing release')
        self.assertEqual(list(self.output.iterdir()), [existing])

    def test_explicit_old_version_and_prerelease_filenames(self):
        paths = self.build('0.55.0-beta.1')
        self.assertTrue(all('_0.55.0~beta.1_' in path.name for path in paths))
        self.assertTrue(all(b'Version: 0.55.0~beta.1\n' in stage['DEBIAN/control'][0] for stage in self.stages))

    def test_version_execution_is_private_and_exact(self):
        response = subprocess.CompletedProcess([], 0, stdout='0.56.3\n')
        home = self.root / 'private-home'
        home.mkdir()
        with patch.dict(os.environ, {'KILO_LOCAL_API_KEY': 'synthetic', 'CODEX_HOME': '/synthetic/account', 'LD_PRELOAD': '/synthetic/injection', 'SSH_AUTH_SOCK': '/synthetic/socket'}), \
             patch.object(package.subprocess, 'run', return_value=response) as run:
            package.verify_version(self.desktop_binary, '0.56.3', home)
        arguments = run.call_args.kwargs
        for name in ('KILO_LOCAL_API_KEY', 'CODEX_HOME', 'LD_PRELOAD', 'SSH_AUTH_SOCK'):
            self.assertNotIn(name, arguments['env'])
        self.assertEqual(arguments['env']['HOME'], str(home))
        self.assertEqual(arguments['timeout'], 20)
        with patch.object(package.subprocess, 'run', return_value=response), self.assertRaisesRegex(ValueError, 'version'):
            package.verify_version(self.desktop_binary, '0.56.2', home)

    def test_packager_checks_frozen_exact_source_bytes(self):
        def verify(path, version, home):
            self.assertNotEqual(path.parent, self.desktop)
            self.assertNotEqual(path.parent, self.headless)
            self.assertEqual(path.read_bytes(), elf(dynamic='headless' not in path.name))
            if 'headless' not in path.name:
                self.desktop_binary.write_bytes(b'changed after preflight')
        with patch.object(package, 'require_native_host'), patch.object(package, 'verify_version', side_effect=verify), \
             patch.object(package, 'desktop_dependencies', return_value='libc6 (>= 2.38)'), \
             patch.object(package.subprocess, 'run', side_effect=self.fake_build), \
             patch.dict(os.environ, {'SOURCE_DATE_EPOCH': '0'}):
            package.build_packages(self.desktop, self.headless, 'amd64', '0.56.3', self.output, root=self.root)
        self.assertEqual(self.stages[0]['usr/bin/kilo-proxy'][0], elf(dynamic=True))

    def test_non_native_host_and_dpkg_mismatch_rejected(self):
        with patch.object(package.platform, 'system', return_value='Darwin'), self.assertRaisesRegex(ValueError, 'native'):
            package.require_native_host('amd64')
        with patch.object(package.platform, 'system', return_value='Linux'), \
             patch.object(package.platform, 'machine', return_value='x86_64'), \
             patch.object(package.shutil, 'which', return_value='/usr/bin/tool'), \
             patch.object(package.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0, stdout='arm64\n')), \
             self.assertRaisesRegex(ValueError, 'dpkg architecture'):
            package.require_native_host('amd64')

    def test_unsafe_documentation_and_output_links_rejected(self):
        try:
            (self.root / 'docs/unsafe.md').symlink_to(self.root / 'README.md')
        except OSError:
            self.skipTest('Host does not permit symlink fixtures')
        with self.assertRaisesRegex(ValueError, 'documentation'):
            self.build()
        self.assertFalse(self.output.exists())
        (self.root / 'docs/unsafe.md').unlink()
        self.output.symlink_to(self.desktop, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, 'Output'):
            self.build()

    def test_invalid_reproducibility_epoch_rejected(self):
        for value in ('-1', '1.5', '4294967296', '0\nPackage: invalid'):
            with patch.object(package, 'require_native_host'), patch.dict(os.environ, {'SOURCE_DATE_EPOCH': value}), self.assertRaises(ValueError):
                package.build_packages(self.desktop, self.headless, 'amd64', '0.56.3', self.output, root=self.root)

    @unittest.skipUnless(platform.system() == 'Linux' and shutil.which('dpkg-deb') and shutil.which('dpkg-shlibdeps'),
                         'real dpkg integration requires a native Debian/Ubuntu host')
    def test_real_dpkg_metadata_payload_and_reproducibility(self):
        arch = {'x86_64': 'amd64', 'aarch64': 'arm64'}.get(platform.machine().lower())
        if arch not in ('amd64', 'arm64'):
            self.skipTest('supported native CPU required')
        desktop = self.desktop / f'kilo-proxy-linux-{arch}'
        headless = self.headless / f'kilo-proxy-headless-linux-{arch}'
        shutil.copyfile('/bin/true', desktop)
        desktop.chmod(0o755)
        headless.write_bytes(elf(arch))
        headless.chmod(0o755)
        with patch.object(package, 'verify_version'), patch.dict(os.environ, {'SOURCE_DATE_EPOCH': '0'}):
            first = package.build_packages(self.desktop, self.headless, arch, '0.56.3', self.output, root=self.root)
            second = package.build_packages(self.desktop, self.headless, arch, '0.56.3', self.root / 'second output', root=self.root)
        for candidate, repeated in zip(first, second):
            self.assertEqual(candidate.read_bytes(), repeated.read_bytes())
            name = 'kilo-proxy-headless' if 'headless' in candidate.name else 'kilo-proxy-desktop'
            fields = subprocess.run(['dpkg-deb', '--field', str(candidate), 'Package', 'Version', 'Architecture', 'Depends'], check=True, capture_output=True, text=True).stdout
            self.assertIn('Package: ' + name, fields)
            self.assertIn('Version: 0.56.3', fields)
            self.assertIn('Architecture: ' + arch, fields)
            extracted = self.root / ('extracted-' + name)
            subprocess.run(['dpkg-deb', '--raw-extract', str(candidate), str(extracted)], check=True)
            self.assertEqual({path.name for path in (extracted / 'DEBIAN').iterdir()}, {'control', 'md5sums'})
            binary = 'kilo-proxy-headless' if 'headless' in name else 'kilo-proxy'
            original = headless if 'headless' in name else desktop
            self.assertEqual((extracted / 'usr/bin' / binary).read_bytes(), original.read_bytes())


if __name__ == '__main__':
    unittest.main()

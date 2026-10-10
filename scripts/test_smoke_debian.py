#!/usr/bin/env python3
"""Check the destructive APT smoke's host guard and fixture integrity without installing packages."""
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import smoke_debian


class DisposableGuardTests(unittest.TestCase):
    def guarded(self, *, system='Linux', uid=0, opted_in='1', marker=True,
                ubuntu='24.04', machine='x86_64', tools=True):
        with patch.object(smoke_debian.platform, 'system', return_value=system), \
                patch.object(smoke_debian.os, 'geteuid', return_value=uid, create=True), \
                patch.dict(os.environ, {'KILO_DEBIAN_DISPOSABLE_CONTAINER': opted_in}), \
                patch.object(Path, 'exists', return_value=marker), \
                patch.object(smoke_debian, 'os_release', return_value={'ID': 'ubuntu', 'VERSION_ID': ubuntu}), \
                patch.object(smoke_debian.platform, 'machine', return_value=machine), \
                patch.object(smoke_debian.shutil, 'which', return_value='/usr/bin/tool' if tools else None):
            smoke_debian.require_disposable_container('24.04', 'amd64')

    def test_root_without_container_marker_is_rejected(self):
        with self.assertRaisesRegex(RuntimeError, 'disposable'):
            self.guarded(marker=False)

    def test_container_without_explicit_opt_in_is_rejected(self):
        with self.assertRaisesRegex(RuntimeError, 'disposable'):
            self.guarded(opted_in='')

    def test_nonroot_or_nonlinux_is_rejected(self):
        for values in ({'uid': 1000}, {'system': 'Darwin'}):
            with self.subTest(values=values), self.assertRaisesRegex(RuntimeError, 'disposable'):
                self.guarded(**values)

    def test_wrong_distribution_release_or_architecture_is_rejected(self):
        for values in ({'ubuntu': '26.04'}, {'machine': 'aarch64'}):
            with self.subTest(values=values), self.assertRaises(RuntimeError):
                self.guarded(**values)

    def test_missing_required_tool_fails(self):
        with self.assertRaisesRegex(RuntimeError, 'Missing'):
            self.guarded(tools=False)

    def test_native_opted_in_container_is_accepted(self):
        self.guarded()

    def test_smoke_guard_runs_before_package_tool_or_file_mutation(self):
        with patch.object(smoke_debian, 'require_disposable_container', side_effect=RuntimeError('refused')), \
                patch.object(smoke_debian, 'Commands') as commands, \
                patch.object(smoke_debian.tempfile, 'TemporaryDirectory') as temporary:
            with self.assertRaisesRegex(RuntimeError, 'refused'):
                smoke_debian.smoke(Path('/repo'), Path('/key'), '24.04', 'amd64', '0.56.3')
            commands.assert_not_called()
            temporary.assert_not_called()


class FixtureTests(unittest.TestCase):
    def test_semantic_to_debian_version_preserves_precedence(self):
        self.assertEqual(smoke_debian.expected_package_version('0.56.3'), '0.56.3')
        self.assertEqual(smoke_debian.expected_package_version('0.57.0-beta.2'), '0.57.0~beta.2')
        for version in ('01.56.3', '0.56.3\nPackage: injected', '0.56.3+local', '0.56.3~alpha.1'):
            with self.subTest(version=version), self.assertRaises(ValueError):
                smoke_debian.expected_package_version(version)

    def test_baseline_only_rewrites_one_version_field(self):
        original = 'Package: kilo-proxy-desktop\nVersion: 0.56.3\nArchitecture: amd64\nDescription: preserve this\n'
        expected = original.replace('Version: 0.56.3', 'Version: 0.56.3~apt-smoke.1')
        self.assertEqual(smoke_debian.replace_control_version(original, '0.56.3~apt-smoke.1'), expected)
        for control in ('Package: kilo-proxy-desktop\n', original + 'Version: 0.56.2\n'):
            with self.assertRaises(ValueError):
                smoke_debian.replace_control_version(control, '0.56.3~apt-smoke.1')
        with self.assertRaises(ValueError):
            smoke_debian.replace_control_version(original, '0.56.3\nPackage: injected')

    def test_environment_does_not_inherit_accounts_or_desktop_session(self):
        home = Path('/temporary/synthetic-home')
        with patch.dict(os.environ, {'OPENAI_API_KEY': 'real-secret', 'DISPLAY': ':0',
                                     'DBUS_SESSION_BUS_ADDRESS': 'real-bus', 'CODEX_HOME': '/real/codex'}):
            env = smoke_debian.clean_environment(home)
        self.assertFalse(set(env) & {'OPENAI_API_KEY', 'DISPLAY', 'DBUS_SESSION_BUS_ADDRESS', 'CODEX_HOME'})
        self.assertEqual(env['HOME'], str(home))
        self.assertEqual(env['LIBGL_ALWAYS_SOFTWARE'], '1')

    def test_public_repository_refuses_symlinks_before_copy(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / 'source'
            source.mkdir()
            try:
                (source / 'outside').symlink_to(root, target_is_directory=True)
            except OSError:
                if os.name == 'nt':
                    self.skipTest('Windows host does not permit directory symlink fixtures')
                raise
            with self.assertRaisesRegex(ValueError, 'symlink'):
                smoke_debian.copy_public_repository(source, root / 'copy')
            self.assertFalse((root / 'copy').exists())

    def test_public_repository_is_readable_to_apt_sandbox(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / 'source'
            source.mkdir(mode=0o700)
            (source / 'InRelease').write_text('signed metadata')
            (source / 'InRelease').chmod(0o600)
            destination = root / 'copy'
            smoke_debian.copy_public_repository(source, destination)
            self.assertEqual((destination / 'InRelease').read_text(), 'signed metadata')
            if os.name == 'posix':
                self.assertEqual(destination.stat().st_mode & 0o777, 0o755)
                self.assertEqual((destination / 'InRelease').stat().st_mode & 0o777, 0o644)

    def test_sentinels_detect_removed_and_rewritten_user_config(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'settings-sentinel'
            path.write_bytes(b'preserve')
            smoke_debian.assert_preserved({path: b'preserve'})
            path.write_bytes(b'changed')
            with self.assertRaisesRegex(RuntimeError, 'sentinel'):
                smoke_debian.assert_preserved({path: b'preserve'})
            path.unlink()
            with self.assertRaisesRegex(RuntimeError, 'sentinel'):
                smoke_debian.assert_preserved({path: b'preserve'})


if __name__ == '__main__':
    unittest.main()

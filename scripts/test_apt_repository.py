"""Test APT metadata, immutable retention and real GnuPG signatures."""
import gzip
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import build_apt_repository as apt


class RepositoryUnitTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)

    def test_control_preserves_multiline_fields_and_rejects_ambiguity(self):
        self.assertEqual(apt.parse_control('Package: kilo-proxy-desktop\nDescription: App\n Long text\n .\n More\n'),
                         {'Package': 'kilo-proxy-desktop', 'Description': 'App\n Long text\n .\n More'})
        for text in ('Package: one\npackage: two\n', 'Package: one\n\nVersion: 1\n',
                     ' continuation\n', 'Invalid line\n', ''):
            with self.subTest(text=text), self.assertRaises(ValueError):
                apt.parse_control(text)

    def test_pinned_primary_key_and_explicit_home_are_required(self):
        for fingerprint in ('12345678', 'person@example.test', 'A' * 39, 'G' * 40):
            with self.subTest(fingerprint=fingerprint), self.assertRaises(ValueError):
                apt.validate_key(self.root, fingerprint)
        with self.assertRaises(ValueError):
            apt.validate_key(self.root / 'missing', 'A' * 40)
        listing = 'sec:u:255:22:key:1:0::::scSC:\nfpr:::::::::' + 'A' * 40 + ':\n'
        with patch.object(apt, 'gpg', return_value=subprocess.CompletedProcess([], 0, listing.encode())):
            self.assertEqual(apt.validate_key(self.root, 'a' * 40), 'A' * 40)
            with self.assertRaises(ValueError):
                apt.validate_key(self.root, 'B' * 40)
        with patch.object(apt, 'gpg', return_value=subprocess.CompletedProcess([], 0, listing.replace('sec:u:', 'sec:e:').encode())):
            with self.assertRaises(ValueError):
                apt.validate_key(self.root, 'A' * 40)

    def test_gpg_never_uses_default_home_or_user_configuration(self):
        with patch.object(apt, 'run') as run:
            apt.gpg(self.root, '--export', 'A' * 40)
        args = run.call_args.args
        self.assertEqual(args[:4], ('gpg', '--no-options', '--homedir', self.root))
        self.assertIn('--no-auto-key-retrieve', args)

    def test_signature_requires_one_valid_pinned_primary_or_subkey(self):
        primary, subkey = 'A' * 40, 'B' * 40
        status = f'[GNUPG:] VALIDSIG {subkey} 2026-10-10 1 0 4 0 22 8 00 {primary}\n'
        with patch.object(apt, 'gpg', return_value=subprocess.CompletedProcess([], 0, status.encode())):
            apt.verify_signature(self.root, primary, 'signature', 'data')
            with self.assertRaises(ValueError):
                apt.verify_signature(self.root, 'C' * 40, 'signature', 'data')
        with patch.object(apt, 'gpg', return_value=subprocess.CompletedProcess([], 0, (status * 2).encode())):
            with self.assertRaises(ValueError):
                apt.verify_signature(self.root, primary, 'signature')

    def test_snapshot_and_index_paths_reject_symlinks_and_traversal(self):
        source = self.root / 'real.deb'
        source.write_bytes(b'fixture')
        link = self.root / 'link.deb'
        try:
            link.symlink_to(source)
        except OSError:
            self.skipTest('This platform does not permit temporary test symlinks')
        with self.assertRaises(ValueError):
            apt.snapshot(link, self.root / 'copy.deb')
        apt.snapshot(source, self.root / 'copy.deb')
        self.assertEqual((self.root / 'copy.deb').read_bytes(), b'fixture')
        directory = self.root / 'linked-directory'
        directory.symlink_to(self.root, target_is_directory=True)
        for path in ('../real.deb', '/real.deb', 'link.deb', 'linked-directory/real.deb'):
            with self.subTest(path=path), self.assertRaises(ValueError):
                apt.safe_repository_path(self.root, path)

    def test_retained_manifest_detects_tamper_unindexed_files_and_bad_paths(self):
        pool = self.root / 'pool/main/k/kilo-proxy'
        pool.mkdir(parents=True)
        archive = pool / 'kilo-proxy-desktop_0.56.3_amd64.deb'
        archive.write_bytes(b'fixture')
        manifest = apt.retained_manifest(self.root)
        self.assertEqual(len(apt.verify_retained(self.root, manifest)), 1)
        archive.write_bytes(b'tampered')
        with self.assertRaises(ValueError):
            apt.verify_retained(self.root, manifest)
        manifest = apt.retained_manifest(self.root)
        (pool / 'unexpected.deb').write_bytes(b'rogue')
        with self.assertRaises(ValueError):
            apt.verify_retained(self.root, manifest)
        with self.assertRaises(ValueError):
            apt.verify_retained(self.root, manifest + manifest)
        with self.assertRaises(ValueError):
            apt.verify_retained(self.root, manifest.replace('pool/main', '../main'))

    def test_existing_output_and_missing_matrix_fail_without_changing_output(self):
        packages, output = self.root / 'packages', self.root / 'apt'
        packages.mkdir()
        output.mkdir()
        marker = output / 'preserve'
        marker.write_text('existing repository')
        with self.assertRaises(ValueError):
            apt.build_repository(packages, output, 'A' * 40, self.root)
        self.assertEqual(marker.read_text(), 'existing repository')
        with patch.object(apt, 'validate_key', return_value='A' * 40):
            with self.assertRaises(ValueError):
                apt.build_repository(packages, self.root / 'new', 'A' * 40, self.root)
        self.assertFalse((self.root / 'new').exists())

    @unittest.skipUnless(sys.platform in ('linux', 'darwin'), 'Atomic APT publication targets Linux/macOS')
    def test_atomic_directory_publication_does_not_replace_even_an_empty_directory(self):
        source, output = self.root / 'stage', self.root / 'apt'
        source.mkdir()
        (source / 'complete').write_text('verified')
        output.mkdir()
        with self.assertRaises(FileExistsError):
            apt.rename_new_directory(source, output)
        self.assertTrue((source / 'complete').exists())
        self.assertEqual(list(output.iterdir()), [])
        output.rmdir()
        apt.rename_new_directory(source, output)
        self.assertEqual((output / 'complete').read_text(), 'verified')

    def test_metadata_rejects_unexpected_names_arch_versions_and_index_fields(self):
        good = ('Package: kilo-proxy-desktop\nVersion: 0.56.3\nArchitecture: amd64\n'
                'Maintainer: Kilo Proxy\nDescription: App\n')
        bad = [good.replace('kilo-proxy-desktop', 'other'), good.replace('amd64', 'all'),
               good.replace('0.56.3', '0.56.3-rc.1'), good + 'Filename: ../bad.deb\n',
               good.replace('Maintainer: Kilo Proxy\n', '')]
        for text in bad:
            with self.subTest(text=text), patch.object(apt, 'run', return_value=subprocess.CompletedProcess([], 0, text)), self.assertRaises(ValueError):
                apt.metadata(self.root / 'fixture.deb')


@unittest.skipUnless(shutil.which('gpg') and shutil.which('dpkg-deb'),
                     'Real GnuPG and Debian package tools are required (Linux CI runs these tests)')
class RepositoryIntegrationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temporary.name)
        cls.home = cls.root / 'gnupg'
        cls.home.mkdir(mode=0o700)
        subprocess.run(['gpg', '--no-options', '--homedir', str(cls.home), '--batch', '--pinentry-mode',
                        'loopback', '--passphrase', '', '--quick-generate-key',
                        'APT repository test <apt-fixture@example.invalid>', 'ed25519', 'sign', '0'],
                       check=True, capture_output=True)
        listing = apt.gpg(cls.home, '--with-colons', '--list-secret-keys').stdout.decode()
        cls.fingerprint = next(line.split(':')[9] for line in listing.splitlines() if line.startswith('fpr:'))

    @classmethod
    def tearDownClass(cls):
        # Stop only the agent belonging to this ephemeral home; never a user's.
        if shutil.which('gpgconf'):
            subprocess.run(['gpgconf', '--homedir', str(cls.home), '--kill', 'gpg-agent'],
                           check=False, capture_output=True)
        cls.temporary.cleanup()

    def setUp(self):
        self.temporary_case = tempfile.TemporaryDirectory(dir=self.root)
        self.addCleanup(self.temporary_case.cleanup)
        self.case = Path(self.temporary_case.name)
        self.packages = self.case / 'packages'
        self.packages.mkdir()
        self.write_packages('0.56.3')

    def write_packages(self, version):
        for old in self.packages.glob('*.deb'):
            old.unlink()
        for package in apt.PACKAGES:
            for architecture in apt.ARCHITECTURES:
                self.write_package(package, architecture, version)

    def write_package(self, package, architecture, version, payload=b'fixture payload'):
        name = f'{package}_{version}_{architecture}'
        tree = self.case / ('tree-' + name)
        (tree / 'DEBIAN').mkdir(parents=True, exist_ok=True)
        (tree / 'DEBIAN/control').write_text(
            f'Package: {package}\nVersion: {version}\nArchitecture: {architecture}\n'
            'Maintainer: Kilo Proxy fixture <apt-fixture@example.invalid>\n'
            'Depends: libc6\nDescription: Fixture package\n Preserved continuation\n')
        (tree / 'usr/share/kilo-proxy').mkdir(parents=True, exist_ok=True)
        (tree / 'usr/share/kilo-proxy/fixture').write_bytes(payload)
        path = self.packages / (name + '.deb')
        subprocess.run(['dpkg-deb', '--root-owner-group', '--build', str(tree), str(path)],
                       check=True, capture_output=True)
        return path

    def build(self, name='apt', previous=None):
        output = self.case / name
        version = apt.build_repository(self.packages, output, self.fingerprint, self.home,
                                       source_date_epoch=1700000000, previous_repository=previous)
        self.assertEqual(version, apt.verify_repository(output, self.fingerprint, self.home))
        return output

    def test_real_signed_repository_complete_matrix_and_deterministic_indexes(self):
        output = self.build()
        second = self.build('apt-second')
        self.assertEqual(len(list((output / 'pool').rglob('*.deb'))), 4)
        self.assertTrue((output / apt.KEY_FILENAME).read_bytes().startswith(b'-----BEGIN PGP PUBLIC KEY BLOCK-----'))
        for suite in apt.SUITES:
            fields = apt.parse_control((output / f'dists/{suite}/Release').read_text())
            self.assertEqual(fields['Version'], '0.56.3')
            self.assertNotIn('Valid-Until', fields)
            for architecture in apt.ARCHITECTURES:
                path = output / f'dists/{suite}/main/binary-{architecture}/Packages'
                self.assertIn('Depends: libc6\n', path.read_text())
                self.assertIn(' Preserved continuation\n', path.read_text())
                self.assertEqual(gzip.decompress(path.with_suffix('.gz').read_bytes()), path.read_bytes())
                self.assertEqual(path.read_bytes(), (second / path.relative_to(output)).read_bytes())
                self.assertEqual(path.with_suffix('.gz').read_bytes(), (second / path.with_suffix('.gz').relative_to(output)).read_bytes())

    def test_tampered_release_pool_indexes_signatures_and_exported_key_are_rejected(self):
        output = self.build()
        for relative in ('dists/noble/Release', 'dists/noble/InRelease',
                         'dists/resolute/main/binary-arm64/Packages',
                         'pool/main/k/kilo-proxy/kilo-proxy-desktop_0.56.3_amd64.deb', apt.KEY_FILENAME):
            path = output / relative
            original = path.read_bytes()
            try:
                if relative == apt.KEY_FILENAME:
                    path.write_bytes(b'not a public key')
                elif relative.endswith('/InRelease'):
                    path.write_bytes(original.replace(b'Origin: Kilo Proxy', b'Origin: Evil Proxy'))
                else:
                    path.write_bytes(original + b'\ntampered\n')
                with self.subTest(path=relative), self.assertRaises((ValueError, subprocess.CalledProcessError)):
                    apt.verify_repository(output, self.fingerprint, self.home)
            finally:
                path.write_bytes(original)
        with self.assertRaises((ValueError, subprocess.CalledProcessError)):
            apt.verify_repository(output, 'F' * 40, self.home)

    def test_old_pool_and_by_hash_survive_upgrade_with_signed_inventory(self):
        first = self.build('old')
        old_files = {path.relative_to(first): path.read_bytes() for path in apt.retained_files(first)}
        self.write_packages('0.56.4')
        updated = self.build('new', previous=first)
        self.assertEqual(len(list((updated / 'pool').rglob('*.deb'))), 8)
        for relative, data in old_files.items():
            self.assertEqual((updated / relative).read_bytes(), data)
        for suite in apt.SUITES:
            for architecture in apt.ARCHITECTURES:
                self.assertIn('Version: 0.56.4\n', (updated / f'dists/{suite}/main/binary-{architecture}/Packages').read_text())
        self.write_packages('0.56.3')
        with self.assertRaisesRegex(ValueError, 'downgrade'):
            self.build('downgrade', previous=updated)
        self.assertFalse((self.case / 'downgrade').exists())

    def test_debian_mapped_prerelease_builds_for_ci_and_upgrades_to_stable(self):
        self.write_packages('0.56.4~beta.1')
        beta = self.build('beta')
        for suite in apt.SUITES:
            release = apt.parse_control((beta / f'dists/{suite}/Release').read_text())
            self.assertEqual(release['Version'], '0.56.4~beta.1')
        self.write_packages('0.56.4')
        stable = self.build('stable', previous=beta)
        self.assertEqual(len(list((stable / 'pool').rglob('*.deb'))), 8)
        self.write_packages('0.56.4~rc.1')
        with self.assertRaisesRegex(ValueError, 'downgrade'):
            self.build('rc-after-stable', previous=stable)

    def test_mutated_previous_state_and_replaced_version_cannot_publish(self):
        first = self.build('old')
        self.write_package('kilo-proxy-desktop', 'amd64', '0.56.3', b'different bytes')
        with self.assertRaisesRegex(ValueError, 'already published'):
            self.build('replacement', previous=first)
        self.assertFalse((self.case / 'replacement').exists())
        archive = next((first / 'pool').rglob('*.deb'))
        archive.write_bytes(b'tampered old package')
        with self.assertRaises((ValueError, subprocess.CalledProcessError)):
            self.build('bad-previous', previous=first)
        self.assertFalse((self.case / 'bad-previous').exists())

    def test_mixed_versions_duplicate_identity_truncated_deb_and_unsigned_file_rejected(self):
        victim = self.packages / 'kilo-proxy-desktop_0.56.3_amd64.deb'
        victim.unlink()
        self.write_package('kilo-proxy-desktop', 'amd64', '0.56.4')
        with self.assertRaisesRegex(ValueError, 'same version'):
            self.build('mixed')
        self.write_packages('0.56.3')
        victim = self.packages / 'kilo-proxy-desktop_0.56.3_amd64.deb'
        victim.write_bytes(victim.read_bytes()[:64])
        with self.assertRaises(subprocess.CalledProcessError):
            self.build('truncated')
        self.write_packages('0.56.3')
        other = self.packages / 'kilo-proxy-headless_0.56.3_arm64.deb'
        other.write_bytes(victim.read_bytes())
        with self.assertRaisesRegex(ValueError, 'Duplicate'):
            self.build('duplicate')
        self.write_packages('0.56.3')
        output = self.build()
        (output / 'pool/main/k/kilo-proxy/unsigned.deb').write_bytes(b'unsigned')
        with self.assertRaisesRegex(ValueError, 'Unsigned'):
            apt.verify_repository(output, self.fingerprint, self.home)


if __name__ == '__main__':
    unittest.main()

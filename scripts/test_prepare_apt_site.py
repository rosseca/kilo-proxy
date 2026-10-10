"""Guard publication inputs, authenticated history recovery and safe extraction."""
import hashlib
import io
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import urllib.error

import prepare_apt_site as site


class AptSiteTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='kilo-apt-site-tests-')
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.packages = self.root / 'packages'
        self.packages.mkdir()
        self.names = sorted(f'kilo-proxy-{kind}_1.2.3_{arch}.deb'
                            for kind in ('desktop', 'headless') for arch in ('amd64', 'arm64'))
        for name in self.names:
            (self.packages / name).write_bytes(name.encode())
        self.manifest = self.packages / 'DEBIAN-SHA256SUMS.txt'
        self.lines = [hashlib.sha256((self.packages / name).read_bytes()).hexdigest() + '  ./' + name
                      for name in self.names]
        self.manifest.write_text('\n'.join(self.lines) + '\n')

    def test_tested_package_manifest_rejects_changed_bytes(self):
        site.verify_packages(self.packages, '1.2.3')
        (self.packages / self.names[0]).write_bytes(b'changed after testing')
        with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
            site.verify_packages(self.packages, '1.2.3')

    def test_manifest_rejects_duplicate_missing_extra_and_unsafe_entries(self):
        for lines in (self.lines[:-1], self.lines + [self.lines[0]],
                      self.lines + ['0' * 64 + '  ../escape.deb'],
                      self.lines + ['0' * 64 + '  unrelated.deb']):
            self.manifest.write_text('\n'.join(lines) + '\n')
            with self.subTest(lines=lines), self.assertRaises(ValueError):
                site.verify_packages(self.packages, '1.2.3')

    def test_wrong_version_and_prerelease_are_not_published(self):
        for version in ('1.2.4', '1.2.3-beta.1', 'v1.2.3', '1.2.3\nunsafe'):
            with self.subTest(version=version), self.assertRaises(ValueError):
                site.verify_packages(self.packages, version)

    def test_package_symlink_is_not_a_tested_input(self):
        path = self.packages / self.names[0]
        original = self.root / 'original'
        path.rename(original)
        try:
            path.symlink_to(original)
        except OSError:
            self.skipTest('Host does not allow symlink fixtures')
        with self.assertRaises(ValueError):
            site.verify_packages(self.packages, '1.2.3')

    def test_snapshot_cannot_escape_or_install_links(self):
        for name, kind in (('../escape', tarfile.REGTYPE), ('/escape', tarfile.REGTYPE),
                           ('pool/link', tarfile.SYMTYPE), ('pool/link', tarfile.LNKTYPE)):
            archive = self.root / 'unsafe.tar.gz'
            with tarfile.open(archive, 'w:gz') as out:
                entry = tarfile.TarInfo(name)
                entry.type = kind
                entry.linkname = '/outside'
                out.addfile(entry, io.BytesIO())
            destination = self.root / 'output'
            destination.mkdir(exist_ok=True)
            with self.subTest(name=name, kind=kind), self.assertRaises(ValueError):
                site.extract_snapshot(archive, destination)
            self.assertEqual(list(destination.iterdir()), [])

    def test_verified_snapshot_extracts_without_restoring_permissions(self):
        archive = self.root / 'safe.tar.gz'
        with tarfile.open(archive, 'w:gz') as out:
            entry = tarfile.TarInfo('pool/package.deb')
            entry.size = 4
            entry.mode = 0o6777
            out.addfile(entry, io.BytesIO(b'data'))
        destination = self.root / 'output'
        destination.mkdir()
        site.extract_snapshot(archive, destination)
        path = destination / 'pool/package.deb'
        self.assertEqual(path.read_bytes(), b'data')
        if site.os.name == 'posix':
            self.assertEqual(path.stat().st_mode & 0o7777, 0o644)

    def test_missing_history_is_not_silent_bootstrap(self):
        absent = urllib.error.HTTPError('https://example.invalid/apt', 404, 'Not found', {}, None)
        with patch.object(site, 'download', side_effect=absent), self.assertRaises(urllib.error.HTTPError):
            site.previous_repository('https://example.invalid/apt', 'A' * 40, self.root, self.root, False)
        with patch.object(site, 'download', side_effect=absent):
            self.assertIsNone(site.previous_repository('https://example.invalid/apt', 'A' * 40,
                                                       self.root, self.root, True))

    def test_bootstrap_does_not_reset_existing_metadata(self):
        absent = urllib.error.HTTPError('https://example.invalid/apt', 404, 'Not found', {}, None)
        with patch.object(site, 'download', side_effect=[absent, None]), self.assertRaisesRegex(ValueError, 'bootstrap refused'):
            site.previous_repository('https://example.invalid/apt', 'A' * 40, self.root, self.root, True)

    def test_signed_archive_is_verified_before_extraction(self):
        with patch.object(site, 'download'), patch.object(site.subprocess, 'run', side_effect=[subprocess.CompletedProcess([], 0), RuntimeError('bad signature')]), \
                patch.object(site, 'extract_snapshot') as extract, self.assertRaises(RuntimeError):
            site.previous_repository('https://example.invalid/apt', 'A' * 40, self.root, self.root, False)
        extract.assert_not_called()

    def test_older_release_cannot_replace_newer_apt_repository(self):
        release = self.root / 'dists/noble/Release'
        release.parent.mkdir(parents=True)
        release.write_text('Origin: Kilo Proxy\nVersion: 1.10.0\n')
        site.check_previous_version(self.root, '1.10.0')
        site.check_previous_version(self.root, '1.11.0')
        with self.assertRaisesRegex(ValueError, 'older release'):
            site.check_previous_version(self.root, '1.9.9')
        release.write_text('Origin: Kilo Proxy\n')
        with self.assertRaises(ValueError):
            site.check_previous_version(self.root, '1.11.0')


@unittest.skipUnless(shutil.which('gpg') and shutil.which('gpgv') and shutil.which('dpkg-deb'),
                     'Real isolated signing and Debian tools required')
class AptSiteIntegrationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        import test_apt_repository as fixtures
        cls.fixtures = fixtures.RepositoryIntegrationTests
        cls.fixtures.setUpClass()

    @classmethod
    def tearDownClass(cls):
        cls.fixtures.tearDownClass()

    def setUp(self):
        self.fixture = self.fixtures()
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.root = self.fixture.case
        self.source = self.root / 'source'
        (self.source / 'scripts').mkdir(parents=True)
        shutil.copyfile(site.ROOT / 'scripts/build_apt_repository.py', self.source / 'scripts/build_apt_repository.py')
        self.identity = self.fixture.fingerprint

    def write_candidate(self, version):
        (self.source / 'VERSION').write_text(version)
        self.fixture.write_packages(version)
        packages = self.fixture.packages
        (packages / 'DEBIAN-SHA256SUMS.txt').write_text(''.join(
            hashlib.sha256(path.read_bytes()).hexdigest() + '  ./' + path.name + '\n'
            for path in sorted(packages.glob('*.deb'))))

    def publish_candidate(self, name, previous=None):
        def public_download(url, destination, maximum):
            if previous is None:
                raise urllib.error.HTTPError(url, 404, 'Not found', {}, None)
            source = previous / 'apt' / url.rsplit('/', 1)[-1]
            shutil.copyfile(source, destination)
        output = self.root / name
        with patch.object(site, 'ROOT', self.source), patch.object(site, 'download', side_effect=public_download):
            site.prepare(self.fixture.packages, output, self.identity, self.fixture.home,
                         'https://example.invalid/apt', previous is None, 1700000000)
        return output

    def test_real_site_bootstrap_and_authenticated_history_upgrade(self):
        self.write_candidate('1.2.3')
        initial = self.publish_candidate('initial')
        self.assertTrue((initial / 'apt/repository-state.tar.gz.asc').is_file())
        self.write_candidate('1.2.4')
        updated = self.publish_candidate('updated', initial)
        self.assertEqual(len(list((updated / 'apt/pool').rglob('*.deb'))), 8)
        self.assertIn('Version: 1.2.4\n', (updated / 'apt/dists/noble/Release').read_text())
        self.write_candidate('1.2.3')
        with self.assertRaisesRegex(ValueError, 'older release'):
            self.publish_candidate('downgrade', updated)
        self.assertFalse((self.root / 'downgrade').exists())

    def test_tampered_public_snapshot_never_reaches_extraction_or_output(self):
        self.write_candidate('1.2.3')
        initial = self.publish_candidate('initial')
        archive = initial / 'apt/repository-state.tar.gz'
        archive.write_bytes(archive.read_bytes() + b'tampered bytes')
        with patch.object(site, 'extract_snapshot') as extract, self.assertRaises(subprocess.CalledProcessError):
            self.publish_candidate('tampered', initial)
        extract.assert_not_called()
        self.assertFalse((self.root / 'tampered').exists())


if __name__ == '__main__':
    unittest.main()

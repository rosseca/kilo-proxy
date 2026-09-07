"""Check release gates and publication ordering without contacting GitHub."""
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import release


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.version = '0.12.0'
        self.prepare()

    def prepare(self):
        self.paths = []
        lines = []
        for name in release.asset_names(self.version):
            path = self.directory / name
            path.write_bytes(b'archive fixture for ' + name.encode())
            self.paths.append(path)
            lines.append(hashlib.sha256(path.read_bytes()).hexdigest() + '  ' + name)
        self.manifest = self.directory / 'SHA256SUMS.txt'
        self.manifest.write_text('\n'.join(lines) + '\n')

    def test_version_and_tag_validation(self):
        for version in ['0.12.0', '1.0.0-rc.1', '1.0.0-beta.2', '1.0.0-alpha.1']:
            (self.directory / 'VERSION').write_text(version + '\n')
            self.assertEqual(release.read_version(self.directory), version)
            release.check_tag('v' + version, version)
        for version in ['../escape', '01.0.0', 'v1.0.0', '1.0.0-rc.0', '1.0.0; command']:
            (self.directory / 'VERSION').write_text(version)
            with self.assertRaises(ValueError):
                release.read_version(self.directory)
        with self.assertRaises(ValueError):
            release.check_tag('v0.11.0', self.version)

    def test_complete_six_target_manifest(self):
        paths = release.verify_assets(self.directory, self.version)
        self.assertEqual(len(paths), 7)
        self.assertTrue(any(p.name.endswith('windows-arm64.zip') for p in paths))

    def test_missing_tampered_duplicate_and_traversal_assets(self):
        self.paths[0].unlink()
        with self.assertRaises(ValueError):
            release.verify_assets(self.directory, self.version)
        self.prepare()
        self.paths[0].write_bytes(b'changed')
        with self.assertRaises(ValueError):
            release.verify_assets(self.directory, self.version)
        self.prepare()
        original = self.manifest.read_text()
        for bad in [original + original.splitlines()[0] + '\n',
                    '\n'.join(original.splitlines()[:-1]),
                    original.replace(self.paths[0].name, '../outside.zip')]:
            self.manifest.write_text(bad)
            with self.assertRaises(ValueError):
                release.verify_assets(self.directory, self.version)

    def fake_gh(self, *args, check=True):
        self.calls.append(args)
        if args[1] == 'view' and args[-1] == 'isDraft':
            return subprocess.CompletedProcess(args, 0 if self.exists else 1,
                                               json.dumps({'isDraft': self.draft}), '')
        if args[1] == 'upload' and self.fail_upload:
            raise subprocess.CalledProcessError(1, args, stderr='upload failed')
        if args[1] == 'view' and args[-1] == 'assets':
            assets = [{'name': p.name, 'size': p.stat().st_size} for p in self.paths + [self.manifest]]
            return subprocess.CompletedProcess(args, 0, json.dumps({'assets': assets}), '')
        return subprocess.CompletedProcess(args, 0, 'https://example.test/release', '')

    def run_publication(self, exists=False, draft=True, fail_upload=False):
        self.calls, self.exists, self.draft, self.fail_upload = [], exists, draft, fail_upload
        with patch.object(release, 'gh', side_effect=self.fake_gh):
            release.publish('v' + self.version, self.version, self.directory)

    def test_publish_only_after_upload_and_remote_verification(self):
        self.run_publication()
        self.assertEqual([c[1] for c in self.calls], ['view', 'create', 'upload', 'view', 'edit', 'view'])
        self.assertIn('--draft', self.calls[1])
        self.assertIn('--latest=true', self.calls[-2])
        self.assertIn('--verify-tag', self.calls[1])

    def test_prerelease_does_not_replace_latest(self):
        self.version = '0.12.0-rc.1'
        self.prepare()
        self.run_publication()
        self.assertIn('--prerelease', self.calls[1])
        self.assertIn('--latest=false', self.calls[-2])

    def test_draft_can_resume_but_published_release_cannot_change(self):
        self.run_publication(exists=True)
        self.assertNotIn('create', [c[1] for c in self.calls])
        with self.assertRaises(ValueError):
            self.run_publication(exists=True, draft=False)
        self.assertEqual(len(self.calls), 1)

    def test_bad_checksums_and_failed_upload_do_not_publish(self):
        self.paths[0].write_bytes(b'bad')
        with self.assertRaises(ValueError):
            self.run_publication()
        self.assertEqual(self.calls, [])
        self.prepare()
        with self.assertRaises(subprocess.CalledProcessError):
            self.run_publication(fail_upload=True)
        self.assertNotIn('edit', [c[1] for c in self.calls])


if __name__ == '__main__':
    unittest.main()

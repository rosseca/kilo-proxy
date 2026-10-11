"""Fail closed when recovering the one-time APT activation artifact."""
import copy
import hashlib
import io
from pathlib import Path
import stat
import tempfile
import unittest
from unittest.mock import patch
import zipfile

import validate_apt_bootstrap as bootstrap

HEAD, MAIN, MERGE, RELEASE = 'a' * 40, 'b' * 40, 'c' * 40, 'd' * 40
RUN, ARTIFACT = 123, 456
VERSION = '1.2.3'


def archive_bytes(override=None, duplicate=False):
    files = {f'kilo-proxy-{kind}_{VERSION}_{arch}.deb': (kind + arch).encode()
             for kind in ('desktop', 'headless') for arch in ('amd64', 'arm64')}
    files['DEBIAN-SHA256SUMS.txt'] = ''.join(
        hashlib.sha256(data).hexdigest() + '  ./' + name + '\n'
        for name, data in sorted(files.items())).encode()
    output = io.BytesIO()
    with zipfile.ZipFile(output, 'w') as archive:
        for name, data in files.items():
            item = zipfile.ZipInfo(name)
            item.external_attr = (stat.S_IFREG | 0o644) << 16
            if override:
                item, data = override(item, data)
            archive.writestr(item, data)
        if duplicate:
            archive.writestr(next(iter(files)), b'duplicate')
    return output.getvalue()


class FakeGit:
    def __init__(self):
        self.ancestors = True
        self.version = VERSION
        self.tag_type = 'tag'
        self.changed = {'scripts/package_debian.py', 'docs/ubuntu-apt.md'}

    def output(self, *args):
        if args[0] == 'show':
            return self.version
        if args[0] == 'cat-file':
            return self.tag_type
        return RELEASE

    def ancestor(self, first, second):
        return self.ancestors

    def changed_paths(self, first, second):
        return self.changed


class FakeGitHub:
    def __init__(self):
        self.archive = archive_bytes()
        self.digest = 'sha256:' + hashlib.sha256(self.archive).hexdigest()
        self.data = {
            f'actions/runs/{RUN}': {
                'id': RUN, 'head_sha': HEAD, 'path': '.github/workflows/build.yml',
                'event': 'pull_request', 'status': 'completed', 'conclusion': 'success',
                'run_attempt': 2,
                'repository': {'full_name': bootstrap.REPOSITORY, 'id': 10, 'private': False},
                'head_repository': {'full_name': bootstrap.REPOSITORY},
                'pull_requests': [],
            },
            f'actions/runs/{RUN}/jobs?filter=latest&per_page=100': {
                'total_count': 30, 'jobs': [
                    {'id': number, 'run_id': RUN, 'head_sha': HEAD, 'name': name,
                     'status': 'completed', 'conclusion': 'success'}
                    for number, name in enumerate(sorted(bootstrap.expected_jobs()), 1)]},
            'commits/main': {'sha': MAIN},
            'commits/' + HEAD + '/pulls?per_page=100': [
                {'number': 43, 'head': {'sha': HEAD}, 'base': {'ref': 'main'}}],
            'pulls/43': {'merged': True, 'merged_at': '2026-10-10T00:00:00Z',
                         'merge_commit_sha': MERGE,
                         'head': {'sha': HEAD, 'repo': {'full_name': bootstrap.REPOSITORY}},
                         'base': {'ref': 'main', 'repo': {'full_name': bootstrap.REPOSITORY}}},
            'releases/latest': {
                'id': 100, 'tag_name': 'v' + VERSION, 'draft': False, 'prerelease': False,
                'published_at': '2026-10-09T00:00:00Z',
                'html_url': f'https://github.com/{bootstrap.REPOSITORY}/releases/tag/v{VERSION}'},
            f'actions/runs/{RUN}/artifacts?per_page=100': {
                'total_count': 1, 'artifacts': [{
                    'id': ARTIFACT, 'name': 'debian-assets', 'expired': False,
                    'digest': self.digest, 'size_in_bytes': len(self.archive),
                    'workflow_run': {'id': RUN, 'head_sha': HEAD,
                                     'repository_id': 10, 'head_repository_id': 10}}]},
        }
        self.downloaded = False
        self.after_download = None

    def get(self, route):
        return copy.deepcopy(self.data[route])

    def download(self, artifact_id, output):
        assert artifact_id == ARTIFACT
        self.downloaded = True
        output.write_bytes(self.archive)
        if self.after_download:
            self.after_download()


class BootstrapValidationTests(unittest.TestCase):
    def setUp(self):
        self.api, self.git = FakeGitHub(), FakeGit()

    def validate(self):
        return bootstrap.validate_source(self.api, self.git, RUN, HEAD, ARTIFACT,
                                         self.api.digest, VERSION, MAIN)

    def test_all_thirty_jobs_and_current_public_release_accept_exact_artifact(self):
        self.assertEqual(len(bootstrap.expected_jobs()), 30)
        self.assertEqual(self.validate()['artifact_id'], ARTIFACT)

    def test_public_pr_metadata_does_not_receive_the_actions_token(self):
        for route in ('commits/' + HEAD + '/pulls?per_page=100', 'pulls/43'):
            with self.subTest(route=route), patch.dict(bootstrap.os.environ, {'GH_TOKEN': 'private-token'}), \
                    patch.object(bootstrap.urllib.request, 'urlopen', return_value=io.BytesIO(b'[]')) as fetch, \
                    patch.object(bootstrap.subprocess, 'check_output') as authenticated:
                self.assertEqual(bootstrap.GitHub().get(route), [])
                request = fetch.call_args[0][0]
                self.assertFalse(request.has_header('Authorization'))
                self.assertEqual(request.full_url, f'https://api.github.com/repos/{bootstrap.REPOSITORY}/{route}')
                authenticated.assert_not_called()

    def test_environment_requires_same_repository_main_manual_pinned_checkout(self):
        good = [bootstrap.REPOSITORY, 'workflow_dispatch', 'refs/heads/main', MAIN, MAIN]
        bootstrap.validate_environment(*good)
        for index, bad in enumerate(('other/repository', 'pull_request', 'refs/tags/v1.2.3', '', HEAD)):
            values = good[:]
            values[index] = bad
            with self.subTest(index=index), self.assertRaises(ValueError):
                bootstrap.validate_environment(*values)

    def test_source_cannot_be_active_failed_fork_or_other_workflow(self):
        for field, bad in (('status', 'in_progress'), ('conclusion', 'failure'),
                           ('path', '.github/workflows/other.yml'), ('head_sha', MAIN),
                           ('event', 'pull_request_target')):
            original = self.api.data[f'actions/runs/{RUN}'][field]
            self.api.data[f'actions/runs/{RUN}'][field] = bad
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.validate()
            self.api.data[f'actions/runs/{RUN}'][field] = original
        self.api.data[f'actions/runs/{RUN}']['head_repository']['full_name'] = 'fork/kilo-proxy'
        with self.assertRaises(ValueError):
            self.validate()

    def test_any_non_green_missing_duplicate_or_foreign_job_is_rejected(self):
        route = f'actions/runs/{RUN}/jobs?filter=latest&per_page=100'
        original = copy.deepcopy(self.api.data[route])
        for field, bad in (('conclusion', 'skipped'), ('conclusion', 'cancelled'),
                           ('status', 'in_progress'), ('head_sha', MAIN),
                           ('run_id', RUN + 1), ('name', 'unrelated job'), ('id', 2)):
            self.api.data[route] = copy.deepcopy(original)
            self.api.data[route]['jobs'][0][field] = bad
            with self.subTest(field=field, bad=bad), self.assertRaises(ValueError):
                self.validate()
        self.api.data[route] = original
        self.api.data[route]['jobs'].pop()
        with self.assertRaises(ValueError):
            self.validate()

    def test_main_advance_or_unmerged_foreign_pull_request_is_rejected(self):
        self.api.data['commits/main']['sha'] = HEAD
        with self.assertRaises(ValueError):
            self.validate()
        self.api.data['commits/main']['sha'] = MAIN
        pr = self.api.data['pulls/43']
        for field, bad in (('merged', False), ('merged_at', None), ('merge_commit_sha', 'invalid')):
            original = pr[field]
            pr[field] = bad
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.validate()
            pr[field] = original
        pr['head']['sha'] = MAIN
        with self.assertRaises(ValueError):
            self.validate()

    def test_latest_release_version_draft_and_prerelease_are_rejected(self):
        release = self.api.data['releases/latest']
        for field, bad in (('tag_name', 'v1.2.4'), ('draft', True), ('prerelease', True),
                           ('published_at', None), ('html_url', 'https://example.invalid')):
            original = release[field]
            release[field] = bad
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.validate()
            release[field] = original

    def test_source_requires_release_ancestry_same_version_and_application_inputs(self):
        for field, bad in (('ancestors', False), ('version', '1.2.4'),
                           ('tag_type', 'commit'), ('changed', {'main.go'})):
            original = getattr(self.git, field)
            setattr(self.git, field, bad)
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.validate()
            setattr(self.git, field, original)

    def test_artifact_id_digest_expiry_repository_and_size_are_pinned(self):
        artifact = self.api.data[f'actions/runs/{RUN}/artifacts?per_page=100']['artifacts'][0]
        for field, bad in (('id', ARTIFACT + 1), ('digest', 'sha256:' + '0' * 64),
                           ('expired', True), ('size_in_bytes', bootstrap.MAX_ARTIFACT + 1)):
            original = artifact[field]
            artifact[field] = bad
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.validate()
            artifact[field] = original
        artifact['workflow_run']['head_repository_id'] = 999
        with self.assertRaises(ValueError):
            self.validate()

    def test_ambiguous_artifact_name_is_rejected(self):
        response = self.api.data[f'actions/runs/{RUN}/artifacts?per_page=100']
        response['artifacts'].append(copy.deepcopy(response['artifacts'][0]))
        response['total_count'] = 2
        with self.assertRaises(ValueError):
            self.validate()

    def test_download_digest_and_second_metadata_validation_precede_extraction(self):
        for change in ('digest', 'main'):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temporary:
                self.api = FakeGitHub()
                if change == 'digest':
                    self.api.archive = self.api.archive[:-1] + b'x'
                else:
                    self.api.after_download = lambda: self.api.data['commits/main'].update(sha=HEAD)
                output = Path(temporary) / 'packages'
                with self.assertRaises(ValueError):
                    bootstrap.recover(self.api, self.git, RUN, HEAD, ARTIFACT, self.api.digest,
                                      VERSION, MAIN, output)
                self.assertFalse(output.exists())

    def test_exact_zip_digest_and_package_manifest_recover_without_overwrite(self):
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / 'packages'
            bootstrap.recover(self.api, self.git, RUN, HEAD, ARTIFACT, self.api.digest,
                              VERSION, MAIN, output)
            self.assertEqual(len(list(output.iterdir())), 5)
            with self.assertRaises(ValueError):
                bootstrap.recover(self.api, self.git, RUN, HEAD, ARTIFACT, self.api.digest,
                                  VERSION, MAIN, output)

    def test_zip_paths_links_extra_files_and_changed_package_manifest_are_rejected(self):
        def traversal(item, data):
            item.filename = '../' + item.filename
            return item, data

        def symlink(item, data):
            item.external_attr = (stat.S_IFLNK | 0o777) << 16
            return item, data

        def tamper(item, data):
            return item, b'tampered' if item.filename.endswith('.deb') else data

        for override in (traversal, symlink, tamper):
            with self.subTest(override=override.__name__), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                archive = root / 'artifact.zip'
                archive.write_bytes(archive_bytes(override))
                with self.assertRaises(ValueError):
                    bootstrap.extract_packages(archive, root / 'packages', VERSION)
                self.assertFalse((root / 'packages').exists())


if __name__ == '__main__':
    unittest.main()

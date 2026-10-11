#!/usr/bin/env python3
"""Recover only the explicitly approved, fully tested APT bootstrap artifact."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import tempfile
import urllib.request
import zipfile

from prepare_apt_site import ROOT, verify_packages

REPOSITORY = 'rosseca/kilo-proxy'
MAX_ARTIFACT = 200 * 1024 * 1024
# The approved source adds APT packaging after 0.56.3; application inputs must
# still be identical to the public release. Keep this one-time scope explicit.
PACKAGING_ONLY_PATHS = {
    '.github/workflows/apt-publish.yml', '.github/workflows/build.yml',
    '.github/workflows/release.yml', 'README.md', 'docs/releases.md',
    'docs/ubuntu-apt.md', 'scripts/build_apt_repository.py',
    'scripts/package_debian.py', 'scripts/prepare_apt_site.py',
    'scripts/smoke_debian.py', 'scripts/test_apt_repository.py',
    'scripts/test_package_debian.py', 'scripts/test_prepare_apt_site.py',
    'scripts/test_smoke_debian.py',
}


def expected_jobs():
    names = {'test (' + runner + ')' for runner in
             ('macos-latest', 'ubuntu-24.04', 'windows-latest')}
    names.update({'browser-e2e (chromium, 1, 1)', 'browser-e2e (webkit, 1, 2)',
                  'browser-e2e (webkit, 2, 2)', 'apt-repository', 'aggregate'})
    targets = [('macos-latest', 'darwin', 'arm64'),
               ('macos-15-intel', 'darwin', 'amd64'),
               ('ubuntu-24.04', 'linux', 'amd64'),
               ('ubuntu-24.04-arm', 'linux', 'arm64'),
               ('windows-latest', 'windows', 'amd64'),
               ('windows-11-arm', 'windows', 'arm64')]
    for runner, system, arch in targets:
        binary = 'kilo-proxy-' + system + '-' + arch + ('.exe' if system == 'windows' else '')
        names.add(f'native ({runner}, {system}, {arch}, {binary})')
        names.add(f'package ({runner}, {system}, {arch})')
        if system != 'windows':
            names.add(f'headless ({runner}, {system}, {arch})')
        if system == 'linux':
            names.add(f'debian-package ({runner}, {arch})')
            for ubuntu in ('24.04', '26.04'):
                names.add(f'apt-install ({runner}, {arch}, {ubuntu})')
    return names


class GitHub:
    def get(self, route):
        if route.startswith('pulls/') or '/pulls?' in route:
            # These two public PR metadata endpoints need no credential. Keep
            # the reusable publisher's token limited to contents/actions read.
            request = urllib.request.Request(
                f'https://api.github.com/repos/{REPOSITORY}/{route}',
                headers={'Accept': 'application/vnd.github+json',
                         'X-GitHub-Api-Version': '2022-11-28',
                         'User-Agent': 'kilo-proxy-apt-bootstrap'})
            with urllib.request.urlopen(request, timeout=60) as response:
                return json.load(response)
        return json.loads(subprocess.check_output(
            ['gh', 'api', '--method', 'GET', 'repos/' + REPOSITORY + '/' + route],
            text=True))

    def download(self, artifact_id, destination):
        with destination.open('xb') as output:
            subprocess.run(['gh', 'api', '--method', 'GET',
                            f'repos/{REPOSITORY}/actions/artifacts/{artifact_id}/zip'],
                           stdout=output, check=True)


class Git:
    def output(self, *args):
        return subprocess.check_output(['git', *args], cwd=ROOT, text=True).strip()

    def ancestor(self, parent, child):
        result = subprocess.run(['git', 'merge-base', '--is-ancestor', parent, child], cwd=ROOT)
        if result.returncode not in (0, 1):
            raise ValueError('Cannot verify source Git ancestry')
        return result.returncode == 0

    def changed_paths(self, first, second):
        paths = subprocess.check_output(['git', 'diff', '--name-only', '-z', first, second], cwd=ROOT)
        return {name.decode('utf-8') for name in paths.split(b'\0') if name}


def validate_environment(repository, event, ref, dispatch_sha, checkout_sha):
    if (repository != REPOSITORY or event != 'workflow_dispatch' or
            ref != 'refs/heads/main' or not re.fullmatch(r'[0-9a-f]{40}', dispatch_sha) or
            checkout_sha != dispatch_sha):
        raise ValueError('APT bootstrap is restricted to a pinned manual dispatch on main')


def validate_source(api, git, run_id, head_sha, artifact_id, digest, version, dispatch_sha):
    if (not re.fullmatch(r'[0-9a-f]{40}', head_sha) or
            not re.fullmatch(r'sha256:[0-9a-f]{64}', digest) or
            not re.fullmatch(r'\d+\.\d+\.\d+', version)):
        raise ValueError('Supply the exact stable version, source SHA and artifact digest')
    run = api.get(f'actions/runs/{run_id}')
    if (run.get('id') != run_id or run.get('head_sha') != head_sha or
            run.get('path') != '.github/workflows/build.yml' or
            run.get('event') != 'pull_request' or run.get('status') != 'completed' or
            run.get('conclusion') != 'success' or
            run.get('repository', {}).get('full_name') != REPOSITORY or
            run.get('repository', {}).get('private') is not False or
            run.get('head_repository', {}).get('full_name') != REPOSITORY):
        raise ValueError('Source CI is not a successful same-repository package run on the pinned SHA')
    jobs = api.get(f'actions/runs/{run_id}/jobs?filter=latest&per_page=100')
    entries = jobs.get('jobs', [])
    if (jobs.get('total_count') != 30 or len(entries) != 30 or
            {job.get('name') for job in entries} != expected_jobs() or
            len({job.get('id') for job in entries}) != 30 or
            any(job.get('run_id') != run_id or job.get('head_sha') != head_sha or
                job.get('status') != 'completed' or job.get('conclusion') != 'success'
                for job in entries)):
        raise ValueError('Every one of the 30 source CI jobs must be individually successful')
    if api.get('commits/main').get('sha') != dispatch_sha:
        raise ValueError('Main advanced after dispatch; dispatch again on its current commit')
    if not git.ancestor(head_sha, dispatch_sha):
        raise ValueError('Source CI head is not included in main')
    # A merged/retried run may have an empty pull_requests field. Resolve the
    # association from its immutable commit, then verify the full PR record.
    associated = api.get('commits/' + head_sha + '/pulls?per_page=100')
    pull_requests = [pr for pr in associated if pr.get('head', {}).get('sha') == head_sha and
                     pr.get('base', {}).get('ref') == 'main']
    if (len(associated) >= 100 or len(pull_requests) != 1 or
            not isinstance(pull_requests[0].get('number'), int)):
        raise ValueError('Source CI must identify the exact merged pull request')
    pr = api.get('pulls/' + str(pull_requests[0]['number']))
    merge = pr.get('merge_commit_sha', '')
    if (pr.get('merged') is not True or not pr.get('merged_at') or
            pr.get('head', {}).get('sha') != head_sha or
            pr.get('head', {}).get('repo', {}).get('full_name') != REPOSITORY or
            pr.get('base', {}).get('ref') != 'main' or
            pr.get('base', {}).get('repo', {}).get('full_name') != REPOSITORY or
            not re.fullmatch(r'[0-9a-f]{40}', merge) or not git.ancestor(merge, dispatch_sha)):
        raise ValueError('The pinned source pull request is not merged into main')
    release = api.get('releases/latest')
    tag = 'v' + version
    if (release.get('tag_name') != tag or release.get('draft') is not False or
            release.get('prerelease') is not False or not release.get('published_at') or
            release.get('html_url') != f'https://github.com/{REPOSITORY}/releases/tag/{tag}'):
        raise ValueError('Bootstrap VERSION must be the current public stable/latest release')
    if git.output('show', head_sha + ':VERSION') != version:
        raise ValueError('Source package VERSION differs from the latest public release')
    if git.output('cat-file', '-t', 'refs/tags/' + tag) != 'tag':
        raise ValueError('The public release must retain its annotated tag')
    released_commit = git.output('rev-parse', 'refs/tags/' + tag + '^{commit}')
    if (not git.ancestor(released_commit, head_sha) or
            git.changed_paths(released_commit, head_sha) - PACKAGING_ONLY_PATHS):
        raise ValueError('Source CI application inputs differ from the public release')
    artifacts = api.get(f'actions/runs/{run_id}/artifacts?per_page=100')
    candidates = [a for a in artifacts.get('artifacts', []) if a.get('name') == 'debian-assets']
    if artifacts.get('total_count', 101) > 100 or len(candidates) != 1:
        raise ValueError('Source run must contain exactly one Debian asset artifact')
    artifact = candidates[0]
    source = artifact.get('workflow_run', {})
    size = artifact.get('size_in_bytes')
    if (artifact.get('id') != artifact_id or artifact.get('expired') is not False or
            artifact.get('digest') != digest or not isinstance(size, int) or
            not 0 < size <= MAX_ARTIFACT or source.get('id') != run_id or
            source.get('head_sha') != head_sha or
            source.get('repository_id') != run['repository'].get('id') or
            source.get('head_repository_id') != run['repository'].get('id')):
        raise ValueError('Debian artifact identity, digest, expiry or repository differs from approval')
    return {'artifact_id': artifact_id, 'size': size, 'digest': digest,
            'release_id': release.get('id'), 'merge': merge,
            'run_attempt': run.get('run_attempt'),
            'job_ids': sorted(job['id'] for job in entries)}


def extract_packages(archive, output, version):
    expected = {f'kilo-proxy-{kind}_{version}_{arch}.deb'
                for kind in ('desktop', 'headless') for arch in ('amd64', 'arm64')}
    expected.add('DEBIAN-SHA256SUMS.txt')
    if output.exists() or output.is_symlink():
        raise ValueError('Refusing to replace a bootstrap package directory')
    with zipfile.ZipFile(archive) as source:
        members = source.infolist()
        if (len(members) != 5 or {item.filename for item in members} != expected or
                sum(item.file_size for item in members) > MAX_ARTIFACT or
                any(item.is_dir() or item.file_size < 0 or
                    stat.S_IFMT(item.external_attr >> 16) not in (0, stat.S_IFREG)
                    for item in members)):
            raise ValueError('Bootstrap ZIP must contain only the four regular packages and manifest')
        output.mkdir(parents=True)
        try:
            for item in members:
                with source.open(item) as inp, (output / item.filename).open('xb') as out:
                    shutil.copyfileobj(inp, out)
                (output / item.filename).chmod(0o644)
            verify_packages(output, version)
        except BaseException:
            shutil.rmtree(output)
            raise


def recover(api, git, run_id, head_sha, artifact_id, digest, version, dispatch_sha, output):
    identity = validate_source(api, git, run_id, head_sha, artifact_id, digest, version, dispatch_sha)
    with tempfile.TemporaryDirectory(prefix='kilo-apt-bootstrap-') as temporary:
        archive = Path(temporary) / 'debian-assets.zip'
        api.download(artifact_id, archive)
        if archive.stat().st_size != identity['size']:
            raise ValueError('Downloaded Debian artifact size differs from GitHub metadata')
        actual = hashlib.sha256(archive.read_bytes()).hexdigest()
        if 'sha256:' + actual != digest:
            raise ValueError('Downloaded Debian artifact digest differs from approval')
        # A rerun, new latest release or main update while downloading must fail
        # before any package is handed to the signing step.
        if validate_source(api, git, run_id, head_sha, artifact_id, digest, version,
                           dispatch_sha) != identity:
            raise ValueError('Bootstrap source metadata changed while downloading')
        extract_packages(archive, output, version)
    print(f'Validated {version}: source CI {run_id}, all 30 jobs green, artifact {artifact_id}')


def positive_integer(value):
    if not re.fullmatch(r'[1-9][0-9]*', value):
        raise argparse.ArgumentTypeError('Use a positive GitHub identifier')
    return int(value)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--run-id', type=positive_integer, required=True)
    parser.add_argument('--head-sha', required=True)
    parser.add_argument('--artifact-id', type=positive_integer, required=True)
    parser.add_argument('--artifact-digest', required=True)
    parser.add_argument('--download-directory', type=Path, required=True)
    args = parser.parse_args()
    git = Git()
    dispatch_sha = os.environ.get('GITHUB_SHA', '')
    validate_environment(os.environ.get('GITHUB_REPOSITORY'), os.environ.get('GITHUB_EVENT_NAME'),
                         os.environ.get('GITHUB_REF'), dispatch_sha, git.output('rev-parse', 'HEAD'))
    recover(GitHub(), git, args.run_id, args.head_sha, args.artifact_id, args.artifact_digest,
            (ROOT / 'VERSION').read_text().strip(), dispatch_sha, args.download_directory)


if __name__ == '__main__':
    main()

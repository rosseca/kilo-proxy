#!/usr/bin/env python3
"""Prepare a signed APT Pages artifact; never deploy or use a personal keyring."""
import argparse
import hashlib
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
MAX_SNAPSHOT = 400 * 1024 * 1024
MAX_SITE = 900 * 1024 * 1024
KEY_NAME = 'kilo-proxy-archive-keyring.asc'


def verify_packages(directory, version):
    if not re.fullmatch(r'\d+\.\d+\.\d+', version):
        raise ValueError('APT publication requires a stable release version')
    expected = {f'kilo-proxy-{kind}_{version}_{arch}.deb'
                for kind in ('desktop', 'headless') for arch in ('amd64', 'arm64')}
    if {p.name for p in directory.glob('*.deb')} != expected:
        raise ValueError('Debian assets must contain exactly the four release packages')
    manifest = directory / 'DEBIAN-SHA256SUMS.txt'
    if manifest.is_symlink() or not manifest.is_file():
        raise ValueError('Missing Debian checksum manifest')
    seen = set()
    for line in manifest.read_text().splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  (?:\./)?([^/\\]+\.deb)', line)
        if not match or match[2] not in expected or match[2] in seen:
            raise ValueError('Invalid or duplicate Debian checksum entry')
        name = match[2]
        package = directory / name
        if package.is_symlink() or not package.is_file():
            raise ValueError('Unsafe Debian package input')
        digest = hashlib.sha256()
        with package.open('rb') as source:
            for chunk in iter(lambda: source.read(1024 * 1024), b''):
                digest.update(chunk)
        if digest.hexdigest() != match[1]:
            raise ValueError('Debian package checksum mismatch')
        seen.add(name)
    if seen != expected:
        raise ValueError('Incomplete Debian checksum manifest')


def download(url, destination, maximum):
    # Public Pages objects need no GitHub token or other authentication.
    with urllib.request.urlopen(url, timeout=60) as response, destination.open('xb') as output:
        total = 0
        while chunk := response.read(1024 * 1024):
            total += len(chunk)
            if total > maximum:
                raise ValueError('Published APT history exceeds the supported size')
            output.write(chunk)


def extract_snapshot(archive, destination):
    with tarfile.open(archive, 'r:gz') as source:
        members = source.getmembers()
        paths = set()
        total = 0
        for member in members:
            path = PurePosixPath(member.name)
            if (not member.name or path.is_absolute() or '..' in path.parts or
                    '\\' in member.name or member.name in paths or
                    not (member.isfile() or member.isdir())):
                raise ValueError('Unsafe APT history archive entry')
            paths.add(member.name)
            total += member.size
            if total > MAX_SNAPSHOT:
                raise ValueError('Extracted APT history exceeds the supported size')
        # Copy explicitly: do not restore archive owners or arbitrary modes.
        for member in members:
            target = destination.joinpath(*PurePosixPath(member.name).parts)
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                with source.extractfile(member) as inp, target.open('xb') as out:
                    shutil.copyfileobj(inp, out)
                target.chmod(0o644)


def previous_repository(url, fingerprint, home, temporary, allow_bootstrap):
    if not url.startswith('https://') or '?' in url or '#' in url:
        raise ValueError('APT history requires a plain HTTPS repository URL')
    url = url.rstrip('/')
    archive, signature = temporary / 'previous.tar.gz', temporary / 'previous.tar.gz.asc'
    try:
        download(url + '/repository-state.tar.gz', archive, MAX_SNAPSHOT)
    except urllib.error.HTTPError as error:
        if error.code != 404 or not allow_bootstrap:
            raise
        # A missing snapshot must not silently reset an existing repository.
        try:
            download(url + '/dists/noble/InRelease', temporary / 'existing-release', 1024 * 1024)
        except urllib.error.HTTPError as absent:
            if absent.code == 404:
                return None
            raise
        raise ValueError('Existing APT metadata has no history snapshot; bootstrap refused')
    download(url + '/repository-state.tar.gz.asc', signature, 1024 * 1024)
    keyring = temporary / 'trusted.gpg'
    with keyring.open('xb') as output:
        subprocess.run(['gpg', '--homedir', str(home), '--batch', '--export', fingerprint],
                       stdout=output, check=True)
    # Authenticate the entire archive before parsing or extracting it.
    subprocess.run(['gpgv', '--homedir', str(home), '--keyring', str(keyring),
                    str(signature), str(archive)], check=True)
    previous = temporary / 'previous'
    previous.mkdir()
    extract_snapshot(archive, previous)
    return previous


def check_previous_version(previous, version):
    if previous is None:
        return
    fields = {}
    for line in (previous / 'dists/noble/Release').read_text().splitlines():
        if ': ' in line and not line.startswith(' '):
            key, value = line.split(': ', 1)
            if key in fields:
                raise ValueError('Duplicate previous repository field')
            fields[key] = value
    current = fields.get('Version', '')
    if not re.fullmatch(r'\d+\.\d+\.\d+', current):
        raise ValueError('Previous APT repository has no stable release identity')
    if tuple(map(int, version.split('.'))) < tuple(map(int, current.split('.'))):
        raise ValueError('Refusing to replace APT with an older release')


def prepare(packages, output, fingerprint, home, previous_url, allow_bootstrap, epoch):
    version = (ROOT / 'VERSION').read_text().strip()
    verify_packages(packages, version)
    if not re.fullmatch(r'(?:[0-9A-Fa-f]{40}|[0-9A-Fa-f]{64})', fingerprint):
        raise ValueError('Supply the complete APT signing fingerprint')
    fingerprint = fingerprint.upper()
    if not home.is_dir() or home.is_symlink() or (home.stat().st_mode & 0o077):
        raise ValueError('Use an explicitly selected private GNUPGHOME with mode 0700')
    if output.exists() or output.is_symlink():
        raise ValueError('Refusing to overwrite an existing site')
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='kilo-apt-site-', dir=output.parent) as temporary_name:
        temporary = Path(temporary_name)
        previous = previous_repository(previous_url, fingerprint, home, temporary, allow_bootstrap)
        check_previous_version(previous, version)
        site = temporary / 'site'
        site.mkdir()
        command = [sys.executable, str(ROOT / 'scripts/build_apt_repository.py'),
                   '--packages-directory', str(packages), '--output', str(site / 'apt'),
                   '--signing-key', fingerprint, '--gnupg-home', str(home),
                   '--source-date-epoch', str(epoch)]
        if previous:
            command += ['--previous-repository', str(previous)]
        subprocess.run(command, check=True)
        apt = site / 'apt'
        # Snapshot excludes itself; its detached signature authenticates history
        # on the next deployment, while APT verifies InRelease and package hashes.
        snapshot = temporary / 'repository-state.tar.gz'
        with tarfile.open(snapshot, 'w:gz') as archive:
            for child in sorted(apt.iterdir()):
                archive.add(child, arcname=child.name)
        if snapshot.stat().st_size > MAX_SNAPSHOT:
            raise ValueError('APT history needs explicit archival maintenance before publishing')
        shutil.move(snapshot, apt / snapshot.name)
        subprocess.run(['gpg', '--homedir', str(home), '--batch', '--yes',
                        '--local-user', fingerprint, '--armor', '--detach-sign',
                        str(apt / snapshot.name)], check=True)
        (site / '.nojekyll').touch()
        (site / 'index.html').write_text(
            '<!doctype html><html lang="en"><meta charset="utf-8">'
            '<title>Kilo Proxy APT repository</title><h1>Kilo Proxy APT repository</h1>'
            '<p>Desktop and headless packages for Ubuntu 24.04 and 26.04 LTS, amd64 and arm64.</p>'
            '<p><a href="https://github.com/rosseca/kilo-proxy/blob/main/docs/ubuntu-apt.md">'
            'Installation and signature verification instructions</a></p></html>\n')
        if sum(p.stat().st_size for p in site.rglob('*') if p.is_file()) > MAX_SITE:
            raise ValueError('APT site exceeds its supported hosting size')
        site.rename(output)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--packages-directory', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--signing-key', required=True)
    parser.add_argument('--gnupg-home', type=Path, default=os.environ.get('GNUPGHOME'))
    parser.add_argument('--previous-url', required=True)
    parser.add_argument('--allow-bootstrap', action='store_true')
    parser.add_argument('--source-date-epoch', type=int, default=None)
    args = parser.parse_args()
    if args.gnupg_home is None:
        parser.error('An isolated --gnupg-home or GNUPGHOME is required')
    epoch = args.source_date_epoch
    if epoch is None:
        epoch = int(subprocess.check_output(['git', 'log', '-1', '--format=%ct'], cwd=ROOT, text=True))
    if epoch < 0:
        parser.error('source-date-epoch must be nonnegative')
    prepare(args.packages_directory.resolve(), args.output.absolute(), args.signing_key,
            Path(args.gnupg_home).resolve(), args.previous_url, args.allow_bootstrap, epoch)


if __name__ == '__main__':
    main()

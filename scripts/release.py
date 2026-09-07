#!/usr/bin/env python3
"""Validate versioned artifacts and publish them through GitHub CLI."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

from package import ROOT, TARGETS

VERSION_PATTERN = r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:alpha|beta|rc)\.[1-9][0-9]*)?'


def read_version(root=ROOT):
    version = (root / 'VERSION').read_text().strip()
    if not re.fullmatch(VERSION_PATTERN, version):
        raise ValueError('VERSION must be X.Y.Z or X.Y.Z-alpha.N / beta.N / rc.N')
    return version


def check_tag(tag, version):
    if tag != 'v' + version:
        raise ValueError(f'Tag {tag!r} does not match VERSION ({version})')


def asset_names(version):
    return [f'kilo-local-{version}-{system}-{arch}' + ('.tar.gz' if system == 'linux' else '.zip')
            for system, arch in TARGETS]


def verify_assets(directory, version):
    expected = asset_names(version)
    manifest = directory / 'SHA256SUMS.txt'
    lines = manifest.read_text().splitlines()
    checksums = {}
    for line in lines:
        match = re.fullmatch(r'([0-9a-f]{64})  (.+)', line)
        if not match or match[2] not in expected or match[2] in checksums:
            raise ValueError('Invalid, unexpected, or duplicate checksum entry')
        checksums[match[2]] = match[1]
    if set(checksums) != set(expected):
        raise ValueError('The checksum manifest must contain all six target archives')
    paths = [directory / name for name in expected]
    for path in paths:
        if not path.is_file() or path.is_symlink() or path.stat().st_size == 0:
            raise ValueError(f'Missing or invalid archive: {path.name}')
        with path.open('rb') as stream:
            digest = hashlib.sha256()
            for chunk in iter(lambda: stream.read(1024 * 1024), b''):
                digest.update(chunk)
        if digest.hexdigest() != checksums[path.name]:
            raise ValueError(f'Checksum mismatch: {path.name}')
    return paths + [manifest]


def gh(*args, check=True):
    return subprocess.run(['gh', *args], check=check, capture_output=True, text=True)


def publish(tag, version, directory):
    check_tag(tag, version)
    paths = verify_assets(directory, version)
    # A failed query still requires create to succeed; no API errors are treated as success.
    result = gh('release', 'view', tag, '--json', 'isDraft', check=False)
    if result.returncode == 0:
        if not json.loads(result.stdout)['isDraft']:
            raise ValueError('This release is already published; refusing to overwrite it')
    else:
        args = ['release', 'create', tag, '--verify-tag', '--draft', '--generate-notes',
                '--title', f'Kilo Local {tag}', '--notes',
                'Portable apps for macOS, Windows, and Linux (x64 and ARM64). '
                'Download the archive for your system and verify it with SHA256SUMS.txt. '
                'Windows ZIPs include Kilo Local.exe; macOS ZIPs include Kilo Local.app. '
                'Packages are unsigned and not notarized. Setup instructions are included in README.md.']
        if '-' in version:
            args.append('--prerelease')
        gh(*args)
    gh('release', 'upload', tag, *map(str, paths), '--clobber')
    uploaded = json.loads(gh('release', 'view', tag, '--json', 'assets').stdout)['assets']
    if {item['name']: item['size'] for item in uploaded} != {path.name: path.stat().st_size for path in paths}:
        raise ValueError('Uploaded asset set or size differs from the verified local files; leaving draft unpublished')
    prerelease = '-' in version
    gh('release', 'edit', tag, '--verify-tag', '--draft=false',
       '--prerelease=' + str(prerelease).lower(), '--latest=' + str(not prerelease).lower())
    print(gh('release', 'view', tag, '--json', 'url', '--jq', '.url').stdout.strip())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['check-tag', 'verify-assets', 'publish'])
    parser.add_argument('--tag')
    parser.add_argument('--directory', type=Path, default=ROOT / 'dist')
    args = parser.parse_args()
    if args.command in ('check-tag', 'publish') and not args.tag:
        parser.error('--tag is required')
    try:
        version = read_version()
        if args.command == 'check-tag':
            check_tag(args.tag, version)
            print(f'Validated {args.tag}')
        elif args.command == 'verify-assets':
            paths = verify_assets(args.directory, version)
            print(f'Verified {len(paths) - 1} archives and checksum manifest')
        else:
            publish(args.tag, version, args.directory)
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        print(f'Release failed: {error}', file=sys.stderr)
        if isinstance(error, subprocess.CalledProcessError) and error.stderr:
            print(error.stderr, file=sys.stderr)
        sys.exit(1)


if __name__ == '__main__':
    main()

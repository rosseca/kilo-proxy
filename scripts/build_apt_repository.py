#!/usr/bin/env python3
"""Build and verify a signed, static Kilo Proxy APT repository.

Only the explicitly supplied GnuPG home is used. The output must be a new
directory: a verified staging tree is renamed into place atomically, so failed
builds cannot publish a partial index or retain stale packages. Publication is a
separate operation. Release deliberately has no Valid-Until: these release
snapshots require no periodic resigning job. Clients must protect the configured
signing key and repository URL; an old, validly signed snapshot can be replayed.
"""
import argparse
import ctypes
from datetime import datetime, timezone
from email.utils import format_datetime
import gzip
import hashlib
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import subprocess
import sys
import tempfile


PACKAGES = ('kilo-proxy-desktop', 'kilo-proxy-headless')
ARCHITECTURES = ('amd64', 'arm64')
SUITES = ('noble', 'resolute')
VERSION_PATTERN = (r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)'
                   r'(?:~(?:alpha|beta|rc)\.[1-9][0-9]*)?')
FINGERPRINT_PATTERN = r'(?:[0-9A-F]{40}|[0-9A-F]{64})'
KEY_FILENAME = 'kilo-proxy-archive-keyring.asc'
INDEX_FIELDS = {'filename', 'size', 'md5sum', 'sha1', 'sha256', 'sha512'}
RETAINED_MANIFEST = 'retained-files.txt'
RETAINED_PATTERN = (r'(?:pool/main/k/kilo-proxy/kilo-proxy-(?:desktop|headless)_'
                    + VERSION_PATTERN + r'_(?:amd64|arm64)\.deb|'
                    r'dists/(?:noble|resolute)/main/binary-(?:amd64|arm64)/by-hash/SHA256/[0-9a-f]{64})')


def run(*args, **kwargs):
    return subprocess.run(list(map(str, args)), check=True, capture_output=True,
                          **kwargs)


def parse_control(text):
    """Read exactly one Debian control paragraph, rejecting ambiguous fields."""
    fields = {}
    current = None
    seen = set()
    for line in text.splitlines():
        if not line:
            raise ValueError('Unexpected empty line in package control metadata')
        if line[0] in ' \t':
            if current is None:
                raise ValueError('Control continuation has no field')
            fields[current] += '\n' + line
            continue
        match = re.fullmatch(r'([A-Za-z0-9][A-Za-z0-9-]*):[ \t]*(.*)', line)
        if not match or match[1].lower() in seen:
            raise ValueError('Invalid or duplicate package control field')
        current = match[1]
        seen.add(current.lower())
        fields[current] = match[2]
    if not fields:
        raise ValueError('Package control metadata is empty')
    return fields


def metadata(path):
    fields = parse_control(run('dpkg-deb', '--field', path, text=True).stdout)
    lower = {key.lower(): value for key, value in fields.items()}
    if INDEX_FIELDS.intersection(lower):
        raise ValueError('Package control metadata contains repository index fields')
    if lower.get('package') not in PACKAGES:
        raise ValueError('Unexpected Debian package name')
    if lower.get('architecture') not in ARCHITECTURES:
        raise ValueError('Unexpected Debian package architecture')
    if not re.fullmatch(VERSION_PATTERN, lower.get('version', '')):
        raise ValueError('APT packages must use X.Y.Z or Debian-mapped X.Y.Z~alpha.N / beta.N / rc.N')
    if not lower.get('maintainer') or not lower.get('description'):
        raise ValueError('Package requires Maintainer and Description')
    # Reading the complete data archive also rejects malformed/truncated .deb
    # files that happen to have a readable control archive.
    with tempfile.TemporaryFile() as stream:
        subprocess.run(['dpkg-deb', '--fsys-tarfile', str(path)], stdout=stream,
                       stderr=subprocess.PIPE, check=True)
    return fields, lower


def digest(path):
    result = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            result.update(chunk)
    return result.hexdigest()


def snapshot(source, destination):
    """Copy only a regular file, without following an input symlink."""
    source_stat = source.lstat()
    if not stat.S_ISREG(source_stat.st_mode):
        raise ValueError(f'Package is not a regular file: {source.name}')
    descriptor = os.open(source, os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0))
    with os.fdopen(descriptor, 'rb') as stream:
        actual = os.fstat(stream.fileno())
        if not stat.S_ISREG(actual.st_mode) or (actual.st_dev, actual.st_ino) != (
                source_stat.st_dev, source_stat.st_ino):
            raise ValueError('Package changed while opening the input')
        with destination.open('xb') as target:
            shutil.copyfileobj(stream, target)


def rename_new_directory(source, destination):
    """Atomically rename without replacing even a concurrently created empty dir."""
    libc = ctypes.CDLL(None, use_errno=True)
    if sys.platform == 'linux':
        rename = libc.renameat2
        rename.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int,
                           ctypes.c_char_p, ctypes.c_uint]
        rename.restype = ctypes.c_int
        result = rename(-100, os.fsencode(source), -100, os.fsencode(destination), 1)  # RENAME_NOREPLACE
    elif sys.platform == 'darwin':
        rename = libc.renamex_np
        rename.argtypes = [ctypes.c_char_p, ctypes.c_char_p, ctypes.c_uint]
        rename.restype = ctypes.c_int
        result = rename(os.fsencode(source), os.fsencode(destination), 4)  # RENAME_EXCL
    else:
        raise ValueError('Atomic repository publication requires Linux or macOS')
    if result != 0:
        number = ctypes.get_errno()
        raise OSError(number, os.strerror(number), str(destination))


def gpg(home, *args):
    return run('gpg', '--no-options', '--homedir', home, '--batch', '--no-tty',
               '--no-auto-key-retrieve', *args)


def validate_key(home, fingerprint):
    fingerprint = fingerprint.upper()
    if not re.fullmatch(FINGERPRINT_PATTERN, fingerprint):
        raise ValueError('--signing-key must be a full primary-key fingerprint')
    home = Path(home)
    if home.is_symlink() or not home.is_dir():
        raise ValueError('An explicit, existing GnuPG home directory is required')
    listing = gpg(home, '--with-colons', '--list-secret-keys', fingerprint).stdout.decode()
    primary = None
    for line in listing.splitlines():
        fields = line.split(':')
        if fields[0] == 'sec':
            if fields[1] in ('r', 'e', 'd'):
                raise ValueError('The signing key is revoked, expired, or disabled')
            primary = True
        elif fields[0] == 'fpr' and primary:
            if fields[9] != fingerprint:
                raise ValueError('Signing fingerprint does not identify the primary key')
            return fingerprint
        elif fields[0] in ('ssb', 'pub', 'sub'):
            primary = None
    raise ValueError('The pinned private primary key is not available in the supplied GnuPG home')


def verify_signature(home, fingerprint, signature, data=None):
    args = ['--status-fd', '1', '--verify', signature]
    if data is not None:
        args.append(data)
    status = gpg(home, *args).stdout.decode()
    valid = []
    for line in status.splitlines():
        if line.startswith('[GNUPG:] VALIDSIG '):
            fields = line.split()
            # A signing subkey's VALIDSIG includes the primary fingerprint last.
            valid.append((fields[2], fields[11] if len(fields) > 11 else fields[2]))
    if len(valid) != 1 or fingerprint not in valid[0]:
        raise ValueError('Repository signature does not match the pinned signing key')


def control_text(fields):
    return ''.join(f'{key}: {value}\n' for key, value in fields.items())


def release_text(suite, root, epoch, version):
    entries = []
    for architecture in ARCHITECTURES:
        for name in ('Packages', 'Packages.gz'):
            path = root / f'main/binary-{architecture}/{name}'
            entries.append(f' {digest(path)} {path.stat().st_size:16d} {path.relative_to(root).as_posix()}')
    manifest = root / RETAINED_MANIFEST
    entries.append(f' {digest(manifest)} {manifest.stat().st_size:16d} {RETAINED_MANIFEST}')
    date = format_datetime(datetime.fromtimestamp(epoch, timezone.utc), usegmt=True)
    return (f'Origin: Kilo Proxy\nLabel: Kilo Proxy\nSuite: {suite}\nCodename: {suite}\nVersion: {version}\n'
            f'Date: {date}\nArchitectures: {" ".join(ARCHITECTURES)}\nComponents: main\n'
            'Description: Kilo Proxy Desktop and headless\nAcquire-By-Hash: yes\n'
            'SHA256:\n' + '\n'.join(entries) + '\n')


def safe_repository_path(root, value):
    path = PurePosixPath(value)
    if path.is_absolute() or not path.parts or any(part in ('.', '..') for part in path.parts):
        raise ValueError('Unsafe repository-relative path')
    result = root
    for part in path.parts:
        result /= part
        if result.is_symlink():
            raise ValueError('Indexed repository path contains a symlink')
    if not result.is_file():
        raise ValueError('Missing or non-regular indexed file')
    return result


def retained_files(directory):
    return sorted(path for path in directory.rglob('*') if path.is_file() and (
        path.relative_to(directory).parts[0] == 'pool' or
        '/by-hash/' in path.relative_to(directory).as_posix()))


def retained_manifest(directory):
    return ''.join(f'{digest(path)} {path.stat().st_size} {path.relative_to(directory).as_posix()}\n'
                   for path in retained_files(directory))


def verify_retained(directory, text):
    expected = set()
    for line in text.splitlines():
        match = re.fullmatch(r'([0-9a-f]{64}) ([0-9]+) (\S+)', line)
        if not match or not re.fullmatch(RETAINED_PATTERN, match[3]) or match[3] in expected:
            raise ValueError('Invalid or duplicate retained repository file')
        expected.add(match[3])
        path = safe_repository_path(directory, match[3])
        if path.stat().st_size != int(match[2]) or digest(path) != match[1]:
            raise ValueError('Retained repository file size or checksum mismatch')
        if '/by-hash/' in match[3] and path.name != match[1]:
            raise ValueError('Retained by-hash filename does not match its content')
    actual = {path.relative_to(directory).as_posix() for path in retained_files(directory)}
    if actual != expected:
        raise ValueError('Unsigned or missing retained repository files')
    return expected


def verify_repository(directory, fingerprint, gnupg_home, include_manifest=False):
    """Check both signatures, exact index/pool membership and every SHA256."""
    directory = Path(directory)
    fingerprint = fingerprint.upper()
    exported = gpg(gnupg_home, '--with-colons', '--show-keys', directory / KEY_FILENAME).stdout.decode()
    primary_fingerprints = []
    pending = False
    for line in exported.splitlines():
        parts = line.split(':')
        if parts[0] == 'pub':
            pending = True
        elif parts[0] == 'fpr' and pending:
            primary_fingerprints.append(parts[9])
            pending = False
    if primary_fingerprints != [fingerprint]:
        raise ValueError('Exported repository key does not match the pinned key')
    expected_pool = set()
    expected_version = None
    previous_manifest = None
    for suite in SUITES:
        root = directory / 'dists' / suite
        release = root / 'Release'
        verify_signature(gnupg_home, fingerprint, root / 'Release.gpg', release)
        verify_signature(gnupg_home, fingerprint, root / 'InRelease')
        plaintext = gpg(gnupg_home, '--decrypt', root / 'InRelease').stdout
        if plaintext != release.read_bytes():
            raise ValueError('InRelease payload differs from Release')
        fields = parse_control(release.read_text())
        if (fields.get('Origin') != 'Kilo Proxy' or fields.get('Label') != 'Kilo Proxy' or
                fields.get('Suite') != suite or fields.get('Codename') != suite or
                fields.get('Architectures') != ' '.join(ARCHITECTURES) or
                fields.get('Components') != 'main' or fields.get('Acquire-By-Hash') != 'yes'):
            raise ValueError('Incorrect Release identity or architectures')
        release_version = fields.get('Version', '')
        if not re.fullmatch(VERSION_PATTERN, release_version):
            raise ValueError('Release must identify a valid Debian package version')
        if expected_version is not None and release_version != expected_version:
            raise ValueError('Suite Release versions disagree')
        expected_version = release_version
        expected_indexes = {f'main/binary-{architecture}/{name}'
                            for architecture in ARCHITECTURES for name in ('Packages', 'Packages.gz')}
        expected_indexes.add(RETAINED_MANIFEST)
        seen_indexes = set()
        for line in fields.get('SHA256', '').splitlines():
            if not line.strip():
                continue
            match = re.fullmatch(r' ([0-9a-f]{64}) +([0-9]+) (\S+)', line)
            if not match or match[3] not in expected_indexes or match[3] in seen_indexes:
                raise ValueError('Invalid or duplicate Release checksum entry')
            seen_indexes.add(match[3])
            path = safe_repository_path(root, match[3])
            if path.stat().st_size != int(match[2]) or digest(path) != match[1]:
                raise ValueError('Release index size or checksum mismatch')
            if match[3] != RETAINED_MANIFEST:
                by_hash = path.parent / 'by-hash' / 'SHA256' / match[1]
                if by_hash.is_symlink() or not by_hash.is_file() or by_hash.read_bytes() != path.read_bytes():
                    raise ValueError('Missing or incorrect by-hash index')
        if seen_indexes != expected_indexes:
            raise ValueError('Release must index all architectures and compression variants')
        manifest = (root / RETAINED_MANIFEST).read_text()
        verify_retained(directory, manifest)
        if previous_manifest is not None and manifest != previous_manifest:
            raise ValueError('Suite retained-file manifests disagree')
        previous_manifest = manifest
        for architecture in ARCHITECTURES:
            path = root / f'main/binary-{architecture}/Packages'
            if gzip.decompress(path.with_suffix('.gz').read_bytes()) != path.read_bytes():
                raise ValueError('Compressed Packages differs from plain index')
            entries = [parse_control(paragraph) for paragraph in path.read_text().strip().split('\n\n')]
            if len(entries) != len(PACKAGES):
                raise ValueError('Package index must contain exactly Desktop and headless')
            found = set()
            for entry in entries:
                package = entry.get('Package')
                if package not in PACKAGES or package in found or entry.get('Architecture') != architecture:
                    raise ValueError('Unexpected or duplicate indexed package identity')
                found.add(package)
                version = entry.get('Version', '')
                if not re.fullmatch(VERSION_PATTERN, version):
                    raise ValueError('Invalid indexed package version')
                if expected_version is None:
                    expected_version = version
                if version != expected_version:
                    raise ValueError('Mixed versions in repository')
                filename = f'pool/main/k/kilo-proxy/{package}_{version}_{architecture}.deb'
                if entry.get('Filename') != filename:
                    raise ValueError('Incorrect package pool filename')
                archive = safe_repository_path(directory, filename)
                if entry.get('Size') != str(archive.stat().st_size) or entry.get('SHA256') != digest(archive):
                    raise ValueError('Package pool size or checksum mismatch')
                _, actual = metadata(archive)
                indexed = {key.lower(): value for key, value in entry.items()}
                if any(indexed.get(key) != value for key, value in actual.items()):
                    raise ValueError('Indexed package metadata differs from archive')
                expected_pool.add(archive)
    if len(expected_pool) != len(PACKAGES) * len(ARCHITECTURES):
        raise ValueError('Repository does not contain the complete current package matrix')
    for archive in (directory / 'pool').rglob('*.deb'):
        _, actual = metadata(archive)
        if archive.name != f'{actual["package"]}_{actual["version"]}_{actual["architecture"]}.deb':
            raise ValueError('Retained package filename differs from its archive metadata')
    return (expected_version, previous_manifest) if include_manifest else expected_version


def build_repository(packages_directory, output, signing_key, gnupg_home, source_date_epoch=None,
                     previous_repository=None):
    packages_directory, output = Path(packages_directory), Path(output)
    if packages_directory.is_symlink() or not packages_directory.is_dir():
        raise ValueError('A regular packages directory is required')
    if output.exists() or output.is_symlink():
        raise ValueError('Output already exists; refusing to retain or overwrite repository files')
    if not output.parent.is_dir() or output.parent.is_symlink():
        raise ValueError('Output parent must be an existing regular directory')
    fingerprint = validate_key(gnupg_home, signing_key)
    epoch = int(source_date_epoch) if source_date_epoch is not None else int(datetime.now(timezone.utc).timestamp())
    if epoch < 0:
        raise ValueError('Source date epoch must not be negative')
    inputs = sorted(packages_directory.glob('*.deb'))
    if len(inputs) != len(PACKAGES) * len(ARCHITECTURES):
        raise ValueError('Exactly four Desktop/headless amd64/arm64 Debian packages are required')
    previous_version = None
    previous_manifest = None
    if previous_repository is not None:
        previous_repository = Path(previous_repository)
        if previous_repository.is_symlink() or not previous_repository.is_dir():
            raise ValueError('Previous repository must be a regular local directory')
        previous_version, previous_manifest = verify_repository(
            previous_repository, fingerprint, gnupg_home, include_manifest=True)
    with tempfile.TemporaryDirectory(prefix='.' + output.name + '-', dir=output.parent) as temporary:
        stage = Path(temporary) / 'repository'
        pool = stage / 'pool/main/k/kilo-proxy'
        pool.mkdir(parents=True)
        if previous_repository is not None:
            for line in previous_manifest.splitlines():
                expected_digest, expected_size, relative = line.split(' ', 2)
                previous = safe_repository_path(previous_repository, relative)
                destination = stage / relative
                destination.parent.mkdir(parents=True, exist_ok=True)
                snapshot(previous, destination)
                if destination.stat().st_size != int(expected_size) or digest(destination) != expected_digest:
                    raise ValueError('Previously verified repository changed while copying it')
        records = {}
        version = None
        for number, source in enumerate(inputs):
            copied = pool / f'input-{number}.deb'
            snapshot(source, copied)
            fields, lower = metadata(copied)
            identity = (lower['package'], lower['architecture'])
            if identity in records:
                raise ValueError('Duplicate Debian package identity')
            version = version or lower['version']
            if lower['version'] != version:
                raise ValueError('All Debian packages must have the same version')
            if previous_version is not None:
                try:
                    run('dpkg', '--compare-versions', version, 'ge', previous_version)
                except subprocess.CalledProcessError as error:
                    if error.returncode == 1:
                        raise ValueError('Refusing to downgrade the previously published repository version') from error
                    raise
            destination = pool / f'{identity[0]}_{version}_{identity[1]}.deb'
            if destination.exists():
                if digest(copied) != digest(destination):
                    raise ValueError('Refusing to replace bytes for an already published package version')
                copied.unlink()
            else:
                copied.rename(destination)
            # Canonicalize required field spelling for APT while preserving the
            # complete package control metadata (including Depends/Conflicts).
            required = {'package': 'Package', 'version': 'Version', 'architecture': 'Architecture'}
            fields = {required.get(key.lower(), key): value for key, value in fields.items()}
            fields.update(Filename=destination.relative_to(stage).as_posix(),
                          Size=str(destination.stat().st_size), SHA256=digest(destination))
            records[identity] = fields
        if set(records) != {(package, architecture) for package in PACKAGES for architecture in ARCHITECTURES}:
            raise ValueError('Incomplete Debian package matrix')
        for suite in SUITES:
            root = stage / 'dists' / suite
            for architecture in ARCHITECTURES:
                binary = root / f'main/binary-{architecture}'
                binary.mkdir(parents=True, exist_ok=True)
                content = '\n'.join(control_text(records[package, architecture]) for package in PACKAGES).encode()
                (binary / 'Packages').write_bytes(content)
                (binary / 'Packages.gz').write_bytes(gzip.compress(content, mtime=0))
                for name in ('Packages', 'Packages.gz'):
                    path = binary / name
                    by_hash = binary / 'by-hash/SHA256' / digest(path)
                    by_hash.parent.mkdir(parents=True, exist_ok=True)
                    by_hash.write_bytes(path.read_bytes())
        manifest = retained_manifest(stage)
        for suite in SUITES:
            root = stage / 'dists' / suite
            (root / RETAINED_MANIFEST).write_text(manifest)
            release = root / 'Release'
            release.write_text(release_text(suite, root, epoch, version))
            gpg(gnupg_home, '--local-user', fingerprint, '--digest-algo', 'SHA256',
                '--armor', '--output', root / 'InRelease', '--clearsign', release)
            gpg(gnupg_home, '--local-user', fingerprint, '--digest-algo', 'SHA256',
                '--armor', '--output', root / 'Release.gpg', '--detach-sign', release)
        exported = gpg(gnupg_home, '--armor', '--export', fingerprint).stdout
        if not exported.startswith(b'-----BEGIN PGP PUBLIC KEY BLOCK-----'):
            raise ValueError('GnuPG did not export a public archive key')
        (stage / KEY_FILENAME).write_bytes(exported)
        verify_repository(stage, fingerprint, gnupg_home)
        # Same-filesystem rename publishes a complete verified directory. Never
        # replace an existing output, including one created during the build.
        if output.exists() or output.is_symlink():
            raise ValueError('Output appeared during build; refusing to overwrite it')
        rename_new_directory(stage, output)
    return version


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--packages-directory', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--signing-key', required=True)
    parser.add_argument('--gnupg-home', type=Path, default=os.environ.get('GNUPGHOME'))
    parser.add_argument('--source-date-epoch', type=int)
    parser.add_argument('--previous-repository', type=Path,
                        help='Verified local repository whose immutable pool/by-hash files are retained')
    args = parser.parse_args()
    if args.gnupg_home is None:
        parser.error('--gnupg-home or an explicit GNUPGHOME is required')
    try:
        version = build_repository(args.packages_directory, args.output, args.signing_key,
                                   args.gnupg_home, args.source_date_epoch, args.previous_repository)
        print(f'Verified signed APT repository for {version}: {args.output}')
    except (ValueError, OverflowError, OSError, subprocess.CalledProcessError) as error:
        print(f'APT repository failed: {error}', file=sys.stderr)
        if isinstance(error, subprocess.CalledProcessError) and error.stderr:
            print(error.stderr.decode(errors='replace') if isinstance(error.stderr, bytes) else error.stderr,
                  file=sys.stderr)
        sys.exit(1)


if __name__ == '__main__':
    main()

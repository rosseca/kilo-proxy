#!/usr/bin/env python3
"""Install and exercise signed APT packages inside an explicitly disposable Ubuntu container.

This script changes the container's package database. It refuses a normal host,
including a root host, and never uses the invoking user's application profile.
The upgrade fixture changes package metadata only: it tests APT's lifecycle with
the candidate executable, not migration from an earlier release's executable.
"""
import argparse
import json
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import uuid


ROOT = Path(__file__).resolve().parents[1]
PACKAGES = ('kilo-proxy-desktop', 'kilo-proxy-headless')
SUITES = {'24.04': 'noble', '26.04': 'resolute'}
BINARIES = {'kilo-proxy-desktop': Path('/usr/bin/kilo-proxy'),
            'kilo-proxy-headless': Path('/usr/bin/kilo-proxy-headless')}


def os_release(path=Path('/etc/os-release')):
    result = {}
    for line in path.read_text().splitlines():
        if '=' in line and not line.startswith('#'):
            name, value = line.split('=', 1)
            result[name] = value.strip('"\'')
    return result


def require_disposable_container(ubuntu_version, arch):
    """Fail before running package tools or creating files on a normal host."""
    if (platform.system() != 'Linux' or os.geteuid() != 0 or
            os.environ.get('KILO_DEBIAN_DISPOSABLE_CONTAINER') != '1' or
            not any(path.exists() for path in (Path('/.dockerenv'), Path('/run/.containerenv')))):
        raise RuntimeError('APT acceptance requires root in an explicitly opted-in disposable Linux container')
    release = os_release()
    if release.get('ID') != 'ubuntu' or release.get('VERSION_ID') != ubuntu_version:
        raise RuntimeError('APT acceptance ran on the wrong Ubuntu release')
    machine = {'x86_64': 'amd64', 'amd64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}.get(platform.machine().lower())
    if machine != arch:
        raise RuntimeError('APT acceptance requires the native CPU architecture')
    for executable in ('apt-get', 'apt-cache', 'dpkg', 'dpkg-deb', 'dpkg-query',
                       'gpg', 'ldd', 'dbus-run-session', 'xvfb-run'):
        if shutil.which(executable) is None:
            raise RuntimeError('Missing APT acceptance tool: ' + executable)


def clean_environment(home):
    # In particular do not carry a real provider, D-Bus or desktop session into
    # the smoke processes. Every profile and upstream is synthetic.
    return {'PATH': '/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin',
            'HOME': str(home), 'XDG_CONFIG_HOME': str(home / '.config'),
            'XDG_DATA_HOME': str(home / '.local/share'), 'XDG_CACHE_HOME': str(home / '.cache'),
            'XDG_STATE_HOME': str(home / '.local/state'), 'XDG_RUNTIME_DIR': str(home / 'runtime'),
            'SHELL': '/bin/bash', 'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8',
            'DEBIAN_FRONTEND': 'noninteractive', 'LIBGL_ALWAYS_SOFTWARE': '1'}


class Commands:
    def __init__(self, root, environment):
        self.root = root
        self.environment = environment
        self.count = 0

    def run(self, arguments, timeout=300, allowed=(0,)):
        self.count += 1
        out_path = self.root / f'command-{self.count}.stdout'
        err_path = self.root / f'command-{self.count}.stderr'
        with out_path.open('wb') as stdout, err_path.open('wb') as stderr:
            process = subprocess.Popen([str(value) for value in arguments], stdout=stdout, stderr=stderr,
                                       env=self.environment, start_new_session=True)
            try:
                process.wait(timeout=timeout)
            except subprocess.TimeoutExpired as error:
                # Only terminate the process group created by this invocation.
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                process.wait(timeout=10)
                raise RuntimeError('APT acceptance command timed out: ' + str(arguments[0])) from error
        stdout = out_path.read_text(errors='replace')
        stderr = err_path.read_text(errors='replace')
        if process.returncode not in allowed:
            diagnostic = (stdout + '\n' + stderr)[-8000:]
            raise RuntimeError(f'APT acceptance command {arguments[0]} failed (exit {process.returncode}):\n{diagnostic}')
        return stdout


def expected_package_version(version):
    if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:alpha|beta|rc)\.(0|[1-9][0-9]*))?', version):
        raise ValueError('Expected a release semantic version')
    return version.replace('-', '~', 1)


def replace_control_version(control, version):
    if '\n' in version or '\r' in version:
        raise ValueError('Invalid baseline version')
    matches = re.findall(r'^Version:.*$', control, re.MULTILINE)
    if len(matches) != 1:
        raise ValueError('Package must have exactly one Version field')
    return re.sub(r'^Version:.*$', 'Version: ' + version, control, flags=re.MULTILINE)


def candidates(repository, arch, version, commands):
    result = {}
    for path in sorted(repository.rglob('*.deb')):
        name = commands.run(['dpkg-deb', '--field', path, 'Package']).strip()
        actual_arch = commands.run(['dpkg-deb', '--field', path, 'Architecture']).strip()
        actual_version = commands.run(['dpkg-deb', '--field', path, 'Version']).strip()
        if name not in PACKAGES or actual_arch not in ('amd64', 'arm64') or actual_version != version:
            raise RuntimeError('Unexpected package identity in the acceptance repository')
        if actual_arch == arch:
            if name in result:
                raise RuntimeError('Duplicate native package in the acceptance repository')
            result[name] = path
    if set(result) != set(PACKAGES):
        raise RuntimeError('Acceptance repository must contain both native packages')
    return result


def baseline_package(candidate, output, version, commands):
    unpacked = output.parent / (output.stem + '-unpacked')
    commands.run(['dpkg-deb', '--raw-extract', candidate, unpacked])
    control = unpacked / 'DEBIAN/control'
    control.write_text(replace_control_version(control.read_text(), version))
    commands.run(['dpkg-deb', '--build', '--root-owner-group', unpacked, output])
    return output


def assert_installed(commands, package_version, arch):
    for package in PACKAGES:
        value = commands.run(['dpkg-query', '--show', '--showformat=${Status}\t${Version}\t${Architecture}', package])
        if value != f'install ok installed\t{package_version}\t{arch}':
            raise RuntimeError('APT installed an unexpected package identity: ' + package)


def assert_preserved(sentinels):
    if any(not path.is_file() or path.is_symlink() or path.read_bytes() != value
           for path, value in sentinels.items()):
        raise RuntimeError('Package lifecycle changed a disposable user configuration sentinel')


def copy_public_repository(source, destination):
    if not source.is_dir() or source.is_symlink():
        raise ValueError('Repository must be a real directory')
    for path in source.rglob('*'):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise ValueError('Repository contains a symlink or special file')
    shutil.copytree(source, destination)
    destination.chmod(0o755)
    for path in destination.rglob('*'):
        path.chmod(0o755 if path.is_dir() else 0o644)


def smoke(repository_directory, trusted_key, ubuntu_version, arch, version):
    require_disposable_container(ubuntu_version, arch)
    package_version = expected_package_version(version)
    suite = SUITES[ubuntu_version]
    checks = []
    with tempfile.TemporaryDirectory(prefix='kilo-apt-acceptance-') as temporary:
        root = Path(temporary)
        root.chmod(0o755)  # APT's unprivileged download user can read public inputs.
        home = root / 'synthetic-home'
        home.mkdir(mode=0o700)
        (home / 'runtime').mkdir(mode=0o700)
        commands = Commands(root, clean_environment(home))
        if commands.run(['dpkg', '--print-architecture']).strip() != arch:
            raise RuntimeError('Ubuntu package architecture does not match the requested native target')
        # The container must start without either package so a prior installation
        # cannot make a broken candidate appear to have installed successfully.
        for package in PACKAGES:
            status = commands.run(['dpkg-query', '--show', '--showformat=${Status}', package], allowed=(0, 1))
            if status.strip() == 'install ok installed':
                raise RuntimeError('APT acceptance requires a fresh container without Kilo Proxy packages')
        repository = root / 'repository'
        copy_public_repository(repository_directory, repository)
        selected = candidates(repository, arch, package_version, commands)
        keyring = root / 'kilo-proxy-test-keyring.gpg'
        commands.run(['gpg', '--batch', '--yes', '--dearmor', '--output', keyring, trusted_key.resolve()])
        keyring.chmod(0o644)
        release = repository / 'dists' / suite / 'InRelease'
        if not release.is_file():
            raise RuntimeError('Signed repository is missing the selected Ubuntu suite')
        commands.run(['gpg', '--batch', '--no-default-keyring', '--keyring', keyring, '--verify', release])
        checks.append('signed-repository-native-ubuntu-suite')

        sentinels = {}
        for relative in ('.config/kilo-proxy/apt-preservation-sentinel',
                         '.config/kilo-proxy-headless/apt-preservation-sentinel',
                         '.codex/apt-preservation-sentinel', '.claude/apt-preservation-sentinel'):
            path = home / relative
            path.parent.mkdir(parents=True, mode=0o700, exist_ok=True)
            value = ('synthetic configuration: ' + relative + '\n').encode()
            path.write_bytes(value)
            path.chmod(0o600)
            sentinels[path] = value

        baseline_version = package_version + '~apt-smoke.1'
        commands.run(['dpkg', '--compare-versions', baseline_version, 'lt', package_version])
        baseline = root / 'baseline'
        baseline.mkdir()
        baseline_paths = [baseline_package(selected[package], baseline / f'{package}_{arch}.deb', baseline_version, commands)
                          for package in PACKAGES]
        source = Path('/etc/apt/sources.list.d') / ('kilo-proxy-acceptance-' + uuid.uuid4().hex + '.sources')
        repository_uri = 'file:' + str(repository)
        source_text = (f'Types: deb\nURIs: {repository_uri}\nSuites: {suite}\nComponents: main\n'
                       f'Architectures: {arch}\nSigned-By: {keyring}\n')
        installed = False
        try:
            source.write_text(source_text)
            source.chmod(0o644)
            apt = ['apt-get', '-o', 'Acquire::AllowInsecureRepositories=false',
                   '-o', 'Acquire::AllowDowngradeToInsecureRepositories=false']
            commands.run([*apt, 'update'], timeout=600)
            commands.run([*apt, 'install', '--yes', '--no-install-recommends', *baseline_paths], timeout=600)
            installed = True
            assert_installed(commands, baseline_version, arch)
            assert_preserved(sentinels)
            checks.append('install-lower-metadata-version-same-candidate-executables')
            # Exact versions and the policy's file: source establish that APT
            # fetched the candidate from the authenticated test repository.
            for package in PACKAGES:
                policy = commands.run(['apt-cache', 'policy', package])
                if f'Candidate: {package_version}' not in policy or repository_uri not in policy:
                    raise RuntimeError('APT did not select the signed repository candidate: ' + package)
            commands.run([*apt, 'install', '--yes', '--no-install-recommends',
                          *(package + '=' + package_version for package in PACKAGES)], timeout=600)
            assert_installed(commands, package_version, arch)
            for package in PACKAGES:
                if commands.run(['dpkg', '--verify', package]).strip():
                    raise RuntimeError('Installed package payload failed verification: ' + package)
            assert_preserved(sentinels)
            checks.append('apt-upgrade-to-authenticated-candidate')
            for package, binary in BINARIES.items():
                if commands.run([binary, '--version']).strip() != version:
                    raise RuntimeError('Installed executable version differs from the release: ' + package)
            desktop_linkage = commands.run(['ldd', BINARIES['kilo-proxy-desktop']])
            if 'not found' in desktop_linkage or 'libc.so' not in desktop_linkage:
                raise RuntimeError('Installed desktop has unresolved native dependencies')
            launcher = Path('/usr/share/applications/kilo-proxy.desktop').read_text()
            if 'Exec=/usr/bin/kilo-proxy\n' not in launcher or 'Icon=kilo-proxy\n' not in launcher:
                raise RuntimeError('Installed desktop launcher is not configured for the installed executable/icon')
            if not Path('/usr/share/icons/hicolor/scalable/apps/kilo-proxy.svg').is_file():
                raise RuntimeError('Installed desktop application icon is missing')
            checks.append('installed-version-desktop-launcher-and-resolved-linkage')
            commands.run(['dbus-run-session', '--', 'xvfb-run', '-a', sys.executable,
                          ROOT / 'scripts/smoke_desktop.py', '--binary', BINARIES['kilo-proxy-desktop'],
                          '--version', version], timeout=210)
            checks.append('installed-native-desktop-lifecycle')
            commands.run([sys.executable, ROOT / 'scripts/smoke_headless.py', '--binary',
                          BINARIES['kilo-proxy-headless'], '--version', version], timeout=240)
            checks.append('installed-static-headless-lifecycle')
            commands.run([*apt, 'remove', '--yes', *PACKAGES], timeout=180)
            if any(binary.exists() for binary in BINARIES.values()):
                raise RuntimeError('Removing the packages left an installed executable')
            assert_preserved(sentinels)
            checks.append('apt-remove-preserves-user-configuration')
            commands.run([*apt, 'purge', '--yes', *PACKAGES], timeout=180)
            installed = False
            assert_preserved(sentinels)
            checks.append('apt-purge-preserves-user-configuration')
        finally:
            source.unlink(missing_ok=True)
            # Do not mask the original acceptance failure with cleanup errors.
            # The opted-in container is disposable; this never cleans a host.
            if installed:
                try:
                    commands.run(['apt-get', 'purge', '--yes', *PACKAGES], timeout=180)
                except RuntimeError:
                    print('Acceptance cleanup failed; discard the disposable container.', file=sys.stderr)
    return {'ubuntu': ubuntu_version, 'suite': suite, 'architecture': arch,
            'version': version, 'passed': True, 'checks': checks,
            'upgradeFixture': 'candidate executables with a lower package metadata version; not a prior-release binary'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repository-directory', required=True, type=Path)
    parser.add_argument('--trusted-key', type=Path,
                        help='Public signing key; defaults to the repository keyring ASC file.')
    parser.add_argument('--ubuntu-version', required=True, choices=SUITES)
    parser.add_argument('--arch', required=True, choices=('amd64', 'arm64'))
    parser.add_argument('--version', required=True)
    parser.add_argument('--report', type=Path)
    args = parser.parse_args()
    repository = args.repository_directory.resolve()
    key = args.trusted_key or repository / 'kilo-proxy-archive-keyring.asc'
    report = smoke(repository, key, args.ubuntu_version, args.arch, args.version)
    if args.report:
        args.report.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()

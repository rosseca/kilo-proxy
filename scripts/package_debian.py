#!/usr/bin/env python3
"""Package previously tested native Linux binaries; never build or configure an app."""
import argparse
import hashlib
import os
from pathlib import Path
import platform
import re
import shutil
import stat
import struct
import subprocess
import tempfile


ROOT = Path(__file__).resolve().parents[1]
VERSION_PATTERN = re.compile(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(alpha|beta|rc)\.([1-9][0-9]*))?')
PACKAGES = ('kilo-proxy-desktop', 'kilo-proxy-headless')
# Gio loads GLES and Vulkan at runtime. dpkg-shlibdeps cannot see dlopen, or
# the Mesa drivers needed for a usable software renderer on a clean system.
DESKTOP_RUNTIME_DEPENDS = ('libegl1', 'libgles2', 'libvulkan1',
                           'libgl1-mesa-dri', 'mesa-vulkan-drivers',
                           'dbus-user-session | dbus-session-bus')
DESKTOP_ENTRY = '''[Desktop Entry]
Type=Application
Name=Kilo Proxy
Comment=Connect your editors to your organization's Kilo credits
Exec=/usr/bin/kilo-proxy
Icon=kilo-proxy
Terminal=false
Categories=Development;
'''


def debian_version(version):
    if not VERSION_PATTERN.fullmatch(version):
        raise ValueError('version must be X.Y.Z or X.Y.Z-alpha.N / beta.N / rc.N')
    # Debian sorts ~ before the stable version, so beta -> stable upgrades work.
    return version.replace('-', '~', 1)


def read_regular(path, executable=False):
    """Reject links and special files, including a link swapped in before open."""
    if path.is_symlink() or not path.is_file():
        raise ValueError(f'Expected a regular file, not a link or special file: {path}')
    descriptor = os.open(path, os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0))
    with os.fdopen(descriptor, 'rb') as source:
        mode = os.fstat(source.fileno()).st_mode
        if not stat.S_ISREG(mode) or (executable and os.name == 'posix' and not mode & 0o111):
            raise ValueError(f'Expected a regular {"executable" if executable else "file"}: {path}')
        return source.read()


def validate_elf(path, arch, headless=False):
    data = read_regular(path, executable=True)
    try:
        if (len(data) < 64 or data[:7] != b'\x7fELF\x02\x01\x01' or data[7] not in (0, 3) or
                struct.unpack_from('<H', data, 16)[0] not in (2, 3) or
                struct.unpack_from('<H', data, 18)[0] != {'amd64': 62, 'arm64': 183}[arch]):
            raise ValueError('wrong Linux ELF class, byte order, type or architecture')
        offset = struct.unpack_from('<Q', data, 32)[0]
        entry_size, count = struct.unpack_from('<HH', data, 54)
        if not count or entry_size < 56 or offset < 64 or offset + count * entry_size > len(data):
            raise ValueError('malformed ELF program header')
        types = {struct.unpack_from('<I', data, offset + index * entry_size)[0]
                 for index in range(count)}
        if headless and types & {2, 3}:  # PT_DYNAMIC and PT_INTERP.
            raise ValueError('headless Linux binary must be static (no dynamic table or interpreter)')
        if not headless and not {2, 3}.issubset(types):
            raise ValueError('desktop Linux binary must contain its dynamic table and interpreter')
    except (struct.error, KeyError) as error:
        raise ValueError(f'Malformed Linux/{arch} binary: {path}') from error
    return data


def require_native_host(arch):
    actual = {'x86_64': 'amd64', 'amd64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}.get(platform.machine().lower())
    if platform.system() != 'Linux' or actual != arch:
        raise ValueError(f'Debian packaging requires a native Linux/{arch} host')
    for command in ('dpkg', 'dpkg-deb', 'dpkg-shlibdeps'):
        if not shutil.which(command):
            raise ValueError(f'Debian packaging requires {command} (dpkg-dev for shlibdeps)')
    actual = subprocess.run(['dpkg', '--print-architecture'], check=True,
                            capture_output=True, text=True).stdout.strip()
    if actual != arch:
        raise ValueError(f'dpkg architecture {actual!r} does not match {arch}')


def isolated_environment(home):
    # --version runs before any profile is opened. Still isolate HOME and remove
    # client/loader hooks so packaging never uses the packager's real account.
    environment = dict(os.environ)
    for name in list(environment):
        if (name.startswith(('KILO_', 'CODEX_', 'CLAUDE_', 'ANTHROPIC_', 'OPENAI_',
                             'OMP_', 'PI_', 'OPENCODE_', 'LD_', 'DYLD_')) or
                name in ('SSH_AUTH_SOCK', 'NODE_OPTIONS', 'ELECTRON_RUN_AS_NODE',
                         'DISPLAY', 'WAYLAND_DISPLAY', 'DBUS_SESSION_BUS_ADDRESS',
                         'DBUS_SYSTEM_BUS_ADDRESS')):
            environment.pop(name, None)
    environment.update(HOME=str(home), XDG_CONFIG_HOME=str(home / 'config'),
                       XDG_DATA_HOME=str(home / 'data'), XDG_CACHE_HOME=str(home / 'cache'),
                       XDG_STATE_HOME=str(home / 'state'), LC_ALL='C')
    return environment


def verify_version(binary, version, home):
    result = subprocess.run([str(binary), '--version'], check=True, capture_output=True,
                            text=True, timeout=20, cwd=home, env=isolated_environment(home))
    if result.stdout.strip() != version:
        raise ValueError(f'Binary version does not match {version}: {binary.name}')


def parse_shlibdeps(output):
    lines = output.strip().splitlines()
    if len(lines) != 1 or not lines[0].startswith('shlibs:Depends='):
        raise ValueError('dpkg-shlibdeps did not produce exactly one runtime dependency field')
    dependencies = lines[0].removeprefix('shlibs:Depends=').strip()
    if not dependencies:
        raise ValueError('Desktop binary has no detected shared-library dependencies')
    package = r'[a-z0-9][a-z0-9+.-]*(?::(?:any|native|amd64|arm64))?'
    constraint = r'(?: \((?:<<|<=|=|>=|>>) [A-Za-z0-9.+:~\-]+\))?'
    term = package + constraint
    if not re.fullmatch(term + r'(?: \| ' + term + r')?(?:, ' + term + r'(?: \| ' + term + r')?)*', dependencies):
        raise ValueError('Unsafe or unexpected dpkg-shlibdeps dependency syntax')
    if re.search(r'\b[a-z0-9+.-]+-dev(?:\b|:)', dependencies):
        raise ValueError('A runtime package must not depend on development packages')
    return dependencies.split(', ')


def desktop_dependencies(binary, working):
    debian = working / 'debian'
    debian.mkdir()
    (debian / 'control').write_text('Source: kilo-proxy\nSection: utils\nPriority: optional\n'
                                  'Maintainer: Kilo Proxy maintainers <noreply@github.com>\n'
                                  'Standards-Version: 4.6.2\n\nPackage: kilo-proxy-desktop\n'
                                  'Architecture: any\nDescription: Kilo Proxy desktop\n')
    result = subprocess.run(['dpkg-shlibdeps', '-O', '-e' + str(binary)], cwd=working,
                            check=True, capture_output=True, text=True,
                            env=dict(os.environ, LC_ALL='C'))
    dependencies = parse_shlibdeps(result.stdout)
    direct_names = {re.split(r'[ :(]', item, 1)[0] for item in dependencies if ' | ' not in item}
    for item in ('ca-certificates', *DESKTOP_RUNTIME_DEPENDS):
        if item not in dependencies and item not in direct_names:
            dependencies.append(item)
    return ', '.join(dependencies)


def write_payload(stage, relative, content, executable=False):
    target = stage / relative
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(content)
    target.chmod(0o755 if executable else 0o644)


def copy_documentation(stage, package, root):
    destination = f'usr/share/doc/{package}'
    for name in ('README.md', 'THIRD-PARTY-NOTICES.txt'):
        write_payload(stage, destination + '/' + name, read_regular(root / name))
    # Keep all packaged guidance and the exact notices/patches used in archives.
    if (root / 'docs').is_symlink() or not (root / 'docs').is_dir():
        raise ValueError('Expected a regular documentation directory')
    for source in sorted((root / 'docs').rglob('*')):
        if source.is_symlink() or not (source.is_file() or source.is_dir()):
            raise ValueError(f'Unsafe documentation entry: {source}')
        if source.is_file():
            write_payload(stage, destination + '/docs/' + source.relative_to(root / 'docs').as_posix(), read_regular(source))
    for dependency in ('gio', 'fyne-systray', 'systray'):
        directory = root / 'third_party' / dependency
        if directory.is_symlink():
            raise ValueError(f'Unsafe third-party notice directory: {directory}')
        if not directory.is_dir():
            continue
        for source in sorted(directory.iterdir()):
            if source.name in ('LICENSE', 'PATCHES.md') or source.suffix == '.patch':
                write_payload(stage, destination + '/third_party/' + dependency + '/' + source.name, read_regular(source))


def normalize_stage(stage, epoch):
    for path in sorted(stage.rglob('*')):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise ValueError(f'Unsafe staged entry: {path}')
        if path.is_dir():
            path.chmod(0o755)
        os.utime(path, (epoch, epoch))
    stage.chmod(0o755)
    os.utime(stage, (epoch, epoch))


def build_packages(binaries_directory, headless_binaries_directory, arch, version, output, root=ROOT):
    deb_version = debian_version(version)
    require_native_host(arch)
    epoch_text = os.environ.get('SOURCE_DATE_EPOCH', '0')
    if not re.fullmatch(r'[0-9]+', epoch_text) or int(epoch_text) > 0xffffffff:
        raise ValueError('SOURCE_DATE_EPOCH must be an integer between 0 and 4294967295')
    epoch = int(epoch_text)
    sources = [Path(binaries_directory).absolute() / f'kilo-proxy-linux-{arch}',
               Path(headless_binaries_directory).absolute() / f'kilo-proxy-headless-linux-{arch}']
    for directory in (Path(binaries_directory), Path(headless_binaries_directory)):
        if directory.is_symlink() or not directory.is_dir():
            raise ValueError(f'Expected a regular binaries directory: {directory}')
    binary_data = [validate_elf(source, arch, headless=index == 1)
                   for index, source in enumerate(sources)]
    output = Path(output).absolute()
    if output.is_symlink() or (output.exists() and not output.is_dir()):
        raise ValueError('Output must be a regular directory')
    destinations = [output / f'{package}_{deb_version}_{arch}.deb' for package in PACKAGES]
    if any(path.exists() or path.is_symlink() for path in destinations):
        raise ValueError('Refusing to overwrite an existing Debian package')
    # No destination is touched until both source versions have been checked.
    with tempfile.TemporaryDirectory(prefix='kilo-debian-') as temporary:
        working = Path(temporary)
        home = working / 'home'
        home.mkdir(mode=0o700)
        inputs = working / 'inputs'
        inputs.mkdir()
        frozen_sources = []
        for source, payload in zip(sources, binary_data):
            frozen = inputs / source.name
            frozen.write_bytes(payload)
            frozen.chmod(0o755)
            frozen_sources.append(frozen)
        for source in frozen_sources:
            verify_version(source, version, home)
        depends = desktop_dependencies(frozen_sources[0], working)
        built = []
        for index, (package, payload) in enumerate(zip(PACKAGES, binary_data)):
            stage = working / package
            stage.mkdir()
            binary = 'kilo-proxy-headless' if index else 'kilo-proxy'
            write_payload(stage, 'usr/bin/' + binary, payload, executable=True)
            copy_documentation(stage, package, root)
            if index == 0:
                write_payload(stage, 'usr/share/applications/kilo-proxy.desktop', DESKTOP_ENTRY.encode())
                write_payload(stage, 'usr/share/icons/hicolor/scalable/apps/kilo-proxy.svg', read_regular(root / 'ui/icon.svg'))
            installed_size = (sum(path.stat().st_size for path in stage.rglob('*') if path.is_file()) + 1023) // 1024
            control = (f'Package: {package}\nVersion: {deb_version}\nArchitecture: {arch}\n'
                       'Section: utils\nPriority: optional\n'
                       'Maintainer: Kilo Proxy maintainers <noreply@github.com>\n'
                       'Homepage: https://github.com/rosseca/kilo-proxy\n'
                       f'Installed-Size: {installed_size}\n'
                       f'Depends: {"ca-certificates" if index else depends}\n'
                       f'Description: Kilo Proxy {"console server" if index else "native desktop app"}\n'
                       ' Connect agent clients to organization Kilo credits.\n'
                       ' Installation does not configure accounts, start a service or alter profiles.\n')
            write_payload(stage, 'DEBIAN/control', control.encode())
            md5sums = ''.join(hashlib.md5(path.read_bytes(), usedforsecurity=False).hexdigest() + '  ' + path.relative_to(stage).as_posix() + '\n'
                              for path in sorted(stage.rglob('*')) if path.is_file() and 'DEBIAN' not in path.relative_to(stage).parts)
            write_payload(stage, 'DEBIAN/md5sums', md5sums.encode())
            normalize_stage(stage, epoch)
            built_path = working / destinations[index].name
            subprocess.run(['dpkg-deb', '--root-owner-group', '--uniform-compression', '-Zxz', '-z9',
                            '--build', str(stage), str(built_path)], check=True,
                           env=dict(os.environ, SOURCE_DATE_EPOCH=str(epoch), LC_ALL='C'))
            built.append(built_path)
        output.mkdir(parents=True, exist_ok=True)
        # Exclusive creation also prevents a concurrent packager overwriting a
        # completed package after the initial preflight.
        for source, destination in zip(built, destinations):
            with source.open('rb') as data, destination.open('xb') as target:
                shutil.copyfileobj(data, target)
            destination.chmod(0o644)
    return destinations


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binaries-directory', type=Path, required=True)
    parser.add_argument('--headless-binaries-directory', type=Path, required=True)
    parser.add_argument('--arch', choices=('amd64', 'arm64'), required=True)
    parser.add_argument('--version', default=(ROOT / 'VERSION').read_text().strip())
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    try:
        paths = build_packages(args.binaries_directory, args.headless_binaries_directory,
                               args.arch, args.version, args.output)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        parser.exit(1, f'Debian packaging failed: {error}\n')
    for path in paths:
        print(path, flush=True)


if __name__ == '__main__':
    main()

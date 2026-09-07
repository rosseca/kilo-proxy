#!/usr/bin/env python3
"""Build desktop executables, then package tested inputs and signed macOS bundles."""
import argparse
import hashlib
import os
from pathlib import Path
import plistlib
import platform
import re
import shutil
import struct
import subprocess
import sys
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
TARGETS = [('darwin', 'arm64'), ('darwin', 'amd64'), ('linux', 'amd64'), ('linux', 'arm64'), ('windows', 'amd64'), ('windows', 'arm64')]


def require_macos(targets):
    if any(system == 'darwin' for system, _ in targets) and sys.platform != 'darwin':
        raise ValueError('macOS app bundles must be packaged on macOS so their complete '
                         'bundle can be signed and verified. Select non-macOS targets '
                         'with --target (repeat it to build several targets).')


def verify_macos_bundle(bundle):
    subprocess.run(['/usr/bin/codesign', '--verify', '--deep', '--strict',
                    '--verbose=2', str(bundle)], check=True)


def sign_macos_bundle(bundle):
    # Go's linker signs the executable alone. Once inside an app bundle that
    # signature does not bind Info.plist or seal Resources; Finder can reject it.
    # Sign the completed bundle, then fail packaging if its integrity is invalid.
    # This is an ad-hoc integrity signature, not Developer ID or notarization.
    subprocess.run(['/usr/bin/codesign', '--force', '--sign', '-',
                    '--timestamp=none', str(bundle)], check=True)
    verify_macos_bundle(bundle)


def verify_macos_archives(directory, version, targets=None):
    require_macos([('darwin', 'arm64')])
    for system, arch in targets or TARGETS:
        if system != 'darwin':
            continue
        name = f'kilo-proxy-{version}-{system}-{arch}'
        archive = directory / (name + '.zip')
        if not archive.is_file():
            raise ValueError(f'Missing macOS archive: {archive.name}')
        with tempfile.TemporaryDirectory(prefix='kilo-macos-archive-') as temporary:
            # Exercise Apple's ZIP extraction, preserving executable permissions.
            subprocess.run(['/usr/bin/ditto', '-x', '-k', str(archive), temporary], check=True)
            bundle = Path(temporary) / name / 'Kilo Proxy.app'
            verify_macos_bundle(bundle)
        print(f'Verified extracted macOS app: {archive.name}', flush=True)


def native_target():
    system = {'Darwin': 'darwin', 'Linux': 'linux', 'Windows': 'windows'}.get(platform.system())
    arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}.get(platform.machine().lower())
    if (system, arch) not in TARGETS:
        raise ValueError('This host is not one of the six supported desktop targets')
    return system, arch


def binary_name(system, arch):
    return f'kilo-proxy-{system}-{arch}' + ('.exe' if system == 'windows' else '')


def require_build_host(targets):
    host_system, host_arch = native_target()
    for system, arch in targets:
        if system == 'windows':
            continue  # Gio uses the Windows Direct3D API through pure Go on both CPUs.
        if system != host_system or (system == 'linux' and arch != host_arch):
            raise ValueError(f'{system}/{arch} requires its native build host and graphics development '
                             'libraries. Use CI or --binaries-directory for prebuilt desktop binaries.')


def build_binary(system, arch, executable, version):
    ldflags = f'-s -w -X main.version={version}'
    if system == 'windows':
        ldflags += ' -H=windowsgui'
    env = dict(os.environ, GOOS=system, GOARCH=arch,
               CGO_ENABLED='0' if system == 'windows' else '1')
    if system == 'darwin':
        # Compile both Intel and Apple Silicon against the documented minimum.
        for name in ('CGO_CFLAGS', 'CGO_LDFLAGS'):
            env[name] = (env.get(name, '') + ' -mmacosx-version-min=12.0').strip()
    executable.parent.mkdir(parents=True, exist_ok=True)
    resource = None
    if system == 'windows':
        resource = ROOT / f'kilo_proxy_icon_windows_{arch}.syso'
        if resource.exists():
            raise ValueError(f'Remove the leftover build resource before retrying: {resource.name}')
    try:
        if resource:
            host_system, host_arch = native_target()
            resource_env = dict(os.environ, GOOS=host_system, GOARCH=host_arch, CGO_ENABLED='0')
            subprocess.run(['go', 'run', 'github.com/tc-hib/go-winres@v0.3.3', 'simply',
                            '--arch', arch, '--out', str(ROOT / 'kilo_proxy_icon'),
                            '--icon', str(ROOT / 'ui' / 'icon.png'), '--manifest', 'gui',
                            '--product-name', 'Kilo Proxy', '--file-description', 'Kilo Proxy',
                            '--original-filename', 'Kilo Proxy.exe',
                            '--product-version', version.split('-')[0],
                            '--file-version', version.split('-')[0]],
                           cwd=ROOT, env=resource_env, check=True)
        subprocess.run(['go', 'build', '-trimpath', '-tags', 'desktop',
                        '-ldflags', ldflags, '-o', str(executable), '.'],
                       cwd=ROOT, env=env, check=True)
    finally:
        if resource:
            resource.unlink(missing_ok=True)

    executable.chmod(0o755)


def windows_imports(executable):
    """Read the PE import table using only the Python standard library."""
    data = executable.read_bytes()
    try:
        pe = struct.unpack_from('<I', data, 60)[0]
        section_count, optional_size = struct.unpack_from('<H', data, pe + 6)[0], struct.unpack_from('<H', data, pe + 20)[0]
        optional = pe + 24
        if struct.unpack_from('<H', data, optional)[0] != 0x20b or optional_size < 128:
            raise ValueError('Expected a 64-bit Windows executable')
        sections = []
        for i in range(section_count):
            offset = optional + optional_size + i * 40
            virtual_size, virtual_address, raw_size, raw_offset = struct.unpack_from('<IIII', data, offset + 8)
            sections.append((virtual_address, max(virtual_size, raw_size), raw_offset, raw_size))

        def offset_for(rva):
            for start, size, raw, raw_size in sections:
                if start <= rva < start + size and rva - start < raw_size:
                    return raw + rva - start
            raise ValueError('Invalid Windows import address')

        imports = []
        rva, size = struct.unpack_from('<II', data, optional + 120)
        if not rva or not size:
            raise ValueError('Windows executable has no import table')
        table = offset_for(rva)
        for cursor in range(table, min(table + size, len(data)), 20):
            descriptor = struct.unpack_from('<IIIII', data, cursor)
            if not any(descriptor):
                return imports
            name = offset_for(descriptor[3])
            end = data.index(b'\0', name, min(name + 256, len(data)))
            imports.append(data[name:end].decode('ascii').lower())
        raise ValueError('Unterminated Windows import table')
    except (struct.error, UnicodeDecodeError, IndexError) as error:
        raise ValueError('Malformed Windows import table') from error


def verify_windows_runtime(executable):
    # Go resolves many OS APIs dynamically; linked imports still must never add
    # a compiler DLL, browser loader, or other non-system runtime prerequisite.
    allowed = {'kernel32.dll', 'user32.dll', 'gdi32.dll', 'advapi32.dll', 'shell32.dll',
               'ole32.dll', 'oleaut32.dll', 'comdlg32.dll', 'comctl32.dll', 'ws2_32.dll',
               'secur32.dll', 'crypt32.dll', 'bcrypt.dll', 'ntdll.dll', 'd3d11.dll',
               'dxgi.dll', 'dwmapi.dll', 'shcore.dll', 'imm32.dll', 'version.dll',
               'winmm.dll', 'uxtheme.dll', 'msvcrt.dll'}
    imports = windows_imports(executable)
    unexpected = set(imports) - allowed
    if unexpected:
        raise ValueError('Windows executable needs a non-system runtime: ' + ', '.join(sorted(unexpected)))
    return imports


def validate_binary(executable, system, arch):
    # Inspect the native file header without executing a possibly foreign target.
    # This catches artifact mix-ups before a correctly named, wrong-CPU ZIP ships.
    if not executable.is_file() or executable.is_symlink():
        raise ValueError(f'Missing or unsafe desktop binary: {executable}')
    with executable.open('rb') as stream:
        header = stream.read(64)
        actual = None
        if len(header) >= 20 and header[:4] == b'\x7fELF' and header[4:6] == b'\x02\x01':
            actual = ('linux', {62: 'amd64', 183: 'arm64'}.get(struct.unpack_from('<H', header, 18)[0]))
        elif len(header) >= 8 and header[:4] == b'\xcf\xfa\xed\xfe':
            actual = ('darwin', {0x01000007: 'amd64', 0x0100000c: 'arm64'}.get(struct.unpack_from('<I', header, 4)[0]))
        elif len(header) >= 64 and header[:2] == b'MZ':
            stream.seek(struct.unpack_from('<I', header, 60)[0])
            pe = stream.read(6)
            if len(pe) == 6 and pe[:4] == b'PE\x00\x00':
                actual = ('windows', {0x8664: 'amd64', 0xaa64: 'arm64'}.get(struct.unpack_from('<H', pe, 4)[0]))
    if actual != (system, arch):
        raise ValueError(f'Desktop binary header does not match {system}/{arch}: {executable}')
    if system == 'windows':
        verify_windows_runtime(executable)


def write_checksums(out, version, targets):
    archives = [out / (f'kilo-proxy-{version}-{system}-{arch}' +
                      ('.tar.gz' if system == 'linux' else '.zip')) for system, arch in targets]
    for archive in archives:
        if not archive.is_file() or archive.is_symlink() or not archive.stat().st_size:
            raise ValueError(f'Missing or invalid archive: {archive.name}')
    # Validate the whole set before replacing a prior manifest.
    content = ''.join(hashlib.sha256(archive.read_bytes()).hexdigest() + '  ' + archive.name + '\n'
                      for archive in archives)
    (out / 'SHA256SUMS.txt').write_text(content)


def icon_png():
    # The same generated artwork is embedded in the native sidebar and tray.
    return (ROOT / 'ui' / 'icon.png').read_bytes()


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--version', default=(ROOT/'VERSION').read_text().strip())
    p.add_argument('--target', action='append', choices=[f'{o}/{a}' for o,a in TARGETS],
                   help='Select a target; repeat for several (default: native host, or all six with prebuilt binaries)')
    p.add_argument('--output', type=Path, default=ROOT/'dist')
    p.add_argument('--build-only', action='store_true', help='Build raw desktop binaries for native smoke tests; do not create bundles')
    p.add_argument('--binaries-directory', type=Path, help='Package previously built and tested kilo-proxy-OS-ARCH binaries without rebuilding')
    p.add_argument('--checksums-only', action='store_true', help='Require all six archives and create their combined checksum manifest')
    p.add_argument('--verify-macos-archives', action='store_true',
                   help='Extract and verify macOS ZIPs without rebuilding; --target selects one (macOS only)')
    args = p.parse_args()
    if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:alpha|beta|rc)\.[1-9][0-9]*)?', args.version):
        p.error('version must be X.Y.Z or X.Y.Z-alpha.N / beta.N / rc.N')
    if sum((args.build_only, bool(args.binaries_directory), args.checksums_only,
            args.verify_macos_archives)) > 1:
        p.error('Choose only one of --build-only, --binaries-directory, --checksums-only, or --verify-macos-archives')
    requested = list(dict.fromkeys(tuple(target.split('/')) for target in args.target)) if args.target else None
    try:
        if args.checksums_only:
            if requested:
                p.error('--checksums-only requires all six archives; omit --target')
            write_checksums(args.output.resolve(), args.version, TARGETS)
            return
        if args.verify_macos_archives:
            if requested and any(system != 'darwin' for system, _ in requested):
                p.error('--verify-macos-archives only accepts Darwin targets')
            verify_macos_archives(args.output.resolve(), args.version, requested)
            return
        targets = requested or (TARGETS if args.binaries_directory else [native_target()])
        if not args.build_only:
            require_macos(targets)
        if not args.binaries_directory:
            require_build_host(targets)
        else:
            # Preflight every input before touching prior staging or archives.
            for system, arch in targets:
                validate_binary(args.binaries_directory / binary_name(system, arch), system, arch)
    except ValueError as error:
        p.error(str(error))
    if args.build_only:
        for system, arch in targets:
            executable = args.output.resolve() / binary_name(system, arch)
            build_binary(system, arch, executable, args.version)
            validate_binary(executable, system, arch)
            print(executable, flush=True)
        return
    bundle_version = args.version.split('-')[0]
    out = args.output.resolve(); out.mkdir(parents=True, exist_ok=True)
    for system, arch in targets:
        name = f'kilo-proxy-{args.version}-{system}-{arch}'
        stage = out/'staging'/name
        if stage.exists(): shutil.rmtree(stage)
        stage.mkdir(parents=True)
        if system == 'darwin':
            bundle = stage/'Kilo Proxy.app'/'Contents'
            (bundle/'MacOS').mkdir(parents=True)
            (bundle/'Resources').mkdir()
            png = icon_png()
            icns = b'ic09'+struct.pack('>I',len(png)+8)+png
            (bundle/'Resources'/'AppIcon.icns').write_bytes(b'icns'+struct.pack('>I',len(icns)+8)+icns)
            with (bundle/'Info.plist').open('wb') as f:
                plistlib.dump({'CFBundleName':'Kilo Proxy','CFBundleDisplayName':'Kilo Proxy','CFBundleIdentifier':'ai.kilo.local-proxy','CFBundleExecutable':'kilo-proxy','CFBundlePackageType':'APPL','CFBundleShortVersionString':bundle_version,'CFBundleVersion':bundle_version,'CFBundleIconFile':'AppIcon','LSUIElement':False,'LSMinimumSystemVersion':'12.0','NSHighResolutionCapable':True},f)
            executable = bundle/'MacOS'/'kilo-proxy'
        else:
            executable = stage/('Kilo Proxy.exe' if system == 'windows' else 'kilo-proxy')
        if args.binaries_directory:
            shutil.copyfile(args.binaries_directory / binary_name(system, arch), executable)
        else:
            build_binary(system, arch, executable, args.version)
        executable.chmod(0o755)
        validate_binary(executable, system, arch)
        if system == 'darwin':
            sign_macos_bundle(bundle.parent)
        shutil.copy2(ROOT/'README.md',stage/'README.md')
        shutil.copy2(ROOT/'THIRD-PARTY-NOTICES.txt',stage/'THIRD-PARTY-NOTICES.txt')
        if (ROOT/'docs').exists(): shutil.copytree(ROOT/'docs',stage/'docs')
        if (ROOT/'third_party'/'systray').is_dir():
            notices = stage/'third_party'/'systray'
            notices.mkdir(parents=True)
            for filename in ('PATCHES.md', 'LICENSE'):
                shutil.copy2(ROOT/'third_party'/'systray'/filename, notices/filename)
        if (ROOT/'third_party'/'gio').is_dir():
            notices = stage/'third_party'/'gio'
            notices.mkdir(parents=True)
            for filename in ('PATCHES.md', 'LICENSE', 'kilo-local.patch'):
                shutil.copy2(ROOT/'third_party'/'gio'/filename, notices/filename)
        if system == 'linux':
            shutil.copy2(ROOT/'ui'/'icon.svg', stage/'kilo-proxy.svg')
            installer = stage/'install-user.sh'
            installer.write_text('''#!/bin/sh
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
APP_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/kilo-local"
DESKTOP_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
mkdir -p "$APP_DIR" "$DESKTOP_DIR"
cp "$HERE/kilo-proxy" "$APP_DIR/kilo-proxy"
cp "$HERE/kilo-proxy.svg" "$APP_DIR/icon.svg"
chmod 755 "$APP_DIR/kilo-proxy"
cat > "$DESKTOP_DIR/kilo-local.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=Kilo Proxy
Comment=Connect your editors to your organization’s Kilo credits
Exec="$APP_DIR/kilo-proxy"
Icon=$APP_DIR/icon.svg
Terminal=false
Categories=Development;
DESKTOP
printf 'Kilo Proxy is available in your applications menu.\\n'
''')
            installer.chmod(0o755)
        if system == 'linux':
            archive = out/(name+'.tar.gz')
            with tarfile.open(archive,'w:gz') as tar: tar.add(stage,arcname=name)
        else:
            archive = out/(name+'.zip')
            with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED) as zipped:
                for entry in sorted(stage.rglob('*')):
                    zipped.write(entry,entry.relative_to(stage.parent))
        print(archive,flush=True)
    write_checksums(out, args.version, targets)

if __name__ == '__main__': main()

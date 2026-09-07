#!/usr/bin/env python3
"""Build portable archives with ad-hoc signed macOS bundles; no publisher signing."""
import argparse
import hashlib
import os
from pathlib import Path
import plistlib
import re
import shutil
import struct
import subprocess
import sys
import tarfile
import tempfile
import zipfile
import zlib

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


def verify_macos_archives(directory, version):
    require_macos([('darwin', 'arm64')])
    for system, arch in TARGETS:
        if system != 'darwin':
            continue
        name = f'kilo-local-{version}-{system}-{arch}'
        archive = directory / (name + '.zip')
        if not archive.is_file():
            raise ValueError(f'Missing macOS archive: {archive.name}')
        with tempfile.TemporaryDirectory(prefix='kilo-macos-archive-') as temporary:
            # Exercise Apple's ZIP extraction, preserving executable permissions.
            subprocess.run(['/usr/bin/ditto', '-x', '-k', str(archive), temporary], check=True)
            bundle = Path(temporary) / name / 'Kilo Local.app'
            verify_macos_bundle(bundle)
        print(f'Verified extracted macOS app: {archive.name}', flush=True)


def icon_png(size=512):
    # Rasterize the project's own simple geometric icon; no image libraries needed.
    polygon = [(17,15),(25,15),(25,31),(39,15),(49,15),(33,33),(50,50),(39,50),(25,36),(25,50),(17,50)]
    def inside(x, y):
        result = False
        for i, (ax, ay) in enumerate(polygon):
            bx, by = polygon[i - 1]
            if (ay > y) != (by > y) and x < (bx-ax)*(y-ay)/(by-ay)+ax:
                result = not result
        return result
    pixels = bytearray()
    for y in range(size):
        pixels.append(0)
        for x in range(size):
            px, py = (x+.5)*64/size, (y+.5)*64/size
            dx, dy = max(17-px, 0, px-47), max(17-py, 0, py-47)
            alpha = 255 if dx*dx+dy*dy <= 17*17 else 0
            color = (32,37,32) if inside(px,py) or (px-47)**2+(py-17)**2 < 16 else (232,243,106)
            pixels.extend((*color, alpha))
    def chunk(kind, content):
        return struct.pack('>I',len(content))+kind+content+struct.pack('>I',zlib.crc32(kind+content)&0xffffffff)
    return b'\x89PNG\r\n\x1a\n'+chunk(b'IHDR',struct.pack('>IIBBBBB',size,size,8,6,0,0,0))+chunk(b'IDAT',zlib.compress(bytes(pixels)))+chunk(b'IEND',b'')


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--version', default=(ROOT/'VERSION').read_text().strip())
    p.add_argument('--target', action='append', choices=[f'{o}/{a}' for o,a in TARGETS],
                   help='Build this target; repeat for several targets (default: all six)')
    p.add_argument('--output', type=Path, default=ROOT/'dist')
    p.add_argument('--verify-macos-archives', action='store_true',
                   help='Extract and verify both macOS ZIPs without rebuilding (macOS only)')
    args = p.parse_args()
    if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:alpha|beta|rc)\.[1-9][0-9]*)?', args.version):
        p.error('version must be X.Y.Z or X.Y.Z-alpha.N / beta.N / rc.N')
    if args.verify_macos_archives:
        if args.target:
            p.error('--verify-macos-archives verifies both macOS targets; omit --target')
        try:
            verify_macos_archives(args.output.resolve(), args.version)
        except ValueError as error:
            p.error(str(error))
        return
    targets = list(dict.fromkeys(tuple(target.split('/')) for target in args.target)) if args.target else TARGETS
    try:
        require_macos(targets)
    except ValueError as error:
        p.error(str(error))
    bundle_version = args.version.split('-')[0]
    out = args.output.resolve(); out.mkdir(parents=True, exist_ok=True)
    archives = []
    for system, arch in targets:
        name = f'kilo-local-{args.version}-{system}-{arch}'
        stage = out/'staging'/name
        if stage.exists(): shutil.rmtree(stage)
        stage.mkdir(parents=True)
        ldflags = f'-s -w -X main.version={args.version}'
        if system == 'darwin':
            bundle = stage/'Kilo Local.app'/'Contents'
            (bundle/'MacOS').mkdir(parents=True)
            (bundle/'Resources').mkdir()
            png = icon_png()
            icns = b'ic09'+struct.pack('>I',len(png)+8)+png
            (bundle/'Resources'/'AppIcon.icns').write_bytes(b'icns'+struct.pack('>I',len(icns)+8)+icns)
            with (bundle/'Info.plist').open('wb') as f:
                plistlib.dump({'CFBundleName':'Kilo Local','CFBundleDisplayName':'Kilo Local','CFBundleIdentifier':'ai.kilo.local-proxy','CFBundleExecutable':'kilo-local','CFBundlePackageType':'APPL','CFBundleShortVersionString':bundle_version,'CFBundleVersion':bundle_version,'CFBundleIconFile':'AppIcon','LSUIElement':True,'LSMinimumSystemVersion':'12.0','NSHighResolutionCapable':True},f)
            executable = bundle/'MacOS'/'kilo-local'
        else:
            executable = stage/('Kilo Local.exe' if system == 'windows' else 'kilo-local')
        if system == 'windows': ldflags += ' -H=windowsgui'
        env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED='0')
        subprocess.run(['go','build','-trimpath','-ldflags',ldflags,'-o',str(executable),'.'],cwd=ROOT,env=env,check=True)
        executable.chmod(0o755)
        if system == 'darwin':
            sign_macos_bundle(bundle.parent)
        shutil.copy2(ROOT/'README.md',stage/'README.md')
        shutil.copy2(ROOT/'THIRD-PARTY-NOTICES.txt',stage/'THIRD-PARTY-NOTICES.txt')
        if (ROOT/'docs').exists(): shutil.copytree(ROOT/'docs',stage/'docs')
        notices = stage/'third_party'/'systray'
        notices.mkdir(parents=True)
        for filename in ('PATCHES.md', 'LICENSE'):
            shutil.copy2(ROOT/'third_party'/'systray'/filename, notices/filename)
        if system == 'linux':
            shutil.copy2(ROOT/'ui'/'icon.svg', stage/'kilo-local.svg')
            installer = stage/'install-user.sh'
            installer.write_text('''#!/bin/sh
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
APP_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/kilo-local"
DESKTOP_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
mkdir -p "$APP_DIR" "$DESKTOP_DIR"
cp "$HERE/kilo-local" "$APP_DIR/kilo-local"
cp "$HERE/kilo-local.svg" "$APP_DIR/icon.svg"
chmod 755 "$APP_DIR/kilo-local"
cat > "$DESKTOP_DIR/kilo-local.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=Kilo Local
Comment=Connect your editors to your organization’s Kilo credits
Exec="$APP_DIR/kilo-local"
Icon=$APP_DIR/icon.svg
Terminal=false
Categories=Development;
DESKTOP
printf 'Kilo Local is available in your applications menu.\\n'
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
        archives.append(archive)
        print(archive,flush=True)
    with (out/'SHA256SUMS.txt').open('w') as checksums:
        for archive in archives:
            checksums.write(hashlib.sha256(archive.read_bytes()).hexdigest()+'  '+archive.name+'\n')

if __name__ == '__main__': main()

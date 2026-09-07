#!/usr/bin/env python3
"""Build unsigned, portable release archives. Requires Go >=1.26 and Python >=3.9."""
import argparse
import hashlib
import os
from pathlib import Path
import plistlib
import re
import shutil
import struct
import subprocess
import tarfile
import zipfile
import zlib

ROOT = Path(__file__).resolve().parents[1]
TARGETS = [('darwin', 'arm64'), ('darwin', 'amd64'), ('linux', 'amd64'), ('linux', 'arm64'), ('windows', 'amd64'), ('windows', 'arm64')]


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
    p.add_argument('--target', choices=[f'{o}/{a}' for o,a in TARGETS])
    p.add_argument('--output', type=Path, default=ROOT/'dist')
    args = p.parse_args()
    if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:alpha|beta|rc)\.[1-9][0-9]*)?', args.version):
        p.error('version must be X.Y.Z or X.Y.Z-alpha.N / beta.N / rc.N')
    bundle_version = args.version.split('-')[0]
    out = args.output.resolve(); out.mkdir(parents=True, exist_ok=True)
    targets = [tuple(args.target.split('/'))] if args.target else TARGETS
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

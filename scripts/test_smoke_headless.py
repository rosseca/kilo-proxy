"""Guard console smoke inputs without starting a daemon or contacting services."""
import io
import os
from pathlib import Path
import struct
import tarfile
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import smoke_headless


class HeadlessSmokeInputsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.extract = self.root / 'extract'
        self.extract.mkdir()

    def archive(self, entries):
        path = self.root / 'console.tar.gz'
        with tarfile.open(path, 'w:gz') as tar:
            for info, data in entries:
                info.size = len(data)
                tar.addfile(info, io.BytesIO(data))
        return path

    def test_archive_preflight_rejects_links_and_escape_before_extraction(self):
        for name, kind in [('../outside', tarfile.REGTYPE), ('/absolute', tarfile.REGTYPE),
                           ('console/link', tarfile.SYMTYPE), ('console/link', tarfile.LNKTYPE)]:
            with self.subTest(name=name, kind=kind):
                valid = tarfile.TarInfo('console/kilo-proxy-headless')
                valid.mode = 0o755
                unsafe = tarfile.TarInfo(name)
                unsafe.type, unsafe.linkname = kind, '../outside'
                archive = self.archive([(valid, b'synthetic executable'), (unsafe, b'')])
                with self.assertRaisesRegex(ValueError, 'Unsafe'):
                    smoke_headless.extract(archive, self.extract)
                self.assertEqual(list(self.extract.iterdir()), [])

    def test_archive_requires_executable_permissions(self):
        entry = tarfile.TarInfo('console/kilo-proxy-headless')
        entry.mode = 0o644
        archive = self.archive([(entry, b'synthetic executable')])
        with self.assertRaisesRegex(ValueError, 'permissions'):
            smoke_headless.extract(archive, self.extract)

    def test_archive_ignores_build_owner_and_preserves_executable_permissions(self):
        entry = tarfile.TarInfo('console/kilo-proxy-headless')
        entry.uid = entry.gid = 501
        entry.uname = entry.gname = 'synthetic-build-user'
        entry.mode = 0o755
        archive = self.archive([(entry, b'synthetic executable')])
        owners = []

        def private_owner(tar, member, target, numeric_owner):
            owners.append((member.uid, member.gid, member.uname, member.gname))
            self.assertIn(member.uid, (None, -1))
            self.assertIn(member.gid, (None, -1))
            self.assertIsNone(member.uname)
            self.assertIsNone(member.gname)

        with patch.object(tarfile.TarFile, 'chown', new=private_owner):
            executable = smoke_headless.extract(archive, self.extract)
        self.assertEqual(executable.read_bytes(), b'synthetic executable')
        if os.name == 'posix':
            self.assertTrue(executable.stat().st_mode & 0o111)
        self.assertEqual(len(owners), 1)

    def test_non_posix_extraction_still_requires_encoded_executable_mode(self):
        entry = tarfile.TarInfo('console/kilo-proxy-headless')
        entry.mode = 0o755
        archive = self.archive([(entry, b'synthetic executable')])

        def windows_mode(tar, member, target):
            os.chmod(target, 0o666)

        # Change only this smoke module's capability view, not global os.name
        # (which would also change pathlib's choice of concrete Path class).
        windows_os = SimpleNamespace(name='nt')
        with patch.object(smoke_headless, 'os', windows_os), \
             patch.object(tarfile.TarFile, 'chmod', new=windows_mode):
            executable = smoke_headless.extract(archive, self.extract)
        self.assertEqual(executable.read_bytes(), b'synthetic executable')
        entry.mode = 0o644
        archive = self.archive([(entry, b'invalid executable mode')])
        with patch.object(smoke_headless, 'os', windows_os), \
             self.assertRaisesRegex(ValueError, 'archive.*permissions'):
            smoke_headless.extract(archive, self.extract)
        self.assertEqual(executable.read_bytes(), b'synthetic executable')

    def elf(self, architecture=183, interpreter=False):
        data = bytearray(256)
        data[:6] = b'\x7fELF\x02\x01'
        struct.pack_into('<H', data, 18, architecture)
        struct.pack_into('<Q', data, 32, 64)
        struct.pack_into('<H', data, 54, 56)
        struct.pack_into('<H', data, 56, 1)
        struct.pack_into('<I', data, 64, 3 if interpreter else 1)
        path = self.root / 'binary'
        path.write_bytes(data)
        return path

    @patch.object(smoke_headless.platform, 'system', return_value='Linux')
    @patch.object(smoke_headless.platform, 'machine', return_value='aarch64')
    def test_linux_smoke_rejects_dynamic_interpreter(self, *_):
        self.assertEqual(smoke_headless.verify_native_header(self.elf()), ('linux', 'arm64'))
        with self.assertRaisesRegex(RuntimeError, 'must be static'):
            smoke_headless.verify_native_header(self.elf(interpreter=True))

    @patch.object(smoke_headless.platform, 'system', return_value='Linux')
    @patch.object(smoke_headless.platform, 'machine', return_value='aarch64')
    def test_linux_smoke_rejects_foreign_architecture(self, *_):
        with self.assertRaisesRegex(RuntimeError, 'wrong CPU/OS'):
            smoke_headless.verify_native_header(self.elf(architecture=62))


if __name__ == '__main__':
    unittest.main()

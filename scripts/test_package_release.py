import io
from pathlib import Path
import tarfile
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import package_release


class ReleasePackageTest(unittest.TestCase):
    def test_only_expected_files_and_reproducible_metadata(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for name in ('README.md', 'LICENSE', 'mcp/server.py', 'mcp/requirements.txt',
                         'mcp/keyed_stdio_launcher.py', 'deploy/systemd/veda.service',
                         'web/public/assets/fonts/LICENSE', 'web/public/assets/icons/LICENSE',
                         'docs/guide.md', '.veda/owner.key'):
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text('synthetic ' + name)
            for path in (package_release.ROOT / 'docs').glob('*.md'):
                (root / 'docs' / path.name).write_text('synthetic documentation')
            binary = root / 'binary'
            binary.write_bytes(b'synthetic executable')
            a, b = root / 'a.tar.gz', root / 'b.tar.gz'
            with patch.object(package_release, 'ROOT', root):
                package_release.package(a, binary, 'test', 'amd64')
                package_release.package(b, binary, 'test', 'amd64')
            self.assertEqual(a.read_bytes(), b.read_bytes())
            with tarfile.open(fileobj=io.BytesIO(a.read_bytes()), mode='r:gz') as tar:
                members = {m.name: m for m in tar.getmembers()}
                prefix = 'veda-test-linux-amd64/'
                self.assertEqual(set(members), {prefix + x for x in (
                    'veda', 'README.md', 'LICENSE', 'mcp/server.py', 'mcp/requirements.txt',
                    'mcp/keyed_stdio_launcher.py', 'deploy/systemd/veda.service',
                    'web/public/assets/fonts/LICENSE', 'web/public/assets/icons/LICENSE')} | {prefix + 'docs/' + p.name for p in (package_release.ROOT / 'docs').glob('*.md')})
                self.assertEqual(members[prefix + 'veda'].mode, 0o755)
                self.assertTrue(all(m.isfile() and m.uid == 0 and m.gid == 0 and m.mtime == 0 for m in members.values()))

    def test_packaged_launcher_relays_key_and_request(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            binary, archive, key = root / 'binary', root / 'archive.tar.gz', root / 'reader.key'
            binary.write_bytes(b'executable')
            key.write_text('a' * 64)
            key.chmod(0o600)
            package_release.package(archive, binary, 'test', 'amd64')
            with tarfile.open(archive, 'r:gz') as tar:
                launcher = root / 'launcher.py'
                launcher.write_bytes(tar.extractfile('veda-test-linux-amd64/mcp/keyed_stdio_launcher.py').read())
            child = "import sys; key=sys.stdin.buffer.read(64); request=sys.stdin.buffer.read(); print('ok' if key == b'a'*64 and request == b'hello' else 'bad')"
            result = subprocess.run([sys.executable, str(launcher), '--key-file', str(key), '--', sys.executable, '-c', child], input=b'hello', capture_output=True, timeout=10, check=True)
            self.assertEqual(result.stdout, b'ok\n')
            self.assertEqual(result.stderr, b'')

    def test_refuses_symlink_and_does_not_replace_archive(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'README.md').symlink_to(root / 'outside')
            binary, archive = root / 'binary', root / 'archive'
            binary.write_bytes(b'executable')
            archive.write_bytes(b'previous')
            with patch.object(package_release, 'ROOT', root), self.assertRaises(ValueError):
                package_release.package(archive, binary, 'test', 'amd64')
            self.assertEqual(archive.read_bytes(), b'previous')
            self.assertFalse(list(root.glob('.veda-release-*')))


if __name__ == '__main__':
    unittest.main()

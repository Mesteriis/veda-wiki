import io
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import package_release


class ReleasePackageTest(unittest.TestCase):
    def fixture(self, root):
        for name in ('README.md', 'LICENSE', 'deploy/systemd/veda.service',
                     'web/public/assets/fonts/LICENSE', 'web/public/assets/icons/LICENSE',
                     'docs/guide.md', 'mcp/server.py', '.veda/owner.key'):
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text('synthetic ' + name)
        for name in package_release.DOCS:
            (root / 'docs' / name).write_text('synthetic documentation')
        binary = root / 'binary'
        binary.write_bytes(b'synthetic executable')
        return binary

    def test_targets(self):
        self.assertEqual(package_release.TARGETS, ('linux-amd64', 'linux-arm64', 'darwin-arm64'))

    def test_only_expected_files_and_reproducible_metadata(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            binary = self.fixture(root)
            common = ('veda', 'README.md', 'LICENSE', 'web/public/assets/fonts/LICENSE',
                      'web/public/assets/icons/LICENSE')
            docs = {'docs/' + name for name in package_release.DOCS}
            for target in package_release.TARGETS:
                with self.subTest(target=target):
                    a, b = root / 'a.tar.gz', root / 'b.tar.gz'
                    with patch.object(package_release, 'ROOT', root):
                        package_release.package(a, binary, 'test', target)
                        package_release.package(b, binary, 'test', target)
                    self.assertEqual(a.read_bytes(), b.read_bytes())
                    with tarfile.open(fileobj=io.BytesIO(a.read_bytes()), mode='r:gz') as tar:
                        members = {m.name: m for m in tar.getmembers()}
                    prefix = f'veda-test-{target}/'
                    expected = set(common) | docs
                    if target.startswith('linux-'):
                        expected.add('deploy/systemd/veda.service')
                    self.assertEqual(set(members), {prefix + x for x in expected})
                    self.assertFalse([n for n in members if '/mcp/' in n])
                    self.assertEqual(members[prefix + 'veda'].mode, 0o755)
                    self.assertTrue(all(m.isfile() and m.uid == 0 and m.gid == 0 and m.mtime == 0 for m in members.values()))

    def test_unknown_target_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            binary = self.fixture(root)
            with patch.object(package_release, 'ROOT', root), self.assertRaises(ValueError):
                package_release.package(root / 'x.tar.gz', binary, 'test', 'windows-amd64')
            self.assertFalse((root / 'x.tar.gz').exists())

    def test_refuses_symlink_and_does_not_replace_archive(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'README.md').symlink_to(root / 'outside')
            binary, archive = root / 'binary', root / 'archive'
            binary.write_bytes(b'executable')
            archive.write_bytes(b'previous')
            with patch.object(package_release, 'ROOT', root), self.assertRaises(ValueError):
                package_release.package(archive, binary, 'test', 'linux-amd64')
            self.assertEqual(archive.read_bytes(), b'previous')
            self.assertFalse(list(root.glob('.veda-release-*')))


if __name__ == '__main__':
    unittest.main()

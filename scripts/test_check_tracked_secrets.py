import tempfile
import unittest
from pathlib import Path

from check_tracked_secrets import scan_paths


class TrackedSecretCheckTests(unittest.TestCase):
    def test_detects_credential_shapes_without_returning_values(self):
        fixtures = {
            'private': b'-----BEGIN ' + b'PRIVATE KEY-----',
            'github': b'gh' + b'p_' + b'A' * 36,
            'aws': b'AK' + b'IA' + b'A' * 16,
            'url': b'postgres' + b'://user:fixture-password@example.invalid/db',
        }
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for name, data in fixtures.items():
                (root / name).write_bytes(data)
            (root / 'safe').write_text('Use example credentials only.')
            found = scan_paths(root, [*fixtures, 'safe'])
            self.assertEqual(found, {
                'private': ['private_key'], 'github': ['github_token'],
                'aws': ['aws_access_key'], 'url': ['credential_url'],
            })
            for secret in fixtures.values():
                self.assertNotIn(secret.decode(), str(found))

    def test_does_not_follow_symlinks(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'outside').write_bytes(b'gh' + b'p_' + b'A' * 36)
            (root / 'link').symlink_to(root / 'outside')
            self.assertEqual(scan_paths(root, ['link']), {})


if __name__ == '__main__':
    unittest.main()

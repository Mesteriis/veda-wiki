"""Validate safety checks without using real key material."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('check_agent_readers', Path(__file__).with_name('check_agent_readers.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class AgentReaderMetadataTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        for name, digit in zip(module.AGENTS, 'abcd'):
            path = self.root / (name + '.key')
            path.write_text(digit * 64)
            path.chmod(0o600)

    def test_accepts_four_distinct_private_keys(self):
        module.validate_key_metadata(self.root)

    def test_rejects_duplicates_and_bad_format(self):
        duplicate = self.root / 'hermes.key'
        duplicate.write_text('a' * 64)
        with self.assertRaises(ValueError):
            module.validate_key_metadata(self.root)
        duplicate.write_text('not-a-key')
        with self.assertRaises(ValueError):
            module.validate_key_metadata(self.root)

    def test_rejects_unsafe_modes_and_symlinks(self):
        key = self.root / 'codex.key'
        key.chmod(0o644)
        with self.assertRaises(ValueError):
            module.validate_key_metadata(self.root)
        key.chmod(0o600)
        self.root.chmod(0o755)
        with self.assertRaises(ValueError):
            module.validate_key_metadata(self.root)
        self.root.chmod(0o700)
        key.unlink()
        key.symlink_to(self.root / 'claude.key')
        with self.assertRaises(OSError):
            module.validate_key_metadata(self.root)


if __name__ == '__main__':
    unittest.main()

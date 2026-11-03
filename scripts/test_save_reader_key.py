"""Ensure local key import protects existing files and never emits token material."""
import importlib.util
import os
import select
import signal
import sys
import time
from pathlib import Path
import stat
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('save_reader_key', Path(__file__).with_name('save_reader_key.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class SaveReaderKeyTests(unittest.TestCase):
    def test_creates_private_file_and_never_overwrites(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'agent.key'
            module.save_reader_key(path, 'a' * 64)
            self.assertEqual(path.read_text(), 'a' * 64)
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            with self.assertRaises(FileExistsError):
                module.save_reader_key(path, 'b' * 64)
            self.assertEqual(path.read_text(), 'a' * 64)

    def test_interactive_prompt_hides_the_key(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'agent.key'
            pid, master = os.forkpty()
            if pid == 0:
                os.execv(sys.executable, [sys.executable, str(Path(__file__).with_name('save_reader_key.py')), '--out', str(path)])
            output = b''
            reaped = False
            try:
                deadline = time.monotonic() + 5
                while b'input hidden' not in output and time.monotonic() < deadline:
                    if select.select([master], [], [], .2)[0]:
                        output += os.read(master, 4096)
                self.assertIn(b'input hidden', output)
                os.write(master, b'a' * 64 + b'\n')
                while time.monotonic() < deadline:
                    if select.select([master], [], [], .2)[0]:
                        try:
                            chunk = os.read(master, 4096)
                        except OSError:
                            break
                        if not chunk:
                            break
                        output += chunk
                waited, status = os.waitpid(pid, os.WNOHANG)
                while not waited and time.monotonic() < deadline:
                    time.sleep(.02)
                    waited, status = os.waitpid(pid, os.WNOHANG)
                if not waited:
                    raise AssertionError('key helper did not exit')
                reaped = True
                self.assertEqual(os.waitstatus_to_exitcode(status), 0)
                self.assertNotIn(b'a' * 64, output)
                self.assertEqual(path.read_text(), 'a' * 64)
            finally:
                if not reaped:
                    try:
                        os.kill(pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    os.waitpid(pid, 0)
                os.close(master)

    def test_rejects_invalid_input_and_unsafe_directory(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'agent.key'
            with self.assertRaises(ValueError):
                module.save_reader_key(path, 'not-a-key')
            self.assertFalse(path.exists())
            Path(tmp).chmod(0o755)
            with self.assertRaises(ValueError):
                module.save_reader_key(path, 'a' * 64)
            self.assertFalse(path.exists())

    def test_rejects_symlinked_directory_and_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            target = root / 'existing'
            target.write_text('untouched')
            (root / 'alias').symlink_to(target)
            with self.assertRaises(FileExistsError):
                module.save_reader_key(root / 'alias', 'a' * 64)
            (root / 'dir').mkdir(mode=0o700)
            (root / 'diralias').symlink_to(root / 'dir', target_is_directory=True)
            with self.assertRaises((ValueError, OSError)):
                module.save_reader_key(root / 'diralias' / 'agent.key', 'a' * 64)
            self.assertEqual(target.read_text(), 'untouched')


if __name__ == '__main__':
    unittest.main()

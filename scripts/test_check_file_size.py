import tempfile
import unittest
from pathlib import Path

from check_file_size import find_violations


def write_lines(path, count):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text('x\n' * count)


class FileSizeCheckTests(unittest.TestCase):
    def test_reports_files_over_limit(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_lines(root / 'scripts' / 'big.py', 501)
            write_lines(root / 'scripts' / 'ok.py', 500)
            found = find_violations(root, ['scripts/**/*.py'], 500)
            self.assertEqual(found, [('scripts/big.py', 501)])

    def test_excluded_lock_file_is_ignored(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_lines(root / 'web' / 'package-lock.json', 900)
            write_lines(root / 'web' / 'node_modules' / 'a' / 'b.js', 900)
            write_lines(root / 'web' / 'dist' / 'app.js', 900)
            write_lines(root / 'go.sum', 900)
            found = find_violations(root, ['**/*.json', '**/*.js', 'web/dist/**', 'go.sum'], 500)
            self.assertEqual(found, [])

    def test_main_exit_code_and_output(self):
        import io
        import contextlib
        from check_file_size import main
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_lines(root / 'a' / 'big.py', 501)
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                code = main(['--max', '500', '--include', 'a/**/*.py'], root=root)
            self.assertEqual(code, 1)
            self.assertEqual(out.getvalue().strip(), 'a/big.py: 501 lines')


if __name__ == '__main__':
    unittest.main()

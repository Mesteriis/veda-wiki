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

    def test_python_service_build_dirs_are_ignored(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            svc = root / 'services' / 'knowledge-extractor'
            write_lines(svc / '.venv' / 'lib' / 'site.py', 900)
            write_lines(svc / 'build' / 'lib' / 'mod.py', 900)
            write_lines(svc / 'src' / 'pkg' / '__pycache__' / 'x.py', 900)
            write_lines(svc / 'src' / 'pkg' / 'big.py', 501)
            found = find_violations(root, ['services/**/*.py'], 500)
            self.assertEqual(found, [('services/knowledge-extractor/src/pkg/big.py', 501)])

    def test_build_dir_names_outside_services_are_still_checked(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_lines(root / 'scripts' / 'build' / 'big.py', 501)
            write_lines(root / 'internal' / 'dist' / 'big.go', 501)
            write_lines(root / 'web' / 'src' / 'build' / 'big.ts', 501)
            write_lines(root / 'internal' / 'venv' / 'x.go', 501)
            found = find_violations(
                root, ['scripts/**/*.py', '**/*.go', 'web/src/**/*.ts'], 500)
            self.assertEqual([rel for rel, _ in found], [
                'internal/dist/big.go', 'internal/venv/x.go', 'scripts/build/big.py',
                'web/src/build/big.ts'])

    def test_service_environments_are_not_traversed(self):
        import os
        from unittest import mock
        import check_file_size

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            svc = root / 'services' / 'svc'
            write_lines(svc / '.venv' / 'lib' / 'deep' / 'site.py', 1)
            write_lines(svc / 'src' / 'ok.py', 1)
            write_lines(root / 'node_modules' / 'a' / 'b.js', 1)
            visited = []
            real = os.scandir

            def recording(path):
                visited.append(Path(path).as_posix())
                return real(path)

            with mock.patch.object(check_file_size.os, 'scandir', recording):
                find_violations(root, ['services/**/*.py'], 0)
            self.assertTrue(any(p.endswith('services/svc/src') for p in visited))
            self.assertFalse(any('.venv' in p or 'node_modules' in p for p in visited))

    def test_glob_semantics_match_pathlib(self):
        from check_file_size import glob_regex
        cases = {
            'scripts/**/*.py': (['scripts/a.py', 'scripts/x/y/a.py'], ['scripts.py', 'a/scripts/a.py']),
            '**/*.go': (['a.go', 'x/y/a.go'], ['a.go.txt']),
            'web/dist/**': (['web/dist/a.js', 'web/dist/x/a.js'], ['web/distx/a.js']),
            'go.sum': (['go.sum'], ['x/go.sum']),
        }
        for pattern, (good, bad) in cases.items():
            rx = glob_regex(pattern)
            for rel in good:
                self.assertTrue(rx.match(rel), (pattern, rel))
            for rel in bad:
                self.assertFalse(rx.match(rel), (pattern, rel))

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
